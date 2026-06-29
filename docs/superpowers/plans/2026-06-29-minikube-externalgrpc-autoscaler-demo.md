# Minikube External gRPC Autoscaler Demo Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a reproducible teaching demo where official Cluster Autoscaler v1.35.0 asks a host-side external gRPC provider to add minikube nodes for Pending Pods.

**Architecture:** A Go gRPC server implements the official externalgrpc v1.35.0 protocol and observes one minikube profile through `kubectl`. It maps the initial hybrid node and added workers into one node group, and implements scale-up by executing `minikube node add`; scale-down stays disabled.

**Tech Stack:** Go 1.25+, gRPC-Go, protobuf, Kubernetes Go API v0.35.0, minikube, kubectl, POSIX shell, Kubernetes YAML.

---

## File map

- `go.mod`, `go.sum`: Go module and pinned dependencies.
- `proto/externalgrpc.proto`: exact official v1.35.0 schema.
- `proto/externalgrpc.pb.go`, `proto/externalgrpc_grpc.pb.go`: committed generated bindings.
- `proto/generate.sh`: optional reproducible regeneration.
- `pkg/minikube/client.go`: timed `kubectl` and `minikube` execution.
- `pkg/minikube/client_test.go`: command-wrapper tests.
- `pkg/provider/provider.go`: one-node-group externalgrpc implementation.
- `pkg/provider/provider_test.go`: provider behavior tests.
- `cmd/provider/main.go`: flags, logging, listener, graceful shutdown.
- `cmd/provider/main_test.go`: flag/default validation.
- `deploy/cluster-autoscaler-rbac.yaml`: tagged upstream RBAC.
- `deploy/cloud-config.yaml`: externalgrpc address and timeout ConfigMap.
- `deploy/cluster-autoscaler.yaml`: official autoscaler Deployment.
- `deploy/workload-unschedulable.yaml`: CPU-pressure Deployment.
- `scripts/*.sh`: linear demo lifecycle.
- `Makefile`: thin aliases only.
- `README.md`: lecture architecture, commands, expected evidence, limitations.

### Task 1: Pin the official protocol and Go module

**Files:**
- Create: `go.mod`
- Create: `go.sum`
- Create: `proto/externalgrpc.proto`
- Create: `proto/externalgrpc.pb.go`
- Create: `proto/externalgrpc_grpc.pb.go`
- Create: `proto/generate.sh`

- [ ] **Step 1: Create the module and copy the exact tagged upstream artifacts**

Run:

```bash
go mod init example.com/minikube-externalgrpc-autoscaler-demo
curl -fSLo proto/externalgrpc.proto https://raw.githubusercontent.com/kubernetes/autoscaler/cluster-autoscaler-1.35.0/cluster-autoscaler/cloudprovider/externalgrpc/protos/externalgrpc.proto
curl -fSLo proto/externalgrpc.pb.go https://raw.githubusercontent.com/kubernetes/autoscaler/cluster-autoscaler-1.35.0/cluster-autoscaler/cloudprovider/externalgrpc/protos/externalgrpc.pb.go
curl -fSLo proto/externalgrpc_grpc.pb.go https://raw.githubusercontent.com/kubernetes/autoscaler/cluster-autoscaler-1.35.0/cluster-autoscaler/cloudprovider/externalgrpc/protos/externalgrpc_grpc.pb.go
```

Expected: three non-empty files whose package declaration is `package protos`.

- [ ] **Step 2: Add the optional generation script**

Create `proto/generate.sh`:

```sh
#!/bin/sh
set -eu

command -v protoc >/dev/null
command -v protoc-gen-go >/dev/null
command -v protoc-gen-go-grpc >/dev/null

cd "$(dirname "$0")"
protoc --go_out=paths=source_relative:. --go-grpc_out=paths=source_relative:. externalgrpc.proto
```

- [ ] **Step 3: Resolve only the generated-code dependencies**

Run:

```bash
chmod +x proto/generate.sh
go mod tidy
go test ./proto
```

