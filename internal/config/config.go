// Package config holds the layered configuration: defaults, then
// ~/.allones/config.yaml, then environment variables.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server   Server     `yaml:"server" json:"server"`
	Model    Model      `yaml:"model" json:"model"`
	Provider []Provider `yaml:"providers" json:"providers"`
	Agent    Agent      `yaml:"agent" json:"agent"`
	Tools    Tools      `yaml:"tools" json:"tools"`
	Security Security   `yaml:"security" json:"security"`
	Memory   Memory     `yaml:"memory" json:"memory"`
	RAG      RAG        `yaml:"rag" json:"rag"`
	MCP      MCP        `yaml:"mcp" json:"mcp"`
	Skills   Skills     `yaml:"skills" json:"skills"`
	Gateway  Gateway    `yaml:"gateway" json:"gateway"`
	Soul     Soul       `yaml:"soul" json:"soul"`
}

type Server struct {
	Host         string   `yaml:"host" json:"host"`
	Port         int      `yaml:"port" json:"port"`
	PasswordHash string   `yaml:"password_hash" json:"-"`
	Token        string   `yaml:"token" json:"-"`
	AuthDisabled bool     `yaml:"auth_disabled" json:"auth_disabled"`
	AllowOrigins []string `yaml:"allow_origins" json:"allow_origins"`
}

type Model struct {
	Default   string   `yaml:"default" json:"default"`
	Aux       string   `yaml:"aux" json:"aux"` // cheap model: titles, compaction, critic, judge
	Panel     []string `yaml:"panel" json:"panel"`
	MaxTokens int      `yaml:"max_tokens" json:"max_tokens"`
	Reasoning string   `yaml:"reasoning" json:"reasoning"` // off|low|medium|high
}

type Provider struct {
	Name         string            `yaml:"name" json:"name"`
	Label        string            `yaml:"label,omitempty" json:"label"`
	Disabled     bool              `yaml:"disabled,omitempty" json:"disabled"`
	Kind         string            `yaml:"kind" json:"kind"` // openai | anthropic | gemini | openai-compatible
	BaseURL      string            `yaml:"base_url" json:"base_url"`
	APIKey       string            `yaml:"api_key" json:"-"`
	RefreshToken string            `yaml:"refresh_token,omitempty" json:"-"`
	ExpiresAt    int64             `yaml:"expires_at,omitempty" json:"expires_at,omitempty"`
	OAuth        bool              `yaml:"oauth,omitempty" json:"oauth,omitempty"`
	Models       []string          `yaml:"models" json:"models"`
	Headers      map[string]string `yaml:"headers" json:"headers,omitempty"`
	Timeout      int               `yaml:"timeout_seconds" json:"timeout_seconds"`
	FromEnv      bool              `yaml:"-" json:"from_env"`
}

type Agent struct {
	MaxTurns          int     `yaml:"max_turns" json:"max_turns"`
	MaxToolCalls      int     `yaml:"max_tool_calls" json:"max_tool_calls"`
	RepeatLimit       int     `yaml:"repeat_limit" json:"repeat_limit"`
	VerifyReplies     bool    `yaml:"verify_replies" json:"verify_replies"`
	VerifyMax         int     `yaml:"verify_max" json:"verify_max"`
	GoalMaxIterations int     `yaml:"goal_max_iterations" json:"goal_max_iterations"`
	ContextWindow     int     `yaml:"context_window" json:"context_window"`
	CompactAt         float64 `yaml:"compact_at" json:"compact_at"`
	ParallelTools     int     `yaml:"parallel_tools" json:"parallel_tools"`
	MaxConcurrent     int     `yaml:"max_concurrent_sessions" json:"max_concurrent_sessions"`
	Temperature       float64 `yaml:"temperature" json:"temperature"`
	SubagentDepth     int     `yaml:"subagent_depth" json:"subagent_depth"`
}

