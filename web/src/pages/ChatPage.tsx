import { useEffect, useRef, useState, useCallback, useMemo } from "react";
import { api, cls, fmtTime, type Message } from "../lib/api";
import { Icon, type IconName } from "../components/Icon";
import { StarMark } from "../components/Logo";
import { Markdown } from "../lib/markdown";
import { toast } from "../components/Toasts";
import { Link } from "react-router-dom";
import { FolderPicker, basename } from "../components/FolderPicker";
import { type LiveBlock, type LiveTool } from "../lib/chat-stream";
import { useChat, type OutImage } from "../lib/chat-context";

// Seventeen roles are too many to choose from mid-thought. The composer offers
// six kinds of agent; a kind with several specialists lets you narrow it down.
interface Category {
  id: string;
  label: string;
  icon: IconName;
  roles: string[];
  hint: string;
  starters: string[];
}

const CATEGORIES: Category[] = [
  {
    id: "assistant",
    label: "Assistant",
    icon: "sparkle",
    roles: ["assistant"],
    hint: "General work with every tool",
    starters: ["Summarise what changed in this workspace today", "Find and fix the failing test in this project", "Draft a status update from my open board tasks"],
  },
  {
    id: "planner",
    label: "Planner",
    icon: "kanban",
    roles: ["planner"],
    hint: "Turns a goal into ordered steps",
    starters: ["Plan the migration of this service to Postgres", "Break this feature into tasks and put them on the board", "What is the riskiest part of this refactor, and in what order should we do it"],
  },
  {
    id: "engineer",
    label: "Engineer",
    icon: "terminal",
    roles: ["coder", "reviewer", "sre"],
    hint: "Writes, reviews and operates code",
    starters: ["Review the last commit for bugs", "Add input validation to the upload handler and run the tests", "Why is the service slow to start, check the logs"],
  },
  {
    id: "analyst",
    label: "Analyst",
    icon: "search",
    roles: ["researcher", "data-analyst"],
    hint: "Research and data, with sources",
    starters: ["Compare the three most used Go web frameworks, with sources", "Load sales.csv and show the monthly trend", "What changed in OWASP Top 10 2025 compared to 2021"],
  },
  {
    id: "security",
    label: "Security",
    icon: "shield",
    roles: ["security-lead", "security-recon", "security-webapp", "security-api", "appsec-auditor", "reverse-engineer", "ai-red-team", "threat-modeler", "incident-responder"],
    hint: "Authorized testing, audits and reverse engineering",
    starters: ["Audit this repository for secrets and vulnerable dependencies", "Show the engagement status and what to test next", "Inspect this binary and report its hardening"],
  },
  {
    id: "writer",
    label: "Writer",
    icon: "edit",
    roles: ["writer"],
    hint: "Clear prose for a specific reader",
    starters: ["Rewrite this README so a new contributor can start in five minutes", "Turn these findings into an executive summary", "Write release notes from the last ten commits"],
  },
];

function categoryOf(role: string): Category {
  return CATEGORIES.find((c) => c.roles.includes(role)) || CATEGORIES[0];
}

function readDataURL(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const fr = new FileReader();
    fr.onload = () => resolve(String(fr.result || ""));
    fr.onerror = () => reject(fr.error);
    fr.readAsDataURL(file);
  });
}

