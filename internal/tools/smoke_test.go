package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/allones/allones/internal/config"
	"github.com/allones/allones/internal/store"
)

// Keep fixtures inside the checkout, rather than the system temp directory. Never
// reuse the running agent's configuration, credentials, database, or workspace.
func smokeEnv(t *testing.T) (*Registry, *Env) {
	t.Helper()
	root, err := os.MkdirTemp(".", "smoke-fixture-")
	if err != nil {
		t.Fatal(err)
	}
	root, err = filepath.Abs(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(root); err != nil {
			t.Error(err)
		}
	})
	db, err := store.Open(filepath.Join(root, "state", "smoke.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	workspace := filepath.Join(root, "workspace")
	if err := os.MkdirAll(workspace, 0700); err != nil {
		t.Fatal(err)
	}
	sess := &store.Session{ID: "smoke-session", Workspace: workspace}
	if err := db.CreateSession(sess); err != nil {
		t.Fatal(err)
	}
	r := Default()
	env := &Env{Cfg: config.Defaults(), Store: db, Workspace: workspace, SessionID: sess.ID,
		Services: &Services{Registry: r, Shells: NewShellSessions(), Procs: NewProcessTable()}}
	return r, env
}

func smokeCall(t *testing.T, r *Registry, env *Env, name string, args map[string]any, wantError bool) Result {
	t.Helper()
	tool := r.Get(name)
	if tool == nil || tool.Run == nil || tool.Schema == nil {
		t.Fatalf("missing registered tool/schema: %s", name)
	}
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	got := tool.Run(ctx, env, raw)
	if got.IsError != wantError {
		t.Fatalf("%s(%s): IsError=%v, want %v: %s", name, raw, got.IsError, wantError, got.Text)
	}
	return got
}

func smokeContains(t *testing.T, got string, want ...string) {
	t.Helper()
	for _, s := range want {
		if !strings.Contains(got, s) {
			t.Fatalf("output %q missing %q", got, s)
		}
	}
}

func TestSmokeFiles(t *testing.T) {
	r, env := smokeEnv(t)
	call := func(name string, args map[string]any, bad bool) Result { return smokeCall(t, r, env, name, args, bad) }
	call("write_file", map[string]any{"path": "nested/sample.txt", "content": "alpha\nbeta\nalpha\n"}, false)
	content := func(want string) {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(env.Workspace, "nested", "sample.txt"))
		if err != nil {
			t.Fatal(err)
		}
		if string(b) != want {
			t.Fatalf("file = %q, want %q", b, want)
		}
	}
	content("alpha\nbeta\nalpha\n")
	got := call("read_file", map[string]any{"path": "nested/sample.txt", "offset": 2, "limit": 1}, false)
	smokeContains(t, got.Text, "lines 2-2 of 3", "\n\nbeta\n")
	for _, old := range []string{"alpha", "missing", ""} {
		call("edit_file", map[string]any{"path": "nested/sample.txt", "old_string": old, "new_string": "oops"}, true)
		content("alpha\nbeta\nalpha\n")
	}
	call("edit_file", map[string]any{"path": "nested/sample.txt", "old_string": "alpha", "new_string": "gamma", "replace_all": true}, false)
	call("edit_file", map[string]any{"path": "nested/sample.txt", "old_string": "beta", "new_string": "BETA"}, false)
	content("gamma\nBETA\ngamma\n")
	call("write_file", map[string]any{"path": "crlf.txt", "content": "one\r\ntwo\r\n"}, false)
	call("edit_file", map[string]any{"path": "crlf.txt", "old_string": "one\ntwo", "new_string": "three\nfour"}, false)
	b, err := os.ReadFile(filepath.Join(env.Workspace, "crlf.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "three\r\nfour\r\n" {
		t.Fatalf("CRLF edit: %q", b)
	}
	call("write_file", map[string]any{"path": "empty.txt", "content": ""}, false)
	call("read_file", map[string]any{"path": "empty.txt"}, false)
	call("write_file", map[string]any{"path": "binary.dat", "content": "abc\x00def"}, false)
	call("read_file", map[string]any{"path": "binary.dat"}, true)
	call("read_file", map[string]any{"path": "nested"}, true)
	call("read_file", map[string]any{"path": "missing.txt"}, true)
	call("write_file", map[string]any{"path": "node_modules/ignored.txt", "content": "BETA"}, false)
	got = call("glob", map[string]any{"pattern": "**/*.txt"}, false)
	smokeContains(t, got.Text, "nested/sample.txt", "crlf.txt", "empty.txt")
	if strings.Contains(got.Text, "ignored") {
		t.Fatalf("glob searched ignored directory: %s", got.Text)
	}
	if got := call("glob", map[string]any{"pattern": "**/*.absent"}, false); got.Text != "no matches" {
		t.Fatal(got.Text)
	}
	got = call("grep", map[string]any{"pattern": "beta", "ignore_case": true, "include": "*.txt", "path": "nested", "context": 1}, false)
	smokeContains(t, got.Text, "sample.txt", "BETA", "gamma")
	call("grep", map[string]any{"pattern": "["}, true)
	got = call("list_files", map[string]any{"path": "nested"}, false)
	smokeContains(t, got.Text, "sample.txt")
	got = call("list_files", map[string]any{"recursive": true}, false)
	smokeContains(t, got.Text, "nested/sample.txt")
	if strings.Contains(got.Text, "ignored.txt") {
		t.Fatalf("recursive listing searched ignored directory: %s", got.Text)
	}
	for _, name := range []string{"read_file", "write_file", "edit_file", "glob", "grep", "list_files"} {
		args := map[string]any{"path": "../escape.txt", "pattern": "*", "content": "must not write", "old_string": "a", "new_string": "b"}
		call(name, args, true)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(env.Workspace), "escape.txt")); !os.IsNotExist(err) {
		t.Fatalf("path escape created a file: %v", err)
	}
}

