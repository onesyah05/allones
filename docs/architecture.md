# Architecture

One process, one binary, no runtime to install.

```
cmd/allones/            the CLI: serve, setup, config
internal/
  config/               layered configuration (defaults → yaml → env) + schema
  store/                SQLite (pure-Go), migrations, every persisted record
  secure/               redaction, SSRF, scope, CVSS v3.1, command guard, vault
  llm/                  provider adapters (OpenAI / Anthropic / Gemini) + SSE streaming
  tools/                the callable tool surface and the toolsets
  skills/               embedded skill library + on-disk discovery (incl. REA)
  roles/                specialist personas
  rag/                  hybrid retrieval (dense + FTS5, reciprocal-rank fusion)
  cron/                 cron expression parser
  scheduler/            runs cron jobs as unattended agent turns
  agent/                the turn loop, the harness, sub-agent delegation
  mcp/                  Model Context Protocol client (stdio + streamable HTTP)
  server/               HTTP API, SSE chat, slash commands, embedded dashboard
web/                    the React dashboard (Vite + React 19 + Tailwind v4)
```

## The turn

1. `server` (or the scheduler, or a sub-agent) calls `agent.Run` with a request.
2. The session is resolved or created; history is loaded.
3. The system prompt is assembled: identity, how-it-works, the role, the security
   posture, environment, standing goal, engagement, memory, the skill catalogue.
4. If the history is close to the context window, older turns are summarised.
5. The model is called, streaming reasoning and text out as they arrive.
6. Tool calls are dispatched in parallel, each streaming its own progress; mutating
   tools ask for approval first (in prompt mode).
7. Results — secret-redacted, and fenced if they came from outside — go back into the
   history and the loop repeats.
8. When the model stops calling tools, the harness decides whether it is really
   finished (steering? verification? goal judge?).
9. `done` is emitted. `Run` owns the terminal events and also broadcasts every event
   to session subscribers, so a second tab (or the approval side-channel) sees them.

## Security model

Guardrails sit between the model and the world, in `internal/secure` and enforced in
`internal/tools`:

- **Secret redaction** on every tool result and report (gitleaks-style rules + entropy).
- **SSRF protection**: the agent's HTTP/browser dial through a `SafeDialer` that resolves
  at connect time and refuses private/loopback/metadata addresses — except hosts inside the
  active engagement scope (the point of an internal test).
- **Scope enforcement**: network tools check targets against the engagement's scope and
  exclusions (`off` / `warn` / `block`).
- **Dangerous-command guard**: destructive shell patterns always require approval.
- **Untrusted-content fencing**: web/tool output is wrapped so the model treats it as data,
  not instructions (OWASP LLM01).
- **At-rest vault**: SSH credentials are sealed with AES-256-GCM; the key lives in
  `~/.allones/master.key`, never in the DB, logs, API or prompt.
- **Audit log**: every mutating action, finding, scope violation and sign-in is recorded.

## Choices

- **Go, standard library where it can be.** One static binary, pure-Go dependencies, no
  CGO. `net/http` method-and-pattern routing; `database/sql` over `modernc.org/sqlite`;
  `embed` carries the dashboard.
- **SSE for streaming** to every surface, with keepalive pings.
- **CDP directly** for the browser (chromedp), no driver process.
- **One `Store`** behind typed methods; timestamps as unix-millis; FTS5 for search.
