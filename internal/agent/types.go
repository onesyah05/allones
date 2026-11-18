// Package agent is the conversation loop: prompt assembly, model streaming,
// tool dispatch, the harness (repeat guard, compaction, verification, goals),
// and sub-agent delegation.
package agent

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/allones/allones/internal/config"
	"github.com/allones/allones/internal/llm"
	"github.com/allones/allones/internal/rag"
	"github.com/allones/allones/internal/roles"
	"github.com/allones/allones/internal/secure"
	"github.com/allones/allones/internal/skills"
	"github.com/allones/allones/internal/store"
	"github.com/allones/allones/internal/tools"
)

// Event is streamed to the caller as a turn progresses.
type Event struct {
	Type       string          `json:"type"` // start | reasoning | text | tool_start | tool_progress | tool_end | notice | usage | error | done
	SessionID  string          `json:"session_id,omitempty"`
	Text       string          `json:"text,omitempty"`
	Tool       string          `json:"tool,omitempty"`
	CallID     string          `json:"call_id,omitempty"`
	Args       json.RawMessage `json:"args,omitempty"`
	Result     string          `json:"result,omitempty"`
	IsError    bool            `json:"is_error,omitempty"`
	Usage      *llm.Usage      `json:"usage,omitempty"`
	MessageID  int64           `json:"message_id,omitempty"`
	Meta       map[string]any  `json:"meta,omitempty"`
	TS         int64           `json:"ts"`
}

type Emitter func(Event)

// Request starts a turn.
type Request struct {
	SessionID   string
	Text        string
	Images      []llm.Image
	Role        string
	Model       string
	Toolset     string
	Source      string // web | tui | telegram | discord | cron | subagent
	Interactive bool
	AutoApprove bool
	Sender      string // display name of who is talking, for gateways
	Workspace   string // per-session execution directory (overrides config)
	Depth       int
}

// Engine owns the shared services and runs turns.
type Engine struct {
	cfg      *config.Manager
	store    *store.Store
	registry *tools.Registry
	skills   *skills.Library
	roles    *roles.Set
	rag      *rag.RAG
	vault    *secure.Vault
	guard    *secure.CommandGuard
	services *tools.Services

	Notify     func(title, body string)
	Scheduler  interface{ NextRuns(string, int) ([]int64, error) }
	MCPStatus  func() []map[string]any

	mu       sync.Mutex
	running  map[string]context.CancelFunc
	steers   map[string][]string
	approver Approver
	asker    QuestionAsker
	sem      chan struct{}
	bcast    *broadcaster
}

// Approver decides tool approvals (interactive surfaces).
type Approver interface {
	Approve(ctx context.Context, sessionID, tool, title, detail string) bool
}

// QuestionAsker resolves ask_user.
type QuestionAsker interface {
	Ask(ctx context.Context, sessionID, question string, options []string) (string, error)
}

func now() int64 { return time.Now().UnixMilli() }
