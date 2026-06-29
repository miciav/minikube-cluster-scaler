#!/bin/sh
set -eu

PROFILE=${PROFILE:-autoscaling-demo}

while :; do
  clear 2>/dev/null || true
  printf '%s\n' 'Nodes:'
  kubectl --context "$PROFILE" get nodes
  printf '\n%s\n' 'Pods:'
  kubectl --context "$PROFILE" get pods -A -o wide
  printf '\n%s\n' 'Pending pods:'
  kubectl --context "$PROFILE" get pods -A --field-selector=status.phase=Pending
  printf '\nAutoscaler logs: kubectl --context %s -n kube-system logs -f deployment/cluster-autoscaler\n' "$PROFILE"
  sleep 3
done
