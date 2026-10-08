# Own outbox table and relay instead of Watermill's SQL Forwarder

Every service writes outgoing messages to its own `outbox` table inside the transaction that changes its state, and an in-process relay polls that table, publishes each row through the confirming AMQP publisher with its stored UUID and metadata, and deletes it once the broker confirms. The technical plan suggests Watermill's SQL Pub/Sub plus Forwarder, but two of its properties cost more than the roughly hundred lines of relay we now own: forwarding stalls behind any open transaction on the shared PostgreSQL cluster (`pg_snapshot_xmin` visibility), so one service's long transaction would hold up all three services' messages, and forwarded rows are never deleted. We still get at-least-once publication, so consumers deduplicate by message ID either way.

## Consequences

- The relay shares the Forwarder's head-of-line blocking: a failed publish stops the batch, and the oldest row is retried until the broker confirms it, so one refused message holds back every message behind it. This is accepted for its simplicity and strict order; a stuck relay shows in the relay's warning logs, and publication attempts appear in the transfer's `send <topic>` spans. No pending-outbox metric is collected.
- Tracing does not separate the two options. Both republish from a context without the enqueuing trace, so the `send <topic>` span must be started as a child of the trace context stored at enqueue time; the relay does this itself, which keeps the M1 trace shape and makes the span measure the real publish.
- Relay polling must not reach Tempo. It stays out because otelpgx records no span for a query without a recording parent, which would hold for the Forwarder's polling too; a test fails if an otelpgx upgrade changes that.
- The relay is the single place publication is attempted and confirmed, which is where any publish-level evidence would be observed.
