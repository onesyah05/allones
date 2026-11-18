package tools

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/allones/allones/internal/secure"
	"github.com/allones/allones/internal/store"
)

func registerSecurity(r *Registry) {
	r.Add(&Tool{Name: "engagement", Category: "security",
		Description: "The active security engagement for this session: scope, authorization, rules of engagement, and methodology status (which phases have evidence: scope → recon → enumeration → vulnerability testing → reporting). Call this first in any security task and whenever deciding what to test next.",
		Schema:      Obj(map[string]any{"action": Enum("status (default) or intel to list recorded facts.", "status", "intel"), "kind": Str("Filter intel by kind.")}),
		Run: func(ctx context.Context, env *Env, raw json.RawMessage) Result {
			var a struct{ Action, Kind string }
			Bind(raw, &a)
			eng := env.Engagement()
			if eng == nil {
				return Text("No engagement is attached to this session. Ask the operator to create one in Security → Engagements (scope + authorization) and attach it, or proceed only against targets they explicitly confirm they own or are authorized to test.")
			}
			if a.Action == "intel" {
				list, _ := env.Store.ListIntel(eng.ID, a.Kind)
				if len(list) == 0 {
					return Text("no intel recorded yet")
				}
				var b strings.Builder
				for _, i := range list {
					fmt.Fprintf(&b, "%-12s %s  %s\n", i.Kind, i.Value, clip(i.Detail, 160))
				}
				return Text(b.String())
			}
			return Text(MethodologyStatus(env.Store, eng))
		}})

	r.Add(&Tool{Name: "add_intel", Category: "security",
		Description: "Record a fact discovered about the target in the engagement ledger: host, subdomain, ip, port, service, endpoint, technology, parameter, account, email, note. The methodology status is computed from this ledger.",
		Schema: Obj(map[string]any{"kind": Enum("Fact type.", "host", "subdomain", "ip", "port", "service", "endpoint", "technology", "parameter", "account", "email", "note"),
			"value": Str("The fact, e.g. api.example.com or 443/tcp nginx 1.25."), "detail": Str("Context/evidence.")}, "kind", "value"),
		Run: func(ctx context.Context, env *Env, raw json.RawMessage) Result {
			var a struct{ Kind, Value, Detail string }
			Bind(raw, &a)
			eng := env.Engagement()
			if eng == nil {
				return Errorf("no engagement attached to this session")
			}
			if _, err := env.Store.AddIntel(&store.Intel{EngagementID: eng.ID, Kind: a.Kind, Value: a.Value, Detail: a.Detail, Source: env.Role}); err != nil {
				return Errorf("%v", err)
			}
			return Text("recorded " + a.Kind + " " + a.Value)
		}})

	r.Add(&Tool{Name: "report_finding", Category: "security",
		Description: "Record a CONFIRMED security finding with evidence. Give a CVSS v3.1 vector when possible (score and severity are computed). Include exact reproduction steps, impact, and remediation. Do not record unverified guesses — verify first.",
		Schema: Obj(map[string]any{
			"title": Str("Short title."), "severity": Enum("Used when no CVSS vector.", "critical", "high", "medium", "low", "info"),
			"cvss_vector": Str("e.g. CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:N/A:N"), "cwe": Str("e.g. CWE-639"),
			"category": Str("OWASP category, e.g. A01:2025 Broken Access Control / API1:2023 BOLA."), "target": Str("Affected URL/host/component."),
			"description": Str("What is wrong."), "reproduction": Str("Exact steps to reproduce."), "impact": Str("Business/security impact."),
			"remediation": Str("How to fix."), "evidence": Str("Request/response excerpts, screenshot paths, tool output."),
			"id": Str("Existing finding id to update."),
		}, "title", "description", "reproduction"),
		Run: func(ctx context.Context, env *Env, raw json.RawMessage) Result {
			var f store.Finding
			if err := Bind(raw, &f); err != nil {
				return Errorf("%v", err)
			}
			var m map[string]string
			json.Unmarshal(raw, &m)
			f.CVSSVector, f.CWE = m["cvss_vector"], m["cwe"]
			if f.CVSSVector != "" {
				score, sev, err := secure.CVSS31(f.CVSSVector)
				if err != nil {
					return Errorf("cvss: %v", err)
				}
				f.CVSSScore, f.Severity = score, sev
			}
			if f.Severity == "" {
				f.Severity = "medium"
			}
			if f.CVSSScore == 0 {
				f.CVSSScore = map[string]float64{"critical": 9.0, "high": 7.5, "medium": 5.0, "low": 3.0, "info": 0}[f.Severity]
			}
			f.SessionID = env.SessionID
			if eng := env.Engagement(); eng != nil {
				f.EngagementID = eng.ID
			}
			f.Evidence, _ = secure.Redact(f.Evidence)
			if err := env.Store.SaveFinding(&f); err != nil {
				return Errorf("%v", err)
			}
			env.Store.Audit("agent:"+env.SessionID, "finding.recorded", f.Target, f.Severity+": "+f.Title, "info")
			if env.Services.Notify != nil && (f.Severity == "critical" || f.Severity == "high") {
				env.Services.Notify("Finding: "+f.Title, fmt.Sprintf("%s (%.1f) on %s", strings.ToUpper(f.Severity), f.CVSSScore, f.Target))
			}
			return Text(fmt.Sprintf("recorded finding %s: %s [%s %.1f]", f.ID, f.Title, f.Severity, f.CVSSScore))
		}})

	r.Add(&Tool{Name: "cvss", Category: "security",
		Description: "Compute a CVSS v3.1 base score and severity from a vector string.",
		Schema:      Obj(map[string]any{"vector": Str("CVSS:3.1/AV:../AC:../PR:../UI:../S:../C:../I:../A:..")}, "vector"),
		Run: func(ctx context.Context, env *Env, raw json.RawMessage) Result {
			var a struct{ Vector string }
			Bind(raw, &a)
			s, sev, err := secure.CVSS31(a.Vector)
			if err != nil {
				return Errorf("%v", err)
			}
			return Text(fmt.Sprintf("%.1f (%s)", s, sev))
		}})

	r.Add(&Tool{Name: "security_report", Category: "security", Mutates: true,
		Description: "Compile the engagement's findings and intel into a professional Markdown penetration-test report (executive summary, scope, methodology, findings worst-first with reproduction and remediation). Saves to reports/ in the workspace.",
		Schema:      Obj(map[string]any{"summary": Str("Executive summary you wrote (optional; a default is generated).")}),
		Run: func(ctx context.Context, env *Env, raw json.RawMessage) Result {
			var a struct{ Summary string }
			Bind(raw, &a)
			eng := env.Engagement()
			if eng == nil {
				return Errorf("no engagement attached")
			}
			md := BuildReport(env.Store, eng, a.Summary)
			eng.Report = md
			eng.Status = "reported"
			env.Store.SaveEngagement(eng)
			dir := filepath.Join(env.Workspace, "reports")
			os.MkdirAll(dir, 0o755)
			name := filepath.Join(dir, safeName(eng.Name)+"-"+time.Now().Format("20060102-1504")+".md")
			os.WriteFile(name, []byte(md), 0o644)
			rel, _ := filepath.Rel(env.Workspace, name)
			return Text("report saved to " + filepath.ToSlash(rel) + "\n\n" + Truncate(md, 6000))
		}})

	r.Add(&Tool{Name: "secret_scan", Category: "security",
		Description: "Scan files or a directory for leaked secrets (cloud keys, tokens, private keys, credential assignments) using gitleaks-style rules plus entropy filtering. Values are masked in output.",
		Schema:      Obj(map[string]any{"path": Str("File or directory (workspace-relative)."), "text": Str("Or scan this text directly.")}),
		Run: func(ctx context.Context, env *Env, raw json.RawMessage) Result {
			var a struct{ Path, Text string }
			Bind(raw, &a)
			var hits []secure.SecretHit
			if a.Text != "" {
				hits = secure.ScanSecrets(a.Text)
			} else {
				root, err := env.Resolve(a.Path)
				if err != nil {
					return Errorf("%v", err)
				}
				files := 0
				filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
					if err != nil || ctx.Err() != nil {
						return nil
					}
					if d.IsDir() {
						if d.Name() == ".git" || (skipDirs[d.Name()] && p != root) {
							return filepath.SkipDir
						}
						return nil
					}
					info, _ := d.Info()
					if info == nil || info.Size() > 4<<20 {
						return nil
					}
					b, err := os.ReadFile(p)
					if err != nil || bytes.IndexByte(b[:min(len(b), 4000)], 0) >= 0 {
						return nil
					}
					files++
					rel, _ := filepath.Rel(env.Workspace, p)
					for _, h := range secure.ScanSecrets(string(b)) {
						h.File = filepath.ToSlash(rel)
						hits = append(hits, h)
					}
					return nil
				})
				if len(hits) == 0 {
					return Text(fmt.Sprintf("no secrets found in %d files", files))
				}
			}
			var b strings.Builder
			for _, h := range hits {
				fmt.Fprintf(&b, "%s:%d  %-22s %s (entropy %.2f)\n", h.File, h.Line, h.Rule, h.Match, h.Entropy)
			}
			fmt.Fprintf(&b, "%d potential secrets. Verify each, rotate real ones, and purge from version-control history.", len(hits))
			return Text(Truncate(b.String(), env.Cfg.Tools.MaxOutputChars))
		}})

	r.Add(&Tool{Name: "dependency_audit", Category: "security",
		Description: "Find known vulnerabilities in a project's dependencies using the OSV.dev database (Go go.mod, npm lockfiles, Python requirements/poetry, Rust Cargo.lock, PHP composer.lock, Ruby Gemfile.lock).",
		Schema:      Obj(map[string]any{"path": Str("Project directory or manifest file.")}),
		Run: func(ctx context.Context, env *Env, raw json.RawMessage) Result {
			var a struct{ Path string }
			Bind(raw, &a)
			root, err := env.Resolve(a.Path)
			if err != nil {
				return Errorf("%v", err)
			}
			pkgs := CollectPackages(root)
			if len(pkgs) == 0 {
				return Text("no supported manifests found")
			}
			vulns, err := QueryOSV(ctx, pkgs)
			if err != nil {
				return Errorf("osv: %v", err)
			}
			if len(vulns) == 0 {
				return Text(fmt.Sprintf("checked %d packages: no known vulnerabilities", len(pkgs)))
			}
			var b strings.Builder
			fmt.Fprintf(&b, "checked %d packages; %d vulnerable:\n", len(pkgs), len(vulns))
			for _, v := range vulns {
				fmt.Fprintf(&b, "- %s %s@%s: %s\n", v.Ecosystem, v.Name, v.Version, strings.Join(v.IDs, ", "))
			}
			b.WriteString("\nDetails: https://osv.dev/vulnerability/<ID>. Upgrade to a fixed version.")
			return Text(b.String())
		}})

	r.Add(&Tool{Name: "code_audit", Category: "security",
		Description: "Fast static pattern scan of source code for risky constructs (injection sinks, unsafe deserialization, weak crypto, disabled TLS verification, XSS sinks, hard-coded secrets). Results are leads for manual review, not confirmed bugs; use semgrep/codeql via sec_tool for deeper analysis.",
		Schema:      Obj(map[string]any{"path": Str("File or directory."), "max": Int("Max results, default 300.")}),
		Run: func(ctx context.Context, env *Env, raw json.RawMessage) Result {
			var a struct {
				Path string
				Max  int
			}
			Bind(raw, &a)
			if a.Max <= 0 {
				a.Max = 300
			}
			root, err := env.Resolve(a.Path)
			if err != nil {
				return Errorf("%v", err)
			}
			hits := CodeAudit(root, a.Max)
			if len(hits) == 0 {
				return Text("no risky patterns matched")
			}
			for i := range hits {
				if rel, err := filepath.Rel(env.Workspace, hits[i].File); err == nil {
					hits[i].File = filepath.ToSlash(rel)
				}
			}
			var b strings.Builder
			for _, h := range hits {
				fmt.Fprintf(&b, "%s:%d [%s] %s — %s\n", h.File, h.Line, h.Rule, h.CWE, clip(strings.TrimSpace(h.Code), 160))
			}
			return Text(Truncate(b.String(), env.Cfg.Tools.MaxOutputChars))
		}})
}

