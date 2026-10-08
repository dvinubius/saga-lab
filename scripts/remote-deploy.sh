#!/usr/bin/env bash

set -euo pipefail

bundle_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
live_dir=${SAGA_LAB_DIR:-/opt/saga-lab}
state_dir=$live_dir/.deploy
check_attempts=${DEPLOY_CHECK_ATTEMPTS:-30}
public_url=https://saga.dinubarbu.com
required_free_bytes=15000000000
keep_snapshots=5
entries=(compose.yaml compose.production.yaml deploy scripts)

die() {
	printf '%s\n' "$*" >&2
	exit 1
}

log() {
	printf '==> %s\n' "$*"
}

usage() {
	die 'Usage: remote-deploy.sh full <commit> <image> | rollback <snapshot>'
}

mode=${1:-}
commit=
image=
rollback_from=
case $mode in
full)
	[[ $# -eq 3 ]] || usage
	commit=$2
	image=$3
	[[ $commit =~ ^[0-9a-f]{40}$ ]] || die 'Commit must be a full 40-character SHA.'
	[[ $image =~ ^ghcr\.io/dvinubius/saga-lab@sha256:[0-9a-f]{64}$ ]] ||
		die 'Image must be ghcr.io/dvinubius/saga-lab@sha256:<64 hex digits>.'
	;;
rollback)
	[[ $# -eq 2 ]] || usage
	rollback_from=$2
	;;
*) usage ;;
esac

ghcr_token=
if [[ $mode == full && -n ${GHCR_USER:-} ]]; then
	IFS= read -r ghcr_token || true
	[[ -n $ghcr_token ]] || die 'GHCR_USER is set, but no registry token arrived on stdin.'
fi

[[ -f $live_dir/.env ]] || die "$live_dir/.env is missing; write it from .env.example first."

mkdir -p "$state_dir/snapshots"
chmod 700 "$state_dir"
exec 9>"$state_dir/lock"
flock -n 9 || die 'Another Saga Lab deployment holds the lock.'

if [[ $mode == rollback ]]; then
	[[ $rollback_from =~ ^[0-9A-Za-z-]+$ && -f $state_dir/snapshots/$rollback_from/.env.image ]] ||
		die "Unknown snapshot or one without a deployment: $rollback_from (see $state_dir/snapshots)."
fi

live_compose() {
	(cd "$live_dir" && docker compose --env-file .env --env-file .env.image \
		--file compose.yaml --file compose.production.yaml "$@")
}

check_headroom() {
	local root available_kib
	root=$(docker info --format '{{.DockerRootDir}}')
	[[ -n $root && -d $root ]] || die "Docker data root is not accessible: $root"
	available_kib=$(df -Pk "$root" | awk 'NR == 2 { print $4 }')
	[[ $available_kib =~ ^[0-9]+$ ]] || die "Could not determine free space on $root."
	((available_kib * 1024 >= required_free_bytes)) ||
		die "Insufficient Docker filesystem space: have $((available_kib * 1024)) bytes, need $required_free_bytes."
	log "Docker filesystem headroom: $((available_kib * 1024)) bytes free."
}

validate_bundle() {
	local entry
	for entry in "$bundle_dir"/* "$bundle_dir"/.[!.]*; do
		[[ -e $entry ]] || continue
		case ${entry##*/} in
		compose.yaml | compose.production.yaml | deploy | scripts) ;;
		*) die "Unexpected bundle entry: ${entry##*/}" ;;
		esac
	done
	for entry in compose.yaml compose.production.yaml deploy \
		scripts/compose.sh scripts/remote-deploy.sh scripts/smoke.sh; do
		[[ -e $bundle_dir/$entry ]] || die "Bundle is missing $entry."
	done
	SAGA_LAB_IMAGE=$image docker compose --env-file "$live_dir/.env" --project-directory "$bundle_dir" \
		--file "$bundle_dir/compose.yaml" --file "$bundle_dir/compose.production.yaml" config --quiet ||
		die 'The bundled Compose files do not render with the live environment.'
}

pull_image() {
	if [[ -z $ghcr_token ]]; then
		docker pull --quiet "$image" >/dev/null
		return
	fi
	local docker_config status=0
	docker_config=$(mktemp -d)
	printf '%s\n' "$ghcr_token" |
		DOCKER_CONFIG=$docker_config docker login ghcr.io --username "$GHCR_USER" --password-stdin >/dev/null &&
		DOCKER_CONFIG=$docker_config docker pull --quiet "$image" >/dev/null || status=$?
	rm -rf "$docker_config"
	return $status
}

pull_stack() {
	local source_dir=$1 stack_image=$2
	SAGA_LAB_IMAGE=$stack_image docker compose --env-file "$live_dir/.env" --project-directory "$source_dir" \
		--file "$source_dir/compose.yaml" --file "$source_dir/compose.production.yaml" pull --quiet --policy missing
	check_headroom
}

snapshot_dir=
take_snapshot() {
	local entry
	snapshot_dir=$state_dir/snapshots/$(date -u +%Y%m%dT%H%M%SZ)-$mode
	[[ ! -e $snapshot_dir ]] || die "Snapshot already exists: $snapshot_dir"
	mkdir -p "$snapshot_dir"
	for entry in "${entries[@]}" .env.image; do
		if [[ -e $live_dir/$entry ]]; then
			cp -a "$live_dir/$entry" "$snapshot_dir/"
		fi
	done
	if [[ -f $state_dir/manifest ]]; then
		cp -a "$state_dir/manifest" "$snapshot_dir/manifest"
	fi
	log "Saved snapshot ${snapshot_dir##*/}."
}

