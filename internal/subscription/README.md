# Subscription OAuth integration

Create one long-lived manager per server:

```go
logins := subscription.New(cfg) // cfg is *config.Manager
status, err := logins.Start("codex") // also "claude-code", "antigravity"
status, found := logins.Status(id)
cancelled := logins.Cancel(id)
```

Expose these operations only to authenticated local UI clients. Start returns a
`starting` status immediately. Poll until `awaiting_user`, then show `url` and
`user_code` (Codex only). Terminal phases are `done`, `error`, `cancelled`.
A returned URL is for the user's browser; this package never launches a process.
Statuses have no credentials. Error messages intentionally omit upstream bodies.
One login per kind can run concurrently; a duplicate Start returns an error.
Completed statuses are retained until a later Start prunes the bounded history.

On success the manager calls cfg.Update, upserting by exact name and kind:
`codex`, `claude-code`, `antigravity`. Existing provider settings are preserved;
APIKey, RefreshToken, ExpiresAt (Unix seconds), OAuth, Disabled and FromEnv are
updated. Codex JWT account routing metadata, when present, is saved in the
`ChatGPT-Account-Id` header. JWT parsing is not authentication verification.
No account stores, local vendor credentials, environment secrets, or reference
configuration are imported.

The main config.Provider must supply these fields (owned by the integrator):

```go
RefreshToken string `yaml:"refresh_token" json:"-"`
ExpiresAt    int64  `yaml:"expires_at" json:"expires_at"`
OAuth        bool  `yaml:"oauth" json:"oauth"`
```

For the llm Resolve refresh path:

```go
updated, err := subscription.Refresh(ctx, &provider)
// On success persist *updated using cfg.Update, then resolve with the new token.
```

Refresh returns a copy and preserves the old refresh token when the vendor does
not rotate it. The caller must serialize refreshes per provider and persist
rotations. This package deliberately imports no llm code.

## Operational limits

* Login flows have an absolute 15-minute maximum including exchanges. Individual
  HTTP requests have a 30-second timeout. Cancelling closes the callback listener
  asynchronously and prevents subsequent token persistence.
* Claude binds IPv4 loopback port 54545, keeping the registered redirect exactly
  `http://localhost:54545/callback`. Antigravity binds 51121 with registered URL
  `http://127.0.0.1:51121/oauth-callback`. A busy port fails the asynchronous flow.
  The user's browser must run on the server's machine (or use explicit loopback
  forwarding); IPv6-only localhost resolution is not supported.
* Callbacks validate state before consuming success **or error**. Invalid state
  does not prevent a later valid callback. PKCE verifier stays server-side.
* Codex uses the reference device endpoints, waits the requested poll interval,
  treats 403/404 as pending, then exchanges the returned code and verifier.
* Antigravity uses the reference public installed-app identity (including its
  public client secret). No project discovery or Cloud Code provisioning is
  performed. Configure existing project/routing headers in the provider when the
  upstream inference implementation needs them; these headers survive login and
  refresh. Optional vendor userinfo/email labels are not fetched.
* This implements OAuth only, not inference protocols, model discovery, automatic
  refresh scheduling, server routes, UI, or config schema changes. Vendor policy,
  account entitlement, and registered-client changes can prevent live login.
* Network tests use injected private endpoints and local callbacks; no live
  vendor/account authorization was attempted.

Run `go test ./internal/subscription` (and `-race` where supported). Tests cover
PKCE exchange for both callback vendors, bad-state non-consumption, device
pending polls and exchange, cancellation, timeout/listener cleanup, refresh
preservation, error sanitization, and UI status credential exclusion.
