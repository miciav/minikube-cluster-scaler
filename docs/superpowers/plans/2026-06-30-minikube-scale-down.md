# Minikube Scale-Down Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let Cluster Autoscaler remove one minikube worker at a time until only the protected control-plane node remains.

**Architecture:** Reuse the provider operation gate and observed node list. Validate one externalgrpc node, resolve it by name or provider ID, delete it through the existing command client, refresh state, and require the node to disappear. Configure Cluster Autoscaler for sequential deletion with overridable one-minute delays.

**Tech Stack:** Go 1.25, gRPC, Kubernetes API types, POSIX shell, Kubernetes YAML, minikube, kubectl.

---

### Task 1: Add the Minikube delete command

**Files:**
- Modify: `pkg/minikube/client_test.go`
- Modify: `pkg/minikube/client.go`

- [ ] **Step 1: Write the failing test**

```go
func TestDeleteNodeUsesArgumentArray(t *testing.T) {
	var name string
	var args []string
	c := New("demo", time.Second, nil, func(_ context.Context, gotName string, gotArgs ...string) ([]byte, []byte, error) {
		name, args = gotName, gotArgs
		return nil, nil, nil
	})
	if err := c.DeleteNode(context.Background(), "demo-m02"); err != nil {
		t.Fatal(err)
	}
	if name != "minikube" || !slices.Equal(args, []string{"node", "delete", "demo-m02", "-p", "demo"}) {
		t.Fatalf("command = %s %v", name, args)
	}
}
```

- [ ] **Step 2: Verify RED**

```sh
env GOCACHE=/tmp/minikube-cluster-scaler-go-cache go test ./pkg/minikube -run TestDeleteNodeUsesArgumentArray -count=1
```

Expected: build failure because `DeleteNode` is undefined.

- [ ] **Step 3: Implement the minimum method**

```go
func (c *Client) DeleteNode(ctx context.Context, name string) error {
	_, err := c.exec(ctx, "minikube", "node", "delete", name, "-p", c.profile)
	return err
}
```

- [ ] **Step 4: Verify GREEN and commit**

```sh
env GOCACHE=/tmp/minikube-cluster-scaler-go-cache go test ./pkg/minikube -count=1
git add pkg/minikube/client.go pkg/minikube/client_test.go
git commit -m "feat: add minikube node deletion"
```

### Task 2: Implement safe single-worker deletion

**Files:**
- Modify: `pkg/provider/provider_test.go`
- Modify: `pkg/provider/provider.go`

- [ ] **Step 1: Write failing provider tests**

Use these observed nodes:

```go
controlPlane := corev1.Node{ObjectMeta: metav1.ObjectMeta{
	Name: "demo",
	Labels: map[string]string{"node-role.kubernetes.io/control-plane": ""},
}}
worker := corev1.Node{
	ObjectMeta: metav1.ObjectMeta{Name: "demo-m02"},
	Spec: corev1.NodeSpec{ProviderID: "minikube://demo-m02"},
}
request := &protos.NodeGroupDeleteNodesRequest{
	Id: "minikube-workers",
	Nodes: []*protos.ExternalGrpcNode{{ProviderID: "minikube://demo-m02"}},
}
```

The success runner returns both nodes before deletion, records `minikube node delete demo-m02 -p demo`, and returns only the control-plane afterward. Assert success and target `1`.

Add explicit table cases:

```text
disabled scale-down         FailedPrecondition
unknown group               NotFound
nil/zero nodes              InvalidArgument
two nodes                   InvalidArgument
unknown node                NotFound
control-plane node          FailedPrecondition
size equal to min-nodes     FailedPrecondition
```

- [ ] **Step 2: Verify RED**

```sh
env GOCACHE=/tmp/minikube-cluster-scaler-go-cache go test ./pkg/provider -run TestNodeGroupDeleteNodes -count=1
```

Expected: enabled deletion returns `Unimplemented`.

- [ ] **Step 3: Add matching and dry-run state**

Import `slices`, add `dryRunDeleted map[string]struct{}` to `Provider`, and initialize it in `New`.

```go
func matchesNode(want *protos.ExternalGrpcNode, node corev1.Node) bool {
	return want != nil && (
		want.Name != "" && want.Name == node.Name ||
		want.ProviderID != "" && want.ProviderID == node.Spec.ProviderID)
}

func isControlPlane(node corev1.Node) bool {
	_, ok := node.Labels["node-role.kubernetes.io/control-plane"]
	return ok
}
```

During `refresh`, filter dry-run-deleted names before updating `p.nodes` and `p.target`:

```go
if p.cfg.DryRun {
	nodes = slices.DeleteFunc(nodes, func(node corev1.Node) bool {
		_, deleted := p.dryRunDeleted[node.Name]
		return deleted
	})
}
```

