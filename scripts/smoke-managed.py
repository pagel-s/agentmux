#!/usr/bin/env python3
"""Real sandbox/broker/agent handoff smoke test; no models or third-party Python packages."""
import argparse
import json
import os
from pathlib import Path
import subprocess
import tempfile
import time


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", default="bin/agentmux")
    options = parser.parse_args()
    binary = str(Path(options.binary).resolve(strict=True))
    with tempfile.TemporaryDirectory(prefix="amx-smoke-") as root:
        state = str(Path(root) / "state")

        def cli(*args, expected=0):
            result = subprocess.run([binary, "managed", *args], capture_output=True,
                                    text=True, timeout=20)
            if result.returncode != expected:
                raise RuntimeError(f"CLI failed: {args}: {result.stdout} {result.stderr}")
            envelope = json.loads(result.stdout)
            assert envelope["ok"] == (expected == 0), envelope
            return envelope.get("result", envelope.get("error"))

        def call(operation, arguments, agent=None, expected=0):
            credential = Path(state) / (f"credentials/{agent}.json" if agent else "owner.json")
            return cli("call", operation, "--credential-file", str(credential),
                       "--json", json.dumps(arguments), expected=expected)

        lead_script = r'''set -eu
test ! -e "$1/owner.json"
test -z "${OWNER_ONLY_SECRET-}"
"$AGENTMUX_CLI" managed call board.post --json '{"board":"team","text":"lead in a sandbox"}' >/dev/null
"$AGENTMUX_CLI" managed call memory.put --json '{"namespace":"project","key":"handoff","value":"authorized","version":0}' >/dev/null
"$AGENTMUX_CLI" managed call instruct --request-id smoke-child --json '{"to":["worker"],"text":"Check our board and report from your sandbox."}' >/dev/null
printf LEAD_OK
'''
        worker_script = r'''set -eu
test ! -e "$1/owner.json"
test ! -e "$1/credentials/lead.json"
test -z "${OWNER_ONLY_SECRET-}"
"$AGENTMUX_CLI" managed call message.list --json '{"board":"team"}' | grep -q 'lead in a sandbox'
"$AGENTMUX_CLI" managed call memory.get --json '{"namespace":"project","key":"handoff"}' | grep -q authorized
if "$AGENTMUX_CLI" managed call instruct --json '{"to":["lead"],"text":"unauthorized reverse command"}' >denied.json; then exit 11; fi
grep -q forbidden denied.json
"$AGENTMUX_CLI" managed call board.post --json '{"board":"team","text":"worker in a different sandbox"}' >/dev/null
printf WORKER_OK
'''
        config = {
            "version": 2, "mode": "managed", "session": "sandbox-smoke",
            "agents": {
                "lead": {"harness": "command", "sandbox": "bubblewrap", "network": "none",
                         "command": ["/bin/sh", "-c", lead_script, "lead", state]},
                "worker": {"harness": "command", "sandbox": "bubblewrap", "network": "none",
                           "command": ["/bin/sh", "-c", worker_script, "worker", state]},
            },
            "boards": {"team": {"readers": ["*"], "writers": ["*"]}},
            "memory": {"project": {"readers": ["*"], "writers": ["lead"]}},
            "policy": {"default": "deny", "grants": [
                {"from": ["lead"], "to": ["worker"], "actions": ["instruct"]}
            ]},
            "limits": {"max_runtime_seconds": 15},
        }
        path = Path(root) / "config.yaml"
        path.write_text(json.dumps(config))  # JSON is a YAML mapping too.
        cli("init", str(path), "--state", state)
        env = dict(os.environ, OWNER_ONLY_SECRET="must-not-enter-agents")
        broker = subprocess.Popen([binary, "managed", "serve", "--state", state,
                                   "--sandbox", "bubblewrap"], env=env,
                                  stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        try:
            deadline = time.monotonic() + 10
            while not (Path(state) / "broker.sock").exists():
                if broker.poll() is not None:
                    output, error = broker.communicate()
                    raise RuntimeError(f"Isolated broker failed: {output} {error}")
                if time.monotonic() >= deadline:
                    raise RuntimeError("Isolated broker did not become ready")
                time.sleep(0.025)
            call("instruct", {"to": ["lead"], "text": "Delegate a bounded task."})
            cli("worker", "--state", state, "--agent", "lead", "--once")
            cli("worker", "--state", state, "--agent", "worker", "--once")
            tasks = call("task.list", {})["tasks"]
            assert len(tasks) == 2, tasks
            assert {task["result"] for task in tasks} == {"LEAD_OK", "WORKER_OK"}, tasks
            assert all(task["state"] == "submitted" for task in tasks), tasks
            for task in tasks:
                call("task.verify", {"id": task["id"], "version": task["version"]})
            assert call("instruct", {"to": ["lead"], "text": "forbidden"}, "worker", 2)["code"] == "forbidden"
            messages = call("message.list", {"board": "team"})["messages"]
            assert len(messages) == 2, messages
            print(json.dumps({"ok": True, "broker_sandbox": "bubblewrap",
                              "agent_sandboxes": 2, "network": "none",
                              "cross_sandbox_board": True, "shared_memory": True,
                              "instruction_hierarchy": True, "peer_credentials_hidden": True,
                              "owner_verified_tasks": 2}, indent=2))
        finally:
            broker.terminate()
            try:
                broker.communicate(timeout=5)
            except subprocess.TimeoutExpired:
                broker.kill()
                broker.communicate(timeout=5)


if __name__ == "__main__":
    main()