export function ChatPage() {
  const {
    sessions, current, selectSession, newChat, removeSession,
    messages, setMessages, streaming, activeSession, liveBlocks, approval, question,
    models, model, setModel, roleTitles, send, stop, approve, answer, runCommand,
  } = useChat();

  const [input, setInput] = useState("");
  const [role, setRole] = useState("assistant");
  const [images, setImages] = useState<OutImage[]>([]);
  const [workspace, setWorkspace] = useState(() => {
    try { return localStorage.getItem("allones_workspace") || ""; } catch { return ""; }
  });
  const [pickerOpen, setPickerOpen] = useState(false);
  const [sidebar, setSidebar] = useState(true);
  const [agentOpen, setAgentOpen] = useState(false);
  const scrollRef = useRef<HTMLDivElement>(null);
  const taRef = useRef<HTMLTextAreaElement>(null);
  const fileRef = useRef<HTMLInputElement>(null);

  const category = categoryOf(role);
  // One turn runs at a time. It may belong to the session on screen (busyHere)
  // or to another one the operator navigated away from (busyElsewhere).
  const busyHere = streaming && activeSession === current;
  const busyElsewhere = streaming && activeSession !== current;

  useEffect(() => {
    api.get<any>("/config").then((c) => setWorkspace((w) => w || c?.tools?.workspace || ""));
  }, []);

  // Role and workspace follow the session that is on screen.
  useEffect(() => {
    const s = sessions.find((x) => x.id === current);
    if (s?.role) setRole(s.role);
    if (s && (s as any).workspace) setWorkspace((s as any).workspace);
  }, [current, sessions]);

  const scrollDown = useCallback(() => {
    requestAnimationFrame(() => {
      const el = scrollRef.current;
      if (el) el.scrollTop = el.scrollHeight;
    });
  }, []);
  useEffect(scrollDown, [messages, liveBlocks, scrollDown]);

  // Tool results live in separate "tool" messages; index them by call id so a
  // past turn shows each call together with what it returned.
  const results = useMemo(() => {
    const m: Record<string, { content: string; error: boolean }> = {};
    for (const x of messages) {
      if (x.role === "tool" && x.tool_call_id) m[x.tool_call_id] = { content: x.content, error: !!x.meta?.error };
    }
    return m;
  }, [messages]);

  function pickWorkspace(path: string) {
    setWorkspace(path);
    try { localStorage.setItem("allones_workspace", path); } catch { /* ignore */ }
    setPickerOpen(false);
    toast("Workspace set", path, "ok");
  }

  async function addFiles(files: Iterable<File>) {
    const out: OutImage[] = [];
    for (const f of files) {
      if (!f.type.startsWith("image/")) continue;
      try {
        const url = await readDataURL(f);
        const data = url.split(",")[1] || "";
        if (data) out.push({ mime: f.type, data, url });
      } catch { /* ignore unreadable file */ }
    }
    if (out.length) setImages((xs) => [...xs, ...out]);
  }

  function onPaste(e: React.ClipboardEvent) {
    const items = e.clipboardData?.items;
    if (!items) return;
    const files: File[] = [];
    for (const it of items) {
      if (it.kind === "file" && it.type.startsWith("image/")) {
        const f = it.getAsFile();
        if (f) files.push(f);
      }
    }
    if (files.length) { e.preventDefault(); void addFiles(files); }
  }

  function resetTa() {
    if (taRef.current) taRef.current.style.height = "auto";
  }

  function submit() {
    const text = input.trim();
    if ((!text && images.length === 0) || streaming) return;
    if (text.startsWith("/")) {
      setInput("");
      resetTa();
      runCommand(text).then((out) => setMessages((m) => [...m, { id: Date.now(), role: "notice", content: out, created_at: Date.now() }]));
      return;
    }
    send(text, { role, workspace: workspace || undefined, images });
    setInput("");
    setImages([]);
    resetTa();
  }

  function startNewChat() {
    if (streaming) return;
    newChat();
    setInput("");
    setImages([]);
    taRef.current?.focus();
  }

  const empty = messages.length === 0 && !busyHere;
  const title = sessions.find((s) => s.id === current)?.title || "New chat";

  return (
    <div className="h-full flex">
      {sidebar && (
        <div className="panel w-64 shrink-0 border-r border-[var(--color-border-soft)] flex flex-col">
          <div className="h-14 shrink-0 px-3 flex items-center">
            <button className="btn btn-ghost w-full" onClick={startNewChat} disabled={streaming} title={streaming ? "Finish the running chat first" : "New chat"}>
              <Icon name="plus" className="w-4 h-4" /> New chat
            </button>
          </div>
          <div className="flex-1 overflow-y-auto px-3 pb-3 space-y-0.5">
            {sessions.map((s) => {
              const working = streaming && activeSession === s.id;
              return (
                <div
                  key={s.id}
                  onClick={() => selectSession(s.id)}
                  className={cls(
                    "group h-9 px-2 rounded-lg cursor-pointer flex items-center gap-2 text-[13px] transition-colors duration-150",
                    current === s.id
                      ? "bg-[var(--color-surface-2)] text-[var(--color-text)] shadow-[inset_0_1px_0_rgb(255_255_255/0.08)]"
                      : "text-[var(--color-muted)] hover:bg-[rgb(237_237_240/0.05)] hover:text-[var(--color-text)]",
                  )}
                >
                  {working && <span className="spinner shrink-0" aria-label="Working" title="This chat is working" />}
                  <span className="truncate flex-1">{s.title || "New chat"}</span>
                  {working ? (
                    <span className="text-[10px] font-mono text-[var(--color-faint)] shrink-0">live</span>
                  ) : (
                    <>
                      <span className="text-xs font-mono text-[var(--color-faint)] group-hover:hidden shrink-0">{fmtTime(s.updated_at).replace(" ago", "")}</span>
                      <button
                        onClick={(e) => { e.stopPropagation(); void removeSession(s.id); }}
                        className="hidden group-hover:block text-[var(--color-faint)] hover:text-[var(--color-danger)] shrink-0"
                        title="Delete"
                      >
                        <Icon name="trash" className="w-4 h-4" />
                      </button>
                    </>
                  )}
                </div>
              );
            })}
            {sessions.length === 0 && <div className="px-2 py-2 text-xs text-[var(--color-faint)]">Conversations appear here</div>}
          </div>
        </div>
      )}

      <div className="flex-1 min-w-0 flex flex-col">
        <div className="h-14 shrink-0 px-4 flex items-center gap-3">
          <button className="btn btn-ghost !w-8 !px-0" onClick={() => setSidebar((s) => !s)} title="Toggle conversations">
            <Icon name="chat" className="w-4 h-4" />
          </button>
          <span className="text-[13px] text-[var(--color-muted)] truncate">{empty ? "" : title}</span>
          <div className="flex-1" />
        </div>

        <div ref={scrollRef} className="flex-1 overflow-y-auto">
          {empty ? (
            <div className="h-full flex items-center">
              <div className="w-full max-w-[820px] mx-auto px-6 pb-16">
                <h2 className="text-[32px] leading-[1.15] font-normal">{category.hint}</h2>
                <p className="mt-2 text-[var(--color-muted)]">Pick a kind of agent below, then describe the work</p>
                <div className="mt-8 space-y-0.5">
                  {category.starters.map((s) => (
                    <button
                      key={s}
                      onClick={() => {
                        setInput(s);
                        taRef.current?.focus();
                      }}
                      className="w-[calc(100%+24px)] h-10 px-3 -mx-3 rounded-lg flex items-center gap-3 text-left text-[var(--color-muted)] hover:bg-[rgb(237_237_240/0.05)] hover:text-[var(--color-text)] transition-colors duration-150"
                    >
                      <Icon name="chevron-right" className="w-4 h-4 text-[var(--color-faint)] shrink-0" />
                      <span className="truncate">{s}</span>
                    </button>
                  ))}
                </div>
              </div>
            </div>
          ) : (
            <div className="max-w-[820px] mx-auto px-6 pt-4 pb-10 space-y-8">
              {messages.map((m) => (
                <MessageView key={m.id} m={m} results={results} />
              ))}
              {busyHere && <LiveTurn blocks={liveBlocks} />}
            </div>
          )}
        </div>

        <div className="shrink-0 px-6 pb-6">
          <div className="max-w-[820px] mx-auto space-y-2">
            {busyElsewhere && (
              <div className="card p-3 flex items-center gap-3 text-[13px] text-[var(--color-muted)] fadein">
                <span className="spinner shrink-0" />
                <span className="flex-1">Agen sedang bekerja di percakapan lain. Tunggu hingga selesai sebelum mengirim yang baru.</span>
                <button className="btn btn-ghost shrink-0" onClick={() => activeSession && selectSession(activeSession)}>
                  Lihat
                </button>
              </div>
            )}
            {approval && (
              <div className="card p-4 fadein">
                <div className="flex items-start gap-3">
                  <Icon name="lock" className="w-4 h-4 mt-0.5 text-[var(--color-warn)] shrink-0" />
                  <div className="flex-1 min-w-0">
                    <div className="font-medium">{approval.title || `Run ${approval.tool}`}</div>
                    <pre className="mt-2 text-xs leading-5 font-mono text-[var(--color-muted)] max-h-28 overflow-y-auto whitespace-pre-wrap break-all">{approval.detail}</pre>
                  </div>
                </div>
                <div className="mt-4 flex justify-end gap-2">
                  <button className="btn btn-ghost" onClick={() => approve(false)}>
                    Deny
                  </button>
                  <button className="btn btn-brand" onClick={() => approve(true)}>
                    Allow once
                  </button>
                </div>
              </div>
            )}
            {question && (
              <div className="card p-4 fadein">
                <div className="font-medium">{question.text}</div>
                <div className="mt-3">
                  {question.options?.length ? (
                    <div className="flex gap-2 flex-wrap">
                      {question.options.map((o) => (
                        <button key={o} className="btn btn-ghost" onClick={() => answer(o)}>
                          {o}
                        </button>
                      ))}
                    </div>
                  ) : (
                    <QuickAnswer onAnswer={answer} />
                  )}
                </div>
              </div>
            )}

            {images.length > 0 && (
              <div className="flex gap-2 flex-wrap px-1">
                {images.map((im, i) => (
                  <div key={i} className="relative group">
                    <img src={im.url} alt="" className="h-16 w-16 object-cover rounded-lg border border-[var(--color-border)]" />
                    <button
                      onClick={() => setImages((xs) => xs.filter((_, j) => j !== i))}
                      className="absolute -top-1.5 -right-1.5 w-5 h-5 rounded-full bg-[var(--color-surface-2)] border border-[var(--color-border)] text-[var(--color-muted)] hover:text-[var(--color-danger)] grid place-items-center"
                      title="Remove"
                    >
                      <Icon name="x" className="w-3 h-3" />
                    </button>
                  </div>
                ))}
              </div>
            )}

            <div className="card focus-within:border-[rgb(237_237_240/0.24)] transition-colors duration-150">
              <textarea
                ref={taRef}
                value={input}
                disabled={busyElsewhere}
                onPaste={onPaste}
                onChange={(e) => {
                  setInput(e.target.value);
                  e.target.style.height = "auto";
                  e.target.style.height = Math.min(e.target.scrollHeight, 200) + "px";
                }}
                onKeyDown={(e) => {
                  if (e.key === "Enter" && !e.shiftKey) {
                    e.preventDefault();
                    submit();
                  }
                }}
                rows={1}
                placeholder={busyElsewhere ? "Agen sedang bekerja…" : "Describe the work"}
                className="block w-full bg-transparent resize-none outline-none px-4 pt-3 pb-2 max-h-[200px] placeholder:text-[var(--color-faint)] disabled:opacity-50"
              />
              <div className="px-2 pb-2 flex items-center gap-1">
                <AgentMenu
                  open={agentOpen}
                  setOpen={setAgentOpen}
                  category={category}
                  onPick={(c) => setRole(c.roles[0])}
                />
                {category.roles.length > 1 && (
                  <Picker value={role} onChange={setRole} options={category.roles.map((r) => ({ value: r, label: roleTitles[r] || r }))} title="Specialist" />
                )}
                <input
                  ref={fileRef}
                  type="file"
                  accept="image/*"
                  multiple
                  className="hidden"
                  onChange={(e) => { if (e.target.files) void addFiles(e.target.files); e.target.value = ""; }}
                />
                <button
                  onClick={() => fileRef.current?.click()}
                  title="Attach images"
                  className="h-7 px-2 rounded-md flex items-center gap-1.5 text-xs shrink-0 text-[var(--color-faint)] hover:text-[var(--color-text)] hover:bg-[rgb(237_237_240/0.05)] transition-colors duration-150"
                >
                  <Icon name="image" className="w-3.5 h-3.5" />
                </button>
                <div className="flex-1" />
                <button
                  onClick={() => setPickerOpen(true)}
                  title={workspace ? "Workspace: " + workspace : "Choose a workspace folder"}
                  className="h-7 px-2 rounded-md flex items-center gap-1.5 text-xs shrink-0 text-[var(--color-muted)] hover:text-[var(--color-text)] hover:bg-[rgb(237_237_240/0.05)] transition-colors duration-150"
                >
                  <Icon name="folder" className="w-3.5 h-3.5" />
                  <span className="max-w-[140px] truncate">{workspace ? basename(workspace) : "Workspace"}</span>
                </button>
                {models.length > 0 ? (
                  <Picker value={model} onChange={setModel} options={models.map((m) => ({ value: m.id, label: m.id }))} title="Model" />
                ) : (
                  <Link to="/providers" className="h-7 px-2 flex items-center text-xs text-[var(--color-warn)] hover:text-[var(--color-text)] shrink-0">
                    Connect a model
                  </Link>
                )}
                <button
                  className={cls("btn !w-8 !px-0 shrink-0", busyHere ? "stream-stop" : (input.trim() || images.length) ? "btn-brand" : "btn-ghost")}
                  onClick={busyHere ? stop : submit}
                  disabled={!busyHere && (busyElsewhere || (!input.trim() && images.length === 0))}
                  title={busyHere ? "Stop generating" : "Send"}
                  aria-label={busyHere ? "Stop generating" : "Send"}
                >
                  <Icon name={busyHere ? "stop" : "send"} className="w-4 h-4" />
                </button>
              </div>
            </div>
            <div className="flex items-center justify-between text-xs text-[var(--color-faint)] px-1">
              <span>
                <Key>Enter</Key> to send, <Key>Shift</Key> <Key>Enter</Key> for a new line
              </span>
              <span>
                <Key>/</Key> for commands
              </span>
            </div>
          </div>
        </div>
      </div>
      <FolderPicker open={pickerOpen} initial={workspace} onClose={() => setPickerOpen(false)} onPick={pickWorkspace} />
    </div>
  );
}