- [ ] **Step 4: Implement `NodeGroupDeleteNodes`**

Validate group, enabled flag, and exactly one requested node before acquiring the gate. Under the gate:

1. refresh;
2. copy observed nodes;
3. reject current size at or below `MinNodes`;
4. resolve with `matchesNode`;
5. reject unknown, unnamed, or control-plane nodes;
6. in dry-run, record/remove the node and decrement target;
7. otherwise call `p.client.DeleteNode(ctx, selected.Name)`;
8. refresh and return `Internal` if the node is still observed;
9. log group, node, target, and dry-run value.

Preserve context errors with:

```go
if ctx.Err() != nil {
	return nil, status.FromContextError(ctx.Err()).Err()
}
```

Keep `NodeGroupDecreaseTargetSize` returning `Unimplemented` when enabled.

- [ ] **Step 5: Add failure-path tests**

Add runner-based tests for:

```text
delete command failure      Internal, target unchanged
context canceled            Canceled
node remains after command  Internal containing "still observed"
dry-run delete              no minikube command, target/instances decrease
dry-run then Refresh        deleted worker remains filtered
```

- [ ] **Step 6: Verify GREEN and commit**

```sh
env GOCACHE=/tmp/minikube-cluster-scaler-go-cache go test ./pkg/provider -count=1
env GOCACHE=/tmp/minikube-cluster-scaler-go-cache go test ./... -count=1
git add pkg/provider/provider.go pkg/provider/provider_test.go
git commit -m "feat: delete minikube workers through externalgrpc"
```

### Task 3: Configure sequential autoscaler scale-down

**Files:**
- Modify: `scripts/02-run-provider.sh`
- Modify: `deploy/cluster-autoscaler.yaml`
- Modify: `scripts/03-deploy-cluster-autoscaler.sh`
- Create: `scripts/03-deploy-cluster-autoscaler_test.sh`
- Modify: `Makefile`

- [ ] **Step 1: Write a failing shell regression**

Stub `minikube` and `kubectl`, run the deployment script with `SCALE_DOWN_DELAY_AFTER_ADD=45s` and `SCALE_DOWN_UNNEEDED_TIME=90s`, and require this logged call:

```text
set env deployment/cluster-autoscaler SCALE_DOWN_DELAY_AFTER_ADD=45s SCALE_DOWN_UNNEEDED_TIME=90s
```

Require these manifest arguments:

```text
--scale-down-enabled=true
--max-scale-down-parallelism=1
--scale-down-delay-after-add=$(SCALE_DOWN_DELAY_AFTER_ADD)
--scale-down-unneeded-time=$(SCALE_DOWN_UNNEEDED_TIME)
```

Add the test to `make shell-test`.

- [ ] **Step 2: Verify RED**

```sh
./scripts/03-deploy-cluster-autoscaler_test.sh
```

Expected: missing scale-down configuration.

- [ ] **Step 3: Implement manifest defaults and overrides**

Add container environment values `SCALE_DOWN_DELAY_AFTER_ADD=1m` and `SCALE_DOWN_UNNEEDED_TIME=1m`. Add the four arguments above.

Default the same variables to `1m` in `03-deploy-cluster-autoscaler.sh`, then apply them with:

```sh
kubectl --context "$PROFILE" -n kube-system set env deployment/cluster-autoscaler   SCALE_DOWN_DELAY_AFTER_ADD="$SCALE_DOWN_DELAY_AFTER_ADD"   SCALE_DOWN_UNNEEDED_TIME="$SCALE_DOWN_UNNEEDED_TIME"
```

Default `ENABLE_SCALE_DOWN` to `true` in `02-run-provider.sh` and pass `--enable-scale-down="$ENABLE_SCALE_DOWN"`.

- [ ] **Step 4: Verify GREEN and commit**

```sh
make shell-test
git add Makefile deploy/cluster-autoscaler.yaml scripts/02-run-provider.sh scripts/03-deploy-cluster-autoscaler.sh scripts/03-deploy-cluster-autoscaler_test.sh
git commit -m "feat: configure sequential autoscaler scale-down"
```

### Task 4: Add the pressure-removal action

**Files:**
- Create: `scripts/06-remove-pressure_test.sh`
- Create: `scripts/06-remove-pressure.sh`
- Modify: `Makefile`

- [ ] **Step 1: Write and run a failing regression**

Stub kubectl and require:

```text
--context test delete -f deploy/workload-unschedulable.yaml --ignore-not-found
```

Run `./scripts/06-remove-pressure_test.sh`; expected failure because the target script is absent.

- [ ] **Step 2: Implement the script**

