import { useEffect, useState } from "react";
import { api, cls } from "../lib/api";
import { PageHeader, Field, Spinner, Modal } from "../components/ui";
import { Icon } from "../components/Icon";
import { toast } from "../components/Toasts";

export function SettingsPage() {
  const [cfg, setCfg] = useState<any>(null);
  const [tab, setTab] = useState("general");
  const [editProvider, setEditProvider] = useState<any>(null);

  async function load() {
    setCfg(await api.get("/config"));
  }
  useEffect(() => {
    load();
  }, []);

  async function save(patch: any) {
    const merged = { ...cfg, ...patch };
    setCfg(merged);
    await api.put("/config", merged);
    toast("Saved", "", "ok");
  }

  if (!cfg) return <div className="h-full flex flex-col"><PageHeader title="Settings" icon="gear" /><Spinner /></div>;

  const tabs = [
    { id: "general", label: "General" },
    { id: "agent", label: "Agent" },
    { id: "security", label: "Security" },
    { id: "tools", label: "Tools" },
  ];

  return (
    <div className="h-full flex flex-col">
      <PageHeader title="Settings" subtitle="Everything lives in ~/.allones/config.yaml" icon="gear" />
      <div className="flex-1 flex min-h-0">
        <div className="w-44 shrink-0 border-r border-[var(--color-border-soft)] p-2 space-y-0.5">
          {tabs.map((t) => (
            <button key={t.id} onClick={() => setTab(t.id)} className={cls("w-full text-left px-3 py-2 rounded-lg text-sm", tab === t.id ? "bg-[var(--color-surface-2)]" : "hover:bg-[var(--color-surface)] text-[var(--color-muted)]")}>
              {t.label}
            </button>
          ))}
        </div>
        <div className="flex-1 overflow-y-auto p-6"><div className="grid xl:grid-cols-2 gap-x-8 content-start [&>div]:contents">
          {tab === "general" && (
            <div>
              <Field label="Agent name"><input className="input" value={cfg.soul?.name || ""} onChange={(e) => setCfg({ ...cfg, soul: { ...cfg.soul, name: e.target.value } })} onBlur={() => save(cfg)} /></Field>
              <Field label="Personality (optional)"><textarea className="input" rows={2} value={cfg.soul?.personality || ""} onChange={(e) => setCfg({ ...cfg, soul: { ...cfg.soul, personality: e.target.value } })} onBlur={() => save(cfg)} /></Field>
              <Field label="Default model"><input className="input font-mono" value={cfg.model?.default || ""} onChange={(e) => setCfg({ ...cfg, model: { ...cfg.model, default: e.target.value } })} onBlur={() => save(cfg)} /></Field>
              <Field label="Aux model (titles, compaction, verify, judge)" hint="A cheaper model; defaults to the main one"><input className="input font-mono" value={cfg.model?.aux || ""} onChange={(e) => setCfg({ ...cfg, model: { ...cfg.model, aux: e.target.value } })} onBlur={() => save(cfg)} /></Field>
              <Field label="Reasoning effort"><select className="input" value={cfg.model?.reasoning || "off"} onChange={(e) => save({ ...cfg, model: { ...cfg.model, reasoning: e.target.value } })}>{["off", "low", "medium", "high"].map((x) => <option key={x}>{x}</option>)}</select></Field>
            </div>
          )}

          {tab === "providers" && (
            <div>
              <div className="flex justify-between items-center mb-3">
                <h3 className="font-medium">Providers</h3>
                <button className="btn btn-brand" onClick={() => setEditProvider({ name: "", kind: "openai-compatible", models: [] })}><Icon name="plus" className="w-4 h-4" /> Add</button>
              </div>
              <div className="space-y-2">
                {(cfg.providers || []).map((p: any, i: number) => (
                  <div key={i} className="card p-3 flex items-center gap-3">
                    <div className="flex-1">
                      <div className="font-medium text-sm">{p.name} <span className="chip ml-1">{p.kind}</span></div>
                      <div className="text-xs text-[var(--color-faint)] font-mono truncate">{p.base_url} · {(p.models || []).length} models {p.from_env ? "· from env" : ""}</div>
                    </div>
                    {!p.from_env && <button className="btn-ghost btn !p-1.5" onClick={() => setEditProvider({ ...p, _idx: i, modelsText: (p.models || []).join(", ") })}><Icon name="edit" className="w-3.5 h-3.5" /></button>}
                  </div>
                ))}
              </div>
            </div>
          )}

          {tab === "agent" && (
            <div>
              <ToggleRow label="Verify replies" hint="A cheap model checks each finished answer against the request" value={cfg.agent?.verify_replies} onChange={(v) => save({ ...cfg, agent: { ...cfg.agent, verify_replies: v } })} />
              <NumRow label="Max turns" value={cfg.agent?.max_turns} onChange={(v) => save({ ...cfg, agent: { ...cfg.agent, max_turns: v } })} />
              <NumRow label="Max tool calls / run" value={cfg.agent?.max_tool_calls} onChange={(v) => save({ ...cfg, agent: { ...cfg.agent, max_tool_calls: v } })} />
              <NumRow label="Parallel tools" value={cfg.agent?.parallel_tools} onChange={(v) => save({ ...cfg, agent: { ...cfg.agent, parallel_tools: v } })} />
              <NumRow label="Repeat guard limit" value={cfg.agent?.repeat_limit} onChange={(v) => save({ ...cfg, agent: { ...cfg.agent, repeat_limit: v } })} />
              <NumRow label="Sub-agent depth" value={cfg.agent?.subagent_depth} onChange={(v) => save({ ...cfg, agent: { ...cfg.agent, subagent_depth: v } })} />
            </div>
          )}

          {tab === "security" && (
            <div>
              <Field label="Scope enforcement" hint="How network tools behave for out-of-scope targets during an engagement">
                <select className="input" value={cfg.security?.scope_enforcement || "warn"} onChange={(e) => save({ ...cfg, security: { ...cfg.security, scope_enforcement: e.target.value } })}>
                  <option value="off">off</option><option value="warn">warn</option><option value="block">block</option>
                </select>
              </Field>
              <ToggleRow label="Redact secrets in tool output" value={cfg.security?.redact_secrets} onChange={(v) => save({ ...cfg, security: { ...cfg.security, redact_secrets: v } })} />
              <ToggleRow label="SSRF protection" hint="Block tool requests to private/loopback/metadata addresses (except in-scope)" value={cfg.security?.ssrf_protect} onChange={(v) => save({ ...cfg, security: { ...cfg.security, ssrf_protect: v } })} />
              <ToggleRow label="Fence untrusted content" hint="Wrap fetched/tool content so the model treats it as data (anti prompt-injection)" value={cfg.security?.untrusted_wrapping} onChange={(v) => save({ ...cfg, security: { ...cfg.security, untrusted_wrapping: v } })} />
              <ToggleRow label="Audit log" value={cfg.security?.audit_log} onChange={(v) => save({ ...cfg, security: { ...cfg.security, audit_log: v } })} />
              <Field label="Extra tools PATH" hint="Directory holding nuclei, httpx, nmap, semgrep… if not on the system PATH"><input className="input font-mono" value={cfg.security?.tools_path || ""} onChange={(e) => setCfg({ ...cfg, security: { ...cfg.security, tools_path: e.target.value } })} onBlur={() => save(cfg)} /></Field>
            </div>
          )}

          {tab === "tools" && (
            <div>
              <Field label="Default toolset"><select className="input" value={cfg.tools?.toolset || "default"} onChange={(e) => save({ ...cfg, tools: { ...cfg.tools, toolset: e.target.value } })}>{["minimal", "coding", "research", "browser", "security", "reverse", "ops", "default", "all"].map((x) => <option key={x}>{x}</option>)}</select></Field>
              <Field label="Approval mode" hint="prompt asks before mutations; auto runs them; deny refuses them"><select className="input" value={cfg.tools?.approval_mode || "prompt"} onChange={(e) => save({ ...cfg, tools: { ...cfg.tools, approval_mode: e.target.value } })}>{["prompt", "auto", "deny"].map((x) => <option key={x}>{x}</option>)}</select></Field>
              <Field label="Workspace" hint="Root for file & shell tools"><input className="input font-mono" value={cfg.tools?.workspace || ""} onChange={(e) => setCfg({ ...cfg, tools: { ...cfg.tools, workspace: e.target.value } })} onBlur={() => save(cfg)} /></Field>
              <Field label="Shell timeout (seconds)"><input className="input" type="number" value={cfg.tools?.shell_timeout || 120} onChange={(e) => setCfg({ ...cfg, tools: { ...cfg.tools, shell_timeout: +e.target.value } })} onBlur={() => save(cfg)} /></Field>
              <Field label="Web search provider"><select className="input" value={cfg.tools?.web_search?.provider || "duckduckgo"} onChange={(e) => save({ ...cfg, tools: { ...cfg.tools, web_search: { ...cfg.tools?.web_search, provider: e.target.value } } })}>{["duckduckgo", "brave", "tavily", "searxng"].map((x) => <option key={x}>{x}</option>)}</select></Field>
            </div>
          )}
        </div></div>
      </div>

      <Modal open={!!editProvider} onClose={() => setEditProvider(null)} title="Provider">
        {editProvider && (
          <div>
            <Field label="Name"><input className="input" value={editProvider.name} onChange={(e) => setEditProvider({ ...editProvider, name: e.target.value })} /></Field>
            <Field label="Kind"><select className="input" value={editProvider.kind} onChange={(e) => setEditProvider({ ...editProvider, kind: e.target.value })}>{["openai-compatible", "anthropic", "openai", "gemini"].map((x) => <option key={x}>{x}</option>)}</select></Field>
            <Field label="Base URL" hint="Leave blank for well-known providers"><input className="input font-mono" value={editProvider.base_url || ""} onChange={(e) => setEditProvider({ ...editProvider, base_url: e.target.value })} /></Field>
            <Field label="API key" hint="Leave blank to keep existing"><input className="input" type="password" value={editProvider.api_key || ""} onChange={(e) => setEditProvider({ ...editProvider, api_key: e.target.value })} /></Field>
            <Field label="Models (comma-separated)"><input className="input font-mono" value={editProvider.modelsText ?? (editProvider.models || []).join(", ")} onChange={(e) => setEditProvider({ ...editProvider, modelsText: e.target.value })} /></Field>
            <div className="flex justify-end gap-2">
              <button className="btn btn-brand" onClick={async () => {
                const prov = { ...editProvider, models: (editProvider.modelsText ?? (editProvider.models || []).join(",")).split(",").map((s: string) => s.trim()).filter(Boolean) };
                delete prov._idx; delete prov.modelsText;
                const providers = [...(cfg.providers || [])];
                if (editProvider._idx !== undefined) providers[editProvider._idx] = prov;
                else providers.push(prov);
                await api.put("/providers", { providers });
                setEditProvider(null);
                load();
                toast("Provider saved", "", "ok");
              }} disabled={!editProvider.name}>Save</button>
            </div>
          </div>
        )}
      </Modal>
    </div>
  );
}

function ToggleRow({ label, hint, value, onChange }: { label: string; hint?: string; value?: boolean; onChange: (v: boolean) => void }) {
  return (
    <div className="flex items-center justify-between py-2.5 border-b border-[var(--color-border-soft)]">
      <div className="pr-4">
        <div className="text-sm">{label}</div>
        {hint && <div className="text-xs text-[var(--color-faint)]">{hint}</div>}
      </div>
      <button onClick={() => onChange(!value)} className={cls("w-10 h-6 rounded-full transition-colors shrink-0 relative", value ? "bg-[var(--color-brand)]" : "bg-[var(--color-surface-2)]")}>
        <span className={cls("absolute top-0.5 w-5 h-5 rounded-full bg-white transition-all", value ? "left-[18px]" : "left-0.5")} />
      </button>
    </div>
  );
}

function NumRow({ label, value, onChange }: { label: string; value?: number; onChange: (v: number) => void }) {
  return (
    <div className="flex items-center justify-between py-2.5 border-b border-[var(--color-border-soft)]">
      <div className="text-sm">{label}</div>
      <input className="input !w-24" type="number" defaultValue={value} onBlur={(e) => onChange(+e.target.value)} />
    </div>
  );
}
