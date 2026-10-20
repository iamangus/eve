# eve

Chat interface and conversation owner for a single fixed agent that lives in
[agentfoundry](../agentfoundry). This service owns visible conversation history
locally and uses AgentFoundry persistent runs for agent state. AgentFoundry
remains responsible for the agent definition, tools/MCP servers, LLM inference,
and run dispatch (Temporal).

## Architecture

```
Browser ──HTTP──> eve BFF ──HTTP + Bearer API key──> agentfoundry
  (Svelte 5)     (Go, net/http, JSON state)   POST /api/v1/agents/{ASSISTANT_AGENT_ID}/runs
                                             POST /api/v1/runs/{run_id}/inputs
                                             GET  /api/v1/runs/{run_id}/events (SSE)
```

- Each web conversation has a tracked frontend persistent run. A visible user
  message is saved locally and submitted as an input to that run; Eve does not
  reconstruct or submit local history on each turn. The conversation scope is
  a stable idempotency key when creating the run.
- A web follow-up during an active web turn is held durably while Eve requests
  a Temporal steering update. The worker accepts it only while the current turn
  can incorporate it, then revises that turn's answer. An unconfirmed update is
  retried with the same input ID; a rejected update becomes the next queued
  turn.
- Owner messages from web, email, Matrix, SMS, and voice share the same
  persistent conversation run. Each turn is queued durably in the conversation
  store and submitted in message order with its message-derived input ID. A
  replacement run is bootstrapped from earlier conversation context. The reply
  destination belongs to the message that started the turn.
  Matrix retries use the same transaction ID, and email retries carry a stable
  Message-ID. SMTP delivery remains at-least-once across an ambiguous server
  acknowledgement; receivers decide whether to collapse repeated Message-IDs.
  A failed channel delivery keeps its turn active for reconciliation with
  persisted exponential backoff instead of recording an undelivered reply as
  complete; later turns wait in the queue.
- Channel senders are matched by their configured `{type,address}` in
  `DATA_DIR/identities.json`; a display name on an unrelated channel does not
  establish owner identity. Authenticated SMS and voice webhooks with no
  `sender` use the owner device default. Explicit webhook senders must match
  a configured channel address to trigger an owner turn.
- During upgrade, an old `frontend:<conversation>` run is canceled once idle.
  New owner turns use `frontend-v2:<conversation>` and the restricted
  `eve-chat` MCP server; automation uses separate runs and the full `eve`
  MCP server.
- Backend automation has its own tracked backend persistent run. OpenDev
  webhook events are held in a leased, durable queue and submitted there as
  hidden inputs, never as visible conversation messages. A `frontend` event is
  likewise hidden but targets a separate frontend-event run for the specified
  conversation, preserving the web chat run's restricted tool set. Eve
  completes a leased event only after Foundry reports that the agent processed
  its input; timed-out deliveries retry with the same input ID and backoff.
  Confirmed agent failures rotate the input ID for a fresh attempt. Five
  confirmed failures leave a durable failed record with its last error.
  The authenticated event-status endpoint lets OpenDev retain its outbox entry
  until Eve records `done`, rather than acknowledging only webhook acceptance.
- The BFF proxies agentfoundry's SSE run-events stream verbatim to the browser.
  Its reconciler persists the final assistant text or error in the local JSON
  conversation store and clears the matching active run.
- On startup the BFF reconciles any conversations with an in-flight
  `active_run_id` by polling agentfoundry's `GET /api/v1/runs/{id}`. Expired
  queue leases are eligible for delivery again after restart.

### Reliability and privacy

- Authentication remains an external deployment concern; Eve does not add a
  login or MCP authentication layer. Do not expose it to untrusted networks.
- A Matrix room may use the private owner conversation only when a fresh
  membership lookup finds exactly two joined members: Eve and a configured
  owner Matrix identity. Membership is checked again before delivery. Shared
  rooms are not given private context; rejected inbound events remain visible
  in `GET /api/inbox/matrix`. Unknown membership never authorizes delivery.