Expected: `? example.com/minikube-externalgrpc-autoscaler-demo/proto [no test files]`.

- [ ] **Step 4: Verify the protocol version marker**

Run:

```bash
rg 'package clusterautoscaler.cloudprovider.v1.externalgrpc|bytes nodeBytes = 2' proto/externalgrpc.proto
```

Expected: both official schema lines are printed.

- [ ] **Step 5: Commit**

```bash
git add go.mod go.sum proto
git commit -m "build: pin externalgrpc protocol v1.35.0"
```

### Task 2: Build the command client with TDD

**Files:**
- Create: `pkg/minikube/client_test.go`
- Create: `pkg/minikube/client.go`

- [ ] **Step 1: Write failing command and decoding tests**

Create `pkg/minikube/client_test.go` with these behaviors:

```go
package minikube

import (
	"context"
	"errors"
	"io"
	"log"
	"strings"
	"testing"
	"time"
)

func TestNodesUsesProfileContextAndDecodesNodes(t *testing.T) {
	var gotName string
	var gotArgs []string
	c := New("demo", time.Second, log.New(io.Discard, "", 0), func(_ context.Context, name string, args ...string) ([]byte, []byte, error) {
		gotName, gotArgs = name, args
		return []byte(`{"items":[{"metadata":{"name":"demo"}}]}`), nil, nil
	})
	nodes, err := c.Nodes(context.Background())
	if err != nil || len(nodes) != 1 || nodes[0].Name != "demo" {
		t.Fatalf("Nodes() = %#v, %v", nodes, err)
	}
	if gotName != "kubectl" || strings.Join(gotArgs, " ") != "--context demo get nodes -o json" {
		t.Fatalf("command = %s %v", gotName, gotArgs)
	}
}

func TestAddNodeUsesArgumentArray(t *testing.T) {
	var command string
	c := New("demo", time.Second, log.New(io.Discard, "", 0), func(_ context.Context, name string, args ...string) ([]byte, []byte, error) {
		command = name + " " + strings.Join(args, " ")
		return nil, nil, nil
	})
	if err := c.AddNode(context.Background()); err != nil {
		t.Fatal(err)
	}
	if command != "minikube node add -p demo" {
		t.Fatalf("command = %q", command)
	}
}

func TestCommandErrorIncludesStderr(t *testing.T) {
	c := New("demo", time.Second, log.New(io.Discard, "", 0), func(context.Context, string, ...string) ([]byte, []byte, error) {
		return nil, []byte("node creation failed"), errors.New("exit 1")
	})
	if err := c.AddNode(context.Background()); err == nil || !strings.Contains(err.Error(), "node creation failed") {
		t.Fatalf("error = %v", err)
	}
}

func TestCommandTimeout(t *testing.T) {
	c := New("demo", time.Millisecond, log.New(io.Discard, "", 0), func(ctx context.Context, _ string, _ ...string) ([]byte, []byte, error) {
		<-ctx.Done()
		return nil, nil, ctx.Err()
	})
	if err := c.AddNode(context.Background()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v", err)
	}
}
```

- [ ] **Step 2: Run the test and verify RED**

Run: `go test ./pkg/minikube`

Expected: FAIL because `New` does not exist.

- [ ] **Step 3: Implement the smallest client**

Create `pkg/minikube/client.go`:

