#!/bin/sh
set -eu

cd "$(dirname "$0")/.."
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

for command in docker sonar-scanner make; do
  cat >"$tmp/$command" <<'STUB'
#!/bin/sh
printf '%s %s\n' "$(basename "$0")" "$*" >>"$TEST_LOG"
STUB
  chmod +x "$tmp/$command"
done
: >"$tmp/log"

TEST_LOG="$tmp/log" PATH="$tmp:$PATH" ./scripts/sonar.sh --dry-run >"$tmp/out"
grep -q 'sonar-scanner' "$tmp/out"
grep -Fq -- '-Dsonar.projectKey=minikube-cluster-scaler' "$tmp/out"
grep -Fq 'make cover tui-test' "$tmp/out"
if grep -q '^docker run' "$tmp/log"; then exit 1; fi
if grep -q '^make ' "$tmp/log"; then exit 1; fi

status=0
TEST_LOG="$tmp/log" PATH="$tmp:$PATH" ./scripts/sonar.sh --bogus >/dev/null 2>&1 || status=$?
test "$status" -ne 0
