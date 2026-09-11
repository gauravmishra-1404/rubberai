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
	got, err := Estimate(u, Rates{Input: "0.1", Output: "0.2"})
	if err != nil || got != "0.500000000000" {
		t.Fatalf("%s %v", got, err)
	}
	if _, err = Estimate(Usage{}, Rates{Input: "1", Output: "2"}); err == nil {
		t.Fatal("estimated unknown usage")
	}
	// input_tokens is the whole input side with the cache counts as subsets, so
	// pricing charges the remainder at the input rate and each subset at its own:
	// 1M fresh @1 + 1M output @2 + 2M cache read @0.1 + 1M cache write @1.25.
	cached := Usage{Input: count(4000000), Output: count(1000000),
		Cached: count(2000000), CacheWrite: count(1000000)}
	got, err = Estimate(cached, Rates{Input: "1", Output: "2", CacheRead: "0.1", CacheWrite: "1.25"})
	if err != nil || got != "4.450000000000" {
		t.Fatalf("cache-aware estimate: got %s err %v", got, err)
	}
	// Charging the cache subsets at the input rate as well would double count
	// them; the whole-input figure alone must not be what gets billed.
	if naive, _ := Estimate(Usage{Input: count(4000000), Output: count(1000000)},
		Rates{Input: "1", Output: "2"}); naive == got {
		t.Fatal("cache tokens charged at the input rate")
	}
	// A priced component with no configured rate must not silently vanish from
	// the total: refusing is correct, quietly undercharging is not.
	if _, err = Estimate(cached, Rates{Input: "1", Output: "2"}); err == nil {
		t.Fatal("estimated cache tokens with no cache rate configured")
	}
	// Subsets larger than the input they belong to mean the counts disagree.
	if _, err = Estimate(Usage{Input: count(10), Output: count(1), Cached: count(99)},
		Rates{Input: "1", Output: "1", CacheRead: "1"}); err == nil {
		t.Fatal("accepted cache tokens exceeding input")
	}
	// A model that never reports cache tokens still needs no cache rates.
	if _, err = Estimate(u, Rates{Input: "1", Output: "2"}); err != nil {
		t.Fatalf("required a cache rate for usage without cache tokens: %v", err)
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
