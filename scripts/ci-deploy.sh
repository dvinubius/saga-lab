#!/usr/bin/env bash

set -euo pipefail

project_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
cd "$project_dir"

remote_dir=/opt/saga-lab
image_repository=ghcr.io/dvinubius/saga-lab

die() {
	printf '%s\n' "$*" >&2
	exit 1
}

require_target() {
	[[ $1 =~ ^[0-9a-f]{40}$ ]] || die 'Target must be a full commit SHA.'
	git cat-file -e "$1^{commit}" 2>/dev/null || die "Target commit is not available: $1"
}

require_main_head() {
	local head
	head=$(git ls-remote --exit-code origin refs/heads/main | cut -f1) || die 'Could not read the head of main.'
	[[ $head == "$1" ]] || die "Stale run: main is at $head, not $1."
}

ssh_options=()
require_ssh() {
	[[ -n ${DEPLOY_HOST:-} && -n ${DEPLOY_USER:-} ]] || die 'DEPLOY_HOST and DEPLOY_USER are required.'
	[[ -s ${DEPLOY_SSH_KEY_FILE:-} ]] || die 'DEPLOY_SSH_KEY_FILE must name the deployment key.'
	[[ -s ${DEPLOY_KNOWN_HOSTS_FILE:-} ]] || die 'DEPLOY_KNOWN_HOSTS_FILE must hold the verified host key.'
	ssh_options=(
		-i "$DEPLOY_SSH_KEY_FILE"
		-o BatchMode=yes
		-o ConnectTimeout=15
		-o IdentitiesOnly=yes
		-o StrictHostKeyChecking=yes
		-o UserKnownHostsFile="$DEPLOY_KNOWN_HOSTS_FILE"
	)
}

remote() {
	ssh "${ssh_options[@]}" "$DEPLOY_USER@$DEPLOY_HOST" "$@"
}

plan() {
	local target=$1 force=${2:-} manifest deployed
	require_target "$target"
	require_ssh
	manifest=$(remote "cat '$remote_dir/.deploy/manifest' 2>/dev/null || true") ||
		die 'Could not read the deployment manifest over SSH.'
	deployed=$(sed -n 's/^commit=//p' <<<"$manifest" | tail -n 1)
	printf 'Last verified deployment: %s\n' "${deployed:-none}" >&2
	if [[ $force == full ]]; then
		printf '%s\n' full
		return
	fi
	bash scripts/classify-deploy.sh "$deployed" "$target"
}

build_bundle() {
	git archive --format=tar "$1" -- compose.yaml compose.production.yaml deploy \
		scripts/compose.sh scripts/remote-deploy.sh scripts/smoke.sh
}

deploy() {
	local target=$1 image=$2 staging command
	[[ $image =~ ^${image_repository//./\\.}@sha256:[0-9a-f]{64}$ ]] ||
		die "Image must be $image_repository@sha256:<digest>."
	require_target "$target"
	require_ssh
	require_main_head "$target"

	staging=$remote_dir/.deploy/staging/$target
	build_bundle "$target" |
		remote "rm -rf '$staging' && mkdir -p '$staging' && tar -x -C '$staging'"

	command="bash '$staging/scripts/remote-deploy.sh' full $target $image; status=\$?; rm -rf '$staging'; exit \$status"
	if [[ -n ${GHCR_USER:-} && -n ${GHCR_PULL_TOKEN:-} ]]; then
		[[ $GHCR_USER =~ ^[A-Za-z0-9-]+(\[bot\])?$ ]] || die 'GHCR_USER is not a GitHub login.'
		remote "GHCR_USER='$GHCR_USER' $command" <<<"$GHCR_PULL_TOKEN"
	else
		remote "$command" </dev/null
	fi
}

case ${1:-} in
plan)
	[[ $# -ge 2 && $# -le 3 ]] || die 'Usage: ci-deploy.sh plan <target-sha> [full]'
	plan "$2" "${3:-}"
	;;
deploy)
	[[ $# -eq 3 ]] || die 'Usage: ci-deploy.sh deploy <target-sha> <image>'
	deploy "$2" "$3"
	;;
*) die 'Usage: ci-deploy.sh plan <target-sha> [full] | deploy <target-sha> <image>' ;;
esac
