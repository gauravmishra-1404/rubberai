# rubberai

Understand AI coding activity across projects, agents, IDEs and programming languages.

**https://gauravmishra-1404.github.io/rubberai/** · [deployment notes](docs/deploy.md)

A Go modular monolith receives versioned HTTP events, stores them in PostgreSQL,
and serves an embedded Svelte dashboard. An optional Go collector buffers events
on disk. No LLM proxy, Kafka, Redis, external analytics database or cloud account
is required.

## Architecture

Integrations send versioned HTTP JSON to one Go binary, which validates, prices,
applies privacy rules and stores each event in PostgreSQL. The Svelte dashboard is
compiled and embedded into that same binary, so a deployment is one process plus a
database — no separate frontend server, message broker or analytics store.

```
  IDE / coding agent / CLI / any HTTP client
                  |
                  v
        adapter (agent-specific)          integrations/
                  |  standard event
                  v
        collector (optional, buffers to disk)   cmd/collector
                  |
                  v  POST /api/v1/events   Bearer <project key>
  +---------------------------------------------------+
  |  auth -> rate limit -> validate -> price ->        |   internal/platform
  |  privacy -> idempotent insert                      |
  +---------------------------------------------------+
                  |
                  v
             PostgreSQL  (events + JSONB payload)
                  |
                  v
        query / analytics  ->  embedded Svelte dashboard
```

Adapters are the only agent-aware code. They translate a tool's own events into
the standard schema; the server has no branch on IDE, agent or language, and an
unknown value is recorded rather than rejected. The
[Claude Code adapter](integrations/claude-code/README.md) is the reference
implementation.

Sessions, traces and prompts are **projections of immutable events**, not
separately editable records — a trace is every event sharing a `trace_id`. That is
why ingestion stays a single insert, and why correcting history means sending a
new event rather than mutating an old one.

Correctness properties the design depends on:

- **Idempotency** — `(project_id, event_id)` is the primary key, so a client may
  safely retry any request after a timeout without double-counting usage.
- **Privacy before persistence** — payloads are filtered on the way in, so a
  project set to metadata-only never stores prompt text at all.
- **Honest attribution** — an `AI` change source without evidence is downgraded to
  `UNKNOWN`; the platform reports what integrations assert, and does not infer.
- **Exact money** — costs are decimal strings summed as rationals, kept separate
  by currency and by actual/estimated.

[docs/architecture.md](docs/architecture.md) records the decisions and their
trade-offs; [docs/api.md](docs/api.md) is the event contract.

## Start locally

```sh
cp .env.example .env
# Set POSTGRES_PASSWORD to a random URL-safe password in .env.
docker compose up --build
```

Open http://localhost:8080. Create an organization account, create a project,
then generate a key under **Connect a project**. Keys are shown once and only
grant event ingestion. Use the account's user ID or your own external user ID in
events. Email is not required.

Default collection is **METADATA_ONLY**. Change it under **Privacy & settings**
when needed. Changing the policy affects future events, not already stored data.

## Native development

Requires Go 1.27+, Node.js 22.12+, npm and PostgreSQL 17+.

```sh
make build
export DATABASE_URL='postgres://USER:PASSWORD@localhost:5432/rubberai?sslmode=disable'
export APP_ORIGIN='http://localhost:8080'
./bin/rubberai
```

The server defaults to 127.0.0.1:8080. `LISTEN_ADDR` overrides it. For Vite hot
reload, run `npm run dev` inside `web` and set backend `APP_ORIGIN` to
`http://localhost:5173`. Vite proxies the API to port 8080. No Node server is
required in production.

Use an HTTPS origin behind your TLS reverse proxy for a remote deployment; the
server then marks session cookies Secure. Docker Compose binds the app to
localhost and does not publish the database. Database backup and deployment
operations are outside the local setup.

## Send an event

```sh
export RUBBERAI_API_KEY='<generated project key>'
export RUBBERAI_PROJECT_ID='<project ID>'
python3 examples/send.py
```

