#!/usr/bin/env python3
"""Regression checks for validation evidence; never invokes provider CLIs."""
import copy
import importlib.util
import json
import os
import signal
from pathlib import Path
import subprocess
import sys
import tempfile
import time
import unittest
from unittest import mock


SCRIPT = Path(__file__).with_name("validate-workflows.py")
SPEC = importlib.util.spec_from_file_location("workflow_validator", SCRIPT)
VALIDATOR = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(VALIDATOR)


def comparison_fixture_source(mutation=""):
    """A provider-free protocol fixture with real retained implementation files."""
    return (
        "import hashlib,json,sys\nfrom pathlib import Path\n"
        "if sys.argv[2]=='run':\n"
        "    plan=json.loads(Path(sys.argv[3]).read_text())\n"
        "    directory=Path(sys.argv[sys.argv.index('--output')+1]);directory.mkdir()\n"
        "    (directory/'git').mkdir()\n"
        "    outcomes=[]\n"
        "    for profile in plan['candidates']:\n"
        "        worktree=directory/profile['id'];worktree.mkdir();(worktree/'.git').mkdir()\n"
        "        (worktree/'tag_normalizer.py').write_text(" + repr(VALIDATOR.IMPLEMENTATION) + ")\n"
        "        patch=directory/(profile['id']+'.patch');patch.write_text('diff --git a/tag_normalizer.py b/tag_normalizer.py\\n')\n"
        "        verification={'passed':True,'repository':str(worktree),'storage':plan['storage'],'process_containment':plan['process_containment'],'store':str(worktree/'.git'),'ignore_sha256':'a'*64,'selection_sha256':'e'*64,'tree':'b'*40,'ending_tree':'b'*40,'content_sha256':'c'*64,'ending_content_sha256':'c'*64,'checks':[dict(check,passed=True,exit_code=0,output='External normalization assertions passed\\n') for check in plan['checks']]}\n"
        "        outcomes.append(dict(profile,state='verified',attempts=1,worktree=str(worktree),patch=str(patch),patch_sha256=hashlib.sha256(patch.read_bytes()).hexdigest(),verification=verification,final_snapshot={'tree':'b'*40,'content_sha256':'c'*64,'ignore_sha256':'a'*64,'selection_sha256':'e'*64}))\n"
        "    result={'version':1,'id':'fixture-run','mode':'compare','execution':'local','state':'completed','repository':plan['repository'],'output':str(directory),'storage':plan['storage'],'process_containment':plan['process_containment'],'snapshot':{'tree':'b'*40,'content_sha256':'c'*64,'store':str(directory/'git'),'ignore_sha256':'a'*64,'selection_sha256':'d'*64},'candidates':outcomes}\n"
        + ("    " + mutation.replace("\n", "\n    ") + "\n" if mutation else "")
        + "    (directory/'report.json').write_text(json.dumps(result))\n"
        "    print(json.dumps({'ok':True,'result':result}));sys.exit(0)\n"
    )


def completed_comparison_fixture_source(mutation=""):
    """Complete the comparison, inspections and exports without any provider."""
    return comparison_fixture_source(mutation) + (
        "directory=Path(sys.argv[3]); report=json.loads((directory/'report.json').read_text())\n"
        "operation=sys.argv[2]\n"
        "if operation=='status': result=report\n"
        "elif operation=='explain': result={'id':report['id'],'state':'completed','items':[{'id':p['id'],'state':'verified','patch_intact':True,'fresh':True} for p in report['candidates']]}\n"
        "elif operation in ('report','handoff'):\n"
        "    destination=Path(sys.argv[sys.argv.index('--output')+1])\n"
        "    destination.write_text('<html>'+str(report['id'])+' tag_normalizer.py</html>' if operation=='report' else str(report['id'])+' Candidate: '+report['candidates'][0]['id']+' passed=true')\n"
        "    result={'report':str(destination)} if operation=='report' else {'brief':str(destination),'delivered':False}\n"
        "else: raise RuntimeError('Unexpected fixture operation')\n"
        "print(json.dumps({'ok':True,'result':result}))\n"
    )


