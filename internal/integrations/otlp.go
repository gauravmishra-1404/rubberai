// Package integrations translates external protocols without coupling storage to tools.
package integrations

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"rubberai/internal/event"
)

type Value struct {
	String *string         `json:"stringValue"`
	Int    json.RawMessage `json:"intValue"`
	Double *float64        `json:"doubleValue"`
	Bool   *bool           `json:"boolValue"`
}

func (v Value) Text() string {
	if v.String != nil {
		return *v.String
	}
	if len(v.Int) > 0 {
		return strings.Trim(string(v.Int), `"`)
	}
	if v.Double != nil {
		return strconv.FormatFloat(*v.Double, 'f', -1, 64)
	}
	if v.Bool != nil {
		return strconv.FormatBool(*v.Bool)
	}
	return ""
}

type Attribute struct {
	Key   string `json:"key"`
	Value Value  `json:"value"`
}
type Log struct {
	Time       string      `json:"timeUnixNano"`
	Observed   string      `json:"observedTimeUnixNano"`
	Name       string      `json:"eventName"`
	Body       Value       `json:"body"`
	Attributes []Attribute `json:"attributes"`
	Trace      string      `json:"traceId"`
	Span       string      `json:"spanId"`
}
type Export struct {
	Resources []struct {
		Resource struct {
			Attributes []Attribute `json:"attributes"`
		} `json:"resource"`
		Scopes []struct {
			Logs []Log `json:"logRecords"`
		} `json:"scopeLogs"`
	} `json:"resourceLogs"`
}

type Options struct{ ProjectID, UserID, IDE string }

// EventNames exposes only event kind names for adapter diagnostics. It never
// returns a prompt, body, attribute value, identifier, or other event content.
func EventNames(raw []byte) []string {
	var batch Export
	if json.Unmarshal(raw, &batch) != nil {
		return nil
	}
	seen := map[string]bool{}
	names := []string{}
	for _, resource := range batch.Resources {
		for _, scope := range resource.Scopes {
			for _, log := range scope.Logs {
				a := attributes(resource.Resource.Attributes, log.Attributes)
				name := first(a["otel.name"], a["event.name"], log.Name)
				if name != "" && !seen[name] && len(names) < 20 {
					seen[name] = true
					names = append(names, name)
				}
			}
		}
	}
	return names
}

