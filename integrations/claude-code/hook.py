#!/usr/bin/env python3
"""Claude Code -> rubberai bridge.

Claude Code invokes this once per hook event, passing the hook payload as JSON on
stdin. Each payload is translated into rubberai events and POSTed to the ingestion
API. Standard library only, to match the SDK.

Two rules govern everything here:

1. Never disturb the editor. Any failure - bad config, server down, malformed
   payload - exits 0 silently. A telemetry bridge must not be able to break a
   coding session.
2. Never lose an LLM request. Token usage is read from the session transcript at
   a stored byte offset, and the offset only advances after the server accepts the
   batch. Event IDs are derived from the transcript message UUID, so a retry after
   a partial failure is deduplicated server-side rather than double-counted.

Configuration lives in ~/.config/rubberai/claude-code.json (override with
RUBBERAI_CLAUDE_CONFIG); see README.md in this directory.
"""

import difflib
import hashlib
import subprocess
import json
import os
import socket
import sys
import urllib.error
import urllib.request
import uuid
from datetime import datetime, timezone

AGENT_NAME = "claude-code"
BATCH_LIMIT = 100
HTTP_TIMEOUT = 5
PROMPT_LIMIT = 20000
DIFF_LIMIT = 40000
SYNTHETIC_MODEL = "<synthetic>"

# Claude Code reports where it is running as an "entrypoint"; rubberai wants an
# IDE name. Anything unlisted falls through as the raw entrypoint string, which
# the server accepts as a custom IDE name.
IDE_BY_ENTRYPOINT = {
    "claude-vscode": "vscode",
    "vscode": "vscode",
    "claude-jetbrains": "jetbrains",
    "jetbrains": "jetbrains",
    "cli": "terminal",
}

LANGUAGE_BY_SUFFIX = {
    ".java": "java", ".kt": "kotlin", ".go": "go", ".py": "python",
    ".js": "javascript", ".jsx": "javascript", ".ts": "typescript",
    ".tsx": "typescript", ".svelte": "svelte", ".rs": "rust", ".rb": "ruby",
    ".php": "php", ".cs": "csharp", ".c": "c", ".h": "c", ".cpp": "cpp",
    ".sql": "sql", ".sh": "shell", ".html": "html", ".css": "css",
    ".scss": "css", ".json": "json", ".yaml": "yaml", ".yml": "yaml",
    ".xml": "xml", ".md": "markdown",
}

# Tools whose successful use means a file changed on disk.
FILE_WRITE_TOOLS = {"Write": "created", "Edit": "modified", "NotebookEdit": "modified"}


def now() -> str:
    return datetime.now(timezone.utc).isoformat()


def debug(message: str) -> None:
    """Trace to stderr when RUBBERAI_DEBUG=1. Off by default: hook stderr is noise
    in a coding session, but silence makes a misconfigured bridge impossible to
    diagnose, so the switch stays available."""
    if os.environ.get("RUBBERAI_DEBUG") == "1":
        sys.stderr.write("[rubberai] " + message + "\n")


def config_path() -> str:
    override = os.environ.get("RUBBERAI_CLAUDE_CONFIG")
    if override:
        return os.path.expanduser(override)
    base = os.environ.get("XDG_CONFIG_HOME") or os.path.expanduser("~/.config")
    return os.path.join(base, "rubberai", "claude-code.json")


def state_path(session_id: str) -> str:
    base = os.environ.get("XDG_STATE_HOME") or os.path.expanduser("~/.local/state")
    directory = os.path.join(base, "rubberai", "claude-code")
    os.makedirs(directory, mode=0o700, exist_ok=True)
    safe = "".join(c for c in session_id if c.isalnum() or c in "-_") or "unknown"
    return os.path.join(directory, safe + ".json")


def read_state(session_id: str) -> dict:
    try:
        with open(state_path(session_id)) as handle:
            return json.load(handle)
    except (OSError, ValueError):
        return {}