- Matrix events are queued before their sync cursor is committed. Missing keys
  and transient storage/network failures retry durably. Replay indices and
  encryption state survive restarts. Previously encrypted rooms never silently
  downgrade to plaintext.
- Proactive messages are accepted only after a durable outbox write. Busy or
  unavailable channels retry with backoff; `GET /api/deliveries` exposes status
  and errors. Supply `delivery_id` to `POST /api/notify` or `send_message` when
  retrying the same notification. Accepted means queued, not delivered.
- Task replies use distinct persistent attempt IDs. Completed attempts are
  applied once; stale outcomes cannot overwrite replies or cancellation.
- A data-directory lock prevents concurrent Eve processes. JSON writes sync
  both the file and containing directory. Conversation deletion is journaled
  and resumed after restart, including derived memories and associated tasks.
- History APIs default to 200 messages; `before_id` and `limit` (1–1000) page
  backwards. The browser offers **Load earlier messages**. The Context tab
  describes local bootstrap history, not the live token usage of an existing
  persistent AgentFoundry run. Task tools query current task state explicitly.

## Leveraging agentfoundry for derived tasks

agentfoundry agents are cheap to spin up and composable. Conversation titles
are truncated from the first user message; there is no title-generating agent.

Near-term extensions along the same pattern (not yet implemented):

- **Topic tagging / smart suggestions**: a small agent that tags
  conversations or suggests follow-up prompts.

## Email triggers

eve can watch IMAP inboxes and run the assistant agent when incoming mail
matches a configured trigger.

- Add an **email account** (IMAP host, port, credentials) from the UI and test
  the connection.
- Create **triggers** against an account with `contains` filters on `sender`,
  `recipient`, `subject`, or `body`. All of a trigger's filters must match.
  Each trigger also has a user **prompt**.
- On every poll (`EMAIL_POLL_INTERVAL`), new mail (UID greater than the last
  seen) is matched against each enabled trigger of its account. On a match eve
  runs `ASSISTANT_AGENT_ID` with the trigger prompt plus the email details and
  records the outcome in a **runs** log (status, agent result, error).
- The first poll of a new account only ingests the last 7 days of mail.
- Accounts, triggers, cursors, processed IDs, and run history persist atomically
  in `DATA_DIR/email_state.json`. Legacy `accounts.json`, `triggers.json`,
  `runs.json`, and `processed.json` are imported when no combined state exists;
  the originals are left untouched and are no longer authoritative after the
  first successful write. Back up before upgrading. Account passwords remain
  plaintext in the protected state file; protect the directory and backups.
- At most 1000 terminal/history records are retained, without evicting pending
  or running jobs. Accepted trigger attempts survive restart and are dispatched
  with stable idempotency keys. IMAP cursors advance only after durable
  acceptance and track UIDVALIDITY. Fetches are batched and bodies are bounded.
- Owner-command email requires both an owner identity match and a passing,
  aligned DMARC result from the account's configured `trusted_authserv_id`.
  Configure this in the account editor only when the trusted mail gateway
  strips forged Authentication-Results headers and enforces sender policy.
  Without that configuration, email can match automation rules but cannot
  impersonate an owner conversation input. From-address matching alone is not
  authentication. Do not use a sender substring trigger as authorization.

## Configuration

All configuration is via environment variables (no YAML).