// The agent kind as a dropdown, so the composer stays one quiet line instead of
// a row of six buttons. A kind with several specialists keeps its Picker beside.
function AgentMenu({ open, setOpen, category, onPick }: { open: boolean; setOpen: (v: boolean) => void; category: Category; onPick: (c: Category) => void }) {
  return (
    <div className="relative shrink-0">
      <button
        onClick={() => setOpen(!open)}
        className="h-7 px-2 rounded-md flex items-center gap-1.5 text-xs whitespace-nowrap text-[var(--color-text)] bg-[var(--color-surface-2)] shadow-[inset_0_1px_0_rgb(255_255_255/0.08)] hover:bg-[rgb(237_237_240/0.08)] transition-colors duration-150"
        title={category.hint}
      >
        <Icon name={category.icon} className="w-3.5 h-3.5" />
        {category.label}
        <Icon name="chevron-down" className="w-3 h-3 text-[var(--color-faint)]" />
      </button>
      {open && (
        <>
          <div className="fixed inset-0 z-10" onClick={() => setOpen(false)} />
          <div className="absolute bottom-full mb-1 left-0 z-20 w-64 card p-1 fadein">
            {CATEGORIES.map((c) => (
              <button
                key={c.id}
                onClick={() => { onPick(c); setOpen(false); }}
                className={cls(
                  "w-full px-2 py-1.5 rounded-md flex items-start gap-2.5 text-left transition-colors duration-150",
                  c.id === category.id ? "bg-[var(--color-surface-2)]" : "hover:bg-[rgb(237_237_240/0.05)]",
                )}
              >
                <Icon name={c.icon} className="w-4 h-4 mt-0.5 shrink-0 text-[var(--color-muted)]" />
                <span className="min-w-0">
                  <span className="block text-[13px] text-[var(--color-text)]">{c.label}</span>
                  <span className="block text-xs text-[var(--color-faint)] truncate">{c.hint}</span>
                </span>
              </button>
            ))}
          </div>
        </>
      )}
    </div>
  );
}

