#!/usr/bin/env python3
"""Retain matched local workflow attempts; fixtures are not an AI benchmark.

Commands are owner-trusted local executables, not sandboxed by this runner.
Linux pidfds bound cleanup to observed process identities. Detached/unobserved
host work, provider billing and independent host writers remain outside it.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import re
import signal
import stat
import string
import subprocess
import sys
import time

import workflow_processes as processes


MAX_FILES = 10000
MAX_BYTES = 128 * 1024 * 1024
CANCELLED = False


def integer(value, name, low, high):
    if type(value) is not int or not low <= value <= high:
        raise ValueError(f"{name} must be an integer in {low}..{high}")
    return value


def identifier(value):
    if not isinstance(value, str) or not re.fullmatch(r"[a-z][a-z0-9-]{0,47}", value):
        raise ValueError("IDs must match [a-z][a-z0-9-]{0,47}")
    return value


def command(value):
    if not isinstance(value, list) or not value or len(value) > 128:
        raise ValueError("argv must be a nonempty list of at most 128 strings")
    for argument in value:
        if not isinstance(argument, str) or not argument or "\0" in argument or len(argument) > 65536:
            raise ValueError("invalid argv argument")
        for _, field, spec, conversion in string.Formatter().parse(argument):
            if field is not None and (field not in {"repo", "task", "output"} or spec or conversion):
                raise ValueError("only {repo}, {task}, {output} substitutions are accepted")
    return value


def validate(config, base):
    if not isinstance(config, dict) or config.get("version") != 1 or type(config.get("version")) is not int:
        raise ValueError("configuration version must be 1")
    if config.get("mode") not in {"fixture", "live"}:
        raise ValueError("mode must be fixture or live")
    source = config.get("source", {})
    if not isinstance(source, dict) or not isinstance(source.get("repository"), str):
        raise ValueError("source.repository is required")
    source["repository"] = str((base / source["repository"]).resolve(strict=True))
    if not re.fullmatch(r"[0-9a-f]{40}|[0-9a-f]{64}", str(source.get("commit", ""))):
        raise ValueError("source.commit must be a full lowercase commit object ID")
    config["repeats"] = integer(config.get("repeats", 2), "repeats", 1, 20)
    config["total_timeout_seconds"] = integer(config.get("total_timeout_seconds", 3600), "total timeout", 1, 86400)
    for group, maximum in (("tasks", 20), ("arms", 8)):
        items = config.get(group)
        if not isinstance(items, list) or not 1 <= len(items) <= maximum or (group == "arms" and len(items) < 2):
            raise ValueError(f"{group} must contain {'2' if group == 'arms' else '1'}..{maximum} items")
        ids = []
        for item in items:
            if not isinstance(item, dict):
                raise ValueError(f"invalid {group} entry")
            ids.append(identifier(item.get("id")))
            if group == "arms":
                command(item.get("argv"))
                integer(item.get("timeout_seconds"), "arm timeout", 1, 3600)
            else:
                if not isinstance(item.get("prompt"), str) or not item["prompt"].strip() or len(item["prompt"]) > 65536:
                    raise ValueError("each task requires a nonempty prompt")
                if "development_check" in item:
                    command(item["development_check"])
                checks = item.get("checks")
                if not isinstance(checks, list) or not 1 <= len(checks) <= 20:
                    raise ValueError("each task requires 1..20 independent checks")
                names = []
                for check in checks:
                    if not isinstance(check, dict):
                        raise ValueError("invalid check")
                    names.append(identifier(check.get("id")))
                    command(check.get("argv"))
                    integer(check.get("timeout_seconds"), "check timeout", 1, 600)
                    protected = check.get("evaluator_files")
                    if not isinstance(protected, list) or not 1 <= len(protected) <= 20 or not all(isinstance(path, str) and Path(path).is_absolute() for path in protected):
                        raise ValueError("each independent check requires absolute external evaluator_files")
                if len(set(names)) != len(names):
                    raise ValueError("check IDs must be unique within a task")
        if len(set(ids)) != len(ids):
            raise ValueError(f"duplicate {group} IDs")
    if config["repeats"] * len(config["arms"]) * len(config["tasks"]) > 200:
        raise ValueError("at most 200 attempts are allowed")
    return config


def git_environment():
    env = {key: value for key, value in os.environ.items() if not key.startswith("GIT_")}
    env.update(GIT_CONFIG_NOSYSTEM="1", GIT_CONFIG_GLOBAL=os.devnull,
               GIT_TERMINAL_PROMPT="0", GIT_OPTIONAL_LOCKS="0", GIT_NO_LAZY_FETCH="1",
               GIT_NO_REPLACE_OBJECTS="1", GIT_GRAFT_FILE=os.devnull, GIT_ALLOW_PROTOCOL="",
               GIT_AUTHOR_NAME="Workflow evaluation", GIT_AUTHOR_EMAIL="evaluation@localhost",
               GIT_COMMITTER_NAME="Workflow evaluation", GIT_COMMITTER_EMAIL="evaluation@localhost",
               GIT_AUTHOR_DATE="2000-01-01T00:00:00Z", GIT_COMMITTER_DATE="2000-01-01T00:00:00Z")
    return env


def remaining_time(deadline):
    if CANCELLED:
        raise InterruptedError("evaluation interrupted")
    remaining = deadline - time.monotonic()
    if remaining <= 0:
        raise TimeoutError("total evaluation deadline exhausted")
    return remaining


def git(repo, *arguments, deadline=None):
    result = subprocess.run(["git", "--no-optional-locks", "-c", "core.hooksPath=/dev/null",
                             "-c", "core.fsmonitor=false", "-c", "maintenance.auto=false",
                             "-c", "gc.auto=0", "-C", str(repo), *arguments],
                            env=git_environment(), stdin=subprocess.DEVNULL,
                            stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                            timeout=30 if deadline is None else min(30, remaining_time(deadline)))
    if result.returncode:
        raise ValueError("Git snapshot operation failed: " + result.stderr.decode(errors="replace")[:1000])
    return result.stdout


def read_snapshot(source, deadline=None):
    repo, commit = Path(source["repository"]), source["commit"]
    for entry in git(repo, "config", "--null", "--list", "--includes", deadline=deadline).split(b"\0"):
        key = entry.partition(b"\n")[0].lower()
        if key == b"extensions.partialclone" or re.fullmatch(rb"remote\..+\.promisor", key):
            raise ValueError("partial/promisor source repositories are unsupported; use a complete local snapshot")
    if git(repo, "cat-file", "-t", commit, deadline=deadline).strip() != b"commit":
        raise ValueError("source pin must name a commit, not a tree or tag")
    files, total, folded = [], 0, set()
    for record in git(repo, "ls-tree", "-r", "-z", "--full-tree", commit, deadline=deadline).split(b"\0"):
        if not record:
            continue
        metadata, raw_path = record.split(b"\t", 1)
        mode, kind, object_id = metadata.decode("ascii").split()
        name = raw_path.decode("utf-8", errors="strict")
        path = PurePosixPath(name)
        if (mode not in {"100644", "100755"} or kind != "blob" or path.is_absolute()
                or any(part in {".", "..", ""} or part.casefold() == ".git" for part in path.parts)
                or "\\" in name or any(ord(char) < 32 or ord(char) == 127 for char in name)
                or str(path) != name or name.casefold() in folded):
            raise ValueError("unsafe source tree: only regular, unambiguous project files are supported")
        folded.add(name.casefold())
        size = int(git(repo, "cat-file", "-s", object_id, deadline=deadline))
        total += size
        if len(files) >= MAX_FILES or total > MAX_BYTES:
            raise ValueError("snapshot exceeds 10000 files or 128 MiB")
        data = git(repo, "cat-file", "blob", object_id, deadline=deadline)
        if len(data) != size:
            raise ValueError("source object size changed")
        files.append((name, mode, data))
    if not files:
        raise ValueError("source commit must contain regular project files")
    # Reject file/directory conflicts even when they differ only by case.
    for name in folded:
        if any(str(parent) in folded for parent in PurePosixPath(name).parents if str(parent) != "."):
            raise ValueError("source has an ambiguous file/directory path")
    digest = hashlib.sha256()
    for name, mode, data in files:
        digest.update(name.encode() + b"\0" + mode.encode() + b"\0" + hashlib.sha256(data).digest())
    return files, digest.hexdigest()


def materialize(repo, files, deadline=None):
    repo.mkdir(mode=0o700)
    for name, mode, data in files:
        if deadline is not None:
            remaining_time(deadline)
        target = repo / name
        target.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
        with target.open("xb") as stream:
            stream.write(data)
        target.chmod(0o700 if mode == "100755" else 0o600)
    git(repo, "-c", "init.templateDir=", "init", "--quiet", deadline=deadline)
    git(repo, "add", "--all", "--force", "--", ".", deadline=deadline)
    git(repo, "-c", "commit.gpgsign=false", "commit", "--quiet", "--no-verify", "-m", "Pinned evaluation snapshot", deadline=deadline)
    return git(repo, "rev-parse", "HEAD", deadline=deadline).decode().strip()


def evaluator_fingerprint(name):
    path = Path(name)
    resolved = path.resolve(strict=True)
    metadata = path.stat()
    if not stat.S_ISREG(metadata.st_mode) or metadata.st_size > 16 * 1024 * 1024:
        raise ValueError("evaluator files must be regular files of at most 16 MiB")
    return {"path": name, "resolved": str(resolved), "sha256": hashlib.sha256(path.read_bytes()).hexdigest()}


def protect_evaluators(config, source, deadline):
    protected = {}
    for task in config["tasks"]:
        for check in task["checks"]:
            pins = []
            for name in check["evaluator_files"]:
                remaining_time(deadline)
                pin = evaluator_fingerprint(name)
                if source == Path(pin["resolved"]) or source in Path(pin["resolved"]).parents:
                    raise ValueError("independent evaluator files must be outside the source repository")
                pins.append(pin)
            protected[(task["id"], check["id"])] = pins
    return protected


def evaluators_intact(pins):
    try:
        return all(evaluator_fingerprint(pin["path"]) == pin for pin in pins)
    except (OSError, ValueError):
        return False


def artifact_fingerprint(repo, deadline):
    """Hash candidate files without following links; graders may not fix them."""
    if not stat.S_ISDIR(repo.lstat().st_mode):
        raise ValueError("candidate repository root must remain a directory, not a symlink")
    entries, pending, total, count = [], [repo], 0, 0
    while pending:
        directory = pending.pop()
        remaining_time(deadline)
        with os.scandir(directory) as children:
            for entry in children:
                remaining_time(deadline)
                if directory == repo and entry.name == ".git":
                    continue
                count += 1
                if count > MAX_FILES:
                    raise ValueError("candidate artifact traversal exceeds 10000 entries")
                path = Path(entry.path)
                name = str(path.relative_to(repo))
                if entry.is_symlink():
                    entries.append((name, "symlink", os.readlink(path)))
                elif entry.is_dir(follow_symlinks=False):
                    pending.append(path)
                else:
                    descriptor = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
                    with os.fdopen(descriptor, "rb") as stream:
                        metadata = os.fstat(stream.fileno())
                        if not stat.S_ISREG(metadata.st_mode):
                            raise ValueError("candidate artifacts must be regular files or symlinks")
                        content = hashlib.sha256()
                        while True:
                            remaining_time(deadline)
                            block = stream.read(1024 * 1024)
                            if not block:
                                break
                            total += len(block)
                            if total > MAX_BYTES:
                                raise ValueError("candidate artifact fingerprint exceeds 128 MiB")
                            content.update(block)
                        entries.append((name, str(metadata.st_mode & 0o111), content.hexdigest()))
    return hashlib.sha256(json.dumps(sorted(entries), ensure_ascii=True).encode()).hexdigest()


def persist(path, value):
    temporary = path.with_name(path.name + ".tmp")
    with temporary.open("w", encoding="utf-8") as stream:
        json.dump(value, stream, indent=2, sort_keys=True, allow_nan=False)
        stream.write("\n")
        stream.flush()
        os.fsync(stream.fileno())
    os.replace(temporary, path)


def stop(proc, tree):
    errors = []
    try:
        tree.observe()
    except Exception as error:
        errors.append(str(error))
    try:
        tree.signal(signal.SIGKILL)
        proc.wait(timeout=5)
        deadline = time.monotonic() + 2
        while tree.live() and time.monotonic() < deadline:
            time.sleep(0.02)
        if tree.live():
            errors.append("observed process remained live after cleanup")
    except Exception as error:
        errors.append(str(error))
    return errors


def run_command(argv, cwd, log, timeout, overall_deadline):
    start = time.monotonic()
    result = {"argv": argv, "elapsed_seconds": None, "exit_code": None,
              "timed_out": False, "error": None, "cleanup_errors": [], "passed": False}
    proc = tree = None
    try:
        if CANCELLED:
            raise InterruptedError("evaluation interrupted")
        deadline = min(start + timeout, overall_deadline)
        if deadline <= start:
            raise TimeoutError("total evaluation deadline exhausted")
        with log.open("xb") as stream:
            environment = git_environment()
            environment["PYTHONDONTWRITEBYTECODE"] = "1"
            proc = processes.launch_pinned(argv, cwd=cwd, env=environment, stdin=subprocess.DEVNULL,
                                            stdout=stream, stderr=subprocess.STDOUT, start_new_session=True)
            tree = proc._validation_tree
            while True:
                tree.observe()
                if CANCELLED:
                    raise InterruptedError("evaluation interrupted")
                if time.monotonic() >= deadline:
                    result["timed_out"] = True
                    raise TimeoutError("command deadline exhausted")
                try:
                    result["exit_code"] = proc.wait(timeout=min(0.02, max(0.001, deadline - time.monotonic())))
                    break
                except subprocess.TimeoutExpired:
                    continue
            tree.observe()
            if tree.live(descendants_only=True):
                raise RuntimeError("command left observed background processes")
            result["passed"] = result["exit_code"] == 0
    except Exception as error:
        result["error"] = f"{type(error).__name__}: {error}"
    finally:
        if tree is not None:
            result["cleanup_errors"] = stop(proc, tree)
            tree.close()
        if result["cleanup_errors"]:
            result["passed"] = False
        result["elapsed_seconds"] = round(time.monotonic() - start, 6)
    return result


def schedule(config):
    """Rotating forward/reverse blocks balance position and adjacent carryover.

    Full position balance requires repeats to be a multiple of arm count;
    forward/reverse balance requires twice that. No randomness is implied.
    """
    arms = config["arms"]
    for repeat in range(config["repeats"]):
        for task_number, task in enumerate(config["tasks"]):
            shift = (repeat + task_number) % len(arms)
            order = arms[shift:] + arms[:shift]
            if (repeat // len(arms)) % 2:
                order = list(reversed(order))
            for position, arm in enumerate(order):
                yield repeat, task, position, arm


def cancel(_number, _frame):
    global CANCELLED
    CANCELLED = True


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--config", required=True)
    parser.add_argument("--output", required=True, help="new private evidence directory outside source")
    parser.add_argument("--fixtures", action="store_true", help="explicitly run declared non-model controls")
    parser.add_argument("--allow-models", action="store_true", help="authorize owner-declared live arms, which may bill")
    args = parser.parse_args()
    os.umask(0o077)
    config_path = Path(args.config).resolve(strict=True)
    config_bytes = config_path.read_bytes()
    config = validate(json.loads(config_bytes), config_path.parent)
    if config["mode"] == "fixture" and (not args.fixtures or args.allow_models):
        parser.error("fixture configurations require --fixtures and exclude --allow-models")
    if config["mode"] == "live" and (not args.allow_models or args.fixtures):
        parser.error("live configurations require explicit --allow-models and exclude --fixtures")
    processes.require_process_support()
    output_arg = Path(args.output).absolute()
    output = output_arg.parent.resolve(strict=True) / output_arg.name
    source = Path(config["source"]["repository"])
    if output == source or source in output.parents:
        parser.error("output must be outside the source repository")
    start = time.monotonic()
    deadline = start + config["total_timeout_seconds"]
    protected = protect_evaluators(config, source, deadline)
    files, snapshot_digest = read_snapshot(config["source"], deadline)
    output.mkdir(mode=0o700)  # Refuse reuse, including an existing empty directory.
    report = {"version": 1, "mode": config["mode"], "completed": False, "passed": False,
              "claim": "offline harness controls; not AI performance evidence" if args.fixtures else "local matched workflow attempts; no comparative claim inferred",
              "source": config["source"], "snapshot_sha256": snapshot_digest,
              "configuration_sha256": hashlib.sha256(config_bytes).hexdigest(),
              "planned_attempts": config["repeats"] * len(config["tasks"]) * len(config["arms"]),
              "order": "deterministic rotating forward/reverse blocks; complete balance requires 2 * arm count repeats",
              "attempts": [], "usage": {"status": "unknown", "tokens": None},
              "cost": {"status": "unknown", "amount": None, "currency": None}, "error": None}
    report["evaluator_files"] = [pin for pins in protected.values() for pin in pins]
    persist(output / "configuration.json", config)
    with (output / "configuration.original.json").open("xb") as stream:
        stream.write(config_bytes)
    persist(output / "comparison.json", report)
    old_handlers = {number: signal.signal(number, cancel) for number in (signal.SIGINT, signal.SIGTERM)}
    try:
        for repeat, task, position, arm in schedule(config):
            if CANCELLED or time.monotonic() >= start + config["total_timeout_seconds"]:
                raise InterruptedError("evaluation interrupted or total deadline exhausted; remaining attempts not run")
            trial = output / f"{len(report['attempts']) + 1:03d}-{task['id']}-{arm['id']}"
            trial.mkdir(mode=0o700)
            repo, task_file, artifacts = trial / "repo", trial / "task.json", trial / "artifacts"
            artifacts.mkdir(mode=0o700)
            record = {"task": task["id"], "arm": arm["id"], "repeat": repeat + 1, "position": position + 1,
                      "directory": trial.name, "completed": False, "passed": False,
                      "usage": {"status": "unknown", "tokens": None},
                      "cost": {"status": "unknown", "amount": None, "currency": None}, "checks": []}
            report["attempts"].append(record)
            persist(output / "comparison.json", report)
            record["initial_commit"] = materialize(repo, files, deadline)
            disclosed = {"id": task["id"], "prompt": task["prompt"]}
            if "development_check" in task:
                disclosed["development_check"] = task["development_check"]
            persist(task_file, disclosed)
            substitutions = {"repo": str(repo), "task": str(task_file), "output": str(artifacts)}
            expand = lambda argv: [argument.format(**substitutions) for argument in argv]
            record["execution"] = run_command(expand(arm["argv"]), repo, trial / "actor.log", arm["timeout_seconds"], start + config["total_timeout_seconds"])
            # Checks are selected by the evaluator, not read from actor output.
            # Run them after failed actors too, retaining partial task outcomes.
            for check in task["checks"]:
                pins = protected[(task["id"], check["id"])]
                intact_before = evaluators_intact(pins)
                before_artifact = after_artifact = None
                artifact_error = None
                try:
                    before_artifact = artifact_fingerprint(repo, deadline)
                except (OSError, ValueError, TimeoutError, InterruptedError) as error:
                    artifact_error = f"candidate fingerprint unavailable: {error}"
                if intact_before and artifact_error is None:
                    result = run_command(expand(check["argv"]), repo, trial / (check["id"] + ".log"), check["timeout_seconds"], deadline)
                else:
                    result = {"argv": expand(check["argv"]), "elapsed_seconds": 0, "exit_code": None,
                              "timed_out": False, "error": artifact_error or "evaluator files changed before check", "cleanup_errors": [], "passed": False}
                try:
                    after_artifact = artifact_fingerprint(repo, deadline)
                except (OSError, ValueError, TimeoutError, InterruptedError) as error:
                    artifact_error = f"candidate fingerprint unavailable: {error}"
                result["artifact_before_sha256"] = before_artifact
                result["artifact_after_sha256"] = after_artifact
                result["artifact_intact"] = before_artifact is not None and before_artifact == after_artifact and artifact_error is None
                if not result["artifact_intact"]:
                    result["passed"] = False
                    result["error"] = artifact_error or "independent check modified candidate artifacts"
                result["evaluator_intact"] = intact_before and evaluators_intact(pins)
                if not result["evaluator_intact"]:
                    result["passed"] = False
                    result["error"] = "evaluator files changed; independent grading invalid"
                record["checks"].append(dict(result, id=check["id"]))
            record["completed"] = True
            record["passed"] = record["execution"]["passed"] and all(item["passed"] for item in record["checks"])
            persist(trial / "attempt.json", record)
            persist(output / "comparison.json", report)
        if CANCELLED:
            raise InterruptedError("evaluation interrupted")
        report["completed"] = len(report["attempts"]) == report["planned_attempts"]
        report["passed"] = report["completed"] and all(item["completed"] and item["passed"] for item in report["attempts"])
    except Exception as error:
        report["error"] = f"{type(error).__name__}: {error}"
    finally:
        report["elapsed_seconds"] = round(time.monotonic() - start, 6)
        for number, handler in old_handlers.items():
            signal.signal(number, handler)
        persist(output / "comparison.json", report)
    print(output / "comparison.json")
    return 0 if report["passed"] else 1


if __name__ == "__main__":
    try:
        sys.exit(main())
    except (ValueError, OSError, subprocess.SubprocessError) as error:
        print(f"comparison refused: {error}", file=sys.stderr)
        sys.exit(2)