| Variable | Default | Description |
|---|---|---|
| `LISTEN` | `:8090` | HTTP listen address |
| `AGENTFOUNDRY_URL` | `http://localhost:3000` | agentfoundry backend API URL |
| `AGENTFOUNDRY_API_KEY` | *(required)* | Personal API key created in agentfoundry (`POST /api/v1/api-keys`). Sent as `Authorization: Bearer <key>`. Runs are attributed to the key owner. |
| `ASSISTANT_AGENT_ID` | *(required)* | Agent id of the eve assistant agent in agentfoundry |
| `ROUTER_AGENT_ID` | *(empty, optional)* | Agent id of the message router (see `definitions/router.yaml` in agentfoundry). When set, proactive and async sends are routed by this agent; when empty, delivery falls back mechanically to the best reachable output channel. |
| `EVEMCP_URL` | `http://localhost:8090/mcp` | URL agentfoundry dials to reach eve's MCP server. Attached as an ephemeral server to automation-triggered runs (task transitions), exposing the full tool surface — `send_message`, `list_channels`, task tools, calendar tools — so Eve can decide whether to proactively contact the user. In Docker, use `http://eve:8090/mcp`. |
| `EVEMCP_CHAT_URL` | `http://localhost:8090/mcp/chat` | URL agentfoundry dials for user-triggered runs (web chat, email, Matrix, SMS, voice). Serves the chat surface: task + calendar tools but **no** `send_message`/`list_channels` — the user triggered Eve, so her reply is delivered mechanically back to the origin channel and she never routes her own reply mid-conversation. Defaults to the same host as `EVEMCP_URL` with the path swapped to `/mcp/chat`. |
| `PROACTIVE_ENABLED` | `true` | Whether Eve may deliver messages outside a conversation turn (notifications, reminders, questions via `send_message`). |
| `PROACTIVE_COOLDOWN` | `2m` | Minimum gap between proactive messages (Go duration). |
| `TASK_POLL_INTERVAL` | `2s` | How often eve polls background task runs in agentfoundry (Go duration). |
| `TASK_HISTORY_RETENTION` | `0s` | Optional time after which reported terminal tasks move to individual files under `tasks-archive/`. Zero disables automatic archiving. Active/unreported tasks and pending cancellations are never archived. |
| `EVENT_HISTORY_RETENTION` | `720h` | Completed events move from the hot JSON queues to indexed individual records after 30 days. Pending, leased, and failed events are never automatically removed. |
| `EVENT_DEDUP_RETENTION` | `2160h` | Completed event deduplication/status records expire after 90 days. Must be at least `EVENT_HISTORY_RETENTION`. Retrying an ID after this window may execute it again. |
| `TASK_DECISION_AGENT` | *(empty, optional)* | Agent id Eve runs on task transitions (normally `ASSISTANT_AGENT_ID`). The run gets the full MCP surface (`send_message` included) and Eve decides autonomously whether to contact the user; no reply text is ever delivered mechanically. When empty, transitions are surfaced without a decision run. |
| `WEB_PRESENCE_TIMEOUT` | `60s` | How long the web channel counts as "present" after the last heartbeat or message. Drives routing decisions for proactive sends. |
| `HISTORIAN_AGENT_ID` | *(empty, optional)* | Agent id of the historian agent (see `definitions/historian.yaml` in agentfoundry). When set, Eve summarizes old history and captures durable memories. When empty, history stays raw, subject to bootstrap safety limits. |
| `CONTEXT_BUDGET_TOKENS` | `64000` | Target size of the rendered conversation history in tokens. |
| `CONTEXT_TRIGGER_FRACTION` | `0.5` | Fraction of the budget at which the unsummarized tail triggers a historian run. |
| `CONTEXT_PROTECTED_TAIL_TOKENS` | `12000` | Newest raw history (tokens) that always stays raw, never summarized or truncated. |
| `CONTEXT_CHUNK_TOKENS` | `20000` | Max input size (tokens) of one historian run. |
| `CONTEXT_MEMORY_LIMIT` | `200` | Cap on the durable memory pool (oldest low-importance entries are curated away). |
| `CONTEXT_CURATE_INTERVAL` | `24h` | How often the memory pool is curated (Go duration). |
| `DATA_DIR` | `./data` | Directory for JSON persistence of email accounts, triggers, conversations, persistent-run IDs, compartments, memories, and internal events |
| `OPENDEV_WEBHOOK_TOKEN` | _unset_ | Enables authenticated `POST /api/inbound/opendev` (Bearer token or `?token=`). Payload is `{"id":"event-id","category":"backend"|"frontend","payload":{...}}`; it is durably queued and never added to UI conversation history. Frontend events require `payload.conversation_id`. |
| `EMAIL_POLL_INTERVAL` | `60s` | How often eve polls configured IMAP inboxes (Go duration, e.g. `30s`) |
| `SMTP_HOST` | _unset_ | SMTP server for outbound email. When set, the email channel is enabled: Eve's `send_message` and proactive notifications can be delivered by email. Owner emails routed into the primary conversation (full duplex) still require SMTP config. |
| `SMTP_PORT` | `587` | SMTP port (STARTTLS; use `465` for implicit TLS). |
| `SMTP_USERNAME` | _unset_ | SMTP auth username (empty disables auth). |
| `SMTP_PASSWORD` | _unset_ | SMTP auth password. |
| `SMTP_FROM` | _unset_ | Sender address used on outbound mail, e.g. `eve@example.com`. |
| `SMTP_TO` | _unset_ | Optional proactive email destination; otherwise the configured owner's email channel is used. Never defaults to the bot sender address. |
| `MATRIX_HOMESERVER` | _unset_ | Matrix homeserver base URL, e.g. `https://matrix.example.com`. When set, the matrix channel is enabled: Eve's `send_message` and proactive notifications can be delivered to a room, and incoming `m.room.message` events are ingested into the primary conversation. |
| `MATRIX_ACCESS_TOKEN` | _unset_ | Matrix client access token for the bot account. |
| `MATRIX_USER_ID` | _unset_ | Matrix user ID of the bot. Startup discovers the canonical user/device from the token; bot messages are never re-ingested. |
| `MATRIX_OWNER_ROOM` | _unset_ | Optional proactive room preference. It must still contain exactly Eve and the owner. Without it, Eve discovers a qualifying joined room. |
| `MATRIX_PICKLE_KEY` | _unset_ | Optional key protecting the matrix E2EE crypto store (`eve_matrix.json`). When unset it is derived from `MATRIX_ACCESS_TOKEN`, so the key stays stable across restarts without extra config. |
| `CALDAV_URL` | _unset_ | CalDAV server base URL. When set, the calendar channel is enabled: Eve gets `get_calendar` / `create_event` / `update_event` / `delete_event` / `free_busy` MCP tools, and a poller fires proactive reminders for events starting within `CAL_REMINDER_LEAD`. Quiet hours are expressed as daily recurring events. |
| `CALDAV_USERNAME` | _unset_ | CalDAV basic-auth username. |
| `CALDAV_PASSWORD` | _unset_ | CalDAV basic-auth password. |
| `CALDAV_CALENDAR_PATH` | _unset_ | Path of the calendar collection relative to `CALDAV_URL` (e.g. `calendars/eve/default`). |
| `CAL_REMINDER_LEAD` | `15m` | How far ahead the calendar poller looks to fire reminders (Go duration). |
| `SMS_WEBHOOK_TOKEN` | _unset_ | Token guarding the SMS inbound webhook `POST /api/inbound/sms`. When set, the SMS channel is registered as input-only: messages land in the primary conversation and replies/notifications are routed to whichever output channel is reachable (there is no SMS gateway in v1). Auth via `?token=` query param or `Authorization: Bearer <token>`. |
| `VOICE_WEBHOOK_TOKEN` | _unset_ | Token guarding the voice-device inbound webhook `POST /api/inbound/voice`. Same behavior as SMS: input-only, with an optional `transcript` field for STT payloads, exercising the fallback router since a speakerless device cannot receive Eve's answer. |

