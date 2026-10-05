#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."

run="$(od -An -N4 -tx1 /dev/urandom | tr -d ' \n')"
export COMPOSE_PROJECT_NAME="sagas-test-$run"
export POSTGRES_PORT=
export RABBITMQ_PORT=
export RABBITMQ_MANAGEMENT_PORT=
export SAGAS_ACCEPTANCE_PREFIX="sagas-acceptance-$run-"

remove_acceptance_projects() {
  local project
  for project in $(docker compose ls --all --quiet); do
    if [[ "$project" == "$SAGAS_ACCEPTANCE_PREFIX"* ]]; then
      docker compose --project-name "$project" down --timeout 0 --volumes --remove-orphans
    fi
  done
}

teardown() {
  local status=$?
  if [ "$status" -ne 0 ]; then
    docker compose logs --no-color --tail=100
  fi
  docker compose down --timeout 0 --volumes --remove-orphans
  remove_acceptance_projects
  exit "$status"
}

short=false
for argument in "$@"; do
  if [ "$argument" = -short ]; then
    short=true
  fi
done

trap teardown EXIT
echo "Test run $run"
if [ "$short" = true ]; then
  pull=never
else
  docker compose build
  docker compose pull --quiet --policy missing postgres rabbitmq otel-collector tempo grafana
  pull=missing
fi
docker compose --file compose.yaml --file compose.test.yaml up --pull "$pull" --detach --wait postgres rabbitmq
postgres_address="$(docker compose port postgres 5432)"
rabbitmq_address="$(docker compose port rabbitmq 5672)"
management_url="http://sagas:sagas@$(docker compose port rabbitmq 15672)"
deadline=$((SECONDS + 30))
until curl --silent --fail --output /dev/null "$management_url/api/overview"; do
  if ((SECONDS > deadline)); then
    echo "RabbitMQ management API is not answering" >&2
    exit 1
  fi
  sleep 0.1
done

SAGAS_POSTGRES_URL="postgres://postgres:postgres@${postgres_address}/postgres?sslmode=disable" \
SAGAS_AMQP_URL="amqp://sagas:sagas@${rabbitmq_address}/" \
SAGAS_RABBITMQ_MANAGEMENT_URL="$management_url" \
  go test -count=1 -parallel 4 ./... "$@"
