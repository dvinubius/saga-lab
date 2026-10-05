# Dedicated Bank B consumer is off at rest; the Transfer Service times its resume

For Bank B unavailable, Bank B's consumer of the dedicated credit queue is cancelled except while it serves one admitted transfer. The Transfer Service records the broker's confirmation of that transfer's credit command, durably schedules a resume after the configured wait, and sends it to Bank B through its outbox. Bank B registers the consumer, handles the credit, cancels the consumer and reports delivery paused, which together with a terminal outcome releases the demonstration slot. The technical plan instead asks Bank B to stop the consumer and confirm before each transfer's credit is published. That handshake adds a pre-debit transfer state, two message types and a setup-failure path. It still needs the drain report after the credit, so it would be this design plus one more round trip. An off-at-rest consumer is also the safe state when Bank B starts.

## Considered Options

- **Suspend before publish (the plan's wording):** its history shows a per-transfer "consumer stopped" moment before publication; here the absence is guaranteed by construction, because the slot is released only after delivery paused, and is explained by a note rather than a history row.
- **Bank B polls the queue depth and times the wait itself:** no resume message, but the timing reference becomes the first poll that sees the command rather than the publisher confirmation, and the transfer is unknown until consumption.

## Consequences

- The dedicated consumer is not a Watermill router handler. Stopping a router handler closes its channel, but Watermill's consume loop can race cancellation and briefly re-register the consumer or take one more delivery. Bank B therefore consumes and cancels on a channel it owns, with a prefetch of one.
- A publisher confirmation does not prove that a queue received the message: the broker confirms unroutable messages too. Both services declare the dedicated queue at startup so that a confirmed credit command cannot vanish.
- If Bank B crashes after accepting a resume but before handling the credit, the consumer comes back off and the deduplicated resume is not repeated, so the credit stays queued. Crash recovery is deferred to V2; Bank B could durably record the resumed transfer and re-register on startup.
