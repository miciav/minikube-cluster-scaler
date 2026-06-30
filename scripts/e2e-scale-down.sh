#!/bin/sh
set -eu

cd "$(dirname "$0")/.."

PROFILE=${PROFILE:-autoscaling-scale-down}
E2E_TIMEOUT_SECONDS=${E2E_TIMEOUT_SECONDS:-600}
provider_pid=
provider_log=/tmp/minikube-cluster-scaler-e2e-$$.log
provider_bin=/tmp/minikube-cluster-scaler-e2e-provider-$$
owns_profile=false

profiles=$(minikube profile list -o json | tr -d '[:space:]')
if printf '%s\n' "$profiles" | grep -Fq "\"Name\":\"$PROFILE\""; then
  printf 'refusing to use existing minikube profile: %s\n' "$PROFILE" >&2
  exit 1
fi

cleanup() {
  if [ -n "$provider_pid" ]; then
    kill "$provider_pid" 2>/dev/null || true
    wait "$provider_pid" 2>/dev/null || true
    provider_pid=
  fi
  if [ "$owns_profile" = true ]; then
    PROFILE="$PROFILE" ./scripts/99-cleanup.sh >/dev/null 2>&1 || true
  fi
  rm -f "$provider_log" "$provider_bin"
}
trap cleanup 0
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

wait_for_provider() {
  deadline=$(($(date +%s) + E2E_TIMEOUT_SECONDS))
  while :; do
    if ! kill -0 "$provider_pid" 2>/dev/null; then
      wait "$provider_pid" 2>/dev/null || true
      provider_pid=
      printf '%s\n' 'provider exited before becoming reachable' >&2
      return 1
    fi
    if minikube ssh -p "$PROFILE" -- "nc -z -w 2 host.minikube.internal 9090" >/dev/null 2>&1; then
      return
    fi
    [ "$(date +%s)" -lt "$deadline" ] || return 1
    sleep 2
  done
}

wait_for_ready_nodes() {
  expected=$1
  deadline=$(($(date +%s) + E2E_TIMEOUT_SECONDS))
  while :; do
    nodes=$(kubectl --context "$PROFILE" get nodes --no-headers 2>/dev/null || true)
    count=$(printf '%s\n' "$nodes" | awk 'NF { n++ } END { print n+0 }')
    if [ "$count" = "$expected" ] && kubectl --context "$PROFILE" wait --for=condition=Ready nodes --all --timeout=1s >/dev/null 2>&1; then
      return
    fi
    [ "$(date +%s)" -lt "$deadline" ] || return 1
    sleep 10
  done
}

PROFILE="$PROFILE" ./scripts/01-start-minikube.sh
owns_profile=true
go build -o "$provider_bin" ./cmd/provider
"$provider_bin" --profile "$PROFILE" --node-group minikube-workers --min-nodes 1 --max-nodes 3 --enable-scale-down=true --listen 0.0.0.0:9090 >"$provider_log" 2>&1 &
provider_pid=$!
wait_for_provider
PROFILE="$PROFILE" SCALE_DOWN_DELAY_AFTER_ADD=1m SCALE_DOWN_UNNEEDED_TIME=1m ./scripts/03-deploy-cluster-autoscaler.sh
PROFILE="$PROFILE" ./scripts/04-create-pressure.sh
wait_for_ready_nodes 2
PROFILE="$PROFILE" ./scripts/06-remove-pressure.sh
wait_for_ready_nodes 1
test "$(kubectl --context "$PROFILE" get nodes -l node-role.kubernetes.io/control-plane --no-headers | awk 'NF { n++ } END { print n+0 }')" = 1
grep -q 'scale-down succeeded' "$provider_log"
