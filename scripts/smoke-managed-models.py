#!/usr/bin/env python3
"""Opt-in real Claude/Codex/local-Pi acceptance; two cloud turns, no mocked model output."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import signal
import stat
import subprocess
import sys
import tempfile
import time
import urllib.parse
import urllib.request
import uuid


FIXTURES = Path(__file__).resolve().parent / "fixtures" / "mixed"
AGENTS = ("lead", "coder", "reviewer")


def private_json(path, value):
    descriptor = os.open(path, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
    with os.fdopen(descriptor, "w") as stream:
        json.dump(value, stream, indent=2)
        stream.write("\n")


def executable(value):
    candidate = shutil.which(value) if os.sep not in value else value
    if not candidate:
        raise RuntimeError("a required executable is unavailable")
    path = Path(candidate).absolute()
    if not path.is_file() or not os.access(path, os.X_OK):
        raise RuntimeError("a required executable is unavailable")
    return path


def dependencies(binary):
    """Expose exact executable aliases and installed package code, never a home."""
    resolved = binary.resolve(strict=True)
    found = {str(binary), str(resolved)}
    # Standalone Codex discovers its code-mode host and bundled utilities from
    # the canonical executable's release directory. Mount only known code
    # companions, not the .codex parent (which also contains native auth).
    if resolved.name == "codex" and resolved.parent.name == "bin":
        release = resolved.parent.parent
        if (release / "codex-package.json").is_file():
            for relative in ("bin/codex-code-mode-host", "codex-resources/bwrap",
                             "codex-path/rg", "codex-package.json"):
                companion = release / relative
                if companion.is_file():
                    found.add(str(companion))
    for parent in list(resolved.parents)[:7]:
        if parent == Path.home() or parent == Path("/"):
            break
        package = parent / "package.json"
        if package.is_file():
            found.add(str(parent))
            metadata = json.loads(package.read_text())
            global_root = parent.parent.parent if metadata.get("name", "").startswith("@") else parent.parent
            for name in metadata.get("optionalDependencies", {}):
                dependency = global_root.joinpath(*name.split("/"))
                if dependency.is_dir():
                    found.add(str(dependency))
            break
    with resolved.open("rb") as source:
        header = source.read(256)
    if b"node" in header:
        node = executable("node")
        found.update((str(node), str(node.resolve())))
    return sorted(path for path in found if not any(Path(path).is_relative_to(base)
                  for base in (Path("/usr"), Path("/bin"), Path("/lib"), Path("/lib64"), Path("/sbin"))))


def auth_source(value):
    path = Path(value).expanduser().absolute()
    info = path.lstat()
    if not stat.S_ISREG(info.st_mode) or stat.S_IMODE(info.st_mode) != 0o600:
        raise RuntimeError("native authentication sources must be regular private 0600 files without symlinks")
    return str(path)


def oracle(content, root):
    """Execute submitted Python only in a separate, credential-free verifier."""
    directory = root / ("oracle-" + uuid.uuid4().hex[:8])
    directory.mkdir(mode=0o700)
    (directory / "tag_normalizer.py").write_text(content)
    shutil.copyfile(FIXTURES / "oracle.py", directory / "oracle.py")
    command = [str(executable("bwrap")), "--die-with-parent", "--new-session",
               "--unshare-all", "--cap-drop", "ALL"]
    for base in ("/usr", "/bin", "/lib", "/lib64", "/sbin"):
        if Path(base).exists():
            command += ["--ro-bind", base, base]
    command += ["--proc", "/proc", "--dev", "/dev", "--tmpfs", "/tmp",
                "--bind", str(directory), "/workspace", "--chdir", "/workspace",
                "--", str(executable("python3")), "-I", "/workspace/oracle.py"]
    process = subprocess.run(command, capture_output=True, text=True, timeout=15,
                             env={"PATH": "/usr/local/bin:/usr/bin:/bin", "HOME": "/tmp", "LANG": "C.UTF-8"})
    if process.returncode != 0:
        return {"ok": False, "reason": "independent sandboxed oracle rejected the artifact"}
    try:
        result = json.loads(process.stdout)
    except ValueError:
        raise RuntimeError("independent oracle returned invalid output") from None
    if result.get("ok") is not True:
        raise RuntimeError("independent oracle returned an invalid verdict")
    return result


def check_fixtures():
    with tempfile.TemporaryDirectory(prefix="amx-fixture-") as temporary:
        root = Path(temporary)
        baseline = oracle((FIXTURES / "tag_normalizer.py").read_text(), root)
        reference = oracle("def normalize_tags(values):\n"
                           "    result = set()\n"
                           "    for value in values:\n"
                           "        if not isinstance(value, str):\n"
                           "            raise TypeError('tags must be strings')\n"
                           "        normalized = value.strip().lower()\n"
                           "        if normalized:\n"
                           "            result.add(normalized)\n"
                           "    return sorted(result)\n", root)
        if baseline["ok"] or not reference["ok"]:
            raise RuntimeError("fixture oracle failed its positive/negative controls")
        print(json.dumps({"ok": True, "model_calls": 0, "baseline_rejected": True,
                          "reference_accepted": True, "oracle_sandbox": "bubblewrap"}))


class Acceptance:
    def __init__(self, options):
        self.options = options
        self.binary = str(executable(options.binary))
        self.root = Path(tempfile.mkdtemp(prefix="amx-models-"))
        self.state = self.root / "s"
        self.helpers = self.root / "helpers"
        self.helpers.mkdir(mode=0o700)
        for name in ("coord.py", "launch_checked.py"):
            shutil.copyfile(FIXTURES / name, self.helpers / name)
        self.audit = (self.root / "runner.jsonl").open("x")
        os.chmod(self.root / "runner.jsonl", 0o600)
        self.broker = None
        self.broker_log = None
        self.supervised = False
        self.run_id = "mixed-" + uuid.uuid4().hex[:16]
        self.deadline = time.monotonic() + options.timeout
        self.report = {"ok": False, "scenario": "claude-codex-local-pi",
                       "broker_sandbox": "bubblewrap", "agent_sandboxes": 3,
                       "reviewer_mode": "analysis-only", "reviewer_ran_tools": False,
                       "requested_cloud_turns": 2, "runtime_seconds": options.timeout}

    def cli(self, *arguments, expected=0, timeout=60):
        process = subprocess.run([self.binary, "managed", *map(str, arguments)],
                                 capture_output=True, text=True, timeout=timeout)
        try:
            envelope = json.loads(process.stdout)
        except ValueError:
            raise RuntimeError("managed CLI returned invalid JSON") from None
        # Retain structured operation evidence privately; never print model
        # output, native diagnostics, credentials, or source-auth contents.
        self.audit.write(json.dumps({"at": time.time(), "operation": arguments[0],
                                     "returncode": process.returncode, "envelope": envelope}) + "\n")
        self.audit.flush()
        if process.returncode != expected or bool(envelope.get("ok")) != (expected == 0):
            raise RuntimeError("managed " + str(arguments[0]) + " failed its expected outcome")
        return envelope.get("result") if expected == 0 else envelope.get("error")

    def call(self, operation, arguments=None, agent=None, expected=0, request_id=None):
        credential = self.state / ("credentials/" + agent + ".json" if agent else "owner.json")
        args = ["call", operation, "--credential-file", str(credential), "--json", json.dumps(arguments or {})]
        if request_id:
            args += ["--request-id", request_id]
        return self.cli(*args, expected=expected)

    def wait(self, predicate, label, seconds=None):
        deadline = min(self.deadline, time.monotonic() + (seconds or self.options.timeout))
        while time.monotonic() < deadline:
            result = predicate()
            if result:
                return result
            time.sleep(0.25)
        raise RuntimeError("bounded deadline expired while waiting for " + label)

    def start_broker(self):
        self.broker_log = (self.root / ("broker-" + uuid.uuid4().hex[:6] + ".log")).open("x")
        os.chmod(self.broker_log.name, 0o600)
        self.broker = subprocess.Popen([self.binary, "managed", "serve", "--state", str(self.state),
                                        "--sandbox", "bubblewrap"], stdout=self.broker_log,
                                       stderr=subprocess.STDOUT, start_new_session=True)
        def ready():
            if self.broker.poll() is not None:
                raise RuntimeError("isolated broker exited; diagnostics retained privately")
            # A killed namespace can leave its socket inode behind. Only an
            # authenticated response proves that the replacement is listening.
            probe = subprocess.run([self.binary, "managed", "call", "identity",
                                    "--credential-file", str(self.state / "owner.json"),
                                    "--json", "{}"], capture_output=True, text=True, timeout=3)
            try:
                envelope = json.loads(probe.stdout)
            except ValueError:
                raise RuntimeError("broker readiness returned invalid JSON") from None
            if probe.returncode == 0 and envelope.get("ok"):
                return envelope.get("result", {}).get("owner") is True
            if envelope.get("error", {}).get("code") == "transport":
                return False
            raise RuntimeError("broker readiness authentication failed")
        self.wait(ready, "isolated broker readiness", 15)

    def stop_broker(self):
        if self.broker is None:
            return
        self.broker.terminate()
        try:
            self.broker.wait(timeout=10)
        except subprocess.TimeoutExpired:
            os.killpg(self.broker.pid, signal.SIGKILL)
            self.broker.wait(timeout=5)
        self.broker = None
        self.broker_log.close()
        self.broker_log = None

    def prepare(self):
        parsed = urllib.parse.urlparse(self.options.pi_base_url)
        if parsed.scheme not in ("http", "https") or parsed.hostname not in ("127.0.0.1", "localhost", "::1") or parsed.username or parsed.password:
            raise RuntimeError("the Pi reviewer endpoint must be an explicit local OpenAI-compatible URL")
        with urllib.request.urlopen(self.options.pi_base_url.rstrip("/") + "/models", timeout=5) as response:
            available = json.load(response)
        if self.options.pi_model not in {model.get("id") for model in available.get("data", [])}:
            raise RuntimeError("the requested local Pi model is not advertised by the endpoint")
        binaries = {"lead": executable(self.options.claude_bin),
                    "coder": executable(self.options.codex_bin),
                    "reviewer": executable(self.options.pi_bin)}
        sources = {"lead": auth_source(self.options.claude_auth_file),
                   "coder": auth_source(self.options.codex_auth_file)}
        native_targets = {"lead": "/home/agent/.claude/.credentials.json", "coder": "/home/agent/.codex/auth.json"}
        sentinel = self.root / "host-only-sentinel"
        sentinel.write_text("This host-only fixture must not be visible to any agent.")
        os.chmod(sentinel, 0o600)
        agents = {}
        extras = {agent: [] for agent in AGENTS}
        for declaration in self.options.dependency:
            actor, separator, path = declaration.partition("=")
            if not separator or actor not in extras:
                raise RuntimeError("dependencies require lead=PATH, coder=PATH, or reviewer=PATH")
            extras[actor].append(str(Path(path).expanduser().resolve(strict=True)))
        for actor in AGENTS:
            workspace = self.state / "workspaces" / actor
            workspace.mkdir(parents=True, mode=0o700)
            for name in ("README.md", "coord.py"):
                shutil.copyfile(FIXTURES / name, workspace / name)
            if actor == "coder":
                for name in ("tag_normalizer.py", "test_tag_normalizer.py"):
                    shutil.copyfile(FIXTURES / name, workspace / name)
            native = [str(binaries[actor].resolve(strict=True))]
            if actor == "reviewer":
                # A file bind of the Pi executable alias loses its symlink
                # semantics. Node must resolve imports from the real package
                # entrypoint rather than the alias's ~/.local/bin directory.
                entrypoint = binaries[actor].resolve(strict=True)
                with entrypoint.open("rb") as source:
                    node_entrypoint = b"node" in source.read(256)
                if node_entrypoint:
                    native = [str(executable("node").resolve(strict=True)), str(entrypoint)]
            prefix = [str(executable("python3")), str(self.helpers / "launch_checked.py"), *native]
            if actor == "lead":
                prefix += ["--allowedTools", "Read,Write,Edit,Bash"]
            if actor == "reviewer":
                prefix += ["--no-tools", "--no-extensions", "--no-context-files", "--no-skills",
                           "--no-prompt-templates", "--no-approve", "--offline"]
            agent = {"harness": {"lead": "claude", "coder": "codex", "reviewer": "pi"}[actor],
                     "command": prefix, "sandbox": "bubblewrap", "network": "host",
                     "workdir": str(workspace), "read_only_paths": sorted(set(dependencies(binaries[actor]) + extras[actor] + [str(self.helpers)])),
                     "env": {"AMX_ACCEPTANCE_AGENT": actor, "AMX_ACCEPTANCE_RUN": self.run_id,
                             "AMX_ACCEPTANCE_TIMEOUT": str(max(30, self.options.timeout - 60)),
                             "AMX_HOST_SENTINEL": str(sentinel), "AMX_HOST_STATE": str(self.state),
                             "AMX_OWNER_CREDENTIAL": str(self.state / "owner.json"),
                             "AMX_NATIVE_AUTH_SOURCES": json.dumps(list(sources.values())),
                             "AMX_PEER_NATIVE_TARGETS": json.dumps([target for agent, target in native_targets.items() if agent != actor]),
                             "AMX_OWN_NATIVE_TARGET": native_targets.get(actor, ""),
                             "AMX_EXPECTED_OPEN_FILES": str(self.options.open_files),
                             "AMX_EXPECTED_CPU_SECONDS": str(self.options.cpu_seconds),
                             "AMX_PEER_CREDENTIALS": json.dumps([str(self.state / "credentials" / (peer + ".json")) for peer in AGENTS if peer != actor])}}
            if actor in sources:
                agent["auth"] = {"file": sources[actor]}
            if actor == "lead" and self.options.claude_model:
                agent["model"] = self.options.claude_model
            if actor == "coder" and self.options.codex_model:
                agent["model"] = self.options.codex_model
            if actor == "coder":
                # Explicitly delegate native tool isolation to the enforced
                # outer boundary: this host can deny a nested user namespace.
                agent["codex_sandbox"] = "external"
            if actor == "reviewer":
                agent.update(model=self.options.pi_model, connection="local-review")
            resources = {"open_files": self.options.open_files, "cpu_seconds": self.options.cpu_seconds}
            if self.options.memory_mib:
                resources["memory_bytes"] = self.options.memory_mib * 1024 * 1024
            if self.options.cpu_percent:
                resources["cpu_percent"] = self.options.cpu_percent
            if self.options.processes:
                resources["processes"] = self.options.processes
            agent["resources"] = resources
            agents[actor] = agent
        self.report["requested_agent_resources"] = resources
        agents["lead"]["role"] = "Coordinate this acceptance task with the trusted coord.py fixture helper. Run python3 coord.py lead-workflow once using Bash with a sufficiently long timeout. It delegates to the coder, waits for its artifact, and delegates a bounded analysis review. Do not spawn agents or edit helper files. Return a short summary."
        agents["coder"]["role"] = "Implement the small normalization task in README.md. Use your tools to edit the implementation, run tests, and publish through coord.py. Preserve helpers and tests."
        agents["reviewer"]["role"] = "Provide bounded source review from the supplied artifact. Tools are disabled. Do not claim to run tests or independently verify execution."
        config = {"version": 2, "mode": "managed", "session": "mixed-model-acceptance",
                  "connections": {"local-review": {"protocol": "openai-compatible", "base_url": self.options.pi_base_url}},
                  "agents": agents,
                  "boards": {"work": {"readers": list(AGENTS), "writers": list(AGENTS)},
                             "leadership": {"readers": ["lead"], "writers": ["lead"]}},
                  "memory": {"project": {"readers": ["lead"], "writers": ["lead"]},
                             "handoff": {"readers": ["lead"], "writers": ["coder"]},
                             "checks": {"readers": ["lead"], "writers": list(AGENTS)}},
                  "policy": {"default": "deny", "grants": [
                      {"from": ["lead"], "to": ["coder", "reviewer"], "actions": ["message.send", "ping.send", "instruct"]},
                      {"from": ["coder", "reviewer"], "to": ["lead"], "actions": ["message.send", "ping.send"]}]},
                  "limits": {"max_runtime_seconds": self.options.timeout, "max_tasks": 4,
                             "max_messages": 50, "max_records": 1000, "max_requests": 1000,
                             "max_concurrent_tasks": 3, "max_message_bytes": 65536}}
        # Init owns the state root; fixture workspaces were staged separately.
        staged = self.root / "staged-workspaces"
        shutil.move(str(self.state / "workspaces"), staged)
        self.state.rmdir()
        self.config_path = self.root / "team.json"
        private_json(self.config_path, config)
        self.cli("init", self.config_path, "--state", self.state)
        for actor in AGENTS:
            shutil.copytree(staged / actor, self.state / "workspaces" / actor, dirs_exist_ok=True)
        shutil.rmtree(staged)
        coder_workspace = self.state / "workspaces" / "coder"
        for arguments in (["init", "--quiet"], ["add", "."],
                          ["-c", "user.name=Agentmux Acceptance", "-c", "user.email=acceptance@localhost",
                           "-c", "commit.gpgsign=false", "commit", "--quiet", "-m", "Acceptance fixture baseline"]):
            subprocess.run([str(executable("git")), "-C", str(coder_workspace), *arguments],
                           check=True, capture_output=True, text=True, timeout=15)

    def run(self):
        self.prepare()
        self.start_broker()
        initial = self.call("instruct", {"to": ["lead"], "text":
            "Read README.md. Run python3 coord.py lead-workflow exactly once through Bash with a timeout covering this bounded run. "
            "That trusted helper uses your authenticated identity to coordinate the board, shared memory, coder instruction, and reviewer instruction. "
            "Wait for the helper to complete, then return a short summary. Never print credentials."}, request_id=self.run_id + "-initial")["task"]
        assert initial["state"] == "proposed" and initial["delivery"] == "pending"
        self.stop_broker()
        self.start_broker()
        preserved = self.call("task.get", {"id": initial["id"]})["task"]
        assert preserved["id"] == initial["id"] and preserved["state"] == "proposed" and preserved["delivery"] == "pending" and preserved["version"] == initial["version"]
        assert len(self.call("task.list")["tasks"]) == 1, "pending instruction was resubmitted"
        self.report["pending_task_survived_broker_restart"] = True
        self.stop_broker()
        self.supervised = True
        self.cli("up", self.config_path, "--state", self.state, "--broker-sandbox", "bubblewrap", timeout=90)
        def completed():
            tasks = self.call("task.list")["tasks"]
            if any(task["state"] in ("failed", "cancelled") for task in tasks):
                raise RuntimeError("a real model task failed; inspect private state without publishing credentials")
            return tasks if len(tasks) == 3 and all(task["state"] in ("submitted", "verified") for task in tasks) else None
        tasks = self.wait(completed, "three real model task submissions")
        assert len([task for task in tasks if task["to"] == "lead"]) == 1
        assert next(task for task in tasks if task["to"] == "lead")["id"] == initial["id"]
        assert all(task["from"] == "lead" for task in tasks if task["to"] != "lead"), "hierarchical delegation did not come from the real lead"
        artifact = json.loads(self.call("memory.get", {"namespace": "handoff", "key": "implementation"})["entry"]["value"])
        assert artifact["name"] == "tag_normalizer.py" and isinstance(artifact["content"], str)
        assert len(artifact["content"].encode()) <= 16384
        assert hashlib.sha256(artifact["content"].encode()).hexdigest() == artifact["sha256"]
        assert self.call("memory.get", {"namespace": "handoff", "key": "implementation"}, "reviewer", 2)["code"] == "forbidden"
        assert self.call("instruct", {"to": ["lead"], "text": "Unauthorized reverse probe."}, "coder", 2)["code"] == "forbidden"
        assert self.call("message.list", {"board": "leadership"}, "coder", 2)["code"] == "forbidden"
        checks = {}
        for actor in AGENTS:
            checks[actor] = json.loads(self.call("memory.get", {"namespace": "checks", "key": actor})["entry"]["value"])
            assert checks[actor]["host_state_hidden"] and checks[actor]["owner_credential_hidden"] and checks[actor]["peer_credentials_hidden"]
        verdict = oracle(artifact["content"], self.root)
        assert verdict["ok"], "independent oracle rejected the implementation"
        coder_task = next(task for task in tasks if task["to"] == "coder")
        verified = self.call("task.verify", {"id": coder_task["id"], "version": coder_task["version"]})["task"]
        assert verified["state"] == "verified" and verified["id"] == coder_task["id"]
        coder_task.update(verified)
        reviewer = next(task for task in tasks if task["to"] == "reviewer")
        assert reviewer.get("result", "").strip(), "local Pi did not produce a genuine review response"
        assert artifact["sha256"] in reviewer["text"] and artifact["content"] in reviewer["text"], "reviewer did not receive the scoped artifact payload"
        assert json.loads(self.call("memory.get", {"namespace": "project", "key": "plan"})["entry"]["value"])["task"] == "normalize_tags"
        messages = self.call("message.list", {"board": "work"})["messages"]
        assert {"lead", "coder"}.issubset({message["from"] for message in messages})
        self.report.update(ok=True, actual_submitted_tasks=3, cloud_tasks=2,
                           local_reviewer_tasks=1, supervised_tmux=True,
                           authenticated_board=True, authenticated_shared_memory=True,
                           lead_instruction_hierarchy=True, unauthorized_reverse_instruction_denied=True,
                           scoped_artifact_sha256=artifact["sha256"], reviewer_direct_artifact_read_denied=True,
                           observer_oracle=verdict, owner_verified_coder_task=True, isolation_checks=checks,
                           review_response_bytes=len(reviewer["result"].encode()),
                           review_response_sha256=hashlib.sha256(reviewer["result"].encode()).hexdigest(),
                           tasks=[{"to":task["to"],"state":task["state"],"usage":task.get("usage")} for task in tasks])

    def close(self):
        safe = True
        try:
            self.stop_broker()
            if self.supervised:
                self.cli("down", "--state", self.state, timeout=45)
        except Exception:
            safe = False
        self.report["clean_shutdown"] = safe
        self.audit.close()
        private_json(self.root / "report.json", self.report)
        if self.options.report:
            private_json(Path(self.options.report).expanduser().absolute(), self.report)
        if safe and self.report["ok"] and not self.options.keep_output:
            shutil.rmtree(self.root)
        else:
            print("Private acceptance artifacts retained at " + str(self.root), file=sys.stderr)
        return safe


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", default="bin/agentmux")
    parser.add_argument("--allow-models", action="store_true", help="authorize two real cloud tasks and one local Pi task")
    parser.add_argument("--check-fixtures", action="store_true", help="validate positive/negative oracle controls without model calls")
    parser.add_argument("--claude-bin", default="claude")
    parser.add_argument("--codex-bin", default="codex")
    parser.add_argument("--pi-bin", default="pi")
    parser.add_argument("--claude-auth-file", default=str(Path.home() / ".claude" / ".credentials.json"))
    parser.add_argument("--codex-auth-file", default=str(Path.home() / ".codex" / "auth.json"))
    parser.add_argument("--claude-model")
    parser.add_argument("--codex-model")
    parser.add_argument("--pi-model", help="exact model ID advertised by the already running local endpoint")
    parser.add_argument("--pi-base-url", default="http://127.0.0.1:11434/v1")
    parser.add_argument("--dependency", action="append", default=[], metavar="AGENT=PATH")
    parser.add_argument("--timeout", type=int, default=600)
    parser.add_argument("--memory-mib", type=int, default=0)
    parser.add_argument("--cpu-percent", type=int, default=0)
    parser.add_argument("--processes", type=int, default=0)
    parser.add_argument("--open-files", type=int, default=1024)
    parser.add_argument("--cpu-seconds", type=int, default=120)
    parser.add_argument("--bounded-resources", action="store_true", help="require per-agent 2GiB/one-CPU/256-task cgroups, plus rlimits")
    parser.add_argument("--keep-output", action="store_true")
    parser.add_argument("--report", help="write a new private secret-safe JSON summary file")
    options = parser.parse_args()
    if options.check_fixtures:
        check_fixtures()
        return
    if not options.allow_models:
        parser.error("real acceptance requires --allow-models; use --check-fixtures for a no-model oracle check")
    if not options.pi_model:
        parser.error("--pi-model is required; the script does not download or start a model")
    if options.timeout < 60 or options.timeout > 1200:
        parser.error("--timeout must be between 60 and 1200 seconds")
    if options.report and Path(options.report).expanduser().exists():
        parser.error("--report must name a new file")
    if options.bounded_resources:
        options.memory_mib = options.memory_mib or 2048
        options.cpu_percent = options.cpu_percent or 100
        options.processes = options.processes or 256
    os.environ["OWNER_ONLY_SECRET"] = "owner-fixture-" + uuid.uuid4().hex
    runner = Acceptance(options)
    try:
        runner.run()
    except Exception:
        runner.report["failure"] = "acceptance failed; inspect private artifacts; no model turns are automatically retried"
        raise RuntimeError(runner.report["failure"]) from None
    finally:
        clean = runner.close()
    if not clean:
        raise RuntimeError("acceptance completed but process cleanup could not be confirmed")
    print(json.dumps(runner.report, indent=2))


if __name__ == "__main__":
    main()
