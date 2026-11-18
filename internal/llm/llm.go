// Package llm adapts model providers to one streaming interface.
package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/allones/allones/internal/config"
)

type Message struct {
	Role         string     `json:"role"` // system | user | assistant | tool
	Content      string     `json:"content"`
	Images       []Image    `json:"images,omitempty"`
	ToolCalls    []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID   string     `json:"tool_call_id,omitempty"`
	Name         string     `json:"name,omitempty"`
	Reasoning    string     `json:"reasoning,omitempty"`
	ReasoningSig string     `json:"reasoning_sig,omitempty"`
}

type Image struct {
	MIME string `json:"mime"`
	Data string `json:"data"` // base64
}

type ToolCall struct {
	ID   string          `json:"id"`
	Name string          `json:"name"`
	Args json.RawMessage `json:"args"`
	Sig  string          `json:"sig,omitempty"` // provider signature (Gemini thoughtSignature)
}

type ToolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Schema      map[string]any `json:"schema"`
}

type Request struct {
	Model       string
	System      string
	Messages    []Message
	Tools       []ToolDef
	MaxTokens   int
	Temperature float64
	Reasoning   string // off | low | medium | high
	JSON        bool
}

type Usage struct {
	Input      int64 `json:"input"`
	Output     int64 `json:"output"`
	CacheRead  int64 `json:"cache_read"`
	CacheWrite int64 `json:"cache_write"`
}

type Response struct {
	Text         string
	Reasoning    string
	ReasoningSig string
	ToolCalls    []ToolCall
	Usage        Usage
	StopReason   string
}

// Delta is streamed while the response is produced.
type Delta struct {
	Text      string
	Reasoning string
}

type Client interface {
	Stream(ctx context.Context, req Request, onDelta func(Delta)) (*Response, error)
}

// New builds a client for a configured provider.
func New(p *config.Provider) (Client, error) {
	timeout := time.Duration(p.Timeout) * time.Second
	if timeout <= 0 {
		timeout = 180 * time.Second
	}
	hc := &http.Client{Timeout: 0} // streaming: inactivity is enforced per read
	base := strings.TrimRight(p.BaseURL, "/")
	if base == "" {
		base = config.DefaultBaseURL(p.Kind, p.Name)
	}
	switch p.Kind {
	case "codex":
		return newCodex(p, hc, timeout), nil
	case "antigravity":
		return newAntigravity(p, hc, timeout), nil
	case "claude-code":
		h := map[string]string{}
		for k, v := range p.Headers {
			h[k] = v
		}
		h["Authorization"] = "Bearer " + p.APIKey
		h["anthropic-beta"] = "oauth-2025-04-20"
		h["anthropic-dangerous-direct-browser-access"] = "true"
		h["User-Agent"] = "claude-cli/2.0.0 (external, cli)"
		h["x-app"] = "cli"
		h["Accept"] = "text/event-stream"
		return &anthropic{base: base, headers: h, hc: hc, idle: timeout}, nil
	case "anthropic":
		return &anthropic{base: base, key: p.APIKey, headers: p.Headers, hc: hc, idle: timeout}, nil
	case "gemini":
		return &gemini{base: base, key: p.APIKey, headers: p.Headers, hc: hc, idle: timeout}, nil
	case "openai", "openai-compatible", "custom", "":
		if base == "" {
			return nil, fmt.Errorf("provider %s has no base_url", p.Name)
		}
		return &openAI{name: p.Name, base: base, key: p.APIKey, headers: p.Headers, hc: hc, idle: timeout}, nil
	}
	return nil, fmt.Errorf("unknown provider kind %q", p.Kind)
}

// Resolve finds a client and bare model id for a "provider/model" reference.
func Resolve(cfg *config.Config, ref string) (Client, string, error) {
	p, model, err := cfg.FindProvider(ref)
	if err != nil {
		return nil, "", err
	}
	c, err := New(p)
	return c, model, err
}

// Complete is a non-streaming convenience wrapper.
func Complete(ctx context.Context, c Client, req Request) (string, error) {
	r, err := c.Stream(ctx, req, nil)
	if err != nil {
		return "", err
	}
	return r.Text, nil
}

