// Package tools is the callable surface the model works through.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/allones/allones/internal/config"
	"github.com/allones/allones/internal/llm"
	"github.com/allones/allones/internal/secure"
	"github.com/allones/allones/internal/store"
)

type Result struct {
	Text      string         `json:"text"`
	IsError   bool           `json:"is_error"`
	Meta      map[string]any `json:"meta,omitempty"`
	Untrusted string         `json:"-"` // non-empty: content came from outside; fence it
	Images    []llm.Image    `json:"-"`
}

func Text(s string) Result                        { return Result{Text: s} }
func Errorf(f string, a ...any) Result            { return Result{Text: fmt.Sprintf(f, a...), IsError: true} }
func External(source, s string) Result             { return Result{Text: s, Untrusted: source} }
func JSON(v any) Result {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return Errorf("encode: %v", err)
	}
	return Text(string(b))
}

type Tool struct {
	Name        string
	Description string
	Schema      map[string]any
	Category    string
	// Mutates marks tools that change state or reach out; prompt mode asks first.
	Mutates bool
	// Network marks tools that touch a target host (scope-checked during engagements).
	Network bool
	Run     func(ctx context.Context, env *Env, args json.RawMessage) Result
	// Source is "builtin" or "mcp:<server>".
	Source string
}

// Delegator lets tools start sub-agents without importing the agent package.
type Delegator interface {
	Delegate(ctx context.Context, parent *Env, role, prompt string, isolate bool) (string, error)
	RoleNames() []string
}

// Asker resolves an approval or question from the operator.
type Asker interface {
	Approve(ctx context.Context, env *Env, tool, title, detail string) bool
	Ask(ctx context.Context, env *Env, question string, options []string) (string, error)
}

// Env is what a tool may touch for one call.
type Env struct {
	SessionID string
	Workspace string
	Role      string
	Depth     int
	Cfg       *config.Config
	Store     *store.Store
	Vault     *secure.Vault
	Services  *Services
	Progress  func(string)
	AutoApprove bool
	Interactive bool
	eng         *engCache
}

type engCache struct {
	mu     sync.Mutex
	loaded bool
	val    *store.Engagement
}

// Engagement returns the session's active engagement, if any (cached per session).
func (e *Env) Engagement() *store.Engagement {
	if e.eng == nil {
		e.eng = &engCache{}
	}
	e.eng.mu.Lock()
	defer e.eng.mu.Unlock()
	if e.eng.loaded {
		return e.eng.val
	}
	e.eng.loaded = true
	if e.Store == nil || e.SessionID == "" {
		return nil
	}
	s, err := e.Store.GetSession(e.SessionID)
	if err != nil || s.EngagementID == "" {
		return nil
	}
	if eng, err := e.Store.GetEngagement(s.EngagementID); err == nil {
		e.eng.val = eng
	}
	return e.eng.val
}

// Resolve maps a model-supplied path into the workspace and refuses escapes.
func (e *Env) Resolve(p string) (string, error) {
	return ResolvePath(e.Workspace, p)
}

func ResolvePath(root, p string) (string, error) {
	if p == "" {
		p = "."
	}
	root, _ = filepath.Abs(root)
	var full string
	if filepath.IsAbs(p) || filepath.VolumeName(p) != "" {
		full = filepath.Clean(p)
	} else {
		full = filepath.Join(root, p)
	}
	rel, err := filepath.Rel(root, full)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q is outside the workspace %s", p, root)
	}
	if real, err := filepath.EvalSymlinks(full); err == nil {
		rr, _ := filepath.EvalSymlinks(root)
		if rr == "" {
			rr = root
		}
		if rel, err := filepath.Rel(rr, real); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return "", fmt.Errorf("path %q resolves outside the workspace through a link", p)
		}
	}
	return full, nil
}

// CheckTarget applies engagement scope to a network target.
// Returns a non-empty warning (warn mode) or an error (block mode).
func (e *Env) CheckTarget(ctx context.Context, tool, target string) (string, error) {
	mode := e.Cfg.Security.ScopeEnforcement
	eng := e.Engagement()
	if mode == "off" || eng == nil {
		return "", nil
	}
	ok, why := secure.Scope{Include: eng.Scope, Exclude: eng.Exclusions}.Check(target)
	if ok {
		if !eng.Authorized {
			return "note: engagement \"" + eng.Name + "\" is not marked authorized yet; confirm written authorization before active testing.", nil
		}
		return "", nil
	}
	msg := fmt.Sprintf("target %s is out of scope for engagement %q (%s)", secure.HostOf(target), eng.Name, why)
	e.Store.Audit("agent:"+e.SessionID, "scope.violation", target, tool+": "+why, "warn")
	if mode == "block" {
		if e.Services != nil && e.Services.Asker != nil && e.Interactive {
			if e.Services.Asker.Approve(ctx, e, tool, "Out-of-scope target", msg+". Allow this one call?") {
				return "operator allowed out-of-scope call", nil
			}
		}
		return "", fmt.Errorf("%s; refused (security.scope_enforcement=block)", msg)
	}
	return "WARNING: " + msg + ". Stay within scope.", nil
}

type Registry struct {
	mu    sync.RWMutex
	tools map[string]*Tool
}

func NewRegistry() *Registry { return &Registry{tools: map[string]*Tool{}} }

func (r *Registry) Add(t *Tool) {
	if t.Source == "" {
		t.Source = "builtin"
	}
	r.mu.Lock()
	r.tools[t.Name] = t
	r.mu.Unlock()
}

