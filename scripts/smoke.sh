#!/usr/bin/env bash
set -euo pipefail

base_url=${1:?usage: scripts/smoke.sh <base-url>}
base_url=${base_url%/}
amount=10
command -v curl >/dev/null || { echo 'curl is required' >&2; exit 1; }

cookie_jar=$(mktemp)
trap 'rm -f "$cookie_jar"' EXIT

fail() {
  printf 'FAIL %s: %s\n' "$1" "$2" >&2
  exit 1
}

request() {
  local args=(--silent --show-error --max-time 10 --cookie "$cookie_jar" --cookie-jar "$cookie_jar" --request "$1" --write-out '\n%{http_code}')
  if [[ $# -gt 2 ]]; then
    args+=(--header 'Content-Type: application/json' --data "$3")
  fi
  local out
  out=$(curl "${args[@]}" "$base_url$2") || return 1
  status=${out##*$'\n'}
  body=${out%$'\n'*}
}

balance() {
  sed -n "s/.*\"$1\":{\"balance\":\([0-9]*\)}.*/\1/p" <<<"$body"
}

count() {
  printf '{"attempts":%d,"effects":%d}' "$1" "$2"
}

poll() {
  local deadline=$((SECONDS + $3))
  while :; do
    request GET "/api/transfers/$2" || fail "$1" "transfer $2 unreachable"
    [[ $status == 200 ]] || fail "$1" "GET /api/transfers/$2 answered $status: $body"
    [[ $body == *"$4"* ]] || return 0
    ((SECONDS < deadline)) || fail "$1" "$5"
    sleep 0.5
  done
}

run() {
  local slug=$1 final=$2 a_after=$3 b_after=$4 debit=$5 credit=$6 refund=$7 suppressed=$8
  request POST /api/transfers "{\"amount\":$amount,\"scenario\":\"$slug\"}" || fail "$slug" 'submission failed'
  if [[ $status == 503 && $body == *'admission limit'* ]]; then
    printf 'WARN %s skipped: refused by the admission limit\n' "$slug"
    return
  fi
  [[ $status == 202 ]] || fail "$slug" "submission answered $status: $body"
  local id
  id=$(sed -n 's/.*"transfer_id":"\([^"]*\)".*/\1/p' <<<"$body")
  poll "$slug" "$id" 120 '"status":"awaiting_admission"' 'not admitted within two minutes'
  poll "$slug" "$id" 60 '"visualisation_ready":false' 'not ready within a minute'
  [[ $body == *"\"status\":\"$final\""* ]] || fail "$slug" "transfer $id did not end $final: $body"
  local want got
  want=$(printf '{"balances":{"bank_a":{"before":%d,"after":%d},"bank_b":{"before":%d,"after":%d,"involved":true}},"commands":{"debit":%s,"credit":%s,"refund":%s},"duplicates_suppressed":%d,"duplicate_effects":0}' \
    "$a" "$a_after" "$b" "$b_after" "$debit" "$credit" "$refund" "$suppressed")
  got=$(sed -n 's/.*"outcome":\(.*\)}$/\1/p' <<<"$body")
  [[ $got == "$want" ]] || fail "$slug" "transfer $id outcome $got, want $want"
  a=$a_after
  b=$b_after
  printf 'ok   %s\n' "$slug"
}

request GET /api/balances || fail setup 'balances unreachable'
[[ $status == 200 ]] || fail setup "GET /api/balances answered $status: $body"
if (($(balance bank_a) < 5 * amount)); then
  request POST /api/top-ups || fail setup 'top-up failed'
  [[ $status == 200 ]] || fail setup "POST /api/top-ups answered $status: $body"
fi
a=$(balance bank_a)
b=$(balance bank_b)

run happy_path completed $((a - amount)) $((b + amount)) "$(count 1 1)" "$(count 1 1)" null 0
run debit_redelivery completed $((a - amount)) $((b + amount)) "$(count 2 1)" "$(count 1 1)" null 1
run credit_rejection refunded "$a" "$b" "$(count 1 1)" "$(count 1 0)" "$(count 1 1)" 0
run bank_b_unavailable completed $((a - amount)) $((b + amount)) "$(count 1 1)" "$(count 1 1)" null 0
run refund_redelivery refunded "$a" "$b" "$(count 1 1)" "$(count 1 0)" "$(count 2 1)" 1
