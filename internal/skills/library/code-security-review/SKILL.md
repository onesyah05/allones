---
name: code-security-review
description: Review source code for security defects — injection, auth/authz flaws, crypto misuse, SSRF, deserialization, secrets, and dependency vulnerabilities. Use when auditing a codebase or a diff for security issues.
tags: [security, sast, code-review, audit]
triggers: [code audit, security review, sast, vulnerability, secure code]
---

# Secure code review

Combine fast tooling with human reasoning. Tools find patterns; you confirm
exploitability and impact.

## Pass 1 — automated sweep

- `code_audit <path>` — fast built-in pattern scan (injection sinks, weak crypto,
  disabled TLS verification, XSS/SSRF sinks, unsafe deserialization, hard-coded
  secrets). Leads, not verdicts.
- `secret_scan <path>` — leaked credentials (entropy-filtered).
- `dependency_audit <path>` — known-vulnerable dependencies via OSV.
- Deeper, if installed: `sec_tool semgrep --config auto`, `sec_tool gitleaks`,
  `sec_tool trivy fs`, `sec_tool codeql`.

## Pass 2 — trace the untrusted data

For each source of attacker-controlled input (HTTP params, headers, uploaded
files, message queues, external APIs, deserialized blobs), trace it to every
sink:

- SQL/ORM → parameterized? (CWE-89)
- OS command / subprocess → shell=true or concatenation? (CWE-78)
- File path → traversal? (CWE-22)
- HTML/template → escaped? (CWE-79)
- Outbound URL → SSRF? (CWE-918)
- Deserializer → arbitrary type? (CWE-502)
- Redirect target → open redirect? (CWE-601)

## Pass 3 — the things scanners miss

Authorization logic (who can do what), authentication flows, business logic,
race conditions, crypto design (not just weak primitives), secrets management,
and trust boundaries. Read the code; a scanner cannot reason about intent.

## Reporting

For each confirmed issue, record a `report_finding` with the CWE, the exact file
and line, a reproduction or proof, impact, and the concrete fix (prefer the
minimal secure change). Reference Trail of Bits' CodeQL/Semgrep variant-analysis
skills for finding every instance of a pattern once you find the first.
