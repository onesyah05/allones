package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type gemini struct {
	base, key string
	headers   map[string]string
	hc        *http.Client
	idle      time.Duration
}

// cleanSchema removes JSON-Schema keywords Gemini rejects.
func cleanSchema(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, val := range t {
			switch k {
			case "additionalProperties", "$schema", "default", "examples", "$id", "$ref", "definitions", "$defs", "pattern", "format":
				continue
			}
			out[k] = cleanSchema(val)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i := range t {
			out[i] = cleanSchema(t[i])
		}
		return out
	}
	return v
}

func (c *gemini) Stream(ctx context.Context, req Request, onDelta func(Delta)) (*Response, error) {
	names := map[string]string{} // tool call id -> name
	var contents []map[string]any
	add := func(role string, parts ...map[string]any) {
		if n := len(contents); n > 0 && contents[n-1]["role"] == role {
			contents[n-1]["parts"] = append(contents[n-1]["parts"].([]map[string]any), parts...)
			return
		}
		contents = append(contents, map[string]any{"role": role, "parts": parts})
	}
	for _, m := range req.Messages {
		switch m.Role {
		case "assistant":
			var parts []map[string]any
			if m.Content != "" {
				parts = append(parts, map[string]any{"text": m.Content})
			}
			for _, tc := range m.ToolCalls {
				names[tc.ID] = tc.Name
				var args any = map[string]any{}
				json.Unmarshal(argsOrEmpty(tc.Args), &args)
				p := map[string]any{"functionCall": map[string]any{"name": tc.Name, "args": args}}
				if tc.Sig != "" {
					p["thoughtSignature"] = tc.Sig
				}
				parts = append(parts, p)
			}
			if len(parts) == 0 {
				parts = append(parts, map[string]any{"text": "(no output)"})
			}
			add("model", parts...)
		case "tool":
			name := names[m.ToolCallID]
			if name == "" {
				name = m.Name
			}
			add("user", map[string]any{"functionResponse": map[string]any{"name": name, "response": map[string]any{"result": m.Content}}})
		default:
			var parts []map[string]any
			for _, im := range m.Images {
				parts = append(parts, map[string]any{"inlineData": map[string]any{"mimeType": im.MIME, "data": im.Data}})
			}
			parts = append(parts, map[string]any{"text": m.Content})
			add("user", parts...)
		}
	}
	gen := map[string]any{"temperature": req.Temperature}
	if req.MaxTokens > 0 {
		gen["maxOutputTokens"] = req.MaxTokens
	}
	if b := budgetFor(req.Reasoning, req.MaxTokens); b > 0 {
		gen["thinkingConfig"] = map[string]any{"thinkingBudget": b, "includeThoughts": true}
	}
	if req.JSON {
		gen["responseMimeType"] = "application/json"
	}
	body := map[string]any{"contents": contents, "generationConfig": gen}
	if req.System != "" {
		body["systemInstruction"] = map[string]any{"parts": []map[string]any{{"text": req.System}}}
	}
	if len(req.Tools) > 0 {
		var decls []map[string]any
		for _, t := range req.Tools {
			decls = append(decls, map[string]any{"name": t.Name, "description": t.Description, "parameters": cleanSchema(t.Schema)})
		}
		body["tools"] = []map[string]any{{"functionDeclarations": decls}}
	}
	h := map[string]string{}
	for k, v := range c.headers {
		h[k] = v
	}
	h["x-goog-api-key"] = c.key
	u := fmt.Sprintf("%s/v1beta/models/%s:streamGenerateContent?alt=sse", c.base, url.PathEscape(req.Model))
	resp, err := postJSON(ctx, c.hc, u, body, h)
	if err != nil {
		return nil, err
	}
	out := &Response{}
	var text, reasoning strings.Builder
	n := 0
	err = readSSE(ctx, resp.Body, c.idle, func(_, data string) error {
		var chunk struct {
			Candidates []struct {
				Content struct {
					Parts []struct {
						Text             string `json:"text"`
						Thought          bool   `json:"thought"`
						ThoughtSignature string `json:"thoughtSignature"`
						FunctionCall     *struct {
							Name string          `json:"name"`
							Args json.RawMessage `json:"args"`
						} `json:"functionCall"`
					} `json:"parts"`
				} `json:"content"`
				FinishReason string `json:"finishReason"`
			} `json:"candidates"`
			Usage struct {
				Prompt  int64 `json:"promptTokenCount"`
				Output  int64 `json:"candidatesTokenCount"`
				Thought int64 `json:"thoughtsTokenCount"`
				Cached  int64 `json:"cachedContentTokenCount"`
			} `json:"usageMetadata"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return nil
		}
		if chunk.Error != nil {
			return fmt.Errorf("gemini: %s", chunk.Error.Message)
		}
		if chunk.Usage.Prompt > 0 {
			out.Usage = Usage{Input: chunk.Usage.Prompt, Output: chunk.Usage.Output + chunk.Usage.Thought, CacheRead: chunk.Usage.Cached}
		}
		for _, cand := range chunk.Candidates {
			for _, p := range cand.Content.Parts {
				switch {
				case p.FunctionCall != nil:
					n++
					out.ToolCalls = append(out.ToolCalls, ToolCall{ID: fmt.Sprintf("gc_%d_%d", time.Now().UnixNano(), n), Name: p.FunctionCall.Name, Args: argsOrEmpty(p.FunctionCall.Args), Sig: p.ThoughtSignature})
				case p.Thought:
					reasoning.WriteString(p.Text)
					if onDelta != nil && p.Text != "" {
						onDelta(Delta{Reasoning: p.Text})
					}
				case p.Text != "":
					text.WriteString(p.Text)
					if onDelta != nil {
						onDelta(Delta{Text: p.Text})
					}
				}
			}
			if cand.FinishReason != "" {
				out.StopReason = cand.FinishReason
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	out.Text, out.Reasoning = text.String(), reasoning.String()
	return out, nil
}
