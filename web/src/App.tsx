import { useEffect, useState } from "react";
import { Routes, Route, NavLink, useLocation, Navigate } from "react-router-dom";
import { api, setToken, ApiError, cls } from "./lib/api";
import { Icon, IconName } from "./components/Icon";
import { ChatPage } from "./pages/ChatPage";
import { SecurityPage } from "./pages/SecurityPage";
import { ToolsPage } from "./pages/ToolsPage";
import { SkillsPage } from "./pages/SkillsPage";
import { RolesPage } from "./pages/RolesPage";
import { MemoryPage } from "./pages/MemoryPage";
import { KnowledgePage } from "./pages/KnowledgePage";
import { AutomationPage } from "./pages/AutomationPage";
import { BoardPage } from "./pages/BoardPage";
import { ServersPage } from "./pages/ServersPage";
import { McpPage } from "./pages/McpPage";
import { SettingsPage } from "./pages/SettingsPage";
import { ProvidersPage } from "./pages/ProvidersPage";
import { SystemPage } from "./pages/SystemPage";
import { FilesPage } from "./pages/FilesPage";
import { SetupPage } from "./pages/SetupPage";
import { LoginPage } from "./pages/LoginPage";
import { Toasts } from "./components/Toasts";
import { StarMark, Wordmark } from "./components/Logo";
import { ChatProvider, useChat } from "./lib/chat-context";
import { UpdatePill } from "./components/Updater";

interface NavItem {
  to: string;
  label: string;
  icon: IconName;
  group: string;
}

const NAV: NavItem[] = [
  { to: "/", label: "Chat", icon: "chat", group: "Work" },
  { to: "/security", label: "Security", icon: "shield", group: "Work" },
  { to: "/board", label: "Board", icon: "kanban", group: "Work" },
  { to: "/automation", label: "Automation", icon: "clock", group: "Work" },
  { to: "/files", label: "Files", icon: "folder", group: "Work" },
  { to: "/tools", label: "Tools", icon: "wrench", group: "Agent" },
  { to: "/skills", label: "Skills", icon: "book", group: "Agent" },
  { to: "/roles", label: "Roles", icon: "users", group: "Agent" },
  { to: "/memory", label: "Memory", icon: "brain", group: "Agent" },
  { to: "/knowledge", label: "Knowledge", icon: "database", group: "Agent" },
  { to: "/mcp", label: "MCP", icon: "plug", group: "Agent" },
  { to: "/providers", label: "Providers", icon: "sparkle", group: "Infra" },
  { to: "/servers", label: "Servers", icon: "server", group: "Infra" },
  { to: "/settings", label: "Settings", icon: "gear", group: "Infra" },
  { to: "/system", label: "System", icon: "activity", group: "Infra" },
];

type AuthState = "loading" | "setup" | "login" | "ready";

export function App() {
  const [auth, setAuth] = useState<AuthState>("loading");

  async function check() {
    try {
      const s = await api.get<{ configured: boolean; auth_required: boolean; authorized: boolean }>("/auth/status");
      if (!s.configured) setAuth("setup");
      else if (!s.authorized) setAuth("login");
      else setAuth("ready");
    } catch (e) {
      if (e instanceof ApiError && e.status === 401) setAuth("login");
      else setAuth("setup");
    }
  }
  useEffect(() => {
    check();
  }, []);

  if (auth === "loading")
    return (
      <div className="h-full grid place-items-center">
        <Wordmark size={20} />
      </div>
    );
  if (auth === "setup") return <SetupPage onDone={check} />;
  if (auth === "login")
    return (
      <LoginPage
        onLogin={(tok: string) => {
          setToken(tok);
          check();
        }}
      />
    );

  return (
    <ChatProvider>
      <Shell />
    </ChatProvider>
  );
}

