# Milestone 3 — Agreed scope

Build specification: [GitHub issue #42 — Compensation, including duplicate-safe refunds](https://github.com/dvinubius/saga-lab/issues/42).

These decisions refine milestone 3. Everything in the milestone 3 entry in [Milestones](../sagas-04-milestones.md) is in scope.

- **Scenarios:** the home form adds Credit rejection and Refund redelivery after Happy path and Debit redelivery. In both, Bank B rejects the credit after Bank A's debit and Bank A refunds. Under credit rejection the refund command is delivered once. Under refund redelivery Bank A commits the refund, then fails once before acknowledging, and recognises the redelivered command. Both end refunded, with the source balance restored and no credit at Bank B. Insufficient funds still ends in an ordinary debit rejection.
- **Credit rejection:** the rejection is a business outcome the scenario selects, not an injected fault. It concerns one credit operation, not the account. Bank B gives the reason "Credit refused by Bank B", and it is shown like a debit rejection's reason. The scenario travels in every bank command, so each bank acts only on its exact scenario.
- **Refund:** unconditional. A refund cannot be rejected. Its redelivery fault mirrors the debit's and uses the same processing observations.
- **States and history:** a credit rejection moves the transfer to pending refund, and the refund ends it refunded. A transfer pending refund holds the pending-transfer restriction. History adds credit rejected, refund requested, refund committed and transfer refunded. Short notes explain that the refund is a new operation, not a rollback, and, after a lost refund acknowledgement, why the transfer still ends refunded.
- **Readiness:** a credit-rejection transfer is ready once refunded. A refund-redelivery transfer also needs the redelivery request from the attempt that committed the refund and a suppressed duplicate from a later attempt, both caused by the refund command.
- **Demonstration:** at the milestone 2 level: plain history, the Evidence row and current balances. Before/after balances and outcome counts remain in milestone 5.
- **Primary test boundary:** both scenarios are proven over HTTP against the running stack with balances, steps and, for refund redelivery, observation identities. Duplicate credit commands after a rejection, duplicate coordinator events and local atomicity of the refund are proven by package-level tests against real PostgreSQL. No first-failing unsafe test and no additional trace check.