The web UI has a **Channels** tab (`GET /api/channels`) showing every registered channel's capabilities, presence (`connected`, last activity, which lapses after `WEB_PRESENCE_TIMEOUT`), and the health of each background poller (email/matrix/calendar — last check time and last error). It refreshes every 5s and live-updates via the `/api/events` stream when presence changes.

Secrets `AGENTFOUNDRY_API_KEY`, `SMTP_PASSWORD`, `MATRIX_ACCESS_TOKEN`,
`MATRIX_PICKLE_KEY`, `CALDAV_PASSWORD`, and webhook tokens also support a
`_FILE` variant containing the secret, for example `SMTP_PASSWORD_FILE`.
Setting both forms is an error. Invalid settings fail startup rather than
silently falling back. `BACKEND_AGENT_ID` defaults to `ASSISTANT_AGENT_ID`.

User-triggered runs attach the restricted server as `eve-chat`; automation
runs attach the full MCP server as `eve`. The distinct names let concurrent
runs use different endpoints without replacing each other's connections.

CalDAV uses discovery when no collection path is configured (exactly one
calendar must be found), otherwise the configured collection. Updates and
deletes retain resource hrefs and require ETags. Event updates affect the
series master and preserve recurrence exceptions. iCalendar parsing supports
IANA time zones, DST, recurrence rules, exclusions, and folded/escaped text;
expansion is bounded. Reminder polling is at least every 30 seconds (faster for short leads), independently of
lead time, and every due occurrence is durably enqueued.

