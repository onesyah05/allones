# Changelog

All notable changes to **AllOnes** are documented here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and
this project adheres to [Semantic Versioning](https://semver.org/). Every tagged
release publishes the matching section below as its GitHub Release notes.

---

## [1.1.1] — 2026-11-18

### Fixed
- **Codex (ChatGPT) subscription login.** OpenAI's device-authorization endpoint
  returns `interval` as a string (`"5"`), which a strict `float64` field rejected
  — the login failed instantly with an opaque *"Login failed"* before any code was
  shown. The device start now accepts a string or numeric interval, and a failed
  login reports the real reason instead of a generic message.

## [1.1.0] — 2026-11-18

A maintenance release that makes every future release documented and the build
reliably green.

### Added
- **Curated release notes.** Each GitHub Release now publishes the matching
  section of this changelog alongside its binaries, so a version's notes are
  written by a human, not stitched from commit subjects.
- This `CHANGELOG.md`.

### Changed
- The release workflow builds its notes from `CHANGELOG.md` (`body_path`) instead
  of auto-generating them.

### Fixed
- **CI stability.** `TestBrowserLifecycle` no longer flakes on teardown. Chromium
  keeps flushing its profile directory as it exits, which raced the test's
  temp-dir cleanup and failed with *"directory not empty"*. The test now owns its
  temp directory and removes it best-effort after the browser has closed.

## [1.0.0] — 2026-11-18

First public release — a self-hosted AI agent and assistant in one binary, built
security-first from the inside out.

### Agent & harness
- Multi-provider streaming agent (OpenAI, Anthropic, Gemini and OpenAI-compatible
  endpoints) with a parallel tool loop and live Server-Sent Events.
- A harness that fails *usefully* on long runs: repetition guard, context
  compaction, opt-in reply verification, standing goals with a completion judge,
  live steering, and per-file rollback.
- A team of 17 specialist roles (assistant, planner, coder, reviewer, researcher,
  writer, data-analyst, sre, and a nine-strong security team) with delegation.

### Security
- Secret redaction on every tool result, an SSRF guard on the agent's own HTTP,
  engagement-scope enforcement on network tools, destructive-command guards,
  untrusted-content fencing (OWASP LLM01), an at-rest encryption vault, and a
  full audit log.
- Authorized-pentest workflow end to end: engagements with written authorization,
  confirmed findings with computed CVSS v3.1 scores and reproduction, and a
  professional Markdown report with a methodology tracker.

### Tools
- 40+ tools across files, a persistent shell, SSRF-guarded web, a real Chromium
  browser, memory, hybrid RAG, recon, reverse engineering (incl. REA), security,
  and ops — plus any tool exposed by a connected MCP server.

### Dashboard
- A React 19 dashboard embedded in the binary: Chat, Security, Board, Automation,
  Files, Tools, Skills, Roles, Memory, Knowledge, MCP, Servers, Settings, System.
- **Chat that keeps running.** Streaming lives above the router, so a turn keeps
  going when you switch menus or sessions — one active turn at a time, with a live
  indicator on the Chat nav, a spinner on the working session, and a locked
  composer elsewhere until it finishes.
- A compact agent-kind dropdown, image paste and attach with thumbnails, and
  last-used model and session remembered as defaults.
- Reasoning-model narration is visible live and kept in the saved transcript.

### Updates
- **In-app self-update.** The dashboard watches GitHub for new releases and, on
  one click, downloads the right binary, swaps it in, and restarts — an Update
  pill in the sidebar and an Update card on the System page.

### Platform
- One pure-Go binary (no CGO, no runtime to install) for Linux, macOS and Windows
  on amd64 and arm64. Your machine, your models, no SaaS.

[1.1.1]: https://github.com/onesyah05/allones/releases/tag/v1.1.1
[1.1.0]: https://github.com/onesyah05/allones/releases/tag/v1.1.0
[1.0.0]: https://github.com/onesyah05/allones/releases/tag/v1.0.0
