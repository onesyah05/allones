package agent

import (
	"path/filepath"
	"testing"
)

func TestBrowserProfileUsesHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ALLONES_HOME", home)
	if got := browserProfile(); got != filepath.Join(home, "browser") {
		t.Fatalf("browser profile escaped ALLONES_HOME: %s", got)
	}
}
