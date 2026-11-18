package agent

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/allones/allones/internal/config"
	"github.com/allones/allones/internal/llm"
	"github.com/allones/allones/internal/store"
)

type titleClient struct {
	text   string
	err    error
	calls  int
	before func()
}

func (c *titleClient) Stream(_ context.Context, req llm.Request, _ func(llm.Delta)) (*llm.Response, error) {
	c.calls++
	if req.Model != "selected-model" {
		return nil, errors.New("wrong selected model")
	}
	if c.before != nil {
		c.before()
	}
	return &llm.Response{Text: c.text}, c.err
}

func titleEngine(t *testing.T) *Engine {
	t.Helper()
	t.Setenv("ALLONES_HOME", t.TempDir())
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	// Environment auto-detection must not supply a global fallback in this test.
	if err := cfg.Update(func(c *config.Config) error {
		c.Model.Default = ""
		c.Model.Aux = ""
		c.Provider = nil
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(t.TempDir(), "titles.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return &Engine{cfg: cfg, store: st}
}

func TestTitleSessionSelectedClient(t *testing.T) {
	for _, aux := range []string{"", "broken/model"} {
		t.Run("aux="+aux, func(t *testing.T) {
			e := titleEngine(t)
			if err := e.cfg.Update(func(c *config.Config) error { c.Model.Aux = aux; return nil }); err != nil {
				t.Fatal(err)
			}
			s := &store.Session{Title: "New chat"}
			if err := e.store.CreateSession(s); err != nil {
				t.Fatal(err)
			}
			client := &titleClient{text: "  \"Selected Chat Title.\" \n"}
			var events []Event
			e.titleSession(s.ID, "Help fix session naming", client, "selected-model", func(ev Event) { events = append(events, ev) })
			got, err := e.store.GetSession(s.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.Title != "Selected Chat Title" || client.calls != 1 {
				t.Fatalf("title=%q calls=%d", got.Title, client.calls)
			}
			if len(events) != 1 || events[0].Type != "session_title" || events[0].SessionID != s.ID || events[0].Text != got.Title {
				t.Fatalf("events=%+v", events)
			}
		})
	}
}

func TestTitleSessionFallback(t *testing.T) {
	for _, tc := range []struct {
		name, text, input, want string
		err                     error
	}{
		{name: "provider error", input: "  Fix   automatic naming  ", want: "Fix automatic naming", err: errors.New("offline")},
		{name: "empty response", input: "Explain naming", want: "Explain naming"},
		{name: "image only", want: "Image conversation"},
		{name: "placeholder response", text: "New chat", input: "Name this conversation", want: "Name this conversation"},
		{name: "unicode", text: strings.Repeat("界", 100), want: strings.Repeat("界", 80)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := titleEngine(t)
			s := &store.Session{}
			if err := e.store.CreateSession(s); err != nil {
				t.Fatal(err)
			}
			e.titleSession(s.ID, tc.input, &titleClient{text: tc.text, err: tc.err}, "selected-model", func(Event) {})
			got, err := e.store.GetSession(s.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.Title != tc.want || !utf8.ValidString(got.Title) {
				t.Fatalf("title=%q want=%q", got.Title, tc.want)
			}
		})
	}
}

func TestTitleSessionPreservesConcurrentChanges(t *testing.T) {
	for _, rename := range []bool{false, true} {
		t.Run(map[bool]string{false: "usage", true: "manual rename"}[rename], func(t *testing.T) {
			e := titleEngine(t)
			s := &store.Session{Title: "New chat"}
			if err := e.store.CreateSession(s); err != nil {
				t.Fatal(err)
			}
			client := &titleClient{text: "Generated Title", before: func() {
				e.store.AddUsage(s.ID, 123, 456)
				if rename {
					current, err := e.store.GetSession(s.ID)
					if err != nil {
						t.Fatal(err)
					}
					current.Title = "My manual title"
					if err := e.store.UpdateSession(current); err != nil {
						t.Fatal(err)
					}
				}
			}}
			count := 0
			e.titleSession(s.ID, "hello", client, "selected-model", func(Event) { count++ })
			got, err := e.store.GetSession(s.ID)
			if err != nil {
				t.Fatal(err)
			}
			want := "Generated Title"
			wantEvents := 1
			if rename {
				want = "My manual title"
				wantEvents = 0
			}
			if got.Title != want || got.TokensIn != 123 || got.TokensOut != 456 || count != wantEvents {
				t.Fatalf("session=%+v events=%d", got, count)
			}
		})
	}
}
