// Package skills is the skill library: embedded built-ins plus user skills
// discovered on disk. A skill is a Markdown file (SKILL.md) with YAML front matter.
package skills

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/allones/allones/internal/tools"
	"gopkg.in/yaml.v3"
)

//go:embed all:library
var embedded embed.FS

type Meta struct {
	Name        string   `yaml:"name"`
	Description string   `yaml:"description"`
	Tags        []string `yaml:"tags"`
	Triggers    []string `yaml:"triggers"`
	Category    string   `yaml:"category"`
}

type skill struct {
	Meta
	source   string // "builtin" or absolute file path
	dir      string // directory for bundled references ("" for flat embedded)
	body     string
	embedded bool
}

type Library struct {
	mu       sync.RWMutex
	skills   map[string]*skill
	dirs     []string
	disabled map[string]bool
}

// New loads embedded skills and discovers user skills under the given dirs plus
// the standard auto-discovery roots.
func New(dirs, disabled []string, autoDiscover bool, home, projectDir string) (*Library, error) {
	l := &Library{skills: map[string]*skill{}, disabled: map[string]bool{}}
	for _, d := range disabled {
		l.disabled[d] = true
	}
	l.loadEmbedded()
	roots := append([]string{}, dirs...)
	if autoDiscover {
		roots = append(roots, discoverRoots(home, projectDir)...)
	}
	for _, d := range roots {
		d = strings.TrimSpace(d)
		if d == "" {
			continue
		}
		l.dirs = append(l.dirs, d)
		l.loadDir(d)
	}
	return l, nil
}

func discoverRoots(home, project string) []string {
	var out []string
	homeSub := []string{".agent/skills", ".agents/skills", ".claude/skills", ".codex/skills", ".config/opencode/skills", ".allones/skills"}
	for _, s := range homeSub {
		out = append(out, filepath.Join(home, filepath.FromSlash(s)))
	}
	if project != "" {
		for _, s := range []string{".agent/skills", ".agents/skills", ".claude/skills", ".github/skills"} {
			out = append(out, filepath.Join(project, filepath.FromSlash(s)))
		}
	}
	return out
}

func (l *Library) loadEmbedded() {
	fs.WalkDir(embedded, "library", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Base(p) != "SKILL.md" {
			return nil
		}
		b, err := embedded.ReadFile(p)
		if err != nil {
			return nil
		}
		if sk := parse(string(b)); sk != nil {
			sk.embedded = true
			sk.source = "builtin"
			sk.dir = filepath.ToSlash(filepath.Dir(p))
			l.skills[sk.Name] = sk
		}
		return nil
	})
}

func (l *Library) loadDir(root string) {
	st, err := os.Stat(root)
	if err != nil || !st.IsDir() {
		return
	}
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if n := d.Name(); n == "node_modules" || n == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		name := d.Name()
		if name != "SKILL.md" && !(strings.HasSuffix(name, ".md") && filepath.Dir(p) == root) {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		if sk := parse(string(b)); sk != nil {
			sk.source = p
			sk.dir = filepath.Dir(p)
			l.mu.Lock()
			l.skills[sk.Name] = sk // user skills override built-ins
			l.mu.Unlock()
		}
		return nil
	})
}

func parse(content string) *skill {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	var meta Meta
	body := content
	if strings.HasPrefix(content, "---\n") {
		if end := strings.Index(content[4:], "\n---"); end >= 0 {
			fm := content[4 : 4+end]
			yaml.Unmarshal([]byte(fm), &meta)
			rest := content[4+end+4:]
			body = strings.TrimLeft(rest, "\n")
		}
	}
	if meta.Name == "" {
		return nil
	}
	return &skill{Meta: meta, body: body}
}

func (l *Library) toInfo(sk *skill) tools.SkillInfo {
	var files []string
	base := ""
	if sk.embedded {
		base = sk.dir
		fs.WalkDir(embedded, sk.dir, func(p string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() && filepath.Base(p) != "SKILL.md" {
				rel, _ := filepath.Rel(base, p)
				files = append(files, filepath.ToSlash(rel))
			}
			return nil
		})
	} else if sk.dir != "" {
		filepath.WalkDir(sk.dir, func(p string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() && filepath.Base(p) != "SKILL.md" && !strings.HasSuffix(p, "LICENSE") {
				rel, _ := filepath.Rel(sk.dir, p)
				files = append(files, filepath.ToSlash(rel))
			}
			return nil
		})
	}
	sort.Strings(files)
	return tools.SkillInfo{Name: sk.Name, Description: sk.Description, Source: sk.source, Tags: sk.Tags, Files: files, Disabled: l.disabled[sk.Name]}
}

