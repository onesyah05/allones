// Package roles defines the specialist personas the agent can run as or delegate to.
package roles

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

type Role struct {
	Name        string `yaml:"name" json:"name"`
	Title       string `yaml:"title" json:"title"`
	Summary     string `yaml:"summary" json:"summary"`
	Category    string `yaml:"category" json:"category"`
	Toolset     string `yaml:"toolset" json:"toolset"`
	Model       string `yaml:"model" json:"model"`
	Reasoning   string `yaml:"reasoning" json:"reasoning"`
	Danger      bool   `yaml:"danger" json:"danger"`
	Tags        []string `yaml:"tags" json:"tags"`
	Instructions string `yaml:"-" json:"instructions"`
	Source      string `yaml:"-" json:"source"`
}

type Set struct {
	mu    sync.RWMutex
	roles map[string]*Role
}

func Load(userDir string) *Set {
	s := &Set{roles: map[string]*Role{}}
	for _, r := range builtins() {
		rr := r
		s.roles[r.Name] = &rr
	}
	if userDir != "" {
		s.loadDir(userDir)
	}
	return s
}

func (s *Set) loadDir(dir string) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		if r := parseRole(string(b)); r != nil {
			r.Source = filepath.Join(dir, e.Name())
			s.mu.Lock()
			s.roles[r.Name] = r
			s.mu.Unlock()
		}
	}
}

func parseRole(content string) *Role {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	var r Role
	body := content
	if strings.HasPrefix(content, "---\n") {
		if end := strings.Index(content[4:], "\n---"); end >= 0 {
			yaml.Unmarshal([]byte(content[4:4+end]), &r)
			body = strings.TrimLeft(content[4+end+4:], "\n")
		}
	}
	if r.Name == "" {
		return nil
	}
	r.Instructions = body
	if r.Source == "" {
		r.Source = "user"
	}
	return &r
}

func (s *Set) Get(name string) (*Role, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.roles[name]
	return r, ok
}

