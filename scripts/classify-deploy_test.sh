#!/usr/bin/env bash

set -euo pipefail

classifier="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)/classify-deploy.sh"
repo=$(mktemp -d)
trap 'rm -rf "$repo"' EXIT

cd "$repo"
git init -q -b main
git config user.email test@example.invalid
git config user.name test
git config commit.gpgsign false

write() {
	mkdir -p "$(dirname -- "$1")"
	printf '%s\n' "${2:-$RANDOM}" >"$1"
}

commit() {
	git add -A
	git commit -q --allow-empty -m change
	git rev-parse HEAD
}

write cmd/transfer-service/main.go
write internal/bank/bank.go
write compose.yaml
write compose.production.yaml
write deploy/tempo/tempo.yaml
write README.md
write docs/architecture.md
write scripts/smoke.sh
base=$(commit)

failures=0
expect() {
	local description=$1 expected=$2 deployed=$3 target=$4 actual
	actual=$(bash "$classifier" "$deployed" "$target" 2>/dev/null) || actual="exit $?"
	if [[ $actual != "$expected" ]]; then
		printf 'FAIL: %s: expected %s, got %s\n' "$description" "$expected" "$actual" >&2
		failures=$((failures + 1))
	fi
}

case_from_base() {
	git checkout -q --detach "$base"
}

case_from_base
write README.md
write docs/architecture.md
write docs/prep/milestones/milestone-8.md
write AGENTS.md
write CLAUDE.md
write GLOSSARY.md
write LICENSE
write .agents/notes.md
write .gitignore
write .ignore
write .env.example
write Makefile
expect 'docs, agent notes, Makefile and .env.example' none "$base" "$(commit)"

case_from_base
write internal/bank/bank_test.go
write cmd/transfer-service/main_test.go
write acceptance/scenarios_test.go
write scripts/remote-deploy_test.sh
write scripts/test.sh
write compose.test.yaml
expect 'tests only' none "$base" "$(commit)"

case_from_base
write compose.yaml
expect 'base compose' full "$base" "$(commit)"

case_from_base
write compose.production.yaml
expect 'production compose' full "$base" "$(commit)"

case_from_base
write deploy/grafana/dashboards/trace.json
expect 'deployment configuration' full "$base" "$(commit)"

case_from_base
write internal/bank/bank.go
write README.md
expect 'application change with docs' full "$base" "$(commit)"

case_from_base
write Dockerfile
expect 'Dockerfile' full "$base" "$(commit)"

case_from_base
write scripts/smoke.sh
expect 'operational script' full "$base" "$(commit)"

case_from_base
write .github/workflows/deploy.yml
expect 'workflow' full "$base" "$(commit)"

case_from_base
write tools/new-operational-file.sh
expect 'unknown path' full "$base" "$(commit)"

case_from_base
write main_test.go
expect 'test outside the Go packages is not assumed harmless' full "$base" "$(commit)"

case_from_base
git rm -q internal/bank/bank.go
expect 'deleted application file' full "$base" "$(commit)"

case_from_base
git mv docs/architecture.md deploy/architecture.md
expect 'rename into a deployed path counts the new path' full "$base" "$(commit)"

case_from_base
git mv deploy/tempo/tempo.yaml docs/tempo.yaml
expect 'rename out of a deployed path counts the old path' full "$base" "$(commit)"

case_from_base
write internal/bank/bank.go
commit >/dev/null
write README.md
expect 'cumulative since a failed deployment' full "$base" "$(commit)"

case_from_base
expect 'target equals deployment' none "$base" "$base"

target=$(git rev-parse HEAD)
expect 'first deployment' full '' "$target"
expect 'malformed baseline' full 'not-a-sha' "$target"
expect 'unknown baseline' full "$(printf '%040d' 0)" "$target"

git checkout -q --orphan unrelated
git rm -q -r --cached . >/dev/null
write README.md
unrelated=$(commit)
expect 'baseline is not an ancestor' full "$unrelated" "$target"

expect 'unknown target stops' 'exit 2' "$base" "$(printf '%040d' 1)"
expect 'abbreviated target stops' 'exit 2' "$base" "${target:0:12}"

if ((failures > 0)); then
	exit 1
fi
printf '%s\n' 'deployment classifier tests passed'
