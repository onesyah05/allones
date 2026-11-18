package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/allones/allones/internal/config"
	"github.com/allones/allones/internal/llm"
	"github.com/allones/allones/internal/secure"
	"github.com/allones/allones/internal/store"
)

// ---------- sessions & history ----------

func (e *Engine) ensureSession(req *Request) (*store.Session, error) {
	if req.SessionID != "" {
		s, err := e.store.GetSession(req.SessionID)
		if err == nil {
			if req.Role != "" {
				s.Role = req.Role
			}
			if req.Model != "" {
				s.Model = req.Model
			}
			if req.Workspace != "" && req.Workspace != s.Workspace {
				s.Workspace = req.Workspace
				e.store.UpdateSession(s)
			}
			return s, nil
		}
	}
	s := &store.Session{ID: req.SessionID, Role: req.Role, Model: req.Model, Toolset: req.Toolset, Workspace: req.Workspace, Source: firstNonEmpty(req.Source, "web")}
	if err := e.store.CreateSession(s); err != nil {
		return nil, err
	}
	req.SessionID = s.ID
	return s, nil
}

func (e *Engine) loadHistory(sessionID string, pendingImages []llm.Image) ([]llm.Message, error) {
	msgs, err := e.store.Messages(sessionID, false)
	if err != nil {
		return nil, err
	}
	var out []llm.Message
	for _, m := range msgs {
		if m.Role == "notice" {
			continue
		}
		out = append(out, toLLM(&m))
	}
	// Attach pending images to the last user message.
	if len(pendingImages) > 0 {
		for i := len(out) - 1; i >= 0; i-- {
			if out[i].Role == "user" {
				out[i].Images = pendingImages
				break
			}
		}
	}
	return out, nil
}

func toLLM(m *store.Message) llm.Message {
	msg := llm.Message{Role: m.Role, Content: m.Content, ToolCallID: m.ToolCallID, Name: m.Name, Reasoning: m.Reasoning, ReasoningSig: m.ReasoningSig}
	if len(m.ToolCalls) > 0 {
		json.Unmarshal(m.ToolCalls, &msg.ToolCalls)
	}
	if m.Role == "notice" {
		msg.Role = "user"
	}
	return msg
}

// ---------- compaction ----------

func (e *Engine) maybeCompact(ctx context.Context, sess *store.Session, c *config.Config, sys string, history []llm.Message, emit Emitter) []llm.Message {
	est := llm.EstimateTokens(history, sys)
	threshold := int(float64(c.Agent.ContextWindow) * c.Agent.CompactAt)
	if est < threshold || len(history) < 8 {
		return history
	}
	// Keep the last third verbatim; summarise the earlier two-thirds.
	keep := len(history) / 3
	if keep < 4 {
		keep = 4
	}
	// Do not split a tool call from its result: walk back to a clean boundary.
	cut := len(history) - keep
	for cut > 0 && history[cut].Role == "tool" {
		cut--
	}
	if cut < 2 {
		return history
	}
	older := history[:cut]
	summary := e.summarise(ctx, c, older, sess.Summary)
	if summary == "" {
		return history
	}
	sess.Summary = summary
	e.store.UpdateSession(sess)
	emit(Event{Type: "notice", Text: fmt.Sprintf("context compacted (~%d tokens → summary of %d earlier messages)", est, cut), TS: now()})
	// Mark compacted messages hidden in storage by id.
	stored, _ := e.store.Messages(sess.ID, false)
	if cut-1 < len(stored) {
		e.store.MarkCompacted(sess.ID, stored[cut-1].ID)
	}
	head := llm.Message{Role: "user", Content: "[Earlier conversation, summarised]\n" + summary + "\n[End of summary; the recent messages follow.]"}
	return append([]llm.Message{head}, history[cut:]...)
}