def write_state(session_id: str, state: dict) -> None:
    try:
        path = state_path(session_id)
        with open(path, "w") as handle:
            json.dump(state, handle)
        os.chmod(path, 0o600)
    except OSError:
        pass


def resolve_project(config: dict, cwd: str):
    """Return the config block for the innermost configured project containing cwd.

    Longest-prefix rather than exact match, so a session started in a subdirectory
    of a tracked repository is still attributed to that repository.
    """
    projects = config.get("projects") or {}
    cwd = os.path.realpath(cwd or os.getcwd())
    best, best_len = None, -1
    for root, project in projects.items():
        root = os.path.realpath(os.path.expanduser(root))
        if (cwd == root or cwd.startswith(root + os.sep)) and len(root) > best_len:
            best, best_len = project, len(root)
    return best


def post(config: dict, project: dict, events: list) -> bool:
    """POST one batch. Returns True only when rubberai has durably accepted it."""
    if not events:
        return True
    url = (project.get("url") or config.get("url") or "http://localhost:8080").rstrip("/")
    body = events[0] if len(events) == 1 else {"events": events}
    request = urllib.request.Request(
        url + "/api/v1/events",
        data=json.dumps(body).encode(),
        headers={
            "Content-Type": "application/json",
            "Authorization": "Bearer " + project["api_key"],
        },
    )
    try:
        with urllib.request.urlopen(request, timeout=HTTP_TIMEOUT) as response:
            debug("sent %d event(s): %s" % (len(events), response.read().decode()[:200]))
            return 200 <= response.status < 300
    except urllib.error.HTTPError as error:
        debug("HTTP %d: %s" % (error.code, error.read().decode()[:200]))
        return False
    except OSError as error:
        debug("network error: %s" % error)
        return False


def send(config: dict, project: dict, events: list) -> bool:
    ok = True
    for start in range(0, len(events), BATCH_LIMIT):
        if not post(config, project, events[start:start + BATCH_LIMIT]):
            ok = False
            break
    return ok


def base_event(config: dict, project: dict, payload: dict, event_type: str, event_id: str) -> dict:
    who = identity(payload.get("cwd") or os.getcwd())
    event = {
        "event_id": event_id,
        "event_type": event_type,
        "project_id": project["project_id"],
        "timestamp": now(),
        "agent": {"name": AGENT_NAME},
        # Git identity is the name a developer already signs work with, so it
        # identifies a person across machines where a local account name does not.
        "user_id": (config.get("user_id") or who.get("email")
                    or os.environ.get("USER") or "unknown"),
    }
    session_id = payload.get("session_id")
    if session_id:
        event["session_id"] = session_id
    if config.get("send_identity") and who:
        # Host and address are personal data, so they travel as metadata, which
        # the server discards unless the project collects in FULL mode. Identity
        # is therefore opt-in twice over, like diffs.
        # Plain values: the event is JSON-encoded once on the way out, and
        # pre-encoding here would store each value as a quoted string of a
        # string.
        event["metadata"] = dict(who)
    entrypoint = os.environ.get("CLAUDE_CODE_ENTRYPOINT")
    if entrypoint:
        event["ide"] = {"name": IDE_BY_ENTRYPOINT.get(entrypoint, entrypoint)}
    return event


def attach_turn(event: dict, state: dict) -> dict:
    """Link an event to the user prompt that caused it, so traces group per turn."""
    prompt_id = state.get("prompt_id")
    if prompt_id:
        event["prompt_id"] = prompt_id
        event["trace_id"] = prompt_id
    return event


