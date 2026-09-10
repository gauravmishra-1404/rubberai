# Architecture decisions

## Deployment and modules

`cmd/server` is the deployable Go application. `internal/platform` contains
authentication, project authorization, ingestion, analytics and the embedded web
assets. Event validation and privacy are pure functions. PostgreSQL is the only
backend store. The independent `cmd/collector` binary is optional.

The dashboard is a static Svelte/TypeScript application compiled by Vite and
embedded in the Go binary. It communicates exclusively through `/api/v1`.

## Storage and correctness

Typed columns hold indexed identifiers and timestamps. JSONB carries extensible
event fields. An immutable event log supplies session, trace and prompt views;
these identifiers need not arrive in order. All events in a batch commit together.
A database primary key on `(project_id,event_id)` enforces idempotency even under
concurrent requests. An ID collision retains the first accepted event; clients
must use a new ID for a new observation. Retry identical payloads with the same ID.

Migration version 1 is embedded in `schema.sql`; startup takes a PostgreSQL
advisory lock and applies it once. Future schema changes need new migration files
and explicit version handling. Do not mutate version 1 after deployment.

Only `llm.request.completed` accepts usage/cost, preventing lifecycle start/end
double counting. Missing token categories remain absent. Cost amounts use decimal
strings at the API boundary and exact rational / PostgreSQL numeric arithmetic.
Different currencies and actual/estimated amounts remain separate.

## Trust boundaries

Organization accounts authenticate with PBKDF2-HMAC-SHA256 password hashes and
random HttpOnly SameSite cookies. Session and API token hashes are stored instead
of plaintext tokens. Dashboard handlers authorize against the signed-in user's
organization. Project keys only grant event ingestion and cannot query analytics
or create/revoke keys. A separate operator token protects process metrics.

The ingestion transaction locks the key and project settings while applying
scope and privacy checks. Unknown top-level fields are ignored for compatibility;
unknown event types and agent/model names are supported. Arbitrary metadata is
discarded outside FULL mode. Redaction handles common credential patterns, not
all possible sensitive information. The safest default remains metadata only.

An adapter may assert AI/HUMAN only with an evidence reference; otherwise the
server records UNKNOWN. This validates the presence of evidence, not its truth.
Time proximity alone never creates AI attribution. Prompt linkage requires a
prompt_id supplied by the integration; sharing a trace does not prove causation.

## Operational bounds

Server requests: 1 MiB and 100 events per ingestion batch; 10 database connections;
request deadlines; per-key/project/organization fixed-window request limits.
Default limits are 600 / 3,000 / 10,000 requests per minute, configurable via
KEY_REQUESTS_PER_MINUTE, PROJECT_REQUESTS_PER_MINUTE and ORG_REQUESTS_PER_MINUTE.
Authentication is limited to 20 requests per minute per peer IP. Reverse-proxy
forwarded headers are intentionally not trusted automatically.

Collector: loopback only, a separate local bearer token, private outbox permissions,
32 MiB / 1,000 request cap, one uploader, exponential retries up to 60 seconds.
Files persist across restarts; a crash after server acceptance can resend an event,
which is safe because ingestion is idempotent. Each successful HTTP submission
acknowledges a durable file. An SDK's in-memory queue before that acknowledgement
is best effort and may be lost if the client process crashes.

Metrics are available at `/api/v1/metrics` with METRICS_TOKEN. They expose request
count/duration totals, accepted/duplicate/failed ingestion counts, heap size,
goroutines and pool statistics. Use process/container monitoring for CPU and RSS.
These metrics are a starting point, not a complete production monitoring setup.