See [the API contract](docs/api.md), [OpenAPI](docs/openapi.json), and examples
for curl, Python, JavaScript and Java. Prompt contents, model names, usage and
Git fields are optional. The platform records supplied observations; it cannot
discover hidden prompts or usage inside an IDE without an adapter.

## Optional durable collector

```sh
export RUBBERAI_URL='http://localhost:8080'
export RUBBERAI_API_KEY='<project ingestion key>'
export RUBBERAI_LOCAL_TOKEN='<random value of at least 32 characters>'
export RUBBERAI_OUTBOX="$HOME/.local/state/rubberai/outbox"
./bin/rubberai-collector
```

Send events to `http://127.0.0.1:4319/api/v1/events`, using the **local token** as
Bearer authorization. The collector saves each request to disk before returning
202. It retains events during outages and retries with unchanged IDs. The server
deduplicates retries. The Python SDK can target either the collector or server.

The default outbox holds at most 32 MiB / 1,000 requests. Full queues return 503;
the SDK uses a bounded nonblocking queue and reports overflow through its `dropped`
counter. Inspect `/api/v1/status` on the collector with the local token. Invalid
payloads are retained as `.rejected` files for inspection and count toward the
limit. These files may contain sensitive data: use a private directory and a
single collector per outbox. Privacy filtering occurs at the server; integrations
should omit sensitive content before it reaches disk when using metadata-only mode.

## Central integration hub

Run one local collector as an **integration hub** when a developer uses several
IDEs or coding agents. Every connection has its own route, local token, project
key and filesystem-permission-protected outbox. An adapter only receives its
own local token; it never needs the project's ingestion key.

1. Generate a project ingestion key in **Connect a project**.
2. Copy [`examples/collector-hub.json`](examples/collector-hub.json) somewhere
   private, replace every placeholder, and add one connection per tool.
3. Start the hub:

```sh
export RUBBERAI_COLLECTOR_CONFIG="$HOME/.config/rubberai/collector.json"
./bin/rubberai-collector
```

The hub listens on `127.0.0.1:4319`. A connection named `codex` accepts the
standard event API at
`http://127.0.0.1:4319/codex/api/v1/events` and OTLP HTTP/JSON logs at
`http://127.0.0.1:4319/codex/v1/logs`. Both require that connection's local
token as `Authorization: Bearer …`. The same layout works for Claude Code,
VS Code extensions, Cursor, terminal wrappers, custom internal agents, and any
tool that can emit HTTP JSON or OTLP logs.

Use one connection per project/tool boundary. This prevents a token configured
for one integration from sending events into a different project. The hub
supports 1–16 connections, rejects duplicate IDs, outboxes and local tokens,
and keeps every outbox private (`0700`). The single-connection environment
variables above remain supported for a small setup.

### What an adapter must send

Adapters translate their source format to the documented event contract. They
must emit `prompt.created` only for a human submission, with a stable
`prompt_id` and the same value as `trace_id`. Tool, LLM, file and Git events
from the resulting agentic loop reuse that trace or prompt ID. Background or
unassigned work remains unassigned; the platform does not attach it to the
latest prompt by guesswork. This is what keeps the primary dashboard to one row
per user prompt while retaining technical detail in that prompt's timeline.

The hub is a common transport and privacy boundary, not a magical listener for
closed applications. A tool needs a hook, plugin, OpenTelemetry exporter, CLI
wrapper, or its own HTTP integration to expose activity. The included Claude
Code hook and Codex adapter are examples; generic HTTP remains the portable
path for every other IDE and agent.

### Codex desktop: prompt hook plus OTLP

Codex's OpenTelemetry stream supplies model usage, timing, and agent activity.
To guarantee that **only a human-submitted prompt** creates a prompt row, use
Codex's `UserPromptSubmit` hook as the prompt source. The hook receives the
human prompt, session ID, turn ID, and active model before the agent begins;
agent continuations do not invoke it.

Create a private hub connection called `codex-rubberai`, then configure Codex:

