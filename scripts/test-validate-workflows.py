#!/usr/bin/env python3
"""Regression checks for validation evidence; never invokes provider CLIs."""
import copy
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest


SCRIPT = Path(__file__).with_name("validate-workflows.py")
SPEC = importlib.util.spec_from_file_location("workflow_validator", SCRIPT)
VALIDATOR = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(VALIDATOR)


class ValidationEvidenceTests(unittest.TestCase):
    def test_disposable_git_commands_ignore_inherited_repository_overrides(self):
        with tempfile.TemporaryDirectory(prefix="agentmux-validator-isolation-") as temporary:
            root = Path(temporary)
            outside = root / "owner-project"
            outside.mkdir()
            clean = {key: value for key, value in os.environ.items()
                     if not key.startswith("GIT_")}
            clean.update(GIT_CONFIG_GLOBAL=os.devnull, GIT_CONFIG_NOSYSTEM="1")

            def git(*arguments):
                return subprocess.run(
                    ["git", "-C", str(outside), "-c", "core.hooksPath=/dev/null",
                     "-c", "commit.gpgsign=false", "-c", "user.name=Fixture",
                     "-c", "user.email=fixture@example.invalid", *arguments],
                    env=clean, capture_output=True, check=True, timeout=10,
                ).stdout

            git("init", "-q")
            note = outside / "owner-note.txt"
            note.write_text("committed owner work\n")
            git("add", ".")
            git("commit", "-qm", "owner baseline")
            note.write_text("uncommitted owner work must remain untouched\n")
            original_head = git("rev-parse", "HEAD")
            original_index = (outside / ".git/index").read_bytes()
            override = root / "owner-index"
            override.write_bytes(original_index)
            fixture = root / "fixture-cli"
            fixture.write_text("#!" + sys.executable + "\nimport json\nprint(json.dumps({'ok':True,'result':{}}))\n")
            fixture.chmod(0o700)
            for number, overrides in enumerate((
                {"GIT_DIR": str(outside / ".git")},
                {"GIT_DIR": str(outside / ".git"), "GIT_WORK_TREE": str(outside),
                 "GIT_INDEX_FILE": str(override)},
            )):
                with self.subTest(overrides=sorted(overrides)):
                    process = subprocess.run(
                        [sys.executable, str(SCRIPT), "--binary", str(fixture),
                         "--output", str(root / ("validation-" + str(number))), "--native-comparison"],
                        env=dict(clean, **overrides), capture_output=True, text=True, timeout=30,
                    )
                    self.assertEqual(process.returncode, 0, process.stdout + process.stderr)
                    self.assertEqual(git("rev-parse", "HEAD"), original_head)
                    self.assertEqual((outside / ".git/index").read_bytes(), original_index)
                    self.assertEqual(override.read_bytes(), original_index)
                    self.assertEqual(note.read_text(), "uncommitted owner work must remain untouched\n")
                    self.assertEqual(set(path.name for path in outside.iterdir()), {".git", "owner-note.txt"})

    def test_diagnostics_cannot_silently_ignore_required_pid_containment(self):
        with tempfile.TemporaryDirectory(prefix="agentmux-validator-policy-") as temporary:
            output = Path(temporary) / "must-not-be-created"
            process = subprocess.run(
                [sys.executable, str(SCRIPT), "--binary", sys.executable,
                 "--output", str(output), "--diagnose-antigravity",
                 "--containment", "pid-namespace"],
                capture_output=True, text=True, timeout=10,
            )
            self.assertEqual(process.returncode, 2)
            self.assertIn("do not provide workflow PID containment", process.stderr)
            self.assertFalse(output.exists())

    def test_negative_control_requires_actual_assertion_and_unchanged_content(self):
        check = {"name": "contract", "command": ["python", "-c", "assert False"]}
        valid = {
            "passed": False,
            "tree": "start",
            "ending_tree": "start",
            "content_sha256": "raw",
            "ending_content_sha256": "raw",
            "checks": [{**check, "passed": False, "exit_code": 1,
                        "output": "Traceback (most recent call last):\nAssertionError\n"}],
        }
        self.assertTrue(VALIDATOR.rejected_by_contract(valid, check))
        stderr_failure = copy.deepcopy(valid)
        stderr_failure["checks"][0]["error"] = stderr_failure["checks"][0].pop("output")
        self.assertTrue(VALIDATOR.rejected_by_contract(stderr_failure, check))
        for key, value in (("exit_code", -1), ("exit_code", True),
                           ("output", "FileNotFoundError: no candidate"),
                           ("output", None), ("name", "different"),
                           ("command", ["missing-executable"]), ("passed", True)):
            broken = copy.deepcopy(valid)
            broken["checks"][0][key] = value
            with self.subTest(key=key, value=value):
                self.assertFalse(VALIDATOR.rejected_by_contract(broken, check))
        for key, value in (("ending_tree", "changed"),
                           ("ending_content_sha256", "changed"), ("checks", [])):
            broken = copy.deepcopy(valid)
            broken[key] = value
            with self.subTest(key=key):
                self.assertFalse(VALIDATOR.rejected_by_contract(broken, check))

    def test_comparison_exit_status_includes_evidence_export_failures(self):
        with tempfile.TemporaryDirectory(prefix="agentmux-validator-test-") as temporary:
            root = Path(temporary)
            # A transparent CLI fixture supplies a successful comparison and
            # failed exports. It cannot launch or contact any model provider.
            fixture = root / "fixture-cli"
            fixture.write_text(
                "#!" + sys.executable + "\n"
                "import json,sys\n"
                "ok=sys.argv[2]=='run'\n"
                "print(json.dumps({'ok':ok,'result':{}}))\n"
                "sys.exit(0 if ok else 1)\n"
            )
            fixture.chmod(0o700)
            evidence = root / "evidence"
            process = subprocess.run(
                [sys.executable, str(SCRIPT), "--binary", str(fixture),
                 "--output", str(evidence), "--native-comparison"],
                capture_output=True, text=True, timeout=30,
            )
            record = json.loads((evidence / "validation.json").read_text())
            self.assertEqual(process.returncode, 1, process.stdout + process.stderr)
            self.assertFalse(record["passed"])
            self.assertTrue(record["cases"][0]["passed"])
            self.assertTrue(any(not case["passed"] for case in record["cases"]))


if __name__ == "__main__":
    unittest.main()
