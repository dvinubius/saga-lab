#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."

services=(transfer-service bank-a bank-b)
trap 'echo "Reset failed; the demonstration state is unreliable until make reset succeeds." >&2' ERR

docker compose up --detach --wait postgres rabbitmq
docker compose stop "${services[@]}"
for service in "${services[@]}"; do
  docker compose run --rm --no-deps "$service" reset
done
docker compose up --detach --wait "${services[@]}"

echo "Reset complete: Bank A holds 100 credits, Bank B 0, and no transfers remain."
