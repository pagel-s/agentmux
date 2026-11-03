#!/usr/bin/env python3
"""Run known good, false-success, nonzero and timeout controls. No model calls."""
import argparse
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import sys


ROOT = Path(__file__).resolve().parents[2]
SPEC = importlib.util.spec_from_file_location("comparison", ROOT / "scripts/compare-workflows.py")
sys.path.insert(0, str(ROOT / "scripts"))
COMPARISON = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(COMPARISON)


def fingerprint(root):
    digest = hashlib.sha256()
    for path in sorted(root.rglob("*")):
        if path.is_file():
            digest.update(str(path.relative_to(root)).encode() + b"\0" + path.read_bytes())
            digest.update(str(path.stat().st_mtime_ns).encode())
    return digest.hexdigest()


def prepare(directory):
    directory.mkdir(mode=0o700)
    source = directory / "source"
    source.mkdir(mode=0o700)
    (source / "normalize.py").write_text("def normalize(text):\n    return text\n")
    COMPARISON.git(source, "-c", "init.templateDir=", "init", "--quiet")
    COMPARISON.git(source, "add", "normalize.py")
    COMPARISON.git(source, "-c", "commit.gpgsign=false", "commit", "--quiet", "--no-verify", "-m", "Control task")
    commit = COMPARISON.git(source, "rev-parse", "HEAD").decode().strip()
    # These deliberately differ from the pinned commit and must survive.
    (source / "owner-draft.txt").write_text("Owner's private draft\n")
    COMPARISON.git(source, "add", "owner-draft.txt")
    (source / "normalize.py").write_text("# owner's unstaged change\n")
    actor = Path(__file__).with_name("fixture-actor.py")
    grader = Path(__file__).with_name("grade-normalize.py")
    config = {"version": 1, "mode": "fixture", "source": {"repository": str(source), "commit": commit},
              "repeats": 1, "total_timeout_seconds": 60,
              "tasks": [{"id": "normalize", "prompt": "Implement normalize(text): trim, lowercase, and join whitespace-separated words with hyphens.",
                         "checks": [{"id": "contract", "argv": [sys.executable, str(grader), "{repo}"], "evaluator_files": [str(grader)], "timeout_seconds": 5}]}],
              "arms": [{"id": mode, "argv": [sys.executable, str(actor), mode, "{repo}", "{task}", "{output}"],
                        "timeout_seconds": 1 if mode == "timeout" else 5}
                       for mode in ("correct", "incorrect", "nonzero", "timeout")]}
    path = directory / "controls.json"
    path.write_text(json.dumps(config, indent=2) + "\n")
    return path, source


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", required=True, help="new private directory; existing paths are refused")
    args = parser.parse_args()
    os.umask(0o077)
    root = Path(args.output).resolve()
    config, source = prepare(root)
    before = fingerprint(source)
    completed = subprocess.run([sys.executable, str(ROOT / "scripts/compare-workflows.py"),
                                "--config", str(config), "--fixtures", "--output", str(root / "evidence")],
                               stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, timeout=45)
    report = json.loads((root / "evidence/comparison.json").read_text())
    attempts = {item["arm"]: item for item in report["attempts"]}
    assertions = {
        "runner_reports_known_failures": completed.returncode == 1 and report["completed"] and not report["passed"],
        "all_attempts_retained": len(report["attempts"]) == report["planned_attempts"] == 4,
        "correct_actor_passes": attempts["correct"]["passed"] is True,
        "exit_zero_does_not_fake_success": attempts["incorrect"]["execution"]["passed"] and not attempts["incorrect"]["checks"][0]["passed"] and not attempts["incorrect"]["passed"],
        "nonzero_actor_cannot_pass_with_good_artifact": attempts["nonzero"]["checks"][0]["passed"] and not attempts["nonzero"]["passed"],
        "timeout_cannot_pass": attempts["timeout"]["execution"]["timed_out"] and not attempts["timeout"]["passed"],
        "source_git_and_dirty_files_preserved": before == fingerprint(source),
        "usage_and_cost_are_unknown": all(item["usage"] == {"status": "unknown", "tokens": None} and item["cost"] == {"status": "unknown", "amount": None, "currency": None} for item in report["attempts"]),
        "private_evidence": (root / "evidence").stat().st_mode & 0o077 == 0,
    }
    summary = {"kind": "offline-harness-controls", "ai_benchmark": False,
               "passed": all(assertions.values()), "assertions": assertions}
    (root / "controls-result.json").write_text(json.dumps(summary, indent=2) + "\n")
    print(root / "controls-result.json")
    return 0 if summary["passed"] else 1


if __name__ == "__main__":
    sys.exit(main())
