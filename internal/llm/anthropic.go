package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type anthropic struct {
	base, key string
	headers   map[string]string
	hc        *http.Client
	idle      time.Duration
}

func (c *anthropic) Stream(ctx context.Context, req Request, onDelta func(Delta)) (*Response, error) {
	budget := budgetFor(req.Reasoning, req.MaxTokens)
	maxTok := req.MaxTokens
	if maxTok <= 0 {
		maxTok = 8192
	}
	if budget > 0 && maxTok <= budget {
		maxTok = budget + 4096
	}
	// Merge into strictly alternating user/assistant turns.
	type turn struct {
		role   string
		blocks []map[string]any
	}
	var turns []turn
	add := func(role string, blocks ...map[string]any) {
		if len(blocks) == 0 {
			return
		}
		if n := len(turns); n > 0 && turns[n-1].role == role {
			turns[n-1].blocks = append(turns[n-1].blocks, blocks...)
			return
		}
		turns = append(turns, turn{role: role, blocks: blocks})
	}
	for _, m := range req.Messages {
		switch m.Role {
		case "assistant":
			var blocks []map[string]any
			if budget > 0 && m.Reasoning != "" && m.ReasoningSig != "" {
				blocks = append(blocks, map[string]any{"type": "thinking", "thinking": m.Reasoning, "signature": m.ReasoningSig})
			}
			if strings.TrimSpace(m.Content) != "" {
				blocks = append(blocks, map[string]any{"type": "text", "text": m.Content})
			}
			for _, tc := range m.ToolCalls {
				var in any = map[string]any{}
				json.Unmarshal(argsOrEmpty(tc.Args), &in)
				blocks = append(blocks, map[string]any{"type": "tool_use", "id": tc.ID, "name": tc.Name, "input": in})
			}
			if len(blocks) == 0 {
				blocks = append(blocks, map[string]any{"type": "text", "text": "(no output)"})
			}
			add("assistant", blocks...)
		case "tool":
			content := m.Content
			if content == "" {
				content = "(empty)"
			}
			add("user", map[string]any{"type": "tool_result", "tool_use_id": m.ToolCallID, "content": content})
		default:
			var blocks []map[string]any
			for _, im := range m.Images {
				blocks = append(blocks, map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": im.MIME, "data": im.Data}})
			}
			text := m.Content
			if text == "" {
				text = "(empty)"
			}
			blocks = append(blocks, map[string]any{"type": "text", "text": text})
			add("user", blocks...)
		}
	}
	if len(turns) > 0 && turns[0].role != "user" {
		turns = append([]turn{{role: "user", blocks: []map[string]any{{"type": "text", "text": "(continue)"}}}}, turns...)
	}
	// Cache the conversation prefix: mark the last block of the second-to-last user turn.
	for i := len(turns) - 2; i >= 0; i-- {
		if turns[i].role == "user" {
			b := turns[i].blocks[len(turns[i].blocks)-1]
			b["cache_control"] = map[string]any{"type": "ephemeral"}
			break
		}
	}
	var msgs []map[string]any
	for _, t := range turns {
		msgs = append(msgs, map[string]any{"role": t.role, "content": t.blocks})
	}
	body := map[string]any{"model": req.Model, "max_tokens": maxTok, "messages": msgs, "stream": true}
	if req.System != "" {
		body["system"] = []map[string]any{{"type": "text", "text": req.System, "cache_control": map[string]any{"type": "ephemeral"}}}
	}
	if budget > 0 {
		body["thinking"] = map[string]any{"type": "enabled", "budget_tokens": budget}
	} else {
		body["temperature"] = req.Temperature
	}
	if len(req.Tools) > 0 {
		var tools []map[string]any
		for i, t := range req.Tools {
			td := map[string]any{"name": t.Name, "description": t.Description, "input_schema": t.Schema}
			if i == len(req.Tools)-1 {
				td["cache_control"] = map[string]any{"type": "ephemeral"}
			}
			tools = append(tools, td)
		}
		body["tools"] = tools
	}
	h := map[string]string{"anthropic-version": "2023-06-01"}
	for k, v := range c.headers {
		h[k] = v
	}
	if c.key != "" {
		h["x-api-key"] = c.key
	}
	// Subscription credentials use OAuth bearer auth, never the API-key header.
	if strings.HasPrefix(h["Authorization"], "Bearer ") {
		delete(h, "x-api-key")
	}
	base := c.base
	if !strings.HasSuffix(base, "/v1") {
		base += "/v1"
	}
	resp, err := postJSON(ctx, c.hc, base+"/messages", body, h)
	if err != nil {
		return nil, err
	}
	out := &Response{}
	type block struct {
		kind, id, name string
		buf            strings.Builder
	}
	blocks := map[int]*block{}
	var order []int
	var text, reasoning strings.Builder
	err = readSSE(ctx, resp.Body, c.idle, func(event, data string) error {
		var ev struct {
			Type         string `json:"type"`
			Index        int    `json:"index"`
			ContentBlock struct {
				Type string `json:"type"`
				ID   string `json:"id"`
				Name string `json:"name"`
				Text string `json:"text"`
			} `json:"content_block"`
			Delta struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				PartialJSON string `json:"partial_json"`
				Thinking    string `json:"thinking"`
				Signature   string `json:"signature"`
				StopReason  string `json:"stop_reason"`
			} `json:"delta"`
			Message struct {
				Usage struct {
					Input      int64 `json:"input_tokens"`
					CacheRead  int64 `json:"cache_read_input_tokens"`
					CacheWrite int64 `json:"cache_creation_input_tokens"`
				} `json:"usage"`
			} `json:"message"`
			Usage struct {
				Output int64 `json:"output_tokens"`
			} `json:"usage"`
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			return nil
		}
		switch ev.Type {
		case "error":
			return fmt.Errorf("anthropic: %s", ev.Error.Message)
		case "message_start":
			u := ev.Message.Usage
			out.Usage.Input, out.Usage.CacheRead, out.Usage.CacheWrite = u.Input+u.CacheRead+u.CacheWrite, u.CacheRead, u.CacheWrite
		case "content_block_start":
			b := &block{kind: ev.ContentBlock.Type, id: ev.ContentBlock.ID, name: ev.ContentBlock.Name}
			blocks[ev.Index] = b
			order = append(order, ev.Index)
		case "content_block_delta":
			b := blocks[ev.Index]
			if b == nil {
				return nil
			}
			switch ev.Delta.Type {
			case "text_delta":
				text.WriteString(ev.Delta.Text)
				if onDelta != nil {
					onDelta(Delta{Text: ev.Delta.Text})
				}
			case "thinking_delta":
				reasoning.WriteString(ev.Delta.Thinking)
				if onDelta != nil {
					onDelta(Delta{Reasoning: ev.Delta.Thinking})
				}
			case "signature_delta":
				out.ReasoningSig += ev.Delta.Signature
			case "input_json_delta":
				b.buf.WriteString(ev.Delta.PartialJSON)
			}
		case "message_delta":
			if ev.Delta.StopReason != "" {
				out.StopReason = ev.Delta.StopReason
			}
			out.Usage.Output = ev.Usage.Output
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	out.Text, out.Reasoning = text.String(), reasoning.String()
	for _, i := range order {
		b := blocks[i]
		if b.kind == "tool_use" {
			out.ToolCalls = append(out.ToolCalls, ToolCall{ID: b.id, Name: b.name, Args: argsOrEmpty(json.RawMessage(b.buf.String()))})
		}
	}
	return out, nil
}
