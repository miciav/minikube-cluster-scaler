# minikube-externalgrpc-autoscaler-demo

> **Teaching demo only — not for production.** The provider is deliberately fake, has no authentication or TLS, and exposes a **plaintext gRPC** endpoint on the host.

This university lecture demo shows the official Kubernetes Cluster Autoscaler (CA) using its `externalgrpc` cloud-provider boundary to turn Pending Pods into a new minikube worker.

## Architecture and responsibilities

```text
Pending Pods
    |
    v
official Cluster Autoscaler in minikube
    | externalgrpc (plaintext)
    v
host.minikube.internal:9090
    |
    v
fake provider on the host
    |
    v
minikube node add
    |
    v
new worker node
```

- The real Cluster Autoscaler makes the scheduling and scale-up decision.
- The official `externalgrpc` API is the cloud-provider boundary.
- The host provider implements one fake node group and translates the increase RPC.
- The minikube CLI performs the actual local node provisioning.

The single initial minikube node is both control plane and worker, and is counted as the first member of `minikube-workers`. Nodes added later are worker-only. This asymmetry is accepted for the lecture because scale-down is disabled.

There is one node group: profile `autoscaling-demo`, group `minikube-workers`, minimum 1, maximum 3. The Docker driver is the tested path; other `MINIKUBE_DRIVER` values are best effort.

Always use a dedicated, disposable `PROFILE` for this demo. Cleanup deletes the entire selected minikube profile, including every workload in it.

## Version policy

Defaults are Kubernetes `v1.35.6` and Cluster Autoscaler `v1.35.0`. Kubernetes and CA must have the same major/minor version; their patch versions need not match. The committed proto and generated Go code must come from the same CA tag as the deployed image.

The current pinned pair can be exported together before running the scripts:

```sh
export KUBERNETES_VERSION=v1.35.6 CA_VERSION=v1.35.0
```

To upgrade, choose Kubernetes and CA releases with the same major/minor and set both variables together. First download or copy `externalgrpc.proto` from the exact chosen CA tag into [`proto/externalgrpc.proto`](proto/externalgrpc.proto), then run `./proto/generate.sh` to regenerate the Go bindings. The generation script uses the local schema; it does not fetch one. Do not change only the CA image tag.

