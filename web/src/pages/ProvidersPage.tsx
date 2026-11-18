import { useEffect, useMemo, useState, useCallback } from "react";
import { api, cls } from "../lib/api";
import { PageHeader, Field, Modal, Spinner } from "../components/ui";
import { Icon } from "../components/Icon";
import { toast } from "../components/Toasts";

interface Provider {
  id: string;
  label: string;
  kind: string;
  key_env: string;
  base_url: string;
  needs_key: boolean;
  models: string[];
  group: string;
  key_url: string;
  note: string;
  connected: boolean;
  disabled: boolean;
  has_key: boolean;
  from_env: boolean;
  custom: boolean;
  enabled_models: string[];
}

const SUBSCRIPTION_IDS = new Set(["codex", "claude-code", "antigravity"]);

interface LoginStatus {
  id: string;
  kind: string;
  phase: "pending" | "success" | "error" | "cancelled";
  url?: string;
  user_code?: string;
  message?: string;
}

const GROUPS: { id: string; label: string }[] = [
  { id: "subscription", label: "Subscriptions" },
  { id: "frontier", label: "Frontier labs" },
  { id: "gateway", label: "Gateways" },
  { id: "fast", label: "Fast inference" },
  { id: "open", label: "Open model labs" },
  { id: "local", label: "Local" },
  { id: "custom", label: "Custom" },
];

export function ProvidersPage() {
  const [list, setList] = useState<Provider[] | null>(null);
  const [defaults, setDefaults] = useState({ default: "", aux: "" });
  const [active, setActive] = useState("");
  const [custom, setCustom] = useState<{ id: string; label: string; kind: string; base_url: string; api_key: string } | null>(null);

  const apply = useCallback((r: { providers: Provider[]; default: string; aux: string }) => {
    setList(r.providers || []);
    setDefaults({ default: r.default || "", aux: r.aux || "" });
  }, []);

  const load = useCallback(async () => apply(await api.get("/providers/catalog")), [apply]);
  useEffect(() => {
    load();
  }, [load]);

  useEffect(() => {
    if (list && !active) setActive((list.find((p) => p.connected) || list[0])?.id || "");
  }, [list, active]);

  if (!list) return <div className="h-full flex flex-col"><PageHeader title="Providers" icon="sparkle" /><Spinner /></div>;

  const current = list.find((p) => p.id === active);
  const connected = list.filter((p) => p.connected).length;

  async function createCustom() {
    if (!custom) return;
    try {
      apply(await api.put("/providers/" + encodeURIComponent(custom.id.trim()), { label: custom.label, kind: custom.kind, base_url: custom.base_url, api_key: custom.api_key, models: [] }));
      setActive(custom.id.trim());
      setCustom(null);
    } catch (e: any) {
      toast("Could not add provider", e.message, "error");
    }
  }

  return (
    <div className="h-full flex flex-col">
      <PageHeader
        title="Providers"
        subtitle={`${connected} connected of ${list.length}`}
        icon="sparkle"
        actions={
          <button className="btn btn-ghost" onClick={() => setCustom({ id: "", label: "", kind: "openai-compatible", base_url: "", api_key: "" })}>
            <Icon name="plus" className="w-4 h-4" /> Custom provider
          </button>
        }
      />
      <div className="flex-1 flex min-h-0">
        <div className="w-72 shrink-0 border-r border-[var(--color-border-soft)] overflow-y-auto px-3 py-4">
          {GROUPS.map((g, gi) => {
            const items = list.filter((p) => (SUBSCRIPTION_IDS.has(p.id) ? "subscription" : p.group) === g.id);
            if (items.length === 0) return null;
            return (
              <div key={g.id} className={gi === 0 ? "" : "mt-6"}>
                <div className="px-2 mb-2 text-xs leading-4 text-[var(--color-faint)]">{g.label}</div>
                <div className="space-y-0.5">
                  {items.map((p) => (
                    <button
                      key={p.id}
                      onClick={() => setActive(p.id)}
                      className={cls(
                        "w-full h-9 px-2 rounded-lg flex items-center gap-2.5 text-[13px] text-left transition-colors duration-150",
                        active === p.id
                          ? "bg-[var(--color-surface-2)] text-[var(--color-text)] shadow-[inset_0_1px_0_rgb(255_255_255/0.08)]"
                          : "text-[var(--color-muted)] hover:bg-[rgb(237_237_240/0.05)] hover:text-[var(--color-text)]",
                      )}
                    >
                      <span
                        className="w-1.5 h-1.5 rounded-full shrink-0"
                        style={{ background: p.connected ? (p.disabled ? "var(--color-faint)" : "var(--color-ok)") : "transparent", boxShadow: p.connected ? "none" : "inset 0 0 0 1px var(--color-border)" }}
                      />
                      <span className="truncate flex-1">{p.label}</span>
                      {p.connected && <span className="text-xs font-mono text-[var(--color-faint)]">{p.enabled_models.length}</span>}
                    </button>
                  ))}
                </div>
              </div>
            );
          })}
        </div>

        <div className="flex-1 min-w-0 overflow-y-auto">
          {current && <Detail key={current.id} p={current} defaults={defaults} onChange={apply} />}
        </div>
      </div>

      <Modal open={!!custom} onClose={() => setCustom(null)} title="Custom provider">
        {custom && (
          <div>
            <Field label="Id" hint="One word, used as the model prefix, like myproxy/model-name">
              <input className="input font-mono" value={custom.id} onChange={(e) => setCustom({ ...custom, id: e.target.value.replace(/[^a-zA-Z0-9_-]/g, "") })} autoFocus />
            </Field>
            <Field label="Name">
              <input className="input" value={custom.label} onChange={(e) => setCustom({ ...custom, label: e.target.value })} />
            </Field>
            <Field label="Protocol">
              <select className="input" value={custom.kind} onChange={(e) => setCustom({ ...custom, kind: e.target.value })}>
                <option value="openai-compatible">OpenAI compatible (chat/completions)</option>
                <option value="anthropic">Anthropic (messages)</option>
                <option value="gemini">Gemini (generateContent)</option>
              </select>
            </Field>
            <Field label="Base URL">
              <input className="input font-mono" placeholder="https://host/v1" value={custom.base_url} onChange={(e) => setCustom({ ...custom, base_url: e.target.value })} />
            </Field>
            <Field label="API key">
              <input className="input" type="password" value={custom.api_key} onChange={(e) => setCustom({ ...custom, api_key: e.target.value })} />
            </Field>
            <div className="flex justify-end">
              <button className="btn btn-brand" onClick={createCustom} disabled={!custom.id || !custom.base_url}>
                Add provider
              </button>
            </div>
          </div>
        )}
      </Modal>
    </div>
  );
}

