import { createContext, useContext, useState, useRef, useCallback, useEffect, type ReactNode } from "react";
import { api, streamChat, type Session, type Message, type AgentEvent } from "./api";
import { reduceLive, type LiveBlock } from "./chat-stream";
import { toast } from "../components/Toasts";

export interface Approval { id: string; tool: string; title: string; detail: string; }
export interface Question { id: string; text: string; options?: string[]; }
// An image ready to send: base64 payload with no data: prefix, plus a data URL for preview.
export interface OutImage { mime: string; data: string; url: string; }

interface ChatCtx {
  sessions: Session[];
  loadSessions: () => Promise<void>;
  current: string;
  selectSession: (id: string) => void;
  newChat: () => void;
  removeSession: (id: string) => Promise<void>;
  messages: Message[];
  setMessages: React.Dispatch<React.SetStateAction<Message[]>>;
  streaming: boolean;
  activeSession: string;
  liveBlocks: LiveBlock[];
  approval: Approval | null;
  question: Question | null;
  models: { id: string }[];
  model: string;
  setModel: (m: string) => void;
  roleTitles: Record<string, string>;
  send: (text: string, opts: { role: string; workspace?: string; images?: OutImage[] }) => void;
  stop: () => Promise<void>;
  approve: (ok: boolean) => Promise<void>;
  answer: (text: string) => Promise<void>;
  runCommand: (text: string) => Promise<string>;
}

const Ctx = createContext<ChatCtx | null>(null);

export function useChat(): ChatCtx {
  const v = useContext(Ctx);
  if (!v) throw new Error("useChat must be used inside <ChatProvider>");
  return v;
}

const LS_MODEL = "allones_model";
const LS_CURRENT = "allones_current_session";

