package aiops

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDiscoverModelsDraftCredentialsAndMetadata(t *testing.T) {
	m, _ := fixture(t)
	server := provider(t, m, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/v1/models" || r.Header.Get("Authorization") != "Bearer secret-key" {
			t.Errorf("request: %s %s %s", r.Method, r.URL.Path, r.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"z-model","context_length":131072,"max_output_tokens":8192},{"id":"a-model","name":"Alpha"},{"id":"z-model"},{"id":""}]}`))
	})
	list, err := m.DiscoverModels(context.Background(), Settings{BaseURL: server.URL + "/v1"})
	if err != nil || len(list) != 2 || list[0].ID != "a-model" || list[0].ContextWindow != 0 || list[1].ContextWindow != 131072 {
		t.Fatalf("models=%+v err=%v", list, err)
	}
	before, _ := m.Settings(false)
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer new-key" {
			t.Error("draft key missing")
		}
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer other.Close()
	if _, err = m.DiscoverModels(context.Background(), Settings{BaseURL: other.URL}); err == nil {
		t.Fatal("saved key reused at changed endpoint")
	}
	if _, err = m.DiscoverModels(context.Background(), Settings{BaseURL: other.URL, APIKey: "new-key"}); err != nil {
		t.Fatal(err)
	}
	after, _ := m.Settings(false)
	if before.BaseURL != after.BaseURL || before.Model != after.Model {
		t.Fatal("discovery persisted draft")
	}
}
func TestDiscoveryRejectsMalformedErrorsAndRedirects(t *testing.T) {
	m, _ := fixture(t)
	for _, body := range []string{`{}`, `{"data":null}`, `oops`} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) }))
		_, err := m.DiscoverModels(context.Background(), Settings{BaseURL: server.URL})
		server.Close()
		if err == nil {
			t.Fatalf("accepted malformed %s", body)
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		_, _ = w.Write([]byte("echo secret-token"))
	}))
	defer server.Close()
	if _, err := m.DiscoverModels(context.Background(), Settings{BaseURL: server.URL, APIKey: "secret-token"}); err == nil || strings.Contains(err.Error(), "secret-token") {
		t.Fatal("upstream error leaked")
	}
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("redirect followed with API key") }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, http.StatusFound) }))
	defer redirect.Close()
	if _, err := m.DiscoverModels(context.Background(), Settings{BaseURL: redirect.URL, APIKey: "secret-token"}); err == nil {
		t.Fatal("redirect accepted")
	}
}
func TestModelSettingsMigrationSelectionAndValidation(t *testing.T) {
	m, _ := fixture(t)
	if err := m.DB.Setting("ai_provider", Settings{Enabled: true, BaseURL: "http://localhost/v1", Model: "legacy"}); err != nil {
		t.Fatal(err)
	}
	s, err := m.Settings(false)
	if err != nil || len(s.Models) != 1 || !s.Models[0].Enabled || s.Models[0].ContextWindow != 32768 {
		t.Fatalf("legacy=%+v %v", s, err)
	}
	temp := 0.0
	s.Models = append(s.Models, ModelConfig{ID: "second", Enabled: true, ContextWindow: 131072, MaxOutputTokens: 8192, Temperature: &temp, OutputParameter: "max_tokens"})
	s.Model = "second"
	if err = m.SaveSettings(s); err != nil {
		t.Fatal(err)
	}
	saved, _ := m.Settings(false)
	chosen, err := saved.selectedModel("")
	if err != nil || chosen.ID != "second" || chosen.Temperature == nil || *chosen.Temperature != 0 {
		t.Fatal("model options lost")
	}
	saved.Models[1].Enabled = false
	if err = m.SaveSettings(saved); err == nil {
		t.Fatal("disabled default allowed")
	}
	saved.Model = "legacy"
	if err = m.SaveSettings(saved); err != nil {
		t.Fatal(err)
	}
	current, _ := m.Settings(false)
	if _, err = current.selectedModel("second"); err == nil {
		t.Fatal("disabled model selected")
	}
	cases := []ModelConfig{
		{ID: "x", Enabled: true, ContextWindow: 100},
		{ID: "x", Enabled: true, ContextWindow: 4096, MaxOutputTokens: 4096},
		{ID: "x", Enabled: true, OutputParameter: "arbitrary"},
	}
	for _, model := range cases {
		bad := Settings{Enabled: true, Model: "x", Models: []ModelConfig{model}}
		if err = normalizeModels(&bad); err == nil {
			t.Fatalf("accepted %+v", model)
		}
	}
	bad := Settings{Enabled: true, Models: []ModelConfig{}}
	if err = normalizeModels(&bad); err == nil {
		t.Fatal("enabled with no models")
	}
	bad = Settings{Models: []ModelConfig{{ID: "same"}, {ID: "same"}}}
	if err = normalizeModels(&bad); err == nil {
		t.Fatal("duplicate ID accepted")
	}
}
func TestSelectedModelParametersAndContextBudget(t *testing.T) {
	s := Settings{Model: "chosen", Models: []ModelConfig{{ID: "chosen", Enabled: true, ContextWindow: 32768, MaxOutputTokens: 4096, Temperature: new(0.0), OutputParameter: "max_tokens"}}}
	raw, err := requestBody(s, []message{{Role: "user", Content: "检查文件"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err = json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if body["model"] != "chosen" || body["max_tokens"] != float64(4096) || body["temperature"] != float64(0) || body["max_completion_tokens"] != nil {
		t.Fatalf("params=%s", raw)
	}
	s.Models[0].Temperature = nil
	s.Models[0].OutputParameter = "max_completion_tokens"
	raw, err = requestBody(s, []message{{Role: "user", Content: "ok"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "temperature") || !strings.Contains(string(raw), "max_completion_tokens") {
		t.Fatalf("params=%s", raw)
	}
	if _, err = requestBody(s, []message{{Role: "user", Content: strings.Repeat("长", 10000)}}, nil); err == nil {
		t.Fatal("context budget ignored")
	}
}
func TestJobSelectsConfiguredModel(t *testing.T) {
	m, j := fixture(t)
	provider(t, m, func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model string `json:"model"`
			Max   int    `json:"max_completion_tokens"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.Model != "special" || body.Max != 1024 {
			t.Errorf("wrong model params %+v", body)
		}
		assistant(w, "ok")
	})
	s, _ := m.Settings(false)
	s.Models = append(s.Models, ModelConfig{ID: "special", Enabled: true, ContextWindow: 16384, MaxOutputTokens: 1024})
	if err := m.SaveSettings(s); err != nil {
		t.Fatal(err)
	}
	j.Model = "special"
	if err := m.SaveJob(&j); err != nil {
		t.Fatal(err)
	}
	id, err := m.Enqueue(j.ID)
	if err != nil {
		t.Fatal(err)
	}
	m.work(context.Background())
	r, _ := m.Run(id)
	if r.Status != "success" || !strings.Contains(r.Output, "special") {
		t.Fatalf("run=%+v", r)
	}
	s, _ = m.Settings(false)
	s.Models[1].Enabled = false
	if err = m.SaveSettings(s); err != nil {
		t.Fatal(err)
	}
	id, err = m.Enqueue(j.ID)
	if err != nil {
		t.Fatal(err)
	}
	m.work(context.Background())
	r, _ = m.Run(id)
	if r.Status != "failed" || !strings.Contains(r.Error, "未启用") {
		t.Fatal("disabled job model was used")
	}
}
