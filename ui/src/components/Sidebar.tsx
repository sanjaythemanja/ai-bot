import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Hash, LogOut, Monitor, Moon, PenSquare, Sun, Trash2, X } from "lucide-react";
import { api, type ConversationSummary, type Me, type Status } from "../api";
import { dayBucket } from "../lib/format";
import { Avatar, IconButton, Mark } from "./ui";

type Theme = "system" | "light" | "dark";

function readTheme(): Theme {
  try {
    return (localStorage.getItem("theme") as Theme) || "system";
  } catch {
    return "system";
  }
}

function applyTheme(t: Theme) {
  if (t === "system") document.documentElement.removeAttribute("data-theme");
  else document.documentElement.setAttribute("data-theme", t);
  try {
    localStorage.setItem("theme", t);
  } catch {
    /* storage may be blocked; the theme still applies for this visit */
  }
}

applyTheme(readTheme());

export function Sidebar({
  activeId,
  onOpen,
  onNew,
  status,
  me,
  onClose,
}: {
  activeId: string;
  onOpen: (id: string) => void;
  onNew: () => void;
  status?: Status;
  me: Me;
  onClose?: () => void;
}) {
  const qc = useQueryClient();
  const convs = useQuery({ queryKey: ["conversations"], queryFn: api.conversations });
  const [armed, setArmed] = useState<string | null>(null);
  const [theme, setTheme] = useState<Theme>(readTheme);

  const del = useMutation({
    mutationFn: api.deleteConversation,
    onSuccess: (_d, id) => {
      qc.invalidateQueries({ queryKey: ["conversations"] });
      if (id === activeId) onNew();
    },
  });

  const groups = new Map<string, ConversationSummary[]>();
  for (const c of convs.data ?? []) {
    const b = dayBucket(c.updated_at);
    groups.set(b, [...(groups.get(b) ?? []), c]);
  }

  const cycleTheme = () => {
    const next: Theme = theme === "system" ? "light" : theme === "light" ? "dark" : "system";
    setTheme(next);
    applyTheme(next);
  };
  const ThemeIcon = theme === "light" ? Sun : theme === "dark" ? Moon : Monitor;

  return (
    <aside className="flex h-full w-[264px] shrink-0 flex-col border-r border-line bg-paper">
      <div className="flex items-center justify-between px-4 pt-4 pb-3">
        <div className="flex items-center gap-2.5">
          <Mark />
          <div className="leading-tight">
            <div className="font-serif text-[17px] font-semibold tracking-tight">Exec Assistant</div>
            <div className="text-[12px] text-ink-3">Mail &amp; calendar</div>
          </div>
        </div>
        {onClose && (
          <IconButton label="Close sidebar" onClick={onClose} className="lg:hidden">
            <X size={16} />
          </IconButton>
        )}
      </div>

      <div className="px-3">
        <button
          onClick={onNew}
          className="flex h-9 w-full items-center gap-2 rounded-lg border border-line bg-surface px-3 text-sm font-medium text-ink transition-colors hover:border-line-strong"
        >
          <PenSquare size={15} className="text-forest" />
          New conversation
        </button>
      </div>

      <nav className="mt-4 flex-1 overflow-y-auto px-2 pb-4" aria-label="Conversations">
        {convs.isLoading && <p className="px-2 text-sm text-ink-3">Loading…</p>}
        {convs.data?.length === 0 && (
          <p className="px-2 text-sm leading-relaxed text-ink-3">Your conversations will show up here.</p>
        )}
        {["Today", "Yesterday", "This week", "Earlier"].map((label) =>
          groups.has(label) ? (
            <div key={label} className="mb-3">
              <div className="px-2 pb-1 text-[11px] font-semibold tracking-wide text-ink-3 uppercase">{label}</div>
              {groups.get(label)!.map((c) => (
                <div
                  key={c.id}
                  className={`group flex items-center rounded-lg transition-colors ${
                    c.id === activeId ? "bg-sunken" : "hover:bg-sunken/70"
                  }`}
                >
                  <button
                    onClick={() => onOpen(c.id)}
                    className={`min-w-0 flex-1 truncate px-2 py-1.5 text-left text-[14px] ${
                      c.id === activeId ? "font-medium text-ink" : "text-ink-2"
                    }`}
                    title={c.title}
                  >
                    {c.title}
                  </button>
                  <button
                    onClick={() => (armed === c.id ? (del.mutate(c.id), setArmed(null)) : setArmed(c.id))}
                    onBlur={() => setArmed(null)}
                    aria-label={armed === c.id ? "Click again to delete" : `Delete ${c.title}`}
                    title={armed === c.id ? "Click again to delete" : "Delete"}
                    className={`mr-1 flex h-7 items-center gap-1 rounded-md px-1.5 text-[12px] transition-all ${
                      armed === c.id
                        ? "bg-danger-soft text-danger opacity-100"
                        : "text-ink-3 opacity-0 group-hover:opacity-100 focus:opacity-100 hover:text-danger"
                    }`}
                  >
                    <Trash2 size={13} />
                    {armed === c.id && "Delete?"}
                  </button>
                </div>
              ))}
            </div>
          ) : null,
        )}
      </nav>

      <div className="border-t border-line px-3 py-3">
        <div className="flex items-center gap-2.5 px-1">
          <Avatar name={me.name || me.email} size={30} />
          <div className="min-w-0 flex-1 leading-tight">
            <div className="truncate text-[13px] font-medium text-ink">{me.name || me.email.split("@")[0]}</div>
            <div className="truncate text-[12px] text-ink-3">{me.email}</div>
          </div>
          <IconButton label={`Theme: ${theme}`} onClick={cycleTheme} className="size-7">
            <ThemeIcon size={14} />
          </IconButton>
        </div>
        <SlackRow />
        <div className="mt-2.5 flex items-center gap-3 px-1 text-[12px] text-ink-3">
          <StatusRow ok={!!status?.ai_configured} label={status?.ai_configured ? status.model : "No AI model"} />
          {status?.demo_mode && <span className="rounded-full bg-clay-soft px-1.5 text-clay">Demo</span>}
        </div>
        <button
          onClick={async () => {
            try {
              await api.logout();
            } finally {
              try {
                sessionStorage.removeItem("session_id");
              } catch {
                /* ignore */
              }
              window.location.replace("/");
            }
          }}
          className="mt-2 flex h-8 w-full items-center gap-2 rounded-lg px-2 text-[13px] text-ink-2 transition-colors hover:bg-sunken hover:text-danger"
        >
          <LogOut size={15} />
          Log out
        </button>
      </div>
    </aside>
  );
}

