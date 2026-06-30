#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.11"
# dependencies = ["rich>=14,<15"]
# ///

import argparse
import datetime
import json
import math
import os
import re
import socket
import subprocess
import sys
import time

from rich.console import Console, Group
from rich.layout import Layout
from rich.live import Live
from rich.panel import Panel
from rich.table import Table
from rich.text import Text


ANSI_ESCAPE = re.compile(
    r"\x1b(?:\][^\x07]*(?:\x07|\x1b\\)|[PX^_].*?\x1b\\|\[[0-?]*[ -/]*[@-~]|[@-_])"
    r"|\x9b[0-?]*[ -/]*[@-~]",
    re.DOTALL,
)


def sanitize(value):
    text = ANSI_ESCAPE.sub("", str(value))
    return "".join(
        " " if character in "\t\r\n" else character
        for character in text
        if character in "\t\r\n"
        or (ord(character) >= 32 and not 127 <= ord(character) <= 159)
    )


def run_command(args):
    return subprocess.run(
        args, check=True, capture_output=True, text=True, timeout=5
    ).stdout


def provider_reachable():
    try:
        with socket.create_connection(("127.0.0.1", 9090), timeout=0.25):
            return True
    except OSError:
        return False


def parse_items(output):
    items = json.loads(output)["items"]
    if not isinstance(items, list):
        raise ValueError("items must be a list")
    return items


def event_timestamp(event):
    value = (
        event.get("eventTime")
        or event.get("lastTimestamp")
        or event.get("metadata", {}).get("creationTimestamp")
    )
    return (
        datetime.datetime.fromisoformat(value)
        if value
        else datetime.datetime.min.replace(tzinfo=datetime.UTC)
    )


def collect_snapshot(profile, runner=run_command, probe=provider_reachable):
    commands = {
        "nodes": ["kubectl", "--context", profile, "get", "nodes", "-o", "json"],
        "pods": ["kubectl", "--context", profile, "get", "pods", "-A", "-o", "json"],
        "events": ["kubectl", "--context", profile, "get", "events", "-A", "-o", "json"],
        "decisions": [
            "kubectl",
            "--context",
            profile,
            "-n",
            "kube-system",
            "logs",
            "deployment/cluster-autoscaler",
            "--tail=40",
        ],
    }
    snapshot = {"nodes": [], "pods": [], "events": [], "decisions": [], "errors": {}}

    for name in ("nodes", "pods", "events"):
        try:
            snapshot[name] = parse_items(runner(commands[name]))
        except (OSError, subprocess.SubprocessError, ValueError, KeyError, TypeError) as error:
            snapshot["errors"][name] = str(error)

    snapshot["pods"] = [
        pod
        for pod in snapshot["pods"]
        if pod.get("metadata", {}).get("labels", {}).get("app") == "autoscaler-pressure"
    ]
    try:
        snapshot["events"] = sorted(
            snapshot["events"],
            key=event_timestamp,
            reverse=True,
        )[:12]
    except (AttributeError, TypeError, ValueError) as error:
        snapshot["events"] = []
        snapshot["errors"]["events"] = str(error)

    try:
        snapshot["decisions"] = [
            line
            for line in runner(commands["decisions"]).splitlines()
            if any(word in line.lower() for word in ("scale", "unschedul", "node"))
        ][-8:]
    except (OSError, subprocess.SubprocessError) as error:
        snapshot["errors"]["decisions"] = str(error)

    try:
        snapshot["provider_reachable"] = probe()
    except OSError as error:
        snapshot["provider_reachable"] = False
        snapshot["errors"]["provider_reachable"] = str(error)
    snapshot["collected_at"] = datetime.datetime.now(datetime.UTC).isoformat()
    return snapshot


def infer_phase(previous, current):
    if (
        previous is not None
        and "nodes" not in previous.get("errors", {})
        and "nodes" not in current.get("errors", {})
    ):
        node_change = len(current["nodes"]) - len(previous["nodes"])
        if node_change > 0:
            return "SCALING UP"
        if node_change < 0:
            return "SCALING DOWN"
    if any(pod.get("status", {}).get("phase") == "Pending" for pod in current["pods"]):
        return "PODS PENDING"
    if previous is None:
        return "OBSERVING"
    return "STABLE"


