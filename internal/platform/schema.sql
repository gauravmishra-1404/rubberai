CREATE TABLE IF NOT EXISTS schema_migrations (version integer PRIMARY KEY);
CREATE TABLE IF NOT EXISTS organizations (id text PRIMARY KEY, name text NOT NULL);
CREATE TABLE IF NOT EXISTS users (
 id text PRIMARY KEY, organization_id text NOT NULL REFERENCES organizations(id),
 username text UNIQUE NOT NULL, display_name text NOT NULL, password_hash text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS logins (
 token_hash text PRIMARY KEY, user_id text NOT NULL REFERENCES users(id), expires_at timestamptz NOT NULL
);
CREATE TABLE IF NOT EXISTS projects (
 id text PRIMARY KEY, organization_id text NOT NULL REFERENCES organizations(id), name text NOT NULL,
 description text NOT NULL DEFAULT '', repository_url text NOT NULL DEFAULT '',
 privacy text NOT NULL DEFAULT 'METADATA_ONLY' CHECK (privacy IN ('METADATA_ONLY','REDACTED','FULL')),
 track_diffs boolean NOT NULL DEFAULT false, created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS api_keys (
 id text PRIMARY KEY, project_id text NOT NULL REFERENCES projects(id), token_hash text UNIQUE NOT NULL,
 name text NOT NULL, created_at timestamptz NOT NULL DEFAULT now(), revoked_at timestamptz
);
CREATE TABLE IF NOT EXISTS events (
 project_id text NOT NULL REFERENCES projects(id), event_id text NOT NULL,
 event_type text NOT NULL, user_id text NOT NULL DEFAULT '', session_id text NOT NULL DEFAULT '',
 trace_id text NOT NULL DEFAULT '', prompt_id text NOT NULL DEFAULT '',
 timestamp timestamptz NOT NULL, received_at timestamptz NOT NULL DEFAULT now(),
 payload jsonb NOT NULL, PRIMARY KEY(project_id,event_id)
);
CREATE INDEX IF NOT EXISTS events_project_time ON events(project_id,timestamp DESC);
CREATE INDEX IF NOT EXISTS events_trace ON events(project_id,trace_id,timestamp);
CREATE INDEX IF NOT EXISTS events_prompt ON events(project_id,prompt_id,timestamp);
CREATE INDEX IF NOT EXISTS events_user ON events(project_id,user_id,timestamp DESC);
INSERT INTO schema_migrations(version) VALUES(1) ON CONFLICT DO NOTHING;
