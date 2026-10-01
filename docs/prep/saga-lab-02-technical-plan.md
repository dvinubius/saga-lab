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

Use a lightweight **cookie-based visitor identity**. On a new visitor session, generate display names for the two banks; provide one account in each. Bank A supports fictional top-ups; transfers go from A to B. One Bank A process owns all visitors’ virtual Bank A banks/accounts in its database; one Bank B process does the equivalent for Bank B. Scope accounts and access by the authenticated cookie-associated visitor identity, without carrying raw cookies into messages or telemetry. Generated bank names do not imply additional processes or databases. The precise table model remains to be settled.

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

The public UI offers **five predefined scenarios only**, selected before starting a transfer. Persist the scenario configuration against the `transfer_id` so the relevant service can act at a particular workflow step. Record when a fault is injected. Keep scenario selection and routing explicit. No generic fault-scripting facility or general operational-control framework is required. Scenario 4 needs a small, explicit coordination handshake for its dedicated consumer lifecycle.

- **Happy path:** no fault.
- **Debit redelivery:** Bank A first commits the inbox/debit/outbox transaction. On that *first* handling attempt, inject a post-commit handler error so Watermill **Nacks and requeues** the delivery. On redelivery the durable inbox suppresses the second debit, and the duplicate is acknowledged. Ensure retry middleware does not swallow the intended handler error before it reaches the acknowledgement path.
- **Permanent credit rejection:** Bank B records a definitive `CreditRejected` outcome; the coordinator requests an idempotent refund from Bank A.
- **Temporary consumer unavailability:** stop only the dedicated scenario-4 Bank B consumer before publishing its credit command, then resume after approximately **five seconds** of broker waiting. Normal Bank B consumption continues. The credit message waits **ready in RabbitMQ**; do **not** fabricate failed delivery attempts during a period with no consumer. See the admission and consumer lifecycle below.
- **Refund redelivery:** after a permanently rejected credit, Bank A commits the first refund/inbox/outbox transaction. Inject a first-attempt post-commit Nack/requeue of `RefundFunds`. The second delivery is recognized as a duplicate and must not refund again.

**Why Nack in V1?** It provides a deterministic redelivery immediately after a successful commit, without introducing consumer-channel closure, reconnection, or process lifecycle management. A missing acknowledgement on an otherwise healthy connection does not itself cause immediate RabbitMQ redelivery, so do not model it as a ten-second acknowledgement timeout.

Do not inject RabbitMQ outages or outbox publication failures as public scenarios. Treat temporary technical unavailability as pending/recoverable, not as a timer-based reason to compensate. The dedicated scenario-4 queue and serialized admission isolate the deliberate delivery interruption from other scenarios.

Actual process termination and restart recovery are **deferred to V2**, as is a possible channel-closure variant of the redelivery experiment. V1 uses controlled handler failures to demonstrate the same idempotency boundary.

### Scenario 4: dedicated queue and serialized admission

Use **two Bank B command queues**: one for normal scenarios and one dedicated to scenario 4. Route each credit command to exactly one queue, never fan it out to both. Both consumers use the same Bank B business handler, database, and idempotency rules. The dedicated queue remains present while its consumer is stopped; do not use auto-delete behavior that removes it on cancellation. Keep consumer lifecycle control independent so stopping it cannot stop the normal router/consumer or evidence publication.

The Transfer Service owns a small durable FIFO admission list and a single active scenario-4 slot. Claim the slot atomically so concurrent requests cannot both start. Persist admission status separately from Saga status. Waiting requests have no debit or outgoing Saga command yet; an HTTP retry for the same request must not add another admission. This is application admission data, not a third Bank B command queue and not a general job scheduler.

For each admitted execution:

1. Reserve the slot and ensure the dedicated queue has no outstanding work from the previous execution. Ask Bank B to stop its dedicated consumer and wait for confirmed cancellation/no active consumer before allowing the credit command to be published. Do not kill the Bank B process. Cancellation does not recall deliveries already in flight, so the previous execution must be drained first.
2. Start the admitted Saga. Route its `CreditFunds` command exclusively to the dedicated queue. Keep the dedicated consumer absent while the command is published.
3. Once broker acceptance of that publication is observed, hold consumption off for approximately five seconds, then re-register the dedicated consumer. Measure and record the actual interval; publisher confirmation is the timing reference, not a claimed exact queue-insertion timestamp. The wait to prepare the consumer or produce the credit command is not part of this five-second interval.
4. Let Bank B process the command normally. Release the slot only when the execution has reached a terminal business outcome and its dedicated-queue work has drained, including in-flight deliveries. The next visitor can then be admitted. Playback and delayed history ingestion do not occupy the slot.

Use a narrow, idempotent request/ready/resume coordination exchange over RabbitMQ, correlated to the active transfer. Bank B owns its consumer; the Transfer Service owns admission and Saga progression. Stale or repeated control messages must not pause or resume a later visitor’s execution. Final message names and Watermill lifecycle hooks are implementation decisions to verify when detailing this milestone. Do not interpret a handler sleep or a prefetched unacknowledged message as the required broker-ready wait.