export function ChatProvider({ children }: { children: ReactNode }) {
  const [sessions, setSessions] = useState<Session[]>([]);
  const [current, setCurrent] = useState(() => {
    try { return localStorage.getItem(LS_CURRENT) || ""; } catch { return ""; }
  });
  const [messages, setMessages] = useState<Message[]>([]);
  const [streaming, setStreaming] = useState(false);
  const [activeSession, setActiveSession] = useState("");
  const [liveBlocks, setLiveBlocks] = useState<LiveBlock[]>([]);
  const [approval, setApproval] = useState<Approval | null>(null);
  const [question, setQuestion] = useState<Question | null>(null);
  const [models, setModels] = useState<{ id: string }[]>([]);
  const [model, setModelState] = useState("");
  const [roleTitles, setRoleTitles] = useState<Record<string, string>>({});

  const abortRef = useRef<() => void>(() => {});
  const currentRef = useRef(current);
  const activeRef = useRef("");
  const streamingRef = useRef(false);
  useEffect(() => { currentRef.current = current; }, [current]);

  // Last-used model is the default: prefer the saved one, fall back to the
  // server default, and only accept it once it is in the available list.
  const setModel = useCallback((m: string) => {
    setModelState(m);
    try { localStorage.setItem(LS_MODEL, m); } catch { /* ignore */ }
  }, []);

  const loadSessions = useCallback(async () => {
    const r = await api.get<{ sessions: Session[] }>("/sessions");
    setSessions((r.sessions || []).filter((s) => (s as any).source !== "subagent"));
  }, []);

  const loadMessages = useCallback(async (id: string) => {
    if (!id) { setMessages([]); return; }
    const r = await api.get<{ messages: Message[] }>(`/sessions/${id}/messages`);
    setMessages(r.messages || []);
  }, []);

  // Initial load: sessions, models (with saved-default resolution), role titles.
  useEffect(() => {
    loadSessions();
    api.get<{ models: { id: string }[]; default: string }>("/models").then((r) => {
      const list = r.models || [];
      setModels(list);
      let saved = "";
      try { saved = localStorage.getItem(LS_MODEL) || ""; } catch { /* ignore */ }
      const pick = (saved && list.some((m) => m.id === saved)) ? saved : (r.default || "");
      setModelState(pick);
    });
    api.get<{ roles: { name: string; title: string }[] }>("/roles").then((r) => {
      const m: Record<string, string> = {};
      (r.roles || []).forEach((x) => (m[x.name] = x.title || x.name));
      setRoleTitles(m);
    });
  }, [loadSessions]);

  // Switch the viewed transcript when the selected session changes, but never
  // clobber the in-flight transcript of the session that is still streaming.
  useEffect(() => {
    try { localStorage.setItem(LS_CURRENT, current); } catch { /* ignore */ }
    if (streamingRef.current && current === activeRef.current) return;
    loadMessages(current);
  }, [current, loadMessages]);

  // While streaming, titles may finish on the auxiliary worker a beat later.
  useEffect(() => {
    if (!streaming) return;
    const timer = window.setInterval(() => { void loadSessions(); }, 2000);
    return () => window.clearInterval(timer);
  }, [streaming, loadSessions]);

  const setBusy = useCallback((v: boolean) => {
    streamingRef.current = v;
    setStreaming(v);
  }, []);

  const onEvent = useCallback((ev: AgentEvent) => {
    // Live blocks only accrue for the streaming session.
    setLiveBlocks((blocks) => reduceLive(blocks, ev));
    switch (ev.type) {
      case "start":
        if (ev.session_id) {
          activeRef.current = ev.session_id;
          setActiveSession(ev.session_id);
          if (ev.session_id !== currentRef.current) {
            currentRef.current = ev.session_id;
            setCurrent(ev.session_id);
          }
        }
        void loadSessions();
        break;
      case "session_title":
        setSessions((items) => items.map((s) => s.id === ev.session_id ? { ...s, title: ev.text || s.title } : s));
        void loadSessions();
        break;
      case "approval_request":
        setApproval({ id: ev.call_id!, tool: ev.tool || "", title: ev.text || "", detail: ev.result || "" });
        break;
      case "question":
        setQuestion({ id: ev.call_id!, text: ev.text || "", options: ev.meta?.options });
        break;
      case "error":
        toast("Something went wrong", ev.text, "error");
        break;
    }
  }, [loadSessions]);

  const send = useCallback((text: string, opts: { role: string; workspace?: string; images?: OutImage[] }) => {
    if (streamingRef.current) return; // one active stream at a time
    const imgs = opts.images || [];
    const optimistic: Message = {
      id: Date.now(), role: "user", content: text, created_at: Date.now(),
      meta: imgs.length ? { images: imgs.map((i) => i.url) } : undefined,
    };
    setMessages((m) => [...m, optimistic]);
    setLiveBlocks([]);
    activeRef.current = current;
    setActiveSession(current);
    setBusy(true);
    abortRef.current = streamChat(
      {
        session_id: current || undefined, text, model, role: opts.role,
        workspace: opts.workspace || undefined,
        images: imgs.map((i) => ({ mime: i.mime, data: i.data })),
      },
      {
        onEvent,
        onDone: async () => {
          const finished = activeRef.current;
          setBusy(false);
          setApproval(null);
          setQuestion(null);
          if (finished === currentRef.current) await loadMessages(finished);
          setLiveBlocks([]);
          setActiveSession("");
          activeRef.current = "";
          void loadSessions();
        },
        onError: (e) => { setBusy(false); setActiveSession(""); activeRef.current = ""; toast("Connection lost", e, "error"); },
      },
    );
  }, [current, model, onEvent, loadMessages, loadSessions, setBusy]);

  const stop = useCallback(async () => {
    try {
      if (activeRef.current) await api.post("/chat/stop", { session_id: activeRef.current });
    } finally {
      abortRef.current();
      setBusy(false);
      setActiveSession("");
      activeRef.current = "";
      setQuestion(null);
      setApproval(null);
    }
  }, [setBusy]);

  const selectSession = useCallback((id: string) => setCurrent(id), []);

  const newChat = useCallback(() => {
    if (streamingRef.current) return;
    setCurrent("");
    setMessages([]);
    setLiveBlocks([]);
  }, []);

  const removeSession = useCallback(async (id: string) => {
    await api.del("/sessions/" + id);
    if (id === currentRef.current) { setCurrent(""); setMessages([]); setLiveBlocks([]); }
    await loadSessions();
  }, [loadSessions]);

  const approve = useCallback(async (ok: boolean) => {
    if (!approval) return;
    await api.post("/chat/approve", { id: approval.id, approved: ok });
    setApproval(null);
  }, [approval]);

  const answer = useCallback(async (text: string) => {
    if (!question) return;
    await api.post("/chat/answer", { id: question.id, answer: text });
    setQuestion(null);
  }, [question]);

  const runCommand = useCallback(async (text: string) => {
    const [cmd, ...rest] = text.slice(1).split(" ");
    const r = await api.post<{ output: string }>("/command", { session_id: currentRef.current, command: cmd, arg: rest.join(" ") });
    if (["model", "role", "toolset", "goal"].includes(cmd)) void loadSessions();
    return r.output;
  }, [loadSessions]);

  const value: ChatCtx = {
    sessions, loadSessions, current, selectSession, newChat, removeSession,
    messages, setMessages, streaming, activeSession, liveBlocks, approval, question,
    models, model, setModel, roleTitles, send, stop, approve, answer, runCommand,
  };
  return <Ctx.Provider value={value}>{children}</Ctx.Provider>;
}
