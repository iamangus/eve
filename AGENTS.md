# AGENTS.md — guidance for opencode and other coding agents

## Commands

- Build frontend: `cd frontend && npm install && npm run build`
- Build backend: `go build -o eve ./cmd/server/`
- Run (dev): `air` (Go BFF reload) + `cd frontend && npm run dev` (Vite, proxies `/api` and `/runs` to :8090)
- Run (prod): `./eve` with env vars set (see README)
- Vet: `go vet ./...`
- Test: `go test ./...`

## Conventions

- Go 1.25.3, module path `github.com/iamangus/eve`.
- Standard library `net/http` with `http.ServeMux` path patterns (`GET /api/...`).
- `log/slog` text handler for logging.
- Chat conversations/messages, persistent-run IDs, and internal events persist
  in JSON under `DATA_DIR` (`internal/store`, `internal/events`). Email
  accounts, triggers, and run history persist to JSON in `DATA_DIR`
  (`internal/store/email.go`). The only external Go deps are
  `github.com/emersion/go-imap/v2` and `github.com/emersion/go-message`
  (IMAP polling + MIME parsing).
- Frontend: Svelte 5 (runes, `$state`/`$effect`), Vite 6, `marked` for markdown. Mirrors `../agentfoundry-ui/frontend`.
- Embed `frontend/dist` via `//go:embed all:dist` in `frontend/embed.go`.
- SPA fallback: serve `index.html` for any non-API path (see `internal/web`).
- No comments in code unless asked.

## Architecture constraints

- This service owns visible conversation history locally and tracks persistent
  AgentFoundry runs in `persistent_runs.json`. Web messages use
  `POST /api/v1/runs/{id}/inputs`; do not reconstruct visible history for each
  turn. Hidden OpenDev inputs go through the durable internal-events queue and
  never enter conversation messages. A web follow-up stays durable in the
  conversation until Foundry confirms a Temporal steering update incorporated
  it into the active turn; rejected updates become ordinary queued turns.
- The BFF proxies agentfoundry's SSE run-events stream verbatim to the browser.
  The reconciler is the single writer of final assistant text, based on the
  durable run/turn result; it also starts queued conversation inputs.
- A web message sent while a web-originated turn is in flight is injected into
  the running persistent run (`metadata.source=web`, folded in by agentfoundry
  at the next LLM boundary) instead of queued. Queueing remains the fallback
  for non-web active turns and injection failures; the active message stays
  the turn anchor, so the shared response keeps the original reply
  destination.
- Auth to agentfoundry is a single personal API key (`AGENTFOUNDRY_API_KEY`)
  sent as `Authorization: Bearer <key>`. No OIDC in this service (single-user).
- Conversation titles are truncated from the first user message at send time;
  there is no title-generating agent.
