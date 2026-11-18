package server

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/allones/allones/internal/config"
	"github.com/allones/allones/internal/cron"
	"github.com/allones/allones/internal/secure"
	"github.com/allones/allones/internal/selfupdate"
	"github.com/allones/allones/internal/store"
	"github.com/allones/allones/internal/version"
)

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "version": version.Current(), "configured": s.cfg.Exists(), "time": time.Now().UnixMilli()})
}

// handleVersion reports the running build and, when reachable, the latest
// release on GitHub so the dashboard can offer a one-click update.
func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, selfupdate.Check(r.Context()))
}

// handleUpdate downloads the latest release binary, swaps it in, and restarts.
func (s *Server) handleUpdate(w http.ResponseWriter, r *http.Request) {
	if err := selfupdate.Apply(r.Context()); err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "restarting": true})
	// Restart after the response has been flushed to the client.
	go func() {
		time.Sleep(300 * time.Millisecond)
		_ = selfupdate.Restart()
	}()
}

func (s *Server) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	c := s.cfg.Get()
	writeJSON(w, http.StatusOK, map[string]any{
		"configured":    s.cfg.Exists() && len(c.Provider) > 0,
		"auth_required": !c.Server.AuthDisabled && c.Server.PasswordHash != "",
		"authorized":    s.authorized(r),
	})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Password string `json:"password"`
	}
	readJSON(r, &b)
	c := s.cfg.Get()
	if c.Server.PasswordHash == "" || !checkPassword(c.Server.PasswordHash, b.Password) {
		writeErr(w, http.StatusUnauthorized, "wrong password")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"token": sessionToken(c.Server.PasswordHash)})
}

type setupRequest struct {
	Provider config.Provider `json:"provider"`
	Model    string          `json:"model"`
	AuxModel string          `json:"aux_model"`
	Password string          `json:"password"`
	SoulName string          `json:"soul_name"`
}

func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) {
	// Setup is allowed only before auth is configured, or by an authorized caller.
	if s.cfg.Get().Provider != nil && len(s.cfg.Get().Provider) > 0 && !s.authorized(r) {
		writeErr(w, http.StatusUnauthorized, "already configured")
		return
	}
	var req setupRequest
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	err := s.cfg.Update(func(c *config.Config) error {
		if req.Provider.Name != "" {
			if req.Provider.BaseURL == "" {
				req.Provider.BaseURL = config.DefaultBaseURL(req.Provider.Kind, req.Provider.Name)
			}
			replaced := false
			for i := range c.Provider {
				if c.Provider[i].Name == req.Provider.Name {
					c.Provider[i] = req.Provider
					replaced = true
				}
			}
			if !replaced {
				c.Provider = append(c.Provider, req.Provider)
			}
		}
		if req.Model != "" {
			c.Model.Default = req.Model
		}
		if req.AuxModel != "" {
			c.Model.Aux = req.AuxModel
		}
		if req.SoulName != "" {
			c.Soul.Name = req.SoulName
		}
		if req.Password != "" {
			h, err := hashPassword(req.Password)
			if err != nil {
				return err
			}
			c.Server.PasswordHash = h
		}
		return nil
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.store.Audit("operator", "setup", "", "configured", "info")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---------- sessions ----------

func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.ListSessions(r.URL.Query().Get("q"), 200)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"sessions": list})
}

func (s *Server) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	var sess store.Session
	readJSON(r, &sess)
	if err := s.store.CreateSession(&sess); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, sess)
}

func (s *Server) handleGetSession(w http.ResponseWriter, r *http.Request) {
	sess, err := s.store.GetSession(r.PathValue("id"))
	if err != nil {
		writeErr(w, 404, "not found")
		return
	}
	writeJSON(w, 200, sess)
}

func (s *Server) handleUpdateSession(w http.ResponseWriter, r *http.Request) {
	sess, err := s.store.GetSession(r.PathValue("id"))
	if err != nil {
		writeErr(w, 404, "not found")
		return
	}
	var patch store.Session
	readJSON(r, &patch)
	if patch.Title != "" {
		sess.Title = patch.Title
	}
	sess.Role = patch.Role
	sess.Model = patch.Model
	sess.Toolset = patch.Toolset
	sess.EngagementID = patch.EngagementID
	sess.Goal = patch.Goal
	sess.Pinned = patch.Pinned
	if err := s.store.UpdateSession(sess); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, sess)
}

