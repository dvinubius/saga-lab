# Saga Lab — Observability Requirements and Technical Plan

**Scope of this document:** why the system needs observability, what the visitor and an engineer should be able to investigate, and how to instrument and present that evidence. Business behavior is specified in [Application Requirements](saga-lab-01-application-requirements.md); service architecture is in [Technical Plan](saga-lab-02-technical-plan.md), and the delivery sequence is in [Milestones](saga-lab-04-milestones.md).

## 1. Purpose: evidence for an orchestrated Saga

Observability is as important to the portfolio demonstration as correctness. Saga Lab should show not only **that** a credit transfer finished with the right balances, but **how** independent services, asynchronous messaging, duplicate delivery, and compensation led to that result.

The public experience has two complementary layers:

- **Application UI:** a concise, understandable account of the particular Saga, including a curated timeline and invariant-oriented outcome summary.
- **Engineering evidence:** distributed traces and broker state that support a deeper investigation of the very same operation.

The goal is to make reliability mechanisms inspectable, not to build a monitoring platform or replicate Grafana within the application.

The five supported demonstrations are: happy path, duplicate debit-command delivery, permanent credit rejection and compensation, temporary Bank B consumer unavailability, and duplicate refund-command delivery. There is **no dedicated public experiment for RabbitMQ outages or outbox publication failures**, and no requirement to validate Watermill's own publisher retries.

## 2. What must be visible

An engineer should be able to follow a transfer from its HTTP request through the coordinator, outgoing commands, RabbitMQ, each bank's local operation, return events, and either completion or compensation.

For the two duplicate-delivery demonstrations, evidence should show:

1. Two processing attempts for the **same logical message ID**.
2. The first attempt's successful local commit followed by a deliberately injected **Nack/requeue**.
3. The second attempt's duplicate detection and suppression of a second business effect.
4. Correct eventual completion (debit scenario) or a single successful refund (refund scenario).

For permanent rejection, the evidence should connect `CreditRejected`, the coordinator's compensating `RefundFunds` command, and `FundsRefunded`. This is **a new transaction**, not a rollback.

For temporary unavailability, show the credit command ready in the dedicated scenario-4 RabbitMQ queue while that queue has no active consumer, followed by resumed consumption and success. Bank B’s normal queue remains active; the process is not killed. Show any pre-execution admission wait separately from broker waiting. **Queue waiting is not the same as a delivered, unacknowledged message.** The five-second pause should be a real operational delay; the faster happy path should not be artificially slowed just for presentation.

Observations about the broker must be honest: publisher confirmation indicates broker acceptance under the chosen configuration; it does **not** independently timestamp precise queue insertion. Consumer acknowledgement/Nack is a separate lifecycle event. Label application intent such as `NackRequested` explicitly; it is not proof that RabbitMQ received the action. A consumer may begin handling before publisher confirmation is observed, so a trace and timeline must preserve actual timing and causal links rather than impose an invented strict order.

## 3. Instrumentation architecture

Use the following stack:

- **OpenTelemetry** for application instrumentation and context propagation.
- **OpenTelemetry Collector** to receive and route traces.
- **Tempo** for distributed traces.
- **Prometheus** for RabbitMQ broker metrics.
- **Grafana** for exploration, the broker dashboard, and deep links.

Instrument the Go services (Transfer Service, Bank A, and Bank B) with traces, and use RabbitMQ's own metrics to show messaging states the application cannot honestly observe. The planned Docker Compose deployment is sufficient; no custom telemetry storage, tracing backend, or dashboard framework is needed.

Conceptual flow:

```text
Go services -- OpenTelemetry --> Collector --> Tempo (traces)
RabbitMQ per-object metrics -------------------> Prometheus
Tempo + Prometheus ----------------------------> Grafana
```

Traces, the persisted execution history, and the outcome summary already explain each scenario; broker metrics add the broker's independent view of the delivery wait. **Log shipping (Loki) and application metrics are out of V1.** Alternative observability stacks are outside the V1 plan.

