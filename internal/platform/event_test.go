package platform

import (
	"encoding/json"
	"testing"
	"time"
)

func count(n int64) *int64 { return &n }
func baseEvent() Event {
	return Event{ID: "evt_1", Type: "llm.request.completed", ProjectID: "prj_1", Timestamp: time.Now().UTC()}
}
func TestUsageDoesNotDoubleCountSubsets(t *testing.T) {
	e := baseEvent()
	e.Usage = &Usage{Input: count(100), Output: count(50), Cached: count(20), CacheWrite: count(5), Reasoning: count(10)}
	if err := e.Validate(); err != nil {
		t.Fatal(err)
	}
	if *e.Usage.Total != 150 {
		t.Fatalf("total=%d", *e.Usage.Total)
	}
}
func TestPartialUsageRemainsUnknown(t *testing.T) {
	e := baseEvent()
	e.Usage = &Usage{Input: count(100)}
	if err := e.Validate(); err != nil {
		t.Fatal(err)
	}
	if e.Usage.Total != nil {
		t.Fatal("invented a total from incomplete usage")
	}
	e.Usage = &Usage{Total: count(99)}
	if err := e.Validate(); err != nil {
		t.Fatal(err)
	}
}
func TestPrivacy(t *testing.T) {
	for _, mode := range []string{"METADATA_ONLY", "REDACTED", "FULL"} {
		t.Run(mode, func(t *testing.T) {
			e := baseEvent()
			e.Prompt = "password=abc123 fix this"
			e.Metadata = map[string]json.RawMessage{"content": json.RawMessage(`"secret"`)}
			e.File = &File{Diff: "private source", Evidence: "agent event", ChangeSource: "AI"}
			e.ApplyPrivacy(mode, false)
			if e.File.Diff != "" {
				t.Fatal("disabled diff was retained")
			}
			if mode == "METADATA_ONLY" && e.Prompt != "" {
				t.Fatal("prompt retained")
			}
			if mode == "REDACTED" && e.Prompt != "[REDACTED] fix this" {
				t.Fatal(e.Prompt)
			}
			if mode != "FULL" && e.Metadata != nil {
				t.Fatal("arbitrary metadata retained")
			}
		})
	}
}
func TestAttributionNeedsEvidence(t *testing.T) {
	e := baseEvent()
	e.File = &File{Path: "x.go", ChangeSource: "AI"}
	if err := e.Validate(); err != nil {
		t.Fatal(err)
	}
	if e.File.ChangeSource != "UNKNOWN" {
		t.Fatal("unsupported attribution")
	}
	e.File.ChangeSource = "AI"
	e.File.Evidence = "agent edit event evt_2"
	if err := e.Validate(); err != nil {
		t.Fatal(err)
	}
	if e.File.ChangeSource != "AI" {
		t.Fatal("lost evidence-backed source")
	}
}
func TestCosts(t *testing.T) {
	u := Usage{Input: count(1000000), Output: count(2000000)}
	got, err := Estimate(u, "0.1", "0.2")
	if err != nil || got != "0.500000000000" {
		t.Fatalf("%s %v", got, err)
	}
	if _, err = Estimate(Usage{}, "1", "2"); err == nil {
		t.Fatal("estimated unknown usage")
	}
	for _, currency := range []string{"USD", "EUR", "INR"} {
		e := baseEvent()
		e.Cost = &Cost{Amount: "0.01", Currency: currency, Type: "actual"}
		if err := e.Validate(); err != nil {
			t.Fatal(err)
		}
	}
}
func TestValidation(t *testing.T) {
	for _, mutate := range []func(*Event){func(e *Event) { e.ID = "" }, func(e *Event) { e.Timestamp = time.Time{} }, func(e *Event) { e.Usage = &Usage{Input: count(-1)} }, func(e *Event) { e.Cost = &Cost{Amount: "NaN", Currency: "USD", Type: "actual"} }, func(e *Event) { e.Type = "prompt.created" }, func(e *Event) { e.Type = "file.modified" }} {
		e := baseEvent()
		mutate(&e)
		if e.Validate() == nil {
			t.Fatalf("accepted invalid event %+v", e)
		}
	}
	e := baseEvent()
	e.Type = "custom.future.event"
	if err := e.Validate(); err != nil {
		t.Fatal(err)
	}
}
