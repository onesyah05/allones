// Package mcp is a Model Context Protocol client: it launches configured servers
// (stdio or streamable HTTP), discovers their tools, and exposes them to the agent
// namespaced as mcp__<server>__<tool>.
package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/allones/allones/internal/config"
	"github.com/allones/allones/internal/tools"
)

const protocolVersion = "2025-06-18"

type Manager struct {
	mu      sync.Mutex
	servers map[string]*Server
	reg     *tools.Registry
}

func NewManager(reg *tools.Registry) *Manager {
	return &Manager{servers: map[string]*Server{}, reg: reg}
}

type Server struct {
	Name    string
	cfg     config.MCPServer
	client  transport
	tools   []ToolInfo
	err     string
	started time.Time
}

type ToolInfo struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

type transport interface {
	call(ctx context.Context, method string, params any) (json.RawMessage, error)
	notify(method string, params any) error
	close()
}

// Sync (re)connects all configured servers and registers their tools.
func (m *Manager) Sync(ctx context.Context, servers []config.MCPServer) {
	m.mu.Lock()
	defer m.mu.Unlock()
	// Drop removed/disabled servers.
	keep := map[string]bool{}
	for _, s := range servers {
		if s.Enabled {
			keep[s.Name] = true
		}
	}
	for name, srv := range m.servers {
		if !keep[name] {
			srv.close()
			m.reg.RemoveSource("mcp:" + name)
			delete(m.servers, name)
		}
	}
	for _, sc := range servers {
		if !sc.Enabled {
			continue
		}
		if existing, ok := m.servers[sc.Name]; ok && existing.err == "" {
			continue // already connected
		}
		srv := &Server{Name: sc.Name, cfg: sc, started: time.Now()}
		if err := srv.connect(ctx); err != nil {
			srv.err = err.Error()
			m.servers[sc.Name] = srv
			continue
		}
		m.servers[sc.Name] = srv
		m.registerTools(srv)
	}
}

func (m *Manager) registerTools(srv *Server) {
	m.reg.RemoveSource("mcp:" + srv.Name)
	for _, ti := range srv.tools {
		ti := ti
		full := "mcp__" + sanitize(srv.Name) + "__" + ti.Name
		schema := ti.InputSchema
		if schema == nil {
			schema = map[string]any{"type": "object"}
		}
		m.reg.Add(&tools.Tool{
			Name: full, Description: clip(ti.Description, 1024), Schema: schema,
			Category: "mcp", Mutates: true, Source: "mcp:" + srv.Name,
			Run: func(ctx context.Context, env *tools.Env, args json.RawMessage) tools.Result {
				return m.callTool(ctx, srv.Name, ti.Name, args)
			},
		})
	}
}

func (m *Manager) callTool(ctx context.Context, server, tool string, args json.RawMessage) tools.Result {
	m.mu.Lock()
	srv := m.servers[server]
	m.mu.Unlock()
	if srv == nil || srv.client == nil {
		return tools.Errorf("MCP server %q is not connected", server)
	}
	var argv any
	if len(args) > 0 {
		json.Unmarshal(args, &argv)
	}
	cctx, cancel := context.WithTimeout(ctx, timeoutOf(srv.cfg))
	defer cancel()
	raw, err := srv.client.call(cctx, "tools/call", map[string]any{"name": tool, "arguments": argv})
	if err != nil {
		return tools.Errorf("mcp %s/%s: %v", server, tool, err)
	}
	var res struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	json.Unmarshal(raw, &res)
	var b strings.Builder
	for _, c := range res.Content {
		if c.Text != "" {
			b.WriteString(c.Text)
			b.WriteString("\n")
		}
	}
	out := strings.TrimSpace(b.String())
	if out == "" {
		out = string(raw)
	}
	return tools.Result{Text: out, IsError: res.IsError, Untrusted: "mcp:" + server}
}

func (s *Server) connect(ctx context.Context) error {
	var tr transport
	var err error
	if s.cfg.URL != "" {
		tr = newHTTPTransport(s.cfg.URL, s.cfg.Headers)
	} else if s.cfg.Command != "" {
		tr, err = newStdioTransport(ctx, s.cfg.Command, s.cfg.Args, s.cfg.Env)
		if err != nil {
			return err
		}
	} else {
		return fmt.Errorf("server %q has neither command nor url", s.Name)
	}
	s.client = tr
	ictx, cancel := context.WithTimeout(ctx, timeoutOf(s.cfg))
	defer cancel()
	if _, err := tr.call(ictx, "initialize", map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "allones", "version": "1.0"},
	}); err != nil {
		tr.close()
		return fmt.Errorf("initialize: %w", err)
	}
	tr.notify("notifications/initialized", map[string]any{})
	raw, err := tr.call(ictx, "tools/list", map[string]any{})
	if err != nil {
		tr.close()
		return fmt.Errorf("tools/list: %w", err)
	}
	var tl struct {
		Tools []ToolInfo `json:"tools"`
	}
	json.Unmarshal(raw, &tl)
	s.tools = tl.Tools
	return nil
}

func (s *Server) close() {
	if s.client != nil {
		s.client.close()
	}
}

