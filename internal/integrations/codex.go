package integrations

import (
	"rubberai/internal/event"
	"strings"
)

// Codex 0.153.x exports Rust's tracing call-site as OTLP event.name instead of
// its semantic event.name attribute. Map only the documented call sites for
// that release; unknown call sites stay unassigned rather than guessed.
func codexSourceKind(kind string) string {
	switch {
	case strings.HasSuffix(kind, "otel/src/events/session_telemetry.rs:1059"):
		return "codex.user_prompt"
	case strings.HasSuffix(kind, "otel/src/events/session_telemetry.rs:1012"):
		return "codex.sse_event"
	case strings.HasSuffix(kind, "otel/src/events/session_telemetry.rs:772"):
		return "codex.websocket_request"
	case strings.HasSuffix(kind, "otel/src/tool_result.rs:54"):
		return "codex.tool_result"
	default:
		return kind
	}
}

func codex(log Log, a map[string]string, kind string, o Options) (event.Event, bool, error) {
	target := ""
	switch kind {
	case "codex.conversation_starts":
		target = "session.started"
	case "codex.user_prompt":
		target = "prompt.created"
	case "codex.sse_event", "codex.websocket_event":
		if first(a["event.kind"], a["event_kind"], a["kind"], a["type"]) != "response.completed" {
			return event.Event{}, false, nil
		}
		target = "llm.request.completed"
	case "codex.tool_result":
		target = "tool.completed"
		if a["success"] == "false" {
			target = "tool.failed"
		}
	default:
		return event.Event{}, false, nil
	}
	e, err := base(log, a, o, target)
	if err != nil {
		return e, false, err
	}
	e.Agent = event.Named{Name: "codex", Version: first(a["app.version"], a["version"])}
	e.SessionID = first(a["conversation.id"], a["conversation_id"], a["session.id"])
	e.Model.Name = first(a["model"], a["gen_ai.response.model"])
	// Turn IDs, when exported, are the evidence linking requests to user input.
	// Never assign an internal call to the most recently received user prompt.
	turn := first(a["turn.id"], a["turn_id"])
	if turn != "" {
		e.PromptID = "pmt_" + hashID(o.ProjectID, e.SessionID, turn)
		e.TraceID = e.PromptID
	}
	if target == "prompt.created" {
		if e.PromptID == "" {
			e.PromptID = "pmt_" + hashID(e.ID)
			e.TraceID = e.PromptID
		}
		e.ID = "evt_prompt_" + hashID(o.ProjectID, e.PromptID)
		if a["prompt"] != "[REDACTED]" {
			e.Prompt = a["prompt"]
		}
	}
	if target == "llm.request.completed" {
		e.Usage, err = usage(a, map[string]string{"input_token_count": "input_tokens", "output_token_count": "output_tokens", "cached_token_count": "cached_tokens", "reasoning_token_count": "reasoning_tokens", "total_token_count": "total_tokens"})
		if response := first(a["response.id"], a["response_id"]); response != "" {
			e.ID = "evt_response_" + hashID(o.ProjectID, e.SessionID, response)
		}
	}
	e.ToolName = first(a["tool_name"], a["tool.name"])
	e.ToolCallID = first(a["call_id"], a["tool.call_id"])
	if e.ToolCallID != "" && (target == "tool.completed" || target == "tool.failed") {
		e.ID = "evt_tool_" + hashID(o.ProjectID, e.SessionID, e.ToolCallID, target)
	}
	e.Status = a["success"]
	return e, true, err
}