type Tools struct {
	Toolset        string            `yaml:"toolset" json:"toolset"`
	Enabled        []string          `yaml:"enabled" json:"enabled"`
	Disabled       []string          `yaml:"disabled" json:"disabled"`
	ApprovalMode   string            `yaml:"approval_mode" json:"approval_mode"` // auto | prompt | deny
	Workspace      string            `yaml:"workspace" json:"workspace"`
	MaxOutputChars int               `yaml:"max_output_chars" json:"max_output_chars"`
	ShellTimeout   int               `yaml:"shell_timeout" json:"shell_timeout"`
	Shell          string            `yaml:"shell" json:"shell"`
	Search         Search            `yaml:"web_search" json:"web_search"`
	Browser        Browser           `yaml:"browser" json:"browser"`
	Platform       map[string]string `yaml:"platform_toolsets" json:"platform_toolsets"`
}

type Search struct {
	Provider string `yaml:"provider" json:"provider"` // duckduckgo | brave | tavily | searxng
	APIKey   string `yaml:"api_key" json:"-"`
	BaseURL  string `yaml:"base_url" json:"base_url"`
}

type Browser struct {
	Executable string `yaml:"executable" json:"executable"`
	Headless   bool   `yaml:"headless" json:"headless"`
	Width      int    `yaml:"width" json:"width"`
	Height     int    `yaml:"height" json:"height"`
}

type Security struct {
	RedactSecrets     bool     `yaml:"redact_secrets" json:"redact_secrets"`
	SSRFProtect       bool     `yaml:"ssrf_protect" json:"ssrf_protect"`
	ScopeEnforcement  string   `yaml:"scope_enforcement" json:"scope_enforcement"` // off | warn | block
	UntrustedWrapping bool     `yaml:"untrusted_wrapping" json:"untrusted_wrapping"`
	DangerousCommands []string `yaml:"dangerous_commands" json:"dangerous_commands"`
	AuditLog          bool     `yaml:"audit_log" json:"audit_log"`
	ToolsPath         string   `yaml:"tools_path" json:"tools_path"` // extra PATH for nuclei, httpx, ...
}

type Memory struct {
	Enabled   bool `yaml:"enabled" json:"enabled"`
	CharLimit int  `yaml:"char_limit" json:"char_limit"`
}

type RAG struct {
	Enabled       bool   `yaml:"enabled" json:"enabled"`
	EmbedProvider string `yaml:"embed_provider" json:"embed_provider"`
	EmbedModel    string `yaml:"embed_model" json:"embed_model"`
	ChunkSize     int    `yaml:"chunk_size" json:"chunk_size"`
	ChunkOverlap  int    `yaml:"chunk_overlap" json:"chunk_overlap"`
	TopK          int    `yaml:"top_k" json:"top_k"`
	AutoContext   bool   `yaml:"auto_context" json:"auto_context"`
}

type MCP struct {
	Servers []MCPServer `yaml:"servers" json:"servers"`
}

type MCPServer struct {
	Name    string            `yaml:"name" json:"name"`
	Command string            `yaml:"command" json:"command"`
	Args    []string          `yaml:"args" json:"args"`
	Env     map[string]string `yaml:"env" json:"env,omitempty"`
	URL     string            `yaml:"url" json:"url"`
	Headers map[string]string `yaml:"headers" json:"-"`
	Enabled bool              `yaml:"enabled" json:"enabled"`
	Timeout int               `yaml:"timeout_seconds" json:"timeout_seconds"`
}

type Skills struct {
	Dirs         []string `yaml:"dirs" json:"dirs"`
	Disabled     []string `yaml:"disabled" json:"disabled"`
	AutoDiscover bool     `yaml:"auto_discover" json:"auto_discover"`
}

type Gateway struct {
	Telegram Bot `yaml:"telegram" json:"telegram"`
	Discord  Bot `yaml:"discord" json:"discord"`
}

type Bot struct {
	Enabled      bool     `yaml:"enabled" json:"enabled"`
	Token        string   `yaml:"token" json:"-"`
	AllowedUsers []string `yaml:"allowed_users" json:"allowed_users"`
	Toolset      string   `yaml:"toolset" json:"toolset"`
	Role         string   `yaml:"role" json:"role"`
}

type Soul struct {
	Name        string `yaml:"name" json:"name"`
	Personality string `yaml:"personality" json:"personality"`
	Language    string `yaml:"language" json:"language"`
}

