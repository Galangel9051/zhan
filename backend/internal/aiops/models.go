package aiops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

type ModelConfig struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	Enabled         bool     `json:"enabled"`
	ContextWindow   int      `json:"context_window"`
	MaxOutputTokens int      `json:"max_output_tokens"`
	Temperature     *float64 `json:"temperature,omitempty"`
	OutputParameter string   `json:"output_parameter"`
}
type AvailableModel struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Owner           string `json:"owned_by"`
	ContextWindow   int    `json:"context_window,omitempty"`
	MaxOutputTokens int    `json:"max_output_tokens,omitempty"`
}

func validateEndpoint(endpoint string) error {
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("请填写 HTTP(S) API 基础地址，不含凭据或查询参数")
	}
	return nil
}

// A nil models list identifies the legacy single-model configuration. An explicit empty list stays empty.
func normalizeModels(s *Settings) error {
	if s.Models == nil {
		s.Models = []ModelConfig{}
		if s.Model != "" {
			s.Models = append(s.Models, ModelConfig{ID: s.Model, Enabled: true})
		}
	}
	if len(s.Models) > 200 {
		return errors.New("最多保存 200 个模型")
	}
	seen := map[string]bool{}
	first := ""
	defaultEnabled := false
	for i := range s.Models {
		m := &s.Models[i]
		m.ID = strings.TrimSpace(m.ID)
		if m.ID == "" || len(m.ID) > 200 || strings.ContainsAny(m.ID, "\r\n") || len(m.Name) > 200 || seen[m.ID] {
			return errors.New("模型 ID 不能为空、重复或超过 200 字节")
		}
		seen[m.ID] = true
		if m.Name == "" {
			m.Name = m.ID
		}
		if m.ContextWindow == 0 {
			m.ContextWindow = 32768
		}
		if m.MaxOutputTokens == 0 {
			m.MaxOutputTokens = 4096
		}
		if m.ContextWindow < 1024 || m.ContextWindow > 4000000 || m.MaxOutputTokens < 1 || m.MaxOutputTokens >= m.ContextWindow {
			return fmt.Errorf("模型 %s 的上下文须为 1024–4000000，最大输出须大于 0 且小于上下文", m.ID)
		}
		if m.Temperature != nil && (math.IsNaN(*m.Temperature) || math.IsInf(*m.Temperature, 0) || *m.Temperature < 0 || *m.Temperature > 2) {
			return errors.New("温度须为 0–2，或留空使用上游默认值")
		}
		if m.OutputParameter == "" {
			m.OutputParameter = "max_completion_tokens"
		}
		if m.OutputParameter != "max_tokens" && m.OutputParameter != "max_completion_tokens" {
			return errors.New("无效输出限制参数")
		}
		if m.Enabled {
			if first == "" {
				first = m.ID
			}
			if m.ID == s.Model {
				defaultEnabled = true
			}
		}
	}
	if first == "" {
		if s.Enabled {
			return errors.New("启用 AI 接口前请至少勾选一个模型")
		}
		s.Model = ""
		return nil
	}
	if s.Model == "" {
		s.Model = first
	} else if !defaultEnabled {
		return errors.New("默认模型必须是已启用的模型")
	}
	return nil
}
func (s Settings) selectedModel(id string) (ModelConfig, error) {
	if id == "" {
		id = s.Model
	}
	for _, m := range s.Models {
		if m.ID == id && m.Enabled {
			return m, nil
		}
	}
	return ModelConfig{}, fmt.Errorf("模型 %q 未启用，请在 AI 接口中勾选，或修改任务模型", id)
}

// DiscoverModels uses a draft endpoint/key without persisting them. It never guesses model capabilities.
func (m *Manager) DiscoverModels(ctx context.Context, draft Settings) ([]AvailableModel, error) {
	if err := validateEndpoint(draft.BaseURL); err != nil {
		return nil, err
	}
	if len(draft.APIKey) > 4096 {
		return nil, errors.New("API Key 过长")
	}
	if draft.APIKey == "" {
		saved, err := m.Settings(true)
		if err != nil {
			return nil, err
		}
		if saved.APIKey != "" {
			if strings.TrimRight(draft.BaseURL, "/") != strings.TrimRight(saved.BaseURL, "/") {
				return nil, errors.New("接口地址已改变，请重新输入 API Key 后拉取")
			}
			draft.APIKey = saved.APIKey
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(draft.BaseURL, "/")+"/models", nil)
	if err != nil {
		return nil, err
	}
	if draft.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+draft.APIKey)
	}
	response, err := m.Client.Do(req)
	if err != nil {
		return nil, errors.New("拉取模型列表失败，请检查连接或超时")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return nil, fmt.Errorf("拉取模型列表失败：上游 HTTP %d", response.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 2*1024*1024+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > 2*1024*1024 {
		return nil, errors.New("上游模型列表超过 2 MiB")
	}
	var result struct {
		Data []struct {
			ID              string `json:"id"`
			Name            string `json:"name"`
			Owner           string `json:"owned_by"`
			ContextWindow   int    `json:"context_window"`
			ContextLength   int    `json:"context_length"`
			MaxOutputTokens int    `json:"max_output_tokens"`
		} `json:"data"`
	}
	if err = json.Unmarshal(raw, &result); err != nil || result.Data == nil || len(result.Data) > 5000 {
		return nil, errors.New("上游模型列表格式无效，预期 data 数组（最多 5000 项）")
	}
	models := []AvailableModel{}
	seen := map[string]bool{}
	for _, item := range result.Data {
		id := strings.TrimSpace(item.ID)
		if id == "" || len(id) > 200 || seen[id] {
			continue
		}
		seen[id] = true
		contextWindow := item.ContextWindow
		if contextWindow == 0 {
			contextWindow = item.ContextLength
		}
		if contextWindow < 1024 || contextWindow > 4000000 {
			contextWindow = 0
		}
		maxOutput := item.MaxOutputTokens
		if maxOutput < 1 || maxOutput > 4000000 {
			maxOutput = 0
		}
		models = append(models, AvailableModel{ID: id, Name: item.Name, Owner: item.Owner, ContextWindow: contextWindow, MaxOutputTokens: maxOutput})
	}
	sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	return models, nil
}

// This byte-based upper estimate is intentionally conservative across unknown upstream tokenizers.
// Stop rather than silently dropping tool results or task instructions.
func requestBody(s Settings, messages []message, tools []any) ([]byte, error) {
	model, err := s.selectedModel(s.Model)
	if err != nil {
		return nil, err
	}
	input, err := json.Marshal(map[string]any{"messages": messages, "tools": tools})
	if err != nil {
		return nil, err
	}
	estimated := len(input) + 1024
	if estimated+model.MaxOutputTokens > model.ContextWindow {
		return nil, fmt.Errorf("上下文预算不足：保守估算输入 %d，加输出预留 %d 超过窗口 %d；请增大上下文、减少输入或降低输出上限", estimated, model.MaxOutputTokens, model.ContextWindow)
	}
	body := map[string]any{"model": model.ID, "messages": messages, "tools": tools, "tool_choice": "auto", model.OutputParameter: model.MaxOutputTokens}
	if model.Temperature != nil {
		body["temperature"] = *model.Temperature
	}
	return json.Marshal(body)
}
