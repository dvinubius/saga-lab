# Saga Lab

Language for a demonstration of orchestrated transfers of fictional credits between two independent banks.

## Language

**Visitor**:
A person using the demonstration, associated with one account at each bank.
_Avoid_: Tenant

**Bank**:
One of the two shared institutions, Bank A or Bank B, that owns its accounts and their balances. A visitor-specific display name does not create another bank.
_Avoid_: Virtual bank, per-visitor bank

**Account**:
A visitor’s holding of fictional credits at one bank. Each visitor owns one account at Bank A and one at Bank B.

**Credit**:
The indivisible fictional unit of value used in the demonstration; amounts are whole numbers and have no real monetary value.

**Transfer**:
An attempt to move a chosen amount of credits from a visitor’s Bank A account to that visitor’s Bank B account, coordinated through independent local operations.

**Pending-transfer restriction**:
A visitor has at most one transfer pending at a time; a submission made while one is pending starts nothing and names the pending transfer. It is not request deduplication: a repeated submission after the pending transfer has ended starts a new transfer.
_Avoid_: Idempotency, deduplication

**Debit**:
A reduction of the source account’s balance for a transfer.

**Debit rejection**:
A definitive refusal by Bank A to debit an account, such as when funds are insufficient. It leaves balances unchanged and ends the transfer without a destination credit or refund.

**Credit operation**:
An increase of the destination account’s balance for a transfer.

**Refund**:
A compensating operation that restores the source amount after a debit when the destination credit is permanently rejected.
_Avoid_: Rollback

**Business outcome**:
The transfer’s result, distinct from whether all evidence needed to explain that result has been collected.

**Scenario**:
One of the five predefined demonstrations, specifying the intended execution conditions and the behavior to explain.

**Admission wait**:
The wait for exclusive use of the scenario-4 demonstration slot, before the transfer begins.

**Execution history**:
The recorded steps and processing observations associated with a transfer.

**Step**:
One business milestone in a transfer's execution history, reported by the service that observed it: requested, debit committed, debit rejected, credit committed, or finished.
_Avoid_: Milestone, event

**Issued message**:
The command a step sends to a bank, such as the debit instruction sent when a transfer is requested. The bank's reply names the issued message as its cause, which links consecutive steps.

**Duplicate delivery**:
A delivery of a message the receiving service has already processed, whatever the cause: redelivery after a failed acknowledgement, or a repeated publication of the same outgoing message. It is acknowledged without repeating any business effect.
_Avoid_: Retry

**Visualisation readiness**:
The condition in which a transfer has reached a terminal business outcome and its history contains the evidence required to explain the selected scenario.

**Playback**:
A read-only presentation of a transfer’s recorded execution history.
_Avoid_: Re-execution
