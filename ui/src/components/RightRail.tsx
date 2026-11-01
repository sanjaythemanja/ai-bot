import { useState } from "react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { MapPin, MoreHorizontal, RefreshCw, Sparkles, Video, X } from "lucide-react";
import { api, type Action, type CalEvent, type Email, type Verdict } from "../api";
import { cleanText, fmtRelative, fmtTime, senderName } from "../lib/format";
import { ActionCard, reasonText } from "./ActionCard";
import { Avatar, Button, IconButton, Pill, SectionHeader, Thinking } from "./ui";

export function RightRail({
  requireApproval,
  undoSeconds,
  onOpenActivity,
  onClose,
}: {
  requireApproval: boolean;
  undoSeconds: number;
  onOpenActivity: () => void;
  onClose?: () => void;
}) {
  const overview = useQuery({ queryKey: ["overview"], queryFn: api.overview, refetchInterval: 120_000 });
  const actions = useQuery({ queryKey: ["actions"], queryFn: api.actions, refetchInterval: 30_000 });

  const pending = (actions.data ?? []).filter((a) => a.status === "pending");
  const recent = (actions.data ?? []).filter((a) => a.status !== "pending").slice(0, 6);

  return (
    <aside className="flex h-full w-[352px] shrink-0 flex-col overflow-y-auto border-l border-line bg-paper">
      <div className="flex items-center justify-between px-5 pt-5 pb-1">
        <h2 className="font-serif text-[22px] font-medium tracking-tight">Today</h2>
        <div className="flex items-center">
          <IconButton
            label="Refresh"
            onClick={() => {
              overview.refetch();
              actions.refetch();
            }}
          >
            <RefreshCw size={15} className={overview.isFetching ? "animate-spin" : ""} />
          </IconButton>
          {onClose && (
            <IconButton label="Close panel" onClick={onClose} className="xl:hidden">
              <X size={16} />
            </IconButton>
          )}
        </div>
      </div>

      <div className="space-y-7 px-5 pt-3 pb-8">
        <BriefingCard />

        {(requireApproval || pending.length > 0) && (
          <section>
            <SectionHeader title="Waiting for you" count={pending.length} />
            {pending.length === 0 ? (
              <p className="text-sm text-ink-3">Nothing to approve.</p>
            ) : (
              <div className="space-y-2.5">
                {pending.map((a) => (
                  <ActionCard key={a.id} a={a} undoSeconds={undoSeconds} />
                ))}
              </div>
            )}
          </section>
        )}

        <section>
          <SectionHeader title="Schedule" />
          {overview.isLoading ? (
            <p className="text-sm text-ink-3">Loading…</p>
          ) : overview.data?.errors?.calendar ? (
            <p className="text-sm text-danger">{overview.data.errors.calendar}</p>
          ) : (
            <Agenda events={overview.data?.events ?? []} />
          )}
        </section>

        <section>
          <SectionHeader title="Unread" count={overview.data?.emails?.length || undefined} />
          {overview.data?.errors?.gmail ? (
            <p className="text-sm text-danger">{overview.data.errors.gmail}</p>
          ) : (
            <Inbox emails={overview.data?.emails ?? []} priority={overview.data?.priority ?? {}} />
          )}
        </section>

        <RecentActions actions={recent} title={requireApproval ? "Recently resolved" : "Activity"} onOpenActivity={onOpenActivity} />
      </div>
    </aside>
  );
}

function BriefingCard() {
  const brief = useMutation({ mutationFn: api.briefing });
  return (
    <div className="rounded-2xl border border-line bg-surface p-4">
      <div className="flex items-start justify-between gap-3">
        <div>
          <div className="flex items-center gap-1.5 text-[13px] font-semibold text-clay">
            <Sparkles size={14} />
            Daily brief
          </div>
          <p className="mt-1 text-[13px] text-ink-2">Priorities, schedule and what needs a reply.</p>
        </div>
        <Button size="sm" variant={brief.data ? "ghost" : "primary"} onClick={() => brief.mutate()} disabled={brief.isPending}>
          {brief.data ? "Refresh" : "Brief me"}
        </Button>
      </div>
      {brief.isPending && (
        <div className="mt-3">
          <Thinking label="Reading your inbox and calendar" />
        </div>
      )}
      {brief.error && <p className="mt-3 text-[13px] text-danger">{(brief.error as Error).message}</p>}
      {brief.data && !brief.isPending && (
        <div className="prose-reply mt-3 border-t border-line pt-3 text-[14px]">
          <ReactMarkdown remarkPlugins={[remarkGfm]}>{brief.data.markdown}</ReactMarkdown>
        </div>
      )}
    </div>
  );
}

