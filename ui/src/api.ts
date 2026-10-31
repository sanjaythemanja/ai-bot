// Typed client for the Go API. Shapes mirror internal/assistant and internal/store.

export type Me = { id: string; email: string; name: string };

export type ConnectionInfo = { provider: string; configured: boolean; connected: boolean; label?: string };

export type Status = {
  demo_mode: boolean;
  ai_configured: boolean;
  ai_provider: string;
  model: string;
  ai_error?: string;
  google_configured: boolean;
  gmail: boolean;
  calendar: boolean;
  timezone: string;
  require_approval: boolean;
  storage: "postgres" | "memory" | string;
};

export type Email = {
  id: string;
  thread_id: string;
  from: string;
  to: string;
  subject: string;
  snippet: string;
  date: string;
  unread: boolean;
};

export type CalEvent = {
  id: string;
  title: string;
  description?: string;
  location?: string;
  start: string;
  end: string;
  all_day: boolean;
  attendees?: string[];
  link?: string;
  meet_link?: string;
};

export type Overview = {
  emails: Email[] | null;
  events: CalEvent[] | null;
  errors?: Record<string, string>;
};

export type ActionStatus = "pending" | "executed" | "rejected" | "failed";

export type Action = {
  id: string;
  kind: string;
  summary: string;
  payload: Record<string, unknown>;
  status: ActionStatus;
  result?: string;
  error?: string;
  created_at: string;
  updated_at: string;
};

export type ToolCall = { name: string; input: string; is_error: boolean; output: string };

export type ChatReply = { reply: string; tool_calls: ToolCall[] | null; warning?: string };

export type ConversationSummary = {
  id: string;
  title: string;
  created_at: string;
  updated_at: string;
};

export type Turn = {
  user_text: string;
  reply: string;
  tool_calls?: ToolCall[] | null;
  error?: string;
  created_at: string;
};

export type Conversation = ConversationSummary & { turns: Turn[] };

export type Briefing = { markdown: string; generated_at: string };

export class ApiError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.status = status;
  }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(path, {
    ...init,
    // JSON content type on every call: the server rejects non-JSON POSTs (CSRF guard).
    headers: { "Content-Type": "application/json", ...(init?.headers ?? {}) },
  });
  const text = await res.text();
  let data: unknown = {};
  if (text) {
    try {
      data = JSON.parse(text);
    } catch {
      data = { error: text };
    }
  }
  if (!res.ok) {
    // A 401 anywhere means the session ended: tell the app to show sign-in.
    if (res.status === 401) window.dispatchEvent(new Event("auth:signed-out"));
    const msg = (data as { error?: string }).error ?? res.statusText;
    throw new ApiError(res.status, msg);
  }
  return data as T;
}

const post = <T,>(path: string, body: unknown = {}) =>
  request<T>(path, { method: "POST", body: JSON.stringify(body) });

export const api = {
  me: () => request<Me>("/api/me"),
  logout: () => post("/auth/logout"),
  connections: () =>
    request<{ connections: ConnectionInfo[] }>("/api/connections").then((r) => r.connections ?? []),
  disconnect: (provider: string) => post(`/auth/${encodeURIComponent(provider)}/disconnect`),
  status: () => request<Status>("/api/status"),
  overview: () => request<Overview>("/api/overview"),
  actions: () => request<{ actions: Action[] | null }>("/api/actions").then((r) => r.actions ?? []),
  approve: (id: string) => post<{ action: Action }>(`/api/actions/${encodeURIComponent(id)}/approve`),
  reject: (id: string) => post<{ action: Action }>(`/api/actions/${encodeURIComponent(id)}/reject`),
  chat: (sessionId: string, message: string) => post<ChatReply>("/api/chat", { session_id: sessionId, message }),
  briefing: () => post<Briefing>("/api/briefing"),
  conversations: () =>
    request<{ conversations: ConversationSummary[] | null }>("/api/conversations").then((r) => r.conversations ?? []),
  conversation: (id: string) => request<Conversation>(`/api/conversations/${encodeURIComponent(id)}`),
  deleteConversation: (id: string) => post(`/api/conversations/${encodeURIComponent(id)}/delete`),
};