prune_snapshots() {
	local snapshots=() name remaining
	while IFS= read -r name; do
		snapshots+=("$name")
	done < <(ls -1 "$state_dir/snapshots" | sort)
	remaining=${#snapshots[@]}
	for name in "${snapshots[@]}"; do
		((remaining > keep_snapshots)) || break
		[[ $name != "${snapshot_dir##*/}" && $name != "$rollback_from" ]] || continue
		rm -rf "${state_dir:?}/snapshots/$name"
		remaining=$((remaining - 1))
	done
}

install_entry() {
	local source_dir=$1 entry=$2
	local target=$live_dir/$entry
	rm -rf "$target.new"
	cp -a "$source_dir/$entry" "$target.new"
	rm -rf "$target"
	mv -f "$target.new" "$target"
}

write_env_image() {
	(umask 077 && printf 'SAGA_LAB_IMAGE=%s\n' "$1" >"$live_dir/.env.image.tmp")
	mv -f "$live_dir/.env.image.tmp" "$live_dir/.env.image"
}

retry() {
	local description=$1 attempt
	shift
	for ((attempt = 1; ; attempt++)); do
		if "$@"; then
			return 0
		fi
		if ((attempt >= check_attempts)); then
			printf '%s failed.\n' "$description" >&2
			return 1
		fi
		sleep 2
	done
}

serves_trace_dashboard() {
	curl --fail --silent --show-error --max-time 10 "$public_url/grafana/api/dashboards/uid/saga-lab-trace" |
		grep -qF '"uid":"saga-lab-trace"'
}

recreate_and_verify() {
	live_compose up --detach --no-build --wait --wait-timeout 300 --force-recreate
	retry 'Loopback readiness' curl --fail --silent --max-time 5 http://127.0.0.1:8090/readyz
	retry 'Public readiness' curl --fail --silent --show-error --max-time 10 "$public_url/readyz"
	retry 'Grafana Trace dashboard' serves_trace_dashboard
	bash "$live_dir/scripts/smoke.sh" "$public_url"
	log 'Saga Lab is ready, publicly reachable, serves the Trace dashboard, and passes the smoke run.'
}

restore() {
	local source_dir=$1 entry
	for entry in "${entries[@]}"; do
		if [[ -e $source_dir/$entry ]]; then
			install_entry "$source_dir" "$entry"
		fi
	done
	write_env_image "$(sed -n 's/^SAGA_LAB_IMAGE=//p' "$source_dir/.env.image" | tail -n 1)"
	recreate_and_verify
}

apply_full() {
	local entry
	for entry in "${entries[@]}"; do
		install_entry "$bundle_dir" "$entry"
	done
	write_env_image "$image"
	recreate_and_verify
}

apply_rollback() {
	restore "$state_dir/snapshots/$rollback_from"
}

diagnostics() {
	live_compose ps --all || true
	live_compose logs --no-color --tail=50 || true
}

record_manifest() {
	(
		umask 077
		printf 'commit=%s\nimage=%s\nmode=%s\ndeployed_at=%s\n' \
			"$commit" "$image" "$mode" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" >"$state_dir/manifest.tmp"
	)
	mv -f "$state_dir/manifest.tmp" "$state_dir/manifest"
}

if [[ $mode == full ]]; then
	command -v curl >/dev/null || die 'curl is required on the host.'
	docker compose version >/dev/null
	docker network inspect saga-lab-edge >/dev/null || die 'The hetzner-one-owned saga-lab-edge network is missing.'
	check_headroom
	log "Pulling $image."
	validate_bundle
	pull_image
	pull_stack "$bundle_dir" "$image"
else
	check_headroom
	pull_stack "$state_dir/snapshots/$rollback_from" "$(sed -n 's/^SAGA_LAB_IMAGE=//p' "$state_dir/snapshots/$rollback_from/.env.image" | tail -n 1)"
fi

take_snapshot
prune_snapshots
log "Applying $mode deployment."
set +e
(
	set -e
	"apply_$mode"
)
status=$?
set -e

if ((status == 0)); then
	if [[ $mode == rollback ]]; then
		if [[ -f $state_dir/snapshots/$rollback_from/manifest ]]; then
			cp -a "$state_dir/snapshots/$rollback_from/manifest" "$state_dir/manifest"
		else
			rm -f "$state_dir/manifest"
		fi
	else
		record_manifest
	fi
	prune_snapshots
	log "Saga Lab $mode deployment verified."
	exit 0
fi

printf 'Saga Lab %s deployment failed.\n' "$mode" >&2
diagnostics
if [[ ! -f $snapshot_dir/.env.image ]]; then
	die "Saga Lab $mode deployment failed and there is no previous deployment to restore; the failed stack is left running for inspection."
fi
printf 'Restoring snapshot %s.\n' "${snapshot_dir##*/}" >&2
set +e
(
	set -e
	restore "$snapshot_dir"
)
restore_status=$?
set -e
if ((restore_status != 0)); then
	die "ROLLBACK FAILED after a failed $mode deployment. Inspect the host; snapshot ${snapshot_dir##*/} holds the previous files."
fi
die "Saga Lab $mode deployment failed; the previous deployment was restored and verified."
