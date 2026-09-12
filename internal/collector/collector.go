// Package collector provides a bounded, durable, local outbox. Its HTTP endpoint
// acknowledges only after the event has been synced to disk.
package collector

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"rubberai/internal/event"
	"rubberai/internal/integrations"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Collector struct {
	Dir, Endpoint, Key, LocalToken  string
	MaxBytes                        int64
	Client                          *http.Client
	ProjectID, UserID, IDE, Privacy string
	received, skipped, uploaded     atomic.Int64
	mu                              sync.Mutex
	wake                            chan struct{}
}

func New(dir, endpoint, key, localToken string) (*Collector, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, errors.New("valid endpoint required")
	}
	if u.Scheme == "http" && u.Hostname() != "localhost" && u.Hostname() != "127.0.0.1" && u.Hostname() != "::1" {
		return nil, errors.New("remote endpoints require HTTPS")
	}
	if dir == "" || key == "" || len(localToken) < 32 {
		return nil, errors.New("outbox directory, project key, and a local token of at least 32 characters are required")
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	if err = os.Chmod(dir, 0700); err != nil {
		return nil, err
	}
	return &Collector{Dir: dir, Endpoint: strings.TrimRight(endpoint, "/") + "/api/v1/events", Key: key, LocalToken: localToken, MaxBytes: 32 << 20, Privacy: "METADATA_ONLY", wake: make(chan struct{}, 1), Client: &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (c *Collector) usage() (int64, int, int, error) {
	entries, err := os.ReadDir(c.Dir)
	if err != nil {
		return 0, 0, 0, err
	}
	var total int64
	pending, rejected := 0, 0
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			return 0, 0, 0, err
		}
		total += info.Size()
		if strings.HasSuffix(e.Name(), ".json") {
			pending++
		}
		if strings.HasSuffix(e.Name(), ".rejected") {
			rejected++
		}
	}
	return total, pending, rejected, nil
}
func (c *Collector) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") || subtle.ConstantTimeCompare([]byte(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")), []byte(c.LocalToken)) != 1 {
			http.Error(w, `{"error":{"code":"UNAUTHENTICATED"}}`, 401)
			return
		}
		if r.Method == "GET" && r.URL.Path == "/api/v1/status" {
			c.mu.Lock()
			size, pending, rejected, err := c.usage()
			c.mu.Unlock()
			if err != nil {
				http.Error(w, `{"error":{"code":"OUTBOX_UNAVAILABLE"}}`, 503)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"queue_bytes": size, "pending": pending, "rejected": rejected, "max_bytes": c.MaxBytes, "project_id": c.ProjectID, "privacy": c.Privacy, "received_events": c.received.Load(), "skipped_logs": c.skipped.Load(), "uploaded_batches": c.uploaded.Load(), "protocols": []string{"http-json", "otlp-http-json-logs"}})
			return
		}
		otlp := r.URL.Path == "/v1/logs"
		if r.Method != "POST" || (r.URL.Path != "/api/v1/events" && !otlp) {
			http.Error(w, `{"error":{"code":"NOT_FOUND"}}`, 404)
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
		if err != nil || !json.Valid(body) {
			http.Error(w, `{"error":{"code":"INVALID_JSON"}}`, 400)
			return
		}
		if r.Header.Get("Content-Encoding") != "" || (otlp && !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json")) {
			http.Error(w, `{"error":{"code":"UNSUPPORTED_ENCODING","message":"Use uncompressed OTLP HTTP JSON"}}`, 415)
			return
		}
		var events []event.Event
		skipped := 0
		if otlp {
			events, skipped, err = integrations.Logs(body, integrations.Options{ProjectID: c.ProjectID, UserID: c.UserID, IDE: c.IDE})
			if skipped > 0 {
				log.Printf("rubberai collector skipped unsupported OTLP event kinds: %v", integrations.EventNames(body))
			}
		} else {
			events, err = event.Decode(body)
		}
		if err != nil {
			http.Error(w, `{"error":{"code":"INVALID_EVENT","message":"Invalid event or OTLP mapping; check schema, project, timestamp and token fields"}}`, 400)
			return
		}
		for i := range events {
			if c.ProjectID != "" && events[i].ProjectID != c.ProjectID {
				http.Error(w, `{"error":{"code":"PROJECT_MISMATCH"}}`, 403)
				return
			}
			events[i].ApplyPrivacy(c.Privacy, false)
		}
		c.skipped.Add(int64(skipped))
		acknowledge := func() {
			if otlp {
				response := map[string]any{}
				if skipped > 0 {
					response["partialSuccess"] = map[string]any{"rejectedLogRecords": strconv.Itoa(skipped), "errorMessage": "Unsupported log records were skipped"}
				}
				_ = json.NewEncoder(w).Encode(response)
			} else {
				w.WriteHeader(202)
				_, _ = w.Write([]byte(`{"queued":true}`))
			}
		}
		if len(events) == 0 {
			acknowledge()
			return
		}
		body, err = json.Marshal(map[string]any{"events": events})
		if err != nil || len(body) > 1<<20 {
			http.Error(w, `{"error":{"code":"PAYLOAD_TOO_LARGE"}}`, 413)
			return
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		size, pending, rejected, err := c.usage()
		if err != nil || size+int64(len(body)) > c.MaxBytes || pending+rejected >= 1000 {
			http.Error(w, `{"error":{"code":"OUTBOX_FULL"}}`, 503)
			return
		}
		b := make([]byte, 16)
		if _, err = rand.Read(b); err != nil {
			http.Error(w, `{"error":{"code":"OUTBOX_UNAVAILABLE"}}`, 503)
			return
		}
		name := filepath.Join(c.Dir, time.Now().UTC().Format("20060102T150405.000000000")+"-"+hex.EncodeToString(b)+".json")
		f, err := os.OpenFile(name+".tmp", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			http.Error(w, `{"error":{"code":"OUTBOX_UNAVAILABLE"}}`, 503)
			return
		}
		_, err = f.Write(body)
		if err == nil {
			err = f.Sync()
		}
		closeErr := f.Close()
		if err == nil {
			err = closeErr
		}
		if err == nil {
			err = os.Rename(name+".tmp", name)
		}
		if err == nil {
			if d, e := os.Open(c.Dir); e == nil {
				if runtime.GOOS != "windows" {
					err = d.Sync()
				}
				_ = d.Close()
			} else {
				err = e
			}
		}
		if err != nil {
			_ = os.Remove(name + ".tmp")
			http.Error(w, `{"error":{"code":"OUTBOX_UNAVAILABLE"}}`, 503)
			return
		}
		select {
		case c.wake <- struct{}{}:
		default:
		}
		c.received.Add(int64(len(events)))
		acknowledge()
	})
}

