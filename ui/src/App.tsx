import { useCallback, useEffect, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { CalendarDays, Menu } from "lucide-react";
import { api, ApiError, type Me } from "./api";
import { SignIn } from "./components/SignIn";
import { Mark } from "./components/ui";
import { Chat, type ChatMessage } from "./components/Chat";
import { RightRail } from "./components/RightRail";
import { Sidebar } from "./components/Sidebar";
import { Activity } from "./components/Activity";
import { IconButton } from "./components/ui";

const newId = () => crypto.randomUUID();

function readSession(): string {
  try {
    return sessionStorage.getItem("session_id") || newId();
  } catch {
    return newId();
  }
}

function saveSession(id: string) {
  try {
    sessionStorage.setItem("session_id", id);
  } catch {
    /* per-tab convenience only */
  }
}

// App shows the sign-in screen until /api/me confirms a session.
export default function App() {
  const qc = useQueryClient();
  const me = useQuery({ queryKey: ["me"], queryFn: api.me, retry: false, staleTime: Infinity });

  // Any 401 (expired or revoked session) drops back to sign-in.
  useEffect(() => {
    const onOut = () => {
      qc.removeQueries({ predicate: (q) => q.queryKey[0] !== "me" });
      qc.setQueryData(["me"], null);
    };
    window.addEventListener("auth:signed-out", onOut);
    return () => window.removeEventListener("auth:signed-out", onOut);
  }, [qc]);

  if (me.isLoading) {
    return (
      <div className="flex h-full items-center justify-center bg-paper">
        <span className="animate-pulse">
          <Mark size={36} />
        </span>
      </div>
    );
  }
  const signedOut = !me.data || (me.error instanceof ApiError && me.error.status === 401);
  if (signedOut) return <SignIn />;
  if (me.error) {
    return (
      <div className="flex h-full items-center justify-center bg-paper px-6 text-center text-ink-2">
        Can't reach the server right now. Is it running? ({(me.error as Error).message})
      </div>
    );
  }
  return <Workspace me={me.data!} />;
}

const FLASH: Record<string, { tone: "ok" | "err"; text: string }> = {
  "connected=slack": { tone: "ok", text: "Slack connected. Try: “What did I miss in Slack today?”" },
  "connect_error=slack_denied": { tone: "err", text: "Slack connection was cancelled." },
  "connect_error=slack_expired": { tone: "err", text: "That Slack link expired. Please try connecting again." },
  "connect_error=slack_failed": { tone: "err", text: "Couldn't connect Slack. Please try again." },
};

// Reads a one-time notice from the URL (after OAuth redirects) and cleans it.
function useFlash() {
  const [flash, setFlash] = useState<{ tone: "ok" | "err"; text: string } | null>(
    () => FLASH[window.location.search.replace(/^\?/, "")] ?? null,
  );
  useEffect(() => {
    if (window.location.search) window.history.replaceState(null, "", "/");
    if (flash) {
      const t = setTimeout(() => setFlash(null), 6000);
      return () => clearTimeout(t);
    }
  }, [flash]);
  return flash;
}

function Workspace({ me }: { me: Me }) {
  const flash = useFlash();
  const status = useQuery({ queryKey: ["status"], queryFn: api.status, refetchInterval: 60_000 });
  const convs = useQuery({ queryKey: ["conversations"], queryFn: api.conversations });
  const actions = useQuery({ queryKey: ["actions"], queryFn: api.actions, refetchInterval: 30_000 });
  const pendingCount = (actions.data ?? []).filter((a) => a.status === "pending").length;
  const [activityOpen, setActivityOpen] = useState(false);

  const [sessionId, setSessionId] = useState(readSession);
  const [messages, setMessages] = useState<ChatMessage[]>([]);
  const [navOpen, setNavOpen] = useState(false);
  const [railOpen, setRailOpen] = useState(false);
  const [restored, setRestored] = useState(false);

  const open = useCallback(async (id: string) => {
    setNavOpen(false);
    try {
      const c = await api.conversation(id);
      const msgs: ChatMessage[] = [];
      for (const t of c.turns ?? []) {
        msgs.push({ role: "user", text: t.user_text });
        msgs.push(
          t.error
            ? { role: "error", text: t.error }
            : { role: "assistant", text: t.reply || "(no reply)", tools: t.tool_calls ?? [] },
        );
      }
      setSessionId(id);
      saveSession(id);
      setMessages(msgs);
    } catch {
      startNew();
    }
  }, []);

  const startNew = () => {
    const id = newId();
    setSessionId(id);
    saveSession(id);
    setMessages([]);
    setNavOpen(false);
  };

  // After a reload, reopen this tab's conversation if it was saved.
  useEffect(() => {
    if (restored || !convs.data) return;
    setRestored(true);
    if (convs.data.some((c) => c.id === sessionId)) open(sessionId);
  }, [convs.data, restored, sessionId, open]);

  const s = status.data;
  const undoSeconds = s?.undo_seconds ?? 0;
  const openActivity = () => {
    setNavOpen(false);
    setRailOpen(false);
    setActivityOpen(true);
  };
  const title = convs.data?.find((c) => c.id === sessionId)?.title ?? (messages.length ? messages[0].text.slice(0, 60) : "New conversation");
  const aiHint = s && !s.ai_configured
    ? s.ai_error ?? "No AI model is configured. Set LLM_API_KEY and LLM_MODEL in .env, then restart the server."
    : undefined;

  return (
    <div className="flex h-full overflow-hidden">
      {/* Sidebar: fixed on large screens, a drawer below that. */}
      <div className="hidden lg:flex">
        <Sidebar activeId={sessionId} onOpen={open} onNew={startNew} status={s} me={me} onOpenActivity={openActivity} />
      </div>
      {navOpen && (
        <Drawer side="left" onClose={() => setNavOpen(false)}>
          <Sidebar activeId={sessionId} onOpen={open} onNew={startNew} status={s} me={me} onOpenActivity={openActivity} onClose={() => setNavOpen(false)} />
        </Drawer>
      )}

      <main className="flex min-w-0 flex-1 flex-col bg-paper">
        <header className="flex h-14 shrink-0 items-center gap-2 border-b border-line/70 px-3 sm:px-5">
          <IconButton label="Open conversations" onClick={() => setNavOpen(true)} className="lg:hidden">
            <Menu size={18} />
          </IconButton>
          <h1 className="min-w-0 flex-1 truncate text-[15px] font-medium text-ink">{title}</h1>
          {flash && (
            <span
              role="status"
              className={`hidden truncate rounded-full px-3 py-1 text-[12.5px] font-medium sm:inline ${
                flash.tone === "ok" ? "bg-forest-soft text-forest" : "bg-danger-soft text-danger"
              }`}
            >
              {flash.text}
            </span>
          )}
          {s?.demo_mode && (
            <span className="rounded-full bg-clay-soft px-2.5 py-1 text-[12px] font-medium text-clay">Demo data</span>
          )}
          <IconButton
            label={pendingCount ? `Open today panel (${pendingCount} waiting for approval)` : "Open today panel"}
            onClick={() => setRailOpen(true)}
            className="relative xl:hidden"
          >
            <CalendarDays size={18} />
            {pendingCount > 0 && (
              <span className="absolute -top-0.5 -right-0.5 inline-flex min-w-[17px] items-center justify-center rounded-full bg-amber px-1 text-[10.5px] leading-[17px] font-semibold text-paper">
                {pendingCount}
              </span>
            )}
          </IconButton>
        </header>

        <Chat
          key={sessionId}
          sessionId={sessionId}
          messages={messages}
          setMessages={setMessages}
          aiReady={!!s?.ai_configured}
          aiHint={aiHint}
          requireApproval={s?.require_approval ?? true}
          undoSeconds={undoSeconds}
        />
      </main>

      <div className="hidden xl:flex">
        <RightRail requireApproval={s?.require_approval ?? true} undoSeconds={undoSeconds} onOpenActivity={openActivity} />
      </div>
      {railOpen && (
        <Drawer side="right" onClose={() => setRailOpen(false)}>
          <RightRail
            requireApproval={s?.require_approval ?? true}
            undoSeconds={undoSeconds}
            onOpenActivity={openActivity}
            onClose={() => setRailOpen(false)}
          />
        </Drawer>
      )}
      {activityOpen && <Activity onClose={() => setActivityOpen(false)} />}
    </div>
  );
}

function Drawer({ side, onClose, children }: { side: "left" | "right"; onClose: () => void; children: React.ReactNode }) {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);
  return (
    <div className="fixed inset-0 z-40 flex" role="dialog" aria-modal="true">
      <button aria-label="Close" className="absolute inset-0 bg-ink/25" onClick={onClose} />
      <div className={`relative z-10 h-full shadow-xl ${side === "right" ? "ml-auto" : ""}`}>{children}</div>
    </div>
  );
}
