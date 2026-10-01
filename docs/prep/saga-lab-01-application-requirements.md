# Saga Lab — Application Requirements

**Scope of this document:** what the application must demonstrate and what a visitor should experience. Implementation choices, testing infrastructure, and observability technology belong in the other two documents.

## 1. Purpose and scope

Saga Lab is a small, public, interactive demonstration of **orchestrated Sagas**: a coordinator guides a business operation across independently owned services, each of which commits its own changes. When an operation cannot finish, the coordinator initiates compensating actions rather than rolling back a distributed transaction.

The first and only V1 business example is a transfer of **fictional credits between two independent banks**. The banking scenario makes the correctness requirements easy to understand; it is not the identity of the project. The design should leave room for other orchestrated-Saga demonstrations in the future **without building a generic Saga engine now**.

Two equally important outcomes matter:

- **Correctness:** the business result stays coherent despite repeated deliveries, partial success, and temporary unavailability.
- **Explainability:** a visitor can see what happened and why the outcome is correct, then inspect technical evidence in the observability interface.

The public experience should be small, visually clear, pleasant to watch, and understandable without reading the code.

## 2. Visitor setup and normal interaction

On first visit, the application provides a lightweight visitor identity and automatically generated names for the two fictional banks. The visitor has **one account in each bank**. There are two shared banks, Bank A and Bank B. Each visitor owns a separate account at each bank. Visitor-specific bank display names are presentation data; they do not identify separate banks.

The visitor can:

- Top up the account at Bank A with fictional credits.
- Choose an amount to transfer **from Bank A to Bank B**.
- Choose one of the five predefined demonstration scenarios before starting the transfer.
- See the current/ultimate transfer status and account balances.
- After the operation has run, explore an animated execution timeline and the outcome summary.
- Follow links from the demonstration into relevant technical observability views.

No real money, payment providers, banking APIs, or elaborate account-management product is involved. V1 does not need freely editable failure scripts or arbitrary multi-bank transfers.

## 3. Transfer behavior

Transfer amounts are positive whole numbers of fictional credits. Malformed, fractional, zero, and negative amounts are rejected before starting a Saga.

One coordinator manages the business workflow. The two banks own their respective account balances and commit changes independently; there is no cross-bank atomic transaction.

A successful transfer follows this logical sequence:

1. The visitor requests a transfer, and the coordinator records it.
2. Bank A receives a debit instruction and commits the debit.
3. Bank A reports success to the coordinator.
4. The coordinator instructs Bank B to credit the destination account.
5. Bank B commits the credit and reports success.
6. The coordinator marks the transfer complete.

The application must represent intermediate states, including a requested transfer, pending debit, successful debit, pending credit, pending refund, completion, and failure/refund, without developing an unnecessarily elaborate state machine.

If Bank A rejects a debit because funds are insufficient, neither balance changes. The coordinator records the rejection and ends the transfer without issuing a credit or refund command. This ordinary business outcome is not an additional predefined scenario.

If Bank B **permanently rejects** the credit after Bank A has already debited the source, the coordinator requests a **refund at Bank A**. This is a new business operation, not a rollback of Bank A's committed debit. Once the refund succeeds, the transfer ends in a coherent failed/refunded state.

A temporary technical interruption is **not** a business rejection. A transfer must not be declared failed merely because a service has not responded within a fixed deadline; it remains pending until the relevant operation can proceed or a definitive business outcome is known.

## 4. Correctness guarantees

Messages may be delivered and handled multiple times. The application must distinguish:

- One logical message from multiple attempts to deliver/handle it.
- Multiple handler executions from a single committed business effect.
- A local bank commit from completion of the entire Saga.

Required invariants:

- A given transfer must not debit Bank A more than once for the same debit operation.
- A given transfer must not credit Bank B more than once for the same credit operation.
- Repeated refund instructions must not refund Bank A more than once.
- A completed transfer reflects one debit and one corresponding credit.
- A transfer that has been permanently rejected and successfully compensated restores the source balance; Bank B receives no credit from that rejected operation.
- Repeated delivery of commands or events must not advance the coordinator or change balances incorrectly.
- Repeated delivery attempts remain visible even when the duplicate produces no business effect.

The central idea is **effectively-once business effects on top of at-least-once message delivery**, not a claim that a message is physically delivered or a handler executed exactly once.

## 5. The five predefined scenarios

These are the entire V1 public scenario set. The failure point and expected recovery are predefined, so demonstrations remain repeatable.

### Scenario 1 — Happy path

The debit and credit succeed without injected faults. The visitor sees the coordinator sequencing two independent local operations and arriving at a completed transfer with correct balances.

### Scenario 2 — Debit redelivery and idempotent consumption

Bank A **commits the first debit**, but the first handling attempt does not end in a successful consumer acknowledgement. The same debit command is delivered again. Bank A recognizes it as previously processed, does **not** debit again, and the transfer eventually completes.

**Takeaway:** two deliveries and handler executions can produce one debit, one credit, and zero duplicate business effects.

