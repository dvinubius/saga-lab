#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."

export COMPOSE_PROJECT_NAME=saga-lab-test
export POSTGRES_PORT="${TEST_POSTGRES_PORT:-15432}"
export TRANSFER_SERVICE_PORT="${TEST_TRANSFER_SERVICE_PORT:-18080}"
export BANK_A_PORT="${TEST_BANK_A_PORT:-18081}"
export BANK_B_PORT="${TEST_BANK_B_PORT:-18082}"

teardown() {
  local status=$?
  if [ "$status" -ne 0 ]; then
    docker compose logs --no-color --tail=100
  fi
  docker compose down --volumes --remove-orphans
  exit "$status"
}

docker compose down --volumes --remove-orphans
trap teardown EXIT
docker compose up --detach --build --wait

SAGA_LAB_URL="http://127.0.0.1:${TRANSFER_SERVICE_PORT}" \
SAGA_LAB_POSTGRES_URL="postgres://postgres:postgres@127.0.0.1:${POSTGRES_PORT}/postgres?sslmode=disable" \
  go test -count=1 ./... "$@"
