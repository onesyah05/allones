package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

// ShellSessions remembers each session's working directory so `cd` persists.
type ShellSessions struct {
	mu  sync.Mutex
	cwd map[string]string
}

func NewShellSessions() *ShellSessions { return &ShellSessions{cwd: map[string]string{}} }

func (s *ShellSessions) Get(id, def string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c, ok := s.cwd[id]; ok {
		if st, err := os.Stat(c); err == nil && st.IsDir() {
			return c
		}
	}
	return def
}

func (s *ShellSessions) Set(id, dir string) {
	s.mu.Lock()
	s.cwd[id] = dir
	s.mu.Unlock()
}

// ShellCommand builds the interpreter invocation for the configured shell.
func ShellCommand(ctx context.Context, shell, script string) (*exec.Cmd, string) {
	kind := shell
	if kind == "" {
		if runtime.GOOS == "windows" {
			if _, err := exec.LookPath("pwsh"); err == nil {
				kind = "pwsh"
			} else {
				kind = "powershell"
			}
		} else if _, err := exec.LookPath("bash"); err == nil {
			kind = "bash"
		} else {
			kind = "sh"
		}
	}
	switch kind {
	case "pwsh", "powershell":
		return exec.CommandContext(ctx, kind, "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", script), kind
	case "cmd":
		return exec.CommandContext(ctx, "cmd", "/C", script), kind
	default:
		return exec.CommandContext(ctx, kind, "-c", script), kind
	}
}

const cwdMarker = "__ALLONES_CWD__"

func wrapForCwd(kind, cmd string) string {
	switch kind {
	case "pwsh", "powershell":
		return "$ErrorActionPreference='Continue'; " + cmd + "\n$__ec = if ($?) { if ($LASTEXITCODE) { $LASTEXITCODE } else { 0 } } else { if ($LASTEXITCODE) { $LASTEXITCODE } else { 1 } }\nWrite-Output \"`n" + cwdMarker + "$((Get-Location).Path)\"; exit $__ec"
	case "cmd":
		return cmd + " & echo. & echo " + cwdMarker + "%CD%"
	}
	return cmd + "\n__ec=$?\nprintf '\\n" + cwdMarker + "%s\\n' \"$(pwd)\"\nexit $__ec"
}

