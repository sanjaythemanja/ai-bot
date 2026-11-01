import { useEffect, useLayoutEffect, useRef, useState } from "react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
  AlertTriangle,
  ArrowUp,
  ChevronDown,
  Hourglass,
  Inbox,
  Mic,
  PenLine,
  ShieldCheck,
  Square,
} from "lucide-react";
import { api, watchJob, type Job, type ToolCall } from "../api";
import { greeting, longDate, stripStamp, toolLabel, toolOutcome } from "../lib/format";
import { canDictate, speak, stopSpeaking, useDictation } from "../lib/speech";
import { ActionCard } from "./ActionCard";
import { JobProgress } from "./JobProgress";
import { StatusPill } from "./RightRail";
import { Mark, Pill, Thinking } from "./ui";

export type ChatMessage =
  | { role: "user"; text: string }
  | { role: "assistant"; text: string; tools: ToolCall[]; warning?: string }
  | { role: "error"; text: string };

const SUGGESTIONS = [
  { icon: Inbox, text: "What needs my attention today, and why?" },
  { icon: Hourglass, text: "Who hasn't replied to me, and what did I promise people?" },
  { icon: ShieldCheck, text: "Keep Tuesday and Thursday 2–4pm free for focus" },
  { icon: PenLine, text: "Draft a reply to my most recent unread email" },
];

