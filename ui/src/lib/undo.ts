// Undo window for approvals: an approved action waits a few seconds before
// the server runs it, so a wrong recipient or time can still be caught.
// Nothing is sent until the timer ends; closing the tab leaves it pending.
import { useEffect, useState, useSyncExternalStore } from "react";

type Entry = { until: number; timer: ReturnType<typeof setTimeout> };

const entries = new Map<string, Entry>();
const listeners = new Set<() => void>();
let version = 0;

function emit() {
  version++;
  listeners.forEach((l) => l());
}

function subscribe(l: () => void) {
  listeners.add(l);
  return () => listeners.delete(l);
}

// Warn before leaving while something is still counting down.
window.addEventListener("beforeunload", (e) => {
  if (entries.size > 0) e.preventDefault();
});

export function scheduleApproval(id: string, seconds: number, run: () => void) {
  cancelApproval(id);
  const timer = setTimeout(() => {
    entries.delete(id);
    emit();
    run();
  }, seconds * 1000);
  entries.set(id, { until: Date.now() + seconds * 1000, timer });
  emit();
}

export function cancelApproval(id: string) {
  const e = entries.get(id);
  if (!e) return;
  clearTimeout(e.timer);
  entries.delete(id);
  emit();
}

// Seconds left before the action runs, or null when it isn't counting down.
export function useCountdown(id: string): number | null {
  useSyncExternalStore(subscribe, () => version);
  const until = entries.get(id)?.until;
  const [, tick] = useState(0);
  useEffect(() => {
    if (!until) return;
    const t = setInterval(() => tick((n) => n + 1), 250);
    return () => clearInterval(t);
  }, [until]);
  return until ? Math.max(0, Math.ceil((until - Date.now()) / 1000)) : null;
}
