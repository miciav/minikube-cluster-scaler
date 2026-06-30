#!/bin/sh
set -eu

cd "$(dirname "$0")/.."
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

cat >"$tmp/kubectl" <<'EOF'
#!/bin/sh
printf '%s\n' "$*" >"$TEST_LOG"
EOF
chmod +x "$tmp/kubectl"

TEST_LOG="$tmp/log" PATH="$tmp:$PATH" PROFILE=test ./scripts/06-remove-pressure.sh >/dev/null
[ "$(cat "$tmp/log")" = '--context test delete -f deploy/workload-unschedulable.yaml --ignore-not-found' ]
