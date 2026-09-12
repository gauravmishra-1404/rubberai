# rubberai

See what AI coding tools are doing across your projects: every prompt a
developer sends, which files it changed, how many tokens it used and what it
cost — from any IDE, coding agent or script, in one dashboard you host yourself.

- **Live demo:** https://ggmwpqxp6f.ap-south-1.awsapprunner.com/demo (read-only)
- **Website:** https://gauravmishra-1404.github.io/rubberai_landing/
- **Ready-made image:** `public.ecr.aws/g0m7w0k0/rubberai`

It is one Go binary and one PostgreSQL database. Nothing else — no message
queue, no cache, no cloud account, no LLM proxy. Any tool that can send an HTTP
request can report to it.

---

## 1. Run it

You need Docker. Nothing else is installed on your machine.

```sh
git clone https://github.com/gauravmishra-1404/rubberai.git
cd rubberai
cp .env.example .env
```

Open `.env` and set one value:

```
POSTGRES_PASSWORD=choose-any-random-password
```

Then start it:

```sh
docker compose up --build
```

Open **http://localhost:8080**. That's it — the database is created, the tables
are set up, and the dashboard is served, all by this one command.

> Running without Docker? See [Run without Docker](#7-run-without-docker).

## 2. Create a project and a key

1. On the sign-in page choose **Create an organization**. Enter a username,
   a password, your display name and an organization name, then **Create
   workspace**. The username can be an email address — if it is, its domain is
   checked for a mail server, but no verification mail is sent.
2. Click **Create project** and give it a name (one project per code
   repository works best).
3. Open the project's **Connect a project** panel and click **Generate key**.
   Copy the key now — it is shown only once. It starts with `rai_` and can only
   send events; it cannot read your data.

You now have two values every tool needs:

| Value | Looks like | Where it comes from |
|---|---|---|
| Project ID | `prj_1a2b…` | Shown on the project page |
| API key | `rai_9f8e…` | The key you just generated |

## 3. Connect your tools

Every tool connects the same way: it sends small JSON **events** to
`POST /api/v1/events` with the API key. rubberai does not care which agent,
IDE or language produced them — an unknown name is recorded, not rejected.
Pick whichever of these fits the tool you use.

### 3a. Any agent, IDE or script (the general way)

Send one HTTP request per thing that happened. This is the whole contract:

```sh
curl http://localhost:8080/api/v1/events \
  -H "Authorization: Bearer rai_YOUR_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "event_id":   "evt_001",
    "event_type": "prompt.created",
    "project_id": "prj_YOUR_PROJECT",
    "timestamp":  "2026-09-13T10:00:00Z",
    "user_id":    "alice",
    "prompt_id":  "turn_1",
    "trace_id":   "turn_1",
    "agent":      {"name": "my-agent"},
    "ide":        {"name": "terminal"},
    "prompt":     "add a retry to the upload"
  }'
```

Four fields are always required: `event_id` (unique, so a retry is never
counted twice), `event_type`, `project_id` and `timestamp`. Everything else is
optional and shows up in the dashboard when present.

The event types the dashboard understands:

| `event_type` | When to send it | Useful extra fields |
|---|---|---|
| `session.started` / `session.ended` | A tool session opens or closes | `session_id`, `repository` |
| `prompt.created` | **A human** submits a prompt | `prompt_id` (required), `prompt` |
| `llm.request.completed` | One model call finished | `model`, `usage` (tokens), `cost` |
| `tool.completed` / `tool.failed` | The agent ran a tool | `tool_name`, `status` |
| `file.created` / `file.modified` / `file.deleted` | A file changed | `file.path` (required), `file.diff`, `lines_added` |

Other names (for example `git.commit`) are accepted and stored as-is; they show
in a prompt's timeline but have no tile of their own.

Two rules keep the dashboard honest:

- Send `prompt.created` **only when a person typed something** — not for the
  agent's own internal steps. Use the same value for `prompt_id` and
  `trace_id`, and put that `trace_id` on every event the agent produces while
  working on that prompt. That is what groups one human request with all its
  tool calls, model calls and file changes into a single row.
- Report what you measured; leave out what you didn't. A missing token count
  shows as **Not reported**, never as zero.

Copy-and-run examples in
[Python](examples/send.py) · [JavaScript](examples/send.mjs) ·
[Java](examples/SendEvent.java); a dependency-free
[Python SDK](sdk/python/rubberai.py) with a bounded background queue; and the
full field list in [docs/api.md](docs/api.md) / [OpenAPI](docs/openapi.json).

### 3b. Claude Code (ready-made adapter)

The [Claude Code adapter](integrations/claude-code/README.md) is a single
Python file — no dependencies — that turns Claude Code's hooks into events:
prompts, tool calls, file diffs and token usage.

1. Create `~/.config/rubberai/claude-code.json` (and `chmod 600` it):

   ```json
   {
     "send_prompts": true,
     "send_diffs": true,
     "projects": {
       "/absolute/path/to/your/repository": {
         "url": "http://localhost:8080",
         "project_id": "prj_YOUR_PROJECT",
         "api_key": "rai_YOUR_KEY"
       }
     }
   }
   ```

   Add one entry per repository. A directory not listed here sends nothing.

2. Register the hook in `~/.claude/settings.json`, for each of `SessionStart`,
   `UserPromptSubmit`, `PostToolUse`, `PostToolUseFailure`, `Stop`, `SessionEnd`:

   ```json
   {"type": "command", "async": true, "timeout": 20,
    "command": "python3 /absolute/path/to/rubberai/integrations/claude-code/hook.py"}
   ```

3. Start Claude Code inside that repository and send a prompt. Refresh the
   dashboard — the prompt row appears when the turn finishes.

To check it is wired up, run the hook by hand with `RUBBERAI_DEBUG=1`; the
adapter README shows how. With prompts and diffs on, set the project's
collection mode to **Full** (step 5), otherwise the server keeps only metadata.

### 3c. Codex desktop (hook + OpenTelemetry)

Codex emits model usage and agent activity as OpenTelemetry logs, and has a
`UserPromptSubmit` hook for the human prompt. Both go through the local
collector (step 4), which accepts OTLP and translates it.

1. Start the collector as a hub with a connection named `codex` (step 4b).
2. Point Codex at it in `~/.codex/config.toml`:

   ```toml
   [otel]
   log_user_prompt = true

   [otel.exporter.otlp-http]
   endpoint = "http://127.0.0.1:4319/codex/v1/logs"
   protocol = "json"

   [otel.exporter.otlp-http.headers]
   Authorization = "Bearer <the codex connection's local token>"
   ```

3. Add a `UserPromptSubmit` hook in `~/.codex/hooks.json` that reads the hook
   JSON from stdin and POSTs a `prompt.created` event to
   `http://127.0.0.1:4319/codex/api/v1/events` with the same token, using
   `session_id` + `turn_id` as a stable `prompt_id` / `trace_id`.

   ```json
   {"hooks": {"UserPromptSubmit": [{"hooks": [{"type": "command",
     "command": "/usr/bin/python3 ~/.codex/hooks/rubberai_user_prompt.py",
     "timeout": 1}]}]}}
   ```

4. Restart Codex.

### 3d. Anything that speaks OpenTelemetry

If a tool can export OTLP HTTP/JSON logs, point it at the collector's
`/<connection>/v1/logs` endpoint exactly as in 3c. No adapter needed.

## 4. The collector (optional, recommended for daily use)

Tools can talk to the server directly. The **collector** is a small local
process you can put in between when you want two things:

- **Nothing lost when the server is down.** It writes every event to disk
  before answering, then retries in the background with the same IDs, so a
  restart or a network blip never loses a turn.
- **One place for many tools.** Each tool gets its own local token and its own
  route; none of them ever sees the real project key.

### 4a. Single tool

```sh
export RUBBERAI_URL='http://localhost:8080'
export RUBBERAI_API_KEY='rai_YOUR_KEY'
export RUBBERAI_LOCAL_TOKEN='any-random-string-of-32-or-more-characters'
./bin/rubberai-collector
```

Tools now send to `http://127.0.0.1:4319/api/v1/events` with
`Authorization: Bearer <local token>` instead of the server URL and key.

### 4b. Several tools (the hub)

Copy [`examples/collector-hub.json`](examples/collector-hub.json) to a private
location, fill in one connection per tool (name, local token, project key,
outbox directory), and start it:

```sh
export RUBBERAI_COLLECTOR_CONFIG="$HOME/.config/rubberai/collector.json"
./bin/rubberai-collector
```

A connection named `codex` then accepts events at
`http://127.0.0.1:4319/codex/api/v1/events` and OTLP logs at
`http://127.0.0.1:4319/codex/v1/logs`. Use one connection per tool/project
pair so a token for one tool can never write into another project. The hub
takes 1–16 connections and keeps every outbox private (`0700`).

The outbox holds up to 32 MiB / 1,000 requests; when full the collector answers
503 rather than dropping silently. `/api/v1/status` on the collector (with the
local token) shows the queue. Rejected payloads are kept as `.rejected` files
for inspection, so keep the outbox directory private.

## 5. Choose what gets stored (privacy)

Every project has a collection mode, set under **Privacy & settings**:

| Mode | Stores | Good for |
|---|---|---|
| **Metadata only** (default) | Counts, tokens, file paths, timings | Teams that must not keep source or prompts |
| **Redacted** | The above plus prompts with secrets masked | A middle ground |
| **Full** | Everything sent: prompt text, diffs, custom metadata | Personal use, demos, debugging |

Filtering happens **before** anything is written, so a metadata-only project
never has prompt text on disk. Changing the mode affects new events only.
Diffs additionally need **Allow diffs in full collection mode** ticked on the
project and `send_diffs` on in the adapter.

## 6. Configuration

Everything is an environment variable. With Docker Compose, set them in `.env`.

| Variable | Required | Default | What it does |
|---|---|---|---|
| `POSTGRES_PASSWORD` | yes (Compose) | — | Password for the bundled database |
| `DATABASE_URL` | yes (without Compose) | — | Postgres connection string, e.g. `postgres://user:pass@host:5432/rubberai?sslmode=disable` |
| `APP_ORIGIN` | no | `http://localhost:8080` | The URL people open the dashboard at. Use your `https://…` address in production — cookies become Secure automatically |
| `LISTEN_ADDR` | no | `127.0.0.1:8080` | Where the server listens. Use `0.0.0.0:8080` inside a container or behind a proxy |
| `PRICING_JSON` | no | `{}` | Per-model token prices for cost estimates — see step 8 |
| `METRICS_TOKEN` | no | off | If set, `GET /api/v1/metrics` returns operational counters to callers presenting this token |
| `DEMO_PROJECT_ID` | no | off | If set, `GET /demo` opens a read-only, one-hour session on that one project (this is how the public demo works) |
| `KEY_REQUESTS_PER_MINUTE` / `PROJECT_REQUESTS_PER_MINUTE` / `ORG_REQUESTS_PER_MINUTE` | no | 600 / 3000 / 10000 | Ingestion rate limits |

Collector variables: `RUBBERAI_URL`, `RUBBERAI_API_KEY`, `RUBBERAI_LOCAL_TOKEN`,
`RUBBERAI_OUTBOX` (default `~/.local/state/rubberai/outbox`) for a single
tool, or `RUBBERAI_COLLECTOR_CONFIG` for the hub file.

**Production checklist:** put the server behind HTTPS (a reverse proxy or a
managed platform), set `APP_ORIGIN` to that HTTPS URL, use a managed or backed-up
Postgres, and run **one** instance — rate limiting is in-memory. Docker Compose
as shipped binds to localhost only and does not expose the database.

## 7. Run without Docker

Needs Go 1.27+, Node.js 22.12+ and PostgreSQL 17+.

```sh
make build                                   # builds the dashboard and both binaries into bin/
export DATABASE_URL='postgres://USER:PASSWORD@localhost:5432/rubberai?sslmode=disable'
export APP_ORIGIN='http://localhost:8080'
./bin/rubberai
```

The database tables are created on first start. For live-reload of the
dashboard while developing, run `npm run dev` inside `web/` and set
`APP_ORIGIN=http://localhost:5173`; Vite proxies API calls to port 8080.

## 8. Cost estimates

rubberai never assumes a price. Give it your rates per million tokens, keyed by
`provider/model`, and it estimates the cost of every request that reports
tokens:

```json
{"anthropic/claude-opus-5": {
   "input_per_million": "15", "output_per_million": "75",
   "cache_read_per_million": "1.5", "cache_write_per_million": "30",
   "currency": "USD", "version": "anthropic-list-2026-09"}}
```

Put that (on one line) in `PRICING_JSON`. Cache reads and writes are priced at
their own rates — a cache-heavy request priced entirely at the input rate would
be overstated several times over. If a request reports cache tokens and no
cache rate is configured, it is left unpriced rather than priced wrong. A cost
sent by the tool itself always wins over an estimate. Estimates and actual
costs are kept apart, per currency, and summed exactly.

## 9. How it works

```
  IDE / agent / script ──► adapter ──► (collector) ──► POST /api/v1/events
                                                              │
                                     auth → rate limit → validate → price
                                       → privacy filter → idempotent insert
                                                              │
                                                         PostgreSQL
                                                              │
                                              analytics API ──► embedded dashboard
```

- **Events are immutable.** A session, trace or prompt is just "every event
  sharing that id" — nothing is edited after the fact; a correction is a new
  event.
- **Retries are safe.** `(project_id, event_id)` is the primary key, so sending
  the same event twice counts once.
- **Privacy is applied on the way in**, not when displaying.
- **Attribution is honest.** A change claimed as AI-made without evidence is
  shown as UNKNOWN; the platform reports what tools assert and does not guess.
- **Money is exact.** Decimal strings summed as rationals, never floats.

[docs/architecture.md](docs/architecture.md) explains the decisions;
[docs/api.md](docs/api.md) is the contract; [docs/specification.md](docs/specification.md)
is the full product spec.

## 10. Tests

```sh
export TEST_DATABASE_URL='postgres://USER:PASSWORD@localhost:5432/rubberai_test?sslmode=disable'
make test        # Go unit + integration tests (the integration test is skipped without the variable)
make check       # vet + formatting
cd web && npm run test:e2e      # browser tests, with the app running locally
```

Adapter tests: `python3 -m unittest` inside `integrations/claude-code/`.

## 11. Current limits

Accounts are one organization each — invitations and password recovery are not
built yet. Nothing prunes old events; retention is manual. Aggregates show up
to 100 groups and event pages up to 1,000 rows. Rate limits are per instance.
User identity in events is whatever the tool asserts, not a verified login.

## License

MIT — see [LICENSE](LICENSE). The rubberai name and logo are not covered by the
license; see [TRADEMARK.md](TRADEMARK.md).
