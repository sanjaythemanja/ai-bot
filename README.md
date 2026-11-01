# Exec Assistant

An AI executive assistant for **Gmail** and **Google Calendar**. It triages your inbox, writes a daily brief, finds meeting slots and drafts replies. It's a Go backend built on **go-kit**, with a simple web UI embedded in the binary.

It works with **any LLM**: any OpenAI-compatible API (ChatGPT/OpenAI, OpenRouter, Groq, Together, Mistral, DeepSeek, or a local Ollama / LM Studio / vLLM) or Anthropic Claude. Its tools are also exposed over HTTP, so **any external agent** can use your mail and calendar, with the same approval rules.

## Run

```bash
cp .env.example .env   # set LLM_API_KEY + LLM_MODEL, and Google OAuth (or DEMO_MODE=true)
make run               # → http://localhost:8080
```

Example `.env` for ChatGPT:

```
LLM_PROVIDER=openai
LLM_API_KEY=sk-...
LLM_MODEL=<an OpenAI chat model that supports tool calling>
```

For a different OpenAI-compatible service, also set `LLM_BASE_URL`. For Claude, set `LLM_PROVIDER=anthropic`; the model defaults to `claude-opus-5-5`. The model you pick must support tool/function calling.

## Features

- **Chat**: ask things like "what needs me today?" or "find 30 min tomorrow with Priya". The model calls tools to look things up.
- **Daily briefing**: top priorities, today's schedule, what needs a reply, and FYI items.
- **Edit anything by chat**: change a meeting's title, time, location, description or attendees, or cancel it. Add people to a meeting and Google sends them the invite. Edit drafts (to, cc, bcc, subject, body), send or delete them, and add BCC to any email. Change something already waiting in Pending actions ("add Priya in CC on that email") without starting over.
- **Today panel**: a raw snapshot of unread mail and today's events. No AI involved.
- **Approval queue**: by default `send_email` and `create_event` are only *queued*, and nothing leaves your account until you click **Approve**. You can turn this off in `config.yaml`. `draft_email` saves a draft directly, since a draft sends nothing.
- **Undo window**: after you approve, the action waits `undo_seconds` (default 30, in `config.yaml`) with an **Undo** button before it runs. Closing the tab during the countdown leaves the action waiting rather than sending it.
- **Activity log**: every action is listed with what it did, how it ended, and why it exists (the request that led to it).
- **Follow-ups**: "who hasn't replied to me?" finds sent threads with no answer, and "what did I promise?" finds commitments ("I'll send it by Friday") in sent mail and, if Slack is connected, in Slack. The daily brief includes both.
- **Protected time**: "keep Tue/Thu 2–4pm for focus" or "never book before 10am" are saved as rules. Slot search skips them, and any booking that breaks a rule or clashes with a meeting shows a ⚠ warning on the approval card.
- **Explained sorting**: unread mail is marked important, normal or low, with the reason for each. Corrections ("Acme is always important", or the ⋯ menu on an email) are saved as rules that always win.
- **Writes like you**: before drafting, the assistant learns your greetings, sign-offs and length from your recent sent mail. Email approvals show which emails it learned from.
- **Voice**: tap the mic to speak a request; pausing sends it, and the reply is read aloud. This uses the browser's built-in speech recognition, so it works in Chrome, Edge and Safari but not Firefox.
- **Phone-friendly approvals**: a badge shows how many actions are waiting, and approval cards appear right under the reply in chat.
- **Live progress, in the background**: each message runs as a background job. The chat shows every step as it happens ("Read transcript: 3 of 5"), with a Stop button. You can close or reload the tab; it keeps running and the conversation picks it back up. A job interrupted by a server restart is marked so you can resend it.
- **Parallel tools**: when the model asks for several lookups at once (five transcripts, five emails), they run concurrently, at most 4 at a time, each with its own 90-second limit. Results go back in the original order. Tools that change something (sending, drafts, saved rules) still run one at a time, so duplicate checks stay reliable.
- **Checks its own claims**: if the model's reply says something was sent, queued or saved but no tool actually did it, the server makes the model act or correct itself (once) before you see the reply. When you ask to *send* an email it queues `send_email` for approval; it only saves a draft when you ask for a draft.
- **Clean email text**: line breaks the model wrote as literal `\n` become real line breaks in emails, drafts and Slack posts; an email still containing a placeholder such as `[Your Name]` is refused back to the model, which is given your display name (never your email address) for sign-offs.
- **Big results condensed**: a tool result over 12,000 characters (a long transcript or email) is summarised for your question by its own model call, in parallel with the others, before the main model sees it. Short results pass through untouched.

