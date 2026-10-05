# Saga Lab — Observability Requirements and Technical Plan

**Scope of this document:** why the system needs observability, what the visitor and an engineer should be able to investigate, and how to instrument and present that evidence. Business behavior is specified in [Application Requirements](saga-lab-01-application-requirements.md); service architecture is in [Technical Plan](saga-lab-02-technical-plan.md), and the delivery sequence is in [Milestones](saga-lab-04-milestones.md).

## 1. Purpose: evidence for an orchestrated Saga

Observability is as important to the portfolio demonstration as correctness. Saga Lab should show not only **that** a credit transfer finished with the right balances, but **how** independent services, asynchronous messaging, duplicate delivery, and compensation led to that result.

The public experience has two complementary layers:

- **Application UI:** a concise, understandable account of the particular Saga, including a curated timeline and invariant-oriented outcome summary.
- **Engineering evidence:** distributed traces, structured logs, metrics, and broker state that support a deeper investigation of the very same operation.

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
- **OpenTelemetry Collector or Grafana Alloy** to collect and route telemetry.
- **Prometheus** for technical and reliability metrics.
- **Loki** for structured logs.
- **Tempo** for distributed traces.
- **Grafana** for dashboards, exploration, and deep links.

Instrument the Go services (Transfer Service, Bank A, and Bank B) and use RabbitMQ's broker metrics to distinguish messaging states. The planned Docker Compose deployment is sufficient; no custom telemetry storage, tracing backend, log search engine, or dashboard framework is needed.

Conceptual flow:

```text
Go services -- OpenTelemetry --> Collector / Grafana Alloy
                                         |--> Tempo (traces)
                                         |--> Loki (logs)
                                         `--> Prometheus (metrics)

Watermill/application and RabbitMQ metrics --> Prometheus
Structured service logs --------------------> Loki