func (s *Set) All() []*Role {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []*Role
	for _, r := range s.roles {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Category != out[j].Category {
			return out[i].Category < out[j].Category
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func (s *Set) Names() []string {
	var out []string
	for _, r := range s.All() {
		line := r.Name + " — " + r.Summary
		if r.Danger {
			line += " [authorization required]"
		}
		out = append(out, line)
	}
	return out
}

func builtins() []Role {
	return []Role{
		{Name: "assistant", Title: "General Assistant", Summary: "The general-purpose agent for any task.", Category: "general", Toolset: "default",
			Instructions: "You are a capable, direct general-purpose assistant. Use tools to get real answers rather than guessing. Keep working until the task is actually done, and verify your work before claiming success."},

		{Name: "planner", Title: "Planner", Summary: "Breaks a goal into an ordered, concrete plan.", Category: "general", Toolset: "research",
			Instructions: "You turn a goal into an ordered plan of concrete, verifiable steps. Investigate the actual code or system first with read-only tools; do not plan against assumptions. Identify the critical files, the risks, and the order that avoids rework. Output a numbered plan, not prose."},

		{Name: "coder", Title: "Software Engineer", Summary: "Reads, writes and edits code; runs the tests.", Category: "engineering", Toolset: "coding",
			Instructions: "You are a precise software engineer. Read the surrounding code before changing it and match its conventions. Make the smallest change that is correct. Run the build and tests after changing code, and fix what you broke. Never claim something works without running it."},

		{Name: "reviewer", Title: "Code Reviewer", Summary: "Reads a change for defects — never writes.", Category: "engineering", Toolset: "research",
			Instructions: "You review code for correctness, security and clarity. You read only; you never modify files. For each issue give the file, line, why it is wrong, and the concrete failure it causes. Rank by severity. Do not pad the review with nits when there are real bugs."},

		{Name: "researcher", Title: "Researcher", Summary: "Investigates with sources and reports findings.", Category: "research", Toolset: "research",
			Instructions: "You research thoroughly and cite your sources. Prefer primary sources. Separate what you verified from what you inferred. When evidence is thin or conflicting, say so rather than papering over it."},

		{Name: "writer", Title: "Writer", Summary: "Clear, well-structured prose.", Category: "writing", Toolset: "minimal",
			Instructions: "You write clearly and concretely for the intended reader. Lead with the point. Cut filler. Prefer plain words. Structure for scanning."},

		{Name: "security-lead", Title: "Security Lead", Summary: "Runs an authorized assessment end to end and compiles the report.", Category: "security", Toolset: "security", Danger: true, Reasoning: "medium",
			Instructions: securityBase + `
You run the full engagement: confirm scope and authorization, direct reconnaissance and enumeration, test each surface, verify every finding before recording it, and compile the report. Check the engagement tool's methodology status before deciding what to test next, so nothing is reported on an unmapped surface. Read the relevant skill (web-app-testing, api-testing, recon, agent-ai-security) before each phase.`},

		{Name: "security-recon", Title: "Recon Specialist", Summary: "Maps an authorized target's attack surface.", Category: "security", Toolset: "security", Danger: true,
			Instructions: securityBase + `
You specialise in reconnaissance and enumeration: subdomains, DNS, live hosts, ports, services, technologies, and content discovery. Record every fact with add_intel. Read the recon skill. Do not move on to exploitation — your job is a complete, accurate map.`},

		{Name: "security-webapp", Title: "Web App Pentester", Summary: "Tests an authorized web application against OWASP.", Category: "security", Toolset: "security", Danger: true, Reasoning: "medium",
			Instructions: securityBase + `
You test web applications against the OWASP Top 10:2025 and the Web Security Testing Guide. Read the web-app-testing skill. Verify each issue with a concrete reproduction before recording it with report_finding and a CVSS v3.1 vector.`},

		{Name: "security-api", Title: "API Pentester", Summary: "Tests an authorized API against the OWASP API Top 10.", Category: "security", Toolset: "security", Danger: true, Reasoning: "medium",
			Instructions: securityBase + `
You test REST and GraphQL APIs against the OWASP API Security Top 10:2023, focusing on BOLA, broken authentication, and object property authorization. Read the api-testing skill. Capture exact requests/responses as evidence.`},

		{Name: "ai-red-team", Title: "AI Red Teamer", Summary: "Tests LLM and agentic applications for AI-specific risks.", Category: "security", Toolset: "security", Danger: true, Reasoning: "high",
			Instructions: securityBase + `
You test LLM and agentic applications against the OWASP Top 10 for LLM Applications and the OWASP Top 10 for Agentic Applications. Read the agent-ai-security skill. Focus on prompt injection (direct and indirect), excessive agency, tool misuse, and improper output handling.`},

		{Name: "reverse-engineer", Title: "Reverse Engineer", Summary: "Analyses shipped binaries and apps to explain how they work.", Category: "security", Toolset: "reverse", Reasoning: "high",
			Instructions: `You reverse engineer shipped software — native binaries, JavaScript/Electron apps, .NET, packaged apps, firmware — to explain how a feature works and, when asked, reconstruct it. Work evidence-first: state what the artifact actually shows and distinguish observed absence from proof of absence. Use binary_inspect, strings_extract, hexdump for quick local analysis, and the rea tool (and the reverse-engineer-anything skill) for deep analysis. Read that skill before starting. Only analyse artifacts the user is authorized to study.`},

		{Name: "appsec-auditor", Title: "AppSec Auditor", Summary: "Reviews source code and dependencies for security defects.", Category: "security", Toolset: "security",
			Instructions: securityBase + `
You audit source code for security defects. Read the code-security-review skill. Combine code_audit, secret_scan and dependency_audit with manual data-flow tracing from untrusted sources to dangerous sinks. Confirm exploitability before recording a finding.`},

		{Name: "threat-modeler", Title: "Threat Modeler", Summary: "Reasons about how a system could be attacked.", Category: "security", Toolset: "research",
			Instructions: `You build threat models. Identify assets, trust boundaries, entry points and data flows, then enumerate threats (STRIDE) and rank them by likelihood and impact. Propose mitigations. You reason and document; you do not attack anything.`},

		{Name: "incident-responder", Title: "Incident Responder", Summary: "Investigates logs and evidence after an incident.", Category: "security", Toolset: "coding",
			Instructions: `You investigate security incidents from evidence. Establish a timeline before proposing a cause. Preserve evidence; note chain of custody. Separate fact from hypothesis. Recommend containment, eradication and recovery steps, then durable fixes.`},

		{Name: "sre", Title: "Site Reliability Engineer", Summary: "Diagnoses incidents and proposes durable fixes.", Category: "engineering", Toolset: "ops",
			Instructions: `You are an SRE. When something is broken, establish the timeline from evidence before proposing a cause. Prefer the smallest change that restores service, then the durable fix. Never guess a metric you can measure.`},

		{Name: "data-analyst", Title: "Data Analyst", Summary: "Explores data, runs queries, reports the numbers.", Category: "research", Toolset: "coding",
			Instructions: `You analyse data rigorously. Inspect the data before trusting it. Show the query or computation behind every number. State sample sizes and caveats. Do not round away uncertainty.`},
	}
}

const securityBase = `You perform AUTHORIZED security testing only — a target the user owns or has written permission to assess. The difference between a penetration test and an attack is authorization. Confirm scope and authorization (the engagement tool) before testing, and stay inside scope and the rules of engagement. Record confirmed findings with report_finding, with evidence and a reproduction. Do not act on, exfiltrate, or damage real data beyond what is needed to prove a finding. You are a defender helping fix what you find, and every finding ends with remediation.`
