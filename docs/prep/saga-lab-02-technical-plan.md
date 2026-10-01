# Saga Lab — Technical Plan

**Scope of this document:** the suggested architecture, implementation mechanisms, milestones, tests, and deployment. The externally observable behavior is specified in [Application Requirements](saga-lab-01-application-requirements.md); telemetry design is specified in [Observability](saga-lab-03-observability.md).

## 1. Stack and deployment shape

Keep the system small:

- **Go** application services using **Watermill**.
- **RabbitMQ** as the asynchronous message broker.
- **PostgreSQL**, with **one independently owned database per service**: Transfer Service, Bank A, and Bank B. These databases may share a PostgreSQL instance.
- **Docker Compose** for development and a modest public deployment.
- A **minimal, preferably server-rendered UI**, with only the client-side behavior needed for interactive playback.
- The Grafana/OpenTelemetry stack defined in the observability document.

No Kubernetes or Redis. Services must never access or modify another service's database; they communicate about workflow state through commands and events. Do not implement cross-service database transactions.

## 2. Service responsibilities and business flow

**Transfer Service** owns the orchestrated Saga: accepts a request, stores a transfer, issues commands, reacts to bank events, tracks state, initiates compensation, and serves the transfer timeline and UI-facing history. Keep the state machine explicit but small; useful states include `requested`, `debit_pending`, `debit_succeeded`, `credit_pending`, `completed`, `refund_pending`, `refunded`, and `failed`.

**Bank A** owns its account, balance, local transaction history, and debit/refund operations. **Bank B** owns its account, balance, local transaction history, and credit operation. Each also owns its incoming-message deduplication and outgoing-message records.

Keep the command/event vocabulary small:

- Commands: `DebitFunds`, `CreditFunds`, `RefundFunds`.
- Events: `FundsDebited`, `FundsCredited`, `FundsRefunded`, `DebitRejected`, `CreditRejected`, and, if useful, `TransferFailed`.

Happy path: record transfer and `DebitFunds` → Bank A commits debit and records `FundsDebited` → Transfer Service records progression and `CreditFunds` → Bank B commits credit and records `FundsCredited` → Transfer Service completes transfer.

Compensation path: `CreditRejected` after a successful debit → Transfer Service records `RefundFunds` → Bank A commits refund and records `FundsRefunded` → Transfer Service records failed/refunded outcome.

Do not turn this one implementation into a generic Saga engine. Preserve logical separation so another business scenario could be added later without requiring V1 to implement it.

## 3. Application identity and message identity

Use a lightweight **cookie-based visitor identity**. On a new visitor session, generate display names for the two banks; provide one account in each. Bank A supports fictional top-ups; transfers go from A to B. The display names belong to the demo presentation, not to additional bank-service instances.

Carry the following identifiers where applicable:

- `transfer_id`: the business Saga identifier, also sufficient as the **business correlation key** for V1.
- `message_id`: immutable identity of each logical command or event; **preserved on redelivery**.
- `causation_id`: identifies the preceding message when a causal link is useful.
- Occurrence timestamps, message types, and delivery/handler attempt information where observable.
- OpenTelemetry trace and span context, described separately in the observability document.

**Do not add a separate `correlation_id` for V1.** A redelivered command keeps its `message_id` but generates a separate processing observation/span.

## 4. Local transaction boundaries and idempotency

Treat RabbitMQ delivery as **at least once**. All command/event consumers, including the Transfer Service, must handle duplicate logical messages without repeating business state transitions.

For a bank command, execute one local PostgreSQL transaction that:

1. Records the incoming `message_id` in a **durable inbox** with a uniqueness constraint.
2. Applies the debit, credit, or refund exactly once, if the message is new and valid.
3. Records the corresponding outgoing event in a **transactional outbox**.

A duplicate must not alter the balance or create a second logical outgoing event. Its *delivery attempt* is still observed, and it can be acknowledged safely. The Transfer Service uses equivalent idempotent handling for bank events and its own state transitions/outgoing commands.

