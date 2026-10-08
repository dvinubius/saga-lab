# Security

Saga Lab is public, anonymous and holds only fictional credits. What it
protects is each visitor's session, the integrity of the evidence other
visitors see, and the shared host it runs on. This document describes the
measures as built; the [deployment runbook](deployment-runbook.md) covers
operating them.

## Exposure

Only two things are reachable from the internet, both through the
[hetzner-one](https://github.com/dvinubius/hetzner-one) Caddy on
`saga.dinubarbu.com`, which terminates TLS:

- the Transfer Service (pages, JSON API, `/readyz`), as `saga-lab:8080`;
- Grafana under `/grafana/`, as `saga-lab-grafana:3000`.

Both join the external `saga-lab-edge` network, which hetzner-one creates and
owns. No Saga Lab container publishes a public port: in production the only
host port is the Transfer Service on `127.0.0.1:8090`, for the deployment's
loopback check. PostgreSQL, RabbitMQ and its management UI, the Collector,
Tempo and both banks are reachable only on the project's internal network.
The local stack binds every host port to `127.0.0.1`.

## Visitor token

The `saga_lab_visitor` cookie holds a secret random token: 32 bytes,
hex-encoded to 64 characters. The visitor ID is the hex SHA-256 of that
token. Only the Transfer Service's visitor middleware sees the token; database
rows, bank accounts, internal HTTP paths, messages, spans and logs carry only
the visitor ID. A visitor ID seen in a trace therefore cannot be used to act as
that visitor, and a client cannot choose its own visitor ID.

A missing cookie, or one that is not exactly 64 hex characters, starts a new
visitor with a new token and fresh accounts. The cookie is `HttpOnly`,
`SameSite=Lax`, `Path=/`, valid for one year, and carries `Secure` when
`VISITOR_COOKIE_SECURE` is true, as production sets it (Compose's
`SAGA_LAB_VISITOR_COOKIE_SECURE`, default `false` locally). Mutations are
`POST`s, which a `SameSite=Lax` cookie does not accompany from another site.

A visitor can only read and change their own transfers and accounts: every
query is scoped to the visitor ID, and another visitor's transfer answers
`404`.

## Grafana

Grafana is public so that **Trace →** works for anyone, and read-only:

- anonymous access as **Viewer** (`GF_AUTH_ANONYMOUS_ORG_ROLE=Viewer`);
- the login form disabled, so the admin account (`GRAFANA_ADMIN_PASSWORD`) is
  usable only through the HTTP API from inside the stack;
- snapshots and public dashboards disabled;
- the Tempo data source and the Trace dashboard provisioned from files and not
  editable (`editable: false`, `allowUiUpdates: false`).

Observed on Grafana 13.0.6: Explore and Drilldown redirect a Viewer to the
home page, saving a dashboard answers `403` and creating a snapshot `401`. A
Viewer can still query Tempo through the data source, for example through the
preinstalled Traces Drilldown app. That exposes nothing beyond what traces
already contain, which is why the telemetry rules below matter. The local
stack uses the same configuration, so what is verified locally is what
visitors get.

Hetzner-one proxies the whole Grafana UI under `/grafana`, unlike its narrow
public-dashboard allowlist for other sites, because an externally shared
dashboard cannot take the `traceId` variable.

## Telemetry rules

Traces are public, so they never contain secrets:

- no cookies or other request headers;
- no credentials, connection strings or database roles (database spans are
  recorded without connection details);
- visitor IDs only as hashes, in bank paths such as `/accounts/{visitorID}`.

An acceptance test fetches a transfer's trace from Tempo and fails if it finds
a connection string, a cookie or a database role. Logs carry no cookies or
credentials either, and stay on the host.

## Rate limits

The application has no rate limiter. Hetzner-one's Caddy limits each direct
client address (`{remote_host}`, never a forwarded header) to 20 POSTs a
minute on `/transfers`, `/top-ups`, `/reset` and `/api/*`, and 300 requests a
minute on `/grafana/*`. Within the application, the pending-transfer
restriction and the admission limit bound how much work one visitor, or all
of them, can queue.

## Visitor expiry and retention

Nothing grows without bound:

- **Visitors.** Each request records the visitor's last-seen time. At startup
  and every `VISITOR_EXPIRY_SWEEP` (1 h), the Transfer Service deletes every
  visitor unseen for longer than `VISITOR_EXPIRY` (168 h), exactly as a
  visitor reset would: its history, transfers and visitor row in one
  transaction, then its accounts at both banks. A visitor whose transfer is
  pending (including one awaiting admission) or holds the demonstration slot
  is skipped and tried again by a later sweep. An account closure that fails
  is remembered and retried by every sweep, and before a returning browser's
  accounts are reopened. A returning browser keeps its cookie and starts over
  with fresh 100 / 0 accounts. Both settings are required positive Go
  durations.
- **Transfers and histories.** Completed, rejected and refunded transfers are
  hidden seven days after their request time and deleted with their execution
  histories by the hourly sweep. This applies even to active visitors and
  preserves their account balances. Pending transfers and demonstration-slot
  holders are kept until safe to remove.
- **Bank inboxes.** Each bank deletes inbox entries received more than seven
  days ago, hourly. A redelivery older than that would no longer be
  recognised as a duplicate.
- **Traces.** Tempo keeps blocks for 168 hours.
- **Logs.** Every container logs with Docker's `json-file` driver at
  3 × 10 MB.
- **HTTP.** Request reads and response writes have a 10-second timeout, so
  a stalled client cannot hold the visitor lifecycle lock indefinitely.
- **Containers.** Every container has a memory limit.

There are no backups: the data is fictional. Active visitors retain their
accounts; terminal transfer histories have a seven-day lifetime.

## Secrets

The local stack uses fixed development credentials (`postgres` for the
PostgreSQL superuser, each service role's name as its password, `saga_lab` for
RabbitMQ, `admin` for Grafana). In
production every secret comes from `/opt/saga-lab/.env` on the VPS (mode
0600, owned by the deployment account): the PostgreSQL superuser and each
service role's password, the RabbitMQ user and password, and the Grafana admin
password, as listed in [`.env.example`](../.env.example). They never enter the
repository or CI. Each service connects with its own role, which can open only
its own database.

Deployment uses a dedicated `saga-lab-deploy` account with its own SSH key,
held as a GitHub `production` environment secret restricted to `main`, and a
pinned host key. The VPS pulls the services image by digest with the run's
short-lived token and never holds source code or a Git credential. The
services run as `nobody` in their container.
