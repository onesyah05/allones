package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/allones/allones/internal/secure"
)

type secToolSpec struct {
	install string
	network bool
	group   string
}

// secTools is the allowlist of security CLIs sec_tool may invoke (no shell).
var secTools = map[string]secToolSpec{
	"nmap":        {"apt install nmap / brew install nmap", true, "network"},
	"masscan":     {"apt install masscan", true, "network"},
	"naabu":       {"go install github.com/projectdiscovery/naabu/v2/cmd/naabu@latest", true, "network"},
	"nuclei":      {"go install github.com/projectdiscovery/nuclei/v3/cmd/nuclei@latest", true, "web"},
	"httpx":       {"go install github.com/projectdiscovery/httpx/cmd/httpx@latest", true, "web"},
	"subfinder":   {"go install github.com/projectdiscovery/subfinder/v2/cmd/subfinder@latest", true, "recon"},
	"dnsx":        {"go install github.com/projectdiscovery/dnsx/cmd/dnsx@latest", true, "recon"},
	"katana":      {"go install github.com/projectdiscovery/katana/cmd/katana@latest", true, "web"},
	"ffuf":        {"go install github.com/ffuf/ffuf/v2@latest", true, "web"},
	"gobuster":    {"go install github.com/OJ/gobuster/v3@latest", true, "web"},
	"feroxbuster": {"cargo install feroxbuster / brew install feroxbuster", true, "web"},
	"sqlmap":      {"pip install sqlmap / apt install sqlmap", true, "web"},
	"nikto":       {"apt install nikto", true, "web"},
	"whatweb":     {"apt install whatweb", true, "web"},
	"wpscan":      {"gem install wpscan", true, "web"},
	"amass":       {"go install github.com/owasp-amass/amass/v4/...@master", true, "recon"},
	"testssl.sh":  {"git clone github.com/drwetter/testssl.sh", true, "network"},
	"sslscan":     {"apt install sslscan", true, "network"},
	"semgrep":     {"pip install semgrep", false, "sast"},
	"gitleaks":    {"brew install gitleaks / go install github.com/gitleaks/gitleaks/v8@latest", false, "sast"},
	"trufflehog":  {"brew install trufflehog", false, "sast"},
	"trivy":       {"brew install trivy / apt install trivy", false, "sca"},
	"grype":       {"brew install grype", false, "sca"},
	"syft":        {"brew install syft", false, "sca"},
	"osv-scanner": {"go install github.com/google/osv-scanner/cmd/osv-scanner@latest", false, "sca"},
	"codeql":      {"download from github.com/github/codeql-cli-binaries", false, "sast"},
	"checkov":     {"pip install checkov", false, "iac"},
	"kube-bench":  {"github.com/aquasecurity/kube-bench", false, "cloud"},
	"prowler":     {"pip install prowler", true, "cloud"},
	"scoutsuite":  {"pip install scoutsuite", true, "cloud"},
	"trufflehog3": {"pip install trufflehog3", false, "sast"},
	"rea":         {"npx -y rea-agents@latest setup (reverse engineering MCP/CLI)", false, "reverse"},
}

