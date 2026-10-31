// Display helpers: names, times, and cleanup of marketing-email noise.

// Marketing emails pad previews with invisible characters; strip them.
const INVISIBLE = new RegExp("[\\u034F\\u00AD\\u200B-\\u200F\\u2028-\\u202F\\u2060-\\u206F\\uFEFF]", "g");

export function cleanText(s: string | undefined): string {
  return (s ?? "").replace(INVISIBLE, "").replace(/\s+/g, " ").trim();
}

// "Priya Shah <priya@example.com>" → "Priya Shah"; bare address → local part.
export function senderName(from: string): string {
  const m = from.match(/^\s*"?([^"<]+?)"?\s*<[^>]+>\s*$/);
  if (m) return m[1].trim();
  return from.replace(/@.*/, "").trim() || from;
}

export function initials(name: string): string {
  const parts = name.replace(/[^\p{L}\s]/gu, " ").trim().split(/\s+/).filter(Boolean);
  return ((parts[0]?.[0] ?? "?") + (parts[1]?.[0] ?? "")).toUpperCase();
}

export const fmtTime = (iso: string) =>
  new Date(iso).toLocaleTimeString([], { hour: "numeric", minute: "2-digit" });

export function fmtRelative(iso: string): string {
  const d = new Date(iso);
  const mins = Math.round((Date.now() - d.getTime()) / 60000);
  if (mins < 1) return "just now";
  if (mins < 60) return `${mins}m ago`;
  const hours = Math.round(mins / 60);
  if (hours < 24) return `${hours}h ago`;
  return d.toLocaleDateString([], { month: "short", day: "numeric" });
}

export function dayBucket(iso: string): "Today" | "Yesterday" | "This week" | "Earlier" {
  const d = new Date(iso);
  const start = new Date();
  start.setHours(0, 0, 0, 0);
  const diff = (start.getTime() - new Date(d).setHours(0, 0, 0, 0)) / 86400000;
  if (diff <= 0) return "Today";
  if (diff === 1) return "Yesterday";
  if (diff < 7) return "This week";
  return "Earlier";
}

export function greeting(now = new Date()): string {
  const h = now.getHours();
  if (h < 5) return "Working late";
  if (h < 12) return "Good morning";
  if (h < 17) return "Good afternoon";
  return "Good evening";
}

export const longDate = (now = new Date()) =>
  now.toLocaleDateString([], { weekday: "long", month: "long", day: "numeric" });

// The server prefixes the stored user turn with a timestamp for the model.
export const stripStamp = (s: string) => s.replace(/^\[Current time:[^\]]*\]\s*/, "");

// Human labels for tool calls, shown under each reply.
const TOOL_LABELS: Record<string, string> = {
  search_emails: "Searched mail",
  read_email: "Read an email",
  draft_email: "Saved a draft",
  send_email: "Email",
  list_drafts: "Checked drafts",
  read_draft: "Read a draft",
  update_draft: "Edited a draft",
  send_draft: "Send draft",
  delete_draft: "Delete draft",
  list_events: "Checked calendar",
  get_event: "Looked up an event",
  find_free_slots: "Found free time",
  create_event: "New event",
  update_event: "Event change",
  delete_event: "Cancel event",
  check_availability: "Checked availability",
  find_common_free_slots: "Compared calendars",
  get_meeting_transcript: "Read transcript",
  list_pending_actions: "Reviewed pending",
  edit_pending_action: "Edited pending action",
  cancel_pending_action: "Cancelled pending action",
};

export const toolLabel = (name: string) => TOOL_LABELS[name] ?? name.replaceAll("_", " ");

// What a tool call ended up doing, from its JSON output.
export function toolOutcome(output: string): "queued" | "done" | null {
  if (output.includes('"awaiting_user_approval"')) return "queued";
  if (output.includes('"status":"done"')) return "done";
  return null;
}
