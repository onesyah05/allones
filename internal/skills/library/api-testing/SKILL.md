---
name: api-testing
description: Methodology for testing an authorized REST/GraphQL API against the OWASP API Security Top 10:2023. Use when assessing an API, testing endpoints for auth/authorization/injection flaws, or doing an authorized API pentest.
tags: [security, api, owasp, graphql]
triggers: [api, rest, graphql, bola, endpoint, swagger, openapi]
---

# API security testing (authorized)

Confirm authorization and scope (`engagement`) first.

## Discover the surface

Find the API contract: OpenAPI/Swagger, GraphQL introspection, HAR captures, JS
bundles (`web_fetch`/`strings_extract` on the frontend bundle). Enumerate every
endpoint, method, parameter and auth requirement with `add_intel`.

## OWASP API Security Top 10:2023

1. **API1 BOLA** (Broken Object Level Authorization) — the most common API flaw.
   For every `/resource/{id}`, swap the id to another user's object with a
   low-privilege token. Confirm with two accounts.
2. **API2 Broken Authentication** — weak tokens, no expiry, JWT flaws
   (`jwt_inspect`), credential stuffing surface, missing rate limits on auth.
3. **API3 Broken Object Property Level Authorization** — mass assignment (send
   extra fields like `role`, `isAdmin`) and excessive data exposure (the API
   returns fields the UI hides).
4. **API4 Unrestricted Resource Consumption** — missing pagination/rate limits,
   large payloads, expensive GraphQL queries (deep nesting, aliases, batching).
5. **API5 Broken Function Level Authorization** — call admin endpoints as a
   normal user; swap GET→PUT/DELETE.
6. **API6 Unrestricted Access to Sensitive Business Flows** — automation abuse.
7. **API7 SSRF** — URL parameters the server fetches.
8. **API8 Security Misconfiguration** — CORS, verbose errors, missing headers.
9. **API9 Improper Inventory Management** — old/undocumented versions
   (`/v1` vs `/v2`), debug/staging hosts.
10. **API10 Unsafe Consumption of Third-party APIs**.

## GraphQL specifics

Enable-check introspection; map the schema; test authorization per field; probe
query depth/alias/batch limits; look for injection in resolver arguments.

## Recording

Use `http_request` to capture exact reproductions. Record confirmed issues with
`report_finding` (CVSS v3.1, CWE, API category, reproduction, impact, fix).
