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

TEST_LOG="$tmp/log" PATH="$tmp:$PATH" PROFILE=test SCALE_DOWN_DELAY_AFTER_ADD=45s SCALE_DOWN_UNNEEDED_TIME=90s ./scripts/03-deploy-cluster-autoscaler.sh >/dev/null
grep -q -- 'set env deployment/cluster-autoscaler SCALE_DOWN_DELAY_AFTER_ADD=45s SCALE_DOWN_UNNEEDED_TIME=90s' "$tmp/log"

for argument in \
  '--scale-down-enabled=true' \
  '--max-scale-down-parallelism=1' \
  '--scale-down-delay-after-add=$(SCALE_DOWN_DELAY_AFTER_ADD)' \
  '--scale-down-unneeded-time=$(SCALE_DOWN_UNNEEDED_TIME)'
do
  grep -Fq -- "$argument" deploy/cluster-autoscaler.yaml
done
