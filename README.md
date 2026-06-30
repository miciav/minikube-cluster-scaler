# minikube-cluster-scaler

`minikube-cluster-scaler` is a host-side implementation of the official Kubernetes Cluster Autoscaler [`externalgrpc`](https://github.com/kubernetes/autoscaler/tree/master/cluster-autoscaler/cloudprovider/externalgrpc) cloud-provider API.

It exposes a single minikube node group over gRPC and translates capacity requests into:

```sh
minikube node add -p <profile>
minikube node delete <node-name> -p <profile>
```

> **Demonstration only — not for production.** The provider has no authentication or TLS, exposes a plaintext gRPC endpoint, models only one node group, and can delete minikube workers from the selected profile.

## Provider architecture

```text
Kubernetes Cluster Autoscaler
        |
        | externalgrpc
        v
host.minikube.internal:9090
        |
        v
minikube-cluster-scaler on the host
        |                         |
        | kubectl get nodes       | minikube node add/delete
        v                         v
observed node-group state     minikube workers
```

Cluster Autoscaler retains all scheduling and autoscaling logic. This provider implements only the cloud-provider boundary required to observe and resize local minikube capacity.

## Implemented externalgrpc behavior

| RPC | Behavior |
| --- | --- |
| `NodeGroups` | Returns one group, `minikube-workers` by default. |
| `NodeGroupForNode` | Maps nodes observed in the configured minikube profile to that group. |
| `Refresh` | Refreshes provider state with `kubectl get nodes`. |
| `NodeGroupTargetSize` | Returns the observed or dry-run target size. |
| `NodeGroupNodes` | Returns the known running instances. |
| `NodeGroupIncreaseSize` | Enforces bounds and executes one `minikube node add` per requested node. |
| `NodeGroupTemplateNodeInfo` | Returns a protobuf-encoded worker template derived from the current node's allocatable resources. |
| `NodeGroupDeleteNodes` | Deletes exactly one validated worker by Kubernetes name or ProviderID, subject to minimum-size and control-plane protection. |
| `NodeGroupDecreaseTargetSize` | Returns `FailedPrecondition` while scale-down is disabled and `Unimplemented` when enabled. |
| `GPULabel`, `GetAvailableGPUTypes`, `Cleanup` | Return minimal successful responses. |
| Pricing and node-group options | Remain officially `Unimplemented`. |

Capacity operations are serialized. Before changing capacity, the provider refreshes the observed nodes and checks the configured bounds. After every successful real node addition or deletion it refreshes state again. Caller cancellation is propagated through gRPC, while command failures include stdout and stderr.

The initial minikube node can act as both control plane and worker. It is counted as the first group member; nodes added later are worker-only. Scale-down rejects deletion of the control-plane node and any deletion that would reduce the group below `--min-nodes`.

`NodeGroupDeleteNodes` accepts exactly one node per request. It resolves the externalgrpc node from either `Name` or `ProviderID`, rejects unknown nodes and the control-plane node, and executes `minikube node delete <node-name> -p <profile>` in real mode. Dry-run mode updates only the provider's simulated node list and target size; all validation and protection rules still apply.

## Build and run the provider

Requirements:

- Go 1.25+ or compatible automatic toolchain selection
- `minikube`
- `kubectl`
- an existing minikube profile

Run directly:

```sh
go run ./cmd/provider \
  --profile autoscaling-demo \
  --node-group minikube-workers \
  --min-nodes 1 \
  --max-nodes 3 \
  --listen 0.0.0.0:9090
```

Or build a binary:

```sh
make build
/tmp/minikube-externalgrpc-provider --profile autoscaling-demo
```

### Provider flags

| Flag | Default | Purpose |
| --- | --- | --- |
| `--listen` | `0.0.0.0:9090` | gRPC listen address. All interfaces are required for minikube host access. |
| `--profile` | `autoscaling-demo` | minikube profile and kubectl context. |
| `--node-group` | `minikube-workers` | Node-group ID exposed to Cluster Autoscaler. |
| `--min-nodes` | `1` | Minimum accepted group size. |
| `--max-nodes` | `3` | Maximum accepted group size. |
| `--dry-run` | `false` | Simulates target changes without invoking minikube add or delete commands. |
| `--enable-scale-down` | `false` | Enables validated worker deletion through `NodeGroupDeleteNodes`. |
| `--v` | `1` | Accepted and reported; operation logs are currently unconditional. |

Typical scale-up logs are intentionally explicit:

```text
scale-up request group=minikube-workers ... dry-run=false
exec: minikube ["node" "add" "-p" "autoscaling-demo"]
scale-up succeeded group=minikube-workers ... target=2 dry-run=false
```

## Cluster Autoscaler integration

Cluster Autoscaler connects to the provider through this cloud configuration:

```yaml
address: host.minikube.internal:9090
grpc_timeout: 10m
```

Required Cluster Autoscaler arguments:

```text
--cloud-provider=externalgrpc
--cloud-config=/etc/cluster-autoscaler/cloud-config.yaml
--scale-down-enabled=true
--max-scale-down-parallelism=1
--scale-down-delay-after-add=$(SCALE_DOWN_DELAY_AFTER_ADD)
--scale-down-unneeded-time=$(SCALE_DOWN_UNNEEDED_TIME)
```

The provider must listen on `0.0.0.0`, not only `127.0.0.1`, because the Cluster Autoscaler Pod reaches the host through `host.minikube.internal`.

The repository includes the required manifests in [`deploy/`](deploy/):

- Cluster Autoscaler RBAC, including Kubernetes 1.35 informer permissions
- externalgrpc cloud configuration
- Cluster Autoscaler Deployment
- a pressure workload used by the demo

## Protocol and version policy

The committed schema and generated bindings come from the official Cluster Autoscaler `cluster-autoscaler-1.35.0` tag:

- Kubernetes: `v1.35.6`
- Cluster Autoscaler: `v1.35.0`
- [`externalgrpc.proto`](https://github.com/kubernetes/autoscaler/blob/cluster-autoscaler-1.35.0/cluster-autoscaler/cloudprovider/externalgrpc/protos/externalgrpc.proto): same CA tag

Kubernetes and Cluster Autoscaler must use the same major/minor version; patch versions do not need to match.

When upgrading:

1. choose Kubernetes and Cluster Autoscaler releases with the same major/minor version;
2. copy `externalgrpc.proto` from the exact Cluster Autoscaler tag into [`proto/externalgrpc.proto`](proto/externalgrpc.proto);
3. install the generator versions checked by [`proto/generate.sh`](proto/generate.sh);
4. run `./proto/generate.sh`;
5. update `KUBERNETES_VERSION`, `CA_VERSION`, manifests, and tests together.

Generated Go files are committed, so protobuf tooling is not required to build or run the provider.

## Development

```sh
make test       # Go tests and shell regression checks
make race       # Go race detector
make vet        # Go static checks
make build      # provider binary in /tmp
make shell-test # shell regressions only
make tui-test   # Rich observer tests only
```

Validate shell syntax:

```sh
for script in scripts/*.sh deploy/*_test.sh proto/generate.sh; do
  sh -n "$script"
done
```

## Minikube scale-up and scale-down demo

The repository contains a complete local scenario:

```text
Pending Pods
    |
    v
official Cluster Autoscaler in minikube
    | externalgrpc
    v
provider on the host
    |
    v
minikube node add
    |
    v
new Ready worker -> Pending Pods become Running
    |
    v
remove pressure -> minikube node delete -> protected control plane remains
```

Always use a dedicated, disposable `PROFILE`. The cleanup script deletes the entire selected minikube profile and every workload in it.

### Prerequisites and resources

Install `minikube`, `kubectl`, Go, [`uv`](https://docs.astral.sh/uv/), and Docker for the tested driver path. Then run:

```sh
./scripts/00-check-prereqs.sh
```

Each node defaults to 2 CPUs and 4 GiB. Added nodes consume additional host CPU, memory, and disk. The pressure script derives each Pod's CPU request from the first node's allocatable CPU, ensuring that four replicas require more than one node but fit across two equivalent nodes.

### Run

Terminal 1:

```sh
./scripts/01-start-minikube.sh
./scripts/02-run-provider.sh
```

Terminal 2, after the provider is listening:

```sh
./scripts/03-deploy-cluster-autoscaler.sh
./scripts/04-create-pressure.sh
make watch
```

The Rich observer shows nodes, pressure-workload Pods, Cluster Autoscaler decisions, Kubernetes events, provider reachability, and the inferred scaling phase. It is read-only and intentionally excludes provider internal logs; keep Terminal 1 visible when those logs are needed.

Run it directly to select a profile or refresh interval:

```sh
PROFILE=autoscaling-demo uv run --script scripts/05-watch-demo.py --interval 2
```

Use `--once` for one non-interactive snapshot. Press `Ctrl-C` to exit continuous mode.

Expected real-mode sequence:

1. pressure Pods become `Pending`;
2. Cluster Autoscaler creates a `minikube-workers 1 -> 2` scale-up plan;
3. the provider receives the request and executes `minikube node add`;
4. a second node becomes `Ready`;
5. all pressure Pods become `Running`.

Remove the pressure workload:

```sh
./scripts/06-remove-pressure.sh
```

Cluster Autoscaler selects one unneeded worker at a time, and the provider validates and applies repeated `N -> N-1` deletions. The sequence stops at one Ready node because both the configured minimum and the control-plane label protect the initial node.

To run the complete `1 -> 2 -> 1` check in a disposable profile:

```sh
./scripts/e2e-scale-down.sh
```

### Dry-run

Start the provider with:

```sh
./scripts/02-run-provider.sh --dry-run
```

Cluster Autoscaler can still call the provider, but no minikube command is executed. Provider logs include `dry-run=true`; scale-up and scale-down update only simulated provider state.

### Demo configuration

| Variable | Default | Purpose |
| --- | --- | --- |
| `PROFILE` | `autoscaling-demo` | Dedicated disposable minikube profile and kubectl context. |
| `MINIKUBE_DRIVER` | `docker` | minikube driver; Docker is tested. |
| `MINIKUBE_CNI` | `flannel` | CNI that configures workers added at runtime. |
| `KUBERNETES_VERSION` | `v1.35.6` | minikube Kubernetes version. |
| `CA_VERSION` | `v1.35.0` | Cluster Autoscaler image tag. |
| `MIN_NODES` | `1` | Provider node-group minimum. |
| `MAX_NODES` | `3` | Provider node-group maximum. |
| `ENABLE_SCALE_DOWN` | `true` | Script setting passed to the provider's `--enable-scale-down` flag. |
| `SCALE_DOWN_DELAY_AFTER_ADD` | `1m` | Delay before Cluster Autoscaler considers scale-down after adding a node. |
| `SCALE_DOWN_UNNEEDED_TIME` | `1m` | Time a node must remain unneeded before scale-down. |
| `MINIKUBE_CPUS` | `2` | Requested CPUs per minikube node. |
| `MINIKUBE_MEMORY` | `4g` | Requested memory per minikube node. |

### Inspect

```sh
kubectl --context autoscaling-demo get nodes -w
kubectl --context autoscaling-demo get pods -A -o wide
kubectl --context autoscaling-demo get pods -A --field-selector=status.phase=Pending
kubectl --context autoscaling-demo -n kube-system logs -f deployment/cluster-autoscaler
kubectl --context autoscaling-demo get events -A --sort-by=.lastTimestamp
```

### Troubleshooting

- **Version mismatch:** update Kubernetes, Cluster Autoscaler, and the externalgrpc bindings together.
- **Provider unreachable:** confirm port 9090 is free, the provider listens on `0.0.0.0`, and `host.minikube.internal:9090` is reachable.
- **No scale-up:** inspect Cluster Autoscaler logs and Pending Pod events; confirm the provider remains running.
- **No scale-down:** confirm `ENABLE_SCALE_DOWN=true`, remove the pressure workload, and inspect Cluster Autoscaler and provider logs.
- **Worker remains `NotReady`:** use the default flannel CNI or another CNI that configures nodes added at runtime.
- **No Pending Pods:** inspect allocatable CPU; `04-create-pressure.sh` adjusts requests from the first node.
- **Host resource failure:** free Docker resources or reduce `MINIKUBE_CPUS` and `MINIKUBE_MEMORY`.

### Cleanup

Stop the foreground provider with `Ctrl-C`, then run:

```sh
./scripts/99-cleanup.sh
```

This removes the entire selected `PROFILE`, not only the resources created by the scripts.

## Scale-down limits

`NodeGroupDeleteNodes` implements the externalgrpc deletion boundary for this local demo. It accepts one observed worker by `Name` or `ProviderID`, protects the control plane and minimum size, and confirms real deletions through a fresh Kubernetes observation. Cluster Autoscaler remains responsible for deciding that a node is unneeded and for its normal eviction checks.

`NodeGroupDecreaseTargetSize` remains `Unimplemented` when scale-down is enabled because minikube node creation is synchronous: there is no requested-but-unprovisioned capacity to cancel. This narrow implementation does not make the plaintext, single-group host provider suitable for production.
