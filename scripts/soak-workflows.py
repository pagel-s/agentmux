#!/usr/bin/env python3
"""Seeded, bounded workflow fault soak in disposable private projects.

This runner never invokes live providers. Guided commands resolve only to the
transparent protocol fixtures created here, with an empty private HOME and no
inherited credentials. Every iteration exercises every selected scenario in a
seeded order. Evidence is retained, including the first failed or interrupted
case. This is operational evidence for the recorded duration, not an availability
promise or an audit of independent host services.
"""
import argparse
import ctypes
import hashlib
import importlib.util
import json
import math
import os
from pathlib import Path
import posixpath
import random
import shutil
import signal
import socket
import stat
import subprocess
import sys
import threading
import time
import uuid

PROCESS_SOURCE = Path(__file__).with_name("workflow_processes.py").resolve()
_process_spec = importlib.util.spec_from_file_location("workflow_evidence_processes", PROCESS_SOURCE)
PROCESS = importlib.util.module_from_spec(_process_spec)
_process_spec.loader.exec_module(PROCESS)


SCENARIOS = (
    "guided_history", "check_failure", "review_rejection", "stale_source",
    "stale_result", "cancel_turn", "crash_turn", "crash_apply_before",
    "crash_apply_after", "concurrent_apply", "isolation_boundary",
)
# Exact fixture-owned control files. They must be visible to the admitted
# source-writing Git namespace, but excluded from snapshots and owner-byte
# comparisons. Similar prefixes and all ordinary owner files remain observed.
FAULT_MARKER_NAMES = tuple(".agentmux-soak-" + name for name in (
    "apply-ready", "apply-release", "apply-count", "unexpected-replay",
))
MAX_COMMAND_OUTPUT = 64 << 20
LIBC = ctypes.CDLL(None, use_errno=True)


class SoakFailure(RuntimeError):
    pass


class SoakInterrupted(SoakFailure):
    pass


def require(condition, message):
    if not condition:
        raise SoakFailure(message)


def exact_int(value, expected):
    return type(value) is int and value == expected


def empty_error(value):
    return value is None or (isinstance(value, str) and value == "")


def optional_text(value):
    return value is None or isinstance(value, str)


def false_flag(record, key):
    return record.get(key, False) is False


def same_json(value, expected):
    return json.dumps(value, sort_keys=True, allow_nan=False) == json.dumps(expected, sort_keys=True, allow_nan=False)


def object_id(value):
    return (isinstance(value, str) and len(value) in (40, 64)
            and all(char in "0123456789abcdefABCDEF" for char in value))


def digest_id(value):
    return (isinstance(value, str) and len(value) == 64
            and all(char in "0123456789abcdef" for char in value))


def revision_evidence(value):
    return (isinstance(value, dict) and object_id(value.get("tree"))
            and digest_id(value.get("content_sha256"))
            and all(key not in value or digest_id(value[key])
                    for key in ("ignore_sha256", "selection_sha256")))


def private_pins(value, *, ignore=None, selection=None):
    return (isinstance(value, dict) and digest_id(value.get("ignore_sha256"))
            and digest_id(value.get("selection_sha256"))
            and (ignore is None or value["ignore_sha256"] == ignore)
            and (selection is None or value["selection_sha256"] == selection))


def stamp():
    return time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())


