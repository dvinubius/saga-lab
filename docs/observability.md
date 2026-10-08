# Observability

Each transfer can be investigated as one distributed trace. The execution
history and outcome summary ([scenarios](scenarios.md)) are the business
record; traces are evidence only, and balances, history and status always come
from the services' databases.

## Topology

The three services export OpenTelemetry traces over OTLP/HTTP to the
OpenTelemetry Collector, which forwards them to Tempo. Grafana reads Tempo
through a provisioned data source. All configuration lives in `deploy/` and is
provisioned on startup:

| Path | Holds |
| --- | --- |
| `deploy/otel-collector/config.yaml` | OTLP/HTTP receiver, export to Tempo |
| `deploy/tempo/tempo.yaml` | local block storage, 168 h retention |
| `deploy/grafana/provisioning/` | the read-only Tempo data source, the dashboard provider |
| `deploy/grafana/dashboards/trace.json` | the Trace dashboard |

There are no metrics, log shipping or alerts. Container logs are Docker's
(`json-file`, 3 × 10 MB per container); the services write text logs to
stderr.

## What a trace contains

A transfer's trace starts with the HTTP request that submitted it. The
Transfer Service starts a new trace for every request (it treats the internet
as untrusted and only links a trace context a client sends); the banks, which
are internal, continue the caller's trace.

- **HTTP spans** for incoming requests (except `/readyz` and `/static/`) and
  for the Transfer Service's calls to the banks.
- **Database spans** for every statement, without connection details.
- **Messaging spans.** `send <queue>` for each publication, lasting until the
  broker confirms it, and `process <queue>` for each handling attempt. Trace
  context travels in each message's metadata (`traceparent`), and the relay
  starts each send span from the context stored with the outbox row, so a
  bank's handling of a command is a child of the span that sent it and the
  whole transfer forms one trace. The next service can start handling a
  message before its send span ends; the trace shows the times as they
  happened.

Message spans carry `messaging.destination.name`, `messaging.message.id`,
`saga.transfer_id` and, for replies, `saga.causation_id`. Every `process` span
also carries `saga.attempt_id` and `messaging.rabbitmq.message.redelivered`,
and ends with error status when its handler fails, as for a simulated lost
acknowledgement.

### Wait spans

Two spans stand for time a transfer spent waiting rather than working. The
Transfer Service records each after the wait has ended, with the wait's real
start and end, under the trace context stored with the transfer:

| Span | From | To |
| --- | --- | --- |
| `admission wait` | the request | admission into the demonstration slot |
| `delivery wait (scheduled)` | the broker's confirmation of the dedicated `CreditFunds` | the Transfer Service issuing `ResumeDelivery` |

Both carry `saga.transfer_id`. The delivery wait is the scheduled pause, not a
measurement inside RabbitMQ: no span stands for a message's time in a queue.
An uncontended Bank B unavailable transfer has no admission wait.

### Span events

| Event | On | Marks |
| --- | --- | --- |
| `fault.injected` | a bank's `process` span | the simulated lost acknowledgement after a commit |
| `duplicate.suppressed` | a bank's `process` span | a command recognised from the inbox and not applied |
| `credit.rejected` | Bank B's `process CreditFunds` | the scenario's credit rejection, with `saga.scenario` |
| `consumer.paused` | Bank B's dedicated `process` span | the dedicated consumer cancelled after its credit |

## The Trace dashboard and link

Grafana has one provisioned dashboard, **Trace** (UID `saga-lab-trace`): a
single Traces panel querying Tempo with the TraceQL `${traceId}`, from a
`traceId` text variable.

**Trace →** on the transfer page opens it as

```
<GRAFANA_URL>/d/saga-lab-trace?var-traceId=<trace ID>&from=<ms>&to=<ms>
```

with the transfer's own time window: from 10 s before it was requested to
10 s after its last history entry, or, while it is pending, to 10 s after the
longest a resume could still take. Grafana passes the window to Tempo, which
uses it to narrow its search for the trace.

By hand: take the trace ID from the transfer page or from `trace_id` in
`GET /api/transfers/{transferID}`, open the Trace dashboard
(<http://localhost:3000/grafana/d/saga-lab-trace> locally,
<https://saga.dinubarbu.com/grafana/d/saga-lab-trace> in public), and paste it
into **Trace ID**.

Grafana is read-only, locally as in public: anonymous visitors are Viewers,
and cannot open Explore or change anything ([security](security.md#grafana)).

## Retention

Tempo keeps trace blocks for 168 hours, matching visitor expiry: every
transfer still listed for a visitor keeps a working trace link. Traces are not
deleted by a visitor reset, visitor expiry or the demo reset; they expire on
their own.

## Configuration

| Variable | Effect |
| --- | --- |
| `SAGA_LAB_OTLP_ENDPOINT` | Compose's OTLP endpoint for the services (default the Collector); empty starts them without tracing |
| `SAGA_LAB_GRAFANA_URL` | Grafana's root URL as browsers reach it, including `/grafana` (default `http://localhost:3000/grafana`); also where **Trace →** points |
| `GRAFANA_ADMIN_PASSWORD` | the Grafana admin password (default `admin` locally; required in production); with the login form disabled it is usable only through the HTTP API |

Production sets the Grafana URL to `https://saga.dinubarbu.com/grafana/` in
`compose.production.yaml`.
