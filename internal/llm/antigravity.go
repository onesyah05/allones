package llm

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/allones/allones/internal/config"
)

type antigravity struct {
	base                                string
	headers                             map[string]string
	hc                                  *http.Client
	idle                                time.Duration
	mu                                  sync.Mutex
	project, agent, trajectory, session string
	step                                uint64
}

func antigravityID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}

func newAntigravity(p *config.Provider, hc *http.Client, idle time.Duration) Client {
	base := strings.TrimRight(p.BaseURL, "/")
	if base == "" {
		base = "https://daily-cloudcode-pa.googleapis.com"
	}
	h := map[string]string{"User-Agent": "antigravity/hub/2.29.0 (aidev_client; os_type=windows; arch=amd64; cl=963137146)", "Accept": "text/event-stream"}
	project := ""
	for k, v := range p.Headers {
		h[k] = v
		if strings.EqualFold(k, "X-Goog-Project-Id") {
			project = v
		}
	}
	h["Authorization"] = "Bearer " + p.APIKey
	if hc == nil {
		hc = http.DefaultClient
	}
	return &antigravity{base: base, headers: h, hc: hc, idle: idle, project: project, agent: antigravityID(), trajectory: antigravityID(), session: antigravityID()}
}

var antigravityMetadata = map[string]any{"ideType": "ANTIGRAVITY", "platform": "PLATFORM_UNSPECIFIED", "pluginType": "GEMINI"}

func (c *antigravity) json(ctx context.Context, method string, body any, out any) error {
	resp, err := postJSON(ctx, c.hc, c.base+"/v1internal:"+method, body, c.headers)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(out)
}

func antigravityProjectID(v json.RawMessage) string {
	var s string
	if json.Unmarshal(v, &s) == nil {
		return s
	}
	var o struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(v, &o)
	return o.ID
}

// Caller holds mu so concurrent first requests cannot onboard the same client twice.
func (c *antigravity) discoverProject(ctx context.Context) error {
	if c.project != "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	var loaded struct {
		Project json.RawMessage `json:"cloudaicompanionProject"`
		Tiers   []struct {
			ID      string `json:"id"`
			Default bool   `json:"isDefault"`
		} `json:"allowedTiers"`
	}
	if err := c.json(ctx, "loadCodeAssist", map[string]any{"metadata": antigravityMetadata}, &loaded); err != nil {
		return err
	}
	c.project = antigravityProjectID(loaded.Project)
	if c.project != "" {
		return nil
	}
	tier := "free-tier"
	for _, t := range loaded.Tiers {
		if t.Default {
			tier = t.ID
			break
		}
	}
	var onboard struct {
		Response struct {
			Project json.RawMessage `json:"cloudaicompanionProject"`
		} `json:"response"`
	}
	if err := c.json(ctx, "onboardUser", map[string]any{"tierId": tier, "metadata": antigravityMetadata}, &onboard); err != nil {
		return err
	}
	c.project = antigravityProjectID(onboard.Response.Project)
	if c.project == "" {
		return fmt.Errorf("antigravity: no Cloud Code project; finish account setup in Antigravity")
	}
	return nil
}

