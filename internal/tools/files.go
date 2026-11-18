package tools

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const readCap = 400 << 10

var skipDirs = map[string]bool{".git": true, "node_modules": true, ".venv": true, "venv": true, "__pycache__": true, "dist": true, "build": true, ".next": true, "target": true, ".idea": true, ".cache": true, "vendor": true}

func registerFiles(r *Registry) {
	r.Add(&Tool{Name: "read_file", Category: "files",
		Description: "Read a text file from the workspace. Returns a header '<path> — lines a-b of n' then the exact bytes. Use offset/limit for large files. Prefer this over shell cat.",
		Schema: Obj(map[string]any{"path": Str("File path, relative to the workspace."), "offset": Int("First line (1-based)."), "limit": Int("Max lines, default 2000.")}, "path"),
		Run: func(ctx context.Context, env *Env, raw json.RawMessage) Result {
			var a struct {
				Path          string
				Offset, Limit int
			}
			if err := Bind(raw, &a); err != nil {
				return Errorf("%v", err)
			}
			p, err := env.Resolve(a.Path)
			if err != nil {
				return Errorf("%v", err)
			}
			f, err := os.Open(p)
			if err != nil {
				return Errorf("%v", err)
			}
			defer f.Close()
			if st, _ := f.Stat(); st != nil && st.IsDir() {
				return Errorf("%s is a directory; use list_files", a.Path)
			}
			head := make([]byte, 8000)
			n, _ := f.Read(head)
			if bytes.IndexByte(head[:n], 0) >= 0 {
				return Errorf("%s looks binary; use binary_inspect or hexdump via shell", a.Path)
			}
			f.Seek(0, 0)
			if a.Offset < 1 {
				a.Offset = 1
			}
			if a.Limit <= 0 {
				a.Limit = 2000
			}
			br := bufio.NewReaderSize(f, 64<<10)
			var out strings.Builder
			line, total, first, last, bytesRead := 0, 0, 0, 0, 0
			truncated := false
			for {
				s, err := br.ReadString('\n')
				if s != "" {
					line++
					total = line
					bytesRead += len(s)
					if line >= a.Offset && line < a.Offset+a.Limit && !truncated {
						if out.Len()+len(s) > readCap {
							truncated = true
						} else {
							if first == 0 {
								first = line
							}
							last = line
							out.WriteString(s)
						}
					}
				}
				if err != nil || bytesRead > 8<<20 {
					break
				}
			}
			if first == 0 && total > 0 && a.Offset > total {
				return Errorf("offset %d is past the end (%d lines)", a.Offset, total)
			}
			hdr := fmt.Sprintf("%s — lines %d-%d of %d\n\n", a.Path, first, last, total)
			body := out.String()
			if last < total && !truncated {
				body += fmt.Sprintf("\n… %d more lines (use offset=%d to continue)", total-last, last+1)
			}
			if truncated {
				body += fmt.Sprintf("\n… file truncated at 400 KB (use offset=%d)", last+1)
			}
			return Result{Text: hdr + body, Meta: map[string]any{"path": a.Path, "first_line": first, "last_line": last, "total_lines": total}}
		}})

	r.Add(&Tool{Name: "write_file", Category: "files", Mutates: true,
		Description: "Create or overwrite a file in the workspace (parent directories are created). The previous content is snapshotted for /rollback.",
		Schema:      Obj(map[string]any{"path": Str("File path."), "content": Str("Full file content.")}, "path", "content"),
		Run: func(ctx context.Context, env *Env, raw json.RawMessage) Result {
			var a struct{ Path, Content string }
			if err := Bind(raw, &a); err != nil {
				return Errorf("%v", err)
			}
			p, err := env.Resolve(a.Path)
			if err != nil {
				return Errorf("%v", err)
			}
			snapshot(env, p)
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				return Errorf("%v", err)
			}
			if err := atomicWrite(p, []byte(a.Content)); err != nil {
				return Errorf("%v", err)
			}
			return Text(fmt.Sprintf("wrote %s (%d bytes, %d lines)", a.Path, len(a.Content), strings.Count(a.Content, "\n")+1))
		}})

	r.Add(&Tool{Name: "edit_file", Category: "files", Mutates: true,
		Description: "Replace an exact string in a file. old_string must match exactly once unless replace_all is true. If it is not found, re-read the file rather than guessing.",
		Schema: Obj(map[string]any{"path": Str("File path."), "old_string": Str("Exact text to replace (byte for byte)."),
			"new_string": Str("Replacement text."), "replace_all": Bool("Replace every occurrence.")}, "path", "old_string", "new_string"),
		Run: func(ctx context.Context, env *Env, raw json.RawMessage) Result {
			var a struct {
				Path, OldString, NewString string
				ReplaceAll                 bool
			}
			var m map[string]any
			json.Unmarshal(raw, &m)
			if err := Bind(raw, &a); err != nil {
				return Errorf("%v", err)
			}
			a.OldString, _ = m["old_string"].(string)
			a.NewString, _ = m["new_string"].(string)
			a.ReplaceAll, _ = m["replace_all"].(bool)
			p, err := env.Resolve(a.Path)
			if err != nil {
				return Errorf("%v", err)
			}
			b, err := os.ReadFile(p)
			if err != nil {
				return Errorf("%v", err)
			}
			if a.OldString == "" {
				return Errorf("old_string is empty; use write_file to create a file")
			}
			content := string(b)
			old, nw := a.OldString, a.NewString
			note := ""
			count := strings.Count(content, old)
			if count == 0 && strings.Contains(content, "\r\n") && !strings.Contains(old, "\r\n") && strings.Contains(old, "\n") {
				old2 := strings.ReplaceAll(old, "\n", "\r\n")
				if c := strings.Count(content, old2); c > 0 {
					old, nw, count = old2, strings.ReplaceAll(nw, "\n", "\r\n"), c
					note = " (matched after converting LF to the file's CRLF line endings)"
				}
			}
			switch {
			case count == 0:
				return Errorf("old_string not found in %s. Re-read the file (read_file) and copy the anchor exactly.", a.Path)
			case count > 1 && !a.ReplaceAll:
				return Errorf("old_string matches %d places in %s; add surrounding context to make it unique or set replace_all", count, a.Path)
			}
			snapshot(env, p)
			if a.ReplaceAll {
				content = strings.ReplaceAll(content, old, nw)
			} else {
				content = strings.Replace(content, old, nw, 1)
			}
			if err := atomicWrite(p, []byte(content)); err != nil {
				return Errorf("%v", err)
			}
			return Text(fmt.Sprintf("edited %s: %d replacement(s)%s", a.Path, map[bool]int{true: count, false: 1}[a.ReplaceAll], note))
		}})

	r.Add(&Tool{Name: "list_files", Category: "files",
		Description: "List directory entries (dirs end with /). recursive walks subdirectories, skipping .git, node_modules and build output.",
		Schema:      Obj(map[string]any{"path": Str("Directory, default workspace root."), "recursive": Bool("Walk subdirectories."), "max": Int("Max entries, default 500.")}),
		Run: func(ctx context.Context, env *Env, raw json.RawMessage) Result {
			var a struct {
				Path      string
				Recursive bool
				Max       int
			}
			Bind(raw, &a)
			if a.Max <= 0 {
				a.Max = 500
			}
			root, err := env.Resolve(a.Path)
			if err != nil {
				return Errorf("%v", err)
			}
			var out []string
			if !a.Recursive {
				ents, err := os.ReadDir(root)
				if err != nil {
					return Errorf("%v", err)
				}
				for _, e := range ents {
					n := e.Name()
					if e.IsDir() {
						n += "/"
					} else if info, err := e.Info(); err == nil {
						n += fmt.Sprintf("  (%s)", human(info.Size()))
					}
					out = append(out, n)
				}
			} else {
				filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
					if err != nil || len(out) >= a.Max {
						return filepath.SkipDir
					}
					if d.IsDir() && skipDirs[d.Name()] && p != root {
						return filepath.SkipDir
					}
					rel, _ := filepath.Rel(root, p)
					if rel == "." {
						return nil
					}
					if d.IsDir() {
						rel += "/"
					}
					out = append(out, filepath.ToSlash(rel))
					return nil
				})
			}
			if len(out) == 0 {
				return Text("(empty)")
			}
			s := strings.Join(out, "\n")
			if len(out) >= a.Max {
				s += fmt.Sprintf("\n… stopped at %d entries", a.Max)
			}
			return Text(s)
		}})

	r.Add(&Tool{Name: "glob", Category: "files",
		Description: "Find files by glob pattern (supports ** for any depth, e.g. **/*.go), newest first.",
		Schema:      Obj(map[string]any{"pattern": Str("Glob pattern."), "path": Str("Base directory.")}, "pattern"),
		Run: func(ctx context.Context, env *Env, raw json.RawMessage) Result {
			var a struct{ Pattern, Path string }
			if err := Bind(raw, &a); err != nil {
				return Errorf("%v", err)
			}
			root, err := env.Resolve(a.Path)
			if err != nil {
				return Errorf("%v", err)
			}
			re, err := globRegexp(a.Pattern)
			if err != nil {
				return Errorf("bad pattern: %v", err)
			}
			type hit struct {
				p string
				t time.Time
			}
			var hits []hit
			filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
				if err != nil {
					return nil
				}
				if d.IsDir() {
					if skipDirs[d.Name()] && p != root {
						return filepath.SkipDir
					}
					return nil
				}
				rel, _ := filepath.Rel(root, p)
				rel = filepath.ToSlash(rel)
				if re.MatchString(rel) {
					info, _ := d.Info()
					var t time.Time
					if info != nil {
						t = info.ModTime()
					}
					hits = append(hits, hit{rel, t})
				}
				if len(hits) > 5000 {
					return fs.SkipAll
				}
				return nil
			})
			sort.Slice(hits, func(i, j int) bool { return hits[i].t.After(hits[j].t) })
			var out []string
			for i, h := range hits {
				if i >= 300 {
					out = append(out, fmt.Sprintf("… %d more", len(hits)-300))
					break
				}
				out = append(out, h.p)
			}
			if len(out) == 0 {
				return Text("no matches")
			}
			return Text(strings.Join(out, "\n"))
		}})

	r.Add(&Tool{Name: "grep", Category: "files",
		Description: "Search file contents with a regular expression (RE2). Returns path:line: text. Filter with include glob (e.g. *.ts).",
		Schema: Obj(map[string]any{"pattern": Str("Regular expression."), "path": Str("File or directory."), "include": Str("Glob filter on file names."),
			"ignore_case": Bool("Case-insensitive."), "max": Int("Max matches, default 200."), "context": Int("Lines of context around each match.")}, "pattern"),
		Run: func(ctx context.Context, env *Env, raw json.RawMessage) Result {
			var a struct {
				Pattern, Path, Include string
				IgnoreCase             bool `json:"ignore_case"`
				Max, Context           int
			}
			if err := Bind(raw, &a); err != nil {
				return Errorf("%v", err)
			}
			if a.Max <= 0 {
				a.Max = 200
			}
			pat := a.Pattern
			if a.IgnoreCase {
				pat = "(?i)" + pat
			}
			re, err := regexp.Compile(pat)
			if err != nil {
				return Errorf("bad regexp: %v", err)
			}
			var inc *regexp.Regexp
			if a.Include != "" {
				inc, _ = globRegexp(a.Include)
			}
			root, err := env.Resolve(a.Path)
			if err != nil {
				return Errorf("%v", err)
			}
			var out []string
			count := 0
			filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
				if err != nil || ctx.Err() != nil {
					return nil
				}
				if d.IsDir() {
					if skipDirs[d.Name()] && p != root {
						return filepath.SkipDir
					}
					return nil
				}
				if inc != nil && !inc.MatchString(d.Name()) {
					return nil
				}
				info, _ := d.Info()
				if info == nil || info.Size() > 8<<20 {
					return nil
				}
				b, err := os.ReadFile(p)
				if err != nil || bytes.IndexByte(b[:min(len(b), 8000)], 0) >= 0 {
					return nil
				}
				rel, _ := filepath.Rel(env.Workspace, p)
				lines := strings.Split(string(b), "\n")
				for i, l := range lines {
					if re.MatchString(l) {
						count++
						if len(out) < a.Max {
							if a.Context > 0 {
								lo, hi := max(0, i-a.Context), min(len(lines), i+a.Context+1)
								for j := lo; j < hi; j++ {
									sep := "-"
									if j == i {
										sep = ":"
									}
									out = append(out, fmt.Sprintf("%s%s%d%s %s", filepath.ToSlash(rel), sep, j+1, sep, strings.TrimRight(clip(lines[j], 400), "\r")))
								}
								out = append(out, "--")
							} else {
								out = append(out, fmt.Sprintf("%s:%d: %s", filepath.ToSlash(rel), i+1, strings.TrimRight(clip(l, 400), "\r")))
							}
						}
					}
				}
				if count > a.Max*5 {
					return fs.SkipAll
				}
				return nil
			})
			if count == 0 {
				return Text("no matches")
			}
			s := strings.Join(out, "\n")
			if count > a.Max {
				s += fmt.Sprintf("\n… %d matches total, showing %d", count, a.Max)
			}
			return Text(s)
		}})

	r.Add(&Tool{Name: "rollback", Category: "files", Mutates: true,
		Description: "Undo file changes made in this session. action=list shows changed files; action=restore with path restores one, or path='all' restores everything to its pre-session state.",
		Schema:      Obj(map[string]any{"action": Enum("What to do.", "list", "restore"), "path": Str("File path or 'all'.")}, "action"),
		Run: func(ctx context.Context, env *Env, raw json.RawMessage) Result {
			var a struct{ Action, Path string }
			Bind(raw, &a)
			snaps, err := env.Store.Snapshots(env.SessionID)
			if err != nil {
				return Errorf("%v", err)
			}
			if a.Action != "restore" {
				if len(snaps) == 0 {
					return Text("no file changes recorded in this session")
				}
				var out []string
				for _, s := range snaps {
					state := "modified"
					if !s.Existed {
						state = "created"
					}
					out = append(out, state+"  "+s.Path)
				}
				return Text(strings.Join(out, "\n"))
			}
			var done []string
			for _, s := range snaps {
				if a.Path != "all" {
					p, _ := env.Resolve(a.Path)
					if p != s.Path {
						continue
					}
				}
				if s.Existed {
					os.MkdirAll(filepath.Dir(s.Path), 0o755)
					if err := atomicWrite(s.Path, s.Content); err != nil {
						return Errorf("restore %s: %v", s.Path, err)
					}
				} else {
					os.Remove(s.Path)
				}
				env.Store.DropSnapshot(env.SessionID, s.Path)
				done = append(done, s.Path)
			}
			if len(done) == 0 {
				return Errorf("nothing to restore for %q", a.Path)
			}
			return Text("restored:\n" + strings.Join(done, "\n"))
		}})
}

