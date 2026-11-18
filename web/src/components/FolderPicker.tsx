import { useEffect, useState, useCallback } from "react";
import { api, cls } from "../lib/api";
import { Icon } from "./Icon";
import { Modal } from "./ui";
import { toast } from "./Toasts";

interface Entry {
  name: string;
  path: string;
}
interface Root {
  label: string;
  path: string;
}

// Pick a directory on this machine to use as the agent's workspace.
export function FolderPicker({ open, initial, onClose, onPick }: { open: boolean; initial?: string; onClose: () => void; onPick: (path: string) => void }) {
  const [roots, setRoots] = useState<Root[]>([]);
  const [path, setPath] = useState("");
  const [parent, setParent] = useState("");
  const [entries, setEntries] = useState<Entry[]>([]);
  const [err, setErr] = useState("");
  const [creating, setCreating] = useState(false);
  const [newName, setNewName] = useState("");

  const browse = useCallback(async (p: string) => {
    setErr("");
    try {
      const r = await api.get<{ path: string; parent: string; entries: Entry[] }>("/fs/list?path=" + encodeURIComponent(p));
      setPath(r.path);
      setParent(r.parent || "");
      setEntries(r.entries || []);
    } catch (e: any) {
      setErr(e.message);
    }
  }, []);

  useEffect(() => {
    if (!open) return;
    api.get<{ roots: Root[] }>("/fs/roots").then((r) => setRoots(r.roots || []));
    browse(initial || "");
  }, [open, initial, browse]);

  async function createFolder() {
    const name = newName.trim();
    if (!name) return;
    try {
      const r = await api.post<{ path: string }>("/fs/mkdir", { parent: path, name });
      setNewName("");
      setCreating(false);
      browse(r.path);
      toast("Folder created", "", "ok");
    } catch (e: any) {
      toast("Could not create folder", e.message, "error");
    }
  }

  const segments = path ? path.split(/[\\/]/).filter(Boolean) : [];
  const sep = path.includes("\\") ? "\\" : "/";

  return (
    <Modal open={open} onClose={onClose} title="Choose a workspace folder" wide>
      <div className="flex flex-wrap gap-1.5 mb-4">
        {roots.map((r) => (
          <button key={r.path} className="chip hover:text-[var(--color-text)] transition-colors duration-150" onClick={() => browse(r.path)}>
            {r.label}
          </button>
        ))}
      </div>

      <div className="flex items-center gap-2 mb-2 text-xs font-mono text-[var(--color-muted)] overflow-x-auto whitespace-nowrap">
        {segments.length === 0 ? (
          <span>{path || "/"}</span>
        ) : (
          segments.map((seg, i) => {
            const upto = (sep === "\\" ? "" : "/") + segments.slice(0, i + 1).join(sep) + (i === 0 && sep === "\\" ? "\\" : "");
            return (
              <span key={i} className="flex items-center gap-1 shrink-0">
                {i > 0 && <Icon name="chevron-right" className="w-3 h-3 text-[var(--color-faint)]" />}
                <button className="hover:text-[var(--color-text)]" onClick={() => browse(upto)}>
                  {seg}
                </button>
              </span>
            );
          })
        )}
      </div>

      {err && <div className="text-xs text-[var(--color-danger)] mb-2">{err}</div>}

      <div className="card max-h-[44vh] overflow-y-auto divide-y divide-[var(--color-border-soft)]">
        {parent && (
          <button className="w-full h-10 px-4 flex items-center gap-3 text-sm hover:bg-[rgb(237_237_240/0.04)] text-[var(--color-muted)]" onClick={() => browse(parent)}>
            <Icon name="chevron-left" className="w-4 h-4 shrink-0" /> Up a level
          </button>
        )}
        {entries.map((e) => (
          <button
            key={e.path}
            className="w-full h-10 px-4 flex items-center gap-3 text-sm hover:bg-[rgb(237_237_240/0.04)] transition-colors duration-150"
            onDoubleClick={() => browse(e.path)}
            onClick={() => browse(e.path)}
          >
            <Icon name="folder" className="w-4 h-4 shrink-0 text-[var(--color-faint)]" />
            <span className="truncate text-left flex-1">{e.name}</span>
            <Icon name="chevron-right" className="w-3.5 h-3.5 shrink-0 text-[var(--color-faint)]" />
          </button>
        ))}
        {entries.length === 0 && !parent && <div className="px-4 py-6 text-sm text-[var(--color-faint)] text-center">No subfolders here</div>}
        {entries.length === 0 && parent && <div className="px-4 py-6 text-sm text-[var(--color-faint)] text-center">No subfolders — you can select this folder</div>}
      </div>

      <div className="mt-4 flex items-center gap-2">
        {creating ? (
          <div className="flex items-center gap-2 flex-1">
            <input className="input font-mono" placeholder="New folder name" autoFocus value={newName} onChange={(e) => setNewName(e.target.value)} onKeyDown={(e) => e.key === "Enter" && createFolder()} />
            <button className="btn btn-ghost" onClick={createFolder} disabled={!newName.trim()}>
              Create
            </button>
            <button className="btn btn-ghost" onClick={() => setCreating(false)}>
              Cancel
            </button>
          </div>
        ) : (
          <button className="btn btn-ghost" onClick={() => setCreating(true)}>
            <Icon name="plus" className="w-4 h-4" /> New folder
          </button>
        )}
        <div className="flex-1" />
        <button className="btn btn-brand" onClick={() => onPick(path)} disabled={!path}>
          Use this folder
        </button>
      </div>
      <p className="mt-3 text-xs text-[var(--color-faint)]">
        The agent's file and shell tools run inside the folder you choose. Paths outside it are refused.
      </p>
    </Modal>
  );
}

export function basename(p: string): string {
  if (!p) return "";
  const parts = p.split(/[\\/]/).filter(Boolean);
  return parts[parts.length - 1] || p;
}

export { cls };