func TestSmokeCodec(t *testing.T) {
	r, env := smokeEnv(t)
	cases := []struct {
		op, input, key, want string
		bad                  bool
	}{
		{op: "b64encode", input: "hello", want: "aGVsbG8="},
		{op: "b64decode", input: "aGVsbG8=", want: "hello"},
		{op: "b64urldecode", input: "SGVsbG8_", want: "Hello?"},
		{op: "urlencode", input: "a b&c", want: "a+b%26c"},
		{op: "urldecode", input: "a+b%26c", want: "a b&c"},
		{op: "hexencode", input: "hello", want: "68656c6c6f"},
		{op: "hexdecode", input: "68656c6c6f", want: "hello"},
		{op: "htmlencode", input: "<a>&", want: "&lt;a&gt;&amp;"},
		{op: "htmldecode", input: "&lt;a&gt;&amp;", want: "<a>&"},
		{op: "rot13", input: "Hello 123!", want: "Uryyb 123!"},
		{op: "md5", input: "abc", want: "900150983cd24fb0d6963f7d28e17f72"},
		{op: "sha1", input: "abc", want: "a9993e364706816aba3e25717850c26c9cd0d89d"},
		{op: "sha256", input: "abc", want: "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"},
		{op: "sha512", input: "abc", want: "ddaf35a193617abacc417349ae20413112e6fa4e89a97ea20a9eeee64b55d39a2192992a274fc1a836ba3c23a3feebbd454d4423643ce80e2a9ac94fa54ca49f"},
		{op: "hmac-sha256", input: "The quick brown fox jumps over the lazy dog", key: "key", want: "f7bc83f430538424b13298e6aa6fb143ef4d59a14946175997479dbc2d1a3cd8"},
		{op: "b64decode", input: "!!!", bad: true},
		{op: "b64urldecode", input: "!!!", bad: true},
		{op: "hexdecode", input: "xyz", bad: true},
		{op: "urldecode", input: "%GG", bad: true},
		{op: "unknown", bad: true},
	}
	for _, tc := range cases {
		t.Run(tc.op, func(t *testing.T) {
			got := smokeCall(t, r, env, "codec", map[string]any{"op": tc.op, "input": tc.input, "key": tc.key}, tc.bad)
			if !tc.bad && got.Text != tc.want {
				t.Fatalf("got %q, want %q", got.Text, tc.want)
			}
		})
	}
	got := smokeCall(t, r, env, "codec", map[string]any{"op": "identify", "input": "900150983cd24fb0d6963f7d28e17f72"}, false)
	smokeContains(t, got.Text, "MD5")
}

