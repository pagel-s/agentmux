#!/usr/bin/env python3
"""Regressions for bounded-soak evidence collection and process cleanup."""
import importlib.util
import os
from pathlib import Path
import signal
import subprocess
import time
import unittest

spec = importlib.util.spec_from_file_location("soak", Path(__file__).with_name("soak-managed.py"))
soak = importlib.util.module_from_spec(spec)
spec.loader.exec_module(soak)


class SoakTests(unittest.TestCase):
    def test_final_audit_includes_tasks_past_first_broker_page(self):
        values = [{"id": f"task_{index}"} for index in range(251)]

        def call(operation, arguments):
            self.assertEqual(operation, "task.list")
            start = 0 if not arguments["after"] else 1 + next(
                i for i, value in enumerate(values) if value["id"] == arguments["after"])
            page = values[start:start + arguments["limit"]]
            return {"tasks": page, "cursor": page[-1]["id"] if page else arguments["after"]}

        self.assertEqual(soak.all_tasks(call), values)

    def test_audit_rejects_duplicate_or_nonadvancing_pages(self):
        for page in ({"tasks": [{"id": "same"}], "cursor": "same"},
                     {"tasks": [{"id": "same"}], "cursor": ""}):
            with self.subTest(page=page), self.assertRaises(RuntimeError):
                soak.all_tasks(lambda operation, arguments: page)

    def test_cleanup_stops_descendant_holding_output_pipe_after_parent_exits(self):
        process = subprocess.Popen(["/bin/sh", "-c", "sleep 30 & echo $!; exit 0"],
                                   stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                                   start_new_session=True)
        try:
            process.wait(timeout=5)
            descendant = int(process.stdout.readline())
            started = time.monotonic()
            soak.stop(process)
            self.assertLess(time.monotonic() - started, 10)
            # The parent was already reaped: only the descendant can keep the
            # pipe open. A no-op stop() must fail here and at the PID check.
            process.communicate(timeout=1)
            until = time.monotonic() + 1
            while True:
                status = subprocess.run(["ps", "-o", "stat=", "-p", str(descendant)],
                                        capture_output=True, text=True, timeout=2)
                if not status.stdout.strip() or status.stdout.lstrip().startswith("Z"):
                    break
                if time.monotonic() > until:
                    self.fail("cleanup left the exact descendant process running")
                time.sleep(0.02)
        finally:
            try:
                os.killpg(process.pid, signal.SIGKILL)
            except ProcessLookupError:
                pass
            process.communicate(timeout=5)


if __name__ == "__main__":
    unittest.main()
