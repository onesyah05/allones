import { useEffect, useState } from "react";
import { api } from "../lib/api";
import { PageHeader, Spinner } from "../components/ui";
import { Icon } from "../components/Icon";

interface Role {
  name: string;
  title: string;
  summary: string;
  category: string;
  toolset: string;
  danger: boolean;
}
interface TeamStat {
  role: string;
  runs: number;
  ok: number;
  failed: number;
  score: number;
}

export function RolesPage() {
  const [roles, setRoles] = useState<Role[] | null>(null);
  const [team, setTeam] = useState<Record<string, TeamStat>>({});

  useEffect(() => {
    api.get<{ roles: Role[] }>("/roles").then((r) => setRoles(r.roles || []));
    api.get<{ team: TeamStat[] }>("/team").then((r) => {
      const m: Record<string, TeamStat> = {};
      (r.team || []).forEach((t) => (m[t.role] = t));
      setTeam(m);
    });
  }, []);

  if (!roles) return <div className="h-full flex flex-col"><PageHeader title="Roles" icon="users" /><Spinner /></div>;
  const cats = Array.from(new Set(roles.map((r) => r.category)));

  return (
    <div className="h-full flex flex-col">
      <PageHeader title="Roles" subtitle={`${roles.length} specialists — delegate work or switch a chat to one`} icon="users" />
      <div className="flex-1 overflow-y-auto p-6 space-y-6">
        {cats.map((c) => (
          <div key={c}>
            <h3 className="text-xs text-[var(--color-faint)] mb-2 capitalize">{c}</h3>
            <div className="grid sm:grid-cols-2 xl:grid-cols-3 2xl:grid-cols-4 gap-2">
              {roles.filter((r) => r.category === c).map((r) => {
                const t = team[r.name];
                return (
                  <div key={r.name} className="card p-3">
                    <div className="flex items-center gap-2 mb-1">
                      <span className="font-medium text-sm">{r.title}</span>
                      {r.danger && <span className="chip" style={{ color: "var(--color-warn)" }} title="Authorized security testing"><Icon name="shield" className="w-3 h-3" /></span>}
                      <div className="flex-1" />
                      <span className="chip font-mono">{r.toolset}</span>
                    </div>
                    <p className="text-xs text-[var(--color-muted)]">{r.summary}</p>
                    {t && t.runs > 0 && (
                      <div className="mt-2 flex items-center gap-2">
                        <div className="flex-1 h-1.5 rounded-full bg-[var(--color-surface-2)] overflow-hidden">
                          <div className="h-full bg-[var(--color-brand)]" style={{ width: t.score + "%" }} />
                        </div>
                        <span className="text-xs text-[var(--color-faint)]">{t.score} · {t.runs} runs</span>
                      </div>
                    )}
                  </div>
                );
              })}
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}
