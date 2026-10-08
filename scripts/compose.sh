#!/usr/bin/env bash

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