func MethodologyStatus(st *store.Store, eng *store.Engagement) string {
	intel, _ := st.ListIntel(eng.ID, "")
	findings, _ := st.ListFindings(eng.ID)
	count := map[string]int{}
	for _, i := range intel {
		count[i.Kind]++
	}
	recon := count["host"] + count["subdomain"] + count["ip"]
	enum := count["port"] + count["service"] + count["endpoint"] + count["technology"] + count["parameter"]
	phases := []struct {
		name string
		done bool
		ev   string
		next string
	}{
		{"1. Scope & Authorization", len(eng.Scope) > 0 && eng.Authorized, fmt.Sprintf("%d scope entries, authorized=%v", len(eng.Scope), eng.Authorized), "define scope and record written authorization"},
		{"2. Reconnaissance", recon > 0, fmt.Sprintf("%d hosts/subdomains/IPs", recon), "map hosts: dns_lookup, subdomain_enum, http_probe"},
		{"3. Enumeration", enum > 0, fmt.Sprintf("%d ports/services/endpoints/technologies", enum), "port_scan, crawl, content_discovery, fingerprint each host"},
		{"4. Vulnerability Testing", len(findings) > 0, fmt.Sprintf("%d confirmed findings", len(findings)), "test each enumerated surface against OWASP WSTG / API Top 10 skills"},
		{"5. Reporting", eng.Report != "", map[bool]string{true: "report compiled", false: "no report"}[eng.Report != ""], "security_report once testing coverage is complete"},
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Engagement: %s (%s)  client=%s  status=%s\n", eng.Name, eng.ID, eng.Client, eng.Status)
	fmt.Fprintf(&b, "Scope: %s\n", strings.Join(eng.Scope, ", "))
	if len(eng.Exclusions) > 0 {
		fmt.Fprintf(&b, "Exclusions: %s\n", strings.Join(eng.Exclusions, ", "))
	}
	if eng.Rules != "" {
		fmt.Fprintf(&b, "Rules of engagement: %s\n", eng.Rules)
	}
	b.WriteString("\nPhases:\n")
	nextSet := false
	for i, p := range phases {
		mark := "[ ]"
		if p.done {
			mark = "[x]"
		}
		fmt.Fprintf(&b, "%s %s — %s\n", mark, p.name, p.ev)
		if p.done && i > 0 && !phases[i-1].done {
			fmt.Fprintf(&b, "    ! earlier phase %q has no evidence yet\n", phases[i-1].name)
		}
		if !p.done && !nextSet {
			fmt.Fprintf(&b, "    -> next: %s\n", p.next)
			nextSet = true
		}
	}
	sev := map[string]int{}
	for _, f := range findings {
		sev[f.Severity]++
	}
	fmt.Fprintf(&b, "\nFindings: critical=%d high=%d medium=%d low=%d info=%d\n", sev["critical"], sev["high"], sev["medium"], sev["low"], sev["info"])
	return b.String()
}

func BuildReport(st *store.Store, eng *store.Engagement, summary string) string {
	findings, _ := st.ListFindings(eng.ID)
	intel, _ := st.ListIntel(eng.ID, "")
	sort.SliceStable(findings, func(i, j int) bool {
		if findings[i].CVSSScore != findings[j].CVSSScore {
			return findings[i].CVSSScore > findings[j].CVSSScore
		}
		return secure.SeverityRank(findings[i].Severity) > secure.SeverityRank(findings[j].Severity)
	})
	sev := map[string]int{}
	for _, f := range findings {
		sev[f.Severity]++
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# Penetration Test Report — %s\n\n", eng.Name)
	fmt.Fprintf(&b, "| | |\n|---|---|\n| Client | %s |\n| Date | %s |\n| Status | %s |\n| Authorized | %v |\n\n", firstNonEmpty(eng.Client, "—"), time.Now().Format("2006-01-02"), eng.Status, eng.Authorized)
	b.WriteString("## Executive summary\n\n")
	if summary == "" {
		summary = fmt.Sprintf("The assessment identified %d issues: %d critical, %d high, %d medium, %d low and %d informational. Address critical and high findings first.", len(findings), sev["critical"], sev["high"], sev["medium"], sev["low"], sev["info"])
	}
	b.WriteString(summary + "\n\n")
	b.WriteString("| Severity | Count |\n|---|---|\n")
	for _, s := range []string{"critical", "high", "medium", "low", "info"} {
		fmt.Fprintf(&b, "| %s | %d |\n", strings.ToUpper(s[:1])+s[1:], sev[s])
	}
	b.WriteString("\n## Scope\n\n")
	for _, s := range eng.Scope {
		fmt.Fprintf(&b, "- `%s`\n", s)
	}
	if len(eng.Exclusions) > 0 {
		b.WriteString("\nOut of scope:\n")
		for _, s := range eng.Exclusions {
			fmt.Fprintf(&b, "- `%s`\n", s)
		}
	}
	if eng.Rules != "" {
		b.WriteString("\nRules of engagement: " + eng.Rules + "\n")
	}
	b.WriteString("\n## Methodology\n\nTesting followed the OWASP Web Security Testing Guide (WSTG), OWASP Top 10:2025, OWASP API Security Top 10:2023, and the standard phases: reconnaissance, enumeration, vulnerability testing with manual verification, and reporting. Severity uses CVSS v3.1 base scores.\n\n")
	b.WriteString("## Findings overview\n\n| # | Title | Severity | CVSS | Target |\n|---|---|---|---|---|\n")
	for i, f := range findings {
		fmt.Fprintf(&b, "| %d | %s | %s | %.1f | %s |\n", i+1, f.Title, f.Severity, f.CVSSScore, f.Target)
	}
	b.WriteString("\n## Detailed findings\n")
	for i, f := range findings {
		fmt.Fprintf(&b, "\n### %d. %s\n\n", i+1, f.Title)
		fmt.Fprintf(&b, "- **Severity:** %s (CVSS %.1f)\n", strings.ToUpper(f.Severity), f.CVSSScore)
		if f.CVSSVector != "" {
			fmt.Fprintf(&b, "- **Vector:** `%s`\n", f.CVSSVector)
		}
		if f.CWE != "" {
			fmt.Fprintf(&b, "- **CWE:** %s\n", f.CWE)
		}
		if f.Category != "" {
			fmt.Fprintf(&b, "- **Category:** %s\n", f.Category)
		}
		fmt.Fprintf(&b, "- **Target:** %s\n- **Status:** %s\n\n", f.Target, f.Status)
		section := func(t, v string) {
			if strings.TrimSpace(v) != "" {
				fmt.Fprintf(&b, "**%s**\n\n%s\n\n", t, v)
			}
		}
		section("Description", f.Description)
		section("Steps to reproduce", f.Reproduction)
		section("Impact", f.Impact)
		if f.Evidence != "" {
			fmt.Fprintf(&b, "**Evidence**\n\n```\n%s\n```\n\n", f.Evidence)
		}
		section("Remediation", f.Remediation)
	}
	if len(intel) > 0 {
		b.WriteString("\n## Appendix A — Attack surface\n\n| Kind | Value | Detail |\n|---|---|---|\n")
		for _, i := range intel {
			fmt.Fprintf(&b, "| %s | %s | %s |\n", i.Kind, i.Value, strings.ReplaceAll(clip(i.Detail, 120), "|", "\\|"))
		}
	}
	b.WriteString("\n---\n_Generated by AllOnes. Findings were recorded by the agent after verification; review before delivery._\n")
	return b.String()
}

func safeName(s string) string {
	s = strings.ToLower(s)
	s = regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(s, "-")
	return strings.Trim(s, "-")
}

// ---------- OSV dependency audit ----------

type Pkg struct{ Ecosystem, Name, Version string }

type VulnPkg struct {
	Pkg
	IDs []string
}

func CollectPackages(root string) []Pkg {
	seen := map[Pkg]bool{}
	var out []Pkg
	add := func(eco, name, ver string) {
		ver = strings.TrimLeft(strings.TrimSpace(ver), "v^~=>< ")
		if name == "" || ver == "" || strings.ContainsAny(ver, "*xX|") {
			return
		}
		p := Pkg{eco, name, ver}
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	st, err := os.Stat(root)
	if err != nil {
		return nil
	}
	walkRoot, single := root, ""
	if !st.IsDir() {
		walkRoot, single = filepath.Dir(root), filepath.Base(root)
	}
	filepath.WalkDir(walkRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if skipDirs[d.Name()] && p != walkRoot {
				return filepath.SkipDir
			}
			return nil
		}
		name := d.Name()
		if single != "" && name != single {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		switch name {
		case "go.mod":
			re := regexp.MustCompile(`(?m)^\s*(?:require\s+)?([\w./\-~]+\.[\w./\-~]+)\s+(v[\w.\-+]+)`)
			for _, m := range re.FindAllStringSubmatch(string(b), -1) {
				add("Go", m[1], strings.TrimPrefix(m[2], "v"))
			}
		case "package-lock.json":
			var lock struct {
				Packages map[string]struct{ Version string } `json:"packages"`
				Deps     map[string]struct{ Version string } `json:"dependencies"`
			}
			json.Unmarshal(b, &lock)
			for k, v := range lock.Packages {
				if i := strings.LastIndex(k, "node_modules/"); i >= 0 {
					add("npm", k[i+13:], v.Version)
				}
			}
			for k, v := range lock.Deps {
				add("npm", k, v.Version)
			}
		case "package.json":
			if _, err := os.Stat(filepath.Join(filepath.Dir(p), "package-lock.json")); err == nil {
				return nil
			}
			var pj struct {
				Deps    map[string]string `json:"dependencies"`
				DevDeps map[string]string `json:"devDependencies"`
			}
			json.Unmarshal(b, &pj)
			for k, v := range pj.Deps {
				add("npm", k, v)
			}
			for k, v := range pj.DevDeps {
				add("npm", k, v)
			}
		case "requirements.txt":
			sc := bufio.NewScanner(bytes.NewReader(b))
			for sc.Scan() {
				l := strings.TrimSpace(strings.SplitN(sc.Text(), "#", 2)[0])
				if n, v, ok := strings.Cut(l, "=="); ok {
					add("PyPI", strings.TrimSpace(strings.SplitN(n, "[", 2)[0]), strings.SplitN(v, ";", 2)[0])
				}
			}
		case "poetry.lock", "Cargo.lock":
			eco := map[string]string{"poetry.lock": "PyPI", "Cargo.lock": "crates.io"}[name]
			re := regexp.MustCompile(`(?m)^name\s*=\s*"([^"]+)"\s*\nversion\s*=\s*"([^"]+)"`)
			for _, m := range re.FindAllStringSubmatch(string(b), -1) {
				add(eco, m[1], m[2])
			}
		case "composer.lock":
			var c struct {
				Packages []struct{ Name, Version string } `json:"packages"`
			}
			json.Unmarshal(b, &c)
			for _, x := range c.Packages {
				add("Packagist", x.Name, x.Version)
			}
		case "Gemfile.lock":
			re := regexp.MustCompile(`(?m)^    ([a-zA-Z0-9_\-]+) \(([0-9][^)]*)\)`)
			for _, m := range re.FindAllStringSubmatch(string(b), -1) {
				add("RubyGems", m[1], m[2])
			}
		}
		if len(out) > 3000 {
			return fs.SkipAll
		}
		return nil
	})
	return out
}

func QueryOSV(ctx context.Context, pkgs []Pkg) ([]VulnPkg, error) {
	var out []VulnPkg
	hc := &http.Client{Timeout: 60 * time.Second}
	for start := 0; start < len(pkgs); start += 500 {
		end := min(start+500, len(pkgs))
		var queries []map[string]any
		for _, p := range pkgs[start:end] {
			queries = append(queries, map[string]any{"version": p.Version, "package": map[string]any{"name": p.Name, "ecosystem": p.Ecosystem}})
		}
		b, _ := json.Marshal(map[string]any{"queries": queries})
		req, _ := http.NewRequestWithContext(ctx, "POST", "https://api.osv.dev/v1/querybatch", bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		resp, err := hc.Do(req)
		if err != nil {
			return nil, err
		}
		var r struct {
			Results []struct {
				Vulns []struct{ ID string } `json:"vulns"`
			} `json:"results"`
		}
		err = json.NewDecoder(resp.Body).Decode(&r)
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		for i, res := range r.Results {
			if len(res.Vulns) == 0 || start+i >= len(pkgs) {
				continue
			}
			v := VulnPkg{Pkg: pkgs[start+i]}
			for _, x := range res.Vulns {
				v.IDs = append(v.IDs, x.ID)
			}
			out = append(out, v)
		}
	}
	return out, nil
}