`GET /healthz` reports process liveness; `GET /readyz` reports whether Eve is
accepting requests. Dependency outages do not prevent durable acceptance.
Authenticated OpenDev event inspection/retry uses
`GET /api/inbound/opendev/events` and
`POST /api/inbound/opendev/events/{id}/retry`. Failed Matrix inbox entries can
be retried with `POST /api/inbox/matrix/{id}/retry` after resolving their cause.

## Build and Run

OpenDev CI runs on draft pull requests.

### Frontend

```bash
cd frontend
npm install
npm run build     # outputs frontend/dist (embedded via //go:embed)
```

### Backend

Use Go **1.26.9 or a newer patched toolchain**. If your environment sets
`GOTOOLCHAIN=local`, update Go or explicitly use `GOTOOLCHAIN=go1.26.9`.
`make build`, `make test`, `make e2e`, `make vet`, and `make audit` are the canonical
commands; `make check` runs all checks. Both local and CI builds use `goolm`.
Install the browser test runtime once with
`cd frontend && npx playwright install chromium`. CI also tests a fresh
container volume and a stopped-store backup/restore before image publishing.
The `docker` workflow calls the reusable Eve checks once, then builds the image
only after they pass. OpenDev CI remains a separate PR check; `ci.yml` has no
independent push/PR trigger, avoiding a duplicate full test run.
Routine Dependabot updates are grouped into at most one open PR per ecosystem
(Go, frontend, Actions, and Docker). Weekly checks are staggered Monday through
Thursday rather than all starting on the same day. Automatic
rebases are disabled to avoid restarting CI after every base-branch change;
update/rebase dependency PRs explicitly when needed. Security updates remain
enabled and are not constrained by the routine-update PR limit. Existing
individual PRs and running jobs are not manually closed/cancelled as part of
this change.
The process-level restore test can be run without Docker:
`python3 scripts/smoke_restore.py /absolute/path/to/the/built/eve`.
It uses isolated temporary data and no external accounts. The complete numbered
implementation audit and remaining live-validation limits are in
[`docs/review-implementation.md`](docs/review-implementation.md).

```bash
go build -tags goolm -o eve ./cmd/server/
AGENTFOUNDRY_API_KEY=... \
ASSISTANT_AGENT_ID=... \
AGENTFOUNDRY_URL=http://localhost:3000 \
./eve
```

> The matrix E2EE channel requires the pure-Go Olm implementation, so local
> builds must pass the `goolm` build tag (the Docker image does this
> automatically). Without it the build falls back to cgo `libolm`, which the
> CGO_ENABLED=0 image cannot link.

