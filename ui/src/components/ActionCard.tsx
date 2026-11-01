import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import {
  AlertTriangle,
  CalendarClock,
  CalendarPlus,
  CalendarX,
  Check,
  ChevronDown,
  FileX,
  Hash,
  Mail,
  PenLine,
  Send,
  Undo2,
} from "lucide-react";
import { api, type Action } from "../api";
import { cancelApproval, scheduleApproval, useCountdown } from "../lib/undo";
import { ActionSnapshot } from "./Preview";
import { Button } from "./ui";

export const KIND: Record<string, { icon: typeof Mail; label: string; verb: string }> = {
  send_email: { icon: Mail, label: "Send email", verb: "Sending" },
  send_draft: { icon: Send, label: "Send draft", verb: "Sending" },
  delete_draft: { icon: FileX, label: "Delete draft", verb: "Deleting" },
  create_event: { icon: CalendarPlus, label: "New event", verb: "Booking" },
  update_event: { icon: CalendarClock, label: "Change event", verb: "Updating" },
  delete_event: { icon: CalendarX, label: "Cancel event", verb: "Cancelling" },
  slack_post: { icon: Hash, label: "Slack message", verb: "Posting" },
};

export const kindOf = (kind: string) => KIND[kind] ?? { icon: Mail, label: kind.replaceAll("_", " "), verb: "Running" };

// "Why" line for an action, from the request that led to it.
export function reasonText(reason?: string): string | null {
  if (!reason) return null;
  if (reason === "Daily brief") return "From your daily brief";
  if (reason.startsWith("Requested through")) return reason;
  const r = reason.replace(/\s+/g, " ").trim();
  return `You asked: “${r.length > 140 ? r.slice(0, 140) + "…" : r}”`;
}

export function ActionCard({ a, undoSeconds }: { a: Action; undoSeconds: number }) {
  const qc = useQueryClient();
  const [raw, setRaw] = useState(false);
  const left = useCountdown(a.id);
  const k = kindOf(a.kind);
  const Icon = k.icon;

  const refresh = () => {
    qc.invalidateQueries({ queryKey: ["actions"] });
    qc.invalidateQueries({ queryKey: ["overview"] });
  };
  // Runs outside the component's lifetime: the card may close while counting.
  const run = () => api.approve(a.id).finally(refresh);
  const now = useMutation({ mutationFn: run });
  const reject = useMutation({ mutationFn: () => api.reject(a.id), onSettled: refresh });
  const busy = now.isPending || reject.isPending;
  const err = now.error ?? reject.error;

  const approve = () => (undoSeconds > 0 ? scheduleApproval(a.id, undoSeconds, () => void run().catch(() => {})) : now.mutate());

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
      <ActionNotes a={a} />
      {raw && (
        <pre className="mt-3 max-h-56 overflow-auto rounded-lg bg-sunken p-2.5 text-[11.5px] leading-relaxed whitespace-pre-wrap text-ink-2">
          {JSON.stringify(a.payload, null, 2)}
        </pre>
      )}
      {err && <p className="mt-2 text-[12px] text-danger">{(err as Error).message}</p>}

      {left !== null ? (
        <div className="mt-3 overflow-hidden rounded-lg bg-forest-soft" role="status">
          <div className="flex items-center gap-2 px-3 py-2 text-[13px] text-forest">
            <span className="flex-1 font-medium tabular-nums">
              {k.verb} in {left}s…
            </span>
            <button
              onClick={() => {
                cancelApproval(a.id);
                now.mutate();
              }}
              className="text-[12px] text-forest/80 hover:text-forest hover:underline"
            >
              Do it now
            </button>
            <Button size="sm" variant="quiet" onClick={() => cancelApproval(a.id)}>
              <Undo2 size={14} /> Undo
            </Button>
          </div>
          <div className="h-0.5 bg-forest/15">
            <div className="h-full bg-forest transition-[width] duration-300 ease-linear" style={{ width: `${(left / undoSeconds) * 100}%` }} />
          </div>
        </div>
      ) : (
        <div className="mt-3 flex items-center gap-2">
          <Button size="sm" variant="primary" onClick={approve} disabled={busy}>
            <Check size={14} /> Approve
          </Button>
          <Button size="sm" variant="ghost" onClick={() => reject.mutate()} disabled={busy}>
            Reject
          </Button>
          <button
            onClick={() => setRaw((o) => !o)}
            className="ml-auto inline-flex items-center gap-0.5 text-[12px] text-ink-3 hover:text-ink-2"
            aria-expanded={raw}
          >
            Raw
            <ChevronDown size={13} className={`transition-transform ${raw ? "rotate-180" : ""}`} />
          </button>
        </div>
      )}
    </div>
  );
}

type StyleNote = { learned_from: number; sources?: { subject: string; to: string; sent_at: string }[] };

// Warnings (clashes, protected time), the learned writing style, and why the
// action exists.
function ActionNotes({ a }: { a: Action }) {
  const [sources, setSources] = useState(false);
  const p = a.payload as { warnings?: string[]; style?: StyleNote };
  const why = reasonText(a.reason);
  return (
    <>
      {!!p.warnings?.length && (
        <div className="mt-2.5 rounded-lg border border-amber/30 bg-amber-soft px-2.5 py-2 text-[12.5px] text-amber">
          {p.warnings.map((w) => (
            <div key={w} className="flex items-start gap-1.5">
              <AlertTriangle size={13} className="mt-0.5 shrink-0" />
              <span>{w}</span>
            </div>
          ))}
        </div>
      )}
      {p.style && (
        <div className="mt-2.5 text-[12px] text-ink-3">
          <button onClick={() => setSources((s) => !s)} className="inline-flex items-center gap-1 hover:text-ink-2" aria-expanded={sources}>
            <PenLine size={12} className="text-clay" />
            Written in your style · learned from {p.style.learned_from} of your sent emails
            <ChevronDown size={12} className={`transition-transform ${sources ? "rotate-180" : ""}`} />
          </button>
          {sources && (
            <ul className="mt-1 space-y-0.5 border-l-2 border-line pl-2.5">
              {(p.style.sources ?? []).map((s, i) => (
                <li key={i} className="truncate">
                  “{s.subject || "(no subject)"}” ·{" "}
                  {new Date(s.sent_at).toLocaleDateString([], { month: "short", day: "numeric" })}
                </li>
              ))}
            </ul>
          )}
        </div>
      )}
      {why && <p className="mt-2 line-clamp-2 text-[12px] text-ink-3">{why}</p>}
    </>
  );
}