// Defaults returns a fully populated configuration.
func Defaults() *Config {
	return &Config{
		Server: Server{Host: "127.0.0.1", Port: 8788},
		Model:  Model{MaxTokens: 8192, Reasoning: "off"},
		Agent: Agent{
			MaxTurns: 120, MaxToolCalls: 150, RepeatLimit: 3, VerifyMax: 2,
			GoalMaxIterations: 10, ContextWindow: 128000, CompactAt: 0.75,
			ParallelTools: 6, MaxConcurrent: 4, Temperature: 0.3, SubagentDepth: 2,
		},
		Tools: Tools{
			Toolset: "default", ApprovalMode: "prompt", MaxOutputChars: 30000,
			ShellTimeout: 120, Search: Search{Provider: "duckduckgo"},
			Browser:  Browser{Headless: true, Width: 1366, Height: 900},
			Platform: map[string]string{"telegram": "research", "discord": "research"},
		},
		Security: Security{
			RedactSecrets: true, SSRFProtect: true, ScopeEnforcement: "warn",
			UntrustedWrapping: true, AuditLog: true,
			DangerousCommands: []string{
				`rm\s+-rf\s+(/|~|\*)(\s|$)`, `mkfs`, `dd\s+if=.*of=/dev/`, `:\(\)\s*\{`,
				`shutdown`, `reboot`, `format\s+[a-z]:`, `Remove-Item\s+.*-Recurse.*[A-Z]:\\\s*$`,
				`curl[^|]*\|\s*(sudo\s+)?(ba)?sh`, `iwr[^|]*\|\s*iex`, `chmod\s+-R\s+777\s+/`,
			},
		},
		Memory: Memory{Enabled: true, CharLimit: 4000},
		RAG:    RAG{Enabled: true, ChunkSize: 1200, ChunkOverlap: 150, TopK: 8, AutoContext: true},
		Skills: Skills{AutoDiscover: true},
		Soul:   Soul{Name: "AllOnes"},
	}
}

// Home is the data directory (ALLONES_HOME or ~/.allones).
func Home() string {
	if h := os.Getenv("ALLONES_HOME"); h != "" {
		return h
	}
	u, err := os.UserHomeDir()
	if err != nil {
		return ".allones"
	}
	return filepath.Join(u, ".allones")
}

func Path() string { return filepath.Join(Home(), "config.yaml") }

// ExpandHome turns a leading ~ into the user's home directory.
func ExpandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		u, _ := os.UserHomeDir()
		return filepath.Join(u, p[1:])
	}
	return p
}

// Manager guards the live configuration and persists it.
type Manager struct {
	mu   sync.RWMutex
	cfg  *Config
	path string
	subs []func(*Config)
}

func Load() (*Manager, error) {
	m := &Manager{path: Path()}
	cfg := Defaults()
	b, err := os.ReadFile(m.path)
	switch {
	case err == nil:
		if err := yaml.Unmarshal(b, cfg); err != nil {
			return nil, fmt.Errorf("parse %s: %w", m.path, err)
		}
	case errors.Is(err, os.ErrNotExist):
	default:
		return nil, err
	}
	// The legacy default conflicts with Antares on this installation. Migrate
	// saved configs as well as fresh defaults; explicit environment overrides
	// are applied below and remain authoritative.
	if cfg.Server.Port == 8787 {
		cfg.Server.Port = 8788
	}
	loadDotEnv(filepath.Join(Home(), ".env"))
	applyEnv(cfg)
	normalize(cfg)
	m.cfg = cfg
	return m, nil
}

// Get returns a deep-enough copy for read-only use.
func (m *Manager) Get() *Config {
	m.mu.RLock()
	defer m.mu.RUnlock()
	c := *m.cfg
	c.Provider = append([]Provider(nil), m.cfg.Provider...)
	c.MCP.Servers = append([]MCPServer(nil), m.cfg.MCP.Servers...)
	return &c
}

// Update mutates the configuration under lock and persists it.
func (m *Manager) Update(fn func(*Config) error) error {
	m.mu.Lock()
	if err := fn(m.cfg); err != nil {
		m.mu.Unlock()
		return err
	}
	normalize(m.cfg)
	err := m.saveLocked()
	cfg := *m.cfg
	subs := append([]func(*Config){}, m.subs...)
	m.mu.Unlock()
	for _, s := range subs {
		s(&cfg)
	}
	return err
}