function Agenda({ events }: { events: CalEvent[] }) {
  if (events.length === 0) return <p className="text-sm text-ink-3">A clear day.</p>;
  const now = Date.now();
  const sorted = [...events].sort((a, b) => +new Date(a.start) - +new Date(b.start));
  const nextIdx = sorted.findIndex((e) => +new Date(e.end) > now);

  return (
    <ol className="relative space-y-1">
      {sorted.map((e, i) => {
        const past = +new Date(e.end) < now;
        const live = +new Date(e.start) <= now && !past;
        return (
          <li key={e.id}>
            {i === nextIdx && !live && <NowLine />}
            <div
              className={`flex gap-3 rounded-xl px-2.5 py-2 transition-colors ${
                live ? "bg-clay-soft/60" : "hover:bg-sunken/60"
              } ${past ? "opacity-55" : ""}`}
            >
              <div className="w-[62px] shrink-0 pt-px text-[13px] tabular-nums text-ink-3">
                {e.all_day ? "All day" : fmtTime(e.start)}
              </div>
              <div className={`min-w-0 flex-1 border-l-2 pl-3 ${live ? "border-clay" : "border-forest/50"}`}>
                <div className="truncate text-[14px] font-medium text-ink">{e.title || "(no title)"}</div>
                <div className="mt-0.5 flex flex-wrap items-center gap-x-2.5 gap-y-0.5 text-[12px] text-ink-3">
                  {!e.all_day && <span>until {fmtTime(e.end)}</span>}
                  {e.location && (
                    <span className="inline-flex min-w-0 items-center gap-1">
                      <MapPin size={11} />
                      <span className="truncate">{e.location}</span>
                    </span>
                  )}
                  {e.meet_link && (
                    <a href={e.meet_link} target="_blank" rel="noreferrer noopener" className="inline-flex items-center gap-1 text-forest hover:underline">
                      <Video size={11} /> Join
                    </a>
                  )}
                  {live && <span className="font-medium text-clay">Now</span>}
                </div>
              </div>
            </div>
          </li>
        );
      })}
      {nextIdx === -1 && <NowLine />}
    </ol>
  );
}

function NowLine() {
  return (
    <div className="flex items-center gap-2 py-1 pl-2.5" aria-label="Current time">
      <span className="text-[11px] font-semibold text-clay tabular-nums">{fmtTime(new Date().toISOString())}</span>
      <span className="h-px flex-1 bg-clay/50" />
    </div>
  );
}

const RANK = { important: 0, normal: 1, low: 2 } as const;

function Inbox({ emails, priority }: { emails: Email[]; priority: Record<string, Verdict> }) {
  if (emails.length === 0) return <p className="text-sm text-ink-3">Inbox is quiet.</p>;
  const sorted = [...emails].sort((a, b) => RANK[priority[a.id]?.priority ?? "normal"] - RANK[priority[b.id]?.priority ?? "normal"]);
  return (
    <ul className="space-y-0.5">
      {sorted.slice(0, 8).map((m) => (
        <InboxRow key={m.id} m={m} v={priority[m.id]} />
      ))}
    </ul>
  );
}

// Bare address of "Name <a@b.com>".
const addrOf = (from: string) => (from.match(/<([^>]+)>/)?.[1] ?? from).trim().toLowerCase();

