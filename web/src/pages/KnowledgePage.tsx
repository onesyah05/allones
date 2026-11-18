import { useEffect, useState } from "react";
import { api } from "../lib/api";
import { PageHeader, Empty, Field } from "../components/ui";
import { Icon } from "../components/Icon";
import { toast } from "../components/Toasts";

interface Collection {
  name: string;
  sources: number;
  chunks: number;
}

export function KnowledgePage() {
  const [cols, setCols] = useState<Collection[]>([]);
  const [col, setCol] = useState("default");
  const [path, setPath] = useState("");
  const [query, setQuery] = useState("");
  const [results, setResults] = useState<any[]>([]);
  const [busy, setBusy] = useState(false);

  async function load() {
    const r = await api.get<{ collections: Collection[] }>("/rag/collections");
    setCols(r.collections || []);
  }
  useEffect(() => {
    load();
  }, []);

  async function index() {
    if (!path.trim()) return;
    setBusy(true);
    try {
      const r = await api.post<{ files: number; chunks: number }>("/rag/index", { collection: col, path });
      toast("Indexed", `${r.files} files, ${r.chunks} chunks`, "ok");
      setPath("");
      load();
    } catch (e: any) {
      toast("Index failed", e.message, "error");
    } finally {
      setBusy(false);
    }
  }
  async function search() {
    if (!query.trim()) return;
    const r = await api.post<{ results: any[] }>("/rag/search", { collection: "", query, k: 8 });
    setResults(r.results || []);
  }
  async function delCol(name: string) {
    await api.del("/rag/collection?collection=" + encodeURIComponent(name));
    load();
  }

  return (
    <div className="h-full flex flex-col">
      <PageHeader title="Knowledge" subtitle="Hybrid RAG — index documents, search by meaning" icon="database" />
      <div className="flex-1 overflow-y-auto p-6 grid xl:grid-cols-2 gap-6 content-start">
        <div className="card p-4">
          <h3 className="font-medium mb-3">Index a path</h3>
          <div className="flex gap-2 items-end">
            <div className="w-40">
              <Field label="Collection">
                <input className="input" value={col} onChange={(e) => setCol(e.target.value)} />
              </Field>
            </div>
            <div className="flex-1">
              <Field label="File or directory (workspace-relative or absolute)">
                <input className="input" placeholder="docs/  or  C:\\project\\README.md" value={path} onChange={(e) => setPath(e.target.value)} onKeyDown={(e) => e.key === "Enter" && index()} />
              </Field>
            </div>
            <button className="btn btn-brand mb-3.5" onClick={index} disabled={busy}>
              {busy ? "…" : <><Icon name="plus" className="w-4 h-4" /> Index</>}
            </button>
          </div>
        </div>

        <div className="card p-4">
          <h3 className="font-medium mb-3">Search</h3>
          <div className="flex gap-2">
            <input className="input" placeholder="What are you looking for?" value={query} onChange={(e) => setQuery(e.target.value)} onKeyDown={(e) => e.key === "Enter" && search()} />
            <button className="btn btn-brand" onClick={search}><Icon name="search" className="w-4 h-4" /></button>
          </div>
          <div className="mt-3 space-y-2">
            {results.map((r, i) => (
              <div key={i} className="card p-3 bg-[var(--color-bg)]">
                <div className="text-xs text-[var(--color-faint)] mb-1 font-mono">{r.source}#{r.index} · {r.score?.toFixed(3)}</div>
                <div className="text-xs whitespace-pre-wrap line-clamp-6">{r.content}</div>
              </div>
            ))}
          </div>
        </div>

        <div>
          <h3 className="font-medium mb-2">Collections</h3>
          {cols.length === 0 ? (
            <Empty icon="database" title="No collections yet" hint="Index a directory above to build your first one." />
          ) : (
            <div className="space-y-1.5">
              {cols.map((c) => (
                <div key={c.name} className="card p-3 flex items-center gap-3 group">
                  <Icon name="database" className="w-4 h-4 text-[var(--color-accent)]" />
                  <span className="font-medium text-sm flex-1">{c.name}</span>
                  <span className="text-xs text-[var(--color-faint)]">{c.sources} sources · {c.chunks} chunks</span>
                  <button className="opacity-0 group-hover:opacity-100 text-[var(--color-faint)] hover:text-[var(--color-danger)]" onClick={() => delCol(c.name)}>
                    <Icon name="trash" className="w-3.5 h-3.5" />
                  </button>
                </div>
              ))}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
