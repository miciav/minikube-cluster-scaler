#!/bin/sh
# Stub bodies expand their variables when the stub runs, not here.
# shellcheck disable=SC2016
set -eu

cd "$(dirname "$0")/.."
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

stub() {
  printf '#!/bin/sh\n%s\n' "$2" >"$tmp/$1"
  chmod +x "$tmp/$1"
}
stub docker 'exit 0'
stub minikube 'printf "%s\n" "${MINIKUBE_STUB_VERSION:-v1.39.0}"'
stub kubectl 'printf "Client Version: %s\n" "${KUBECTL_STUB_VERSION:-v1.35.6}"'
# Only the stubs and the base system: no go, uv or protoc.
path="$tmp:/usr/bin:/bin"

PATH="$path" LAB_CONTAINER=1 ./scripts/00-check-prereqs.sh >"$tmp/out" 2>"$tmp/err"
if grep -q 'warning:' "$tmp/err"; then exit 1; fi

PATH="$path" LAB_CONTAINER=1 KUBECTL_STUB_VERSION=v1.30.0 ./scripts/00-check-prereqs.sh >/dev/null 2>"$tmp/err"
grep -q 'warning: kubectl' "$tmp/err"

PATH="$path" LAB_CONTAINER=1 MINIKUBE_STUB_VERSION=v1.37.0 ./scripts/00-check-prereqs.sh >/dev/null 2>"$tmp/err"
grep -q 'warning: minikube' "$tmp/err"

status=0
PATH="$path" ./scripts/00-check-prereqs.sh >/dev/null 2>"$tmp/err" || status=$?
test "$status" -ne 0
grep -q 'required command not found: go' "$tmp/err"
