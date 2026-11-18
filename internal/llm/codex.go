package llm

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
)

const codexVersion = "0.160.0"

type codex struct {
	base    string
	headers map[string]string
	hc      *http.Client
	idle    time.Duration
}

func newCodex(p *config.Provider, hc *http.Client, idle time.Duration) Client {
	base := strings.TrimRight(p.BaseURL, "/")
	if base == "" {
		base = "https://chatgpt.com/backend-api/codex"
	}
	h := map[string]string{"User-Agent": "codex_cli_rs/" + codexVersion, "originator": "codex_cli_rs", "version": codexVersion, "OpenAI-Beta": "responses=experimental", "Accept": "text/event-stream"}
	for k, v := range p.Headers {
		h[k] = v
	}
	h["Authorization"] = "Bearer " + p.APIKey
	if hc == nil {
		hc = http.DefaultClient
	}
	return &codex{base: base, headers: h, hc: hc, idle: idle}
}

func (c *codex) Stream(ctx context.Context, req Request, onDelta func(Delta)) (*Response, error) {
	input := []any{}
	instructions := []string{}
	if req.System != "" {
		instructions = append(instructions, req.System)
	}
	for _, m := range req.Messages {
		switch m.Role {
		case "system":
			instructions = append(instructions, m.Content)
		case "tool":
			input = append(input, map[string]any{"type": "function_call_output", "call_id": m.ToolCallID, "output": m.Content})
		case "assistant":
			if m.ReasoningSig != "" {
				input = append(input, map[string]any{"type": "reasoning", "encrypted_content": m.ReasoningSig, "summary": []any{}})
			}
			if m.Content != "" {
				input = append(input, map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": m.Content}}})
			}
			for _, tc := range m.ToolCalls {
				input = append(input, map[string]any{"type": "function_call", "call_id": tc.ID, "name": tc.Name, "arguments": string(argsOrEmpty(tc.Args))})
			}
		default:
			parts := []any{}
			if m.Content != "" {
				parts = append(parts, map[string]any{"type": "input_text", "text": m.Content})
			}
			for _, im := range m.Images {
				parts = append(parts, map[string]any{"type": "input_image", "image_url": "data:" + im.MIME + ";base64," + im.Data})
			}
			if len(parts) > 0 {
				input = append(input, map[string]any{"role": "user", "content": parts})
			}
		}
	}
	body := map[string]any{"model": req.Model, "instructions": strings.Join(instructions, "\n\n"), "input": input, "stream": true, "store": false, "include": []string{"reasoning.encrypted_content"}}
	// The subscription endpoint rejects temperature and max_output_tokens.
	if req.Reasoning != "" && req.Reasoning != "off" {
		body["reasoning"] = map[string]any{"effort": req.Reasoning, "summary": "auto"}
	}
	if req.JSON {
		body["text"] = map[string]any{"format": map[string]any{"type": "json_object"}}
	}
	if len(req.Tools) > 0 {
		tools := []any{}
		for _, t := range req.Tools {
			tools = append(tools, map[string]any{"type": "function", "name": t.Name, "description": t.Description, "parameters": t.Schema})
		}
		body["tools"] = tools
		body["tool_choice"] = "auto"
	}
	resp, err := postJSON(ctx, c.hc, c.base+"/responses", body, c.headers)
	if err != nil {
		return nil, err
	}
	out := &Response{}
	type item struct {
		ID        string `json:"id"`
		CallID    string `json:"call_id"`
		Type      string `json:"type"`
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
		Encrypted string `json:"encrypted_content"`
	}
	calls := map[string]*ToolCall{}
	order := []string{}
	save := func(it item, final bool) {
		if it.Type == "reasoning" && it.Encrypted != "" {
			out.ReasoningSig = it.Encrypted
		}
		if it.Type != "function_call" {
			return
		}
		key := it.ID
		if key == "" {
			key = it.CallID
		}
		tc := calls[key]
		if tc == nil {
			tc = &ToolCall{}
			calls[key] = tc
			order = append(order, key)
		}
		if it.CallID != "" {
			tc.ID = it.CallID
		} else if tc.ID == "" {
			tc.ID = key
		}
		if it.Name != "" {
			tc.Name = it.Name
		}
		if final || it.Arguments != "" {
			tc.Args = json.RawMessage(it.Arguments)
		}
	}
	completed := false
	err = readSSE(ctx, resp.Body, c.idle, func(_, data string) error {
		var e struct {
			Type      string          `json:"type"`
			Delta     string          `json:"delta"`
			ItemID    string          `json:"item_id"`
			Arguments string          `json:"arguments"`
			Item      item            `json:"item"`
			Error     json.RawMessage `json:"error"`
			Response  struct {
				Status     string          `json:"status"`
				Error      json.RawMessage `json:"error"`
				Output     []item          `json:"output"`
				Incomplete struct {
					Reason string `json:"reason"`
				} `json:"incomplete_details"`
				Usage struct {
					Input   int64 `json:"input_tokens"`
					Output  int64 `json:"output_tokens"`
					Details struct {
						Cached int64 `json:"cached_tokens"`
					} `json:"input_tokens_details"`
				} `json:"usage"`
			} `json:"response"`
		}
		if data == "[DONE]" {
			return nil
		}
		if err := json.Unmarshal([]byte(data), &e); err != nil {
			return fmt.Errorf("codex stream: %w", err)
		}
		d := Delta{}
		switch e.Type {
		case "response.output_text.delta":
			out.Text += e.Delta
			d.Text = e.Delta
		case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
			out.Reasoning += e.Delta
			d.Reasoning = e.Delta
		case "response.output_item.added":
			save(e.Item, false)
		case "response.output_item.done":
			save(e.Item, true)
		case "response.function_call_arguments.delta":
			if tc := calls[e.ItemID]; tc != nil {
				tc.Args = append(tc.Args, []byte(e.Delta)...)
			}
		case "response.function_call_arguments.done":
			if tc := calls[e.ItemID]; tc != nil {
				tc.Args = json.RawMessage(e.Arguments)
			}
		case "error":
			return fmt.Errorf("codex stream error: %s", data)
		case "response.failed":
			return fmt.Errorf("codex response failed: %s", e.Response.Error)
		case "response.completed", "response.incomplete":
			if e.Response.Status == "failed" {
				return fmt.Errorf("codex response failed: %s", e.Response.Error)
			}
			completed = true
			for _, it := range e.Response.Output {
				save(it, true)
			}
			out.Usage = Usage{Input: e.Response.Usage.Input, Output: e.Response.Usage.Output, CacheRead: e.Response.Usage.Details.Cached}
			out.StopReason = "stop"
			if len(order) > 0 {
				out.StopReason = "tool_calls"
			}
			if e.Response.Incomplete.Reason == "max_output_tokens" {
				out.StopReason = "length"
			}
		}
		if onDelta != nil && (d.Text != "" || d.Reasoning != "") {
			onDelta(d)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if !completed {
		return nil, fmt.Errorf("codex: stream ended before response completion")
	}
	for _, id := range order {
		tc := *calls[id]
		tc.Args = argsOrEmpty(tc.Args)
		out.ToolCalls = append(out.ToolCalls, tc)
	}
	return out, nil
}

func (c *codex) listModels(ctx context.Context) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/models?client_version="+codexVersion, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, &APIError{Status: resp.StatusCode, Body: string(b)}
	}
	var v struct {
		Models []struct {
			Slug       string `json:"slug"`
			Visibility string `json:"visibility"`
			Priority   *int   `json:"priority"`
		} `json:"models"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&v); err != nil {
		return nil, err
	}
	sort.SliceStable(v.Models, func(i, j int) bool {
		a, b := 99, 99
		if v.Models[i].Priority != nil {
			a = *v.Models[i].Priority
		}
		if v.Models[j].Priority != nil {
			b = *v.Models[j].Priority
		}
		return a < b
	})
	models := []string{}
	for _, m := range v.Models {
		if m.Slug != "" && m.Visibility != "hide" {
			models = append(models, m.Slug)
		}
	}
	return models, nil
}
