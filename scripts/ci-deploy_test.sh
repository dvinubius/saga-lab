#!/usr/bin/env bash

set -euo pipefail

project_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
temporary_dir=$(mktemp -d)
trap 'rm -rf "$temporary_dir"' EXIT

bin=$temporary_dir/bin
repo=$temporary_dir/repo
ssh_log=$temporary_dir/ssh.log
mkdir -p "$bin" "$repo/scripts" "$repo/deploy/tempo" "$repo/cmd/transfer-service"

cat >"$bin/ssh" <<'EOF'
#!/usr/bin/env bash
printf 'ARGS %s\n' "$*" >>"$CI_TEST_SSH_LOG"
[[ -z ${CI_TEST_SSH_FAIL:-} ]] || exit 255
command=${!#}
if [[ $command == *'tar -x'* ]]; then
	tar -t | sed 's/^/BUNDLE /' >>"$CI_TEST_SSH_LOG"
elif [[ $command == *remote-deploy.sh* ]]; then
	sed 's/^/STDIN /' >>"$CI_TEST_SSH_LOG"
elif [[ $command == *manifest* ]]; then
	cat "$CI_TEST_MANIFEST" 2>/dev/null || true
fi
EOF
chmod +x "$bin/ssh"

for script in ci-deploy classify-deploy remote-deploy compose smoke; do
	cp "$project_dir/scripts/$script.sh" "$repo/scripts/"
done
touch "$repo/compose.yaml" "$repo/compose.production.yaml" "$repo/compose.test.yaml" \
	"$repo/deploy/tempo/tempo.yaml" "$repo/cmd/transfer-service/main.go" "$repo/Dockerfile" \
	"$repo/README.md" "$repo/scripts/remote-deploy_test.sh" "$repo/scripts/test.sh" "$repo/scripts/reset.sh"
printf 'key\n' >"$temporary_dir/key"
printf 'host key\n' >"$temporary_dir/known_hosts"

git init -q --bare "$temporary_dir/origin.git"
cd "$repo"
git init -q -b main
git config user.email test@example.invalid
git config user.name test
git config commit.gpgsign false
git remote add origin "$temporary_dir/origin.git"
git add -A
git commit -q -m base
base=$(git rev-parse HEAD)
printf 'docs\n' >>README.md
git commit -q -am docs
head=$(git rev-parse HEAD)
git push -q origin main

image=ghcr.io/dvinubius/saga-lab@sha256:$(printf '3%.0s' {1..64})

ci() {
	PATH="$bin:$PATH" CI_TEST_SSH_LOG="$ssh_log" CI_TEST_MANIFEST="$temporary_dir/manifest" \
		DEPLOY_HOST=vps.example DEPLOY_USER=saga-lab-deploy \
		DEPLOY_SSH_KEY_FILE="$temporary_dir/key" DEPLOY_KNOWN_HOSTS_FILE="$temporary_dir/known_hosts" \
		bash scripts/ci-deploy.sh "$@"
}

fail() {
	printf 'FAIL: %s\n' "$*" >&2
	exit 1
}

reset() {
	rm -f "$ssh_log" "$temporary_dir/manifest"
}

expect_no_ssh() {
	[[ ! -e $ssh_log ]] || fail "$1 opened SSH"
}

reset
[[ $(ci plan "$head" 2>/dev/null) == full ]] || fail 'first deployment without a manifest is not full'
grep -q 'StrictHostKeyChecking=yes' "$ssh_log" || fail 'SSH does not use strict host-key checking'
grep -q "UserKnownHostsFile=$temporary_dir/known_hosts" "$ssh_log" || fail 'SSH ignores the verified known-hosts file'
grep -q 'saga-lab-deploy@vps.example' "$ssh_log" || fail 'SSH did not use the deployment account'

reset
printf 'commit=%s\nimage=%s\n' "$base" "$image" >"$temporary_dir/manifest"
[[ $(ci plan "$head" 2>/dev/null) == none ]] || fail 'docs-only change since the manifest is not none'
[[ $(ci plan "$head" full 2>/dev/null) == full ]] || fail 'manual full deployment was not honored'

reset
if output=$(CI_TEST_SSH_FAIL=1 ci plan "$head" 2>/dev/null); then
	fail 'plan continued after an SSH failure'
fi
[[ -z $output ]] || fail "plan printed a mode after an SSH failure: $output"

reset
if output=$(ci plan "$(printf '%040d' 1)" 2>/dev/null); then
	fail 'plan continued for an unknown target commit'
fi
[[ -z $output ]] || fail "plan printed a mode for an unknown target: $output"

reset
ci deploy "$base" "$image" >/dev/null 2>&1 && fail 'deployed a commit that is not the head of main'
expect_no_ssh 'stale target'

reset
ci deploy "$head" "ghcr.io/other/saga-lab@sha256:$(printf '3%.0s' {1..64})" >/dev/null 2>&1 &&
	fail 'accepted an image from another repository'
ci deploy "$head" ghcr.io/dvinubius/saga-lab:latest >/dev/null 2>&1 && fail 'accepted a mutable tag'
expect_no_ssh 'invalid image'

reset
GHCR_USER=github-actions GHCR_PULL_TOKEN=secret-token ci deploy "$head" "$image" >/dev/null
for entry in compose.yaml compose.production.yaml deploy/tempo/tempo.yaml \
	scripts/compose.sh scripts/remote-deploy.sh scripts/smoke.sh; do
	grep -q "^BUNDLE $entry\$" "$ssh_log" || fail "bundle is missing $entry"
done
! grep -qE '^BUNDLE (Dockerfile|README\.md|compose\.test\.yaml|cmd/.*|scripts/(.*_test|test|reset|ci-deploy|classify-deploy)\.sh)$' "$ssh_log" ||
	fail 'bundle contains files the VPS does not need'
grep -q "remote-deploy.sh' full $head $image" "$ssh_log" || fail 'deployment did not pass the exact image digest'
grep -q '^STDIN secret-token$' "$ssh_log" || fail 'registry token did not arrive on stdin'
! grep -q '^ARGS .*secret-token' "$ssh_log" || fail 'registry token appeared in SSH arguments'
! grep -qE '^ARGS .*(git |docker build|compose build)' "$ssh_log" || fail 'deployment ran Git or a build on the VPS'

printf '%s\n' 'CI deploy script tests passed'
