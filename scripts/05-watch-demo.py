#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.11"
# dependencies = ["rich>=14,<15"]
# ///

import datetime
import json
import socket
import subprocess


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
    if previous is not None:
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