function Key({ children }: { children: React.ReactNode }) {
  return <kbd className="inline-block px-1 h-4 leading-4 rounded border border-[var(--color-border)] font-mono text-[11px] text-[var(--color-muted)]">{children}</kbd>;
}

function Picker({ value, onChange, options, title }: { value: string; onChange: (v: string) => void; options: { value: string; label: string }[]; title: string }) {
  return (
    <div className="relative shrink-0" title={title}>
      <select
        value={value}
        onChange={(e) => onChange(e.target.value)}
        className="h-7 max-w-[176px] appearance-none bg-transparent rounded-md pl-2 pr-6 text-xs text-[var(--color-muted)] hover:text-[var(--color-text)] hover:bg-[rgb(237_237_240/0.05)] outline-none cursor-pointer truncate transition-colors duration-150"
      >
        {options.map((o) => (
          <option key={o.value} value={o.value} className="bg-[var(--color-surface)]">
            {o.label}
          </option>
        ))}
      </select>
      <Icon name="chevron-down" className="w-3 h-3 absolute right-1.5 top-2 text-[var(--color-faint)] pointer-events-none" />
    </div>
  );
}

function QuickAnswer({ onAnswer }: { onAnswer: (a: string) => void }) {
  const [v, setV] = useState("");
  return (
    <div className="flex gap-2">
      <input className="input" value={v} onChange={(e) => setV(e.target.value)} onKeyDown={(e) => e.key === "Enter" && onAnswer(v)} autoFocus />
      <button className="btn btn-brand" onClick={() => onAnswer(v)}>
        Reply
      </button>
    </div>
  );
}

