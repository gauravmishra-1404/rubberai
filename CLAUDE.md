# Working notes — Claude

Companion to [AGENTS.md](AGENTS.md); everything there applies here too. This file
records what Claude changed, and the state each change is in, so the next agent
does not have to re-derive it from the diff.

Read [docs/specification.md](docs/specification.md) and
[docs/architecture.md](docs/architecture.md) before extending the product. The
public contract is HTTP JSON, and no business logic may depend on a particular
IDE, agent or source language.

## Current verification status

Codex built the current source with Go 1.27.1 and the installed frontend lockfile.
Frontend checks, Go unit/integration tests with race detection, and Go vet passed.
PostgreSQL tests use a separate temporary database, never the local application DB.

The dashboard now defaults to recorded user prompts with separate input/output/cache
columns. A prompt detail includes cache writes, reasoning and total, with partial
coverage labels. Raw activity remains expandable. Prompt-scoped summaries exclude
orphan calls. No historical rows were modified: earlier attribution/token mistakes
remain in previously stored events and cannot be treated as repaired measurements.

The adapter starts at EOF when state is absent and retains prompt byte boundaries
across retries. It normalizes Anthropic input including cache reads/writes, preserves
reported zeros, and retries pending prompt submissions. This boundary approach is
for sequential turns in one transcript; it does not reconstruct a subagent tree or
prove causality for late asynchronous records crossing turn boundaries.

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
  disappearing. Event IDs derive from the provider message ID (falling back to transcript UUID), which is what
  makes that retry safe.
- **Do not clear transcript offsets to replay history after a dashboard reset.**
  The updated adapter starts at the current file position when state is missing.
  Prompt boundaries preserve the original association when delivery is retried.
- **Claude Code writes its own local notices into the transcript** as assistant
  records with model `<synthetic>` and all-zero usage ("No response requested.",
  "API Error: ..."). No request ever reached a provider. The adapter skips them,
  while preserving real zero-token measurements; without that filter they inflate the request count
  with calls that never happened (76 of them across one project's history).

Subagent turns (`isSidechain`) are labelled `agent.type: subagent` rather than
dropped. They are real billable usage, so hiding them would make cost totals
wrong; labelling lets the dashboard separate user-driven work from the agent's own.

## 2. Turn-shaped analytics (original change, now built)

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

The current implementation adds per-group cost and a structured prompt detail.
The earlier notes above describe the original change, not its final validation state.

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