```go
package minikube

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os/exec"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
)

type RunFunc func(context.Context, string, ...string) ([]byte, []byte, error)

type Client struct {
	profile string
	timeout time.Duration
	run     RunFunc
	log     *log.Logger
}

func New(profile string, timeout time.Duration, logger *log.Logger, run RunFunc) *Client {
	if run == nil {
		run = runCommand
	}
	return &Client{profile: profile, timeout: timeout, run: run, log: logger}
}

func (c *Client) Nodes(ctx context.Context) ([]corev1.Node, error) {
	stdout, err := c.exec(ctx, "kubectl", "--context", c.profile, "get", "nodes", "-o", "json")
	if err != nil {
		return nil, err
	}
	var list corev1.NodeList
	if err := json.Unmarshal(stdout, &list); err != nil {
		return nil, fmt.Errorf("decode kubectl nodes: %w", err)
	}
	return list.Items, nil
}

func (c *Client) AddNode(ctx context.Context) error {
	_, err := c.exec(ctx, "minikube", "node", "add", "-p", c.profile)
	return err
}

func (c *Client) exec(parent context.Context, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(parent, c.timeout)
	defer cancel()
	c.log.Printf("exec: %s %s", name, strings.Join(args, " "))
	stdout, stderr, err := c.run(ctx, name, args...)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w: stdout=%q stderr=%q", name, strings.Join(args, " "), err, strings.TrimSpace(string(stdout)), strings.TrimSpace(string(stderr)))
	}
	return stdout, nil
}

func runCommand(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}
```

- [ ] **Step 4: Verify GREEN**

Run: `go test ./pkg/minikube`

Expected: PASS, four tests.

- [ ] **Step 5: Commit**

```bash
git add pkg/minikube go.mod go.sum
git commit -m "feat: add timed minikube command client"
```

### Task 3: Implement node-group reads with TDD

**Files:**
- Create: `pkg/provider/provider_test.go`
- Create: `pkg/provider/provider.go`

- [ ] **Step 1: Write failing tests for refresh, mapping, and target size**

Create a helper in `pkg/provider/provider_test.go` that supplies Kubernetes NodeList JSON through the real command client seam:

```go
func testProvider(t *testing.T, dryRun bool, run minikube.RunFunc) *Provider {
	t.Helper()
	cfg := Config{NodeGroup: "minikube-workers", MinNodes: 1, MaxNodes: 3, DryRun: dryRun}
	logger := log.New(io.Discard, "", 0)
	p, err := New(cfg, minikube.New("demo", time.Second, logger, run), logger)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func nodeList(names ...string) []byte {
	items := make([]map[string]any, len(names))
	for i, name := range names {
		items[i] = map[string]any{"metadata": map[string]any{"name": name}}
	}
	b, _ := json.Marshal(map[string]any{"items": items})
	return b
}
```

Add tests asserting:

```go
func TestRefreshExposesOneGroupAndObservedTarget(t *testing.T) {
	p := testProvider(t, false, func(context.Context, string, ...string) ([]byte, []byte, error) {
		return nodeList("demo"), nil, nil
	})
	if _, err := p.Refresh(context.Background(), &protos.RefreshRequest{}); err != nil { t.Fatal(err) }
	groups, _ := p.NodeGroups(context.Background(), &protos.NodeGroupsRequest{})
	if len(groups.NodeGroups) != 1 || groups.NodeGroups[0].Id != "minikube-workers" { t.Fatalf("groups = %#v", groups) }
	size, _ := p.NodeGroupTargetSize(context.Background(), &protos.NodeGroupTargetSizeRequest{Id: "minikube-workers"})
	if size.TargetSize != 1 { t.Fatalf("size = %d", size.TargetSize) }
}

func TestNodeGroupForNodeMapsOnlyRefreshedNodes(t *testing.T) {
	p := testProvider(t, false, func(context.Context, string, ...string) ([]byte, []byte, error) { return nodeList("demo"), nil, nil })
	p.Refresh(context.Background(), &protos.RefreshRequest{})
	known, _ := p.NodeGroupForNode(context.Background(), &protos.NodeGroupForNodeRequest{Node: &protos.ExternalGrpcNode{Name: "demo"}})
	unknown, _ := p.NodeGroupForNode(context.Background(), &protos.NodeGroupForNodeRequest{Node: &protos.ExternalGrpcNode{Name: "foreign"}})
	if known.NodeGroup.Id != "minikube-workers" || unknown.NodeGroup.Id != "" { t.Fatalf("known=%#v unknown=%#v", known, unknown) }
}
```

- [ ] **Step 2: Run the tests and verify RED**