func (s *Server) handleDeleteSession(w http.ResponseWriter, r *http.Request) {
	s.engine.Stop(r.PathValue("id"))
	if err := s.store.DeleteSession(r.PathValue("id")); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) handleMessages(w http.ResponseWriter, r *http.Request) {
	msgs, err := s.store.Messages(r.PathValue("id"), false)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"messages": msgs, "running": s.engine.IsRunning(r.PathValue("id"))})
}

// ---------- config / providers ----------

func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.cfg.Get())
}

func (s *Server) handleUpdateConfig(w http.ResponseWriter, r *http.Request) {
	var patch config.Config
	if err := readJSON(r, &patch); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	err := s.cfg.Update(func(c *config.Config) error {
		patch.Server.PasswordHash = c.Server.PasswordHash // don't clobber secrets via config editor
		patch.Server.Token = c.Server.Token
		// Preserve provider API keys when the editor sends blanks.
		for i := range patch.Provider {
			for j := range c.Provider {
				if patch.Provider[i].Name == c.Provider[j].Name && patch.Provider[i].APIKey == "" {
					patch.Provider[i].APIKey = c.Provider[j].APIKey
					patch.Provider[i].RefreshToken = c.Provider[j].RefreshToken
				}
			}
		}
		*c = patch
		return nil
	})
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if s.mcp != nil {
		go s.mcp.Sync(context.Background(), s.cfg.Get().MCP.Servers)
	}
	writeJSON(w, 200, s.cfg.Get())
}

func (s *Server) handleProviders(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"providers": s.cfg.Get().Provider})
}

