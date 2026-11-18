import { ReactNode, useEffect } from "react";
import { cls } from "../lib/api";
import { Icon, IconName } from "./Icon";

export function PageHeader({
  title,
  subtitle,
  icon,
  actions,
}: {
  title: string;
  subtitle?: string;
  icon?: IconName;
  actions?: ReactNode;
}) {
  return (
    <div className="h-14 shrink-0 border-b border-[var(--color-border-soft)] px-6 flex items-center justify-between glass sticky top-0 z-10">
      <div className="flex items-center gap-2.5 min-w-0">
        {icon && <Icon name={icon} className="w-4 h-4 text-[var(--color-faint)] shrink-0" />}
        <div className="min-w-0">
          <h1 className="text-[15px] font-medium leading-5 truncate">{title}</h1>
          {subtitle && <p className="text-xs text-[var(--color-faint)] truncate">{subtitle}</p>}
        </div>
      </div>
      <div className="flex items-center gap-2">{actions}</div>
    </div>
  );
}

export function Scroll({ children, className }: { children: ReactNode; className?: string }) {
  return <div className={cls("overflow-y-auto", className)}>{children}</div>;
}

export function Empty({ icon, title, hint }: { icon: IconName; title: string; hint?: string }) {
  return (
    <div className="h-full grid place-items-center text-center p-8">
      <div className="max-w-sm">
        <div className="w-12 h-12 rounded-xl bg-[var(--color-surface-2)] grid place-items-center mx-auto mb-3 text-[var(--color-faint)]">
          <Icon name={icon} className="w-6 h-6" />
        </div>
        <p className="font-medium">{title}</p>
        {hint && <p className="text-sm text-[var(--color-faint)] mt-1">{hint}</p>}
      </div>
    </div>
  );
}

export function Spinner({ label }: { label?: string }) {
  return (
    <div className="flex items-center gap-2 text-sm text-[var(--color-faint)] p-4">
      <svg className="w-4 h-4 animate-spin" viewBox="0 0 24 24" fill="none">
        <circle cx="12" cy="12" r="9" stroke="currentColor" strokeWidth="2" opacity="0.25" />
        <path d="M21 12a9 9 0 0 0-9-9" stroke="currentColor" strokeWidth="2" strokeLinecap="round" />
      </svg>
      {label || "Loading…"}
    </div>
  );
}

export function Modal({
  open,
  onClose,
  title,
  children,
  wide,
}: {
  open: boolean;
  onClose: () => void;
  title: string;
  children: ReactNode;
  wide?: boolean;
}) {
  useEffect(() => {
    if (!open) return;
    const h = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    window.addEventListener("keydown", h);
    return () => window.removeEventListener("keydown", h);
  }, [open, onClose]);
  if (!open) return null;
  return (
    <div className="fixed inset-0 z-50 grid place-items-center p-4 bg-black/50 backdrop-blur-sm" onClick={onClose}>
      <div
        className={cls("card w-full max-h-[85vh] flex flex-col fadein", wide ? "max-w-3xl" : "max-w-lg")}
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-center justify-between px-5 h-13 py-3 border-b border-[var(--color-border-soft)]">
          <h2 className="font-medium">{title}</h2>
          <button className="btn-ghost btn !p-1.5" onClick={onClose}>
            <Icon name="x" className="w-4 h-4" />
          </button>
        </div>
        <div className="overflow-y-auto p-5">{children}</div>
      </div>
    </div>
  );
}

export function Field({ label, children, hint }: { label: string; children: ReactNode; hint?: string }) {
  return (
    <label className="block mb-3.5">
      <span className="text-xs font-medium text-[var(--color-muted)] mb-1 block">{label}</span>
      {children}
      {hint && <span className="text-xs text-[var(--color-faint)] mt-0.5 block">{hint}</span>}
    </label>
  );
}

export function SevBadge({ sev, score }: { sev: string; score?: number }) {
  const bg = {
    critical: "var(--color-crit)",
    high: "var(--color-high)",
    medium: "var(--color-med)",
    low: "var(--color-low)",
    info: "var(--color-info)",
  }[sev];
  return (
    <span
      className="chip font-medium text-white"
      style={{ background: bg, color: sev === "medium" || sev === "low" ? "#1a1a1d" : "white" }}
    >
      {sev}
      {score ? ` ${score.toFixed(1)}` : ""}
    </span>
  );
}

export function Stat({ label, value, accent }: { label: string; value: ReactNode; accent?: boolean }) {
  return (
    <div className="card p-4">
      <div className="text-xs leading-4 text-[var(--color-faint)]">{label}</div>
      <div className={cls("text-[28px] leading-8 font-medium mt-2", accent && "text-[var(--color-brand)]")}>{value}</div>
    </div>
  );
}
