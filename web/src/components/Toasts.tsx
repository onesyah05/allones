import { useEffect, useState } from "react";
import { Icon } from "./Icon";
import { cls } from "../lib/api";

export interface Toast {
  id: number;
  title: string;
  body?: string;
  kind: "info" | "ok" | "error";
}

let push: (t: Omit<Toast, "id">) => void = () => {};

export function toast(title: string, body?: string, kind: Toast["kind"] = "info") {
  push({ title, body, kind });
}

export function Toasts() {
  const [items, setItems] = useState<Toast[]>([]);
  useEffect(() => {
    push = (t) => {
      const id = Date.now() + Math.random();
      setItems((xs) => [...xs, { ...t, id }]);
      setTimeout(() => setItems((xs) => xs.filter((x) => x.id !== id)), 5000);
    };
    // Subscribe to server notifications.
    const es = new EventSource("/api/notifications");
    es.onmessage = (e) => {
      try {
        const n = JSON.parse(e.data);
        if (n.title) push({ title: n.title, body: n.body, kind: "info" });
      } catch {
        /* ignore */
      }
    };
    return () => es.close();
  }, []);

  return (
    <div className="fixed bottom-4 right-4 z-[100] space-y-2 w-80 max-w-[calc(100vw-2rem)]">
      {items.map((t) => (
        <div
          key={t.id}
          className="card p-3 flex items-start gap-2.5 fadein shadow-lg"
          style={{ borderLeftColor: color(t.kind), borderLeftWidth: 3 }}
        >
          <Icon name={icon(t.kind)} className="w-4 h-4 mt-0.5 shrink-0" style={{ color: color(t.kind) }} />
          <div className="min-w-0 flex-1">
            <div className="text-sm font-medium">{t.title}</div>
            {t.body && <div className="text-xs text-[var(--color-faint)] mt-0.5 break-words">{t.body}</div>}
          </div>
          <button
            className="text-[var(--color-faint)] hover:text-[var(--color-text)]"
            onClick={() => setItems((xs) => xs.filter((x) => x.id !== t.id))}
          >
            <Icon name="x" className="w-3.5 h-3.5" />
          </button>
        </div>
      ))}
    </div>
  );
}

function color(k: Toast["kind"]) {
  return k === "error" ? "var(--color-danger)" : k === "ok" ? "var(--color-ok)" : "var(--color-accent)";
}
function icon(k: Toast["kind"]) {
  return k === "error" ? ("warning" as const) : k === "ok" ? ("check" as const) : ("sparkle" as const);
}

export { cls };
