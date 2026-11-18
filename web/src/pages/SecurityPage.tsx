import { useEffect, useState, useCallback } from "react";
import { api, cls, type Engagement, type Finding } from "../lib/api";
import { PageHeader, Empty, Modal, Field, SevBadge, Stat } from "../components/ui";
import { Icon } from "../components/Icon";
import { Markdown } from "../lib/markdown";
import { toast } from "../components/Toasts";

export function SecurityPage() {
  const [engagements, setEngagements] = useState<Engagement[]>([]);
  const [active, setActive] = useState<string>("");
  const [detail, setDetail] = useState<any>(null);
  const [editEng, setEditEng] = useState<Engagement | null>(null);
  const [editFinding, setEditFinding] = useState<Partial<Finding> | null>(null);
  const [report, setReport] = useState<string | null>(null);

  const load = useCallback(async () => {
    const r = await api.get<{ engagements: Engagement[] }>("/engagements");
    setEngagements(r.engagements || []);
    if (!active && r.engagements?.length) setActive(r.engagements[0].id);
  }, [active]);

  useEffect(() => {
    load();
  }, [load]);

  const loadDetail = useCallback(async (id: string) => {
    if (!id) return setDetail(null);
    const d = await api.get(`/engagements/${id}`);
    setDetail(d);
  }, []);
  useEffect(() => {
    loadDetail(active);
  }, [active, loadDetail]);

  async function saveEng() {
    if (!editEng) return;
    await api.post("/engagements", editEng);
    setEditEng(null);
    toast("Engagement saved", "", "ok");
    load();
  }
  async function saveFinding() {
    if (!editFinding) return;
    await api.put("/findings/" + (editFinding.id || "new"), { ...editFinding, engagement_id: active });
    setEditFinding(null);
    toast("Finding saved", "", "ok");
    loadDetail(active);
  }
  async function viewReport() {
    const r = await api.get<{ report: string }>(`/engagements/${active}/report?rebuild=1`);
    setReport(r.report);
  }

  const eng: Engagement | undefined = detail?.engagement;
  const findings: Finding[] = detail?.findings || [];
  const intel: any[] = detail?.intel || [];
  const sevCount = (s: string) => findings.filter((f) => f.severity === s).length;

  return (
    <div className="h-full flex flex-col">
      <PageHeader
        title="Security"
        subtitle="Authorized engagements, findings & reports"
        icon="shield"
        actions={
          <button className="btn btn-brand" onClick={() => setEditEng({ id: "", name: "", client: "", scope: [], exclusions: [], authorized: false, authorization_note: "", rules: "", status: "active" })}>
            <Icon name="plus" className="w-4 h-4" /> New engagement
          </button>
        }
      />
      <div className="flex-1 flex min-h-0">
        {/* engagement list */}
        <div className="w-60 shrink-0 border-r border-[var(--color-border-soft)] overflow-y-auto p-2 space-y-1">
          {engagements.map((e) => (
            <button
              key={e.id}
              onClick={() => setActive(e.id)}
              className={cls(
                "w-full text-left px-3 py-2.5 rounded-lg",
                active === e.id ? "bg-[var(--color-surface-2)]" : "hover:bg-[var(--color-surface)]",
              )}
            >
              <div className="flex items-center gap-1.5">
                <Icon name={e.authorized ? "check" : "warning"} className={cls("w-3.5 h-3.5 shrink-0", e.authorized ? "text-[var(--color-ok)]" : "text-[var(--color-warn)]")} />
                <span className="text-sm font-medium truncate">{e.name}</span>
              </div>
              <div className="text-xs text-[var(--color-faint)] truncate">{e.client || "no client"} · {e.status}</div>
            </button>
          ))}
          {engagements.length === 0 && <div className="text-xs text-[var(--color-faint)] p-3 text-center">No engagements. Create one to define an authorized scope.</div>}
        </div>

        {/* detail */}
        <div className="flex-1 min-w-0 overflow-y-auto">
          {!eng ? (
            <Empty icon="shield" title="No engagement selected" hint="An engagement sets the authorized scope. Attach it to a chat session to run a scoped assessment." />
          ) : (
            <div className="p-6 space-y-6">
              {!eng.authorized && (
                <div className="card p-3 flex items-center gap-2 border-l-[3px]" style={{ borderLeftColor: "var(--color-warn)" }}>
                  <Icon name="warning" className="w-4 h-4 text-[var(--color-warn)]" />
                  <span className="text-sm">This engagement is not marked authorized. Confirm written authorization before active testing.</span>
                </div>
              )}
              <div className="grid grid-cols-2 sm:grid-cols-5 gap-3">
                <Stat label="Critical" value={sevCount("critical")} accent={sevCount("critical") > 0} />
                <Stat label="High" value={sevCount("high")} />
                <Stat label="Medium" value={sevCount("medium")} />
                <Stat label="Low" value={sevCount("low")} />
                <Stat label="Intel" value={intel.length} />
              </div>

              <div className="card p-4">
                <div className="flex items-center justify-between mb-2">
                  <h3 className="font-medium">{eng.name}</h3>
                  <div className="flex gap-2">
                    <button className="btn btn-ghost" onClick={() => setEditEng(eng)}>
                      <Icon name="edit" className="w-3.5 h-3.5" /> Edit
                    </button>
                    <button className="btn btn-ghost" onClick={viewReport}>
                      <Icon name="file" className="w-3.5 h-3.5" /> Report
                    </button>
                  </div>
                </div>
                <div className="text-sm text-[var(--color-muted)] space-y-1">
                  <div><span className="text-[var(--color-faint)]">Scope:</span> {eng.scope.join(", ") || "—"}</div>
                  {eng.exclusions.length > 0 && <div><span className="text-[var(--color-faint)]">Exclusions:</span> {eng.exclusions.join(", ")}</div>}
                  {eng.rules && <div><span className="text-[var(--color-faint)]">Rules:</span> {eng.rules}</div>}
                </div>
                {detail?.methodology && (
                  <details className="mt-3">
                    <summary className="text-sm cursor-pointer text-[var(--color-accent)]">Methodology status</summary>
                    <pre className="text-xs font-mono mt-2 whitespace-pre-wrap text-[var(--color-muted)]">{detail.methodology}</pre>
                  </details>
                )}
              </div>

              <div>
                <div className="flex items-center justify-between mb-2">
                  <h3 className="font-medium">Findings</h3>
                  <button className="btn btn-ghost" onClick={() => setEditFinding({ severity: "medium" })}>
                    <Icon name="plus" className="w-3.5 h-3.5" /> Add finding
                  </button>
                </div>
                <div className="grid xl:grid-cols-2 gap-2">
                  {findings.map((f) => (
                    <div key={f.id} className="card p-3 hover:border-[var(--color-border)] cursor-pointer" onClick={() => setEditFinding(f)}>
                      <div className="flex items-center gap-2">
                        <SevBadge sev={f.severity} score={f.cvss_score} />
                        <span className="font-medium text-sm flex-1 truncate">{f.title}</span>
                        {f.cwe && <span className="chip">{f.cwe}</span>}
                      </div>
                      {f.target && <div className="text-xs text-[var(--color-faint)] mt-1 truncate">{f.target}</div>}
                    </div>
                  ))}
                  {findings.length === 0 && <div className="text-sm text-[var(--color-faint)] p-3">No findings recorded yet.</div>}
                </div>
              </div>

              {intel.length > 0 && (
                <div>
                  <h3 className="font-medium mb-2">Attack surface ({intel.length})</h3>
                  <div className="card divide-y divide-[var(--color-border-soft)]">
                    {intel.slice(0, 50).map((i) => (
                      <div key={i.id} className="px-3 py-1.5 flex items-center gap-3 text-sm">
                        <span className="chip w-24 justify-center shrink-0">{i.kind}</span>
                        <span className="font-mono text-xs truncate flex-1">{i.value}</span>
                      </div>
                    ))}
                  </div>
                </div>
              )}
            </div>
          )}
        </div>
      </div>

      {/* engagement editor */}
      <Modal open={!!editEng} onClose={() => setEditEng(null)} title={editEng?.id ? "Edit engagement" : "New engagement"}>
        {editEng && (
          <div>
            <Field label="Name">
              <input className="input" value={editEng.name} onChange={(e) => setEditEng({ ...editEng, name: e.target.value })} />
            </Field>
            <Field label="Client">
              <input className="input" value={editEng.client} onChange={(e) => setEditEng({ ...editEng, client: e.target.value })} />
            </Field>
            <Field label="Scope" hint="One entry per line: example.com, *.example.com, 10.0.0.0/24, https://app.example.com">
              <textarea className="input font-mono text-xs" rows={3} value={editEng.scope.join("\n")} onChange={(e) => setEditEng({ ...editEng, scope: e.target.value.split("\n").map((s) => s.trim()).filter(Boolean) })} />
            </Field>
            <Field label="Exclusions" hint="Out-of-scope hosts, one per line">
              <textarea className="input font-mono text-xs" rows={2} value={editEng.exclusions.join("\n")} onChange={(e) => setEditEng({ ...editEng, exclusions: e.target.value.split("\n").map((s) => s.trim()).filter(Boolean) })} />
            </Field>
            <Field label="Rules of engagement">
              <textarea className="input text-xs" rows={2} value={editEng.rules} onChange={(e) => setEditEng({ ...editEng, rules: e.target.value })} />
            </Field>
            <label className="flex items-center gap-2 mb-4 cursor-pointer">
              <input type="checkbox" checked={editEng.authorized} onChange={(e) => setEditEng({ ...editEng, authorized: e.target.checked })} />
              <span className="text-sm">I have written authorization to test this scope</span>
            </label>
            <div className="flex justify-end gap-2">
              <button className="btn btn-ghost" onClick={() => setEditEng(null)}>Cancel</button>
              <button className="btn btn-brand" onClick={saveEng} disabled={!editEng.name}>Save</button>
            </div>
          </div>
        )}
      </Modal>

      {/* finding editor */}
      <Modal open={!!editFinding} onClose={() => setEditFinding(null)} title={editFinding?.id ? "Edit finding" : "New finding"} wide>
        {editFinding && (
          <div>
            <Field label="Title">
              <input className="input" value={editFinding.title || ""} onChange={(e) => setEditFinding({ ...editFinding, title: e.target.value })} />
            </Field>
            <div className="grid grid-cols-2 gap-3">
              <Field label="CVSS v3.1 vector" hint="Score & severity computed automatically">
                <input className="input font-mono text-xs" placeholder="CVSS:3.1/AV:N/AC:L/..." value={editFinding.cvss_vector || ""} onChange={(e) => setEditFinding({ ...editFinding, cvss_vector: e.target.value })} />
              </Field>
              <div className="grid grid-cols-2 gap-3">
                <Field label="Severity (if no vector)">
                  <select className="input" value={editFinding.severity || "medium"} onChange={(e) => setEditFinding({ ...editFinding, severity: e.target.value })}>
                    {["critical", "high", "medium", "low", "info"].map((s) => <option key={s}>{s}</option>)}
                  </select>
                </Field>
                <Field label="CWE">
                  <input className="input" placeholder="CWE-89" value={editFinding.cwe || ""} onChange={(e) => setEditFinding({ ...editFinding, cwe: e.target.value })} />
                </Field>
              </div>
            </div>
            <div className="grid grid-cols-2 gap-3">
              <Field label="Target"><input className="input" value={editFinding.target || ""} onChange={(e) => setEditFinding({ ...editFinding, target: e.target.value })} /></Field>
              <Field label="Category"><input className="input" placeholder="A01:2025 Broken Access Control" value={editFinding.category || ""} onChange={(e) => setEditFinding({ ...editFinding, category: e.target.value })} /></Field>
            </div>
            {(["description", "reproduction", "impact", "remediation", "evidence"] as const).map((k) => (
              <Field key={k} label={k[0].toUpperCase() + k.slice(1)}>
                <textarea className="input text-xs" rows={k === "reproduction" ? 3 : 2} value={(editFinding as any)[k] || ""} onChange={(e) => setEditFinding({ ...editFinding, [k]: e.target.value })} />
              </Field>
            ))}
            <div className="flex justify-between">
              {editFinding.id ? (
                <button className="btn btn-ghost text-[var(--color-danger)]" onClick={async () => { await api.del("/findings/" + editFinding.id); setEditFinding(null); loadDetail(active); }}>
                  <Icon name="trash" className="w-3.5 h-3.5" /> Delete
                </button>
              ) : <span />}
              <div className="flex gap-2">
                <button className="btn btn-ghost" onClick={() => setEditFinding(null)}>Cancel</button>
                <button className="btn btn-brand" onClick={saveFinding} disabled={!editFinding.title}>Save</button>
              </div>
            </div>
          </div>
        )}
      </Modal>

      {/* report viewer */}
      <Modal open={report !== null} onClose={() => setReport(null)} title="Penetration test report" wide>
        <div className="flex justify-end mb-3">
          <button className="btn btn-ghost" onClick={() => { navigator.clipboard?.writeText(report || ""); toast("Copied", "", "ok"); }}>
            <Icon name="copy" className="w-3.5 h-3.5" /> Copy Markdown
          </button>
        </div>
        {report && <Markdown text={report} />}
      </Modal>
    </div>
  );
}
