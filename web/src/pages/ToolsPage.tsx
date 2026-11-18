import { useEffect, useState } from "react";
import { api } from "../lib/api";
import { PageHeader, Spinner } from "../components/ui";
import { Icon } from "../components/Icon";

interface ToolView {
  Name: string;
  Description: string;
  Category: string;
  Source: string;
  Mutates: boolean;
  Network: boolean;
}

export function ToolsPage() {
  const [tools, setTools] = useState<ToolView[] | null>(null);
  const [q, setQ] = useState("");

  useEffect(() => {
    api.get<{ tools: ToolView[] }>("/tools").then((r) => setTools(r.tools || []));
  }, []);

  if (!tools) return <div className="h-full flex flex-col"><PageHeader title="Tools" icon="wrench" /><Spinner /></div>;

  const filtered = tools.filter((t) => (t.Name + t.Description).toLowerCase().includes(q.toLowerCase()));
  const cats = Array.from(new Set(filtered.map((t) => t.Category)));

  return (
    <div className="h-full flex flex-col">
      <PageHeader
        title="Tools"
        subtitle={`${tools.length} callable tools`}
        icon="wrench"
        actions={
          <div className="relative">
            <Icon name="search" className="w-4 h-4 absolute left-2.5 top-1/2 -translate-y-1/2 text-[var(--color-faint)]" />
            <input className="input !pl-8 !py-1.5 w-56" placeholder="Search tools…" value={q} onChange={(e) => setQ(e.target.value)} />
          </div>
        }
      />
      <div className="flex-1 overflow-y-auto p-6 space-y-6">
        {cats.map((c) => (
          <div key={c}>
            <h3 className="text-xs text-[var(--color-faint)] mb-2 capitalize">{c}</h3>
            <div className="grid sm:grid-cols-2 xl:grid-cols-3 2xl:grid-cols-4 gap-2">
              {filtered.filter((t) => t.Category === c).map((t) => (
                <div key={t.Name} className="card p-3">
                  <div className="flex items-center gap-2 mb-1">
                    <span className="font-mono text-sm font-medium">{t.Name}</span>
                    {t.Mutates && <span className="chip" title="Changes state / asks approval"><Icon name="edit" className="w-3 h-3" /></span>}
                    {t.Network && <span className="chip" title="Network / scope-checked"><Icon name="globe" className="w-3 h-3" /></span>}
                    {t.Source.startsWith("mcp:") && <span className="chip" style={{ color: "var(--color-accent)" }}>MCP</span>}
                  </div>
                  <p className="text-xs text-[var(--color-muted)] line-clamp-3">{t.Description}</p>
                </div>
              ))}
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}
