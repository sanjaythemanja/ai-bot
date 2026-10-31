// Mini snapshots of what approving an action will produce: an email as it
// will be sent, a calendar entry as it will appear, or a before/after view
// of a meeting change.
import { useState } from "react";
import { ArrowDown, MapPin, Reply, Users } from "lucide-react";
import type { Action } from "../api";
import { Avatar } from "./ui";

type Mail = {
  to?: string[];
  cc?: string[];
  bcc?: string[];
  subject?: string;
  body?: string;
  reply_to_id?: string;
  in_reply_to?: string;
};

type Ev = {
  title?: string;
  description?: string;
  location?: string;
  start: string;
  end: string;
  all_day?: boolean;
  attendees?: string[];
};

type Field = "title" | "time" | "location" | "description";

export function ActionSnapshot({ a }: { a: Action }) {
  const p = a.payload as Record<string, any>;
  switch (a.kind) {
    case "send_email":
      return <EmailSnapshot mail={p as Mail} />;
    case "send_draft":
      return p.message ? <EmailSnapshot mail={p.message} /> : <Plain text={a.summary} />;
    case "delete_draft":
      return p.message ? <EmailSnapshot mail={p.message} removing /> : <Plain text={a.summary} />;
    case "create_event":
      return <EventSnapshot ev={p as Ev} />;
    case "update_event":
      return p.before ? <EventDiff before={p.before} update={p.update ?? {}} /> : <Plain text={a.summary} />;
    case "delete_event":
      return p.event ? <EventSnapshot ev={p.event} removing /> : <Plain text={a.summary} />;
    default:
      return <Plain text={a.summary} />;
  }
}

function Plain({ text }: { text: string }) {
  return <p className="text-[13px] text-ink">{text}</p>;
}

// ---------- Email ----------

function EmailSnapshot({ mail, removing = false }: { mail: Mail; removing?: boolean }) {
  const [full, setFull] = useState(false);
  // Some models double-escape newlines; show them as real line breaks.
  const body = (mail.body ?? "").replace(/\\n/g, "\n").trim();
  const long = body.split("\n").length > 6 || body.length > 320;

  return (
    <div className={`overflow-hidden rounded-xl border border-line bg-paper ${removing ? "opacity-60" : ""}`}>
      <div className="space-y-1 border-b border-line px-3 py-2.5 text-[12px]">
        <Row label="From">You</Row>
        <Row label="To">
          <Addrs list={mail.to} />
        </Row>
        {!!mail.cc?.length && (
          <Row label="Cc">
            <Addrs list={mail.cc} />
          </Row>
        )}
        {!!mail.bcc?.length && (
          <Row label="Bcc">
            <Addrs list={mail.bcc} />
            <span className="ml-1 text-ink-3">(hidden from others)</span>
          </Row>
        )}
      </div>
      <div className="px-3 py-2.5">
        <div className="flex items-center gap-1.5">
          {(mail.reply_to_id || mail.in_reply_to) && (
            <span title="Reply in the same thread" className="text-ink-3">
              <Reply size={13} />
            </span>
          )}
          <div className={`text-[14px] font-semibold text-ink ${removing ? "line-through" : ""}`}>
            {mail.subject?.trim() || <span className="font-normal text-danger">No subject</span>}
          </div>
        </div>
        {body ? (
          <>
            <p className={`mt-1.5 text-[13px] leading-relaxed whitespace-pre-wrap text-ink-2 ${!full && long ? "line-clamp-6" : ""}`}>
              {body}
            </p>
            {long && (
              <button onClick={() => setFull((f) => !f)} className="mt-1 text-[12px] font-medium text-forest hover:underline">
                {full ? "Show less" : "Show full message"}
              </button>
            )}
          </>
        ) : (
          <p className="mt-1.5 text-[13px] text-ink-3 italic">Empty message</p>
        )}
      </div>
      {removing && <div className="border-t border-line bg-danger-soft px-3 py-1.5 text-[12px] text-danger">This draft will be deleted</div>}
    </div>
  );
}

