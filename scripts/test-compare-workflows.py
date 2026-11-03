#!/usr/bin/env python3
"""Black-box controls for matched comparison evidence. No providers invoked."""
import importlib.util
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest


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
            identity = CONTROLS.COMPARISON.processes.process_identity(pid)
            self.assertTrue(identity is None or identity["state"] == "Z")


if __name__ == "__main__":
    unittest.main()
