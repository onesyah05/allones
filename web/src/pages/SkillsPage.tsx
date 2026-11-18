import { useEffect, useState } from "react";
import { api } from "../lib/api";
import { PageHeader, Modal, Spinner } from "../components/ui";
import { Icon } from "../components/Icon";
import { Markdown } from "../lib/markdown";

interface Skill {
  name: string;
  description: string;
  source: string;
  tags?: string[];
  files?: string[];
}

export function SkillsPage() {
  const [skills, setSkills] = useState<Skill[] | null>(null);
  const [q, setQ] = useState("");
  const [open, setOpen] = useState<{ name: string; content: string } | null>(null);
  const [total, setTotal] = useState(0);

  async function load(query: string) {
    const r = await api.get<{ skills: Skill[] }>("/skills" + (query ? "?q=" + encodeURIComponent(query) : ""));
    setSkills(r.skills || []);
    if (!query) setTotal(r.skills?.length || 0);
  }
  useEffect(() => {
    load("");
  }, []);
  useEffect(() => {
    const t = setTimeout(() => load(q), 250);
    return () => clearTimeout(t);
  }, [q]);

  async function view(name: string) {
    const r = await api.get<{ content: string }>("/skills/" + encodeURIComponent(name));
    setOpen({ name, content: r.content });
  }

  if (!skills) return <div className="h-full flex flex-col"><PageHeader title="Skills" icon="book" /><Spinner /></div>;

  return (
    <div className="h-full flex flex-col">
      <PageHeader
        title="Skills"
        subtitle={`${total} procedures the agent can follow`}
        icon="book"
        actions={
          <div className="relative">
            <Icon name="search" className="w-4 h-4 absolute left-2.5 top-1/2 -translate-y-1/2 text-[var(--color-faint)]" />
            <input className="input !pl-8 !py-1.5 w-56" placeholder="Search skills…" value={q} onChange={(e) => setQ(e.target.value)} />
          </div>
        }
      />
      <div className="flex-1 overflow-y-auto p-6">
        <div className="grid sm:grid-cols-2 lg:grid-cols-3 2xl:grid-cols-4 gap-2">
          {skills.map((s) => (
            <button key={s.name} onClick={() => view(s.name)} className="card p-3 text-left hover:border-[var(--color-border)]">
              <div className="flex items-center gap-1.5 mb-1">
                <span className="font-mono text-sm font-medium truncate">{s.name}</span>
                {s.source === "builtin" && <Icon name="check" className="w-3 h-3 text-[var(--color-ok)] shrink-0" />}
                {s.files && s.files.length > 0 && <span className="chip shrink-0">{s.files.length} files</span>}
              </div>
              <p className="text-xs text-[var(--color-muted)] line-clamp-3">{s.description}</p>
            </button>
          ))}
        </div>
        {skills.length === 0 && <div className="text-sm text-[var(--color-faint)] text-center p-8">No skills match.</div>}
      </div>

      <Modal open={!!open} onClose={() => setOpen(null)} title={open?.name || ""} wide>
        {open && <Markdown text={open.content} />}
      </Modal>
    </div>
  );
}
