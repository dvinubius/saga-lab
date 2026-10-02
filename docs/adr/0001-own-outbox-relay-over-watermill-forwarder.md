# Own outbox table and relay instead of Watermill's SQL Forwarder

Every service writes outgoing messages to its own `outbox` table inside the transaction that changes its state, and an in-process relay polls that table, publishes each row through the confirming AMQP publisher with its stored UUID and metadata, and deletes it once the broker confirms. The technical plan suggests Watermill's SQL Pub/Sub plus Forwarder, but for a demonstration whose purpose is explaining its own message flow its quirks cost more than the roughly hundred lines of relay we now own: forwarding stalls behind any open transaction on the shared PostgreSQL cluster (`pg_snapshot_xmin` visibility), one refused message blocks every topic behind it, rows are never deleted, and the Forwarder's router emits parentless spans unless worked around. We still get at-least-once publication, so consumers deduplicate by message ID either way.

## Consequences

- The relay starts the `send <topic>` span as a child of the trace context stored at enqueue time, so traces keep the M1 shape and the span measures the real publish; relay polling must not reach Tempo.
- The relay is the single place publication is attempted and confirmed, which is where any publish-level evidence would be observed.
