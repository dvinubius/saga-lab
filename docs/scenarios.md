# Scenarios and invariants

A **transfer** moves a whole number of fictional credits from a visitor's
Bank A account to the same visitor's Bank B account. The Transfer Service
orchestrates it as a Saga: Bank A's debit and Bank B's credit are separate
local transactions, joined only by messages, and a credit Bank B rejects is
compensated by a refund rather than rolled back. The visitor picks one of five
**scenarios** before submitting; it stays fixed for that transfer.

This document states what each scenario does, the invariants every transfer
keeps, and what a representative run looks like, so the claims can be checked
against the live site or a local stack. [Architecture](architecture.md)
explains the messages, outbox and inbox the scenarios rely on.

## The common path

1. The Transfer Service records the transfer as `debit_pending` with its
   `requested` step and sends `DebitFunds`.
2. Bank A commits the debit and replies `FundsDebited`, or, when the account
   cannot afford it, replies `DebitRejected` ("Insufficient funds").
3. On `FundsDebited` the Transfer Service records the debit, moves to
   `credit_pending` and sends `CreditFunds`.
4. Bank B commits the credit and replies `FundsCredited`.
5. The Transfer Service records the credit and the transfer ends `completed`.

On `DebitRejected` the transfer ends `rejected` with the reason, both balances
unchanged and no credit command. This holds under every scenario: an
unaffordable debit is rejected as usual, with no fault, refund or wait.

A bank that is unavailable leaves its command queued; the transfer stays
pending until the bank processes it. Nothing times out.

## Invariants

- **Credits are conserved.** A `completed` transfer moves exactly its amount:
  Bank A loses it and Bank B gains it. A `rejected` or `refunded` transfer
  leaves both balances where they were before it.
- **No duplicate effects.** Each command commits its business effect at most
  once, however often it is delivered. The outcome summary counts
  `duplicate_effects`, and it is always 0.
- **State and messages commit together.** Every service writes its state
  change and the messages it causes in one transaction, so no message is lost
  or sent for a change that did not happen.
- **A refund cannot be rejected.** It is a new operation that restores the
  source balance, not an undo of the debit.
- **One pending transfer per visitor.** A submission made while one is pending
  starts nothing and names the pending one (the pending-transfer
  restriction). It is not request deduplication: a repeated submission after
  the pending transfer has ended starts a new transfer.
- **One Bank B unavailable transfer at a time.** It holds the demonstration
  slot; at most five more await admission.
- **Status and evidence are separate.** A transfer's business outcome is
  recorded as it happens; whether its history holds all the evidence to
  explain it (visualisation readiness) is computed on every read and gates
  nothing.

## Scenarios

### Happy path

The common path with no fault: one attempt and one effect for each command.

### Debit redelivery

Bank A commits the debit, but its acknowledgement of `DebitFunds` never
reaches RabbitMQ, as if Bank A had crashed right after the commit. The
demonstration simulates this lost acknowledgement by requesting redelivery (a
Nack) instead of crashing, and reports `NackRequested`. RabbitMQ redelivers
`DebitFunds`; Bank A finds its message ID in the inbox, applies nothing, and
reports `DuplicateSuppressed`. `FundsDebited` already left through Bank A's
outbox with the commit, so the transfer completes with the happy path's
balances: two attempts, one effect.

### Credit rejection & refund

Bank A commits the debit, then Bank B permanently rejects the credit with
`CreditRejected` ("Credit refused by Bank B"), its balance unchanged. This is
a business outcome the scenario selects, not an injected fault, and it
concerns that one credit; a later transfer under another scenario credits
normally. The Transfer Service records the rejection, moves to
`refund_pending` and sends `RefundFunds`. Bank A adds the amount back and
replies `FundsRefunded`, and the transfer ends `refunded` ("Refunded to
Bank A"). The intermediate Bank A balance shows in the debit and refund steps.

### Bank B unavailable

The credit command's delivery pauses for a few seconds while it waits in the
broker, with no consumer for it. Bank B keeps serving every other transfer.

- **Admission.** The transfer claims the demonstration slot when it is
  submitted. If another Bank B unavailable transfer holds the slot or is
  already waiting, the new one exists at once as `awaiting_admission`, with
  its `requested` step and no debit command; Bank A keeps its balance and the
  page says "Another visitor is trying this demo. Yours will start
  automatically when it's your turn." It is pending, so it blocks that
  visitor's next submission. When the holder ends (completed and Bank B has
  reported `DeliveryPaused`, or its debit was rejected), the same transaction
  releases the slot and admits the oldest waiting transfer, recording
  `Admitted` and sending its debit. Admission never times out.
- **Admission limit.** At most five transfers await admission at once, not
  counting the slot holder. A sixth Bank B unavailable submission is refused
  with no transfer created and nothing debited: `503 Service Unavailable` with
  `Retry-After: 60` in the API, and on the page the form, amount and scenario
  kept, with the note "The Bank B unavailable demo is busy right now. Try
  again in a minute, or pick another scenario." Other scenarios are never
  limited, and transfers already waiting keep their place.
