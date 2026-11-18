package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/allones/allones/internal/config"
	"github.com/allones/allones/internal/roles"
	"github.com/allones/allones/internal/skills"
	"github.com/allones/allones/internal/store"
	"github.com/allones/allones/internal/tools"
)

func TestRunTitleBeforeDone(t *testing.T) {
	for _, selection := range []string{"request", "session", "role"} {
		t.Run(selection, func(t *testing.T) {
			e := titleEngine(t)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					Model    string `json:"model"`
					Messages []struct {
						Content string `json:"content"`
					} `json:"messages"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					http.Error(w, err.Error(), 400)
					return
				}
				if body.Model != "selected-model" {
					http.Error(w, "wrong model", 400)
					return
				}
				text := "Chat response"
				if len(body.Messages) > 0 && len(body.Messages[0].Content) > 0 && body.Messages[0].Content[:min(4, len(body.Messages[0].Content))] == "Give" {
					// Force the naming request to finish after the chat response.
					time.Sleep(50 * time.Millisecond)
					text = "Selected Provider Title"
				}
				w.Header().Set("Content-Type", "text/event-stream")
				payload, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": text}}}})
				fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", payload)
			}))
			defer server.Close()
			if err := e.cfg.Update(func(c *config.Config) error {
				c.Provider = []config.Provider{{Name: "selected", Kind: "openai-compatible", BaseURL: server.URL}}
				c.Model.Default = ""
				c.Agent.MaxTurns = 1
				c.Agent.VerifyReplies = false
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			e.roles = roles.Load("")
			if selection == "role" {
				r, _ := e.roles.Get("assistant")
				r.Model = "selected/selected-model"
			}
			e.registry = tools.NewRegistry()
			var err error
			e.skills, err = skills.New(nil, nil, false, t.TempDir(), t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			e.sem = make(chan struct{}, 1)
			e.running = map[string]context.CancelFunc{}
			e.steers = map[string][]string{}
			e.bcast = newBroadcaster()
			s := &store.Session{Title: "New chat"}
			if selection == "session" {
				s.Model = "selected/selected-model"
			}
			if err := e.store.CreateSession(s); err != nil {
				t.Fatal(err)
			}
			sub := e.Subscribe(s.ID)
			defer e.Unsubscribe(s.ID, sub)
			req := Request{SessionID: s.ID, Text: "Help fix naming", Source: "web"}
			if selection == "request" {
				req.Model = "selected/selected-model"
			}
			var events []Event
			if err := e.Run(context.Background(), req, func(ev Event) {
				events = append(events, ev)
				if ev.Type == "done" {
					got, err := e.store.GetSession(s.ID)
					if err != nil {
						t.Fatal(err)
					}
					if got.Title != "Selected Provider Title" {
						t.Errorf("title at done=%q", got.Title)
					}
				}
			}); err != nil {
				t.Fatal(err)
			}
			titleIndex, doneIndex := -1, -1
			for i, ev := range events {
				if ev.Type == "error" {
					t.Errorf("event error: %s", ev.Text)
				}
				if ev.Type == "session_title" {
					titleIndex = i
				}
				if ev.Type == "done" {
					doneIndex = i
				}
			}
			if titleIndex < 0 || doneIndex <= titleIndex {
				t.Fatalf("title=%d done=%d events=%+v", titleIndex, doneIndex, events)
			}
			found := false
			for len(sub) > 0 {
				if ev := <-sub; ev.Type == "session_title" && ev.Text == "Selected Provider Title" {
					found = true
				}
			}
			if !found {
				t.Fatal("subscriber did not receive persisted title")
			}
		})
	}
}