def describe_change(tool_name: str, tool_input: dict, send_diffs: bool) -> dict:
    """Summarise what an edit did, from the tool call that performed it.

    Claude Code hands the adapter the exact strings it replaced, so the change can
    be described without re-reading the file - which would race with whatever the
    agent does next. Line counts are metadata and always reported; the diff text is
    source code, so it is only attached when explicitly enabled. The server drops
    it again unless the project stores diffs.
    """
    if tool_name == "Write":
        # A whole-file write: no prior content reaches the hook, so every line is
        # reported as added rather than guessing at what it replaced.
        after = tool_input.get("content") or ""
        before = ""
    else:
        before = tool_input.get("old_string") or ""
        after = tool_input.get("new_string") or ""
    before_lines = before.splitlines()
    after_lines = after.splitlines()
    # The replaced strings carry surrounding context, so their raw lengths overstate
    # the change. Counting only the lines the matcher reports as replaced or
    # inserted gives the figures a reader expects from "+2 -1".
    added = removed = 0
    for op, i1, i2, j1, j2 in difflib.SequenceMatcher(
        None, before_lines, after_lines, autojunk=False
    ).get_opcodes():
        if op != "equal":
            removed += i2 - i1
            added += j2 - j1
    change = {"lines_added": added, "lines_removed": removed}
    if before:
        change["before_hash"] = hashlib.sha256(before.encode()).hexdigest()
    if after:
        change["after_hash"] = hashlib.sha256(after.encode()).hexdigest()
    if send_diffs:
        diff = "\n".join(difflib.unified_diff(
            before_lines, after_lines, lineterm="", n=2,
            fromfile="before", tofile="after",
        ))
        if diff:
            change["diff"] = diff[:DIFF_LIMIT]
    return change


def git(cwd: str, *args, timeout: int = 5):
    """Run a read-only git command in cwd. Returns None outside a repository."""
    try:
        done = subprocess.run(
            ("git", "-C", cwd, *args),
            capture_output=True, text=True, timeout=timeout,
        )
    except (OSError, subprocess.SubprocessError):
        return None
    return done.stdout if done.returncode == 0 else None


def worktree_state(cwd: str):
    """Line counts per path for everything differing from HEAD, plus untracked files.

    Tool inputs only describe edits made through an edit tool. Most real work also
    changes files through the shell - sed, a heredoc, a formatter, a build - and
    those are invisible to a hook that inspects tool arguments. Asking git instead
    catches every one of them regardless of how the file was written, and gets
    gitignored paths excluded for free.

    Values are [added, removed, untracked] lists rather than tuples, because this
    is persisted as JSON and would otherwise compare unequal after a round trip.
    """
    numstat = git(cwd, "diff", "--numstat", "HEAD")
    if numstat is None:
        return None
    state = {}
    for line in numstat.splitlines():
        parts = line.split("\t")
        if len(parts) == 3:
            added, removed, path = parts
            # Binary files report "-"; record them as changed with no line counts.
            state[path] = [int(added) if added.isdigit() else 0,
                           int(removed) if removed.isdigit() else 0, False]
    untracked = git(cwd, "ls-files", "--others", "--exclude-standard")
    for path in (untracked or "").splitlines():
        if not path:
            continue
        try:
            with open(os.path.join(cwd, path), "rb") as handle:
                state[path] = [handle.read().count(b"\n") + 1, 0, True]
        except OSError:
            state[path] = [0, 0, True]
    return state


def worktree_changes(cwd: str, state: dict, send_diffs: bool):
    """Files changed since the last check, with the line delta for each."""
    current = worktree_state(cwd)
    if current is None:
        return None, None
    if "worktree" not in state:
        # First observation: record the baseline without reporting it. A tree that
        # was already dirty is not work this prompt did.
        return [], current
    previous = state["worktree"]
    changes = []
    for path, entry in current.items():
        added, removed, untracked = entry
        was = previous.get(path) or [0, 0, untracked]
        if [added, removed] == [was[0], was[1]]:
            continue
        change = {
            "path": path,
            # Untracked means the file did not exist in the repository before, so
            # it is a creation. A tracked file with new lines is a modification,
            # however it was written.
            "operation": "created" if untracked else "modified",
            "change_source": "AI",
            "evidence": AGENT_NAME + ":worktree",
            # The delta since the previous check, so a row reads as what this step
            # did rather than everything accumulated since the last commit.
            "lines_added": max(added - was[0], 0),
            "lines_removed": max(removed - was[1], 0),
        }
        if send_diffs:
            diff = git(cwd, "diff", "--unified=2", "HEAD", "--", path)
            if diff:
                change["diff"] = diff[:DIFF_LIMIT]
        changes.append(change)
    for path in previous:
        if path not in current:
            changes.append({
                "path": path, "operation": "deleted", "change_source": "AI",
                "evidence": AGENT_NAME + ":worktree",
            })
    return changes, current


