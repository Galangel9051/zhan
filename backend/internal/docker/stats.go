package docker

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"zhan/backend/internal/command"
)

type ContainerStats struct {
	CPUPercent    *float64 `json:"cpu_percent"`
	MemoryBytes   *uint64  `json:"memory_bytes"`
	MemoryLimit   *uint64  `json:"memory_limit"`
	MemoryPercent *float64 `json:"memory_percent"`
	NetworkRX     *uint64  `json:"network_rx"`
	NetworkTX     *uint64  `json:"network_tx"`
	BlockRead     *uint64  `json:"block_read"`
	BlockWrite    *uint64  `json:"block_write"`
	PIDs          *uint64  `json:"pids"`
}
type StatsSnapshot struct {
	SampledAt        string                    `json:"sampled_at"`
	Containers       map[string]ContainerStats `json:"containers"`
	UnavailableCount int                       `json:"unavailable_count"`
}

var byteQuantity = regexp.MustCompile(`^([0-9]+(?:\.[0-9]+)?)\s*([a-zA-Z]*)$`)

func statsBytes(value string) *uint64 {
	parts := byteQuantity.FindStringSubmatch(strings.TrimSpace(value))
	if len(parts) != 3 {
		return nil
	}
	number, err := strconv.ParseFloat(parts[1], 64)
	units := map[string]float64{"": 1, "B": 1, "KB": 1e3, "MB": 1e6, "GB": 1e9, "TB": 1e12, "PB": 1e15, "KIB": 1024, "MIB": 1 << 20, "GIB": 1 << 30, "TIB": 1 << 40, "PIB": 1 << 50}
	multiplier, ok := units[strings.ToUpper(parts[2])]
	bytes := number * multiplier
	if err != nil || !ok || math.IsInf(bytes, 0) || math.IsNaN(bytes) || bytes < 0 || bytes >= math.Exp2(64) {
		return nil
	}
	result := uint64(math.Round(bytes))
	return &result
}
func statsPercent(value string) *float64 {
	value = strings.TrimSpace(value)
	if !strings.HasSuffix(value, "%") {
		return nil
	}
	number, err := strconv.ParseFloat(strings.TrimSuffix(value, "%"), 64)
	if err != nil || number < 0 || math.IsNaN(number) || math.IsInf(number, 0) {
		return nil
	}
	return &number
}
func statsPair(value string) (*uint64, *uint64) {
	parts := strings.Split(value, "/")
	if len(parts) != 2 {
		return nil, nil
	}
	return statsBytes(parts[0]), statsBytes(parts[1])
}
func cloneStats(value StatsSnapshot) StatsSnapshot {
	result := value
	result.Containers = make(map[string]ContainerStats, len(value.Containers))
	for id, item := range value.Containers {
		result.Containers[id] = item
	}
	return result
}

// One bounded, cached sample for all running containers in imported projects.
// Never invoke stats with no IDs: that would silently sample unrelated containers.
func (c *Client) Stats(ctx context.Context, projects []string) (StatsSnapshot, error) {
	if len(projects) == 0 {
		return StatsSnapshot{SampledAt: time.Now().UTC().Format(time.RFC3339Nano), Containers: map[string]ContainerStats{}}, nil
	}
	containers, err := c.Containers(ctx)
	if err != nil {
		return StatsSnapshot{}, err
	}
	allowed := map[string]bool{}
	for _, project := range projects {
		allowed[project] = true
	}
	ids, known := []string{}, map[string]bool{}
	for _, container := range containers {
		if !allowed[container.Project] || (container.State != "running" && container.State != "paused") {
			continue
		}
		if !containerID.MatchString(container.ID) {
			return StatsSnapshot{}, errors.New("Docker 返回无效容器标识")
		}
		if !known[container.ID] {
			known[container.ID] = true
			ids = append(ids, container.ID)
		}
	}
	slices.Sort(ids)
	key := strings.Join(ids, ",")
	c.statsMu.Lock()
	defer c.statsMu.Unlock()
	if !c.statsAt.IsZero() && c.statsKey == key && time.Since(c.statsAt) < 5*time.Second {
		return cloneStats(c.stats), c.statsErr
	}
	snapshot := StatsSnapshot{Containers: map[string]ContainerStats{}}
	if len(ids) > 0 {
		args := append([]string{"stats", "--no-stream", "--no-trunc", "--format", "{{json .}}"}, ids...)
		output := &command.Buffer{Limit: 4 * 1024 * 1024}
		err = c.Runner.Run(ctx, "", "docker", args, output)
		if !output.Overflow {
			for _, line := range strings.Split(strings.TrimSpace(string(output.Data)), "\n") {
				var row struct{ ID, CPUPerc, MemPerc, MemUsage, NetIO, BlockIO, PIDs string }
				if json.Unmarshal([]byte(line), &row) != nil || !known[row.ID] {
					continue
				}
				item := ContainerStats{CPUPercent: statsPercent(row.CPUPerc), MemoryPercent: statsPercent(row.MemPerc)}
				item.MemoryBytes, item.MemoryLimit = statsPair(row.MemUsage)
				item.NetworkRX, item.NetworkTX = statsPair(row.NetIO)
				item.BlockRead, item.BlockWrite = statsPair(row.BlockIO)
				if pids, err := strconv.ParseUint(row.PIDs, 10, 64); err == nil {
					item.PIDs = &pids
				}
				if item.CPUPercent != nil || item.MemoryBytes != nil || item.NetworkRX != nil || item.BlockRead != nil || item.PIDs != nil {
					snapshot.Containers[row.ID] = item
				}
			}
		} else {
			err = errors.New("Docker 资源数据超过单次查询上限")
		}
		if len(snapshot.Containers) > 0 {
			// A container may exit during sampling; retain valid rows from others.
			err = nil
		}
	}
	snapshot.SampledAt = time.Now().UTC().Format(time.RFC3339Nano)
	snapshot.UnavailableCount = len(ids) - len(snapshot.Containers)
	c.stats, c.statsAt, c.statsKey, c.statsErr = snapshot, time.Now(), key, err
	return cloneStats(snapshot), err
}
