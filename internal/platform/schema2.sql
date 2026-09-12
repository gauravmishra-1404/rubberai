-- SPDX-License-Identifier: MIT
-- Copyright (c) 2026 Gaurav Mishra
-- Optional contact address for an account. Nullable and never required: the
-- platform identifies a user by username, and the specification forbids making
-- an email address necessary to identify anyone.
ALTER TABLE users ADD COLUMN IF NOT EXISTS email text;

-- Records that the domain was checked for deliverability, not that the person
-- proved they own the address. Named for what it actually establishes, so a
-- later ownership-confirmation flow can add its own column without this one
-- having overclaimed.
ALTER TABLE users ADD COLUMN IF NOT EXISTS email_domain_checked_at timestamptz;

-- Addresses are stored normalised, so uniqueness is enforced on the stored form.
-- Partial, because an absent address must never collide with another absent one.
CREATE UNIQUE INDEX IF NOT EXISTS users_email_unique ON users(email) WHERE email IS NOT NULL;

INSERT INTO schema_migrations(version) VALUES(2) ON CONFLICT DO NOTHING;
