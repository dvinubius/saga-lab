# Development

Running Saga Lab locally, resetting it, and testing it. The
[architecture](architecture.md) describes the pieces this starts.

## Requirements

- Docker with Compose v2
- Go 1.27, to run the tests
- `curl`, for the smoke run

## Run

```bash
make up
```

This builds the services image, starts the services with PostgreSQL,
RabbitMQ, the OpenTelemetry Collector, Tempo and Grafana, and waits until
every container reports ready. Open <http://localhost:8080>. Follow logs with
`make logs` and stop with `make down`.

| Service | Host address | Database |
| --- | --- | --- |
| Transfer Service | <http://localhost:8080> | `transfer_service` |
| Bank A | <http://localhost:8081> | `bank_a` |
| Bank B | <http://localhost:8082> | `bank_b` |
| PostgreSQL | `localhost:5432` | |
| RabbitMQ | `localhost:5672` | |
| RabbitMQ management | <http://localhost:15672> (user and password `saga_lab`) | |
| Grafana | <http://localhost:3000/grafana/> | |
| Tempo API | <http://localhost:3200> | |
| Collector (OTLP/HTTP) | `localhost:4318` | |

Every host port binds to `127.0.0.1`. Override them with `POSTGRES_PORT`,
`RABBITMQ_PORT`, `RABBITMQ_MANAGEMENT_PORT`, `TRANSFER_SERVICE_PORT`,
`BANK_A_PORT`, `BANK_B_PORT`, `GRAFANA_PORT`, `TEMPO_PORT` and
`OTEL_COLLECTOR_HTTP_PORT`; an empty value picks a free port.

Each browser is its own visitor (a private window is another), starting with
100 credits at Bank A and 0 at Bank B. Data lives in the `postgres-data`,
`rabbitmq-data` and `tempo-data` volumes, so `make down` followed by
`make up` keeps balances, transfers, queued messages and traces.
`docker compose down --volumes` deletes all of it.

### Configuration

Compose passes these to the services; the `SAGA_LAB_` variables override them
from the shell.

| Service variable | Compose default | Meaning |
| --- | --- | --- |
| `BANK_B_RESUME_WAIT` | `5s` (`SAGA_LAB_BANK_B_RESUME_WAIT`) | the Bank B unavailable delivery wait; required positive Go duration |
| `VISITOR_EXPIRY` | `168h` | how long an unseen visitor is kept; required |
| `VISITOR_EXPIRY_SWEEP` | `1h` | how often expired visitors are deleted; required |
| `VISITOR_COOKIE_SECURE` | `false` (`SAGA_LAB_VISITOR_COOKIE_SECURE`) | add `Secure` to the visitor cookie; set it when served over HTTPS |
| `GRAFANA_URL` | `http://localhost:3000/grafana` (`SAGA_LAB_GRAFANA_URL`) | where **Trace →** points; also Grafana's root URL |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | the Collector (`SAGA_LAB_OTLP_ENDPOINT`) | empty turns tracing off |
| `DATABASE_URL`, `AMQP_URL` | per service | the service's own database role and the broker |

