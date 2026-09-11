package event

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"strings"
	"time"
)

type Named struct {
	Name    string `json:"name,omitempty"`
	Version string `json:"version,omitempty"`
	Type    string `json:"type,omitempty"`
}
type Model struct {
	Provider string `json:"provider,omitempty"`
	Name     string `json:"name,omitempty"`
}
type Usage struct {
	Input      *int64 `json:"input_tokens,omitempty"`
	Output     *int64 `json:"output_tokens,omitempty"`
	CacheWrite *int64 `json:"cache_write_tokens,omitempty"`
	Cached     *int64 `json:"cached_tokens,omitempty"`
	Reasoning  *int64 `json:"reasoning_tokens,omitempty"`
	Total      *int64 `json:"total_tokens,omitempty"`
}
type Cost struct {
	Amount         string `json:"amount"`
	Currency       string `json:"currency"`
	Type           string `json:"type"`
	PricingVersion string `json:"pricing_version,omitempty"`
}
type File struct {
	Path         string `json:"path"`
	OldPath      string `json:"old_path,omitempty"`
	Language     string `json:"language,omitempty"`
	Operation    string `json:"operation,omitempty"`
	LinesAdded   int64  `json:"lines_added,omitempty"`
	LinesRemoved int64  `json:"lines_removed,omitempty"`
	Diff         string `json:"diff,omitempty"`
	BeforeHash   string `json:"before_hash,omitempty"`
	AfterHash    string `json:"after_hash,omitempty"`
	ChangeSource string `json:"change_source,omitempty"`
	Evidence     string `json:"evidence,omitempty"`
}
type Event struct {
	ID         string                     `json:"event_id"`
	Type       string                     `json:"event_type"`
	ProjectID  string                     `json:"project_id"`
	UserID     string                     `json:"user_id,omitempty"`
	SessionID  string                     `json:"session_id,omitempty"`
	TraceID    string                     `json:"trace_id,omitempty"`
	PromptID   string                     `json:"prompt_id,omitempty"`
	Timestamp  time.Time                  `json:"timestamp"`
	ReceivedAt time.Time                  `json:"received_at,omitempty"`
	Agent      Named                      `json:"agent,omitempty"`
	IDE        Named                      `json:"ide,omitempty"`
	Model      Model                      `json:"model,omitempty"`
	Language   string                     `json:"language,omitempty"`
	Usage      *Usage                     `json:"usage,omitempty"`
	Cost       *Cost                      `json:"cost,omitempty"`
	File       *File                      `json:"file,omitempty"`
	Prompt     string                     `json:"prompt,omitempty"`
	ToolName   string                     `json:"tool_name,omitempty"`
	ToolCallID string                     `json:"tool_call_id,omitempty"`
	Status     string                     `json:"status,omitempty"`
	DurationMS *int64                     `json:"duration_ms,omitempty"`
	Repository string                     `json:"repository,omitempty"`
	Branch     string                     `json:"branch,omitempty"`
	CommitSHA  string                     `json:"commit_sha,omitempty"`
	Metadata   map[string]json.RawMessage `json:"metadata,omitempty"`
}

var identifier = regexp.MustCompile(`^[a-zA-Z0-9_.:@/-]{1,160}$`)
var currencyCode = regexp.MustCompile(`^[A-Z]{3}$`)
var decimalAmount = regexp.MustCompile(`^[0-9]{1,16}(\.[0-9]{1,12})?$`)
var secretPattern = regexp.MustCompile(`(?i)(bearer\s+\S+|(?:api[_-]?key|password|secret|token)\s*[:=]\s*\S+|\b(?:sk-|rai_)[A-Za-z0-9_-]+)`)

