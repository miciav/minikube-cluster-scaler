# Rich Demo Observer Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the shell watch loop with a separate read-only Rich observer that explains cluster state, autoscaler decisions, and Kubernetes events.

**Architecture:** A single PEP 723 Python script calls kubectl through argument arrays, builds a small snapshot model, and renders one Rich Layout either once or continuously. A standard-library unittest script injects fake command and provider probes; Makefile and README expose the observer without creating a Python package.

**Tech Stack:** Python 3.11+, Rich 14, uv inline scripts, Python standard library, kubectl, POSIX shell, Go project Makefile.

---

## File Map

- Create: `scripts/05-watch-demo.py` — collector, model, Rich rendering, and CLI.
- Create: `scripts/05-watch-demo_test.py` — unittest coverage with fake sources.
- Delete: `scripts/05-watch-demo.sh` — superseded polling loop.
- Modify: `Makefile` — `watch` and `tui-test` targets.
- Modify: `scripts/00-check-prereqs.sh` — require uv for the observer.
- Modify: `README.md` — observer usage, requirements, and read-only behavior.

### Task 1: Collect and model observer data

**Files:**
- Create: `scripts/05-watch-demo.py`
- Create: `scripts/05-watch-demo_test.py`

- [ ] **Step 1: Create the test script metadata and dynamic import**

Start `scripts/05-watch-demo_test.py` with:

```python
# /// script
# requires-python = ">=3.11"
# dependencies = ["rich>=14,<15"]
# ///

import importlib.util
import json
import pathlib
import sys
import unittest

SCRIPT = pathlib.Path(__file__).with_name("05-watch-demo.py")
spec = importlib.util.spec_from_file_location("watch_demo", SCRIPT)
watch_demo = importlib.util.module_from_spec(spec)
sys.modules[spec.name] = watch_demo
spec.loader.exec_module(watch_demo)
```

Create fixture dictionaries for two nodes, four `app=autoscaler-pressure` Pods, two events with different timestamps, and representative Cluster Autoscaler log lines.

- [ ] **Step 2: Write failing collector and model tests**

Use a fake callable that records argument tuples and returns JSON/log text by command. Assert `collect_snapshot("demo", runner=fake, probe=lambda: True)` issues exactly:

```python
[
    ("kubectl", "--context", "demo", "get", "nodes", "-o", "json"),
    ("kubectl", "--context", "demo", "get", "pods", "-A", "-o", "json"),
    ("kubectl", "--context", "demo", "get", "events", "-A", "-o", "json"),
    (
        "kubectl", "--context", "demo", "-n", "kube-system", "logs",
        "deployment/cluster-autoscaler", "--tail=40",
    ),
]
```

Assert the returned snapshot has two nodes, four pressure Pods, provider reachable, events newest-first, and only relevant autoscaler decision lines.

Add tests for:

```python
self.assertEqual(watch_demo.infer_phase(None, snapshot), "OBSERVING")
self.assertEqual(watch_demo.infer_phase(snapshot_one_node, snapshot_two_nodes), "SCALING UP")
self.assertEqual(watch_demo.infer_phase(snapshot_two_nodes, snapshot_one_node), "SCALING DOWN")
self.assertEqual(watch_demo.infer_phase(snapshot_without_pending, snapshot_with_pending), "PODS PENDING")
self.assertEqual(watch_demo.infer_phase(snapshot_two_nodes, snapshot_two_nodes), "STABLE")
```

Add a failure test where events return malformed JSON and verify `errors["events"]` is set while nodes, Pods, and logs remain available.

- [ ] **Step 3: Run the tests and verify RED**

```sh
uv run --script scripts/05-watch-demo_test.py
```

Expected: import failure because `scripts/05-watch-demo.py` does not exist.

- [ ] **Step 4: Add the observer metadata and collection primitives**

Create executable `scripts/05-watch-demo.py`:

```python
#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.11"
# dependencies = ["rich>=14,<15"]
# ///

import argparse
import json
import os
import socket
import subprocess
import sys
import time
from datetime import datetime, timezone
```

Implement:

```python
def run_command(args):
    completed = subprocess.run(
        args, check=True, capture_output=True, text=True, timeout=5
    )
    return completed.stdout

def provider_reachable():
    try:
        with socket.create_connection(("127.0.0.1", 9090), timeout=0.25):
            return True
    except OSError:
        return False
```

`collect_snapshot(profile, runner=run_command, probe=provider_reachable)` must call each source independently, decode JSON, retain errors by source, filter Pods to label `app=autoscaler-pressure`, sort events by `eventTime`, `lastTimestamp`, then `metadata.creationTimestamp`, and keep the newest 12. Keep the last 8 autoscaler lines containing case-insensitive `scale`, `unschedul`, or `node`.

Return one dictionary with keys `nodes`, `pods`, `events`, `decisions`, `provider_reachable`, `errors`, and `collected_at`.

Implement `infer_phase(previous, current)` in this priority: node count increased, node count decreased, Pending pressure Pods exist, first snapshot, otherwise stable.

- [ ] **Step 5: Run tests and verify GREEN**

```sh
uv run --script scripts/05-watch-demo_test.py
```

Expected: collector, filtering, ordering, error isolation, and phase tests pass.

- [ ] **Step 6: Commit**