func (m *Manager) OnChange(fn func(*Config)) {
	m.mu.Lock()
	m.subs = append(m.subs, fn)
	m.mu.Unlock()
}

func (m *Manager) Save() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.saveLocked()
}

func (m *Manager) Exists() bool {
	_, err := os.Stat(m.path)
	return err == nil
}

func (m *Manager) saveLocked() error {
	out := *m.cfg
	// Providers synthesised from env vars are not written to disk.
	out.Provider = nil
	for _, p := range m.cfg.Provider {
		if !p.FromEnv {
			out.Provider = append(out.Provider, p)
		}
	}
	b, err := yaml.Marshal(&out)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(m.path), 0o700); err != nil {
		return err
	}
	tmp := m.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, m.path)
}

func normalize(c *Config) {
	d := Defaults()
	if c.Server.Port == 0 {
		c.Server.Port = d.Server.Port
	}
	if c.Server.Host == "" {
		c.Server.Host = d.Server.Host
	}
	if c.Agent.MaxTurns <= 0 {
		c.Agent.MaxTurns = d.Agent.MaxTurns
	}
	if c.Agent.MaxToolCalls <= 0 {
		c.Agent.MaxToolCalls = d.Agent.MaxToolCalls
	}
	if c.Agent.RepeatLimit <= 0 {
		c.Agent.RepeatLimit = d.Agent.RepeatLimit
	}
	if c.Agent.ContextWindow <= 0 {
		c.Agent.ContextWindow = d.Agent.ContextWindow
	}
	if c.Agent.CompactAt <= 0 || c.Agent.CompactAt >= 1 {
		c.Agent.CompactAt = d.Agent.CompactAt
	}
	if c.Agent.ParallelTools <= 0 {
		c.Agent.ParallelTools = d.Agent.ParallelTools
	}
	if c.Tools.MaxOutputChars <= 0 {
		c.Tools.MaxOutputChars = d.Tools.MaxOutputChars
	}
	if c.Tools.ShellTimeout <= 0 {
		c.Tools.ShellTimeout = d.Tools.ShellTimeout
	}
	switch c.Tools.ApprovalMode {
	case "auto", "prompt", "deny":
	default:
		c.Tools.ApprovalMode = "prompt"
	}
	switch c.Security.ScopeEnforcement {
	case "off", "warn", "block":
	default:
		c.Security.ScopeEnforcement = "warn"
	}
	if c.Tools.Workspace == "" {
		c.Tools.Workspace = filepath.Join(Home(), "workspace")
	}
	c.Tools.Workspace = ExpandHome(c.Tools.Workspace)
	if c.Model.MaxTokens <= 0 {
		c.Model.MaxTokens = d.Model.MaxTokens
	}
	if c.RAG.ChunkSize <= 0 {
		c.RAG.ChunkSize = d.RAG.ChunkSize
	}
	if c.RAG.TopK <= 0 {
		c.RAG.TopK = d.RAG.TopK
	}
	if c.Memory.CharLimit <= 0 {
		c.Memory.CharLimit = d.Memory.CharLimit
	}
	if c.Soul.Name == "" {
		c.Soul.Name = "AllOnes"
	}
	for i := range c.Provider {
		p := &c.Provider[i]
		if p.Kind == "" {
			p.Kind = "openai-compatible"
		}
		if p.BaseURL == "" {
			p.BaseURL = DefaultBaseURL(p.Kind, p.Name)
		}
	}
	if c.Model.Default == "" && len(c.Provider) > 0 && len(c.Provider[0].Models) > 0 {
		c.Model.Default = c.Provider[0].Name + "/" + c.Provider[0].Models[0]
	}
}

