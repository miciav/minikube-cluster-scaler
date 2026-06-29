#!/bin/sh
set -eu

PROFILE=${PROFILE:-autoscaling-demo}
MINIKUBE_DRIVER=${MINIKUBE_DRIVER:-docker}
MINIKUBE_CNI=${MINIKUBE_CNI:-flannel}
KUBERNETES_VERSION=${KUBERNETES_VERSION:-v1.35.6}
MINIKUBE_CPUS=${MINIKUBE_CPUS:-2}
MINIKUBE_MEMORY=${MINIKUBE_MEMORY:-4g}

minikube start -p "$PROFILE" --driver="$MINIKUBE_DRIVER" --cni="$MINIKUBE_CNI" --nodes=1 --cpus="$MINIKUBE_CPUS" --memory="$MINIKUBE_MEMORY" --kubernetes-version="$KUBERNETES_VERSION"
kubectl --context "$PROFILE" taint nodes --all node-role.kubernetes.io/control-plane- 2>/dev/null || true

printf '%s\n' 'Next: ./scripts/02-run-provider.sh'