### Scenario 3 — Permanent credit rejection and compensation

Bank A commits the debit. Bank B permanently rejects the credit. The coordinator requests a refund, Bank A commits it, and the transfer ends failed/refunded.

**Takeaway:** a Saga restores a coherent outcome through a compensating transaction rather than a distributed rollback.

### Scenario 4 — Temporary Bank B consumer unavailability

Delivery to the dedicated scenario-4 Bank B consumer is genuinely interrupted: the credit command waits in the message broker with no consumer for approximately **five seconds**. Consumption resumes and the transfer completes. Bank B continues serving other scenarios. This demonstrates consumer unavailability for this transfer, not termination of the whole Bank B process.

Only one scenario-4 execution runs at a time across visitors. Additional requests wait for admission **before any debit or Saga execution begins**. An uncontended request starts without a special waiting message. A queued visitor sees a brief explanation such as: “Another visitor is trying this demo. Yours will start automatically when it’s your turn.” Do not promise a fixed short wait when several requests are ahead.

The admission wait and the subsequent five-second message wait are separate: the first is waiting for a shared demonstration slot; the second is the actual Saga scenario. Neither changes another visitor’s balances or interrupts their other scenarios.

**Takeaway:** a message **waiting to be delivered** is different from one already delivered but awaiting acknowledgement. During the pause there need not be any failed delivery attempts; the message can simply remain queued.

### Scenario 5 — Refund redelivery and idempotent compensation

As in scenario 3, Bank B permanently rejects the credit and the coordinator requests a refund. Bank A **commits the refund**, but the first handling attempt does not finish with a successful consumer acknowledgement. The same refund command is delivered again; Bank A recognizes the duplicate and does **not** refund twice.

**Takeaway:** compensation is itself an operation requiring duplicate protection.

## 6. Execution timeline and playback

The primary interface should offer an understandable execution narrative, not a replica of Grafana or a raw log viewer.

The actual workflow runs first, at its natural speed. Show progress while it runs. Business completion and **visualisation readiness** are distinct: playback starts only after the business outcome and the evidence required for the selected scenario have been recorded in the application history. For example, a completed transfer may still be waiting for its duplicate-suppression observation to reach that history. Show “Transfer complete — preparing the replay” while collecting that evidence; missing evidence must not turn a successful transfer into a business failure. **Once ready,** the visitor explores the recorded history through:

- Automatic playback at a comfortable viewing speed.
- Step-by-step navigation, including backward/forward steps, pause, and replay.
- A visual indication of which of the coordinator, broker, Bank A, or Bank B is involved in the highlighted step.
- Actual occurrence timestamps and elapsed durations, distinguishable from the slower playback time.
- Playback timing that conveys the relative size of actual delays, while giving very brief steps enough on-screen time to be understood. The five-second queue wait should feel longer than millisecond-scale delivery.

Playback must never re-run the transfer. A fast underlying execution must not be presented as if messages literally spend hundreds of milliseconds crossing the network.

The application should show, where observed, meaningful distinctions between: an outgoing command being recorded, publication attempted, acceptance confirmed by the broker, delivery to a consumer, a committed business effect, and observed acknowledgement/Nack actions. An application request to acknowledge or Nack must not be presented as proof that the broker received it. These stages may be simplified in the main view and expanded in detail. **Broker acceptance is not a separately measured instant of queue insertion.** A publisher confirmation and a consumer acknowledgement are different events.

A timeline must reflect actual observations and causal relationships, not invent missing events or force asynchronous observations into a misleading order. Redelivery should visibly be another attempt for **the same logical message**, not a new debit/refund command.

## 7. Outcome summary and investigation

Each demonstration concludes with a concise, domain-level result: status, before/after balances, and counts relevant to the scenario.

For the canonical debit-redelivery experiment, the visitor should be able to see **two debit-command deliveries, two handling attempts, one debit, one credit, and zero duplicate effects**. For compensation, the visitor should be able to see the rejected credit and the refund restoring the source balance.

The application presents this curated factual timeline and outcome; the observability interface provides the deeper traces, logs, metrics, and broker evidence. Direct links should connect the two. The user should not need to understand Grafana to understand the basic demonstration.

## 8. V1 boundaries and acceptance

Do **not** add a sixth public scenario for outbox publication failures, broker outages, slow-handler timeouts, or process crashes. There is no transfer-completion deadline that reclassifies technical unavailability as a permanent business failure. Keep failure injection predefined, not a general chaos-engineering interface.

V1 is not a real banking product, a generic orchestration engine, a reusable Saga framework, or a workflow DSL. Avoid real payments, authentication complexity, fees, currency exchange, settlement, event sourcing, and complex UI functionality.

The application succeeds when a visitor can **follow an orchestrated transfer, witness duplicate delivery without duplicate effects, observe compensation and duplicate-safe refunding, distinguish queue waiting from processing, and investigate the recorded evidence** without reading the source code.
