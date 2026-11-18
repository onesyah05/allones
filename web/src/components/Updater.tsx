import { useEffect, useRef, useState } from "react";
import { Link } from "react-router-dom";
import { api, cls, type VersionInfo } from "../lib/api";
import { Icon } from "./Icon";
import { Markdown } from "../lib/markdown";
import { toast } from "./Toasts";

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

// Polls the version endpoint; the server caches the GitHub lookup, so a frequent
// poll is cheap. Returns the latest status and a manual refresh.
export function useVersion(pollMs = 30 * 60 * 1000): { info: VersionInfo | null; refresh: () => void } {
  const [info, setInfo] = useState<VersionInfo | null>(null);
  const timer = useRef<number | undefined>(undefined);
  function refresh() {
    api.get<VersionInfo>("/version").then(setInfo).catch(() => { /* offline is fine */ });
  }
  useEffect(() => {
    refresh();
    timer.current = window.setInterval(refresh, pollMs);
    return () => window.clearInterval(timer.current);
  }, [pollMs]);
  return { info, refresh };
}

// A quiet pill in the sidebar footer, shown only when a newer release exists. It
// routes to System, where the actual update happens with its release notes.
export function UpdatePill({ collapsed }: { collapsed: boolean }) {
  const { info } = useVersion();
  if (!info?.update_available) return null;
  return (
    <Link
      to="/system"
      title={`Update available: v${info.latest}`}
      className={cls(
        "update-pill shrink-0 flex items-center gap-2 border-t border-[var(--color-border-soft)] text-[13px] transition-colors duration-150",
        collapsed ? "h-10 justify-center" : "h-10 px-4",
      )}
    >
      <Icon name="download" className="w-4 h-4 shrink-0" strokeWidth={1.5} />
      {!collapsed && <span className="truncate">Update to v{info.latest}</span>}
    </Link>
  );
}

// The full update card for the System page: current vs latest, notes, and the
// one-click action that installs the release and restarts the server.
export function UpdateCard() {
  const { info, refresh } = useVersion();
  const [busy, setBusy] = useState(false);
  const [phase, setPhase] = useState("");

  async function doUpdate(latest: string) {
    setBusy(true);
    setPhase("Downloading and installing…");
    try {
      await api.post("/update");
    } catch {
      // The server may drop the connection as it restarts — not a failure.
    }
    setPhase("Restarting…");
    for (let i = 0; i < 40; i++) {
      await sleep(1500);
      try {
        const h = await api.get<{ version: string }>("/health");
        if (h.version === latest) {
          setPhase("Updated. Reloading…");
          await sleep(700);
          location.reload();
          return;
        }
      } catch { /* still restarting */ }
    }
    setPhase("");
    setBusy(false);
    toast("Update is taking longer than expected", "Reload the page in a moment to pick up the new version.", "info");
    refresh();
  }

  if (!info) return null;

  if (!info.update_available) {
    return (
      <div className="card p-4 flex items-center gap-3">
        <Icon name="check" className="w-4 h-4 text-[var(--color-ok)] shrink-0" />
        <div className="flex-1 min-w-0">
          <div className="text-[13px]">You are on the latest version</div>
          <div className="text-xs text-[var(--color-faint)]">v{info.current}</div>
        </div>
        <a href={`https://github.com/onesyah05/allones/releases`} target="_blank" rel="noopener noreferrer" className="btn btn-ghost shrink-0">
          Releases
        </a>
      </div>
    );
  }

  return (
    <div className="card p-4 border-[rgb(237_237_240/0.22)]">
      <div className="flex items-start gap-3">
        <span className="update-dot shrink-0 mt-1" />
        <div className="flex-1 min-w-0">
          <div className="font-medium">Update available — v{info.latest}</div>
          <div className="text-xs text-[var(--color-faint)]">You are on v{info.current}{info.published_at ? ` · released ${new Date(info.published_at).toLocaleDateString()}` : ""}</div>
          {info.notes && (
            <details className="mt-3">
              <summary className="cursor-pointer select-none text-xs text-[var(--color-muted)] hover:text-[var(--color-text)]">Release notes</summary>
              <div className="mt-2 max-h-56 overflow-y-auto border-l border-[var(--color-border)] pl-4">
                <Markdown text={info.notes} />
              </div>
            </details>
          )}
        </div>
      </div>
      <div className="mt-4 flex items-center gap-3 justify-end">
        {busy && <span className="text-xs text-[var(--color-muted)]">{phase}</span>}
        {busy && <span className="spinner shrink-0" />}
        <a href={info.url || "https://github.com/onesyah05/allones/releases"} target="_blank" rel="noopener noreferrer" className="btn btn-ghost">
          View on GitHub
        </a>
        <button className="btn btn-brand" disabled={busy} onClick={() => doUpdate(info.latest)}>
          <Icon name="download" className="w-4 h-4" /> Update now
        </button>
      </div>
    </div>
  );
}
