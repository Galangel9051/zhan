package config

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Listen             string   `yaml:"listen"`
	DataDir            string   `yaml:"data_dir"`
	AllowedRoots       []string `yaml:"allowed_roots"`
	SecureCookies      bool     `yaml:"secure_cookies"`
	SessionHours       int      `yaml:"session_hours"`
	Workers            int      `yaml:"workers"`
	TaskTimeoutMinutes int      `yaml:"task_timeout_minutes"`
	LogRetentionDays   int      `yaml:"log_retention_days"`
	MaxLogMB           int      `yaml:"max_log_mb"`
	RetryBaseSeconds   int      `yaml:"retry_base_seconds"`
	RetryMaxSeconds    int      `yaml:"retry_max_seconds"`
	RetryMaxAttempts   int      `yaml:"retry_max_attempts"`
	RetryDelays        []int    `yaml:"retry_delays_seconds"`
}

func Load(path string) (Config, error) {
	c := Config{Listen: "127.0.0.1:12345", AllowedRoots: []string{}, SessionHours: 12, Workers: 1, TaskTimeoutMinutes: 30, LogRetentionDays: 30, MaxLogMB: 20, RetryBaseSeconds: 60, RetryMaxSeconds: 3600, RetryMaxAttempts: 5}
	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return c, err
		}
		dec := yaml.NewDecoder(strings.NewReader(string(data)))
		dec.KnownFields(true)
		if err = dec.Decode(&c); err != nil {
			return c, err
		}
	}
	if v := os.Getenv("ZHAN_LISTEN"); v != "" {
		c.Listen = v
	}
	if v := os.Getenv("ZHAN_DATA_DIR"); v != "" {
		c.DataDir = v
	}
	if v := os.Getenv("ZHAN_HOST_DATA_DIR"); v != "" {
		c.DataDir = v
	}
	if v := os.Getenv("ZHAN_ALLOWED_ROOTS"); v != "" {
		c.AllowedRoots = filepath.SplitList(v)
	}
	if c.AllowedRoots == nil {
		c.AllowedRoots = []string{}
	}
	if _, _, err := net.SplitHostPort(c.Listen); err != nil {
		return c, err
	}
	if c.Workers < 1 || c.Workers > 8 || c.SessionHours < 1 || c.SessionHours > 168 || c.TaskTimeoutMinutes < 1 || c.TaskTimeoutMinutes > 1440 || c.LogRetentionDays < 1 || c.MaxLogMB < 1 || c.MaxLogMB > 100 {
		return c, fmt.Errorf("invalid worker, timeout or retention settings")
	}
	var err error
	base, err := dataBaseDir()
	if err != nil {
		return c, err
	}
	if c.DataDir == "" {
		c.DataDir = filepath.Join(base, "data")
	} else if !filepath.IsAbs(c.DataDir) {
		c.DataDir = filepath.Join(base, c.DataDir)
	}
	c.DataDir, err = filepath.Abs(c.DataDir)
	if err != nil {
		return c, err
	}
	if v := os.Getenv("ZHAN_DATA_DIR"); v != "" && os.Getenv("ZHAN_HOST_DATA_DIR") != "" {
		if !filepath.IsAbs(v) {
			v = filepath.Join(base, v)
		}
		if filepath.Clean(v) != c.DataDir {
			return c, fmt.Errorf("ZHAN_HOST_DATA_DIR 与 ZHAN_DATA_DIR 必须指向同一目录")
		}
	}
	for i, root := range c.AllowedRoots {
		root, err = filepath.EvalSymlinks(root)
		if err != nil {
			return c, fmt.Errorf("allowed root: %w", err)
		}
		c.AllowedRoots[i], err = filepath.Abs(root)
		if err != nil {
			return c, err
		}
	}
	for _, v := range c.RetryDelays {
		if v < 1 || time.Duration(v)*time.Second > 24*time.Hour {
			return c, fmt.Errorf("invalid retry delay")
		}
	}
	if c.RetryBaseSeconds < 1 || time.Duration(c.RetryBaseSeconds)*time.Second > 24*time.Hour {
		return c, fmt.Errorf("invalid retry base delay")
	}
	if c.RetryMaxSeconds < c.RetryBaseSeconds || time.Duration(c.RetryMaxSeconds)*time.Second > 24*time.Hour {
		return c, fmt.Errorf("invalid retry max delay")
	}
	if c.RetryMaxAttempts < 0 || c.RetryMaxAttempts > 20 {
		return c, fmt.Errorf("invalid retry max attempts")
	}
	return c, nil
}

// RetryDelay returns the wait before the given 1-based retry attempt and whether
// another automatic attempt is still allowed. The default schedule doubles from
// retry_base_seconds and is capped at retry_max_seconds, so a project that keeps
// failing backs off instead of rebuilding in a tight loop. A non-empty
// retry_delays_seconds keeps working as an explicit fixed schedule.
func (c Config) RetryDelay(attempt int) (time.Duration, bool) {
	if attempt < 1 {
		return 0, false
	}
	if len(c.RetryDelays) > 0 {
		if attempt > len(c.RetryDelays) {
			return 0, false
		}
		return time.Duration(c.RetryDelays[attempt-1]) * time.Second, true
	}
	if c.RetryMaxAttempts < 1 || attempt > c.RetryMaxAttempts {
		return 0, false
	}
	delay := time.Duration(c.RetryBaseSeconds) * time.Second
	limit := time.Duration(c.RetryMaxSeconds) * time.Second
	for i := 1; i < attempt && delay < limit; i++ {
		delay *= 2
	}
	return min(delay, limit), true
}

// Source runs use the repository; released binaries use their installation
// directory (or its parent when installed in bin/). Neither depends on cwd.
func dataBaseDir() (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", err
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		return "", err
	}
	_, source, _, _ := runtime.Caller(0)
	return resolveDataBase(executable, source), nil
}

func resolveDataBase(executable, source string) string {
	// With -trimpath the source path is relative and must never resolve via cwd.
	if filepath.IsAbs(source) {
		root := filepath.Clean(filepath.Join(filepath.Dir(source), "../../.."))
		if _, err := os.Stat(filepath.Join(root, "backend", "go.mod")); err == nil {
			return root
		}
	}
	base := filepath.Dir(executable)
	if filepath.Base(base) == "bin" {
		base = filepath.Dir(base)
	}
	return base
}
