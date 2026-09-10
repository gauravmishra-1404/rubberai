package platform

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"
)

func (a *App) ingest(w http.ResponseWriter, r *http.Request) {
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer rai_") || len(auth) > 200 {
		fail(w, 401, "INVALID_API_KEY", "A project ingestion key is required")
		return
	}
	// Resolve scope before accepting a large payload. Lock key and privacy settings
	// throughout the transaction so revocation and privacy changes serialize safely.
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		fail(w, 503, "STORAGE_ERROR", "Database unavailable")
		return
	}
	defer tx.Rollback(r.Context())
	var keyID, projectID, orgID, privacy string
	var diffs bool
	err = tx.QueryRow(r.Context(), `SELECT k.id,p.id,p.organization_id,p.privacy,p.track_diffs FROM api_keys k JOIN projects p ON p.id=k.project_id WHERE k.token_hash=$1 AND k.revoked_at IS NULL FOR SHARE OF k,p`, digest(strings.TrimPrefix(auth, "Bearer "))).Scan(&keyID, &projectID, &orgID, &privacy, &diffs)
	if err != nil {
		fail(w, 401, "INVALID_API_KEY", "Invalid or revoked ingestion key")
		return
	}
	if !a.limiter.allow("key:"+keyID, a.keyLimit) || !a.limiter.allow("project:"+projectID, a.projectLimit) || !a.limiter.allow("org:"+orgID, a.orgLimit) {
		w.Header().Set("Retry-After", "60")
		fail(w, 429, "RATE_LIMITED", "Retry in one minute")
		return
	}
	var raw json.RawMessage
	if !decode(w, r, &raw) {
		return
	}
	var batch []Event
	var envelope struct {
		Events []Event `json:"events"`
	}
	if json.Unmarshal(raw, &envelope) != nil {
		fail(w, 400, "INVALID_EVENT", "Expected an event or events envelope")
		return
	}
	if envelope.Events != nil {
		batch = envelope.Events
	} else {
		var e Event
		if json.Unmarshal(raw, &e) != nil {
			fail(w, 400, "INVALID_EVENT", "Invalid event fields")
			return
		}
		batch = []Event{e}
	}
	if len(batch) == 0 || len(batch) > 100 {
		fail(w, 400, "INVALID_BATCH", "Send between 1 and 100 events")
		return
	}
	for i := range batch {
		e := &batch[i]
		if e.ProjectID != projectID {
			fail(w, 403, "PROJECT_MISMATCH", "API key does not grant access to this project")
			return
		}
		if err := e.Validate(); err != nil {
			a.failures.Add(1)
			fail(w, 400, "INVALID_EVENT", err.Error())
			return
		}
		if e.Cost == nil && e.Usage != nil {
			if p, ok := a.prices[e.Model.Provider+"/"+e.Model.Name]; ok {
				if amount, err := Estimate(*e.Usage, p.Input, p.Output); err == nil {
					e.Cost = &Cost{amount, p.Currency, "estimated", p.Version}
				}
			}
		}
		e.ApplyPrivacy(privacy, diffs)
		e.ReceivedAt = time.Now().UTC()
	}
	accepted, duplicates := 0, 0
	for _, e := range batch {
		data, err := json.Marshal(e)
		if err != nil {
			fail(w, 400, "INVALID_EVENT", "Could not encode event")
			return
		}
		tag, err := tx.Exec(r.Context(), `INSERT INTO events(project_id,event_id,event_type,user_id,session_id,trace_id,prompt_id,timestamp,payload) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) ON CONFLICT(project_id,event_id) DO NOTHING`, e.ProjectID, e.ID, e.Type, e.UserID, e.SessionID, e.TraceID, e.PromptID, e.Timestamp, data)
		if err != nil {
			a.failures.Add(1)
			fail(w, 503, "STORAGE_ERROR", "Could not store events; retry with the same event IDs")
			return
		}
		if tag.RowsAffected() == 0 {
			duplicates++
		} else {
			accepted++
		}
	}
	if tx.Commit(r.Context()) != nil {
		fail(w, 503, "STORAGE_ERROR", "Commit uncertain; retry with the same event IDs")
		return
	}
	a.accepted.Add(int64(accepted))
	a.duplicates.Add(int64(duplicates))
	writeJSON(w, 200, map[string]int{"accepted": accepted, "duplicates": duplicates})
}

