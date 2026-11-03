#!/usr/bin/env python3
"""Bounded no-model soak, restart and in-flight broker-crash validation."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import signal
import subprocess
import tempfile
import time


def require(condition, message):
    # Validation must also run when Python is invoked with -O.
    if not condition:
        raise RuntimeError(message)


def stop(process, crash=False):
    if process is None:
        return
    if process.poll() is None:
        try:
            os.killpg(process.pid, signal.SIGKILL if crash else signal.SIGTERM)
        except ProcessLookupError:
            pass
    try:
        process.communicate(timeout=5)
    except subprocess.TimeoutExpired:
        try:
            os.killpg(process.pid, signal.SIGKILL)
        except ProcessLookupError:
            pass
        process.communicate(timeout=5)


def all_tasks(call):
    """Audit all pages, including soaks beyond the broker's 200-row limit."""
    tasks, seen, cursor = [], set(), ""
    while True:
        page = call("task.list", {"limit": 200, "after": cursor})
        if not page["tasks"]:
            return tasks
        for task in page["tasks"]:
            require(task["id"] not in seen, "task pagination repeated an ID")
            seen.add(task["id"])
            tasks.append(task)
        next_cursor = page["cursor"]
        require(next_cursor and next_cursor != cursor, "task pagination did not advance")
        cursor = next_cursor


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", default="bin/amx")
    parser.add_argument("--turns", type=int, default=30)
    parser.add_argument("--interval", type=float, default=3)
    parser.add_argument("--output", help="optional new private JSON report")
    args = parser.parse_args()
    if not 10 <= args.turns <= 1000 or not 0 <= args.interval <= 60:
        parser.error("turns must be 10..1000 and interval 0..60 seconds")
    binary = str(Path(args.binary).resolve(strict=True))
    binary_sha = hashlib.sha256(Path(binary).read_bytes()).hexdigest()
    started = time.monotonic()
    with tempfile.TemporaryDirectory(prefix="amx-soak-") as root:
        state = Path(root) / "s"
        broker = worker = None

        def cli(*parts):
            result = subprocess.run([binary, "managed", *map(str, parts)],
                                    capture_output=True, text=True, timeout=30)
            if result.returncode != 0:
                raise RuntimeError(result.stdout + " " + result.stderr)
            envelope = json.loads(result.stdout)
            require(envelope.get("ok") is True, "CLI returned an unsuccessful envelope")
            return envelope.get("result")

        def call(operation, body):
            return cli("call", operation, "--credential-file", state / "owner.json",
                       "--json", json.dumps(body))

        def launch(*parts):
            return subprocess.Popen([binary, "managed", *map(str, parts)],
                                    stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                                    start_new_session=True)

        def task(task_id):
            return call("task.get", {"id": task_id})["task"]

        def verify_trace(value):
            cli("trace", "verify", "--state", state, "--agent", "worker",
                "--id", value["trace"]["id"], "--mac", value["trace"]["mac"])

        def start_broker():
            process = launch("serve", "--state", state, "--sandbox", "bubblewrap")
            try:
                deadline = time.monotonic() + 10
                while time.monotonic() < deadline:
                    if process.poll() is not None:
                        raise RuntimeError(process.communicate()[1].decode())
                    try:
                        call("identity", {})
                        return process
                    except (RuntimeError, FileNotFoundError):
                        time.sleep(0.03)
                raise RuntimeError("broker not ready")
            except BaseException:
                stop(process)
                raise

        script = r'''set -eu
input=$(cat)
case "$input" in *CRASH_ME*) printf STARTED > crash-started; sleep 30; printf UNEXPECTED >> crash-started;; esac
printf SOAK_OK
'''
        config = {
            "version": 2, "mode": "managed", "session": "soak",
            "agents": {"worker": {
                "harness": "command", "sandbox": "bubblewrap", "network": "none",
                "command": ["/bin/sh", "-c", script],
                "trace": {"enabled": True, "max_turn_bytes": 8192,
                          "max_total_bytes": (args.turns + 2) * 8192},
            }},
            "limits": {"max_runtime_seconds": max(300, int(args.turns * (args.interval + 2) + 120)),
                       "max_tasks": args.turns + 10},
            "policy": {"default": "deny"},
        }
        path = Path(root) / "config.yaml"
        path.write_text(json.dumps(config))
        cli("init", path, "--state", state)
        try:
            broker = start_broker()
            deadline, restarts, verified = None, 0, set()
            for index in range(args.turns):
                # Use the mutation's authoritative ID, never the last item of
                # a bounded list that stops representing new tasks after 200.
                task_id = call("instruct", {"to": ["worker"], "text": f"bounded soak turn {index}"})["task"]["id"]
                cli("worker", "--state", state, "--agent", "worker", "--once")
                value = task(task_id)
                require(value["state"] == "submitted" and value["result"] == "SOAK_OK",
                        f"soak turn {index} did not submit its expected result")
                verify_trace(value)
                call("task.verify", {"id": task_id, "version": value["version"]})
                verified.add(task_id)
                current = call("identity", {})["runtime_deadline"]
                if deadline is None:
                    deadline = current
                require(current == deadline, "restart extended session deadline")
                if (index + 1) % 5 == 0:
                    stop(broker)
                    broker = start_broker()
                    restarts += 1
                time.sleep(args.interval)

            crash_id = call("instruct", {"to": ["worker"], "text": "CRASH_ME"})["task"]["id"]
            worker = launch("worker", "--state", state, "--agent", "worker", "--once")
            marker = state / "workspaces" / "worker" / "crash-started"
            until = time.monotonic() + 10
            while not marker.exists():
                require(worker.poll() is None, "crash fixture ended early")
                if time.monotonic() > until:
                    raise TimeoutError("crash fixture did not start")
                time.sleep(0.03)
            stop(broker, crash=True)
            worker.communicate(timeout=10)
            broker = start_broker()
            value = task(crash_id)
            require(value["state"] == "running" and value["delivery"] == "uncertain",
                    "broker crash did not preserve delivery uncertainty")
            cli("worker", "--state", state, "--agent", "worker", "--once")
            require(marker.read_text() == "STARTED", "crashed task was reexecuted")
            require(call("identity", {})["runtime_deadline"] == deadline,
                    "crash recovery extended session deadline")
            value = task(crash_id)
            call("task.resolve", {"id": crash_id, "version": value["version"], "resolution": "fail"})

            audited = all_tasks(call)
            require(len(audited) == args.turns + 1, "unexpected task count after restart")
            for value in audited:
                if value["id"] == crash_id:
                    require(value["state"] == "failed", "owner resolution did not persist")
                else:
                    require(value["id"] in verified and value["state"] == "verified",
                            "previously verified work changed after restart")
                    verify_trace(value)
            require(hashlib.sha256(Path(binary).read_bytes()).hexdigest() == binary_sha,
                    "validation executable changed during collection")
            report = {
                "ok": True, "turns_verified": args.turns, "tasks_audited": len(audited),
                "clean_restarts": restarts, "seconds": round(time.monotonic() - started, 3),
                "binary_sha256": binary_sha, "session_deadline_unchanged": True,
                "every_completed_trace_verified_after_recovery": True,
                "inflight_broker_crash": "uncertain, process stopped, no automatic replay, owner resolved fail",
                "model_calls": 0, "coverage": "bounded soak only; no multi-day availability claim",
            }
        finally:
            stop(worker)
            stop(broker)

    # Publish success only after process and private-state cleanup succeeds.
    if args.output:
        descriptor = os.open(args.output, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        with os.fdopen(descriptor, "w") as output:
            json.dump(report, output, indent=2)
            output.write("\n")
    print(json.dumps(report, indent=2))


if __name__ == "__main__":
    main()
