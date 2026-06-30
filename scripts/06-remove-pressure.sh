#!/bin/sh
set -eu

cd "$(dirname "$0")/.."

PROFILE=${PROFILE:-autoscaling-demo}

kubectl --context "$PROFILE" delete -f deploy/workload-unschedulable.yaml --ignore-not-found
printf 'Watch nodes scale down: kubectl --context %s get nodes -w\n' "$PROFILE"
