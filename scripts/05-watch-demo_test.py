#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.11"
# dependencies = ["rich>=14,<15"]
# ///

import importlib.util
import json
import pathlib
import subprocess
import unittest
from unittest.mock import patch


MODULE_PATH = pathlib.Path(__file__).with_name("05-watch-demo.py")
SPEC = importlib.util.spec_from_file_location("watch_demo", MODULE_PATH)
watch_demo = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(watch_demo)


class WatchDemoTest(unittest.TestCase):
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


if __name__ == "__main__":
    unittest.main()
