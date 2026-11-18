package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultPort(t *testing.T) {
	if got := Defaults().Server.Port; got != 8788 {
		t.Fatalf("default port = %d, want 8788", got)
	}
}

func TestLegacySavedPort(t *testing.T) {
	for _, tc := range []struct {
		name, env string
		want      int
	}{
		{"saved legacy default", "", 8788},
		{"explicit override", "8790", 8790},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("ALLONES_HOME", home)
			t.Setenv("ALLONES_PORT", tc.env)
			if err := os.WriteFile(filepath.Join(home, "config.yaml"), []byte("server:\n  port: 8787\n"), 0600); err != nil {
				t.Fatal(err)
			}
			m, err := Load()
			if err != nil {
				t.Fatal(err)
			}
			if got := m.Get().Server.Port; got != tc.want {
				t.Fatalf("port=%d want=%d", got, tc.want)
			}
		})
	}
}
