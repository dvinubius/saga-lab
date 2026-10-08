#!/usr/bin/env bash

set -euo pipefail

project_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
temporary_dir=$(mktemp -d)
trap 'rm -rf "$temporary_dir"' EXIT

bin=$temporary_dir/bin
bundle=$temporary_dir/bundle
live=$temporary_dir/live
log=$temporary_dir/commands.log
mkdir -p "$bin" "$temporary_dir/docker-root"

commit=$(printf 'a%.0s' {1..40})
old_commit=$(printf 'b%.0s' {1..40})
old_image=ghcr.io/dvinubius/saga-lab@sha256:$(printf '1%.0s' {1..64})
new_image=ghcr.io/dvinubius/saga-lab@sha256:$(printf '2%.0s' {1..64})

cat >"$bin/docker" <<'EOF'
#!/usr/bin/env bash
printf 'docker %s\n' "$*" >>"$REMOTE_TEST_LOG"
case " $* " in
*' login '*)
	read -r token
	printf 'login token=%s\n' "$token" >>"$REMOTE_TEST_LOG"
	;;
*' info '*) printf '%s\n' "$REMOTE_TEST_DOCKER_ROOT" ;;
*' network inspect '*) [[ -z ${REMOTE_TEST_NO_EDGE:-} ]] || exit 1 ;;
esac
exit 0
EOF

cat >"$bin/df" <<'EOF'
#!/usr/bin/env bash
printf 'Filesystem 1024-blocks Used Available Capacity Mounted on\n'
printf '/dev/sda1 40000000 20000000 %s 50%% /\n' "${REMOTE_TEST_FREE_KIB:-20000000}"
EOF

# A check fails only while the bad image is installed, so the restored
# deployment can verify successfully.
cat >"$bin/curl" <<'EOF'
#!/usr/bin/env bash
printf 'curl %s\n' "$*" >>"$REMOTE_TEST_LOG"
case ${!#} in
http://127.0.0.1:8090/readyz) check=loopback ;;
https://saga.dinubarbu.com/readyz) check=public ;;
https://saga.dinubarbu.com/grafana/api/dashboards/uid/saga-lab-trace) check=grafana ;;
*) exit 6 ;;
esac
if [[ -n ${REMOTE_TEST_BAD_IMAGE:-} && $check == "$REMOTE_TEST_FAIL" ]] &&
	grep -qF "$REMOTE_TEST_BAD_IMAGE" "$SAGA_LAB_DIR/.env.image" 2>/dev/null; then
	exit 22
fi
if [[ $check == grafana ]]; then
	printf '{"meta":{"slug":"trace"},"dashboard":{"title":"Trace","uid":"saga-lab-trace"}}'
else
	printf 'ready\n'
fi
EOF

cat >"$bin/date" <<'EOF'
#!/usr/bin/env bash
count=$(($(cat "$REMOTE_TEST_DATE" 2>/dev/null || echo 0) + 1))
printf '%s\n' "$count" >"$REMOTE_TEST_DATE"
if [[ $* == *%Y%m%dT%H%M%SZ* ]]; then
	printf '20261008T1200%02dZ\n' "$count"
else
	printf '2026-10-08T12:00:%02dZ\n' "$count"
fi
EOF

