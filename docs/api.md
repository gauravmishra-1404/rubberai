# HTTP API v1

Base path: `/api/v1`. JSON responses use `Content-Type: application/json`.
Requests that have a body must use JSON. Errors use:

```json
{"error":{"code":"INVALID_EVENT","message":"An explanation"}}
```

## Accounts and projects

| Method | Path | Purpose |
| --- | --- | --- |
| POST | /auth/register | `{username,password,display_name,organization,email?}`; creates organization and cookie session |
| POST | /auth/login | `{username,password}`; establishes a 24-hour cookie session |
| POST | /auth/logout | Invalidates session |
| GET | /me | Current user ID, display name and organization ID |
| GET, POST | /projects | List / create projects in the current organization |
| PATCH | /projects/{id} | Update name, description, repository_url, privacy or track_diffs |
| GET, POST | /projects/{id}/keys | List / create ingestion keys; POST body `{name}` |
| DELETE | /projects/{id}/keys/{key_id} | Revoke a key |

`email` is optional. When supplied it is validated for shape, normalised (the
domain is lowercased, the local part is not, since only the domain is
case-insensitive) and checked for a published mail host, which rejects a
mistyped domain. A lookup that cannot complete - no resolver, an offline
deployment - accepts the address and records it as unchecked rather than
refusing the registration. The check establishes that a domain can receive
mail, never that the person owns the address. Returns 400 `INVALID_EMAIL`.

`GET /demo` (not under `/api/v1`) opens a read-only session confined to the
project named by the server's `DEMO_PROJECT_ID`, then redirects to the dashboard.
It is a plain GET so a link on any page can open it, and it holds no credential:
the session is minted server-side and expires after an hour. A demo session can
read that one project - events, analytics, diffs - and nothing else: every
mutating route, and the key listing, answers 403 `READ_ONLY`, and any other
project in the organization answers 404. `/me` reports `demo` with the project
id so a client can hide what the session cannot do. With no `DEMO_PROJECT_ID`
configured the route answers 404.

A demo session never sees who the developers are. In its responses the
identity an adapter recorded in `metadata` (`email`, `host`, `ip`) and the
aggregate `prompt_email`/`prompt_host`/`prompt_ip` fields are replaced with
stand-ins (`<user_id>@example.com`, `dev-laptop`, `10.0.4.17`) - always
present on prompts, so the feature is visible, and substituted on any other
event that carries one of the keys. The replacement happens on the way out;
the stored values are unchanged and the owner's own sessions read them as
recorded.

The session cookie is HttpOnly, SameSite=Strict, and Secure when APP_ORIGIN uses
HTTPS. Browser writes must originate from APP_ORIGIN. Ingestion API keys cannot
use account, project, analytics or key-management endpoints.

## Events

`POST /events` uses `Authorization: Bearer <project API key>`. Send one event or
`{"events":[...]}` for an atomic batch of 1–100 events. Maximum body size: 1 MiB.

Required: `event_id`, `event_type`, `project_id`, `timestamp` (RFC3339). Identifiers
are 1–160 characters drawn from letters, digits and `_.:@/-`. Timestamps may be
delayed; more than five minutes in the future is rejected. `prompt.created`
requires `prompt_id`. File events require `file.path`.

Optional fields: `user_id`, `session_id`, `trace_id`, `prompt_id`, `agent`, `ide`,
`model`, `language`, `prompt`, `usage`, `cost`, `file`, `tool_name`, `tool_call_id`,
`duration_ms`, `status`, `repository`, `branch`, `commit_sha`, `metadata`.

Unknown event types and custom agent/IDE/model names are supported. Unknown
top-level properties are ignored; use `metadata` for extensions in FULL mode.
Missing model or language is normalized to `unknown`. User identity is a
project-scoped external identifier asserted by the integration, not proof of a
login identity. Repository is an optional URL/string; Git is not required.

```sh
curl --fail-with-body http://localhost:8080/api/v1/events \
  -H "Authorization: Bearer $RUBBERAI_API_KEY" \
  -H 'Content-Type: application/json' \
  --data "{\"event_id\":\"evt_example_1\",\"event_type\":\"session.started\",\"project_id\":\"$RUBBERAI_PROJECT_ID\",\"timestamp\":\"2026-09-10T12:00:00Z\",\"session_id\":\"session_1\",\"user_id\":\"developer_1\"}"
```

