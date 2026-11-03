#!/usr/bin/env python3
"""Safety/evidence regressions for the workflow soak runner; no providers."""
import copy
from contextlib import contextmanager
import importlib.util
import io
import json
import os
from pathlib import Path
import signal
import stat
import subprocess
import sys
import tempfile
import time
from types import SimpleNamespace
import unittest
from unittest import mock


SCRIPT = Path(__file__).with_name("soak-workflows.py")
SPEC = importlib.util.spec_from_file_location("workflow_soak", SCRIPT)
SOAK = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(SOAK)


@contextmanager
def integration_evidence_directory(*, prefix):
    """Keep opt-in integration evidence in a configured sink, including successes."""
    configured = os.environ.get("AGENTMUX_WORKFLOW_TEST_EVIDENCE_ROOT")
    if configured is None:
        with tempfile.TemporaryDirectory(prefix=prefix) as temporary:
            yield temporary
        return
    root = Path(configured)
    if not configured or not root.is_absolute():
        raise ValueError("AGENTMUX_WORKFLOW_TEST_EVIDENCE_ROOT must be an absolute directory")
    root.mkdir(mode=0o700, parents=True, exist_ok=True)
    if not stat.S_ISDIR(root.lstat().st_mode):
        raise ValueError("AGENTMUX_WORKFLOW_TEST_EVIDENCE_ROOT must be a directory, not a symlink")
    temporary = tempfile.mkdtemp(prefix=prefix, dir=root)
    try:
        os.chmod(temporary, 0o700)
        yield temporary
    finally:
        try:
            print("Retained workflow integration test evidence: " + temporary, file=sys.stderr, flush=True)
        except OSError:
            pass  # A broken diagnostic stream must not replace the original test failure.


