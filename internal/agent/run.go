package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/allones/allones/internal/config"
	"github.com/allones/allones/internal/llm"
	"github.com/allones/allones/internal/store"
	"github.com/allones/allones/internal/tools"
)

// Run executes one turn and streams events. It owns the terminal "done" event.
func (e *Engine) Run(ctx context.Context, req Request, emit Emitter) error {
	caller := emit
	if caller == nil {
		caller = func(Event) {}
	}
	var titleDone chan Event
	// Every event also goes to session subscribers (other tabs, side channels).
	emit = func(ev Event) {
		// The dashboard refreshes sessions when the turn stream closes. Persist
		// and deliver the title first, even when the chat response is faster.
		if ev.Type == "done" && titleDone != nil {
			if title, ok := <-titleDone; ok {
				caller(title)
				e.bcast.publish(title.SessionID, title)
			}
			titleDone = nil
		}
		if ev.SessionID == "" {
			ev.SessionID = req.SessionID
		}
		caller(ev)
		e.bcast.publish(ev.SessionID, ev)
	}
	e.sem <- struct{}{}
	defer func() { <-e.sem }()

	sess, err := e.ensureSession(&req)
	if err != nil {
		emit(Event{Type: "error", Text: err.Error(), TS: now()})
		emit(Event{Type: "done", TS: now()})
		return err
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	e.mu.Lock()
	if _, busy := e.running[sess.ID]; busy && req.Depth == 0 {
		e.mu.Unlock()
		err := fmt.Errorf("a turn is already running in this session")
		emit(Event{Type: "error", Text: err.Error(), TS: now()})
		emit(Event{Type: "done", TS: now()})
		return err
	}
	e.running[sess.ID] = cancel
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		delete(e.running, sess.ID)
		delete(e.steers, sess.ID)
		e.mu.Unlock()
	}()

	emit(Event{Type: "start", SessionID: sess.ID, TS: now()})

	c := e.cfg.Get()
	role := e.resolveRole(firstNonEmpty(req.Role, sess.Role))
	model := firstNonEmpty(req.Model, sess.Model, role.Model, c.Model.Default)
	client, modelID, err := e.resolveClient(ctx, model)
	if err != nil {
		emit(Event{Type: "error", Text: err.Error(), TS: now()})
		emit(Event{Type: "done", TS: now()})
		return err
	}
	activeTools := e.selectTools(c, role, firstNonEmpty(req.Toolset, sess.Toolset), req.Source)
	env := e.buildEnv(sess, req, role)

	// Persist the user message.
	if strings.TrimSpace(req.Text) != "" || len(req.Images) > 0 {
		um := &store.Message{SessionID: sess.ID, Role: "user", Content: req.Text}
		if len(req.Images) > 0 {
			// Keep the images as data URLs so the saved transcript can show them.
			urls := make([]string, 0, len(req.Images))
			for _, im := range req.Images {
				urls = append(urls, "data:"+im.MIME+";base64,"+im.Data)
			}
			meta, _ := json.Marshal(map[string]any{"images": urls})
			um.Meta = meta
		}
		e.store.AppendMessage(um)
		if sess.Title == "" || sess.Title == "New chat" {
			titleDone = make(chan Event, 1)
			go func(done chan Event) {
				defer close(done)
				e.titleSession(sess.ID, req.Text, client, modelID, func(ev Event) { done <- ev })
			}(titleDone)
		}
	}

	history, err := e.loadHistory(sess.ID, req.Images)
	if err != nil {
		emit(Event{Type: "error", Text: err.Error(), TS: now()})
		emit(Event{Type: "done", TS: now()})
		return err
	}

	toolMap := map[string]*tools.Tool{}
	for _, t := range activeTools {
		toolMap[t.Name] = t
	}

	rg := &repeatGuard{limit: c.Agent.RepeatLimit}
	verifyLeft := 0
	if c.Agent.VerifyReplies {
		verifyLeft = c.Agent.VerifyMax
	}
	totalCalls := 0
	var totalUsage llm.Usage

	sys := e.systemPrompt(c, sess, role, req, activeTools)

	for turn := 0; turn < c.Agent.MaxTurns; turn++ {
		if ctx.Err() != nil {
			break
		}
		// Compact if the history is getting close to the window.
		history = e.maybeCompact(ctx, sess, c, sys, history, emit)

		lr := llm.Request{
			Model: modelID, System: sys, Messages: history, Tools: toolDefs2(activeTools),
			MaxTokens: c.Model.MaxTokens, Temperature: c.Agent.Temperature, Reasoning: firstNonEmpty(role.Reasoning, c.Model.Reasoning),
		}
		resp, err := e.stream(ctx, client, lr, sess.ID, emit)
		if err != nil {
			if ctx.Err() != nil {
				emit(Event{Type: "notice", Text: "stopped.", TS: now()})
				break
			}
			emit(Event{Type: "error", Text: "model error: " + err.Error(), TS: now()})
			break
		}
		totalUsage.Input += resp.Usage.Input
		totalUsage.Output += resp.Usage.Output
		e.store.AddUsage(sess.ID, resp.Usage.Input, resp.Usage.Output)

		// Persist the assistant message.
		am := &store.Message{SessionID: sess.ID, Role: "assistant", Content: resp.Text, Reasoning: resp.Reasoning, ReasoningSig: resp.ReasoningSig}
		if len(resp.ToolCalls) > 0 {
			tc, _ := json.Marshal(resp.ToolCalls)
			am.ToolCalls = tc
		}
		e.store.AppendMessage(am)
		history = append(history, toLLM(am))

		if len(resp.ToolCalls) == 0 {
			// Model is finished. Steering?
			if steers := e.takeSteers(sess.ID); len(steers) > 0 {
				history = e.injectSteer(sess, history, steers, emit)
				continue
			}
			// Verification?
			if verifyLeft > 0 {
				if gap := e.verify(ctx, c, sess, req, history, emit); gap != "" {
					verifyLeft--
					m := &store.Message{SessionID: sess.ID, Role: "user", Content: "Before finishing: " + gap + " Do that now, or explain why it cannot be done."}
					e.store.AppendMessage(m)
					history = append(history, toLLM(m))
					emit(Event{Type: "notice", Text: "verification: sending the turn back — " + gap, TS: now()})
					continue
				}
			}
			// Goal judge?
			if sess.Goal != "" && req.Depth == 0 {
				if next := e.judgeGoal(ctx, c, sess, history, emit); next != "" {
					m := &store.Message{SessionID: sess.ID, Role: "user", Content: "The goal is not yet met. Next concrete step: " + next}
					e.store.AppendMessage(m)
					history = append(history, toLLM(m))
					continue
				}
			}
			break
		}

		// Execute tool calls (parallel), honoring the per-turn budget.
		results, stop := e.dispatch(ctx, env, toolMap, resp.ToolCalls, rg, &totalCalls, c, emit)
		for _, r := range results {
			e.store.AppendMessage(r)
			history = append(history, toLLM(r))
		}
		if stop != "" {
			m := &store.Message{SessionID: sess.ID, Role: "user", Content: stop}
			e.store.AppendMessage(m)
			history = append(history, toLLM(m))
			emit(Event{Type: "notice", Text: stop, TS: now()})
			// Let the model make one wrap-up turn, then it will stop.
		}
		// Steering between batches.
		if steers := e.takeSteers(sess.ID); len(steers) > 0 {
			history = e.injectSteer(sess, history, steers, emit)
		}
	}

	emit(Event{Type: "usage", Usage: &totalUsage, TS: now()})
	emit(Event{Type: "done", SessionID: sess.ID, TS: now()})
	if req.Source == "subagent" {
		e.store.RecordRole(role.Name, ctx.Err() == nil, totalCalls)
	}
	return nil
}

