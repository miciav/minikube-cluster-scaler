#!/bin/sh
set -eu

cd "$(dirname "$0")/.."

PROFILE=${PROFILE:-autoscaling-demo}
MIN_NODES=${MIN_NODES:-1}
MAX_NODES=${MAX_NODES:-3}
ENABLE_SCALE_DOWN=${ENABLE_SCALE_DOWN:-true}

printf '%s\n' 'warning: this local teaching demo uses a plaintext provider connection'
exec go run ./cmd/provider --profile "$PROFILE" --node-group minikube-workers --min-nodes "$MIN_NODES" --max-nodes "$MAX_NODES" --enable-scale-down="$ENABLE_SCALE_DOWN" --listen 0.0.0.0:9090 "$@"