function MessageView({ m, results }: { m: Message; results: Record<string, { content: string; error: boolean }> }) {
  if (m.role === "tool") return null;
  if (m.role === "notice") {
    return <pre className="text-xs leading-5 font-mono text-[var(--color-muted)] whitespace-pre-wrap border-l border-[var(--color-border)] pl-4">{m.content}</pre>;
  }
  if (m.role === "user") {
    const imgs: string[] = Array.isArray(m.meta?.images) ? m.meta.images : [];
    return (
      <div className="flex justify-end">
        <div className="max-w-[85%] space-y-2">
          {imgs.length > 0 && (
            <div className="flex gap-2 flex-wrap justify-end">
              {imgs.map((src, i) => (
                <img key={i} src={src} alt="" className="max-h-48 rounded-xl border border-[var(--color-border)]" />
              ))}
            </div>
          )}
          {m.content && <div className="rounded-xl bg-[var(--color-surface-2)] px-4 py-2 whitespace-pre-wrap leading-6">{m.content}</div>}
        </div>
      </div>
    );
  }
  const calls = m.tool_calls || [];
  if (!m.content && !m.reasoning && calls.length === 0) return null;
  return (
    <AgentRow>
      {m.reasoning && <ReasoningBlock text={m.reasoning} />}
      {m.content && <Markdown text={m.content} />}
      {calls.length > 0 && (
        <Trace
          items={calls.map((tc) => ({
            callId: tc.id,
            name: tc.name,
            args: tc.args,
            result: results[tc.id]?.content,
            isError: results[tc.id]?.error,
            running: false,
          }))}
        />
      )}
    </AgentRow>
  );
}

