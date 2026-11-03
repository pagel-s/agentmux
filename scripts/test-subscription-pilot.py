#!/usr/bin/env python3
"""Subscription route, profile, provenance and invocation-budget controls; no models."""
import contextlib
import importlib.util
import io
import json
import os
from pathlib import Path
import subprocess
from types import SimpleNamespace
import tempfile
import unittest
from unittest import mock

ROOT = Path(__file__).resolve().parents[1]


def module(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    loaded = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(loaded)
    return loaded


PILOT = module("subscription_pilot", ROOT / "examples/evaluation/subscription-pilot.py")
ADAPTER = module("pilot_agentmux", ROOT / "examples/evaluation/run-agentmux.py")


class SubscriptionPilotTests(unittest.TestCase):
    def test_api_environment_is_refused_without_printing_values(self):
        for name in ("OPENAI_API_KEY", "OPENAI_BASE_URL", "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN",
                     "CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_OAUTH_TOKEN_FILE", "CODEX_ACCESS_TOKEN", "AGENTMUX_CODEX_API_KEY"):
            with self.subTest(name=name):
                with self.assertRaises(ValueError) as error:
                    PILOT.checked_environment({name: "private-value"})
                self.assertIn(name, str(error.exception))
                self.assertNotIn("private-value", str(error.exception))
        self.assertEqual(PILOT.checked_environment({"OPENAI_API_KEY": "", "PATH": "/usr/bin"})["PATH"], "/usr/bin")

    def test_native_profiles_pin_model_auth_and_refuse_route_overrides(self):
        manifest = {"models": {"codex": "pinned-codex", "claude": "pinned-claude"}}
        args = PILOT.native_arguments("codex", ["exec", "--model", "old-model", "--sandbox", "workspace-write", "-"], manifest)
        self.assertIn('forced_login_method="chatgpt"', args)
        self.assertIn('model_provider="openai"', args)
        self.assertIn("--ignore-user-config", args)
        self.assertIn("pinned-codex", args)
        self.assertNotIn("old-model", args)
        self.assertEqual(args[-1], "-")
        for override in (["-c", "model_provider=custom"], ["-cmodel_provider=custom"], ["--profile", "custom"],
                         ["--sandbox=danger-full-access"], ["--oss"], ["--dangerously-bypass-approvals-and-sandbox"]):
            with self.subTest(override=override), self.assertRaises(ValueError):
                PILOT.native_arguments("codex", ["exec", *override], manifest)
        args = PILOT.native_arguments("claude", ["--print", "--model", "sonnet", "--input-format", "stream-json"], manifest)
        self.assertIn("pinned-claude", args)
        self.assertIn("--safe-mode", args)
        self.assertIn("--strict-mcp-config", args)
        self.assertNotIn("sonnet", args)
        for override in ("--bare", "--fallback-model=anything", "--settings", "--mcp-config", "--agents"):
            with self.subTest(override=override), self.assertRaises(ValueError):
                PILOT.native_arguments("claude", ["--print", override], manifest)

    def test_preparation_freezes_external_graders_without_starting_any_actor(self):
        with tempfile.TemporaryDirectory(prefix="amx-pilot-controls-") as temporary:
            root = Path(temporary)
            # Preparation needs a real exact source commit, but must not depend
            # on the checkout containing the live pilot's historical pin.
            repository = root / "repository"
            repository.mkdir()
            environment = {key: value for key, value in os.environ.items()
                           if not PILOT.DISALLOWED_ENV.fullmatch(key) and not key.startswith("GIT_")}
            environment.update(GIT_CONFIG_NOSYSTEM="1", GIT_CONFIG_GLOBAL=os.devnull)

            def git(*arguments):
                return subprocess.run(
                    ["git", "-C", str(repository), "-c", "core.hooksPath=" + os.devnull,
                     "-c", "user.name=Subscription Pilot Fixture", "-c", "user.email=fixture@example.invalid",
                     "-c", "commit.gpgSign=false", *arguments],
                    env=environment, check=True, capture_output=True, text=True, timeout=10,
                ).stdout.strip()

            git("init", "--quiet", "--template=")
            (repository / "README.md").write_text("Disposable subscription preparation fixture.\n")
            git("add", "README.md")
            git("commit", "--quiet", "-m", "Create preparation fixture")
            commit = git("rev-parse", "HEAD")
            marker = root / "actor-started"
            executable = root / "fixture-tool"
            executable.write_text("#!" + os.sys.executable + "\nimport sys\nfrom pathlib import Path\n"
                                  "a=sys.argv[1:]\n"
                                  "if a in (['--version'], ['version']):print('fixture-native 1')\n"
                                  "elif a==['env','GOMODCACHE']:print('/tmp/pilot-module-cache')\n"
                                  "else:Path(" + repr(str(marker)) + ").touch();raise SystemExit(90)\n")
            executable.chmod(0o700)
            output = root / "study"
            argv = ["prepare", "--output", str(output), "--repository", str(repository), "--commit", commit]
            for name in ("codex", "claude", "amx", "go"):
                argv += ["--" + name, str(executable)]
            with mock.patch.dict(os.environ, environment, clear=True), contextlib.redirect_stdout(io.StringIO()):
                self.assertEqual(PILOT.main(argv), 0)
            self.assertFalse(marker.exists())
            manifest = PILOT.load_manifest(output / "pilot.json")
            plan = json.loads((output / "plan.json").read_text())
            self.assertEqual(manifest["source_commit"], commit)
            self.assertEqual(plan["source"], {"repository": str(repository), "commit": commit})
            self.assertEqual(plan["repeats"] * len(plan["tasks"]) * len(plan["arms"]), 6)
            self.assertTrue(all(arm["timeout_seconds"] == 480 for arm in plan["arms"]))
            self.assertEqual(output.stat().st_mode & 0o777, 0o700)
            for task in plan["tasks"]:
                for path in task["checks"][0]["evaluator_files"]:
                    self.assertTrue(Path(path).is_relative_to(output / "grading") or path.endswith("scripts/workflow_processes.py"))
            protected = output / "grading/native-evidence-redaction_test.go.txt"
            protected.write_text("changed frozen grader")
            with self.assertRaisesRegex(ValueError, "pinned pilot file changed"):
                PILOT.load_manifest(output / "pilot.json")

    def test_native_boundary_limits_attempts_and_retains_only_safe_auth_summary(self):
        with tempfile.TemporaryDirectory(prefix="amx-pilot-invocations-") as temporary:
            root = Path(temporary)
            root.chmod(0o700)
            auth = root / "codex-auth"
            auth.mkdir(mode=0o700)
            authfile = auth / "auth.json"
            authfile.write_text(json.dumps({"auth_mode": "chatgpt", "tokens": {"access_token": "private-token"}}))
            authfile.chmod(0o600)
            output = root / "attempt"
            output.mkdir(mode=0o700)
            manifest = {"directory": str(root), "native_auth": {"codex_home": str(auth), "claude_config_dir": str(root)},
                        "go_mod_cache": "/tmp/module-cache", "tools": {"codex": {"path": "/bin/true", "sha256": "pinned"}, "go": {"path": "/usr/bin/go"}},
                        "models": {"codex": "pinned-codex"}}
            args = SimpleNamespace(allow_models=True, manifest=root / "pilot.json", provider="codex", output=output,
                                   kind="native-codex", arguments=["exec", "--sandbox", "workspace-write", "-"])
            with mock.patch.dict(os.environ, {"PATH": "/usr/bin", "HOME": str(root), "UNSELECTED_SECRET": "private-environment"}, clear=True), \
                    mock.patch.object(PILOT, "load_manifest", return_value=manifest), \
                    mock.patch.object(PILOT, "captured", return_value=("Logged in using ChatGPT", "")), \
                    mock.patch.object(PILOT.os, "execve") as execute:
                PILOT.native(args)
                self.assertNotIn("UNSELECTED_SECRET", execute.call_args.args[2])
                self.assertIn('forced_login_method="chatgpt"', execute.call_args.args[1])
                with self.assertRaisesRegex(ValueError, "invocation bound"):
                    PILOT.native(args)
                self.assertEqual(execute.call_count, 1)
            evidence = (output / "native-invocations.jsonl").read_text()
            self.assertNotIn("private-token", evidence)
            self.assertNotIn("private-environment", evidence)
            self.assertEqual(len(evidence.splitlines()), 1)
            self.assertEqual((output / "native-invocations.jsonl").stat().st_mode & 0o777, 0o600)

    def test_adapter_passes_profiles_and_one_attempt_to_real_launch_boundary(self):
        with tempfile.TemporaryDirectory(prefix="amx-pilot-adapter-") as temporary:
            root = Path(temporary)
            binary = root / "fixture-amx"
            binary.write_text("fixture")
            repository = root / "repo"
            repository.mkdir()
            output = root / "output"
            output.mkdir()
            task = root / "task.json"
            task.write_text(json.dumps({"prompt": "Fixture task", "development_check": ["/bin/true"]}))
            args = ["--binary", str(binary), "--repo", str(repository), "--task", str(task), "--output", str(output),
                    "--coder", "codex", "--reviewer", "claude", "--attempts", "1"]
            with mock.patch.object(ADAPTER.subprocess, "run", return_value=SimpleNamespace(returncode=0)) as launch:
                with contextlib.redirect_stderr(io.StringIO()), self.assertRaises(SystemExit):
                    ADAPTER.main(args)
                launch.assert_not_called()
                self.assertEqual(ADAPTER.main([*args, "--allow-models"]), 0)
                command = launch.call_args_list[0].args[0]
                self.assertEqual(command[command.index("--coder") + 1], "codex")
                self.assertEqual(command[command.index("--reviewer") + 1], "claude")
                self.assertEqual(command[command.index("--attempts") + 1], "1")
                self.assertEqual(launch.call_args_list[1].args[0][1:3], ["work", "apply"])


if __name__ == "__main__":
    unittest.main()
