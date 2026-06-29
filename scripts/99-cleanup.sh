#!/bin/sh
set -eu

PROFILE=${PROFILE:-autoscaling-demo}

printf 'Deleting demo resources and minikube profile: %s\n' "$PROFILE"
kubectl --context "$PROFILE" delete -f deploy/workload-unschedulable.yaml --ignore-not-found 2>/dev/null || true
kubectl --context "$PROFILE" delete -f deploy/cluster-autoscaler.yaml -f deploy/cloud-config.yaml -f deploy/cluster-autoscaler-rbac.yaml --ignore-not-found 2>/dev/null || true
minikube delete -p "$PROFILE"
