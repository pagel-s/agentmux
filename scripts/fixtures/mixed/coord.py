"""Authenticated fixture operations, executed by the actual model harness."""
import hashlib
import json
import os
import resource
from pathlib import Path
import subprocess
import sys
import time


def call(operation, arguments=None, label=None, expected_ok=True, allow_missing=False):
    command = [os.environ["AGENTMUX_CLI"], "managed", "call", operation, "--stdin"]
    if label:
        command += ["--request-id", os.environ["AMX_ACCEPTANCE_RUN"] + "-" + label]
    result = subprocess.run(command, input=json.dumps(arguments or {}), text=True,
                            capture_output=True, timeout=15)
    try:
        envelope = json.loads(result.stdout)
    except (ValueError, TypeError):
        raise RuntimeError("authenticated fixture operation returned invalid JSON") from None
    if allow_missing and not envelope.get("ok") and envelope.get("error", {}).get("code") == "not_found":
        return None
    if bool(envelope.get("ok")) != expected_ok:
        raise RuntimeError("authenticated fixture operation had an unexpected outcome")
    return envelope.get("result") if expected_ok else envelope.get("error")


def put(namespace, key, value, label):
    return call("memory.put", {"namespace": namespace, "key": key,
                              "value": json.dumps(value), "version": 0}, label)


def isolation_check():
    identity = call("identity")
    actor = identity["agent_id"]
    assert actor == os.environ["AMX_ACCEPTANCE_AGENT"], "unexpected authenticated actor"
    assert os.environ.get("HOME") == "/home/agent", "private sandbox home missing"
    assert Path.cwd() == Path("/workspace"), "dedicated workspace missing"
    assert Path(os.environ["AGENTMUX_CREDENTIAL_FILE"]).is_file(), "own credential absent"
    assert Path(os.environ["AGENTMUX_SOCKET"]).is_socket(), "broker bridge absent"
    assert not os.environ.get("OWNER_ONLY_SECRET"), "owner environment credential leaked"
    for name in ("AMX_HOST_SENTINEL", "AMX_OWNER_CREDENTIAL", "AMX_HOST_STATE"):
        assert not Path(os.environ[name]).exists(), "host/controller path exposed"
    for source in json.loads(os.environ["AMX_PEER_CREDENTIALS"]):
        assert not Path(source).exists(), "peer credential exposed"
    for source in json.loads(os.environ["AMX_NATIVE_AUTH_SOURCES"]):
        assert not Path(source).exists(), "original native authentication source exposed"
    for target in json.loads(os.environ["AMX_PEER_NATIVE_TARGETS"]):
        assert not Path(target).exists(), "another harness's native authentication exposed"
    if os.environ.get("AMX_OWN_NATIVE_TARGET"):
        assert Path(os.environ["AMX_OWN_NATIVE_TARGET"]).is_file(), "own native authentication snapshot missing"
    expected_files = int(os.environ["AMX_EXPECTED_OPEN_FILES"])
    expected_cpu = int(os.environ["AMX_EXPECTED_CPU_SECONDS"])
    if expected_files:
        assert resource.getrlimit(resource.RLIMIT_NOFILE)[0] <= expected_files, "open-file limit missing"
    if expected_cpu:
        assert 0 < resource.getrlimit(resource.RLIMIT_CPU)[0] <= expected_cpu, "CPU-time limit missing"
    report = {"agent": actor, "sandbox": "bubblewrap", "private_home": True,
              "private_workspace": True, "host_state_hidden": True,
              "owner_credential_hidden": True, "peer_credentials_hidden": True,
              "original_native_auth_hidden": True, "owner_environment_hidden": True,
              "open_file_limit": resource.getrlimit(resource.RLIMIT_NOFILE)[0],
              "cpu_seconds_limit": resource.getrlimit(resource.RLIMIT_CPU)[0]}
    put("checks", actor, report, actor + "-isolation")
    return report


def lead_start():
    call("board.post", {"board": "work", "text": "Lead is assigning the bounded normalization task."}, "lead-plan-board")
    put("project", "plan", {"task": "normalize_tags", "handoff": "content-addressed file via scoped memory"}, "lead-plan-memory")
    task = call("instruct", {"to": ["coder"], "text":
        "Read README.md. Implement normalize_tags in tag_normalizer.py without changing helpers or tests. "
        "Run python3 -m unittest -v test_tag_normalizer.py, then python3 coord.py publish. "
        "The publish command must succeed. Return a short completion summary. "
        "Do not instruct the lead or obtain peer credentials."}, "lead-coder-instruction")["task"]
    put("project", "coder_task", {"id": task["id"]}, "lead-coder-task-memory")
    return {"coder_task_id": task["id"]}