func registerShell(r *Registry) {
	r.Add(&Tool{Name: "shell", Category: "shell", Mutates: true,
		Description: "Run a shell command in the workspace (PowerShell on Windows, bash elsewhere). The working directory persists between calls in a session. Use for builds, tests, git, package managers and installed CLI tools. For long-running servers use the process tool instead.",
		Schema: Obj(map[string]any{"command": Str("Command line to run."), "timeout": Int("Seconds before the command is killed (default from config, max 1800)."),
			"cwd": Str("Optional working directory (workspace-relative).")}, "command"),
		Run: func(ctx context.Context, env *Env, raw json.RawMessage) Result {
			var a struct {
				Command string
				Timeout int
				Cwd     string
			}
			if err := Bind(raw, &a); err != nil {
				return Errorf("%v", err)
			}
			if strings.TrimSpace(a.Command) == "" {
				return Errorf("command is empty")
			}
			if res, blocked := guardCommand(ctx, env, "shell", a.Command); blocked {
				return res
			}
			to := a.Timeout
			if to <= 0 {
				to = env.Cfg.Tools.ShellTimeout
			}
			if to > 1800 {
				to = 1800
			}
			dir := env.Services.Shells.Get(env.SessionID, env.Workspace)
			if a.Cwd != "" {
				d, err := env.Resolve(a.Cwd)
				if err != nil {
					return Errorf("%v", err)
				}
				dir = d
			}
			os.MkdirAll(dir, 0o755)
			cctx, cancel := context.WithTimeout(ctx, time.Duration(to)*time.Second)
			defer cancel()
			cmd, kind := ShellCommand(cctx, env.Cfg.Tools.Shell, "")
			script := wrapForCwd(kind, a.Command)
			cmd.Args[len(cmd.Args)-1] = script
			cmd.Dir = dir
			cmd.Env = toolEnv(env)
			setProcGroup(cmd)
			cmd.Cancel = func() error { return killTree(cmd) }
			cmd.WaitDelay = 3 * time.Second
			var buf syncBuffer
			cmd.Stdout, cmd.Stderr = &buf, &buf
			start := time.Now()
			err := cmd.Run()
			out := buf.String()
			if i := strings.LastIndex(out, cwdMarker); i >= 0 {
				nd := strings.TrimSpace(out[i+len(cwdMarker):])
				out = strings.TrimRight(out[:i], "\r\n ")
				if nd != "" {
					if _, rerr := ResolvePath(env.Workspace, nd); rerr == nil {
						env.Services.Shells.Set(env.SessionID, nd)
					}
				}
			}
			code := 0
			if cmd.ProcessState != nil {
				code = cmd.ProcessState.ExitCode()
			}
			meta := map[string]any{"exit_code": code, "duration_ms": time.Since(start).Milliseconds(), "cwd": dir}
			if cctx.Err() == context.DeadlineExceeded {
				return Result{Text: Truncate(out, env.Cfg.Tools.MaxOutputChars) + fmt.Sprintf("\n[killed after %ds timeout; use process for long-running commands]", to), IsError: true, Meta: meta}
			}
			if out == "" {
				out = "(no output)"
			}
			res := Result{Text: Truncate(out, env.Cfg.Tools.MaxOutputChars), Meta: meta, Untrusted: "shell"}
			if err != nil && code != 0 {
				res.Text += fmt.Sprintf("\n[exit code %d]", code)
				res.IsError = true
			} else if err != nil {
				res.Text += "\n[error: " + err.Error() + "]"
				res.IsError = true
			}
			return res
		}})

	r.Add(&Tool{Name: "process", Category: "shell", Mutates: true,
		Description: "Manage background processes (dev servers, watchers, long scans). action=start runs command detached; status/log shows recent output; kill stops it; list shows all.",
		Schema: Obj(map[string]any{"action": Enum("Operation.", "start", "list", "log", "kill"), "command": Str("Command for start."),
			"id": Str("Process id for log/kill."), "tail": Int("Lines of log to show (default 80).")}, "action"),
		Run: func(ctx context.Context, env *Env, raw json.RawMessage) Result {
			var a struct {
				Action, Command, ID string
				Tail                int
			}
			Bind(raw, &a)
			pt := env.Services.Procs
			switch a.Action {
			case "start":
				if res, blocked := guardCommand(ctx, env, "process", a.Command); blocked {
					return res
				}
				dir := env.Services.Shells.Get(env.SessionID, env.Workspace)
				p, err := pt.Start(env, a.Command, dir)
				if err != nil {
					return Errorf("%v", err)
				}
				time.Sleep(1500 * time.Millisecond)
				return Text(fmt.Sprintf("started %s (pid %d)\n%s", p.ID, p.PID, p.Tail(30)))
			case "log", "status":
				p := pt.Get(a.ID)
				if p == nil {
					return Errorf("no process %q", a.ID)
				}
				if a.Tail <= 0 {
					a.Tail = 80
				}
				return Result{Text: fmt.Sprintf("%s [%s] %s\n%s", p.ID, p.State(), p.Command, p.Tail(a.Tail)), Untrusted: "process"}
			case "kill":
				if err := pt.Kill(a.ID); err != nil {
					return Errorf("%v", err)
				}
				return Text("killed " + a.ID)
			default:
				list := pt.List()
				if len(list) == 0 {
					return Text("no background processes")
				}
				var out []string
				for _, p := range list {
					out = append(out, fmt.Sprintf("%s  pid=%d  %s  %s", p.ID, p.PID, p.State(), p.Command))
				}
				return Text(strings.Join(out, "\n"))
			}
		}})
}

// guardCommand forces approval for destructive patterns regardless of approval mode.
func guardCommand(ctx context.Context, env *Env, tool, cmd string) (Result, bool) {
	g := env.Services.CommandGuard
	if g == nil {
		return Result{}, false
	}
	pat, bad := g.Dangerous(cmd)
	if !bad {
		return Result{}, false
	}
	env.Store.Audit("agent:"+env.SessionID, "command.dangerous", tool, cmd, "warn")
	if env.Interactive && env.Services.Asker != nil &&
		env.Services.Asker.Approve(ctx, env, tool, "Potentially destructive command", fmt.Sprintf("%s\n\nmatched guard pattern: %s", cmd, pat)) {
		return Result{}, false
	}
	return Errorf("refused: command matches a destructive pattern (%s). Ask the operator to run it themselves or choose a safer command.", pat), true
}

