# Production deployment

rubberai runs on AWS App Runner in `ap-south-1`, with its own RDS PostgreSQL
instance. Everything below was created with the AWS CLI; nothing is managed by
Terraform yet.

| Piece | Name | Notes |
| --- | --- | --- |
| Service | App Runner `rubberai` | 0.25 vCPU / 0.5 GB, health check `/healthz`, public ingress, VPC egress |
| URL | `https://ggmwpqxp6f.ap-south-1.awsapprunner.com` | AWS-issued TLS; `APP_ORIGIN` must equal this exactly |
| Image | ECR `rubberai:latest` | Built and pushed by `.github/workflows/deploy.yml` |
| Database | RDS `rubberai-postgres` | PostgreSQL 18, `db.t4g.micro`, 20 GB gp3 (autoscales to 40), encrypted, 7-day backups, deletion protection on, not public |
| Network | VPC connector `rubberai-vpc` | Security group `rubberai-app` → `rubberai-db` on 5432 only |
| Secrets | Secrets Manager `rubberai/database-url`, `rubberai/metrics-token`, `rubberai/db-master` | Injected as `DATABASE_URL` and `METRICS_TOKEN`; never in the service config |

## Deploying a change

Push to `main`. The workflow assumes `rubberai-github-deploy` over OIDC (no stored
AWS key), builds the image on GitHub's runners, pushes `:sha` and `:latest`, and
calls `apprunner start-deployment`. Auto-deploy on the service is deliberately
off so a push to ECR alone never rolls out; the workflow's final step is the
only trigger. Changes limited to `site/`, `docs/` or Markdown skip the build.

The OIDC trust policy accepts both forms of GitHub's subject claim — the plain
`repo:owner/name:ref:…` form and the newer one carrying owner and repository
IDs. GitHub emits the ID form for this repository; a policy matching only the
plain form fails with "Not authorized to perform sts:AssumeRoleWithWebIdentity".

## Changing configuration

`PRICING_JSON` and `APP_ORIGIN` are plain environment variables on the service;
change them with `aws apprunner update-service`, which redeploys. Secrets are
read at instance start, so rotating one in Secrets Manager needs a redeploy to
take effect.

## Limits to know

- **One instance.** Rate limiting is in-memory, so the service is fixed at a
  single instance; raising the max would silently multiply every limit.
- **Retention is manual.** Nothing prunes `events`; the 20 GB volume autoscales
  to 40 GB and then stops.
- **The database is reachable only from the VPC.** Ad-hoc SQL needs a host
  inside it; there is no public endpoint by design.

## Cost

Roughly USD 20–25 a month at low traffic: App Runner ~5–7, RDS ~13 plus ~2 for
storage, Secrets Manager under 1. ECR and the VPC connector are negligible.
