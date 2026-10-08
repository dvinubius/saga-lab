#!/usr/bin/env bash

set -euo pipefail

die() {
	printf '%s\n' "$*" >&2
	exit 2
}

[[ $# -eq 2 ]] || die 'Usage: classify-deploy.sh <deployed-sha-or-empty> <target-sha>'
deployed=$1
target=$2

[[ $target =~ ^[0-9a-f]{40}$ ]] || die 'Target must be a full commit SHA.'
git cat-file -e "$target^{commit}" 2>/dev/null || die "Target commit is not available: $target"

if [[ ! $deployed =~ ^[0-9a-f]{40}$ ]] ||
	! git cat-file -e "$deployed^{commit}" 2>/dev/null ||
	! git merge-base --is-ancestor "$deployed" "$target" 2>/dev/null; then
	printf '%s\n' full
	exit 0
fi

changed=$(git diff --name-only --no-renames "$deployed" "$target") || die 'git diff failed.'

while IFS= read -r path; do
	[[ -n $path ]] || continue
	case $path in
	docs/* | .agents/*) ;;
	README.md | AGENTS.md | CLAUDE.md | GLOSSARY.md | LICENSE) ;;
	.gitignore | .ignore | .env.example | Makefile) ;;
	cmd/*_test.go | internal/*_test.go | acceptance/*) ;;
	scripts/*_test.sh | scripts/test.sh | compose.test.yaml) ;;
	*)
		printf '%s\n' full
		exit 0
		;;
	esac
done <<<"$changed"

printf '%s\n' none