```toml
# ~/.codex/config.toml
[otel]
log_user_prompt = true

[otel.exporter.otlp-http]
endpoint = "http://127.0.0.1:4319/codex-rubberai/v1/logs"
protocol = "json"

[otel.exporter.otlp-http.headers]
Authorization = "Bearer <codex-rubberai local token>"
```

Add a `UserPromptSubmit` command hook in `~/.codex/hooks.json`. Its program
should read the hook JSON from standard input and POST a `prompt.created` event
to `http://127.0.0.1:4319/codex-rubberai/api/v1/events` using that connection's
local token. Reuse `session_id` and `turn_id` to create a stable `prompt_id` and
use it as `trace_id`. The hook must exit quickly and never block the coding
session if RubberAI is unavailable.

```json
{
  "hooks": {
    "UserPromptSubmit": [{
      "hooks": [{
        "type": "command",
        "command": "/usr/bin/python3 ~/.codex/hooks/rubberai_user_prompt.py",
        "timeout": 1
      }]
    }]
  }
}
```

Restart Codex after changing its configuration. A completed turn then has this
relationship: `UserPromptSubmit` creates the user prompt; Codex OTLP records
add usage and tool activity; any adapter file or Git events reuse the same
session/turn identifiers. If Codex does not emit a measurement, RubberAI shows
**Not reported** instead of estimating it. With `METADATA_ONLY`, the prompt row
is visible but its text, diffs, and arbitrary metadata are discarded before the
event is written to the collector outbox. Set both the project's collection
policy and the hub connection's `privacy` value to `FULL` only when storing this
content is intended.

## Cost estimates

`PRICING_JSON` supplies explicit per-million token prices, keyed by provider/model:

```json
{"example/model":{"input_per_million":"1.0","output_per_million":"2.0",
 "cache_read_per_million":"0.1","cache_write_per_million":"2.0",
 "currency":"USD","version":"internal-2026-09"}}
```

These are illustrative rates, not provider prices. Estimates require both input
and output token counts. Supplied costs take precedence.

`input_tokens` is the whole input side, with cached and cache-write counts as
subsets of it, so the estimator charges the remainder at the input rate and each
subset at its own. This matters: a cache read costs a fraction of fresh input and
a cache write rather more, so pricing the whole input figure at the input rate can
overstate a cache-heavy request several times over.

The cache rates are optional, because a model that never reports cache tokens
never needs them. But a request that does report them and has no rate configured
is left unpriced rather than priced at the input rate — an estimate that silently
omits a priced component would be wrong in a way nothing on screen reveals. The
estimator still does not model tiers or other provider billing rules; supply
calculated costs for those schemes.

Reasoning tokens are a subset of output and are never charged twice. A partial
measurement does not become a complete total.

## Verification

```sh
export TEST_DATABASE_URL='postgres://USER:PASSWORD@localhost:5432/rubberai_test?sslmode=disable'
make test
make check
# With the local application running:
cd web && npm run test:e2e
```

Integration tests create organizations and events in the selected test database;
they do not delete existing data. Without TEST_DATABASE_URL, Go reports the
database test as skipped. Browser tests use a temporary organization and the real
API. [Validation notes](docs/validation.md) record checks performed for this build.

## Initial scope and limits

Implemented: organization accounts, projects, scoped/revocable keys, atomic event
batches, validation, project-scoped deduplication, privacy modes, usage/cost
analytics, trace/prompt drill-down, file/Git metadata, a Go outbox collector and a
dependency-free Python SDK.

External user identities are integration assertions, not independently verified
login identities. Account registration creates a new organization; invitations,
shared organization membership and password recovery are not implemented yet.
Sessions, traces and prompts are projections of immutable events rather than
separately editable records. Retention is manual in this first version. Aggregate
groups are capped at 100 and event pages at 1,000 rows. A single backend instance
enforces in-memory per-minute request limits; distributed rate limiting is not
implemented. No automatic IDE/agent instrumentation or production deployment has
been performed.

## License

MIT. See [LICENSE](LICENSE).