func filters(r *http.Request) (string, []any, error) {
	q := r.URL.Query()
	to := time.Now().Add(5 * time.Minute)
	from := time.Now().AddDate(0, 0, -30)
	var err error
	if q.Get("from") != "" {
		from, err = time.Parse(time.RFC3339, q.Get("from"))
		if err != nil {
			return "", nil, err
		}
	}
	if q.Get("to") != "" {
		to, err = time.Parse(time.RFC3339, q.Get("to"))
		if err != nil {
			return "", nil, err
		}
	}
	if from.After(to) {
		return "", nil, strconv.ErrSyntax
	}
	where := `project_id=$1 AND timestamp >= $2 AND timestamp <= $3`
	args := []any{r.PathValue("project"), from, to}
	for _, f := range []struct{ key, expr string }{{"user_id", "user_id"}, {"session_id", "session_id"}, {"trace_id", "trace_id"}, {"prompt_id", "prompt_id"}, {"event_type", "event_type"}, {"agent", "payload->'agent'->>'name'"}, {"ide", "payload->'ide'->>'name'"}, {"model", "payload->'model'->>'name'"}, {"language", "payload->>'language'"}, {"repository", "payload->>'repository'"}, {"branch", "payload->>'branch'"}} {
		if v := q.Get(f.key); v != "" {
			args = append(args, v)
			where += " AND " + f.expr + "=$" + strconv.Itoa(len(args))
		}
	}
	return where, args, nil
}

func (a *App) events(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.project(w, r); !ok {
		return
	}
	where, args, err := filters(r)
	if err != nil {
		fail(w, 400, "INVALID_FILTER", "Provide ordered RFC3339 from/to timestamps")
		return
	}
	limit := 100
	if s := r.URL.Query().Get("limit"); s != "" {
		limit, err = strconv.Atoi(s)
		if err != nil || limit < 1 || limit > 1000 {
			fail(w, 400, "INVALID_LIMIT", "limit must be 1–1000")
			return
		}
	}
	offset := 0
	if s := r.URL.Query().Get("offset"); s != "" {
		offset, err = strconv.Atoi(s)
		if err != nil || offset < 0 || offset > 1000000 {
			fail(w, 400, "INVALID_OFFSET", "Invalid offset")
			return
		}
	}
	args = append(args, limit+1, offset)
	rows, err := a.db.Query(r.Context(), `SELECT payload FROM events WHERE `+where+` ORDER BY timestamp,event_id LIMIT $`+strconv.Itoa(len(args)-1)+` OFFSET $`+strconv.Itoa(len(args)), args...)
	if err != nil {
		fail(w, 500, "STORAGE_ERROR", "Could not load events")
		return
	}
	defer rows.Close()
	out := []json.RawMessage{}
	for rows.Next() {
		var raw []byte
		if rows.Scan(&raw) != nil {
			fail(w, 500, "STORAGE_ERROR", "Could not decode events")
			return
		}
		out = append(out, raw)
	}
	if rows.Err() != nil {
		fail(w, 500, "STORAGE_ERROR", "Could not load events")
		return
	}
	more := len(out) > limit
	if more {
		out = out[:limit]
	}
	writeJSON(w, 200, map[string]any{"events": out, "has_more": more, "next_offset": offset + len(out)})
}

const aggregateSQL = `jsonb_build_object(
 'events',count(*),'prompts',count(*) FILTER(WHERE event_type='prompt.created'),
 'requests',count(*) FILTER(WHERE event_type='llm.request.completed'),
 'failures',count(*) FILTER(WHERE event_type IN ('llm.request.failed','tool.failed')),
 'sessions',count(DISTINCT NULLIF(session_id,'')),'traces',count(DISTINCT NULLIF(trace_id,'')),
 'users',count(DISTINCT NULLIF(user_id,'')),
 'files_changed',count(DISTINCT payload->'file'->>'path') FILTER(WHERE event_type IN ('file.created','file.modified','file.deleted','file.renamed')),
 'lines_added',coalesce(sum((payload->'file'->>'lines_added')::numeric),0),
 'lines_removed',coalesce(sum((payload->'file'->>'lines_removed')::numeric),0),
 'input_tokens',coalesce(sum((payload->'usage'->>'input_tokens')::numeric),0),
 'output_tokens',coalesce(sum((payload->'usage'->>'output_tokens')::numeric),0),
 'cached_tokens',coalesce(sum((payload->'usage'->>'cached_tokens')::numeric),0),
 'reasoning_tokens',coalesce(sum((payload->'usage'->>'reasoning_tokens')::numeric),0),
 'total_tokens',coalesce(sum((payload->'usage'->>'total_tokens')::numeric),0),
 'requests_with_usage',count(*) FILTER(WHERE payload ? 'usage'),
 'requests_with_total',count(*) FILTER(WHERE payload->'usage' ? 'total_tokens'),
 'requests_with_cost',count(*) FILTER(WHERE payload ? 'cost')
 )`

