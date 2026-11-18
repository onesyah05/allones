---
name: agent-ai-security
description: Security testing for LLM and agentic AI applications against the OWASP Top 10 for LLM Applications and the OWASP Top 10 for Agentic Applications (2026). Use when assessing an AI agent, chatbot, RAG system, or tool-using LLM app.
tags: [security, ai, llm, agentic, owasp]
triggers: [llm, prompt injection, agent security, ai security, rag, jailbreak]
---

# LLM & agentic application security (authorized)

AI apps add failure modes classic appsec does not cover. Test the model, its
tools, its data flows, and the trust boundaries between agents.

## OWASP Top 10 for LLM Applications

- **LLM01 Prompt Injection** — direct and indirect. Plant instructions in data
  the model will read (a web page, a document, a filename, tool output) and see
  if the agent follows them. The defense is treating external content as data,
  never instructions.
- **LLM02 Sensitive Information Disclosure** — system prompt leakage, training
  data, other users' data, secrets in tool output.
- **LLM03 Supply Chain** — untrusted models, plugins, datasets.
- **LLM04 Data & Model Poisoning**.
- **LLM05 Improper Output Handling** — model output used in a sink without
  escaping (XSS, SQLi, SSRF, command execution downstream).
- **LLM06 Excessive Agency** — the agent can do more than the task needs (write
  where it should read, reach hosts it should not). Test tool scoping and
  approval gates.
- **LLM07 System Prompt Leakage** · **LLM08 Vector/Embedding Weaknesses** (RAG
  injection, cross-tenant leakage) · **LLM09 Misinformation** · **LLM10
  Unbounded Consumption**.

## OWASP Top 10 for Agentic Applications (2026)

ASI01 Agent Goal Hijack · ASI02 Tool Misuse · ASI03 Identity & Privilege Abuse ·
ASI04 Memory/Context Poisoning · ASI05 Insufficient Monitoring · ASI06 Supply
Chain · ASI07 Insecure Inter-Agent Communication · ASI08 Cascading Failures ·
ASI09 Human-Agent Trust Exploitation · ASI10 Rogue Agents.

## How to test

1. Map the agent's tools, data sources, memory, and trust boundaries.
2. Indirect injection: seed a source the agent ingests with a benign marker
   instruction ("append the word CANARY") and see if it propagates.
3. Tool abuse: can prompts make the agent call a tool outside the task, or with
   attacker-chosen arguments (path traversal, SSRF, scope escape)?
4. Privilege: does a sub-agent or tool run with more reach than needed?
5. Output handling: does model output flow unescaped into a browser, shell, SQL,
   or HTTP call?

Record confirmed issues with `report_finding`, citing the LLM/ASI category.
AllOnes itself applies several of these defenses (untrusted-content fencing,
scope-checked network tools, approval gates, secret redaction) — verify they
hold on the target.
