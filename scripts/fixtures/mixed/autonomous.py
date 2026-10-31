"""Trusted transport helper for the bounded multi-turn acceptance fixture."""
import hashlib
import json
from pathlib import Path
import subprocess
import sys

from coord import call


def memory(namespace, key):
    entry = call("memory.get", {"namespace": namespace, "key": key}, allow_missing=True)
    return (json.loads(entry["entry"]["value"]), entry["entry"]["version"]) if entry else (None, 0)


def save(namespace, key, value, label):
    _, version = memory(namespace, key)
    return call("memory.put", {"namespace": namespace, "key": key,
                "value": json.dumps(value), "version": version}, label)


def lead():
    phase, _ = memory("private", "phase")
    if phase is None:
        task = call("instruct", {"to": ["coder"], "text":
            "Implement normalize_tags(values) in tag_normalizer.py for string inputs: "
            "strip, lowercase, drop empty, deduplicate and return a new sorted list without mutation. "
            "Support finite iterables. Run unittest test_tag_normalizer.py. "
            "Then run python3 autonomous.py publish; preserve all fixture helpers and tests. "
            "A subsequent review will add a type-validation requirement."}, "auto-code-initial")["task"]
        save("private", "phase", 1, "auto-lead-phase-1")
        return {"waiting_for": task["id"]}
    artifact, _ = memory("handoff", "implementation")
    assert artifact and hashlib.sha256(artifact["content"].encode()).hexdigest() == artifact["sha256"]
    if phase == 1:
        task = call("instruct", {"to": ["coder"], "text":
            "Review refinement: explicitly validate each element with isinstance(value, str) "
            "BEFORE calling string methods and raise TypeError for all non-string values. "
            "Add regression tests for None, integers and objects as elements in test_revision.py. "
            "Run both unittest files, then python3 autonomous.py publish. "
            "Preserve all fixture helpers and original tests. Return a short result."}, "auto-code-revision")["task"]
        save("private", "phase", 2, "auto-lead-phase-2")
        return {"reviewed_sha256": artifact["sha256"], "revision_task": task["id"]}
    assert phase == 2 and artifact["revision"] == 2
    Path("tag_normalizer.py").write_text(artifact["content"])
    result = subprocess.run([sys.executable, "-I", "oracle.py"], capture_output=True, text=True, timeout=15)
    assert result.returncode == 0 and json.loads(result.stdout)["ok"], "independent lead check failed"
    save("project", "completion", {"sha256": artifact["sha256"], "revision": 2,
                                  "lead_checks": json.loads(result.stdout)}, "auto-completion")
    call("board.post", {"board": "work", "text": "Implementation reviewed, revised and checked. COMPLETE."}, "auto-final-board")
    save("private", "phase", 3, "auto-lead-phase-3")
    return {"complete": True, "sha256": artifact["sha256"]}


def publish():
    old, _ = memory("handoff", "implementation")
    revision = 1 if old is None else old["revision"] + 1
    tests = ["test_tag_normalizer.py"]
    if revision == 2:
        assert Path("test_revision.py").is_file(), "revision regression tests missing"
        tests.append("test_revision.py")
    result = subprocess.run([sys.executable, "-m", "unittest", *tests], capture_output=True, text=True, timeout=20)
    assert result.returncode == 0, "development tests failed"
    content = Path("tag_normalizer.py").read_text()
    assert len(content.encode()) < 16384
    artifact = {"content": content, "sha256": hashlib.sha256(content.encode()).hexdigest(), "revision": revision}
    save("handoff", "implementation", artifact, "auto-artifact-" + str(revision))
    call("ping.send", {"to": ["lead"], "text": "Revision " + str(revision) + " artifact is ready for review."}, "auto-ready-" + str(revision))
    return {"revision": revision, "sha256": artifact["sha256"]}


if __name__ == "__main__":
    assert len(sys.argv) == 2 and sys.argv[1] in ("lead", "publish")
    print(json.dumps(lead() if sys.argv[1] == "lead" else publish()))
