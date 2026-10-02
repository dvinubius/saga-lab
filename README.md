# Saga Lab

A local demonstration of orchestrated Sagas: a Transfer Service coordinates transfers of fictional credits between two independently owned banks.

Milestone 1 is in progress. One prepared visitor holds an account at each bank, starting with 100 credits at Bank A and 0 at Bank B. The visitor transfers a whole number of credits from Bank A to Bank B and follows the transfer's status, balances, and recorded history on a minimal page. Each transfer can be followed as one distributed trace in Grafana. The development reset arrives in a later increment.

## Requirements

- Docker with Compose v2
- Go 1.27, to run the tests

## Run

```bash
make up
```

This builds the services, starts them with PostgreSQL, RabbitMQ, and the tracing stack (OpenTelemetry Collector, Tempo, Grafana), and waits until every container reports ready. Open <http://localhost:8080>.

| Service          | Host URL                | Database           |
| ---------------- | ----------------------- | ------------------ |
| Transfer Service | <http://localhost:8080> | `transfer_service` |
| Bank A           | <http://localhost:8081> | `bank_a`           |
| Bank B           | <http://localhost:8082> | `bank_b`           |
| PostgreSQL       | `localhost:5432`        |                    |
| RabbitMQ         | `localhost:5672`        |                    |
| Grafana          | <http://localhost:3000> |                    |
| Tempo API        | <http://localhost:3200> |                    |
| Collector (OTLP/HTTP) | `localhost:4318`   |                    |

The RabbitMQ management UI is at <http://localhost:15672> (user and password `saga_lab`).

HTTP interfaces:

- Transfer Service pages: `GET /` shows balances, a transfer form, and earlier transfers; `POST /transfers` submits the form and redirects to `GET /transfers/{transferID}`, which shows the transfer and refreshes itself while it is pending. While a transfer is pending, `GET /` disables the form and links the pending transfer, and `POST /transfers` answers `409 Conflict` with the same page.
- Transfer Service JSON: `GET /api/balances`; `POST /api/transfers` with `{"amount": 25}` answers `202 Accepted` with the transfer and its `Location`, or `409 Conflict` with `pending_transfer_id` while another transfer is pending; `GET /api/transfers` lists the prepared visitor's transfers; `GET /api/transfers/{transferID}` returns status and history.
- Bank A and Bank B: `GET /accounts/{visitorID}`, for example `/accounts/prepared-visitor`.
- Every service: `GET /readyz`.

Each service connects with its own PostgreSQL role, which can open only that service's database. The Transfer Service obtains balances from the banks' HTTP interfaces.

On startup, each bank creates the prepared visitor's account if it does not exist yet; it never overwrites an existing account. Data lives in the `postgres-data` and `rabbitmq-data` volumes, so `make down` followed by `make up` keeps balances, transfers, and queued messages. `docker compose down --volumes` deletes all data.

Override host ports with `POSTGRES_PORT`, `RABBITMQ_PORT`, `RABBITMQ_MANAGEMENT_PORT`, `TRANSFER_SERVICE_PORT`, `BANK_A_PORT`, `BANK_B_PORT`, `GRAFANA_PORT`, `TEMPO_PORT`, and `OTEL_COLLECTOR_HTTP_PORT`; an empty value picks a free port. Follow logs with `make logs` and stop with `make down`.

## Transfers

Amounts are whole numbers of credits written with digits only, greater than zero and no larger than 9223372036854775807. Anything else is rejected with `400 Bad Request` before a transfer is recorded.

An accepted transfer runs through RabbitMQ, one durable queue per message type:

1. The Transfer Service records the transfer as `debit_pending` and sends `DebitFunds`.
2. Bank A commits the debit, then publishes `FundsDebited`.
3. The Transfer Service records the committed debit, moves to `credit_pending`, and sends `CreditFunds`.
4. Bank B commits the credit, then publishes `FundsCredited`.
5. The Transfer Service records the committed credit and completes the transfer.