Define cleanup for debit rejection (no credit command), setup failure, and failed resume so the slot cannot silently remain occupied. Expose an operational problem if recovery cannot proceed; elapsed time alone must neither compensate a transfer nor release the slot while old work is unresolved. Public admission must be bounded. Deliberate process-crash recovery exercises remain outside V1.

With no contention, start automatically without special admission UI. With contention, show a friendly waiting explanation and start automatically on admission. Track admission waiting separately from Saga duration and broker waiting; do not promise a fixed “couple of seconds.”

Consumer lifecycle reference: [RabbitMQ consumers and cancellation](https://www.rabbitmq.com/docs/consumers#canceling).

## 6. Application-owned timeline and frontend playback

Persist **explicit application events** for the domain and selected operational milestones. Do not reconstruct the primary timeline from Grafana logs or attempt to use Prometheus as a transfer-history database.

For a message, capture the evidence available for: outbox record committed, publication attempted, broker acceptance confirmed, consumer delivery, local business commit, and consumer acknowledgement/Nack. Publication attempt, publisher confirmation, and consumer acknowledgement are different facts. **A publisher confirmation is not an independent timestamp for exact queue insertion.** Record business milestones only when the underlying transaction has actually committed.

Each timeline record should carry a stable observation/event ID, the transfer ID, event type, actual timestamp, responsible service, relevant message/causation identity, delivery-attempt identity, outcome, and available trace correlation. Preserve observed timestamps and causality even when broker acceptance is observed after a consumer has already started processing.

### Evidence transport and ownership

Bank-owned evidence reaches the Transfer Service through **bank events → RabbitMQ → Transfer Service → durable history**, with two distinct categories:

- **Business execution evidence:** Bank A atomically commits the inbox entry, debit, and `FundsDebited` outbox event. The Transfer Service consumes that event and atomically records its idempotent Saga progression, corresponding timeline entry, and any outgoing command. The same principle applies to credit, rejection, and refund events.
- **Message-processing evidence:** observations such as `DebitCommandReceived`, `DuplicateSuppressed`, and `NackRequested` describe individual handling attempts. Banks durably record these observations and forward them through their outbox over RabbitMQ. The Transfer Service persists them idempotently by observation ID; they do not advance the Saga. Preserve the observed command's message ID and attempt ID separately from the evidence message's own transport identity.

Redelivery of an evidence event retains its observation ID; a genuinely new observation gets a new ID. A duplicate command can create new attempt evidence without creating a second business outcome event. Post-commit observations require their own local recording transaction; do not imply that a later Nack action was atomic with the earlier debit. `NackRequested` describes application intent, not proof of broker receipt. Record only what the instrumentation actually observes. Keep this evidence vocabulary bounded; do not recursively generate durable evidence about the delivery of evidence messages.

Keep occurrence time separate from history-ingestion time. No service reads another service's database, and history does not depend on telemetry scraping.

### Visualisation readiness

The Transfer Service exposes business status, evidence readiness, and persisted history through its per-transfer API. **A terminal business outcome is necessary but not sufficient for playback readiness.** For example, `FundsDebited` can advance the Saga and `FundsCredited` can complete it before Bank A's `DuplicateSuppressed` observation is consumed and persisted in the Transfer Service. The redelivery itself may also occur after the business outcome. This is asynchronous application evidence, not delayed Prometheus scraping.

Define a bounded readiness predicate per scenario:

- Happy path: terminal outcome and the committed debit/credit history needed for its explanation.
- Debit redelivery: successful outcome plus first-attempt commit/Nack-request evidence and a distinct subsequent attempt with duplicate suppression for the same debit command.
- Permanent credit rejection: terminal outcome plus rejection and committed refund evidence.
- Temporary consumer unavailability: terminal outcome plus dedicated-consumer suspension/resumption and publication/delivery observations supporting the broker-wait explanation.
- Refund redelivery: compensated outcome plus first-refund commit/Nack-request evidence and a subsequent refund attempt with duplicate suppression.

Account for early business rejection as a terminal alternative: do not wait for downstream evidence from operations that never occurred. Final required event sets are defined with each milestone. Readiness means sufficient evidence for the selected narrative, not proof that no further observations can ever arrive.

Show the business outcome immediately and “preparing the replay” while required evidence is pending. If evidence collection fails, expose that separately and allow an honestly incomplete history; do not fabricate observations, declare readiness after an arbitrary delay, or change the business outcome. Late evidence remains ingestible idempotently. A simple status/history polling endpoint is sufficient; live streaming is optional.

**Default experience: wait for execution and required persisted evidence, then animate the history.** This avoids using real backend delays to make the animation legible.

Playback is strictly **read-only**. Allow automatic playback, pause, backward/forward stepping, and replay; optional speed control is acceptable. Use approximately **500–800 ms of minimum screen time for short stages** and stretch longer actual delays proportionally enough to show their significance, while displaying the real timestamps and durations. A five-second queue wait must stand out from millisecond-scale delivery. A simple logical service/broker diagram is sufficient; do not build a Grafana clone or pretend animated movement measures network latency.

Prefer server-rendered pages with small client-side playback behavior. Fetch the durable history when ready; status polling can report admission, execution, and evidence readiness, while a live event feed via Server-Sent Events is optional. Link directly to the appropriate Grafana trace/log/dashboard investigation.

## 7. Automated verification: application invariants

Add tests with each milestone. Exercise the **application's** correctness rather than Watermill's internals. Integration tests should cover:

- The happy path and exactly one debit/credit effect per successful transfer.
- Duplicate `DebitFunds` with one committed debit and eventual completion.
- Duplicate `CreditFunds` without a second credit.
- Permanent credit rejection followed by correct compensation and final balances.
- Duplicate `RefundFunds` after a committed refund without another refund.
- Scenario-4 credit waiting ready with no dedicated consumer, then eventual progress on resumption; normal Bank B traffic continues.
- Concurrent scenario-4 requests admitted sequentially before debit, correct queue routing, and cleanup on early rejection or lifecycle failure.
- Evidence-event deduplication, distinct attempts, and readiness with delayed/out-of-order evidence even after business completion.
- Atomicity of local inbox, business-state, and outbox changes.
- Stable logical message IDs across repeated delivery and distinguishable processing attempts.
- Correct idempotent progression of the Transfer Service on duplicate events.
- Basic trace-context propagation and visibility of repeated attempts, as described in the observability plan.

**Excluded from V1 tests:** broker-outage simulations, dedicated outbox publication-retry/recovery tests, and deliberate process crashes/restarts. Those do not need to be recreated merely to test Watermill or add complexity to V1. Process-crash tests are deferred to V2.

## 8. Milestones

Each milestone ends in demonstrable behavior with its own tests and recorded evidence. Basic logs, tracing, and durable history grow with the behavior; final verification is not postponed until release.

1. **One observable transfer end to end:** connect Go, RabbitMQ, Watermill, the coordinator, and independently owned bank databases. Start a happy-path transfer through a minimal page; show balances, basic persisted history, and correlated HTTP/database/message spans. An integration test proves the happy path. Reliability guarantees are explicitly incomplete at this stage.
2. **Reliable transfer processing: atomicity, inbox/outbox, and redelivery:** retain reliability as one combined milestone, implemented through small tickets. Explore acknowledgements/redelivery, demonstrate unsafe behavior in a bounded experiment or failing test, then add durable inboxes, local atomicity, transactional outboxes, and post-commit Nack injection. Prove duplicate debit suppression and eventual completion, duplicate credit safety, and idempotent coordinator progression. Persist attempt evidence and establish debit-scenario playback readiness.
3. **Compensation, including duplicate-safe refunds:** implement permanent credit rejection, refund, and refund redelivery. Demonstrate both compensation scenarios with correct balances, tests, and sufficient history for playback.
4. **Isolated consumer unavailability and recovery:** implement the dedicated Bank B queue, serialized admission, consumer cancellation/resumption, and friendly contention UI. Demonstrate approximately five seconds of real broker waiting while normal traffic continues. Test sequential visitors, routing, cleanup, and evidence readiness.
5. **Complete visitor experience:** complete cookie-associated visitor setup and virtual accounts, top-ups, all five scenario choices, outcome counts, and automatic/pause/step/replay controls. Clearly distinguish admission waiting, Saga execution, and preparation of the replay. Playback never repeats business operations.
6. **Complete engineering investigation:** finish structured logs, reliability metrics, broker views, provisioned Grafana dashboards, and transfer-specific trace/log links. Verify that evidence supports every scenario's explanation.
7. **Public release:** deploy application and observability, run full scenario acceptance checks and polish, bound public resource use and retention, keep public observability read-only, and document architecture, semantics, invariants, and representative demonstrations. Process-crash and broker-outage exercises remain excluded.

Detail the first milestone into a spec and small tracer-bullet tickets, then refine later milestones using what the working system teaches us. Each implementation ticket carries its tests; the combined reliability milestone is not one oversized implementation ticket.

## 9. Public deployment and explicit exclusions

Use Docker Compose for the Go services, three PostgreSQL databases on a shared instance if convenient, RabbitMQ, the minimal app UI, and the observability stack. Make the demo publicly accessible without exposing write-enabled observability tooling; keep Grafana effectively read-only. Use fictional/generated identities and exclude secrets, tokens, cookies, raw client IPs, database connection strings, and arbitrary personal input from telemetry. Maintain dashboard and collector configuration in source control.

Keep the infrastructure modest and protected against obvious public abuse. No real banking features, many banks, generic Saga/workflow framework, custom broker, consensus implementation, custom tracing/log/dashboard backend, production-grade multi-tenant observability isolation, large frontend framework, or general chaos-engineering platform. There is no V1 need for Kubernetes, Redis, or a network fault proxy to manufacture animation latency.
