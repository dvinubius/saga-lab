# Milestone 2 — Agreed scope

Build specification: [GitHub issue #30 — Repeated debit delivery without repeated effects](https://github.com/dvinubius/saga-lab/issues/30), synthesized from the wayfinder map [#8](https://github.com/dvinubius/saga-lab/issues/8).

These decisions refine milestone 2. Everything in the milestone 2 entry in [Milestones](../saga-lab-04-milestones.md) is in scope.

- **Scenarios:** the visitor chooses Happy path (default) or Debit redelivery on the existing home form. The scenario is fixed for the transfer and carried to Bank A in `DebitFunds`. Under debit redelivery, Bank A commits the debit, then fails once before acknowledging. The broker redelivers the command and Bank A recognises the repeat. The transfer completes with the happy path's balances. Insufficient funds still ends in an ordinary debit rejection, with no injected fault.
- **Durable progression:** every local state change commits atomically with its outgoing message through a per-service transactional outbox in all three services, including the initial `DebitFunds`. A publish failure after commit never strands a transfer. This is guaranteed by construction; crash/restart tests stay deferred.
- **Duplicate safety:** the banks deduplicate commands through a durable inbox keyed by message ID. The coordinator deduplicates bank events through its status-guarded transitions. Redelivered commands and events never repeat a transition or business effect.
- **Sufficient evidence:** durable Transfer Service history holds business steps plus per-attempt processing observations (a requeue requested after commit, a suppressed duplicate). Trace spans distinguish handling attempts of one message ID. Log fields are added where cheap.
- **Demonstration:** plain history on the existing page shows both attempts. A readiness status is shown separately from business status. There is no animation or playback.
- **Unsafe behaviour:** shown by a test that fails first and is never committed red. It is not a kept unsafe mode or a broker experiment.
- **Request deduplication:** no durable HTTP request deduplication in milestone 2. The pending-transfer restriction covers a lost response while pending. A blind retry after the transfer has ended may start a second transfer, which is accepted.
- **Watermill `cqrs`:** evaluated and not adopted. Handlers keep hand-written decoding.
- **Primary test boundary:** debit redelivery is proven over HTTP against the running stack. Guarantees no public scenario produces (duplicate credits, duplicate coordinator events, local atomicity) are proven by package-level tests against real PostgreSQL.
