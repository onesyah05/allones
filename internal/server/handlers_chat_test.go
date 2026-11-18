package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/allones/allones/internal/agent"
)

func TestStreamChatParallelEvents(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/chat", nil)
	streamChatEvents(w, r, func(ctx context.Context, emit agent.Emitter) {
		var wg sync.WaitGroup
		for i := 0; i < 20; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := 0; j < 100; j++ {
					emit(agent.Event{Type: "tool_end", Text: "parallel"})
				}
			}()
		}
		wg.Wait()
		emit(agent.Event{Type: "done"})
	})
	lines := strings.Split(strings.TrimSpace(w.Body.String()), "\n\n")
	if len(lines) != 2001 {
		t.Fatalf("lost events: got %d", len(lines))
	}
	for _, line := range lines {
		var e agent.Event
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &e); err != nil {
			t.Fatal(err)
		}
	}
	if !strings.Contains(lines[len(lines)-1], `"done"`) {
		t.Fatal("missing terminal event")
	}
}

func TestStreamChatCancellationLateEmit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := httptest.NewRequest("POST", "/api/chat", nil).WithContext(ctx)
	w := httptest.NewRecorder()
	release := make(chan struct{})
	finished := make(chan struct{})
	streamChatEvents(w, r, func(ctx context.Context, emit agent.Emitter) {
		emit(agent.Event{Type: "start"})
		cancel()
		<-release
		emit(agent.Event{Type: "tool_end"})
		close(finished)
	})
	before := w.Body.String()
	close(release)
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("late emitter blocked")
	}
	if w.Body.String() != before {
		t.Fatal("write after handler returned")
	}
}
