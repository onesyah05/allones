import { useEffect, useState } from "react";
import { api, cls } from "../lib/api";
import { PageHeader, Modal } from "../components/ui";
import { Icon } from "../components/Icon";
import { FolderPicker, basename } from "../components/FolderPicker";
import { toast } from "../components/Toasts";

interface Entry {
  name: string;
  dir: boolean;
  size: number;
  mtime: number;
}

export function FilesPage() {
  const [path, setPath] = useState("");
  const [entries, setEntries] = useState<Entry[]>([]);
  const [view, setView] = useState<{ path: string; content: string } | null>(null);
  const [err, setErr] = useState("");
  const [root, setRoot] = useState(() => {
    try { return localStorage.getItem("allones_workspace") || ""; } catch { return ""; }
  });
  const [pickerOpen, setPickerOpen] = useState(false);
  const [resolvedRoot, setResolvedRoot] = useState("");

  async function load(p: string, r?: string) {
    setErr("");
    const rt = r ?? root;
    try {
      const resp = await api.get<{ entries: Entry[]; root: string }>("/files?path=" + encodeURIComponent(p) + "&root=" + encodeURIComponent(rt));
      setEntries(resp.entries || []);
      setResolvedRoot(resp.root || "");
      setPath(p);
    } catch (e: any) {
      setErr(e.message);
    }
  }
  useEffect(() => {
    load("");
  }, []); // eslint-disable-line react-hooks/exhaustive-deps

  function chooseRoot(path: string) {
    setRoot(path);
    try { localStorage.setItem("allones_workspace", path); } catch { /* ignore */ }
    setPickerOpen(false);
    setPath("");
    load("", path);
    toast("Workspace set", path, "ok");
  }

  async function open(e: Entry) {
    const np = path ? path + "/" + e.name : e.name;
    if (e.dir) {
      load(np);
    } else {
      try {
        const r = await api.get<{ content: string }>("/files/content?path=" + encodeURIComponent(np) + "&root=" + encodeURIComponent(root));
        setView({ path: np, content: r.content });
      } catch (err: any) {
        setErr(err.message);
      }
    }
  }

  const crumbs = path ? path.split("/") : [];

  return (
    <div className="h-full flex flex-col">
      <PageHeader
        title="Files"
        subtitle={resolvedRoot || "The agent's workspace"}
        icon="folder"
        actions={
          <button className="btn btn-ghost" onClick={() => setPickerOpen(true)}>
            <Icon name="folder" className="w-4 h-4" /> {root ? basename(root) : "Change folder"}
          </button>
        }
      />
      <div className="flex-1 overflow-y-auto p-6">
        <div className="flex items-center gap-1 text-sm mb-3 flex-wrap">
          <button className="text-[var(--color-accent)] hover:underline" onClick={() => load("")}>workspace</button>
          {crumbs.map((c, i) => (
            <span key={i} className="flex items-center gap-1">
              <Icon name="chevron-right" className="w-3 h-3 text-[var(--color-faint)]" />
              <button className="text-[var(--color-accent)] hover:underline" onClick={() => load(crumbs.slice(0, i + 1).join("/"))}>{c}</button>
            </span>
          ))}
        </div>
        {err && <div className="text-sm text-[var(--color-danger)] mb-3">{err}</div>}
        <div className="card divide-y divide-[var(--color-border-soft)]">
          {path && (
            <button className="w-full px-3 py-2 flex items-center gap-2.5 hover:bg-[var(--color-surface-2)] text-sm" onClick={() => load(crumbs.slice(0, -1).join("/"))}>
              <Icon name="folder" className="w-4 h-4 text-[var(--color-faint)]" /> ..
            </button>
          )}
          {entries.map((e) => (
            <button key={e.name} className="w-full px-3 py-2 flex items-center gap-2.5 hover:bg-[var(--color-surface-2)] text-sm" onClick={() => open(e)}>
              <Icon name={e.dir ? "folder" : "file"} className={cls("w-4 h-4 shrink-0", e.dir ? "text-[var(--color-accent)]" : "text-[var(--color-faint)]")} />
              <span className="flex-1 text-left truncate">{e.name}</span>
              {!e.dir && <span className="text-xs text-[var(--color-faint)]">{fmtSize(e.size)}</span>}
            </button>
          ))}
          {entries.length === 0 && !path && !err && <div className="p-6 text-center text-[var(--color-faint)] text-sm">The workspace is empty.</div>}
        </div>
      </div>

      <FolderPicker open={pickerOpen} initial={root} onClose={() => setPickerOpen(false)} onPick={chooseRoot} />
      <Modal open={!!view} onClose={() => setView(null)} title={view?.path || ""} wide>
        <pre className="font-mono text-xs whitespace-pre-wrap overflow-x-auto">{view?.content}</pre>
      </Modal>
    </div>
  );
}

function fmtSize(n: number): string {
  if (n < 1024) return n + " B";
  if (n < 1048576) return (n / 1024).toFixed(1) + " KB";
  return (n / 1048576).toFixed(1) + " MB";
}
