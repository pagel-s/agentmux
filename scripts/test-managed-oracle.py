#!/usr/bin/env python3
"""Model-free regressions for independent verifier failures and preflight."""
import importlib.util
import json
from pathlib import Path
import stat
import subprocess
import tempfile
import unittest
from unittest.mock import Mock, patch


def load(name, filename):
    spec = importlib.util.spec_from_file_location(name, Path(__file__).with_name(filename))
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


mixed = load("mixed", "smoke-managed-models.py")
autonomy = load("autonomy", "smoke-managed-autonomy.py")


class OracleTests(unittest.TestCase):
    def invoke(self, result=None, error=None):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        with patch.object(mixed, "executable", side_effect=lambda name: Path("/usr/bin") / name), \
                patch.object(mixed, "oracle_python", return_value=Path("/usr/bin/python3")), \
                patch.object(mixed.subprocess, "run", return_value=result, side_effect=error) as run:
            verdict = mixed.oracle("def normalize_tags(values): return []\n", self.root)
        diagnostics = Path(verdict["diagnostics"])
        self.assertEqual(stat.S_IMODE(diagnostics.stat().st_mode), 0o600)
        self.assertEqual(stat.S_IMODE(diagnostics.parent.stat().st_mode), 0o700)
        return verdict, json.loads(diagnostics.read_text()), run.call_args

    def test_launch_failure_retains_reason_without_printing_child_output(self):
        stderr = "bwrap: execvp /outside/python3: No such file or directory\n"
        verdict, evidence, call = self.invoke(subprocess.CompletedProcess([], 1, "", stderr))
        self.assertFalse(verdict["ok"])
        self.assertEqual(evidence["returncode"], 1)
        self.assertEqual(evidence["stderr"], stderr)
        self.assertNotIn(stderr, verdict["reason"])
        command = evidence["command"]
        self.assertIn("--unshare-all", command)
        self.assertEqual(command[-3:], ["/usr/bin/python3", "-I", "/workspace/oracle.py"])
        self.assertNotIn("AUTH", call.kwargs["env"])

    def test_success_keeps_verdict_and_private_evidence(self):
        verdict, evidence, _ = self.invoke(subprocess.CompletedProcess([], 0, '{"ok": true, "cases": 6}', ""))
        self.assertTrue(verdict["ok"])
        self.assertEqual(verdict["cases"], 6)
        self.assertEqual(evidence["verdict"], {"ok": True, "cases": 6})

    def test_invalid_output_fails_closed_with_diagnostics(self):
        for stdout in ("not json", "[]", '"ok"', '{"ok": false}', '{"ok": 1}'):
            with self.subTest(stdout=stdout):
                verdict, evidence, _ = self.invoke(subprocess.CompletedProcess([], 0, stdout, ""))
                self.assertFalse(verdict["ok"])
                self.assertEqual(evidence["stdout"], stdout)

    def test_timeout_preserves_bounded_partial_output(self):
        error = subprocess.TimeoutExpired(["bwrap"], 15, output=b"x" * 20000, stderr=b"timeout detail")
        verdict, evidence, _ = self.invoke(error=error)
        self.assertFalse(verdict["ok"])
        self.assertEqual(evidence["timeout_seconds"], 15)
        self.assertEqual(len(evidence["stdout"]), 16384)
        self.assertEqual(evidence["stderr"], "timeout detail")

    def test_os_launch_error_is_retained(self):
        verdict, evidence, _ = self.invoke(error=OSError("cannot execute verifier"))
        self.assertFalse(verdict["ok"])
        self.assertEqual(evidence["launch_error"], "cannot execute verifier")

    def test_failed_preflight_stops_both_runners_before_model_turns(self):
        for module in (mixed, autonomy):
            with self.subTest(module=module.__name__):
                runner = object.__new__(module.Acceptance)
                runner.root = Path("/tmp/preflight-test")
                runner.report = {"ok": False}
                runner.prepare = Mock()
                runner.start_broker = Mock()
                # The autonomy runner inherits methods from its own imported
                # mixed module, rather than the separate test import above.
                owner = module.mixed if module is autonomy else module
                with patch.object(owner, "oracle_controls", return_value={"ok": False}):
                    with self.assertRaisesRegex(RuntimeError, "before model turns"):
                        runner.run()
                runner.start_broker.assert_not_called()
                self.assertFalse(runner.report["oracle_preflight"]["ok"])

    def test_final_failure_is_saved_in_report_and_names_diagnostics(self):
        runner = object.__new__(mixed.Acceptance)
        runner.root = Path("/tmp/verdict-test")
        runner.report = {"ok": False}
        verdict = {"ok": False, "reason": "verifier could not start", "diagnostics": "/private/diagnostics.json"}
        with patch.object(mixed, "oracle", return_value=verdict):
            with self.assertRaisesRegex(RuntimeError, "/private/diagnostics.json"):
                runner.verify_artifact("source", "independent_oracle")
        self.assertEqual(runner.report["independent_oracle"], verdict)
        self.assertFalse(runner.report["ok"])

    def test_python_entrypoint_is_visible_without_caller_path(self):
        with tempfile.TemporaryDirectory() as temporary:
            # A host virtualenv/shim may work for the runner but is not mounted
            # in the credential-free verifier. It must never become its argv.
            shim = Path(temporary) / "python3"
            shim.symlink_to("/usr/bin/python3")
            with patch.dict(mixed.os.environ, {"PATH": str(shim.parent)}):
                interpreter = mixed.oracle_python()
            self.assertTrue(interpreter.is_relative_to("/usr"))
            self.assertNotEqual(interpreter, shim)


if __name__ == "__main__":
    unittest.main()