function SubscriptionLogin({ p, onChange }: { p: Provider; onChange: (r: any) => void }) {
  const [attempt, setAttempt] = useState(0);
  const [status, setStatus] = useState<LoginStatus | null>(null);
  const [error, setError] = useState("");

  useEffect(() => {
    if (!attempt) return;
    let disposed = false;
    let pendingID = "";
    let timer: ReturnType<typeof setTimeout> | undefined;

    const cancel = (id: string) => {
      void api.del(`/provider-logins/${encodeURIComponent(id)}`).catch((e: Error) => {
        toast("Could not cancel account login", e.message, "error");
      });
    };
    const receive = async (next: LoginStatus) => {
      pendingID = next.phase === "pending" ? next.id : "";
      // A start request may finish after navigating away or cancelling.
      if (disposed) {
        if (pendingID) cancel(pendingID);
        return;
      }
      setStatus(next);
      if (next.phase === "success") {
        const catalog = await api.get("/providers/catalog");
        if (!disposed) onChange(catalog);
      } else if (next.phase === "pending") {
        timer = setTimeout(() => { void poll(next.id); }, 2000);
      }
    };
    const fail = (e: Error) => {
      if (!disposed) setError(e.message);
    };
    const poll = async (id: string) => {
      try {
        await receive(await api.get<LoginStatus>(`/provider-logins/${encodeURIComponent(id)}`));
      } catch (e) {
        fail(e as Error);
      }
    };
    setStatus(null);
    setError("");
    void api.post<LoginStatus>(`/providers/${encodeURIComponent(p.id)}/login`).then(receive).catch(fail);
    return () => {
      disposed = true;
      clearTimeout(timer);
      if (pendingID) cancel(pendingID);
    };
  }, [attempt, p.id, onChange]);

  let authURL: string | undefined;
  if (status?.url) {
    try {
      const url = new URL(status.url);
      if ((url.protocol === "https:" || url.protocol === "http:") && !url.username && !url.password) authURL = url.href;
    } catch {
      // Authentication URLs are backend data, not trusted navigation targets.
    }
  }
  const pending = attempt !== 0 && !error && (!status || status.phase === "pending");

  return (
    <div className="space-y-4">
      <p className="text-sm text-[var(--color-muted)]">Use your subscription account to log in. No API key is required.</p>
      <div role="status" aria-live="polite" className="text-sm space-y-2">
        <p>{status ? `Login ${status.phase}` : pending ? "Starting account login…" : p.connected ? "Account connected" : "Account not connected"}</p>
        {status?.message && <p className="whitespace-pre-wrap break-words">{status.message}</p>}
        {status?.phase === "pending" && authURL && <a href={authURL} target="_blank" rel="noopener noreferrer" className="underline underline-offset-2">Open account authorization</a>}
        {status?.phase === "pending" && status.url && !authURL && <p className="text-[var(--color-danger)]">The authorization URL is invalid or uses an unsupported scheme.</p>}
        {status?.phase === "pending" && status.user_code && <p>Authorization code: <code className="font-mono select-all">{status.user_code}</code></p>}
      </div>
      {error && <p role="alert" className="text-sm text-[var(--color-danger)]">{error}</p>}
      <div className="flex items-center gap-2">
        <button className="btn btn-brand" disabled={pending} onClick={() => setAttempt((n) => n + 1)}>{p.connected ? "Log in again" : "Log in with account"}</button>
        {pending && <button className="btn btn-ghost" onClick={() => {
          setAttempt(0);
          setStatus({ id: status?.id || "", kind: status?.kind || "", phase: "cancelled", message: "Cancellation requested." });
          setError("");
        }}>Cancel login</button>}
      </div>
    </div>
  );
}