func hashID(parts ...string) string {
	h := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(h[:])
}
func first(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
func attributes(base []Attribute, extra []Attribute) map[string]string {
	out := map[string]string{}
	for _, list := range [][]Attribute{base, extra} {
		for _, a := range list {
			out[a.Key] = a.Value.Text()
		}
	}
	return out
}

// Logs accepts OTLP/HTTP JSON logs only. No event is correlated by arrival order.
// Unknown logs are explicitly reported as skipped, never converted into user prompts.
func Logs(raw []byte, options Options) ([]event.Event, int, error) {
	if !event.ValidIdentifier(options.ProjectID) {
		return nil, 0, errors.New("collector project_id is required")
	}
	var batch Export
	if err := json.Unmarshal(raw, &batch); err != nil || batch.Resources == nil {
		return nil, 0, errors.New("expected OTLP JSON resourceLogs")
	}
	out := []event.Event{}
	skipped := 0
	seen := map[string]bool{}
	records := 0
	for _, resource := range batch.Resources {
		for _, scope := range resource.Scopes {
			for _, log := range scope.Logs {
				records++
				if records > 1000 {
					return nil, 0, errors.New("at most 1000 log records per export")
				}
				a := attributes(resource.Resource.Attributes, log.Attributes)
				// Codex uses otel.name for the semantic event name. In current
				// releases, event.name is overwritten by Rust's tracing call-site
				// (for example, "event …session_telemetry.rs:1012"). Prefer the
				// semantic attribute before falling back to standard OTLP fields.
				kind := codexSourceKind(first(a["otel.name"], a["event.name"], log.Name))
				var e event.Event
				var ok bool
				var err error
				if strings.HasPrefix(kind, "codex.") {
					e, ok, err = codex(log, a, kind, options)
				} else {
					e, ok, err = generic(log, a, options)
				}
				if err != nil {
					return nil, 0, err
				}
				if !ok {
					skipped++
					continue
				}
				if err = e.Validate(); err != nil {
					return nil, 0, err
				}
				if !seen[e.ID] {
					out = append(out, e)
					seen[e.ID] = true
				}
				if len(out) > 100 {
					return nil, 0, errors.New("at most 100 normalized events per export")
				}
			}
		}
	}
	return out, skipped, nil
}

func base(log Log, a map[string]string, o Options, kind string) (event.Event, error) {
	stamp := first(log.Time, log.Observed)
	ns, err := strconv.ParseInt(stamp, 10, 64)
	if err != nil || ns <= 0 {
		return event.Event{}, errors.New("log requires timeUnixNano or observedTimeUnixNano")
	}
	if supplied := a["rubberai.project_id"]; supplied != "" && supplied != o.ProjectID {
		return event.Event{}, errors.New("project does not match collector connection")
	}
	canonical, _ := json.Marshal(a)
	return event.Event{ID: "evt_otel_" + hashID(o.ProjectID, stamp, log.Trace, log.Span, kind, string(canonical)), Type: kind, ProjectID: o.ProjectID, Timestamp: time.Unix(0, ns).UTC(), UserID: first(a["rubberai.user_id"], o.UserID), IDE: event.Named{Name: first(a["ide.name"], o.IDE)}, Agent: event.Named{Name: first(a["agent.name"], a["service.name"])}, Model: event.Model{Provider: a["gen_ai.provider.name"], Name: first(a["gen_ai.response.model"], a["model"])}, SessionID: a["session.id"], Repository: a["repository"], Branch: a["branch"]}, nil
}
func usage(a map[string]string, fields map[string]string) (*event.Usage, error) {
	u := &event.Usage{}
	dest := map[string]**int64{"input_tokens": &u.Input, "output_tokens": &u.Output, "cached_tokens": &u.Cached, "cache_write_tokens": &u.CacheWrite, "reasoning_tokens": &u.Reasoning, "total_tokens": &u.Total}
	found := false
	for source, target := range fields {
		raw, ok := a[source]
		if !ok || raw == "" {
			continue
		}
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || n < 0 || n > 1_000_000_000_000 {
			return nil, errors.New("invalid token measurement")
		}
		*dest[target] = &n
		found = true
	}
	if !found {
		return nil, nil
	}
	return u, nil
}
func generic(log Log, a map[string]string, o Options) (event.Event, bool, error) {
	kind := a["rubberai.event_type"]
	if kind == "" {
		return event.Event{}, false, nil
	}
	if kind == "prompt.created" && a["rubberai.prompt_origin"] != "human" {
		return event.Event{}, false, nil
	}
	e, err := base(log, a, o, kind)
	if err != nil {
		return e, false, err
	}
	if id := a["rubberai.event_id"]; id != "" {
		e.ID = id
	}
	e.PromptID = a["rubberai.prompt_id"]
	e.TraceID = a["rubberai.trace_id"]
	e.Prompt = a["rubberai.prompt"]
	e.ToolName = a["tool.name"]
	e.ToolCallID = a["tool.call_id"]
	e.Status = a["status"]
	e.Language = a["language"]
	e.CommitSHA = a["commit_sha"]
	if path := a["file.path"]; path != "" {
		e.File = &event.File{Path: path, OldPath: a["file.old_path"], Operation: a["file.operation"], ChangeSource: a["file.change_source"], Evidence: a["file.evidence"]}
	}
	if kind == "llm.request.completed" {
		e.Usage, err = usage(a, map[string]string{"gen_ai.usage.input_tokens": "input_tokens", "gen_ai.usage.output_tokens": "output_tokens", "gen_ai.usage.cache_read.input_tokens": "cached_tokens", "gen_ai.usage.cache_write.input_tokens": "cache_write_tokens", "gen_ai.usage.reasoning.output_tokens": "reasoning_tokens", "rubberai.total_tokens": "total_tokens"})
	}
	return e, true, err
}