func (e *Engine) summarise(ctx context.Context, c *config.Config, msgs []llm.Message, prior string) string {
	client, model, err := e.auxClient(ctx, c)
	if err != nil {
		return ""
	}
	var b strings.Builder
	if prior != "" {
		b.WriteString("Summary so far:\n" + prior + "\n\n")
	}
	b.WriteString("Conversation to fold in:\n")
	for _, m := range msgs {
		content := m.Content
		if len(content) > 2000 {
			content = content[:2000] + "…"
		}
		if m.Role == "tool" {
			fmt.Fprintf(&b, "[tool %s result]: %s\n", m.Name, clip(content, 500))
		} else if len(m.ToolCalls) > 0 {
			var names []string
			for _, tc := range m.ToolCalls {
				names = append(names, tc.Name)
			}
			fmt.Fprintf(&b, "[assistant called: %s] %s\n", strings.Join(names, ", "), content)
		} else {
			fmt.Fprintf(&b, "[%s]: %s\n", m.Role, content)
		}
	}
	out, err := llm.Complete(ctx, client, llm.Request{
		Model:     model,
		System:    "You compress a conversation into a durable summary for an agent to keep working. Preserve: the task and goal, decisions made, facts discovered, files and paths touched, commands that worked or failed, findings, and anything still open. Drop chit-chat. Write tight notes, not prose. Keep it under 500 words.",
		Messages:  []llm.Message{{Role: "user", Content: b.String()}},
		MaxTokens: 1200, Temperature: 0.2,
	})
	if err != nil {
		return prior
	}
	return out
}

func (e *Engine) auxClient(ctx context.Context, c *config.Config) (llm.Client, string, error) {
	ref := c.Model.Aux
	if ref == "" {
		ref = c.Model.Default
	}
	return e.resolveClient(ctx, ref)
}

// ---------- repeat guard ----------

type repeatGuard struct {
	limit  int
	counts map[string]int
	nudged map[string]bool
}

func (g *repeatGuard) key(call llm.ToolCall) string {
	// write_file/edit_file fingerprint by target path, not full args.
	if call.Name == "write_file" || call.Name == "edit_file" {
		var a struct{ Path string }
		json.Unmarshal(call.Args, &a)
		return call.Name + ":" + a.Path
	}
	var norm any
	if json.Unmarshal(call.Args, &norm) == nil {
		b, _ := json.Marshal(norm)
		sum := sha256.Sum256(append([]byte(call.Name), b...))
		return hex.EncodeToString(sum[:8])
	}
	return call.Name + ":" + string(call.Args)
}

// check returns (nudge, abort). nudge fires once at the limit; abort at 2x.
func (g *repeatGuard) check(call llm.ToolCall) (string, bool) {
	if g.limit <= 0 {
		g.limit = 3
	}
	if g.counts == nil {
		g.counts = map[string]int{}
		g.nudged = map[string]bool{}
	}
	k := g.key(call)
	g.counts[k]++
	n := g.counts[k]
	switch {
	case n >= g.limit*2:
		return "", true
	case n >= g.limit && !g.nudged[k]:
		g.nudged[k] = true
		return repeatNudge(call.Name), false
	}
	return "", false
}

// ---------- steering ----------

func (e *Engine) injectSteer(sess *store.Session, history []llm.Message, steers []string, emit Emitter) []llm.Message {
	note := "[Steering from the operator] " + strings.Join(steers, " ")
	m := &store.Message{SessionID: sess.ID, Role: "user", Content: note}
	e.store.AppendMessage(m)
	emit(Event{Type: "notice", Text: "steering delivered: " + strings.Join(steers, " "), TS: now()})
	return append(history, toLLM(m))
}

// ---------- verification ----------