class ValidationEvidenceTests(unittest.TestCase):
    def test_complete_comparison_refuses_malformed_and_unbound_revision_pins(self):
        cases = [
            ("", True),
            ("result['snapshot']['tree']='b'*64\nfor item in result['candidates']:\n item['verification'].update(tree='b'*64,ending_tree='b'*64);item['final_snapshot']['tree']='b'*64", True),
            ("result['snapshot'].update(tree=True,content_sha256=True,ignore_sha256=True,selection_sha256=True)", False),
            ("for item in result['candidates']:\n item['verification'].update(tree=True,ending_tree=1,content_sha256=True,ending_content_sha256=1.0);item['final_snapshot'].update(tree=1.0,content_sha256=1)", False),
            ("result['snapshot']['content_sha256']=''", False),
            ("result['snapshot']['tree']='g'*40", False),
            ("result['snapshot']['content_sha256']='C'*64", False),
            ("result['snapshot'].pop('selection_sha256')", False),
            ("for item in result['candidates']:\n item['verification']['selection_sha256']=True;item['final_snapshot']['selection_sha256']=1.0", False),
            ("result['candidates'][0]['final_snapshot']['selection_sha256']='f'*64", False),
            ("for item in result['candidates']:\n item['verification']['ignore_sha256']='f'*64;item['final_snapshot']['ignore_sha256']='f'*64", False),
            ("result['id']=True", False),
            ("result['error']=False", False),
            ("result['error']=0", False),
            ("result['candidates'][0]['error']=False", False),
            ("result['candidates'][0]['verification']['checks'][0]['error']=0", False),
            ("result['error']=None\nfor item in result['candidates']:\n item['error']='';item['verification']['checks'][0]['error']=None", True),
        ]
        with tempfile.TemporaryDirectory(prefix="amx-validator-complete-digest-oracle-") as temporary:
            root = Path(temporary)
            for number, (mutation, passing) in enumerate(cases):
                with self.subTest(mutation=mutation):
                    fixture = root / ("fixture-" + str(number))
                    fixture.write_text("#!" + sys.executable + "\n" + completed_comparison_fixture_source(mutation))
                    fixture.chmod(0o700)
                    output = root / ("evidence-" + str(number))
                    process = subprocess.run([sys.executable, str(SCRIPT), "--binary", str(fixture),
                                              "--output", str(output), "--native-comparison"], capture_output=True, text=True, timeout=30)
                    record = json.loads((output / "validation.json").read_text())
                    self.assertEqual(process.returncode, 0 if passing else 1, process.stdout + process.stderr)
                    self.assertIs(record["passed"], passing)
                    self.assertTrue(record["completed"])
                    self.assertIs(record["cases"][0]["passed"], passing)
                    if passing:
                        self.assertEqual(len(record["cases"]), 7)
                    else:
                        self.assertEqual(record["cases"][0]["error"], "ValueError")

    def test_policy_requires_exact_integer_types_in_declared_isolation_structure(self):
        plan = {"storage": "private", "process_containment": "pid-namespace",
                "isolation": {"mode": "bubblewrap", "network": "none", "bridge_max_requests": 1}}
        valid = {"storage": "private", "process_containment": "pid-namespace",
                 "isolation": dict(plan["isolation"], home_bytes=64 << 20, temp_bytes=64 << 20)}
        oracle = VALIDATOR.WorkflowOracle(Path("/tmp"), plan, {})
        oracle.policy(valid, plan)
        for field, value in (("home_bytes", float(64 << 20)), ("temp_bytes", float(64 << 20)),
                             ("bridge_max_requests", True), ("bridge_max_requests", 1.0)):
            altered = copy.deepcopy(valid)
            altered["isolation"][field] = value
            with self.subTest(field=field, value=value), self.assertRaisesRegex(ValueError, "isolation policy"):
                oracle.policy(altered, plan)

    def test_guided_preview_requires_exact_boolean_step_roles(self):
        base = {"repository": "/source", "storage": "private", "process_containment": "process-group",
                "checks": [{"name": "contract", "command": [sys.executable, "-c", "print('ok')"]}]}
        plan = dict(base, mode="mission", kind="work", max_attempts=1, max_parallel=1,
                    candidates=[{"id": "coder", "harness": "codex", "model": "gpt-6.1-sol"},
                                {"id": "reviewer", "harness": "antigravity", "model": "gemini-3.8-flash-low"}],
                    checks=[dict(base["checks"][0], name="project-check", timeout_seconds=60)],
                    steps=[{"id": "implement", "candidate": "coder", "verify": True},
                           {"id": "review", "candidate": "reviewer", "depends_on": ["implement"], "review": True, "read_only": True},
                           {"id": "address-review", "candidate": "coder", "depends_on": ["implement", "review"], "verify": True},
                           {"id": "final-review", "candidate": "reviewer", "depends_on": ["implement", "review", "address-review"],
                            "review": True, "read_only": True, "gate_review": True},
                           {"id": "final-check", "depends_on": ["final-review"], "verify": True}])
        oracle = VALIDATOR.WorkflowOracle(Path("/tmp"), base, {})
        oracle.preview({"plan": plan, "max_native_turns": 4})
        explicit = copy.deepcopy(plan)
        for step in explicit["steps"]:
            for flag in ("verify", "read_only", "review", "gate_review"):
                step.setdefault(flag, False)
        oracle.preview({"plan": explicit, "max_native_turns": 4})
        for number, flag, value in ((0, "read_only", 0), (2, "review", 0), (4, "read_only", ""),
                                    (0, "gate_review", 0), (3, "gate_review", 1), (1, "review", 1.0),
                                    (2, "read_only", None), (0, "read_only", True), (3, "review", False),
                                    (0, "verify", 1), (1, "verify", 0)):
            altered = copy.deepcopy(plan)
            altered["steps"][number][flag] = value
            with self.subTest(step=number, flag=flag, value=value), self.assertRaisesRegex(ValueError, "verification or review gates"):
                oracle.preview({"plan": altered, "max_native_turns": 4})

    def test_diagnostic_case_evidence_uses_one_completion_authority_across_finalization(self):
        with tempfile.TemporaryDirectory(prefix="amx-validator-diagnostic-transaction-") as temporary:
            root = Path(temporary)
            fixtures = root / "fixtures"
            fixtures.mkdir()
            provider = fixtures / "agy"
            provider.write_text("#!" + sys.executable + "\nimport json,sys\nfrom pathlib import Path\n"
                                "json.load(sys.stdin)\nPath('greeting.txt').write_bytes(b'AG_OK\\n')\n"
                                "print(json.dumps({'event':'result','result':{'status':'SUCCESS','response':'Fixture completed.'}}))\n")
            provider.chmod(0o700)
            probe = root / "probe.py"
            probe.write_text("""import builtins,importlib.util,os,signal,sys
from pathlib import Path
spec=importlib.util.spec_from_file_location('diagnostic_validator',sys.argv[1])
validator=importlib.util.module_from_spec(spec);spec.loader.exec_module(validator)
stage,output=sys.argv[2:4]
fired=False
def fault(selected):
    global fired
    if fired or stage!=selected:return
    fired=True
    if stage=='hard-crash-after-case-record':os.kill(os.getpid(),signal.SIGKILL)
    if stage=='diagnostic-print':raise BrokenPipeError('diagnostic evidence output failed')
    raise OSError('diagnostic finalization failed: '+selected)
printing=builtins.print
def emit(*args,**kwargs):
    if args and str(args[0]).endswith('/diagnostic.json'):fault('diagnostic-print')
    return printing(*args,**kwargs)
builtins.print=emit
cleanup=validator.ValidationProcesses.cleanup
def stop(self):
    result=cleanup(self);fault('cleanup');return result
validator.ValidationProcesses.cleanup=stop
persist=validator.persist_manifest
def retain(path,value):
    result=persist(path,value)
    if path.name=='diagnostic.json' and value.get('cases_completed') is True:
        fault('diagnostic-case-write');fault('hard-crash-after-case-record')
    if path.name=='validation.json' and value.get('completed') is True:fault('canonical-success-write')
    return result
validator.persist_manifest=retain
sys.argv=[sys.argv[1],'--binary',sys.executable,'--output',output,'--diagnose-antigravity']
raise SystemExit(validator.main())
""")
            for stage in ("success", "diagnostic-print", "cleanup", "diagnostic-case-write", "canonical-success-write", "hard-crash-after-case-record"):
                with self.subTest(stage=stage):
                    output = root / stage
                    process = subprocess.run([sys.executable, str(probe), str(SCRIPT.resolve()), stage, str(output)],
                                             env=dict(os.environ, PATH=str(fixtures) + os.pathsep + os.environ.get("PATH", "")),
                                             capture_output=True, text=True, timeout=15)
                    canonical = json.loads((output / "validation.json").read_text())
                    diagnostic = json.loads((output / "diagnostic.json").read_text())
                    self.assertEqual(diagnostic["evidence_scope"], "diagnostic-case-results")
                    self.assertEqual(Path(diagnostic["completion_record"]), output / "validation.json")
                    self.assertNotIn("passed", diagnostic)
                    self.assertNotIn("completed", diagnostic)
                    self.assertTrue(diagnostic["cases_passed"])
                    self.assertTrue(diagnostic["cases_completed"])
                    self.assertEqual(canonical["passed"], stage == "success")
                    self.assertEqual(canonical["completed"], stage == "success")
                    if stage == "hard-crash-after-case-record":
                        self.assertEqual(process.returncode, -signal.SIGKILL)
                        self.assertFalse(json.loads(Path(diagnostic["completion_record"]).read_text())["completed"])
                    else:
                        self.assertEqual(process.returncode == 0, stage == "success", process.stdout + process.stderr)
                        if stage != "success":
                            self.assertIn("BrokenPipeError" if stage == "diagnostic-print" else "OSError", canonical["error"])

    def test_secondary_diagnostic_write_failure_cannot_skip_canonical_revocation(self):
        with tempfile.TemporaryDirectory(prefix="amx-validator-diagnostic-compensation-") as temporary:
            output = Path(temporary)
            summary = {"completed": True, "passed": True, "cases": [], "diagnostic": {"cases": []}}
            VALIDATOR.persist_validation(output, summary)
            summary.update(completed=False, passed=False)
            persist = VALIDATOR.persist_manifest
            def retain(path, value):
                if path.name == "diagnostic.json":
                    raise OSError("secondary evidence filesystem failed")
                return persist(path, value)
            with mock.patch.object(VALIDATOR, "persist_manifest", side_effect=retain):
                with self.assertRaises(OSError):
                    VALIDATOR.persist_validation(output, summary)
            self.assertFalse(json.loads((output / "validation.json").read_text())["passed"])

    def test_reaped_process_without_admission_handles_never_signals_a_group(self):
        process = mock.Mock(pid=os.getpid(), returncode=0)
        del process._validation_tree
        with mock.patch.object(VALIDATOR.os, "kill") as numeric_kill, \
                mock.patch.object(VALIDATOR.os, "killpg") as group_kill, \
                mock.patch.object(VALIDATOR.PROCESS, "ProcessTree") as admit:
            VALIDATOR.stop_process_group(process)
            numeric_kill.assert_not_called()
            group_kill.assert_not_called()
            admit.assert_not_called()

    def test_pidfd_cleanup_stops_admitted_session_escaped_descendant(self):
        with tempfile.TemporaryDirectory(prefix="amx-validator-pidfd-cleanup-") as temporary:
            root = Path(temporary)
            ready = root / "ready"
            code = ("import subprocess,sys,time; from pathlib import Path; "
                    "child=subprocess.Popen([sys.executable,'-c','import signal,time; signal.signal(signal.SIGTERM,signal.SIG_IGN); time.sleep(60)'],"
                    "start_new_session=True,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL); "
                    "Path(sys.argv[1]).write_text(str(child.pid)); time.sleep(60)")
            processes = VALIDATOR.ValidationProcesses()
            process = processes.launch([sys.executable, "-c", code, str(ready)],
                                       stdout=subprocess.PIPE, stderr=subprocess.PIPE, start_new_session=True)
            try:
                until = time.monotonic() + 5
                while not ready.exists():
                    self.assertLess(time.monotonic(), until)
                    time.sleep(.01)
                process._validation_tree.observe()
                child = VALIDATOR.PROCESS.PinnedProcess.open(int(ready.read_text()), parent_pid=process.pid)
                self.assertIsNotNone(child)
                try:
                    with mock.patch.object(os, "kill") as numeric_kill, mock.patch.object(os, "killpg") as group_kill:
                        with self.assertRaises(subprocess.TimeoutExpired):
                            processes.communicate(process, timeout=.05)
                        self.assertFalse(child.live(), "pidfd cleanup left an admitted escaped descendant running")
                        numeric_kill.assert_not_called()
                        group_kill.assert_not_called()
                finally:
                    if child is not None:
                        child.send_signal(signal.SIGKILL)
                        child.close()
            finally:
                processes.cleanup()

    def test_missing_pidfd_support_refuses_admission_before_any_process(self):
        with tempfile.TemporaryDirectory(prefix="amx-validator-no-pidfds-") as temporary:
            output = Path(temporary) / "evidence"
            with mock.patch.object(VALIDATOR.PROCESS, "require_process_support", side_effect=RuntimeError("pidfd support unavailable")), \
                    mock.patch.object(VALIDATOR.subprocess, "Popen") as launch, \
                    mock.patch.object(sys, "argv", [str(SCRIPT), "--binary", sys.executable, "--output", str(output)]):
                with self.assertRaises(SystemExit) as stopped:
                    VALIDATOR.main()
                self.assertEqual(stopped.exception.code, 2)
                launch.assert_not_called()
                self.assertFalse(output.exists())

    def test_denied_pidfd_signaling_refuses_admission_before_any_process(self):
        with tempfile.TemporaryDirectory(prefix="amx-validator-denied-pidfd-signal-") as temporary:
            output = Path(temporary) / "evidence"
            close = os.close
            with mock.patch.object(VALIDATOR.PROCESS.signal, "pidfd_send_signal", side_effect=PermissionError("signaling denied")) as send, \
                    mock.patch.object(VALIDATOR.PROCESS.os, "close", wraps=close) as closed, \
                    mock.patch.object(VALIDATOR.subprocess, "Popen") as launch, \
                    mock.patch.object(sys, "argv", [str(SCRIPT), "--binary", sys.executable, "--output", str(output)]):
                with self.assertRaises(SystemExit) as stopped:
                    VALIDATOR.main()
                self.assertEqual(stopped.exception.code, 2)
                send.assert_called_once()
                self.assertEqual(send.call_args.args[1], 0)
                closed.assert_called_once_with(send.call_args.args[0])
                launch.assert_not_called()
                self.assertFalse(output.exists())

    def test_failed_process_pin_retains_nonpassing_admission_without_executing_target(self):
        with tempfile.TemporaryDirectory(prefix="amx-validator-pin-admission-") as temporary:
            output = Path(temporary)
            marker = output / "executed"
            summary = {"cases": [{"name": "admitted-fixture", "passed": False}]}
            with mock.patch.object(VALIDATOR.PROCESS, "ProcessTree", side_effect=RuntimeError("pin admission failed")), \
                    mock.patch.object(os, "kill") as numeric_kill, mock.patch.object(os, "killpg") as group_kill:
                with self.assertRaisesRegex(RuntimeError, "pin admission failed"):
                    with VALIDATOR.validation_evidence(output, summary) as processes:
                        processes.launch([sys.executable, "-c", "from pathlib import Path; Path(" + repr(str(marker)) + ").touch()"],
                                         stdout=subprocess.PIPE, stderr=subprocess.PIPE, start_new_session=True)
                self.assertEqual(processes.active, [])
                self.assertFalse(marker.exists())
                record = json.loads((output / "validation.json").read_text())
                self.assertFalse(record["passed"])
                self.assertFalse(record["completed"])
                self.assertEqual(record["error"], "RuntimeError")
                numeric_kill.assert_not_called()
                group_kill.assert_not_called()

    def test_observation_failure_cannot_skip_pinned_controller_termination(self):
        processes = VALIDATOR.ValidationProcesses()
        process = processes.launch([sys.executable, "-c", "import time; time.sleep(60)"],
                                   stdout=subprocess.PIPE, stderr=subprocess.PIPE, start_new_session=True)
        with mock.patch.object(process._validation_tree, "observe", side_effect=OSError("process observation failed")):
            with self.assertRaisesRegex(RuntimeError, "process observation failed"):
                processes.cleanup()
        self.assertIsNotNone(process.returncode)
        processes.cleanup()

    def test_provider_free_subprocess_cannot_inherit_owner_login_roots_or_secrets(self):
        with tempfile.TemporaryDirectory(prefix="agentmux-validator-environment-") as temporary:
            root = Path(temporary)
            owner = root / "owner-home"
            owner.mkdir()
            (owner / "login-marker").write_text("owner login must not be discovered")
            fixture = root / "fixture-cli"
            fixture.write_text(
                "#!" + sys.executable + "\n"
                "import json,os\nfrom pathlib import Path\n"
                "observation={'environment':dict(os.environ),'home_entries':sorted(p.name for p in Path.home().iterdir()),'cwd':os.getcwd()}\n"
                "print(json.dumps({'ok':True,'result':{},'observation':observation}))\n"
            )
            fixture.chmod(0o700)
            output = root / "validation"
            environment = dict(os.environ, HOME=str(owner), CODEX_HOME=str(owner / ".codex"),
                               XDG_CONFIG_HOME=str(owner / "config"), XDG_RUNTIME_DIR=str(owner / "runtime"),
                               OPENAI_API_KEY="dummy-openai-secret", CLAUDE_CODE_OAUTH_TOKEN="dummy-claude-secret",
                               UNRELATED_OWNER_SECRET="dummy-unrelated-secret", PYTHONSTARTUP=str(owner / "startup.py"),
                               AGENTMUX_SANDBOX_INTEGRATION="1")
            process = subprocess.run(
                [sys.executable, str(SCRIPT), "--binary", str(fixture), "--output", str(output)],
                env=environment, capture_output=True, text=True, timeout=30,
            )
            # The transparent fixture cannot satisfy the real negative control.
            self.assertEqual(process.returncode, 1, process.stdout + process.stderr)
            record = json.loads((output / "validation.json").read_text())
            self.assertEqual(record["environment_profile"], "private-fixtures")
            helper = output / "process_identity_helper-under-test.py"
            self.assertEqual(VALIDATOR.hashlib.sha256(helper.read_bytes()).hexdigest(), record["harness_files_sha256"]["process_identity_helper"])
            observation = json.loads((output / "comparison.stdout").read_text())["observation"]
            observed = observation["environment"]
            self.assertEqual(observed["HOME"], str(output / "empty-home"))
            self.assertEqual(observed["CODEX_HOME"], str(output / "empty-home/.codex"))
            self.assertEqual(observation["home_entries"], [])
            self.assertEqual(observation["cwd"], str(output))
            for name in ("OPENAI_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN", "UNRELATED_OWNER_SECRET",
                         "XDG_RUNTIME_DIR", "PYTHONSTARTUP", "AGENTMUX_SANDBOX_INTEGRATION"):
                self.assertNotIn(name, observed)
            self.assertFalse(any(str(owner) in value for value in observed.values()))

    def test_explicit_native_login_environment_is_still_allowlisted(self):
        with tempfile.TemporaryDirectory(prefix="agentmux-validator-native-environment-") as temporary:
            root = Path(temporary)
            with mock.patch.dict(os.environ, {"HOME": "/selected/native-home", "CODEX_HOME": "/selected/codex-login",
                                            "OPENAI_API_KEY": "dummy-secret", "CLAUDE_CODE_OAUTH_TOKEN": "dummy-token",
                                            "UNRELATED_OWNER_SECRET": "dummy-unrelated", "GIT_DIR": "/owner/git"}):
                environment = VALIDATOR.validation_environment(root, live_login=True)
            self.assertEqual(environment["HOME"], "/selected/native-home")
            self.assertEqual(environment["CODEX_HOME"], "/selected/codex-login")
            for name in ("OPENAI_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN", "UNRELATED_OWNER_SECRET", "GIT_DIR"):
                self.assertNotIn(name, environment)

    def test_full_isolation_refuses_shared_storage_or_implicit_live_profiles(self):
        with tempfile.TemporaryDirectory(prefix="agentmux-validator-full-policy-") as temporary:
            for number, arguments in enumerate((
                ["--storage", "shared"], ["--native"],
                ["--native-comparison"], ["--diagnose-antigravity"],
            )):
                with self.subTest(arguments=arguments):
                    output = Path(temporary) / str(number)
                    process = subprocess.run(
                        [sys.executable, str(SCRIPT), "--binary", sys.executable,
                         "--output", str(output), "--isolation", "bubblewrap", *arguments],
                        capture_output=True, text=True, timeout=10,
                    )
                    self.assertEqual(process.returncode, 2)
                    self.assertIn("full", process.stderr)
                    self.assertFalse(output.exists())

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
                    self.assertEqual(process.returncode, 1, process.stdout + process.stderr)
                    record = json.loads((root / ("validation-" + str(number)) / "validation.json").read_text())
                    self.assertFalse(record["passed"])
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
            "tree": "b" * 40,
            "ending_tree": "b" * 40,
            "content_sha256": "c" * 64,
            "ending_content_sha256": "c" * 64,
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

    def test_malformed_cli_shapes_fail_admitted_scenario_after_passing_comparison(self):
        malformed = [
            [], None, "not-an-envelope", {}, {"ok": 1, "result": {}},
            {"ok": True}, {"ok": True, "result": None}, {"ok": True, "result": []},
            *({"ok": True, "result": {"candidates": candidates}}
              for candidates in (None, {}, "invalid", [None], [[]], ["invalid"], [{}, None])),
            *({"ok": True, "result": {"candidates": [{"verification": verification}]}}
              for verification in (None, [], "invalid")),
        ]
        with tempfile.TemporaryDirectory(prefix="agentmux-validator-shapes-") as temporary:
            root = Path(temporary)
            for number, payload in enumerate(malformed):
                with self.subTest(payload=payload):
                    fixture = root / ("fixture-" + str(number))
                    fixture.write_text(
                        "#!" + sys.executable + "\n" + comparison_fixture_source() +
                        "payload=" + repr(payload) + "\n"
                        "print(json.dumps(payload if sys.argv[2]=='status' else {'ok':True,'result':{}}))\n"
                    )
                    fixture.chmod(0o700)
                    output = root / ("evidence-" + str(number))
                    process = subprocess.run(
                        [sys.executable, str(SCRIPT), "--binary", str(fixture),
                         "--output", str(output), "--native-comparison"],
                        capture_output=True, text=True, timeout=30,
                    )
                    record = json.loads((output / "validation.json").read_text())
                    self.assertEqual(process.returncode, 1, process.stdout + process.stderr)
                    self.assertTrue(record["completed"])
                    self.assertFalse(record["passed"])
                    self.assertTrue(record["cases"][0]["passed"])
                    status = next(case for case in record["cases"] if case["name"] == "status")
                    self.assertFalse(status["passed"])
                    self.assertEqual(status["error"], "ValueError")
                    self.assertEqual(record["cases"][-1]["name"], "handoff")
                    self.assertNotIn("Traceback", process.stderr)

    def test_list_result_protocol_is_validated_without_rejecting_work_history(self):
        history = [{"id": "retained-run"}]
        self.assertEqual(VALIDATOR.workflow_result({"ok": True, "result": history}, list_result=True), history)
        for payload in (None, {}, [None], ["invalid"]):
            with self.subTest(payload=payload), self.assertRaises(ValueError):
                VALIDATOR.workflow_result({"ok": True, "result": payload}, list_result=True)

    def test_strict_json_rejects_ambiguous_fields_and_nonfinite_numbers_at_every_depth(self):
        valid = {"ok": True, "result": {"checks": [{"passed": True}, {"passed": False}],
                                        "usage": {"ratio": 0.125, "count": 3}}}
        self.assertEqual(VALIDATOR.strict_json(json.dumps(valid).encode()), valid)
        malformed = [
            '{"ok":false,"ok":true,"result":{}}',
            '{"ok":true,"OK":false,"result":{}}',
            '{"ok":true,"o\\u212a":false,"result":{}}',
            '{"result":{"state":"failed","STATE":"completed"}}',
            '{"result":{"checks":[{"passed":false,"passed":true}]}}',
            *('{"result":{"usage":[' + number + ']}}'
              for number in ("NaN", "Infinity", "-Infinity", "1e10000", "-1e10000")),
            b'{"result":"\xff"}',
            '{"ok":true}'.encode("utf-16"),
        ]
        for payload in malformed:
            with self.subTest(payload=payload), self.assertRaises(ValueError):
                VALIDATOR.strict_json(payload)
        with tempfile.TemporaryDirectory(prefix="agentmux-validator-retained-json-") as temporary:
            path = Path(temporary) / "report.json"
            oracle = VALIDATOR.WorkflowOracle(Path(temporary), {}, {})
            for payload in malformed:
                path.write_bytes(payload if isinstance(payload, bytes) else payload.encode())
                with self.subTest(retained=payload), self.assertRaises(ValueError):
                    oracle.retained(path)

    def test_adversarial_cli_envelopes_and_retained_records_cannot_pass_successful_prefix(self):
        # This safe CLI fixture creates real retained implementation files for
        # comparison, then corrupts only a later status envelope or report. It
        # cannot invoke a provider, even though the bounded comparison mode is
        # selected to keep this regression focused on the consumed evidence.
        mutations = (
            ("envelope-duplicate", "envelope", '{"ok":false,', "duplicate"),
            ("envelope-case-alias", "envelope", '{"OK":false,', "case-aliased"),
            ("envelope-unicode-alias", "envelope", '{"o\\u212a":false,', "case-aliased"),
            ("envelope-nonfinite", "envelope", '{"ignored":NaN,', "nonfinite"),
            ("retained-duplicate", "retained", '{"state":"failed",', "duplicate"),
            ("retained-case-alias", "retained", '{"STATE":"failed",', "case-aliased"),
            ("retained-nested-duplicate", "nested", None, "duplicate"),
        )
        with tempfile.TemporaryDirectory(prefix="agentmux-validator-ambiguous-") as temporary:
            root = Path(temporary)
            for number, (name, target, prefix, reason) in enumerate(mutations):
                with self.subTest(name=name):
                    fixture = root / ("fixture-" + str(number))
                    fixture.write_text(
                        "#!" + sys.executable + "\n" + comparison_fixture_source() +
                        "if sys.argv[2]=='status':\n"
                        "    path=Path(sys.argv[3])/'report.json'\n"
                        "    result=json.loads(path.read_text())\n"
                        "    envelope=json.dumps({'ok':True,'result':result})\n"
                        + ("    envelope=" + repr(prefix) + "+envelope[1:]\n" if target == "envelope" else
                           "    path.write_text(" + repr(prefix) + "+json.dumps(result)[1:])\n" if target == "retained" else
                           "    path.write_text(json.dumps(result).replace('\"passed\": true','\"passed\": false, \"passed\": true',1))\n")
                        + "    print(envelope);sys.exit(0)\n"
                        "print(json.dumps({'ok':True,'result':{}}))\n"
                    )
                    fixture.chmod(0o700)
                    output = root / ("evidence-" + str(number))
                    process = subprocess.run(
                        [sys.executable, str(SCRIPT), "--binary", str(fixture),
                         "--output", str(output), "--native-comparison"],
                        capture_output=True, text=True, timeout=30,
                    )
                    record = json.loads((output / "validation.json").read_text())
                    self.assertEqual(process.returncode, 1, process.stdout + process.stderr)
                    self.assertTrue(record["completed"])
                    self.assertFalse(record["passed"])
                    self.assertTrue(record["cases"][0]["passed"])
                    status = next(case for case in record["cases"] if case["name"] == "status")
                    self.assertFalse(status["passed"])
                    self.assertEqual(status["error"], "ValueError")
                    self.assertIn(reason, status["reason"])
                    self.assertNotIn("Traceback", process.stderr)

    def test_unexpected_exception_and_keyboard_interrupt_cannot_pass_successful_prefix(self):
        with tempfile.TemporaryDirectory(prefix="agentmux-validator-exceptions-") as temporary:
            for number, error in enumerate((RuntimeError("fixture failure"), KeyboardInterrupt(), SystemExit(2))):
                with self.subTest(error=type(error).__name__):
                    output = Path(temporary) / str(number)
                    output.mkdir()
                    summary = {"cases": []}
                    with self.assertRaises(type(error)):
                        with VALIDATOR.validation_evidence(output, summary):
                            initial = json.loads((output / "validation.json").read_text())
                            self.assertFalse(initial["completed"])
                            self.assertFalse(initial["passed"])
                            summary["cases"].append({"name": "successful-prefix", "passed": True})
                            raise error
                    record = json.loads((output / "validation.json").read_text())
                    self.assertFalse(record["passed"])
                    self.assertFalse(record["completed"])
                    self.assertEqual(record["error"], type(error).__name__)
                    self.assertEqual(record["cases"][-1], {"name": "validator-completion", "passed": False,
                                                           "error": type(error).__name__})

    @unittest.skipUnless(os.name == "posix", "process-group cleanup requires POSIX")
    def test_interrupt_after_successful_cases_fails_evidence_and_cleans_subprocess_group(self):
        self.check_interrupted_validator(signal.SIGINT)

    @unittest.skipUnless(os.name == "posix", "process-group cleanup requires POSIX")
    def test_sigterm_after_successful_cases_fails_evidence_and_cleans_subprocess_group(self):
        self.check_interrupted_validator(signal.SIGTERM)

    @unittest.skipUnless(os.name == "posix", "hard-kill evidence requires POSIX")
    def test_hard_kill_retains_incomplete_nonpassing_admitted_scenario(self):
        self.check_interrupted_validator(signal.SIGKILL)

    def check_interrupted_validator(self, interruption):
        with tempfile.TemporaryDirectory(prefix="agentmux-validator-interrupted-") as temporary:
            root = Path(temporary)
            fixture = root / "fixture-cli"
            fixture.write_text(
                "#!" + sys.executable + "\n"
                "import json,os,subprocess,sys,time\nfrom pathlib import Path\n" + comparison_fixture_source() +
                "if sys.argv[2]=='status':\n"
                "    child=subprocess.Popen([sys.executable,'-c','import time; time.sleep(60)'])\n"
                "    Path('interrupt-ready.json').write_text(json.dumps({'group':os.getpid(),'child':child.pid}))\n"
                "    time.sleep(60)\n"
                "print(json.dumps({'ok':True,'result':{}}))\n"
            )
            fixture.chmod(0o700)
            output = root / "evidence"
            process = subprocess.Popen(
                [sys.executable, str(SCRIPT), "--binary", str(fixture),
                 "--output", str(output), "--native-comparison"],
                stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True,
            )
            marker = output / "interrupt-ready.json"
            children = None
            cleanup_handles = []
            try:
                deadline = time.monotonic() + 10
                while not marker.exists() and process.poll() is None and time.monotonic() < deadline:
                    time.sleep(0.02)
                self.assertTrue(marker.exists(), "validator did not reach status after comparison")
                children = json.loads(marker.read_text())
                for name, pid in children.items():
                    parent = process.pid if name == "group" else children["group"]
                    handle = VALIDATOR.PROCESS.PinnedProcess.open(pid, parent_pid=parent)
                    if handle is not None:
                        cleanup_handles.append(handle)
                incomplete = json.loads((output / "validation.json").read_text())
                self.assertFalse(incomplete["completed"])
                self.assertFalse(incomplete["passed"])
                self.assertTrue(incomplete["cases"][0]["passed"])
                self.assertEqual(incomplete["cases"][-1], {"name": "status", "passed": False})
                process.send_signal(interruption)
                stdout, stderr = process.communicate(timeout=15)
                self.assertNotEqual(process.returncode, 0, stdout + stderr)
                record = json.loads((output / "validation.json").read_text())
                self.assertFalse(record["passed"])
                self.assertFalse(record["completed"])
                status = next(case for case in record["cases"] if case["name"] == "status")
                self.assertFalse(status["passed"])
                if interruption in (signal.SIGINT, signal.SIGTERM):
                    error = "KeyboardInterrupt" if interruption == signal.SIGINT else "ValidationInterrupted"
                    self.assertEqual(status["error"], error)
                    self.assertEqual(record["error"], error)
                    # A dead orphan may briefly remain a zombie until its new
                    # parent reaps it; neither it nor the fixture may keep running.
                    for pid in children.values():
                        information = Path("/proc") / str(pid) / "stat"
                        if information.exists():
                            self.assertEqual(information.read_text().rsplit(")", 1)[1].split()[0], "Z")
                else:
                    self.assertNotIn("finished_at", record)
            finally:
                if process.poll() is None:
                    process.kill()
                process.communicate(timeout=5)
                if children is None and marker.exists():
                    children = json.loads(marker.read_text())
                for handle in cleanup_handles:
                    handle.send_signal(signal.SIGKILL)
                    handle.close()

    def finalization_probe(self, root):
        probe = root / "finalization-probe.py"
        probe.write_text("""import builtins, importlib.util, json, os, signal, sys, time
from pathlib import Path
spec=importlib.util.spec_from_file_location('finalization_validator',sys.argv[1])
validator=importlib.util.module_from_spec(spec);spec.loader.exec_module(validator)
stage,outcome,output=sys.argv[2:5]
fired=False
class FinalizationFault(BaseException): pass
def fault(selected):
    global fired
    if fired or stage!=selected:return
    fired=True
    if outcome in ('sigint','sigterm'):
        os.kill(os.getpid(),signal.SIGINT if outcome=='sigint' else signal.SIGTERM)
    elif outcome=='keyboardinterrupt':raise KeyboardInterrupt('publication fault')
    elif outcome=='oserror':raise OSError('publication fault')
    else:raise FinalizationFault('publication fault')
def run(args,binary,output,summary,processes):
    summary['cases'].append({'name':'successful-fixture','passed':True})
    return 0
validator.run_validation=run
cleanup=validator.ValidationProcesses.cleanup
def stop(self):
    result=cleanup(self);fault('cleanup');return result
validator.ValidationProcesses.cleanup=stop
replace=validator.os.replace
def publish(source,destination):
    result=replace(source,destination)
    if Path(destination).name=='validation.json' and json.loads(Path(destination).read_text()).get('passed') is True:
        fault('success-replace')
    return result
validator.os.replace=publish
printing=builtins.print
def emit(*args,**kwargs):
    global fired
    evidence=args and str(args[0]).startswith('Evidence:')
    if evidence and stage=='closed-pipe' and not fired:
        fired=True
        Path(output+'.ready').touch()
        until=time.monotonic()+5
        while not Path(output+'.release').exists():
            if time.monotonic()>=until:raise RuntimeError('closed-pipe readiness expired')
            time.sleep(.01)
    result=printing(*args,**kwargs)
    if evidence:fault('evidence-print')
    return result
builtins.print=emit
set_handler=signal.signal
handler_calls=0
def select_handler(*args,**kwargs):
    global handler_calls
    result=set_handler(*args,**kwargs)
    handler_calls+=1
    if handler_calls==3:fault('handler-restore-first')
    if handler_calls==4:fault('handler-restore-last')
    return result
signal.signal=select_handler
sys.argv=[sys.argv[1],'--binary',sys.executable,'--output',output]
raise SystemExit(validator.main())
""")
        return probe

    @unittest.skipUnless(os.name == "posix", "real lifecycle signals require POSIX")
    def test_finalization_signals_and_post_replace_exceptions_revoke_success(self):
        with tempfile.TemporaryDirectory(prefix="agentmux-validator-finalization-") as temporary:
            root = Path(temporary)
            probe = self.finalization_probe(root)
            for stage in ("cleanup", "evidence-print", "success-replace", "handler-restore-first", "handler-restore-last"):
                for outcome in ("sigint", "sigterm", "keyboardinterrupt", "oserror", "baseexception"):
                    with self.subTest(stage=stage, outcome=outcome):
                        output = root / (stage + "-" + outcome)
                        process = subprocess.run([sys.executable, str(probe), str(SCRIPT), stage, outcome, str(output)],
                                                 capture_output=True, text=True, timeout=10)
                        self.assertNotEqual(process.returncode, 0, process.stdout + process.stderr)
                        record = json.loads((output / "validation.json").read_text())
                        self.assertIs(record["passed"], False)
                        self.assertIs(record["completed"], False)
                        expected = {"sigint": "KeyboardInterrupt", "sigterm": "ValidationInterrupted",
                                    "keyboardinterrupt": "KeyboardInterrupt", "oserror": "OSError",
                                    "baseexception": "FinalizationFault"}[outcome]
                        self.assertEqual(record["error"], expected)
                        self.assertEqual(record["cases"][-1], {"name": "validator-finalization", "passed": False, "error": expected})

    def test_closed_stdout_at_final_evidence_output_retains_failure(self):
        with tempfile.TemporaryDirectory(prefix="agentmux-validator-closed-pipe-") as temporary:
            root = Path(temporary)
            output = root / "evidence"
            probe = self.finalization_probe(root)
            process = subprocess.Popen([sys.executable, str(probe), str(SCRIPT), "closed-pipe", "oserror", str(output)],
                                       stdout=subprocess.PIPE, stderr=subprocess.PIPE)
            try:
                until = time.monotonic() + 10
                while not Path(str(output) + ".ready").exists():
                    self.assertIsNone(process.poll(), "probe stopped before final stdout readiness")
                    self.assertLess(time.monotonic(), until, "probe never reached final stdout")
                    time.sleep(.01)
                process.stdout.close()
                process.stdout = None
                Path(str(output) + ".release").touch()
                _, stderr = process.communicate(timeout=10)
                self.assertNotEqual(process.returncode, 0, stderr)
                record = json.loads((output / "validation.json").read_text())
                self.assertIs(record["passed"], False)
                self.assertIs(record["completed"], False)
                self.assertEqual(record["error"], "BrokenPipeError")
                self.assertIn("BrokenPipeError", record["output_error"])
            finally:
                if process.poll() is None:
                    process.kill()
                    process.communicate(timeout=5)

    @unittest.skipUnless(os.name == "posix", "process-group cleanup requires POSIX")
    def test_sigterm_during_popen_admission_keeps_process_reachable_for_cleanup(self):
        with tempfile.TemporaryDirectory(prefix="agentmux-validator-launch-signal-") as temporary:
            root = Path(temporary)
            output = root / "evidence"
            output.mkdir()
            summary = {"cases": []}
            admitted = []
            popen = VALIDATOR.subprocess.Popen

            def interrupted_launch(*args, **kwargs):
                process = popen(*args, **kwargs)
                admitted.append(process)
                os.kill(os.getpid(), signal.SIGTERM)
                return process

            with mock.patch.object(VALIDATOR.subprocess, "Popen", side_effect=interrupted_launch):
                with self.assertRaises(VALIDATOR.ValidationInterrupted):
                    with VALIDATOR.validation_evidence(output, summary) as processes:
                        processes.launch([sys.executable, "-c", "import time;time.sleep(60)"],
                                         stdout=subprocess.PIPE, stderr=subprocess.PIPE, start_new_session=True)
            self.assertEqual(len(admitted), 1)
            self.assertIsNotNone(admitted[0].returncode)
            record = json.loads((output / "validation.json").read_text())
            self.assertFalse(record["passed"])
            self.assertFalse(record["completed"])
            self.assertEqual(record["error"], "ValidationInterrupted")

    def test_failure_compensation_retains_cleanup_output_and_persistence_errors(self):
        with tempfile.TemporaryDirectory(prefix="agentmux-validator-failure-compensation-") as temporary:
            output = Path(temporary)
            summary = {"cases": []}
            persist = VALIDATOR.persist_validation
            attempts = 0

            def retain(path, value):
                nonlocal attempts
                attempts += 1
                if attempts == 2:
                    raise OSError("first failure evidence write failed")
                return persist(path, value)

            with mock.patch.object(VALIDATOR.ValidationProcesses, "cleanup", side_effect=RuntimeError("cleanup fault")), \
                    mock.patch("builtins.print", side_effect=BrokenPipeError("output fault")), \
                    mock.patch.object(VALIDATOR, "persist_validation", side_effect=retain):
                with self.assertRaisesRegex(RuntimeError, "primary failure"):
                    with VALIDATOR.validation_evidence(output, summary):
                        raise RuntimeError("primary failure")
            record = json.loads((output / "validation.json").read_text())
            self.assertFalse(record["passed"])
            self.assertFalse(record["completed"])
            self.assertEqual(attempts, 3)
            for field, text in (("error_detail", "primary failure"), ("cleanup_error", "cleanup fault"),
                                ("output_error", "output fault"), ("evidence_error", "evidence write failed")):
                self.assertIn(text, record[field])

    def test_noop_cli_never_passes_and_native_turn_bounds_match_the_selected_mode(self):
        with tempfile.TemporaryDirectory(prefix="agentmux-validator-noop-") as temporary:
            root = Path(temporary); fixture = root / "noop-cli"
            fixture.write_text("#!" + sys.executable + "\nimport json\nprint(json.dumps({'ok':True,'result':{}}))\n")
            fixture.chmod(0o700)
            for number, (arguments, native, turns) in enumerate((
                (["--native-comparison"], True, 2), (["--guided", "--native"], True, 4), (["--guided"], False, 0),
            )):
                with self.subTest(arguments=arguments):
                    output = root / str(number)
                    process = subprocess.run([sys.executable, str(SCRIPT), "--binary", str(fixture),
                                              "--output", str(output), *arguments], capture_output=True, text=True, timeout=30)
                    record = json.loads((output / "validation.json").read_text())
                    self.assertEqual(process.returncode, 1, process.stdout + process.stderr)
                    self.assertFalse(record["passed"])
                    self.assertEqual(record["native"], native)
                    self.assertEqual(record["provider_turn_limit"], turns)
                    self.assertFalse(record["cases"][0]["passed"])

    def test_empty_success_and_partial_semantic_comparison_evidence_cannot_pass(self):
        mutations = [
            "result={}", "result['version']=True", "result['version']=1.0", "result.pop('candidates')", "result['candidates'].reverse()",
            "result['candidates'].pop()", "result['state']='uncertain'",
            "result['candidates'][0]['state']='failed'", "result['candidates'][0]['attempts']=0",
            "result['candidates'][0]['attempts']=True", "result['candidates'][0]['attempts']=1.0",
            "result['candidates'][0]['harness']='other'", "result['candidates'][0].pop('model')",
            "result['candidates'][0]['verification']['passed']=False",
            "result['candidates'][0]['verification']['checks']=[]",
            "result['candidates'][0]['verification']['checks'][0]['command']=['true']",
            "result['candidates'][0]['verification']['checks'][0]['exit_code']=1",
            "result['candidates'][0]['verification']['checks'][0]['exit_code']=False",
            "result['candidates'][0]['verification']['checks'][0]['exit_code']=0.0",
            "result['candidates'][0]['verification']['checks'][0]['output']='no assertions'",
            "result['candidates'][0]['final_snapshot']['tree']='different'",
            "Path(result['candidates'][0]['patch']).unlink()",
            "Path(result['candidates'][0]['worktree'],'tag_normalizer.py').write_text('def normalize_tags(tags): return []')",
            "result['candidates'][0]['verification'].pop('store')",
            "result['snapshot']['store']=str(Path(plan['repository'])/'.git')",
            "result.pop('process_containment')", "result['isolation']={'mode':'none'}",
        ]
        with tempfile.TemporaryDirectory(prefix="agentmux-validator-semantic-cases-") as temporary:
            root = Path(temporary)
            for number, mutation in enumerate(mutations):
                with self.subTest(mutation=mutation):
                    fixture = root / ("fixture-" + str(number))
                    fixture.write_text("#!" + sys.executable + "\n" + comparison_fixture_source(mutation) +
                                       "print(json.dumps({'ok':True,'result':{}}))\n")
                    fixture.chmod(0o700)
                    output = root / ("evidence-" + str(number))
                    process = subprocess.run(
                        [sys.executable, str(SCRIPT), "--binary", str(fixture),
                         "--output", str(output), "--native-comparison"],
                        capture_output=True, text=True, timeout=30,
                    )
                    record = json.loads((output / "validation.json").read_text())
                    self.assertEqual(process.returncode, 1, process.stdout + process.stderr)
                    self.assertFalse(record["passed"])
                    self.assertTrue(record["completed"])
                    self.assertEqual(record["cases"][0]["name"], "comparison")
                    self.assertFalse(record["cases"][0]["passed"])
                    self.assertEqual(record["cases"][0]["error"], "ValueError")

    def test_mission_step_coverage_and_final_approval_cannot_be_omitted(self):
        with tempfile.TemporaryDirectory(prefix="agentmux-validator-mission-cases-") as temporary:
            root = Path(temporary)
            directory = root / "run"
            worktree = root / "worktree"; worktree.mkdir(); (worktree / ".git").mkdir()
            directory.mkdir(); (directory / "git").mkdir()
            (worktree / "tag_normalizer.py").write_text(VALIDATOR.IMPLEMENTATION)
            patch = directory / "changes.patch"; patch.write_text("diff --git a/tag_normalizer.py b/tag_normalizer.py\n")
            check = {"name": "contract", "command": [sys.executable, "-B", "-c", VALIDATOR.CONTRACT]}
            plan = {"mode": "mission", "repository": str(root / "project"), "storage": "private",
                    "process_containment": "process-group", "checks": [check], "max_attempts": 1,
                    "candidates": [{"id": "coder", "harness": "command"}, {"id": "reviewer", "harness": "command"}],
                    "steps": [{"id": "implement", "candidate": "coder", "verify": True},
                              {"id": "review", "candidate": "reviewer", "read_only": True, "review": True},
                              {"id": "address-review", "candidate": "coder", "verify": True},
                              {"id": "final-review", "candidate": "reviewer", "read_only": True, "review": True, "gate_review": True},
                              {"id": "final-check", "verify": True}]}
            verification = {"passed": True, "storage": "private", "store": str(worktree / ".git"),
                            "process_containment": "process-group", "repository": str(worktree), "ignore_sha256": "a" * 64, "selection_sha256": "e" * 64,
                            "tree": "b" * 40, "ending_tree": "b" * 40, "content_sha256": "c" * 64, "ending_content_sha256": "c" * 64,
                            "checks": [dict(check, passed=True, exit_code=0, output="External normalization assertions passed\n")]}
            report = {"version": 1, "id": "fixture", "mode": "mission", "execution": "local", "state": "completed",
                      "repository": plan["repository"], "output": str(directory), "storage": "private", "process_containment": "process-group",
                      "snapshot": {"tree": "b" * 40, "content_sha256": "c" * 64, "store": str(directory / "git"), "ignore_sha256": "a" * 64, "selection_sha256": "d" * 64},
                      "worktree": str(worktree), "verification": verification,
                      "patch": str(patch), "patch_sha256": VALIDATOR.hashlib.sha256(patch.read_bytes()).hexdigest(),
                      "final_snapshot": {"tree": "b" * 40, "content_sha256": "c" * 64, "ignore_sha256": "a" * 64, "selection_sha256": "e" * 64},
                      "review_context": "fresh", "steps": []}
            revision = report["final_snapshot"]
            current = report["snapshot"]
            for step in plan["steps"]:
                outcome = {"id": step["id"], "harness": "command" if step.get("candidate") else "",
                           "state": "verified" if step.get("verify") else "completed",
                           "attempts": 1 if step.get("candidate") else 0}
                if step.get("candidate"):
                    outcome.update(starting_snapshot=current, final_snapshot=revision)
                    current = revision
                if step.get("verify"):
                    outcome["verification"] = verification
                if step.get("review"):
                    outcome.update(read_only=True, review_context="fresh", starting_snapshot=revision,
                                   review={"decision": "approve", "summary": "Reviewed actual files", "findings": []})
                if step.get("gate_review"):
                    outcome["gate_review"] = True
                report["steps"].append(outcome)
            oracle = VALIDATOR.WorkflowOracle(root, plan, dict(os.environ))
            (directory / "report.json").write_text(json.dumps(report))
            oracle.report(report, plan, directory)
            for number, flag, value in ((0, "read_only", 0), (2, "review", 0), (4, "read_only", ""),
                                        (0, "gate_review", 0), (0, "verify", 1)):
                altered_plan = copy.deepcopy(plan)
                altered_plan["steps"][number][flag] = value
                with self.subTest(planned_flag=flag, value=value), self.assertRaisesRegex(ValueError, "planned mission roles"):
                    oracle.report(report, altered_plan, directory)
            # General missions can include an unchecked writing turn and a
            # read-only observer before a verifier-only terminal check.
            general_plan = dict(plan, steps=[dict(plan["steps"][0]),
                {"id": "unchecked-writer", "candidate": "coder"},
                {"id": "observer", "candidate": "reviewer", "read_only": True},
                {"id": "acceptance", "verify": True}])
            general = copy.deepcopy(report)
            general["steps"] = [copy.deepcopy(report["steps"][i]) for i in (0, 2, 1, 4)]
            for item, step in zip(general["steps"], general_plan["steps"]):
                item["id"] = step["id"]
                item["state"] = "verified" if step.get("verify") else "completed"
                if not step.get("verify"):
                    item.pop("verification", None)
                if not step.get("review"):
                    item.pop("review", None)
            (directory / "report.json").write_text(json.dumps(general))
            oracle.report(general, general_plan, directory)
            source = Path(plan["repository"])
            source.mkdir()
            (source / "tag_normalizer.py").write_text(VALIDATOR.IMPLEMENTATION)
            oracle.guided = report
            receipt = directory / "application.json"
            applied = {"applied": True, "phase": "finished", "receipt": str(receipt), "repository": str(source),
                       "run": report["id"], "candidate": "mission-result", "patch_sha256": report["patch_sha256"]}
            receipt.write_text(json.dumps(applied))
            oracle.validate("work-apply", applied, (), "work")
            for uncertain in (0, "", None):
                altered = dict(applied, uncertain=uncertain)
                receipt.write_text(json.dumps(altered))
                with self.subTest(uncertain=uncertain), self.assertRaisesRegex(ValueError, "application is incomplete"):
                    oracle.validate("work-apply", altered, (), "work")
            mutations = [
                lambda value: value["steps"].pop(),
                lambda value: value["steps"].reverse(),
                lambda value: value["steps"][3].update(state="failed"),
                lambda value: value["steps"][3].pop("read_only"),
                lambda value: value["steps"][3].pop("gate_review"),
                lambda value: value["steps"][3]["review"].update(decision="request_changes"),
                lambda value: value["steps"][3]["review"].update(findings=[{"file": "tag_normalizer.py", "line": 1, "message": "defect"}]),
                lambda value: value["steps"][-1]["verification"].update(checks=[]),
                lambda value: value.pop("review_context"),
                lambda value: value["steps"][1].update(gate_review=True),
                lambda value: value["steps"][1].update(gate_review=1),
                lambda value: value["steps"][0].update(starting_snapshot={"tree": True}),
                lambda value: value["steps"][2].update(starting_snapshot=dict(revision, tree="d" * 40)),
                lambda value: value["steps"][-1].update(starting_snapshot=True),
                lambda value: value["steps"][-1].update(final_snapshot={"tree": False}),
            ]
            def contradictory_implemented_review(value):
                fake = dict(value["steps"][0]["final_snapshot"], tree="d" * 40,
                            content_sha256="f" * 64, selection_sha256="0" * 64)
                value["steps"][0]["final_snapshot"] = fake
                value["steps"][1]["starting_snapshot"] = dict(fake)
                value["steps"][1]["final_snapshot"] = dict(fake)
            def contradictory_terminal_check(value):
                value["steps"][-1]["verification"] = dict(value["steps"][-1]["verification"],
                    tree="d" * 40, ending_tree="d" * 40, content_sha256="f" * 64,
                    ending_content_sha256="f" * 64, selection_sha256="0" * 64)
            def contradictory_last_writer(value):
                value["steps"][2]["final_snapshot"] = dict(value["steps"][2]["final_snapshot"],
                    tree="d" * 40, content_sha256="f" * 64, selection_sha256="0" * 64)
                value["steps"][2]["verification"] = dict(value["steps"][2]["verification"],
                    tree="d" * 40, ending_tree="d" * 40, content_sha256="f" * 64,
                    ending_content_sha256="f" * 64, selection_sha256="0" * 64)
            mutations.extend([contradictory_implemented_review, contradictory_terminal_check, contradictory_last_writer])
            for index in range(len(report["steps"])):
                for attempts in (0 if index != 4 else 1, 999, None, True, False, "1"):
                    mutations.append(lambda value, index=index, attempts=attempts: value["steps"][index].update(attempts=attempts))
                mutations.append(lambda value, index=index: value["steps"][index].pop("attempts"))
            for index in (1, 3):
                mutations.extend([
                    lambda value, index=index: value["steps"][index].pop("review_context"),
                    lambda value, index=index: value["steps"][index].update(review_context="reused"),
                    lambda value, index=index: value["steps"][index].pop("starting_snapshot"),
                    lambda value, index=index: value["steps"][index].pop("final_snapshot"),
                    lambda value, index=index: value["steps"][index].update(starting_snapshot={"tree": "wrong", "content_sha256": "wrong"}),
                    lambda value, index=index: value["steps"][index].update(final_snapshot={"tree": "wrong", "content_sha256": "wrong"}),
                    lambda value, index=index: value["steps"][index].update(review={"decision": "approve"}),
                    lambda value, index=index: value["steps"][index]["review"].pop("summary"),
                    lambda value, index=index: value["steps"][index]["review"].pop("findings"),
                ])
                for summary in (None, [], "", "   "):
                    mutations.append(lambda value, index=index, summary=summary: value["steps"][index]["review"].update(summary=summary))
                for findings in (None, {}, "", [None], ["invalid"], [{"file": "tag_normalizer.py", "line": 1, "message": "defect"}]):
                    mutations.append(lambda value, index=index, findings=findings: value["steps"][index]["review"].update(findings=findings))
            for number, mutate in enumerate(mutations):
                with self.subTest(mutation=number):
                    altered = copy.deepcopy(report); mutate(altered)
                    (directory / "report.json").write_text(json.dumps(altered))
                    with self.assertRaises(ValueError):
                        oracle.report(altered, plan, directory)

    def test_structured_review_requires_complete_valid_finding_fields(self):
        oracle = VALIDATOR.WorkflowOracle(Path("/tmp"), {}, {})
        verdict = {"decision": "request_changes", "summary": "Fix the reported defect", "findings": [
            {"file": "tag_normalizer.py", "line": 1, "message": "Handle the missing contract case"}]}
        oracle.structured_review(verdict)
        malformed = [None, {}, {"file": "tag_normalizer.py"},
                     {"file": "tag_normalizer.py", "line": True, "message": "defect"},
                     {"file": "tag_normalizer.py", "line": 0, "message": "defect"},
                     {"file": "tag_normalizer.py", "line": "1", "message": "defect"},
                     {"file": "tag_normalizer.py", "line": 1, "message": None},
                     {"file": "tag_normalizer.py", "line": 1, "message": "  "}]
        for filename in (None, [], "", "/outside", "../outside", ".git/config", "a/../../outside", "bad\\path", "bad\npath"):
            malformed.append({"file": filename, "line": 1, "message": "defect"})
        for finding in malformed:
            with self.subTest(finding=finding), self.assertRaises(ValueError):
                oracle.structured_review(dict(verdict, findings=[finding]))
        with self.assertRaises(ValueError):
            oracle.structured_review(dict(verdict, findings=[]))

    def test_aggregate_validation_requires_every_watch_and_rotated_lab_run(self):
        with tempfile.TemporaryDirectory(prefix="agentmux-validator-aggregate-cases-") as temporary:
            root = Path(temporary)
            plan = {"repository": str(root / "project"), "storage": "private", "process_containment": "process-group",
                    "candidates": [{"id": "first"}, {"id": "second"}]}
            for name in ("watch", "lab"):
                directory = root / name; directory.mkdir()
                runs = []
                for number in range(1 if name == "watch" else 2):
                    trial = directory / str(number); trial.mkdir(); (trial / "report.json").write_text("{}")
                    order = plan["candidates"][number:] + plan["candidates"][:number]
                    runs.append({"state": "completed", "output": str(trial), "repetition": number + 1,
                                 "order": [item["id"] for item in order], "superseded": False})
                result = {"state": "completed", "output": str(directory), "repository": plan["repository"],
                          "storage": "private", "process_containment": "process-group", "runs": runs}
                if name == "lab":
                    result.update(repetitions=2, snapshot={"repository": plan["repository"]},
                                  candidates=[{"id": item["id"], "runs": 2, "verified_runs": 2, "failed_runs": 0}
                                              for item in plan["candidates"]])
                oracle = VALIDATOR.WorkflowOracle(root, plan, {})
                oracle.plans["plan.json"] = plan
                argv = (name, "plan.json", "--output", directory)
                (directory / (name + ".json")).write_text(json.dumps(result))
                with mock.patch.object(oracle, "report") as reports:
                    oracle.validate(name, result, argv, "flow")
                    self.assertEqual(reports.call_count, len(runs))
                mutations = [lambda value: value.update(runs=[]),
                             lambda value: value["runs"].append(copy.deepcopy(value["runs"][0])),
                             lambda value: value["runs"][0].update(state="failed"),
                             lambda value: value["runs"][0].update(superseded=True),
                             lambda value: value["runs"][0].update(superseded=0),
                             lambda value: value["runs"][0].update(superseded=None),
                             lambda value: value["runs"][0].update(error=False)]
                if name == "lab":
                    mutations.extend([lambda value: value.update(repetitions=1),
                                      lambda value: value.update(repetitions=2.0),
                                      lambda value: value["runs"].pop(),
                                      lambda value: value["runs"][1]["order"].reverse(),
                                      lambda value: value["runs"][0].update(repetition=True),
                                      lambda value: value["candidates"][0].update(verified_runs=1),
                                      lambda value: value["candidates"][0].update(failed_runs=1),
                                      lambda value: value["candidates"][0].update(failed_runs=False),
                                      lambda value: value["candidates"][0].update(runs=2.0),
                                      lambda value: value["candidates"].pop()])
                for number, mutate in enumerate(mutations):
                    with self.subTest(operation=name, mutation=number):
                        altered = copy.deepcopy(result); mutate(altered)
                        (directory / (name + ".json")).write_text(json.dumps(altered))
                        with mock.patch.object(oracle, "report"), self.assertRaises(ValueError):
                            oracle.validate(name, altered, argv, "flow")

    def test_diagnostic_native_admissions_are_truthful_for_success_failure_and_interrupt(self):
        with tempfile.TemporaryDirectory(prefix="agentmux-validator-diagnostic-fixtures-") as temporary:
            root = Path(temporary); fixtures = root / "fixtures"; fixtures.mkdir()
            fixture = fixtures / "agy"
            for number, outcome in enumerate(("success", "failure", "interrupt")):
                with self.subTest(outcome=outcome):
                    fixture.write_text(
                        "#!" + sys.executable + "\nimport json,os,signal,sys,time\nfrom pathlib import Path\n"
                        "Path('greeting.txt').write_text('AG_OK\\n')\n"
                        + ("os.kill(os.getppid(),signal.SIGINT);time.sleep(60)\n" if outcome == "interrupt" else "")
                        + "print(json.dumps({'event':'result','result':{'status':'SUCCESS','response':'Created greeting.txt.'}}))\n"
                        + ("sys.exit(1)\n" if outcome == "failure" else "")
                    )
                    fixture.chmod(0o700)
                    output = root / ("evidence-" + str(number))
                    process = subprocess.run(
                        [sys.executable, str(SCRIPT), "--binary", sys.executable,
                         "--output", str(output), "--diagnose-antigravity"],
                        env=dict(os.environ, PATH=str(fixtures) + os.pathsep + os.environ.get("PATH", "")),
                        capture_output=True, text=True, timeout=30,
                    )
                    record = json.loads((output / "validation.json").read_text())
                    self.assertTrue(record["native"])
                    self.assertEqual(record["provider_turn_limit"], 2)
                    self.assertEqual(record["environment_profile"], "explicit-native-login")
                    self.assertEqual(record["passed"], outcome == "success")
                    self.assertEqual(process.returncode == 0, outcome == "success")
                    if outcome == "interrupt":
                        self.assertFalse(record["completed"])
                        self.assertFalse(record["cases"][0]["passed"])
                        self.assertEqual(record["error"], "KeyboardInterrupt")
                    else:
                        self.assertTrue(record["completed"])
                        self.assertEqual(len(record["cases"]), 2)

    def test_diagnostic_terminal_protocol_requires_unambiguous_success_without_errors(self):
        success = {"event": "result", "result": {"status": "SUCCESS", "response": "Created greeting.txt."}}
        step = {"event": "step_update", "step_update": {"step_type": "agent_response", "text_delta": "Earlier draft"}}
        fixtures = [
            ("success", [success], "", True),
            ("empty-answer", [{"event": "result", "result": {"status": "SUCCESS", "response": "", "denied_actions": []}}], "", True),
            ("retained-informational-frames", [
                {"event": "init", "conversation_id": "fixture-session", "metadata": {"fixture": True}}, step,
                {"event": "result", "result": {"status": "SUCCESS", "response": "Created greeting.txt.", "conversation_id": "fixture-session", "usage": {"input_tokens": 0}}},
                {"event": "informational", "conversation_id": "fixture-session", "detail": "teardown"},
            ], "", True),
            ("failed-terminal", [{"event": "result", "result": {"status": "ERROR", "error": "intentional native failure"}}], "", False),
            ("waiting-terminal", [{"event": "result", "result": {"status": "WAITING", "response": "partial"}}], "", False),
            ("cancelled-terminal", [{"event": "result", "result": {"status": "CANCELLED", "response": "partial"}}], "", False),
            ("empty-stream", [], "", False),
            ("incomplete-stream", [step], "", False),
            ("missing-response", [step, {"event": "result", "result": {"status": "SUCCESS"}}], "", False),
            ("null-response", [{"event": "result", "result": {"status": "SUCCESS", "response": None}}], "", False),
            ("object-response", [{"event": "result", "result": {"status": "SUCCESS", "response": {}}}], "", False),
            ("missing-status", [{"event": "result", "result": {"response": "Created greeting.txt."}}], "", False),
            ("null-result", [{"event": "result", "result": None}], "", False),
            ("nonstring-status", [{"event": "result", "result": {"status": True, "response": "Created greeting.txt."}}], "", False),
            ("missing-discriminator", [{}, success], "", False),
            ("duplicate-terminal", [success, success], "", False),
            ("failure-then-success", [{"event": "result", "result": {"status": "ERROR"}}, success], "", False),
            ("success-then-failure", [success, {"event": "result", "result": {"status": "ERROR"}}], "", False),
            ("late-answer", [success, step], "", False),
            ("changed-session", [{"event": "init", "conversation_id": "first"}, dict(success, conversation_id="second")], "", False),
            ("contradictory-result-session", [{"event": "result", "conversation_id": "first", "result": dict(success["result"], conversation_id="second")}], "", False),
            ("error-event", [{"event": "error", "message": "native failure"}, success], "", False),
            ("top-level-error", [dict(success, error="native failure")], "", False),
            ("terminal-error", [{"event": "result", "result": dict(success["result"], error={"message": "native failure"})}], "", False),
            ("terminal-errors", [{"event": "result", "result": dict(success["result"], errors=["native failure"])}], "", False),
            ("terminal-error-flag", [{"event": "result", "result": dict(success["result"], is_error=True)}], "", False),
            ("malformed-error-flag", [dict(success, is_error="false")], "", False),
            ("denied-action", [{"event": "result", "result": dict(success["result"], denied_actions=[{"action": "write_file"}])}], "", False),
            ("malformed-denied-actions", [{"event": "result", "result": dict(success["result"], denied_actions="none")}], "", False),
            ("null-denied-actions", [{"event": "result", "result": dict(success["result"], denied_actions=None)}], "", False),
            ("unidentified-denied-action", [{"event": "result", "result": dict(success["result"], denied_actions=[{}])}], "", False),
            ("tool-error", [{"event": "step_update", "step_update": {"step_type": "tool_call", "tool_info": {"error": "write failed"}}}, success], "", False),
            ("stderr-denial", [success], "RunCommand soft-denied: approval required\n", False),
        ]
        with tempfile.TemporaryDirectory(prefix="agentmux-validator-diagnostic-terminals-") as temporary:
            root = Path(temporary)
            directory = root / "fixtures"
            directory.mkdir()
            wrapper = directory / "agy"
            for name, events, stderr, passing in fixtures:
                with self.subTest(name=name):
                    wrapper.write_text(
                        "#!" + sys.executable + "\nimport json,sys\nfrom pathlib import Path\n"
                        "request=json.load(sys.stdin)\n"
                        "assert request['event']=='user' and request['message']['content']\n"
                        "assert sys.argv[sys.argv.index('--model')+1]=='gemini-3.8-flash-low'\n"
                        "Path('greeting.txt').write_text('AG_OK\\n')\n"
                        "for event in " + repr(events) + ": print(json.dumps(event))\n"
                        "sys.stderr.write(" + repr(stderr) + ")\n"
                    )
                    wrapper.chmod(0o700)
                    output = root / name
                    process = subprocess.run(
                        [sys.executable, str(SCRIPT), "--binary", sys.executable,
                         "--output", str(output), "--diagnose-antigravity"],
                        env=dict(os.environ, PATH=str(directory) + os.pathsep + os.environ.get("PATH", "")),
                        capture_output=True, text=True, timeout=30,
                    )
                    self.assertEqual(process.returncode, 0 if passing else 1, process.stdout + process.stderr)
                    record = json.loads((output / "validation.json").read_text())
                    diagnostic = json.loads((output / "diagnostic.json").read_text())
                    self.assertTrue(record["completed"])
                    self.assertEqual(record["passed"], passing)
                    self.assertEqual(record["provider_turn_limit"], 2)
                    self.assertEqual([case["name"] for case in record["cases"]], ["implicit-stream", "explicit-print"])
                    self.assertEqual(diagnostic["cases_passed"], passing)
                    self.assertTrue(diagnostic["cases_completed"])
                    self.assertNotIn("passed", diagnostic)
                    self.assertNotIn("completed", diagnostic)
                    self.assertEqual(diagnostic["provider_turn_limit"], 2)
                    for case in record["cases"]:
                        self.assertEqual(case["passed"], passing)
                        retained = json.loads((output / (case["name"] + ".events.json")).read_text())
                        self.assertEqual(retained["exit_code"], 0)
                        self.assertTrue(retained["artifact_correct"])
                        self.assertEqual(retained["events"], events)
                        self.assertEqual(retained["protocol_passed"], passing)
                        self.assertEqual(bool(retained.get("protocol_error")), not passing)

    def test_diagnostic_malformed_stream_cannot_pass_with_a_correct_artifact(self):
        with tempfile.TemporaryDirectory(prefix="agentmux-validator-diagnostic-json-") as temporary:
            root = Path(temporary)
            fixtures = root / "fixtures"
            fixtures.mkdir()
            fixture = fixtures / "agy"
            for number, payload in enumerate((
                '{"event":"result","result":{"status":"ERROR","status":"SUCCESS"}}',
                '{"event":"result","result":{"status":"ERROR","STATUS":"SUCCESS"}}',
                '{"event":"result","result":{"usage":NaN}}',
                '[]', 'not-json',
            )):
                with self.subTest(payload=payload):
                    fixture.write_text(
                        "#!" + sys.executable + "\nfrom pathlib import Path\n"
                        "Path('greeting.txt').write_text('AG_OK\\n')\n"
                        "print(" + repr(payload) + ")\n"
                    )
                    fixture.chmod(0o700)
                    output = root / ("evidence-" + str(number))
                    process = subprocess.run(
                        [sys.executable, str(SCRIPT), "--binary", sys.executable,
                         "--output", str(output), "--diagnose-antigravity"],
                        env=dict(os.environ, PATH=str(fixtures) + os.pathsep + os.environ.get("PATH", "")),
                        capture_output=True, text=True, timeout=30,
                    )
                    record = json.loads((output / "validation.json").read_text())
                    self.assertNotEqual(process.returncode, 0, process.stdout + process.stderr)
                    self.assertFalse(record["completed"])
                    self.assertFalse(record["passed"])
                    error = "JSONDecodeError" if payload == "not-json" else "ValueError"
                    self.assertEqual(record["error"], error)
                    self.assertEqual(record["cases"][0], {"name": "implicit-stream", "passed": False,
                                                         "error": error})
                    self.assertEqual((output / "implicit-stream.stdout").read_text(), payload + "\n")

    def test_comparison_exit_status_includes_evidence_export_failures(self):
        with tempfile.TemporaryDirectory(prefix="agentmux-validator-test-") as temporary:
            root = Path(temporary)
            # A transparent CLI fixture supplies a successful comparison and
            # failed exports. It cannot launch or contact any model provider.
            fixture = root / "fixture-cli"
            fixture.write_text(
                "#!" + sys.executable + "\n" + comparison_fixture_source() +
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
