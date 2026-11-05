package docker

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"zhan/backend/internal/testutil"
)

type statsRunner struct {
	base   *testutil.Fake
	output string
	err    error
	calls  int
	args   []string
}

func (r *statsRunner) Run(ctx context.Context, dir, name string, args []string, out io.Writer) error {
	if len(args) > 0 && args[0] == "stats" {
		r.calls++
		r.args = append([]string{}, args...)
		io.WriteString(out, r.output)
		return r.err
	}
	return r.base.Run(ctx, dir, name, args, out)
}
func statsRow(id string) string {
	data, _ := json.Marshal(map[string]string{"ID": id, "CPUPerc": "125.50%", "MemPerc": "6.25%", "MemUsage": "64MiB / 1GiB", "NetIO": "1.5kB / 2KiB", "BlockIO": "3MB / 4MiB", "PIDs": "7"})
	return string(data) + "\n"
}
func TestStatsSamplingScopeCacheAndPartialFailure(t *testing.T) {
	a, b, foreign := strings.Repeat("a", 64), strings.Repeat("b", 64), strings.Repeat("c", 64)
	r := &statsRunner{base: &testutil.Fake{Project: "fixture", Running: true}, output: statsRow(a) + statsRow(b) + statsRow(foreign)}
	c := &Client{Runner: r}
	ctx := context.Background()
	value, err := c.Stats(ctx, []string{"fixture"})
	if err != nil || len(value.Containers) != 2 || value.UnavailableCount != 0 || value.SampledAt == "" {
		t.Fatalf("invalid sample: %+v (%v)", value, err)
	}
	metrics := value.Containers[a]
	if metrics.CPUPercent == nil || *metrics.CPUPercent != 125.5 || metrics.MemoryBytes == nil || *metrics.MemoryBytes != 64<<20 || *metrics.MemoryLimit != 1<<30 || *metrics.NetworkRX != 1500 || *metrics.NetworkTX != 2048 || *metrics.BlockRead != 3000000 || *metrics.BlockWrite != 4<<20 || *metrics.PIDs != 7 {
		t.Fatalf("incorrect metrics: %+v", metrics)
	}
	want := []string{"stats", "--no-stream", "--no-trunc", "--format", "{{json .}}", a, b}
	if !reflect.DeepEqual(r.args, want) {
		t.Fatalf("unexpected stats command: %v", r.args)
	}
	delete(value.Containers, a)
	if next, err := c.Stats(ctx, []string{"fixture"}); err != nil || len(next.Containers) != 2 || r.calls != 1 {
		t.Fatal("cache was not reused or was mutated")
	}
	if next, err := c.Stats(ctx, []string{"other"}); err != nil || len(next.Containers) != 0 || r.calls != 1 {
		t.Fatal("sampled unrelated containers")
	}
	if next, err := c.Stats(ctx, nil); err != nil || len(next.Containers) != 0 || r.calls != 1 {
		t.Fatal("empty project list sampled containers")
	}
	r.output, r.err = statsRow(a)+"container disappeared\n", errors.New("container disappeared")
	next, err := c.Stats(ctx, []string{"fixture"})
	if err != nil || len(next.Containers) != 1 || next.UnavailableCount != 1 {
		t.Fatalf("valid partial sample lost: %+v (%v)", next, err)
	}
	c.statsAt = time.Now().Add(-6 * time.Second)
	r.output = ""
	if _, err := c.Stats(ctx, []string{"fixture"}); err == nil {
		t.Fatal("sampling failure hidden")
	}
	calls := r.calls
	if _, err := c.Stats(ctx, []string{"fixture"}); err == nil || r.calls != calls {
		t.Fatal("failure cache did not prevent repeated sampling")
	}
	stoppedRunner := &statsRunner{base: &testutil.Fake{Project: "fixture", Running: false}}
	stopped := &Client{Runner: stoppedRunner}
	if next, err := stopped.Stats(ctx, []string{"fixture"}); err != nil || len(next.Containers) != 0 || stoppedRunner.calls != 0 {
		t.Fatal("stopped containers were sampled")
	}
}
func TestStatsQuantitiesAndUnsupportedValues(t *testing.T) {
	for value, want := range map[string]uint64{"0B": 0, "1.5kB": 1500, "2MiB": 2 << 20, "1 GB": 1000000000} {
		if got := statsBytes(value); got == nil || *got != want {
			t.Errorf("%s decoded incorrectly", value)
		}
	}
	for _, value := range []string{"--", "NaN", "-1B", "1XB", "999999999999999999999999999TB"} {
		if statsBytes(value) != nil {
			t.Errorf("invalid bytes accepted: %s", value)
		}
	}
	for _, value := range []string{"--", "NaN%", "Inf%", "-2%", "0.0"} {
		if statsPercent(value) != nil {
			t.Errorf("invalid percentage accepted: %s", value)
		}
	}
	if a, b := statsPair("unsupported"); a != nil || b != nil {
		t.Fatal("unsupported metric became zero")
	}
}
