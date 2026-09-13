# Security policy

rubberai stores prompts, diffs and usage data on behalf of the people running
it, so security reports are taken seriously and handled quickly.

## Reporting a vulnerability

**Please do not open a public issue for a security problem.**

Use GitHub's private reporting: go to the repository's **Security** tab and
choose **Report a vulnerability**
(https://github.com/gauravmishra-1404/rubberai/security/advisories/new).
Only the maintainer can see it.

Include what you can: the affected version (image tag or commit), steps to
reproduce, and what an attacker could do with it. A proof of concept helps but
is not required.

You will get an acknowledgement within a few days. Once a fix is ready it is
published as a new image and a GitHub security advisory that credits you,
unless you prefer not to be named.

## Scope

In scope: the server (`cmd/server`, `internal/`), the collector, the Claude
Code adapter, the Python SDK and the dashboard — anything in this repository.

The public demo at `ggmwpqxp6f.ap-south-1.awsapprunner.com` runs this code
read-only; if you find a way to write to it or to read another project through
it, that is in scope too. Please do not run automated scanners or load tests
against it.

## Supported versions

Only the latest published image (`public.ecr.aws/g0m7w0k0/rubberai:latest`)
and the `main` branch receive fixes.