func registerSecTool(r *Registry) {
	r.Add(&Tool{Name: "sec_tool", Category: "security", Mutates: true, Network: true,
		Description: `Run an installed security CLI with arguments (executed directly, no shell). action=status lists which are installed with install hints.
Groups: recon (subfinder, amass, dnsx), network (nmap, naabu, masscan, sslscan), web (httpx, nuclei, katana, ffuf, gobuster, sqlmap, nikto, whatweb, wpscan), sast (semgrep, gitleaks, codeql, trufflehog), sca (trivy, grype, syft, osv-scanner), cloud (prowler, scoutsuite, kube-bench), reverse (rea).
Prefer JSON output flags (-json, -jsonl, -oJ). Targets found in args are scope-checked during an engagement. For runs over a few minutes, launch via the process tool instead.`,
		Schema: Obj(map[string]any{"action": Enum("Operation.", "status", "run"), "tool": Str("Binary name from the allowlist."), "args": StrArr("Arguments."), "timeout": Int("Seconds, default 600, max 3600.")}, "action"),
		Run: func(ctx context.Context, env *Env, raw json.RawMessage) Result {
			var a struct {
				Action, Tool string
				Args         []string
				Timeout      int
			}
			Bind(raw, &a)
			if a.Action != "run" {
				return Text(ToolchainStatus(env))
			}
			spec, ok := secTools[a.Tool]
			if !ok {
				return Errorf("tool %q is not in the allowlist; use shell for other commands", a.Tool)
			}
			bin := lookTool(env, a.Tool)
			if bin == "" {
				return Errorf("%s is not installed. Install: %s", a.Tool, spec.install)
			}
			var warns []string
			if spec.network {
				for _, t := range targetsInArgs(a.Args) {
					w, err := env.CheckTarget(ctx, a.Tool, t)
					if err != nil {
						return Errorf("%v", err)
					}
					if w != "" {
						warns = append(warns, w)
					}
				}
			}
			if a.Timeout <= 0 {
				a.Timeout = 600
			}
			if a.Timeout > 3600 {
				a.Timeout = 3600
			}
			cctx, cancel := context.WithTimeout(ctx, time.Duration(a.Timeout)*time.Second)
			defer cancel()
			cmd := exec.CommandContext(cctx, bin, a.Args...)
			cmd.Dir = env.Services.Shells.Get(env.SessionID, env.Workspace)
			cmd.Env = toolEnv(env)
			setProcGroup(cmd)
			cmd.Cancel = func() error { return killTree(cmd) }
			cmd.WaitDelay = 3 * time.Second
			var buf syncBuffer
			cmd.Stdout, cmd.Stderr = &buf, &buf
			env.Store.Audit("agent:"+env.SessionID, "sec_tool.run", a.Tool, strings.Join(a.Args, " "), "info")
			err := cmd.Run()
			out := buf.String()
			if s, n := secure.Redact(out); n > 0 {
				out = s
			}
			if len(warns) > 0 {
				out = strings.Join(warns, "\n") + "\n\n" + out
			}
			res := Result{Text: Truncate(out, env.Cfg.Tools.MaxOutputChars), Untrusted: a.Tool}
			if cctx.Err() == context.DeadlineExceeded {
				res.Text += fmt.Sprintf("\n[killed after %ds; use the process tool for long scans]", a.Timeout)
				res.IsError = true
			} else if err != nil && (cmd.ProcessState == nil || cmd.ProcessState.ExitCode() > 1) {
				// exit 1 is normal for many scanners when they report findings
				res.Text += "\n[" + err.Error() + "]"
				res.IsError = true
			}
			if strings.TrimSpace(res.Text) == "" {
				res.Text = "(no output)"
			}
			return res
		}})
}

func lookTool(env *Env, name string) string {
	// Build a lookup PATH that includes the configured extra tools path and go bin.
	old := ""
	for _, kv := range toolEnv(env) {
		if strings.HasPrefix(strings.ToUpper(kv), "PATH=") {
			old = kv[5:]
		}
	}
	paths := strings.Split(old, string(pathSep()))
	for _, dir := range paths {
		for _, ext := range execExts() {
			cand := dir + string(sep()) + name + ext
			if isExecutable(cand) {
				return cand
			}
		}
	}
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	return ""
}

func ToolchainStatus(env *Env) string {
	groups := map[string][]string{}
	for name, spec := range secTools {
		mark := "not installed"
		if lookTool(env, name) != "" {
			mark = "installed"
		}
		groups[spec.group] = append(groups[spec.group], fmt.Sprintf("  %-14s %s", name, mark))
	}
	order := []string{"recon", "network", "web", "sast", "sca", "cloud", "iac", "reverse"}
	var b strings.Builder
	b.WriteString("Security toolchain (sec_tool allowlist):\n")
	for _, g := range order {
		if lines := groups[g]; len(lines) > 0 {
			sort.Strings(lines)
			fmt.Fprintf(&b, "\n[%s]\n%s\n", g, strings.Join(lines, "\n"))
		}
	}
	if env.Cfg.Security.ToolsPath != "" {
		fmt.Fprintf(&b, "\nextra tools path: %s\n", env.Cfg.Security.ToolsPath)
	}
	b.WriteString("\nMissing tools: install them, or set security.tools_path to a directory that holds them.")
	return b.String()
}


func targetsInArgs(args []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, a := range args {
		if a == "" || strings.HasPrefix(a, "-") {
			continue
		}
		host := ""
		switch {
		case strings.Contains(a, "://"):
			host = secure.HostOf(a)
		case strings.Contains(a, ".") && !strings.ContainsAny(a, " /\\") && !strings.HasSuffix(a, ".txt") && !strings.HasSuffix(a, ".json") && !strings.HasSuffix(a, ".yaml"):
			host = secure.HostOf(a)
		}
		if host != "" && !seen[host] {
			seen[host] = true
			out = append(out, host)
		}
	}
	return out
}
