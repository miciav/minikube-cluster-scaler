#!/bin/sh
set -eu

cd "$(dirname "$0")/.."
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
mkdir "$tmp/with-binary" "$tmp/go-only"

for command in with-binary/minikube-externalgrpc-provider with-binary/go go-only/go; do
  cat >"$tmp/$command" <<'STUB'
#!/bin/sh
printf '%s %s\n' "$(basename "$0")" "$*" >>"$TEST_LOG"
STUB
  chmod +x "$tmp/$command"
done

TEST_LOG="$tmp/binary.log" PATH="$tmp/with-binary:$PATH" PROFILE=test ./scripts/02-run-provider.sh >/dev/null
grep -Fq 'minikube-externalgrpc-provider --profile test --node-group minikube-workers' "$tmp/binary.log"
grep -Fq -- '--listen 0.0.0.0:9090' "$tmp/binary.log"
if grep -q '^go ' "$tmp/binary.log"; then exit 1; fi

TEST_LOG="$tmp/go.log" PATH="$tmp/go-only:$PATH" PROFILE=test ./scripts/02-run-provider.sh >/dev/null
grep -Fq 'go run ./cmd/provider --profile test' "$tmp/go.log"
