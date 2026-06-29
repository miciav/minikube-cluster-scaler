#!/bin/sh
set -eu

cd "$(dirname "$0")/.."
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

cat >"$tmp/kubectl" <<'EOF'
#!/bin/sh
printf '%s\n' "$*" >>"$TEST_LOG"
case "$*" in
  *jsonpath=*) printf '%s' 11 ;;
esac
EOF
chmod +x "$tmp/kubectl"

TEST_LOG="$tmp/log" PATH="$tmp:$PATH" PROFILE=test ./scripts/04-create-pressure.sh >/dev/null
grep -q -- '--requests=cpu=3667m,memory=64Mi' "$tmp/log"