def private_json(path, value):
    temporary = path.with_name(".soak-" + uuid.uuid4().hex)
    descriptor = os.open(temporary, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    try:
        with os.fdopen(descriptor, "w", encoding="utf-8") as handle:
            json.dump(value, handle, indent=2, ensure_ascii=True)
            handle.write("\n")
            handle.flush()
            os.fsync(handle.fileno())
        os.replace(temporary, path)
        directory = os.open(path.parent, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
        try:
            os.fsync(directory)
        finally:
            os.close(directory)
    finally:
        try:
            temporary.unlink()
        except FileNotFoundError:
            pass


def read_json(path, maximum=32 << 20):
    descriptor = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
    with os.fdopen(descriptor, "rb") as handle:
        information = os.fstat(handle.fileno())
        require(stat.S_ISREG(information.st_mode) and information.st_size <= maximum,
                "evidence must be a bounded regular file")
        data = handle.read(maximum + 1)
    require(len(data) <= maximum, "evidence grew beyond its size limit")
    def unambiguous(pairs):
        result, folded = {}, set()
        for key, value in pairs:
            identity = key.casefold()
            require(identity not in folded, "duplicate JSON evidence field or case-aliased spelling")
            folded.add(identity)
            result[key] = value
        return result

    def nonfinite(_):
        raise SoakFailure("JSON evidence contains a nonfinite number")

    def finite_float(literal):
        value = float(literal)
        if not math.isfinite(value):
            return nonfinite(literal)
        return value

    return json.loads(data.decode("utf-8"), object_pairs_hook=unambiguous,
                      parse_constant=nonfinite, parse_float=finite_float)


def process_identity(pid):
    """Host PID + Linux start ticks; never use namespace-local fixture PIDs."""
    return PROCESS.process_identity(pid)


def same_process(identity):
    current = process_identity(identity["pid"])
    return current is not None and current["start_ticks"] == identity["start_ticks"] and current["state"] != "Z"


def identity_exists(identity):
    current = process_identity(identity["pid"])
    return current is not None and current["start_ticks"] == identity["start_ticks"]


def enable_subreaper():
    # Hard-killing a controller creates orphans by design. Adopt only this
    # runner's descendants so cleanup can reap their zombies instead of relying
    # on an outer container's PID 1 to do so during a long fault soak.
    result = LIBC.prctl(ctypes.c_int(36), ctypes.c_ulong(1), ctypes.c_ulong(0), ctypes.c_ulong(0), ctypes.c_ulong(0))
    if result != 0:
        raise OSError(ctypes.get_errno(), "cannot establish soak child-subreaper cleanup")


def parent_death_guard():
    # No thread may hold Python/libc locks across this small fork child hook.
    # This command-line runner is deliberately single-threaded; refuse an
    # embedding which would make preexec unsafe instead of weakening cleanup.
    require(threading.active_count() == 1, "soak child launch requires its single-threaded controller")
    owner = os.getpid()

    def prepare():
        result = LIBC.prctl(ctypes.c_int(1), ctypes.c_ulong(signal.SIGKILL), ctypes.c_ulong(0), ctypes.c_ulong(0), ctypes.c_ulong(0))
        if result != 0 or os.getppid() != owner:
            os._exit(125)
    return prepare


def stop_identities(handles):
    """Never turn diagnostic PID/start-time dictionaries into signal targets."""
    stopped, errors = 0, []
    for handle in reversed(list(handles)):
        require(isinstance(handle, PROCESS.PinnedProcess), "cleanup requires admitted pidfd handles")
        try:
            stopped += handle.send_signal(signal.SIGKILL)
        except BaseException as error:
            errors.append(type(error).__name__)
    if errors:
        raise SoakFailure("admitted pidfd signaling failed: " + "; ".join(errors))
    return stopped


def artifact_bytes(root):
    total, count = 0, 0
    for current, directories, files in os.walk(root, followlinks=False):
        for name in directories + files:
            information = os.lstat(Path(current) / name)
            count += 1
            require(count <= 500000, "soak evidence exceeded 500,000 filesystem entries")
            if stat.S_ISREG(information.st_mode) or stat.S_ISLNK(information.st_mode):
                total += information.st_size
    return total


class Soak:
    def __init__(self, args):
        PROCESS.require_process_support()
        self.args = args
        self.root = args.output
        self.binary = self.root / "amx-under-test"
        shutil.copyfile(args.binary, self.binary)
        self.binary.chmod(0o700)
        self.binary_sha256 = hashlib.sha256(self.binary.read_bytes()).hexdigest()
        self.harness_source = Path(__file__).resolve()
        self.harness_bytes = self.harness_source.read_bytes()
        self.harness_sha256 = hashlib.sha256(self.harness_bytes).hexdigest()
        harness_copy = self.root / "soak-runner-under-test.py"
        harness_copy.write_bytes(self.harness_bytes)
        harness_copy.chmod(0o600)
        self.harness_paths = {"runner": self.harness_source,
                              "regression_tests": self.harness_source.with_name("test-soak-workflows.py"),
                              "process_identity_helper": PROCESS_SOURCE}
        self.harness_hashes = {}
        for name, path in self.harness_paths.items():
            content = path.read_bytes()
            self.harness_hashes[name] = hashlib.sha256(content).hexdigest()
            if name != "runner":
                copy = self.root / (name + "-under-test.py")
                copy.write_bytes(content)
                copy.chmod(0o600)
        self.deadline = time.monotonic() + args.duration_seconds
        self.cancelled = None
        self.active = []
        enable_subreaper()
        self.iteration = 0
        self.current = None
        self.current_index = None
        self.guided_reports = []
        self.fixture_directory = self.root / "fixture-providers"
        self.fixture_directory.mkdir(mode=0o700)
        self.home = self.root / "empty-home"
        self.home.mkdir(mode=0o700)
        temporary = self.root / "tmp"
        temporary.mkdir(mode=0o700)
        self.python = "/usr/bin/python3" if os.access("/usr/bin/python3", os.X_OK) else str(Path(sys.executable).resolve())
        self.git_binary = shutil.which("git")
        require(self.git_binary is not None, "Git is required")
        self.git_binary = str(Path(self.git_binary).resolve())
        self.environment = {
            "PATH": str(self.fixture_directory) + os.pathsep + os.environ.get("PATH", "/usr/bin:/bin"),
            "HOME": str(self.home), "TMPDIR": str(temporary),
            "XDG_STATE_HOME": str(self.home / "state"), "LANG": "C.UTF-8", "TERM": "dumb",
            "PYTHONDONTWRITEBYTECODE": "1", "TZ": "UTC",
        }
        self.git_environment = dict(self.environment, GIT_CONFIG_GLOBAL=os.devnull,
                                    GIT_CONFIG_NOSYSTEM="1", GIT_TERMINAL_PROMPT="0")
        self.summary = {
            "version": 1, "state": "running", "completed": False, "started_at": stamp(),
            "seed": args.seed, "iterations_requested": args.iterations,
            "iterations_completed": 0, "duration_limit_seconds": args.duration_seconds,
            "storage": args.storage, "process_containment": args.containment,
            "isolation": args.isolation, "scenarios": args.scenario or list(SCENARIOS),
            "binary_sha256": self.binary_sha256, "model_calls": 0,
            "harness_sha256": self.harness_sha256,
            "harness_files_sha256": self.harness_hashes,
            "cases": [], "assertions_checked": 0, "process_identities_observed": 0,
            "coverage_exclusions": [],
            "weaker_policy_processes_cleaned": 0,
            "child_subreaper": True, "adopted_fixture_processes_reaped": 0,
            "controller_parent_death_signal": "SIGKILL",
            "process_cleanup": "Linux pidfd signals to admitted controllers and observed descendants; no numeric PID/group signaling",
            "artifact_budget_enforcement": "Observed after quiescent cases with 64 KiB final-record reserve; not a hostile-code disk quota.",
            "coverage": "Recorded model-free workflow faults and observed fixture process trees; no multi-day or independent-host-service guarantee.",
        }
        self.write_fixtures()
        private_json(self.root / "soak.json", self.summary)

    def check_budget(self):
        if self.cancelled is not None:
            raise SoakInterrupted("soak interrupted by signal " + str(self.cancelled))
        if time.monotonic() >= self.deadline:
            raise SoakInterrupted("soak deadline expired before all requested cases completed")

    def save(self, index=False):
        # Detailed command/process evidence stays in case.json. Keeping only a
        # compact index here avoids rewriting all prior commands after every
        # assertion during long soaks. Admission/finalization publish the index.
        # Update the in-memory index before I/O so failure recovery can retain a
        # nonpassing summary even when the detailed case record cannot be saved.
        if index:
            if self.current_index is not None:
                for key in ("state", "error", "finished_at", "artifact_bytes", "coverage_note"):
                    if key in self.current:
                        self.current_index[key] = self.current[key]
                self.current_index["assertions_checked"] = len(self.current["assertions"])
                self.current_index["commands_observed"] = len(self.current["commands"])
        if self.current is not None:
            private_json(Path(self.current["directory"]) / "case.json", self.current)
        if index:
            private_json(self.root / "soak.json", self.summary)

    def check(self, condition, message):
        self.check_budget()
        require(condition, message)
        self.current["assertions"].append(message)
        self.summary["assertions_checked"] += 1
        self.save()
        self.check_budget()

    def write_fixtures(self):
        common = (
            "#!" + self.python + "\nimport json,re,sys,uuid\nfrom pathlib import Path\n"
            "prompt=sys.stdin.read()\nmatch=re.search(r'SOAK_VALUE:([0-9]+)',prompt)\n"
            "assert match is not None, 'Missing deterministic soak task'\nvalue=match.group(1)+'\\n'\n"
        )
        coder = common + (
            "Path('answer.txt').write_text(value)\np=Path('provider-turns.txt')\n"
            "p.write_text(str(int(p.read_text())+1 if p.exists() else 1))\n"
            "print(json.dumps({'type':'thread.started','thread_id':str(uuid.uuid4())}))\n"
            "print(json.dumps({'type':'item.completed','item':{'type':'agent_message','text':'Protocol fixture wrote the exact requested answer.'}}))\n"
            "print(json.dumps({'type':'turn.completed','usage':{'input_tokens':0,'output_tokens':0}}))\n"
        )
        reviewer = common + (
            "assert Path('answer.txt').read_text()==value, 'Reviewer observed wrong answer'\n"
            "verdict=json.dumps({'decision':'approve','summary':'Protocol fixture read the exact expected file value.','findings':[]})\n"
        )
        fixtures = {
            "codex": coder,
            "agy": reviewer + "print(json.dumps({'event':'result','result':{'status':'SUCCESS','response':verdict}}))\n",
            # This is a protocol fixture, never the Claude CLI or a live model.
            "claude": reviewer + "print(json.dumps({'type':'result','subtype':'success','result':verdict,'is_error':False,'permission_denials':[]}))\n",
            "pi": "#!" + self.python + "\nraise RuntimeError('Unexpected provider profile; soak cannot fall back to live tools')\n",
        }
        hashes = {}
        for name, code in fixtures.items():
            path = self.fixture_directory / name
            path.write_text(code)
            path.chmod(0o700)
            hashes[name] = hashlib.sha256(path.read_bytes()).hexdigest()
        private_json(self.root / "fixture-providers.json", {"live": False, "sha256": hashes})

    def git(self, repository, *arguments):
        self.check_budget()
        process = subprocess.run(
            [self.git_binary, "-C", str(repository), "-c", "core.hooksPath=/dev/null",
             "-c", "commit.gpgsign=false", *arguments], env=self.git_environment,
            capture_output=True, timeout=min(15, max(0.1, self.deadline - time.monotonic())),
            preexec_fn=parent_death_guard(),
        )
        require(process.returncode == 0, "disposable Git operation failed: " + process.stderr.decode(errors="replace")[:2048])
        return process.stdout

    def project(self, root):
        repository = root / "project"
        repository.mkdir(mode=0o700)
        self.git(repository, "init", "-q")
        # Keep strict source metadata checks free of fixture-owned detached
        # maintenance, including Git versions using the older auto-GC path.
        self.git(repository, "config", "--local", "maintenance.auto", "false")
        self.git(repository, "config", "--local", "gc.auto", "0")
        (repository / "owner.txt").write_text("committed\n")
        (repository / ".gitignore").write_text("__pycache__/\n" + "".join("/" + name + "\n" for name in FAULT_MARKER_NAMES))
        self.git(repository, "add", ".")
        self.git(repository, "-c", "user.name=Workflow Soak", "-c", "user.email=soak@example.invalid", "commit", "-qm", "Soak baseline")
        (repository / "owner.txt").write_text("staged owner work\n")
        self.git(repository, "add", "owner.txt")
        (repository / "owner.txt").write_text("unstaged owner work\n")
        (repository / "owner.bin").write_bytes(bytes([0, 1, 2, 255]))
        return repository

    def source(self, repository):
        files = {}
        for path in repository.iterdir():
            if path.name != ".git" and path.name not in FAULT_MARKER_NAMES:
                information = path.lstat()
                require(stat.S_ISREG(information.st_mode), "unexpected fixture source type")
                files[path.name] = {"sha256": hashlib.sha256(path.read_bytes()).hexdigest(), "mode": stat.S_IMODE(information.st_mode)}
        result = {"head": self.git(repository, "rev-parse", "HEAD").decode().strip(),
                  "index_sha256": hashlib.sha256((repository / ".git/index").read_bytes()).hexdigest(), "files": files}
        if self.args.storage == "private":
            digest = hashlib.sha256()
            for path in sorted((repository / ".git").rglob("*")):
                relative = path.relative_to(repository / ".git").as_posix()
                if relative == "agentmux-apply.lock":
                    continue  # Explicit application owns this coordination inode.
                information = path.lstat()
                digest.update(relative.encode() + b"\0" + str(information.st_mode).encode() + b"\0")
                if stat.S_ISREG(information.st_mode):
                    digest.update(path.read_bytes())
                elif stat.S_ISLNK(information.st_mode):
                    digest.update(os.readlink(path).encode())
            result["git_metadata_sha256"] = digest.hexdigest()
        return result

    def plan(self, repository, code=None, checks=None):
        if code is None:
            code = "from pathlib import Path; Path('answer.txt').write_text('42\\n'); Path('turn-count.txt').write_text('1'); print('Fixture implemented answer')"
        result = {
            "version": 1, "repository": str(repository), "execution": "local",
            "storage": self.args.storage, "process_containment": self.args.containment,
            "mode": "compare", "goal": "Create answer.txt containing exactly 42 followed by a newline.",
            "timeout_seconds": 45, "max_attempts": 1, "max_parallel": 1,
            "candidates": [{"id": "writer", "harness": "command", "command": [self.python, "-B", "-c", code]}],
            "checks": checks or [{"name": "exact-answer", "command": [self.python, "-B", "-c", "from pathlib import Path; assert Path('answer.txt').read_bytes()==b'42\\n'"], "timeout_seconds": 5}],
        }
        if self.args.isolation == "bubblewrap":
            result["isolation"] = {"mode": "bubblewrap", "network": "none", "read_only_paths": [str(self.fixture_directory)]}
        return result

    def launch(self, label, arguments, environment=None):
        self.check_budget()
        number = len(self.current["commands"]) + 1
        prefix = Path(self.current["directory"]) / (str(number).zfill(2) + "-" + label)
        stdout = open(prefix.with_suffix(".stdout"), "xb")
        stderr = open(prefix.with_suffix(".stderr"), "xb")
        os.chmod(stdout.name, 0o600)
        os.chmod(stderr.name, 0o600)
        row = {"label": label, "argv": list(map(str, arguments)), "started_at": stamp(), "state": "running", "stdout": stdout.name, "stderr": stderr.name}
        try:
            process = PROCESS.launch_pinned([str(self.binary), *map(str, arguments)], env=environment or self.environment,
                                       stdin=subprocess.DEVNULL, stdout=stdout, stderr=stderr, start_new_session=True,
                                       preexec_fn=parent_death_guard())
        except BaseException:
            stdout.close()
            stderr.close()
            raise
        row["controller_pid"] = process.pid
        item = {"process": process, "row": row, "stdout": stdout, "stderr": stderr, "identities": {}, "started": time.monotonic()}
        self.active.append(item)
        item["tree"] = process._validation_tree
        self.current["commands"].append(row)
        self.observe(item)
        self.save()
        return item

    def observe(self, item):
        for identity in item["tree"].observe():
            item["identities"][(identity["pid"], identity["start_ticks"])] = identity
        for stream in (item["stdout"], item["stderr"]):
            require(os.fstat(stream.fileno()).st_size <= MAX_COMMAND_OUTPUT, "command output exceeded 64 MiB")

    def wait_ready(self, item, marker, seconds=15):
        until = min(self.deadline, time.monotonic() + seconds)
        while not marker.exists():
            self.check_budget()
            self.observe(item)
            require(item["process"].poll() is None, "fixture command ended before its readiness marker")
            require(time.monotonic() < until, "fixture readiness deadline expired")
            time.sleep(0.02)
        self.observe(item)

    def finish(self, item, expected=0, timeout=60, envelope=True):
        until = min(self.deadline, time.monotonic() + timeout)
        while item["process"].poll() is None:
            self.check_budget()
            self.observe(item)
            require(time.monotonic() < until, "CLI operation exceeded its deadline")
            time.sleep(0.02)
        self.observe(item)
        item["stdout"].close()
        item["stderr"].close()
        row = item["row"]
        row.update(state="finished", finished_at=stamp(), exit_code=item["process"].returncode,
                   duration_seconds=round(time.monotonic() - item["started"], 3), observed_processes=list(item["identities"].values()))
        self.summary["process_identities_observed"] += len(item["identities"])
        self.save()
        if expected is not None:
            self.check(item["process"].returncode == expected, row["label"] + " returned its expected exit status")
        result = None
        if envelope:
            parsed = read_json(Path(row["stdout"]))
            self.check(parsed.get("ok") is (expected == 0), row["label"] + " returned its expected structured outcome")
            result = parsed.get("result")
        return result

    def call(self, label, *arguments, expected=0, environment=None):
        return self.finish(self.launch(label, arguments, environment), expected=expected)

    def run_plan(self, repository, plan, expected=0):
        root = Path(self.current["directory"])
        before = self.source(repository)
        private_json(root / "plan.json", plan)
        result = self.call("run", "flow", "mission" if plan["mode"] == "mission" else "run", root / "plan.json", "--output", root / "run", expected=expected)
        self.assert_report(result, repository, root / "run", plan["mode"], "completed" if expected == 0 else "failed", plan["goal"])
        if plan["mode"] == "compare":
            candidates = result.get("candidates")
            self.check(isinstance(candidates, list) and [row.get("id") for row in candidates] == [row["id"] for row in plan["candidates"]],
                       "comparison retained every declared candidate in its expected order")
            for candidate, declared in zip(candidates, plan["candidates"]):
                worktree = root / "run/candidates" / declared["id"] / "worktree"
                self.check(candidate.get("harness") == declared["harness"] and candidate.get("worktree") == str(worktree) and exact_int(candidate.get("attempts"), 1),
                           "comparison recorded exactly one expected fixture attempt in its retained worktree")
                self.check(optional_text(candidate.get("error")), "candidate error evidence retained its optional text type")
                if expected == 0:
                    self.check(candidate.get("state") == "verified" and empty_error(candidate.get("error")), "successful comparison retained a verified candidate without a recorded failure")
                    self.assert_verification(candidate.get("verification"), plan["checks"], worktree, snapshot=result["snapshot"])
                    self.assert_final_snapshot(candidate.get("final_snapshot"), candidate["verification"], result["snapshot"])
                    self.check((worktree / "answer.txt").read_bytes() == b"42\n" and (worktree / "turn-count.txt").read_bytes() == b"1",
                               "retained comparison fixture produced its exact answer and single-turn proof")
        self.check(self.source(repository) == before, "workflow execution preserved the complete source, staging and Git metadata before application")
        return result

    def assert_report(self, result, repository, output, mode, state, goal):
        self.check(isinstance(result, dict), "workflow result is an object")
        self.check(same_json(result, read_json(output / "report.json")), "CLI result exactly matches its retained workflow report")
        self.check(exact_int(result.get("version"), 1) and isinstance(result.get("id"), str) and bool(result["id"])
                   and result.get("repository") == str(repository) and result.get("output") == str(output)
                   and result.get("mode") == mode and result.get("state") == state and result.get("goal") == goal
                   and result.get("execution") == "local"
                   and (empty_error(result.get("error")) if state == "completed" else optional_text(result.get("error"))),
                   "workflow retained the exact fixture identity, source, output, mode and outcome")
        self.check(isinstance(result.get("storage", "shared"), str)
                   and result.get("storage", "shared") == self.args.storage and result.get("process_containment") == self.args.containment,
                   "workflow recorded the requested storage and process containment policies")
        isolation = result.get("isolation")
        self.check((isolation is None and self.args.isolation == "none")
                   or (isinstance(isolation, dict) and isolation.get("mode") == self.args.isolation
                       and (self.args.isolation != "bubblewrap" or isolation.get("network") == "none")),
                   "workflow recorded the requested filesystem and network isolation policies")
        snapshot = result.get("snapshot")
        self.check(revision_evidence(snapshot), "workflow retained valid starting Git and exact-content digests")
        if self.args.storage == "private":
            self.check(private_pins(snapshot), "private workflow retained its pinned ignore and selection digests")

    def assert_verification(self, verification, checks, worktree, *, snapshot=None):
        self.check(isinstance(verification, dict) and verification.get("passed") is True
                   and verification.get("repository") == str(worktree), "verification passed against the expected retained worktree")
        actual = verification.get("checks")
        self.check(isinstance(actual, list) and len(actual) == len(checks)
                   and all(row.get("name") == declared["name"] and row.get("command") == declared["command"]
                           and row.get("passed") is True and exact_int(row.get("exit_code"), 0) and empty_error(row.get("error"))
                           for row, declared in zip(actual, checks)), "every exact declared check ran successfully")
        self.check(revision_evidence(verification) and object_id(verification.get("ending_tree"))
                   and verification["tree"] == verification["ending_tree"]
                   and digest_id(verification.get("ending_content_sha256"))
                   and verification["content_sha256"] == verification["ending_content_sha256"],
                   "successful checks retained matching starting and ending result fingerprints")
        if self.args.storage == "private":
            self.check(private_pins(verification, ignore=snapshot["ignore_sha256"] if snapshot else None),
                       "private checks retained valid pinned selection and expected ignore digests")
        self.check(isinstance(verification.get("storage", "shared"), str)
                   and verification.get("storage", "shared") == self.args.storage
                   and verification.get("process_containment") == self.args.containment
                   and isinstance(verification.get("isolation", "none"), str)
                   and verification.get("isolation", "none") == self.args.isolation,
                   "declared checks used the required process and filesystem boundaries")

    def assert_final_snapshot(self, final, verification, snapshot):
        self.check(revision_evidence(final) and final["tree"] == verification["tree"]
                   and final["content_sha256"] == verification["content_sha256"],
                   "final snapshot retained the exact verified revision digests")
        if self.args.storage == "private":
            self.check(private_pins(final, ignore=snapshot["ignore_sha256"], selection=verification["selection_sha256"]),
                       "private final snapshot retained the verified selection and expected ignore digests")

    def assert_same_revision(self, observed, expected):
        self.check(revision_evidence(observed) and revision_evidence(expected)
                   and observed["tree"] == expected["tree"]
                   and observed["content_sha256"] == expected["content_sha256"],
                   "mission evidence preserved the current revision chain")
        if self.args.storage == "private":
            self.check(private_pins(expected) and private_pins(observed, ignore=expected["ignore_sha256"],
                                                               selection=expected["selection_sha256"]),
                       "mission evidence preserved the current ignore and selection chain")

    def assert_review(self, step, report, expected, *, gate, decision):
        self.check(optional_text(step.get("error")), "review error evidence retained its optional text type")
        self.check(report.get("review_context") == "fresh" and step.get("review_context") == "fresh"
                   and step.get("read_only") is True,
                   "independent review retained fresh report/step context and its read-only boundary")
        self.check(step.get("gate_review", False) is gate,
                   "review recorded its exact planned final-gate role")
        review = step.get("review")
        self.check(isinstance(review, dict) and set(review) == {"decision", "summary", "findings"}
                   and review.get("decision") in ("approve", "request_changes")
                   and isinstance(review.get("summary"), str) and bool(review["summary"].strip())
                   and len(review["summary"].encode()) <= 16 << 10,
                   "structured review retained its exact verdict fields and meaningful bounded summary")
        findings = review["findings"]
        self.check(isinstance(findings, list) and len(findings) <= 50
                   and all(isinstance(finding, dict) for finding in findings)
                   and (not findings if review["decision"] == "approve" else bool(findings)),
                   "structured review decision agreed with its explicit findings array")
        for finding in findings:
            self.check(set(finding) == {"file", "line", "message"}, "review finding retained every required field")
            filename, line, message = finding["file"], finding["line"], finding["message"]
            self.check(isinstance(filename, str) and bool(filename) and len(filename.encode()) <= 4096
                       and not filename.startswith("/") and not any(char in filename for char in "\\\0\r\n")
                       and posixpath.normpath(filename) not in ("..", ".git")
                       and not posixpath.normpath(filename).startswith(("../", ".git/"))
                       and type(line) is int and 1 <= line <= 10_000_000
                       and isinstance(message, str) and bool(message.strip()) and len(message.encode()) <= 8192,
                       "review finding identified a valid project-relative location and actionable message")
        self.check(review["decision"] == decision, "review returned the expected fixture decision")
        self.check(revision_evidence(expected), "review expected revision retained valid Git and exact-content digests")
        if self.args.storage == "private":
            self.check(private_pins(expected, ignore=report["snapshot"]["ignore_sha256"]),
                       "review expected revision retained the run's ignore policy and a valid selection pin")
        for key in ("starting_snapshot", "final_snapshot"):
            observed = step.get(key)
            self.check(revision_evidence(observed) and observed["tree"] == expected["tree"]
                       and observed["content_sha256"] == expected["content_sha256"],
                       "read-only review " + key + " covered its exact expected revision")
            if self.args.storage == "private":
                self.check(private_pins(observed, ignore=report["snapshot"]["ignore_sha256"],
                                        selection=expected["selection_sha256"]),
                           "read-only review " + key + " retained its expected ignore and selection pins")

    def assert_guided_summary(self, summary, report):
        self.check(isinstance(summary, dict) and all(summary.get(key) == report.get(key) for key in
                   ("id", "goal", "state", "repository", "started_at", "finished_at"))
                   and summary.get("directory") == report["output"], "guided inspection identifies the exact retained completed report")
        phases = [{key: row.get(key, "") for key in ("id", "state", "harness")} for row in report["steps"]]
        self.check(isinstance(summary.get("phases"), list)
                   and [{key: row.get(key, "") for key in ("id", "state", "harness")} for row in summary["phases"]] == phases,
                   "guided inspection retained every recorded phase and outcome")
        checks = report["verification"]["checks"]
        self.check(exact_int(summary.get("checks_passed"), len(checks)) and exact_int(summary.get("checks_total"), len(checks))
                   and summary.get("source_fresh") is True and summary.get("result_fresh") is True
                   and summary.get("can_apply") is True and summary.get("review_state") == "approve"
                   and isinstance(report.get("patch"), str) and bool(report["patch"])
                   and summary.get("patch_path") == report["patch"]
                   and isinstance(summary.get("patch_preview"), str) and bool(summary["patch_preview"]),
                   "guided inspection exposes the retained checks, approved review, fresh source/result and applicable patch")

    def assert_history(self, runs):
        expected = [{"id": report["id"], "goal": report["goal"], "state": report["state"], "directory": report["output"],
                     "started_at": report["started_at"], "finished_at": report["finished_at"]} for report in reversed(self.guided_reports)]
        self.check(runs == expected, "project history returns exactly the expected retained records in newest-first order")

    def assert_applied_source(self, before, after, worktree, changed):
        expected = dict(before, files=dict(before["files"]))
        for name in changed:
            path = worktree / name
            expected["files"][name] = {"sha256": hashlib.sha256(path.read_bytes()).hexdigest(),
                                       "mode": stat.S_IMODE(path.lstat().st_mode)}
        self.check(after == expected, "application produced exactly the retained fixture files while preserving every owner file, mode, HEAD, index and observed Git metadata")

    def assert_no_processes(self, item, enforced=True):
        until = time.monotonic() + 3
        while any(identity_exists(identity) for identity in item["identities"].values()) and time.monotonic() < until:
            self.reap_orphans(item)
            time.sleep(0.02)
        self.reap_orphans(item)
        survivors = [identity for identity in item["identities"].values() if identity_exists(identity)]
        if enforced:
            self.check(not survivors, "observed fixture descendants stopped within the required lifetime boundary")
        else:
            self.current["weaker_policy_survivors"] = survivors
            self.summary["weaker_policy_processes_cleaned"] += stop_identities(item["tree"].descendants())
        self.save()

    def reap_orphans(self, item):
        controllers = {active["process"].pid for active in self.active}
        for identity in item["identities"].values():
            current = process_identity(identity["pid"])
            if current is None or current["start_ticks"] != identity["start_ticks"] or current["parent_pid"] != os.getpid() or current["pid"] in controllers:
                continue
            if current["state"] == "Z":
                try:
                    reaped, _ = os.waitpid(current["pid"], os.WNOHANG)
                except ChildProcessError:
                    continue
                if reaped:
                    self.summary["adopted_fixture_processes_reaped"] += 1

    def case_guided_history(self, root):
        project_root = self.root / "guided"
        if not project_root.exists():
            project_root.mkdir(mode=0o700)
            repository = self.project(project_root)
        else:
            repository = project_root / "project"
        before = self.source(repository)
        value = str(self.iteration + 1)
        checks = [self.python, "-B", "-c", "from pathlib import Path; assert Path('answer.txt').read_text()==" + repr(value + "\n")]
        options = ["--repo", repository, "--catalog", self.root / "history", "--output", root / "run", "--storage", self.args.storage,
                   "--containment", self.args.containment, "--attempts", "1", "--timeout", "90s", "--json"]
        if self.args.isolation == "bubblewrap":
            options.extend(["--isolation", "bubblewrap", "--network", "none", "--read-only-path", self.fixture_directory, "--reviewer", "claude"])
        result = self.call("guided-run", "work", "SOAK_VALUE:" + value, *options, "--allow-models", "--", *checks)
        self.assert_report(result, repository, root / "run", "mission", "completed", "SOAK_VALUE:" + value)
        steps = result.get("steps")
        self.check(isinstance(steps, list) and [step.get("id") for step in steps] == ["implement", "review", "address-review", "final-review", "final-check"],
                   "guided work retained every required coding, review and verification phase")
        declared = [{"name": "project-check", "command": checks}]
        current = result["snapshot"]
        for step in steps:
            reviewer = step["id"] in ("review", "final-review")
            harness = ("claude" if self.args.isolation == "bubblewrap" else "antigravity") if reviewer else "codex"
            verifier_only = step["id"] == "final-check"
            self.check(step.get("harness", "") == ("" if verifier_only else harness)
                       and exact_int(step.get("attempts"), 0 if verifier_only else 1)
                       and step.get("state") == ("completed" if reviewer else "verified") and empty_error(step.get("error")),
                       "guided phase " + step["id"] + " executed its expected bounded fixture successfully")
            self.check(step.get("read_only", False) is reviewer
                       and step.get("gate_review", False) is (step["id"] == "final-review"),
                       "guided phase retained its exact planned read-only and gate roles")
            if not verifier_only:
                self.assert_same_revision(step.get("starting_snapshot"), current)
            else:
                for key in ("starting_snapshot", "final_snapshot"):
                    if key in step:
                        self.assert_same_revision(step[key], current)
            if reviewer:
                gate = step["id"] == "final-review"
                self.assert_review(step, result, current, gate=gate, decision="approve")
            else:
                self.assert_verification(step.get("verification"), declared, Path(result["worktree"]), snapshot=result["snapshot"])
                if not verifier_only:
                    self.assert_final_snapshot(step.get("final_snapshot"), step["verification"], result["snapshot"])
                    current = step["final_snapshot"]
                self.assert_same_revision(step["verification"], current)
        self.assert_verification(result.get("verification"), declared, Path(result["worktree"]), snapshot=result["snapshot"])
        self.assert_final_snapshot(result.get("final_snapshot"), result["verification"], result["snapshot"])
        self.assert_same_revision(result["final_snapshot"], current)
        self.assert_same_revision(result["verification"], current)
        self.check((Path(result["worktree"]) / "answer.txt").read_bytes() == (value + "\n").encode()
                   and (Path(result["worktree"]) / "provider-turns.txt").read_text() == str(2 * (self.iteration + 1)),
                   "guided result retained the exact requested answer and bounded fixture turn count")
        self.guided_reports.append(result)
        self.check(self.source(repository) == before, "guided work preserved source and staging before explicit application")
        for operation in ("status", "show", "list"):
            inspected = self.call("guided-" + operation, "work", operation, "--repo", repository, "--catalog", self.root / "history", "--json")
            if operation == "list":
                self.assert_history(inspected)
            else:
                self.assert_guided_summary(inspected, result)
            self.check(read_json(root / "run/report.json") == result and self.source(repository) == before,
                       "guided " + operation + " inspection preserved retained evidence, source and staging")
        applied = self.call("guided-apply", "work", "apply", "latest", "--repo", repository, "--catalog", self.root / "history", "--json")
        self.check(applied.get("applied") is True and false_flag(applied, "uncertain"), "guided result applied with confirmed evidence")
        after = self.source(repository)
        self.assert_applied_source(before, after, Path(result["worktree"]), ("answer.txt", "provider-turns.txt"))
        self.check(before["head"] == after["head"] and before["index_sha256"] == after["index_sha256"], "guided application preserved HEAD and staged owner work")
        if self.args.storage == "private":
            self.check(before["git_metadata_sha256"] == after["git_metadata_sha256"], "private guided application left source Git objects, refs and worktree metadata unchanged")
        self.check((repository / "answer.txt").read_text() == value + "\n", "guided application retained the exact requested answer")
        self.check((repository / "owner.txt").read_text() == "unstaged owner work\n" and (repository / "owner.bin").read_bytes() == bytes([0, 1, 2, 255]), "guided application preserved unrelated owner bytes")
        self.check(int((repository / "provider-turns.txt").read_text()) == 2 * (self.iteration + 1), "guided coding attempts were bounded without hidden replay")
        runs = self.call("growing-history", "work", "list", "--repo", repository, "--catalog", self.root / "history", "--json")
        self.assert_history(runs)

    def case_check_failure(self, root):
        repository = self.project(root)
        before = self.source(repository)
        checks = [{"name": "intentional-negative-control", "command": [self.python, "-c", "raise SystemExit(7)"], "timeout_seconds": 5}]
        result = self.run_plan(repository, self.plan(repository, checks=checks), expected=1)
        candidate = result["candidates"][0]
        self.check(exact_int(candidate.get("attempts"), 1) and candidate.get("state") == "checks_failed", "negative control reached exactly one completed coding attempt")
        verification = candidate["verification"]["checks"][0]
        fingerprints = candidate["verification"]
        self.check(revision_evidence(fingerprints) and object_id(fingerprints.get("ending_tree"))
                   and fingerprints["tree"] == fingerprints["ending_tree"]
                   and digest_id(fingerprints.get("ending_content_sha256"))
                   and fingerprints["content_sha256"] == fingerprints["ending_content_sha256"],
                   "negative checks retained valid unchanged revision digests")
        if self.args.storage == "private":
            self.check(private_pins(fingerprints, ignore=result["snapshot"]["ignore_sha256"]),
                       "private negative checks retained valid selection and expected ignore digests")
        self.check(candidate["verification"].get("passed") is False
                   and candidate["verification"].get("repository") == candidate["worktree"]
                   and len(candidate["verification"]["checks"]) == 1
                   and verification.get("name") == checks[0]["name"] and verification.get("command") == checks[0]["command"]
                   and verification.get("passed") is False and exact_int(verification.get("exit_code"), 7),
                   "actual exact declared negative check ran and rejected the candidate")
        self.check((Path(candidate["worktree"]) / "answer.txt").read_bytes() == b"42\n"
                   and (Path(candidate["worktree"]) / "turn-count.txt").read_bytes() == b"1",
                   "failed-check fixture retained its actual single coding attempt and exact answer")
        self.check(self.source(repository) == before, "failed checks preserved source and staging")

    def case_review_rejection(self, root):
        repository = self.project(root)
        before = self.source(repository)
        plan = self.plan(repository)
        plan.update(kind="work", mode="mission")
        verdict = {"decision": "request_changes", "summary": "Deterministic rejection fixture.", "findings": [{"file": "answer.txt", "line": 1, "message": "Deliberate review gate negative control."}]}
        plan["candidates"].append({"id": "reviewer", "harness": "command", "command": [self.python, "-c", "print(" + repr(json.dumps(verdict)) + ")"]})
        plan["steps"] = [{"id": "implement", "candidate": "writer", "verify": True}, {"id": "final-review", "candidate": "reviewer", "depends_on": ["implement"], "read_only": True, "review": True, "gate_review": True, "require_result": True}, {"id": "accept", "verify": True, "depends_on": ["final-review"]}]
        result = self.run_plan(repository, plan, expected=1)
        steps = result.get("steps")
        self.check(isinstance(steps, list) and [step.get("id") for step in steps] == ["implement", "final-review"],
                   "rejected mission retained the implementation and rejecting review without executing acceptance")
        implementation, review = steps
        self.assert_same_revision(implementation.get("starting_snapshot"), result["snapshot"])
        self.check(implementation.get("read_only", False) is False and implementation.get("gate_review", False) is False,
                   "rejected mission implementation retained its planned writing role")
        self.check(implementation.get("state") == "verified" and implementation.get("harness") == "command" and exact_int(implementation.get("attempts"), 1)
                   and empty_error(implementation.get("error")),
                   "rejected mission executed its exact single implementation fixture")
        self.assert_verification(implementation.get("verification"), plan["checks"], root / "run/worktree", snapshot=result["snapshot"])
        self.assert_final_snapshot(implementation.get("final_snapshot"), implementation["verification"], result["snapshot"])
        self.assert_review(review, result, implementation.get("final_snapshot"), gate=True, decision="request_changes")
        self.check(review.get("state") == "changes_requested" and review.get("harness") == "command" and exact_int(review.get("attempts"), 1)
                   and same_json(review.get("review"), verdict), "structured final review fixture returned its exact rejection and stopped acceptance")
        rejected = self.call("rejected-apply", "flow", "apply", root / "run", expected=1)
        self.check(rejected.get("applied") is False and self.source(repository) == before, "rejected review could not mutate source")

    def stale(self, root, candidate):
        repository = self.project(root)
        result = self.run_plan(repository, self.plan(repository))
        if candidate:
            Path(result["candidates"][0]["worktree"], "answer.txt").write_text("tampered\n")
        else:
            (repository / "owner.txt").write_text("new owner changes after acceptance\n")
        before = self.source(repository)
        item = self.launch("stale-apply", ["flow", "apply", root / "run"])
        rejected = self.finish(item, expected=1)
        error = read_json(Path(item["row"]["stdout"])).get("error", "")
        self.check(rejected.get("applied") is False and isinstance(error, str) and "changed since" in error, "stale evidence was rejected for its exact freshness failure")
        self.check(self.source(repository) == before, "stale application preserved current source and staging")

    def case_stale_source(self, root):
        self.stale(root, False)

    def case_stale_result(self, root):
        self.stale(root, True)

    def slow_turn(self, root, crash):
        repository = self.project(root)
        before = self.source(repository)
        escaped = self.args.containment == "pid-namespace" or self.args.isolation == "bubblewrap"
        code = "from pathlib import Path; import subprocess,sys,time; Path('turn-count.txt').write_text('1'); subprocess.Popen([sys.executable,'-c',\"import time; time.sleep(120)\"],start_new_session=" + repr(escaped) + "); Path('started').write_text('ready'); time.sleep(120)"
        plan = self.plan(repository, code)
        private_json(root / "plan.json", plan)
        item = self.launch("crash-turn" if crash else "cancel-turn", ["flow", "run", root / "plan.json", "--output", root / "run"])
        marker = root / "run/candidates/writer/worktree/started"
        self.wait_ready(item, marker)
        item["tree"].root.send_signal(signal.SIGKILL if crash else signal.SIGTERM)
        self.finish(item, expected=-signal.SIGKILL if crash else 1, envelope=not crash)
        self.assert_no_processes(item, enforced=not crash or escaped)
        stored = read_json(root / "run/report.json")
        self.check(stored.get("state") == ("running" if crash else "interrupted"), "interrupted work remained honestly recorded without invented completion")
        worktree = Path(stored["candidates"][0]["worktree"])
        counter = (worktree / "turn-count.txt").read_bytes()
        inspected = self.call("interrupted-status", "flow", "status", root / "run")
        self.check(inspected == stored and read_json(root / "run/report.json") == stored,
                   "interrupted status returned the exact retained report without changing its recorded state")
        self.check(counter == b"1" and (worktree / "turn-count.txt").read_bytes() == counter, "inspection never replayed an interrupted coding turn")
        self.check(self.source(repository) == before, "interrupted coding preserved source and staging")

    def case_cancel_turn(self, root):
        self.slow_turn(root, False)

    def case_crash_turn(self, root):
        self.slow_turn(root, True)

    def git_fault(self, root, repository, after=False):
        directory = root / "git-fault"
        directory.mkdir(mode=0o700)
        ready, release, counter, replay = (repository / name for name in FAULT_MARKER_NAMES)
        code = ("#!" + self.python + "\nimport os,sys,time\nfrom pathlib import Path\na=sys.argv[1:]\n"
                "selected='apply' in a and '--check' not in a and '-C' in a and a[a.index('-C')+1]==" + repr(str(repository)) + "\n"
                "if selected:\n p=Path(" + repr(str(counter)) + "); n=int(p.read_text())+1 if p.exists() else 1; p.write_text(str(n))\n"
                " if n>1: Path(" + repr(str(replay)) + ").write_text('replayed'); sys.exit(86)\n")
        if after:
            code += " import subprocess\n subprocess.run([" + repr(self.git_binary) + "]+a,check=True)\n"
        code += " Path(" + repr(str(ready)) + ").write_text('ready')\n until=time.monotonic()+30\n while not Path(" + repr(str(release)) + ").exists():\n  if time.monotonic()>until: sys.exit(87)\n  time.sleep(0.02)\n"
        if after:
            code += " sys.exit(0)\n"
        code += "os.execv(" + repr(self.git_binary) + ",[" + repr(self.git_binary) + "]+a)\n"
        path = directory / "git"
        path.write_text(code)
        path.chmod(0o700)
        environment = dict(self.environment, PATH=str(directory) + os.pathsep + self.environment["PATH"])
        return environment, ready, release, counter, replay

    def apply_fault(self, root, after=False, concurrent=False):
        repository = self.project(root)
        before = self.source(repository)
        result = self.run_plan(repository, self.plan(repository))
        environment, ready, release, counter, replay = self.git_fault(root, repository, after)
        item = self.launch("admitted-apply", ["flow", "apply", root / "run"], environment)
        self.wait_ready(item, ready)
        if concurrent:
            other = self.launch("concurrent-apply", ["flow", "apply", root / "run"], environment)
            rejected = self.finish(other, expected=1)
            error = read_json(Path(other["row"]["stdout"])).get("error", "")
            self.check(rejected.get("applied") is False and isinstance(error, str) and "active" in error, "concurrent application was refused while the first writer held its lease")
            release.write_text("release")
            applied = self.finish(item)
            self.check(applied.get("applied") is True and false_flag(applied, "uncertain"), "first concurrent application completed exactly once")
        else:
            item["tree"].root.send_signal(signal.SIGKILL)
            self.finish(item, expected=-signal.SIGKILL, envelope=False)
            if self.args.containment == "process-group" and self.args.isolation == "none":
                busy = self.launch("recover-while-writer-active", ["flow", "recover", root / "run"], environment)
                self.finish(busy, expected=1)
                self.check("active" in read_json(Path(busy["row"]["stdout"])).get("error", ""), "surviving writer retained the application lease after controller death")
            if self.args.containment == "pid-namespace" or self.args.isolation == "bubblewrap":
                self.assert_no_processes(item)
            else:
                self.summary["weaker_policy_processes_cleaned"] += stop_identities(item["tree"].descendants())
                self.assert_no_processes(item)
            recovery = None
            until = min(self.deadline, time.monotonic() + 5)
            while time.monotonic() < until:
                recovery_item = self.launch("recover-crashed-apply", ["flow", "recover", root / "run"], environment)
                while recovery_item["process"].poll() is None:
                    self.check_budget()
                    self.observe(recovery_item)
                    time.sleep(0.02)
                code = recovery_item["process"].returncode
                require(code in (0, 1), "recovery returned an unexpected CLI exit class")
                recovery = self.finish(recovery_item, expected=code)
                if code == 0:
                    break
                require("active" in read_json(Path(recovery_item["row"]["stdout"])).get("error", ""), "recovery failed for an unexpected reason")
                time.sleep(0.03)
            self.check(isinstance(recovery, dict) and recovery.get("state") == ("applied" if after else "not_applied"), "explicit recovery classified the exact pre/post-application source")
        self.check(counter.read_text() == "1" and not replay.exists(), "application and recovery never replayed the source-writing operation")
        source = self.source(repository)
        self.check(before["head"] == source["head"] and before["index_sha256"] == source["index_sha256"], "faulted application preserved HEAD and staging")
        if self.args.storage == "private":
            self.check(before["git_metadata_sha256"] == source["git_metadata_sha256"], "private application faults left source Git objects, refs and worktree metadata unchanged")
        self.check((repository / "owner.txt").read_text() == "unstaged owner work\n", "faulted application preserved unrelated owner content")
        if concurrent or after:
            self.assert_applied_source(before, source, Path(result["candidates"][0]["worktree"]), ("answer.txt", "turn-count.txt"))
            self.check((repository / "answer.txt").read_bytes() == b"42\n", "post-application source retained the checked exact answer")
        else:
            self.check(self.source(repository) == before, "pre-application crash left source unchanged")

    def case_crash_apply_before(self, root):
        self.apply_fault(root)

    def case_crash_apply_after(self, root):
        self.apply_fault(root, after=True)

    def case_concurrent_apply(self, root):
        self.apply_fault(root, concurrent=True)

    def case_isolation_boundary(self, root):
        if self.args.isolation != "bubblewrap":
            self.current["coverage_note"] = "Filesystem/network isolation was not requested; this case only records that explicit coverage boundary."
            self.current["state"] = "not_applicable"
            if "isolation_boundary" not in self.summary["coverage_exclusions"]:
                self.summary["coverage_exclusions"].append("isolation_boundary")
            return
        repository = self.project(root)
        before = self.source(repository)
        secret, outside_write = root / "host-secret", root / "outside-write"
        secret.write_text("SOAK_PRIVATE_" + uuid.uuid4().hex)
        original = secret.read_bytes()
        with socket.socket() as listener:
            listener.bind(("127.0.0.1", 0))
            listener.listen(1)
            port = listener.getsockname()[1]
            code = "from pathlib import Path; import socket\n"
            code += "try:\n Path(" + repr(str(secret)) + ").read_bytes()\nexcept OSError: pass\nelse: raise AssertionError('host secret was exposed')\n"
            # /tmp is intentionally private and writable. An absolute write can
            # succeed there without reaching the host; inspect the host below.
            code += "try: Path(" + repr(str(outside_write)) + ").write_text('namespace-private write')\nexcept OSError: pass\n"
            code += "try: socket.create_connection(('127.0.0.1'," + str(port) + "),timeout=1)\nexcept OSError: pass\nelse: raise AssertionError('host network escape')\nPath('answer.txt').write_text('42\\n')\n"
            proof = {"version": 1, "fixture": "isolation_boundary", "nonce": uuid.uuid4().hex,
                     "assertions": ["host-secret-unreadable", "outside-write-attempted", "host-network-unreachable"]}
            proof_name = ".agentmux-soak-isolation-proof.json"
            # Publish proof last: a no-op CLI, an unexecuted candidate or an
            # early failure cannot satisfy the retained execution oracle. This
            # checks omitted execution, not a malicious binary forging records.
            code += "Path('turn-count.txt').write_text('1')\nimport json\nPath(" + repr(proof_name) + ").write_text(json.dumps(" + repr(proof) + "))\n"
            check_code = "import json; from pathlib import Path; assert Path('answer.txt').read_bytes()==b'42\\n'; assert json.loads(Path(" + repr(proof_name) + ").read_text())==" + repr(proof)
            checks = [{"name": "exact-answer-and-isolation-proof", "command": [self.python, "-B", "-c", check_code], "timeout_seconds": 5}]
            plan = self.plan(repository, code, checks=checks)
            self.run_plan(repository, plan)
            worktree = root / "run/candidates/writer/worktree"
            self.check(same_json(read_json(worktree / proof_name, maximum=16384), proof),
                       "isolated candidate retained its exact post-assertion fixture proof")
            listener.settimeout(0.1)
            try:
                connection, _ = listener.accept()
                connection.close()
            except TimeoutError:
                pass
            else:
                raise SoakFailure("host listener received an isolated connection")
        self.check(secret.read_bytes() == original and not outside_write.exists(), "isolated candidate preserved host secrets and refused outside writes")
        self.check(self.source(repository) == before, "isolated run preserved original source and staging")

    def cleanup(self, expect_stopped=False):
        unexpected, errors = [], []
        if expect_stopped:
            until = time.monotonic() + 1
            while any(item["process"].poll() is None or any(same_process(identity) for identity in item["identities"].values()) for item in self.active) and time.monotonic() < until:
                time.sleep(0.02)
            for item in self.active:
                if item["process"].poll() is None or any(same_process(identity) for identity in item["identities"].values()):
                    unexpected.append(item["row"]["label"])
        for item in reversed(self.active):
            tree = item["tree"]
            if item["process"].poll() is None:
                try:
                    self.observe(item)
                except BaseException as error:
                    errors.append(type(error).__name__)
                try:
                    tree.root.send_signal(signal.SIGTERM)
                except BaseException as error:
                    errors.append(type(error).__name__)
                until = time.monotonic() + 2
                while item["process"].poll() is None and time.monotonic() < until:
                    try:
                        self.observe(item)
                    except BaseException as error:
                        errors.append(type(error).__name__)
                    time.sleep(.02)
                if item["process"].poll() is None:
                    try:
                        tree.root.send_signal(signal.SIGKILL)
                    except BaseException as error:
                        errors.append(type(error).__name__)
                    try:
                        item["process"].wait(timeout=2)
                    except BaseException as error:
                        errors.append(type(error).__name__)
            try:
                stop_identities(tree.descendants())
            except BaseException as error:
                errors.append(type(error).__name__)
            until = time.monotonic() + 2
            while any(identity_exists(identity) for identity in item["identities"].values()) and time.monotonic() < until:
                self.reap_orphans(item)
                time.sleep(0.01)
            self.reap_orphans(item)
            for stream in (item["stdout"], item["stderr"]):
                if not stream.closed:
                    try:
                        stream.close()
                    except BaseException as error:
                        errors.append(type(error).__name__)
            item["row"]["observed_processes"] = list(item["identities"].values())
            if item["row"]["state"] == "running":
                item["row"].update(state="stopped-during-cleanup", exit_code=item["process"].returncode, finished_at=stamp())
            if any(identity_exists(identity) for identity in item["identities"].values()):
                errors.append("observed fixture process or zombie survived final cleanup")
            elif item["process"].returncode is not None:
                tree.close()
        if unexpected:
            errors.append("unexpected surviving command/descendant after completion: " + ", ".join(unexpected))
        require(not errors, "admitted cleanup observation/signaling failed: " + "; ".join(errors))

    def note_failure(self, error, field="error"):
        # Error paths must remain bounded and must not depend on a diagnostic's
        # __str__ implementation succeeding. Secondary failures stay visible.
        try:
            detail = type(error).__name__ + ": " + str(error)
        except BaseException:
            detail = type(error).__name__ + ": diagnostic text unavailable"
        previous = self.summary.get(field, "")
        self.summary[field] = (previous + "; " + detail if previous else detail)[:4096]
        interrupted = isinstance(error, (KeyboardInterrupt, SoakInterrupted))
        state = "interrupted" if interrupted and self.summary["state"] != "failed" else "failed"
        self.summary.update(state=state, completed=False)
        if self.current is not None:
            self.current.update(state=state, error=self.summary.get("error", detail))

    def fail(self, error):
        """Revoke success and retain every bounded cleanup/finalization failure.

        Output precedes the last failure write. Even a failed stdout/stderr or a
        case-record write must not prevent a direct nonpassing summary attempt.
        Recovery has a fixed number of writes; a broken evidence filesystem is
        reported in memory without an unbounded retry or another exception.
        """
        self.note_failure(error, "finalization_error" if "error" in self.summary else "error")
        try:
            self.cleanup()
        except BaseException as cleanup_error:
            self.note_failure(cleanup_error, "cleanup_error")
        try:
            self.summary["finished_at"] = stamp()
            if self.current is not None:
                self.current["finished_at"] = self.summary["finished_at"]
        except BaseException as timestamp_error:
            self.note_failure(timestamp_error, "finalization_error")
        for stream, message in ((sys.stderr, "Soak " + self.summary["state"] + ": " + self.summary["error"]),
                                (sys.stdout, "Evidence: " + str(self.root / "soak.json"))):
            try:
                print(message, file=stream, flush=True)
            except BaseException as output_error:
                self.note_failure(output_error, "output_error")
        try:
            self.save(index=True)
        except BaseException as evidence_error:
            self.note_failure(evidence_error, "evidence_error")
            try:
                private_json(self.root / "soak.json", self.summary)
            except BaseException as summary_error:
                self.note_failure(summary_error, "evidence_error")
        return 1

    def run(self):
        randomizer = random.Random(self.args.seed)
        scenarios = list(self.summary["scenarios"])
        try:
            for iteration in range(self.args.iterations):
                self.iteration = iteration
                randomizer.shuffle(scenarios)
                for name in scenarios:
                    self.check_budget()
                    root = self.root / (str(iteration + 1).zfill(4) + "-" + name)
                    root.mkdir(mode=0o700)
                    self.current = {"iteration": iteration + 1, "scenario": name, "directory": str(root), "state": "running", "started_at": stamp(), "commands": [], "assertions": []}
                    self.current_index = {key: self.current[key] for key in ("iteration", "scenario", "directory", "state", "started_at")}
                    self.summary["cases"].append(self.current_index)
                    self.save(index=True)
                    getattr(self, "case_" + name)(root)
                    self.check_budget()
                    self.cleanup(expect_stopped=True)
                    self.check_budget()
                    self.active.clear()
                    self.check(hashlib.sha256(self.binary.read_bytes()).hexdigest() == self.binary_sha256, "frozen validation executable remained unchanged")
                    self.check(all(hashlib.sha256(path.read_bytes()).hexdigest() == self.harness_hashes[name] for name, path in self.harness_paths.items()),
                               "all recorded soak harness sources remained unchanged throughout collection")
                    size = artifact_bytes(self.root)
                    self.check(size <= self.args.max_artifact_bytes - (64 << 10), "retained soak artifacts remained within the configured byte budget and final-record reserve")
                    state = "not_applicable" if self.current["state"] == "not_applicable" else "passed"
                    self.current.update(state=state, finished_at=stamp(), artifact_bytes=size)
                    self.save(index=True)
                    self.check_budget()
                    print(str(iteration + 1) + "/" + str(self.args.iterations) + " " + name + ": " + state.upper(), flush=True)
                    self.check_budget()
                self.summary["iterations_completed"] = iteration + 1
            # Keep durable evidence nonpassing through all required output and
            # finalization. The success write is the final required I/O, and a
            # signal or exception during/just after that write revokes it.
            self.check_budget()
            self.summary.update(state="finalizing", finished_at=stamp())
            self.save(index=True)
            self.check_budget()
            print("Evidence: " + str(self.root / "soak.json"), flush=True)
            self.check_budget()
            # main() restores required handlers and commits success while
            # termination signals remain blocked and pending ones are checked.
            return 0
        except BaseException as error:
            return self.fail(error)


def parse_args(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True, help="new private evidence directory with an existing parent")
    parser.add_argument("--iterations", type=int, default=10)
    parser.add_argument("--duration-seconds", type=int, default=1800)
    parser.add_argument("--seed", type=int, default=20261102)
    parser.add_argument("--containment", choices=("process-group", "pid-namespace"), default="pid-namespace")
    parser.add_argument("--storage", choices=("private", "shared"), default="private")
    parser.add_argument("--isolation", choices=("none", "bubblewrap"), default="none")
    parser.add_argument("--scenario", action="append", choices=SCENARIOS, help="explicit coverage subset; default exercises every scenario each iteration")
    parser.add_argument("--max-artifact-bytes", type=int, default=2 << 30, help="quiescent evidence bound checked between cases, not an OS disk quota")
    args = parser.parse_args(argv)
    try:
        PROCESS.require_process_support()
    except RuntimeError as error:
        parser.error(str(error))
    if not 1 <= args.iterations <= 1000 or not 1 <= args.duration_seconds <= 86400:
        parser.error("iterations must be 1..1000 and duration-seconds 1..86400")
    if not 64 << 20 <= args.max_artifact_bytes <= 16 << 30:
        parser.error("max-artifact-bytes must be 64 MiB..16 GiB")
    if args.scenario and len(args.scenario) != len(set(args.scenario)):
        parser.error("each explicit scenario may be selected only once")
    if args.isolation == "bubblewrap" and args.storage != "private":
        parser.error("full isolation soak requires private snapshot storage")
    args.binary = args.binary.resolve(strict=True)
    information = args.binary.stat()
    if not stat.S_ISREG(information.st_mode) or not os.access(args.binary, os.X_OK) or information.st_size > 256 << 20:
        parser.error("binary must be an executable regular file of at most 256 MiB")
    if information.st_size > args.max_artifact_bytes - (256 << 10):
        parser.error("artifact budget cannot admit the frozen binary and initial evidence")
    parent = args.output.absolute().parent.resolve(strict=True)
    args.output = parent / args.output.name
    if args.output.exists() or args.output.is_symlink():
        parser.error("output must not already exist")
    return args


def main(argv=None):
    args = parse_args(argv)
    args.output.mkdir(mode=0o700)
    bootstrap = {"version": 1, "state": "initializing", "completed": False, "started_at": stamp(), "model_calls": 0}
    private_json(args.output / "soak.json", bootstrap)
    try:
        runner = Soak(args)
    except BaseException as error:
        bootstrap.update(state="interrupted" if isinstance(error, KeyboardInterrupt) else "failed",
                         finished_at=stamp(), error=type(error).__name__ + ": " + str(error)[:4096])
        private_json(args.output / "soak.json", bootstrap)
        print("Soak initialization failed: " + bootstrap["error"], file=sys.stderr)
        return 1
    previous = {}
    status = 1
    managed = {signal.SIGINT, signal.SIGTERM}
    final_mask = None
    try:
        for number in (signal.SIGINT, signal.SIGTERM):
            previous[number] = signal.signal(number, lambda signum, _frame: setattr(runner, "cancelled", signum))
        status = runner.run()
    except BaseException as error:
        status = runner.fail(error)
    finally:
        # Handler restoration is required work. Keep durable evidence
        # nonpassing until it finishes, then publish under the same signal mask.
        # Once the final write and pending-signal check finish, the evidence
        # transaction is committed; later process death does not revoke it.
        try:
            final_mask = signal.pthread_sigmask(signal.SIG_BLOCK, managed)
            for number, handler in previous.items():
                signal.signal(number, handler)
            for number in PROCESS.consume_pending_signals(managed, final_mask):
                runner.cancelled = number
            if status == 0:
                runner.check_budget()
                runner.summary.update(state="passed", completed=True)
                runner.save(index=True)
                for number in PROCESS.consume_pending_signals(managed, final_mask):
                    runner.cancelled = number
                runner.check_budget()
        except BaseException as error:
            status = runner.fail(error)
        finally:
            if final_mask is not None:
                for number in PROCESS.consume_pending_signals(managed, final_mask):
                    runner.cancelled = number
                # A pending signal discovered during failure compensation
                # cannot interrupt its already-written nonpassing record.
                signal.pthread_sigmask(signal.SIG_SETMASK, final_mask)
    return status


if __name__ == "__main__":
    raise SystemExit(main())
