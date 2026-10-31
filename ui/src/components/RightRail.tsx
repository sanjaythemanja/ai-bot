import { useState } from "react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  CalendarClock,
  CalendarPlus,
  CalendarX,
  Check,
  ChevronDown,
  FileX,
  Hash,
  Mail,
  MapPin,
  RefreshCw,
  Send,
  Sparkles,
  Video,
  X,
} from "lucide-react";
import { api, type Action, type CalEvent, type Email } from "../api";
import { cleanText, fmtRelative, fmtTime, senderName } from "../lib/format";
import { ActionSnapshot } from "./Preview";
import { Avatar, Button, IconButton, Pill, SectionHeader, Thinking } from "./ui";

export function RightRail({ requireApproval, onClose }: { requireApproval: boolean; onClose?: () => void }) {
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
                  <ActionCard key={a.id} a={a} />
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
            <Inbox emails={overview.data?.emails ?? []} />
          )}
        </section>

        {recent.length > 0 && <RecentActions actions={recent} title={requireApproval ? "Recently resolved" : "Activity"} />}
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

function Inbox({ emails }: { emails: Email[] }) {
  if (emails.length === 0) return <p className="text-sm text-ink-3">Inbox is quiet.</p>;
  return (
    <ul className="space-y-0.5">
      {emails.slice(0, 8).map((m) => {
        const name = senderName(m.from);
        return (
          <li key={m.id} className="flex gap-3 rounded-xl px-2 py-2 hover:bg-sunken/60">
            <Avatar name={name} />
            <div className="min-w-0 flex-1">
              <div className="flex items-baseline justify-between gap-2">
                <span className="truncate text-[13px] font-semibold text-ink">{name}</span>
                <span className="shrink-0 text-[11px] text-ink-3">{fmtRelative(m.date)}</span>
              </div>
              <div className="truncate text-[13px] text-ink">{cleanText(m.subject) || "(no subject)"}</div>
              <div className="line-clamp-1 text-[12px] text-ink-3">{cleanText(m.snippet)}</div>
            </div>
          </li>
        );
      })}
    </ul>
  );
}

const KIND: Record<string, { icon: typeof Mail; label: string }> = {
  send_email: { icon: Mail, label: "Send email" },
  send_draft: { icon: Send, label: "Send draft" },
  delete_draft: { icon: FileX, label: "Delete draft" },
  create_event: { icon: CalendarPlus, label: "New event" },
  update_event: { icon: CalendarClock, label: "Change event" },
  delete_event: { icon: CalendarX, label: "Cancel event" },
  slack_post: { icon: Hash, label: "Slack message" },
};

function ActionCard({ a }: { a: Action }) {
  const qc = useQueryClient();
  const [open, setOpen] = useState(false);
  const resolve = useMutation({
    mutationFn: (approve: boolean) => (approve ? api.approve(a.id) : api.reject(a.id)),
    onSettled: () => {
      qc.invalidateQueries({ queryKey: ["actions"] });
      qc.invalidateQueries({ queryKey: ["overview"] });
    },
  });
  const k = KIND[a.kind] ?? { icon: Mail, label: a.kind };
  const Icon = k.icon;

  return (
    <div className="rounded-2xl border border-amber/30 bg-surface p-3.5 rise">
      <div className="mb-2.5 flex items-center gap-2">
        <span className="inline-flex size-6 shrink-0 items-center justify-center rounded-md bg-amber-soft text-amber">
          <Icon size={13} />
        </span>
        <span className="text-[13px] font-semibold text-ink">{k.label}</span>
        <span className="ml-auto text-[11px] text-ink-3">Preview</span>
      </div>
      <ActionSnapshot a={a} />
      {open && (
        <pre className="mt-3 max-h-56 overflow-auto rounded-lg bg-sunken p-2.5 text-[11.5px] leading-relaxed whitespace-pre-wrap text-ink-2">
          {JSON.stringify(a.payload, null, 2)}
        </pre>
      )}
      {resolve.error && <p className="mt-2 text-[12px] text-danger">{(resolve.error as Error).message}</p>}
      <div className="mt-3 flex items-center gap-2">
        <Button size="sm" variant="primary" onClick={() => resolve.mutate(true)} disabled={resolve.isPending}>
          <Check size={14} /> Approve
        </Button>
        <Button size="sm" variant="ghost" onClick={() => resolve.mutate(false)} disabled={resolve.isPending}>
          Reject
        </Button>
        <button
          onClick={() => setOpen((o) => !o)}
          className="ml-auto inline-flex items-center gap-0.5 text-[12px] text-ink-3 hover:text-ink-2"
          aria-expanded={open}
        >
          Raw
          <ChevronDown size={13} className={`transition-transform ${open ? "rotate-180" : ""}`} />
        </button>
      </div>
    </div>
  );
}

function RecentActions({ actions, title }: { actions: Action[]; title: string }) {
  return (
    <section>
      <SectionHeader title={title} />
      <ul className="space-y-1.5">
        {actions.map((a) => (
          <li key={a.id} className="flex items-start gap-2 text-[13px]">
            <span className="mt-0.5">
              {a.status === "executed" ? (
                <Pill tone="forest">Done</Pill>
              ) : a.status === "failed" ? (
                <Pill tone="danger">Failed</Pill>
              ) : (
                <Pill>Rejected</Pill>
              )}
            </span>
            <span className="min-w-0 flex-1 text-ink-2" title={a.error || a.result}>
              <span className="line-clamp-2">{a.summary}</span>
              {a.error && <span className="line-clamp-2 text-[12px] text-danger">{a.error}</span>}
            </span>
          </li>
        ))}
      </ul>
    </section>
  );
}
