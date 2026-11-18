---
name: recon
description: Reconnaissance and attack-surface mapping for an authorized target — subdomains, DNS, live hosts, ports, services and technologies. Use at the start of an engagement to enumerate what exists before testing it.
tags: [security, recon, osint, enumeration]
triggers: [recon, subdomain, enumerate, attack surface, footprint, discovery]
---

# Reconnaissance (authorized)

Map before you test. Confirm scope (`engagement`) and record every fact with
`add_intel` so the methodology status reflects real coverage.

## Passive first

- Subdomains: `sec_tool subfinder -d <domain> -silent` (needs API keys for full
  coverage; works without). Also certificate transparency via `web_fetch` on
  `https://crt.sh/?q=%25.<domain>&output=json`.
- DNS: `dns_lookup` for A/AAAA/CNAME/MX/NS/TXT; TXT often reveals SPF includes
  (cloud providers, SaaS) and verification records.
- Public exposure: search engines, code search, `web_search` for the org.

## Active (in scope only)

- Live hosts: `http_probe` on the resolved hosts, or `sec_tool httpx -json` over
  a list; record status, title, server, tech.
- Ports: `port_scan host ports=top` for a quick pass; `sec_tool nmap -sV -oX`
  for depth/service versions/UDP. Record `port` and `service` intel.
- Content: `sec_tool katana` to crawl; `sec_tool ffuf`/`gobuster` for content
  discovery of hidden paths (use a sensible wordlist; stay within rate limits).
- Fingerprint: `http_probe` detects frameworks; `sec_tool whatweb`/`nuclei
  -t http/technologies` for depth.

## Hygiene

Respect rate limits and scope exclusions. Everything recorded with `add_intel`
feeds the enumeration phase; `engagement` shows what still needs evidence before
vulnerability testing is meaningful.