func snapshot(env *Env, p string) {
	if env.Store == nil || env.SessionID == "" {
		return
	}
	b, err := os.ReadFile(p)
	env.Store.SnapshotOnce(env.SessionID, p, err == nil, b)
}

func atomicWrite(p string, b []byte) error {
	mode := os.FileMode(0o644)
	if st, err := os.Stat(p); err == nil {
		mode = st.Mode().Perm()
	}
	tmp := p + ".allones-tmp"
	if err := os.WriteFile(tmp, b, mode); err != nil {
		return err
	}
	if err := os.Rename(tmp, p); err != nil {
		os.Remove(tmp)
		return os.WriteFile(p, b, mode)
	}
	return nil
}

func globRegexp(pat string) (*regexp.Regexp, error) {
	pat = filepath.ToSlash(pat)
	var b strings.Builder
	b.WriteString("^")
	if !strings.Contains(pat, "/") {
		b.WriteString("(?:.*/)?")
	}
	for i := 0; i < len(pat); i++ {
		c := pat[i]
		switch c {
		case '*':
			if i+1 < len(pat) && pat[i+1] == '*' {
				i++
				if i+1 < len(pat) && pat[i+1] == '/' {
					i++
					b.WriteString("(?:.*/)?")
				} else {
					b.WriteString(".*")
				}
			} else {
				b.WriteString("[^/]*")
			}
		case '?':
			b.WriteString("[^/]")
		case '{':
			b.WriteString("(?:")
		case '}':
			b.WriteString(")")
		case ',':
			b.WriteString("|")
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	b.WriteString("$")
	return regexp.Compile(b.String())
}

func human(n int64) string {
	switch {
	case n < 1<<10:
		return fmt.Sprintf("%d B", n)
	case n < 1<<20:
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	case n < 1<<30:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	}
	return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
