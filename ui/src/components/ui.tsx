import type { ButtonHTMLAttributes, ReactNode } from "react";
import { initials } from "../lib/format";

type Variant = "primary" | "quiet" | "ghost" | "danger";

const variants: Record<Variant, string> = {
  primary: "bg-forest text-on-forest hover:bg-forest-hover shadow-[0_1px_0_rgba(0,0,0,0.06)]",
  quiet: "bg-surface text-ink border border-line hover:border-line-strong hover:bg-sunken/60",
  ghost: "text-ink-2 hover:text-ink hover:bg-sunken",
  danger: "text-danger hover:bg-danger-soft",
};

export function Button({
  variant = "quiet",
  size = "md",
  className = "",
  ...props
}: ButtonHTMLAttributes<HTMLButtonElement> & { variant?: Variant; size?: "sm" | "md" }) {
  const sizing = size === "sm" ? "h-8 px-3 text-[13px] gap-1.5" : "h-9 px-3.5 text-sm gap-2";
  return (
    <button
      {...props}
      className={`inline-flex shrink-0 items-center justify-center whitespace-nowrap rounded-lg font-medium transition-colors disabled:opacity-50 disabled:pointer-events-none ${sizing} ${variants[variant]} ${className}`}
    />
  );
}

export function IconButton({
  label,
  className = "",
  ...props
}: ButtonHTMLAttributes<HTMLButtonElement> & { label: string }) {
  return (
    <button
      aria-label={label}
      title={label}
      {...props}
      className={`inline-flex size-8 items-center justify-center rounded-lg text-ink-3 transition-colors hover:bg-sunken hover:text-ink ${className}`}
    />
  );
}

type Tone = "neutral" | "forest" | "clay" | "amber" | "danger";

const tones: Record<Tone, string> = {
  neutral: "bg-sunken text-ink-2",
  forest: "bg-forest-soft text-forest",
  clay: "bg-clay-soft text-clay",
  amber: "bg-amber-soft text-amber",
  danger: "bg-danger-soft text-danger",
};

export function Pill({ tone = "neutral", children, className = "" }: { tone?: Tone; children: ReactNode; className?: string }) {
  return (
    <span className={`inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-[12px] font-medium ${tones[tone]} ${className}`}>
      {children}
    </span>
  );
}

export function SectionHeader({ title, count, action }: { title: string; count?: number; action?: ReactNode }) {
  return (
    <div className="mb-2 flex items-center justify-between">
      <h3 className="flex items-center gap-2 text-[13px] font-semibold text-ink-2">
        {title}
        {count ? (
          <span className="rounded-full bg-amber-soft px-1.5 text-[11px] font-semibold text-amber">{count}</span>
        ) : null}
      </h3>
      {action}
    </div>
  );
}

// Warm, deterministic avatar colours keyed on the name.
const avatarTones = [
  "bg-forest-soft text-forest",
  "bg-clay-soft text-clay",
  "bg-amber-soft text-amber",
  "bg-sunken text-ink-2",
];

export function Avatar({ name, size = 28 }: { name: string; size?: number }) {
  const tone = avatarTones[[...name].reduce((a, c) => a + c.charCodeAt(0), 0) % avatarTones.length];
  return (
    <span
      className={`inline-flex shrink-0 items-center justify-center rounded-full font-semibold ${tone}`}
      style={{ width: size, height: size, fontSize: size * 0.38 }}
      aria-hidden
    >
      {initials(name)}
    </span>
  );
}

export function Thinking({ label = "Thinking" }: { label?: string }) {
  return (
    <span className="inline-flex items-center gap-2 text-sm text-ink-3" role="status">
      <span className="inline-flex gap-1">
        <span className="dot-breathe size-1.5 rounded-full bg-clay" />
        <span className="dot-breathe size-1.5 rounded-full bg-clay" />
        <span className="dot-breathe size-1.5 rounded-full bg-clay" />
      </span>
      {label}
    </span>
  );
}

export function Mark({ size = 28 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 32 32" aria-hidden>
      <rect width="32" height="32" rx="8" fill="var(--forest)" />
      <path d="M10 21.5V10.5h11M10 16h8" stroke="var(--on-forest)" strokeWidth="2.6" strokeLinecap="round" strokeLinejoin="round" fill="none" />
      <circle cx="22.5" cy="21.5" r="2.2" fill="var(--clay)" />
    </svg>
  );
}
