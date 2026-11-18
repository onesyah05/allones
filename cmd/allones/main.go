// Command allones is the self-hosted AI agent: one binary serving an HTTP API and
// the embedded dashboard, with a security-first agent at its core.
package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/allones/allones/internal/agent"
	"github.com/allones/allones/internal/config"
	"github.com/allones/allones/internal/mcp"
	"github.com/allones/allones/internal/rag"
	"github.com/allones/allones/internal/roles"
	"github.com/allones/allones/internal/scheduler"
	"github.com/allones/allones/internal/secure"
	"github.com/allones/allones/internal/server"
	"github.com/allones/allones/internal/skills"
	"github.com/allones/allones/internal/store"
	"github.com/allones/allones/internal/tools"
	"github.com/allones/allones/internal/version"
)

func main() {
	args := os.Args[1:]
	cmd := "serve"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	switch cmd {
	case "serve", "server", "start":
		serve(args)
	case "version", "-v", "--version":
		fmt.Println("allones", version.Version)
	case "config":
		configCmd(args)
	case "setup":
		setupCmd()
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintln(os.Stderr, "unknown command:", cmd)
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Print(`allones — self-hosted AI agent

Usage:
  allones [serve]          start the API + dashboard (default)
  allones setup            configure interactively
  allones config get KEY   read a config value
  allones config set K V    set a config value
  allones config path      print the config file path
  allones version

Environment:
  ALLONES_HOME     data directory (default ~/.allones)
  ALLONES_HOST     bind host override
  ALLONES_PORT     bind port override
  ANTHROPIC_API_KEY / OPENAI_API_KEY / GEMINI_API_KEY / OPENROUTER_API_KEY — auto-detected
`)
}

func must[T any](v T, err error) T {
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	return v
}

func buildStack() (*config.Manager, *store.Store, *agent.Engine, *mcp.Manager, *scheduler.Scheduler, *secure.Vault) {
	cfg := must(config.Load())
	home := config.Home()
	os.MkdirAll(home, 0o700)
	c := cfg.Get()
	os.MkdirAll(c.Tools.Workspace, 0o755)

	st := must(store.Open(filepath.Join(home, "allones.db")))
	vault := must(secure.OpenVault(home))

	reg := tools.Default()
	sk := must(skills.New(c.Skills.Dirs, c.Skills.Disabled, c.Skills.AutoDiscover, osHome(), c.Tools.Workspace))
	rl := roles.Load(filepath.Join(home, "roles"))
	rg := rag.New(st, cfg)
	eng := agent.New(cfg, st, reg, sk, rl, rg, vault)

	mc := mcp.NewManager(reg)
	eng.MCPStatus = mc.Status
	sched := scheduler.New(st, eng)
	eng.Scheduler = sched
	return cfg, st, eng, mc, sched, vault
}

func serve(args []string) {
	foreground := true
	for _, a := range args {
		if a == "--background" {
			foreground = false
		}
	}
	_ = foreground

	cfg, st, eng, mc, sched, vault := buildStack()
	defer st.Close()
	defer eng.Close()

	c := cfg.Get()

	// Connect MCP servers in the background.
	go mc.Sync(context.Background(), c.MCP.Servers)
	sched.Start()
	defer sched.Stop()
	defer mc.Close()

	srv := server.New(cfg, st, eng, mc, sched, vault)
	addr := net.JoinHostPort(c.Server.Host, fmt.Sprint(c.Server.Port))
	httpSrv := &http.Server{Addr: addr, Handler: srv.Handler(), ReadHeaderTimeout: 15 * time.Second}

	// Refuse an unauthenticated non-loopback bind.
	if c.Server.Host != "127.0.0.1" && c.Server.Host != "localhost" && !c.Server.AuthDisabled && c.Server.PasswordHash == "" && c.Server.Token == "" {
		fmt.Fprintf(os.Stderr, "refusing to bind %s without auth: set a dashboard password, server.token, or server.auth_disabled: true\n", addr)
		os.Exit(1)
	}

	st.Audit("system", "server.start", addr, "version "+version.Version, "info")
	fmt.Printf("AllOnes %s\n", version.Version)
	fmt.Printf("  dashboard: http://%s\n", displayAddr(c.Server.Host, c.Server.Port))
	fmt.Printf("  api:       http://%s/api/health\n", displayAddr(c.Server.Host, c.Server.Port))
	if len(c.Provider) == 0 {
		fmt.Printf("  note: no model provider configured — open the dashboard to finish setup, or run `allones setup`\n")
	}
	if c.Server.Host == "0.0.0.0" {
		if ips := localIPs(); len(ips) > 0 {
			fmt.Printf("  reachable on: %s\n", strings.Join(ips, ", "))
		}
	}

	errc := make(chan error, 1)
	go func() { errc <- httpSrv.ListenAndServe() }()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	select {
	case err := <-errc:
		if err != nil && err != http.ErrServerClosed {
			fmt.Fprintln(os.Stderr, "server error:", err)
			os.Exit(1)
		}
	case <-stop:
		fmt.Println("\nshutting down…")
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		httpSrv.Shutdown(ctx)
		st.Audit("system", "server.stop", addr, "", "info")
	}
}

func configCmd(args []string) {
	cfg := must(config.Load())
	if len(args) == 0 {
		fmt.Println("usage: allones config [get KEY | set KEY VALUE | path]")
		return
	}
	switch args[0] {
	case "path":
		fmt.Println(config.Path())
	case "get":
		if len(args) < 2 {
			fmt.Println("usage: allones config get KEY")
			return
		}
		fmt.Println(configGet(cfg.Get(), args[1]))
	case "set":
		if len(args) < 3 {
			fmt.Println("usage: allones config set KEY VALUE")
			return
		}
		if err := cfg.Update(func(c *config.Config) error { return configSet(c, args[1], args[2]) }); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		fmt.Printf("set %s = %s\n", args[1], args[2])
	default:
		fmt.Println("unknown: config", args[0])
	}
}

func osHome() string {
	h, _ := os.UserHomeDir()
	return h
}

func displayAddr(host string, port int) string {
	if host == "0.0.0.0" || host == "" {
		host = "localhost"
	}
	return fmt.Sprintf("%s:%d", host, port)
}

func localIPs() []string {
	var out []string
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if ipnet, ok := a.(*net.IPNet); ok && !ipnet.IP.IsLoopback() && ipnet.IP.To4() != nil {
			out = append(out, fmt.Sprintf("http://%s", ipnet.IP.String()))
		}
	}
	return out
}