export function Chat({
  sessionId,
  messages,
  setMessages,
  aiReady,
  aiHint,
  requireApproval,
  undoSeconds,
  resumeJob,
}: {
  sessionId: string;
  messages: ChatMessage[];
  setMessages: (fn: (m: ChatMessage[]) => ChatMessage[]) => void;
  aiReady: boolean;
  aiHint?: string;
  requireApproval: boolean;
  undoSeconds: number;
  resumeJob?: Job | null; // a job still running when this conversation was opened
}) {
  const qc = useQueryClient();
  const [draft, setDraft] = useState("");
  const [busy, setBusy] = useState(false);
  const [job, setJob] = useState<Job | null>(null);
  const [stopping, setStopping] = useState(false);
  const unwatch = useRef<(() => void) | null>(null);
  const scroller = useRef<HTMLDivElement>(null);
  const input = useRef<HTMLTextAreaElement>(null);

  useEffect(() => {
    scroller.current?.scrollTo({ top: scroller.current.scrollHeight, behavior: "smooth" });
  }, [messages.length, busy, job?.steps?.length]);

  useEffect(() => input.current?.focus(), [sessionId]);

  const refresh = () => {
    qc.invalidateQueries({ queryKey: ["conversations"] });
    qc.invalidateQueries({ queryKey: ["actions"] });
    qc.invalidateQueries({ queryKey: ["overview"] });
  };

  // follow watches a job until it ends, then turns it into a reply.
  // viaVoice: the message was spoken, so the reply is read aloud too.
  function follow(j: Job, viaVoice: boolean) {
    setJob(j);
    setBusy(true);
    unwatch.current?.();
    unwatch.current = watchJob(j.id, (u) => {
      setJob(u);
      qc.invalidateQueries({ queryKey: ["actions"] }); // approvals appear as soon as they're queued
      if (u.status === "running") return;
      if (u.status === "done") {
        setMessages((m) => [...m, { role: "assistant", text: u.reply || "(no reply)", tools: u.tool_calls ?? [], warning: u.warning }]);
        if (viaVoice && u.reply) speak(u.reply);
      } else {
        setMessages((m) => [...m, { role: "error", text: u.error || "Something went wrong." }]);
      }
      unwatch.current = null;
      setJob(null);
      setBusy(false);
      setStopping(false);
      refresh();
      input.current?.focus();
    });
  }

  // Pick up a job that was still running when this conversation opened;
  // stop watching (not the job) when leaving it.
  useEffect(() => {
    if (resumeJob?.status === "running") {
      setMessages((m) => [...m, { role: "user", text: resumeJob.message }]);
      follow(resumeJob, false);
    }
    return () => unwatch.current?.();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  async function send(text: string, viaVoice = false) {
    const msg = text.trim();
    if (!msg || busy) return;
    stopSpeaking();
    setDraft("");
    setBusy(true);
    setMessages((m) => [...m, { role: "user", text: msg }]);
    try {
      follow(await api.startChat(sessionId, msg), viaVoice);
    } catch (e) {
      setMessages((m) => [...m, { role: "error", text: (e as Error).message }]);
      setBusy(false);
      refresh();
    }
  }

  async function stop() {
    if (!job) return;
    setStopping(true);
    try {
      await api.cancelJob(job.id);
    } catch {
      setStopping(false);
    }
  }

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div ref={scroller} className="flex-1 overflow-y-auto">
        <div className="mx-auto w-full max-w-[720px] px-5 pt-8 pb-6 sm:px-8">
          {messages.length === 0 ? (
            <Welcome onPick={send} disabled={!aiReady || busy} hint={aiReady ? undefined : aiHint} />
          ) : (
            <div className="space-y-7">
              {messages.map((m, i) => (
                <Message key={i} m={m} undoSeconds={undoSeconds} />
              ))}
              {job ? (
                <JobProgress job={job} onStop={stop} stopping={stopping} />
              ) : (
                busy && (
                  <div className="flex gap-3 rise">
                    <Mark size={26} />
                    <div className="pt-1">
                      <Thinking label="Working on it" />
                    </div>
                  </div>
                )
              )}
            </div>
          )}
        </div>
      </div>
      <Composer
        value={draft}
        onChange={setDraft}
        onSend={() => send(draft)}
        onVoiceSend={(t) => send(t, true)}
        disabled={busy || !aiReady}
        inputRef={input}
        requireApproval={requireApproval}
      />
    </div>
  );
}

function Welcome({ onPick, disabled, hint }: { onPick: (t: string) => void; disabled: boolean; hint?: string }) {
  return (
    <div className="pt-[8vh]">
      <p className="text-sm text-ink-3">{longDate()}</p>
      <h1 className="mt-1 font-serif text-[40px] leading-[1.1] font-medium tracking-tight text-ink">
        {greeting()}.
      </h1>
      <p className="mt-2 max-w-md text-[16px] text-ink-2">
        I can read your inbox, plan around your calendar, and draft or send things once you've had a look.
      </p>
      {hint && (
        <div className="mt-5 flex items-start gap-2 rounded-xl border border-amber/30 bg-amber-soft px-4 py-3 text-sm text-amber">
          <AlertTriangle size={16} className="mt-0.5 shrink-0" />
          <span>{hint}</span>
        </div>
      )}
      <div className="mt-8 grid gap-2.5 sm:grid-cols-2">
        {SUGGESTIONS.map(({ icon: Icon, text }) => (
          <button
            key={text}
            disabled={disabled}
            onClick={() => onPick(text)}
            className="group flex items-start gap-3 rounded-xl border border-line bg-surface p-3.5 text-left text-[14px] text-ink transition-all hover:-translate-y-px hover:border-line-strong hover:bg-sunken/40 disabled:opacity-50 disabled:hover:translate-y-0"
          >
            <Icon size={17} className="mt-0.5 shrink-0 text-clay" />
            <span>{text}</span>
          </button>
        ))}
      </div>
    </div>
  );
}

function Message({ m, undoSeconds }: { m: ChatMessage; undoSeconds: number }) {
  if (m.role === "user") {
    return (
      <div className="flex justify-end rise">
        <div className="max-w-[85%] rounded-2xl rounded-br-md bg-sunken px-4 py-2.5 whitespace-pre-wrap text-ink">
          {stripStamp(m.text)}
        </div>
      </div>
    );
  }
  if (m.role === "error") {
    return (
      <div className="flex items-start gap-2 rounded-xl border border-danger/25 bg-danger-soft px-4 py-3 text-sm text-danger rise">
        <AlertTriangle size={16} className="mt-0.5 shrink-0" />
        <span>{m.text}</span>
      </div>
    );
  }
  return (
    <div className="flex gap-3 rise">
      <Mark size={26} />
      <div className="min-w-0 flex-1 pt-0.5">
        <div className="prose-reply text-[15px] text-ink">
          <ReactMarkdown
            remarkPlugins={[remarkGfm]}
            components={{ a: (p) => <a {...p} target="_blank" rel="noreferrer noopener" /> }}
          >
            {m.text}
          </ReactMarkdown>
        </div>
        {m.warning && <p className="mt-2 text-[13px] text-amber">{m.warning}</p>}
        {m.tools.length > 0 && <ToolTrail tools={m.tools} />}
        <InlineApprovals ids={queuedIds(m.tools)} undoSeconds={undoSeconds} />
      </div>
    </div>
  );
}

// Action IDs this reply queued for approval.
function queuedIds(tools: ToolCall[]): string[] {
  const ids: string[] = [];
  for (const t of tools) {
    if (t.is_error || toolOutcome(t.output) !== "queued") continue;
    try {
      const id = (JSON.parse(t.output) as { action_id?: string }).action_id;
      if (id) ids.push(id);
    } catch {
      /* not JSON */
    }
  }
  return ids;
}

// On phones and tablets the approvals panel is a tap away, so approvals a
// reply queued are shown right under it (the wide layout has the panel open).
function InlineApprovals({ ids, undoSeconds }: { ids: string[]; undoSeconds: number }) {
  const actions = useQuery({ queryKey: ["actions"], queryFn: api.actions, enabled: ids.length > 0 });
  if (ids.length === 0) return null;
  const mine = (actions.data ?? []).filter((a) => ids.includes(a.id));
  if (mine.length === 0) return null;
  return (
    <div className="mt-3 space-y-2.5 xl:hidden">
      {mine.map((a) =>
        a.status === "pending" ? (
          <ActionCard key={a.id} a={a} undoSeconds={undoSeconds} />
        ) : (
          <div key={a.id} className="flex items-start gap-2 rounded-xl border border-line bg-surface px-3 py-2 text-[13px] text-ink-2">
            <StatusPill status={a.status} />
            <span className="min-w-0 flex-1 line-clamp-2">{a.summary}</span>
          </div>
        ),
      )}
    </div>
  );
}

function ToolTrail({ tools }: { tools: ToolCall[] }) {
  const [open, setOpen] = useState(false);
  return (
    <div className="mt-3">
      <div className="flex flex-wrap items-center gap-1.5">
        {tools.map((t, i) => {
          const outcome = toolOutcome(t.output);
          const tone = t.is_error ? "danger" : outcome === "queued" ? "amber" : outcome === "done" ? "forest" : "neutral";
          return (
            <Pill key={i} tone={tone}>
              {toolLabel(t.name)}
              {outcome === "queued" && " · needs approval"}
              {outcome === "done" && " · done"}
              {t.is_error && " · failed"}
            </Pill>
          );
        })}
        <button
          onClick={() => setOpen((o) => !o)}
          className="inline-flex items-center gap-0.5 rounded-full px-1.5 text-[12px] text-ink-3 hover:text-ink-2"
          aria-expanded={open}
        >
          Details
          <ChevronDown size={13} className={`transition-transform ${open ? "rotate-180" : ""}`} />
        </button>
      </div>
      {open && (
        <div className="mt-2 space-y-1.5 rounded-xl border border-line bg-surface p-3 font-mono text-[12px] text-ink-2">
          {tools.map((t, i) => (
            <div key={i} className="break-all">
              <span className="text-ink">{t.name}</span> {t.input}
              {t.is_error && <div className="text-danger">{t.output}</div>}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

function Composer({
  value,
  onChange,
  onSend,
  onVoiceSend,
  disabled,
  inputRef,
  requireApproval,
}: {
  value: string;
  onChange: (v: string) => void;
  onSend: () => void;
  onVoiceSend: (text: string) => void;
  disabled: boolean;
  inputRef: React.RefObject<HTMLTextAreaElement | null>;
  requireApproval: boolean;
}) {
  const voice = useDictation();
  const [voiceError, setVoiceError] = useState<string | null>(null);

  // Speak; a pause sends the message. Tapping stop keeps the text to edit.
  const toggleMic = () => {
    if (voice.listening) return voice.stop();
    stopSpeaking();
    setVoiceError(null);
    const base = value.trim();
    const join = (t: string) => (base ? `${base} ${t}` : t);
    voice.start({
      onText: (t) => onChange(join(t)),
      onDone: (t, stoppedByUser) => {
        if (t && !stoppedByUser) onVoiceSend(join(t));
        else inputRef.current?.focus();
      },
      onError: setVoiceError,
    });
  };

  useLayoutEffect(() => {
    const el = inputRef.current;
    if (!el) return;
    el.style.height = "auto";
    el.style.height = Math.min(el.scrollHeight, 200) + "px";
  }, [value, inputRef]);

  return (
    <div className="border-t border-line/70 bg-paper/90 backdrop-blur-sm">
      <div className="mx-auto w-full max-w-[720px] px-5 py-4 sm:px-8">
        <form
          onSubmit={(e) => {
            e.preventDefault();
            onSend();
          }}
          className="flex items-end gap-2 rounded-2xl border border-line-strong/70 bg-surface p-2 pl-4 shadow-[0_1px_2px_rgba(40,30,20,0.04)] transition-colors focus-within:border-forest/50"
        >
          <textarea
            ref={inputRef}
            rows={1}
            value={value}
            onChange={(e) => onChange(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing) {
                e.preventDefault();
                onSend();
              }
            }}
            placeholder={voice.listening ? "Listening… pause when done" : "Ask about your day…"}
            aria-label="Message"
            className="max-h-[200px] min-h-[24px] flex-1 resize-none bg-transparent py-1.5 text-[15px] text-ink outline-none placeholder:text-ink-3"
          />
          {canDictate && (
            <button
              type="button"
              onClick={toggleMic}
              disabled={disabled && !voice.listening}
              aria-label={voice.listening ? "Stop listening" : "Speak your request"}
              aria-pressed={voice.listening}
              title={voice.listening ? "Stop listening" : "Speak your request"}
              className={`inline-flex size-9 shrink-0 items-center justify-center rounded-xl transition-colors disabled:opacity-50 ${
                voice.listening ? "animate-pulse bg-danger-soft text-danger" : "text-ink-3 hover:bg-sunken hover:text-ink"
              }`}
            >
              {voice.listening ? <Square size={15} fill="currentColor" /> : <Mic size={18} />}
            </button>
          )}
          <button
            type="submit"
            disabled={disabled || !value.trim()}
            aria-label="Send"
            className="inline-flex size-9 shrink-0 items-center justify-center rounded-xl bg-forest text-on-forest transition-colors hover:bg-forest-hover disabled:bg-line disabled:text-ink-3"
          >
            <ArrowUp size={18} strokeWidth={2.2} />
          </button>
        </form>
        {voiceError && <p className="mt-2 text-center text-[12px] text-danger">{voiceError}</p>}
        <p className="mt-2 text-center text-[12px] text-ink-3">
          Enter to send{canDictate ? " · Tap the mic to speak" : " · Shift + Enter for a new line"} ·{" "}
          {requireApproval ? "Emails and invites wait for your approval" : "Approval is off: emails and invites go out right away"}
        </p>
      </div>
    </div>
  );
}
