#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.11"
# dependencies = ["rich>=14,<15"]
# ///

import importlib.util
from io import StringIO
import json
import os
import pathlib
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

from rich.console import Console


MODULE_PATH = pathlib.Path(__file__).with_name("05-watch-demo.py")
sys.dont_write_bytecode = True
SPEC = importlib.util.spec_from_file_location("watch_demo", MODULE_PATH)
watch_demo = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(watch_demo)


SNAPSHOT_ONE_NODE = {
    "nodes": [
        {
            "metadata": {
                "name": "demo",
                "creationTimestamp": "2026-06-30T08:00:00Z",
                "labels": {"node-role.kubernetes.io/control-plane": ""},
            },
            "status": {
                "allocatable": {"cpu": "4"},
                "conditions": [{"type": "Ready", "status": "True"}],
            },
        }
    ],
    "pods": [
        {
            "metadata": {"name": "pressure-1", "namespace": "default"},
            "spec": {"nodeName": "demo"},
            "status": {"phase": "Running"},
        },
        {
            "metadata": {"name": "pressure-2", "namespace": "default"},
            "spec": {},
            "status": {"phase": "Pending"},
        },
    ],
    "events": [],
    "decisions": [],
    "provider_reachable": True,
    "errors": {},
    "collected_at": "2026-06-30T10:00:00+00:00",
}

SNAPSHOT_TWO_NODES = {
    **SNAPSHOT_ONE_NODE,
    "nodes": [
        *SNAPSHOT_ONE_NODE["nodes"],
        {
            "metadata": {
                "name": "demo-m02",
                "creationTimestamp": "2026-06-30T09:59:00Z",
                "labels": {"node-role.kubernetes.io/worker": ""},
            },
            "status": {
                "allocatable": {"cpu": "2"},
                "conditions": [{"type": "Ready", "status": "True"}],
            },
        },
    ],
    "pods": [
        *SNAPSHOT_ONE_NODE["pods"],
        {
            "metadata": {"name": "pressure-3", "namespace": "default"},
            "spec": {"nodeName": "demo-m02"},
            "status": {"phase": "Running"},
        },
        {
            "metadata": {"name": "pressure-4", "namespace": "default"},
            "spec": {"nodeName": "demo-m02"},
            "status": {"phase": "Running"},
        },
    ],
    "events": [
        {
            "metadata": {"name": "scaled-up", "namespace": "kube-system"},
            "lastTimestamp": "2026-06-30T10:00:00Z",
            "reason": "TriggeredScaleUp",
            "message": "pod triggered scale-up: [{minikube 1->2}]",
        }
    ],
    "decisions": ["Scale-up: setting group size to 2"],
}


