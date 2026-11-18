package server

import (
	"net/http"

	"github.com/allones/allones/internal/agent"
	"github.com/allones/allones/internal/secure"
	"github.com/allones/allones/internal/store"
	"github.com/allones/allones/internal/tools"
)

func (s *Server) handleEngagements(w http.ResponseWriter, r *http.Request) {
	list, _ := s.store.ListEngagements()
	writeJSON(w, 200, map[string]any{"engagements": list})
}

func (s *Server) handleSaveEngagement(w http.ResponseWriter, r *http.Request) {
	var e store.Engagement
	if err := readJSON(r, &e); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if err := s.store.SaveEngagement(&e); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	s.store.Audit("operator", "engagement.save", e.Name, "authorized="+boolStr(e.Authorized), "info")
	writeJSON(w, 200, e)
}

func (s *Server) handleGetEngagement(w http.ResponseWriter, r *http.Request) {
	e, err := s.store.GetEngagement(r.PathValue("id"))
	if err != nil {
		writeErr(w, 404, "not found")
		return
	}
	intel, _ := s.store.ListIntel(e.ID, "")
	findings, _ := s.store.ListFindings(e.ID)
	writeJSON(w, 200, map[string]any{"engagement": e, "intel": intel, "findings": findings, "methodology": tools.MethodologyStatus(s.store, e)})
}

func (s *Server) handleDeleteEngagement(w http.ResponseWriter, r *http.Request) {
	s.store.DeleteEngagement(r.PathValue("id"))
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) handleIntel(w http.ResponseWriter, r *http.Request) {
	list, _ := s.store.ListIntel(r.PathValue("id"), r.URL.Query().Get("kind"))
	writeJSON(w, 200, map[string]any{"intel": list})
}

func (s *Server) handleReport(w http.ResponseWriter, r *http.Request) {
	e, err := s.store.GetEngagement(r.PathValue("id"))
	if err != nil {
		writeErr(w, 404, "not found")
		return
	}
	md := e.Report
	if md == "" || r.URL.Query().Get("rebuild") == "1" {
		md = tools.BuildReport(s.store, e, "")
	}
	if r.URL.Query().Get("format") == "markdown" {
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		w.Write([]byte(md))
		return
	}
	writeJSON(w, 200, map[string]string{"report": md})
}

func (s *Server) handleFindings(w http.ResponseWriter, r *http.Request) {
	list, _ := s.store.ListFindings(r.URL.Query().Get("engagement"))
	writeJSON(w, 200, map[string]any{"findings": list})
}

func (s *Server) handleSaveFinding(w http.ResponseWriter, r *http.Request) {
	var f store.Finding
	readJSON(r, &f)
	f.ID = r.PathValue("id")
	if f.ID == "new" {
		f.ID = ""
	}
	if f.CVSSVector != "" {
		if score, sev, err := secure.CVSS31(f.CVSSVector); err == nil {
			f.CVSSScore, f.Severity = score, sev
		}
	}
	if f.Severity == "" {
		f.Severity = "medium"
	}
	if f.CVSSScore == 0 {
		f.CVSSScore = map[string]float64{"critical": 9.0, "high": 7.5, "medium": 5.0, "low": 3.0, "info": 0}[f.Severity]
	}
	if err := s.store.SaveFinding(&f); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, f)
}

func (s *Server) handleDeleteFinding(w http.ResponseWriter, r *http.Request) {
	s.store.DeleteFinding(r.PathValue("id"))
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// ---------- command (slash commands answered by the server) ----------

func (s *Server) handleCommand(w http.ResponseWriter, r *http.Request) {
	var b struct {
		SessionID string `json:"session_id"`
		Command   string `json:"command"`
		Arg       string `json:"arg"`
	}
	readJSON(r, &b)
	out := s.runCommand(b.SessionID, b.Command, b.Arg)
	writeJSON(w, 200, map[string]string{"output": out})
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func agentRequestFromJob(j store.CronJob, sessionID string) agent.Request {
	return agent.Request{
		SessionID: sessionID, Text: j.Prompt, Role: j.Role, Model: j.Model,
		Source: "cron", Interactive: false, AutoApprove: j.AutoApprove,
	}
}

func toolsetMap() map[string][]string { return tools.Toolsets }
