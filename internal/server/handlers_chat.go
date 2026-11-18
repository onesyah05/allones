package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/allones/allones/internal/agent"
	"github.com/allones/allones/internal/llm"
)

type chatRequest struct {
	SessionID string      `json:"session_id"`
	Text      string      `json:"text"`
	Role      string      `json:"role"`
	Model     string      `json:"model"`
	Toolset   string      `json:"toolset"`
	Workspace string      `json:"workspace"`
	Images    []llm.Image `json:"images"`
}

// handleChat starts a turn and streams Server-Sent Events.
func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	var req chatRequest
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	streamChatEvents(w, r, func(ctx context.Context, send agent.Emitter) {
		runChatWithSideEvents(ctx, s.engine, send, func(emit agent.Emitter) {
			s.engine.Run(ctx, agent.Request{
				SessionID: req.SessionID, Text: req.Text, Images: req.Images,
				Role: req.Role, Model: req.Model, Toolset: req.Toolset, Workspace: req.Workspace,
				Source: "web", Interactive: true,
			}, emit)
		})
	})
}

// Subscribe synchronously on start: Run has assigned even a new session's ID,
// but cannot reach a tool that asks for input until this callback returns.
// Ordinary events already use the caller stream and must not be forwarded twice.
func runChatWithSideEvents(ctx context.Context, engine *agent.Engine, send agent.Emitter, run func(agent.Emitter)) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var stopped chan struct{}
	defer func() {
		cancel()
		if stopped != nil {
			<-stopped
		}
	}()
	run(func(ev agent.Event) {
		if ev.Type == "start" && ev.SessionID != "" && stopped == nil {
			sessionID := ev.SessionID
			events := engine.Subscribe(sessionID)
			stopped = make(chan struct{})
			go func() {
				defer close(stopped)
				defer engine.Unsubscribe(sessionID, events)
				for {
					select {
					case <-ctx.Done():
						return
					case side, ok := <-events:
						if !ok {
							return
						}
						if side.Type == "question" || side.Type == "approval_request" {
							send(side)
						}
					}
				}
			}()
		}
		send(ev)
	})
}

// Only the handler goroutine may touch ResponseWriter. Parallel tools can emit
// concurrently, and cancellation must never leave them writing after return.
func streamChatEvents(w http.ResponseWriter, r *http.Request, run func(context.Context, agent.Emitter)) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	events := make(chan agent.Event)
	go func() {
		defer close(events)
		run(ctx, func(ev agent.Event) {
			select {
			case events <- ev:
			case <-ctx.Done():
			}
		})
	}()

	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				return
			}
			b, _ := json.Marshal(ev)
			if _, err := fmt.Fprintf(w, "data: %s\n\n", b); err != nil {
				return
			}
			flusher.Flush()
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := fmt.Fprintf(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// handleChatStream subscribes to a session's live events (a second tab watching).
func (s *Server) handleChatStream(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("session_id")
	if sessionID == "" {
		writeErr(w, http.StatusBadRequest, "session_id required")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	ch := s.engine.Subscribe(sessionID)
	defer s.engine.Unsubscribe(sessionID, ch)
	ctx := r.Context()
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			b, _ := json.Marshal(ev)
			fmt.Fprintf(w, "data: %s\n\n", b)
			flusher.Flush()
		case <-ticker.C:
			fmt.Fprintf(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}

func (s *Server) handleStop(w http.ResponseWriter, r *http.Request) {
	var b struct {
		SessionID string `json:"session_id"`
	}
	readJSON(r, &b)
	writeJSON(w, http.StatusOK, map[string]bool{"stopped": s.engine.Stop(b.SessionID)})
}

func (s *Server) handleSteer(w http.ResponseWriter, r *http.Request) {
	var b struct {
		SessionID string `json:"session_id"`
		Note      string `json:"note"`
	}
	readJSON(r, &b)
	writeJSON(w, http.StatusOK, map[string]bool{"delivered": s.engine.Steer(b.SessionID, b.Note)})
}

func (s *Server) handleApprove(w http.ResponseWriter, r *http.Request) {
	var b struct {
		ID       string `json:"id"`
		Approved bool   `json:"approved"`
	}
	readJSON(r, &b)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": s.resolveApproval(b.ID, b.Approved)})
}

func (s *Server) handleAnswer(w http.ResponseWriter, r *http.Request) {
	var b struct {
		ID     string `json:"id"`
		Answer string `json:"answer"`
	}
	readJSON(r, &b)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": s.resolveQuestion(b.ID, b.Answer)})
}

func sessionToken(passwordHash string) string {
	sum := sha256.Sum256([]byte("allones-session:" + passwordHash))
	return hex.EncodeToString(sum[:])
}