func (l *Library) List() []tools.SkillInfo {
	l.mu.RLock()
	defer l.mu.RUnlock()
	var out []tools.SkillInfo
	for _, sk := range l.skills {
		if l.disabled[sk.Name] {
			continue
		}
		out = append(out, l.toInfo(sk))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Catalogue is the compact list for the system prompt: built-in skills always,
// then others up to a cap. The full set is reachable via skill search.
func (l *Library) Catalogue() []tools.SkillInfo {
	const cap = 60
	l.mu.RLock()
	var builtin, other []tools.SkillInfo
	for _, sk := range l.skills {
		if l.disabled[sk.Name] {
			continue
		}
		if sk.embedded {
			builtin = append(builtin, l.toInfo(sk))
		} else {
			other = append(other, l.toInfo(sk))
		}
	}
	l.mu.RUnlock()
	sort.Slice(builtin, func(i, j int) bool { return builtin[i].Name < builtin[j].Name })
	sort.Slice(other, func(i, j int) bool { return other[i].Name < other[j].Name })
	out := builtin
	for _, s := range other {
		if len(out) >= cap {
			break
		}
		out = append(out, s)
	}
	return out
}

// Count returns the total number of enabled skills.
func (l *Library) Count() int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	n := 0
	for _, sk := range l.skills {
		if !l.disabled[sk.Name] {
			n++
		}
	}
	return n
}

func (l *Library) Search(q string, limit int) []tools.SkillInfo {
	words := strings.Fields(strings.ToLower(q))
	type scored struct {
		info  tools.SkillInfo
		score int
	}
	l.mu.RLock()
	var all []scored
	for _, sk := range l.skills {
		if l.disabled[sk.Name] {
			continue
		}
		hay := strings.ToLower(sk.Name + " " + sk.Description + " " + strings.Join(sk.Tags, " ") + " " + strings.Join(sk.Triggers, " ") + " " + sk.body)
		score := 0
		for _, w := range words {
			if strings.Contains(strings.ToLower(sk.Name), w) {
				score += 5
			}
			if strings.Contains(strings.ToLower(sk.Description), w) {
				score += 3
			}
			for _, t := range sk.Triggers {
				if strings.Contains(strings.ToLower(t), w) {
					score += 4
				}
			}
			if strings.Contains(hay, w) {
				score++
			}
		}
		if score > 0 || len(words) == 0 {
			all = append(all, scored{l.toInfo(sk), score})
		}
	}
	l.mu.RUnlock()
	sort.Slice(all, func(i, j int) bool {
		if all[i].score != all[j].score {
			return all[i].score > all[j].score
		}
		return all[i].info.Name < all[j].info.Name
	})
	var out []tools.SkillInfo
	for i, s := range all {
		if limit > 0 && i >= limit {
			break
		}
		out = append(out, s.info)
	}
	return out
}

func (l *Library) Read(name string) (string, error) {
	l.mu.RLock()
	sk := l.skills[name]
	l.mu.RUnlock()
	if sk == nil {
		return "", fmt.Errorf("no skill %q (use skill action=list)", name)
	}
	if l.disabled[name] {
		return "", fmt.Errorf("skill %q is disabled", name)
	}
	hdr := fmt.Sprintf("# Skill: %s\n%s\n\n", sk.Name, sk.Description)
	if len(sk.info().Files) > 0 {
		hdr += "Bundled files (read with skill action=read file=<path>): " + strings.Join(sk.info().Files, ", ") + "\n\n"
	}
	return hdr + sk.body, nil
}

func (sk *skill) info() tools.SkillInfo {
	var files []string
	if sk.embedded {
		fs.WalkDir(embedded, sk.dir, func(p string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() && filepath.Base(p) != "SKILL.md" {
				rel, _ := filepath.Rel(sk.dir, p)
				files = append(files, filepath.ToSlash(rel))
			}
			return nil
		})
	} else if sk.dir != "" {
		filepath.WalkDir(sk.dir, func(p string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() && filepath.Base(p) != "SKILL.md" && !strings.HasSuffix(p, "LICENSE") {
				rel, _ := filepath.Rel(sk.dir, p)
				files = append(files, filepath.ToSlash(rel))
			}
			return nil
		})
	}
	sort.Strings(files)
	return tools.SkillInfo{Files: files}
}

func (l *Library) ReadFile(name, rel string) (string, error) {
	l.mu.RLock()
	sk := l.skills[name]
	l.mu.RUnlock()
	if sk == nil {
		return "", fmt.Errorf("no skill %q", name)
	}
	rel = filepath.ToSlash(filepath.Clean("/" + rel))[1:] // strip traversal
	if sk.embedded {
		b, err := embedded.ReadFile(sk.dir + "/" + rel)
		if err != nil {
			return "", fmt.Errorf("no file %q in skill %q", rel, name)
		}
		return string(b), nil
	}
	full := filepath.Join(sk.dir, filepath.FromSlash(rel))
	if r, err := filepath.Rel(sk.dir, full); err != nil || strings.HasPrefix(r, "..") {
		return "", fmt.Errorf("path escapes skill directory")
	}
	b, err := os.ReadFile(full)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func (l *Library) Save(name, content string) (string, error) {
	sk := parse(content)
	if sk == nil {
		return "", fmt.Errorf("content needs YAML front matter with at least a name:")
	}
	if sk.Name != name && name != "" {
		return "", fmt.Errorf("front-matter name %q does not match %q", sk.Name, name)
	}
	dir := ""
	for _, d := range l.dirs {
		if d != "" {
			dir = d
			break
		}
	}
	if dir == "" {
		dir = filepath.Join(defaultSkillDir(), "skills")
	}
	target := filepath.Join(dir, sk.Name)
	if err := os.MkdirAll(target, 0o755); err != nil {
		return "", err
	}
	p := filepath.Join(target, "SKILL.md")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		return "", err
	}
	sk.source, sk.dir = p, target
	l.mu.Lock()
	l.skills[sk.Name] = sk
	l.mu.Unlock()
	return p, nil
}

func (l *Library) SetDisabled(name string, off bool) {
	l.mu.Lock()
	l.disabled[name] = off
	l.mu.Unlock()
}

func defaultSkillDir() string {
	if h := os.Getenv("ALLONES_HOME"); h != "" {
		return h
	}
	u, _ := os.UserHomeDir()
	return filepath.Join(u, ".allones")
}