The outbox eliminates the application's dual-write gap: state change and outgoing-message record commit together. A separate publisher forwards committed outbox records to RabbitMQ. Consider Watermill's **SQL Pub/Sub** and **Forwarder** for this; begin with Watermill's simple **Router, Publisher, and Subscriber** APIs. Adopt middleware or the CQRS package only when they simplify the application, not to maximize framework usage.

Publication and consumer acknowledgement remain distinct. Outbox forwarding may produce duplicate publications, so consumer idempotency still matters. **Do not build a custom reliable-publishing framework or dedicated tests of Watermill's internal publisher retry/recovery behavior.** Test the application's own transactional inbox/state/outbox guarantees.

## 5. Deterministic fault injection

The public UI offers **five predefined scenarios only**, selected before starting a transfer. Persist the scenario configuration against the `transfer_id` so the relevant service can act at a particular workflow step. Record when a fault is injected. No generic fault-scripting facility, embedded ad hoc message flags, or separate operational-message protocol is required.

- **Happy path:** no fault.
- **Debit redelivery:** Bank A first commits the inbox/debit/outbox transaction. On that *first* handling attempt, inject a post-commit handler error so Watermill **Nacks and requeues** the delivery. On redelivery the durable inbox suppresses the second debit, and the duplicate is acknowledged. Ensure retry middleware does not swallow the intended handler error before it reaches the acknowledgement path.
- **Permanent credit rejection:** Bank B records a definitive `CreditRejected` outcome; the coordinator requests an idempotent refund from Bank A.
- **Temporary consumer unavailability:** stop Bank B's consumption for approximately **five seconds**, then resume it. The credit message waits **ready in RabbitMQ**; do **not** fabricate failed delivery attempts during a period with no consumer.
- **Refund redelivery:** after a permanently rejected credit, Bank A commits the first refund/inbox/outbox transaction. Inject a first-attempt post-commit Nack/requeue of `RefundFunds`. The second delivery is recognized as a duplicate and must not refund again.

**Why Nack in V1?** It provides a deterministic redelivery immediately after a successful commit, without introducing consumer-channel closure, reconnection, or process lifecycle management. A missing acknowledgement on an otherwise healthy connection does not itself cause immediate RabbitMQ redelivery, so do not model it as a ten-second acknowledgement timeout.

Do not inject RabbitMQ outages or outbox publication failures as public scenarios. Treat temporary technical unavailability as pending/recoverable, not as a timer-based reason to compensate. In a shared public deployment, keep any consumer-wide pause controlled so its impact on concurrent visitors is understood.

Actual process termination and restart recovery are **deferred to V2**, as is a possible channel-closure variant of the redelivery experiment. V1 uses controlled handler failures to demonstrate the same idempotency boundary.

## 6. Application-owned timeline and frontend playback

Persist **explicit application events** for the domain and selected operational milestones. Do not reconstruct the primary timeline from Grafana logs or attempt to use Prometheus as a transfer-history database.

For a message, capture the evidence available for: outbox record committed, publication attempted, broker acceptance confirmed, consumer delivery, local business commit, and consumer acknowledgement/Nack. Publication attempt, publisher confirmation, and consumer acknowledgement are different facts. **A publisher confirmation is not an independent timestamp for exact queue insertion.** Record business milestones only when the underlying transaction has actually committed.

Each timeline record should carry the transfer ID, event type, actual timestamp, responsible service, relevant message/causation identity, delivery-attempt identity, outcome, and available trace correlation. Preserve observed timestamps and causality even when broker acceptance is observed after a consumer has already started processing.

The Transfer Service exposes a per-transfer history endpoint to the UI. A simple status/progress indication is optional while execution runs. **Default experience: wait for the real Saga execution, then animate its persisted history.** This avoids using real backend delays to make the animation legible.

