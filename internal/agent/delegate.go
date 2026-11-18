package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/allones/allones/internal/store"
	"github.com/allones/allones/internal/tools"
)

// Delegate runs a sub-agent on a standalone prompt and returns its final answer.
// It implements tools.Delegator.
func (e *Engine) Delegate(ctx context.Context, parent *tools.Env, role, prompt string, isolate bool) (string, error) {
	if strings.TrimSpace(prompt) == "" {
		return "", fmt.Errorf("delegate_task needs a prompt")
	}
	if role == "" {
		role = "assistant"
	}
	if _, ok := e.roles.Get(role); !ok {
		return "", fmt.Errorf("no role %q (use list_roles)", role)
	}
	// Create an isolated child session.
	child := &store.Session{
		Role: role, Source: "subagent", EngagementID: parentEngagement(e, parent.SessionID),
		Title: "sub: " + clip(prompt, 50),
	}
	if err := e.store.CreateSession(child); err != nil {
		return "", err
	}

	var answer strings.Builder
	var lastErr string
	req := Request{
		SessionID: child.ID, Text: prompt, Role: role, Source: "subagent",
		Interactive: false, AutoApprove: parent.AutoApprove, Depth: parent.Depth + 1,
	}
	emit := func(ev Event) {
		switch ev.Type {
		case "text":
			answer.WriteString(ev.Text)
		case "error":
			lastErr = ev.Text
		case "tool_start":
			if parent.Progress != nil {
				parent.Progress(fmt.Sprintf("sub-agent[%s]: %s", role, ev.Tool))
			}
		}
	}
	if err := e.Run(ctx, req, emit); err != nil {
		return answer.String(), err
	}
	out := strings.TrimSpace(answer.String())
	if out == "" {
		if lastErr != "" {
			return "", fmt.Errorf("sub-agent produced no answer: %s", lastErr)
		}
		return "(the sub-agent finished without a textual answer)", nil
	}
	return out, nil
}

func parentEngagement(e *Engine, sessionID string) string {
	if s, err := e.store.GetSession(sessionID); err == nil {
		return s.EngagementID
	}
	return ""
}