function Row({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex gap-2">
      <span className="w-9 shrink-0 text-ink-3">{label}</span>
      <span className="min-w-0 flex-1 break-words text-ink">{children}</span>
    </div>
  );
}

function Addrs({ list }: { list?: string[] }) {
  if (!list?.length) return <span className="text-danger">nobody</span>;
  return <>{list.join(", ")}</>;
}

// ---------- Calendar ----------

const sameDay = (a: Date, b: Date) => a.toDateString() === b.toDateString();
const hm = (d: Date) => d.toLocaleTimeString([], { hour: "numeric", minute: "2-digit" });

function timeRange(ev: Ev): string {
  if (ev.all_day) return "All day";
  const s = new Date(ev.start);
  const e = new Date(ev.end);
  const mins = Math.round((+e - +s) / 60000);
  const dur = mins >= 60 ? `${Math.floor(mins / 60)}h${mins % 60 ? ` ${mins % 60}m` : ""}` : `${mins}m`;
  const end = sameDay(s, e) ? hm(e) : e.toLocaleString([], { weekday: "short", hour: "numeric", minute: "2-digit" });
  return `${hm(s)} – ${end} · ${dur}`;
}

function DateBlock({ iso, muted }: { iso: string; muted?: boolean }) {
  const d = new Date(iso);
  return (
    <div
      className={`flex w-12 shrink-0 flex-col items-center rounded-lg border py-1.5 ${
        muted ? "border-line bg-sunken text-ink-3" : "border-forest/25 bg-forest-soft text-forest"
      }`}
    >
      <span className="text-[10px] font-semibold tracking-wider uppercase">{d.toLocaleDateString([], { month: "short" })}</span>
      <span className="font-serif text-[22px] leading-none font-medium">{d.getDate()}</span>
      <span className="mt-0.5 text-[10px]">{d.toLocaleDateString([], { weekday: "short" })}</span>
    </div>
  );
}

function EventSnapshot({
  ev,
  removing = false,
  muted = false,
  changed,
  added = [],
  removed = [],
}: {
  ev: Ev;
  removing?: boolean;
  muted?: boolean;
  changed?: Set<Field>;
  added?: string[];
  removed?: string[];
}) {
  // In the "after" view, changed fields get a soft amber highlight; in the
  // "before" view (muted), they're struck through.
  const mark = (f: Field) =>
    changed?.has(f) ? (muted ? "line-through decoration-danger/60" : "rounded bg-amber-soft px-1 -mx-1 text-ink") : "";
  const guests = ev.attendees ?? [];
  const shownGuests = muted ? guests : [...guests];

  return (
    <div className={`rounded-xl border border-line bg-paper p-3 ${muted || removing ? "opacity-70" : ""}`}>
      <div className="flex gap-3">
        <DateBlock iso={ev.start} muted={muted || removing} />
        <div className="min-w-0 flex-1">
          <div className={`text-[14px] font-semibold text-ink ${removing ? "line-through" : ""}`}>
            <span className={mark("title")}>{ev.title || "(no title)"}</span>
          </div>
          <div className="mt-0.5 text-[12px] text-ink-2">
            <span className={mark("time")}>{timeRange(ev)}</span>
          </div>
          {(ev.location || changed?.has("location")) && (
            <div className="mt-1 flex items-start gap-1 text-[12px] text-ink-2">
              <MapPin size={12} className="mt-0.5 shrink-0 text-ink-3" />
              <span className={`break-words ${mark("location")}`}>{ev.location || "No location"}</span>
            </div>
          )}
        </div>
      </div>

      {(shownGuests.length > 0 || removed.length > 0) && (
        <div className="mt-2.5">
          <div className="mb-1 flex items-center gap-1 text-[11px] font-medium text-ink-3">
            <Users size={11} /> {guests.length} guest{guests.length === 1 ? "" : "s"}
          </div>
          <div className="flex flex-wrap gap-1">
            {shownGuests.map((g) => (
              <Guest key={g} email={g} state={added.includes(g) ? "added" : muted && removed.includes(g) ? "removed" : "same"} />
            ))}
          </div>
        </div>
      )}

      {ev.description && (
        <p className={`mt-2 line-clamp-3 border-l-2 border-line pl-2 text-[12px] whitespace-pre-wrap text-ink-2 ${mark("description")}`}>
          {ev.description}
        </p>
      )}
      {!ev.description && changed?.has("description") && !muted && (
        <p className="mt-2 text-[12px] text-ink-3 italic">Description removed</p>
      )}

      {removing && <div className="mt-2.5 rounded-lg bg-danger-soft px-2 py-1 text-[12px] text-danger">This event will be cancelled and guests notified</div>}
    </div>
  );
}