Playback is strictly **read-only**. Allow automatic playback, pause, backward/forward stepping, and replay; optional speed control is acceptable. Use approximately **500–800 ms of minimum screen time for short stages** and stretch longer actual delays proportionally enough to show their significance, while displaying the real timestamps and durations. A five-second queue wait must stand out from millisecond-scale delivery. A simple logical service/broker diagram is sufficient; do not build a Grafana clone or pretend animated movement measures network latency.

Prefer server-rendered pages with small client-side playback behavior. Fetch the durable history after execution; a live feed via Server-Sent Events or polling is optional later, not required for the default replay experience. Link directly to the appropriate Grafana trace/log/dashboard investigation.

## 7. Automated verification: application invariants

Exercise the **application's** correctness rather than Watermill's internals. Integration tests should cover:

- The happy path and exactly one debit/credit effect per successful transfer.
- Duplicate `DebitFunds` with one committed debit and eventual completion.
- Duplicate `CreditFunds` without a second credit.
- Permanent credit rejection followed by correct compensation and final balances.
- Duplicate `RefundFunds` after a committed refund without another refund.
- Temporarily unavailable Bank B and eventual progress when consumption resumes.
- Atomicity of local inbox, business-state, and outbox changes.
- Stable logical message IDs across repeated delivery and distinguishable processing attempts.
- Correct idempotent progression of the Transfer Service on duplicate events.
- Basic trace-context propagation and visibility of repeated attempts, as described in the observability plan.

**Excluded from V1 tests:** broker-outage simulations, dedicated outbox publication-retry/recovery tests, and deliberate process crashes/restarts. Those do not need to be recreated merely to test Watermill or add complexity to V1. Process-crash tests are deferred to V2.

## 8. Milestones

1. **Basic messaging:** set up Go, RabbitMQ, Watermill Publisher/Subscriber/Router, and basic commands/events. No failure handling yet.
2. **Transfer workflow:** introduce the coordinator and two independently owned bank databases, transfer records, local debit/credit, and a basic happy path.
3. **Early trace instrumentation:** add HTTP, database, and message producer/consumer spans and propagate context through messages before failure behavior grows complicated.
4. **Reliability in one combined milestone (formerly milestones 4–6):** explore acknowledgements/redelivery; add deterministic post-commit Nack injection; implement durable inbox, local atomicity, and transactional outbox; demonstrate duplicate suppression. It is useful to observe the unsafe behavior before protecting it.
5. **Compensation:** implement permanent credit rejection, refund, and the duplicate-safe refund scenario.
6. **Demo UI:** visitor setup, top-up/transfer controls, scenario selection, durable timeline, visual replay, outcome counts, and telemetry deep links.
7. **Full observability:** complete metrics, structured logs, Grafana/Prometheus/Loki/Tempo dashboards, and investigation paths.
8. **V1 integration tests and polish:** verify invariants and scenario behavior without process-crash or broker-outage exercises.
9. **Publish:** deploy the application and observability stack, provision dashboards from version-controlled configuration, and document architecture, delivery semantics, invariants, scenarios, and representative traces/screenshots.

Instrumentation starts early (milestone 3); the full dashboard experience can be finalized later. The combined reliability milestone can still be built incrementally within that milestone.

## 9. Public deployment and explicit exclusions

Use Docker Compose for the Go services, three PostgreSQL databases on a shared instance if convenient, RabbitMQ, the minimal app UI, and the observability stack. Make the demo publicly accessible without exposing write-enabled observability tooling; keep Grafana effectively read-only. Use fictional/generated identities and exclude secrets, tokens, cookies, raw client IPs, database connection strings, and arbitrary personal input from telemetry. Maintain dashboard and collector configuration in source control.

Keep the infrastructure modest and protected against obvious public abuse. No real banking features, many banks, generic Saga/workflow framework, custom broker, consensus implementation, custom tracing/log/dashboard backend, production-grade multi-tenant observability isolation, large frontend framework, or general chaos-engineering platform. There is no V1 need for Kubernetes, Redis, or a network fault proxy to manufacture animation latency.