The schema is the official [`externalgrpc.proto` from the `cluster-autoscaler-1.35.0` tag](https://github.com/kubernetes/autoscaler/blob/cluster-autoscaler-1.35.0/cluster-autoscaler/cloudprovider/externalgrpc/protos/externalgrpc.proto). Generated code is committed, so `protoc` is optional unless regenerating it with `./proto/generate.sh`.

## Prerequisites and host resources

Install `minikube`, `kubectl`, and Go 1.25+ (or a Go installation compatible with automatic toolchain selection). The default Docker path also needs a running Docker daemon. Run the prerequisite check before creating the cluster.

Each minikube node defaults to 2 CPUs and 4 GiB of memory. Adding a node increases host CPU, memory, and disk consumption. On a smaller lecture machine, set `MINIKUBE_CPUS` and `MINIKUBE_MEMORY`; the pressure script sizes CPU requests from the node capacity reported by Kubernetes.

## Manual lecture flow

Use two terminals from the repository root. The provider intentionally remains in the foreground so the RPC and `minikube node add` evidence stays visible.

Terminal 1:

```sh
./scripts/00-check-prereqs.sh
./scripts/01-start-minikube.sh
./scripts/02-run-provider.sh
```

Terminal 2, after the provider is listening:

```sh
./scripts/03-deploy-cluster-autoscaler.sh
./scripts/04-create-pressure.sh
./scripts/05-watch-demo.sh
```

To demonstrate decisions without changing minikube, replace the final Terminal 1 command with:

```sh
./scripts/02-run-provider.sh --dry-run
```

Dry-run proves only that CA calls the provider. The provider emits `scale-up request group=minikube-workers ... dry-run=true` and `scale-up succeeded ... dry-run=true`; it emits no minikube command and adds no node, so the pressure Pods stay `Pending`.

In real mode, expected evidence is, in order:

1. At least one pressure Pod becomes `Pending`.
2. CA logs show an unschedulable Pod and a scale-up decision.
3. Provider logs show `scale-up request group=minikube-workers ... dry-run=false`.
4. The command log shows `exec: minikube ["node" "add" "-p" "autoscaling-demo"]`.
5. Provider logs show `scale-up succeeded ... dry-run=false`.
6. `kubectl` shows a second node becoming `Ready`.
7. Pressure Pods become `Running`.

Useful inspection commands:

```sh
kubectl --context autoscaling-demo get nodes -w
kubectl --context autoscaling-demo get pods -A -o wide
kubectl --context autoscaling-demo get pods -A --field-selector=status.phase=Pending
kubectl --context autoscaling-demo -n kube-system logs -f deployment/cluster-autoscaler
kubectl --context autoscaling-demo describe deployment/autoscaler-pressure
kubectl --context autoscaling-demo get events -A --sort-by=.lastTimestamp
```

## Configuration

Scripts read these environment variables:

| Variable | Default | Purpose |
| --- | --- | --- |
| `PROFILE` | `autoscaling-demo` | dedicated disposable minikube profile and kubectl context |
| `MINIKUBE_DRIVER` | `docker` | minikube driver; Docker is tested |
| `KUBERNETES_VERSION` | `v1.35.6` | minikube Kubernetes version |
| `CA_VERSION` | `v1.35.0` | Cluster Autoscaler image tag |
| `MIN_NODES` | `1` | provider node-group minimum |
| `MAX_NODES` | `3` | provider node-group maximum |
| `MINIKUBE_CPUS` | `2` | CPUs per minikube node |
| `MINIKUBE_MEMORY` | `4g` | memory per minikube node |

`./scripts/02-run-provider.sh` forwards extra arguments to the provider. Its CLI flags are:

| Flag | Default | Purpose |
| --- | --- | --- |
| `--listen` | `0.0.0.0:9090` | gRPC listen address; all interfaces are needed for host reachability |
| `--profile` | `autoscaling-demo` | minikube profile |
| `--node-group` | `minikube-workers` | single node-group ID |
| `--min-nodes` | `1` | minimum node count |
| `--max-nodes` | `3` | maximum node count |
| `--dry-run` | `false` | simulate increases without adding nodes |
| `--enable-scale-down` | `false` | expose the future scale-down boundary |
| `--v` | `1` | accepted, parsed, and reported; didactic operation logs are currently unconditional |

## Automated verification

These checks do not run the live demo:

```sh
go test ./...
go test -race ./...
go vet ./...
go build -o /tmp/minikube-externalgrpc-provider ./cmd/provider
for script in scripts/*.sh proto/generate.sh; do sh -n "$script"; done
```

When a Kubernetes cluster is connected, validate the manifests without applying them:

```sh
kubectl apply --dry-run=client -f deploy/cluster-autoscaler-rbac.yaml
kubectl apply --dry-run=client -f deploy/cloud-config.yaml
kubectl apply --dry-run=client -f deploy/cluster-autoscaler.yaml
kubectl apply --dry-run=client -f deploy/workload-unschedulable.yaml
```

## Troubleshooting

- **Version mismatch:** export `KUBERNETES_VERSION` and `CA_VERSION` together with the same major/minor, then rerun `./scripts/00-check-prereqs.sh`.
- **Docker or resource failure:** verify `docker info`, free host resources, or lower `MINIKUBE_CPUS`/`MINIKUBE_MEMORY`. Remember that every added node consumes more host resources.
- **Port 9090 or host reachability:** ensure nothing else owns port 9090. The provider must bind `0.0.0.0:9090`, not loopback, so `host.minikube.internal:9090` is reachable. `./scripts/03-deploy-cluster-autoscaler.sh` performs a best-effort probe.
- **No scale-up:** inspect CA logs and Pending Pod events with the commands above. Confirm the provider is still running in Terminal 1.
- **No Pending Pod, or Pods never fit:** inspect the allocatable CPU reported by `kubectl get nodes`; the pressure script sizes each request to one third of the first node.

## Cleanup

Stop the foreground provider with Ctrl-C, then run:

```sh
./scripts/99-cleanup.sh
```

This deletes the entire selected `PROFILE` and every workload in it, not just resources created by the demo. Use only the dedicated disposable profile described above.

## TODO: future scale-down

Scale-down is not implemented. Even with `--enable-scale-down=true`, delete and decrease RPCs currently return gRPC `Unimplemented`.

A safe implementation must map Kubernetes node names to minikube node identities, validate removal, cordon and drain, then call `minikube node delete`. It must deliberately handle DaemonSets, local storage, PodDisruptionBudgets, and non-evictable Pods before removing capacity.

No live end-to-end result is claimed by this repository documentation; run the manual flow on the lecture machine to collect it.