func toolEnv(env *Env) []string {
	e := os.Environ()
	// Never leak the server's own provider credentials into child processes.
	var out []string
	for _, kv := range e {
		k, _, _ := strings.Cut(kv, "=")
		switch strings.ToUpper(k) {
		case "ANTHROPIC_API_KEY", "OPENAI_API_KEY", "GEMINI_API_KEY", "OPENROUTER_API_KEY", "ALLONES_TOKEN", "GROQ_API_KEY", "DEEPSEEK_API_KEY":
			continue
		}
		out = append(out, kv)
	}
	if extra := env.Cfg.Security.ToolsPath; extra != "" {
		for i, kv := range out {
			if strings.HasPrefix(strings.ToUpper(kv), "PATH=") {
				out[i] = kv[:5] + extra + string(os.PathListSeparator) + kv[5:]
			}
		}
	}
	home, _ := os.UserHomeDir()
	gobin := filepath.Join(home, "go", "bin")
	for i, kv := range out {
		if strings.HasPrefix(strings.ToUpper(kv), "PATH=") && !strings.Contains(kv, gobin) {
			out[i] = kv + string(os.PathListSeparator) + gobin
		}
	}
	return append(out, "ALLONES_SESSION="+env.SessionID, "NO_COLOR=1", "TERM=dumb")
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.b.Len() > 4<<20 { // cap memory; keep the head
		return len(p), nil
	}
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// ---------- background processes ----------

type Proc struct {
	ID, Command, Session string
	PID                  int
	Started              time.Time
	cmd                  *exec.Cmd
	mu                   sync.Mutex
	lines                []string
	partial              string
	done                 bool
	exit                 int
}

func (p *Proc) Write(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	s := p.partial + string(b)
	parts := strings.Split(s, "\n")
	p.partial = parts[len(parts)-1]
	p.lines = append(p.lines, parts[:len(parts)-1]...)
	if len(p.lines) > 4000 {
		p.lines = p.lines[len(p.lines)-3000:]
	}
	return len(b), nil
}

func (p *Proc) Tail(n int) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	l := p.lines
	if p.partial != "" {
		l = append(append([]string{}, l...), p.partial)
	}
	if len(l) > n {
		l = l[len(l)-n:]
	}
	return strings.Join(l, "\n")
}

func (p *Proc) State() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.done {
		return fmt.Sprintf("exited %d", p.exit)
	}
	return "running " + time.Since(p.Started).Round(time.Second).String()
}

type ProcessTable struct {
	mu    sync.Mutex
	procs map[string]*Proc
	n     int
}

func NewProcessTable() *ProcessTable { return &ProcessTable{procs: map[string]*Proc{}} }

func (t *ProcessTable) Start(env *Env, command, dir string) (*Proc, error) {
	cmd, _ := ShellCommand(context.Background(), env.Cfg.Tools.Shell, command)
	cmd.Dir = dir
	cmd.Env = toolEnv(env)
	setProcGroup(cmd)
	t.mu.Lock()
	t.n++
	p := &Proc{ID: fmt.Sprintf("p%d", t.n), Command: command, Session: env.SessionID, Started: time.Now(), cmd: cmd}
	t.procs[p.ID] = p
	t.mu.Unlock()
	cmd.Stdout, cmd.Stderr = p, p
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	p.PID = cmd.Process.Pid
	go func() {
		cmd.Wait()
		p.mu.Lock()
		p.done = true
		p.exit = cmd.ProcessState.ExitCode()
		p.mu.Unlock()
	}()
	return p, nil
}

func (t *ProcessTable) Get(id string) *Proc {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.procs[id]
}

func (t *ProcessTable) List() []*Proc {
	t.mu.Lock()
	defer t.mu.Unlock()
	var out []*Proc
	for _, p := range t.procs {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Started.Before(out[j].Started) })
	return out
}

func (t *ProcessTable) Kill(id string) error {
	p := t.Get(id)
	if p == nil {
		return fmt.Errorf("no process %q", id)
	}
	return killTree(p.cmd)
}

func (t *ProcessTable) KillAll() {
	for _, p := range t.List() {
		killTree(p.cmd)
	}
}
