#!/bin/sh
set -eu

cd "$(dirname "$0")/.."

PROFILE=${PROFILE:-autoscaling-demo}

allocatable_cpu=$(kubectl --context "$PROFILE" get nodes -o jsonpath='{.items[0].status.allocatable.cpu}')
case "$allocatable_cpu" in
  *m) allocatable_millicpu=${allocatable_cpu%m} ;;
  *) allocatable_millicpu=$((allocatable_cpu * 1000)) ;;
esac
cpu_request=$(((allocatable_millicpu + 2) / 3))

kubectl --context "$PROFILE" apply -f deploy/workload-unschedulable.yaml
kubectl --context "$PROFILE" set resources deployment/autoscaler-pressure -c=pause \
  --requests="cpu=${cpu_request}m,memory=64Mi" --limits="cpu=${cpu_request}m,memory=64Mi"
printf 'Watch pods: kubectl --context %s get pods -w\n' "$PROFILE"
