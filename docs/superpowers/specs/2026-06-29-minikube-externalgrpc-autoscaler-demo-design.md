# Minikube External gRPC Autoscaler Demo Design

## Purpose

Build a small teaching demo in which the official Kubernetes Cluster Autoscaler runs inside minikube, calls a host-side provider through the official `externalgrpc` API, and causes `minikube node add` to provision capacity for Pending Pods.

The first milestone ends when Cluster Autoscaler calls `NodeGroupIncreaseSize`, the provider adds a minikube node, and Pending Pods become schedulable. This is not a production cloud provider.

## Scope

Part A implements one node group named `minikube-workers`, scale-up, dry-run, deployment manifests, orchestration scripts, automated unit tests, and a documented manual end-to-end test.

Part B is deliberately limited to a disabled-by-default boundary. The provider exposes `--enable-scale-down=false`; delete and decrease operations return a clear disabled error. If the flag is enabled, they return an unimplemented error because node removal is outside the first milestone.

The demo does not add CRDs, a Kubernetes controller, a database, a web UI, multiple node groups, Docker API integration, or production security controls.

## Architecture

```text
+--------------------------------------------------+
|                minikube cluster                  |
|                                                  |
|  Pending Pods                                    |
|       |                                          |
|       v                                          |
|  official Cluster Autoscaler                     |
|       | externalgrpc                             |
+-------|------------------------------------------+
        |
        | plaintext gRPC to
        | host.minikube.internal:9090
        v
+--------------------------------------------------+
|                  host machine                    |
|                                                  |
|  fake-minikube-externalgrpc-provider             |
|       |                                          |
|       v                                          |
|  minikube node add -p autoscaling-demo           |
|       |                                          |
|       v                                          |
|  new minikube worker joins the cluster           |
+--------------------------------------------------+
```

The initial cluster has one node that acts as both control plane and worker. For this resource-constrained teaching setup, that hybrid node is counted as the first member of `minikube-workers`. Nodes added later are worker-only nodes. This asymmetry is explicit and acceptable because scale-down is not implemented; it avoids requiring two machines before demonstrating scale-up.

The Docker driver is the tested path on macOS and Linux. `MINIKUBE_DRIVER` remains configurable, and other drivers are best effort. The provider contains no Docker-specific code.

## Versioning

Cluster Autoscaler and Kubernetes must share the same minor version. The defaults are Kubernetes `v1.35.6` and Cluster Autoscaler `v1.35.0`, the latest releases in the matching supported minor at design time. They are pinned rather than following the Kubernetes version selected implicitly by the newest minikube. Users can update the pair through environment variables, and scripts reject a minor mismatch; patch versions do not need to match.

The external gRPC schema and generated Go files come from the exact Cluster Autoscaler release tag used by the deployment image. They are committed to the repository so running the demo does not require `protoc`. `proto/generate.sh` documents and automates regeneration for maintainers who have the protobuf tools installed.

This repository uses the official proto package `clusterautoscaler.cloudprovider.v1.externalgrpc`; it does not redefine the protocol. There is no protocol handshake, so pinning the image and schema together is the compatibility mechanism.

## Components

### Provider executable

`cmd/provider/main.go` parses these flags and starts the gRPC server:

- `--listen=0.0.0.0:9090`
- `--profile=autoscaling-demo`
- `--node-group=minikube-workers`
- `--min-nodes=1`
- `--max-nodes=3`
- `--dry-run=false`
- `--enable-scale-down=false`
- `--v=1`

Startup validates the node bounds and required names before listening. Logs show RPC names, group size decisions, external commands, dry-run actions, and failures.

### Command client

`pkg/minikube/client.go` is a small wrapper over `exec.CommandContext`. It accepts a replaceable runner for tests, invokes commands with argument arrays, applies timeouts, captures stdout and stderr, and returns errors containing the command context and captured output.

The client uses:

- `kubectl --context <profile> get nodes -o json` to observe cluster nodes;
- `minikube node add -p <profile>` to add each requested node.

It never invokes a shell or Docker directly.

### gRPC provider

`pkg/provider/provider.go` owns the single node-group behavior. Kubernetes nodes returned by the configured context belong to `minikube-workers`, including the initial hybrid node. A node not present in that cluster returns an empty group response.

The required RPC behavior is:

