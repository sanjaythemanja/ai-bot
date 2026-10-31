import { AlertTriangle, CalendarCheck, Lock, MailCheck } from "lucide-react";
import { Mark } from "./ui";

const ERRORS: Record<string, string> = {
  not_allowed: "This Google account isn't on the invite list yet. Ask the person who shared the app to add you.",
  denied: "Sign-in was cancelled. You can try again whenever you're ready.",
  expired: "That sign-in link expired. Please try again.",
  failed: "Something went wrong signing you in. Please try again.",
};

export function SignIn() {
  const code = new URLSearchParams(window.location.search).get("auth_error");
  const error = code ? ERRORS[code] ?? ERRORS.failed : null;

  return (
    <div className="flex min-h-full items-center justify-center bg-paper px-5 py-12">
      <div className="w-full max-w-[420px]">
        <div className="flex items-center gap-2.5">
          <Mark size={32} />
          <span className="font-serif text-[19px] font-semibold tracking-tight">Exec Assistant</span>
        </div>

        <h1 className="mt-10 font-serif text-[38px] leading-[1.1] font-medium tracking-tight text-ink">
          Your inbox and calendar, handled.
        </h1>
        <p className="mt-3 text-[16px] text-ink-2">
          Sign in with Google to let the assistant read your mail, plan around your calendar, and draft replies. Nothing is sent
          until you approve it.
        </p>

        {error && (
          <div role="alert" className="mt-6 flex items-start gap-2 rounded-xl border border-danger/25 bg-danger-soft px-4 py-3 text-sm text-danger">
            <AlertTriangle size={16} className="mt-0.5 shrink-0" />
            <span>{error}</span>
          </div>
        )}

        <a
          href="/auth/google/start"
          className="mt-8 flex h-12 w-full items-center justify-center gap-3 rounded-xl border border-line-strong bg-surface text-[15px] font-medium text-ink shadow-[0_1px_2px_rgba(40,30,20,0.06)] transition-colors hover:border-ink-3 hover:bg-sunken/40"
        >
          <GoogleG />
          Continue with Google
        </a>

        <ul className="mt-10 space-y-3 text-[14px] text-ink-2">
          <li className="flex gap-3">
            <MailCheck size={18} className="mt-0.5 shrink-0 text-forest" />
            Triage, summaries and drafts from your own Gmail.
          </li>
          <li className="flex gap-3">
            <CalendarCheck size={18} className="mt-0.5 shrink-0 text-forest" />
            Scheduling that checks your calendar first.
          </li>
          <li className="flex gap-3">
            <Lock size={18} className="mt-0.5 shrink-0 text-forest" />
            Your access is encrypted and private to you. Sign out any time.
          </li>
        </ul>
      </div>
    </div>
  );
}

// Google's "G" mark, drawn inline (no external image requests).
function GoogleG() {
  return (
    <svg width="18" height="18" viewBox="0 0 48 48" aria-hidden>
      <path fill="#EA4335" d="M24 9.5c3.5 0 6.6 1.2 9.1 3.6l6.8-6.8C35.8 2.4 30.3 0 24 0 14.6 0 6.6 5.4 2.7 13.2l7.9 6.1C12.5 13.6 17.8 9.5 24 9.5z" />
      <path fill="#4285F4" d="M46.1 24.5c0-1.6-.1-3.1-.4-4.5H24v9h12.4c-.5 2.9-2.2 5.3-4.6 6.9l7.5 5.8c4.4-4 6.8-10 6.8-17.2z" />
      <path fill="#FBBC05" d="M10.6 28.7c-.5-1.4-.8-3-.8-4.7s.3-3.2.8-4.7l-7.9-6.1C1 16.6 0 20.2 0 24s1 7.4 2.7 10.8l7.9-6.1z" />
      <path fill="#34A853" d="M24 48c6.5 0 11.9-2.1 15.8-5.8l-7.5-5.8c-2.1 1.4-4.8 2.3-8.3 2.3-6.2 0-11.5-4.1-13.4-9.8l-7.9 6.1C6.6 42.6 14.6 48 24 48z" />
    </svg>
  );
}
