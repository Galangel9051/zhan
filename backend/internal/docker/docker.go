package docker

import (
	"compose-manager/backend/internal/command"
	"compose-manager/backend/internal/model"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

var projectName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,127}$`)
var serviceName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$`)
var containerID = regexp.MustCompile(`^[a-f0-9]{12,64}$`)

type Client struct {
	Runner   command.Runner
	Roots    []string
	mu       sync.Mutex
	cached   []model.Container
	cacheAt  time.Time
	cacheErr error
	cfgMu    sync.Mutex
	configs  map[string]configEntry
}

type configEntry struct {
	At       time.Time
	Services []model.Service
}

func cloneServices(items []model.Service) []model.Service {
	result := make([]model.Service, len(items))
	for i, item := range items {
		result[i] = item
		result[i].Dependencies = append([]string{}, item.Dependencies...)
		result[i].Containers = []model.Container{}
	}
	return result
}
func (c *Client) Allowed(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("请填写服务器上的绝对路径，当前填写：%q", path)
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("路径不存在：%s。请确认填写的是运行 Compose Manager 的服务器上的路径", path)
		}
		if os.IsPermission(err) {
			return "", fmt.Errorf("无权访问路径：%s。请为运行 Compose Manager 的账户授予读取权限", path)
		}
		return "", fmt.Errorf("无法读取路径 %s：%w", path, err)
	}
	real, err = filepath.Abs(real)
	if err != nil {
		return "", err
	}
	if len(c.Roots) == 0 {
		return real, nil
	}
	for _, root := range c.Roots {
		rel, err := filepath.Rel(root, real)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel) {
			return real, nil
		}
	}
	return "", fmt.Errorf("路径 %s 不在允许管理的目录中。当前允许目录：%s。请在配置文件的 allowed_roots 中加入项目所在目录，或设置 CM_ALLOWED_ROOTS，然后重启服务", real, strings.Join(c.Roots, "、"))
}
func (c *Client) Validate(s *model.Stack) error {
	if !projectName.MatchString(s.ProjectName) {
		return errors.New("Compose Project Name 仅允许小写字母、数字、下划线和短横线")
	}
	if strings.TrimSpace(s.Name) == "" || len(s.Name) > 128 {
		return errors.New("请填写有效的显示名称")
	}
	dir, err := c.Allowed(s.Directory)
	if err != nil {
		return err
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return errors.New("项目目录不存在")
	}
	s.Directory = dir
	if len(s.Files) < 1 || len(s.Files) > 10 {
		return errors.New("请指定 1–10 个 Compose 文件，按覆盖顺序填写")
	}
	for i, file := range s.Files {
		if !filepath.IsAbs(file) {
			file = filepath.Join(dir, file)
		}
		file, err = c.Allowed(file)
		if err != nil {
			return err
		}
		info, err = os.Stat(file)
		if err != nil || !info.Mode().IsRegular() {
			return errors.New("Compose 文件不存在或不是普通文件")
		}
		s.Files[i] = file
	}
	return nil
}
func Args(s model.Stack, tail ...string) []string {
	args := []string{"compose", "-p", s.ProjectName, "--project-directory", s.Directory}
	for _, file := range s.Files {
		args = append(args, "-f", file)
	}
	return append(args, tail...)
}
func (c *Client) Exec(ctx context.Context, s model.Stack, out io.Writer, tail ...string) error {
	if err := c.Validate(&s); err != nil {
		return err
	}
	return c.Runner.Run(ctx, s.Directory, "docker", Args(s, tail...), out)
}
func (c *Client) Config(ctx context.Context, s model.Stack) ([]model.Service, error) {
	if err := c.Validate(&s); err != nil {
		return nil, err
	}
	key := s.ProjectName + "|" + s.Directory + "|" + strings.Join(s.Files, "|")
	c.cfgMu.Lock()
	defer c.cfgMu.Unlock()
	if entry, ok := c.configs[key]; ok && time.Since(entry.At) < 30*time.Second {
		return cloneServices(entry.Services), nil
	}
	data, err := command.Capture(ctx, c.Runner, s.Directory, "docker", Args(s, "config", "--format", "json")...)
	if err != nil {
		return nil, err
	}
	var cfg struct {
		Services map[string]struct {
			Image        string         `json:"image"`
			Build        any            `json:"build"`
			Dependencies map[string]any `json:"depends_on"`
		} `json:"services"`
	}
	if err = json.Unmarshal([]byte(data), &cfg); err != nil {
		return nil, fmt.Errorf("解析 Compose 配置失败: %w", err)
	}
	result := []model.Service{}
	for name, item := range cfg.Services {
		deps := []string{}
		for dep := range item.Dependencies {
			deps = append(deps, dep)
		}
		sort.Strings(deps)
		result = append(result, model.Service{Name: name, Image: item.Image, Build: item.Build != nil, Dependencies: deps, Status: "stopped", Containers: []model.Container{}})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	if len(result) == 0 {
		return nil, errors.New("Compose 配置没有可管理服务")
	}
	if c.configs == nil {
		c.configs = map[string]configEntry{}
	}
	c.configs[key] = configEntry{At: time.Now(), Services: cloneServices(result)}
	return result, nil
}
func (c *Client) Discover(ctx context.Context) ([]model.Discovery, error) {
	data, err := command.Capture(ctx, c.Runner, "", "docker", "compose", "ls", "--all", "--format", "json")
	if err != nil {
		return nil, err
	}
	var raw []struct {
		Name        string
		Status      string
		ConfigFiles string
	}
	if err = json.Unmarshal([]byte(data), &raw); err != nil {
		return nil, err
	}
	items := []model.Discovery{}
	for _, r := range raw {
		files := []string{}
		if r.ConfigFiles != "" {
			files = strings.Split(r.ConfigFiles, ",")
		}
		items = append(items, model.Discovery{Name: r.Name, Status: r.Status, Files: files})
	}
	return items, nil
}
func (c *Client) InvalidateRuntime() { c.mu.Lock(); c.cacheAt = time.Time{}; c.mu.Unlock() }
func (c *Client) Invalidate() {
	c.mu.Lock()
	c.cacheAt = time.Time{}
	c.mu.Unlock()
	c.cfgMu.Lock()
	c.configs = nil
	c.cfgMu.Unlock()
}
func (c *Client) Containers(ctx context.Context) ([]model.Container, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if time.Since(c.cacheAt) < 5*time.Second {
		return append([]model.Container{}, c.cached...), c.cacheErr
	}
	list, err := command.Capture(ctx, c.Runner, "", "docker", "ps", "-aq", "--filter", "label=com.docker.compose.project")
	if err != nil {
		c.cacheErr = err
		c.cacheAt = time.Now()
		return nil, err
	}
	ids := strings.Fields(list)
	if len(ids) == 0 {
		c.cached = []model.Container{}
		c.cacheErr = nil
		c.cacheAt = time.Now()
		return c.cached, nil
	}
	if len(ids) > 2000 {
		return nil, errors.New("容器数量超过单次查询限制")
	}
	for _, id := range ids {
		if !containerID.MatchString(id) {
			return nil, errors.New("Docker 返回无效容器标识")
		}
	}
	data, err := command.Capture(ctx, c.Runner, "", "docker", append([]string{"inspect"}, ids...)...)
	if err != nil {
		return nil, err
	}
	var raw []struct {
		ID     string `json:"Id"`
		Name   string
		Config struct {
			Image  string
			Labels map[string]string
		}
		State struct {
			Status string
			Health *struct{ Status string }
		}
		NetworkSettings struct {
			Ports map[string][]struct {
				HostIP   string
				HostPort string
			}
		}
	}
	if err = json.Unmarshal([]byte(data), &raw); err != nil {
		return nil, err
	}
	items := []model.Container{}
	for _, r := range raw {
		ports := []string{}
		for port, bindings := range r.NetworkSettings.Ports {
			for _, binding := range bindings {
				ports = append(ports, binding.HostIP+":"+binding.HostPort+" → "+port)
			}
		}
		sort.Strings(ports)
		health := ""
		if r.State.Health != nil {
			health = r.State.Health.Status
		}
		items = append(items, model.Container{ID: r.ID, Name: strings.TrimPrefix(r.Name, "/"), Service: r.Config.Labels["com.docker.compose.service"], State: r.State.Status, Health: health, Image: r.Config.Image, Ports: strings.Join(ports, ", "), Project: r.Config.Labels["com.docker.compose.project"]})
	}
	c.cached = items
	c.cacheErr = nil
	c.cacheAt = time.Now()
	return append([]model.Container{}, items...), nil
}
func (c *Client) Status(ctx context.Context, s *model.Stack) error {
	containers, err := c.Containers(ctx)
	if err != nil {
		return err
	}
	services, err := c.Config(ctx, *s)
	if err != nil {
		return err
	}
	for _, container := range containers {
		if container.Project != s.ProjectName {
			continue
		}
		found := false
		for i := range services {
			if services[i].Name == container.Service {
				services[i].Containers = append(services[i].Containers, container)
				found = true
				break
			}
		}
		if !found {
			services = append(services, model.Service{Name: container.Service, Image: container.Image, Containers: []model.Container{container}, Dependencies: []string{}})
		}
	}
	running, total, unhealthy := 0, 0, false
	for i := range services {
		serviceRunning := 0
		for _, container := range services[i].Containers {
			total++
			if container.State == "running" {
				running++
				serviceRunning++
			}
			if container.State == "running" && container.Health == "unhealthy" {
				unhealthy = true
			}
		}
		if len(services[i].Containers) == 0 {
			total++
		}
		switch {
		case serviceRunning == 0:
			services[i].Status = "stopped"
		case serviceRunning == len(services[i].Containers):
			services[i].Status = "running"
		default:
			services[i].Status = "partial"
		}
		for _, item := range services[i].Containers {
			if item.State == "running" && item.Health == "unhealthy" {
				services[i].Status = "unhealthy"
			}
		}
	}
	s.Services = services
	switch {
	case unhealthy:
		s.Runtime = "unhealthy"
	case running == 0:
		s.Runtime = "stopped"
	case running == total:
		s.Runtime = "running"
	default:
		s.Runtime = "partial"
	}
	return nil
}
func (c *Client) CheckTarget(ctx context.Context, s model.Stack, service, container string) ([]model.Service, error) {
	if service != "" && !serviceName.MatchString(service) {
		return nil, errors.New("无效服务名")
	}
	if container != "" && !containerID.MatchString(container) {
		return nil, errors.New("无效容器 ID")
	}
	if err := c.Status(ctx, &s); err != nil {
		return nil, err
	}
	if service != "" {
		found := false
		for _, item := range s.Services {
			if item.Name == service {
				found = true
			}
		}
		if !found {
			return nil, errors.New("服务不属于该项目")
		}
	}
	if container != "" {
		found := false
		for _, item := range s.Services {
			for _, inst := range item.Containers {
				if inst.ID == container {
					found = true
				}
			}
		}
		if !found {
			return nil, errors.New("容器不属于该项目")
		}
	}
	return s.Services, nil
}

// WaitHealthy reads Docker state without another up operation. Stopped containers'
// historical health values never count as a running health failure.
func (c *Client) WaitHealthy(ctx context.Context, s *model.Stack) error {
	wait, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		c.InvalidateRuntime()
		if err := c.Status(wait, s); err != nil {
			return err
		}
		if s.Runtime == "unhealthy" {
			return errors.New("容器健康检查失败")
		}
		ready := s.Runtime == "running"
		for _, service := range s.Services {
			for _, container := range service.Containers {
				if container.Health == "starting" {
					ready = false
				}
			}
		}
		if ready {
			return nil
		}
		select {
		case <-wait.Done():
			return fmt.Errorf("等待容器运行与健康状态超时: %w", wait.Err())
		case <-ticker.C:
		}
	}
}
