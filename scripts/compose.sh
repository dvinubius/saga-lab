#!/usr/bin/env bash

# Run Docker Compose for the deployed Saga Lab project with its VPS-owned
# secrets (.env) and the pinned published image (.env.image), so a reboot or a
# manual `up` uses the deployed image.
#
# Usage: scripts/compose.sh <compose arguments...>

set -euo pipefail

cd "$(dirname -- "${BASH_SOURCE[0]}")/.."

for file in .env .env.image; do
	[[ -f $file ]] || {
		printf '%s is missing in %s.\n' "$file" "$PWD" >&2
		exit 1
	}
done
exec docker compose --env-file .env --env-file .env.image \
	--file compose.yaml --file compose.production.yaml "$@"
