# Saga Lab — Milestones

**Scope:** delivery sequence for the V1 target described in [Application Requirements](saga-lab-01-application-requirements.md), [Technical Plan](saga-lab-02-technical-plan.md), and [Observability](saga-lab-03-observability.md). Early milestones deliberately implement only part of that target.

Each milestone ends in demonstrable behavior with its own tests and recorded evidence. Basic logs, tracing, and durable history grow with the behavior; final verification is not postponed until release.

1. **One observable transfer end to end:** connect Go, RabbitMQ, Watermill, the coordinator, and independently owned bank databases. Start a happy-path transfer through a minimal page; show balances, basic persisted history, and correlated HTTP/database/message spans. An integration test proves the happy path. Reliability guarantees are explicitly incomplete at this stage.
2. **Reliable transfer processing: atomicity, inbox/outbox, and redelivery:** retain reliability as one combined milestone, implemented through small tickets. Explore acknowledgements/redelivery, demonstrate unsafe behavior in a bounded experiment or failing test, then add durable inboxes, local atomicity, transactional outboxes, and post-commit Nack injection. Prove duplicate debit suppression and eventual completion, duplicate credit safety, and idempotent coordinator progression. Persist attempt evidence and establish debit-scenario playback readiness. Discuss the need for durable HTTP request deduplication. While reshaping the message handlers for inboxes and outboxes, evaluate replacing hand-written payload decoding with Watermill's `cqrs` typed command and event handlers.
3. **Compensation, including duplicate-safe refunds:** implement permanent credit rejection, refund, and refund redelivery. Demonstrate both compensation scenarios with correct balances, tests, and sufficient history for playback.
4. **Isolated consumer unavailability and recovery:** implement the dedicated Bank B queue, serialized admission, consumer cancellation/resumption, and friendly contention UI. Demonstrate approximately five seconds of real broker waiting while normal traffic continues. Test sequential visitors, routing, cleanup, and evidence readiness.
5. **Complete visitor experience:** complete cookie-associated visitor setup and visitor-owned accounts, top-ups, all five scenario choices, outcome counts, and automatic/pause/step/replay controls. Clearly distinguish admission waiting, Saga execution, and preparation of the replay. Playback never repeats business operations.
6. **Complete engineering investigation:** label the admission and delivery waits in traces, mark scenario facts with span events, put trace IDs in logs, and link each transfer to its trace on the transfer's time window. Verify that evidence supports every scenario's explanation. Log shipping, broker metrics, application metrics and dashboards are out of V1.
7. **Public release:** deploy application and observability on the shared VPS behind its Caddy ingress, run full scenario acceptance checks and polish, bound public resource use and retention (an admission limit of five transfers waiting for Bank B unavailability, beyond which a submission is refused without creating a transfer; per-address rate limits at the ingress; seven-day visitor expiry and trace retention), keep public observability read-only (anonymous Grafana Viewers open the transfer's trace through a provisioned Trace dashboard), and document architecture, semantics, invariants, and representative demonstrations. Process-crash and broker-outage exercises remain excluded.

Detail the first milestone into a spec and small tracer-bullet tickets, then refine later milestones using what the working system teaches us. Each implementation ticket carries its tests; the combined reliability milestone is not one oversized implementation ticket.

## Agreed scope

Each milestone, once refined, has its agreed scope in a separate document that links its build specification:

- [Milestone 1](milestones/milestone-1-agreed-scope.md)
- [Milestone 2](milestones/milestone-2-agreed-scope.md)
- [Milestone 3](milestones/milestone-3-agreed-scope.md)
- [Milestone 4](milestones/milestone-4-agreed-scope.md)
- [Milestone 5](milestones/milestone-5-agreed-scope.md)
- [Milestone 6](milestones/milestone-6-agreed-scope.md)
- [Milestone 7](milestones/milestone-7-agreed-scope.md)
