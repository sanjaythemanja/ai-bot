// Voice input and read-aloud, using the browser's built-in speech APIs (no
// server or API cost). Chrome, Edge and Safari support dictation; Firefox
// doesn't, so the mic button is hidden there.
import { useRef, useState } from "react";

// eslint-disable-next-line @typescript-eslint/no-explicit-any
const Recognition: any =
  typeof window !== "undefined" &&
  ((window as unknown as Record<string, unknown>).SpeechRecognition ??
    (window as unknown as Record<string, unknown>).webkitSpeechRecognition);

export const canDictate = !!Recognition;

const ERRORS: Record<string, string> = {
  "not-allowed": "Microphone access is blocked. Allow it for this site in your browser settings.",
  "service-not-allowed": "Microphone access is blocked. Allow it for this site in your browser settings.",
  "audio-capture": "No microphone was found.",
  network: "Voice input needs an internet connection.",
};

type Handlers = {
  onText: (text: string) => void; // live transcript so far
  onDone: (text: string, stoppedByUser: boolean) => void;
  onError: (message: string) => void;
};

export function useDictation() {
  const [listening, setListening] = useState(false);
  const active = useRef<{ stop: () => void } | null>(null);

  const start = (h: Handlers) => {
    if (!Recognition || active.current) return;
    const r = new Recognition();
    r.lang = navigator.language || "en-US";
    r.interimResults = true;
    r.continuous = false; // ends on a pause, which sends the message
    let finalText = "";
    let manual = false;
    r.onresult = (e: { resultIndex: number; results: ArrayLike<{ isFinal: boolean; 0: { transcript: string } }> }) => {
      let interim = "";
      for (let i = e.resultIndex; i < e.results.length; i++) {
        const res = e.results[i];
        if (res.isFinal) finalText += res[0].transcript;
        else interim += res[0].transcript;
      }
      h.onText((finalText + interim).trim());
    };
    r.onerror = (e: { error: string }) => {
      if (e.error !== "aborted" && e.error !== "no-speech") h.onError(ERRORS[e.error] ?? "Voice input stopped: " + e.error);
    };
    r.onend = () => {
      active.current = null;
      setListening(false);
      h.onDone(finalText.trim(), manual);
    };
    active.current = {
      stop: () => {
        manual = true;
        r.stop();
      },
    };
    try {
      r.start();
      setListening(true);
    } catch {
      active.current = null;
    }
  };

  return { listening, start, stop: () => active.current?.stop() };
}

// speak reads a reply aloud, skipping Markdown syntax and link targets.
export function speak(markdown: string) {
  if (!("speechSynthesis" in window)) return;
  window.speechSynthesis.cancel();
  const text = markdown
    .replace(/```[\s\S]*?```/g, " ")
    .replace(/\[([^\]]+)\]\([^)]+\)/g, "$1")
    .replace(/https?:\/\/\S+/g, "link")
    .replace(/[#*_`>|~]+/g, " ")
    .replace(/\s+/g, " ")
    .trim();
  if (!text) return;
  const u = new SpeechSynthesisUtterance(text.length > 1500 ? text.slice(0, 1500) + "…" : text);
  u.lang = navigator.language || "en-US";
  window.speechSynthesis.speak(u);
}

export const stopSpeaking = () => {
  if ("speechSynthesis" in window) window.speechSynthesis.cancel();
};
