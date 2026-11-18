---
name: web-app-testing
description: Methodology for testing an authorized web application against the OWASP Top 10:2025 and the OWASP Web Security Testing Guide. Use when assessing a web app's security, hunting for web vulnerabilities, or doing an authorized web pentest.
tags: [security, web, owasp]
triggers: [owasp, web app, xss, sqli, idor, pentest, websec]
---

# Web application security testing (authorized)

Only test a target whose owner authorized the assessment. Confirm the engagement
scope (`engagement` tool) before touching anything, and stay inside it.

## Order of work

1. **Map the surface.** Crawl with `browser` or `sec_tool katana`; record every
   host, endpoint, parameter and technology with `add_intel`. Fingerprint with
   `http_probe` (server, framework, security headers).
2. **Authentication & session.** Test credential policy, lockout, password reset
   flow, session fixation, cookie flags (HttpOnly/Secure/SameSite), logout,
   remember-me, MFA bypass, and JWT handling (`jwt_inspect`).
3. **Authorization.** This is #1 on the OWASP Top 10:2025 (Broken Access
   Control). For every object reference and privileged action, test horizontal
   and vertical access as a lower-privileged or unauthenticated user: IDOR, path
   traversal, forced browsing, mass assignment, and missing function-level
   checks. Verify with two accounts.
4. **Input handling.** Injection (SQL/NoSQL/command/LDAP/template), XSS
   (reflected/stored/DOM), SSRF (A-moved into Broken Access Control in 2025),
   open redirect, XXE, deserialization, file upload. Reproduce each with
   `http_request` and capture the exact request/response.
5. **Configuration & supply chain.** Security misconfiguration is #2 in 2025 and
   software supply chain failures are #3: check for debug endpoints, default
   creds, verbose errors, exposed admin, CORS misconfig, missing headers, and
   vulnerable dependencies (`dependency_audit`).
6. **Business logic.** Race conditions, workflow bypass, price/quantity
   tampering, replay. These need human reasoning, not a scanner.

## Recording

Confirm a bug before recording it — a scanner hit is a lead, not a finding. Use
`report_finding` with a CVSS v3.1 vector, exact reproduction, impact, and
remediation. Prefer manual verification over scanner output alone. Run
`engagement` to see which methodology phase still lacks evidence.

## OWASP Top 10:2025

A01 Broken Access Control · A02 Security Misconfiguration · A03 Software Supply
Chain Failures · A04 Cryptographic Failures · A05 Injection · A06 Insecure
Design · A07 Authentication Failures · A08 Software & Data Integrity Failures ·
A09 Logging & Monitoring Failures · A10 Mishandling of Exceptional Conditions.
