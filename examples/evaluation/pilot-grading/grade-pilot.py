#!/usr/bin/env python3
"""Frozen behavioral graders, kept outside each candidate snapshot.

Go overlays add private external tests without modifying candidate files. This
is a trusted-local development evaluation, not containment for hostile code.
The comparison runner pins this script, every payload and process-support file.
"""
import argparse
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import signal
import subprocess
import sys
import tempfile
import time


TASKS = {
    "native-evidence-redaction": "internal/managed",
    "authenticated-trace-display": "cmd/agentmux",
    "worker-secret-extraction": "internal/managed",
}


def outside(path, repository):
    return path != repository and repository not in path.parents


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--task", required=True, choices=TASKS)
    parser.add_argument("--repo", required=True, type=Path)
    parser.add_argument("--go", required=True, type=Path)
    parser.add_argument("--process-support", type=Path,
                        default=Path(__file__).resolve().parents[3] / "scripts/workflow_processes.py")
    parser.add_argument("--output", type=Path)
    options = parser.parse_args()
    result = {"version": 1, "task": options.task, "passed": False,
              "exit_code": None, "timed_out": False, "error": None,
              "cleanup_errors": [], "model_calls": 0}
    started = time.monotonic()
    try:
        repo = options.repo.resolve(strict=True)
        go = options.go.resolve(strict=True)
        if not repo.is_dir() or not go.is_file() or not options.go.is_absolute():
            raise ValueError("--repo requires a directory and --go an absolute executable")
        payload = Path(__file__).resolve().with_name(options.task + "_test.go.txt")
        raw = payload.read_bytes()
        result["payload_sha256"] = hashlib.sha256(raw).hexdigest()
        support = options.process_support.resolve(strict=True)
        if not outside(support, repo):
            raise ValueError("pinned process support must be outside the candidate repository")
        result["process_support_sha256"] = hashlib.sha256(support.read_bytes()).hexdigest()
        spec = importlib.util.spec_from_file_location("pilot_process_support", support)
        processes = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(processes)
        processes.require_process_support()
        cache = Path(os.environ.get("GOCACHE", "/tmp/agentmux-pilot-grading-go-cache")).resolve()
        if not outside(cache, repo):
            raise ValueError("GOCACHE must be outside the candidate repository")
        cache.mkdir(mode=0o700, parents=True, exist_ok=True)
        # Preserve access to already downloaded dependencies, without passing
        # provider credentials or the owner's HOME into candidate test code.
        metadata_env = {"PATH": str(go.parent) + ":/usr/bin:/bin", "HOME": str(Path.home()),
                        "GOENV": "off", "GOTOOLCHAIN": "local"}
        metadata = subprocess.run([str(go), "env", "GOMODCACHE"], env=metadata_env,
                                  cwd="/tmp", capture_output=True, text=True, timeout=5, check=True)
        modules = Path(metadata.stdout.strip()).resolve()
        if not outside(modules, repo):
            raise ValueError("module cache must be outside the candidate repository")
        with tempfile.TemporaryDirectory(prefix="amx-pilot-grade-", dir="/tmp") as private:
            private = Path(private)
            copied = private / "frozen_test.go"
            copied.write_bytes(raw)
            copied.chmod(0o600)
            virtual = repo / TASKS[options.task] / "pilot_external_grader_test.go"
            if virtual.exists() or virtual.is_symlink():
                raise ValueError("candidate collides with external grader file")
            overlay = private / "overlay.json"
            overlay.write_text(json.dumps({"Replace": {str(virtual): str(copied)}}))
            overlay.chmod(0o600)
            for name in ("home", "tmp"):
                (private / name).mkdir(mode=0o700)
            environment = {"PATH": str(go.parent) + ":/usr/bin:/bin", "HOME": str(private / "home"),
                           "TMPDIR": str(private / "tmp"), "GOTMPDIR": str(private / "tmp"),
                           "LANG": "C.UTF-8", "GOCACHE": str(cache), "GOMODCACHE": str(modules),
                           "GOENV": "off", "GOFLAGS": "", "GOTOOLCHAIN": "local",
                           "GOPROXY": "off", "GOSUMDB": "off", "GOWORK": "off",
                           "PYTHONDONTWRITEBYTECODE": "1"}
            command = [str(go), "test", "-overlay=" + str(overlay), "./" + TASKS[options.task],
                       "-run", "^TestPilot", "-count=1", "-timeout=85s", "-vet=off"]
            result["command"] = command
            proc = tree = None
            with (private / "grader.log").open("w+b", buffering=0) as log:
                try:
                    proc = processes.launch_pinned(command, cwd=repo, env=environment,
                                                   stdin=subprocess.DEVNULL, stdout=log,
                                                   stderr=subprocess.STDOUT, start_new_session=True)
                    tree = proc._validation_tree
                    deadline = time.monotonic() + 100
                    while True:
                        tree.observe()
                        if log.tell() > 4 * 1024 * 1024:
                            raise RuntimeError("grader output exceeded its evidence allocation")
                        if time.monotonic() >= deadline:
                            result["timed_out"] = True
                            raise TimeoutError("grader exceeded its lifetime boundary")
                        try:
                            result["exit_code"] = proc.wait(timeout=0.02)
                            break
                        except subprocess.TimeoutExpired:
                            pass
                    tree.observe()
                    if tree.live(descendants_only=True):
                        raise RuntimeError("grader left observed background processes")
                    result["passed"] = result["exit_code"] == 0
                finally:
                    if tree is not None:
                        for action in (tree.observe, lambda: tree.signal(signal.SIGKILL)):
                            try:
                                action()
                            except Exception as error:
                                result["cleanup_errors"].append(type(error).__name__ + ": " + str(error))
                        try:
                            proc.wait(timeout=5)
                            deadline = time.monotonic() + 2
                            while tree.live() and time.monotonic() < deadline:
                                time.sleep(0.02)
                            if tree.live():
                                result["cleanup_errors"].append("observed grader process remained live")
                        except Exception as error:
                            result["cleanup_errors"].append(type(error).__name__ + ": " + str(error))
                        tree.close()
                    log.seek(0)
                    evidence = log.read(4 * 1024 * 1024)
                    result["log_prefix_sha256"] = hashlib.sha256(evidence).hexdigest()
                    result["log_bytes"] = os.fstat(log.fileno()).st_size
                    result["log_tail"] = evidence.decode(errors="replace")[-12000:]
        if result["cleanup_errors"]:
            result["passed"] = False
    except Exception as error:
        result["passed"] = False
        result["error"] = type(error).__name__ + ": " + str(error)
    result["elapsed_seconds"] = round(time.monotonic() - started, 3)
    encoded = json.dumps(result, indent=2) + "\n"
    if options.output:
        with open(options.output, "x", encoding="utf-8", opener=lambda path, flags: os.open(path, flags, 0o600)) as stream:
            stream.write(encoded)
    sys.stdout.write(encoded)
    return 0 if result["passed"] else 1


if __name__ == "__main__":
    raise SystemExit(main())