Run: `go test ./pkg/provider`

Expected: FAIL because `Config`, `Provider`, and `New` do not exist.

- [ ] **Step 3: Implement configuration and read RPCs**

Create `pkg/provider/provider.go` with:

```go
type Config struct {
	NodeGroup      string
	MinNodes       int32
	MaxNodes       int32
	DryRun         bool
	EnableScaleDown bool
}

type Provider struct {
	protos.UnimplementedCloudProviderServer
	mu     sync.Mutex
	cfg    Config
	client *minikube.Client
	log    *log.Logger
	nodes  []corev1.Node
	target int32
}
```

`New` must reject an empty group, negative min, or `max < min`. Add `group()` returning the configured `protos.NodeGroup`. Implement `Refresh`, `NodeGroups`, `NodeGroupForNode`, `NodeGroupTargetSize`, and `NodeGroupNodes`. Match nodes by name or provider ID. Use `Node.Spec.ProviderID` as `Instance.Id` when present and fall back to the node name, set status to `instanceRunning`, and return `codes.NotFound` for an unknown group ID. In dry-run mode `Refresh` sets `target = max(target, observed)`; normal mode sets it to the observed count. Protect `nodes` and `target` with the single mutex.

- [ ] **Step 4: Verify GREEN**

Run: `go test ./pkg/provider`

Expected: PASS for read behavior.

- [ ] **Step 5: Commit**

```bash
git add pkg/provider go.mod go.sum
git commit -m "feat: expose minikube node group over grpc"
```

### Task 4: Implement scale-up with TDD

**Files:**
- Modify: `pkg/provider/provider_test.go`
- Modify: `pkg/provider/provider.go`

- [ ] **Step 1: Add failing tests for dry-run, bounds, and real commands**

Add table-driven tests that assert:

```go
func TestIncreaseSizeDryRunAdvancesTargetWithoutCommand(t *testing.T) {
	adds := 0
	p := testProvider(t, true, func(_ context.Context, name string, _ ...string) ([]byte, []byte, error) {
		if name == "minikube" { adds++ }
		return nodeList("demo"), nil, nil
	})
	p.Refresh(context.Background(), &protos.RefreshRequest{})
	_, err := p.NodeGroupIncreaseSize(context.Background(), &protos.NodeGroupIncreaseSizeRequest{Id: "minikube-workers", Delta: 1})
	if err != nil || adds != 0 { t.Fatalf("err=%v adds=%d", err, adds) }
	size, _ := p.NodeGroupTargetSize(context.Background(), &protos.NodeGroupTargetSizeRequest{Id: "minikube-workers"})
	if size.TargetSize != 2 { t.Fatalf("size=%d", size.TargetSize) }
	p.Refresh(context.Background(), &protos.RefreshRequest{})
	size, _ = p.NodeGroupTargetSize(context.Background(), &protos.NodeGroupTargetSizeRequest{Id: "minikube-workers"})
	if size.TargetSize != 2 { t.Fatalf("size after refresh=%d", size.TargetSize) }
}

func TestIncreaseSizeRejectsMax(t *testing.T) {
	p := testProvider(t, true, func(context.Context, string, ...string) ([]byte, []byte, error) { return nodeList("demo"), nil, nil })
	p.Refresh(context.Background(), &protos.RefreshRequest{})
	_, err := p.NodeGroupIncreaseSize(context.Background(), &protos.NodeGroupIncreaseSizeRequest{Id: "minikube-workers", Delta: 3})
	if status.Code(err) != codes.FailedPrecondition { t.Fatalf("code=%s err=%v", status.Code(err), err) }
}
```

Add a real-mode test with a scripted runner sequence: initial `kubectl` returns one node, `minikube` increments an add counter, and the following `kubectl` returns two nodes. Assert one add and target two. Add a delta-zero test expecting `codes.InvalidArgument`.

- [ ] **Step 2: Run the tests and verify RED**

Run: `go test ./pkg/provider -run IncreaseSize`

