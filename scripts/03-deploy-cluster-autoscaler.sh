#!/bin/sh
set -eu

cd "$(dirname "$0")/.."

PROFILE=${PROFILE:-autoscaling-demo}
CA_VERSION=${CA_VERSION:-v1.35.0}

if ! minikube ssh -p "$PROFILE" -- "nc -z -w 2 host.minikube.internal 9090"; then
  printf '%s\n' 'warning: provider port 9090 is not reachable from minikube'
fi

kubectl --context "$PROFILE" apply -f deploy/cluster-autoscaler-rbac.yaml
kubectl --context "$PROFILE" apply -f deploy/cloud-config.yaml
kubectl --context "$PROFILE" apply -f deploy/cluster-autoscaler.yaml
kubectl --context "$PROFILE" -n kube-system set image deployment/cluster-autoscaler cluster-autoscaler="registry.k8s.io/autoscaling/cluster-autoscaler:$CA_VERSION"
kubectl --context "$PROFILE" -n kube-system rollout status deployment/cluster-autoscaler --timeout=120s

printf 'Follow logs: kubectl --context %s -n kube-system logs -f deployment/cluster-autoscaler\n' "$PROFILE"
