import { useEffect, useState } from "react";
import { api, fmtTime } from "../lib/api";
import { PageHeader, Stat } from "../components/ui";
import { Icon } from "../components/Icon";
import { UpdateCard } from "../components/Updater";

interface Audit {
  id: number;
  ts: number;
  actor: string;
  action: string;
  target: string;
  detail: string;
  level: string;
}

export function SystemPage() {
  const [stats, setStats] = useState<Record<string, number>>({});
  const [audit, setAudit] = useState<Audit[]>([]);
  const [q, setQ] = useState("");

  async function load() {
    const [s, a] = await Promise.all([api.get<{ stats: any }>("/stats"), api.get<{ audit: Audit[] }>("/audit" + (q ? "?q=" + encodeURIComponent(q) : ""))]);
    setStats(s.stats || {});
    setAudit(a.audit || []);
  }
  useEffect(() => {
    const t = setTimeout(load, 200);
    return () => clearTimeout(t);
  }, [q]);

  const lvlColor = (l: string) => (l === "warn" ? "var(--color-warn)" : l === "error" ? "var(--color-danger)" : "var(--color-faint)");

  return (
    <div className="h-full flex flex-col">
      <PageHeader title="System" subtitle="Usage, activity & the audit log" icon="activity" />
      <div className="flex-1 overflow-y-auto p-6 space-y-5">
        <UpdateCard />
        <div className="grid grid-cols-2 sm:grid-cols-4 lg:grid-cols-6 gap-3">
          <Stat label="Sessions" value={stats.sessions ?? 0} />
          <Stat label="Messages" value={stats.messages ?? 0} />
          <Stat label="Findings" value={stats.findings ?? 0} accent={(stats.findings ?? 0) > 0} />
          <Stat label="Engagements" value={stats.engagements ?? 0} />
          <Stat label="Memories" value={stats.memories ?? 0} />
          <Stat label="RAG chunks" value={stats.chunks ?? 0} />
          <Stat label="Open tasks" value={stats.tasks ?? 0} />
          <Stat label="Cron jobs" value={stats.cron_jobs ?? 0} />
          <Stat label="Tokens in" value={fmtNum(stats.tokens_in ?? 0)} />
          <Stat label="Tokens out" value={fmtNum(stats.tokens_out ?? 0)} />
        </div>

        <div>
          <div className="flex items-center justify-between mb-2">
            <h3 className="font-medium">Audit log</h3>
            <div className="relative">
              <Icon name="search" className="w-4 h-4 absolute left-2.5 top-1/2 -translate-y-1/2 text-[var(--color-faint)]" />
              <input className="input !pl-8 !py-1.5 w-56" placeholder="Filter…" value={q} onChange={(e) => setQ(e.target.value)} />
            </div>
          </div>
          <div className="card divide-y divide-[var(--color-border-soft)] text-sm">
            {audit.map((a) => (
              <div key={a.id} className="px-3 py-2 flex items-center gap-3">
                <span className="w-1.5 h-1.5 rounded-full shrink-0" style={{ background: lvlColor(a.level) }} />
                <span className="text-[var(--color-faint)] text-xs w-20 shrink-0">{fmtTime(a.ts)}</span>
                <span className="font-mono text-xs w-40 shrink-0 truncate">{a.action}</span>
                <span className="text-xs truncate flex-1">{a.target} {a.detail && <span className="text-[var(--color-faint)]">· {a.detail}</span>}</span>
              </div>
            ))}
            {audit.length === 0 && <div className="p-4 text-center text-[var(--color-faint)] text-sm">No activity recorded.</div>}
          </div>
        </div>
      </div>
    </div>
  );
}

function fmtNum(n: number): string {
  if (n >= 1e6) return (n / 1e6).toFixed(1) + "M";
  if (n >= 1e3) return (n / 1e3).toFixed(1) + "k";
  return String(n);
}
