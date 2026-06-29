#!/bin/sh
set -eu

PROFILE=${PROFILE:-autoscaling-demo}

kubectl --context "$PROFILE" apply -f deploy/workload-unschedulable.yaml
printf 'Watch pods: kubectl --context %s get pods -w\n' "$PROFILE"