function AgentRow({ children, live }: { children: React.ReactNode; live?: boolean }) {
  return (
    <div className="flex gap-4">
      <div className="w-4 pt-1 shrink-0 text-[var(--color-text)]">{live ? <ThinkingStar /> : <StarMark size={14} />}</div>
      <div className="min-w-0 flex-1 space-y-3">{children}</div>
    </div>
  );
}

function ThinkingStar() {
  return <span className="thinking-star inline-flex" aria-label="Thinking"><StarMark size={16} /></span>;
}

// Chain-of-thought from reasoning models. Shown expanded while the turn streams
// (so the model's words are visible live, not just tool rows) and collapsed in
// the saved transcript, where it is still the only narration when content is empty.
function ReasoningBlock({ text, live }: { text: string; live?: boolean }) {
  return (
    <details open={live} className="reasoning">
      <summary className="cursor-pointer select-none text-xs text-[var(--color-faint)] hover:text-[var(--color-muted)] transition-colors">
        {live ? "Thinking…" : "Reasoning"}
      </summary>
      <div className="mt-2 leading-6 whitespace-pre-wrap border-l border-[var(--color-border)] pl-4 text-[var(--color-muted)] text-[13px]">{text}</div>
    </details>
  );
}

function LiveTurn({ blocks }: { blocks: LiveBlock[] }) {
  return (
    <AgentRow live>
      {blocks.map((block, i) => {
        if (block.kind === "tool") return <Trace key={i} items={[block.tool]} />;
        if (block.kind === "text") return <Markdown key={i} text={block.text} streaming={i === blocks.length - 1} />;
        if (block.kind === "reasoning") return <ReasoningBlock key={i} text={block.text} live />;
        return <div key={i} className="text-xs text-[var(--color-faint)]">{block.text}</div>;
      })}
      {blocks.length === 0 && <div className="text-[var(--color-faint)] leading-6">Thinking</div>}
    </AgentRow>
  );
}