Expected: FAIL because `NodeGroupIncreaseSize` still returns generated `Unimplemented`.

- [ ] **Step 3: Implement minimal scale-up**

Implement `NodeGroupIncreaseSize` so it:

```go
if req.Id != p.cfg.NodeGroup { return nil, status.Error(codes.NotFound, "unknown node group") }
if req.Delta <= 0 { return nil, status.Error(codes.InvalidArgument, "delta must be positive") }
if p.target+req.Delta > p.cfg.MaxNodes { return nil, status.Error(codes.FailedPrecondition, "scale-up exceeds max-nodes") }
```

In dry-run, log and increment `target`. Otherwise loop exactly `Delta` times, call `client.AddNode(ctx)`, then `client.Nodes(ctx)`, and replace observed state and target after every successful add. Convert external failures to `codes.Internal` without hiding their text.

- [ ] **Step 4: Verify GREEN and the full package**

Run:

```bash
go test ./pkg/provider -run IncreaseSize
go test ./...
```

Expected: all tests PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/provider
git commit -m "feat: translate scale-up requests to minikube"
```

### Task 5: Implement template and scale-down boundary with TDD

**Files:**
- Modify: `pkg/provider/provider_test.go`
- Modify: `pkg/provider/provider.go`

- [ ] **Step 1: Add failing template and scale-down tests**

Use a source node containing CPU/memory capacity, allocatable values, `kubernetes.io/os`, `kubernetes.io/hostname`, `node-role.kubernetes.io/control-plane`, a provider ID, and a control-plane taint. After `Refresh`, call `NodeGroupTemplateNodeInfo`, unmarshal `NodeBytes` into `corev1.Node`, and assert:

```go
if template.Status.Allocatable.Cpu().String() != "2" { t.Fatalf("cpu=%s", template.Status.Allocatable.Cpu()) }
if template.Labels["kubernetes.io/os"] != "linux" { t.Fatalf("labels=%v", template.Labels) }
if _, ok := template.Labels["kubernetes.io/hostname"]; ok { t.Fatal("hostname copied") }
if _, ok := template.Labels["node-role.kubernetes.io/control-plane"]; ok { t.Fatal("control-plane label copied") }
if len(template.Spec.Taints) != 0 || template.Spec.ProviderID != "" { t.Fatalf("spec=%#v", template.Spec) }
```

Add tests for both delete and decrease methods: disabled config expects `codes.FailedPrecondition`; enabled config expects `codes.Unimplemented`.

- [ ] **Step 2: Run tests and verify RED**

Run: `go test ./pkg/provider -run 'Template|Delete|Decrease'`

Expected: FAIL with unimplemented RPCs.

- [ ] **Step 3: Implement the worker template and boundary RPCs**

Build a fresh node using only `kubernetes.io/arch`, `kubernetes.io/os`, and `node.kubernetes.io/instance-type` when present. Deep-copy capacity and allocatable, leave identity and taints empty, call `template.Marshal()`, and return `NodeGroupTemplateNodeInfoResponse{NodeBytes: data}`.

Implement delete/decrease using `status.Error` and the two codes above. Implement successful empty responses for `GPULabel`, `GetAvailableGPUTypes`, and `Cleanup`. Leave the generated pricing and options methods untouched.

- [ ] **Step 4: Verify GREEN**

Run: `go test ./...`

Expected: all tests PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/provider
git commit -m "feat: add node template and scale-down boundary"
```

### Task 6: Wire the host server with TDD

**Files:**
- Create: `cmd/provider/main_test.go`
- Create: `cmd/provider/main.go`

- [ ] **Step 1: Write failing flag tests**

Test a `parseFlags(args []string) (options, error)` function. Assert default listen/profile/group/min/max values, and assert `--min-nodes=4 --max-nodes=3` returns an error.

- [ ] **Step 2: Run and verify RED**

Run: `go test ./cmd/provider`

Expected: FAIL because `parseFlags` does not exist.

- [ ] **Step 3: Implement flags and server wiring**