printf '#!/usr/bin/env bash\n[[ -z ${REMOTE_TEST_LOCKED:-} ]]\n' >"$bin/flock"
printf '#!/usr/bin/env bash\nexit 0\n' >"$bin/sleep"
chmod +x "$bin"/*

smoke_stub() {
	cat >"$1" <<'EOF'
#!/usr/bin/env bash
printf 'smoke %s\n' "$*" >>"$REMOTE_TEST_LOG"
if [[ -n ${REMOTE_TEST_BAD_IMAGE:-} && $REMOTE_TEST_FAIL == smoke ]] &&
	grep -qF "$REMOTE_TEST_BAD_IMAGE" "$SAGA_LAB_DIR/.env.image" 2>/dev/null; then
	exit 1
fi
EOF
}

make_bundle() {
	rm -rf "$bundle"
	mkdir -p "$bundle/scripts" "$bundle/deploy/tempo"
	printf 'name: saga-lab # new\n' >"$bundle/compose.yaml"
	printf 'services: {} # new\n' >"$bundle/compose.production.yaml"
	printf 'new tempo\n' >"$bundle/deploy/tempo/tempo.yaml"
	cp "$project_dir/scripts/remote-deploy.sh" "$project_dir/scripts/compose.sh" "$bundle/scripts/"
	smoke_stub "$bundle/scripts/smoke.sh"
}

# A host with a verified deployment, or with first=1 a prepared empty one.
make_live() {
	rm -rf "$live" "$temporary_dir/date"
	mkdir -p "$live"
	printf 'POSTGRES_PASSWORD=secret\n' >"$live/.env"
	if [[ ${1:-} != first ]]; then
		mkdir -p "$live/scripts" "$live/deploy/tempo" "$live/.deploy"
		printf 'name: saga-lab # old\n' >"$live/compose.yaml"
		printf 'services: {} # old\n' >"$live/compose.production.yaml"
		printf 'old tempo\n' >"$live/deploy/tempo/tempo.yaml"
		cp "$project_dir/scripts/remote-deploy.sh" "$project_dir/scripts/compose.sh" "$live/scripts/"
		smoke_stub "$live/scripts/smoke.sh"
		printf 'SAGA_LAB_IMAGE=%s\n' "$old_image" >"$live/.env.image"
		printf 'commit=%s\nimage=%s\nmode=full\n' "$old_commit" "$old_image" >"$live/.deploy/manifest"
	fi
	: >"$log"
}

run() {
	PATH="$bin:$PATH" SAGA_LAB_DIR="$live" REMOTE_TEST_LOG="$log" REMOTE_TEST_DATE="$temporary_dir/date" \
		REMOTE_TEST_DOCKER_ROOT="$temporary_dir/docker-root" REMOTE_TEST_FAIL=${REMOTE_TEST_FAIL:-} \
		DEPLOY_CHECK_ATTEMPTS=2 bash "$@"
}

deploy() {
	run "$bundle/scripts/remote-deploy.sh" "$@"
}

fail() {
	printf 'FAIL: %s\n' "$*" >&2
	exit 1
}

expect_log() {
	grep -qF -- "$1" "$log" || fail "expected command log to contain: $1"
}

refute_log() {
	! grep -qF -- "$1" "$log" || fail "command log unexpectedly contains: $1"
}

file_mode() {
	stat -c '%a' "$1" 2>/dev/null || stat -f '%Lp' "$1"
}

manifest_value() {
	sed -n "s/^$1=//p" "$live/.deploy/manifest"
}

snapshot_count() {
	if [[ -d $live/.deploy/snapshots ]]; then
		ls "$live/.deploy/snapshots" | wc -l | tr -d ' '
	else
		echo 0
	fi
}

refute_forbidden_commands() {
	refute_log ' down'
	refute_log 'volume'
	refute_log ' build'
	refute_log 'git '
	refute_log 'caddy'
	refute_log 'hooklook'
	refute_log 'zibs'
	[[ $(cat "$live/.env") == 'POSTGRES_PASSWORD=secret' ]] || fail 'deployment changed .env'
}

expect_previous_deployment() {
	[[ $(cat "$live/.env.image") == "SAGA_LAB_IMAGE=$old_image" ]] || fail "$1: previous image not in place"
	grep -q '# old' "$live/compose.yaml" || fail "$1: previous compose.yaml not in place"
	grep -q 'old tempo' "$live/deploy/tempo/tempo.yaml" || fail "$1: previous deploy/ not in place"
	[[ $(manifest_value commit) == "$old_commit" && -z $(manifest_value deployed_at) ]] ||
		fail "$1: manifest changed"
}

test_full_deploy() {
	make_bundle
	make_live
	deploy full "$commit" "$new_image" >/dev/null
	expect_log "docker network inspect saga-lab-edge"
	expect_log "docker pull --quiet $new_image"
	expect_log 'config --quiet'
	expect_log 'compose --env-file .env --env-file .env.image --file compose.yaml --file compose.production.yaml up --detach --no-build --wait'
	expect_log 'curl --fail --silent --max-time 5 http://127.0.0.1:8090/readyz'
	expect_log 'https://saga.dinubarbu.com/readyz'
	expect_log 'https://saga.dinubarbu.com/grafana/api/dashboards/uid/saga-lab-trace'
	expect_log 'smoke https://saga.dinubarbu.com'
	refute_forbidden_commands
	[[ $(cat "$live/.env.image") == "SAGA_LAB_IMAGE=$new_image" ]] || fail 'deploy did not pin the new image'
	[[ $(file_mode "$live/.env.image") == 600 ]] || fail '.env.image is not mode 0600'
	[[ $(file_mode "$live/.deploy/manifest") == 600 ]] || fail 'manifest is not mode 0600'
	[[ $(manifest_value commit) == "$commit" && $(manifest_value image) == "$new_image" &&
		$(manifest_value mode) == full && -n $(manifest_value deployed_at) ]] ||
		fail 'deploy wrote the wrong manifest'
	grep -q '# new' "$live/compose.yaml" || fail 'deploy did not install compose.yaml'
	grep -q '# new' "$live/compose.production.yaml" || fail 'deploy did not install compose.production.yaml'
	grep -q 'new tempo' "$live/deploy/tempo/tempo.yaml" || fail 'deploy did not install deploy/'
	[[ -f $live/scripts/remote-deploy.sh && -f $live/scripts/compose.sh ]] || fail 'deploy did not install scripts'
	local snapshot
	snapshot=$(ls -d "$live/.deploy/snapshots/"*-full)
	grep -q "$old_image" "$snapshot/.env.image" || fail 'snapshot lost the previous image'
	grep -q '# old' "$snapshot/compose.yaml" || fail 'snapshot lost the previous compose.yaml'
	grep -q 'old tempo' "$snapshot/deploy/tempo/tempo.yaml" || fail 'snapshot lost the previous deploy/'
	grep -q "commit=$old_commit" "$snapshot/manifest" || fail 'snapshot lost the previous manifest'
}

test_first_deploy() {
	make_bundle
	make_live first
	deploy full "$commit" "$new_image" >/dev/null
	[[ $(manifest_value commit) == "$commit" ]] || fail 'first deploy did not write the manifest'
	grep -q '# new' "$live/compose.yaml" || fail 'first deploy did not install compose.yaml'
}

test_registry_token() {
	make_bundle
	make_live
	printf '%s\n' short-lived-token | GHCR_USER=actor deploy full "$commit" "$new_image" >/dev/null
	expect_log 'login ghcr.io --username actor --password-stdin'
	expect_log 'login token=short-lived-token'
	[[ $(grep -c short-lived-token "$log") -eq 1 ]] || fail 'registry token appeared in command arguments'
}

test_held_lock_refuses() {
	make_bundle
	make_live
	if output=$(REMOTE_TEST_LOCKED=1 deploy full "$commit" "$new_image" 2>&1); then
		fail 'deployed while another deployment held the lock'
	fi
	[[ $output == *'holds the lock'* ]] || fail "unexpected lock output: $output"
	[[ $(snapshot_count) == 0 ]] || fail 'refused deploy took a snapshot'
	refute_log 'docker pull'
	expect_previous_deployment 'held lock'
}

test_failed_precondition_changes_nothing() {
	local name output
	for name in headroom edge; do
		make_bundle
		make_live
		if [[ $name == headroom ]]; then
			output=$(REMOTE_TEST_FREE_KIB=14000000 deploy full "$commit" "$new_image" 2>&1) &&
				fail 'deployed with less than 15 GB free'
			[[ $output == *'Insufficient Docker filesystem space'* ]] || fail "unexpected headroom output: $output"
		else
			output=$(REMOTE_TEST_NO_EDGE=1 deploy full "$commit" "$new_image" 2>&1) &&
				fail 'deployed without the saga-lab-edge network'
			[[ $output == *'saga-lab-edge'* ]] || fail "unexpected network output: $output"
		fi
		[[ $(snapshot_count) == 0 ]] || fail "$name refusal took a snapshot"
		refute_log 'docker pull'
		refute_log ' up '
		expect_previous_deployment "$name refusal"
	done
}

test_failed_check_restores_previous_deployment() {
	local check output
	for check in loopback public grafana smoke; do
		make_bundle
		make_live
		if output=$(REMOTE_TEST_BAD_IMAGE=$new_image REMOTE_TEST_FAIL=$check deploy full "$commit" "$new_image" 2>&1); then
			fail "deploy accepted a failing $check check"
		fi
		[[ $output == *'previous deployment was restored and verified'* ]] || fail "unexpected $check failure output: $output"
		expect_previous_deployment "failed $check check"
		expect_log 'logs --no-color --tail=50'
		[[ $(grep -c ' up --detach --no-build --wait' "$log") -eq 2 ]] || fail "failed $check check did not recreate the previous stack"
		[[ $(grep -c '^smoke ' "$log") -ge 1 ]] || fail "failed $check check ran no smoke test"
		refute_forbidden_commands
	done
}

test_failed_restore_reports_rollback_failed() {
	make_bundle
	make_live
	local output
	if output=$(REMOTE_TEST_BAD_IMAGE=sha256 REMOTE_TEST_FAIL=public deploy full "$commit" "$new_image" 2>&1); then
		fail 'deploy accepted a failing public check'
	fi
	[[ $output == *'ROLLBACK FAILED'* ]] || fail "failed restore was not reported: $output"
}

test_failed_first_deploy_has_nothing_to_restore() {
	make_bundle
	make_live first
	local output
	if output=$(REMOTE_TEST_BAD_IMAGE=$new_image REMOTE_TEST_FAIL=smoke deploy full "$commit" "$new_image" 2>&1); then
		fail 'first deploy accepted a failing smoke test'
	fi
	[[ $output == *'no previous deployment'* ]] || fail "unexpected first deploy failure output: $output"
	[[ ! -e $live/.deploy/manifest ]] || fail 'failed first deploy wrote a manifest'
}

test_manual_rollback() {
	make_bundle
	make_live
	local snapshot
	deploy full "$commit" "$new_image" >/dev/null
	snapshot=$(basename "$(ls -d "$live/.deploy/snapshots/"*-full)")
	: >"$log"
	run "$live/scripts/remote-deploy.sh" rollback "$snapshot" >/dev/null
	expect_previous_deployment 'manual rollback'
	expect_log ' up --detach --no-build --wait'
	expect_log 'smoke https://saga.dinubarbu.com'
	refute_forbidden_commands
	run "$live/scripts/remote-deploy.sh" rollback 20990101T000000Z-full >/dev/null 2>&1 &&
		fail 'rolled back to an unknown snapshot'
	return 0
}

test_keeps_five_snapshots() {
	make_bundle
	make_live
	local i
	for i in 1 2 3 4 5 6; do
		deploy full "$commit" "$new_image" >/dev/null
	done
	[[ $(snapshot_count) == 5 ]] || fail "kept $(snapshot_count) snapshots, not 5"
}

test_invalid_arguments_make_no_calls() {
	make_bundle
	make_live
	deploy full "$commit" ghcr.io/dvinubius/saga-lab:latest >/dev/null 2>&1 && fail 'accepted a mutable tag'
	deploy full "$commit" "ghcr.io/other/saga-lab@sha256:$(printf '2%.0s' {1..64})" >/dev/null 2>&1 &&
		fail 'accepted an image from another repository'
	deploy full abc123 "$new_image" >/dev/null 2>&1 && fail 'accepted an abbreviated commit'
	[[ ! -s $log ]] || fail 'invalid arguments reached Docker or curl'
}

test_unexpected_bundle_entry_is_rejected() {
	make_bundle
	touch "$bundle/Dockerfile"
	make_live
	deploy full "$commit" "$new_image" >/dev/null 2>&1 && fail 'accepted a bundle containing source'
	expect_previous_deployment 'rejected bundle'
}

test_full_deploy
test_first_deploy
test_registry_token
test_held_lock_refuses
test_failed_precondition_changes_nothing
test_failed_check_restores_previous_deployment
test_failed_restore_reports_rollback_failed
test_failed_first_deploy_has_nothing_to_restore
test_manual_rollback
test_keeps_five_snapshots
test_invalid_arguments_make_no_calls
test_unexpected_bundle_entry_is_rejected
printf '%s\n' 'remote deploy script tests passed'
