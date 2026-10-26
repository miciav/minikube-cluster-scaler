#!/bin/sh
# Entrypoint of the lab image: maps each `lab` subcommand to a demo script.
set -eu

root=$(cd "$(dirname "$0")/.." && pwd)

usage() {
  cat <<'USAGE'
usage: lab <subcommand> [args]

  check            verify minikube, kubectl and Docker
  start            start the minikube profile
  provider         run the externalgrpc provider (foreground)
  deploy           deploy Cluster Autoscaler
  pressure         create Pods that do not fit (scale-up)
  watch            live view of nodes, Pods and autoscaler decisions
  remove-pressure  delete the pressure Pods (scale-down)
  cleanup          delete the whole minikube profile
  e2e              run the 1 -> 2 -> 1 check in a disposable profile
  wrapper          print the lab wrapper for this image
  help             show this help
USAGE
}

subcommand=${1:-help}
[ "$#" -eq 0 ] || shift
case "$subcommand" in
  help | -h | --help) usage; exit 0 ;;
  check) script=00-check-prereqs.sh ;;
  start) script=01-start-minikube.sh ;;
  provider) script=02-run-provider.sh ;;
  deploy) script=03-deploy-cluster-autoscaler.sh ;;
  pressure) script=04-create-pressure.sh ;;
  watch) exec python3 "$root/scripts/05-watch-demo.py" "$@" ;;
  remove-pressure) script=06-remove-pressure.sh ;;
  cleanup) script=99-cleanup.sh ;;
  e2e) script=e2e-scale-down.sh ;;
  wrapper)
    sed "s|__LAB_IMAGE__|ghcr.io/miciav/minikube-cluster-scaler:${LAB_IMAGE_TAG:-dev}|" "$root/scripts/lab"
    exit 0
    ;;
  *) usage >&2; exit 2 ;;
esac
exec "$root/scripts/$script" "$@"
