import { useCallback, useEffect, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { CalendarDays, Menu } from "lucide-react";
import { api, ApiError, type Me } from "./api";
import { SignIn } from "./components/SignIn";
import { Mark } from "./components/ui";
import { Chat, type ChatMessage } from "./components/Chat";
import { RightRail } from "./components/RightRail";
import { Sidebar } from "./components/Sidebar";
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
  // Clean the ?auth_error= left over from a failed attempt.
  if (window.location.search) window.history.replaceState(null, "", "/");
  return <Workspace me={me.data!} />;
}

function Workspace({ me }: { me: Me }) {
  const status = useQuery({ queryKey: ["status"], queryFn: api.status, refetchInterval: 60_000 });
  const convs = useQuery({ queryKey: ["conversations"], queryFn: api.conversations });

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
  const title = convs.data?.find((c) => c.id === sessionId)?.title ?? (messages.length ? messages[0].text.slice(0, 60) : "New conversation");
  const aiHint = s && !s.ai_configured
    ? s.ai_error ?? "No AI model is configured. Set LLM_API_KEY and LLM_MODEL in .env, then restart the server."
    : undefined;

  return (
    <div className="flex h-full overflow-hidden">
      {/* Sidebar: fixed on large screens, a drawer below that. */}
      <div className="hidden lg:flex">
        <Sidebar activeId={sessionId} onOpen={open} onNew={startNew} status={s} me={me} />
      </div>
      {navOpen && (
        <Drawer side="left" onClose={() => setNavOpen(false)}>
          <Sidebar activeId={sessionId} onOpen={open} onNew={startNew} status={s} me={me} onClose={() => setNavOpen(false)} />
        </Drawer>
      )}

      <main className="flex min-w-0 flex-1 flex-col bg-paper">
        <header className="flex h-14 shrink-0 items-center gap-2 border-b border-line/70 px-3 sm:px-5">
          <IconButton label="Open conversations" onClick={() => setNavOpen(true)} className="lg:hidden">
            <Menu size={18} />
          </IconButton>
          <h1 className="min-w-0 flex-1 truncate text-[15px] font-medium text-ink">{title}</h1>
          {s?.demo_mode && (
            <span className="rounded-full bg-clay-soft px-2.5 py-1 text-[12px] font-medium text-clay">Demo data</span>
          )}
          <IconButton label="Open today panel" onClick={() => setRailOpen(true)} className="xl:hidden">
            <CalendarDays size={18} />
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
        />
      </main>

      <div className="hidden xl:flex">
        <RightRail requireApproval={s?.require_approval ?? true} />
      </div>
      {railOpen && (
        <Drawer side="right" onClose={() => setRailOpen(false)}>
          <RightRail requireApproval={s?.require_approval ?? true} onClose={() => setRailOpen(false)} />
        </Drawer>
      )}
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
