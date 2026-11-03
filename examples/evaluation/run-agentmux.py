#!/usr/bin/env python3
"""Opt-in live Agentmux arm: checked workflow then apply to its disposable repo.

The outer comparison runner owns the process deadline and independent grading.
This adapter does not claim that provider charges or detached work are bounded.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys


def main(argv=None):
    os.umask(0o077)
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", required=True)
    parser.add_argument("--repo", required=True)
    parser.add_argument("--task", required=True)
    parser.add_argument("--output", required=True)
    parser.add_argument("--timeout", default="8m")
    parser.add_argument("--coder", choices=("codex", "claude", "antigravity", "antigravity-bridge", "pi"))
    parser.add_argument("--reviewer", choices=("codex", "claude", "antigravity", "antigravity-bridge", "pi"))
    parser.add_argument("--attempts", type=int, choices=range(1, 4))
    parser.add_argument("--allow-models", action="store_true")
    args = parser.parse_args(argv)
    if not args.allow_models:
        parser.error("this live adapter requires --allow-models")
    binary = Path(args.binary).resolve(strict=True)
    repository = Path(args.repo).resolve(strict=True)
    output = Path(args.output).resolve(strict=True)
    task = json.loads(Path(args.task).read_text())
    check = task.get("development_check")
    if not isinstance(check, list) or not check or not all(isinstance(item, str) and item for item in check):
        parser.error("task requires a disclosed development_check argv")
    run = output / "agentmux-run"
    command = [str(binary), "work", task["prompt"], "--repo", str(repository),
               "--output", str(run), "--timeout", args.timeout]
    for name in ("coder", "reviewer", "attempts"):
        value = getattr(args, name)
        if value is not None:
            command.extend(["--" + name, str(value)])
    command.extend(["--allow-models", "--json", "--", *check])
    (output / "agentmux-launch.json").write_text(json.dumps({"argv": command, "binary_sha256": hashlib.sha256(binary.read_bytes()).hexdigest()}, indent=2) + "\n")
    result = subprocess.run(command, stdin=subprocess.DEVNULL)
    if result.returncode:
        return result.returncode
    return subprocess.run([str(binary), "work", "apply", str(run), "--repo", str(repository), "--json"], stdin=subprocess.DEVNULL).returncode


if __name__ == "__main__":
    sys.exit(main())
