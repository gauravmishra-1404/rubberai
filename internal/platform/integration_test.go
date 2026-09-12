// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Gaurav Mishra

package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"
)

func TestPostgresEndToEnd(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL integration tests")
	}
	a, err := New(context.Background(), dsn, "http://localhost:8080")
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	srv := httptest.NewServer(a.Handler())
	defer srv.Close()
	jar, _ := cookiejar.New(nil)
	owner := &http.Client{Jar: jar}
	jar2, _ := cookiejar.New(nil)
	other := &http.Client{Jar: jar2}
	request := func(client *http.Client, method, path, key string, body any, want int) map[string]any {
		t.Helper()
		raw, _ := json.Marshal(body)
		req, _ := http.NewRequest(method, srv.URL+path, bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		if key != "" {
			req.Header.Set("Authorization", "Bearer "+key)
		}
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		data, _ := io.ReadAll(res.Body)
		if res.StatusCode != want {
			t.Fatalf("%s %s: got %d want %d: %s", method, path, res.StatusCode, want, data)
		}
		var out map[string]any
		_ = json.Unmarshal(data, &out)
		return out
	}
	prefix := "/api/v1"
	for _, client := range []*http.Client{owner, other} {
		request(client, "POST", prefix+"/auth/register", "", map[string]string{"username": token("test_"), "password": "secure-test-password", "display_name": "Test user", "organization": "Test org"}, 200)
	}
	p := request(owner, "POST", prefix+"/projects", "", map[string]string{"name": "Test project"}, 201)
	id := p["id"].(string)
	path := prefix + "/projects/" + id
	k := request(owner, "POST", path+"/keys", "", map[string]string{"name": "test"}, 201)
	key := k["api_key"].(string)
	request(other, "POST", path+"/keys", "", map[string]string{"name": "unauthorized"}, 404)
	request(other, "PATCH", path, "", map[string]string{"privacy": "FULL"}, 404)
	request(other, "DELETE", path+"/keys/"+k["id"].(string), "", nil, 404)
	request(other, "GET", path+"/analytics", "", nil, 404)
	request(http.DefaultClient, "GET", path+"/analytics", key, nil, 401)
	e := baseEvent()
	e.ProjectID = id
	e.ID = token("evt_")
	e.UserID = "external-user"
	e.TraceID = "trace_1"
	e.PromptID = "prompt_1"
	e.SessionID = "session_1"
	e.Usage = &Usage{Input: count(100), Output: count(50), Cached: count(20), Reasoning: count(10)}
	e.Prompt = "do not store"
	e.Cost = &Cost{Amount: "0.1", Currency: "USD", Type: "actual"}
	request(http.DefaultClient, "POST", prefix+"/events", "rai_invalid", e, 401)
	wrong := e
	wrong.ProjectID = "other_project"
	request(http.DefaultClient, "POST", prefix+"/events", key, wrong, 403)
	invalid := e
	invalid.ID = ""
	request(http.DefaultClient, "POST", prefix+"/events", key, invalid, 400)
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			raw, _ := json.Marshal(e)
			req, _ := http.NewRequest("POST", srv.URL+prefix+"/events", bytes.NewReader(raw))
			req.Header.Set("Authorization", "Bearer "+key)
			req.Header.Set("Content-Type", "application/json")
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Error(err)
				return
			}
			defer res.Body.Close()
			if res.StatusCode != 200 {
				t.Errorf("concurrent request returned %d", res.StatusCode)
			}
		}()
	}
	wg.Wait()
	duplicate := request(http.DefaultClient, "POST", prefix+"/events", key, e, 200)
	if duplicate["duplicates"] != float64(1) {
		t.Fatal(duplicate)
	}
	partial := e
	partial.ID = token("evt_")
	partial.Usage = &Usage{Total: count(77)}
	partial.Cost = &Cost{Amount: "0.2", Currency: "EUR", Type: "estimated"}
	partial.Timestamp = time.Now().Add(-24 * time.Hour)
	file := e
	file.ID = token("evt_")
	file.Type = "file.modified"
	file.Usage = nil
	file.Cost = nil
	file.File = &File{Path: "src/a.rs", Diff: "private", ChangeSource: "AI", LinesAdded: 5}
	prompt := file
	prompt.ID = token("evt_")
	prompt.Type = "prompt.created"
	prompt.File = nil
	prompt.Prompt = "secret prompt"
	request(http.DefaultClient, "POST", prefix+"/events", key, map[string]any{"events": []Event{partial, file, prompt}}, 200)
	stats := request(owner, "GET", path+"/analytics", "", nil, 200)
	totals := stats["totals"].(map[string]any)
	if totals["total_tokens"] != float64(227) || totals["requests"] != float64(2) || totals["files_changed"] != float64(1) || totals["prompts"] != float64(1) {
		t.Fatal(totals)
	}
	if len(stats["costs"].([]any)) != 2 {
		t.Fatal("currencies merged")
	}
	timeline := request(owner, "GET", path+"/events?trace_id=trace_1", "", nil, 200)
	items := timeline["events"].([]any)
	if len(items) != 4 {
		t.Fatal(timeline)
	}
	for _, item := range items {
		m := item.(map[string]any)
		if _, ok := m["prompt"]; ok {
			t.Fatal("prompt leaked")
		}
		if f, ok := m["file"].(map[string]any); ok {
			if _, exists := f["diff"]; exists || f["change_source"] != "UNKNOWN" {
				t.Fatal(f)
			}
		}
	}
	filtered := request(owner, "GET", path+"/analytics?user_id=someone_else", "", nil, 200)
	if filtered["totals"].(map[string]any)["events"] != float64(0) {
		t.Fatal("filter ignored")
	}
	page := request(owner, "GET", path+"/events?limit=2", "", nil, 200)
	if page["has_more"] != true || len(page["events"].([]any)) != 2 {
		t.Fatal(page)
	}
	request(owner, "GET", path+"/analytics?from=bad", "", nil, 400)
	request(owner, "PATCH", path, "", map[string]any{"privacy": "FULL", "track_diffs": true}, 200)
	file.ID = token("evt_")
	file.File.ChangeSource = "AI"
	file.File.Evidence = "tool call edit-1"
	request(http.DefaultClient, "POST", prefix+"/events", key, file, 200)
	full := request(owner, "GET", path+"/events?event_type=file.modified", "", nil, 200)
	found := false
	for _, item := range full["events"].([]any) {
		m := item.(map[string]any)
		if m["event_id"] == file.ID {
			f := m["file"].(map[string]any)
			found = f["diff"] == "private" && f["change_source"] == "AI"
		}
	}
	if !found {
		t.Fatal("full collection lost content")
	}

	// Human submissions define rows; orphan calls never create prompt rows.
	for _, pid := range []string{"prompt_2", "prompt_3"} {
		next := prompt
		next.ID = token("evt_")
		next.PromptID = pid
		next.TraceID = pid
		request(http.DefaultClient, "POST", prefix+"/events", key, next, 200)
	}
	orphan := e
	orphan.ID = token("evt_")
	orphan.PromptID = "internal_only"
	request(http.DefaultClient, "POST", prefix+"/events", key, orphan, 200)
	grouped := request(owner, "GET", path+"/analytics?group_by=prompt&user_prompts=true", "", nil, 200)
	groups := grouped["breakdown"].([]any)
	if len(groups) != 3 {
		t.Fatalf("want 3 user prompts: %v", grouped)
	}
	for _, raw := range groups {
		row := raw.(map[string]any)
		st := row["stats"].(map[string]any)
		if row["name"] == "prompt_1" {
			if st["input_tokens"] != float64(100) || st["reported_input_tokens"] != float64(1) || st["requests"] != float64(2) {
				t.Fatal(st)
			}
			if len(row["costs"].([]any)) != 2 {
				t.Fatal("group costs merged currencies")
			}
		} else if st["input_tokens"] != nil {
			t.Fatal("missing tokens became zero", st)
		}
	}
	request(owner, "DELETE", path+"/keys/"+k["id"].(string), "", nil, 200)
	request(http.DefaultClient, "POST", prefix+"/events", key, e, 401)
	request(owner, "POST", prefix+"/auth/logout", "", nil, 200)
	request(owner, "GET", path+"/analytics", "", nil, 401)
}
