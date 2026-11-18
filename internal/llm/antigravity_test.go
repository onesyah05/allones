package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/allones/allones/internal/config"
)

func TestAntigravityWire(t *testing.T) {
	var session, trajectory string
	streams, loads, onboards := 0, 0, 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer token" || !strings.Contains(r.Header.Get("User-Agent"), "antigravity/hub/") {
			t.Error("auth headers")
		}
		var b map[string]any
		if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
			t.Error(err)
		}
		switch r.URL.Path {
		case "/v1internal:loadCodeAssist":
			loads++
			fmt.Fprint(w, `{"allowedTiers":[{"id":"free","isDefault":true}]}`)
		case "/v1internal:onboardUser":
			onboards++
			if b["tierId"] != "free" {
				t.Error(b)
			}
			fmt.Fprint(w, `{"done":true,"response":{"cloudaicompanionProject":{"id":"project"}}}`)
		case "/v1internal:streamGenerateContent":
			streams++
			if r.URL.Query().Get("alt") != "sse" || b["project"] != "project" || b["model"] != "gemini-test" || b["requestType"] != "agent" {
				t.Error(b)
			}
			p := b["request"].(map[string]any)
			labels := p["labels"].(map[string]any)
			if streams == 1 {
				session = p["sessionId"].(string)
				trajectory = labels["trajectory_id"].(string)
			} else if session != p["sessionId"] || trajectory != labels["trajectory_id"] {
				t.Error("unstable session")
			}
			if labels["last_step_index"] != fmt.Sprint(streams-1) {
				t.Error(labels)
			}
			wire, _ := json.Marshal(p)
			for _, want := range []string{"thoughtSignature", "old-signature", "skip_thought_signature_validator", "inlineData", "functionResponse", "systemInstruction"} {
				if !strings.Contains(string(wire), want) {
					t.Errorf("missing %s", want)
				}
			}
			if strings.Contains(string(wire), "additionalProperties") || strings.Contains(string(wire), "$schema") {
				t.Error("unclean schema")
			}
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: {\"response\":{\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"thought\",\"thought\":true,\"thoughtSignature\":\"reason-sig\"},{\"text\":\"answer\"},{\"functionCall\":{\"name\":\"lookup\",\"args\":{\"x\":1}},\"thoughtSignature\":\"tool-sig\"}]},\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":12,\"candidatesTokenCount\":3,\"thoughtsTokenCount\":2,\"cachedContentTokenCount\":4}}}\n\n")
		default:
			t.Errorf("unexpected %s", r.URL)
			w.WriteHeader(404)
		}
	}))
	defer s.Close()
	c := newAntigravity(&config.Provider{APIKey: "token", BaseURL: s.URL}, s.Client(), time.Second)
	req := Request{Model: "gemini-test", System: "system", Messages: []Message{{Role: "user", Content: "hello", Images: []Image{{MIME: "image/png", Data: "YQ=="}}}, {Role: "assistant", ToolCalls: []ToolCall{{ID: "one", Name: "lookup", Args: json.RawMessage(`{}`), Sig: "old-signature"}, {ID: "two", Name: "lookup"}}}, {Role: "tool", ToolCallID: "one", Content: "result"}}, Tools: []ToolDef{{Name: "lookup", Schema: map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{"x": map[string]any{"type": "integer", "$schema": "ignored"}}}}}}
	for i := 0; i < 2; i++ {
		var text, reason string
		out, err := c.Stream(context.Background(), req, func(d Delta) { text += d.Text; reason += d.Reasoning })
		if err != nil {
			t.Fatal(err)
		}
		if out.Text != "answer" || text != "answer" || reason != "thought" || out.ReasoningSig != "reason-sig" || out.Usage.Output != 5 || out.Usage.CacheRead != 4 || out.StopReason != "tool_calls" || len(out.ToolCalls) != 1 || out.ToolCalls[0].Sig != "tool-sig" || out.ToolCalls[0].ID == "" {
			t.Fatalf("response %#v", out)
		}
	}
	if loads != 1 || onboards != 1 {
		t.Fatalf("discovery %d/%d", loads, onboards)
	}
}

func TestAntigravityErrors(t *testing.T) {
	for _, data := range []string{`{"error":{"code":403,"message":"denied"}}`, `{"response":{"error":{"message":"denied"}}}`, `{"response":{"promptFeedback":{"blockReason":"SAFETY"}}}`, `{"response":{"candidates":[{"content":{"parts":[{"text":"partial"}]}}]}}`, `bad-json`} {
		t.Run(data, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1internal:streamGenerateContent" {
					t.Error("project override ignored")
				}
				fmt.Fprintf(w, "data: %s\n\n", data)
			}))
			defer s.Close()
			c := newAntigravity(&config.Provider{BaseURL: s.URL, Headers: map[string]string{"X-Goog-Project-Id": "explicit"}}, s.Client(), time.Second)
			if _, err := c.Stream(context.Background(), Request{}, nil); err == nil {
				t.Fatal("expected failure")
			}
		})
	}
}

func TestAntigravityModels(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1internal:loadCodeAssist":
			fmt.Fprint(w, `{"cloudaicompanionProject":"project"}`)
		case "/v1internal:fetchAvailableModels":
			fmt.Fprint(w, `{"models":{"z":{},"a":{}}}`)
		default:
			t.Error(r.URL)
			w.WriteHeader(404)
		}
	}))
	defer s.Close()
	c := newAntigravity(&config.Provider{BaseURL: s.URL}, s.Client(), time.Second).(*antigravity)
	models, err := c.listModels(context.Background())
	if err != nil || strings.Join(models, ",") != "a,z" {
		t.Fatalf("%v %v", models, err)
	}
}