class WatchDemoTest(unittest.TestCase):
    def test_script_is_executable_and_declares_rich(self):
        source = MODULE_PATH.read_text()
        self.assertTrue(os.access(MODULE_PATH, os.X_OK))
        self.assertIn('dependencies = ["rich>=14,<15"]', source)

    def test_once_runs_through_uv_with_fake_kubectl(self):
        with tempfile.TemporaryDirectory() as directory:
            directory = pathlib.Path(directory)
            calls = directory / "calls"
            kubectl = directory / "kubectl"
            kubectl.write_text(
                """#!/bin/sh
printf '%s\\n' "$*" >> "$KUBECTL_CALLS"
case "$*" in
  *" get nodes "*) printf '%s\\n' '{"items":[{"metadata":{"name":"n1","labels":{"node-role.kubernetes.io/control-plane":""}},"status":{"allocatable":{"cpu":"2"},"conditions":[{"type":"Ready","status":"True"}]}}]}' ;;
  *" get pods "*|*" get events "*) printf '%s\\n' '{"items":[]}' ;;
  *" logs "*) printf '%s\\n' 'scale decision from fake kubectl' ;;
  *) exit 64 ;;
esac
"""
            )
            kubectl.chmod(0o755)
            env = os.environ.copy()
            env["PATH"] = f"{directory}{os.pathsep}{env['PATH']}"
            env["KUBECTL_CALLS"] = str(calls)

            completed = subprocess.run(
                ["uv", "run", "--script", str(MODULE_PATH), "--profile", "fake", "--once"],
                check=False,
                capture_output=True,
                text=True,
                timeout=15,
                env=env,
            )

            self.assertEqual(completed.returncode, 0, completed.stderr)
            self.assertIn("minikube-cluster-scaler observer", completed.stdout)
            self.assertIn("n1", completed.stdout)
            self.assertEqual(
                calls.read_text().splitlines(),
                [
                    "--context fake get nodes -o json",
                    "--context fake get pods -A -o json",
                    "--context fake get events -A -o json",
                    "--context fake -n kube-system logs deployment/cluster-autoscaler --tail=40",
                ],
            )

    def test_positive_float_rejects_nonfinite_and_nonpositive_values(self):
        for value in ("nan", "inf", "-inf", "0", "-1"):
            with self.subTest(value=value), self.assertRaises(
                watch_demo.argparse.ArgumentTypeError
            ):
                watch_demo.positive_float(value)

    def test_keyboard_interrupt_during_initial_collection_exits_zero(self):
        with (
            patch.object(watch_demo.sys.stdout, "isatty", return_value=True),
            patch.object(watch_demo, "collect_snapshot", side_effect=KeyboardInterrupt),
        ):
            self.assertEqual(watch_demo.main([]), 0)

    def test_build_screen_renders_observer_state(self):
        output = StringIO()
        console = Console(file=output, width=140, height=24, color_system=None)

        console.print(
            watch_demo.build_screen(
                SNAPSHOT_TWO_NODES, SNAPSHOT_ONE_NODE, "demo", 2.0
            )
        )

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
            with self.subTest(expected=expected):
                self.assertIn(expected, text)
        self.assertTrue(text.rstrip().endswith("╯"), "footer must fit a 24-row terminal")

    def test_header_keeps_context_phase_and_timestamp_at_80_columns(self):
        output = StringIO()
        console = Console(file=output, width=80, height=24, color_system=None)

        console.print(
            watch_demo.build_screen(
                SNAPSHOT_TWO_NODES, SNAPSHOT_ONE_NODE, "demo", 2.0
            )
        )

        text = output.getvalue()
        self.assertIn("profile=demo", text)
        self.assertIn("phase=SCALING UP", text)
        self.assertIn("timestamp=2026-06-30T10:00:00+00:00", text)

    def test_build_screen_sanitizes_external_control_sequences(self):
        snapshot = {
            **SNAPSHOT_TWO_NODES,
            "nodes": [
                {
                    **SNAPSHOT_TWO_NODES["nodes"][1],
                    "metadata": {
                        **SNAPSHOT_TWO_NODES["nodes"][1]["metadata"],
                        "name": "demo\x08-m02",
                    },
                }
            ],
            "pods": [
                {
                    "metadata": {"name": "pressure\x7f-1"},
                    "spec": {"nodeName": "demo\x08-m02"},
                    "status": {"phase": "Running\x00"},
                }
            ],
            "events": [
                {
                    "lastTimestamp": "2026-06-30T10:00:00Z\x01",
                    "reason": "\x1b]0;owned\x07TriggeredScaleUp",
                    "message": "node added\x9b31m\x02",
                }
            ],
            "decisions": ["\x1b[31mscale decision\x1b[0m\x00"],
        }
        output = StringIO()
        console = Console(file=output, width=140, height=24, color_system=None)

        console.print(
            watch_demo.build_screen(
                snapshot, SNAPSHOT_ONE_NODE, "\x1b[2Jdemo\x07", 2.0
            )
        )

        text = output.getvalue()
        self.assertIn("profile=demo", text)
        self.assertIn("scale decision", text)
        self.assertIn("TriggeredScaleUp", text)
        self.assertNotIn("\x1b", text)
        self.assertFalse(
            any(
                (ord(character) < 32 and character != "\n")
                or 127 <= ord(character) <= 159
                for character in text
            )
        )

    def test_build_screen_keeps_nodes_when_events_fail(self):
        snapshot = {
            **SNAPSHOT_TWO_NODES,
            "events": [],
            "errors": {"events": "invalid JSON"},
        }
        output = StringIO()
        console = Console(file=output, width=140, color_system=None)

        console.print(watch_demo.build_screen(snapshot, SNAPSHOT_ONE_NODE, "demo", 2.0))

        text = output.getvalue()
        self.assertIn("demo-m02", text)
        self.assertIn("invalid JSON", text)

    def test_run_command_uses_safe_bounded_subprocess(self):
        completed = subprocess.CompletedProcess(["kubectl"], 0, stdout="ok\n")
        with patch.object(watch_demo.subprocess, "run", return_value=completed) as run:
            self.assertEqual(watch_demo.run_command(["kubectl"]), "ok\n")
        run.assert_called_once_with(
            ["kubectl"], check=True, capture_output=True, text=True, timeout=5
        )

    def test_provider_reachable_probes_local_provider(self):
        with patch.object(watch_demo.socket, "create_connection") as connect:
            self.assertTrue(watch_demo.provider_reachable())
        connect.assert_called_once_with(("127.0.0.1", 9090), timeout=0.25)

        with patch.object(
            watch_demo.socket, "create_connection", side_effect=OSError("closed")
        ):
            self.assertFalse(watch_demo.provider_reachable())

    def test_collect_snapshot_filters_and_orders_state(self):
        commands = [
            ["kubectl", "--context", "demo", "get", "nodes", "-o", "json"],
            ["kubectl", "--context", "demo", "get", "pods", "-A", "-o", "json"],
            ["kubectl", "--context", "demo", "get", "events", "-A", "-o", "json"],
            [
                "kubectl",
                "--context",
                "demo",
                "-n",
                "kube-system",
                "logs",
                "deployment/cluster-autoscaler",
                "--tail=40",
            ],
        ]
        events = [
            {"metadata": {"name": f"event-{i}", "creationTimestamp": f"2026-01-{i + 1:02}T00:00:00Z"}}
            for i in range(13)
        ]
        events += [
            {"metadata": {"name": "last"}, "lastTimestamp": "2026-02-01T00:00:00Z"},
            {"metadata": {"name": "event-time"}, "eventTime": "2026-03-01T00:00:00Z"},
        ]
        outputs = {
            tuple(commands[0]): json.dumps(
                {"items": [{"metadata": {"name": "node-1"}}, {"metadata": {"name": "node-2"}}]}
            ),
            tuple(commands[1]): json.dumps(
                {
                    "items": [
                        {"metadata": {"name": "pressure", "labels": {"app": "autoscaler-pressure"}}, "status": {"phase": "Pending"}},
                        {"metadata": {"name": "pressure-2", "labels": {"app": "autoscaler-pressure"}}},
                        {"metadata": {"name": "pressure-3", "labels": {"app": "autoscaler-pressure"}}},
                        {"metadata": {"name": "pressure-4", "labels": {"app": "autoscaler-pressure"}}},
                        {"metadata": {"name": "other", "labels": {"app": "other"}}},
                    ]
                }
            ),
            tuple(commands[2]): json.dumps({"items": events}),
            tuple(commands[3]): "ignore\nScale up one\nUNSCHEDULABLE two\nnode three\nquiet\nscale down four\n",
        }
        seen = []

        def runner(args):
            seen.append(args)
            return outputs[tuple(args)]

        snapshot = watch_demo.collect_snapshot("demo", runner=runner, probe=lambda: True)

        self.assertEqual(seen, commands)
        self.assertEqual(len(snapshot["nodes"]), 2)
        self.assertEqual(len(snapshot["pods"]), 4)
        self.assertTrue(
            all(pod["metadata"]["labels"]["app"] == "autoscaler-pressure" for pod in snapshot["pods"])
        )
        self.assertEqual(len(snapshot["events"]), 12)
        self.assertEqual(snapshot["events"][0]["metadata"]["name"], "event-time")
        self.assertEqual(snapshot["events"][1]["metadata"]["name"], "last")
        self.assertEqual(
            snapshot["decisions"],
            ["Scale up one", "UNSCHEDULABLE two", "node three", "scale down four"],
        )
        self.assertTrue(snapshot["provider_reachable"])
        self.assertEqual(snapshot["errors"], {})
        self.assertIn("collected_at", snapshot)

    def test_collect_snapshot_isolates_malformed_events(self):
        def runner(args):
            if "events" in args:
                return "not json"
            if "logs" in args:
                return "scale up remains visible"
            if "nodes" in args:
                return '{"items": [{"metadata": {"name": "node-1"}}]}'
            return '{"items": [{"metadata": {"name": "pressure", "labels": {"app": "autoscaler-pressure"}}}]}'

        snapshot = watch_demo.collect_snapshot("demo", runner=runner, probe=lambda: True)

        self.assertEqual(snapshot["nodes"][0]["metadata"]["name"], "node-1")
        self.assertEqual(snapshot["pods"][0]["metadata"]["name"], "pressure")
        self.assertEqual(snapshot["events"], [])
        self.assertEqual(snapshot["decisions"], ["scale up remains visible"])
        self.assertIn("events", snapshot["errors"])
        self.assertTrue(snapshot["provider_reachable"])

    def test_collection_errors_do_not_block_other_sources(self):
        def runner(args):
            if "pods" in args:
                raise subprocess.CalledProcessError(1, args)
            if "logs" in args:
                return "node decision"
            return '{"items": []}'

        snapshot = watch_demo.collect_snapshot("demo", runner=runner, probe=lambda: True)

        self.assertEqual(snapshot["nodes"], [])
        self.assertEqual(snapshot["events"], [])
        self.assertEqual(snapshot["decisions"], ["node decision"])
        self.assertIn("pods", snapshot["errors"])

    def test_non_list_items_do_not_block_other_sources(self):
        def runner(args):
            if "pods" in args:
                return '{"items": null}'
            if "nodes" in args:
                return '{"items": [{"metadata": {"name": "node-1"}}]}'
            if "events" in args:
                return '{"items": [{"metadata": {"name": "event-1", "creationTimestamp": "2026-01-01T00:00:00Z"}}]}'
            return "node decision"

        snapshot = watch_demo.collect_snapshot("demo", runner=runner, probe=lambda: True)

        self.assertEqual(snapshot["pods"], [])
        self.assertIn("pods", snapshot["errors"])
        self.assertEqual(snapshot["nodes"][0]["metadata"]["name"], "node-1")
        self.assertEqual(snapshot["events"][0]["metadata"]["name"], "event-1")
        self.assertEqual(snapshot["decisions"], ["node decision"])

    def test_events_are_sorted_by_rfc3339_time(self):
        def runner(args):
            if "events" in args:
                return json.dumps(
                    {
                        "items": [
                            {"metadata": {"name": "whole"}, "eventTime": "2026-01-01T00:00:00Z"},
                            {"metadata": {"name": "fractional"}, "eventTime": "2026-01-01T00:00:00.9Z"},
                        ]
                    }
                )
            if "logs" in args:
                return ""
            return '{"items": []}'

        snapshot = watch_demo.collect_snapshot("demo", runner=runner, probe=lambda: True)

        self.assertEqual(
            [event["metadata"]["name"] for event in snapshot["events"]],
            ["fractional", "whole"],
        )

    def test_infer_phase(self):
        cases = [
            (None, {"nodes": [], "pods": []}, "OBSERVING"),
            (None, {"nodes": [], "pods": [{"status": {"phase": "Pending"}}]}, "PODS PENDING"),
            ({"nodes": [{}]}, {"nodes": [{}, {}], "pods": []}, "SCALING UP"),
            ({"nodes": [{}, {}]}, {"nodes": [{}], "pods": []}, "SCALING DOWN"),
            ({"nodes": []}, {"nodes": [], "pods": [{"status": {"phase": "Pending"}}]}, "PODS PENDING"),
            ({"nodes": []}, {"nodes": [], "pods": [{"status": {"phase": "Running"}}]}, "STABLE"),
        ]
        for previous, current, expected in cases:
            with self.subTest(expected=expected):
                self.assertEqual(watch_demo.infer_phase(previous, current), expected)

    def test_infer_phase_ignores_node_change_when_collection_failed(self):
        previous = {"nodes": [{}, {}], "pods": [], "errors": {}}
        current = {
            "nodes": [],
            "pods": [],
            "errors": {"nodes": "kubectl timed out"},
        }

        self.assertEqual(watch_demo.infer_phase(previous, current), "STABLE")


if __name__ == "__main__":
    unittest.main()