def object_age(metadata, now=None):
    created = metadata.get("creationTimestamp")
    if not created:
        return "-"
    try:
        then = datetime.datetime.fromisoformat(created.replace("Z", "+00:00"))
        seconds = max(0, int(((now or datetime.datetime.now(datetime.UTC)) - then).total_seconds()))
    except (TypeError, ValueError):
        return "?"
    if seconds >= 86400:
        return f"{seconds // 86400}d"
    if seconds >= 3600:
        return f"{seconds // 3600}h"
    if seconds >= 60:
        return f"{seconds // 60}m"
    return f"{seconds}s"


def nodes_table(snapshot):
    table = Table(expand=True, box=None)
    for heading in ("NAME", "ROLE", "READY", "CPU", "AGE"):
        table.add_column(heading)
    error = snapshot["errors"].get("nodes")
    if error:
        table.add_row(Text(f"Error: {sanitize(error)}", style="red"), "", "", "", "")
    elif not snapshot["nodes"]:
        table.add_row(Text("No nodes found", style="dim"), "", "", "", "")
    for node in snapshot["nodes"]:
        metadata = node.get("metadata", {})
        labels = metadata.get("labels", {})
        role = "control-plane" if "node-role.kubernetes.io/control-plane" in labels else "worker"
        ready = next(
            (condition.get("status") for condition in node.get("status", {}).get("conditions", []) if condition.get("type") == "Ready"),
            "Unknown",
        )
        table.add_row(
            Text(sanitize(metadata.get("name", "-"))),
            Text(role),
            Text("Yes" if ready == "True" else "No", style="green" if ready == "True" else "red"),
            Text(sanitize(node.get("status", {}).get("allocatable", {}).get("cpu", "-"))),
            Text(object_age(metadata)),
        )
    return Panel(table, title="NODES", border_style="cyan")


def pods_table(snapshot):
    table = Table(expand=True, box=None)
    for heading in ("POD", "PHASE", "NODE"):
        table.add_column(heading)
    error = snapshot["errors"].get("pods")
    if error:
        table.add_row(Text(f"Error: {sanitize(error)}", style="red"), "", "")
    elif not snapshot["pods"]:
        table.add_row(Text("No pressure Pods found", style="dim"), "", "")
    for pod in snapshot["pods"]:
        metadata = pod.get("metadata", {})
        phase = sanitize(pod.get("status", {}).get("phase", "Unknown"))
        table.add_row(
            Text(sanitize(metadata.get("name", "-"))),
            Text(phase, style="green" if phase == "Running" else "yellow" if phase == "Pending" else "red"),
            Text(sanitize(pod.get("spec", {}).get("nodeName", "-"))),
        )
    return Panel(table, title="WORKLOAD PODS", border_style="cyan")


def summary_panel(snapshot, phase):
    phases = {}
    for pod in snapshot["pods"]:
        pod_phase = sanitize(pod.get("status", {}).get("phase", "Unknown"))
        phases[pod_phase] = phases.get(pod_phase, 0) + 1
    pod_counts = ", ".join(f"{name}: {count}" for name, count in sorted(phases.items())) or "none"
    provider = "reachable" if snapshot.get("provider_reachable") else "unreachable"
    autoscaler = "unavailable" if "decisions" in snapshot["errors"] else "available"
    return Panel(
        Group(
            Text(f"Nodes: {len(snapshot['nodes'])}"),
            Text(f"Pressure Pods: {len(snapshot['pods'])} ({pod_counts})"),
            Text(f"Provider: {provider}", style="green" if snapshot.get("provider_reachable") else "red"),
            Text(f"Autoscaler: {autoscaler}", style="green" if autoscaler == "available" else "red"),
            Text(f"Direction: {phase}", style="yellow" if phase != "STABLE" else "green"),
        ),
        title="SUMMARY",
        border_style="cyan",
    )


