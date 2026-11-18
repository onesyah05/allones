import { useState } from "react";
import { api } from "../lib/api";
import { Wordmark } from "../components/Logo";

export function LoginPage({ onLogin }: { onLogin: (token: string) => void }) {
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");

  async function submit() {
    setBusy(true);
    setErr("");
    try {
      const r = await api.post<{ token: string }>("/auth/login", { password });
      onLogin(r.token);
    } catch (e: any) {
      setErr(e.message || "login failed");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="h-full grid place-items-center p-4">
      <div className="w-full max-w-sm">
        <div className="flex items-center gap-2.5 mb-6 justify-center">
          <Wordmark size={22} />
        </div>
        <div className="card p-5">
          <label className="text-xs font-medium text-[var(--color-muted)] mb-1 block">Dashboard password</label>
          <input
            className="input mb-3"
            type="password"
            autoFocus
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            onKeyDown={(e) => e.key === "Enter" && submit()}
          />
          {err && <div className="text-sm text-[var(--color-danger)] mb-3">{err}</div>}
          <button className="btn btn-brand w-full" disabled={busy} onClick={submit}>
            {busy ? "…" : "Unlock"}
          </button>
        </div>
      </div>
    </div>
  );
}
