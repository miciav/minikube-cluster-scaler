#!/bin/sh
set -eu

MINIKUBE_DRIVER=${MINIKUBE_DRIVER:-docker}
KUBERNETES_VERSION=${KUBERNETES_VERSION:-v1.35.6}
CA_VERSION=${CA_VERSION:-v1.35.0}

for command in minikube kubectl go; do
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

if [ -z "$KUBERNETES_VERSION" ] || [ -z "$CA_VERSION" ]; then
  printf '%s\n' 'error: KUBERNETES_VERSION and CA_VERSION must not be empty' >&2
  exit 1
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

minikube version --short
kubectl version --client
go version
printf 'versions: Kubernetes %s, Cluster Autoscaler %s (major.minor %s)\n' "$KUBERNETES_VERSION" "$CA_VERSION" "$kubernetes_minor"
printf 'driver: %s (ready)\n' "$MINIKUBE_DRIVER"

for command in protoc protoc-gen-go protoc-gen-go-grpc; do
  if command -v "$command" >/dev/null 2>&1; then
    printf 'optional: %s found\n' "$command"
  else
    printf 'optional: %s not found (not required)\n' "$command"
  fi
done
