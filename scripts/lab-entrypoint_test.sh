#!/bin/sh
set -eu

cd "$(dirname "$0")/.."
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

./scripts/lab-entrypoint.sh help >"$tmp/help"
for subcommand in check start provider deploy pressure watch remove-pressure cleanup e2e wrapper; do
  grep -q "^  $subcommand " "$tmp/help"
done

status=0
./scripts/lab-entrypoint.sh bogus >/dev/null 2>"$tmp/err" || status=$?
test "$status" -eq 2
grep -q 'usage:' "$tmp/err"

LAB_IMAGE_TAG=v9.9.9 ./scripts/lab-entrypoint.sh wrapper >"$tmp/lab"
grep -Fq 'ghcr.io/miciav/minikube-cluster-scaler:v9.9.9' "$tmp/lab"
if grep -Fq '__LAB_IMAGE__' "$tmp/lab"; then exit 1; fi

cat >"$tmp/kubectl" <<'STUB'
#!/bin/sh
printf '%s\n' "$*" >>"$TEST_LOG"
case "$*" in *jsonpath*) printf '2' ;; esac
STUB
chmod +x "$tmp/kubectl"
TEST_LOG="$tmp/log" PATH="$tmp:$PATH" PROFILE=test ./scripts/lab-entrypoint.sh pressure >/dev/null
grep -Fq 'apply -f deploy/workload-unschedulable.yaml' "$tmp/log"
