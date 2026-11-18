package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
)

type openAI struct {
	name, base, key string
	headers         map[string]string
	hc              *http.Client
	idle            time.Duration
}

func (c *openAI) Stream(ctx context.Context, req Request, onDelta func(Delta)) (*Response, error) {
	msgs := []map[string]any{}
	if req.System != "" {
		msgs = append(msgs, map[string]any{"role": "system", "content": req.System})
	}
	for _, m := range req.Messages {
		switch m.Role {
		case "tool":
			msgs = append(msgs, map[string]any{"role": "tool", "tool_call_id": m.ToolCallID, "content": m.Content})
		case "assistant":
			am := map[string]any{"role": "assistant", "content": m.Content}
			if len(m.ToolCalls) > 0 {
				var tcs []map[string]any
				for _, tc := range m.ToolCalls {
					tcs = append(tcs, map[string]any{"id": tc.ID, "type": "function",
						"function": map[string]any{"name": tc.Name, "arguments": string(argsOrEmpty(tc.Args))}})
				}
				am["tool_calls"] = tcs
				if m.Content == "" {
					am["content"] = nil
				}
			}
			if m.Reasoning != "" && strings.Contains(c.base, "deepseek") {
				am["reasoning_content"] = m.Reasoning
			}
			msgs = append(msgs, am)
		default:
			if len(m.Images) > 0 {
				parts := []map[string]any{{"type": "text", "text": m.Content}}
				for _, im := range m.Images {
					parts = append(parts, map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:" + im.MIME + ";base64," + im.Data}})
				}
				msgs = append(msgs, map[string]any{"role": m.Role, "content": parts})
			} else {
				msgs = append(msgs, map[string]any{"role": m.Role, "content": m.Content})
			}
		}
	}
	body := map[string]any{
		"model": req.Model, "messages": msgs, "stream": true,
		"stream_options": map[string]any{"include_usage": true},
	}
	isOpenAI := strings.Contains(c.base, "api.openai.com")
	reasoningModel := isOpenAI && (strings.HasPrefix(req.Model, "o") || strings.HasPrefix(req.Model, "gpt-5"))
	if req.MaxTokens > 0 {
		if isOpenAI {
			body["max_completion_tokens"] = req.MaxTokens
		} else {
			body["max_tokens"] = req.MaxTokens
		}
	}
	if !reasoningModel {
		body["temperature"] = req.Temperature
	}
	if req.Reasoning != "" && req.Reasoning != "off" {
		if reasoningModel {
			body["reasoning_effort"] = req.Reasoning
		} else if strings.Contains(c.base, "openrouter") {
			body["reasoning"] = map[string]any{"effort": req.Reasoning}
		}
	}
	if req.JSON {
		body["response_format"] = map[string]any{"type": "json_object"}
	}
	if len(req.Tools) > 0 {
		var tools []map[string]any
		for _, t := range req.Tools {
			tools = append(tools, map[string]any{"type": "function", "function": map[string]any{
				"name": t.Name, "description": t.Description, "parameters": t.Schema}})
		}
		body["tools"] = tools
	}
	h := map[string]string{}
	for k, v := range c.headers {
		h[k] = v
	}
	if c.key != "" {
		h["Authorization"] = "Bearer " + c.key
	}
	if strings.Contains(c.base, "openrouter") {
		h["HTTP-Referer"] = "https://github.com/allones/allones"
		h["X-Title"] = "AllOnes"
	}
	resp, err := postJSON(ctx, c.hc, c.base+"/chat/completions", body, h)
	if err != nil {
		return nil, err
	}
	out := &Response{}
	type partial struct {
		id, name string
		args     strings.Builder
	}
	calls := map[int]*partial{}
	var text, reasoning strings.Builder
	err = readSSE(ctx, resp.Body, c.idle, func(_, data string) error {
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content          string `json:"content"`
					Reasoning        string `json:"reasoning"`
					ReasoningContent string `json:"reasoning_content"`
					ToolCalls        []struct {
						Index    int    `json:"index"`
						ID       string `json:"id"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
			Usage *struct {
				PromptTokens     int64 `json:"prompt_tokens"`
				CompletionTokens int64 `json:"completion_tokens"`
				Details          struct {
					Cached int64 `json:"cached_tokens"`
				} `json:"prompt_tokens_details"`
			} `json:"usage"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return nil
		}
		if chunk.Error != nil {
			return fmt.Errorf("provider error: %s", chunk.Error.Message)
		}
		if chunk.Usage != nil {
			out.Usage = Usage{Input: chunk.Usage.PromptTokens, Output: chunk.Usage.CompletionTokens, CacheRead: chunk.Usage.Details.Cached}
		}
		for _, ch := range chunk.Choices {
			d := ch.Delta
			r := d.ReasoningContent + d.Reasoning
			if d.Content != "" || r != "" {
				text.WriteString(d.Content)
				reasoning.WriteString(r)
				if onDelta != nil {
					onDelta(Delta{Text: d.Content, Reasoning: r})
				}
			}
			for _, tc := range d.ToolCalls {
				p := calls[tc.Index]
				if p == nil {
					p = &partial{}
					calls[tc.Index] = p
				}
				if tc.ID != "" {
					p.id = tc.ID
				}
				if tc.Function.Name != "" {
					p.name += tc.Function.Name
				}
				p.args.WriteString(tc.Function.Arguments)
			}
			if ch.FinishReason != "" {
				out.StopReason = ch.FinishReason
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	out.Text, out.Reasoning = text.String(), reasoning.String()
	idx := make([]int, 0, len(calls))
	for i := range calls {
		idx = append(idx, i)
	}
	sort.Ints(idx)
	for _, i := range idx {
		p := calls[i]
		id := p.id
		if id == "" {
			id = fmt.Sprintf("call_%d_%d", time.Now().UnixNano(), i)
		}
		out.ToolCalls = append(out.ToolCalls, ToolCall{ID: id, Name: p.name, Args: argsOrEmpty(json.RawMessage(p.args.String()))})
	}
	return out, nil
}

// Embed calls an OpenAI-compatible /embeddings endpoint.
func Embed(ctx context.Context, base, key, model string, inputs []string) ([][]float32, error) {
	hc := &http.Client{Timeout: 120 * time.Second}
	h := map[string]string{}
	if key != "" {
		h["Authorization"] = "Bearer " + key
	}
	resp, err := postJSON(ctx, hc, strings.TrimRight(base, "/")+"/embeddings", map[string]any{"model": model, "input": inputs}, h)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var r struct {
		Data []struct {
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, err
	}
	out := make([][]float32, len(inputs))
	for _, d := range r.Data {
		if d.Index < len(out) {
			out[d.Index] = d.Embedding
		}
	}
	return out, nil
}
