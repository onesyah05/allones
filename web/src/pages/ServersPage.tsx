import { useEffect, useState } from "react";
import { api } from "../lib/api";
import { PageHeader, Empty, Modal, Field } from "../components/ui";
import { Icon } from "../components/Icon";
import { toast } from "../components/Toasts";

interface Srv {
  id: string;
  name: string;
  host: string;
  port: number;
  user: string;
  auth: string;
  host_key: string;
  notes: string;
}

export function ServersPage() {
  const [servers, setServers] = useState<Srv[]>([]);
  const [edit, setEdit] = useState<(Partial<Srv> & { secret?: string }) | null>(null);

  async function load() {
    const r = await api.get<{ servers: Srv[] }>("/servers");
    setServers(r.servers || []);
  }
  useEffect(() => {
    load();
  }, []);

  async function save() {
    if (!edit) return;
    await api.post("/servers", edit);
    setEdit(null);
    toast("Server saved", "Credentials are encrypted at rest", "ok");
    load();
  }
  async function del(id: string) {
    await api.del("/servers/" + id);
    setEdit(null);
    load();
  }

  return (
    <div className="h-full flex flex-col">
      <PageHeader
        title="Servers"
        subtitle="Saved SSH servers for vps_run / vps_transfer — host keys pinned (TOFU)"
        icon="server"
        actions={<button className="btn btn-brand" onClick={() => setEdit({ port: 22, auth: "password" })}><Icon name="plus" className="w-4 h-4" /> Add server</button>}
      />
      <div className="flex-1 overflow-y-auto p-6">
        {servers.length === 0 ? (
          <Empty icon="server" title="No servers saved" hint="Add an SSH server the agent can run commands on and transfer files to." />
        ) : (
          <div className="grid xl:grid-cols-2 gap-2 content-start">
            {servers.map((s) => (
              <div key={s.id} className="card p-3 flex items-center gap-3 cursor-pointer hover:border-[var(--color-border)]" onClick={() => setEdit({ ...s, secret: "" })}>
                <Icon name="server" className="w-4 h-4 text-[var(--color-accent)]" />
                <div className="flex-1">
                  <div className="font-medium text-sm">{s.name}</div>
                  <div className="text-xs text-[var(--color-faint)] font-mono">{s.user}@{s.host}:{s.port}</div>
                </div>
                {s.host_key ? <span className="chip" title="Host key pinned"><Icon name="lock" className="w-3 h-3" /> pinned</span> : <span className="chip" style={{ color: "var(--color-warn)" }}>new</span>}
              </div>
            ))}
          </div>
        )}
      </div>

      <Modal open={!!edit} onClose={() => setEdit(null)} title={edit?.id ? "Edit server" : "Add server"}>
        {edit && (
          <div>
            <Field label="Name"><input className="input" value={edit.name || ""} onChange={(e) => setEdit({ ...edit, name: e.target.value })} /></Field>
            <div className="grid grid-cols-3 gap-2">
              <div className="col-span-2"><Field label="Host"><input className="input" value={edit.host || ""} onChange={(e) => setEdit({ ...edit, host: e.target.value })} /></Field></div>
              <Field label="Port"><input className="input" type="number" value={edit.port || 22} onChange={(e) => setEdit({ ...edit, port: +e.target.value })} /></Field>
            </div>
            <div className="grid grid-cols-2 gap-2">
              <Field label="User"><input className="input" value={edit.user || ""} onChange={(e) => setEdit({ ...edit, user: e.target.value })} /></Field>
              <Field label="Auth"><select className="input" value={edit.auth} onChange={(e) => setEdit({ ...edit, auth: e.target.value })}><option value="password">Password</option><option value="key">Private key</option></select></Field>
            </div>
            <Field label={edit.auth === "key" ? "Private key (PEM)" : "Password"} hint={edit.id ? "Leave blank to keep the stored credential" : "Encrypted with AES-256-GCM at rest"}>
              {edit.auth === "key" ? (
                <textarea className="input font-mono text-xs" rows={4} value={edit.secret || ""} onChange={(e) => setEdit({ ...edit, secret: e.target.value })} />
              ) : (
                <input className="input" type="password" value={edit.secret || ""} onChange={(e) => setEdit({ ...edit, secret: e.target.value })} />
              )}
            </Field>
            <div className="flex justify-between">
              {edit.id ? <button className="btn btn-ghost text-[var(--color-danger)]" onClick={() => del(edit.id!)}><Icon name="trash" className="w-3.5 h-3.5" /> Delete</button> : <span />}
              <button className="btn btn-brand" onClick={save} disabled={!edit.name || !edit.host}>Save</button>
            </div>
          </div>
        )}
      </Modal>
    </div>
  );
}