function Shell() {
  const [collapsed, setCollapsed] = useState(false);
  const loc = useLocation();
  const { streaming } = useChat();
  const groups = Array.from(new Set(NAV.map((n) => n.group)));

  return (
    <div className="h-full flex">
      <aside
        className={cls(
          "panel shrink-0 border-r border-[var(--color-border-soft)] flex flex-col",
          collapsed ? "w-14" : "w-60",
        )}
      >
        <div className={cls("h-14 flex items-center shrink-0", collapsed ? "justify-center" : "px-5")}>
          {collapsed ? <StarMark size={16} /> : <Wordmark />}
        </div>
        <nav className="flex-1 overflow-y-auto px-3 pb-3">
          {groups.map((g, gi) => (
            <div key={g} className={gi === 0 ? "" : "mt-6"}>
              {!collapsed && <div className="px-2 mb-2 text-xs leading-4 text-[var(--color-faint)]">{g}</div>}
              <div className="space-y-0.5">
                {NAV.filter((n) => n.group === g).map((n) => (
                  <NavLink
                    key={n.to}
                    to={n.to}
                    end={n.to === "/"}
                    className={({ isActive }) =>
                      cls(
                        "h-8 flex items-center gap-2.5 rounded-lg text-[13px] transition-colors duration-150",
                        collapsed ? "justify-center" : "px-2",
                        isActive
                          ? "bg-[var(--color-surface-2)] text-[var(--color-text)] shadow-[inset_0_1px_0_rgb(255_255_255/0.08)]"
                          : "text-[var(--color-muted)] hover:bg-[rgb(237_237_240/0.05)] hover:text-[var(--color-text)]",
                      )
                    }
                    title={n.to === "/" && streaming ? n.label + " — a chat is running" : n.label}
                  >
                    <span className="relative shrink-0 flex">
                      <Icon name={n.icon} className="w-4 h-4" strokeWidth={1.5} />
                      {n.to === "/" && streaming && collapsed && <span className="live-dot absolute -top-0.5 -right-0.5" />}
                    </span>
                    {!collapsed && <span className="flex-1">{n.label}</span>}
                    {n.to === "/" && streaming && !collapsed && <span className="live-dot shrink-0" title="A chat is running" />}
                  </NavLink>
                ))}
              </div>
            </div>
          ))}
        </nav>
        <UpdatePill collapsed={collapsed} />
        <button
          onClick={() => setCollapsed((c) => !c)}
          className="h-10 shrink-0 border-t border-[var(--color-border-soft)] text-[var(--color-faint)] hover:text-[var(--color-text)] transition-colors duration-150 flex items-center justify-center"
          title={collapsed ? "Expand" : "Collapse"}
        >
          <Icon name={collapsed ? "chevron-right" : "chevron-left"} className="w-4 h-4" strokeWidth={1.5} />
        </button>
      </aside>

      <main className="flex-1 min-w-0 flex flex-col">
        <div key={loc.pathname} className="flex-1 min-h-0 fadein">
          <Routes>
            <Route path="/" element={<ChatPage />} />
            <Route path="/security" element={<SecurityPage />} />
            <Route path="/board" element={<BoardPage />} />
            <Route path="/automation" element={<AutomationPage />} />
            <Route path="/files" element={<FilesPage />} />
            <Route path="/tools" element={<ToolsPage />} />
            <Route path="/skills" element={<SkillsPage />} />
            <Route path="/roles" element={<RolesPage />} />
            <Route path="/memory" element={<MemoryPage />} />
            <Route path="/knowledge" element={<KnowledgePage />} />
            <Route path="/mcp" element={<McpPage />} />
            <Route path="/servers" element={<ServersPage />} />
            <Route path="/providers" element={<ProvidersPage />} />
            <Route path="/settings" element={<SettingsPage />} />
            <Route path="/system" element={<SystemPage />} />
            <Route path="*" element={<Navigate to="/" />} />
          </Routes>
        </div>
      </main>
      <Toasts />
    </div>
  );
}