```sh
git add scripts/05-watch-demo.py scripts/05-watch-demo_test.py
git commit -m "feat: collect demo observer state"
```

### Task 2: Render the Rich observer

**Files:**
- Modify: `scripts/05-watch-demo.py`
- Modify: `scripts/05-watch-demo_test.py`

- [ ] **Step 1: Write failing rendering tests**

Import `StringIO` and Rich `Console`. Render the fixture snapshot into a fixed-width no-color console:

```python
output = StringIO()
console = Console(file=output, width=140, color_system=None)
console.print(watch_demo.build_screen(snapshot, snapshot_one_node, "demo", 2.0))
text = output.getvalue()
for expected in (
    "minikube-cluster-scaler observer",
    "NODES",
    "SUMMARY",
    "WORKLOAD PODS",
    "AUTOSCALER DECISIONS",
    "KUBERNETES EVENTS",
    "demo-m02",
    "SCALING UP",
    "read-only",
):
    self.assertIn(expected, text)
```

Add one rendering test with `errors={"events": "invalid JSON"}` and assert the error is visible without suppressing nodes.

- [ ] **Step 2: Verify RED**

```sh
uv run --script scripts/05-watch-demo_test.py
```

Expected: failure because `build_screen` is undefined.

- [ ] **Step 3: Implement Rich rendering**

Import `Console`, `Group`, `Layout`, `Live`, `Panel`, `Table`, and `Text`.

Implement small functions `nodes_table(snapshot)`, `pods_table(snapshot)`, `summary_panel(snapshot)`, `decisions_panel(snapshot)`, and `events_panel(snapshot)`. Use `Text` objects for all external Kubernetes/log text; do not interpolate external values into Rich markup strings.

`build_screen(snapshot, previous, profile, interval)` creates this fixed structure:

```text
header
body.top: nodes | summary
body.middle: workload pods | autoscaler decisions
body.events: Kubernetes events
footer
```

Show role, Ready state, allocatable CPU, and age for nodes. Show Pod name, phase, and assigned node. Summary shows node and Pod counts, provider reachability, autoscaler availability, and direction. Empty data and source errors render explicit messages.

- [ ] **Step 4: Implement CLI and one-shot mode**

Use argparse options:

```text
--profile    default PROFILE or autoscaling-demo
--interval   positive float, default 2
--once       render one snapshot and exit
```

Reject nonpositive intervals. In `--once`, print one screen and exit. Continuous mode requires `sys.stdout.isatty()`, uses `Live(..., screen=True, refresh_per_second=4)`, sleeps for the configured interval, and exits zero on `KeyboardInterrupt`.

- [ ] **Step 5: Verify GREEN**

```sh
uv run --script scripts/05-watch-demo_test.py
uv run --script scripts/05-watch-demo.py --profile missing --once
```

Expected: tests pass; one-shot mode renders panels with source errors and exits without traceback.

- [ ] **Step 6: Commit**

```sh
git add scripts/05-watch-demo.py scripts/05-watch-demo_test.py
git commit -m "feat: render rich demo observer"
```

### Task 3: Integrate and document the observer

**Files:**
- Delete: `scripts/05-watch-demo.sh`
- Modify: `Makefile`
- Modify: `scripts/00-check-prereqs.sh`
- Modify: `README.md`

- [ ] **Step 1: Write the failing integration assertions**

Extend the unittest script to assert the Python observer is executable and PEP 723 metadata contains `rich>=14,<15`.

Before changing integration, verify these commands fail:

```sh
grep -q 'uv run --script scripts/05-watch-demo.py' Makefile
grep -q 'minikube kubectl go uv' scripts/00-check-prereqs.sh
```

- [ ] **Step 2: Replace the old watcher**

Delete `scripts/05-watch-demo.sh`. Update Makefile:

```make
.PHONY: test shell-test tui-test race vet build generate check start provider deploy pressure watch remove-pressure cleanup

test: shell-test tui-test
	go test ./...

tui-test:
	uv run --script scripts/05-watch-demo_test.py

watch:
	uv run --script scripts/05-watch-demo.py
```

Add `uv` to the required-command loop in `scripts/00-check-prereqs.sh`.

- [ ] **Step 3: Update README**

Replace `./scripts/05-watch-demo.sh` with `make watch` or:

```sh
PROFILE=autoscaling-demo uv run --script scripts/05-watch-demo.py
```

Document `--interval`, `--once`, `Ctrl-C`, the panels, the read-only boundary, and that provider logs are intentionally excluded. Add uv to prerequisites and development verification.

- [ ] **Step 4: Run full verification**

```sh
uv run --script scripts/05-watch-demo_test.py
env GOCACHE=/tmp/minikube-cluster-scaler-go-cache make test
env GOCACHE=/tmp/minikube-cluster-scaler-go-cache make race
env GOCACHE=/tmp/minikube-cluster-scaler-go-cache make vet
env GOCACHE=/tmp/minikube-cluster-scaler-go-cache make build
sh -n scripts/*.sh deploy/*_test.sh proto/generate.sh
git diff --check
```

Expected: all commands exit zero.

- [ ] **Step 5: Commit**

```sh
git add Makefile README.md scripts/00-check-prereqs.sh scripts/05-watch-demo.py scripts/05-watch-demo_test.py
git add -u scripts/05-watch-demo.sh
git commit -m "docs: integrate rich demo observer"
```