Use `flag.NewFlagSet`, `net.Listen`, `grpc.NewServer`, and `signal.NotifyContext`. Create the command client with a ten-minute timeout, create and initially refresh the provider, register `protos.RegisterCloudProviderServer`, log the listen address, serve in one goroutine, and call `GracefulStop` when SIGINT or SIGTERM cancels the context. Keep `--v` as an integer controlling whether command output and refresh details are logged; RPC decisions and errors are always logged.

- [ ] **Step 4: Verify GREEN and build**

Run:

```bash
go test ./cmd/provider
go test ./...
go build -o /tmp/minikube-externalgrpc-provider ./cmd/provider
```

Expected: all tests PASS and build exits 0.

- [ ] **Step 5: Commit**

```bash
git add cmd/provider go.mod go.sum
git commit -m "feat: serve provider on host grpc endpoint"
```

### Task 7: Add deployment manifests

**Files:**
- Create: `deploy/cluster-autoscaler-rbac.yaml`
- Create: `deploy/cloud-config.yaml`
- Create: `deploy/cluster-autoscaler.yaml`
- Create: `deploy/workload-unschedulable.yaml`

- [ ] **Step 1: Copy RBAC from the tagged official externalgrpc example**

Use the ServiceAccount, ClusterRole, Role, ClusterRoleBinding, and RoleBinding documents from:

```text
https://github.com/kubernetes/autoscaler/blob/cluster-autoscaler-1.35.0/cluster-autoscaler/cloudprovider/externalgrpc/examples/cluster-autoscaler-manifests/cluster-autoscaler.yaml
```

Place only those five documents in `deploy/cluster-autoscaler-rbac.yaml`; do not copy the example Deployment, TLS Secret, or hostPath certificate volume.

- [ ] **Step 2: Create the plaintext demo cloud config**

Create `deploy/cloud-config.yaml`:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: cluster-autoscaler-cloud-config
  namespace: kube-system
data:
  cloud-config.yaml: |
    # Teaching demo only: plaintext endpoint on the host.
    address: host.minikube.internal:9090
    grpc_timeout: 10m
