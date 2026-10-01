# Saga Lab

A local demonstration of orchestrated Sagas: a Transfer Service coordinates transfers of fictional credits between two independently owned banks.

Milestone 1 is in progress. One prepared visitor holds an account at each bank, starting with 100 credits at Bank A and 0 at Bank B, and a minimal page shows both balances. Transfers arrive in later increments.

## Requirements

- Docker with Compose v2
- Go 1.27, to run the tests

## Run

```bash
make up
```

This builds the services, starts them with PostgreSQL, and waits until every container reports ready. Open <http://localhost:8080>.

| Service          | Host URL                | Database           |
| ---------------- | ----------------------- | ------------------ |
| Transfer Service | <http://localhost:8080> | `transfer_service` |
| Bank A           | <http://localhost:8081> | `bank_a`           |
| Bank B           | <http://localhost:8082> | `bank_b`           |
| PostgreSQL       | `localhost:5432`        |                    |

HTTP interfaces:

- Transfer Service: `GET /` (page) and `GET /api/balances` (JSON).
- Bank A and Bank B: `GET /accounts/{visitorID}`, for example `/accounts/prepared-visitor`.
- Every service: `GET /readyz`.

Each service connects with its own PostgreSQL role, which can open only that service's database. The Transfer Service obtains balances from the banks' HTTP interfaces.

On startup, each bank creates the prepared visitor's account if it does not exist yet; it never overwrites an existing account. Data lives in the `postgres-data` volume, so `make down` followed by `make up` keeps balances. `docker compose down --volumes` deletes all data.

Override host ports with `POSTGRES_PORT`, `TRANSFER_SERVICE_PORT`, `BANK_A_PORT`, and `BANK_B_PORT`. Follow logs with `make logs` and stop with `make down`.

## Test

```bash
make test
```

This starts a separate Compose project, `saga-lab-test`, with fresh databases on ports 15432 and 18080–18082, runs `go test ./...` against it, and removes it afterwards. It leaves the development stack and its data untouched. Arguments to `scripts/test.sh` are passed to `go test`, for example `scripts/test.sh -run TestPage -v`.

Tests that need the running stack skip when `SAGA_LAB_URL` or `SAGA_LAB_POSTGRES_URL` is not set.
