package server

import (
	"net/http"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

func (s *Server) routes(mux *http.ServeMux) {
	// Public
	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("GET /api/version", s.handleVersion)
	mux.HandleFunc("GET /api/auth/status", s.handleAuthStatus)
	mux.HandleFunc("POST /api/auth/login", s.handleLogin)
	mux.HandleFunc("POST /api/setup", s.handleSetup)

	// Protected API
	p := s.auth
	mux.HandleFunc("POST /api/chat", p(s.handleChat))
	mux.HandleFunc("GET /api/chat/stream", p(s.handleChatStream))
	mux.HandleFunc("POST /api/chat/stop", p(s.handleStop))
	mux.HandleFunc("POST /api/chat/steer", p(s.handleSteer))
	mux.HandleFunc("POST /api/chat/approve", p(s.handleApprove))
	mux.HandleFunc("POST /api/chat/answer", p(s.handleAnswer))
	mux.HandleFunc("POST /api/command", p(s.handleCommand))

	mux.HandleFunc("GET /api/sessions", p(s.handleListSessions))
	mux.HandleFunc("POST /api/sessions", p(s.handleCreateSession))
	mux.HandleFunc("GET /api/sessions/{id}", p(s.handleGetSession))
	mux.HandleFunc("PUT /api/sessions/{id}", p(s.handleUpdateSession))
	mux.HandleFunc("DELETE /api/sessions/{id}", p(s.handleDeleteSession))
	mux.HandleFunc("GET /api/sessions/{id}/messages", p(s.handleMessages))

	mux.HandleFunc("GET /api/config", p(s.handleGetConfig))
	mux.HandleFunc("PUT /api/config", p(s.handleUpdateConfig))
	mux.HandleFunc("POST /api/update", p(s.handleUpdate))

	mux.HandleFunc("GET /api/providers", p(s.handleProviders))
	mux.HandleFunc("PUT /api/providers", p(s.handleSaveProviders))
	mux.HandleFunc("GET /api/models", p(s.handleModels))
	mux.HandleFunc("PUT /api/models/defaults", p(s.handleModelDefaults))
	mux.HandleFunc("GET /api/providers/catalog", p(s.handleProviderCatalog))
	mux.HandleFunc("POST /api/providers/{name}/login", p(s.handleProviderLogin))
	mux.HandleFunc("GET /api/provider-logins/{id}", p(s.handleProviderLoginStatus))
	mux.HandleFunc("DELETE /api/provider-logins/{id}", p(s.handleProviderLoginCancel))
	mux.HandleFunc("PUT /api/providers/{name}", p(s.handleProviderSave))
	mux.HandleFunc("DELETE /api/providers/{name}", p(s.handleProviderDelete))
	mux.HandleFunc("GET /api/providers/{name}/models", p(s.handleProviderModels))
	mux.HandleFunc("POST /api/providers/{name}/test", p(s.handleProviderTest))

	mux.HandleFunc("GET /api/tools", p(s.handleTools))
	mux.HandleFunc("GET /api/roles", p(s.handleRoles))
	mux.HandleFunc("GET /api/skills", p(s.handleSkills))
	mux.HandleFunc("GET /api/skills/{name}", p(s.handleSkillRead))
	mux.HandleFunc("POST /api/skills", p(s.handleSkillSave))

	mux.HandleFunc("GET /api/memory", p(s.handleMemory))
	mux.HandleFunc("POST /api/memory", p(s.handleSaveMemory))
	mux.HandleFunc("DELETE /api/memory/{id}", p(s.handleDeleteMemory))

	mux.HandleFunc("GET /api/rag/collections", p(s.handleCollections))
	mux.HandleFunc("POST /api/rag/index", p(s.handleRAGIndex))
	mux.HandleFunc("POST /api/rag/search", p(s.handleRAGSearch))
	mux.HandleFunc("DELETE /api/rag/collection", p(s.handleRAGDelete))

	mux.HandleFunc("GET /api/engagements", p(s.handleEngagements))
	mux.HandleFunc("POST /api/engagements", p(s.handleSaveEngagement))
	mux.HandleFunc("GET /api/engagements/{id}", p(s.handleGetEngagement))
	mux.HandleFunc("DELETE /api/engagements/{id}", p(s.handleDeleteEngagement))
	mux.HandleFunc("GET /api/engagements/{id}/intel", p(s.handleIntel))
	mux.HandleFunc("GET /api/engagements/{id}/report", p(s.handleReport))
	mux.HandleFunc("GET /api/findings", p(s.handleFindings))
	mux.HandleFunc("PUT /api/findings/{id}", p(s.handleSaveFinding))
	mux.HandleFunc("DELETE /api/findings/{id}", p(s.handleDeleteFinding))

	mux.HandleFunc("GET /api/board", p(s.handleBoard))
	mux.HandleFunc("POST /api/board", p(s.handleSaveTask))
	mux.HandleFunc("DELETE /api/board/{id}", p(s.handleDeleteTask))

	mux.HandleFunc("GET /api/cron", p(s.handleCronList))
	mux.HandleFunc("POST /api/cron", p(s.handleCronSave))
	mux.HandleFunc("POST /api/cron/{id}/run", p(s.handleCronRun))
	mux.HandleFunc("DELETE /api/cron/{id}", p(s.handleCronDelete))

	mux.HandleFunc("GET /api/servers", p(s.handleServers))
	mux.HandleFunc("POST /api/servers", p(s.handleSaveServer))
	mux.HandleFunc("DELETE /api/servers/{id}", p(s.handleDeleteServer))

	mux.HandleFunc("GET /api/mcp", p(s.handleMCP))
	mux.HandleFunc("POST /api/mcp/refresh", p(s.handleMCPRefresh))

	mux.HandleFunc("GET /api/team", p(s.handleTeam))
	mux.HandleFunc("GET /api/stats", p(s.handleStats))
	mux.HandleFunc("GET /api/audit", p(s.handleAudit))
	mux.HandleFunc("GET /api/notifications", p(s.handleNotifications))
	mux.HandleFunc("GET /api/fs/roots", p(s.handleFSRoots))
	mux.HandleFunc("GET /api/fs/list", p(s.handleFSList))
	mux.HandleFunc("POST /api/fs/mkdir", p(s.handleFSCreate))
	mux.HandleFunc("GET /api/files", p(s.handleFiles))
	mux.HandleFunc("GET /api/files/content", p(s.handleFileContent))

	// Dashboard (embedded SPA) — must be last.
	mux.HandleFunc("/", s.handleDashboard)
}

// auth wraps a handler with token/password check.
func (s *Server) auth(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.authorized(r) {
			h(w, r)
			return
		}
		writeErr(w, http.StatusUnauthorized, "unauthorized")
	}
}

func (s *Server) authorized(r *http.Request) bool {
	c := s.cfg.Get()
	if c.Server.AuthDisabled {
		return true
	}
	if c.Server.PasswordHash == "" && c.Server.Token == "" {
		// Loopback with no auth set is open (local machine default).
		return isLoopbackRequest(r)
	}
	tok := bearer(r)
	if tok == "" {
		tok = r.URL.Query().Get("token")
	}
	if c.Server.Token != "" && tok == c.Server.Token {
		return true
	}
	if c.Server.PasswordHash != "" && tok != "" {
		// token may be a session token we issued (= sha256 of password hash)
		if tok == sessionToken(c.Server.PasswordHash) {
			return true
		}
	}
	return false
}

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if after, ok := strings.CutPrefix(h, "Bearer "); ok {
		return strings.TrimSpace(after)
	}
	return ""
}

func isLoopbackRequest(r *http.Request) bool {
	host := r.Host
	if i := strings.LastIndex(host, ":"); i >= 0 {
		host = host[:i]
	}
	host = strings.Trim(host, "[]")
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

func checkPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

func hashPassword(password string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(b), err
}
