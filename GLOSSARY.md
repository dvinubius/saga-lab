# Saga Lab

Language for a demonstration of orchestrated transfers of fictional credits between two independent banks.

## Language

**Visitor**:
A person using the demonstration, associated with one account at each bank.
_Avoid_: Tenant

**Bank**:
One of the two shared institutions, Bank A or Bank B, that owns its accounts and their balances.
_Avoid_: Virtual bank, per-visitor bank

**Account**:
A visitor’s holding of fictional credits at one bank. Each visitor owns one account at Bank A and one at Bank B.

**Credit**:
The indivisible fictional unit of value used in the demonstration; amounts are whole numbers and have no real monetary value.

**Top-up**:
An addition of a fixed number of fictional credits to a visitor's Bank A account, outside any transfer. It is not part of a Saga and leaves no execution history.
_Avoid_: Deposit

**Visitor reset**:
A visitor's request to start over: the browser becomes a new visitor with fresh accounts, and the old visitor's accounts, transfers and execution histories are deleted. It is refused while one of the visitor's transfers is pending or holds the demonstration slot. Observability data is left to expire.
_Avoid_: Demo reset (the operator's wipe of every visitor)

**Visitor expiry**:
The deletion of a visitor unseen for seven days, with the same effect as a visitor reset: a returning browser starts over with fresh accounts. A visitor whose transfer is pending or holds the demonstration slot does not expire.

**Transfer retention**:
Completed, rejected and refunded transfers disappear seven days after their request time, along with their execution histories. Account balances remain. A pending transfer or demonstration-slot holder is kept until it can be safely removed.

**Transfer**:
An attempt to move a chosen amount of credits from a visitor’s Bank A account to that visitor’s Bank B account, coordinated through independent local operations. It exists from submission and begins with its debit, which may first wait for admission.

**Pending-transfer restriction**:
A visitor has at most one transfer pending at a time; a submission made while one is pending starts nothing and names the pending transfer, and a top-up or visitor reset made then is refused. It is not request deduplication: a repeated submission after the pending transfer has ended starts a new transfer.
_Avoid_: Idempotency, deduplication

**Debit**:
A reduction of the source account’s balance for a transfer.

**Debit rejection**:
A definitive refusal by Bank A to debit an account, such as when funds are insufficient. It leaves balances unchanged and ends the transfer without a destination credit or refund.

**Credit operation**:
An increase of the destination account’s balance for a transfer.

**Credit rejection**:
A definitive refusal by Bank B to credit the destination account after Bank A has debited the source. It concerns that one credit operation, not the account, and leads to a refund.
_Avoid_: Credit failure (a temporary failure to credit is not a rejection)

**Refund**:
A compensating operation that restores the source amount after a debit when the destination credit is permanently rejected. A refund cannot itself be rejected.
_Avoid_: Rollback

**Business outcome**:
The transfer’s result, distinct from whether all evidence needed to explain that result has been collected.

**Outcome summary**:
A transfer's concluding account: its business outcome, each account's balance before and after it as reported by its bank, and, per command, the handling attempts and committed business effects.

**Duplicate effect**:
A business effect committed more than once for the same command. The demonstration counts them to show there are none.
_Avoid_: Duplicate delivery (a delivery can be duplicate without a duplicate effect)

**Scenario**:
One of the five predefined demonstrations, specifying the intended execution conditions and the behavior to explain. It is selected before a transfer starts and stays fixed for that transfer.

**Bank B unavailability**:
The scenario in which delivery of one transfer's credit command to Bank B pauses for a few seconds while the command waits in the broker. Bank B keeps serving every other transfer.
_Avoid_: Bank B outage, Bank B crash

**Demonstration slot**:
The exclusive right, shared by all visitors, to run a Bank B unavailability transfer; one transfer holds it at a time.

**Admission wait**:
A submitted transfer's wait for the demonstration slot, before the transfer begins.

**Admission limit**:
The most transfers that may await admission at once: five, not counting the one holding the demonstration slot. A Bank B unavailability submission beyond it is refused and creates no transfer.
_Avoid_: Queue limit

**Delivery wait**:
In Bank B unavailability, the few seconds during which the transfer's credit command waits in the broker with no consumer for it. Distinct from the admission wait, which precedes the transfer.
_Avoid_: Queue wait, Bank B downtime

**Execution history**:
The recorded steps and processing observations associated with a transfer.

**Step**:
One business milestone in a transfer's execution history, reported by the service that observed it: requested, debit committed, credit requested, debit rejected, transfer rejected, credit committed, finished, credit rejected, refund requested, refund committed, or transfer refunded. The Transfer Service reports the steps that react to a bank's outcome — credit requested, transfer rejected, finished, refund requested, transfer refunded — each naming that outcome as its cause.
_Avoid_: Milestone, event

**Issued message**:
The command a step sends to a bank, such as the debit instruction sent when a transfer is requested. The bank's reply names the issued message as its cause, which links consecutive steps.

**Delivery**:
One handover of a message by the broker to the receiving service. A redelivery hands over the same message again, so its deliveries share the message's identity and are told apart by their handling attempts.
_Avoid_: Message (for a second delivery)

**Duplicate delivery**:
A delivery of a message the receiving service has already processed, whatever the cause: redelivery after a failed acknowledgement, or a repeated publication of the same outgoing message. It is acknowledged without repeating any business effect.
_Avoid_: Retry

**Handling attempt**:
One processing of one delivery of a message by the receiving service. A message can have several handling attempts; at most one of them commits its business effect.
_Avoid_: Retry, delivery

**Redelivery**:
The broker delivering a message again because the receiving service did not acknowledge it. It is one cause of a duplicate delivery.
_Avoid_: Retry, republish

**Lost acknowledgement (simulated)**:
The fault the redelivery scenarios demonstrate: the receiving service commits its work, but its acknowledgement never reaches the broker, as after a crash right after the commit. The demonstration stands in for the crash by requesting a redelivery.
_Avoid_: Publish failure, failure after commit

**Processing observation**:
A recorded fact about one handling attempt, such as a requested redelivery or a suppressed duplicate. It belongs to a transfer's execution history but never advances the transfer.
_Avoid_: Event, step

**Visualisation readiness**:
The condition in which a transfer has reached a terminal business outcome and its history contains the evidence required to explain the selected scenario.

**Playback**:
A read-only presentation of a transfer’s recorded execution history.
_Avoid_: Re-execution