### Dev (live reload)

```bash
# terminal 1 — Go BFF with air
air
# terminal 2 — Vite dev server (proxies /api + /runs to :8090)
cd frontend && npm run dev
```

Open the Vite dev URL (default http://localhost:5173).

### Docker

```bash
docker build -t eve .
docker run -p 8090:8090 \
  -e AGENTFOUNDRY_URL=http://host.docker.internal:3000 \
  -e AGENTFOUNDRY_API_KEY=... \
  -e ASSISTANT_AGENT_ID=... \
  -e DATA_DIR=/data \
  -v $(pwd)/data:/data \
  eve
```

## Store

### Backup and restore

1. Stop Eve and wait for shutdown; never copy a live multi-file store as a
   supposedly consistent backup.
2. Back up the entire `DATA_DIR`, including crypto metadata, inbox/outbox
   subdirectories, deletion journals, and `email_state.json`. Encrypt backups.
3. Restore into a dedicated directory readable/writable only by Eve's runtime
   user. Preserve the Matrix pickle key independently; rotating the access
   token without an explicit stable pickle key changes the derived key.
4. Start one Eve instance. Verify health, task/queue statuses, and an owner
   channel reply before resuming automation. Do not run an older version
   against a directory migrated to `email_state.json`.

Completed queue payloads are discarded after successful acknowledgement while
deduplication tombstones remain queryable for `EVENT_DEDUP_RETENTION` (90 days
by default). Records older than `EVENT_HISTORY_RETENTION` (30 days) move to
individual indexed files under each queue's `completed/` directory, reducing
the hot store's rewrite costs. Inspection lists and queue metrics cover the hot
store; the individual event status API also looks up archived records. Failed
or unfinished work never expires automatically. Conversation history and tasks are not
silently expired. The task board can archive reported terminal tasks explicitly,
or `TASK_HISTORY_RETENTION` can move old reported results out of the hot store.
Archived results remain readable by task ID and from the paginated archive
view; no task data is deleted. Conversations can be exported from Chat before
explicit deletion. `go test -tags goolm ./internal/store -run '^$'
-bench BenchmarkConversationAppend -benchtime=1x` measures whole-file write
cost; large deployments should plan an indexed/transactional storage migration.

Matrix keys and replay-protection state intentionally do not expire
automatically: removing them can make retained history undecryptable or weaken
replay protection. Include them in protected backups and budget disk space for
retained encrypted history. Conversation exports followed by explicit deletion
and task archival are deliberate, non-silent history retention controls.

SMTP delivery is **at least once**, not exactly once. A disconnect after DATA
but before its acknowledgement is ambiguous: retries retain the same
Message-ID, but receiving servers need not deduplicate it. The delivery error
reports this uncertainty instead of claiming success. A disconnect during QUIT
does not invalidate a successful DATA acknowledgement.

Existing persistent runs can call `get_context_snapshot` for current durable
memories and `list_tasks` for current task state. They are not silently rebuilt
each turn. The Context page separately reports renderer estimates and the
next-run bootstrap preview after task context and final message/byte limits;
neither is advertised as live AgentFoundry token usage.

Conversations live in a Go map guarded by a sync.RWMutex (`internal/store`)
and persist as JSON in `DATA_DIR` (`conversations.json`,
`compartments.json`, `memories.json`) via atomic tmp-file + rename writes,
so history survives restarts. Compartments hold the tiered summaries
produced by the historian (raw messages are retained in
`conversations.json`); memories are the durable facts promoted from
compartment manifests.

```go
type Conversation struct {
    ID          string
    Title       string
    CreatedAt   time.Time
    UpdatedAt   time.Time
    ActiveRunID string
    Messages    []Message
}

type Message struct {
    ID        int64
    Role      string   // "user" | "assistant"
    Content   string
    RunID     string
    CreatedAt time.Time
}
```
