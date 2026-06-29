#!/bin/sh
set -eu

cd "$(dirname "$0")/.."
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

for command in minikube kubectl; do
  cat >"$tmp/$command" <<'EOF'
#!/bin/sh
printf '%s %s\n' "$(basename "$0")" "$*" >>"$TEST_LOG"
EOF
  chmod +x "$tmp/$command"
done

TEST_LOG="$tmp/log" PATH="$tmp:$PATH" PROFILE=test ./scripts/01-start-minikube.sh >/dev/null
grep -q -- 'minikube start .*--cni=flannel' "$tmp/log"