func (e *Event) Validate() error {
	if !identifier.MatchString(e.ID) || !identifier.MatchString(e.Type) || !identifier.MatchString(e.ProjectID) {
		return errors.New("event_id, event_type and project_id must be identifiers of 1–160 characters")
	}
	for _, id := range []string{e.UserID, e.SessionID, e.TraceID, e.PromptID, e.ToolCallID} {
		if id != "" && !identifier.MatchString(id) {
			return errors.New("invalid correlation identifier")
		}
	}
	if e.Timestamp.IsZero() || e.Timestamp.After(time.Now().Add(5*time.Minute)) {
		return errors.New("timestamp is required and cannot be more than five minutes in the future")
	}
	if e.Type == "prompt.created" && e.PromptID == "" {
		return errors.New("prompt_id is required for prompt.created")
	}
	if strings.HasPrefix(e.Type, "file.") && (e.File == nil || e.File.Path == "") {
		return errors.New("file.path is required for file events")
	}
	if e.Usage != nil {
		for _, v := range []*int64{e.Usage.Input, e.Usage.Output, e.Usage.Cached, e.Usage.CacheWrite, e.Usage.Reasoning, e.Usage.Total} {
			if v != nil && (*v < 0 || *v > 1_000_000_000_000) {
				return errors.New("token counts must be between 0 and 1000000000000")
			}
		}
		if e.Usage.Total == nil && e.Usage.Input != nil && e.Usage.Output != nil {
			n := *e.Usage.Input + *e.Usage.Output
			e.Usage.Total = &n
		}
		if e.Type != "llm.request.completed" {
			return errors.New("usage belongs on llm.request.completed to avoid double counting lifecycle events")
		}
	}
	if e.Cost != nil {
		if !decimalAmount.MatchString(e.Cost.Amount) || !currencyCode.MatchString(e.Cost.Currency) || (e.Cost.Type != "actual" && e.Cost.Type != "estimated") {
			return errors.New("cost needs a non-negative decimal string amount, three-letter currency and actual/estimated type")
		}
		if e.Type != "llm.request.completed" {
			return errors.New("cost belongs on llm.request.completed")
		}
	}
	if e.File != nil {
		if e.File.LinesAdded < 0 || e.File.LinesRemoved < 0 || e.File.LinesAdded > 1_000_000_000 || e.File.LinesRemoved > 1_000_000_000 {
			return errors.New("invalid line counts")
		}
		switch e.File.ChangeSource {
		case "", "UNKNOWN":
			e.File.ChangeSource = "UNKNOWN"
		case "AI", "HUMAN":
			if e.File.Evidence == "" {
				e.File.ChangeSource = "UNKNOWN"
			}
		default:
			return errors.New("change_source must be AI, HUMAN or UNKNOWN")
		}
	}
	if e.Model.Name == "" {
		e.Model.Name = "unknown"
	}
	if e.Language == "" {
		e.Language = "unknown"
	}
	return nil
}

func (e *Event) ApplyPrivacy(mode string, diffs bool) {
	if mode == "METADATA_ONLY" {
		e.Prompt = ""
		e.Metadata = nil
		if e.File != nil {
			e.File.Diff = ""
			e.File.Evidence = ""
		}
	}
	if mode == "REDACTED" {
		e.Prompt = secretPattern.ReplaceAllString(e.Prompt, "[REDACTED]")
		e.Metadata = nil
		if e.File != nil {
			e.File.Diff = ""
			e.File.Evidence = ""
		}
	}
	if !diffs && e.File != nil {
		e.File.Diff = ""
	}
}

// Estimate uses caller-configured rates, never a guessed provider price. Cached and
// reasoning counts are subsets and are not added to input/output a second time.
func Estimate(u Usage, inputRate, outputRate string) (string, error) {
	if u.Input == nil || u.Output == nil {
		return "", errors.New("input and output tokens are required to estimate cost")
	}
	a, ok := new(big.Rat).SetString(inputRate)
	if !ok || a.Sign() < 0 {
		return "", errors.New("invalid input rate")
	}
	b, ok := new(big.Rat).SetString(outputRate)
	if !ok || b.Sign() < 0 {
		return "", errors.New("invalid output rate")
	}
	a.Mul(a, new(big.Rat).SetInt64(*u.Input))
	b.Mul(b, new(big.Rat).SetInt64(*u.Output))
	a.Add(a, b)
	a.Quo(a, big.NewRat(1_000_000, 1))
	return a.FloatString(12), nil
}

func addDecimal(a, b string) string {
	x, ok := new(big.Rat).SetString(a)
	if !ok {
		x = new(big.Rat)
	}
	y, ok := new(big.Rat).SetString(b)
	if !ok {
		return a
	}
	return x.Add(x, y).FloatString(12)
}
func costKey(c *Cost) string { return fmt.Sprintf("%s:%s", c.Currency, c.Type) }

// ValidIdentifier is shared by configuration and event validation.
func ValidIdentifier(s string) bool { return identifier.MatchString(s) }
func ValidPrice(amount, currency string) bool { return decimalAmount.MatchString(amount) && currencyCode.MatchString(currency) }

// Decode accepts the public single-event or batch envelope.
func Decode(raw []byte) ([]Event, error) {
 var envelope struct { Events []Event `json:"events"` }
 if err := json.Unmarshal(raw,&envelope); err != nil { return nil,err }
 events := envelope.Events
 if events == nil { var e Event; if err:=json.Unmarshal(raw,&e);err!=nil{return nil,err}; events=[]Event{e} }
 if len(events)<1 || len(events)>100 {return nil,errors.New("send 1–100 events")}
 for i:=range events { if err:=events[i].Validate();err!=nil{return nil,err} }
 return events,nil
}