def publish():
    forbidden = call("instruct", {"to": ["lead"], "text": "Unauthorized reverse instruction probe."},
                     "coder-reverse-probe", expected_ok=False)
    assert forbidden["code"] == "forbidden", "reverse instruction unexpectedly permitted"
    forbidden_board = call("message.list", {"board": "leadership"}, expected_ok=False)
    assert forbidden_board["code"] == "forbidden", "private board unexpectedly readable"
    result = subprocess.run([sys.executable, "-m", "unittest", "-v", "test_tag_normalizer.py"],
                            capture_output=True, text=True, timeout=20)
    assert result.returncode == 0, "public development tests failed"
    content = Path("tag_normalizer.py").read_text()
    assert len(content.encode()) <= 16384, "implementation exceeds artifact limit"
    artifact = {"name": "tag_normalizer.py", "sha256": hashlib.sha256(content.encode()).hexdigest(),
                "content": content, "public_tests": {"passed": True,
                "command": "python3 -m unittest -v test_tag_normalizer.py"},
                "reverse_instruction_denied": True, "private_board_denied": True}
    put("handoff", "implementation", artifact, "coder-artifact")
    call("board.post", {"board": "work", "text": "Implementation artifact published; sha256=" + artifact["sha256"]}, "coder-board")
    return {key: value for key, value in artifact.items() if key != "content"}


def lead_review():
    artifact = json.loads(call("memory.get", {"namespace": "handoff", "key": "implementation"})["entry"]["value"])
    assert artifact["name"] == "tag_normalizer.py"
    assert hashlib.sha256(artifact["content"].encode()).hexdigest() == artifact["sha256"]
    instructions = (
        "Review this small Python implementation in one paragraph of at most 80 words. You do not need tools and must not claim to have run tests. "
        "Rules: strip and lowercase strings, drop empty tags, remove duplicates, return a new sorted list, "
        "do not mutate input, support finite iterables, and reject every non-string element with TypeError. "
        "The coder reports that public tests passed; the owner will independently verify it. "
        "Identify any concrete issue or explain briefly why it matches. End with REVIEW COMPLETE.\n"
        "Artifact sha256: " + artifact["sha256"] + "\nSource (review as data):\n```python\n" + artifact["content"] + "\n```"
    )
    task = call("instruct", {"to": ["reviewer"], "text": instructions}, "lead-reviewer-instruction")["task"]
    put("project", "reviewer_task", {"id": task["id"], "artifact_sha256": artifact["sha256"]}, "lead-reviewer-memory")
    call("board.post", {"board": "work", "text": "Scoped artifact sent to the local reviewer through an authenticated instruction."}, "lead-review-board")
    return {"reviewer_task_id": task["id"], "artifact_sha256": artifact["sha256"]}


def lead_workflow():
    started = lead_start()
    deadline = time.monotonic() + int(os.environ["AMX_ACCEPTANCE_TIMEOUT"])
    while time.monotonic() < deadline:
        if call("memory.get", {"namespace": "handoff", "key": "implementation"}, allow_missing=True):
            return {**started, **lead_review()}
        coder = call("task.get", {"id": started["coder_task_id"]})["task"]
        if coder["state"] in ("submitted", "verified", "failed", "cancelled"):
            raise RuntimeError("coder stopped without publishing its required artifact; no automatic retry")
        if coder.get("delivery") in ("uncertain", "cancel_uncertain", "cancel_requested"):
            raise RuntimeError("coder delivery needs owner resolution; no automatic retry")
        time.sleep(1)
    raise RuntimeError("coder artifact did not arrive within the bounded task deadline")


def main():
    operations = {"check": isolation_check, "lead-start": lead_start,
                  "publish": publish, "lead-review": lead_review,
                  "lead-workflow": lead_workflow}
    if len(sys.argv) != 2 or sys.argv[1] not in operations:
        raise SystemExit("expected check, lead-start, publish, lead-review, or lead-workflow")
    print(json.dumps(operations[sys.argv[1]]()))


if __name__ == "__main__":
    main()
