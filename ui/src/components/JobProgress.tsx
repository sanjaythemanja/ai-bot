// Live progress of a background chat job: each tool step as it starts and
// finishes, with parallel calls of the same tool grouped ("Read transcript:
// 3 of 5"), and a Stop button.
import { useEffect, useState } from "react";
import { Check, Loader2, Square, X } from "lucide-react";
import type { Job, Step } from "../api";
import { toolLabel } from "../lib/format";
import { Mark } from "./ui";

type Group = { name: string; steps: Step[] };

// Consecutive calls of the same tool form one group.
function groupTools(steps: Step[]): Group[] {
  const out: Group[] = [];
  for (const s of steps) {
    if (s.kind !== "tool") continue;
    const last = out.at(-1);
    if (last && last.name === s.name) last.steps.push(s);
    else out.push({ name: s.name ?? "", steps: [s] });
  }
  return out;
}

const finished = (s: Step) => s.status === "done" || s.status === "error";

function Icon({ s }: { s: Step["status"] }) {
  if (s === "done") return <Check size={14} className="shrink-0 text-forest" />;
  if (s === "error") return <X size={14} className="shrink-0 text-danger" />;
  return <Loader2 size={14} className="shrink-0 animate-spin text-clay" />;
}

function useElapsed(since: string) {
  const [now, setNow] = useState(Date.now());
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(t);
  }, []);
  return Math.max(0, Math.round((now - new Date(since).getTime()) / 1000));
}

export function JobProgress({ job, onStop, stopping }: { job: Job; onStop: () => void; stopping: boolean }) {
  const steps = job.steps ?? [];
  const groups = groupTools(steps);
  const last = steps.at(-1);
  const thinking = !last || (last.kind === "model" && last.status === "running");
  const secs = useElapsed(job.created_at);

  return (
    <div className="flex gap-3 rise" role="status" aria-live="polite">
      <Mark size={26} />
      <div className="min-w-0 flex-1 rounded-2xl border border-line bg-surface p-3.5">
        <ul className="space-y-1.5 text-[13.5px]">
          {groups.map((g, i) => {
            const done = g.steps.filter(finished).length;
            const groupStatus: Step["status"] = done < g.steps.length ? "running" : g.steps.some((s) => s.status === "error") ? "error" : "done";
            if (g.steps.length === 1) {
              const s = g.steps[0];
              return (
                <li key={i} className="flex items-start gap-2">
                  <span className="mt-0.5"><Icon s={s.status} /></span>
                  <span className="min-w-0 text-ink">
                    {toolLabel(g.name)}
                    {s.hint && <span className="text-ink-3"> · {s.hint}</span>}
                    {s.status === "summarizing" && <span className="text-clay"> · summarising</span>}
                    {s.error && <span className="block text-[12px] text-danger">{s.error}</span>}
                  </span>
                </li>
              );
            }
            return (
              <li key={i}>
                <div className="flex items-center gap-2">
                  <Icon s={groupStatus} />
                  <span className="text-ink">
                    {toolLabel(g.name)}: <span className="tabular-nums">{done} of {g.steps.length}</span>
                  </span>
                </div>
                <div className="mt-1 ml-[22px] flex flex-wrap gap-1.5">
                  {g.steps.map((s) => (
                    <span
                      key={s.id}
                      title={s.error}
                      className={`inline-flex max-w-full items-center gap-1 rounded-full border px-2 py-0.5 text-[12px] ${
                        s.status === "error" ? "border-danger/30 text-danger" : finished(s) ? "border-line text-ink-2" : "border-clay/30 text-ink"
                      }`}
                    >
                      <Icon s={s.status} />
                      <span className="truncate">{s.hint || "item"}</span>
                      {s.status === "summarizing" && <span className="text-clay">summarising</span>}
                    </span>
                  ))}
                </div>
              </li>
            );
          })}
          {thinking && (
            <li className="flex items-center gap-2 text-ink-2">
              <Loader2 size={14} className="animate-spin text-clay" />
              {groups.length === 0 ? "Understanding your request" : "Thinking about the results"}
            </li>
          )}
        </ul>
        <div className="mt-3 flex items-center gap-3 border-t border-line pt-2.5 text-[12px] text-ink-3">
          <span className="tabular-nums">{secs}s</span>
          <span className="flex-1">You can leave this page; it keeps running.</span>
          <button
            onClick={onStop}
            disabled={stopping}
            className="inline-flex items-center gap-1 rounded-md px-2 py-1 font-medium text-ink-2 hover:bg-sunken hover:text-danger disabled:opacity-50"
          >
            <Square size={11} fill="currentColor" />
            {stopping ? "Stopping…" : "Stop"}
          </button>
        </div>
      </div>
    </div>
  );
}
