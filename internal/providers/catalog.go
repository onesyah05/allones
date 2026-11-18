// Package providers is the catalogue of well-known model providers: what the
// dashboard offers to connect in one step, and how to list a provider's models
// or test its credentials.
package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/allones/allones/internal/config"
	"github.com/allones/allones/internal/llm"
)

type Info struct {
	ID       string   `json:"id"`
	Label    string   `json:"label"`
	Kind     string   `json:"kind"`
	KeyEnv   string   `json:"key_env"`
	BaseURL  string   `json:"base_url"`
	NeedsKey bool     `json:"needs_key"`
	Models   []string `json:"models"`
	Group    string   `json:"group"` // frontier | gateway | fast | open | local | custom
	KeyURL   string   `json:"key_url"`
	Note     string   `json:"note"`
}

var catalog = []Info{
	{"codex", "Codex (ChatGPT subscription)", "codex", "", "https://chatgpt.com/backend-api/codex", false,
		[]string{"gpt-5.3-codex", "gpt-5.2-codex", "gpt-5.1-codex-mini"}, "subscription", "https://chatgpt.com", "Sign in with your ChatGPT account using the device authorization flow"},
	{"claude-code", "Claude Code (subscription)", "claude-code", "", "https://api.anthropic.com", false,
		[]string{"claude-sonnet-4-6", "claude-opus-4-6", "claude-haiku-4-5"}, "subscription", "https://claude.ai", "Sign in with your Claude account using OAuth; requires subscription access"},
	{"antigravity", "Antigravity (Google subscription)", "antigravity", "", "https://daily-cloudcode-pa.googleapis.com", false,
		[]string{"gemini-3-flash", "gemini-3-pro-high", "claude-sonnet-4-6"}, "subscription", "https://accounts.google.com", "Sign in with Google; models and availability depend on account entitlement"},
	{"anthropic", "Anthropic", "anthropic", "ANTHROPIC_API_KEY", "https://api.anthropic.com", true,
		[]string{"claude-opus-4-5", "claude-sonnet-4-5", "claude-haiku-4-5"}, "frontier", "https://console.anthropic.com/settings/keys", "Claude over the native adapter, with extended thinking and prompt caching"},
	{"openai", "OpenAI", "openai", "OPENAI_API_KEY", "https://api.openai.com/v1", true,
		[]string{"gpt-5", "gpt-5-mini", "o4-mini"}, "frontier", "https://platform.openai.com/api-keys", "GPT and o-series models"},
	{"google", "Google Gemini", "gemini", "GEMINI_API_KEY", "https://generativelanguage.googleapis.com", true,
		[]string{"gemini-2.5-pro", "gemini-2.5-flash"}, "frontier", "https://aistudio.google.com/apikey", "Gemini over the native adapter, with thinking budgets"},
	{"xai", "xAI Grok", "openai-compatible", "XAI_API_KEY", "https://api.x.ai/v1", true,
		[]string{"grok-4", "grok-3-mini"}, "frontier", "https://console.x.ai", ""},
	{"openrouter", "OpenRouter", "openai-compatible", "OPENROUTER_API_KEY", "https://openrouter.ai/api/v1", true,
		[]string{"anthropic/claude-sonnet-4.5", "openai/gpt-5", "google/gemini-2.5-pro"}, "gateway", "https://openrouter.ai/keys", "One key for hundreds of models"},
	{"opencode", "OpenCode Zen", "openai-compatible", "OPENCODE_API_KEY", "https://opencode.ai/zen/v1", true,
		[]string{}, "gateway", "https://opencode.ai/auth", "Curated coding models. Fetch the live list after connecting"},
	{"together", "Together AI", "openai-compatible", "TOGETHER_API_KEY", "https://api.together.xyz/v1", true,
		[]string{"meta-llama/Llama-3.3-70B-Instruct-Turbo"}, "gateway", "https://api.together.ai/settings/api-keys", ""},
	{"fireworks", "Fireworks AI", "openai-compatible", "FIREWORKS_API_KEY", "https://api.fireworks.ai/inference/v1", true,
		[]string{}, "gateway", "https://fireworks.ai/account/api-keys", ""},
	{"groq", "Groq", "openai-compatible", "GROQ_API_KEY", "https://api.groq.com/openai/v1", true,
		[]string{"llama-3.3-70b-versatile", "moonshotai/kimi-k2-instruct"}, "fast", "https://console.groq.com/keys", "Very fast inference"},
	{"cerebras", "Cerebras", "openai-compatible", "CEREBRAS_API_KEY", "https://api.cerebras.ai/v1", true,
		[]string{"llama-3.3-70b"}, "fast", "https://cloud.cerebras.ai", "Very fast inference"},
	{"deepseek", "DeepSeek", "openai-compatible", "DEEPSEEK_API_KEY", "https://api.deepseek.com/v1", true,
		[]string{"deepseek-chat", "deepseek-reasoner"}, "open", "https://platform.deepseek.com/api_keys", ""},
	{"mistral", "Mistral", "openai-compatible", "MISTRAL_API_KEY", "https://api.mistral.ai/v1", true,
		[]string{"mistral-large-latest", "codestral-latest"}, "open", "https://console.mistral.ai/api-keys", ""},
	{"zai", "Z.ai GLM", "anthropic", "ZAI_API_KEY", "https://api.z.ai/api/anthropic/v1", true,
		[]string{"glm-4.6"}, "open", "https://z.ai/manage-apikey/apikey-list", "GLM coding plan over the Anthropic protocol"},
	{"moonshot", "Moonshot Kimi", "openai-compatible", "MOONSHOT_API_KEY", "https://api.moonshot.ai/v1", true,
		[]string{"kimi-k2-0905-preview"}, "open", "https://platform.moonshot.ai/console/api-keys", ""},
	{"qwen", "Alibaba Qwen", "openai-compatible", "DASHSCOPE_API_KEY", "https://dashscope-intl.aliyuncs.com/compatible-mode/v1", true,
		[]string{"qwen-max", "qwen-plus"}, "open", "https://bailian.console.alibabacloud.com", ""},
	{"ollama", "Ollama", "openai-compatible", "", "http://127.0.0.1:11434/v1", false,
		[]string{}, "local", "", "Local models. Fetch the list from the running server"},
	{"lmstudio", "LM Studio", "openai-compatible", "", "http://127.0.0.1:1234/v1", false,
		[]string{}, "local", "", "Local models. Start the LM Studio server first"},
}

