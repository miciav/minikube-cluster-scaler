#!/bin/sh
set -eu

PROFILE=${PROFILE:-autoscaling-demo}
MIN_NODES=${MIN_NODES:-1}
MAX_NODES=${MAX_NODES:-3}

printf '%s\n' 'warning: this local teaching demo uses a plaintext provider connection'
exec go run ./cmd/provider --profile "$PROFILE" --node-group minikube-workers --min-nodes "$MIN_NODES" --max-nodes "$MAX_NODES" --listen 0.0.0.0:9090 "$@"