func (c *antigravity) Stream(ctx context.Context, req Request, onDelta func(Delta)) (*Response, error) {
	c.mu.Lock()
	err := c.discoverProject(ctx)
	if err != nil {
		c.mu.Unlock()
		return nil, err
	}
	c.step++
	step := c.step
	project := c.project
	c.mu.Unlock()
	contents := []map[string]any{}
	systems := []string{}
	if req.System != "" {
		systems = append(systems, req.System)
	}
	names := map[string]string{}
	add := func(role string, parts []any) {
		if len(parts) == 0 {
			return
		}
		if len(contents) > 0 && contents[len(contents)-1]["role"] == role {
			last := contents[len(contents)-1]
			last["parts"] = append(last["parts"].([]any), parts...)
		} else {
			contents = append(contents, map[string]any{"role": role, "parts": parts})
		}
	}
	for _, m := range req.Messages {
		if m.Role == "system" {
			systems = append(systems, m.Content)
			continue
		}
		if m.Role == "tool" {
			name := m.Name
			if name == "" {
				name = names[m.ToolCallID]
			}
			if name == "" {
				name = "tool"
			}
			add("user", []any{map[string]any{"functionResponse": map[string]any{"id": m.ToolCallID, "name": name, "response": map[string]any{"result": m.Content}}}})
			continue
		}
		parts := []any{}
		if m.Role == "assistant" && m.Reasoning != "" && m.ReasoningSig != "" {
			parts = append(parts, map[string]any{"text": m.Reasoning, "thought": true, "thoughtSignature": m.ReasoningSig})
		}
		if m.Content != "" {
			parts = append(parts, map[string]any{"text": m.Content})
		}
		for _, im := range m.Images {
			parts = append(parts, map[string]any{"inlineData": map[string]any{"mimeType": im.MIME, "data": im.Data}})
		}
		for _, tc := range m.ToolCalls {
			names[tc.ID] = tc.Name
			var args any
			_ = json.Unmarshal(argsOrEmpty(tc.Args), &args)
			sig := tc.Sig
			if sig == "" {
				sig = "skip_thought_signature_validator"
			}
			parts = append(parts, map[string]any{"functionCall": map[string]any{"id": tc.ID, "name": tc.Name, "args": args}, "thoughtSignature": sig})
		}
		role := "user"
		if m.Role == "assistant" {
			role = "model"
		}
		add(role, parts)
	}
	generation := map[string]any{"temperature": req.Temperature}
	if req.MaxTokens > 0 {
		generation["maxOutputTokens"] = req.MaxTokens
	}
	if req.JSON {
		generation["responseMimeType"] = "application/json"
	}
	if req.Reasoning != "" {
		budget := budgetFor(req.Reasoning, req.MaxTokens)
		generation["thinkingConfig"] = map[string]any{"thinkingBudget": budget, "includeThoughts": budget > 0}
	}
	payload := map[string]any{"contents": contents, "generationConfig": generation, "sessionId": c.session, "labels": map[string]any{"trajectory_id": c.trajectory, "last_step_index": fmt.Sprint(step - 1)}}
	if len(systems) > 0 {
		payload["systemInstruction"] = map[string]any{"role": "user", "parts": []any{map[string]any{"text": strings.Join(systems, "\n\n")}}}
	}
	if len(req.Tools) > 0 {
		decls := []any{}
		for _, t := range req.Tools {
			decls = append(decls, map[string]any{"name": t.Name, "description": t.Description, "parameters": cleanSchema(t.Schema)})
		}
		payload["tools"] = []any{map[string]any{"functionDeclarations": decls}}
	}
	envelope := map[string]any{"requestId": fmt.Sprintf("agent/%s/%d/%s/%d", c.agent, time.Now().UnixMilli(), c.trajectory, step), "model": req.Model, "userAgent": "antigravity", "requestType": "agent", "project": project, "request": payload}
	resp, err := postJSON(ctx, c.hc, c.base+"/v1internal:streamGenerateContent?alt=sse", envelope, c.headers)
	if err != nil {
		return nil, err
	}
	out := &Response{}
	finished := false
	err = readSSE(ctx, resp.Body, c.idle, func(_, data string) error {
		if data == "[DONE]" {
			return nil
		}
		var wrapper struct {
			Response json.RawMessage `json:"response"`
			Error    json.RawMessage `json:"error"`
		}
		if err := json.Unmarshal([]byte(data), &wrapper); err != nil {
			return fmt.Errorf("antigravity stream: %w", err)
		}
		if len(wrapper.Error) > 0 && string(wrapper.Error) != "null" {
			return fmt.Errorf("antigravity stream error: %s", wrapper.Error)
		}
		raw := []byte(data)
		if len(wrapper.Response) > 0 {
			raw = wrapper.Response
		}
		var v struct {
			Error      json.RawMessage `json:"error"`
			Candidates []struct {
				Content struct {
					Parts []struct {
						Text    string `json:"text"`
						Thought bool   `json:"thought"`
						Sig     string `json:"thoughtSignature"`
						Call    *struct {
							ID   string          `json:"id"`
							Name string          `json:"name"`
							Args json.RawMessage `json:"args"`
						} `json:"functionCall"`
					} `json:"parts"`
				} `json:"content"`
				Finish string `json:"finishReason"`
			} `json:"candidates"`
			Feedback struct {
				Reason string `json:"blockReason"`
			} `json:"promptFeedback"`
			Usage struct {
				Input    int64 `json:"promptTokenCount"`
				Output   int64 `json:"candidatesTokenCount"`
				Thoughts int64 `json:"thoughtsTokenCount"`
				Cached   int64 `json:"cachedContentTokenCount"`
			} `json:"usageMetadata"`
		}
		if err := json.Unmarshal(raw, &v); err != nil {
			return err
		}
		if len(v.Error) > 0 && string(v.Error) != "null" {
			return fmt.Errorf("antigravity stream error: %s", v.Error)
		}
		if v.Feedback.Reason != "" {
			return fmt.Errorf("antigravity blocked prompt: %s", v.Feedback.Reason)
		}
		if v.Usage.Input != 0 || v.Usage.Output != 0 || v.Usage.Thoughts != 0 {
			out.Usage = Usage{Input: v.Usage.Input, Output: v.Usage.Output + v.Usage.Thoughts, CacheRead: v.Usage.Cached}
		}
		if len(v.Candidates) == 0 {
			return nil
		}
		candidate := v.Candidates[0]
		for _, p := range candidate.Content.Parts {
			if p.Call != nil {
				id := p.Call.ID
				if id == "" {
					id = "call_" + antigravityID()
				}
				out.ToolCalls = append(out.ToolCalls, ToolCall{ID: id, Name: p.Call.Name, Args: argsOrEmpty(p.Call.Args), Sig: p.Sig})
				continue
			}
			d := Delta{}
			if p.Thought {
				out.Reasoning += p.Text
				d.Reasoning = p.Text
				if p.Sig != "" {
					out.ReasoningSig = p.Sig
				}
			} else {
				out.Text += p.Text
				d.Text = p.Text
			}
			if onDelta != nil && (d.Text != "" || d.Reasoning != "") {
				onDelta(d)
			}
		}
		if candidate.Finish != "" {
			finished = true
			switch candidate.Finish {
			case "STOP":
				out.StopReason = "stop"
				if len(out.ToolCalls) > 0 {
					out.StopReason = "tool_calls"
				}
			case "MAX_TOKENS":
				out.StopReason = "length"
			default:
				return fmt.Errorf("antigravity finish: %s", candidate.Finish)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if !finished {
		return nil, fmt.Errorf("antigravity: stream ended before candidate completion")
	}
	return out, nil
}

func (c *antigravity) listModels(ctx context.Context) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	c.mu.Lock()
	err := c.discoverProject(ctx)
	project := c.project
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}
	var v struct {
		Models map[string]json.RawMessage `json:"models"`
	}
	if err := c.json(ctx, "fetchAvailableModels", map[string]any{"project": project}, &v); err != nil {
		return nil, err
	}
	models := []string{}
	for id := range v.Models {
		models = append(models, id)
	}
	sort.Strings(models)
	return models, nil
}