func Catalog() []Info { return catalog }

func For(id string) (Info, bool) {
	for _, p := range catalog {
		if p.ID == id {
			return p, true
		}
	}
	return Info{}, false
}

// ListModels asks a provider which models it serves.
func ListModels(ctx context.Context, p *config.Provider) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	base := strings.TrimRight(p.BaseURL, "/")
	if base == "" {
		base = config.DefaultBaseURL(p.Kind, p.Name)
	}
	if p.Kind == "codex" || p.Kind == "antigravity" {
		return llm.SubscriptionModels(ctx, p)
	}
	var url string
	h := map[string]string{}
	for k, v := range p.Headers {
		h[k] = v
	}
	switch p.Kind {
	case "anthropic", "claude-code":
		if !strings.HasSuffix(base, "/v1") {
			base += "/v1"
		}
		url = base + "/models?limit=1000"
		if p.Kind == "claude-code" {
			h["Authorization"] = "Bearer " + p.APIKey
			h["anthropic-beta"] = "oauth-2025-04-20"
		} else {
			h["x-api-key"] = p.APIKey
		}
		h["anthropic-version"] = "2023-06-01"
	case "gemini":
		url = base + "/v1beta/models?pageSize=1000"
		h["x-goog-api-key"] = p.APIKey
	default:
		url = base + "/models"
		if p.APIKey != "" {
			h["Authorization"] = "Bearer " + p.APIKey
		}
	}
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range h {
		if v != "" {
			req.Header.Set(k, v)
		}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode/100 != 2 {
		msg := string(body)
		if len(msg) > 300 {
			msg = msg[:300]
		}
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, msg)
	}
	var r struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
		Models []struct {
			Name    string   `json:"name"`
			Methods []string `json:"supportedGenerationMethods"`
		} `json:"models"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		// Some servers return a bare array.
		var arr []struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(body, &arr) != nil {
			return nil, fmt.Errorf("unexpected response from %s", url)
		}
		r.Data = arr
	}
	seen := map[string]bool{}
	var out []string
	for _, m := range r.Data {
		if m.ID != "" && !seen[m.ID] {
			seen[m.ID] = true
			out = append(out, m.ID)
		}
	}
	for _, m := range r.Models {
		ok := len(m.Methods) == 0
		for _, x := range m.Methods {
			if x == "generateContent" {
				ok = true
			}
		}
		id := strings.TrimPrefix(m.Name, "models/")
		if ok && id != "" && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out, nil
}

// Test sends a one-word request to prove the key, endpoint and model work.
func Test(ctx context.Context, p *config.Provider, model string) (string, time.Duration, error) {
	client, err := llm.New(p)
	if err != nil {
		return "", 0, err
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	start := time.Now()
	out, err := llm.Complete(ctx, client, llm.Request{
		Model: model, Messages: []llm.Message{{Role: "user", Content: "Reply with the single word: ready"}},
		MaxTokens: 256, Temperature: 0,
	})
	return strings.TrimSpace(out), time.Since(start), err
}
