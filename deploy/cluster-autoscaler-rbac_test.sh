#!/bin/sh
set -eu

cd "$(dirname "$0")/.."
for resource in resourceclaims resourceslices deviceclasses volumeattachments; do
  grep -q "\"$resource\"" deploy/cluster-autoscaler-rbac.yaml
done
