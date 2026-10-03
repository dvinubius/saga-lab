# Saga Lab — Milestones

**Scope:** delivery sequence for the V1 target described in [Application Requirements](saga-lab-01-application-requirements.md), [Technical Plan](saga-lab-02-technical-plan.md), and [Observability](saga-lab-03-observability.md). Early milestones deliberately implement only part of that target.

Each milestone ends in demonstrable behavior with its own tests and recorded evidence. Basic logs, tracing, and durable history grow with the behavior; final verification is not postponed until release.

1. **One observable transfer end to end:** connect Go, RabbitMQ, Watermill, the coordinator, and independently owned bank databases. Start a happy-path transfer through a minimal page; show balances, basic persisted history, and correlated HTTP/database/message spans. An integration test proves the happy path. Reliability guarantees are explicitly incomplete at this stage.
2. **Reliable transfer processing: atomicity, inbox/outbox, and redelivery:** retain reliability as one combined milestone, implemented through small tickets. Explore acknowledgements/redelivery, demonstrate unsafe behavior in a bounded experiment or failing test, then add durable inboxes, local atomicity, transactional outboxes, and post-commit Nack injection. Prove duplicate debit suppression and eventual completion, duplicate credit safety, and idempotent coordinator progression. Persist attempt evidence and establish debit-scenario playback readiness. Discuss the need for durable HTTP request deduplication. While reshaping the message handlers for inboxes and outboxes, evaluate replacing hand-written payload decoding with Watermill's `cqrs` typed command and event handlers.
3. **Compensation, including duplicate-safe refunds:** implement permanent credit rejection, refund, and refund redelivery. Demonstrate both compensation scenarios with correct balances, tests, and sufficient history for playback.
4. **Isolated consumer unavailability and recovery:** implement the dedicated Bank B queue, serialized admission, consumer cancellation/resumption, and friendly contention UI. Demonstrate approximately five seconds of real broker waiting while normal traffic continues. Test sequential visitors, routing, cleanup, and evidence readiness.
5. **Complete visitor experience:** complete cookie-associated visitor setup and visitor-owned accounts, top-ups, all five scenario choices, outcome counts, and automatic/pause/step/replay controls. Clearly distinguish admission waiting, Saga execution, and preparation of the replay. Playback never repeats business operations.
6. **Complete engineering investigation:** finish structured logs, reliability metrics, broker views, provisioned Grafana dashboards, and transfer-specific trace/log links. Verify that evidence supports every scenario's explanation.
7. **Public release:** deploy application and observability, run full scenario acceptance checks and polish, bound public resource use and retention, keep public observability read-only (and decide how anonymous visitors then open the transfer page's trace links), and document architecture, semantics, invariants, and representative demonstrations. Process-crash and broker-outage exercises remain excluded.

Detail the first milestone into a spec and small tracer-bullet tickets, then refine later milestones using what the working system teaches us. Each implementation ticket carries its tests; the combined reliability milestone is not one oversized implementation ticket.

## Milestone 1 — Agreed scope

Build specification: [GitHub issue #1 — One observable transfer end to end](https://github.com/dvinubius/saga-lab/issues/1).

These decisions refine milestone 1; the later milestones retain the rest of the V1 target.

- **Starting state:** one prepared demonstration visitor with one account per bank; Bank A starts with 100 credits and Bank B with zero. Repeat transfers are possible while funds remain. Provide a development reset command. Cookie-based visitor provisioning and top-ups remain in milestone 5.
- **Amounts and rejection:** use positive whole-number credits. Reject malformed, fractional, zero, and negative amounts before starting a Saga. Bank A authoritatively rejects insufficient funds without changing either balance; record the rejection and end the transfer without credit or refund. This is ordinary business handling, not a sixth public scenario.
- **Minimal page:** accept a transfer amount and show its transfer ID, current status, account balances, and plain persisted history. The successful narrative is requested → debit committed → credit committed → completed. Poll while pending and preserve the selected transfer on refresh. Animation and playback controls remain out of scope.
- **Trace inspection:** include a minimal OpenTelemetry Collector → Tempo → Grafana path to inspect correlated HTTP, message, and database spans across the three services. Dashboards, metrics infrastructure, and log aggregation remain for later milestones.
- **Primary test boundary:** exercise the running application through HTTP with real PostgreSQL, RabbitMQ, and the three services. From the prepared state, transferring 25 credits must complete with balances of 75 and 25 and persisted history available through application interfaces. Cover insufficient funds and input validation, plus a focused trace-propagation check. Avoid coupling acceptance tests to Watermill handler structure or internal table layouts.

**Submission boundary:** allow only one pending transfer for the prepared visitor. Disable submission while pending and enforce the restriction atomically on the server. An overlapping request creates no transfer and identifies the active transfer. Refreshing the result page never resubmits.

## Milestone 2 — Agreed scope

Build specification: [GitHub issue #30 — Repeated debit delivery without repeated effects](https://github.com/dvinubius/saga-lab/issues/30), synthesized from the wayfinder map [#8](https://github.com/dvinubius/saga-lab/issues/8).

These decisions refine milestone 2. Everything in the milestone 2 entry above is in scope.

- **Scenarios:** the visitor chooses Happy path (default) or Debit redelivery on the existing home form. The scenario is fixed for the transfer and carried to Bank A in `DebitFunds`. Under debit redelivery, Bank A commits the debit, then fails once before acknowledging. The broker redelivers the command and Bank A recognises the repeat. The transfer completes with the happy path's balances. Insufficient funds still ends in an ordinary debit rejection, with no injected fault.
- **Durable progression:** every local state change commits atomically with its outgoing message through a per-service transactional outbox in all three services, including the initial `DebitFunds`. A publish failure after commit never strands a transfer. This is guaranteed by construction; crash/restart tests stay deferred.
- **Duplicate safety:** the banks deduplicate commands through a durable inbox keyed by message ID. The coordinator deduplicates bank events through its status-guarded transitions. Redelivered commands and events never repeat a transition or business effect.
- **Sufficient evidence:** durable Transfer Service history holds business steps plus per-attempt processing observations (a requeue requested after commit, a suppressed duplicate). Trace spans distinguish handling attempts of one message ID. Log fields are added where cheap.
- **Demonstration:** plain history on the existing page shows both attempts. A readiness status is shown separately from business status. There is no animation or playback.
- **Unsafe behaviour:** shown by a test that fails first and is never committed red. It is not a kept unsafe mode or a broker experiment.
- **Request deduplication:** no durable HTTP request deduplication in milestone 2. The pending-transfer restriction covers a lost response while pending. A blind retry after the transfer has ended may start a second transfer, which is accepted.
- **Watermill `cqrs`:** evaluated and not adopted. Handlers keep hand-written decoding.
- **Primary test boundary:** debit redelivery is proven over HTTP against the running stack. Guarantees no public scenario produces (duplicate credits, duplicate coordinator events, local atomicity) are proven by package-level tests against real PostgreSQL.