func (a *App) analytics(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.project(w, r); !ok {
		return
	}
	where, args, err := filters(r)
	if err != nil {
		fail(w, 400, "INVALID_FILTER", "Provide ordered RFC3339 from/to timestamps")
		return
	}
	var totals json.RawMessage
	if err = a.db.QueryRow(r.Context(), `SELECT `+aggregateSQL+` FROM events WHERE `+where, args...).Scan(&totals); err != nil {
		fail(w, 500, "STORAGE_ERROR", "Could not aggregate events")
		return
	}
	group := r.URL.Query().Get("group_by")
	if group == "" {
		group = "user"
	}
	groups := map[string]string{"user": "NULLIF(user_id,'')", "agent": "payload->'agent'->>'name'", "model": "concat(payload->'model'->>'provider','/',payload->'model'->>'name')", "ide": "payload->'ide'->>'name'", "language": "payload->>'language'", "day": "to_char(timestamp AT TIME ZONE 'UTC','YYYY-MM-DD')"}
	expr, ok := groups[group]
	if !ok {
		fail(w, 400, "INVALID_GROUP", "group_by must be user, agent, model, ide, language or day")
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT coalesce(`+expr+`,'unknown'),`+aggregateSQL+` FROM events WHERE `+where+` GROUP BY 1 ORDER BY count(*) DESC LIMIT 100`, args...)
	if err != nil {
		fail(w, 500, "STORAGE_ERROR", "Could not group events")
		return
	}
	breakdown := []map[string]any{}
	for rows.Next() {
		var name string
		var stats json.RawMessage
		if rows.Scan(&name, &stats) != nil {
			rows.Close()
			fail(w, 500, "STORAGE_ERROR", "Could not read groups")
			return
		}
		breakdown = append(breakdown, map[string]any{"name": name, "stats": stats})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		fail(w, 500, "STORAGE_ERROR", "Could not read groups")
		return
	}
	rows, err = a.db.Query(r.Context(), `SELECT payload->'cost'->>'currency',payload->'cost'->>'type',sum((payload->'cost'->>'amount')::numeric)::text FROM events WHERE `+where+` AND payload ? 'cost' GROUP BY 1,2 ORDER BY 1,2`, args...)
	if err != nil {
		fail(w, 500, "STORAGE_ERROR", "Could not aggregate costs")
		return
	}
	costs := []map[string]string{}
	for rows.Next() {
		var c, t, v string
		if rows.Scan(&c, &t, &v) != nil {
			rows.Close()
			fail(w, 500, "STORAGE_ERROR", "Could not read costs")
			return
		}
		costs = append(costs, map[string]string{"currency": c, "type": t, "amount": v})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		fail(w, 500, "STORAGE_ERROR", "Could not read costs")
		return
	}
	writeJSON(w, 200, map[string]any{"totals": totals, "costs": costs, "group_by": group, "breakdown": breakdown, "group_limit": 100})
}
func (a *App) metrics(w http.ResponseWriter, r *http.Request) {
	expected := os.Getenv("METRICS_TOKEN")
	got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if expected == "" || subtle.ConstantTimeCompare([]byte(expected), []byte(got)) != 1 {
		fail(w, 401, "UNAUTHENTICATED", "An operator metrics token is required")
		return
	}
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	stats := a.db.Stat()
	writeJSON(w, 200, map[string]any{"requests": a.requests.Load(), "request_duration_ns_total": a.requestNanos.Load(), "accepted_events": a.accepted.Load(), "duplicate_events": a.duplicates.Load(), "failed_ingestion": a.failures.Load(), "heap_bytes": mem.HeapAlloc, "goroutines": runtime.NumGoroutine(), "database_connections": stats.TotalConns(), "database_acquire_duration_ns": stats.AcquireDuration().Nanoseconds()})
}