// DefaultBaseURL knows the well-known endpoints.
func DefaultBaseURL(kind, name string) string {
	switch strings.ToLower(name) {
	case "openrouter":
		return "https://openrouter.ai/api/v1"
	case "groq":
		return "https://api.groq.com/openai/v1"
	case "deepseek":
		return "https://api.deepseek.com/v1"
	case "mistral":
		return "https://api.mistral.ai/v1"
	case "xai":
		return "https://api.x.ai/v1"
	case "together":
		return "https://api.together.xyz/v1"
	case "ollama":
		return "http://127.0.0.1:11434/v1"
	case "lmstudio":
		return "http://127.0.0.1:1234/v1"
	}
	switch kind {
	case "codex":
		return "https://chatgpt.com/backend-api/codex"
	case "antigravity":
		return "https://daily-cloudcode-pa.googleapis.com"
	case "anthropic", "claude-code":
		return "https://api.anthropic.com"
	case "gemini":
		return "https://generativelanguage.googleapis.com"
	case "openai":
		return "https://api.openai.com/v1"
	}
	return ""
}

// envProviders are added automatically when their key is present.
var envProviders = []struct{ env, name, kind, model string }{
	{"ANTHROPIC_API_KEY", "anthropic", "anthropic", "claude-sonnet-4-5"},
	{"OPENAI_API_KEY", "openai", "openai", "gpt-5"},
	{"GEMINI_API_KEY", "gemini", "gemini", "gemini-2.5-pro"},
	{"OPENROUTER_API_KEY", "openrouter", "openai-compatible", "anthropic/claude-sonnet-4.5"},
	{"GROQ_API_KEY", "groq", "openai-compatible", "llama-3.3-70b-versatile"},
	{"DEEPSEEK_API_KEY", "deepseek", "openai-compatible", "deepseek-chat"},
}

func applyEnv(c *Config) {
	if v := os.Getenv("ALLONES_HOST"); v != "" {
		c.Server.Host = v
	}
	if v := os.Getenv("ALLONES_PORT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.Server.Port = n
		}
	}
	if v := os.Getenv("ALLONES_MODEL"); v != "" {
		c.Model.Default = v
	}
	if v := os.Getenv("ALLONES_WORKSPACE"); v != "" {
		c.Tools.Workspace = v
	}
	if v := os.Getenv("ALLONES_TOKEN"); v != "" {
		c.Server.Token = v
	}
	for _, e := range envProviders {
		key := os.Getenv(e.env)
		if key == "" {
			continue
		}
		found := false
		for i := range c.Provider {
			if c.Provider[i].Name == e.name {
				found = true
				if c.Provider[i].APIKey == "" {
					c.Provider[i].APIKey = key
				}
			}
		}
		if !found {
			c.Provider = append(c.Provider, Provider{Name: e.name, Kind: e.kind, APIKey: key, Models: []string{e.model}, FromEnv: true})
		}
	}
	if v := os.Getenv("BRAVE_API_KEY"); v != "" && c.Tools.Search.APIKey == "" {
		c.Tools.Search.Provider, c.Tools.Search.APIKey = "brave", v
	}
	if v := os.Getenv("TAVILY_API_KEY"); v != "" && c.Tools.Search.APIKey == "" {
		c.Tools.Search.Provider, c.Tools.Search.APIKey = "tavily", v
	}
}

func loadDotEnv(path string) {
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(strings.TrimPrefix(k, "export "))
		v = strings.Trim(strings.TrimSpace(v), `"'`)
		if os.Getenv(k) == "" {
			os.Setenv(k, v)
		}
	}
}

// FindProvider resolves "provider/model" to a provider and bare model id.
// A ref without a known provider prefix uses the first provider listing the model,
// or the first provider.
func (c *Config) FindProvider(ref string) (*Provider, string, error) {
	if ref == "" {
		ref = c.Model.Default
	}
	if ref == "" {
		return nil, "", errors.New("no model configured: add a provider in Settings → Providers")
	}
	if name, model, ok := strings.Cut(ref, "/"); ok {
		for i := range c.Provider {
			if strings.EqualFold(c.Provider[i].Name, name) {
				return &c.Provider[i], model, nil
			}
		}
	}
	for i := range c.Provider {
		if c.Provider[i].Disabled {
			continue
		}
		for _, m := range c.Provider[i].Models {
			if m == ref {
				return &c.Provider[i], ref, nil
			}
		}
	}
	for i := range c.Provider {
		if !c.Provider[i].Disabled {
			return &c.Provider[i], ref, nil
		}
	}
	return nil, "", fmt.Errorf("no provider for model %q", ref)
}
