package server

import (
	"net/http"

	"github.com/allones/allones/internal/subscription"
)

func loginView(status subscription.Status) subscription.Status {
	if status.Phase == "done" {
		status.Phase = "success"
	}
	if status.Phase != "success" && status.Phase != "error" && status.Phase != "cancelled" {
		status.Phase = "pending"
	}
	return status
}

func (s *Server) handleProviderLogin(w http.ResponseWriter, r *http.Request) {
	status, err := s.subscriptions.Start(r.PathValue("name"))
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, loginView(status))
}
func (s *Server) handleProviderLoginStatus(w http.ResponseWriter, r *http.Request) {
	status, ok := s.subscriptions.Status(r.PathValue("id"))
	if !ok {
		writeErr(w, 404, "login not found")
		return
	}
	writeJSON(w, 200, loginView(status))
}
func (s *Server) handleProviderLoginCancel(w http.ResponseWriter, r *http.Request) {
	s.subscriptions.Cancel(r.PathValue("id"))
	s.handleProviderLoginStatus(w, r)
}
