#!/usr/bin/env python3
"""Black-box controls for matched comparison evidence. No providers invoked."""
import importlib.util
import json
from pathlib import Path
import signal
import subprocess
import sys
import tempfile
import time
from types import SimpleNamespace
import unittest
from unittest import mock


ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / "examples/evaluation"))
SPEC = importlib.util.spec_from_file_location("controls", ROOT / "examples/evaluation/run-offline-controls.py")
CONTROLS = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(CONTROLS)
RUNNER = ROOT / "scripts/compare-workflows.py"


class ComparisonTests(unittest.TestCase):
    def run_comparison(self, config, output, *flags):
        return subprocess.run([sys.executable, str(RUNNER), "--config", str(config), "--output", str(output), *flags],
                              capture_output=True, text=True, timeout=30)

    def test_offline_controls_reject_false_success_and_retain_failures_and_unknowns(self):
        with tempfile.TemporaryDirectory(prefix="amx-comparison-controls-") as temporary:
            output = Path(temporary) / "controls"
            process = subprocess.run([sys.executable, str(ROOT / "examples/evaluation/run-offline-controls.py"), "--output", str(output)],
                                     capture_output=True, text=True, timeout=45)
            self.assertEqual(process.returncode, 0, process.stdout + process.stderr)
            result = json.loads((output / "controls-result.json").read_text())
            self.assertTrue(result["passed"])
            self.assertFalse(result["ai_benchmark"])
            report = json.loads((output / "evidence/comparison.json").read_text())
            self.assertEqual(report["mode"], "fixture")
            for attempt in report["attempts"]:
                repository = output / "evidence" / attempt["directory"] / "repo"
                self.assertTrue((repository / ".git/objects").is_dir())
                self.assertFalse((repository / ".git/objects/info/alternates").exists())
                self.assertFalse((repository / "owner-draft.txt").exists())
                self.assertEqual(len(attempt["checks"]), 1)

    def test_no_checks_no_explicit_mode_and_live_without_authorization_are_refused(self):
        with tempfile.TemporaryDirectory(prefix="amx-comparison-admission-") as temporary:
            root = Path(temporary)
            config, _ = CONTROLS.prepare(root / "control")
            base = json.loads(config.read_text())
            for number, mutation in enumerate(("missing-checks", "no-fixtures-flag", "live-without-allow")):
                value = json.loads(json.dumps(base))
                flags = ["--fixtures"]
                if mutation == "missing-checks":
                    value["tasks"][0]["checks"] = []
                elif mutation == "no-fixtures-flag":
                    flags = []
                else:
                    value["mode"] = "live"
                    flags = []
                candidate = root / f"config-{number}.json"
                candidate.write_text(json.dumps(value))
                output = root / f"result-{number}"
                result = self.run_comparison(candidate, output, *flags)
                self.assertEqual(result.returncode, 2, result.stdout + result.stderr)
                self.assertFalse(output.exists())

    def test_output_reuse_and_unsafe_source_tree_are_refused_without_source_writes(self):
        with tempfile.TemporaryDirectory(prefix="amx-comparison-snapshot-") as temporary:
            root = Path(temporary)
            config, source = CONTROLS.prepare(root / "control")
            before = CONTROLS.fingerprint(source)
            output = root / "exists"
            output.mkdir()
            sentinel = output / "keep.txt"
            sentinel.write_text("keep")
            result = self.run_comparison(config, output, "--fixtures")
            self.assertEqual(result.returncode, 2)
            self.assertEqual(sentinel.read_text(), "keep")
            self.assertEqual(before, CONTROLS.fingerprint(source))
            (source / "link").symlink_to("/etc/passwd")
            CONTROLS.COMPARISON.git(source, "add", "link")
            CONTROLS.COMPARISON.git(source, "-c", "commit.gpgsign=false", "commit", "--quiet", "--no-verify", "-m", "Unsafe link")
            value = json.loads(config.read_text())
            value["source"]["commit"] = CONTROLS.COMPARISON.git(source, "rev-parse", "HEAD").decode().strip()
            config.write_text(json.dumps(value))
            before = CONTROLS.fingerprint(source)
            result = self.run_comparison(config, root / "unsafe-output", "--fixtures")
            self.assertEqual(result.returncode, 2, result.stdout + result.stderr)
            self.assertIn("unsafe source tree", result.stderr)
            self.assertEqual(before, CONTROLS.fingerprint(source))
            self.assertFalse((root / "unsafe-output").exists())

    def test_order_is_reproducible_and_balances_positions_with_full_blocks(self):
        config = {"repeats": 6, "tasks": [{"id": "task"}], "arms": [{"id": name} for name in ("baseline", "agentmux", "competitor")]}
        first = [(repeat, position, arm["id"]) for repeat, _, position, arm in CONTROLS.COMPARISON.schedule(config)]
        self.assertEqual(first, [(repeat, position, arm["id"]) for repeat, _, position, arm in CONTROLS.COMPARISON.schedule(config)])
        self.assertEqual(len(first), 18)
        for arm in ("baseline", "agentmux", "competitor"):
            self.assertEqual([sum(item[1:] == (position, arm) for item in first) for position in range(3)], [2, 2, 2])

    def test_background_metadata_is_bounded_private_and_identity_checked(self):
        comparison = CONTROLS.COMPARISON
        handles = []
        for number in range(35):
            identity = {"pid": 1000 + number, "start_ticks": "12345", "parent_pid": 900}
            before = dict(identity, parent_pid=901)
            after = dict(identity, parent_pid=902)
            if number == 1:
                after["start_ticks"] = "replacement"
            current = mock.Mock(side_effect=[None] if number == 4 else [before, after])
            handles.append(SimpleNamespace(identity=identity, current=current))

        def executable(path):
            pid = int(path.parts[-2])
            if pid == 1003:
                raise OSError("RAW_ERROR_SECRET /private/owner/file")
            basename = "UNSAFE_NAME_SECRET" if pid == 1000 else "compile"
            return "/PRIVATE_PATH_SENTINEL/" + basename

        def session(pid):
            if pid == 1002:
                raise PermissionError("RAW_ERROR_SECRET")
            return 88

        with mock.patch.object(comparison.os, "readlink", side_effect=executable), \
                mock.patch.object(comparison.os, "getpgid", return_value=77), \
                mock.patch.object(comparison.os, "getsid", side_effect=session):
            receipt = comparison.background_process_metadata(handles)
        self.assertEqual(receipt["observed_count"], 35)
        self.assertEqual(receipt["inspected_count"], 32)
        self.assertEqual(receipt["omitted_count"], 3)
        self.assertEqual(receipt["identity_unknown_count"], 2)
        self.assertEqual(len(receipt["processes"]), 30)
        by_pid = {row["pid"]: row for row in receipt["processes"]}
        self.assertNotIn(1001, by_pid, "a recycled identity must publish no process fields")
        self.assertNotIn(1004, by_pid, "an unavailable identity must stay unknown")
        self.assertEqual(by_pid[1000]["unknowns"], {"executable_basename": "unsafe_or_unrecognized"})
        self.assertIsNone(by_pid[1000]["executable_basename"])
        self.assertEqual(by_pid[1002]["unknowns"], {"session_id": "unavailable"})
        self.assertIsNone(by_pid[1002]["session_id"])
        self.assertEqual(by_pid[1003]["unknowns"], {"executable_basename": "unavailable"})
        self.assertEqual(by_pid[1005]["executable_basename"], "compile")
        self.assertEqual(by_pid[1005]["admitted_parent_pid"], 900)
        self.assertEqual(by_pid[1005]["current_parent_pid"], 902)
        for handle in handles[32:]:
            handle.current.assert_not_called()
        serialized = json.dumps(receipt)
        for secret in ("PRIVATE_PATH_SENTINEL", "UNSAFE_NAME_SECRET", "RAW_ERROR_SECRET", "/private/owner"):
            self.assertNotIn(secret, serialized)

    def test_failed_background_diagnostics_preserve_failure_and_cleanup(self):
        comparison = CONTROLS.COMPARISON
        with tempfile.TemporaryDirectory(prefix="amx-comparison-diagnostic-failure-") as temporary:
            root = Path(temporary)
            tree = mock.Mock()
            tree.live.side_effect = [[mock.Mock()], [], []]
            process = mock.Mock(_validation_tree=tree)
            process.wait.return_value = 0
            with mock.patch.object(comparison.processes, "launch_pinned", return_value=process), \
                    mock.patch.object(comparison, "background_process_metadata", side_effect=RuntimeError("PRIVATE_ERROR_SECRET")):
                result = comparison.run_command(["fixture"], root, root / "actor.log", 5, time.monotonic() + 5)
            self.assertFalse(result["passed"])
            self.assertEqual(result["error"], "RuntimeError: command left observed background processes")
            self.assertEqual(result["cleanup_errors"], [])
            self.assertEqual(result["background_processes"]["error_code"], "unavailable")
            self.assertEqual(result["background_processes"]["identity_unknown_count"], 1)
            self.assertNotIn("PRIVATE_ERROR_SECRET", json.dumps(result))
            tree.signal.assert_called_once_with(signal.SIGKILL)
            tree.close.assert_called_once()

    def test_changed_external_evaluator_cannot_report_success(self):
        with tempfile.TemporaryDirectory(prefix="amx-comparison-grader-") as temporary:
            root = Path(temporary)
            config, _ = CONTROLS.prepare(root / "control")
            grader = root / "grader.py"
            grader.write_text("raise SystemExit(9)\n")
            value = json.loads(config.read_text())
            value["tasks"][0]["checks"][0].update(argv=[sys.executable, str(grader)], evaluator_files=[str(grader)])
            code = "from pathlib import Path;Path(" + repr(str(grader)) + ").write_text('raise SystemExit(0)\\n')"
            value["arms"] = [{"id": "tamper", "argv": [sys.executable, "-c", code], "timeout_seconds": 5},
                             {"id": "other", "argv": [sys.executable, "-c", "pass"], "timeout_seconds": 5}]
            config.write_text(json.dumps(value))
            output = root / "evidence"
            process = self.run_comparison(config, output, "--fixtures")
            self.assertEqual(process.returncode, 1, process.stdout + process.stderr)
            report = json.loads((output / "comparison.json").read_text())
            self.assertTrue(report["completed"])
            self.assertFalse(report["passed"])
            for attempt in report["attempts"]:
                self.assertFalse(attempt["passed"])
                self.assertFalse(attempt["checks"][0]["evaluator_intact"])

    def test_repairing_grader_cannot_rescue_incorrect_actor(self):
        with tempfile.TemporaryDirectory(prefix="amx-comparison-repair-") as temporary:
            root = Path(temporary)
            config, _ = CONTROLS.prepare(root / "control")
            grader = root / "repairing-grader.py"
            grader.write_text("from pathlib import Path\nimport sys\nPath(sys.argv[1], 'normalize.py').write_text(\"def normalize(text):\\n    return '-'.join(text.strip().lower().split())\\n\")\n")
            value = json.loads(config.read_text())
            value["tasks"][0]["checks"][0].update(argv=[sys.executable, str(grader), "{repo}"], evaluator_files=[str(grader)])
            value["arms"] = [{"id": name, "argv": [sys.executable, "-c", "pass"], "timeout_seconds": 5} for name in ("baseline", "other")]
            config.write_text(json.dumps(value))
            output = root / "evidence"
            process = self.run_comparison(config, output, "--fixtures")
            self.assertEqual(process.returncode, 1, process.stdout + process.stderr)
            report = json.loads((output / "comparison.json").read_text())
            self.assertTrue(report["completed"])
            self.assertFalse(report["passed"])
            for attempt in report["attempts"]:
                check = attempt["checks"][0]
                self.assertEqual(check["exit_code"], 0)
                self.assertTrue(check["evaluator_intact"])
                self.assertFalse(check["artifact_intact"])
                self.assertFalse(check["passed"])
                self.assertFalse(attempt["passed"])
                self.assertIn("modified candidate artifacts", check["error"])

    def test_source_pin_ignores_replacement_objects(self):
        with tempfile.TemporaryDirectory(prefix="amx-comparison-replace-") as temporary:
            root = Path(temporary)
            config, source = CONTROLS.prepare(root / "control")
            value = json.loads(config.read_text())
            original = value["source"]["commit"]
            (source / "normalize.py").write_text("REPLACEMENT CONTENT\n")
            CONTROLS.COMPARISON.git(source, "add", "normalize.py")
            CONTROLS.COMPARISON.git(source, "-c", "commit.gpgsign=false", "commit", "--quiet", "--no-verify", "-m", "Replacement")
            replacement = CONTROLS.COMPARISON.git(source, "rev-parse", "HEAD").decode().strip()
            CONTROLS.COMPARISON.git(source, "replace", original, replacement)
            before = CONTROLS.fingerprint(source)
            files, _ = CONTROLS.COMPARISON.read_snapshot(value["source"])
            self.assertEqual({name: data for name, _, data in files}, {"normalize.py": b"def normalize(text):\n    return text\n"})
            self.assertEqual(before, CONTROLS.fingerprint(source))

    def test_promisor_source_is_refused_and_observed_background_work_cannot_pass(self):
        with tempfile.TemporaryDirectory(prefix="amx-comparison-processes-") as temporary:
            root = Path(temporary)
            config, source = CONTROLS.prepare(root / "control")
            CONTROLS.COMPARISON.git(source, "config", "remote.partial.promisor", "true")
            before = CONTROLS.fingerprint(source)
            process = self.run_comparison(config, root / "partial", "--fixtures")
            self.assertEqual(process.returncode, 2)
            self.assertIn("partial/promisor", process.stderr)
            self.assertFalse((root / "partial").exists())
            self.assertEqual(before, CONTROLS.fingerprint(source))
            CONTROLS.COMPARISON.git(source, "config", "--unset", "remote.partial.promisor")
            value = json.loads(config.read_text())
            value["arms"] = value["arms"][:2]
            value["arms"][0]["argv"][2] = "background"
            config.write_text(json.dumps(value))
            output = root / "background"
            process = self.run_comparison(config, output, "--fixtures")
            self.assertEqual(process.returncode, 1, process.stdout + process.stderr)
            report = json.loads((output / "comparison.json").read_text())
            attempt = report["attempts"][0]
            self.assertFalse(attempt["execution"]["passed"])
            self.assertIn("background processes", attempt["execution"]["error"])
            self.assertEqual(attempt["execution"]["cleanup_errors"], [])
            pid = int((output / attempt["directory"] / "artifacts/child.pid").read_text())
            receipt = attempt["execution"]["background_processes"]
            self.assertEqual(receipt["observed_count"], 1)
            self.assertEqual(receipt["omitted_count"], 0)
            self.assertEqual(receipt["identity_unknown_count"], 0)
            self.assertEqual(len(receipt["processes"]), 1)
            child = receipt["processes"][0]
            self.assertEqual(child["pid"], pid)
            self.assertTrue(child["start_ticks"].isdigit())
            self.assertEqual(child["executable_basename"], Path(sys.executable).resolve().name)
            self.assertEqual(child["process_group_id"], child["admitted_parent_pid"])
            self.assertEqual(child["session_id"], child["admitted_parent_pid"])
            self.assertEqual(child["unknowns"], {})
            self.assertNotIn(str(root), json.dumps(receipt))
            identity = CONTROLS.COMPARISON.processes.process_identity(pid)
            self.assertTrue(identity is None or identity["state"] == "Z")


if __name__ == "__main__":
    unittest.main()