The Transfer Service refuses to start without a positive resume wait, expiry
and sweep interval. [Observability](observability.md#configuration) lists the
Grafana settings, and `compose.production.yaml` the production overrides.

## Demo reset

```bash
make reset
```

The demo reset clears every visitor, account, transfer and history, and the
work still queued for the services, on the local Compose stack. A returning
browser keeps its cookie and receives fresh 100 / 0 accounts on its next
request. It builds the services image, starts PostgreSQL and RabbitMQ if
needed, stops the Transfer Service and both banks, and runs each service's
own reset in a one-off container (`docker compose run --rm --no-deps
<service> reset`):

- Bank A purges its command queues (`DebitFunds`, `RefundFunds`), Bank B its
  own (`CreditFunds`, `CreditFundsDedicated`, `ResumeDelivery`); each
  recreates its accounts table empty, its inbox and its outbox.
- The Transfer Service purges its event queues (`FundsDebited`,
  `DebitRejected`, `FundsCredited`, `CreditRejected`, `FundsRefunded`,
  `ProcessingObserved`) and recreates its visitors, transfers, history,
  demonstration slot (empty), account closures and outbox.

A service's reset refuses to run while its queues still have consumers, so it
never races a running service. With all three services stopped nothing is in
flight: a message is either in a database, its outbox included, or waiting in
a queue, and the reset clears both. The services then start again and the
command waits until they are ready. Any failure ends the command with an
error and without the completion message; the state is then unreliable until
`make reset` succeeds. Traces stay in Tempo.

Only the demo reset, a visitor reset and visitor expiry discard demonstration
state; startup and page reloads never do.

## Test

```bash
make test         # Go and acceptance tests
make test-deploy  # deployment script tests (bash, no Docker)
```

`make test` runs `scripts/test.sh`, which builds the services image once and
runs `go test ./...`. Each run gets a random run ID and starts one PostgreSQL
and one RabbitMQ for the whole run in its own Compose project,
`saga-lab-test-<run>`, on free ports. Package tests create their databases
there, and most acceptance tests use both. At most four tests run at a time;
the run's PostgreSQL allows 300 connections, enough for four tests' services
and the package tests. A run removes only its own projects afterwards, so
concurrent runs, the development stack and its data are untouched. A run
killed outright can leave its projects behind; `docker compose ls` lists them.
Arguments to `scripts/test.sh` go to `go test`, for example
`scripts/test.sh -run TestRefreshing -v ./acceptance`.

```bash
scripts/test.sh -short
```

The fast loop. It needs only the run's PostgreSQL and RabbitMQ: it builds and
pulls no images and skips the Compose-backed tests. It starts PostgreSQL and
RabbitMQ with `--pull never`, so it fails if their images are missing; a full
run pulls them.

**In-process acceptance tests** (`acceptance/`) run Bank A, Bank B and the
Transfer Service inside the test process, with tracing off, and drive the
Transfer Service only over HTTP. Each test gets three fresh databases, each
owned by its own login role as in the Compose stack, and its own RabbitMQ
virtual host, removed when the test ends, so every visitor client begins with
fresh 100 / 0 accounts. Each visitor client has its own cookie jar. A failing
test prints its services' logs, each line naming its service. Restart and
reset are tested this way too: stopping a service cancels it and waits for it
to return, and reset calls each service's reset entry point while the
services are stopped, in the order `scripts/reset.sh` uses. Configuration
overrides, such as a short visitor expiry, are passed per test.

**Compose-backed acceptance tests** start their own Compose project each,
`saga-lab-acceptance-<run>-<random>`, on free ports, and skip with `-short`:

- `TestTransferTraceCoversAllServices`, `TestTransferPagesLinkToTheirTrace`,
  `TestBankBUnavailabilityTraceLabelsItsWaits` and
  `TestCompensationTraceCarriesScenarioMarkers` start the whole stack with
  tracing. They check a transfer's spans in Tempo, that the trace holds no
  secrets, the wait spans and span events, each scenario's **Trace →** link
  and window, and Grafana's anonymous access: the Trace dashboard and the
  trace query are served, while Explore, saving a dashboard and creating a
  snapshot are refused.
- `TestResetScriptGivesReturningVisitorFreshAccounts` runs `scripts/reset.sh`
  against a project with a debit waiting in its queue.
- `TestSmokeScriptPassesAgainstAFreshStack` and
  `TestSmokeScriptSkipsBankBUnavailableRefusedByTheAdmissionLimit` run
  `scripts/smoke.sh` against the services.

Package tests skip unless `SAGA_LAB_POSTGRES_URL` is set. In-process
acceptance tests also need `SAGA_LAB_AMQP_URL` and
`SAGA_LAB_RABBITMQ_MANAGEMENT_URL`, and Compose-backed ones skip unless
`SAGA_LAB_ACCEPTANCE_PREFIX` names their project prefix; `scripts/test.sh`
sets all four. It builds the services image as
`saga-lab-services-<hash of the checkout path>`, so runs in different
worktrees never swap each other's image; the development stack keeps
`saga-lab-services`. Compose-backed tests fail if a stack is not ready within
three minutes. With many stacks at once, Docker Desktop on macOS sometimes
leaves a healthy container's published port unforwarded, and requests to it
are refused.

`make test-deploy` runs `scripts/classify-deploy_test.sh`,
`scripts/ci-deploy_test.sh` and `scripts/remote-deploy_test.sh`, which
exercise the deployment scripts with `ssh`, `docker`, `curl` and `git`
stubbed on `PATH`. The [`Test`](../.github/workflows/test.yml) workflow runs
both targets on every pull request into `main` and before every deployment.

The UI has no browser tests; page behavior is asserted over HTTP and visual
changes are reviewed by eye.

## Smoke run

```bash
scripts/smoke.sh http://localhost:8080
```

This proves a running Saga Lab works for a visitor, locally or on the public
site, and is the deployment's last check. It acts as one new visitor through
the JSON API, keeping its own cookie, and needs only `bash`, `curl` and `sed`.
It tops up Bank A if it holds less than 50, then runs happy path, debit
redelivery, credit rejection, Bank B unavailability and refund redelivery in
turn, 10 credits each. It waits up to a minute for each transfer to be ready
and compares the outcome summary with the scenario's expected balances,
attempts, effects and duplicates. The Bank B unavailability transfer may first
wait up to two minutes for admission; if the admission limit refuses it, the
step is skipped with a warning. It prints one line per scenario, `ok`, `WARN`
or `FAIL`, stops at the first failure, and exits 0 only when every scenario
that ran passed. Its visitor expires like any other.

## Repository layout

| Path | Holds |
| --- | --- |
| `cmd/` | one `main` per service |
| `internal/transferservice/` | the orchestrator: HTTP, pages (`pages.html`, `static/`), saga handlers, admission, expiry |
| `internal/bank/` | both banks: accounts, command handlers, the dedicated consumer |
| `internal/messaging/` | messages, broker connection, outbox relay, inbox, message tracing |
| `internal/web/`, `internal/service/`, `internal/telemetry/`, `internal/postgres/` | shared HTTP serving, process startup, tracing and database setup |
| `acceptance/` | acceptance tests |
| `deploy/` | PostgreSQL init, Collector, Tempo and Grafana configuration |
| `scripts/` | test, reset, smoke and deployment scripts |
| `compose.yaml`, `compose.production.yaml` | the local stack and its production overlay |
