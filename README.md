# Saga Lab

A local demonstration of orchestrated Sagas: a Transfer Service coordinates transfers of fictional credits between two independently owned banks.

Milestone 1 is complete. One prepared visitor holds an account at each bank, starting with 100 credits at Bank A and 0 at Bank B. The visitor transfers a whole number of credits from Bank A to Bank B and follows the transfer's status, balances, and recorded history on a minimal page. Each transfer can be followed as one distributed trace in Grafana. A development reset restores the prepared state for another run.

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

## Reset

```bash
make reset
```

This restores the prepared state, 100 credits at Bank A and 0 at Bank B, and discards every transfer, its history, and the work still queued for the services, so the demonstration can run again. It builds the service images, starts PostgreSQL and RabbitMQ if needed, stops the Transfer Service and both banks, and runs each service's own reset in a one-off container (`docker compose run --rm --no-deps <service> reset`):

- Bank A and Bank B purge their command queue (`DebitFunds`, `CreditFunds`) and recreate their accounts table with the prepared account.
- The Transfer Service purges its event queues (`FundsDebited`, `DebitRejected`, `FundsCredited`) and recreates its transfer and history tables.

A service's reset refuses to run while its queues still have consumers, so it never races a running service. With all three services stopped, nothing is in flight: a message is either in a database or waiting in a queue, and the reset clears both. The services then start again and the command waits until they are ready. Any failure ends the command with an error and without the completion message; the state is then unreliable until `make reset` succeeds. Traces stay in Tempo.

Only `make reset` discards demonstration state; startup and page reloads never do.

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

The prepared visitor has at most one pending transfer. Admission is atomic in the Transfer Service database, so concurrent submissions, from any tab or client, start one transfer; the others start nothing and name the pending one. Completion and rejection both release the restriction; a delay never does. This is not request deduplication: a submission repeated after the pending transfer has ended starts a new transfer. Durable request identity arrives in milestone 2. Earlier increments could leave transfers pending for good; the Transfer Service refuses to start on such data, and `make reset` clears it.

This increment has no inbox or outbox, so a redelivered command can be applied twice, and a crash between a local commit and the following publish leaves a transfer pending for good, which also holds the visitor's next submission until `make reset`. A handler that fails rejects its message, which RabbitMQ redelivers at once, without backoff.

## Traces

The three services send OpenTelemetry traces to the Collector, which forwards them to Tempo; Grafana reads Tempo. All configuration lives in `deploy/` and is provisioned on startup. Traces are kept in the `tempo-data` volume.

A transfer's trace starts with the request that submitted it; the Transfer Service starts a new trace for every request and only links trace context a client sends. Incoming HTTP requests (except `/readyz` and `/static/`), the Transfer Service's calls to the banks, database statements, and every message publication and consumption are spans. Trace context travels with each message in its Watermill metadata (`traceparent`), so a bank's handling of a command is a child of the span that sent it, and the whole transfer forms one trace. Message spans carry `messaging.destination.name`, `messaging.message.id`, `saga.transfer_id`, and, for replies, `saga.causation_id`. A send span lasts until the broker confirms the message; the next service can start handling it before that, and the trace shows the times as they happened. No span stands for time spent inside RabbitMQ.

To inspect a transfer's trace, follow **Explore the trace in Grafana →** on the transfer page; it opens Grafana's Explore with the trace ID as a TraceQL query. By hand: take the trace ID from the transfer page or from `trace_id` in `GET /api/transfers/{transferID}`, and in Grafana (<http://localhost:3000>, no login) open **Explore**, choose **Tempo**, select **TraceQL**, and paste the trace ID. Searching TraceQL for `{span.saga.transfer_id="<transfer ID>"}` finds the same trace from a transfer ID. Spans do not record cookies, credentials, connection strings, or database roles. Traces are evidence only; balances, history, and status come from the services' databases.

Set `SAGA_LAB_OTLP_ENDPOINT` to an empty value to start the services without tracing. Set `SAGA_LAB_GRAFANA_URL` to the Grafana address visitors' browsers reach (default `http://localhost:3000`); the transfer page's trace link points there.

## Test

```bash
make test
```

This builds the service images once and runs `go test ./...`. Each run gets a random run ID and starts one PostgreSQL and one RabbitMQ for the whole run in its own Compose project, `saga-lab-test-<run>`, on free ports. Package tests create their databases there, and most acceptance tests use both. At most four tests run at a time: the run's PostgreSQL allows 300 connections, enough for four tests' services and the package tests, and with more Compose stacks, Docker Desktop on macOS sometimes leaves a healthy container's published port unforwarded, and requests to it are refused. A run removes only its own projects afterwards, so concurrent runs and the development stack and its data are untouched. A run killed outright can leave its projects behind; `docker compose ls` lists them. Arguments to `scripts/test.sh` are passed to `go test`, for example `scripts/test.sh -run TestRefreshing -v ./acceptance`.

Most acceptance tests in `acceptance/` run Bank A, Bank B, and the Transfer Service inside the test process, with tracing turned off, and drive the Transfer Service only over HTTP. Each test gets three fresh databases, each owned by its own login role that alone can open it, as in the Compose stack, and its own RabbitMQ virtual host. They are removed when the test ends, also when it fails, so every test begins from the prepared 100/0 state regardless of order. A failing test prints its services' logs, each line naming its service.

`TestTransferTraceCoversAllServices` and `TestResetRestoresThePreparedDemonstration` still start their own Compose project each, `saga-lab-acceptance-<run>-<random>`, on free ports. The trace test starts the whole stack, completes a transfer, and polls Tempo's API until the transfer's trace holds the HTTP, database, send, and process spans it expects from each service. It then checks that the trace contains no connection strings, cookies, or database roles. The reset test starts only the services and their PostgreSQL and RabbitMQ, with tracing turned off. It completes a transfer, restarts the services and finds it kept, stops Bank A and submits another transfer so its `DebitFunds` waits in the queue, runs `scripts/reset.sh` against its project, and then expects 100/0, no transfers, and a fresh 25-credit transfer ending at 75/25.

Package tests skip unless `SAGA_LAB_POSTGRES_URL` is set. In-process acceptance tests also need `SAGA_LAB_AMQP_URL` and `SAGA_LAB_RABBITMQ_MANAGEMENT_URL`, and Compose-backed ones skip unless `SAGA_LAB_ACCEPTANCE_PREFIX` names their project prefix; `scripts/test.sh` sets all four. The Compose-backed tests use the image `saga-lab-services` as last built, and fail if a stack is not ready within three minutes.
