# rubberai

Go modular monolith + PostgreSQL + Svelte/TypeScript. The public contract is HTTP
JSON; no business logic may depend on a particular IDE, agent or source language.
Read docs/specification.md and docs/architecture.md before extending the product.

- Build: `make build` (Go 1.27+, Node.js 22.12+).
- Check: `make check`.
- Test: `TEST_DATABASE_URL=... make test`. Use an isolated PostgreSQL database.
- Browser tests: start the application, then `cd web && npm run test:e2e`.
- Never use a production database for tests or run destructive migrations.
- Run the relevant tests and frontend checks before finishing changes.
- New migration versions must be explicit; never edit an applied migration.
- Check authorization in API handlers; a project API key only grants ingestion.
- Apply privacy rules before persisting any payload; never log keys or prompts.
- Preserve event IDs on retry and enforce project-scoped idempotency atomically.
- Missing measurements are unknown; do not claim inferred AI attribution as fact.
- Keep costs as decimal values, grouped by currency and actual/estimated status.
- Verify dashboard changes at 390px and desktop widths.
