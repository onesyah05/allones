package agent

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/allones/allones/internal/config"
	"github.com/allones/allones/internal/rag"
	"github.com/allones/allones/internal/roles"
	"github.com/allones/allones/internal/secure"
	"github.com/allones/allones/internal/skills"
	"github.com/allones/allones/internal/store"
	"github.com/allones/allones/internal/tools"
)

func New(cfg *config.Manager, st *store.Store, reg *tools.Registry, sk *skills.Library, rl *roles.Set, rg *rag.RAG, vault *secure.Vault) *Engine {
	c := cfg.Get()
	e := &Engine{
		cfg: cfg, store: st, registry: reg, skills: sk, roles: rl, rag: rg, vault: vault,
		guard:   secure.NewCommandGuard(c.Security.DangerousCommands),
		running: map[string]context.CancelFunc{},
		steers:  map[string][]string{},
		bcast:   newBroadcaster(),
	}
	n := c.Agent.MaxConcurrent
	if n <= 0 {
		n = 4
	}
	e.sem = make(chan struct{}, n)
	e.services = &tools.Services{
		Registry: reg, Delegator: e, Skills: sk, RAG: rg,
		Browser: tools.NewBrowserPool(c.Tools.Browser.Executable, c.Tools.Browser.Headless, c.Tools.Browser.Width, c.Tools.Browser.Height, browserProfile()),
		Procs:   tools.NewProcessTable(), Shells: tools.NewShellSessions(),
		CommandGuard: e.guard,
		Notify: func(t, b string) {
			if e.Notify != nil {
				e.Notify(t, b)
			}
		},
		Scheduler: schedWrap{e},
	}
	e.services.Asker = e // Env uses Services.Asker for approvals/questions
	cfg.OnChange(func(nc *config.Config) {
		e.guard = secure.NewCommandGuard(nc.Security.DangerousCommands)
		e.services.CommandGuard = e.guard
	})
	return e
}

type schedWrap struct{ e *Engine }

func (s schedWrap) NextRuns(expr string, n int) ([]int64, error) {
	if s.e.Scheduler != nil {
		return s.e.Scheduler.NextRuns(expr, n)
	}
	return nil, fmt.Errorf("scheduler not wired")
}

func browserProfile() string {
	return filepath.Join(config.Home(), "browser")
}

func (e *Engine) SetApprover(a Approver)    { e.approver = a }
func (e *Engine) SetAsker(a QuestionAsker)  { e.asker = a }
func (e *Engine) Store() *store.Store       { return e.store }
func (e *Engine) Registry() *tools.Registry { return e.registry }
func (e *Engine) Skills() *skills.Library   { return e.skills }
func (e *Engine) Roles() *roles.Set         { return e.roles }
func (e *Engine) Services() *tools.Services { return e.services }

// Close releases the browser and background processes.
func (e *Engine) Close() {
	if e.services.Browser != nil {
		e.services.Browser.Close()
	}
	if e.services.Procs != nil {
		e.services.Procs.KillAll()
	}
}

// resolveRole returns the role and its effective toolset/model.
func (e *Engine) resolveRole(name string) *roles.Role {
	if name == "" {
		name = "assistant"
	}
	if r, ok := e.roles.Get(name); ok {
		return r
	}
	r, _ := e.roles.Get("assistant")
	return r
}

func (e *Engine) buildEnv(sess *store.Session, req Request, role *roles.Role) *tools.Env {
	c := e.cfg.Get()
	ws := c.Tools.Workspace
	if sess.Workspace != "" {
		ws = sess.Workspace
	}
	return &tools.Env{
		SessionID: sess.ID, Workspace: ws, Role: role.Name, Depth: req.Depth,
		Cfg: c, Store: e.store, Vault: e.vault, Services: e.services,
		Interactive: req.Interactive, AutoApprove: req.AutoApprove,
	}
}

// RoleNames implements tools.Delegator.
func (e *Engine) RoleNames() []string { return e.roles.Names() }

// RAG passthroughs for the HTTP API.
func (e *Engine) RAGSearch(ctx context.Context, collection, query string, k int) (any, error) {
	return e.rag.Search(ctx, collection, query, k)
}
func (e *Engine) RAGIndexText(ctx context.Context, collection, source, text string) (int, error) {
	return e.rag.IndexText(ctx, collection, source, text)
}
func (e *Engine) RAGIndexPath(ctx context.Context, collection, path string) (int, int, error) {
	return e.rag.IndexPath(ctx, collection, path)
}

// Steer queues a steering note delivered after the current tool batch.
func (e *Engine) Steer(sessionID, note string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, running := e.running[sessionID]; !running {
		return false
	}
	e.steers[sessionID] = append(e.steers[sessionID], note)
	return true
}

func (e *Engine) takeSteers(sessionID string) []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	s := e.steers[sessionID]
	delete(e.steers, sessionID)
	return s
}

// Stop cancels a running turn.
func (e *Engine) Stop(sessionID string) bool {
	e.mu.Lock()
	cancel := e.running[sessionID]
	e.mu.Unlock()
	if cancel != nil {
		cancel()
		return true
	}
	return false
}

func (e *Engine) IsRunning(sessionID string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	_, ok := e.running[sessionID]
	return ok
}

// Approve implements tools.Asker (approval path).
func (e *Engine) Approve(ctx context.Context, env *tools.Env, tool, title, detail string) bool {
	mode := e.cfg.Get().Tools.ApprovalMode
	if env.AutoApprove || mode == "auto" {
		return true
	}
	if mode == "deny" {
		return false
	}
	if !env.Interactive || e.approver == nil {
		return false
	}
	return e.approver.Approve(ctx, env.SessionID, tool, title, detail)
}

// Ask implements tools.Asker (question path).
func (e *Engine) Ask(ctx context.Context, env *tools.Env, question string, options []string) (string, error) {
	if !env.Interactive || e.asker == nil {
		return "", fmt.Errorf("no operator available")
	}
	return e.asker.Ask(ctx, env.SessionID, question, options)
}

// selectTools resolves the toolset for a role/request.
func (e *Engine) selectTools(c *config.Config, role *roles.Role, reqToolset, source string) []*tools.Tool {
	ts := reqToolset
	if ts == "" {
		if pt := c.Tools.Platform[source]; pt != "" && source != "web" && source != "tui" {
			ts = pt
		}
	}
	if ts == "" && role.Toolset != "" {
		ts = role.Toolset
	}
	if ts == "" {
		ts = c.Tools.Toolset
	}
	return e.registry.Select(ts, c.Tools.Enabled, c.Tools.Disabled)
}

var _ = strings.TrimSpace
var _ = skills.New
