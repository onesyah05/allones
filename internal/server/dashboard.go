package server

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed all:dist
var dashboardFS embed.FS

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	sub, err := fs.Sub(dashboardFS, "dist")
	if err != nil {
		s.fallbackPage(w)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/")
	if path == "" {
		path = "index.html"
	}
	// Try the exact file; otherwise serve index.html (SPA client routing).
	if f, err := sub.Open(path); err == nil {
		if st, _ := f.Stat(); st != nil && !st.IsDir() {
			f.Close()
			setCache(w, path)
			http.ServeFileFS(w, r, sub, path)
			return
		}
		f.Close()
	}
	if _, err := sub.Open("index.html"); err != nil {
		s.fallbackPage(w)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeFileFS(w, r, sub, "index.html")
}

func setCache(w http.ResponseWriter, path string) {
	if strings.HasPrefix(path, "assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}
}

func (s *Server) fallbackPage(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(`<!doctype html><html><head><meta charset="utf-8"><title>AllOnes</title>
<style>body{font-family:system-ui;background:#0a0a0b;color:#e5e5e7;display:grid;place-items:center;height:100vh;margin:0}
.c{text-align:center;max-width:40rem;padding:2rem}a{color:#ff3b5c}code{background:#1a1a1d;padding:.2em .4em;border-radius:4px}</style></head>
<body><div class="c"><h1>AllOnes</h1><p>The API is running, but the dashboard bundle is not built yet.</p>
<p>Build it with <code>cd web &amp;&amp; bun install &amp;&amp; bun run build</code>, then rebuild the binary.</p>
<p>The API is live at <code>/api/health</code>.</p></div></body></html>`))
}