func TestSmokeMemoryTodo(t *testing.T) {
	r, env := smokeEnv(t)
	call := func(name string, args map[string]any, bad bool) Result { return smokeCall(t, r, env, name, args, bad) }
	if got := call("memory", map[string]any{"action": "list"}, false); got.Text != "no memories" {
		t.Fatal(got.Text)
	}
	call("memory", map[string]any{"action": "save", "content": "  "}, true)
	call("memory", map[string]any{"action": "save", "key": "smoke", "content": "Use zebras for smoke fixtures", "tags": "testing"}, false)
	smokeContains(t, call("memory", map[string]any{"action": "search", "query": "zebras"}, false).Text, "[smoke]", "zebras")
	call("memory", map[string]any{"action": "save", "key": "smoke", "content": "Use otters instead"}, false)
	got := call("memory", map[string]any{"action": "list"}, false)
	smokeContains(t, got.Text, "otters")
	if strings.Contains(got.Text, "zebras") {
		t.Fatal("memory overwrite retained old content")
	}
	call("memory", map[string]any{"action": "delete", "key": "smoke"}, false)
	call("memory", map[string]any{"action": "delete", "key": "smoke"}, true)
	if got := call("memory", map[string]any{"action": "list"}, false); got.Text != "no memories" {
		t.Fatal(got.Text)
	}
	if got := call("todo", map[string]any{"action": "get"}, false); got.Text != "(no tasks)" {
		t.Fatal(got.Text)
	}
	items := []map[string]any{{"text": "pending default"}, {"text": "finished", "status": "done"}, {"text": "working", "status": "in_progress"}, {"text": "blocked", "status": "blocked"}}
	call("todo", map[string]any{"action": "set", "items": items}, false)
	smokeContains(t, call("todo", map[string]any{"action": "get"}, false).Text, "[ ] pending default", "[x] finished", "[~] working", "[!] blocked", "(1/4 done)")
	other := *env
	other.SessionID = "other-session"
	if got := smokeCall(t, r, &other, "todo", map[string]any{"action": "get"}, false); got.Text != "(no tasks)" {
		t.Fatal("todo leaked across sessions: " + got.Text)
	}
	call("todo", map[string]any{"action": "set", "items": []TodoItem{{Text: "replacement", Status: "done"}}}, false)
	got = call("todo", map[string]any{"action": "get"}, false)
	smokeContains(t, got.Text, "replacement", "(1/1 done)")
	if strings.Contains(got.Text, "pending default") {
		t.Fatal("todo set appended instead of replacing")
	}
	call("todo", map[string]any{"action": "set", "items": []TodoItem{}}, false)
	if got := call("todo", map[string]any{"action": "get"}, false); got.Text != "(no tasks)" {
		t.Fatal(got.Text)
	}
}

func TestSmokeShellProcess(t *testing.T) {
	r, env := smokeEnv(t)
	// Scripts are fixed constants, contain no network access, and only cd inside
	// this fixture. No inherited provider credential is printed to test output.
	if err := os.Mkdir(filepath.Join(env.Workspace, "child"), 0700); err != nil {
		t.Fatal(err)
	}
	call := func(name string, args map[string]any, bad bool) Result { return smokeCall(t, r, env, name, args, bad) }
	call("shell", map[string]any{"command": " "}, true)
	got := call("shell", map[string]any{"command": "echo smoke-shell", "timeout": 5}, false)
	smokeContains(t, got.Text, "smoke-shell")
	if got.Meta["exit_code"] != 0 {
		t.Fatalf("exit metadata: %#v", got.Meta)
	}
	call("shell", map[string]any{"command": "cd child", "timeout": 5}, false)
	cwd := "pwd"
	failure := "false"
	sleep := "echo smoke-background; sleep 30"
	if runtime.GOOS == "windows" {
		cwd = "(Get-Location).Path"
		failure = "Write-Error 'smoke-failure'"
		sleep = "Write-Output smoke-background; Start-Sleep -Seconds 30"
	}
	got = call("shell", map[string]any{"command": cwd, "timeout": 5}, false)
	smokeContains(t, got.Text, filepath.Join(env.Workspace, "child"))
	if env.Services.Shells.Get("different-session", env.Workspace) != env.Workspace {
		t.Fatal("shell cwd leaked across sessions")
	}
	call("shell", map[string]any{"command": "echo safe", "cwd": "../", "timeout": 5}, true)
	got = call("shell", map[string]any{"command": failure, "timeout": 5}, true)
	if got.Meta["exit_code"] == 0 {
		t.Fatalf("failure exit metadata: %#v", got.Meta)
	}
	if got := call("process", map[string]any{"action": "list"}, false); got.Text != "no background processes" {
		t.Fatal(got.Text)
	}
	call("process", map[string]any{"action": "status", "id": "missing"}, true)
	call("process", map[string]any{"action": "kill", "id": "missing"}, true)
	// Cleanup must terminate a child even if an assertion below fails.
	t.Cleanup(func() {
		for _, p := range env.Services.Procs.List() {
			_ = env.Services.Procs.Kill(p.ID)
			deadline := time.Now().Add(5 * time.Second)
			for p.State() == "running" && time.Now().Before(deadline) {
				time.Sleep(20 * time.Millisecond)
			}
			if p.State() == "running" {
				t.Errorf("child %s did not stop", p.ID)
			}
		}
	})
	call("process", map[string]any{"action": "start", "command": sleep}, false)
	procs := env.Services.Procs.List()
	if len(procs) != 1 {
		t.Fatalf("process count = %d", len(procs))
	}
	id := procs[0].ID
	smokeContains(t, call("process", map[string]any{"action": "list"}, false).Text, id)
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(procs[0].Tail(10), "smoke-background") && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	smokeContains(t, call("process", map[string]any{"action": "log", "id": id, "tail": 10}, false).Text, "smoke-background")
	smokeContains(t, call("process", map[string]any{"action": "status", "id": id}, false).Text, "running")
	call("process", map[string]any{"action": "kill", "id": id}, false)
	deadline = time.Now().Add(5 * time.Second)
	for procs[0].State() == "running" && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if procs[0].State() == "running" {
		t.Fatal("process remained running after kill")
	}
}