@unittest.skipUnless(sys.platform == "linux", "process identity auditing requires Linux")
class WorkflowSoakTests(unittest.TestCase):
    def executable(self, root, code):
        path = root / "fixture-cli"
        path.write_text("#!" + sys.executable + "\n" + code)
        path.chmod(0o700)
        return path

    def arguments(self, root, binary, **extra):
        output = root / "evidence"
        output.mkdir(mode=0o700)
        options = dict(output=output, binary=binary, duration_seconds=30, seed=1,
                       iterations=1, storage="private", containment="process-group",
                       isolation="none", scenario=["check_failure"], max_artifact_bytes=64 << 20)
        options.update(extra)
        return SimpleNamespace(**options)

    def test_fixture_disables_automatic_maintenance_before_first_commit(self):
        with tempfile.TemporaryDirectory(prefix="amx-soak-maintenance-") as temporary:
            root = Path(temporary)
            runner = SOAK.Soak(self.arguments(root, self.executable(root, "raise SystemExit(1)\n")))
            git = runner.git
            commits = []

            def inspect_first_commit(repository, *arguments):
                if "commit" in arguments:
                    for key, expected in (("maintenance.auto", b"false"), ("gc.auto", b"0")):
                        self.assertEqual(git(repository, "config", "--local", "--get", key).strip(), expected,
                                         key + " must be set before any fixture commit")
                    commits.append(repository)
                return git(repository, *arguments)

            with mock.patch.object(runner, "git", side_effect=inspect_first_commit):
                repository = runner.project(root)
            self.assertEqual(commits, [repository])
            self.assertEqual(runner.git(repository, "config", "--local", "--get", "maintenance.auto").strip(), b"false")
            self.assertEqual(runner.git(repository, "config", "--local", "--get", "gc.auto").strip(), b"0")

    def test_integration_evidence_defaults_to_temporary_cleanup(self):
        with mock.patch.dict(os.environ):
            os.environ.pop("AGENTMUX_WORKFLOW_TEST_EVIDENCE_ROOT", None)
            with integration_evidence_directory(prefix="amx-evidence-default-") as temporary:
                path = Path(temporary)
                self.assertTrue(path.is_dir())
            self.assertFalse(path.exists())
            with self.assertRaisesRegex(AssertionError, "fixture failure"):
                with integration_evidence_directory(prefix="amx-evidence-default-") as temporary:
                    failed_path = Path(temporary)
                    raise AssertionError("fixture failure")
            self.assertFalse(failed_path.exists())

    def test_configured_integration_evidence_retains_private_unique_roots(self):
        with tempfile.TemporaryDirectory(prefix="amx-evidence-retention-test-") as temporary:
            sink = Path(temporary) / "retained"
            paths = []
            diagnostics = io.StringIO()
            with mock.patch.dict(os.environ, {"AGENTMUX_WORKFLOW_TEST_EVIDENCE_ROOT": str(sink)}), \
                    mock.patch.object(sys, "stderr", diagnostics):
                with integration_evidence_directory(prefix="integration-") as allocated:
                    paths.append(Path(allocated))
                    (paths[-1] / "normal").write_text("completed")
                for error in (AssertionError("fixture failure"), KeyboardInterrupt()):
                    with self.assertRaises(type(error)):
                        with integration_evidence_directory(prefix="integration-") as allocated:
                            paths.append(Path(allocated))
                            (paths[-1] / "interrupted").write_text(type(error).__name__)
                            raise error

                class SubtestFailure(unittest.TestCase):
                    def runTest(inner_self):
                        with integration_evidence_directory(prefix="integration-") as allocated:
                            paths.append(Path(allocated))
                            (paths[-1] / "subtest").write_text("retained")
                            with inner_self.subTest(case="caught failure"):
                                inner_self.fail("fixture subtest failure")

                result = unittest.TestResult()
                SubtestFailure().run(result)
                self.assertEqual(len(result.failures), 1)
                self.assertFalse(result.errors)
            self.assertEqual(len(paths), 4)
            self.assertEqual(len(set(paths)), 4)
            self.assertEqual(set(sink.iterdir()), set(paths))
            for path in paths:
                self.assertEqual(path.parent, sink)
                self.assertEqual(stat.S_IMODE(path.stat().st_mode), 0o700)
                self.assertTrue(any(path.iterdir()))
                self.assertIn("Retained workflow integration test evidence: " + str(path), diagnostics.getvalue())

    def test_integration_evidence_refuses_invalid_sinks(self):
        with tempfile.TemporaryDirectory(prefix="amx-evidence-invalid-test-") as temporary:
            root = Path(temporary)
            ordinary_file = root / "file"
            ordinary_file.write_text("not a directory")
            directory = root / "directory"
            directory.mkdir()
            symlink = root / "symlink"
            symlink.symlink_to(directory, target_is_directory=True)
            for sink in ("", "relative-evidence", str(ordinary_file), str(symlink)):
                with self.subTest(sink=sink), \
                        mock.patch.dict(os.environ, {"AGENTMUX_WORKFLOW_TEST_EVIDENCE_ROOT": sink}):
                    with self.assertRaises((ValueError, OSError)):
                        with integration_evidence_directory(prefix="invalid-"):
                            self.fail("invalid evidence sink admitted an integration test")
            self.assertFalse(list(directory.iterdir()))

    def test_cleanup_never_signals_a_reused_pid(self):
        identity = {"pid": os.getpid(), "start_ticks": "previous-owner", "state": "S"}
        with mock.patch.object(SOAK, "process_identity", return_value={"pid": os.getpid(), "start_ticks": "new-owner", "state": "S"}), mock.patch.object(SOAK.os, "kill") as kill:
            with self.assertRaisesRegex(SOAK.SoakFailure, "admitted pidfd handles"):
                SOAK.stop_identities([identity])
            kill.assert_not_called()

    def test_reused_controller_cannot_adopt_new_owner_descendants(self):
        helper = SOAK.PROCESS
        for changed_during_scan in (False, True):
            with self.subTest(changed_during_scan=changed_during_scan):
                root = mock.Mock(spec=helper.PinnedProcess)
                root.identity = {"pid": 900001, "start_ticks": "admitted", "parent_pid": os.getpid(), "state": "S"}
                root.current.side_effect = [root.identity, None] if changed_during_scan else [None]
                child = mock.Mock(spec=helper.PinnedProcess)
                child.identity = {"pid": 900002, "start_ticks": "unrelated", "parent_pid": 900001, "state": "S"}
                tree = helper.ProcessTree.__new__(helper.ProcessTree)
                tree.root = root
                tree.handles = {(900001, "admitted"): root}
                task = mock.MagicMock()
                (task / "children").read_text.return_value = "900002"
                with mock.patch.object(helper, "Path") as path, \
                        mock.patch.object(helper.PinnedProcess, "open", return_value=child) as admit, \
                        mock.patch.object(os, "kill") as numeric_kill, mock.patch.object(os, "killpg") as group_kill:
                    path.return_value.iterdir.return_value = [task]
                    self.assertEqual(tree.observe(), [])
                    self.assertEqual(tree.descendants(), [])
                    if changed_during_scan:
                        admit.assert_called_once_with(900002, parent_pid=900001)
                        child.close.assert_called_once()
                    else:
                        admit.assert_not_called()
                        path.assert_not_called()
                    child.send_signal.assert_not_called()
                    numeric_kill.assert_not_called()
                    group_kill.assert_not_called()

    def test_pidfd_signals_never_retarget_a_reaped_identity(self):
        process = subprocess.Popen([sys.executable, "-c", "import time; time.sleep(.05)"])
        tree = SOAK.PROCESS.ProcessTree(process)
        try:
            process.wait(timeout=5)
            replacement = dict(tree.root.identity, start_ticks="new-owner", state="S")
            with mock.patch.object(SOAK.PROCESS, "process_identity", return_value=replacement), \
                    mock.patch.object(os, "kill") as numeric_kill, mock.patch.object(os, "killpg") as group_kill:
                self.assertIsNone(tree.root.current())
                self.assertEqual(tree.observe(), [])
                self.assertFalse(tree.root.send_signal(signal.SIGKILL))
                numeric_kill.assert_not_called()
                group_kill.assert_not_called()
        finally:
            tree.close()

    def test_missing_pidfd_support_refuses_admission_before_any_process(self):
        with tempfile.TemporaryDirectory(prefix="amx-soak-no-pidfds-") as temporary:
            output = Path(temporary) / "evidence"
            with mock.patch.object(SOAK.PROCESS, "require_process_support", side_effect=RuntimeError("pidfd support unavailable")), \
                    mock.patch.object(SOAK.subprocess, "Popen") as launch:
                with self.assertRaises(SystemExit) as stopped:
                    SOAK.main(["--binary", sys.executable, "--output", str(output)])
                self.assertEqual(stopped.exception.code, 2)
                launch.assert_not_called()
                self.assertFalse(output.exists())

    def test_denied_pidfd_signaling_refuses_admission_before_any_process(self):
        with tempfile.TemporaryDirectory(prefix="amx-soak-denied-pidfd-signal-") as temporary:
            output = Path(temporary) / "evidence"
            close = os.close
            with mock.patch.object(SOAK.PROCESS.signal, "pidfd_send_signal", side_effect=PermissionError("signaling denied")) as send, \
                    mock.patch.object(SOAK.PROCESS.os, "close", wraps=close) as closed, \
                    mock.patch.object(SOAK.subprocess, "Popen") as launch:
                with self.assertRaises(SystemExit) as stopped:
                    SOAK.main(["--binary", sys.executable, "--output", str(output)])
                self.assertEqual(stopped.exception.code, 2)
                send.assert_called_once()
                self.assertEqual(send.call_args.args[1], 0)
                closed.assert_called_once_with(send.call_args.args[0])
                launch.assert_not_called()
                self.assertFalse(output.exists())

    def test_failed_process_pin_never_executes_the_target(self):
        with tempfile.TemporaryDirectory(prefix="amx-soak-pin-admission-") as temporary:
            root = Path(temporary)
            marker = root / "executed"
            target = self.executable(root, "from pathlib import Path\nPath(" + repr(str(marker)) + ").touch()\n")
            runner = SOAK.Soak(self.arguments(root, target))
            runner.current = {"directory": str(root), "commands": [], "assertions": [], "state": "running"}
            admitted = []
            popen = subprocess.Popen
            def launch(*args, **kwargs):
                process = popen(*args, **kwargs)
                admitted.append(process)
                return process
            with mock.patch.object(SOAK.PROCESS, "ProcessTree", side_effect=RuntimeError("pin admission failed")), \
                    mock.patch.object(subprocess, "Popen", side_effect=launch), \
                    mock.patch.object(os, "kill") as numeric_kill, mock.patch.object(os, "killpg") as group_kill:
                with self.assertRaisesRegex(RuntimeError, "pin admission failed"):
                    runner.launch("failed-admission", [])
                self.assertEqual(len(admitted), 1)
                self.assertEqual(admitted[0].returncode, 125)
                self.assertFalse(marker.exists())
                self.assertEqual(runner.active, [])
                numeric_kill.assert_not_called()
                group_kill.assert_not_called()

    def test_observation_failure_still_stops_the_pinned_controller(self):
        with tempfile.TemporaryDirectory(prefix="amx-soak-failed-observation-") as temporary:
            root = Path(temporary)
            runner = SOAK.Soak(self.arguments(root, self.executable(root, "import time\ntime.sleep(60)\n")))
            runner.current = {"directory": str(root), "commands": [], "assertions": [], "state": "running"}
            item = runner.launch("observe-failure", [])
            with mock.patch.object(runner, "observe", side_effect=OSError("output observation failed")):
                self.assertEqual(runner.fail(OSError("primary observation failed")), 1)
            self.assertIsNotNone(item["process"].returncode)
            record = SOAK.read_json(runner.root / "soak.json")
            self.assertEqual(record["state"], "failed")
            self.assertFalse(record["completed"])
            self.assertIn("cleanup", record["cleanup_error"])

    def finalization_probe(self, root):
        """Real subprocess signals and BaseExceptions at otherwise successful boundaries."""
        probe = root / "finalization-probe.py"
        probe.write_text("""import builtins, importlib.util, os, signal, sys, time
from pathlib import Path
spec = importlib.util.spec_from_file_location('finalization_soak', sys.argv[1])
soak = importlib.util.module_from_spec(spec); spec.loader.exec_module(soak)
stage, outcome, output = sys.argv[2:5]
fired = False
class FinalizationFault(BaseException): pass
def fault(selected):
    global fired
    if fired or stage != selected: return
    fired = True
    if outcome in ('sigint', 'sigterm'):
        os.kill(os.getpid(), signal.SIGINT if outcome == 'sigint' else signal.SIGTERM)
    elif outcome == 'brokenpipe': raise BrokenPipeError('finalization output fault')
    else: raise FinalizationFault('finalization boundary fault')
cleanup = soak.Soak.cleanup
def finish_cleanup(self, *args, **kwargs):
    result = cleanup(self, *args, **kwargs)
    if kwargs.get('expect_stopped'): fault('cleanup')
    return result
soak.Soak.cleanup = finish_cleanup
artifacts = soak.artifact_bytes
def scan_artifacts(path):
    result = artifacts(path); fault('artifact-scan'); return result
soak.artifact_bytes = scan_artifacts
persist = soak.private_json
def retain(path, value):
    result = persist(path, value)
    if path.name == 'case.json' and value.get('state') == 'not_applicable': fault('case-evidence')
    if path.name == 'soak.json' and value.get('state') == 'finalizing': fault('finalizing-evidence')
    if path.name == 'soak.json' and value.get('state') == 'passed': fault('success-evidence')
    return result
soak.private_json = retain
printing = builtins.print
def emit(*args, **kwargs):
    global fired
    evidence = args and str(args[0]).startswith('Evidence:')
    if evidence and stage == 'closed-pipe' and not fired:
        fired = True
        Path(output + '.ready').touch()
        until = time.monotonic() + 5
        while not Path(output + '.release').exists():
            if time.monotonic() >= until: raise RuntimeError('closed-pipe test readiness expired')
            time.sleep(0.01)
    result = printing(*args, **kwargs)
    if evidence: fault('evidence-print')
    return result
builtins.print = emit
set_handler = signal.signal
handler_calls = 0
def select_handler(*args, **kwargs):
    global handler_calls
    result = set_handler(*args, **kwargs)
    handler_calls += 1
    if handler_calls == 3: fault('handler-restore')
    if handler_calls == 4: fault('handler-restore-last')
    return result
signal.signal = select_handler
raise SystemExit(soak.main(['--binary', sys.executable, '--output', output,
                          '--iterations', '1', '--scenario', 'isolation_boundary',
                          '--containment', 'process-group']))
""")
        return probe

    def test_late_finalization_signals_cannot_publish_passing_evidence(self):
        with tempfile.TemporaryDirectory(prefix="amx-soak-late-finalization-signals-") as temporary:
            root = Path(temporary)
            probe = self.finalization_probe(root)
            for stage in ("cleanup", "artifact-scan", "case-evidence", "finalizing-evidence",
                          "evidence-print", "success-evidence", "handler-restore", "handler-restore-last"):
                for outcome in ("sigint", "sigterm"):
                    with self.subTest(stage=stage, outcome=outcome):
                        output = root / (stage + "-" + outcome)
                        process = subprocess.run([sys.executable, str(probe), str(SCRIPT), stage, outcome, str(output)],
                                                 capture_output=True, text=True, timeout=10)
                        self.assertEqual(process.returncode, 1, process.stdout + process.stderr)
                        record = SOAK.read_json(output / "soak.json")
                        self.assertEqual(record["state"], "interrupted")
                        self.assertIs(record["completed"], False)
                        self.assertTrue(any(name in record["error"] for name in ("SoakInterrupted", "KeyboardInterrupt")), record["error"])
                        self.assertEqual(record["cases"][0]["state"], "interrupted")

    def test_late_finalization_exceptions_revoke_even_a_persisted_success(self):
        with tempfile.TemporaryDirectory(prefix="amx-soak-late-finalization-errors-") as temporary:
            root = Path(temporary)
            probe = self.finalization_probe(root)
            for stage in ("cleanup", "artifact-scan", "case-evidence", "finalizing-evidence",
                          "evidence-print", "success-evidence", "handler-restore", "handler-restore-last"):
                for outcome in ("brokenpipe", "baseexception"):
                    with self.subTest(stage=stage, outcome=outcome):
                        output = root / (stage + "-" + outcome)
                        process = subprocess.run([sys.executable, str(probe), str(SCRIPT), stage, outcome, str(output)],
                                                 capture_output=True, text=True, timeout=10)
                        self.assertEqual(process.returncode, 1, process.stdout + process.stderr)
                        record = SOAK.read_json(output / "soak.json")
                        self.assertEqual(record["state"], "failed")
                        self.assertIs(record["completed"], False)
                        self.assertIn("BrokenPipeError" if outcome == "brokenpipe" else "FinalizationFault", record["error"])
                        self.assertEqual(record["cases"][0]["state"], "failed")

    def test_closed_stdout_during_final_evidence_output_retains_failure(self):
        with tempfile.TemporaryDirectory(prefix="amx-soak-finalization-closed-pipe-") as temporary:
            root = Path(temporary)
            probe = self.finalization_probe(root)
            output = root / "evidence"
            process = subprocess.Popen([sys.executable, str(probe), str(SCRIPT), "closed-pipe", "brokenpipe", str(output)],
                                       stdout=subprocess.PIPE, stderr=subprocess.PIPE)
            try:
                until = time.monotonic() + 10
                while not Path(str(output) + ".ready").exists():
                    self.assertIsNone(process.poll(), "probe stopped before final stdout readiness")
                    self.assertLess(time.monotonic(), until, "probe never reached final stdout")
                    time.sleep(0.01)
                process.stdout.close()
                process.stdout = None
                Path(str(output) + ".release").touch()
                _, stderr = process.communicate(timeout=10)
                self.assertNotEqual(process.returncode, 0, stderr)
                record = SOAK.read_json(output / "soak.json")
                self.assertEqual(record["state"], "failed")
                self.assertIs(record["completed"], False)
                self.assertIn("BrokenPipeError", record["error"])
                self.assertIn("BrokenPipeError", record["output_error"])
            finally:
                if process.poll() is None:
                    process.kill()
                    process.communicate(timeout=5)

    def test_failure_finalization_retains_secondary_cleanup_output_and_evidence_errors(self):
        with tempfile.TemporaryDirectory(prefix="amx-soak-secondary-finalization-errors-") as temporary:
            root = Path(temporary)
            runner = SOAK.Soak(self.arguments(root, self.executable(root, "raise SystemExit(1)\n")))
            runner.cleanup = mock.Mock(side_effect=RuntimeError("cleanup failure"))
            runner.save = mock.Mock(side_effect=OSError("case evidence failure"))
            with mock.patch("builtins.print", side_effect=BrokenPipeError("diagnostic output failure")):
                self.assertEqual(runner.fail(SOAK.SoakFailure("primary failure")), 1)
            record = SOAK.read_json(runner.root / "soak.json")
            self.assertEqual(record["state"], "failed")
            self.assertIs(record["completed"], False)
            for key, fragment in (("error", "primary failure"), ("cleanup_error", "cleanup failure"),
                                  ("output_error", "diagnostic output failure"), ("evidence_error", "case evidence failure")):
                self.assertIn(fragment, record[key])

    def test_apply_markers_are_exact_ignored_source_paths_without_hiding_owner_changes(self):
        with tempfile.TemporaryDirectory(prefix="amx-soak-apply-markers-") as temporary:
            root = Path(temporary)
            runner = SOAK.Soak(self.arguments(root, self.executable(root, "raise SystemExit(1)\n")))
            repository = runner.project(root)
            before = runner.source(repository)
            _, *markers = runner.git_fault(root, repository)
            self.assertEqual({path.name for path in markers}, set(SOAK.FAULT_MARKER_NAMES))
            for path in markers:
                self.assertEqual(path.parent, repository)
                path.write_text("fixture control\n")
                self.assertEqual(runner.git(repository, "check-ignore", "--", path.name).decode().strip(), path.name)
            self.assertEqual(runner.source(repository), before)
            # Only the four named control files are exempt, not their prefix.
            neighbor = repository / ".agentmux-soak-owner-note"
            neighbor.write_text("owner content\n")
            observed = runner.source(repository)
            self.assertNotEqual(observed, before)
            self.assertIn(neighbor.name, observed["files"])
            neighbor.unlink()
            (repository / "owner.bin").write_bytes(b"changed owner bytes")
            self.assertNotEqual(runner.source(repository), before)

    @unittest.skipUnless(os.environ.get("AGENTMUX_SANDBOX_INTEGRATION") == "1", "set AGENTMUX_SANDBOX_INTEGRATION=1 for real namespace regression")
    def test_full_namespace_apply_fault_readiness_and_source_preservation(self):
        selected = os.environ.get("AGENTMUX_INTEGRATION_BINARY")
        self.assertTrue(selected, "required full integration needs AGENTMUX_INTEGRATION_BINARY")
        binary = Path(selected).resolve(strict=True)
        with integration_evidence_directory(prefix="amx-soak-full-apply-faults-") as temporary:
            output = Path(temporary) / "evidence"
            process = subprocess.run(
                [sys.executable, str(SCRIPT), "--binary", str(binary), "--output", str(output),
                 "--iterations", "1", "--duration-seconds", "90", "--containment", "pid-namespace",
                 "--storage", "private", "--isolation", "bubblewrap",
                 "--scenario", "crash_apply_before", "--scenario", "crash_apply_after", "--scenario", "concurrent_apply",
                 "--scenario", "isolation_boundary", "--scenario", "guided_history"],
                capture_output=True, text=True, timeout=100,
            )
            self.assertEqual(process.returncode, 0, process.stdout + process.stderr)
            record = json.loads((output / "soak.json").read_text())
            self.assertEqual(record["state"], "passed")
            self.assertEqual(len(record["cases"]), 5)
            for case in record["cases"]:
                self.assertEqual(case["state"], "passed")
                details = json.loads((Path(case["directory"]) / "case.json").read_text())
                if case["scenario"] not in ("crash_apply_before", "crash_apply_after", "concurrent_apply"):
                    continue
                self.assertTrue(any("source Git objects" in assertion for assertion in details["assertions"]))
                source = Path(case["directory"]) / "project"
                self.assertEqual((source / "owner.txt").read_bytes(), b"unstaged owner work\n")
                self.assertEqual((source / "owner.bin").read_bytes(), bytes([0, 1, 2, 255]))
                self.assertEqual((source / SOAK.FAULT_MARKER_NAMES[2]).read_text(), "1")

    def test_isolation_oracle_permits_private_tmp_writes_and_rejects_host_writes(self):
        with tempfile.TemporaryDirectory(prefix="amx-soak-boundary-oracle-") as temporary:
            root = Path(temporary)
            namespace = root / "private-tmp"
            namespace.mkdir()
            runner = SOAK.Soak(self.arguments(root, self.executable(root, "raise SystemExit(1)\n"), isolation="bubblewrap"))
            runner.current = {"assertions": [], "directory": str(root)}
            runner.project = lambda case: case
            runner.source = lambda repository: {"unchanged": True}
            worktree = root / "run/candidates/writer/worktree"
            worktree.mkdir(parents=True)

            def isolated_fixture(repository, plan, network_error=OSError("network disabled")):
                code = plan["candidates"][0]["command"][-1]
                def private_path(value):
                    path = namespace / str(value).lstrip("/") if str(value).startswith("/") else worktree / value
                    path.parent.mkdir(parents=True, exist_ok=True)
                    return path
                # Execute the actual probe with a separate private filesystem
                # view. The integration soak supplies the real kernel boundary.
                with mock.patch("pathlib.Path", private_path), mock.patch("socket.create_connection", side_effect=network_error):
                    exec(code, {})
                self.assertTrue(any(namespace.rglob("outside-write")))

            runner.run_plan = isolated_fixture
            listener = mock.MagicMock()
            listener.getsockname.return_value = ("127.0.0.1", 12345)
            listener.accept.side_effect = TimeoutError()
            with mock.patch("socket.socket") as socket_factory:
                socket_factory.return_value.__enter__.return_value = listener
                runner.case_isolation_boundary(root)
                self.assertFalse((root / "outside-write").exists())
                # Removing the post-assertion artifact or retaining a prior
                # fixture's nonce cannot pass even if the host stayed untouched.
                proof = worktree / ".agentmux-soak-isolation-proof.json"
                runner.run_plan = lambda repository, plan: None
                with self.assertRaisesRegex(SOAK.SoakFailure, "post-assertion fixture proof"):
                    runner.case_isolation_boundary(root)
                proof.unlink()
                with self.assertRaises(FileNotFoundError):
                    runner.case_isolation_boundary(root)
                exposed_secret = namespace / str(root / "host-secret").lstrip("/")
                exposed_secret.write_text("unexpectedly exposed host secret")
                runner.run_plan = isolated_fixture
                with self.assertRaisesRegex(AssertionError, "host secret was exposed"):
                    runner.case_isolation_boundary(root)
                self.assertFalse(proof.exists(), "secret assertion failure published probe proof")
                exposed_secret.unlink()
                runner.run_plan = lambda repository, plan: isolated_fixture(repository, plan, network_error=None)
                with self.assertRaisesRegex(AssertionError, "host network escape"):
                    runner.case_isolation_boundary(root)
                self.assertFalse(proof.exists(), "network assertion failure published probe proof")
                def host_write(repository, plan):
                    isolated_fixture(repository, plan)
                    (root / "outside-write").write_text("host escape")
                runner.run_plan = host_write
                with self.assertRaisesRegex(SOAK.SoakFailure, "refused outside writes"):
                    runner.case_isolation_boundary(root)

    def test_successful_compare_requires_retained_fixture_attempt_and_exact_checks(self):
        with tempfile.TemporaryDirectory(prefix="amx-soak-positive-oracle-") as temporary:
            root = Path(temporary)
            runner = SOAK.Soak(self.arguments(root, self.executable(root, "raise SystemExit(1)\n")))
            case = runner.root / "case"
            case.mkdir()
            runner.current = {"directory": str(case), "commands": [], "assertions": []}
            repository = runner.project(case)
            plan = runner.plan(repository)
            output = case / "run"
            worktree = output / "candidates/writer/worktree"
            worktree.mkdir(parents=True)
            (worktree / "answer.txt").write_bytes(b"42\n")
            (worktree / "turn-count.txt").write_bytes(b"1")
            verification = {"passed": True, "repository": str(worktree), "tree": "b" * 40, "ending_tree": "b" * 40,
                            "content_sha256": "c" * 64, "ending_content_sha256": "c" * 64, "process_containment": "process-group",
                            "storage": "private", "ignore_sha256": "a" * 64, "selection_sha256": "e" * 64,
                            "checks": [dict(plan["checks"][0], passed=True, exit_code=0)]}
            valid = {"version": 1, "id": "fixture-run", "repository": str(repository), "output": str(output),
                     "mode": "compare", "state": "completed", "goal": plan["goal"], "execution": "local",
                     "storage": "private", "process_containment": "process-group",
                     "snapshot": {"tree": "b" * 40, "content_sha256": "c" * 64, "ignore_sha256": "a" * 64, "selection_sha256": "d" * 64},
                     "candidates": [{"id": "writer", "harness": "command", "attempts": 1, "state": "verified",
                                     "worktree": str(worktree), "verification": verification,
                                     "final_snapshot": {"tree": "b" * 40, "content_sha256": "c" * 64, "ignore_sha256": "a" * 64, "selection_sha256": "e" * 64}}]}
            def exercise(report, retained=None):
                SOAK.private_json(output / "report.json", report if retained is None else retained)
                runner.call = lambda *args, **kwargs: report
                return runner.run_plan(repository, plan)
            self.assertEqual(exercise(valid), valid)
            invalid = [{}, {"state": "completed"}, dict(valid, version=True), dict(valid, candidates=[]), dict(valid, repository="/wrong-source"),
                       dict(valid, output="/wrong-result"), dict(valid, state="failed"), dict(valid, goal="other-fixture")]
            missing = copy.deepcopy(valid)
            del missing["candidates"]
            invalid.append(missing)
            for field, value in (("id", "other-writer"), ("harness", "codex"), ("attempts", 0), ("attempts", 2), ("attempts", True), ("attempts", 1.0),
                                 ("state", "failed"), ("error", "failed attempt"), ("verification", None)):
                row = copy.deepcopy(valid)
                row["candidates"][0][field] = value
                invalid.append(row)
            for field, value in (("checks", []), ("passed", False), ("repository", "/wrong-worktree"), ("ending_tree", "changed")):
                row = copy.deepcopy(valid)
                row["candidates"][0]["verification"][field] = value
                invalid.append(row)
            for field, value in (("command", ["/bin/true"]), ("name", "undeclared-check"), ("passed", False), ("exit_code", 7), ("exit_code", False), ("exit_code", 0.0)):
                row = copy.deepcopy(valid)
                row["candidates"][0]["verification"]["checks"][0][field] = value
                invalid.append(row)
            for number, row in enumerate(invalid):
                with self.subTest(invalid=number), self.assertRaises(SOAK.SoakFailure):
                    exercise(row)
            with self.assertRaisesRegex(SOAK.SoakFailure, "exactly matches"):
                exercise(valid, dict(valid, state="failed"))
            (worktree / "turn-count.txt").write_bytes(b"0")
            with self.assertRaisesRegex(SOAK.SoakFailure, "single-turn proof"):
                exercise(valid)

    def test_complete_stale_source_soak_refuses_malformed_or_unbound_revision_pins(self):
        # A complete passing fixture retains real answer/count files and reaches
        # the stale-source rejection; malformed variants must fail its run oracle.
        cases = [
            ("", True, "private"),
            ("snapshot['tree']='b'*64;verification.update(tree='b'*64,ending_tree='b'*64);final['tree']='b'*64", True, "private"),
            ("", True, "shared"),
            ("snapshot.update(tree=True,content_sha256=True)", False, "private"),
            ("verification.update(tree=True,ending_tree=1,content_sha256=True,ending_content_sha256=1.0);final.update(tree=1.0,content_sha256=1)", False, "private"),
            ("snapshot['content_sha256']=''", False, "private"),
            ("verification['content_sha256']='g'*64;verification['ending_content_sha256']='g'*64;final['content_sha256']='g'*64", False, "private"),
            ("snapshot.pop('selection_sha256')", False, "private"),
            ("verification['selection_sha256']=True;final['selection_sha256']=1.0", False, "private"),
            ("final['selection_sha256']='f'*64", False, "private"),
            ("verification['ignore_sha256']='f'*64;final['ignore_sha256']='f'*64", False, "private"),
            ("result['isolation']=False", False, "private"),
            ("result['isolation']=None", True, "private"),
            ("result['storage']=False", False, "shared"),
            ("verification['storage']=False", False, "shared"),
            ("verification['isolation']=False", False, "private"),
            ("verification['isolation']=''", False, "private"),
            ("result['error']=False", False, "private"),
            ("candidate['error']=0", False, "private"),
            ("verification['checks'][0]['error']=False", False, "private"),
            ("result['error']=None;candidate['error']='';verification['checks'][0]['error']=None", True, "private"),
        ]
        with tempfile.TemporaryDirectory(prefix="amx-soak-complete-digest-oracle-") as temporary:
            for number, (mutation, passing, storage) in enumerate(cases):
                with self.subTest(mutation=mutation, storage=storage):
                    root = Path(temporary) / str(number)
                    root.mkdir()
                    binary = self.executable(root,
                        "import json,sys\nfrom pathlib import Path\n"
                        "if sys.argv[2]=='apply':\n print(json.dumps({'ok':False,'error':'source changed since fixture snapshot','result':{'applied':False}}));raise SystemExit(1)\n"
                        "plan=json.loads(Path(sys.argv[3]).read_text());output=Path(sys.argv[sys.argv.index('--output')+1]);output.mkdir()\n"
                        "worktree=output/'candidates/writer/worktree';worktree.mkdir(parents=True)\n"
                        "(worktree/'answer.txt').write_bytes(b'42\\n');(worktree/'turn-count.txt').write_bytes(b'1')\n"
                        "snapshot={'tree':'b'*40,'content_sha256':'c'*64,'ignore_sha256':'a'*64,'selection_sha256':'d'*64}\n"
                        "verification=dict(snapshot,storage=plan['storage'],process_containment=plan['process_containment'],repository=str(worktree),passed=True,ending_tree='b'*40,ending_content_sha256='c'*64,selection_sha256='e'*64,checks=[dict(plan['checks'][0],passed=True,exit_code=0)])\n"
                        "final=dict(snapshot,selection_sha256='e'*64)\n"
                        "if plan['storage']=='shared':\n for row in (snapshot,verification,final):row.pop('ignore_sha256');row.pop('selection_sha256')\n"
                        "candidate={'id':'writer','harness':'command','attempts':1,'state':'verified','worktree':str(worktree),'verification':verification,'final_snapshot':final}\n"
                        "result={'version':1,'id':'fixture-run','repository':plan['repository'],'output':str(output),'mode':'compare','state':'completed','goal':plan['goal'],'execution':'local','storage':plan['storage'],'process_containment':plan['process_containment'],'snapshot':snapshot,'candidates':[candidate]}\n"
                        + mutation + "\n"
                        "(output/'report.json').write_text(json.dumps(result));print(json.dumps({'ok':True,'result':result}))\n")
                    output = root / "evidence"
                    process = subprocess.run([sys.executable, str(SCRIPT), "--binary", str(binary), "--output", str(output),
                                              "--iterations", "1", "--scenario", "stale_source", "--containment", "process-group", "--storage", storage],
                                             capture_output=True, text=True, timeout=20)
                    record = SOAK.read_json(output / "soak.json")
                    self.assertEqual(process.returncode, 0 if passing else 1, process.stdout + process.stderr)
                    self.assertEqual(record["state"], "passed" if passing else "failed")
                    self.assertIs(record["completed"], passing)
                    self.assertEqual(record["model_calls"], 0)
                    details = SOAK.read_json(Path(record["cases"][0]["directory"]) / "case.json")
                    self.assertEqual([row["label"] for row in details["commands"]], ["run", "stale-apply"] if passing else ["run"])

    @unittest.skipUnless(os.environ.get("AGENTMUX_SANDBOX_INTEGRATION") == "1", "set AGENTMUX_SANDBOX_INTEGRATION=1 for real review-evidence mutation regressions")
    def test_complete_real_fixture_journeys_reject_missing_or_contradictory_review_evidence(self):
        selected = os.environ.get("AGENTMUX_INTEGRATION_BINARY")
        self.assertTrue(selected, "review integration needs AGENTMUX_INTEGRATION_BINARY")
        binary = Path(selected).resolve(strict=True)
        cases = [
            ("guided_history", ""), ("review_rejection", ""),
            ("guided_history", "r.pop('review_context',None)\nfor row in r['steps']:row.pop('review_context',None)"),
            ("guided_history", "r['review_context']='reused'"),
            ("guided_history", "reviews[0]['review_context']='reused'"),
            ("guided_history", "reviews[0].pop('read_only')"),
            ("guided_history", "reviews[-1].pop('gate_review')"),
            ("guided_history", "reviews[0]['gate_review']=True"),
            ("guided_history", "reviews[0]['review'].pop('findings')"),
            ("guided_history", "reviews[0]['review']['summary']=' '"),
            ("guided_history", "reviews[0]['review']['findings']=[{'file':'answer.txt','line':1,'message':'contradicts approval'}]"),
            ("guided_history", "reviews[0]['starting_snapshot']['tree']='f'*40"),
            ("guided_history", "reviews[-1]['final_snapshot']['selection_sha256']='f'*64"),
            ("guided_history", "reviews[0]['starting_snapshot']['ignore_sha256']='f'*64"),
            ("guided_history", "fake=dict(r['steps'][0]['final_snapshot'],tree='f'*40,content_sha256='0'*64,selection_sha256='1'*64)\nr['steps'][0]['final_snapshot']=fake\nreviews[0]['starting_snapshot']=dict(fake)\nreviews[0]['final_snapshot']=dict(fake)"),
            ("guided_history", "r['steps'][-1]['verification'].update(tree='f'*40,ending_tree='f'*40,content_sha256='0'*64,ending_content_sha256='0'*64,selection_sha256='1'*64)"),
            ("guided_history", "r['steps'][2]['final_snapshot'].update(tree='f'*40,content_sha256='0'*64,selection_sha256='1'*64)\nr['steps'][2]['verification'].update(tree='f'*40,ending_tree='f'*40,content_sha256='0'*64,ending_content_sha256='0'*64,selection_sha256='1'*64)"),
            ("guided_history", "r['steps'][0]['starting_snapshot']={'tree':True}"),
            ("guided_history", "r['steps'][2]['starting_snapshot'].update(tree='f'*40,content_sha256='0'*64,selection_sha256='1'*64)"),
            ("guided_history", "r['steps'][-1]['starting_snapshot']=True\nr['steps'][-1]['final_snapshot']={'tree':False}"),
            ("guided_history", "application:r['uncertain']=0"),
            ("guided_history", "application:r['uncertain']=None"),
            ("review_rejection", "reviews[0].pop('review_context')"),
            ("review_rejection", "reviews[0]['read_only']=False"),
            ("review_rejection", "reviews[0]['gate_review']=False"),
            ("review_rejection", "reviews[0]['final_snapshot']['content_sha256']='0'*64"),
            ("review_rejection", "reviews[0]['review']['findings'][0]['line']=True"),
        ]
        with integration_evidence_directory(prefix="amx-soak-real-review-oracle-") as temporary:
            for number, (scenario, mutation) in enumerate(cases):
                with self.subTest(scenario=scenario, mutation=mutation):
                    root = Path(temporary) / str(number)
                    root.mkdir()
                    application_mutation = mutation[len("application:"):] if mutation.startswith("application:") else ""
                    report_mutation = "" if application_mutation else mutation
                    wrapper = self.executable(root,
                        "import json,subprocess,sys\nfrom pathlib import Path\n"
                        "process=subprocess.run([" + repr(str(binary)) + "]+sys.argv[1:],capture_output=True)\n"
                        "stdout=process.stdout\n"
                        "try: envelope=json.loads(stdout)\nexcept (ValueError,UnicodeDecodeError): envelope={}\n"
                        "r=envelope.get('result')\n"
                        "if isinstance(r,dict) and isinstance(r.get('steps'),list):\n"
                        " reviews=[row for row in r['steps'] if 'review' in row]\n"
                        " if reviews:\n"
                        + ("  " + report_mutation.replace("\n", "\n  ") + "\n" if report_mutation else "  pass\n")
                        + "  (Path(r['output'])/'report.json').write_text(json.dumps(r))\n"
                        "  stdout=json.dumps(envelope).encode()\n"
                        "if isinstance(r,dict) and r.get('applied') is True:\n"
                        + (" " + application_mutation + "\n" if application_mutation else " pass\n")
                        + " stdout=json.dumps(envelope).encode()\n"
                        "sys.stdout.buffer.write(stdout);sys.stderr.buffer.write(process.stderr);raise SystemExit(process.returncode)\n")
                    output = root / "evidence"
                    process = subprocess.run([sys.executable, str(SCRIPT), "--binary", str(wrapper), "--output", str(output),
                                              "--iterations", "1", "--scenario", scenario, "--containment", "process-group"],
                                             capture_output=True, text=True, timeout=30)
                    record = SOAK.read_json(output / "soak.json")
                    self.assertEqual(process.returncode, 1 if mutation else 0, process.stdout + process.stderr)
                    self.assertEqual(record["state"], "failed" if mutation else "passed")
                    self.assertIs(record["completed"], not bool(mutation))
                    self.assertEqual(record["model_calls"], 0)
                    details = SOAK.read_json(Path(record["cases"][0]["directory"]) / "case.json")
                    report = SOAK.read_json(Path(record["cases"][0]["directory"]) / "run/report.json")
                    implementation = report["steps"][0]
                    self.assertIs(implementation["verification"]["passed"], True)
                    self.assertEqual((Path(implementation["worktree"]) / "answer.txt").read_bytes(), b"1\n" if scenario == "guided_history" else b"42\n")
                    if mutation:
                        if application_mutation:
                            self.assertEqual(details["commands"][-1]["label"], "guided-apply")
                        else:
                            self.assertEqual(len(details["commands"]), 1, "oracle did not reject the mutated run before later operations")
                        self.assertNotIn("cleanup_error", record)

    def test_completed_only_noop_cli_cannot_pass_isolation_boundary(self):
        with tempfile.TemporaryDirectory(prefix="amx-soak-noop-probe-") as temporary:
            root = Path(temporary)
            binary = self.executable(root,
                "import json,sys\nfrom pathlib import Path\n"
                "output=Path(sys.argv[sys.argv.index('--output')+1]); output.mkdir()\n"
                "result={'state':'completed'}\n(output/'report.json').write_text(json.dumps(result))\n"
                "print(json.dumps({'ok':True,'result':result}))\n")
            output = root / "evidence"
            listener = mock.MagicMock()
            listener.getsockname.return_value = ("127.0.0.1", 12345)
            with mock.patch("socket.socket") as socket_factory:
                socket_factory.return_value.__enter__.return_value = listener
                result = SOAK.main(["--binary", str(binary), "--output", str(output), "--scenario", "isolation_boundary",
                                    "--iterations", "1", "--containment", "pid-namespace", "--isolation", "bubblewrap"])
            self.assertEqual(result, 1)
            record = json.loads((output / "soak.json").read_text())
            self.assertEqual(record["state"], "failed")
            self.assertEqual(record["iterations_completed"], 0)
            self.assertIn("exact fixture identity", record["error"])
            self.assertEqual(record["model_calls"], 0)

    def test_guided_inspection_rejects_empty_partial_wrong_and_missing_history_records(self):
        with tempfile.TemporaryDirectory(prefix="amx-soak-inspection-oracle-") as temporary:
            root = Path(temporary)
            runner = SOAK.Soak(self.arguments(root, self.executable(root, "raise SystemExit(1)\n")))
            runner.current = {"directory": str(root), "assertions": []}
            report = {"id": "fixture-id", "goal": "SOAK_VALUE:1", "state": "completed", "repository": "/source", "output": "/run",
                      "started_at": "2026-11-03T00:00:00Z", "finished_at": "2026-11-03T00:00:01Z", "patch": "/run/change.patch",
                      "steps": [{"id": "implement", "state": "verified", "harness": "command"}], "verification": {"checks": [{"passed": True}]}}
            summary = {key: report[key] for key in ("id", "goal", "state", "repository", "started_at", "finished_at")}
            summary.update(directory="/run", phases=report["steps"], checks_passed=1, checks_total=1, source_fresh=True,
                           result_fresh=True, can_apply=True, review_state="approve", patch_path="/run/change.patch", patch_preview="+42")
            runner.assert_guided_summary(summary, report)
            for row in ({}, {"state": "completed"}, dict(summary, id="wrong-fixture"), dict(summary, phases=[]),
                        dict(summary, checks_total=0), dict(summary, checks_total=True), dict(summary, checks_passed=True),
                        dict(summary, checks_total=1.0), dict(summary, source_fresh=False), dict(summary, result_fresh=False),
                        dict(summary, can_apply=False), dict(summary, review_state="request_changes"), dict(summary, patch_preview=""),
                        dict(summary, patch_preview=True), dict(summary, patch_preview=["+42"])):
                with self.subTest(summary=row), self.assertRaises(SOAK.SoakFailure):
                    runner.assert_guided_summary(row, report)
            runner.guided_reports = [report]
            expected = {"id": report["id"], "goal": report["goal"], "state": report["state"], "directory": report["output"],
                        "started_at": report["started_at"], "finished_at": report["finished_at"]}
            runner.assert_history([expected])
            for rows in (None, {}, [], [{"state": "completed"}], [dict(expected, id="wrong-run")], [dict(expected, state="running")], [expected, expected]):
                with self.subTest(history=rows), self.assertRaises(SOAK.SoakFailure):
                    runner.assert_history(rows)

    def test_complete_applied_source_rejects_unrelated_missing_modified_and_added_files(self):
        with tempfile.TemporaryDirectory(prefix="amx-soak-applied-source-oracle-") as temporary:
            root = Path(temporary)
            runner = SOAK.Soak(self.arguments(root, self.executable(root, "raise SystemExit(1)\n")))
            runner.current = {"directory": str(root), "assertions": []}
            repository = runner.project(root)
            before = runner.source(repository)
            worktree = root / "result"
            worktree.mkdir()
            (worktree / "answer.txt").write_bytes(b"42\n")
            (repository / "answer.txt").write_bytes(b"42\n")
            after = runner.source(repository)
            runner.assert_applied_source(before, after, worktree, ("answer.txt",))
            invalid = []
            for field, value in (("head", "other-head"), ("index_sha256", "other-index"), ("git_metadata_sha256", "other-metadata")):
                invalid.append(dict(after, **{field: value}))
            missing = copy.deepcopy(after)
            del missing["files"]["owner.bin"]
            invalid.append(missing)
            changed = copy.deepcopy(after)
            changed["files"]["owner.bin"]["sha256"] = "other-owner-bytes"
            invalid.append(changed)
            mode = copy.deepcopy(after)
            mode["files"]["owner.txt"]["mode"] ^= 0o100
            invalid.append(mode)
            extra = copy.deepcopy(after)
            extra["files"]["unexpected.txt"] = extra["files"]["answer.txt"]
            invalid.append(extra)
            for state in invalid:
                with self.subTest(source=state), self.assertRaises(SOAK.SoakFailure):
                    runner.assert_applied_source(before, state, worktree, ("answer.txt",))

    def test_frozen_binary_and_child_environment_exclude_ambient_auth_and_git(self):
        with tempfile.TemporaryDirectory(prefix="amx-soak-env-") as temporary:
            root = Path(temporary)
            binary = self.executable(root, "raise SystemExit(1)\n")
            args = self.arguments(root, binary)
            with mock.patch.dict(os.environ, {"OPENAI_API_KEY": "dummy-secret", "GIT_DIR": "/outside/git", "CLAUDE_CODE_OAUTH_TOKEN": "dummy-token"}):
                runner = SOAK.Soak(args)
            self.assertNotIn("OPENAI_API_KEY", runner.environment)
            self.assertNotIn("CLAUDE_CODE_OAUTH_TOKEN", runner.environment)
            self.assertNotIn("GIT_DIR", runner.environment)
            self.assertEqual(runner.environment["HOME"], str(args.output / "empty-home"))
            binary.write_text("changed after freeze")
            self.assertNotEqual(binary.read_bytes(), runner.binary.read_bytes())
            self.assertEqual((args.output.stat().st_mode & 0o777), 0o700)
            self.assertEqual(((args.output / "soak.json").stat().st_mode & 0o777), 0o600)
            record = SOAK.read_json(args.output / "soak.json")
            helper = args.output / "process_identity_helper-under-test.py"
            self.assertEqual(SOAK.hashlib.sha256(helper.read_bytes()).hexdigest(), record["harness_files_sha256"]["process_identity_helper"])

    def test_command_deadline_stops_observed_session_escaped_descendant(self):
        with tempfile.TemporaryDirectory(prefix="amx-soak-timeout-") as temporary:
            root = Path(temporary)
            binary = self.executable(root,
                "import subprocess,sys,time,threading\n"
                "child=\"from pathlib import Path; import os,sys,time; Path(sys.argv[1]).write_text(str(os.getpid())); time.sleep(120)\"\n"
                "threading.Thread(target=lambda:(subprocess.Popen([sys.executable,'-c',child,sys.argv[1]],start_new_session=True),time.sleep(120)),daemon=True).start()\n"
                "time.sleep(120)\n")
            runner = SOAK.Soak(self.arguments(root, binary))
            case = runner.root / "case"
            case.mkdir(mode=0o700)
            runner.current = {"directory": str(case), "commands": [], "assertions": [], "state": "running"}
            marker = case / "child-ready"
            item = runner.launch("timeout", [marker])
            try:
                runner.wait_ready(item, marker)
                child = SOAK.process_identity(int(marker.read_text()))
                self.assertIsNotNone(child)
                with self.assertRaisesRegex(SOAK.SoakFailure, "exceeded its deadline"):
                    runner.finish(item, timeout=0.1)
            finally:
                runner.cleanup()
            self.assertFalse(SOAK.identity_exists(child), "cleanup left a live process or zombie")
            self.assertIsNotNone(item["process"].returncode)
            self.assertEqual(item["row"]["state"], "stopped-during-cleanup")
            self.assertTrue(Path(item["row"]["stdout"]).is_file())

    def test_ordinary_success_cannot_hide_a_surviving_descendant(self):
        with tempfile.TemporaryDirectory(prefix="amx-soak-leak-") as temporary:
            root = Path(temporary)
            binary = self.executable(root, "raise SystemExit(0)\n")
            runner = SOAK.Soak(self.arguments(root, binary))
            case = runner.root / "case"
            case.mkdir(mode=0o700)
            runner.current = {"directory": str(case), "commands": [], "assertions": [], "state": "running"}
            child = subprocess.Popen([sys.executable, "-c", "import time; time.sleep(120)"], start_new_session=True)
            identity = SOAK.process_identity(child.pid)
            finished = subprocess.Popen([sys.executable, "-c", "import time; time.sleep(.05)"])
            tree = SOAK.PROCESS.ProcessTree(finished)
            child_handle = SOAK.PROCESS.PinnedProcess.open(child.pid, parent_pid=os.getpid())
            tree.handles[(child.pid, identity["start_ticks"])] = child_handle
            finished.wait(timeout=5)
            output = open(case / "stdout", "wb")
            error = open(case / "stderr", "wb")
            runner.active = [{"process": finished, "row": {"label": "success", "state": "finished"}, "stdout": output, "stderr": error,
                              "identities": {(identity["pid"], identity["start_ticks"]): identity}, "tree": tree}]
            try:
                with self.assertRaisesRegex(SOAK.SoakFailure, "unexpected surviving"):
                    runner.cleanup(expect_stopped=True)
                child.wait(timeout=5)
                self.assertFalse(SOAK.identity_exists(identity), "success cleanup left a process or zombie")
            finally:
                if child.poll() is None:
                    child.kill()
                    child.wait(timeout=5)

    def test_invalid_native_or_output_requests_run_nothing(self):
        with tempfile.TemporaryDirectory(prefix="amx-soak-preflight-") as temporary:
            root = Path(temporary)
            marker = root / "must-not-run"
            binary = self.executable(root, "from pathlib import Path; Path(" + repr(str(marker)) + ").write_text('ran')\n")
            existing = root / "existing"
            existing.mkdir()
            for extra, output in ((["--native"], root / "native"),
                                  (["--iterations", "0"], root / "bad-count"),
                                  (["--scenario", "check_failure", "--scenario", "check_failure"], root / "duplicate"),
                                  ([], existing)):
                with self.subTest(extra=extra, output=output.name):
                    result = subprocess.run([sys.executable, str(SCRIPT), "--binary", str(binary), "--output", str(output), *extra], capture_output=True, timeout=10)
                    self.assertEqual(result.returncode, 2)
                    self.assertFalse(marker.exists())
                    if output != existing:
                        self.assertFalse(output.exists())

    def test_unrelated_failure_cannot_pass_negative_control(self):
        with tempfile.TemporaryDirectory(prefix="amx-soak-negative-") as temporary:
            root = Path(temporary)
            binary = self.executable(root, "import json; print(json.dumps({'ok':False,'result':{}})); raise SystemExit(1)\n")
            output = root / "evidence"
            process = subprocess.run([sys.executable, str(SCRIPT), "--binary", str(binary), "--output", str(output),
                                      "--scenario", "check_failure", "--iterations", "1", "--containment", "process-group"],
                                     capture_output=True, text=True, timeout=20)
            record = json.loads((output / "soak.json").read_text())
            self.assertEqual(process.returncode, 1)
            self.assertEqual(record["state"], "failed")
            self.assertEqual(record["iterations_completed"], 0)
            self.assertEqual(record["model_calls"], 0)
            self.assertEqual(len(record["cases"]), 1)
            self.assertEqual(record["cases"][0]["state"], "failed")
            details = json.loads((Path(record["cases"][0]["directory"]) / "case.json").read_text())
            self.assertTrue(Path(details["commands"][0]["stderr"]).is_file())

    def test_sigterm_retains_interrupted_evidence_and_cleans_descendants(self):
        with tempfile.TemporaryDirectory(prefix="amx-soak-signal-") as temporary:
            root = Path(temporary)
            binary = self.executable(root,
                "import pathlib,subprocess,sys,time\n"
                "run=pathlib.Path(sys.argv[sys.argv.index('--output')+1]); run.mkdir(mode=0o700)\n"
                "child=\"from pathlib import Path; import os,sys,time; Path(sys.argv[1]).write_text(str(os.getpid())); time.sleep(120)\"\n"
                "subprocess.Popen([sys.executable,'-c',child,str(run/'child-ready')],start_new_session=True)\n"
                "time.sleep(120)\n")
            output = root / "evidence"
            process = subprocess.Popen([sys.executable, str(SCRIPT), "--binary", str(binary), "--output", str(output),
                                        "--scenario", "check_failure", "--iterations", "1", "--containment", "process-group"],
                                       stdout=subprocess.PIPE, stderr=subprocess.PIPE, start_new_session=True)
            child = None
            try:
                deadline = time.monotonic() + 10
                while time.monotonic() < deadline:
                    ready = list(output.glob("*/run/child-ready")) if output.exists() else []
                    if ready:
                        child = SOAK.process_identity(int(ready[0].read_text()))
                        break
                    self.assertIsNone(process.poll(), process.communicate() if process.poll() is not None else "")
                    time.sleep(0.02)
                self.assertIsNotNone(child, "fixture child did not start")
                process.send_signal(signal.SIGTERM)
                stdout, stderr = process.communicate(timeout=10)
                self.assertEqual(process.returncode, 1, (stdout, stderr))
                record = json.loads((output / "soak.json").read_text())
                self.assertEqual(record["state"], "interrupted")
                self.assertEqual(record["iterations_completed"], 0)
                self.assertNotIn("cleanup_error", record)
                self.assertFalse(SOAK.identity_exists(child), "signal cleanup left a process or zombie")
                self.assertGreaterEqual(record["adopted_fixture_processes_reaped"], 1)
            finally:
                if process.poll() is None:
                    process.kill()
                    process.communicate(timeout=5)
                if child and SOAK.same_process(child):
                    os.kill(child["pid"], signal.SIGKILL)

    def test_evidence_rejects_ambiguous_json_and_does_not_follow_symlinks(self):
        with tempfile.TemporaryDirectory(prefix="amx-soak-evidence-") as temporary:
            root = Path(temporary)
            record = root / "record.json"
            for payload in (
                '{"state":"failed","state":"passed"}',
                '{"state":"failed","STATE":"passed"}',
                '{"ok":true,"o\\u212a":false}',
                '{"result":{"checks":[{"passed":false,"PASSED":true}]}}',
            ):
                record.write_text(payload)
                with self.subTest(payload=payload), self.assertRaisesRegex(SOAK.SoakFailure, "duplicate JSON"):
                    SOAK.read_json(record)
            for literal in ("NaN", "Infinity", "-Infinity", "1e10000", "-1e10000"):
                record.write_text('{"result":{"usage":[' + literal + ']}}')
                with self.subTest(literal=literal), self.assertRaisesRegex(SOAK.SoakFailure, "nonfinite"):
                    SOAK.read_json(record)
            for payload in (b'{"state":"\xff"}', '{"state":"passed"}'.encode("utf-16")):
                record.write_bytes(payload)
                with self.subTest(payload=payload), self.assertRaises(UnicodeDecodeError):
                    SOAK.read_json(record)
            link = root / "link.json"
            link.symlink_to(record)
            with self.assertRaises(OSError):
                SOAK.read_json(link)

    def test_ambiguous_cli_evidence_cannot_pass_an_otherwise_valid_negative_control(self):
        # This transparent fixture reproduces the retained files and exact
        # negative-check outcome without running any agent or provider. The
        # unmodified control must pass, so malformed variants cannot succeed by
        # merely failing for an unrelated infrastructure or oracle reason.
        with tempfile.TemporaryDirectory(prefix="amx-soak-ambiguous-cli-") as temporary:
            for number, (mutation, reason) in enumerate((
                ("", None),
                ("envelope='{\"ok\":true,'+envelope[1:]", "duplicate JSON"),
                ("envelope='{\"OK\":true,'+envelope[1:]", "case-aliased"),
                ("envelope='{\"o\\\\u212a\":true,'+envelope[1:]", "case-aliased"),
                ("envelope='{\"ignored\":NaN,'+envelope[1:]", "nonfinite"),
                ("raw=raw.replace('\"state\": \"failed\"','\"STATE\":\"completed\",\"state\":\"failed\"',1); envelope='{\"ok\":false,\"result\":'+raw+'}'", "case-aliased"),
            )):
                with self.subTest(mutation=mutation):
                    root = Path(temporary) / str(number)
                    root.mkdir()
                    binary = self.executable(root,
                        "import json,sys\nfrom pathlib import Path\n"
                        "plan=json.loads(Path(sys.argv[3]).read_text())\n"
                        "output=Path(sys.argv[sys.argv.index('--output')+1]); output.mkdir()\n"
                        "worktree=output/'candidates/writer/worktree'; worktree.mkdir(parents=True)\n"
                        "(worktree/'answer.txt').write_bytes(b'42\\n'); (worktree/'turn-count.txt').write_bytes(b'1')\n"
                        "snapshot={'tree':'b'*40,'content_sha256':'c'*64,'ignore_sha256':'a'*64,'selection_sha256':'d'*64}\n"
                        "verification=dict(snapshot,passed=False,repository=str(worktree),ending_tree='b'*40,ending_content_sha256='c'*64,selection_sha256='e'*64,checks=[dict(plan['checks'][0],passed=False,exit_code=7)])\n"
                        "candidate={'id':'writer','harness':'command','attempts':1,'state':'checks_failed','worktree':str(worktree),'verification':verification}\n"
                        "result={'version':1,'id':'fixture-run','repository':plan['repository'],'output':str(output),'mode':'compare','state':'failed','goal':plan['goal'],'execution':'local','storage':plan['storage'],'process_containment':plan['process_containment'],'snapshot':snapshot,'candidates':[candidate]}\n"
                        "raw=json.dumps(result); envelope='{\"ok\":false,\"result\":'+raw+'}'\n"
                        + mutation + "\n"
                        "(output/'report.json').write_text(raw)\n"
                        "print(envelope); raise SystemExit(1)\n"
                    )
                    runner = SOAK.Soak(self.arguments(root, binary))
                    status = runner.run()
                    record = json.loads((runner.root / "soak.json").read_text())
                    self.assertEqual(status, 0 if reason is None else 1)
                    self.assertEqual(record["state"], "finalizing" if reason is None else "failed")
                    self.assertIs(record["completed"], False)
                    self.assertEqual(record["iterations_completed"], 1 if reason is None else 0)
                    self.assertEqual(len(record["cases"]), 1)
                    self.assertEqual(record["cases"][0]["state"], "passed" if reason is None else "failed")
                    self.assertNotIn("cleanup_error", record)
                    if reason is not None:
                        self.assertIn(reason, record["error"])

    def test_killing_the_soak_controller_stops_its_admitted_cli(self):
        SOAK.enable_subreaper()
        with tempfile.TemporaryDirectory(prefix="amx-soak-owner-kill-") as temporary:
            root = Path(temporary)
            binary = self.executable(root,
                "import os,pathlib,sys,time\n"
                "run=pathlib.Path(sys.argv[sys.argv.index('--output')+1]); run.mkdir(mode=0o700)\n"
                "(run/'owner-ready').write_text(str(os.getpid()))\n"
                "time.sleep(120)\n")
            output = root / "evidence"
            process = subprocess.Popen([sys.executable, str(SCRIPT), "--binary", str(binary), "--output", str(output),
                                        "--scenario", "check_failure", "--iterations", "1", "--containment", "process-group"],
                                       stdout=subprocess.PIPE, stderr=subprocess.PIPE, start_new_session=True)
            identity = None
            try:
                until = time.monotonic() + 10
                while time.monotonic() < until:
                    ready = list(output.glob("*/run/owner-ready")) if output.exists() else []
                    if ready:
                        identity = SOAK.process_identity(int(ready[0].read_text()))
                        break
                    self.assertIsNone(process.poll())
                    time.sleep(0.02)
                self.assertIsNotNone(identity, "admitted CLI did not start")
                process.kill()
                process.communicate(timeout=5)
                until = time.monotonic() + 3
                while SOAK.same_process(identity) and time.monotonic() < until:
                    time.sleep(0.02)
                self.assertFalse(SOAK.same_process(identity), "admitted CLI survived its soak owner SIGKILL")
                # Reap the intentionally orphaned CLI if this test adopted it.
                try:
                    os.waitpid(identity["pid"], os.WNOHANG)
                except ChildProcessError:
                    pass
                record = json.loads((output / "soak.json").read_text())
                self.assertEqual(record["state"], "running")
                self.assertIs(record["completed"], False)
                self.assertEqual(record["iterations_completed"], 0)
                self.assertEqual(record["controller_parent_death_signal"], "SIGKILL")
            finally:
                if process.poll() is None:
                    process.kill()
                    process.communicate(timeout=5)
                if identity and SOAK.same_process(identity):
                    os.kill(identity["pid"], signal.SIGKILL)


if __name__ == "__main__":
    unittest.main()
