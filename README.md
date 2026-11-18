<div align="center">

<br>

### ✴ A L L O N E S

**The self-hosted AI agent that does the work — with a sharp edge for security.**

One Go binary. Your machine, your models, no SaaS. Authorized pentesting, reverse
engineering and code auditing sit next to files, shell, a real browser, durable
memory, hybrid RAG, MCP and a team of specialist roles — behind one quiet dashboard.

<br>

[![Go](https://img.shields.io/badge/Go-1.26-00ADD8?style=flat-square&logo=go&logoColor=white)](https://go.dev)
[![React](https://img.shields.io/badge/React-19-0b7285?style=flat-square&logo=react&logoColor=white)](https://react.dev)
[![License](https://img.shields.io/badge/license-Apache--2.0-e8001f?style=flat-square)](LICENSE)
[![Platforms](https://img.shields.io/badge/Linux_·_macOS_·_Windows-1c1c1f?style=flat-square)](#install)
[![Release](https://img.shields.io/github/v/release/onesyah05/allones?style=flat-square&color=4cc38a)](https://github.com/onesyah05/allones/releases)

<br>

</div>

---

## Why AllOnes

Most "AI agents" are a chat box with a system prompt. **AllOnes is a workspace** — it
reads and writes your files, runs your shell, drives a real browser, remembers, plans,
delegates, and keeps going when a run gets long. And it is built **security-first from
the inside out**, not bolted on after.

```
   nocturnal · exact · watchful
   ─────────────────────────────
   you  ▸  an agent with real tools  ▸  work that ships
```

<table>
<tr>
<td width="50%" valign="top">

**🛡 Security-first by construction**
Secret redaction on every tool result · SSRF guard on the agent's own HTTP ·
engagement-scope enforcement on network tools · destructive-command guards ·
untrusted-content fencing (OWASP LLM01) · an at-rest encryption vault · a full audit log.

**🎯 Authorized pentesting, end to end**
Define an engagement (scope + written authorization). Specialist roles map the target,
test it against **OWASP Top 10:2025** and **API Top 10:2023**, record each confirmed
finding with a computed **CVSS v3.1** score and reproduction, and compile a professional
Markdown report. A methodology tracker won't call a phase done without the evidence.

**🔬 Reverse engineering**
Inspect shipped binaries (ELF/PE/Mach-O hardening, imports, strings, hex) and drive
**REA — Reverse Engineer Anything** for native, JS/Electron, .NET and packaged apps.

</td>
<td width="50%" valign="top">

**🧰 Everything an agent needs**
Files, a persistent shell, a real Chromium, web search + fetch, durable memory, a
built-in hybrid **RAG** index, and delegation to a team of specialist sub-agents.

**♾ It keeps going**
A harness with a repetition guard, context compaction, reply verification, standing
goals with a completion judge, and live steering. Schedules its own unattended runs.

**📦 One binary, self-hosted**
A Go backend and a React 19 dashboard in a single process. Pure-Go dependencies — no
CGO, no runtime to install. Bring your own models.

**⬆ Updates itself**
The dashboard watches GitHub for new releases and installs them in one click — download,
swap, restart. No package manager, no reinstall.

</td>
</tr>
</table>

> [!IMPORTANT]
> AllOnes performs security testing only against targets you **own or are authorized to
> assess**. It ships no exploit payloads or offensive tooling — it gives authorized testers
> the same tools every role has, under scope, and the structure to turn findings into a report.

---

## Install

### Download a release *(recommended)*

Grab the binary for your platform from the [**latest release**](https://github.com/onesyah05/allones/releases/latest)
— it already has the dashboard baked in. Then run it:

```bash
# macOS / Linux
chmod +x allones_* && ./allones_*        # first run: open the dashboard and finish setup

# Windows (PowerShell)
.\allones_windows_amd64.exe
```

Once you're running, AllOnes keeps itself current: when a newer release lands, an **Update**
pill appears in the sidebar and the **System** page installs it for you.

### Build from source

Needs **Go 1.26+** and **Bun** (or npm) for the dashboard.

```bash
git clone https://github.com/onesyah05/allones && cd allones
make build          # builds the dashboard, embeds it, stamps the version, produces ./allones(.exe)
./allones           # first run: open the dashboard and finish setup
```

Open **<http://localhost:8787>**, pick a provider, paste a key, and go. Prefer the terminal?
`./allones setup`. Provider keys are also auto-detected from the environment:

```bash
export ANTHROPIC_API_KEY=…     # or OPENAI_API_KEY, GEMINI_API_KEY, OPENROUTER_API_KEY
./allones
```

### Develop

```bash
make dev            # backend on :8787 + Vite dev server on :5273 (proxies /api)
```

Open **:5273** for a hot-reloading UI; **:8787** serves the embedded bundle.

---

## The tools

Forty-plus tools across ten categories, grouped into toolsets a role or conversation selects.

| Category | Tools |
|---|---|
| **files** | `read_file` · `write_file` · `edit_file` · `list_files` · `glob` · `grep` · `rollback` |
| **shell** | `shell` (persistent cwd) · `process` (background) |
| **web** | `web_search` · `web_fetch` · `http_request` (SSRF-guarded) |
| **browser** | `browser` — real Chromium: navigate, click, type, snapshot, screenshot, cookies |
| **memory** | `memory` · `session_search` · `rag_search` · `rag_index` |
| **agent** | `todo` · `skill` · `delegate_task` · `list_roles` · `ask_user` |
| **security** | `engagement` · `add_intel` · `report_finding` · `cvss` · `security_report` · `secret_scan` · `dependency_audit` (OSV) · `code_audit` · `jwt_inspect` · `codec` |
| **recon** | `dns_lookup` · `port_scan` · `http_probe` |
| **reverse** | `binary_inspect` · `strings_extract` · `hexdump` · `rea` |
| **ops** | `schedule` · `board` · `notify` · `vps_run` · `vps_transfer` |
| **sec_tool** | runs installed CLIs (`nuclei`, `httpx`, `subfinder`, `katana`, `nmap`, `ffuf`, `sqlmap`, `semgrep`, `gitleaks`, `trivy`, …) on an allowlist, scope-checked |

Plus any tools exposed by connected **MCP** servers (`mcp__server__tool`).

## The roles

`assistant` · `planner` · `coder` · `reviewer` · `researcher` · `writer` · `data-analyst` ·
`sre` — and the security team: `security-lead` · `security-recon` · `security-webapp` ·
`security-api` · `ai-red-team` · `reverse-engineer` · `appsec-auditor` · `threat-modeler` ·
`incident-responder`. Each has its own instructions, toolset and (optionally) model.
Delegate to one, or run a whole conversation as one.

## The harness

Everything wrapped around the model call so a long run fails *usefully*, not quietly:

- **Repetition guard** — catches the same call made over and over and redirects it.
- **Compaction** — summarises older turns when the window fills, without splitting a tool call from its result.
- **Verification** *(opt-in)* — a cheap model checks a finished answer against the request and sends the turn back if something is missing.
- **Standing goals** — a goal outlives a turn; a judge decides when it is actually met.
- **Steering** — nudge a running turn between tool batches without discarding its work.
- **Rollback** — every file write is snapshotted; undo the whole session or one file.

See [docs/architecture.md](docs/architecture.md).

---

## Configuration

Everything lives in `~/.allones/config.yaml`, editable from the dashboard, the CLI, or the
file. Environment variables override the file; `~/.allones/.env` is loaded automatically.

```bash
allones config get model.default
allones config set tools.approval_mode auto
allones config path
```

A non-loopback bind (`server.host: 0.0.0.0`) requires a dashboard password or `server.token`;
AllOnes refuses to start otherwise.

---

## Releases & versioning

AllOnes follows **semantic versioning**. Tagging a commit `vX.Y.Z` and pushing it runs the
[release workflow](.github/workflows/release.yml): it builds the dashboard, cross-compiles
for Linux, macOS and Windows (amd64 + arm64), stamps the version into the binary, and
publishes GitHub Release assets named `allones_<os>_<arch>`. The in-app updater looks for
exactly those — so every tagged release is an over-the-air update for every running instance.

```bash
# cut a release
echo 1.1.0 > VERSION && git commit -am "release: v1.1.0"
git tag v1.1.0 && git push origin main --tags
```

---

## Reverse Engineer Anything (REA)

The `reverse-engineer-anything` skill is bundled from
[morluto/rea](https://github.com/morluto/rea) (MIT). The `rea` tool runs the upstream
`rea-agents` CLI (`npx -y rea-agents@latest`) or an installed `rea` binary; register REA's
MCP server on the **MCP** page (`npx -y rea-agents@latest mcp`). Analysis runs locally —
install REA from its official sources. See [NOTICE](NOTICE).

---

## License

**Apache-2.0.** Bundled third-party material and its licenses are listed in [NOTICE](NOTICE).

<div align="center"><br><sub>Built to be watchful, exact, and entirely yours.</sub></div>