Prometheus + Loki + Tempo ------------------> Grafana
```

This is the **preferred stack**, not a requirement to use every available OpenTelemetry or Watermill abstraction. Alternative observability stacks are outside the V1 plan.

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

Use structured, queryable logs containing, when relevant: service, event name, transfer ID, logical message ID, message type, handler, trace/span IDs, processing outcome, and delivery attempt.

Events worth recording include:

- Message received, redelivered, acknowledged/Nacked, or failed during handling.
- Duplicate message suppressed; inbox and outbox records committed.
- Transfer started, advanced, completed, or failed/refunded.
- Compensation started or completed.
- A deterministic fault injected and, for Bank B, dedicated scenario-4 consumption suspended/resumed.
- Scenario-4 admission queued/started/released, distinct from broker delivery and Saga execution.
- Required history evidence pending/ready, distinct from the business outcome.
- Outbox forwarding and publication problems **when they occur naturally**; these are operational signals, not a new V1 fault-injection demonstration.

A duplicate should generate an observable processing record even though it produces no second business mutation. Logs aid investigation; they are **not** the authoritative source from which the frontend reconstructs transfer history.

## 6. Metrics

Separate operational measurements from business/reliability measurements.

**Technical / infrastructure metrics** include HTTP request volume, errors and latency; Watermill message publish/consume rates; handler and publish duration; messages acknowledged and negatively acknowledged; handler failures; RabbitMQ messages **ready** and **unacknowledged**, consumer count, and queue depth; and, where useful, database pool usage and transaction duration.

**Business / reliability metrics** include:

- Transfers started, completed, failed, and compensated.
- Compensation started and completed.
- Transfer completion duration, including tail latency (for example p95).
- Message redeliveries and duplicate messages suppressed.
- Pending outbox count and outbox publication failures as routine operational health signals.
- Injected faults by bounded failure category.
- Scenario-4 admission backlog and wait duration, separately from Saga execution duration and broker waiting.

The key demonstration is that **redeliveries and duplicate suppression can increase while duplicate business effects remain zero**. This outcome must also be confirmed by actual application state, not inferred from a metric alone.

### Metric cardinality

Never use `transfer_id`, `message_id`, or `trace_id` as Prometheus label values. These are high-cardinality identifiers. Keep them in structured logs, traces, and durable application timeline records.

Use bounded metric dimensions such as service, handler, message type, result, failure mode, and normal versus scenario-4 queue. Do not create visitor-specific metric labels. The UI reads transfer-specific evidence from application data; Grafana shows system-wide measurements and offers trace/log searches for a specific transfer.

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

The primary narrative may show fewer milestones and expose technical details on expansion. Do not claim an observation that was not actually recorded. The event's real timestamp is independent of the frontend's animation speed; a redelivery preserves its logical message identity. The same causation and transfer references should make navigation from a timeline event to a corresponding trace or log straightforward.

Playback requires **both a terminal business outcome and the selected scenario’s required evidence persisted in the Transfer Service**. A duplicate-suppression observation can arrive after transfer completion; this is independent of telemetry collection or scraping. The UI reports evidence preparation separately, and missing evidence never changes the business result. Once recorded, the UI can slow short milestones to legible intervals while keeping real timestamps visible and allowing the five-second queue wait to appear meaningfully longer. Do not introduce simulated network latency or confuse a consumer pause with repeated failed broker deliveries.

## 8. Grafana dashboard and investigation plan

Keep the public dashboard compact and organized around a few clear questions.

### System overview

Show transfer rate, completion/success rate, compensation rate, duplicate-suppression rate, p95 completion latency, and current outbox backlog. A service graph is optional.

### Messaging health

Show publish/consume rates, Ack versus Nack, redeliveries, handler failures and latency, ready/unacknowledged queue depth, consumer presence, and pending outbox messages. Separate the normal and scenario-4 queues so the latter shows zero consumers and ready messages during the five-second pause while the former continues operating. Choose sampling appropriate to capture this short interval; do not rely on a slow aggregate scrape to establish the per-transfer history. Admission backlog is a separate application measurement.

### Correctness and fault injection

Show injected failures over time, redeliveries, duplicate suppressions, and compensations. The goal is to connect an induced post-commit Nack to a second processing attempt **without** a second committed debit or refund. Do not add a special outbox-publication-failure experiment or dashboard story.

### Investigation

Make it practical to move from a selected transfer to a relevant trace, then to a processing span and associated structured logs. Provide filtering by transfer ID in logs/traces where practical, and link to the right time range and dashboard section so visitors do not land in an unrelated overview.

The application's own outcome summary should remain understandable without opening Grafana. Grafana is the evidence layer, not the only user interface.

## 9. Public deployment and milestones

The public Grafana experience should be **effectively read-only**. The local stack lets anonymous visitors into Grafana as Editors so the transfer page's trace links open in Explore; the public release decides how visitors reach those traces while Grafana stays effectively read-only. Telemetry must not expose cookies, authorization headers, access tokens, secrets, raw visitor IPs, arbitrary personally identifying input, or database connection strings. Prefer generated/demo identities and fictional data. Keep dashboard provisioning, collector, Prometheus, Tempo, and Loki configuration in source control so deployments are reproducible.

Introduce trace IDs, service metadata, HTTP/database spans, producer/consumer spans, and message-context propagation **early**, before adding complex failure handling. Add structured logs, reliability counters, and durable execution evidence with each scenario. After inbox/outbox and the five scenarios work, finish the RabbitMQ views, compact Grafana dashboards, and direct transfer-specific investigation links.

Automated checks should validate **the application's observability obligations**: stable logical message IDs on redelivery, distinguishable processing attempts, propagated trace context, and a timeline/outcome consistent with committed state. Cover evidence redelivery, delayed evidence after business completion, readiness predicates, and scenario-4 admission versus broker-wait observations. Do **not** introduce dedicated tests of Watermill's outbox-forwarder retry implementation. Tests involving real service crashes/restarts are deferred to V2; broker-outage testing and public outage injection are omitted from V1.

**Acceptance criterion:** a visitor can see that the debit (or refund) command was delivered twice, discover why the second delivery produced no second business effect, follow the compensation path when it occurs, and inspect traces/logs/metrics that substantiate the explanation.
