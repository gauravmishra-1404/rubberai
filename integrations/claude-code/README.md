# Claude Code adapter

Records Claude Code activity as rubberai events: sessions, prompts, tool calls,
file changes, and per-request token usage. Standard library Python 3 only.

Claude Code runs [hook.py](hook.py) on each lifecycle hook and passes the hook
payload as JSON on stdin. The script maps the payload to events and POSTs them to
`/api/v1/events`.

## Configure

Credentials live outside this repository, in `~/.config/rubberai/claude-code.json`
(mode 600). Override the location with `RUBBERAI_CLAUDE_CONFIG`.

```json
{
  "url": "http://localhost:8080",
  "user_id": "your-name",
  "send_prompts": false,
  "projects": {
    "/absolute/path/to/repository": {
      "project_id": "prj_...",
      "api_key": "rai_..."
    }
  }
}
```

Sessions are attributed by working directory, matched innermost-first, so a
session started in a subdirectory still resolves to its repository. A directory
that matches no entry sends nothing — the adapter never guesses a project. Add a
second repository by adding a second entry with that project's own key.

`send_prompts` is false by default; prompt text is only attached when it is true,
and the server still discards it unless the project's privacy mode is `FULL`.

## Register the hooks

In `~/.claude/settings.json` (or a project's `.claude/settings.json`), for each of
`SessionStart`, `UserPromptSubmit`, `PostToolUse`, `PostToolUseFailure`, `Stop`
and `SessionEnd`:

```json
{
  "hooks": {
    "Stop": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "python3 /path/to/rubberai/integrations/claude-code/hook.py",
            "async": true,
            "timeout": 20
          }
        ]
      }
    ]
  }
}
```

`async: true` matters: hooks then run in the background instead of delaying the
editor. Omitting `matcher` on the tool events records every tool.

## Recording what changed

Line counts and before/after hashes are metadata and are always sent. Diff text is
source code, so it is sent only when `send_diffs` is true in the configuration —
and the server still discards it unless the project stores diffs and is not
collecting metadata only. Both gates must be open for a diff to be kept.

Changes are derived from the tool call itself, which carries the exact strings
replaced, rather than by re-reading the file: the hook runs after the edit, so
re-reading would race with whatever the agent does next. Line counts come from the
diff opcodes, not the sizes of the replaced strings, because those strings include
surrounding context and would overstate the change. A whole-file `Write` reports
every line as added, since no prior content reaches the hook.

Diffs are truncated at 40,000 characters so one large edit cannot push a batch past
the server's 1 MiB body limit.

## Events

| Hook | Events |
| --- | --- |
| SessionStart | `session.started` |
| UserPromptSubmit | `prompt.created`, starting a new trace |
| PostToolUse | `tool.completed`, plus `file.created`/`file.modified` for Write, Edit and NotebookEdit |
| PostToolUseFailure | `tool.failed` |
| Stop | `llm.request.completed` with token usage |
| SessionEnd | a final usage flush, then `session.ended` |

Each user prompt opens a trace; the tool and LLM events of that turn carry its
`trace_id` and `prompt_id`, so a turn can be read end to end in the dashboard.

## Token usage

Usage is not present in any hook payload, so it is read from the Claude Code
session transcript (`transcript_path`, JSONL) at a stored byte offset kept under
`~/.local/state/rubberai/claude-code/`. Mapping per assistant message:

| rubberai | Claude Code |
| --- | --- |
| `input_tokens` | `input_tokens` + `cache_creation_input_tokens` + `cache_read_input_tokens` |
| `output_tokens` | `output_tokens` |
| `cached_tokens` | `cache_read_input_tokens` |
| `cache_write_tokens` | `cache_creation_input_tokens` |
| `reasoning_tokens` | `output_tokens_details.thinking_tokens` |

Cache reads and writes are included in normalized input and also shown separately.
The total adds input and output only, so subsets are not counted twice.

The offset advances only after rubberai accepts the batch, so a failed send is
retried on the next turn rather than dropped. Event IDs are derived from the
provider message ID (falling back to transcript UUID), so a retry after a partial failure is deduplicated by
the server instead of double-counting tokens.

Costs are not sent. Configure `PRICING_JSON` on the server to estimate them, per
the main README.

## Failure behavior

The adapter never disrupts a coding session: unreadable config, an unreachable
server, or a malformed payload all exit 0 silently. Failed submissions retain
the transcript offset and pending prompts for retry. Permanent rejections require
correcting the integration/configuration; debug mode exposes the response.

Because it is silent, set `RUBBERAI_DEBUG=1` to trace to stderr when checking
whether it works:

```sh
RUBBERAI_DEBUG=1 python3 hook.py <<'EOF'
{"hook_event_name":"SessionStart","session_id":"test","cwd":"/absolute/path/to/repository"}
EOF
```

Expect `[rubberai] sent 1 event(s): {"accepted":1,...}`. No output means the
working directory matched no configured project, or no config file was found.

## Limits

Claude Code reports no per-request duration or provider cost to a hook, so
`duration_ms` and `cost` are absent. `change_source` is `AI` with the tool name as
evidence — an integration assertion, not independent verification. Events reach
the server per hook invocation; run the [collector](../../cmd/collector) and point
`url` at it for durable buffering across server restarts.

## Prompt boundaries and token accounting

New or missing adapter state starts at the current transcript position. Do not
clear offsets to replay old history after clearing a dashboard. Each submitted
prompt records a byte boundary; retrying delivery retains the original boundary.
Pending prompt submissions are retried with their original IDs. Internal model
records do not create `prompt.created` events. Late asynchronous records that
cross turn boundaries are not reconstructed into a proven causality tree.

Input is normalized as fresh input + cache reads + cache writes, following
[Anthropic's usage definition](https://platform.claude.com/docs/en/build-with-claude/prompt-caching).
`cached_tokens` and `cache_write_tokens` expose those subsets separately; total
is input + output. Zero is preserved when reported, and absent counts stay absent.
Existing stored events are not rewritten to the new accounting.