## 4. Distributed tracing and message identity

Instrument at least:

- Incoming HTTP transfer requests and the coordinator's state transitions.
- Database operations and local transactions.
- Outbox publication and message producer activity.
- Message consumer handling at each service, including duplicate deliveries.
- Fault injection, permanent rejection, compensation, and recovery.

Propagate OpenTelemetry context through **Watermill message metadata**: inject context on publication and extract it when consuming a message. A small publisher decorator and handler middleware are sufficient if they keep the mechanics easy to understand. This is an integration exercise, **not a reusable tracing framework**.

The identifiers have separate jobs:

| Identifier | Purpose |
| --- | --- |
| `transfer_id` | One business Saga; V1's business correlation key. |
| `message_id` | One logical command/event; unchanged on redelivery. |
| `causation_id` | Previous message that caused this message, where useful. |
| `trace_id` / `span_id` | OpenTelemetry execution and span context. |
| Observation/event ID | Deduplicates a durable evidence observation independently of the command being observed. |
| Delivery/handling attempt identity | Distinguishes separate processing attempts for the same logical message. |

**No separate `correlation_id` is needed in V1.** Repeated handling attempts should produce distinct processing observations/spans while preserving the logical message ID and transfer association.

For example, the primary incident should be navigable as:

```text
HTTP request -> transfer recorded -> DebitFunds publication
    -> Bank A consumes DebitFunds (attempt 1)
         -> local inbox + debit + outbox commit
         -> injected post-commit Nack
    -> Bank A consumes DebitFunds (attempt 2)
         -> existing inbox record, no second debit
    -> FundsDebited -> coordinator -> CreditFunds
    -> Bank B commits credit -> FundsCredited -> completed
```

This example is illustrative, not a required total ordering: `FundsDebited` may advance the Saga before the debit redelivery is handled or its evidence is recorded. The precise span tree may differ according to the messaging instrumentation; the required result is a comprehensible, correctly correlated account of asynchronous causality.

## 5. Structured logs

Services write structured `slog` logs to stderr, read through `docker logs`. Where relevant, records carry service, transfer ID, logical message ID, handling attempt ID, and trace ID, so a log line leads to its trace. Logs are not shipped to a log store in V1.

Within a transfer, a log line would repeat what a span and its events already show, so the trace is the investigation path, not the logs. A canonical per-transfer log line is also out of V1: it would repeat the persisted outcome summary, and without a log store it could not be queried or aggregated. Logs are **not** the authoritative source from which the frontend reconstructs transfer history.

## 6. Metrics

V1 collects **broker metrics only**. Prometheus scrapes RabbitMQ's built-in per-object metrics: messages **ready** and **unacknowledged** and consumer count per queue. This is the only evidence of the delivery wait that does not come from the application itself: Bank B unavailability's credit command ready in the dedicated queue while that queue has no consumer, as Bank B's normal queue keeps consuming.

Sample often enough to capture a five-second wait: scrape every second, and lower RabbitMQ's statistics collection interval to one second so the wait is not lost between statistics updates. Do not rely on metrics to establish per-transfer history.

**Application metrics are out of V1.** The demonstration has no load target or service level, so technical health metrics (HTTP, handler, and database measurements) explain nothing about a scenario. Business and reliability counters would aggregate what each transfer's outcome summary already derives from committed state, including that duplicate business effects remain zero while redeliveries and suppressed duplicates occur.

Queue names are bounded and safe as label values. Never use `transfer_id`, `message_id`, or `trace_id` as Prometheus label values.

## 7. Application timeline versus raw telemetry

The frontend's narrated execution is backed by **explicit, persisted application events**, not inferred from traces, raw logs, or Prometheus time series. Banks send both business outcome events and separate message-processing observations through RabbitMQ; the Transfer Service persists them as durable history. Business events advance the Saga; processing observations do not. Stable observation IDs deduplicate evidence delivery while attempt IDs distinguish repeated handling of the observed command. The [technical plan](saga-lab-02-technical-plan.md#6-application-owned-timeline-and-frontend-playback) defines transport and readiness.