## Plugins

Every integration is a plugin (`internal/plugins/*`), and the core engine (`internal/assistant/tools.go`) never imports one. A plugin implements `plugin.Plugin` (a name and tools), plus only the optional capabilities that fit it:

| Interface | Purpose | Gmail | Calendar | Meet | Slack |
|---|---|---|---|---|---|
| `Plugin` | tools (reads run now; writes return a `Proposal`) | ✓ | ✓ | ✓ | ✓ |
| `Executor` | performs approved proposals of its kinds | ✓ | ✓ | – | ✓ |
| `Editor` | edits a proposal while it awaits approval | ✓ | ✓ | – | ✓ |
| `Deduper` | blocks near-duplicate pending proposals | ✓ | ✓ | – | ✓ |
| `Guide` | adds its own instructions to the AI's prompt | ✓ | ✓ | ✓ | ✓ |

The engine handles everything shared: approval (`require_approval`), the action queue, duplicate checks, `edit_pending_action`/`cancel_pending_action`, and routing an approved action to its plugin. **To add an integration:** write a package implementing the interfaces above and add it to the list in `cmd/server/main.go`. Nothing else changes.

### Slack

Each user connects **their own** Slack. The token is stored encrypted per user in `connections`, and the assistant reads and posts **as that user**. Tools: `list_slack_channels`, `read_slack_channel`, `search_slack`, `slack_mentions`, and `post_slack_message`, which queues for approval and can be edited or duplicate-checked like an email.

Setup:
1. Create an app at **api.slack.com/apps** (From scratch) in your workspace.
2. Under **OAuth & Permissions**, add the redirect URL `https://<your-app>/auth/slack/callback`.
3. Under **User Token Scopes**, add: `channels:read groups:read im:read mpim:read channels:history groups:history im:history mpim:history users:read search:read chat:write`.
4. Copy the **Client ID** and **Client Secret** into `SLACK_CLIENT_ID` / `SLACK_CLIENT_SECRET`, then restart.
5. In the app, click **Connect** next to Slack in the sidebar.

Slack requires an `https://` redirect URL, so connect from the deployed app (or an HTTPS tunnel). Demo mode includes a sample Slack workspace.

## Working with colleagues (Google Workspace)

| You ask | What the bot uses | Needs |
|---|---|---|
| "Is Sara free at 3pm?" / "When are Sara and Marcus both free tomorrow?" | `check_availability`, `find_common_free_slots` (Google free/busy) | Same Workspace. Busy times only, no details, no sharing needed. |
| "List Sara's meetings this week" | `list_events` with `calendar=sara@…` | Sara shares her calendar with you ("See all event details"), or your admin sets org sharing to full details |
| "Summarize yesterday's staff sync" | `get_meeting_transcript`, then the model summarizes | The meeting was on Google Meet with transcription on, and you organized it or the transcript was shared with you |