```sh
#!/bin/sh
set -eu

cd "$(dirname "$0")/.."
PROFILE=${PROFILE:-autoscaling-demo}

kubectl --context "$PROFILE" delete -f deploy/workload-unschedulable.yaml --ignore-not-found
printf 'Watch scale-down: kubectl --context %s get nodes -w\n' "$PROFILE"
```

Make both scripts executable, add the test to `shell-test`, and add a `remove-pressure` Make target.

- [ ] **Step 3: Verify GREEN and commit**

```sh
env GOCACHE=/tmp/minikube-cluster-scaler-go-cache make test
git add Makefile scripts/06-remove-pressure.sh scripts/06-remove-pressure_test.sh
git commit -m "feat: add pressure removal step"
```

### Task 5: Document and verify

**Files:**
- Modify: `README.md`
- Create: `scripts/e2e-scale-down.sh`

- [ ] **Step 1: Update README**

Document the implemented delete RPC, one-node requests, control-plane/minimum protection, `ENABLE_SCALE_DOWN`, both timing variables, and `./scripts/06-remove-pressure.sh`. Describe repeated `N -> N-1` transitions until one node remains. Retain the non-production warning and keep `NodeGroupDecreaseTargetSize` documented as unimplemented.

- [ ] **Step 2: Run complete local verification**

```sh
gofmt -w pkg/minikube/client.go pkg/minikube/client_test.go pkg/provider/provider.go pkg/provider/provider_test.go
env GOCACHE=/tmp/minikube-cluster-scaler-go-cache make test
env GOCACHE=/tmp/minikube-cluster-scaler-go-cache make race
env GOCACHE=/tmp/minikube-cluster-scaler-go-cache make vet
env GOCACHE=/tmp/minikube-cluster-scaler-go-cache make build
git diff --check
```

Expected: every command exits zero.

- [ ] **Step 3: Add and run live E2E**

Create `scripts/e2e-scale-down.sh`. It must use a disposable `autoscaling-scale-down` profile by default, start Minikube, launch the provider in the background, deploy Cluster Autoscaler with both delays set to `1m`, create pressure, and poll every 10 seconds with a 10-minute deadline until two Ready nodes exist. It must then remove pressure and poll until exactly one node remains and that node has `node-role.kubernetes.io/control-plane`. A trap must stop the provider and invoke `99-cleanup.sh`.

```sh
#!/bin/sh
set -eu

cd "$(dirname "$0")/.."
PROFILE=${PROFILE:-autoscaling-scale-down}
E2E_TIMEOUT_SECONDS=${E2E_TIMEOUT_SECONDS:-600}
provider_pid=
provider_log=/tmp/minikube-cluster-scaler-e2e-$$.log

cleanup() {
  if [ -n "$provider_pid" ]; then kill "$provider_pid" 2>/dev/null || true; fi
  PROFILE="$PROFILE" ./scripts/99-cleanup.sh >/dev/null 2>&1 || true
  rm -f "$provider_log"
}
trap cleanup EXIT INT TERM

wait_for_count() {
  expected=$1
  deadline=$(($(date +%s) + E2E_TIMEOUT_SECONDS))
  while [ "$(kubectl --context "$PROFILE" get nodes --no-headers 2>/dev/null | wc -l | tr -d ' ')" != "$expected" ]; do
    [ "$(date +%s)" -lt "$deadline" ] || return 1
    sleep 10
  done
}

PROFILE="$PROFILE" ./scripts/01-start-minikube.sh
PROFILE="$PROFILE" ./scripts/02-run-provider.sh >"$provider_log" 2>&1 &
provider_pid=$!
PROFILE="$PROFILE" SCALE_DOWN_DELAY_AFTER_ADD=1m SCALE_DOWN_UNNEEDED_TIME=1m ./scripts/03-deploy-cluster-autoscaler.sh
PROFILE="$PROFILE" ./scripts/04-create-pressure.sh
wait_for_count 2
PROFILE="$PROFILE" ./scripts/06-remove-pressure.sh
wait_for_count 1
test "$(kubectl --context "$PROFILE" get nodes -l node-role.kubernetes.io/control-plane --no-headers | wc -l | tr -d ' ')" = 1
grep -q 'scale-down succeeded' "$provider_log"
```

Run:

```sh
./scripts/e2e-scale-down.sh
```

The script verifies this exact sequence:

1. start Minikube and provider;
2. deploy Cluster Autoscaler with both delays set to `1m`;
3. create pressure and wait for two Ready nodes;
4. run `06-remove-pressure.sh`;
5. wait until exactly one node remains;
6. require that remaining node has `node-role.kubernetes.io/control-plane`;
7. inspect provider logs for one delete request;
8. run `99-cleanup.sh`.

- [ ] **Step 4: Commit and verify repository state**

```sh
git add README.md
git commit -m "docs: document minikube scale-down"
git status --short
git log -6 --oneline
```

Expected: clean worktree and one commit for each completed task.