function Detail({ p, defaults, onChange }: { p: Provider; defaults: { default: string; aux: string }; onChange: (r: any) => void }) {
  const [key, setKey] = useState("");
  const [baseURL, setBaseURL] = useState(p.base_url);
  const [live, setLive] = useState<string[] | null>(null);
  const [filter, setFilter] = useState("");
  const [manual, setManual] = useState("");
  const [busy, setBusy] = useState("");
  const [test, setTest] = useState<{ ok: boolean; text: string } | null>(null);

  const enabled = useMemo(() => new Set(p.enabled_models), [p.enabled_models]);
  const all = useMemo(() => {
    const s = new Set<string>([...p.enabled_models, ...p.models, ...(live || [])]);
    const q = filter.toLowerCase();
    return [...s].filter((m) => m.toLowerCase().includes(q)).sort((a, b) => Number(enabled.has(b)) - Number(enabled.has(a)) || a.localeCompare(b));
  }, [p, live, filter, enabled]);

  async function save(patch: any, okMsg?: string) {
    try {
      onChange(await api.put("/providers/" + encodeURIComponent(p.id), patch));
      if (okMsg) toast(okMsg, "", "ok");
      return true;
    } catch (e: any) {
      toast("Could not save", e.message, "error");
      return false;
    }
  }

  async function connect() {
    setBusy("connect");
    const ok = await save({ api_key: key, base_url: baseURL }, p.connected ? "Saved" : `${p.label} connected`);
    if (ok) {
      setKey("");
      fetchLive(true);
    }
    setBusy("");
  }

  async function fetchLive(quiet?: boolean) {
    setBusy("models");
    try {
      const r = await api.get<{ models: string[] }>(`/providers/${encodeURIComponent(p.id)}/models`);
      setLive(r.models || []);
      if (!quiet) toast(`${r.models.length} models available`, "", "ok");
    } catch (e: any) {
      if (!quiet) toast("Could not list models", e.message, "error");
    } finally {
      setBusy("");
    }
  }

  async function toggle(m: string) {
    const next = enabled.has(m) ? p.enabled_models.filter((x) => x !== m) : [...p.enabled_models, m];
    await save({ models: next });
  }

  async function addManual() {
    const m = manual.trim();
    if (!m) return;
    setManual("");
    if (!enabled.has(m)) await save({ models: [...p.enabled_models, m] });
  }

  async function runTest(model?: string) {
    setBusy("test");
    setTest(null);
    try {
      const r = await api.post<{ ok: boolean; reply?: string; error?: string; ms?: number; model: string }>(`/providers/${encodeURIComponent(p.id)}/test`, { model: model || "" });
      setTest(r.ok ? { ok: true, text: `${r.model} answered in ${r.ms} ms` } : { ok: false, text: r.error || "failed" });
    } catch (e: any) {
      setTest({ ok: false, text: e.message });
    } finally {
      setBusy("");
    }
  }

  async function setDefault(kind: "default" | "aux", m: string) {
    const id = p.id + "/" + m;
    await api.put("/models/defaults", { [kind]: id });
    onChange(await api.get("/providers/catalog"));
    toast(kind === "default" ? "Default model set" : "Aux model set", id, "ok");
  }

  async function remove() {
    onChange(await api.del("/providers/" + encodeURIComponent(p.id)));
    toast(`${p.label} disconnected`, "", "ok");
  }

  const canConnect = !p.needs_key || key.trim() !== "" || p.has_key;

  return (
    <div className="p-6 space-y-6">
      <div className="flex items-start gap-4">
        <div className="flex-1 min-w-0">
          <h2 className="text-[20px] leading-7 font-medium">{p.label}</h2>
          <p className="text-[var(--color-muted)] mt-1">
            {p.note || (p.kind === "openai-compatible" ? "OpenAI compatible endpoint" : p.kind)}
          </p>
        </div>
        {p.connected && (
          <div className="flex items-center gap-2 shrink-0">
            <button className="btn btn-ghost" onClick={() => save({ disabled: !p.disabled }, p.disabled ? "Enabled" : "Paused")}>
              {p.disabled ? "Enable" : "Pause"}
            </button>
            <button className="btn btn-ghost" onClick={remove}>
              <Icon name="trash" className="w-3.5 h-3.5" /> Disconnect
            </button>
          </div>
        )}
      </div>

      <div className="grid xl:grid-cols-[minmax(0,5fr)_minmax(0,7fr)] gap-6 items-start">
        <div className="card p-5">
          <div className="font-medium mb-4">Connection</div>
          {SUBSCRIPTION_IDS.has(p.id) ? <SubscriptionLogin p={p} onChange={onChange} /> : <>
          {p.needs_key && (
            <Field
              label="API key"
              hint={
                p.has_key
                  ? p.from_env || !p.connected
                    ? `A key was found in ${p.key_env}. Leave blank to use it`
                    : "A key is stored. Leave blank to keep it"
                  : p.key_env
                    ? `Or set ${p.key_env} in the environment`
                    : undefined
              }
            >
              <input className="input font-mono" type="password" placeholder={p.has_key ? "••••••••••••" : "Paste the key"} value={key} onChange={(e) => setKey(e.target.value)} onKeyDown={(e) => e.key === "Enter" && canConnect && connect()} />
            </Field>
          )}
          <Field label="Base URL">
            <input className="input font-mono" value={baseURL} onChange={(e) => setBaseURL(e.target.value)} />
          </Field>
          <div className="flex items-center gap-2 text-xs text-[var(--color-faint)] mb-4">
            <span className="chip">{p.kind}</span>
            {p.key_url && (
              <a href={p.key_url} target="_blank" rel="noopener noreferrer" className="underline underline-offset-2 hover:text-[var(--color-text)]">
                Get a key
              </a>
            )}
          </div>
          <div className="flex items-center gap-2">
            <button className="btn btn-brand" onClick={connect} disabled={!canConnect || busy === "connect"}>
              {busy === "connect" ? "Saving" : p.connected ? "Save" : "Connect"}
            </button>
          </div>
          </>}
          <div className="mt-3">
            {p.connected && (
              <button className="btn btn-ghost" onClick={() => runTest()} disabled={busy === "test"}>
                {busy === "test" ? "Testing" : "Test connection"}
              </button>
            )}
          </div>
          {test && (
            <div className="mt-4 flex items-start gap-2 text-xs leading-5">
              <Icon name={test.ok ? "check" : "warning"} className="w-3.5 h-3.5 mt-0.5 shrink-0" style={{ color: test.ok ? "var(--color-ok)" : "var(--color-danger)" }} />
              <span className={cls("break-all", test.ok ? "text-[var(--color-muted)]" : "text-[var(--color-danger)]")}>{test.text}</span>
            </div>
          )}
        </div>

        <div className={cls("card", !p.connected && "opacity-60")}>
          <div className="p-5 pb-4 flex items-center gap-3">
            <div className="flex-1">
              <div className="font-medium">Models</div>
              <div className="text-xs text-[var(--color-faint)] mt-1">
                {p.connected ? `${p.enabled_models.length} enabled. Enabled models appear in the chat model picker` : "Connect first to choose models"}
              </div>
            </div>
            <button className="btn btn-ghost" onClick={() => fetchLive()} disabled={!p.connected || busy === "models"}>
              {busy === "models" ? "Fetching" : "Fetch live list"}
            </button>
          </div>
          <div className="px-5 pb-4 flex gap-2">
            <div className="relative flex-1">
              <Icon name="search" className="w-4 h-4 absolute left-3 top-2 text-[var(--color-faint)]" />
              <input className="input !pl-9" placeholder="Filter" value={filter} onChange={(e) => setFilter(e.target.value)} />
            </div>
            <input className="input font-mono flex-1" placeholder="Add a model id by hand" value={manual} disabled={!p.connected} onChange={(e) => setManual(e.target.value)} onKeyDown={(e) => e.key === "Enter" && addManual()} />
            <button className="btn btn-ghost" onClick={addManual} disabled={!p.connected || !manual.trim()}>
              Add
            </button>
          </div>
          <div className="border-t border-[var(--color-border-soft)] max-h-[520px] overflow-y-auto divide-y divide-[var(--color-border-soft)]">
            {all.map((m) => {
              const on = enabled.has(m);
              const id = p.id + "/" + m;
              return (
                <div key={m} className="group h-10 px-5 flex items-center gap-3">
                  <input type="checkbox" checked={on} disabled={!p.connected} onChange={() => toggle(m)} className="accent-[var(--color-text)] w-4 h-4 shrink-0" />
                  <span className={cls("font-mono text-xs truncate flex-1", on ? "text-[var(--color-text)]" : "text-[var(--color-muted)]")}>{m}</span>
                  {defaults.default === id && <span className="chip">default</span>}
                  {defaults.aux === id && <span className="chip">aux</span>}
                  {on && (
                    <div className="hidden group-hover:flex items-center gap-1 shrink-0">
                      {defaults.default !== id && (
                        <button className="btn btn-ghost !h-6 !px-2 !text-xs" onClick={() => setDefault("default", m)}>
                          Make default
                        </button>
                      )}
                      {defaults.aux !== id && (
                        <button className="btn btn-ghost !h-6 !px-2 !text-xs" onClick={() => setDefault("aux", m)} title="Cheaper model for titles, compaction and checks">
                          Use as aux
                        </button>
                      )}
                      <button className="btn btn-ghost !h-6 !px-2 !text-xs" onClick={() => runTest(m)}>
                        Test
                      </button>
                    </div>
                  )}
                </div>
              );
            })}
            {all.length === 0 && (
              <div className="px-5 py-8 text-center text-[var(--color-faint)]">{filter ? "No model matches the filter" : "No models yet. Fetch the live list or add an id by hand"}</div>
            )}
          </div>
        </div>
      </div>
    </div>
  );
}
