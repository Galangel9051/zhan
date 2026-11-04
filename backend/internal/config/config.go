package config

import (
	"fmt"
	"gopkg.in/yaml.v3"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
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
	RetryDelays        []int    `yaml:"retry_delays_seconds"`
}

func Load(path string) (Config, error) {
	c := Config{Listen: "127.0.0.1:18080", DataDir: "data", AllowedRoots: []string{}, SessionHours: 12, Workers: 1, TaskTimeoutMinutes: 30, LogRetentionDays: 30, MaxLogMB: 20, RetryDelays: []int{300, 900}}
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
	if v := os.Getenv("CM_LISTEN"); v != "" {
		c.Listen = v
	}
	if v := os.Getenv("CM_DATA_DIR"); v != "" {
		c.DataDir = v
	}
	if v := os.Getenv("CM_ALLOWED_ROOTS"); v != "" {
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
	c.DataDir, err = filepath.Abs(c.DataDir)
	if err != nil {
		return c, err
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
	return c, nil
}