_identity_cache = {}


def identity(cwd: str) -> dict:
    """Who and where, for attributing a prompt to a person and a machine.

    The email comes from git rather than the operating system account, because
    git config is the identity a developer has already chosen to sign their work
    with, and the specification lists Git identity as a supported user identity.
    Nothing is looked up over the network: the address is read from the local
    routing table, so no request leaves the machine to discover it.
    """
    if cwd in _identity_cache:
        return _identity_cache[cwd]
    who = {}
    email = (git(cwd, "config", "user.email") or "").strip()
    if email:
        who["email"] = email
    try:
        who["host"] = socket.gethostname()
    except OSError:
        pass
    try:
        # Connecting a UDP socket sends no packets; it only asks the kernel which
        # local address would reach that destination, which picks the real egress
        # interface rather than a docker bridge or loopback.
        probe = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
        try:
            probe.connect(("192.0.2.1", 9))   # TEST-NET-1, never routed
            who["ip"] = probe.getsockname()[0]
        finally:
            probe.close()
    except OSError:
        pass
    _identity_cache[cwd] = who
    return who


def language_for(path: str):
    return LANGUAGE_BY_SUFFIX.get(os.path.splitext(path or "")[1].lower())


def flush_transcript(config: dict, project: dict, payload: dict, state: dict) -> None:
    """Emit llm.request.completed for assistant messages written since last flush.

    Token usage never reaches a hook payload - it is only in the session transcript
    - so this tails the JSONL from a stored byte offset. The offset advances only
    on a delivered batch; a failed send leaves it so the next turn retries.
    """
    path = payload.get("transcript_path")
    if not path or not os.path.exists(path):
        return
    if "boundaries" not in state:
        # First use / old state / reset: start now, never replay old history.
        state.clear()
        state.update(offset=os.path.getsize(path), boundaries=[])
        write_state(payload.get("session_id") or "", state)
        return
    offset = state.get("offset", 0)
    try:
        size = os.path.getsize(path)
        # A shorter file than last time means a different or rewritten transcript.
        if offset > size:
            state.clear()
            state.update(offset=size, boundaries=[])
            write_state(payload.get("session_id") or "", state)
            return
        with open(path, "rb") as handle:
            handle.seek(offset)
            raw = handle.read()
            end = handle.tell()
    except OSError:
        return

    # Only whole lines are safe to parse; a trailing fragment is re-read next time.
    text = raw.decode("utf-8", "replace")
    complete, _, remainder = text.rpartition("\n")
    if not complete:
        return
    end -= len(remainder.encode("utf-8"))

    events = {}
    position = offset
    for line in complete.split("\n"):
        line_start = position
        position += len(line.encode("utf-8")) + 1
        turn = {}
        for boundary in state.get("boundaries", []):
            if boundary["offset"] <= line_start:
                turn = boundary
        if not line.strip():
            continue
        try:
            record = json.loads(line)
        except ValueError:
            continue
        if record.get("type") != "assistant":
            continue
        message = record.get("message") or {}
        usage = message.get("usage") or {}
        if not usage:
            continue
        # Claude Code writes its own local notices ("No response requested.",
        # "API Error: ...") into the transcript as assistant records with the
        # model "<synthetic>" and all-zero usage. No request reached a provider,
        # so recording them would inflate the request count with calls that never
        # happened. Genuine reported zero usage remains a measurement.
        if message.get("model") == SYNTHETIC_MODEL:
            continue
        # Normalize input to include cache reads/writes; both cache categories
        # are then subsets, matching the platform's input + output total.
        counts = {}
        fields = {"output_tokens": "output_tokens",
                  "cache_read_input_tokens": "cached_tokens",
                  "cache_creation_input_tokens": "cache_write_tokens"}
        for source, target in fields.items():
            if usage.get(source) is not None:
                counts[target] = int(usage[source])
        if usage.get("input_tokens") is not None:
            counts["input_tokens"] = (int(usage["input_tokens"])
                + int(usage.get("cache_read_input_tokens") or 0)
                + int(usage.get("cache_creation_input_tokens") or 0))
        thinking = (usage.get("output_tokens_details") or {}).get("thinking_tokens")
        if thinking is not None:
            counts["reasoning_tokens"] = int(thinking)

        # Provider message identity deduplicates response fragments; transcript
        # UUID is the fallback when a provider message ID is unavailable.
        identifier = record.get("uuid") or hashlib.sha256(line.encode()).hexdigest()
        if message.get("id"):
            identifier = hashlib.sha256((str(payload.get("session_id", "")) + ":" + str(message["id"])).encode()).hexdigest()
        event = base_event(
            config, project, payload, "llm.request.completed", "evt_llm_" + identifier
        )
        if record.get("timestamp"):
            event["timestamp"] = record["timestamp"]
        if message.get("model"):
            event["model"] = {"provider": "anthropic", "name": message["model"]}
        # A sidechain record is a subagent turn: real billable usage, but work the
        # agent did on its own rather than a turn the user drove. Labelled instead
        # of dropped, so token and cost totals stay complete while the dashboard
        # can still filter user-driven work apart from the agent's own.
        if record.get("isSidechain"):
            event["agent"] = {"name": AGENT_NAME, "type": "subagent"}
        event["usage"] = counts
        event["status"] = "success"
        if record.get("gitBranch"):
            event["branch"] = record["gitBranch"]
        # A response may have several transcript records (text/tool blocks).
        # Retain its last usage snapshot once, preserving its first turn link.
        previous = events.get(event["event_id"])
        if previous and previous.get("prompt_id"):
            turn = {"prompt_id": previous["prompt_id"]}
        events[event["event_id"]] = attach_turn(event, turn)

    if not events or send(config, project, list(events.values())):
        state["offset"] = end
        write_state(payload.get("session_id") or "", state)