// The trace: every tool call of a step in one quiet list. Rows open to show
// arguments and the result.
function Trace({ items }: { items: LiveTool[] }) {
  const [open, setOpen] = useState("");
  return (
    <div className="card overflow-hidden divide-y divide-[var(--color-border-soft)]">
      {items.map((t) => {
        const isOpen = open === t.callId;
        return (
          <div key={t.callId}>
            <button
              className="w-full h-9 px-3 flex items-center gap-3 text-xs hover:bg-[rgb(237_237_240/0.04)] transition-colors duration-150"
              onClick={() => setOpen(isOpen ? "" : t.callId)}
            >
              <Icon name={toolIcon(t.name)} className="w-3.5 h-3.5 shrink-0 text-[var(--color-faint)]" />
              <span className="font-mono text-[var(--color-text)] shrink-0">{t.name}</span>
              <span className="font-mono text-[var(--color-faint)] truncate flex-1 text-left">{argSummary(t.args)}</span>
              {t.running ? (
                <ThinkingStar />
              ) : t.isError ? (
                <span className="text-[var(--color-danger)] shrink-0">failed</span>
              ) : (
                <Icon name="check" className="w-3.5 h-3.5 shrink-0 text-[var(--color-faint)]" />
              )}
            </button>
            {isOpen && (
              <div className="px-3 pb-3 space-y-2">
                <pre className="font-mono text-xs leading-5 text-[var(--color-muted)] bg-[rgb(12_12_14/0.9)] rounded-lg p-3 overflow-x-auto">{JSON.stringify(t.args, null, 2)}</pre>
                {t.result !== undefined && (
                  <pre className={cls("font-mono text-xs leading-5 bg-[rgb(12_12_14/0.9)] rounded-lg p-3 overflow-auto max-h-72 whitespace-pre-wrap", t.isError ? "text-[var(--color-danger)]" : "text-[var(--color-muted)]")}>
                    {t.result}
                  </pre>
                )}
              </div>
            )}
          </div>
        );
      })}
    </div>
  );
}

function argSummary(args: any): string {
  if (!args || typeof args !== "object") return "";
  for (const k of ["path", "command", "url", "query", "target", "host", "pattern", "prompt", "action", "name", "title"]) {
    if (args[k]) return String(args[k]).replace(/\s+/g, " ").slice(0, 96);
  }
  const first = Object.values(args)[0];
  return first ? String(first).replace(/\s+/g, " ").slice(0, 96) : "";
}

function toolIcon(name: string): IconName {
  if (name.endsWith("_file") || name === "glob" || name === "grep" || name === "list_files") return "file";
  if (name === "shell" || name === "process" || name.startsWith("vps_")) return "terminal";
  if (name.startsWith("web_") || name === "http_request" || name === "browser" || name === "http_probe" || name === "dns_lookup") return "globe";
  if (["engagement", "add_intel", "report_finding", "security_report", "cvss"].includes(name)) return "shield";
  if (name.includes("scan") || name.includes("audit") || name === "sec_tool") return "bug";
  if (name === "memory" || name.startsWith("rag_") || name === "session_search") return "brain";
  if (name === "delegate_task" || name === "list_roles") return "users";
  if (name === "skill") return "book";
  return "wrench";
}
