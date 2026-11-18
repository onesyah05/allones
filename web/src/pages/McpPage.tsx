import { useEffect, useState } from "react";
import { api, cls } from "../lib/api";
import { PageHeader, Empty, Modal, Field } from "../components/ui";
import { Icon } from "../components/Icon";
import { toast } from "../components/Toasts";

interface McpServer {
  name: string;
  command: string;
  args: string[];
  url: string;
  enabled: boolean;
}
interface McpStatus {
  name: string;
  connected: boolean;
  error: string;
  tools: string[];
  tool_count: number;
}

export function McpPage() {
  const [status, setStatus] = useState<McpStatus[]>([]);
  const [config, setConfig] = useState<McpServer[]>([]);
  const [edit, setEdit] = useState<(Partial<McpServer> & { argsText?: string }) | null>(null);
  const [busy, setBusy] = useState(false);

  async function load() {
    const r = await api.get<{ servers: McpStatus[]; config: McpServer[] }>("/mcp");
    setStatus(r.servers || []);
    setConfig(r.config || []);
  }
  useEffect(() => {
    load();
  }, []);

  async function saveServer() {
    if (!edit) return;
    const srv: McpServer = {
      name: edit.name || "",
      command: edit.command || "",
      args: (edit.argsText || "").split(/\s+/).filter(Boolean),
      url: edit.url || "",
      enabled: edit.enabled ?? true,
    };
    // Merge into config and save via /config.
    const cfg = await api.get<any>("/config");
    const servers = (cfg.mcp?.servers || []).filter((s: McpServer) => s.name !== srv.name);
    servers.push(srv);
    cfg.mcp = { servers };
    await api.put("/config", cfg);
    setEdit(null);
    refresh();
  }

  async function removeServer(name: string) {
    const cfg = await api.get<any>("/config");
    cfg.mcp = { servers: (cfg.mcp?.servers || []).filter((s: McpServer) => s.name !== name) };
    await api.put("/config", cfg);
    refresh();
  }

  async function refresh() {
    setBusy(true);
    try {
      await api.post("/mcp/refresh");
      await load();
      toast("MCP refreshed", "", "ok");
    } finally {
      setBusy(false);
    }
  }

  const merged = config.map((c) => ({ config: c, status: status.find((s) => s.name === c.name) }));

  return (
    <div className="h-full flex flex-col">
      <PageHeader
        title="MCP"
        subtitle="Model Context Protocol servers — their tools appear as mcp__server__tool"
        icon="plug"
        actions={
          <>
            <button className="btn btn-ghost" onClick={refresh} disabled={busy}>{busy ? "…" : "Refresh"}</button>
            <button className="btn btn-brand" onClick={() => setEdit({ enabled: true })}><Icon name="plus" className="w-4 h-4" /> Add</button>
          </>
        }
      />
      <div className="flex-1 overflow-y-auto p-6">
        <div className="card p-3 mb-4 text-xs text-[var(--color-muted)]">
          <div className="font-medium mb-1 text-[var(--color-text)]">Tip: Reverse Engineer Anything (REA)</div>
          Add a server with command <code className="font-mono">npx</code> and args <code className="font-mono">-y rea-agents@latest mcp</code> to give the agent REA's reverse-engineering tools. The <span className="font-mono">rea</span> built-in tool also works via the CLI.
        </div>
        {merged.length === 0 ? (
          <Empty icon="plug" title="No MCP servers" hint="Connect external tool servers over stdio or streamable HTTP." />
        ) : (
          <div className="grid xl:grid-cols-2 gap-2 content-start">
            {merged.map(({ config: c, status: s }) => (
              <div key={c.name} className="card p-3 group">
                <div className="flex items-center gap-2">
                  <span className={cls("w-2 h-2 rounded-full shrink-0", s?.connected ? "bg-[var(--color-ok)]" : c.enabled ? "bg-[var(--color-danger)]" : "bg-[var(--color-faint)]")} />
                  <span className="font-medium text-sm flex-1">{c.name}</span>
                  {s?.connected && <span className="chip">{s.tool_count} tools</span>}
                  <button className="btn-ghost btn !p-1.5" onClick={() => setEdit({ ...c, argsText: c.args?.join(" ") })}><Icon name="edit" className="w-3.5 h-3.5" /></button>
                  <button className="btn-ghost btn !p-1.5 text-[var(--color-danger)]" onClick={() => removeServer(c.name)}><Icon name="trash" className="w-3.5 h-3.5" /></button>
                </div>
                <div className="text-xs text-[var(--color-faint)] font-mono mt-1 truncate">{c.url || `${c.command} ${(c.args || []).join(" ")}`}</div>
                {s?.error && <div className="text-xs text-[var(--color-danger)] mt-1">{s.error}</div>}
              </div>
            ))}
          </div>
        )}
      </div>

      <Modal open={!!edit} onClose={() => setEdit(null)} title="MCP server">
        {edit && (
          <div>
            <Field label="Name"><input className="input" value={edit.name || ""} onChange={(e) => setEdit({ ...edit, name: e.target.value })} /></Field>
            <Field label="Command (stdio)" hint="e.g. npx, node, python"><input className="input font-mono" value={edit.command || ""} onChange={(e) => setEdit({ ...edit, command: e.target.value })} /></Field>
            <Field label="Arguments" hint="space-separated, e.g. -y rea-agents@latest mcp"><input className="input font-mono" value={edit.argsText || ""} onChange={(e) => setEdit({ ...edit, argsText: e.target.value })} /></Field>
            <Field label="Or HTTP URL" hint="For a streamable-HTTP MCP server (leave command blank)"><input className="input font-mono" value={edit.url || ""} onChange={(e) => setEdit({ ...edit, url: e.target.value })} /></Field>
            <label className="flex items-center gap-2 mb-4 text-sm"><input type="checkbox" checked={edit.enabled ?? true} onChange={(e) => setEdit({ ...edit, enabled: e.target.checked })} /> Enabled</label>
            <div className="flex justify-end"><button className="btn btn-brand" onClick={saveServer} disabled={!edit.name}>Save & connect</button></div>
          </div>
        )}
      </Modal>
    </div>
  );
}
