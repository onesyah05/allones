# Subscription providers: Codex, Antigravity, Claude Code

Open **Providers → Subscriptions**, select a provider and choose **Sign in**. No API key is needed for these entries. Existing API-key providers are unchanged.

- **Codex**: open the displayed authorization URL, enter the device code, and approve your ChatGPT account.
- **Claude Code**: open the authorization link, sign in to Claude and approve. The callback listener uses `http://localhost:54545/callback`.
- **Antigravity**: sign in with Google and approve the requested scopes. Callback: `http://127.0.0.1:51121/oauth-callback`. The adapter discovers/onboards the account's Cloud Code project, using the daily endpoint from the reference implementation.

The dashboard polls until success, rejection or cancellation. Logins expire after at most 15 minutes. Callback ports must be free on the machine running AllOnes. If the dashboard is accessed remotely, authorization must return to that same machine, not a different computer's localhost.

After login, use **Fetch models**, enable the models available to your account, **Test**, then choose a default model. Catalog IDs are suggestions, not a promise of account entitlement. Provider availability and OAuth client policy are vendor-controlled.

## Implementation

Based on read-only reference `D:/video/server/subscription`: device flow and PKCE, native Codex Responses API, Claude OAuth Messages API, and Antigravity Cloud Code envelope. Text, reasoning, images, history, tool calls, usage and model discovery are translated to AllOnes' existing interface. OAuth state is validated before consuming a callback. Login/status API responses never include access or refresh tokens.

Credentials are stored in the AllOnes `config.yaml` under `ALLONES_HOME`, following existing provider credential storage (not vault encryption). Protect this directory; do not commit/share it. Expired OAuth tokens are refreshed and persisted before chat, auxiliary requests, model listing and provider tests. Refresh operations are serialized in-process. Do not run multiple AllOnes processes against the same credential file.

AllOnes does not import tokens or accounts from `D:/video` or local vendor CLIs. Users must authorize each account themselves.

## Verification

Mock HTTP integration tests cover token exchange, refresh rotation, device polling, PKCE state rejection, cancellation, timeout/listener cleanup, Codex/Antigravity streaming and tool calls, Claude bearer authentication, and provider login route registration. These are protocol-level tests; successful live authorization and model calls require the user's own vendor accounts and have not been claimed.

Run: `go test ./...`; dashboard: `cd web && bun test && bun run build`; full binary: `make build`.