Every message has its own message ID; each reply carries the ID of the message that caused it. The history lists the steps `requested`, `debit_committed`, `credit_committed`, and `finished`. Each entry has an `observed_at` time, when the reporting service saw the step happen (for a bank, just after its commit returned), and a `recorded_at` time, when the Transfer Service stored it. An entry also keeps the `message_id` of the bank event it records, the `causation_id` of the message that caused it, and the `issued_message_id` of the command it sent, so the chain from `DebitFunds` to `FundsCredited` can be followed in the history.

A transfer whose bank is unavailable stays pending until the bank processes the queued message; nothing times out.

Bank A rejects a debit it cannot afford with `DebitRejected`; the transfer ends `rejected` with a reason, both balances unchanged, and no credit.

The prepared visitor has at most one pending transfer. Admission is atomic in the Transfer Service database, so concurrent submissions, from any tab or client, start one transfer; the others start nothing and name the pending one. Completion and rejection both release the restriction; a delay never does. This is not request deduplication: a submission repeated after the pending transfer has ended starts a new transfer. Durable request identity arrives in milestone 2. Earlier increments could leave transfers pending for good; the Transfer Service refuses to start on such data, and `docker compose down --volumes` clears it.

This increment has no inbox or outbox, so a redelivered command can be applied twice, and a crash between a local commit and the following publish leaves a transfer pending for good, which also holds the visitor's next submission. A handler that fails rejects its message, which RabbitMQ redelivers at once, without backoff.

## Traces

The three services send OpenTelemetry traces to the Collector, which forwards them to Tempo; Grafana reads Tempo. All configuration lives in `deploy/` and is provisioned on startup. Traces are kept in the `tempo-data` volume.

A transfer's trace starts with the request that submitted it; the Transfer Service starts a new trace for every request and only links trace context a client sends. Incoming HTTP requests (except `/readyz` and `/static/`), the Transfer Service's calls to the banks, database statements, and every message publication and consumption are spans. Trace context travels with each message in its Watermill metadata (`traceparent`), so a bank's handling of a command is a child of the span that sent it, and the whole transfer forms one trace. Message spans carry `messaging.destination.name`, `messaging.message.id`, `saga.transfer_id`, and, for replies, `saga.causation_id`. A send span lasts until the broker confirms the message; the next service can start handling it before that, and the trace shows the times as they happened. No span stands for time spent inside RabbitMQ.

To inspect a transfer's trace, take its trace ID from the transfer page or from `trace_id` in `GET /api/transfers/{transferID}`. In Grafana (<http://localhost:3000>, no login) open **Explore**, choose **Tempo**, select **TraceQL**, and paste the trace ID. Searching TraceQL for `{span.saga.transfer_id="<transfer ID>"}` finds the same trace from a transfer ID. Spans do not record cookies, credentials, connection strings, or database roles. Traces are evidence only; balances, history, and status come from the services' databases.

Set `SAGA_LAB_OTLP_ENDPOINT` to an empty value to start the services without tracing.

## Test

```bash
make test
```

This builds the service images once and runs `go test ./...`. Each run gets a random run ID. Each acceptance test in `acceptance/` starts its own Compose project, `saga-lab-acceptance-<run>-<random>`, with fresh databases and queues on free ports, so every test begins from the prepared 100/0 state regardless of order; the tests run in parallel. Package tests that need only PostgreSQL share a separate project, `saga-lab-test-<run>`, on a free port. A run removes only its own projects afterwards, so concurrent runs and the development stack and its data are untouched. A run killed outright can leave its projects behind; `docker compose ls` lists them. Arguments to `scripts/test.sh` are passed to `go test`, for example `scripts/test.sh -run TestRefreshing -v ./acceptance`.

The acceptance tests start only the services and their PostgreSQL and RabbitMQ, with tracing turned off, except `TestTransferTraceCoversAllServices`. That test starts the whole stack, completes a transfer, and polls Tempo's API until the transfer's trace holds the HTTP, database, send, and process spans it expects from each service. It then checks that the trace contains no connection strings, cookies, or database roles.

Acceptance tests skip unless `SAGA_LAB_ACCEPTANCE_PREFIX` names their project prefix, and package tests skip unless `SAGA_LAB_POSTGRES_URL` is set; `scripts/test.sh` sets both. The acceptance tests use the images `saga-lab-transfer-service`, `saga-lab-bank-a`, and `saga-lab-bank-b` as last built, and fail if a stack is not ready within three minutes.
