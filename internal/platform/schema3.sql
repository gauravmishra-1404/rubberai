-- SPDX-License-Identifier: MIT
-- Copyright (c) 2026 Gaurav Mishra

-- A session may be confined to reading one project. Empty means the session is a
-- normal login with the full rights of its user; a project id means a demo
-- session, which can read that project and nothing else, and write nothing.
ALTER TABLE logins ADD COLUMN IF NOT EXISTS scope text NOT NULL DEFAULT '';

INSERT INTO schema_migrations(version) VALUES(3) ON CONFLICT DO NOTHING;
