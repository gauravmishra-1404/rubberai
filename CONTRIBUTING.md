# Contributing to rubberai

Thanks for taking the time. This page tells you how to set up, what a good
change looks like, and what happens after you open a pull request. It is
short on purpose — the [README](README.md) explains the product, and
[docs/architecture.md](docs/architecture.md) explains why things are built
the way they are.

## Before you start

- **Bug?** Open an issue with the *Bug report* template. Include the version
  (image tag or commit), what you did, what you expected, and what happened.
- **Idea?** Open an issue with the *Feature request* template first, before
  writing code, so we can agree on the shape. Small fixes (typos, obvious
  bugs) do not need an issue — just send the PR.
- **Security problem?** Do **not** open an issue. See [SECURITY.md](SECURITY.md).

Look for issues labelled `good first issue` if you want somewhere to begin.

## Set up

You need Go 1.27+, Node.js 22+, Python 3 and PostgreSQL 17+ (Docker is fine
for Postgres).

```sh
git clone https://github.com/<your-fork>/rubberai
cd rubberai
make build                    # dashboard + both binaries into bin/
```

To run the server locally, follow [README → Run without Docker](README.md#7-run-without-docker).

## Run the tests

Everything CI runs, you can run locally:

```sh
# Go: needs a database for the integration test (it is skipped without one)
export TEST_DATABASE_URL='postgres://USER:PASSWORD@localhost:5432/rubberai_test?sslmode=disable'
gofmt -l .                    # prints nothing when formatting is right
go vet ./...
go test -race ./...

# Dashboard
cd web && npm ci && npm run check && npm run build && cd ..

# Python SDK and the Claude Code adapter
(cd sdk/python && python3 -m unittest)
(cd integrations/claude-code && python3 -m unittest)
```

Browser tests (`cd web && npm run test:e2e`) need the app running on
localhost:8080; run them when you change the dashboard.

## Where things live

| Path | What |
|---|---|
| `cmd/server`, `cmd/collector` | Entry points |
| `internal/platform` | HTTP API, auth, ingestion, analytics, migrations, embedded dashboard |
| `internal/event` | The event schema, validation, privacy filtering, cost estimation |
| `internal/collector` | The local outbox collector and hub |
| `web/` | Svelte dashboard (built into `internal/platform/web/` — commit the build output) |
| `integrations/claude-code` | Claude Code adapter (single Python file + tests) |
| `sdk/python` | Dependency-free Python SDK |
| `docs/` | API contract, architecture, specification |

## What a good change looks like

- **Keep the contract.** `docs/api.md` is the promise to every integration. A
  change to the event schema or an endpoint updates the doc in the same PR,
  and does not break existing clients.
- **Tests with the change.** A bug fix comes with a test that failed before
  the fix. New behaviour comes with a test that shows it. The integration
  test in `internal/platform/integration_test.go` is the place for anything
  that touches the API end to end.
- **Privacy stays server-side.** Anything that decides what is stored or
  shown belongs in the server, not the dashboard. A project set to
  metadata-only must never store prompt text, whatever the client sends.
- **No new dependencies without a reason.** The server has one (pgx) and the
  adapter and SDK have none. That is deliberate — say why in the PR if you
  need to add one.
- **Match the code around you.** Comments explain *why*, not what. Run
  `gofmt`. Keep the dashboard in the existing style; no new UI framework.
- **Small PRs.** One change per PR. A refactor and a feature are two PRs.

Every source file starts with the SPDX header:

```
// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Gaurav Mishra
```

Keep it on files you add. By contributing you agree your work is licensed
under the [MIT license](LICENSE) like the rest of the project.

## Opening the pull request

1. Branch from `main`: `git checkout -b short-description`.
2. Make the change, run the tests above.
3. Push to your fork and open a PR against `main`. The template asks what
   changed, why, and how you tested it — fill it in; it is what the reviewer
   reads first.
4. CI runs automatically (Go, dashboard, Python). Fix anything red.
5. A maintainer reviews. Push follow-up commits to the same branch; no need
   to squash, it is done at merge.

Commit messages: a short imperative first line ("Fix diff counts for
untracked files"), then a blank line, then *why* if it is not obvious.

## Questions

Open an issue — there are no wrong questions, and the answer usually turns
into a doc improvement.
