// Package server hosts the HTTP API and the embedded dashboard.
package server

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/allones/allones/internal/agent"
	"github.com/allones/allones/internal/config"
	"github.com/allones/allones/internal/mcp"
	"github.com/allones/allones/internal/scheduler"
	"github.com/allones/allones/internal/secure"
	"github.com/allones/allones/internal/store"
	"github.com/allones/allones/internal/subscription"
)

type Server struct {
	cfg           *config.Manager
	store         *store.Store
	engine        *agent.Engine
	mcp           *mcp.Manager
	sched         *scheduler.Scheduler
	vault         *secure.Vault
	subscriptions *subscription.Manager

	mu        sync.Mutex
	approvals map[string]chan approvalReply
	questions map[string]chan string
	notifSubs map[chan Notification]struct{}
}

type Notification struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	TS    int64  `json:"ts"`
	Level string `json:"level"`
}

type approvalReply struct{ approved bool }

func New(cfg *config.Manager, st *store.Store, eng *agent.Engine, mc *mcp.Manager, sched *scheduler.Scheduler, vault *secure.Vault) *Server {
	s := &Server{
		cfg: cfg, store: st, engine: eng, mcp: mc, sched: sched, vault: vault,
		subscriptions: subscription.New(cfg),
		approvals:     map[string]chan approvalReply{},
		questions:     map[string]chan string{},
		notifSubs:     map[chan Notification]struct{}{},
	}
	eng.SetApprover(s)
	eng.SetAsker(s)
	eng.Notify = s.pushNotification
	return s
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	s.routes(mux)
	return s.recover(s.cors(mux))
}

func (s *Server) recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("panic handling %s %s: %v", r.Method, r.URL.Path, rec)
				http.Error(w, "internal error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (s *Server) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		allowed := s.cfg.Get().Server.AllowOrigins
		if origin != "" && (len(allowed) == 0 && isLocal(origin) || contains(allowed, origin) || contains(allowed, "*")) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ---------- JSON helpers ----------

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func readJSON(r *http.Request, v any) error {
	defer r.Body.Close()
	return json.NewDecoder(http.MaxBytesReader(nil, r.Body, 8<<20)).Decode(v)
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

func isLocal(origin string) bool {
	return strings.Contains(origin, "localhost") || strings.Contains(origin, "127.0.0.1") || strings.Contains(origin, "[::1]")
}

// ---------- approvals / questions ----------

func (s *Server) Approve(ctx context.Context, sessionID, tool, title, detail string) bool {
	id := store.NewID("ap_")
	ch := make(chan approvalReply, 1)
	s.mu.Lock()
	s.approvals[id] = ch
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.approvals, id)
		s.mu.Unlock()
	}()
	s.engine.Emit(sessionID, agent.Event{Type: "approval_request", CallID: id, Tool: tool, Text: title, Result: detail, SessionID: sessionID, TS: time.Now().UnixMilli()})
	select {
	case <-ctx.Done():
		return false
	case <-time.After(5 * time.Minute):
		return false
	case r := <-ch:
		return r.approved
	}
}

func (s *Server) Ask(ctx context.Context, sessionID, question string, options []string) (string, error) {
	id := store.NewID("q_")
	ch := make(chan string, 1)
	s.mu.Lock()
	s.questions[id] = ch
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.questions, id)
		s.mu.Unlock()
	}()
	meta := map[string]any{"options": options, "question_id": id}
	s.engine.Emit(sessionID, agent.Event{Type: "question", CallID: id, Text: question, Meta: meta, SessionID: sessionID, TS: time.Now().UnixMilli()})
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-time.After(10 * time.Minute):
		return "", context.DeadlineExceeded
	case ans := <-ch:
		return ans, nil
	}
}

func (s *Server) resolveApproval(id string, approved bool) bool {
	s.mu.Lock()
	ch := s.approvals[id]
	s.mu.Unlock()
	if ch == nil {
		return false
	}
	ch <- approvalReply{approved}
	return true
}

func (s *Server) resolveQuestion(id, answer string) bool {
	s.mu.Lock()
	ch := s.questions[id]
	s.mu.Unlock()
	if ch == nil {
		return false
	}
	ch <- answer
	return true
}

func (s *Server) pushNotification(title, body string) {
	n := Notification{Title: title, Body: body, TS: time.Now().UnixMilli(), Level: "info"}
	s.mu.Lock()
	for ch := range s.notifSubs {
		select {
		case ch <- n:
		default:
		}
	}
	s.mu.Unlock()
}