// stream runs one model call, emitting reasoning/text deltas.
func (e *Engine) stream(ctx context.Context, client llm.Client, req llm.Request, sessionID string, emit Emitter) (*llm.Response, error) {
	var started bool
	resp, err := client.Stream(ctx, req, func(d llm.Delta) {
		if d.Reasoning != "" {
			emit(Event{Type: "reasoning", Text: d.Reasoning, SessionID: sessionID, TS: now()})
		}
		if d.Text != "" {
			if !started {
				started = true
			}
			emit(Event{Type: "text", Text: d.Text, SessionID: sessionID, TS: now()})
		}
	})
	return resp, err
}

// dispatch runs a batch of tool calls in parallel and returns tool-result messages.
func (e *Engine) dispatch(ctx context.Context, env *tools.Env, toolMap map[string]*tools.Tool, calls []llm.ToolCall, rg *repeatGuard, totalCalls *int, c *config.Config, emit Emitter) ([]*store.Message, string) {
	type outcome struct {
		idx int
		msg *store.Message
	}
	parallel := c.Agent.ParallelTools
	if parallel <= 0 {
		parallel = 4
	}
	sem := make(chan struct{}, parallel)
	results := make([]*store.Message, len(calls))
	var wg sync.WaitGroup
	var mu sync.Mutex
	stop := ""

	for i, call := range calls {
		*totalCalls++
		if *totalCalls > c.Agent.MaxToolCalls {
			results[i] = toolResult(env.SessionID, call, tools.Errorf("tool-call limit (%d) reached for this run; summarise what you have and finish", c.Agent.MaxToolCalls), c)
			stop = "You have reached the tool-call limit for this run. Summarise what you accomplished and stop."
			continue
		}
		t := toolMap[call.Name]
		if t == nil {
			results[i] = toolResult(env.SessionID, call, tools.Errorf("no tool named %q is available this turn", call.Name), c)
			continue
		}
		// Repeat guard
		if nudge, abort := rg.check(call); abort {
			results[i] = toolResult(env.SessionID, call, tools.Errorf("aborted: %s has been called the same way too many times. Stop and explain what is blocking you.", call.Name), c)
			stop = "You are repeating the same failing call. Stop and tell the operator what is blocking you."
			continue
		} else if nudge != "" {
			emit(Event{Type: "notice", Text: nudge, TS: now()})
		}

		emit(Event{Type: "tool_start", Tool: call.Name, CallID: call.ID, Args: call.Args, SessionID: env.SessionID, TS: now()})
		// Approval for mutating tools.
		if t.Mutates && needsApproval(c, env) {
			if !e.Approve(ctx, env, t.Name, "Run "+t.Name+"?", string(call.Args)) {
				results[i] = toolResult(env.SessionID, call, tools.Errorf("the operator declined to run %s. Choose another approach.", t.Name), c)
				emit(Event{Type: "tool_end", Tool: call.Name, CallID: call.ID, Result: "declined by operator", IsError: true, TS: now()})
				continue
			}
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, call llm.ToolCall, t *tools.Tool) {
			defer wg.Done()
			defer func() { <-sem }()
			defer func() {
				if r := recover(); r != nil {
					mu.Lock()
					results[i] = toolResult(env.SessionID, call, tools.Errorf("tool panicked: %v", r), c)
					mu.Unlock()
					emit(Event{Type: "tool_end", Tool: call.Name, CallID: call.ID, Result: "panic", IsError: true, TS: now()})
				}
			}()
			callEnv := *env
			callEnv.Progress = func(s string) {
				emit(Event{Type: "tool_progress", Tool: call.Name, CallID: call.ID, Text: s, TS: now()})
			}
			res := t.Run(ctx, &callEnv, call.Args)
			msg := toolResult(env.SessionID, call, res, c)
			mu.Lock()
			results[i] = msg
			mu.Unlock()
			preview := res.Text
			if len(preview) > 600 {
				preview = preview[:600] + "…"
			}
			emit(Event{Type: "tool_end", Tool: call.Name, CallID: call.ID, Result: preview, IsError: res.IsError, Meta: res.Meta, TS: now()})
		}(i, call, t)
	}
	wg.Wait()
	_ = outcome{}
	return results, stop
}

func needsApproval(c *config.Config, env *tools.Env) bool {
	if env.AutoApprove {
		return false
	}
	switch c.Tools.ApprovalMode {
	case "auto":
		return false
	case "deny":
		return true
	}
	return env.Interactive
}

// toolResult wraps a tool Result into a stored tool message, fencing untrusted output.
func toolResult(sessionID string, call llm.ToolCall, res tools.Result, c *config.Config) *store.Message {
	text := res.Text
	if c.Security.RedactSecrets {
		if s, n := redact(text); n > 0 {
			text = s
		}
	}
	if res.Untrusted != "" {
		text = fence(res.Untrusted, text)
	}
	m := &store.Message{SessionID: sessionID, Role: "tool", ToolCallID: call.ID, Name: call.Name, Content: text}
	if res.IsError {
		meta, _ := json.Marshal(map[string]any{"error": true})
		m.Meta = meta
	}
	return m
}

func toolDefs2(ts []*tools.Tool) []llm.ToolDef {
	out := make([]llm.ToolDef, len(ts))
	for i, t := range ts {
		out[i] = llm.ToolDef{Name: t.Name, Description: t.Description, Schema: t.Schema}
	}
	return out
}

var _ = time.Now
