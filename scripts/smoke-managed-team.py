#!/usr/bin/env python3
"""Exercise real supervised sandbox spawning and teams without any model calls."""
import argparse
import json
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import time


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", default="bin/amx")
    options = parser.parse_args()
    binary = str(Path(options.binary).resolve(strict=True))
    root = Path(tempfile.mkdtemp(prefix="amx-team-"))
    state = root / "s"

    def cli(*args, expected=0):
        process = subprocess.run([binary, "managed", *args], capture_output=True,
                                 text=True, timeout=45)
        if process.returncode != expected:
            raise RuntimeError(f"managed {args[0]} returned {process.returncode}, expected {expected}")
        envelope = json.loads(process.stdout)
        if envelope["ok"] != (expected == 0):
            raise RuntimeError("unexpected CLI result envelope")
        return envelope.get("result", envelope.get("error"))

    def call(operation, arguments=None, agent=None, expected=0):
        credential = state / (f"credentials/{agent}.json" if agent else "owner.json")
        return cli("call", operation, "--credential-file", str(credential),
                   "--json", json.dumps(arguments or {}), expected=expected)

    def wait_for(predicate, label, seconds=15):
        deadline = time.monotonic() + seconds
        while time.monotonic() < deadline:
            result = predicate()
            if result:
                return result
            time.sleep(0.05)
        raise RuntimeError(f"timed out waiting for {label}")

    def update(team, operation, actor="lead", **arguments):
        return call(operation, {"id": team["id"], "version": team["version"],
                                **arguments}, actor)["team"]

    def runtime():
        return json.loads((state / "runtime.json").read_text())

    def pane_dead(pane):
        process = subprocess.run(["tmux", "-S", str(state / "tmux.sock"),
                                  "display-message", "-p", "-t", pane,
                                  "#{pane_dead}"], capture_output=True, text=True, timeout=5)
        return process.returncode == 0 and process.stdout.strip() == "1"

    config = {
        "version": 2, "mode": "managed", "session": "team-smoke",
        "agents": {
            name: {"harness": "command", "command": ["/bin/cat"],
                   "sandbox": "bubblewrap", "network": "none"}
            for name in ("lead", "outsider")
        },
        "boards": {"team": {"readers": ["lead"], "writers": ["lead"]}},
        "memory": {"project": {"readers": ["lead"], "writers": ["lead"]}},
        "policy": {"default": "deny"},
        "spawn": {"enabled": True, "allow": {"lead": ["helper"]},
                  "templates": {"helper": {
                      "harness": "command", "command": ["/usr/bin/printf", "CHILD_OK"],
                      "sandbox": "bubblewrap", "network": "none"}}},
        "teams": {"enabled": True, "creators": ["lead"], "history": "since-join"},
        "limits": {"max_runtime_seconds": 120, "max_spawns": 1,
                   "max_spawn_depth": 1, "max_concurrent_tasks": 3,
                   "max_teams": 2, "max_team_members": 3},
    }
    config_path = root / "team.yaml"
    config_path.write_text(json.dumps(config))
    try:
        launched = cli("up", str(config_path), "--state", str(state),
                       "--broker-sandbox", "bubblewrap")
        assert launched["state"] == "running", "supervised startup failed"
        initial_deadline = call("agent.list")["agents"][0]["deadline"]
        call("board.post", {"board": "team", "text": "delegated-board"}, "lead")
        call("memory.put", {"namespace": "project", "key": "handoff",
                            "value": "delegated-memory", "version": 0}, "lead")
        spawn = {"template": "helper", "capabilities": {
            "boards_read": ["team"], "boards_write": ["team"],
            "memory_read": ["project"], "memory_write": ["project"],
            "join_teams": True,
        }}
        child = call("agent.spawn", spawn, "lead")["agent"]["id"]
        wait_for(lambda: runtime()["agents"].get(child), "automatic child pane")
        assert call("message.list", {"board": "team"}, child)["messages"], "board delegation failed"
        assert call("memory.get", {"namespace": "project", "key": "handoff"}, child)["entry"]["value"] == "delegated-memory"
        call("board.post", {"board": "team", "text": "child-authorized"}, child)
        call("instruct", {"to": [child], "text": "Return the configured child fixture result."}, "lead")

        def submitted():
            tasks = call("task.list")["tasks"]
            return tasks[0] if len(tasks) == 1 and tasks[0]["state"] == "submitted" else None

        task = wait_for(submitted, "spawned sandbox task submission")
        assert task["result"] == "CHILD_OK", "child fixture did not execute"
        team = call("team.create", {"name": "consenting-reviewers"}, "lead")["team"]
        call("board.post", {"board": team["board"], "text": "BEFORE_JOIN"}, "lead")
        assert call("message.list", {"board": team["board"]}, "outsider", 2)["code"] == "forbidden"
        assert call("message.list", {"board": team["board"]}, child, 2)["code"] == "forbidden"
        old_version = team["version"]
        team = update(team, "team.invite", agent=child)
        assert call("team.join", {"id": team["id"], "version": old_version}, child, 1)["code"] == "conflict"
        team = update(team, "team.join", actor=child)
        assert not call("message.list", {"board": team["board"]}, child)["messages"], "pre-join history leaked"
        call("board.post", {"board": team["board"], "text": "AFTER_JOIN"}, child)
        visible = call("message.list", {"board": team["board"]}, child)["messages"]
        assert [message["text"] for message in visible] == ["AFTER_JOIN"], "since-join board visibility failed"
        team = update(team, "team.remove", agent=child)
        assert call("message.list", {"board": team["board"]}, child, 2)["code"] == "forbidden"
        team = update(team, "team.invite", agent=child)
        team = update(team, "team.join", actor=child)
        team = update(team, "team.leave", actor=child)
        assert call("memory.list", {"namespace": team["memory"]}, child, 2)["code"] == "forbidden"
        team = update(team, "team.dissolve")
        assert team["state"] == "dissolved", "team did not dissolve"
        assert call("agent.spawn", spawn, "lead", 1)["code"] == "limit", "spawn lifetime limit was bypassed"
        child_pane = runtime()["agents"][child]["pane_id"]
        revoked = call("agent.stop", {"id": child}, "lead")
        assert revoked["process_stopped"] is False, "revocation claimed process termination"
        wait_for(lambda: pane_dead(child_pane), "actual child pane termination")
        time.sleep(0.3)
        assert runtime()["agents"][child]["pane_id"] == child_pane and pane_dead(child_pane), "revoked worker was automatically restarted"
        cli("down", "--state", str(state))
        restarted = cli("restart", "--state", str(state))
        assert restarted["attempt_id"] != launched["attempt_id"], "restart reused runtime attempt"
        assert call("agent.list")["agents"][0]["deadline"] == initial_deadline, "restart renewed the run budget"
        tasks = call("task.list")["tasks"]
        assert len(tasks) == 1 and tasks[0]["id"] == task["id"] and tasks[0]["state"] == "submitted", "restart replayed or changed submitted work"
        cli("down", "--state", str(state))
        print(json.dumps({"ok": True, "broker_sandbox": "bubblewrap",
                          "static_agent_sandboxes": 2, "spawned_agent_sandboxes": 1,
                          "supervised_dynamic_pane": True, "delegated_scopes": True,
                          "team_invitation_consent": True, "team_version_conflicts": True,
                          "history_since_join": True, "nonmembers_denied": True,
                          "remove_leave_dissolve": True, "spawn_lifetime_limit": True,
                          "revoked_process_stopped": True, "automatic_restart": False,
                          "restart_preserved_deadline": True, "submitted_tasks_replayed": False}, indent=2))
    finally:
        cleanup = subprocess.run([binary, "managed", "down", "--state", str(state)],
                                 capture_output=True, text=True, timeout=35)
        if cleanup.returncode == 0:
            shutil.rmtree(root)
        else:
            print(f"Managed test cleanup unconfirmed; private state retained at {state}", file=sys.stderr)


if __name__ == "__main__":
    main()