def handle(config: dict, project: dict, payload: dict, event_name: str) -> None:
    session_id = payload.get("session_id") or ""
    state = read_state(session_id)
    pending = state.get("pending_prompts", [])
    if pending and send(config, project, pending):
        state.pop("pending_prompts", None)
        write_state(session_id, state)

    if event_name == "SessionStart":
        if "boundaries" not in state:
            path = payload.get("transcript_path")
            state = {"offset": os.path.getsize(path) if path and os.path.exists(path) else 0,
                     "boundaries": []}
        baseline = worktree_state(payload.get("cwd") or os.getcwd())
        if baseline is not None:
            state["worktree"] = baseline
        write_state(session_id, state)
        event = base_event(
            config, project, payload, "session.started", "evt_sess_" + (session_id or uuid.uuid4().hex)
        )
        if payload.get("cwd"):
            event["repository"] = os.path.basename(payload["cwd"].rstrip("/"))
        send(config, project, [event])
        return

    if event_name == "UserPromptSubmit":
        # One trace per user turn: every tool call and LLM request until the next
        # prompt is attributed to this ID.
        flush_transcript(config, project, payload, state)
        path = payload.get("transcript_path")
        boundary_offset = os.path.getsize(path) if path and os.path.exists(path) else state.get("offset", 0)
        prompt_id = "pmt_" + uuid.uuid4().hex
        state.setdefault("boundaries", []).append({"offset": boundary_offset, "prompt_id": prompt_id})
        state["prompt_id"] = prompt_id
        # Measure this turn's file changes from the moment the user asked, so
        # anything already uncommitted is not credited to this prompt.
        baseline = worktree_state(payload.get("cwd") or os.getcwd())
        if baseline is not None:
            state["worktree"] = baseline
        write_state(session_id, state)
        event = base_event(
            config, project, payload, "prompt.created", "evt_prompt_" + prompt_id
        )
        event["prompt_id"] = prompt_id
        event["trace_id"] = prompt_id
        if config.get("send_prompts") and payload.get("prompt"):
            # `prompt` is a plain string in the event schema, not an object - an
            # object is rejected as INVALID_EVENT. Truncated so one pasted wall of
            # text cannot push a batch past the server's 1 MiB body limit.
            event["prompt"] = payload["prompt"][:PROMPT_LIMIT]
        state.setdefault("pending_prompts", []).append(event)
        write_state(session_id, state)
        if send(config, project, state["pending_prompts"]):
            state.pop("pending_prompts", None)
            write_state(session_id, state)
        return

    if event_name in ("PostToolUse", "PostToolUseFailure"):
        failed = event_name == "PostToolUseFailure"
        tool_name = payload.get("tool_name") or "unknown"
        tool_input = payload.get("tool_input") or {}
        event = base_event(
            config,
            project,
            payload,
            "tool.failed" if failed else "tool.completed",
            "evt_tool_" + uuid.uuid4().hex,
        )
        event["tool_name"] = tool_name
        event["status"] = "failure" if failed else "success"
        events = [attach_turn(event, state)]

        # Ask git what actually changed. This covers every tool - a shell edit, a
        # formatter, a build - not just the edit tools, and it is what the tool
        # argument inspection below cannot see. Tool inputs remain the fallback
        # outside a git repository.
        changes, snapshot = worktree_changes(
            payload.get("cwd") or os.getcwd(), state, bool(config.get("send_diffs"))
        )
        if changes is None:
            operation = FILE_WRITE_TOOLS.get(tool_name)
            file_path = tool_input.get("file_path") or tool_input.get("notebook_path")
            if operation and file_path and not failed:
                changes = [dict(
                    {"path": file_path, "operation": operation, "change_source": "AI",
                     "evidence": AGENT_NAME + ":" + tool_name},
                    **describe_change(tool_name, tool_input, bool(config.get("send_diffs"))),
                )]
            else:
                changes = []
        else:
            state["worktree"] = snapshot
            write_state(payload.get("session_id") or "", state)

        for change in changes:
            file_event = base_event(
                config, project, payload,
                "file." + (change.get("operation") or "modified"),
                "evt_file_" + uuid.uuid4().hex,
            )
            file_event["file"] = change
            language = language_for(change["path"])
            if language:
                file_event["language"] = language
                file_event["file"]["language"] = language
            events.append(attach_turn(file_event, state))

        send(config, project, events)
        return

    if event_name == "Stop":
        flush_transcript(config, project, payload, state)
        return

    if event_name == "SessionEnd":
        flush_transcript(config, project, payload, read_state(session_id))
        send(
            config,
            project,
            [base_event(
                config, project, payload, "session.ended",
                "evt_sessend_" + (session_id or uuid.uuid4().hex),
            )],
        )
        return


def main() -> None:
    try:
        payload = json.loads(sys.stdin.read() or "{}")
    except ValueError:
        return
    event_name = payload.get("hook_event_name") or (sys.argv[1] if len(sys.argv) > 1 else "")
    if not event_name:
        return
    try:
        with open(config_path()) as config_file:
            config = json.load(config_file)
    except (OSError, ValueError):
        debug("no readable config at " + config_path())
        return
    project = resolve_project(config, payload.get("cwd") or "")
    if not project or not project.get("project_id") or not project.get("api_key"):
        # Not a tracked repository: stay silent rather than guessing a project.
        return
    handle(config, project, payload, event_name)


if __name__ == "__main__":
    try:
        main()
    except Exception as error:
        # Last resort: a bridge bug must never surface as a hook failure. It still
        # gets reported under RUBBERAI_DEBUG - swallowing exceptions with no way to
        # see them is how a bridge ends up silently sending nothing.
        debug("unhandled error: %r" % (error,))
    sys.exit(0)