function Guest({ email, state }: { email: string; state: "added" | "removed" | "same" }) {
  const name = email.replace(/@.*/, "");
  const style =
    state === "added"
      ? "border-forest/30 bg-forest-soft text-forest"
      : state === "removed"
        ? "border-danger/30 bg-danger-soft text-danger line-through"
        : "border-line bg-surface text-ink-2";
  return (
    <span title={email} className={`inline-flex max-w-full items-center gap-1 rounded-full border py-0.5 pr-2 pl-0.5 text-[11.5px] ${style}`}>
      <Avatar name={name} size={18} />
      <span className="truncate">
        {state === "added" ? "+ " : state === "removed" ? "− " : ""}
        {name}
      </span>
    </span>
  );
}

type Update = Partial<Pick<Ev, "title" | "description" | "location" | "start" | "end" | "attendees">>;

function EventDiff({ before, update }: { before: Ev; update: Update }) {
  const after: Ev = { ...before, ...Object.fromEntries(Object.entries(update).filter(([, v]) => v !== undefined && v !== null)) };

  const changed = new Set<Field>();
  if (update.title !== undefined && update.title !== before.title) changed.add("title");
  if ((update.start && +new Date(update.start) !== +new Date(before.start)) || (update.end && +new Date(update.end) !== +new Date(before.end)))
    changed.add("time");
  if (update.location !== undefined && update.location !== (before.location ?? "")) changed.add("location");
  if (update.description !== undefined && update.description !== (before.description ?? "")) changed.add("description");

  const was = before.attendees ?? [];
  const now = after.attendees ?? [];
  const lower = (l: string[]) => l.map((x) => x.toLowerCase());
  const added = now.filter((g) => !lower(was).includes(g.toLowerCase()));
  const removed = was.filter((g) => !lower(now).includes(g.toLowerCase()));

  const chips = [
    changed.has("title") && "Renamed",
    changed.has("time") && "New time",
    changed.has("location") && "New location",
    changed.has("description") && "Agenda edited",
    added.length > 0 && `+${added.length} guest${added.length > 1 ? "s" : ""}`,
    removed.length > 0 && `−${removed.length} guest${removed.length > 1 ? "s" : ""}`,
  ].filter(Boolean) as string[];

  return (
    <div>
      {chips.length > 0 && (
        <div className="mb-2 flex flex-wrap gap-1">
          {chips.map((c) => (
            <span key={c} className="rounded-full bg-amber-soft px-2 py-0.5 text-[11.5px] font-medium text-amber">
              {c}
            </span>
          ))}
        </div>
      )}
      <Label>Now</Label>
      <EventSnapshot ev={before} muted changed={changed} removed={removed} />
      <div className="flex justify-center py-1 text-ink-3" aria-hidden>
        <ArrowDown size={14} />
      </div>
      <Label>After approval</Label>
      <EventSnapshot ev={after} changed={changed} added={added} />
      {added.length > 0 && (
        <p className="mt-1.5 text-[11.5px] text-ink-3">New guests get a calendar invite with the time and join link.</p>
      )}
    </div>
  );
}

function Label({ children }: { children: React.ReactNode }) {
  return <div className="mb-1 text-[11px] font-semibold tracking-wide text-ink-3 uppercase">{children}</div>;
}