```

- [ ] **Step 3: Create the Cluster Autoscaler Deployment**

Create one replica in `kube-system` using image `registry.k8s.io/autoscaling/cluster-autoscaler:v1.35.0`, service account `cluster-autoscaler`, `priorityClassName: system-cluster-critical`, requests `100m/300Mi`, and these arguments:

```yaml
- ./cluster-autoscaler
- --cloud-provider=externalgrpc
- --cloud-config=/etc/cluster-autoscaler/cloud-config.yaml
- --scale-down-enabled=false
- --v=5
```

Mount ConfigMap key `cloud-config.yaml` at `/etc/cluster-autoscaler/cloud-config.yaml` with `subPath`. Do not add TLS or host certificate volumes.

- [ ] **Step 4: Create deterministic CPU pressure**

Create `deploy/workload-unschedulable.yaml` as a four-replica Deployment named `autoscaler-pressure`, using `registry.k8s.io/pause:3.10`, with each replica requesting and limiting `600m` CPU and `64Mi` memory. Add comments stating why total CPU exceeds one two-CPU node and fits after scale-up.

- [ ] **Step 5: Validate YAML syntax client-side**

Run:

```bash
kubectl apply --dry-run=client -f deploy/cluster-autoscaler-rbac.yaml
kubectl apply --dry-run=client -f deploy/cloud-config.yaml
kubectl apply --dry-run=client -f deploy/cluster-autoscaler.yaml
kubectl apply --dry-run=client -f deploy/workload-unschedulable.yaml
```

Expected: every resource prints `(dry run)` and no validation error.

- [ ] **Step 6: Commit**

```bash
git add deploy
git commit -m "feat: deploy externalgrpc autoscaler demo"
```

### Task 8: Add the reproducible scripts

**Files:**
- Create: `scripts/00-check-prereqs.sh`
- Create: `scripts/01-start-minikube.sh`
- Create: `scripts/02-run-provider.sh`
- Create: `scripts/03-deploy-cluster-autoscaler.sh`
- Create: `scripts/04-create-pressure.sh`
- Create: `scripts/05-watch-demo.sh`
- Create: `scripts/99-cleanup.sh`

- [ ] **Step 1: Add shared defaults directly to each short script**

Use these defaults where relevant, without creating a shared shell library:

```sh
PROFILE=${PROFILE:-autoscaling-demo}
MINIKUBE_DRIVER=${MINIKUBE_DRIVER:-docker}
KUBERNETES_VERSION=${KUBERNETES_VERSION:-v1.35.6}
CA_VERSION=${CA_VERSION:-v1.35.0}
MIN_NODES=${MIN_NODES:-1}
MAX_NODES=${MAX_NODES:-3}
MINIKUBE_CPUS=${MINIKUBE_CPUS:-2}
MINIKUBE_MEMORY=${MINIKUBE_MEMORY:-4g}
```

- [ ] **Step 2: Implement the prerequisite check**

`00-check-prereqs.sh` must require `minikube`, `kubectl`, and `go`; require a working `docker info` only when Docker is selected; compare minor versions using `sed -E 's/^v?([0-9]+\.[0-9]+).*/\1/'`; and print optional status for `protoc`, `protoc-gen-go`, and `protoc-gen-go-grpc`. Exit nonzero on missing required tools or minor mismatch.

- [ ] **Step 3: Implement the lifecycle scripts**

Use these core commands:

```sh
# 01-start-minikube.sh
minikube start -p "$PROFILE" --driver="$MINIKUBE_DRIVER" --nodes=1 --cpus="$MINIKUBE_CPUS" --memory="$MINIKUBE_MEMORY" --kubernetes-version="$KUBERNETES_VERSION"
kubectl --context "$PROFILE" taint nodes --all node-role.kubernetes.io/control-plane- 2>/dev/null || true

# 02-run-provider.sh
exec go run ./cmd/provider --profile "$PROFILE" --node-group minikube-workers --min-nodes "$MIN_NODES" --max-nodes "$MAX_NODES" --listen 0.0.0.0:9090 "$@"

# 03-deploy-cluster-autoscaler.sh
if ! minikube ssh -p "$PROFILE" -- "nc -z -w 2 host.minikube.internal 9090"; then
  printf '%s\n' 'warning: provider port 9090 is not reachable from minikube'
fi
kubectl --context "$PROFILE" apply -f deploy/cluster-autoscaler-rbac.yaml
kubectl --context "$PROFILE" apply -f deploy/cloud-config.yaml
kubectl --context "$PROFILE" apply -f deploy/cluster-autoscaler.yaml
kubectl --context "$PROFILE" -n kube-system set image deployment/cluster-autoscaler cluster-autoscaler="registry.k8s.io/autoscaling/cluster-autoscaler:$CA_VERSION"

# 04-create-pressure.sh
kubectl --context "$PROFILE" apply -f deploy/workload-unschedulable.yaml

