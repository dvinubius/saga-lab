# Saga Lab — Milestones

**Scope:** delivery sequence for the V1 target described in [Application Requirements](saga-lab-01-application-requirements.md), [Technical Plan](saga-lab-02-technical-plan.md), and [Observability](saga-lab-03-observability.md). Early milestones deliberately implement only part of that target.

Each milestone ends in demonstrable behavior with its own tests and recorded evidence. Basic logs, tracing, and durable history grow with the behavior; final verification is not postponed until release.

1. **One observable transfer end to end:** connect Go, RabbitMQ, Watermill, the coordinator, and independently owned bank databases. Start a happy-path transfer through a minimal page; show balances, basic persisted history, and correlated HTTP/database/message spans. An integration test proves the happy path. Reliability guarantees are explicitly incomplete at this stage.
2. **Reliable transfer processing: atomicity, inbox/outbox, and redelivery:** retain reliability as one combined milestone, implemented through small tickets. Explore acknowledgements/redelivery, demonstrate unsafe behavior in a bounded experiment or failing test, then add durable inboxes, local atomicity, transactional outboxes, and post-commit Nack injection. Prove duplicate debit suppression and eventual completion, duplicate credit safety, and idempotent coordinator progression. Persist attempt evidence and establish debit-scenario playback readiness. Discuss the need for durable HTTP request deduplication.
3. **Compensation, including duplicate-safe refunds:** implement permanent credit rejection, refund, and refund redelivery. Demonstrate both compensation scenarios with correct balances, tests, and sufficient history for playback.
4. **Isolated consumer unavailability and recovery:** implement the dedicated Bank B queue, serialized admission, consumer cancellation/resumption, and friendly contention UI. Demonstrate approximately five seconds of real broker waiting while normal traffic continues. Test sequential visitors, routing, cleanup, and evidence readiness.
5. **Complete visitor experience:** complete cookie-associated visitor setup and visitor-owned accounts, top-ups, all five scenario choices, outcome counts, and automatic/pause/step/replay controls. Clearly distinguish admission waiting, Saga execution, and preparation of the replay. Playback never repeats business operations.
6. **Complete engineering investigation:** finish structured logs, reliability metrics, broker views, provisioned Grafana dashboards, and transfer-specific trace/log links. Verify that evidence supports every scenario's explanation.
7. **Public release:** deploy application and observability, run full scenario acceptance checks and polish, bound public resource use and retention, keep public observability read-only, and document architecture, semantics, invariants, and representative demonstrations. Process-crash and broker-outage exercises remain excluded.

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