- `NodeGroups`: return the one stable group with configured min/max values.
- `NodeGroupForNode`: map known profile nodes to the group; return an empty group otherwise.
- `Refresh`: reload current nodes from Kubernetes.
- `NodeGroupTargetSize`: return the observed node count, or the simulated count in dry-run mode.
- `NodeGroupNodes`: return known nodes as running instances.
- `NodeGroupIncreaseSize`: require a positive delta, reject a result above max, log the request, and invoke one `minikube node add` per requested node.
- `NodeGroupDeleteNodes` and `NodeGroupDecreaseTargetSize`: return gRPC `FailedPrecondition` while scale-down is disabled and `Unimplemented` when explicitly enabled.
- `NodeGroupTemplateNodeInfo`: return protobuf bytes for a realistic Kubernetes Node template.
- `GPULabel` and `GetAvailableGPUTypes`: return empty successful responses because the demo has no GPU model.
- `Cleanup`: return success without work.

The officially optional pricing and node-group-options RPCs remain `Unimplemented` through the generated server embedding.

## Node template

Cluster Autoscaler needs a schedulable worker shape. The provider derives capacity and allocatable resources from the current minikube node instead of hard-coding a machine size. It builds a fresh Node value that:

- copies capacity and allocatable resources;
- retains only scheduling-relevant stable labels;
- omits hostname and control-plane role labels;
- has no control-plane taints;
- contains no identity, addresses, provider ID, or runtime status.

The Node is serialized with Kubernetes protobuf encoding into the protocol's `nodeBytes` field. The result models nodes created by `minikube node add` closely enough for Cluster Autoscaler's scheduling simulation.

## Scale-up state and errors

Normal mode treats Kubernetes as the source of truth. Before an increase, the provider reads the current nodes and checks `current + delta <= max`. Each successful `node add` is followed by a refresh; an external command failure stops the operation and is returned to Cluster Autoscaler.

Dry-run mode must not execute minikube. A successful increase advances an in-memory simulated target so repeated requests still exercise bounds checking. A refresh keeps the simulated target at least as large as the observed cluster but does not pretend that a real node joined.

Invalid group IDs return `NotFound`. Invalid deltas and bound violations return `InvalidArgument` or `FailedPrecondition` with readable messages. Command timeouts and failures return `Internal` with useful host-side logs.

## Deployment

The manifests create the Cluster Autoscaler service account and RBAC, a ConfigMap containing:

```yaml
address: host.minikube.internal:9090
grpc_timeout: 10m
```

and the official Cluster Autoscaler Deployment configured with `--cloud-provider=externalgrpc`, the mounted cloud config, `--scale-down-enabled=false`, and `--v=5`. The long per-RPC timeout allows `NodeGroupIncreaseSize` to wait for minikube to create and register a node.

Plaintext gRPC is an intentional local-demo simplification and is clearly warned about. The provider binds to all interfaces because a process bound only to loopback is not reachable through `host.minikube.internal`.

The pressure workload is a small Deployment with explicit CPU requests. Defaults target a two-CPU minikube node so at least one replica remains Pending initially and fits after a second node joins. Node CPU, memory, replica count, and workload requests are documented and adjustable for the lecture machine.

## Scripts

Scripts use POSIX-oriented shell with `set -eu` and keep configuration in a few environment variables shared by convention: profile, driver, Kubernetes version, Cluster Autoscaler version, CPU, memory, min nodes, and max nodes.

- `00-check-prereqs.sh` checks required executables, version compatibility, Docker when selected, and reports optional protobuf tools.
- `01-start-minikube.sh` creates the one-node profile with configurable resources and the pinned Kubernetes version.
- `02-run-provider.sh` runs the provider on the host.
- `03-deploy-cluster-autoscaler.sh` verifies host connectivity where practical and applies RBAC, config, and Deployment.
- `04-create-pressure.sh` applies the workload.
- `05-watch-demo.sh` displays nodes, Pods, Pending Pods, and the log command without implementing a custom dashboard.
- `99-cleanup.sh` deletes only the demo resources and the explicitly named profile.

The Makefile is a thin command index for build, test, generation, and demo steps. It contains no duplicate orchestration logic.

## Testing and verification

Implementation follows red-green-refactor for behavior-bearing Go code. Tests use a fake command runner and real provider methods rather than starting minikube.

Automated coverage includes:

- command argument construction, timeout/error propagation, and captured output;
- group mapping and target-size refresh;
- rejection beyond max size;
- dry-run not executing `minikube node add`;
- one command per requested node;
- disabled and unimplemented scale-down responses;
- worker template labels, taints, capacity, and protobuf encoding.

Phase checks are `go test ./...`, `go build ./cmd/provider`, shell syntax checks, and `kubectl apply --dry-run=client` for manifests. The final end-to-end check uses the dedicated `autoscaling-demo` profile when Docker and host resources are available, then runs the cleanup script.

## Future scale-down boundary

A future Part B requires a deliberate node identity mapping, safety validation, cordon, drain, `minikube node delete`, and handling for DaemonSets, local storage, PodDisruptionBudgets, and non-evictable Pods. None of that logic belongs in Part A.
