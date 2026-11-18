import { useEffect, useState } from "react";
import { api } from "../lib/api";
import { PageHeader, Modal, Field } from "../components/ui";
import { Icon } from "../components/Icon";

interface Task {
  id: string;
  title: string;
  description: string;
  status: string;
  priority: string;
  assignee: string;
}

const COLS = [
  { id: "todo", label: "To do" },
  { id: "doing", label: "In progress" },
  { id: "review", label: "Review" },
  { id: "done", label: "Done" },
];
const PRIO: Record<string, string> = { urgent: "var(--color-crit)", high: "var(--color-high)", normal: "var(--color-faint)", low: "var(--color-low)" };

export function BoardPage() {
  const [tasks, setTasks] = useState<Task[]>([]);
  const [edit, setEdit] = useState<Partial<Task> | null>(null);
  const [drag, setDrag] = useState<string>("");

  async function load() {
    const r = await api.get<{ tasks: Task[] }>("/board");
    setTasks(r.tasks || []);
  }
  useEffect(() => {
    load();
  }, []);

  async function save() {
    if (!edit) return;
    await api.post("/board", edit);
    setEdit(null);
    load();
  }
  async function move(id: string, status: string) {
    const t = tasks.find((x) => x.id === id);
    if (!t || t.status === status) return;
    await api.post("/board", { ...t, status });
    load();
  }
  async function del(id: string) {
    await api.del("/board/" + id);
    setEdit(null);
    load();
  }

  return (
    <div className="h-full flex flex-col">
      <PageHeader
        title="Board"
        subtitle="Shared work board — the agent and you"
        icon="kanban"
        actions={<button className="btn btn-brand" onClick={() => setEdit({ status: "todo", priority: "normal" })}><Icon name="plus" className="w-4 h-4" /> Add task</button>}
      />
      <div className="flex-1 overflow-x-auto p-4">
        <div className="flex gap-3 h-full min-w-max">
          {COLS.map((c) => (
            <div
              key={c.id}
              className="w-72 flex flex-col bg-[var(--color-bg-soft)] rounded-xl border border-[var(--color-border-soft)]"
              onDragOver={(e) => e.preventDefault()}
              onDrop={() => { if (drag) move(drag, c.id); setDrag(""); }}
            >
              <div className="px-3 py-2.5 flex items-center justify-between border-b border-[var(--color-border-soft)]">
                <span className="text-sm font-medium">{c.label}</span>
                <span className="chip">{tasks.filter((t) => t.status === c.id).length}</span>
              </div>
              <div className="flex-1 overflow-y-auto p-2 space-y-2">
                {tasks.filter((t) => t.status === c.id).map((t) => (
                  <div
                    key={t.id}
                    draggable
                    onDragStart={() => setDrag(t.id)}
                    onClick={() => setEdit(t)}
                    className="card p-2.5 cursor-pointer hover:border-[var(--color-border)]"
                  >
                    <div className="flex items-start gap-2">
                      <span className="w-1.5 h-1.5 rounded-full mt-1.5 shrink-0" style={{ background: PRIO[t.priority] }} />
                      <span className="text-sm flex-1">{t.title}</span>
                    </div>
                    {t.assignee && <div className="text-xs text-[var(--color-faint)] mt-1 ml-3.5">@{t.assignee}</div>}
                  </div>
                ))}
              </div>
            </div>
          ))}
        </div>
      </div>

      <Modal open={!!edit} onClose={() => setEdit(null)} title={edit?.id ? "Edit task" : "New task"}>
        {edit && (
          <div>
            <Field label="Title"><input className="input" value={edit.title || ""} onChange={(e) => setEdit({ ...edit, title: e.target.value })} autoFocus /></Field>
            <Field label="Description"><textarea className="input" rows={3} value={edit.description || ""} onChange={(e) => setEdit({ ...edit, description: e.target.value })} /></Field>
            <div className="grid grid-cols-3 gap-2">
              <Field label="Status"><select className="input" value={edit.status} onChange={(e) => setEdit({ ...edit, status: e.target.value })}>{COLS.map((c) => <option key={c.id} value={c.id}>{c.label}</option>)}</select></Field>
              <Field label="Priority"><select className="input" value={edit.priority} onChange={(e) => setEdit({ ...edit, priority: e.target.value })}>{["low", "normal", "high", "urgent"].map((p) => <option key={p}>{p}</option>)}</select></Field>
              <Field label="Assignee"><input className="input" value={edit.assignee || ""} onChange={(e) => setEdit({ ...edit, assignee: e.target.value })} /></Field>
            </div>
            <div className="flex justify-between">
              {edit.id ? <button className="btn btn-ghost text-[var(--color-danger)]" onClick={() => del(edit.id!)}><Icon name="trash" className="w-3.5 h-3.5" /> Delete</button> : <span />}
              <button className="btn btn-brand" onClick={save} disabled={!edit.title}>Save</button>
            </div>
          </div>
        )}
      </Modal>
    </div>
  );
}
