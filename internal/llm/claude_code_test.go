package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/allones/allones/internal/config"
)

func TestClaudeCodeSubscription(t *testing.T) {
	var calls int
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/v1/messages" {
			t.Errorf("path %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer oauth-token" || r.Header.Get("X-Api-Key") != "" {
			t.Error("expected OAuth bearer only")
		}
		if r.Header.Get("Anthropic-Beta") != "oauth-2025-04-20" {
			t.Error("missing OAuth beta")
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body["model"] != "claude-sonnet-4-6" {
			t.Error("model mapping")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":9}}}\n\n")
		fmt.Fprint(w, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n")
		fmt.Fprint(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"ready\"}}\n\n")
		fmt.Fprint(w, "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\n")
		fmt.Fprint(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	}))
	defer s.Close()
	client, err := New(&config.Provider{Name: "claude-code", Kind: "claude-code", APIKey: "oauth-token", BaseURL: s.URL})
	if err != nil {
		t.Fatal(err)
	}
	var streamed string
	out, err := client.Stream(context.Background(), Request{Model: "claude-sonnet-4-6", Messages: []Message{{Role: "user", Content: "hello"}}}, func(d Delta) { streamed += d.Text })
	if err != nil {
		t.Fatal(err)
	}
	if out.Text != "ready" || streamed != "ready" || calls != 1 {
		t.Fatalf("unexpected output %#v %s", out, streamed)
	}
}