# 99-cleanup.sh
kubectl --context "$PROFILE" delete -f deploy/workload-unschedulable.yaml --ignore-not-found 2>/dev/null || true
kubectl --context "$PROFILE" delete -f deploy/cluster-autoscaler.yaml -f deploy/cloud-config.yaml -f deploy/cluster-autoscaler-rbac.yaml --ignore-not-found 2>/dev/null || true
minikube delete -p "$PROFILE"
```

`05-watch-demo.sh` uses one `while :` loop with a three-second sleep to print nodes, Pods, Pending Pods, and the exact autoscaler log command. It contains no state or custom dashboard logic.

- [ ] **Step 4: Verify shell syntax**

Run:

```bash
chmod +x scripts/*.sh
for script in scripts/*.sh; do sh -n "$script"; done
```

Expected: exit 0 with no output.

- [ ] **Step 5: Commit**

```bash
git add scripts
git commit -m "feat: script the autoscaling lecture demo"
```

### Task 9: Document and index the demo

**Files:**
- Create: `README.md`
- Create: `Makefile`

- [ ] **Step 1: Write README with the approved architecture**

Include: production warning; text architecture diagram from the design; explanation of CA versus provider responsibilities; current version defaults; Docker-tested/other-driver-best-effort policy; one-hybrid-node group asymmetry; prerequisites; terminal-by-terminal commands; expected Pending/IncreaseSize/node Ready evidence; dry-run example; configuration table; troubleshooting for version mismatch, port 9090, insufficient host resources, and host connectivity; automated verification commands; cleanup; and the future scale-down requirements from the design.

- [ ] **Step 2: Add a thin Makefile**

Create:

```make
.PHONY: test build check start provider deploy pressure watch cleanup

test:
	go test ./...

build:
	go build -o /tmp/minikube-externalgrpc-provider ./cmd/provider

check:
	./scripts/00-check-prereqs.sh

start:
	./scripts/01-start-minikube.sh

provider:
	./scripts/02-run-provider.sh

deploy:
	./scripts/03-deploy-cluster-autoscaler.sh

pressure:
	./scripts/04-create-pressure.sh

watch:
	./scripts/05-watch-demo.sh

cleanup:
	./scripts/99-cleanup.sh
```

- [ ] **Step 3: Verify docs reference real paths and commands**

Run:

```bash
rg 'scripts/0[0-5]-|scripts/99-|host.minikube.internal:9090|NodeGroupIncreaseSize|scale-down' README.md
make test
make build
```

Expected: references are printed; tests and build exit 0.

- [ ] **Step 4: Commit**

```bash
git add README.md Makefile
git commit -m "docs: add lecture runbook"
```

### Task 10: Verify the complete milestone

**Files:**
- Modify only if verification exposes a defect; every defect starts with a failing regression test.

- [ ] **Step 1: Run all static verification fresh**

```bash
go test ./...
go build -o /tmp/minikube-externalgrpc-provider ./cmd/provider
for script in scripts/*.sh; do sh -n "$script"; done
kubectl apply --dry-run=client -f deploy/cluster-autoscaler-rbac.yaml -f deploy/cloud-config.yaml -f deploy/cluster-autoscaler.yaml -f deploy/workload-unschedulable.yaml
git diff --check
```

Expected: all commands exit 0.

- [ ] **Step 2: Run the host provider dry-run smoke test**

With a demo profile running, execute the provider with `--dry-run`, call it through Cluster Autoscaler, and confirm logs contain `NodeGroupIncreaseSize` but no `minikube node add` execution. Stop the dry-run provider before the real test.

- [ ] **Step 3: Run the real end-to-end flow**

Terminal 1:

```bash
./scripts/00-check-prereqs.sh
./scripts/01-start-minikube.sh
./scripts/02-run-provider.sh
```

Terminal 2:

```bash
./scripts/03-deploy-cluster-autoscaler.sh
./scripts/04-create-pressure.sh
kubectl --context autoscaling-demo get pods -w
```

Capture evidence that some Pods are initially Pending, Cluster Autoscaler logs a scale-up decision, provider logs `NodeGroupIncreaseSize`, a second node becomes Ready, and every pressure Pod reaches Running.

- [ ] **Step 4: Clean up and rerun static verification**

```bash
./scripts/99-cleanup.sh
go test ./...
go build -o /tmp/minikube-externalgrpc-provider ./cmd/provider
git status --short
```

Expected: profile deleted, tests/build exit 0, and no unexpected generated files.

- [ ] **Step 5: Request code and specification review**

Review the final diff against `docs/superpowers/specs/2026-06-29-minikube-externalgrpc-autoscaler-demo-design.md`. Fix every critical or important issue with TDD, then rerun Step 1.

- [ ] **Step 6: Commit verification fixes only if needed**

```bash
git add -u
git commit -m "fix: close autoscaler demo verification gaps"
```

Skip this commit when verification required no code changes.