func (s *Server) handleSaveProviders(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Providers []config.Provider `json:"providers"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	err := s.cfg.Update(func(c *config.Config) error {
		for i := range body.Providers {
			if body.Providers[i].APIKey == "" {
				for j := range c.Provider {
					if c.Provider[j].Name == body.Providers[i].Name {
						body.Providers[i].APIKey = c.Provider[j].APIKey
						body.Providers[i].RefreshToken = c.Provider[j].RefreshToken
					}
				}
			}
		}
		c.Provider = body.Providers
		return nil
	})
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"providers": s.cfg.Get().Provider})
}

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	c := s.cfg.Get()
	var models []map[string]string
	for _, p := range c.Provider {
		if p.Disabled {
			continue
		}
		for _, m := range p.Models {
			models = append(models, map[string]string{"id": p.Name + "/" + m, "provider": p.Name, "label": firstNonEmptyStr(p.Label, p.Name), "model": m})
		}
	}
	writeJSON(w, 200, map[string]any{"models": models, "default": c.Model.Default, "aux": c.Model.Aux})
}

// ---------- tools / roles / skills ----------

func (s *Server) handleTools(w http.ResponseWriter, r *http.Request) {
	all := s.engine.Registry().All()
	type toolView struct {
		Name, Description, Category, Source string
		Mutates, Network                    bool
	}
	var out []toolView
	for _, t := range all {
		out = append(out, toolView{t.Name, t.Description, t.Category, t.Source, t.Mutates, t.Network})
	}
	writeJSON(w, 200, map[string]any{"tools": out, "toolsets": toolsetNames()})
}

func (s *Server) handleRoles(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"roles": s.engine.Roles().All()})
}

func (s *Server) handleSkills(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if q != "" {
		writeJSON(w, 200, map[string]any{"skills": s.engine.Skills().Search(q, 50)})
		return
	}
	writeJSON(w, 200, map[string]any{"skills": s.engine.Skills().List()})
}

func (s *Server) handleSkillRead(w http.ResponseWriter, r *http.Request) {
	body, err := s.engine.Skills().Read(r.PathValue("name"))
	if err != nil {
		writeErr(w, 404, err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"name": r.PathValue("name"), "content": body})
}

func (s *Server) handleSkillSave(w http.ResponseWriter, r *http.Request) {
	var b struct{ Name, Content string }
	readJSON(r, &b)
	p, err := s.engine.Skills().Save(b.Name, b.Content)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"path": p})
}

// ---------- memory ----------

func (s *Server) handleMemory(w http.ResponseWriter, r *http.Request) {
	list, _ := s.store.ListMemories(r.URL.Query().Get("q"), 500)
	writeJSON(w, 200, map[string]any{"memories": list})
}

func (s *Server) handleSaveMemory(w http.ResponseWriter, r *http.Request) {
	var m store.Memory
	readJSON(r, &m)
	if err := s.store.SaveMemory(&m); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, m)
}

func (s *Server) handleDeleteMemory(w http.ResponseWriter, r *http.Request) {
	s.store.DeleteMemory(r.PathValue("id"))
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// ---------- RAG ----------

func (s *Server) handleCollections(w http.ResponseWriter, r *http.Request) {
	cols, _ := s.store.Collections()
	writeJSON(w, 200, map[string]any{"collections": cols})
}

func (s *Server) handleRAGIndex(w http.ResponseWriter, r *http.Request) {
	var b struct{ Collection, Path, Source, Text string }
	readJSON(r, &b)
	if b.Collection == "" {
		b.Collection = "default"
	}
	ctx := r.Context()
	if b.Text != "" {
		n, err := s.engine.RAGIndexText(ctx, b.Collection, firstNonEmptyStr(b.Source, "note"), b.Text)
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		writeJSON(w, 200, map[string]any{"chunks": n})
		return
	}
	p := config.ExpandHome(b.Path)
	if !filepath.IsAbs(p) {
		p = filepath.Join(s.cfg.Get().Tools.Workspace, p)
	}
	files, chunks, err := s.engine.RAGIndexPath(ctx, b.Collection, p)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"files": files, "chunks": chunks})
}

func (s *Server) handleRAGSearch(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Collection, Query string
		K                 int
	}
	readJSON(r, &b)
	chunks, err := s.engine.RAGSearch(r.Context(), b.Collection, b.Query, b.K)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"results": chunks})
}

func (s *Server) handleRAGDelete(w http.ResponseWriter, r *http.Request) {
	col := r.URL.Query().Get("collection")
	src := r.URL.Query().Get("source")
	s.store.DeleteCollection(col, src)
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// ---------- board ----------

func (s *Server) handleBoard(w http.ResponseWriter, r *http.Request) {
	tasks, _ := s.store.ListTasks()
	writeJSON(w, 200, map[string]any{"tasks": tasks})
}

func (s *Server) handleSaveTask(w http.ResponseWriter, r *http.Request) {
	var t store.Task
	readJSON(r, &t)
	if err := s.store.SaveTask(&t); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, t)
}

func (s *Server) handleDeleteTask(w http.ResponseWriter, r *http.Request) {
	s.store.DeleteTask(r.PathValue("id"))
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// ---------- cron ----------

func (s *Server) handleCronList(w http.ResponseWriter, r *http.Request) {
	jobs, _ := s.store.ListCron()
	writeJSON(w, 200, map[string]any{"jobs": jobs})
}

func (s *Server) handleCronSave(w http.ResponseWriter, r *http.Request) {
	var j store.CronJob
	readJSON(r, &j)
	if _, err := cron.Parse(j.Schedule); err != nil {
		writeErr(w, 400, "bad schedule: "+err.Error())
		return
	}
	if runs, _ := cron.NextRuns(j.Schedule, 1); len(runs) > 0 {
		j.NextRun = runs[0]
	}
	if err := s.store.SaveCron(&j); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, j)
}

func (s *Server) handleCronRun(w http.ResponseWriter, r *http.Request) {
	jobs, _ := s.store.ListCron()
	for _, j := range jobs {
		if j.ID == r.PathValue("id") {
			sess := &store.Session{Role: j.Role, Model: j.Model, Source: "cron", Title: "cron: " + j.Name}
			s.store.CreateSession(sess)
			go s.engine.Run(context.Background(), agentRequestFromJob(j, sess.ID), nil)
			writeJSON(w, 200, map[string]string{"session_id": sess.ID})
			return
		}
	}
	writeErr(w, 404, "no job")
}

func (s *Server) handleCronDelete(w http.ResponseWriter, r *http.Request) {
	s.store.DeleteCron(r.PathValue("id"))
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// ---------- servers (VPS) ----------

func (s *Server) handleServers(w http.ResponseWriter, r *http.Request) {
	list, _ := s.store.ListServers()
	for i := range list {
		list[i].Secret = "" // never expose
	}
	writeJSON(w, 200, map[string]any{"servers": list})
}

func (s *Server) handleSaveServer(w http.ResponseWriter, r *http.Request) {
	var v store.Server
	readJSON(r, &v)
	if v.Secret != "" && s.vault != nil {
		sealed, err := s.vault.Seal(v.Secret)
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		v.Secret = sealed
	}
	if err := s.store.SaveServer(&v); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	v.Secret = ""
	writeJSON(w, 200, v)
}

func (s *Server) handleDeleteServer(w http.ResponseWriter, r *http.Request) {
	s.store.DeleteServer(r.PathValue("id"))
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// ---------- mcp / team / stats / audit / files ----------

func (s *Server) handleMCP(w http.ResponseWriter, r *http.Request) {
	var status []map[string]any
	if s.mcp != nil {
		status = s.mcp.Status()
	}
	writeJSON(w, 200, map[string]any{"servers": status, "config": s.cfg.Get().MCP.Servers})
}

func (s *Server) handleMCPRefresh(w http.ResponseWriter, r *http.Request) {
	if s.mcp != nil {
		s.mcp.Sync(r.Context(), s.cfg.Get().MCP.Servers)
	}
	s.handleMCP(w, r)
}

func (s *Server) handleTeam(w http.ResponseWriter, r *http.Request) {
	stats, _ := s.store.RoleStats()
	writeJSON(w, 200, map[string]any{"team": stats})
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"stats": s.store.Stats(r.Context())})
}

func (s *Server) handleAudit(w http.ResponseWriter, r *http.Request) {
	entries, _ := s.store.AuditLog(300, r.URL.Query().Get("q"))
	writeJSON(w, 200, map[string]any{"audit": entries})
}

func (s *Server) handleNotifications(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, 500, "no streaming")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	ch := make(chan Notification, 16)
	s.mu.Lock()
	s.notifSubs[ch] = struct{}{}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.notifSubs, ch)
		s.mu.Unlock()
	}()
	enc := func(n Notification) {
		b, _ := json.Marshal(n)
		w.Write([]byte("data: " + string(b) + "\n\n"))
		flusher.Flush()
	}
	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case n := <-ch:
			enc(n)
		case <-time.After(25 * time.Second):
			w.Write([]byte(": ping\n\n"))
			flusher.Flush()
		}
	}
}

func (s *Server) handleFiles(w http.ResponseWriter, r *http.Request) {
	root := fsRootParam(s, r)
	sub := r.URL.Query().Get("path")
	dir := filepath.Join(root, filepath.Clean("/"+sub))
	ents, err := os.ReadDir(dir)
	if err != nil {
		writeErr(w, 404, err.Error())
		return
	}
	type entry struct {
		Name  string `json:"name"`
		Dir   bool   `json:"dir"`
		Size  int64  `json:"size"`
		Mtime int64  `json:"mtime"`
	}
	var out []entry
	for _, e := range ents {
		info, _ := e.Info()
		var sz, mt int64
		if info != nil {
			sz, mt = info.Size(), info.ModTime().UnixMilli()
		}
		out = append(out, entry{e.Name(), e.IsDir(), sz, mt})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Dir != out[j].Dir {
			return out[i].Dir
		}
		return out[i].Name < out[j].Name
	})
	writeJSON(w, 200, map[string]any{"path": sub, "root": root, "entries": out})
}

func (s *Server) handleFileContent(w http.ResponseWriter, r *http.Request) {
	root := fsRootParam(s, r)
	p := filepath.Join(root, filepath.Clean("/"+r.URL.Query().Get("path")))
	if rel, err := filepath.Rel(root, p); err != nil || strings.HasPrefix(rel, "..") {
		writeErr(w, 403, "outside workspace")
		return
	}
	info, err := os.Stat(p)
	if err != nil || info.IsDir() || info.Size() > 2<<20 {
		writeErr(w, 400, "not a readable file (or too large)")
		return
	}
	b, _ := os.ReadFile(p)
	content, _ := secure.Redact(string(b))
	writeJSON(w, 200, map[string]any{"path": r.URL.Query().Get("path"), "content": content, "size": info.Size()})
}

func firstNonEmptyStr(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

func toolsetNames() []string {
	var out []string
	for k := range toolsetMap() {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// fsRootParam resolves the ?root= query to an allowed absolute directory,
// defaulting to the configured workspace. Any existing directory on the host is
// allowed — the dashboard is the owner's, behind auth.
func fsRootParam(s *Server, r *http.Request) string {
	root := strings.TrimSpace(r.URL.Query().Get("root"))
	if root == "" {
		return s.cfg.Get().Tools.Workspace
	}
	root = config.ExpandHome(root)
	if abs, err := filepath.Abs(root); err == nil {
		if st, err := os.Stat(abs); err == nil && st.IsDir() {
			return abs
		}
	}
	return s.cfg.Get().Tools.Workspace
}