Preserve occurrence and ingestion times separately. Do not generate a recursive stream of evidence about evidence-message handling. Application-observed acknowledgement/Nack requests and broker-confirmed observations must remain distinguishable.

Where observed, it distinguishes:

1. Outgoing message recorded in an outbox.
2. Publication attempted.
3. Broker acceptance confirmed by the publisher.
4. Message delivered to a consumer.
5. Business operation locally committed.
6. Consumer acknowledgement or Nack.

The primary narrative may show fewer milestones and expose technical details on expansion. Do not claim an observation that was not actually recorded. The event's real timestamp is independent of the frontend's animation speed; a redelivery preserves its logical message identity. The same causation and transfer references should make navigation from a timeline event to the corresponding trace straightforward.

Playback requires **both a terminal business outcome and the selected scenario’s required evidence persisted in the Transfer Service**. A duplicate-suppression observation can arrive after transfer completion; this is independent of telemetry collection or scraping. The UI reports evidence preparation separately, and missing evidence never changes the business result. Once recorded, the UI can slow short milestones to legible intervals while keeping real timestamps visible and allowing the five-second queue wait to appear meaningfully longer. Do not introduce simulated network latency or confuse a consumer pause with repeated failed broker deliveries.

## 8. Grafana dashboard and investigation plan

### Broker dashboard

One provisioned dashboard shows RabbitMQ queue state. Its first row puts Bank B's normal credit queue beside the dedicated Bank B unavailability queue, each with ready, unacknowledged, and consumer counts, so the latter shows zero consumers and a ready message during the delivery wait while the former continues operating. A second row shows ready and unacknowledged messages for every queue as context. Admission waiting is not broker state and does not appear here.

### Investigation

The transfer page links to its trace. Recorded wait spans label the admission wait and the delivery wait, and span events mark injected faults, suppressed duplicates, scenario credit rejections, and the dedicated consumer pausing again. A Bank B unavailability transfer also links to the broker dashboard on its own time window, so visitors do not land in an unrelated range.

The application's own outcome summary should remain understandable without opening Grafana. Grafana is the evidence layer, not the only user interface.

## 9. Public deployment and milestones

The public Grafana experience should be **effectively read-only**. The local stack lets anonymous visitors into Grafana as Editors so the transfer page's trace links open in Explore; the public release decides how visitors reach those traces while Grafana stays effectively read-only. Telemetry must not expose cookies, authorization headers, access tokens, secrets, raw visitor IPs, arbitrary personally identifying input, or database connection strings. Prefer generated/demo identities and fictional data. Keep dashboard provisioning, collector, Prometheus, and Tempo configuration in source control so deployments are reproducible.

Introduce trace IDs, service metadata, HTTP/database spans, producer/consumer spans, and message-context propagation **early**, before adding complex failure handling. Add durable execution evidence with each scenario. After inbox/outbox and the five scenarios work, label the waits in traces and add the RabbitMQ broker dashboard and direct transfer-specific investigation links.

Automated checks should validate **the application's observability obligations**: stable logical message IDs on redelivery, distinguishable processing attempts, propagated trace context, and a timeline/outcome consistent with committed state. Cover evidence redelivery, delayed evidence after business completion, readiness predicates, and scenario-4 admission versus broker-wait observations. Do **not** introduce dedicated tests of Watermill's outbox-forwarder retry implementation. Tests involving real service crashes/restarts are deferred to V2; broker-outage testing and public outage injection are omitted from V1.

**Acceptance criterion:** a visitor can see that the debit (or refund) command was delivered twice, discover why the second delivery produced no second business effect, follow the compensation path when it occurs, and inspect traces and broker metrics that substantiate the explanation.
