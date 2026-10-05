# Milestone 1 — Agreed scope

Build specification: [GitHub issue #1 — One observable transfer end to end](https://github.com/dvinubius/saga-lab/issues/1).

These decisions refine milestone 1 in [Milestones](../saga-lab-04-milestones.md); the later milestones retain the rest of the V1 target.

- **Starting state:** one prepared demonstration visitor with one account per bank; Bank A starts with 100 credits and Bank B with zero. Repeat transfers are possible while funds remain. Provide a development reset command. Cookie-based visitor provisioning and top-ups remain in milestone 5.
- **Amounts and rejection:** use positive whole-number credits. Reject malformed, fractional, zero, and negative amounts before starting a Saga. Bank A authoritatively rejects insufficient funds without changing either balance; record the rejection and end the transfer without credit or refund. This is ordinary business handling, not a sixth public scenario.
- **Minimal page:** accept a transfer amount and show its transfer ID, current status, account balances, and plain persisted history. The successful narrative is requested → debit committed → credit committed → completed. Poll while pending and preserve the selected transfer on refresh. Animation and playback controls remain out of scope.
- **Trace inspection:** include a minimal OpenTelemetry Collector → Tempo → Grafana path to inspect correlated HTTP, message, and database spans across the three services. Dashboards, metrics infrastructure, and log aggregation remain for later milestones.
- **Primary test boundary:** exercise the running application through HTTP with real PostgreSQL, RabbitMQ, and the three services. From the prepared state, transferring 25 credits must complete with balances of 75 and 25 and persisted history available through application interfaces. Cover insufficient funds and input validation, plus a focused trace-propagation check. Avoid coupling acceptance tests to Watermill handler structure or internal table layouts.

**Submission boundary:** allow only one pending transfer for the prepared visitor. Disable submission while pending and enforce the restriction atomically on the server. An overlapping request creates no transfer and identifies the active transfer. Refreshing the result page never resubmits.