function StatusRow({ ok, label }: { ok: boolean; label: string }) {
  return (
    <div className="flex min-w-0 items-center gap-1.5">
      <span className={`size-1.5 shrink-0 rounded-full ${ok ? "bg-forest" : "bg-line-strong"}`} />
      <span className="truncate">{label}</span>
    </div>
  );
}

// Slack connection: shown only when the server has a Slack app configured.
function SlackRow() {
  const qc = useQueryClient();
  const conns = useQuery({ queryKey: ["connections"], queryFn: api.connections });
  const slack = conns.data?.find((c) => c.provider === "slack");
  const disconnect = useMutation({
    mutationFn: () => api.disconnect("slack"),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["connections"] }),
  });
  if (!slack?.configured) return null;
  return (
    <div className="mt-2.5 flex items-center gap-2 rounded-lg border border-line bg-surface px-2.5 py-2">
      <Hash size={15} className={slack.connected ? "text-forest" : "text-ink-3"} />
      <div className="min-w-0 flex-1 leading-tight">
        <div className="text-[13px] font-medium text-ink">Slack</div>
        <div className="truncate text-[11.5px] text-ink-3">{slack.connected ? slack.label || "Connected" : "Not connected"}</div>
      </div>
      {slack.connected ? (
        slack.label === "Demo workspace" ? null : (
          <button
            onClick={() => disconnect.mutate()}
            disabled={disconnect.isPending}
            className="text-[12px] text-ink-3 hover:text-danger"
          >
            Disconnect
          </button>
        )
      ) : (
        <a href="/auth/slack/start" className="rounded-md bg-forest px-2 py-1 text-[12px] font-medium text-on-forest hover:bg-forest-hover">
          Connect
        </a>
      )}
    </div>
  );
}
