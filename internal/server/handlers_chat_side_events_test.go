package server

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/allones/allones/internal/agent"
	"github.com/allones/allones/internal/config"
	"github.com/allones/allones/internal/roles"
	"github.com/allones/allones/internal/skills"
	"github.com/allones/allones/internal/store"
	"github.com/allones/allones/internal/tools"
)

// Exercise the real Run -> tool -> Server.Ask/Approve -> Emit path, not a
// synthetic question sent through the ordinary turn callback.
func TestHandleChatSideEventsHTTP(t *testing.T) {
	for _, kind := range []string{"question", "approval_request"} {
		for _, existing := range []bool{false, true} {
			for _, disconnect := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/existing=%t/disconnect=%t", kind, existing, disconnect), func(t *testing.T) {
					home := t.TempDir()
					t.Setenv("ALLONES_HOME", home)
					tool := "ask_user"
					args := `{"question":"Choose?","options":["yes","no"]}`
					if kind == "approval_request" {
						tool, args = "test_mutation", `{}`
					}
					model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						var req struct {
							Messages []struct {
								Role string `json:"role"`
							} `json:"messages"`
						}
						if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
							t.Error(err)
							return
						}
						w.Header().Set("Content-Type", "text/event-stream")
						for _, msg := range req.Messages {
							if msg.Role == "tool" {
								fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Finished\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
								return
							}
						}
						chunk := map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "call_1", "type": "function", "function": map[string]any{"name": tool, "arguments": args}}}}, "finish_reason": "tool_calls"}}}
						b, _ := json.Marshal(chunk)
						fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", b)
					}))
					defer model.Close()
					cfg, err := config.Load()
					if err != nil {
						t.Fatal(err)
					}
					if err := cfg.Update(func(c *config.Config) error {
						c.Provider = []config.Provider{{Name: "test", Kind: "openai-compatible", BaseURL: model.URL}}
						c.Model.Default = "test/model"
						c.Agent.VerifyReplies = false
						c.Tools.ApprovalMode = "prompt"
						c.Tools.Enabled = []string{tool}
						c.Tools.Disabled = nil
						return nil
					}); err != nil {
						t.Fatal(err)
					}
					st, err := store.Open(filepath.Join(home, "test.db"))
					if err != nil {
						t.Fatal(err)
					}
					defer st.Close()
					reg := tools.NewRegistry()
					tools.Register(reg)
					reg.Add(&tools.Tool{Name: "test_mutation", Category: "agent", Mutates: true, Schema: tools.Obj(map[string]any{}), Run: func(context.Context, *tools.Env, json.RawMessage) tools.Result { return tools.Text("approved") }})
					sk, err := skills.New(nil, nil, false, home, home)
					if err != nil {
						t.Fatal(err)
					}
					eng := agent.New(cfg, st, reg, sk, roles.Load(""), nil, nil)
					s := New(cfg, st, eng, nil, nil, nil)
					sessionID := ""
					if existing {
						sess := &store.Session{Title: "Existing"}
						if err := st.CreateSession(sess); err != nil {
							t.Fatal(err)
						}
						sessionID = sess.ID
					}
					finished := make(chan struct{})
					api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						defer close(finished)
						s.handleChat(w, r)
					}))
					defer api.Close()
					ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
					defer cancel()
					body := fmt.Sprintf(`{"session_id":%q,"workspace":%q}`, sessionID, home)
					req, err := http.NewRequestWithContext(ctx, "POST", api.URL+"/api/chat", strings.NewReader(body))
					if err != nil {
						t.Fatal(err)
					}
					resp, err := api.Client().Do(req)
					if err != nil {
						t.Fatal(err)
					}
					defer resp.Body.Close()
					if resp.StatusCode != http.StatusOK {
						t.Fatalf("HTTP %s", resp.Status)
					}
					counts := map[string]int{}
					scanner := bufio.NewScanner(resp.Body)
					for scanner.Scan() {
						line := scanner.Text()
						if !strings.HasPrefix(line, "data: ") {
							continue
						}
						var ev agent.Event
						if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &ev); err != nil {
							t.Fatal(err)
						}
						counts[ev.Type]++
						if ev.Type == "error" {
							t.Fatalf("turn failed: %s", ev.Text)
						}
						if ev.Type == "start" {
							if ev.SessionID == "" {
								t.Fatal("missing new session ID")
							}
							if sessionID != "" && sessionID != ev.SessionID {
								t.Fatal("session changed")
							}
							sessionID = ev.SessionID
						}
						if ev.Type == kind {
							if ev.SessionID != sessionID || ev.CallID == "" {
								t.Fatalf("invalid side event: %+v", ev)
							}
							if disconnect {
								cancel()
								break
							}
							if kind == "question" {
								if ev.Text != "Choose?" || ev.Meta["question_id"] != ev.CallID {
									t.Fatalf("invalid question: %+v", ev)
								}
								if !s.resolveQuestion(ev.CallID, "yes") {
									t.Fatal("question not pending")
								}
							} else if !s.resolveApproval(ev.CallID, true) {
								t.Fatal("approval not pending")
							}
						}
					}
					if !disconnect && scanner.Err() != nil {
						t.Fatal(scanner.Err())
					}
					if counts[kind] != 1 || counts["start"] != 1 || counts["tool_start"] != 1 {
						t.Fatalf("lost or duplicate events: %v", counts)
					}
					if !disconnect && (counts["done"] != 1 || counts["tool_end"] != 1) {
						t.Fatalf("lost or duplicate terminal events: %v", counts)
					}
					select {
					case <-finished:
					case <-time.After(2 * time.Second):
						t.Fatal("chat handler leaked")
					}
					deadline := time.Now().Add(2 * time.Second)
					for {
						s.mu.Lock()
						pending := len(s.questions) + len(s.approvals)
						s.mu.Unlock()
						if pending == 0 && !eng.IsRunning(sessionID) {
							break
						}
						if time.Now().After(deadline) {
							t.Fatal("turn or pending input leaked after stream ended")
						}
						time.Sleep(time.Millisecond)
					}
				})
			}
		}
	}
}
