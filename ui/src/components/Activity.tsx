import { useEffect } from "react";
import { useQuery } from "@tanstack/react-query";
import { X } from "lucide-react";
import { api, type Action } from "../api";
import { dayBucket } from "../lib/format";
import { kindOf, reasonText } from "./ActionCard";
import { StatusPill } from "./RightRail";
import { IconButton } from "./ui";

// Activity log: everything the assistant proposed or did, why, and how it
// ended, newest first.
export function Activity({ onClose }: { onClose: () => void }) {
  const actions = useQuery({ queryKey: ["actions"], queryFn: api.actions });

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);

  const groups = new Map<string, Action[]>();
  for (const a of actions.data ?? []) {
    const b = dayBucket(a.created_at);
    groups.set(b, [...(groups.get(b) ?? []), a]);
  }

  return (
    <div className="fixed inset-0 z-50 flex items-end justify-center sm:items-center" role="dialog" aria-modal="true" aria-label="Activity">
      <button aria-label="Close" className="absolute inset-0 bg-ink/25" onClick={onClose} />
      <div className="relative z-10 flex max-h-[88vh] w-full max-w-[640px] flex-col rounded-t-2xl border border-line bg-paper shadow-xl sm:rounded-2xl">
        <div className="flex items-start justify-between gap-3 border-b border-line px-5 pt-5 pb-3">
          <div>
            <h2 className="font-serif text-[22px] font-medium tracking-tight">Activity</h2>
            <p className="mt-0.5 text-[13px] text-ink-3">Everything the assistant proposed or did for you, and why.</p>
          </div>
          <IconButton label="Close activity" onClick={onClose}>
            <X size={16} />
          </IconButton>
        </div>
        <div className="overflow-y-auto px-5 py-4">
          {actions.isLoading && <p className="text-sm text-ink-3">Loading…</p>}
          {actions.data?.length === 0 && <p className="text-sm text-ink-3">Nothing yet.</p>}
          {["Today", "Yesterday", "This week", "Earlier"].map((label) =>
            groups.has(label) ? (
              <section key={label} className="mb-5">
                <h3 className="mb-2 text-[11px] font-semibold tracking-wide text-ink-3 uppercase">{label}</h3>
                <ol className="space-y-2">
                  {groups.get(label)!.map((a) => (
                    <Row key={a.id} a={a} />
                  ))}
                </ol>
              </section>
            ) : null,
          )}
        </div>
      </div>
    </div>
  );
}

function Row({ a }: { a: Action }) {
  const k = kindOf(a.kind);
  const Icon = k.icon;
  const when = new Date(a.created_at);
  const why = reasonText(a.reason);
  return (
    <li className="flex gap-3 rounded-xl border border-line bg-surface px-3 py-2.5">
      <div className="w-12 shrink-0 pt-0.5 text-[12px] text-ink-3 tabular-nums">
        {when.toLocaleTimeString([], { hour: "numeric", minute: "2-digit" })}
      </div>
      <div className="min-w-0 flex-1">
        <div className="flex flex-wrap items-center gap-1.5">
          <Icon size={13} className="text-ink-3" />
          <span className="text-[12px] font-medium text-ink-3">{k.label}</span>
          <StatusPill status={a.status} />
        </div>
        <p className="mt-1 text-[13.5px] break-words text-ink">{a.summary}</p>
        {why && <p className="mt-0.5 text-[12.5px] text-ink-2">{why}</p>}
        {a.status === "executed" && a.result && <p className="mt-0.5 text-[12px] break-all text-ink-3">{a.result}</p>}
        {a.error && <p className="mt-0.5 text-[12px] text-danger">{a.error}</p>}
      </div>
    </li>
  );
}
