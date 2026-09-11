# Validation record

Validated on the local Linux development machine, 2026-09-11 (Asia/Kolkata).
Go 1.27.1 was downloaded from go.dev and verified against the published SHA-256.
Node 22.22.3, PostgreSQL 17 and installed Google Chrome were used.

## Passed checks

- `go test -race ./...` with TEST_DATABASE_URL pointing at an isolated PostgreSQL
  instance: event validation, token subset accounting, incomplete totals, exact
  cost arithmetic, multiple currencies, privacy modes and source attribution.
- PostgreSQL end-to-end: organization registration, project creation, key
  issuance, cross-organization read/write denial, ingestion-key privilege
  isolation, invalid/revoked keys, mismatched projects, concurrent duplicates,
  delayed events, trace correlation, pagination, usage/cost aggregation, full vs
  metadata-only storage, key revocation and logout.
- Collector: private outbox permissions, bounded storage, authentication,
  persistence across a collector restart, retry after server failure and stable
  payloads/IDs across retries.
- `go vet ./...`.
- Python SDK: 3 tests passed for preserved retry IDs, nonblocking emission during
  an outage, bounded overflow and nonfatal invalid input.
- `npm run check`: 0 errors and 0 warnings.
- `npm run build`: compiled dashboard JavaScript 57.94 KB (22.48 KB gzip),
  CSS 7.74 KB (2.45 KB gzip).
- Playwright: desktop and 390×844 mobile passed the complete registration →
  project → key → ingestion → trace → privacy change → revocation → logout flow.
  Both viewports passed a horizontal-overflow assertion. Mobile screenshot was
  also visually inspected.
- Docker Compose configuration parsed successfully with a placeholder password.
- OpenAPI JSON parsed successfully; it has not been checked by a dedicated
  OpenAPI conformance validator.

## Small local performance sample

`scripts/benchmark.py` sent 3,000 synthetic completed LLM request events to an
isolated local application/database, in 30 serial batches of 100:

| Measurement | Result |
| --- | --- |
| Effective ingestion rate | 3,431 events/second |
| Mean batch response time | 29.15 ms |
| Observed p95 batch response time | 39.19 ms |
| Analytics response over that project | 36.96 ms |

The native collector ran idle for 10 seconds under `/usr/bin/time -v`:
maximum RSS 7,660 KiB (about 7.5 MiB); reported user CPU 0.00s and system CPU
0.01s. The timeout intentionally stopped the process, producing exit status 124.
This is an idle sample, not peak memory under load. CPU is rounded by the tool.

These results do not establish sustained throughput, multi-tenant capacity,
long-term memory behavior or latency on remote infrastructure. No production
load test, Docker image build, deployment, non-Linux collector test or independent
security review was performed. The native application and embedded production
dashboard were exercised directly.

## Known first-version gaps

Organization invitations/shared membership, password recovery, automatic data
retention, provider-specific cache/tier pricing, built-in agent adapters, per-file
content snapshots and richer user/session management remain future work. Existing
events are immutable; changing privacy settings does not erase past stored data.
External user identities and attribution evidence are integration assertions.
See README and architecture.md for the current operational bounds.

## User-prompt view and token breakdown (2026-09-11)

Validated with Go 1.27.1, `go test -race -p 1 ./...` against isolated PostgreSQL,
`go vet ./...`, seven adapter unittest cases, Svelte checks and Vite build.
Desktop and 390px Playwright tests passed: three user submissions produce three
rows despite an orphan internal request; prompt details show input/output/cache
and missing data; privacy, key revocation, and page overflow checks pass.
Screenshots reviewed at both widths. Tests never cleared the application database.
Historical attribution/token rows are preserved, not retroactively repaired.

Response-fragment regression: multiple transcript records for one provider message
produce one usage event using the latest snapshot within the flush.