function InboxRow({ m, v }: { m: Email; v?: Verdict }) {
  const qc = useQueryClient();
  const [menu, setMenu] = useState(false);
  const name = senderName(m.from);
  const addr = addrOf(m.from);
  const rule = useMutation({
    mutationFn: (p: "important" | "low") => api.callTool("add_sorting_rule", { match: addr, priority: p }),
    onSuccess: () => {
      setMenu(false);
      qc.invalidateQueries({ queryKey: ["overview"] });
    },
  });
  const dot = v?.priority === "important" ? "bg-clay" : v?.priority === "low" ? "bg-line-strong" : "bg-forest/50";
  return (
    <li className={`group relative flex gap-3 rounded-xl px-2 py-2 hover:bg-sunken/60 ${v?.priority === "low" ? "opacity-65" : ""}`}>
      <Avatar name={name} />
      <div className="min-w-0 flex-1">
        <div className={`flex items-baseline justify-between gap-2 ${v ? "pr-7 xl:pr-0 xl:group-hover:pr-7" : ""}`}>
          <span className="truncate text-[13px] font-semibold text-ink">{name}</span>
          <span className="shrink-0 text-[11px] text-ink-3">{fmtRelative(m.date)}</span>
        </div>
        <div className="truncate text-[13px] text-ink">{cleanText(m.subject) || "(no subject)"}</div>
        {v ? (
          <div className="mt-0.5 flex items-start gap-1.5 text-[12px] text-ink-3" title={v.reasons.join("\n")}>
            <span className={`mt-[5px] size-1.5 shrink-0 rounded-full ${dot}`} aria-hidden />
            <span className="line-clamp-2">
              <span className="font-medium text-ink-2">{v.priority === "important" ? "Important" : v.priority === "low" ? "Low" : "Normal"}:</span>{" "}
              {v.reasons[0]}
            </span>
          </div>
        ) : (
          <div className="line-clamp-1 text-[12px] text-ink-3">{cleanText(m.snippet)}</div>
        )}
      </div>
      {v && (
        <button
          onClick={() => setMenu((o) => !o)}
          aria-label={`Change priority for ${name}`}
          aria-expanded={menu}
          className="absolute top-1.5 right-1.5 inline-flex size-6 items-center justify-center rounded-md bg-paper text-ink-3 opacity-0 transition-opacity group-hover:opacity-100 focus:opacity-100 hover:text-ink max-xl:opacity-100"
        >
          <MoreHorizontal size={14} />
        </button>
      )}
      {menu && (
        <div className="absolute top-8 right-1.5 z-10 w-56 rounded-xl border border-line bg-surface p-1 text-[13px] shadow-lg" role="menu">
          <div className="px-2.5 pt-1.5 pb-1 text-[11px] text-ink-3">Always treat mail from {addr} as…</div>
          {(["important", "low"] as const).map((p) => (
            <button
              key={p}
              role="menuitem"
              disabled={rule.isPending}
              onClick={() => rule.mutate(p)}
              className="block w-full rounded-lg px-2.5 py-1.5 text-left text-ink hover:bg-sunken"
            >
              {p === "important" ? "Important" : "Low priority"}
            </button>
          ))}
          {rule.error && <p className="px-2.5 py-1 text-[12px] text-danger">{(rule.error as Error).message}</p>}
        </div>
      )}
    </li>
  );
}

function RecentActions({ actions, title, onOpenActivity }: { actions: Action[]; title: string; onOpenActivity: () => void }) {
  return (
    <section>
      <SectionHeader
        title={title}
        action={
          <button onClick={onOpenActivity} className="text-[12px] font-medium text-forest hover:underline">
            See all
          </button>
        }
      />
      {actions.length === 0 ? (
        <p className="text-sm text-ink-3">Nothing yet. Everything the assistant does will be listed here.</p>
      ) : (
        <ul className="space-y-1.5">
          {actions.map((a) => (
            <li key={a.id} className="flex items-start gap-2 text-[13px]">
              <span className="mt-0.5">
                <StatusPill status={a.status} />
              </span>
              <span className="min-w-0 flex-1 text-ink-2" title={a.error || a.result}>
                <span className="line-clamp-2">{a.summary}</span>
                {a.error && <span className="line-clamp-2 text-[12px] text-danger">{a.error}</span>}
                {reasonText(a.reason) && <span className="line-clamp-1 text-[12px] text-ink-3">{reasonText(a.reason)}</span>}
              </span>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

export function StatusPill({ status }: { status: Action["status"] }) {
  if (status === "executed") return <Pill tone="forest">Done</Pill>;
  if (status === "failed") return <Pill tone="danger">Failed</Pill>;
  if (status === "pending") return <Pill tone="amber">Waiting</Pill>;
  return <Pill>Rejected</Pill>;
}
