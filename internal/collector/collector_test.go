package collector

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestDurableRetryAndRestart(t *testing.T) {
	status := 503
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(b))
		w.WriteHeader(status)
	}))
	defer srv.Close()
	dir := t.TempDir()
	secret := strings.Repeat("x", 32)
	c, err := New(dir, srv.URL, "rai_test", secret)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/api/v1/events", strings.NewReader(`{"event_id":"evt_1"}`))
	req.Header.Set("Authorization", "Bearer "+secret)
	w := httptest.NewRecorder()
	c.Handler().ServeHTTP(w, req)
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	if _, err = c.FlushOne(context.Background()); err == nil {
		t.Fatal("expected outage")
	}
	_, pending, _, _ := c.usage()
	if pending != 1 {
		t.Fatal("lost buffered event")
	}
	c, err = New(dir, srv.URL, "rai_test", secret)
	if err != nil {
		t.Fatal(err)
	}
	status = 200
	if _, err = c.FlushOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, pending, _, _ = c.usage()
	if pending != 0 {
		t.Fatal("event not removed")
	}
	if len(bodies) != 2 || bodies[0] != bodies[1] {
		t.Fatal("retry changed event")
	}
	info, _ := os.Stat(dir)
	if info.Mode().Perm() != 0700 {
		t.Fatal("outbox is not private")
	}
}
func TestCollectorAuthAndCapacity(t *testing.T) {
	c, err := New(t.TempDir(), "http://localhost:1", "rai_test", strings.Repeat("x", 32))
	if err != nil {
		t.Fatal(err)
	}
	for _, authorized := range []bool{false, true} {
		c.MaxBytes = 1
		req := httptest.NewRequest("POST", "/api/v1/events", strings.NewReader(`{}`))
		if authorized {
			req.Header.Set("Authorization", "Bearer "+c.LocalToken)
		}
		w := httptest.NewRecorder()
		c.Handler().ServeHTTP(w, req)
		want := 401
		if authorized {
			want = 503
		}
		if w.Code != want {
			t.Fatalf("got %d want %d", w.Code, want)
		}
	}
}
