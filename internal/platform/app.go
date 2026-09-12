// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Gaurav Mishra

package platform

import (
	"context"
 schema "rubberai/internal/event"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed schema.sql schema2.sql schema3.sql web/*
var assets embed.FS

type rateWindow struct {
	start time.Time
	count int
}
type rateLimiter struct {
	mu      sync.Mutex
	windows map[string]rateWindow
}

func (l *rateLimiter) allow(key string, limit int) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if l.windows == nil {
		l.windows = map[string]rateWindow{}
	}
	if len(l.windows) > 10000 {
		for k, v := range l.windows {
			if now.Sub(v.start) > time.Minute {
				delete(l.windows, k)
			}
		}
		if len(l.windows) > 10000 {
			return false
		}
	}
	w := l.windows[key]
	if now.Sub(w.start) > time.Minute {
		w = rateWindow{start: now}
	}
	w.count++
	l.windows[key] = w
	return w.count <= limit
}

type Price struct {
	Input  string `json:"input_per_million"`
	Output string `json:"output_per_million"`
	// Optional: needed only for models whose requests report cache tokens. A
	// request carrying cache tokens with no rate here is left unpriced rather
	// than estimated from the input rate, which would misprice it.
	CacheRead  string `json:"cache_read_per_million,omitempty"`
	CacheWrite string `json:"cache_write_per_million,omitempty"`
	Currency   string `json:"currency"`
	Version    string `json:"version"`
}
type App struct {
	db           *pgxpool.Pool
	origin       string
	secure       bool
	limiter      rateLimiter
	prices       map[string]Price
	accepted     atomic.Int64
	duplicates   atomic.Int64
	failures     atomic.Int64
	requests     atomic.Int64
	requestNanos atomic.Int64
	keyLimit     int
	projectLimit int
	orgLimit     int
	demoProject  string
}
type User struct {
	ID             string `json:"id"`
	OrganizationID string `json:"organization_id"`
	DisplayName    string `json:"display_name"`
	// Demo is the project a read-only session is confined to; empty for a
	// normal login. Exposed so the dashboard can hide what such a session
	// cannot do rather than letting every write fail on click.
	Demo string `json:"demo,omitempty"`
}
type Project struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Description   string `json:"description"`
	RepositoryURL string `json:"repository_url"`
	Privacy       string `json:"privacy"`
	TrackDiffs    bool   `json:"track_diffs"`
}

func New(ctx context.Context, dsn, origin string) (*App, error) {
	if dsn == "" {
		return nil, errors.New("DATABASE_URL is required")
	}
	if origin == "" {
		origin = "http://localhost:8080"
	}
	origin = strings.TrimRight(origin, "/")
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, errors.New("APP_ORIGIN must be an absolute HTTP(S) origin")
	}
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, errors.New("invalid database configuration")
	}
	config.MaxConns = 10
	db, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, errors.New("could not create database pool")
	}
	if err = migrate(ctx, db); err != nil {
		db.Close()
		return nil, err
	}
	a := &App{db: db, origin: origin, secure: u.Scheme == "https", keyLimit: envInt("KEY_REQUESTS_PER_MINUTE", 600), projectLimit: envInt("PROJECT_REQUESTS_PER_MINUTE", 3000), orgLimit: envInt("ORG_REQUESTS_PER_MINUTE", 10000), demoProject: os.Getenv("DEMO_PROJECT_ID")}
	if raw := os.Getenv("PRICING_JSON"); raw != "" {
		if err = json.Unmarshal([]byte(raw), &a.prices); err != nil {
			db.Close()
			return nil, errors.New("invalid PRICING_JSON")
		}
		for _, p := range a.prices {
			optional := (p.CacheRead == "" || schema.ValidPrice(p.CacheRead, p.Currency)) &&
				(p.CacheWrite == "" || schema.ValidPrice(p.CacheWrite, p.Currency))
			if !schema.ValidPrice(p.Input, p.Currency) || !schema.ValidPrice(p.Output, p.Currency) || !optional || p.Version == "" {
				db.Close()
				return nil, errors.New("invalid pricing entry")
			}
		}
	}
	return a, nil
}
// nullTime keeps an unchecked timestamp out of the column entirely, so a stored
// value always means the check actually ran.
func nullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}
func envInt(key string, fallback int) int {
	v, err := strconv.Atoi(os.Getenv(key))
	if err != nil || v < 1 {
		return fallback
	}
	return v
}
func (a *App) Close() { a.db.Close() }
func token(prefix string) string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return prefix + hex.EncodeToString(b)
}
func digest(s string) string { d := sha256.Sum256([]byte(s)); return hex.EncodeToString(d[:]) }
func passwordHash(password, salt string) string {
	b, err := pbkdf2.Key(sha256.New, password, []byte(salt), 600000, 32)
	if err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": msg}})
}
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		fail(w, 415, "CONTENT_TYPE", "Use application/json")
		return false
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := d.Decode(v); err != nil {
		fail(w, 400, "INVALID_JSON", "Invalid JSON or request exceeds 1 MiB")
		return false
	}
	if err := d.Decode(new(any)); err != io.EOF {
		fail(w, 400, "INVALID_JSON", "Expected exactly one JSON value")
		return false
	}
	return true
}
func (a *App) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if a.db.Ping(ctx) != nil {
			fail(w, 503, "NOT_READY", "Database unavailable")
			return
		}
		writeJSON(w, 200, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("POST /api/v1/auth/register", a.register)
	mux.HandleFunc("POST /api/v1/auth/login", a.login)
	mux.HandleFunc("POST /api/v1/auth/logout", a.logout)
	mux.HandleFunc("GET /demo", a.demo)
	mux.HandleFunc("GET /api/v1/me", func(w http.ResponseWriter, r *http.Request) {
		u, ok := a.user(w, r)
		if ok {
			writeJSON(w, 200, u)
		}
	})
	mux.HandleFunc("GET /api/v1/projects", a.projects)
	mux.HandleFunc("POST /api/v1/projects", a.createProject)
	mux.HandleFunc("PATCH /api/v1/projects/{project}", a.updateProject)
	mux.HandleFunc("GET /api/v1/projects/{project}/keys", a.keys)
	mux.HandleFunc("POST /api/v1/projects/{project}/keys", a.createKey)
	mux.HandleFunc("DELETE /api/v1/projects/{project}/keys/{key}", a.revokeKey)
	mux.HandleFunc("POST /api/v1/events", a.ingest)
	mux.HandleFunc("GET /api/v1/projects/{project}/events", a.events)
	mux.HandleFunc("DELETE /api/v1/projects/{project}/events/{event}", a.deleteEvent)
	mux.HandleFunc("GET /api/v1/projects/{project}/analytics", a.analytics)
	mux.HandleFunc("GET /api/v1/metrics", a.metrics)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) { fail(w, 404, "NOT_FOUND", "Unknown API endpoint") })
	web, _ := fs.Sub(assets, "web")
	mux.Handle("/", http.FileServer(http.FS(web)))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		a.requests.Add(1)
		defer func() { a.requestNanos.Add(time.Since(start).Nanoseconds()) }()
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; connect-src 'self'; object-src 'none'; frame-ancestors 'none'")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		if r.Method != "GET" && r.Method != "HEAD" && r.URL.Path != "/api/v1/events" {
			if origin := r.Header.Get("Origin"); origin != "" && origin != a.origin {
				fail(w, 403, "ORIGIN_DENIED", "Request origin is not allowed")
				return
			}
			if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
				fail(w, 403, "ORIGIN_DENIED", "Cross-site request denied")
				return
			}
		}
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		mux.ServeHTTP(w, r.WithContext(ctx))
	})
}
func (a *App) user(w http.ResponseWriter, r *http.Request) (User, bool) {
	var u User
	c, err := r.Cookie("rubberai_session")
	if err == nil {
		err = a.db.QueryRow(r.Context(), `SELECT u.id,u.organization_id,u.display_name,l.scope FROM logins l JOIN users u ON u.id=l.user_id WHERE l.token_hash=$1 AND l.expires_at>now()`, digest(c.Value)).Scan(&u.ID, &u.OrganizationID, &u.DisplayName, &u.Demo)
	}
	if err != nil {
		fail(w, 401, "UNAUTHENTICATED", "Sign in to continue")
		return u, false
	}
	return u, true
}
// writer resolves a session that is allowed to change things. A demo session
// can read one project and nothing more; refusing it here, in one place, is what
// makes "read-only" a property of the server rather than of whichever buttons
// the dashboard happens to hide.
func (a *App) writer(w http.ResponseWriter, r *http.Request) (User, bool) {
	u, ok := a.user(w, r)
	if !ok {
		return u, false
	}
	if u.Demo != "" {
		fail(w, 403, "READ_ONLY", "The demo is read-only. Create your own workspace to make changes.")
		return u, false
	}
	return u, true
}

func (a *App) project(w http.ResponseWriter, r *http.Request) (Project, bool) {
	var p Project
	u, ok := a.user(w, r)
	if !ok {
		return p, false
	}
	if u.Demo != "" && r.PathValue("project") != u.Demo {
		fail(w, 404, "PROJECT_NOT_FOUND", "Project not found")
		return p, false
	}
	err := a.db.QueryRow(r.Context(), `SELECT id,name,description,repository_url,privacy,track_diffs FROM projects WHERE id=$1 AND organization_id=$2`, r.PathValue("project"), u.OrganizationID).Scan(&p.ID, &p.Name, &p.Description, &p.RepositoryURL, &p.Privacy, &p.TrackDiffs)
	if err != nil {
		fail(w, 404, "PROJECT_NOT_FOUND", "Project not found")
		return p, false
	}
	return p, true
}
func (a *App) authLimit(w http.ResponseWriter, r *http.Request) bool {
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	if !a.limiter.allow("auth:"+host, 20) {
		w.Header().Set("Retry-After", "60")
		fail(w, 429, "RATE_LIMITED", "Try again in one minute")
		return false
	}
	return true
}
func (a *App) session(w http.ResponseWriter, r *http.Request, u User) {
	key := token("")
	_, err := a.db.Exec(r.Context(), `INSERT INTO logins(token_hash,user_id,expires_at) VALUES($1,$2,now()+interval '24 hours')`, digest(key), u.ID)
	if err != nil {
		fail(w, 500, "STORAGE_ERROR", "Could not create session")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "rubberai_session", Value: key, Path: "/", HttpOnly: true, Secure: a.secure, SameSite: http.SameSiteStrictMode, MaxAge: 86400})
	writeJSON(w, 200, u)
}
func (a *App) register(w http.ResponseWriter, r *http.Request) {
	if !a.authLimit(w, r) {
		return
	}
	var b struct {
		Username     string `json:"username"`
		Password     string `json:"password"`
		DisplayName  string `json:"display_name"`
		Organization string `json:"organization"`
		Email        string `json:"email"`
	}
	if !decode(w, r, &b) {
		return
	}
	b.Username = strings.ToLower(strings.TrimSpace(b.Username))
	if !schema.ValidIdentifier(b.Username) || len(b.Password) < 12 || len(b.Password) > 256 || len(strings.TrimSpace(b.DisplayName)) == 0 || len(b.DisplayName) > 120 || len(strings.TrimSpace(b.Organization)) == 0 || len(b.Organization) > 120 {
		fail(w, 400, "INVALID_REGISTRATION", "Provide username, display name, organization and a 12–256 character password")
		return
	}
	// Optional by design: an account is identified by its username, and the
	// specification forbids requiring an address to identify anyone.
	email, checkedAt, err := checkEmail(r.Context(), b.Email)
	if err != nil {
		fail(w, 400, "INVALID_EMAIL", err.Error())
		return
	}
	salt := token("")
	hash := salt + ":" + passwordHash(b.Password, salt)
	u := User{ID: token("usr_"), OrganizationID: token("org_"), DisplayName: b.DisplayName}
	tx, beginErr := a.db.Begin(r.Context())
	if beginErr != nil {
		fail(w, 503, "STORAGE_ERROR", "Database unavailable")
		return
	}
	defer tx.Rollback(r.Context())
	_, err = tx.Exec(r.Context(), `INSERT INTO organizations(id,name) VALUES($1,$2)`, u.OrganizationID, b.Organization)
	if err == nil {
		_, err = tx.Exec(r.Context(), `INSERT INTO users(id,organization_id,username,display_name,password_hash,email,email_domain_checked_at) VALUES($1,$2,$3,$4,$5,NULLIF($6,''),$7)`, u.ID, u.OrganizationID, b.Username, b.DisplayName, hash, email, nullTime(checkedAt))
	}
	if err != nil {
		// Two unique constraints can fail here now. Saying "username" for both
		// sends the user to correct a field that was never the problem.
		message := "Could not register; username may already exist"
		if strings.Contains(err.Error(), "users_email_unique") {
			message = "That email address is already registered"
		}
		fail(w, 409, "REGISTRATION_FAILED", message)
		return
	}
	if tx.Commit(r.Context()) != nil {
		fail(w, 500, "STORAGE_ERROR", "Could not register")
		return
	}
	a.session(w, r, u)
}
func (a *App) login(w http.ResponseWriter, r *http.Request) {
	if !a.authLimit(w, r) {
		return
	}
	var b struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decode(w, r, &b) {
		return
	}
	if len(b.Password) > 256 {
		fail(w, 400, "INVALID_LOGIN", "Invalid credentials")
		return
	}
	var u User
	var stored string
	err := a.db.QueryRow(r.Context(), `SELECT id,organization_id,display_name,password_hash FROM users WHERE username=$1`, strings.ToLower(strings.TrimSpace(b.Username))).Scan(&u.ID, &u.OrganizationID, &u.DisplayName, &stored)
	parts := strings.Split(stored, ":")
	if len(parts) != 2 {
		parts = []string{"dummy", "dummy"}
	}
	calculated := passwordHash(b.Password, parts[0])
	if err != nil || subtle.ConstantTimeCompare([]byte(calculated), []byte(parts[1])) != 1 {
		fail(w, 401, "INVALID_LOGIN", "Invalid credentials")
		return
	}
	a.session(w, r, u)
}
// demo issues a read-only session confined to the configured demo project and
// sends the visitor to the dashboard. It is a GET so a plain link on a marketing
// page can open it, and it holds no credential: the session is minted here, tied
// to the project's owning organization, and can only read.
//
// The cookie is SameSite=Lax rather than Strict. Strict cookies are withheld on
// a navigation that started on another site, which is exactly how every demo
// visitor arrives, so a Strict cookie would land them on the login page. Lax is
// safe here because the session cannot perform any write for a forged request
// to abuse.
func (a *App) demo(w http.ResponseWriter, r *http.Request) {
	if a.demoProject == "" {
		fail(w, 404, "NO_DEMO", "No demo is configured")
		return
	}
	if !a.authLimit(w, r) {
		return
	}
	var owner string
	err := a.db.QueryRow(r.Context(), `SELECT u.id FROM projects p JOIN users u ON u.organization_id=p.organization_id WHERE p.id=$1 ORDER BY u.created_at LIMIT 1`, a.demoProject).Scan(&owner)
	if err != nil {
		fail(w, 404, "NO_DEMO", "The demo project does not exist")
		return
	}
	key := token("")
	if _, err = a.db.Exec(r.Context(), `INSERT INTO logins(token_hash,user_id,expires_at,scope) VALUES($1,$2,now()+interval '1 hour',$3)`, digest(key), owner, a.demoProject); err != nil {
		fail(w, 500, "STORAGE_ERROR", "Could not start the demo")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "rubberai_session", Value: key, Path: "/", HttpOnly: true, Secure: a.secure, SameSite: http.SameSiteLaxMode, MaxAge: 3600})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (a *App) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie("rubberai_session"); err == nil {
		if _, err = a.db.Exec(r.Context(), `DELETE FROM logins WHERE token_hash=$1`, digest(c.Value)); err != nil {
			fail(w, 503, "STORAGE_ERROR", "Could not sign out")
			return
		}
	}
	http.SetCookie(w, &http.Cookie{Name: "rubberai_session", Value: "", Path: "/", HttpOnly: true, Secure: a.secure, SameSite: http.SameSiteStrictMode, MaxAge: -1})
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func (a *App) projects(w http.ResponseWriter, r *http.Request) {
	u, ok := a.user(w, r)
	if !ok {
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT id,name,description,repository_url,privacy,track_diffs FROM projects WHERE organization_id=$1 AND ($2='' OR id=$2) ORDER BY created_at DESC`, u.OrganizationID, u.Demo)
	if err != nil {
		fail(w, 500, "STORAGE_ERROR", "Could not list projects")
		return
	}
	defer rows.Close()
	out := []Project{}
	for rows.Next() {
		var p Project
		if rows.Scan(&p.ID, &p.Name, &p.Description, &p.RepositoryURL, &p.Privacy, &p.TrackDiffs) != nil {
			fail(w, 500, "STORAGE_ERROR", "Could not read projects")
			return
		}
		out = append(out, p)
	}
	if rows.Err() != nil {
		fail(w, 500, "STORAGE_ERROR", "Could not list projects")
		return
	}
	writeJSON(w, 200, out)
}
func validProject(p Project) bool {
	return len(strings.TrimSpace(p.Name)) > 0 && len(p.Name) <= 120 && len(p.Description) <= 2000 && len(p.RepositoryURL) <= 2000 && (p.Privacy == "METADATA_ONLY" || p.Privacy == "REDACTED" || p.Privacy == "FULL")
}
func (a *App) createProject(w http.ResponseWriter, r *http.Request) {
	u, ok := a.writer(w, r)
	if !ok {
		return
	}
	p := Project{Privacy: "METADATA_ONLY"}
	if !decode(w, r, &p) {
		return
	}
	if !validProject(p) {
		fail(w, 400, "INVALID_PROJECT", "Provide a name and valid privacy mode")
		return
	}
	p.ID = token("prj_")
	_, err := a.db.Exec(r.Context(), `INSERT INTO projects(id,organization_id,name,description,repository_url,privacy,track_diffs) VALUES($1,$2,$3,$4,$5,$6,$7)`, p.ID, u.OrganizationID, p.Name, p.Description, p.RepositoryURL, p.Privacy, p.TrackDiffs)
	if err != nil {
		fail(w, 500, "STORAGE_ERROR", "Could not create project")
		return
	}
	writeJSON(w, 201, p)
}
func (a *App) updateProject(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.writer(w, r); !ok {
		return
	}
	p, ok := a.project(w, r)
	if !ok {
		return
	}
	id := p.ID
	if !decode(w, r, &p) {
		return
	}
	p.ID = id
	if !validProject(p) {
		fail(w, 400, "INVALID_PROJECT", "Invalid project settings")
		return
	}
	_, err := a.db.Exec(r.Context(), `UPDATE projects SET name=$2,description=$3,repository_url=$4,privacy=$5,track_diffs=$6 WHERE id=$1`, p.ID, p.Name, p.Description, p.RepositoryURL, p.Privacy, p.TrackDiffs)
	if err != nil {
		fail(w, 500, "STORAGE_ERROR", "Could not update project")
		return
	}
	writeJSON(w, 200, p)
}
func (a *App) keys(w http.ResponseWriter, r *http.Request) {
	// Key names and ids are not secrets, but they are operational detail a demo
	// visitor has no reason to see; the guard keeps the list owner-only.
	if _, ok := a.writer(w, r); !ok {
		return
	}
	p, ok := a.project(w, r)
	if !ok {
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT id,name,created_at,revoked_at FROM api_keys WHERE project_id=$1 ORDER BY created_at DESC`, p.ID)
	if err != nil {
		fail(w, 500, "STORAGE_ERROR", "Could not list keys")
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, name string
		var created time.Time
		var revoked *time.Time
		if rows.Scan(&id, &name, &created, &revoked) != nil {
			fail(w, 500, "STORAGE_ERROR", "Could not read keys")
			return
		}
		out = append(out, map[string]any{"id": id, "name": name, "created_at": created, "revoked_at": revoked})
	}
	if rows.Err() != nil {
		fail(w, 500, "STORAGE_ERROR", "Could not list keys")
		return
	}
	writeJSON(w, 200, out)
}
func (a *App) createKey(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.writer(w, r); !ok {
		return
	}
	p, ok := a.project(w, r)
	if !ok {
		return
	}
	var b struct {
		Name string `json:"name"`
	}
	if !decode(w, r, &b) {
		return
	}
	if len(b.Name) == 0 || len(b.Name) > 120 {
		fail(w, 400, "INVALID_KEY", "Provide a key name")
		return
	}
	raw := token("rai_")
	id := token("key_")
	_, err := a.db.Exec(r.Context(), `INSERT INTO api_keys(id,project_id,token_hash,name) VALUES($1,$2,$3,$4)`, id, p.ID, digest(raw), b.Name)
	if err != nil {
		fail(w, 500, "STORAGE_ERROR", "Could not create key")
		return
	}
	writeJSON(w, 201, map[string]string{"id": id, "api_key": raw, "scope": "events:write", "project_id": p.ID})
}
func (a *App) revokeKey(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.writer(w, r); !ok {
		return
	}
	p, ok := a.project(w, r)
	if !ok {
		return
	}
	tag, err := a.db.Exec(r.Context(), `UPDATE api_keys SET revoked_at=now() WHERE id=$1 AND project_id=$2`, r.PathValue("key"), p.ID)
	if err != nil {
		fail(w, 500, "STORAGE_ERROR", "Could not revoke key")
		return
	}
	if tag.RowsAffected() == 0 {
		fail(w, 404, "KEY_NOT_FOUND", "Key not found")
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func migrate(ctx context.Context, db *pgxpool.Pool) error {
	tx, err := db.Begin(ctx)
	if err != nil {
		return errors.New("database unavailable")
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(8272331)"); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, "CREATE TABLE IF NOT EXISTS schema_migrations(version integer PRIMARY KEY)"); err != nil {
		return err
	}
	// Each version is applied once and in order. An applied migration is never
	// edited; a change to the schema is always a new numbered file.
	for _, step := range []struct {
		version int
		file    string
	}{{1, "schema.sql"}, {2, "schema2.sql"}, {3, "schema3.sql"}} {
		var applied bool
		if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=$1)", step.version).Scan(&applied); err != nil {
			return err
		}
		if applied {
			continue
		}
		statements, readErr := assets.ReadFile(step.file)
		if readErr != nil {
			return errors.New("missing migration " + step.file)
		}
		if _, err = tx.Exec(ctx, string(statements)); err != nil {
			return errors.New("database migration failed at " + step.file)
		}
	}
	return tx.Commit(ctx)
}
