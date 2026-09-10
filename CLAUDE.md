# Working notes — Claude

Companion to [AGENTS.md](AGENTS.md); everything there applies here too. This file
records what Claude changed, and the state each change is in, so the next agent
does not have to re-derive it from the diff.

Read [docs/specification.md](docs/specification.md) and
[docs/architecture.md](docs/architecture.md) before extending the product. The
public contract is HTTP JSON, and no business logic may depend on a particular
IDE, agent or source language.

## Build status of these changes — read first

**The Go changes in this file have never been compiled.** At the time of writing,
no Go toolchain was installed on the development machine: `bin/rubberai` was built
earlier with go1.27.1 and still runs, but the compiler was gone by the time these
edits were made, so `make build` could not run.

Consequences:

- The running server still serves the **old** aggregate and the **old** `group_by`
  set. The dashboard changes are inert against it — selecting the new grouping
  returns `INVALID_GROUP` until the binary is rebuilt.
- `internal/platform/ingest.go` and `web/src/App.svelte` have not been vetted by
  `go vet`, `npm run check`, or any test.

Before trusting any of it: `make build && make check`, then
`TEST_DATABASE_URL=... make test`.

## 1. Claude Code adapter (new, working)

[integrations/claude-code/](integrations/claude-code/) — the first agent adapter.
Standard-library Python 3; see its
[README](integrations/claude-code/README.md) for configuration and event mapping.

This part **is** verified end to end against a running server: session, prompt,
tool, file and token-usage events were all accepted, and replaying a batch
returned `duplicates` rather than double-counting.

Three things about it are non-obvious and worth not rediscovering the hard way:

- **Token usage is not in any hook payload.** It exists only in the Claude Code
  session transcript (JSONL). The adapter tails that file from a stored byte
  offset under `~/.local/state/rubberai/claude-code/`, and advances the offset
  only after the server accepts the batch, so a failed send retries rather than
  disappearing. Event IDs derive from the transcript message UUID, which is what
  makes that retry safe.
- **If you ever clear the events table, clear those offsets too.** An offset
  pointing at end-of-file while the rows are gone means that session's usage is
  skipped permanently — the offset says "already sent" and nothing re-reads it.
- **Claude Code writes its own local notices into the transcript** as assistant
  records with model `<synthetic>` and all-zero usage ("No response requested.",
  "API Error: ..."). No request ever reached a provider. The adapter skips them,
  plus any zero-token message; without that filter they inflate the request count
  with calls that never happened (76 of them across one project's history).

Subagent turns (`isSidechain`) are labelled `agent.type: subagent` rather than
dropped. They are real billable usage, so hiding them would make cost totals
wrong; labelling lets the dashboard separate user-driven work from the agent's own.

## 2. Turn-shaped analytics (new, unbuilt)

`group_by` accepted `user`, `agent`, `model`, `ide`, `language` and `day` — none
of which answer "what did *I* ask for, and what did it cost". One typed prompt
fans out into dozens of LLM requests and tool calls, so a flat event list is
dominated by the agentic loop rather than by the user's own turns.

- `internal/platform/ingest.go` — adds `prompt` and `trace` to `group_by`, so one
  user-driven turn collapses into one row. These two orderings are newest-first
  (`max(timestamp)`), not by volume, because a turn list reads chronologically.
  The aggregate also gained `tools`, `started_at`, `ended_at`, `models`, and
  `prompt_text` (taken from the turn's `prompt.created` event, so a row can be
  labelled with the actual prompt instead of an opaque `pmt_` identifier).
- `web/src/App.svelte` — the new groupings in the picker, a Tools column, and
  prompt rows that render the prompt text and link into the trace. `Stats` had an
  index signature of `[key:string]:number`, which the text and array fields
  violate; it is now `unknown`, so new non-numeric aggregate fields do not
  silently break the type.
- `web/src/style.css` — `.text-button.turn` truncates a prompt to one line so the
  numeric columns stay aligned, with a narrower cap under the existing 600px
  breakpoint.

Still missing after this: per-group **cost** (the aggregate has no cost field, so
cost by user/model/day needs one filtered query per group), and a dedicated
prompt-detail page. Both are called for by the specification.

## Conventions worth keeping

- Validate **before** applying privacy. `Validate()` downgrades an unevidenced
  `AI` change source to `UNKNOWN`; `ApplyPrivacy()` then strips evidence in
  metadata-only mode. Reversing that order would silently erase every AI
  attribution in the default privacy mode.
- Cached and reasoning tokens are subsets, never added into a total a second time.
- An adapter that cannot resolve its project stays silent rather than guessing one.
- A telemetry bridge must never break the editor: it exits 0 on every failure.
  Pair that with a debug switch (`RUBBERAI_DEBUG=1`), or a silently broken bridge
  is undiagnosable — that is exactly how a shadowed variable made the adapter send
  nothing at all, with no error anywhere.
