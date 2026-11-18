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

func TestCodexWire(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" || r.Header.Get("ChatGPT-Account-Id") != "account" || r.Header.Get("version") != codexVersion {
			t.Error("missing subscription headers")
		}
		if r.URL.Path != "/responses" {
			t.Errorf("path %s", r.URL.Path)
		}
		var b map[string]any
		if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
			t.Error(err)
		}
		if b["instructions"] != "system\n\nextra" || b["store"] != false || b["stream"] != true {
			t.Errorf("body %#v", b)
		}
		if _, ok := b["max_output_tokens"]; ok {
			t.Error("unsupported token limit")
		}
		input := b["input"].([]any)
		if len(input) != 5 {
			t.Errorf("input %#v", input)
		}
		wire, _ := json.Marshal(b)
		for _, want := range []string{"input_image", "data:image/png;base64,YQ==", "function_call_output", "output_text", "encrypted_content"} {
			if !strings.Contains(string(wire), want) {
				t.Errorf("missing %s", want)
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, event := range []string{
			`{"type":"response.output_text.delta","delta":"hello"}`,
			`{"type":"response.reasoning_summary_text.delta","delta":"think"}`,
			`{"type":"response.output_item.added","item":{"type":"function_call","id":"item1","call_id":"call1","name":"lookup","arguments":""}}`,
			`{"type":"response.output_item.done","item":{"type":"function_call","id":"item1","call_id":"call1","name":"lookup","arguments":"{\"x\":1}"}}`,
			`{"type":"response.completed","response":{"status":"completed","output":[{"type":"function_call","id":"item1","call_id":"call1","name":"lookup","arguments":"{\"x\":1}"},{"type":"reasoning","encrypted_content":"new-sig"}],"usage":{"input_tokens":10,"output_tokens":4,"input_tokens_details":{"cached_tokens":2}}}}`,
		} {
			fmt.Fprintf(w, "data: %s\n\n", event)
		}
	}))
	defer server.Close()
	c := newCodex(&config.Provider{APIKey: "test-token", BaseURL: server.URL, Headers: map[string]string{"ChatGPT-Account-Id": "account"}}, server.Client(), time.Second)
	var text, reason string
	out, err := c.Stream(context.Background(), Request{Model: "test", System: "system", MaxTokens: 100, Messages: []Message{{Role: "system", Content: "extra"}, {Role: "user", Content: "hi", Images: []Image{{MIME: "image/png", Data: "YQ=="}}}, {Role: "assistant", Content: "old", ReasoningSig: "sig", ToolCalls: []ToolCall{{ID: "oldcall", Name: "lookup", Args: json.RawMessage(`{}`)}}}, {Role: "tool", ToolCallID: "oldcall", Content: "result"}}}, func(d Delta) { text += d.Text; reason += d.Reasoning })
	if err != nil {
		t.Fatal(err)
	}
	if text != "hello" || reason != "think" || out.ReasoningSig != "new-sig" || out.StopReason != "tool_calls" || out.Usage.CacheRead != 2 || len(out.ToolCalls) != 1 || string(out.ToolCalls[0].Args) != `{"x":1}` || out.ToolCalls[0].ID != "call1" {
		t.Fatalf("response %#v", out)
	}
}

func TestCodexErrors(t *testing.T) {
	for _, data := range []string{`{"type":"response.failed","response":{"error":{"message":"denied"}}}`, `{"type":"response.output_text.delta","delta":"partial"}`, `not-json`} {
		t.Run(data, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprintf(w, "data: %s\n\n", data) }))
			defer s.Close()
			c := newCodex(&config.Provider{BaseURL: s.URL}, s.Client(), time.Second)
			if _, err := c.Stream(context.Background(), Request{}, nil); err == nil {
				t.Fatal("expected stream error")
			}
		})
	}
}

func TestCodexModels(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" || r.URL.Query().Get("client_version") != codexVersion {
			t.Error(r.URL)
		}
		fmt.Fprint(w, `{"models":[{"slug":"later"},{"slug":"hidden","visibility":"hide"},{"slug":"first","priority":1}]}`)
	}))
	defer s.Close()
	c := newCodex(&config.Provider{BaseURL: s.URL}, s.Client(), time.Second).(*codex)
	models, err := c.listModels(context.Background())
	if err != nil || strings.Join(models, ",") != "first,later" {
		t.Fatalf("%v %v", models, err)
	}
}