// Status reports each server for the dashboard.
func (m *Manager) Status() []map[string]any {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []map[string]any
	for _, s := range m.servers {
		var names []string
		for _, t := range s.tools {
			names = append(names, t.Name)
		}
		out = append(out, map[string]any{
			"name": s.Name, "connected": s.err == "" && s.client != nil,
			"error": s.err, "tools": names, "tool_count": len(names),
		})
	}
	return out
}

func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.servers {
		s.close()
	}
}

func timeoutOf(c config.MCPServer) time.Duration {
	if c.Timeout > 0 {
		return time.Duration(c.Timeout) * time.Second
	}
	return 60 * time.Second
}

func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' {
			return r
		}
		return '_'
	}, s)
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// ---------- JSON-RPC envelope ----------

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// ---------- stdio transport ----------

type stdioTransport struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	mu     sync.Mutex
	id     int
	pending map[int]chan rpcResponse
	closed  bool
}

func newStdioTransport(ctx context.Context, command string, args []string, env map[string]string) (*stdioTransport, error) {
	cmd := exec.Command(command, args...)
	cmd.Env = childEnv(env)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	t := &stdioTransport{cmd: cmd, stdin: stdin, pending: map[int]chan rpcResponse{}}
	go t.readLoop(stdout)
	return t, nil
}

func (t *stdioTransport) readLoop(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 8<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || !strings.HasPrefix(line, "{") {
			continue
		}
		var resp rpcResponse
		if json.Unmarshal([]byte(line), &resp) != nil {
			continue
		}
		t.mu.Lock()
		ch := t.pending[resp.ID]
		delete(t.pending, resp.ID)
		t.mu.Unlock()
		if ch != nil {
			ch <- resp
		}
	}
}

func (t *stdioTransport) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil, fmt.Errorf("transport closed")
	}
	t.id++
	id := t.id
	ch := make(chan rpcResponse, 1)
	t.pending[id] = ch
	t.mu.Unlock()
	body, _ := json.Marshal(rpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params})
	if _, err := t.stdin.Write(append(body, '\n')); err != nil {
		return nil, err
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case resp := <-ch:
		if resp.Error != nil {
			return nil, fmt.Errorf("%s (code %d)", resp.Error.Message, resp.Error.Code)
		}
		return resp.Result, nil
	}
}

func (t *stdioTransport) notify(method string, params any) error {
	body, _ := json.Marshal(rpcRequest{JSONRPC: "2.0", Method: method, Params: params})
	t.mu.Lock()
	defer t.mu.Unlock()
	_, err := t.stdin.Write(append(body, '\n'))
	return err
}

func (t *stdioTransport) close() {
	t.mu.Lock()
	t.closed = true
	t.mu.Unlock()
	t.stdin.Close()
	if t.cmd.Process != nil {
		t.cmd.Process.Kill()
	}
}

// ---------- streamable HTTP transport ----------

type httpTransport struct {
	url     string
	headers map[string]string
	hc      *http.Client
	mu      sync.Mutex
	id      int
	session string
}

func newHTTPTransport(url string, headers map[string]string) *httpTransport {
	return &httpTransport{url: url, headers: headers, hc: &http.Client{Timeout: 120 * time.Second}}
}

func (t *httpTransport) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	t.mu.Lock()
	t.id++
	id := t.id
	t.mu.Unlock()
	body, _ := json.Marshal(rpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params})
	req, _ := http.NewRequestWithContext(ctx, "POST", t.url, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if t.session != "" {
		req.Header.Set("Mcp-Session-Id", t.session)
	}
	for k, v := range t.headers {
		req.Header.Set(k, v)
	}
	resp, err := t.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
		t.session = sid
	}
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	// The response may be plain JSON or an SSE stream; extract the JSON-RPC object with our id.
	if ct := resp.Header.Get("Content-Type"); strings.Contains(ct, "event-stream") {
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if after, ok := strings.CutPrefix(line, "data:"); ok {
				var r rpcResponse
				if json.Unmarshal([]byte(strings.TrimSpace(after)), &r) == nil && r.ID == id {
					return rpcResult(r)
				}
			}
		}
		return nil, fmt.Errorf("no matching response in stream")
	}
	var r rpcResponse
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("decode: %v (%s)", err, clip(string(data), 200))
	}
	return rpcResult(r)
}

func rpcResult(r rpcResponse) (json.RawMessage, error) {
	if r.Error != nil {
		return nil, fmt.Errorf("%s (code %d)", r.Error.Message, r.Error.Code)
	}
	return r.Result, nil
}

func (t *httpTransport) notify(method string, params any) error {
	body, _ := json.Marshal(rpcRequest{JSONRPC: "2.0", Method: method, Params: params})
	req, _ := http.NewRequest("POST", t.url, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if t.session != "" {
		req.Header.Set("Mcp-Session-Id", t.session)
	}
	for k, v := range t.headers {
		req.Header.Set(k, v)
	}
	resp, err := t.hc.Do(req)
	if err == nil {
		resp.Body.Close()
	}
	return err
}

func (t *httpTransport) close() {}
