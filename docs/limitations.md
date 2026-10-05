# Limitations

Known limitations of the current design, accepted for now.

## History order depends on the reporting services' clocks

Each history entry's `observed_at` is set by the service that reports it, using that service's own clock. The Transfer Service sets `recorded_at` when it writes the entry. The history is ordered by `observed_at`, so an entry appears where it happened rather than where it arrived.

On Compose all services share the host clock, so the order is reliable. If services ran on separate machines, clock skew could reorder entries that are close in time, for example a `DuplicateSuppressed` from Bank A and Bank B's credit.