Being CEO doesn't grant access by itself; Google only shows what's shared with your account. Company-wide access (any employee's calendar) needs Workspace domain-wide delegation set up by an admin. That isn't built in yet, on purpose.

**One-time setup after upgrading:** the app now asks for two more Google permissions (free/busy and Meet transcripts):
1. In Google Cloud Console, enable the **Google Meet REST API** for your project.
2. In the app, **Log out** and sign in again, then allow the new permissions.

## Sign-in and users

Everyone signs in with Google first. The same consent grants Gmail and Calendar access, and each user only ever sees their own mail, calendar, chats and approvals.

- **Users** are stored in `users`, keyed by Google account ID. Each user's Google refresh token is **encrypted with AES-256-GCM** using `APP_SECRET` before it reaches the database.
- **Sessions** are random tokens in an `HttpOnly`, `SameSite=Lax` cookie (`Secure` on HTTPS). Only the token's SHA-256 is stored in `sessions`, so **Log out** ends the session server-side.
- **Isolation:** `conversations` and `actions` carry `user_id`, and every query filters on it. Approving or reading another user's item returns 404.
- **Who can sign in:** set `ALLOWED_EMAILS` to emails and/or `@yourcompany.com`. While the Google OAuth app is in Testing mode, Google also limits sign-in to the test users you add (up to 100).
- **Demo mode** skips Google and signs everyone in as a sample "Demo User".

Required settings: `APP_SECRET` (generate with `openssl rand -base64 48`; changing it means everyone has to sign in again), plus the Google OAuth client. Every `/api/*` route needs a session; external agents can use `/api/tools` with `AGENT_API_TOKEN`, acting as `AGENT_USER_EMAIL`.

## Running in Docker

```bash
docker build -t exec-assistant .
docker run --rm -p 8080:8080 --env-file .env -e DATABASE_URL=postgres://… exec-assistant
```

The image is three stages (Node builds the UI, Go builds a static binary, then distroless runtime). It runs as non-root with no shell, and `.env` is never copied in; pass settings with `--env-file` or your host's secret settings. `GET /healthz` is the health check.

**Production protections**
- **Security headers:** strict CSP, no framing, `nosniff`, and HSTS on HTTPS.
- **Rate limit:** `/api/chat` and `/api/briefing` are limited per user (`CHAT_PER_MINUTE`).
- **`/metrics`:** needs `METRICS_TOKEN`, or is local-only.
- **Logs:** tool inputs are left out unless `LOG_TOOL_INPUTS=true`.
- **No duplicate approvals:** when a near-identical email or event is already waiting, the assistant must edit or cancel that one instead of adding a copy.

## Web UI

The UI lives in `ui/`: React 19, TypeScript, Tailwind CSS 4 and Vite. Fonts (Newsreader and Public Sans) and icons (Lucide) are bundled, so there are no third-party requests at runtime. `make build` builds the UI into `web/dist`, and the Go binary embeds it.

```bash
make ui        # build the UI once (npm ci + vite build)
make build     # UI + Go binary
make ui-dev    # live-reloading UI on http://localhost:5173 (run ./bin/server alongside)
```

Light and dark themes follow the system; the button in the sidebar footer switches between system, light and dark.

## Conversation history (PostgreSQL)

Every chat is saved, including the model's internal messages, so you can reopen it later from the **Conversations** list and keep going. "New conversation" starts a fresh chat; old ones stay until you delete them (×, then click again to confirm).

```bash
make db-up        # start PostgreSQL in Docker (localhost:5432)
./bin/server      # picks up DATABASE_URL from .env
make db-psql      # SQL shell, e.g. SELECT id, title FROM conversations;
make db-test      # integration test against the running database
make db-down      # stop it (data is kept in the pgdata volume)
```

Tables: `conversations` (one row per chat), `messages` (the model history as JSONB, replayed to resume), `turns` (the transcript shown in the UI). The schema is applied automatically on startup. Without `DATABASE_URL`, history is kept in memory and lost on restart.

## Configuration

Secrets (API keys, Google OAuth) live in `.env`. App behaviour lives in `config.yaml`:

```yaml
require_approval: true   # false = send_email / create_event run immediately
```

| `require_approval` | What happens |
|---|---|
| `true` (default) | Writes wait in **Pending actions** until you click Approve or Reject. |
| `false` | Writes run at once. They're still listed in the panel, renamed **Actions log**. The AI's instructions and tool descriptions switch to "runs immediately", so it asks when details are unclear. |

You can override the file for one run with `REQUIRE_APPROVAL=false ./bin/server`, or point to another file with `CONFIG_FILE=path.yaml`. Restart the server after any change. The first log line prints `require_approval=…`.

With `false`, the assistant can send email on its own. Text hidden in an incoming email could try to steer it into doing that, so keep `true` unless you trust everything it reads. The same setting applies to external agents calling `/api/tools`.

## Architecture

```
cmd/server/main.go            wiring: providers → LLM → service → middleware → endpoints → HTTP
internal/llm/                 provider-neutral chat + tool-calling interface
  llm.go                      Client, Message, ToolSpec, config/auto-detect
  openai.go                   OpenAI-compatible Chat Completions (plain HTTP, any base URL)
  anthropic.go                Claude via the official SDK
internal/assistant/
  service.go                  go-kit Service interface + business logic
  middleware.go               service middleware: logging, Prometheus instrumentation
  endpoints.go                go-kit endpoints, request/response types, endpoint logging
  transport_http.go           go-kit httptransport servers, decoders, error encoding
  agent.go                    tool-use loop (works with any llm.Client)
  tools.go                    tool definitions → provider calls; writes become Actions
  actions.go                  approval queue (approve/reject resolves exactly once)
internal/provider/            Mail / Calendar interfaces
  google/                     OAuth2 + Gmail + Calendar
  mock/                       sample data for DEMO_MODE
internal/authhttp/            Google OAuth redirect flow
ui/                           React + TypeScript + Tailwind source (Vite)
web/dist/                     built UI, embedded into the binary (go:embed)
```

**Adding another LLM vendor**: implement `llm.Client` (one method, `Complete`) and add a case in `llm.New`.
**Adding an integration** (Slack, Outlook…): implement an interface in `internal/provider` and register its tools in `tools.go`.

## HTTP API

| Method | Path | |
|---|---|---|
| POST | `/api/chat` | `{session_id, message}` → `{reply, tool_calls}` (waits for the whole answer) |
| POST | `/api/chat/start` | same body → `{job}` at once; the turn runs in the background |
| GET | `/api/jobs/{id}` | a job's status, steps and, when done, its reply |
| GET | `/api/jobs/{id}/events` | Server-Sent Events: a job snapshot on every step until it ends |
| POST | `/api/jobs/{id}/cancel` | stop a running job |
| GET | `/api/conversations` · `/api/conversations/{id}` | conversation list, one transcript |
| GET | `/api/conversations/{id}/job` | the conversation's latest job (to resume after a reload) |
| POST | `/api/conversations/{id}/delete` | delete a conversation |
| POST | `/api/briefing` | generate the daily brief |
| GET | `/api/overview` | unread mail, today's events |
| GET | `/api/actions` | approval queue |
| POST | `/api/actions/{id}/approve` · `/reject` | resolve an action |
| GET | `/api/status` | AI provider/model and Google connection |
| GET | `/api/tools` | tool list with JSON Schemas (for external agents) |
| POST | `/api/tools/{name}` | call a tool with its JSON input → `{result}` or `{error}` |
| GET | `/metrics` · `/healthz` | Prometheus metrics, liveness |

### Tools the AI (and external agents) can use

| Area | Read | Write (follows `require_approval`) |
|---|---|---|
| Gmail | `search_emails`, `read_email`, `list_drafts`, `read_draft` | `send_email`, `send_draft`, `delete_draft` |
| Gmail (safe, no approval) | | `draft_email`, `update_draft` |
| Calendar | `list_events`, `get_event`, `find_free_slots` | `create_event`, `update_event`, `delete_event` |
| Queue | `list_pending_actions` | `edit_pending_action`, `cancel_pending_action` |

POSTs to `/api/*` must send `Content-Type: application/json`. This blocks cross-site form submissions (CSRF).

### Using it from another agent

Any agent framework can fetch `GET /api/tools` and register each entry as a function or tool. `input_schema` is standard JSON Schema. When the agent invokes a tool, forward the call:

```bash
curl -s localhost:8080/api/tools/search_emails \
  -H 'Content-Type: application/json' -H "Authorization: Bearer $AGENT_API_TOKEN" \
  -d '{"query":"is:unread newer_than:1d"}'
```

Tools with `needs_approval: true` don't execute; they land in the UI's approval queue. Set `AGENT_API_TOKEN` so only agents you give the token to can call these endpoints.

## Connecting Google

1. In Google Cloud Console, enable the **Gmail API** and **Google Calendar API**.
2. Configure the OAuth consent screen and add yourself as a test user.
3. Create an OAuth client of type **Web application**, with the redirect URI `http://localhost:8080/auth/google/callback`.
4. Put the ID and secret in `.env`, restart, and click **Continue with Google** on the sign-in page. Your token is stored encrypted in the database.

## Notes

- No personal data is hard-coded. Your email address only comes from Google at runtime, and demo data uses fictional `example.com` addresses.
- Sessions and the action queue live in memory and reset on restart.
- This is single-user with no app login. Keep it on localhost, or add auth before exposing it.
- Email content is treated as untrusted data in the system prompt, and every outbound write needs approval. Those two rules together are the main prompt-injection guard.
