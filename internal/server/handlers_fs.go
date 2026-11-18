package server

import (
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/allones/allones/internal/config"
)

// The folder browser lets the operator pick a directory on this machine to use
// as the agent's workspace (where file and shell tools run). It lists directory
// names only, never file contents, and is behind the dashboard's auth.

type fsEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type fsRoot struct {
	Label string `json:"label"`
	Path  string `json:"path"`
}

// handleFSRoots returns useful starting points: home, the current default
// workspace, the process working dir, and (on Windows) the drive letters.
func (s *Server) handleFSRoots(w http.ResponseWriter, r *http.Request) {
	var roots []fsRoot
	seen := map[string]bool{}
	add := func(label, p string) {
		if p == "" {
			return
		}
		ap, err := filepath.Abs(p)
		if err != nil || seen[ap] {
			return
		}
		if st, err := os.Stat(ap); err != nil || !st.IsDir() {
			return
		}
		seen[ap] = true
		roots = append(roots, fsRoot{Label: label, Path: ap})
	}
	if home, err := os.UserHomeDir(); err == nil {
		add("Home", home)
	}
	add("Current workspace", s.cfg.Get().Tools.Workspace)
	if wd, err := os.Getwd(); err == nil {
		add("Working directory", wd)
	}
	if runtime.GOOS == "windows" {
		for c := 'A'; c <= 'Z'; c++ {
			d := string(c) + ":\\"
			if _, err := os.Stat(d); err == nil {
				add(string(c)+" drive", d)
			}
		}
	} else {
		add("Root", "/")
	}
	writeJSON(w, 200, map[string]any{"roots": roots})
}

// handleFSList lists the subdirectories of a path.
func (s *Server) handleFSList(w http.ResponseWriter, r *http.Request) {
	raw := config.ExpandHome(strings.TrimSpace(r.URL.Query().Get("path")))
	if raw == "" {
		if home, err := os.UserHomeDir(); err == nil {
			raw = home
		} else {
			raw = "."
		}
	}
	p, err := filepath.Abs(raw)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	ents, err := os.ReadDir(p)
	if err != nil {
		writeErr(w, 400, friendlyFSErr(err))
		return
	}
	var dirs []fsEntry
	for _, e := range ents {
		name := e.Name()
		if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "$") {
			continue
		}
		isDir := e.IsDir()
		if !isDir && e.Type()&os.ModeSymlink != 0 {
			if st, err := os.Stat(filepath.Join(p, name)); err == nil && st.IsDir() {
				isDir = true
			}
		}
		if isDir {
			dirs = append(dirs, fsEntry{Name: name, Path: filepath.Join(p, name)})
		}
	}
	sort.Slice(dirs, func(i, j int) bool { return strings.ToLower(dirs[i].Name) < strings.ToLower(dirs[j].Name) })
	parent := filepath.Dir(p)
	if parent == p {
		parent = "" // at a drive/filesystem root
	}
	writeJSON(w, 200, map[string]any{"path": p, "parent": parent, "entries": dirs})
}

// handleFSCreate makes a new directory under a parent.
func (s *Server) handleFSCreate(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Parent string `json:"parent"`
		Name   string `json:"name"`
	}
	readJSON(r, &b)
	name := strings.TrimSpace(b.Name)
	if name == "" || strings.ContainsAny(name, `/\:*?"<>|`) {
		writeErr(w, 400, "invalid folder name")
		return
	}
	parent, err := filepath.Abs(config.ExpandHome(b.Parent))
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	full := filepath.Join(parent, name)
	if err := os.MkdirAll(full, 0o755); err != nil {
		writeErr(w, 400, friendlyFSErr(err))
		return
	}
	writeJSON(w, 200, map[string]any{"path": full})
}

func friendlyFSErr(err error) string {
	if os.IsPermission(err) {
		return "permission denied for that folder"
	}
	if os.IsNotExist(err) {
		return "that folder does not exist"
	}
	return err.Error()
}
