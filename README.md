# eve

Chat interface and conversation owner for a single fixed agent that lives in
[agentfoundry](../agentfoundry). This service owns conversation history locally
(in-memory) and triggers agent runs in agentfoundry via its stateless run API.
agentfoundry remains responsible for the agent definition, tools/MCP servers,
LLM inference, and run dispatch (Temporal).

## Architecture

```
Browser ──HTTP──> eve BFF ──HTTP + Bearer API key──> agentfoundry
  (Svelte 5)     (Go, net/http, in-memory)    POST /api/v1/agents/{ASSISTANT_AGENT_ID}/run
                                           GET  /api/v1/runs/{run_id}/events (SSE)
```

- Conversations and messages are kept in memory.
- On send, the BFF reconstructs `History` (role+content) from stored messages
  and calls agentfoundry's **stateless** run path (`POST /api/v1/agents/{id}/run`
  with `history`, no `session_id`). agentfoundry never persists our history.
- The BFF proxies agentfoundry's SSE run-events stream verbatim to the browser;
  on the `done` event it persists the final assistant text in memory and clears
  the conversation's `active_run_id`. On `error` it clears `active_run_id`.
- On startup the BFF reconciles any conversations with an in-flight
  `active_run_id` by polling agentfoundry's `GET /api/v1/runs/{id}`.

## Leveraging agentfoundry for derived tasks

agentfoundry agents are cheap to spin up and composable. This service uses a
secondary small-model agent (configured in agentfoundry) to generate concise
conversation titles asynchronously after the first user message. Set
`TITLE_AGENT_ID` to the agent id of a lightweight title-generating agent; leave
it empty to fall back to a truncated first-message title.

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
- Accounts, triggers, and run history persist as JSON in `DATA_DIR`
  (`accounts.json`, `triggers.json`, `runs.json`). Account passwords are stored
  in plaintext in `accounts.json` — fine for a single-user self-hosted
  deployment, not for shared hosting. Run history is capped at the latest 1000
  runs. A `processed.json` set records delivered message IDs so a message is
  handled at most once even if it is re-fetched.

## Configuration

All configuration is via environment variables (no YAML).

| Variable | Default | Description |
|---|---|---|
| `LISTEN` | `:8090` | HTTP listen address |
| `AGENTFOUNDRY_URL` | `http://localhost:3000` | agentfoundry backend API URL |
| `AGENTFOUNDRY_API_KEY` | *(required)* | Personal API key created in agentfoundry (`POST /api/v1/api-keys`). Sent as `Authorization: Bearer <key>`. Runs are attributed to the key owner. |
| `ASSISTANT_AGENT_ID` | *(required)* | Agent id of the eve assistant agent in agentfoundry |
| `TITLE_AGENT_ID` | *(empty, optional)* | Agent id of a small-model agent used to generate conversation titles. If empty, titles are truncated from the first user message. |
| `HISTORIAN_AGENT_ID` | *(empty, optional)* | Agent id of the historian agent (see `definitions/historian.yaml` in agentfoundry). When set, eve compresses old history into tiered summaries, captures durable memories, and renders a decayed context within the budget. When empty, full raw history is sent. |
| `CONTEXT_BUDGET_TOKENS` | `64000` | Target size of the rendered conversation history in tokens. |
| `CONTEXT_TRIGGER_FRACTION` | `0.5` | Fraction of the budget at which the unsummarized tail triggers a historian run. |
| `CONTEXT_PROTECTED_TAIL_TOKENS` | `12000` | Newest raw history (tokens) that always stays raw, never summarized or truncated. |
| `CONTEXT_CHUNK_TOKENS` | `20000` | Max input size (tokens) of one historian run. |
| `CONTEXT_MEMORY_LIMIT` | `200` | Cap on the durable memory pool (oldest low-importance entries are curated away). |
| `CONTEXT_CURATE_INTERVAL` | `24h` | How often the memory pool is curated (Go duration). |
| `DATA_DIR` | `./data` | Directory for JSON persistence of email accounts, triggers, conversations, compartments, and memories |
| `EMAIL_POLL_INTERVAL` | `60s` | How often eve polls configured IMAP inboxes (Go duration, e.g. `30s`) |

## Build and Run

### Frontend

```bash
cd frontend
npm install
npm run build     # outputs frontend/dist (embedded via //go:embed)
```

### Backend

```bash
go build -o eve ./cmd/server/
AGENTFOUNDRY_API_KEY=... \
ASSISTANT_AGENT_ID=... \
AGENTFOUNDRY_URL=http://localhost:3000 \
./eve
```

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