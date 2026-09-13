## What changed

<!-- One or two sentences. Link the issue if there is one: "Fixes #12". -->

## Why

<!-- The problem this solves, or the behaviour it adds. -->

## How I tested it

<!-- Which of these did you run? Anything manual? -->
- [ ] `go test -race ./...` with `TEST_DATABASE_URL` set
- [ ] `cd web && npm run check && npm run build`
- [ ] Python tests (`sdk/python`, `integrations/claude-code`) if touched
- [ ] Tried it in the dashboard / with the adapter (say what you did)

## Checklist

- [ ] `docs/api.md` updated if the event contract or an endpoint changed
- [ ] New source files carry the SPDX header
- [ ] Nothing here stores or shows more than the project's privacy mode allows