func (r *Registry) RemoveSource(source string) {
	r.mu.Lock()
	for n, t := range r.tools {
		if t.Source == source {
			delete(r.tools, n)
		}
	}
	r.mu.Unlock()
}

func (r *Registry) Get(name string) *Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.tools[name]
}

func (r *Registry) All() []*Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Tool, 0, len(r.tools))
	for _, t := range r.tools {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Category != out[j].Category {
			return out[i].Category < out[j].Category
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// Toolsets name groups; categories expand to every tool in them.
var Toolsets = map[string][]string{
	"none":     {},
	"minimal":  {"read_file", "list_files", "grep", "glob", "todo"},
	"coding":   {"@files", "shell", "process", "todo", "skill", "delegate_task", "list_roles", "memory", "web_fetch", "web_search", "rollback"},
	"research": {"read_file", "list_files", "grep", "web_search", "web_fetch", "browser", "http_request", "todo", "memory", "session_search", "rag_search", "rag_index", "skill", "delegate_task", "list_roles", "write_file"},
	"browser":  {"browser", "web_search", "web_fetch", "read_file", "write_file", "todo"},
	"security": {"@files", "shell", "process", "@web", "browser", "@security", "@recon", "@reverse", "todo", "memory", "skill", "delegate_task", "list_roles", "rag_search", "ask_user", "@mcp"},
	"reverse":  {"@files", "shell", "process", "@reverse", "todo", "memory", "skill", "web_search", "web_fetch", "rag_search", "@mcp"},
	"ops":      {"@files", "shell", "process", "@ops", "todo", "memory", "skill", "web_fetch", "web_search", "schedule", "board"},
	"default":  {"@files", "shell", "process", "@web", "browser", "@memory", "@agent", "@security", "@recon", "@reverse", "@ops", "rollback"},
	"all":      {"*"},
}

// Select returns the tools for a toolset with config adjustments applied.
func (r *Registry) Select(toolset string, enabled, disabled []string) []*Tool {
	spec, ok := Toolsets[toolset]
	if !ok {
		spec = Toolsets["default"]
	}
	all := r.All()
	pick := map[string]bool{}
	for _, s := range spec {
		switch {
		case s == "*":
			for _, t := range all {
				pick[t.Name] = true
			}
		case s == "@mcp":
			for _, t := range all {
				if strings.HasPrefix(t.Source, "mcp:") {
					pick[t.Name] = true
				}
			}
		case strings.HasPrefix(s, "@"):
			for _, t := range all {
				if t.Category == s[1:] {
					pick[t.Name] = true
				}
			}
		default:
			pick[s] = true
		}
	}
	if toolset == "default" { // default also carries MCP tools
		for _, t := range all {
			if strings.HasPrefix(t.Source, "mcp:") {
				pick[t.Name] = true
			}
		}
	}
	for _, n := range enabled {
		pick[n] = true
	}
	var out []*Tool
	for _, t := range all {
		if pick[t.Name] && !slices.Contains(disabled, t.Name) {
			out = append(out, t)
		}
	}
	return out
}

// ---------- helpers ----------

func Obj(props map[string]any, required ...string) map[string]any {
	s := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

func Str(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
func Int(desc string) map[string]any { return map[string]any{"type": "integer", "description": desc} }
func Bool(desc string) map[string]any { return map[string]any{"type": "boolean", "description": desc} }
func Num(desc string) map[string]any  { return map[string]any{"type": "number", "description": desc} }
func Enum(desc string, vals ...string) map[string]any {
	return map[string]any{"type": "string", "description": desc, "enum": vals}
}
func Arr(desc string, item map[string]any) map[string]any {
	return map[string]any{"type": "array", "description": desc, "items": item}
}
func StrArr(desc string) map[string]any { return Arr(desc, map[string]any{"type": "string"}) }

func Bind(args json.RawMessage, v any) error {
	if len(args) == 0 {
		args = json.RawMessage(`{}`)
	}
	if err := json.Unmarshal(args, v); err != nil {
		return fmt.Errorf("invalid arguments: %v", err)
	}
	return nil
}

// Truncate cuts s to max characters on a rune boundary with a note.
func Truncate(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + fmt.Sprintf("\n… [truncated %d of %d characters]", len(s)-cut, len(s))
}

// Services are long-lived dependencies shared by tools.
type Services struct {
	Registry  *Registry
	Delegator Delegator
	Asker     Asker
	Skills    SkillLibrary
	RAG       Retriever
	Browser   *BrowserPool
	Procs     *ProcessTable
	Shells    *ShellSessions
	Notify    func(title, body string)
	CommandGuard *secure.CommandGuard
	MCPStatus func() []map[string]any
	Scheduler interface{ NextRuns(expr string, n int) ([]int64, error) }
}

type SkillLibrary interface {
	List() []SkillInfo
	Search(q string, limit int) []SkillInfo
	Read(name string) (string, error)
	ReadFile(name, rel string) (string, error)
	Save(name, content string) (string, error)
}

type SkillInfo struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Source      string   `json:"source"`
	Tags        []string `json:"tags,omitempty"`
	Files       []string `json:"files,omitempty"`
	Disabled    bool     `json:"disabled"`
}

type Retriever interface {
	Search(ctx context.Context, collection, query string, k int) ([]store.Chunk, error)
	IndexPath(ctx context.Context, collection, path string) (int, int, error)
	IndexText(ctx context.Context, collection, source, text string) (int, error)
}
