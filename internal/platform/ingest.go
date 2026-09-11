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
				if amount, err := Estimate(*e.Usage, Rates{Input: p.Input, Output: p.Output, CacheRead: p.CacheRead, CacheWrite: p.CacheWrite}); err == nil {
					e.Cost = &Cost{Amount: amount, Currency: p.Currency, Type: "estimated", PricingVersion: p.Version}
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
 'files',(SELECT jsonb_agg(DISTINCT payload->'file'->>'path') FILTER(WHERE event_type IN ('file.created','file.modified','file.deleted','file.renamed'))),
 'file_changes',(SELECT jsonb_agg(jsonb_build_object(
     'path',payload->'file'->>'path','status',payload->'file'->>'status',
     'operation',payload->'file'->>'operation',
     'lines_added',payload->'file'->>'lines_added','lines_removed',payload->'file'->>'lines_removed',
     'diff',left(payload->'file'->>'diff',4000)) ORDER BY timestamp)
   FILTER(WHERE event_type IN ('file.created','file.modified','file.deleted','file.renamed'))),
 'lines_added',coalesce(sum((payload->'file'->>'lines_added')::numeric),0),
 'lines_removed',coalesce(sum((payload->'file'->>'lines_removed')::numeric),0),
 'input_tokens',sum((payload->'usage'->>'input_tokens')::numeric),
 'reported_input_tokens',count(payload->'usage'->>'input_tokens'),
 'output_tokens',sum((payload->'usage'->>'output_tokens')::numeric),
 'reported_output_tokens',count(payload->'usage'->>'output_tokens'),
 'cached_tokens',sum((payload->'usage'->>'cached_tokens')::numeric),
 'reported_cached_tokens',count(payload->'usage'->>'cached_tokens'),
 'reasoning_tokens',sum((payload->'usage'->>'reasoning_tokens')::numeric),
 'reported_reasoning_tokens',count(payload->'usage'->>'reasoning_tokens'),
 'total_tokens',sum((payload->'usage'->>'total_tokens')::numeric),
 'reported_total_tokens',count(payload->'usage'->>'total_tokens'),
 'cache_write_tokens',sum((payload->'usage'->>'cache_write_tokens')::numeric),
 'reported_cache_write_tokens',count(payload->'usage'->>'cache_write_tokens'),
 'requests_with_usage',count(*) FILTER(WHERE payload ? 'usage'),
 'requests_with_total',count(*) FILTER(WHERE payload->'usage' ? 'total_tokens'),
 'requests_with_cost',count(*) FILTER(WHERE payload ? 'cost'),
 'tools',count(*) FILTER(WHERE event_type LIKE 'tool.%'),
 'started_at',min(timestamp),'ended_at',max(timestamp),
 'prompt_text',max(payload->>'prompt') FILTER(WHERE event_type='prompt.created'),
 'models',jsonb_agg(DISTINCT payload->'model'->>'name') FILTER(WHERE event_type='llm.request.completed' AND payload->'model'->>'name' IS NOT NULL),
 'prompt_user',max(user_id) FILTER(WHERE event_type='prompt.created'),
 'prompt_ide',max(payload->'ide'->>'name') FILTER(WHERE event_type='prompt.created'),
 'prompt_agent',max(payload->'agent'->>'name') FILTER(WHERE event_type='prompt.created'),
 'prompt_host',max(payload->'metadata'->>'host') FILTER(WHERE event_type='prompt.created'),
 'prompt_ip',max(payload->'metadata'->>'ip') FILTER(WHERE event_type='prompt.created'),
 'prompt_email',max(payload->'metadata'->>'email') FILTER(WHERE event_type='prompt.created')
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
	if r.URL.Query().Get("user_prompts") == "true" {
		where = "project_id=$1 AND EXISTS (SELECT 1 FROM events matched WHERE matched.project_id=events.project_id AND matched.prompt_id=events.prompt_id AND matched.session_id=events.session_id AND " + where + ") AND EXISTS (SELECT 1 FROM events p WHERE p.project_id=events.project_id AND p.prompt_id=events.prompt_id AND p.session_id=events.session_id AND p.event_type='prompt.created')"
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
	// prompt and trace group one user-driven turn together: the agentic loop that
	// follows a single prompt collapses into one row instead of dominating the list.
	groups := map[string]string{"user": "NULLIF(user_id,'')", "agent": "payload->'agent'->>'name'", "model": "concat(payload->'model'->>'provider','/',payload->'model'->>'name')", "ide": "payload->'ide'->>'name'", "language": "payload->>'language'", "day": "to_char(timestamp AT TIME ZONE 'UTC','YYYY-MM-DD')", "prompt": "NULLIF(prompt_id,'')", "trace": "NULLIF(trace_id,'')"}
	expr, ok := groups[group]
	if !ok {
		fail(w, 400, "INVALID_GROUP", "group_by must be user, agent, model, ide, language, day, prompt or trace")
		return
	}
	// Turn-shaped groups read best newest-first; the rest stay ranked by volume.
	having := ""
	order := "count(*) DESC"
	if group == "prompt" || group == "trace" {
		order = "max(timestamp) DESC"
		having = " HAVING count(*) FILTER(WHERE event_type='prompt.created') > 0"
	}
	rows, err := a.db.Query(r.Context(), `SELECT coalesce(`+expr+`,'unknown'),`+aggregateSQL+` FROM events WHERE `+where+` GROUP BY 1`+having+` ORDER BY `+order+` LIMIT 100`, args...)
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
	rows, err = a.db.Query(r.Context(), `SELECT coalesce(`+expr+`,'unknown'),payload->'cost'->>'currency',payload->'cost'->>'type',sum((payload->'cost'->>'amount')::numeric)::text FROM events WHERE `+where+` AND payload ? 'cost' GROUP BY 1,2,3 ORDER BY 1,2,3`, args...)
	if err != nil {
		fail(w, 500, "STORAGE_ERROR", "Could not group costs")
		return
	}
	groupedCosts := map[string][]map[string]string{}
	for rows.Next() {
		var name, c, t, v string
		if rows.Scan(&name, &c, &t, &v) != nil {
			rows.Close()
			fail(w, 500, "STORAGE_ERROR", "Could not read costs")
			return
		}
		groupedCosts[name] = append(groupedCosts[name], map[string]string{"currency": c, "type": t, "amount": v})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		fail(w, 500, "STORAGE_ERROR", "Could not read costs")
		return
	}
	for _, item := range breakdown {
		item["costs"] = groupedCosts[item["name"].(string)]
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