- **Delivery wait.** After the debit, the Transfer Service sends `CreditFunds`
  to the `CreditFundsDedicated` queue, which Bank B does not consume at rest.
  When RabbitMQ confirms the publication, the Transfer Service records
  `CreditConfirmed` and stores the confirmation time and the resume time on
  the transfer, so it can restart during the wait. Confirmation is when the
  broker accepted the command, not the exact moment it entered the queue. The
  transfer stays `credit_pending` ("Waiting for Bank B to credit"). The wait
  is `BANK_B_RESUME_WAIT`: 5 s locally and in production, 2.5 s in the test
  suite.
- **Resumption.** After the wait the Transfer Service sends `ResumeDelivery`
  through its outbox. Bank B claims it through its inbox, registers its
  dedicated consumer and reports `DeliveryResumed`. The credit runs through
  the normal credit handler, inbox and outbox. After acknowledging it, Bank B
  cancels the consumer and reports `DeliveryPaused`, which releases the slot.
  A duplicate resume is suppressed; a failed registration is logged and the
  resume redelivered.

The history shows Bank B as unavailable right after the confirmation, with a
note that the command waits in the broker's queue with no consumer, neither
delivered nor failed, and the resumption row shows the measured wait.

### Credit rejection & refund redelivery

Everything happens as under credit rejection up to the refund. Bank A commits
the refund, but its acknowledgement of `RefundFunds` is lost, simulated as for
the debit: RabbitMQ redelivers the command, Bank A recognises it and applies
nothing, and the transfer still ends `refunded` with the same balances. The
refund, a command like any other, survives redelivery too.

## Representative demonstrations

Each run below is a fresh visitor (100 credits at Bank A, 0 at Bank B)
transferring 25 credits. The history lists entries as the JSON API returns
them, ordered by when they were observed; steps carry the bank's balance
before and after its own effect. The outcome summary is the JSON `outcome`.
`smoke.sh` checks the same summaries against a running stack.

### Happy path → `completed`

| Entry | Service | Balance |
| --- | --- | --- |
| `requested` | Transfer Service | |
| `debit_committed` | Bank A | 100 → 75 |
| `credit_requested` | Transfer Service | |
| `credit_committed` | Bank B | 0 → 25 |
| `finished` | Transfer Service | |

Outcome: Bank A 100 → 75, Bank B 0 → 25; debit 1 attempt / 1 effect, credit
1 / 1, no refund; 0 duplicates suppressed.

### Debit redelivery → `completed`

| Entry | Service | Balance |
| --- | --- | --- |
| `requested` | Transfer Service | |
| `debit_committed` | Bank A (attempt 1) | 100 → 75 |
| `NackRequested` | Bank A (attempt 1) | |
| `DuplicateSuppressed` | Bank A (attempt 2) | |
| `credit_requested` | Transfer Service | |
| `credit_committed` | Bank B | 0 → 25 |
| `finished` | Transfer Service | |

Outcome: Bank A 100 → 75, Bank B 0 → 25; debit **2 attempts / 1 effect**,
credit 1 / 1, no refund; 1 duplicate suppressed.

### Credit rejection & refund → `refunded`

| Entry | Service | Balance |
| --- | --- | --- |
| `requested` | Transfer Service | |
| `debit_committed` | Bank A | 100 → 75 |
| `credit_requested` | Transfer Service | |
| `credit_rejected` | Bank B | 0 → 0 |
| `refund_requested` | Transfer Service | |
| `refund_committed` | Bank A | 75 → 100 |
| `transfer_refunded` | Transfer Service | |

`rejection_reason` is "Credit refused by Bank B". Outcome: Bank A 100 → 100,
Bank B 0 → 0; debit 1 / 1, credit **1 attempt / 0 effects**, refund 1 / 1; 0
duplicates suppressed.

### Bank B unavailable → `completed`

| Entry | Service | Balance |
| --- | --- | --- |
| `requested` | Transfer Service | |
| `debit_committed` | Bank A | 100 → 75 |
| `credit_requested` | Transfer Service | |
| `CreditConfirmed` | Transfer Service | |
| `DeliveryResumed` | Bank B | |
| `credit_committed` | Bank B | 0 → 25 |
| `DeliveryPaused` | Bank B | |
| `finished` | Transfer Service | |

`DeliveryResumed` follows `CreditConfirmed` by the delivery wait. A transfer
that had to wait for the slot also has `Admitted` after `requested`, and its
debit is issued by that entry. Outcome: as the happy path.

### Credit rejection & refund redelivery → `refunded`

