#!/bin/sh
set -eu

cd "$(dirname "$0")/.."
file=${1:-deploy/cluster-autoscaler-rbac.yaml}

awk '
function has(value, item) { return index(value, "\"" item "\"") }
function verify() {
  verbs = rule
  sub(/^.*verbs: \[/, "", verbs)
  sub(/\].*$/, "", verbs)
  required_verbs = has(verbs, "watch") && has(verbs, "list") && has(verbs, "get")

  if (index(rule, "apiGroups: [\"storage.k8s.io\"]"))
    storage = has(rule, "volumeattachments") && required_verbs
  if (index(rule, "apiGroups: [\"resource.k8s.io\"]"))
    resources = has(rule, "resourceclaims") && has(rule, "resourceslices") && has(rule, "deviceclasses") && required_verbs
}
/^  - apiGroups:/ {
  verify()
  rule = $0 "\n"
  next
}
/^---$/ {
  verify()
  rule = ""
  next
}
rule != "" { rule = rule $0 "\n" }
END {
  verify()
  exit !(storage && resources)
}
' "$file"
