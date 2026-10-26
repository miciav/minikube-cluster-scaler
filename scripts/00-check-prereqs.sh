#!/bin/sh
set -eu

MINIKUBE_DRIVER=${MINIKUBE_DRIVER:-docker}
KUBERNETES_VERSION=${KUBERNETES_VERSION:-v1.35.6}
CA_VERSION=${CA_VERSION:-v1.35.0}
# First minikube release whose NewestKubernetesVersion is v1.35 (pkg/minikube/constants at v1.38.0).
MIN_MINIKUBE_VERSION=v1.38.0

# Inside the lab image the provider is prebuilt and the observer has its
# dependencies, so only the tools mounted from the VM are required.
if [ "${LAB_CONTAINER:-}" = 1 ]; then
  required='minikube kubectl'
else
  required='minikube kubectl go uv'
fi

for command in $required; do
  if ! command -v "$command" >/dev/null 2>&1; then
    printf 'error: required command not found: %s\n' "$command" >&2
    exit 1
  fi
done
if [ "$MINIKUBE_DRIVER" = docker ]; then
  if ! command -v docker >/dev/null 2>&1; then
    printf '%s\n' 'error: Docker driver selected but docker is not installed' >&2
    exit 1
  fi
  if ! docker info >/dev/null 2>&1; then
    printf '%s\n' 'error: Docker driver selected but docker info failed' >&2
    exit 1
  fi
fi

kubernetes_minor=$(printf '%s\n' "$KUBERNETES_VERSION" | sed -E -n 's/^v?([0-9]+\.[0-9]+)(\.[0-9]+([-+][0-9A-Za-z][0-9A-Za-z.-]*)?)?$/\1/p')
ca_minor=$(printf '%s\n' "$CA_VERSION" | sed -E -n 's/^v?([0-9]+\.[0-9]+)(\.[0-9]+([-+][0-9A-Za-z][0-9A-Za-z.-]*)?)?$/\1/p')
if [ -z "$kubernetes_minor" ] || [ -z "$ca_minor" ]; then
  printf 'error: invalid version: Kubernetes %s, Cluster Autoscaler %s\n' "$KUBERNETES_VERSION" "$CA_VERSION" >&2
  exit 1
fi
if [ "$kubernetes_minor" != "$ca_minor" ]; then
  printf 'error: Kubernetes %s and Cluster Autoscaler %s must have the same major.minor version\n' "$KUBERNETES_VERSION" "$CA_VERSION" >&2
  exit 1
fi

# version_part VERSION INDEX prints the INDEX-th (1-based) number of vMAJOR.MINOR.PATCH.
version_part() {
  printf '%s\n' "$1" | sed -E -n "s/^v?([0-9]+)\\.([0-9]+)\\.([0-9]+).*$/\\$2/p"
}

minikube_version=$(minikube version --short)
kubectl_version=$(kubectl version --client 2>/dev/null | sed -n 's/^Client Version: //p')
printf 'minikube: %s\nkubectl: %s\n' "$minikube_version" "$kubectl_version"
minikube_minor=$(version_part "$minikube_version" 2)
if [ "$(version_part "$minikube_version" 1)" != 1 ] || [ "${minikube_minor:-0}" -lt "$(version_part "$MIN_MINIKUBE_VERSION" 2)" ]; then
  printf 'warning: minikube %s may not support Kubernetes %s; install %s or newer\n' "$minikube_version" "$KUBERNETES_VERSION" "$MIN_MINIKUBE_VERSION" >&2
fi
kubectl_minor=$(version_part "$kubectl_version" 2)
kubernetes_minor_number=$(version_part "$KUBERNETES_VERSION" 2)
if [ -z "$kubectl_minor" ] || [ "$kubectl_minor" -lt $((kubernetes_minor_number - 1)) ] || [ "$kubectl_minor" -gt $((kubernetes_minor_number + 1)) ]; then
  printf 'warning: kubectl %s is outside the supported skew of Kubernetes %s (one minor version)\n' "$kubectl_version" "$KUBERNETES_VERSION" >&2
fi
if [ "${LAB_CONTAINER:-}" != 1 ]; then
  go version
fi
printf 'versions: Kubernetes %s, Cluster Autoscaler %s (major.minor %s)\n' "$KUBERNETES_VERSION" "$CA_VERSION" "$kubernetes_minor"
printf 'driver: %s (ready)\n' "$MINIKUBE_DRIVER"