def decisions_panel(snapshot):
    error = snapshot["errors"].get("decisions")
    if error:
        content = Text(f"Error: {sanitize(error)}", style="red")
    elif snapshot["decisions"]:
        content = Group(*(Text(sanitize(line)) for line in snapshot["decisions"]))
    else:
        content = Text("No recent autoscaler decisions", style="dim")
    return Panel(content, title="AUTOSCALER DECISIONS", border_style="cyan")


def events_panel(snapshot):
    error = snapshot["errors"].get("events")
    if error:
        content = Text(f"Error: {sanitize(error)}", style="red")
    elif snapshot["events"]:
        lines = []
        for event in snapshot["events"]:
            metadata = event.get("metadata", {})
            timestamp = event.get("eventTime") or event.get("lastTimestamp") or metadata.get("creationTimestamp", "-")
            reason = event.get("reason", "-")
            message = event.get("message", "-")
            lines.append(
                Text(f"{sanitize(timestamp)}  {sanitize(reason)}  {sanitize(message)}")
            )
        content = Group(*lines)
    else:
        content = Text("No recent Kubernetes events", style="dim")
    return Panel(content, title="KUBERNETES EVENTS", border_style="cyan")


def build_screen(snapshot, previous, profile, interval):
    phase = infer_phase(previous, snapshot)
    layout = Layout()
    layout.split_column(
        Layout(name="header", size=5),
        Layout(name="top", size=7),
        Layout(name="middle", size=6),
        Layout(name="events", size=3),
        Layout(name="footer", size=3),
    )
    layout["top"].split_row(Layout(name="nodes"), Layout(name="summary"))
    layout["middle"].split_row(Layout(name="pods"), Layout(name="decisions"))
    header = Text("minikube-cluster-scaler observer", style="bold cyan")
    header.append(f"\nprofile={sanitize(profile)}  phase={phase}")
    header.append(f"\ntimestamp={sanitize(snapshot['collected_at'])}")
    layout["header"].update(Panel(header))
    layout["nodes"].update(nodes_table(snapshot))
    layout["summary"].update(summary_panel(snapshot, phase))
    layout["pods"].update(pods_table(snapshot))
    layout["decisions"].update(decisions_panel(snapshot))
    layout["events"].update(events_panel(snapshot))
    layout["footer"].update(
        Panel(Text(f"Refresh: {interval:g}s  |  read-only  |  Ctrl-C to exit", style="dim"))
    )
    return layout


def positive_float(value):
    number = float(value)
    if not math.isfinite(number) or number <= 0:
        raise argparse.ArgumentTypeError("must be greater than zero")
    return number


def parse_args(argv=None):
    parser = argparse.ArgumentParser(description="Read-only minikube autoscaling observer")
    parser.add_argument("--profile", default=os.environ.get("PROFILE", "autoscaling-demo"))
    parser.add_argument("--interval", type=positive_float, default=2.0)
    parser.add_argument("--once", action="store_true")
    return parser.parse_args(argv)


def main(argv=None):
    args = parse_args(argv)
    console = Console()
    if args.once:
        snapshot = collect_snapshot(args.profile)
        console.print(build_screen(snapshot, None, args.profile, args.interval))
        return 0
    if not sys.stdout.isatty():
        print("continuous mode requires a TTY; use --once", file=sys.stderr)
        return 2

    try:
        previous = None
        snapshot = collect_snapshot(args.profile)
        with Live(
            build_screen(snapshot, previous, args.profile, args.interval),
            console=console,
            screen=True,
            refresh_per_second=4,
        ) as live:
            while True:
                time.sleep(args.interval)
                previous, snapshot = snapshot, collect_snapshot(args.profile)
                live.update(build_screen(snapshot, previous, args.profile, args.interval))
    except KeyboardInterrupt:
        return 0


if __name__ == "__main__":
    raise SystemExit(main())
