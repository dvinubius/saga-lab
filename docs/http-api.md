# HTTP API

The Transfer Service serves the pages and the JSON API on one origin:
<https://saga.dinubarbu.com> in public, <http://localhost:8080> locally. The
banks' HTTP interfaces are internal to the stack. [Scenarios](scenarios.md)
explains the statuses, history entries and outcome summary these routes
return.

## Visitor cookie

Every route except `/readyz` and `/static/` belongs to a visitor, identified
by the `saga_lab_visitor` cookie (`HttpOnly`, `SameSite=Lax`, `Path=/`, one
year, `Secure` in production). A request without a valid cookie becomes a new
visitor: the response sets a fresh cookie, and the visitor's accounts are
opened at both banks before the request is handled. API clients must keep and
resend the cookie, for example with curl's `-c cookies.txt -b cookies.txt`;
without it every request is a new visitor. See
[security](security.md#visitor-token) for the token.

If a bank cannot be reached while opening a new visitor's accounts, the
request answers `502 Bad Gateway` (`{"error":"balances unavailable"}` under
`/api/`), and the next request tries again.

## JSON API

Errors are JSON objects with an `error` message.

### `GET /api/balances`

`200 OK`:

```json
{"bank_a":{"balance":100},"bank_b":{"balance":0}}
```

`502 Bad Gateway` when a bank is unavailable.

### `POST /api/top-ups`

No body. Adds 100 credits to the visitor's Bank A account and answers `200 OK`
with both balances, as above. While a transfer is pending it answers
`409 Conflict` and adds nothing:

```json
{"error":"a transfer is still pending; top up once it has finished","pending_transfer_id":"<id>"}
```

### `POST /api/transfers`

Body, at most 1 KB, one JSON object:

```json
{"amount": 25, "scenario": "debit_redelivery"}
```

`amount` is a JSON integer greater than zero. `scenario` is optional and one of
`happy_path` (the default), `debit_redelivery`, `credit_rejection`,
`bank_b_unavailable` or `refund_redelivery`.

| Status | When |
| --- | --- |
| `202 Accepted` | started; `Location: /api/transfers/{transferID}`, body the transfer as `GET` returns it |
| `400 Bad Request` | not one JSON object with an amount, an invalid amount, or an unknown scenario (the error lists the accepted values) |
| `409 Conflict` | another transfer of this visitor is pending; nothing started; body has `pending_transfer_id` |
| `503 Service Unavailable` | a `bank_b_unavailable` submission met the admission limit; `Retry-After: 60`; nothing created or debited |

The `503` error reads "the Bank B unavailable demo has reached its admission
limit; try again in a minute or pick another scenario".

### `GET /api/transfers`

The visitor's transfers, newest first:

```json
{"transfers":[{"transfer_id":"…","amount":25,"scenario":"happy_path","status":"completed","requested_at":"…"}]}
```

### `GET /api/transfers/{transferID}`

One of the visitor's transfers; `404 Not Found` for an unknown ID or another
visitor's transfer.

```json
{
  "transfer_id": "…",
  "amount": 25,
  "scenario": "credit_rejection",
  "status": "refunded",
  "requested_at": "…",
  "rejection_reason": "Credit refused by Bank B",
  "trace_id": "…",
  "history": [
    {"step": "requested", "service": "Transfer Service", "observed_at": "…", "recorded_at": "…", "issued_message_id": "…"},
    {"step": "debit_committed", "service": "Bank A", "attempt_id": "…", "observed_at": "…", "recorded_at": "…",
     "message_id": "…", "causation_id": "…", "balance_before": 100, "balance_after": 75}
  ],
  "visualisation_ready": true,
  "outcome": {
    "balances": {"bank_a": {"before": 100, "after": 100}, "bank_b": {"before": 0, "after": 0, "involved": true}},
    "commands": {"debit": {"attempts": 1, "effects": 1}, "credit": {"attempts": 1, "effects": 0}, "refund": {"attempts": 1, "effects": 1}},
    "duplicates_suppressed": 0,
    "duplicate_effects": 0
  }
}
```

- `status`: `awaiting_admission`, `debit_pending`, `credit_pending` or
  `refund_pending` while pending; `completed`, `rejected` or `refunded` at the
  end.
- `rejection_reason`: for `rejected` and refunded transfers.
- `trace_id`: absent when tracing is off.
- `history`: steps and processing observations ordered by `observed_at`;
  optional fields are omitted when empty.
- `visualisation_ready`: computed on every read; `outcome` is present only
  when it is true.

## Pages

Pages answer HTML with `Cache-Control: no-store`. Forms post
`application/x-www-form-urlencoded`.

| Route | Behavior |
| --- | --- |
| `GET /` | introduction, transfer form, balances, top-up and reset buttons, and the visitor's transfers, newest first. While a transfer is pending the form and buttons are disabled and the pending transfer is linked |
| `POST /transfers` | fields `amount` and `scenario`; `303 See Other` to the transfer page, or the home page again with the error and the amount and scenario kept: `400` invalid input, `409` another transfer pending, `503` with `Retry-After: 60` at the admission limit |
| `GET /transfers/{transferID}` | the transfer page; `404` for an unknown or another visitor's transfer |
| `POST /top-ups` | **+100 Bank A**; `303` to `/`, or `409` with the home page while a transfer is pending |
| `POST /reset` | **Reset all** (visitor reset): `303` to `/` with a new visitor cookie, or `409` with the home page while a transfer is pending or holds the demonstration slot |

## Service routes

| Route | Service | Behavior |
| --- | --- | --- |
| `GET /readyz` | all three | `200` `ready` once the database and broker are reachable, else `503` |
| `GET /static/…` | Transfer Service | styles, scripts, fonts (immutable cache) and images |

The banks' routes are internal: the Transfer Service calls them over the
Compose network, and they are not proxied in public. Locally they are on
`127.0.0.1:8081` (Bank A) and `127.0.0.1:8082` (Bank B).

| Route | Behavior |
| --- | --- |
| `GET /accounts/{visitorID}` | `{"visitor_id":"…","balance":75}`, or `404` |
| `PUT /accounts/{visitorID}` | opens the account with the bank's opening balance (Bank A 100, Bank B 0); idempotent, never overwrites a balance; `204` |
| `DELETE /accounts/{visitorID}` | closes the account; idempotent; `204` |
| `POST /accounts/{visitorID}/top-ups` | Bank A only: adds 100 and returns the account with `"amount":100`; `404` for a missing account |
