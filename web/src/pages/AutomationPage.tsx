import { useEffect, useState } from "react";
import { api, cls, fmtTime } from "../lib/api";
import { PageHeader, Empty, Modal, Field } from "../components/ui";
import { Icon } from "../components/Icon";
import { toast } from "../components/Toasts";

interface Job {
  id: string;
  name: string;
  schedule: string;
  prompt: string;
  role: string;
  enabled: boolean;
  auto_approve: boolean;
  last_run: number;
  next_run: number;
  last_status: string;
}

export function AutomationPage() {
  const [jobs, setJobs] = useState<Job[]>([]);
  const [edit, setEdit] = useState<Partial<Job> | null>(null);
  const [roles, setRoles] = useState<{ name: string; title: string }[]>([]);

  async function load() {
    const r = await api.get<{ jobs: Job[] }>("/cron");
    setJobs(r.jobs || []);
  }
  useEffect(() => {
    load();
    api.get<{ roles: any[] }>("/roles").then((r) => setRoles(r.roles || []));
  }, []);

  async function save() {
    if (!edit) return;
    try {
      await api.post("/cron", { ...edit, enabled: edit.enabled ?? true });
      setEdit(null);
      load();
    } catch (e: any) {
      toast("Save failed", e.message, "error");
    }
  }
  async function run(id: string) {
    await api.post(`/cron/${id}/run`);
    toast("Job started", "", "ok");
  }
  async function del(id: string) {
    await api.del("/cron/" + id);
    setEdit(null);
    load();
  }

  return (
    <div className="h-full flex flex-col">
      <PageHeader
        title="Automation"
        subtitle="Scheduled agent runs — cron expressions or @every / @daily"
        icon="clock"
        actions={<button className="btn btn-brand" onClick={() => setEdit({ schedule: "0 9 * * *", enabled: true })}><Icon name="plus" className="w-4 h-4" /> New job</button>}
      />
      <div className="flex-1 overflow-y-auto p-6">
        {jobs.length === 0 ? (
          <Empty icon="clock" title="No scheduled jobs" hint="Schedule the agent to run unattended — a morning brief, a recurring scan, a nightly report." />
        ) : (
          <div className="grid xl:grid-cols-2 gap-2 content-start">
            {jobs.map((j) => (
              <div key={j.id} className="card p-3">
                <div className="flex items-center gap-2">
                  <span className={cls("w-2 h-2 rounded-full shrink-0", j.enabled ? "bg-[var(--color-ok)]" : "bg-[var(--color-faint)]")} />
                  <span className="font-medium text-sm flex-1 cursor-pointer" onClick={() => setEdit(j)}>{j.name}</span>
                  <span className="chip font-mono">{j.schedule}</span>
                  {j.role && <span className="chip">{j.role}</span>}
                  <button className="btn-ghost btn !p-1.5" onClick={() => run(j.id)} title="Run now"><Icon name="play" className="w-3.5 h-3.5" /></button>
                </div>
                <div className="text-xs text-[var(--color-muted)] mt-1.5 line-clamp-2">{j.prompt}</div>
                <div className="text-xs text-[var(--color-faint)] mt-1 flex gap-3">
                  {j.next_run > 0 && <span>next {fmtTime(j.next_run).replace("ago", "")}</span>}
                  {j.last_status && <span>last: {j.last_status}</span>}
                </div>
              </div>
            ))}
          </div>
        )}
      </div>

      <Modal open={!!edit} onClose={() => setEdit(null)} title={edit?.id ? "Edit job" : "New scheduled job"}>
        {edit && (
          <div>
            <Field label="Name"><input className="input" value={edit.name || ""} onChange={(e) => setEdit({ ...edit, name: e.target.value })} /></Field>
            <Field label="Schedule" hint="5-field cron (0 9 * * 1-5), @daily, @hourly, or @every 15m">
              <input className="input font-mono" value={edit.schedule || ""} onChange={(e) => setEdit({ ...edit, schedule: e.target.value })} />
            </Field>
            <Field label="Prompt" hint="What the agent should do each run">
              <textarea className="input" rows={3} value={edit.prompt || ""} onChange={(e) => setEdit({ ...edit, prompt: e.target.value })} />
            </Field>
            <div className="grid grid-cols-2 gap-2">
              <Field label="Role">
                <select className="input" value={edit.role || ""} onChange={(e) => setEdit({ ...edit, role: e.target.value })}>
                  <option value="">Assistant</option>
                  {roles.map((r) => <option key={r.name} value={r.name}>{r.title || r.name}</option>)}
                </select>
              </Field>
              <div className="pt-6 space-y-2">
                <label className="flex items-center gap-2 text-sm"><input type="checkbox" checked={edit.enabled ?? true} onChange={(e) => setEdit({ ...edit, enabled: e.target.checked })} /> Enabled</label>
                <label className="flex items-center gap-2 text-sm"><input type="checkbox" checked={edit.auto_approve || false} onChange={(e) => setEdit({ ...edit, auto_approve: e.target.checked })} /> Auto-approve tools</label>
              </div>
            </div>
            <div className="flex justify-between mt-2">
              {edit.id ? <button className="btn btn-ghost text-[var(--color-danger)]" onClick={() => del(edit.id!)}><Icon name="trash" className="w-3.5 h-3.5" /> Delete</button> : <span />}
              <button className="btn btn-brand" onClick={save} disabled={!edit.name || !edit.prompt}>Save</button>
            </div>
          </div>
        )}
      </Modal>
    </div>
  );
}