func (e *Engine) verify(ctx context.Context, c *config.Config, sess *store.Session, req Request, history []llm.Message, emit Emitter) string {
	client, model, err := e.auxClient(ctx, c)
	if err != nil {
		return ""
	}
	// Gather the ran tools and the final answer.
	var toolsRan []string
	var finalAnswer string
	for _, m := range history {
		for _, tc := range m.ToolCalls {
			toolsRan = append(toolsRan, tc.Name)
		}
		if m.Role == "assistant" && m.Content != "" {
			finalAnswer = m.Content
		}
	}
	prompt := fmt.Sprintf("The user asked:\n%s\n\nTools that actually ran: %s\n\nThe assistant's final answer:\n%s\n\nWas the user's request actually carried out? Answer only whether what was asked for happened — not whether it was done well, not whether more could be done. If something concrete that was asked for is missing, reply with one sentence naming exactly what is missing, starting with 'MISSING:'. Otherwise reply 'OK'.",
		clip(req.Text, 2000), strings.Join(dedupe(toolsRan), ", "), clip(finalAnswer, 3000))
	out, err := llm.Complete(ctx, client, llm.Request{Model: model, System: "You are a strict verifier. Do not invent extra requirements.", Messages: []llm.Message{{Role: "user", Content: prompt}}, MaxTokens: 200, Temperature: 0})
	if err != nil {
		return ""
	}
	out = strings.TrimSpace(out)
	if strings.HasPrefix(strings.ToUpper(out), "MISSING:") {
		return strings.TrimSpace(out[len("MISSING:"):])
	}
	return ""
}

// ---------- goal judge ----------

func (e *Engine) judgeGoal(ctx context.Context, c *config.Config, sess *store.Session, history []llm.Message, emit Emitter) string {
	client, model, err := e.auxClient(ctx, c)
	if err != nil {
		return ""
	}
	var recent strings.Builder
	start := len(history) - 12
	if start < 0 {
		start = 0
	}
	for _, m := range history[start:] {
		recent.WriteString(m.Role + ": " + clip(m.Content, 800) + "\n")
	}
	prompt := fmt.Sprintf("Goal:\n%s\n\nRecent work:\n%s\n\nIs the goal met? A plan is not completion; an intention is not doing. But do not invent extra work beyond the goal. If met, reply 'DONE'. If not, reply with one concrete next step, starting with 'NEXT:'.", sess.Goal, recent.String())
	out, err := llm.Complete(ctx, client, llm.Request{Model: model, System: "You judge whether a goal is met and name the next step if not.", Messages: []llm.Message{{Role: "user", Content: prompt}}, MaxTokens: 200, Temperature: 0})
	if err != nil {
		return ""
	}
	out = strings.TrimSpace(out)
	if strings.HasPrefix(strings.ToUpper(out), "NEXT:") {
		emit(Event{Type: "notice", Text: "goal not yet met; continuing", TS: now()})
		return strings.TrimSpace(out[len("NEXT:"):])
	}
	emit(Event{Type: "notice", Text: "goal judged met.", TS: now()})
	return ""
}

// ---------- titles ----------

func (e *Engine) titleSession(sessionID, firstMsg string, client llm.Client, model string, emit Emitter) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	// An explicit auxiliary model is optional; the selected chat client is
	// already resolved (including subscription credentials) and always works
	// without a global default model.
	generate := func(client llm.Client, model string) string {
		out, err := llm.Complete(ctx, client, llm.Request{
			Model: model, System: "Give a 2-5 word title for a conversation that starts with this message. Title only, no quotes, no punctuation at the end.",
			Messages: []llm.Message{{Role: "user", Content: clip(firstMsg, 500)}}, MaxTokens: 64, Temperature: 0.3, Reasoning: "off",
		})
		if err != nil {
			return ""
		}
		return strings.Trim(strings.TrimSpace(out), "\"'. \r\n\t")
	}
	title := ""
	if aux := e.cfg.Get().Model.Aux; aux != "" {
		if ac, am, err := e.resolveClient(ctx, aux); err == nil {
			title = generate(ac, am)
		}
	}
	if title == "" || title == "New chat" {
		title = generate(client, model)
	}
	if title == "" || title == "New chat" {
		// Naming must not depend on a second successful provider request.
		title = strings.Join(strings.Fields(firstMsg), " ")
		if title == "" || title == "New chat" {
			title = "Image conversation"
		}
	}
	title = string([]rune(title)[:min(len([]rune(title)), 80)])
	if updated, err := e.store.SetSessionTitleIfUnnamed(sessionID, title); err == nil && updated {
		emit(Event{Type: "session_title", SessionID: sessionID, Text: title, TS: now()})
	}
}

// ---------- helpers ----------

func redact(s string) (string, int) { return secure.Redact(s) }

func fence(source, content string) string { return secure.Fence(source, content) }

func dedupe(s []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range s {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	sort.Strings(out)
	return out
}