| Entry | Service | Balance |
| --- | --- | --- |
| `requested` | Transfer Service | |
| `debit_committed` | Bank A | 100 → 75 |
| `credit_requested` | Transfer Service | |
| `credit_rejected` | Bank B | 0 → 0 |
| `refund_requested` | Transfer Service | |
| `refund_committed` | Bank A (attempt 1) | 75 → 100 |
| `NackRequested` | Bank A (attempt 1) | |
| `DuplicateSuppressed` | Bank A (attempt 2) | |
| `transfer_refunded` | Transfer Service | |

Outcome: Bank A 100 → 100, Bank B 0 → 0; debit 1 / 1, credit 1 / 0, refund
**2 attempts / 1 effect**; 1 duplicate suppressed.

### Any scenario, unaffordable → `rejected`

`requested`, `debit_rejected` (Bank A, balance unchanged), `transfer_rejected`.
`rejection_reason` is "Insufficient funds". Outcome: Bank A unchanged, Bank B
not involved, no credit or refund issued.

## Execution history

**Steps** are business milestones, reported by the service that observed
them: `requested`, `debit_committed`, `debit_rejected`, `credit_requested`,
`transfer_rejected`, `credit_committed`, `credit_rejected`, `finished`,
`refund_requested`, `refund_committed`, `transfer_refunded`. A bank's outcome
step carries `balance_before` and `balance_after`, read in the transaction
that committed it; a rejection reports its balance unchanged.

**Processing observations** are facts about one handling attempt that never
advance the transfer: `Admitted`, `NackRequested`, `DuplicateSuppressed`,
`CreditConfirmed`, `DeliveryResumed`, `DeliveryPaused`. An observation entry
carries `observation` instead of `step`.

Every entry has `observed_at` (when the reporting service saw it happen, by
that service's clock) and `recorded_at` (when the Transfer Service stored it),
plus the `message_id` of the bank message it records, the `causation_id` of
the message that caused it, and the `issued_message_id` of the command it
sent. A bank's entries carry the `attempt_id` of the handling attempt that
reported them, so two deliveries of one command show as two attempts.
Entries are ordered by `observed_at`, so a late-recorded observation sits
where it happened ([limitations](limitations.md)).

## Visualisation readiness

A transfer is ready once it has a terminal outcome and its history holds the
evidence its scenario needs:

- at once for a `rejected` transfer, a completed happy path and a refunded
  credit rejection, whose every step commits with its status change;
- debit redelivery: Bank A's `NackRequested` from the attempt that committed
  the debit and its `DuplicateSuppressed` from a later attempt, both caused by
  the transfer's `DebitFunds`;
- refund redelivery: the same for the refund and `RefundFunds`;
- Bank B unavailable: `CreditConfirmed` and `DeliveryResumed`
  (`DeliveryPaused` is not required).

Readiness never holds the next submission.

## Outcome summary

Once a transfer is ready, its `outcome` is derived from the history alone:

- `balances.bank_a`: Bank A's `before` from the debit or debit-rejection step
  and its `after` from the last Bank A step that reported a balance.
- `balances.bank_b`: Bank B's pair from the credit or credit-rejection step,
  with `involved` false after a debit rejection.
- `commands.debit`, `commands.credit`, `commands.refund`: the `attempts`
  (distinct handling attempts among the entries the command caused) and the
  `effects` (its committed steps); `null` for a command never issued.
- `duplicates_suppressed`: the `DuplicateSuppressed` observations those
  commands caused; `duplicate_effects`: effects beyond one per command.

## On the transfer page

The transfer page shows the status, the amount, the transfer and trace IDs
with **JSON →** and **Trace →** links, and the history in one lane per
service. While the transfer is pending or its evidence is incomplete, the
page updates every second in place. Once ready it shows the balance change,
the outcome summary under the history, and a playback panel: an animated
diagram (Overview, Detailed or Sequence) that plays the history once from
the start if it became ready while the page was open, and otherwise rests on
the first entry. Each entry stays on screen for its real gap to the next, at
least 700 ms and at most 4 s; the delivery wait's row carries the broker
wait. The visitor can pause, step and replay. Playback only steps through
rendered rows and never calls the API, so it repeats no business operation.

## Around transfers

- **Amounts** are whole numbers of credits written with digits only, greater
  than zero and at most 9223372036854775807. Anything else is refused with
  `400 Bad Request` before a transfer is recorded.
- **Top-up**: **+100 Bank A** adds 100 credits to the visitor's Bank A account
  at once, over Bank A's internal HTTP. It is not part of a Saga, sends no
  message, appears in no history, and is refused while a transfer is pending.
  It is not deduplicated: repeating it adds 100 again.
- **Visitor reset**: **Reset all** starts the browser over as a new visitor
  with fresh 100 / 0 accounts and deletes the old visitor's transfers,
  histories and accounts. It is refused while one of its transfers is pending
  or holds the demonstration slot.
- **Visitor expiry**: a visitor unseen for seven days is deleted the same
  way ([security](security.md#visitor-expiry-and-retention)).
