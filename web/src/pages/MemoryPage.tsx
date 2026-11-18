import { useEffect, useState } from "react";
import { api } from "../lib/api";
import { PageHeader, Empty } from "../components/ui";
import { Icon } from "../components/Icon";
import { toast } from "../components/Toasts";

interface Mem {
  id: string;
  key: string;
  content: string;
  tags: string;
  used_at: number;
}

export function MemoryPage() {
  const [mems, setMems] = useState<Mem[]>([]);
  const [q, setQ] = useState("");
  const [nkey, setNkey] = useState("");
  const [ncontent, setNcontent] = useState("");

  async function load() {
    const r = await api.get<{ memories: Mem[] }>("/memory" + (q ? "?q=" + encodeURIComponent(q) : ""));
    setMems(r.memories || []);
  }
  useEffect(() => {
    const t = setTimeout(load, 200);
    return () => clearTimeout(t);
  }, [q]);

  async function add() {
    if (!ncontent.trim()) return;
    await api.post("/memory", { key: nkey.trim(), content: ncontent.trim() });
    setNkey("");
    setNcontent("");
    toast("Remembered", "", "ok");
    load();
  }
  async function del(id: string) {
    await api.del("/memory/" + id);
    load();
  }

  return (
    <div className="h-full flex flex-col">
      <PageHeader
        title="Memory"
        subtitle="Durable facts the agent keeps about you and your work"
        icon="brain"
        actions={
          <div className="relative">
            <Icon name="search" className="w-4 h-4 absolute left-2.5 top-1/2 -translate-y-1/2 text-[var(--color-faint)]" />
            <input className="input !pl-8 !py-1.5 w-56" placeholder="Search…" value={q} onChange={(e) => setQ(e.target.value)} />
          </div>
        }
      />
      <div className="flex-1 overflow-y-auto p-6">
        <div className="card p-3 mb-4 flex gap-2 items-start">
          <input className="input !w-32" placeholder="key (optional)" value={nkey} onChange={(e) => setNkey(e.target.value)} />
          <input className="input flex-1" placeholder="A fact worth remembering…" value={ncontent} onChange={(e) => setNcontent(e.target.value)} onKeyDown={(e) => e.key === "Enter" && add()} />
          <button className="btn btn-brand" onClick={add}><Icon name="plus" className="w-4 h-4" /></button>
        </div>
        {mems.length === 0 ? (
          <Empty icon="brain" title="No memories yet" hint="The agent saves durable facts as you work — or add one above." />
        ) : (
          <div className="grid xl:grid-cols-2 gap-2 content-start">
            {mems.map((m) => (
              <div key={m.id} className="card p-3 flex items-start gap-2 group">
                <div className="flex-1 min-w-0">
                  {m.key && <span className="chip mr-2 font-mono">{m.key}</span>}
                  <span className="text-sm">{m.content}</span>
                </div>
                <button className="opacity-0 group-hover:opacity-100 text-[var(--color-faint)] hover:text-[var(--color-danger)]" onClick={() => del(m.id)}>
                  <Icon name="trash" className="w-3.5 h-3.5" />
                </button>
              </div>
            ))}
          </div>
        )}
      </div>
    </div>
  );
}