Successful response: `{"accepted":1,"duplicates":0}`. A retry returns
`{"accepted":0,"duplicates":1}`. Reuse the event ID and original payload after
timeouts, 408, 429 and 5xx responses. A commit error may be ambiguous; retry is
safe. `Retry-After: 60` accompanies rate-limit responses. Do not retry invalid
payloads without correcting them. A batch fails entirely when any event is
invalid or belongs to another project.

Supported lifecycle conventions: `session.started/ended`, `prompt.created`,
`llm.request.started/completed/failed`, `tool.started/completed/failed`,
`file.read/created/modified/deleted/renamed`, `test.started/completed`,
`git.commit/branch/checkout`. Future names do not require a server upgrade.

Usage and cost must be attached to `llm.request.completed`. Usage fields are
optional non-negative integers: `input_tokens`, `output_tokens`, `cached_tokens`,
`reasoning_tokens`, `total_tokens`. Total is derived only when both input and
output are present. Cached/reasoning counts are not added again. The upper bound
per category is 10^12 tokens. Cost is `{amount:"0.012",currency:"USD",type:"actual"}`;
amount is an exact decimal string (up to 16 integer and 12 fractional digits).
Estimated costs may include `pricing_version`. See README for configured prices.

File metadata includes `path`, `old_path`, `operation`, `language`, `lines_added`,
`lines_removed`, `before_hash`, `after_hash`, `diff`, `change_source`, `evidence`.
An AI/HUMAN source without evidence becomes UNKNOWN. Metadata-only collection
discards prompt content, diffs, arbitrary metadata and evidence text. The source
label remains an integration assertion. REDACTED filters common credential
patterns in prompts and disables diffs/metadata; it is not a general DLP system.

## Query and analytics

`GET /projects/{id}/events` returns `{events,has_more,next_offset}` in chronological
order. Pagination uses `limit` (1–1000, default 100) and `offset` (default 0).
Offset pagination may shift while new delayed events are arriving.

Both `/events` and `/analytics` accept `from`, `to` (RFC3339 UTC or explicit
offset), `user_id`, `session_id`, `trace_id`, `prompt_id`, `event_type`, `agent`,
`ide`, `model`, `language`, `repository`, `branch`. Default range is the last
30 days. The filters match exact values. Use a wider explicit range to inspect
older traces. Events are linked only by supplied identifiers.

`GET /projects/{id}/analytics` returns `totals`, `costs`, `breakdown`, `group_by`.
Group by `user`, `agent`, `model`, `ide`, `language` or `day` (UTC). The top 100
groups by event count are returned. Costs remain grouped by currency and type.
Apply a user/model/etc. filter to obtain that group's cost. Coverage counts
distinguish requests with known totals/costs from incomplete observations.

## Limits and operations

400: invalid input; 401: invalid authentication; 403: wrong project or origin;
404: missing/inaccessible resource; 409: registration conflict; 415: wrong content
type; 429: rate limited; 503: temporary storage failure. Oversize ingestion bodies
currently return 400 INVALID_JSON. Retry only transient errors.

`GET /healthz` checks the database. `GET /api/v1/metrics` requires the separate
METRICS_TOKEN. Rate limits and operational bounds are documented in
[architecture.md](architecture.md).

The collector offers `/api/v1/events` and `/api/v1/status` on localhost:4319 with
its own token. SDK `Client.emit()` queues without network I/O and returns False
when the bounded queue is full. SDK shutdown is best effort; use the collector
for durable storage after its 202 acknowledgement.

### Prompt view and token breakdown

`GET /projects/{id}/analytics?group_by=prompt&user_prompts=true` returns one
group per recorded `prompt.created` submission, with linked calls aggregated.
Internal requests alone never create prompt rows. Filters select matching prompt
groups; the summary includes their linked activity. Raw events remain available.
Adapters must reserve `prompt.created` for human submissions and keep uncertain
activity unassigned. Existing incorrect associations are not rewritten.

Usage optionally accepts `cache_write_tokens` alongside `cached_tokens` (cache
reads). Cache categories are subsets of normalized input; reasoning is a subset
of output. Providers with exclusive cache counts must normalize input or provide
an explicit total. Missing categories aggregate to null, with `reported_*` counts
showing coverage. A zero is a reported zero. Never add subsets to total again.
Prompt breakdown rows include exact costs grouped by currency and actual/estimated.
