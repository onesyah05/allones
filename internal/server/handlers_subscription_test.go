package server

import (
	"github.com/allones/allones/internal/subscription"
	"testing"
)

func TestLoginViewPhases(t *testing.T) {
	for _, phase := range []string{"starting", "awaiting_user", "exchanging", "done", "error", "cancelled"} {
		t.Run(phase, func(t *testing.T) {
			expected := "pending"
			if phase == "done" {
				expected = "success"
			}
			if phase == "error" || phase == "cancelled" {
				expected = phase
			}
			got := loginView(subscription.Status{ID: "id", Phase: phase, URL: "https://example.com"})
			if got.Phase != expected || got.ID != "id" {
				t.Fatalf("unexpected view %#v", got)
			}
		})
	}
}

func TestSubscriptionRoutesRegister(t *testing.T) {
	// ServeMux rejects crossing wildcard patterns at runtime, not compile time.
	s := &Server{}
	_ = s.Handler()
}
