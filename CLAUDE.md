# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

A host-side Go implementation of the Kubernetes Cluster Autoscaler `externalgrpc` cloud-provider API. It exposes one minikube node group over plaintext gRPC (`0.0.0.0:9090`) and turns resize requests into `minikube node add/delete -p <profile>`. Cluster Autoscaler runs inside minikube and reaches the provider via `host.minikube.internal:9090`. Demo/teaching only: no auth, no TLS, single node group. Go module path is `github.com/miciav/minikube-cluster-scaler`.

## Commands

```sh
make verify      # lint tidy-check vuln cover race shell-test tui-test (what CI runs locally)
make lint        # uvx pre-commit run --all-files (golangci-lint, shellcheck, ruff, bandit,
                 # basedpyright, hadolint, kubeconform, actionlint, hygiene)
make cover       # Go tests; fails below 90% total (cmd/ + pkg/)
make tui-test    # observer unittest under coverage; fails below 90%
make test        # shell-test + tui-test + go test ./...
make race        # go test -race ./...
make vuln        # govulncheck
make build       # -> /tmp/minikube-externalgrpc-provider
make image       # lab image as minikube-cluster-scaler:dev
make generate    # proto/generate.sh (pins exact protoc/plugin versions, fails otherwise)
make sonar       # local SonarQube in Docker

go test ./pkg/provider -run TestNodeGroupDeleteNodes   # single Go test
./scripts/04-create-pressure_test.sh                  # single shell test
LAB_IMAGE=minikube-cluster-scaler:dev ./scripts/lab help   # try the wrapper against a local image

go run ./cmd/provider --profile autoscaling-demo --dry-run   # no minikube commands executed
```

Demo flow (numbered scripts, also Makefile targets): `check` → `start` → `provider` (foreground) → `deploy` → `pressure` → `watch` → `remove-pressure` → `cleanup`. `scripts/e2e-scale-down.sh` runs a full 1→2→1 cycle in a disposable profile. `99-cleanup.sh` deletes the whole `PROFILE`. Scripts are configured via env vars (`PROFILE`, `MIN_NODES`, `MAX_NODES`, `ENABLE_SCALE_DOWN`, `CA_VERSION`, …; see README).

## Architecture

- `cmd/provider/main.go`: flag parsing/validation, initial `Refresh`, gRPC server, graceful shutdown on SIGINT/SIGTERM.
- `pkg/minikube/client.go`: thin exec wrapper. `Nodes` = `kubectl --context <profile> get nodes -o json`; `AddNode`/`DeleteNode` shell out to minikube. Command execution is injected via `RunFunc` — tests pass a fake runner instead of running real binaries (see `nodeRunner`/`testProvider` helpers in `pkg/provider/provider_test.go`).
- `pkg/provider/provider.go`: the `CloudProviderServer` implementation. Key invariants:
  - Two locks: `gate` (1-slot channel) serializes capacity operations and refreshes and honors ctx cancellation while waiting; `mu` guards `nodes`/`target` for cheap reads. Functions commented "requires the operation gate" must be called with `gate` held.
  - Capacity ops refresh observed state before checking bounds and again after each real add/delete.
  - The initial minikube node (control-plane + worker) counts as the first group member. `NodeGroupDeleteNodes` deletes exactly one node per request, resolved by `Name` or `ProviderID`, and refuses the control plane or going below `MinNodes`. Disabled unless `--enable-scale-down`.
  - `NodeGroupDecreaseTargetSize` is intentionally `FailedPrecondition` (scale-down off) / `Unimplemented` (on): minikube adds are synchronous, so there is no pending capacity to cancel.
  - Dry-run: `target` never decreases on refresh, and dry-run deletions are tracked in `dryRunDeleted` so they survive refreshes.
  - Context errors map to gRPC status via `status.FromContextError`; command failures include stdout/stderr.
- `proto/`: committed `externalgrpc.proto` and generated bindings from the Cluster Autoscaler `cluster-autoscaler-1.35.0` tag. Never hand-edit `*.pb.go`; regenerate via `proto/generate.sh`.
- `deploy/`: Cluster Autoscaler RBAC (with K8s 1.35 informer permissions), cloud-config, Deployment, and the pressure workload. `03-deploy-cluster-autoscaler.sh` patches image tag and scale-down env vars after applying.
- `scripts/05-watch-demo.py`: standalone `uv` script (inline deps, `rich`) rendering a live TUI of nodes, pods, CA decisions and events. It shells out to kubectl; `collect_snapshot` takes injectable `runner`/`probe` for tests.
- Lab container: `Dockerfile` packages the provider (`/usr/local/bin/minikube-externalgrpc-provider`), a docker CLI, Python+rich, `scripts/` and `deploy/` under `/opt/lab`, with `LAB_CONTAINER=1`. It contains **no** minikube/kubectl. `scripts/lab-entrypoint.sh` maps subcommands (`start`, `provider`, `deploy`, `watch`, …) to the numbered scripts; `wrapper` prints `scripts/lab` with `__LAB_IMAGE__` replaced by the image tag. `scripts/lab` (runs on the student's Linux VM) discovers the VM's minikube/kubectl (resolving symlinks, rejecting non-ELF snap shims), the Docker socket, `MINIKUBE_HOME`/`KUBECONFIG`, bind-mounts them at identical paths and runs the image with `--init --network host --user uid:gid`.
- Container mode in scripts: `02-run-provider.sh` and `e2e-scale-down.sh` use `minikube-externalgrpc-provider` when on `PATH`, else Go; `00-check-prereqs.sh` with `LAB_CONTAINER=1` does not require go/uv. Both modes must keep working.
- CI (`.github/workflows/`): `ci.yml` (pre-commit, go gates, proto drift, script tests, per-arch image build + Trivy + real e2e through `lab`), `release.yml` (GHCR multi-arch, `edge`/semver/`latest`), `codeql.yml`; Dependabot. Actions are pinned by SHA.

## Testing conventions

- Shell scripts have sibling `*_test.sh` files that put fake `minikube`/`kubectl` stubs on `PATH` logging to `$TEST_LOG`, then grep the log. New script behavior needs an assertion there and, if new, a line in the Makefile `shell-test` target. Tests must pass on macOS sh and Linux dash; when a test needs a tool to be *absent*, build a PATH of symlinked utilities (CI runners ship go, kubectl and docker in `/usr/bin`).
- `//nolint` and `# shellcheck disable` need a specific linter/code and a reason (`nolintlint` enforces it for Go).
- The Python test imports `05-watch-demo.py` via `importlib` (hyphenated filename).

## Version policy

Kubernetes (`v1.35.6`), Cluster Autoscaler (`v1.35.0`), and the externalgrpc proto must share major/minor. Bump them together.
