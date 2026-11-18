// Tiny typed API client for the AllOnes backend.

const TOKEN_KEY = "allones_token";

export function getToken(): string {
  try {
    return localStorage.getItem(TOKEN_KEY) || "";
  } catch {
    return "";
  }
}
export function setToken(t: string) {
  try {
    if (t) localStorage.setItem(TOKEN_KEY, t);
    else localStorage.removeItem(TOKEN_KEY);
  } catch {
    /* ignore */
  }
}

async function req<T = any>(method: string, path: string, body?: any): Promise<T> {
  const headers: Record<string, string> = {};
  const tok = getToken();
  if (tok) headers["Authorization"] = "Bearer " + tok;
  if (body !== undefined) headers["Content-Type"] = "application/json";
  const res = await fetch("/api" + path, {
    method,
    headers,
    body: body !== undefined ? JSON.stringify(body) : undefined,
  });
  if (res.status === 401) {
    throw new ApiError("unauthorized", 401);
  }
  const text = await res.text();
  let data: any = null;
  try {
    data = text ? JSON.parse(text) : null;
  } catch {
    data = { raw: text };
  }
  if (!res.ok) {
    throw new ApiError((data && data.error) || res.statusText, res.status);
  }
  return data as T;
}

export class ApiError extends Error {
  status: number;
  constructor(msg: string, status: number) {
    super(msg);
    this.status = status;
  }
}

export interface VersionInfo {
  current: string;
  latest: string;
  update_available: boolean;
  notes: string;
  url: string;
  published_at: string;
}

export const api = {
  get: <T = any>(p: string) => req<T>("GET", p),
  post: <T = any>(p: string, b?: any) => req<T>("POST", p, b ?? {}),
  put: <T = any>(p: string, b?: any) => req<T>("PUT", p, b ?? {}),
  del: <T = any>(p: string) => req<T>("DELETE", p),
};

// ---------- types ----------

export interface Session {
  id: string;
  title: string;
  role: string;
  model: string;
  toolset: string;
  engagement_id: string;
  goal: string;
  pinned: boolean;
  updated_at: number;
  message_count: number;
  tokens_in: number;
  tokens_out: number;
}

export interface Message {
  id: number;
  role: string;
  content: string;
  reasoning?: string;
  tool_calls?: ToolCall[];
  tool_call_id?: string;
  name?: string;
  meta?: any;
  created_at: number;
}

export interface ToolCall {
  id: string;
  name: string;
  args: any;
}

export interface AgentEvent {
  type: string;
  session_id?: string;
  text?: string;
  tool?: string;
  call_id?: string;
  args?: any;
  result?: string;
  is_error?: boolean;
  usage?: { input: number; output: number };
  meta?: any;
  ts: number;
}

export interface Finding {
  id: string;
  engagement_id: string;
  title: string;
  severity: string;
  cvss_vector: string;
  cvss_score: number;
  cwe: string;
  category: string;
  target: string;
  description: string;
  reproduction: string;
  impact: string;
  remediation: string;
  evidence: string;
  status: string;
}

export interface Engagement {
  id: string;
  name: string;
  client: string;
  scope: string[];
  exclusions: string[];
  authorized: boolean;
  authorization_note: string;
  rules: string;
  status: string;
}

// ---------- chat streaming ----------

export interface ChatCallbacks {
  onEvent: (ev: AgentEvent) => void;
  onDone: () => void;
  onError: (e: string) => void;
}

export function streamChat(
  body: { session_id?: string; text: string; role?: string; model?: string; toolset?: string; workspace?: string; images?: any[] },
  cb: ChatCallbacks,
): () => void {
  const ctrl = new AbortController();
  const tok = getToken();
  const headers: Record<string, string> = { "Content-Type": "application/json" };
  if (tok) headers["Authorization"] = "Bearer " + tok;

  (async () => {
    try {
      const res = await fetch("/api/chat", {
        method: "POST",
        headers,
        body: JSON.stringify(body),
        signal: ctrl.signal,
      });
      if (!res.ok || !res.body) {
        cb.onError("HTTP " + res.status);
        cb.onDone();
        return;
      }
      const reader = res.body.getReader();
      const dec = new TextDecoder();
      let buf = "";
      while (true) {
        const { done, value } = await reader.read();
        if (done) break;
        buf += dec.decode(value, { stream: true });
        const parts = buf.split("\n\n");
        buf = parts.pop() || "";
        for (const part of parts) {
          const line = part.trim();
          if (!line.startsWith("data:")) continue;
          const json = line.slice(5).trim();
          if (!json) continue;
          try {
            cb.onEvent(JSON.parse(json) as AgentEvent);
          } catch {
            /* ignore malformed */
          }
        }
      }
      cb.onDone();
    } catch (e: any) {
      if (e?.name !== "AbortError") cb.onError(String(e?.message || e));
      cb.onDone();
    }
  })();

  return () => ctrl.abort();
}

export function cls(...xs: (string | false | undefined | null)[]): string {
  return xs.filter(Boolean).join(" ");
}

export function fmtTime(ms: number): string {
  const d = new Date(ms);
  const now = Date.now();
  const diff = now - ms;
  if (diff < 60000) return "just now";
  if (diff < 3600000) return Math.floor(diff / 60000) + "m ago";
  if (diff < 86400000) return Math.floor(diff / 3600000) + "h ago";
  if (diff < 7 * 86400000) return Math.floor(diff / 86400000) + "d ago";
  return d.toLocaleDateString();
}

export function sevColor(sev: string): string {
  return (
    {
      critical: "var(--color-crit)",
      high: "var(--color-high)",
      medium: "var(--color-med)",
      low: "var(--color-low)",
      info: "var(--color-info)",
    }[sev] || "var(--color-info)"
  );
}
