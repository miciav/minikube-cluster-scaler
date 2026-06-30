#!/bin/sh
set -eu

cd "$(dirname "$0")/.."

grep -Fq 'go build -o "$provider_bin" ./cmd/provider' scripts/e2e-scale-down.sh
grep -Fq '"$provider_bin" --profile "$PROFILE"' scripts/e2e-scale-down.sh
grep -Fq 'minikube ssh -p "$PROFILE" -- "nc -z -w 2 host.minikube.internal 9090"' scripts/e2e-scale-down.sh
grep -Fq 'kill -0 "$provider_pid"' scripts/e2e-scale-down.sh
grep -Fq 'wait "$provider_pid"' scripts/e2e-scale-down.sh
grep -Fq 'rm -f "$provider_log" "$provider_bin"' scripts/e2e-scale-down.sh
! grep -Fq './scripts/02-run-provider.sh' scripts/e2e-scale-down.sh

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' 0

cat >"$tmp/minikube" <<'EOF'
#!/bin/sh
printf '%s\n' "$*" >>"$TEST_LOG"
if [ "$*" = 'profile list -o json' ]; then
  printf '%s\n' '{"invalid":[],"valid":[{"Name":"existing","Status":"Stopped"}]}'
fi
EOF
cat >"$tmp/go" <<'EOF'
#!/bin/sh
exit 1
EOF
chmod +x "$tmp/minikube" "$tmp/go"

status=0
output=$(TEST_LOG="$tmp/log" PATH="$tmp:$PATH" PROFILE=existing ./scripts/e2e-scale-down.sh 2>&1) || status=$?
test "$status" -ne 0
printf '%s\n' "$output" | grep -Fq 'refusing to use existing minikube profile: existing'
test "$(wc -l <"$tmp/log" | tr -d ' ')" = 1
grep -Fxq 'profile list -o json' "$tmp/log"
