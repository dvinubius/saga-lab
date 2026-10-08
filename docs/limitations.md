# Limitations

Known limitations of the current design, and known gaps in the
implementation, accepted for now.

## History order depends on the reporting services' clocks

Each history entry's `observed_at` is set by the service that reports it, using that service's own clock. The Transfer Service sets `recorded_at` when it writes the entry. The history is ordered by `observed_at`, so an entry appears where it happened rather than where it arrived.

On Compose all services share the host clock, so the order is reliable. If services ran on separate machines, clock skew could reorder entries that are close in time, for example a `DuplicateSuppressed` from Bank A and Bank B's credit.

## Known gaps

A passing demonstration is not a crash-safe or stall-safe system. Process
crashes, restarts and broker outages are not exercised; these gaps are known:

- If Bank B crashes after committing a resume but before handling its credit,
  the dedicated consumer starts off and the credit stays queued until a demo
  reset ([ADR 0002](adr/0002-dedicated-consumer-off-at-rest.md)). The transfer
  keeps the demonstration slot meanwhile, so later Bank B unavailable
  transfers wait for admission.
- A connected broker that withholds publisher confirms (during a memory or
  disk alarm) stalls the outbox relay. No metric or alert reports it; the
  stalled transfer stays visibly pending.
- A `NackRequested` whose own transaction fails leaves its transfer not ready
  indefinitely; the history honestly shows what was recorded.
- A command for a missing account, possible only after a reset in mid-flight,
  is dropped with a warning and leaves its transfer pending; for a refund,
  the transfer stays pending its refund.
- A partial reset, of some services by hand rather than through
  `make reset`, is unguarded.
- A blind HTTP retry of a submission after the transfer has ended starts a
  second transfer: there is no durable request identity.
- Admission never times out, and a bank that never comes back leaves its
  transfer pending: nothing times out.