// FlushOne retains requests on transport failures, auth failures, and server
// errors. Invalid payloads are quarantined, never silently discarded.
func (c *Collector) FlushOne(ctx context.Context) (bool, error) {
	entries, err := os.ReadDir(c.Dir)
	if err != nil {
		return false, err
	}
	var name string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") {
			name = filepath.Join(c.Dir, e.Name())
			break
		}
	}
	if name == "" {
		return false, nil
	}
	body, err := os.ReadFile(name)
	if err != nil {
		return true, err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", c.Endpoint, bytes.NewReader(body))
	if err != nil {
		return true, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.Key)
	res, err := c.Client.Do(req)
	if err != nil {
		return true, errors.New("upload failed; retained locally")
	}
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 4096))
	c.mu.Lock()
	defer c.mu.Unlock()
	if res.StatusCode >= 200 && res.StatusCode < 300 {
		err := os.Remove(name)
		if err == nil {
			c.uploaded.Add(1)
		}
		return true, err
	}
	switch res.StatusCode {
	case 400, 404, 413, 415, 422:
		if err = os.Rename(name, strings.TrimSuffix(name, ".json")+".rejected"); err != nil {
			return true, err
		}
		return true, nil
	}
	return true, errors.New("server did not accept upload; retained locally")
}
func (c *Collector) Run(ctx context.Context) {
	delay := time.Second
	for {
		if ctx.Err() != nil {
			return
		}
		pending, err := c.FlushOne(ctx)
		if err != nil {
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
			delay = min(delay*2, time.Minute)
			continue
		}
		delay = time.Second
		if pending {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-c.wake:
		case <-time.After(5 * time.Second):
		}
	}
}
