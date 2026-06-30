# Rich Demo Observer Design

## Goal

Replace the current shell watch loop with a read-only Rich terminal observer that explains the minikube Cluster Autoscaler scenario while it runs. The observer displays cluster state, autoscaler decisions, Kubernetes events, and the current scaling direction without starting, modifying, or deleting resources.

## Placement and Execution

The observer is a separate executable from the Go externalgrpc provider:

```text
scripts/05-watch-demo.py
```

It replaces `scripts/05-watch-demo.sh`. The existing `make watch` target invokes it through `uv run --script`.

The Python file uses PEP 723 inline script metadata with Python `>=3.11` and `rich>=14,<15`. This avoids a Python package, `pyproject.toml`, lockfile, virtual environment committed to the repository, Click, Textual, and the Kubernetes Python client.

## Responsibilities

The observer is strictly read-only. It may:

- run `kubectl get` and `kubectl logs`;
- open a short TCP connection to `127.0.0.1:9090` to report provider reachability;
- retain an in-memory timeline for the lifetime of the process;
- redraw its own terminal screen.

It must not invoke minikube, call scaling RPCs, apply or delete manifests, start the provider, or perform cleanup. Provider logs remain outside the observer.

## Data Sources

Every refresh uses the configured Kubernetes context, defaulting to `PROFILE=autoscaling-demo`:

- `kubectl get nodes -o json`;
- `kubectl get pods -A -o json`;
- `kubectl get events -A -o json`;
- `kubectl -n kube-system logs deployment/cluster-autoscaler --tail=<bounded count>`.

The refresh interval defaults to two seconds and is configurable with `--interval`. Command execution uses `subprocess.run` argument arrays, bounded timeouts, captured output, and no shell.

The observer derives a small presentation model from these responses: node role/readiness, workload Pod phase and assignment, aggregate Pod counts, recent Kubernetes events, recent autoscaler lines, provider reachability, and node-count transitions.

## Layout

A single Rich `Layout` is rendered with `Live`:

1. header: profile, timestamp, and inferred phase;
2. nodes table and summary panel;
3. workload Pod table and Cluster Autoscaler decisions;
4. recent Kubernetes event timeline;
5. footer: refresh interval, read-only status, and `Ctrl-C` exit hint.

`Table` renders nodes and Pods. `Panel` contains summary, decisions, errors, and events. Colors distinguish Ready/Running/success, Pending/scaling activity, and failures.

The inferred phase is descriptive, not authoritative. It uses current Pending Pods plus changes in observed node count to show states such as stable, scaling up, or scaling down. Raw events and autoscaler lines remain visible so the user can verify the inference.

## Interaction

The default mode refreshes continuously until `Ctrl-C`. Rich `Live` restores the terminal on exit. No custom keyboard handling or Textual event loop is added.

`--once` renders one snapshot and exits. This supports non-interactive use, deterministic testing, and troubleshooting. When stdout is not a TTY, continuous mode fails with a clear message directing the user to `--once`.

## Error Handling

Each source is collected independently. A failed or timed-out command produces an error entry for its panel while other panels continue to update from successful sources or the last valid snapshot.

Malformed JSON is reported as a source error rather than crashing. A missing profile, unavailable Cluster Autoscaler Deployment, or unreachable provider is visible in the summary. `Ctrl-C` exits with status zero; invalid arguments and unrecoverable startup problems return nonzero.

External text is rendered as plain Rich `Text` or escaped before markup so Kubernetes names, messages, and log lines cannot alter terminal styling.

## Testing

Tests use only `unittest` and the Python standard library. A fake command runner returns small node, Pod, event, and autoscaler fixtures. Tests verify:

- exact kubectl argument arrays and configured profile;
- node role/readiness and Pod aggregation;
- event ordering and bounded history;
- phase changes for node addition and removal;
- independent source errors;
- one-shot rendering without a live cluster.

A shell regression invokes `uv run --script scripts/05-watch-demo.py --once` with a fake `kubectl` executable and verifies successful output. `make test` runs this regression alongside existing Go and shell tests.

## Documentation

The README replaces references to `05-watch-demo.sh` with the Python observer, lists `uv` as an observer prerequisite, explains `make watch`, `PROFILE`, `--interval`, and `--once`, and reiterates that the observer is read-only and demonstration-only.