// ---------- shared HTTP + SSE plumbing ----------

type APIError struct {
	Status int
	Body   string
}

func (e *APIError) Error() string {
	b := e.Body
	if len(b) > 600 {
		b = b[:600] + "…"
	}
	return fmt.Sprintf("provider returned HTTP %d: %s", e.Status, b)
}

// Retryable reports transient failures worth another attempt.
func Retryable(err error) bool {
	var ae *APIError
	if errors.As(err, &ae) {
		return ae.Status == 429 || ae.Status == 408 || ae.Status >= 500
	}
	return false
}

func postJSON(ctx context.Context, hc *http.Client, url string, body any, headers map[string]string) (*http.Response, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	var resp *http.Response
	for attempt := 0; attempt < 3; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "allones/1.0")
		for k, v := range headers {
			if v != "" {
				req.Header.Set(k, v)
			}
		}
		resp, err = hc.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if attempt < 2 {
				time.Sleep(time.Duration(attempt+1) * time.Second)
				continue
			}
			return nil, err
		}
		if resp.StatusCode/100 == 2 {
			return resp, nil
		}
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		resp.Body.Close()
		aerr := &APIError{Status: resp.StatusCode, Body: string(data)}
		if !Retryable(aerr) || attempt == 2 {
			return nil, aerr
		}
		wait := time.Duration(2<<attempt) * time.Second
		if ra := resp.Header.Get("Retry-After"); ra != "" {
			var s int
			if _, err := fmt.Sscan(ra, &s); err == nil && s > 0 && s < 60 {
				wait = time.Duration(s) * time.Second
			}
		}
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return nil, errors.New("request failed")
}

// readSSE calls fn for each data payload; idle bounds silence between events.
func readSSE(ctx context.Context, body io.ReadCloser, idle time.Duration, fn func(event, data string) error) error {
	defer body.Close()
	lines := make(chan string)
	errc := make(chan error, 1)
	go func() {
		sc := bufio.NewScanner(body)
		sc.Buffer(make([]byte, 64<<10), 16<<20)
		for sc.Scan() {
			select {
			case lines <- sc.Text():
			case <-ctx.Done():
				return
			}
		}
		errc <- sc.Err()
		close(lines)
	}()
	timer := time.NewTimer(idle)
	defer timer.Stop()
	var event string
	var data strings.Builder
	flush := func() error {
		if data.Len() == 0 {
			event = ""
			return nil
		}
		d := data.String()
		data.Reset()
		ev := event
		event = ""
		if d == "[DONE]" {
			return io.EOF
		}
		return fn(ev, d)
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return fmt.Errorf("stream idle for %s", idle)
		case line, ok := <-lines:
			if !ok {
				if err := flush(); err != nil && err != io.EOF {
					return err
				}
				return <-errc
			}
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(idle)
			switch {
			case line == "":
				if err := flush(); err != nil {
					if err == io.EOF {
						return nil
					}
					return err
				}
			case strings.HasPrefix(line, "event:"):
				event = strings.TrimSpace(line[6:])
			case strings.HasPrefix(line, "data:"):
				if data.Len() > 0 {
					data.WriteByte('\n')
				}
				data.WriteString(strings.TrimPrefix(line[5:], " "))
			}
		}
	}
}

func budgetFor(effort string, max int) int {
	switch effort {
	case "low":
		return 2048
	case "medium":
		return 8192
	case "high":
		if max > 24000 {
			return 24000
		}
		return 16000
	}
	return 0
}

// EstimateTokens is a cheap heuristic (~4 chars/token).
func EstimateTokens(msgs []Message, system string) int {
	n := len(system)
	for _, m := range msgs {
		n += len(m.Content) + len(m.Reasoning) + 16
		for _, tc := range m.ToolCalls {
			n += len(tc.Args) + len(tc.Name)
		}
		n += len(m.Images) * 4000
	}
	return n / 4
}

func argsOrEmpty(a json.RawMessage) json.RawMessage {
	if len(bytes.TrimSpace(a)) == 0 || !json.Valid(a) {
		return json.RawMessage(`{}`)
	}
	return a
}
