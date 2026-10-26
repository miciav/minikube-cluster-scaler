#!/bin/sh
set -eu

cd "$(dirname "$0")/.."
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

cat >"$tmp/minikube" <<'STUB'
#!/bin/sh
printf '%s\n' "$*" >>"$TEST_LOG"
STUB
chmod +x "$tmp/minikube"

TEST_LOG="$tmp/log" PATH="$tmp:$PATH" PROFILE=test ./scripts/99-cleanup.sh >/dev/null
test "$(tail -n 1 "$tmp/log")" = 'delete -p test'
