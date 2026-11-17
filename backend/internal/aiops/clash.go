package aiops

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type ClashSettings struct {
	BaseURL   string   `json:"base_url"`
	Secret    string   `json:"secret,omitempty"`
	SecretSet bool     `json:"secret_set"`
	Groups    []string `json:"groups"`
}

func (m *Manager) ClashSettings(secret bool) (ClashSettings, error) {
	var s ClashSettings
	err := m.DB.GetSetting("ai_clash", &s)
	if errors.Is(err, sql.ErrNoRows) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	s.SecretSet = s.Secret != ""
	if secret && s.SecretSet {
		s.Secret, err = m.Auth.Decrypt(s.Secret)
	} else {
		s.Secret = ""
	}
	return s, err
}
func (m *Manager) SaveClash(s ClashSettings) error {
	if s.BaseURL != "" {
		if err := validateEndpoint(s.BaseURL); err != nil {
			return err
		}
	}
	if len(s.Secret) > 4096 {
		return errors.New("Clash 密钥过长")
	}
	old, err := m.ClashSettings(true)
	if err != nil {
		return err
	}
	if s.Secret == "" && old.Secret != "" {
		if strings.TrimRight(old.BaseURL, "/") != strings.TrimRight(s.BaseURL, "/") {
			return errors.New("Clash 地址改变，请重新输入密钥")
		}
		s.Secret = old.Secret
	}
	if s.Secret != "" {
		s.Secret, err = m.Auth.Encrypt(s.Secret)
		if err != nil {
			return err
		}
	}
	s.SecretSet = false
	return m.DB.Setting("ai_clash", s)
}
func (m *Manager) clashTool(ctx context.Context, j Job, raw json.RawMessage) (any, error) {
	if !j.AllowClash {
		return nil, errors.New("此任务未授权 Clash 操作")
	}
	var a struct {
		Operation string `json:"operation"`
		Group     string `json:"group"`
		Node      string `json:"node"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, err
	}
	s, err := m.ClashSettings(true)
	if err != nil {
		return nil, err
	}
	if s.BaseURL == "" {
		return nil, errors.New("尚未配置 Clash 控制地址")
	}
	method, path, body := "GET", "/proxies", []byte(nil)
	switch a.Operation {
	case "inspect":
	case "delay":
		if a.Node == "" {
			return nil, errors.New("缺少节点")
		}
		path = "/proxies/" + url.PathEscape(a.Node) + "/delay?timeout=5000&url=" + url.QueryEscape("https://www.gstatic.com/generate_204")
	case "select":
		allowed := false
		for _, g := range s.Groups {
			if g == a.Group {
				allowed = true
			}
		}
		if !allowed {
			return nil, errors.New("此 Clash 策略组未授权")
		}
		if a.Node == "" {
			return nil, errors.New("缺少节点")
		}
		method = "PUT"
		path = "/proxies/" + url.PathEscape(a.Group)
		body, _ = json.Marshal(map[string]string{"name": a.Node})
	default:
		return nil, errors.New("未知 Clash 操作")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(s.BaseURL, "/")+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	if s.Secret != "" {
		req.Header.Set("Authorization", "Bearer "+s.Secret)
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return nil, errors.New("Clash 控制接口连接失败")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("Clash 返回 HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 32*1024+1))
	if err != nil {
		return nil, err
	}
	if len(data) > 32*1024 {
		return nil, errors.New("Clash 响应超过 32 KiB")
	}
	if len(data) == 0 {
		return map[string]any{"success": true, "group": a.Group, "node": a.Node}, nil
	}
	var result any
	if err = json.Unmarshal(data, &result); err != nil {
		return nil, errors.New("Clash 响应无效")
	}
	return result, nil
}
