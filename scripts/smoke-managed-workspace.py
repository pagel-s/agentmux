#!/usr/bin/env python3
"""Ten native PTY fixtures across projects: scoped daily collaboration, no models."""
import argparse
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import time


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", default="bin/agentmux")
    parser.add_argument("--keep-output", action="store_true")
    parser.add_argument("--sandbox", choices=("bubblewrap", "local"), default="bubblewrap")
    options = parser.parse_args()
    binary = str(Path(options.binary).resolve(strict=True))
    root = Path(tempfile.mkdtemp(prefix="amx-workspace-"))
    state = root / "s"
    started = False

    def run(*args, expected=0):
        result = subprocess.run([binary, *map(str, args)], capture_output=True,
                                text=True, timeout=45)
        if result.returncode != expected:
            raise RuntimeError(f"{args[0]} returned {result.returncode}: {result.stdout} {result.stderr}")
        return result.stdout

    def call(op, args, agent=None, denied=False):
        credential = state / "credentials" / f"{agent}.json" if agent else state / "owner.json"
        result = subprocess.run([binary, "managed", "call", op, "--credential-file", str(credential),
                                 "--json", json.dumps(args)], capture_output=True, text=True, timeout=10)
        body = json.loads(result.stdout)
        if denied:
            assert not body["ok"] and body["error"]["code"] == "forbidden", body
        else:
            assert result.returncode == 0 and body["ok"], body
        return body.get("result")

    def tmux(*args):
        return subprocess.check_output(["tmux", "-S", str(state / "tmux.sock"), *args], text=True, timeout=5)

    def until(predicate):
        deadline = time.monotonic() + 15
        while time.monotonic() < deadline:
            if predicate():
                return
            time.sleep(.05)
        raise RuntimeError("daily workspace condition did not become observable")

    names = [f"a{i}" for i in range(1, 11)]
    script = '''test -t 0 && test -t 1 || exit 7
printf 'NATIVE_READY\\n'
while IFS= read -r line; do printf 'HUMAN:%s\\n' "$line"; done
'''
    agents = {}
    for name in names:
        project = root / ("project-" + name)
        project.mkdir(mode=0o700)
        agents[name] = {"harness": "command", "interaction": "interactive", "sandbox": options.sandbox,
                        "network": "none" if options.sandbox == "bubblewrap" else "host",
                        "workdir": str(project), "command": ["/bin/sh", "-c", script]}
    config = {"version": 2, "mode": "managed", "session": "workspace-uat",
              "agents": {name: agents[name] for name in names[:8]},
              "policy": {"default": "deny"}, "limits": {"max_runtime_seconds": 180}}
    config_path = root / "team.json"
    config_path.write_text(json.dumps(config))
    os.chmod(config_path, 0o600)
    try:
        runtime = json.loads(run("managed", "up", config_path, "--state", state,
                                 "--broker-sandbox", "local"))["result"]
        started = True
        for name in names[8:]:
            call("agent.add", {"id": name, "config": agents[name]})
        until(lambda: len(json.loads((state / "runtime.json").read_text())["agents"]) == 10)
        runtime = json.loads((state / "runtime.json").read_text())
        panes = {name: runtime["agents"][name]["pane_id"] for name in names}

        def capture(name):
            return tmux("capture-pane", "-p", "-t", panes[name], "-S", "-100")

        def human_input(name, marker):
            tmux("send-keys", "-t", panes[name], "-l", marker)
            tmux("send-keys", "-t", panes[name], "Enter")
            until(lambda: "HUMAN:" + marker in capture(name))

        for name in names:
            until(lambda name=name: "NATIVE_READY" in capture(name))
            human_input(name, "initial-" + name)
            boundaries = call("boundaries.get", {}, name)
            assert boundaries["targets"] == [] and boundaries["boards"] == [] and boundaries["memory"] == [], boundaries

        groups = []
        for members in (("a1", "a2"), ("a3", "a4"), ("a5", "a6"), ("a2", "a3")):
            output = run("collaborate", *members, "--state", state)
            controls = call("control.get", {})
            group = next(g for g in controls["groups"] if set(g["agents"]) == set(members))
            assert group["board"] in output and group["memory"] in output, output
            groups.append(group)
            call("memory.put", {"namespace": group["memory"], "key": "design", "value": "+".join(members), "version": 0}, members[0])
            call("board.post", {"board": group["board"], "text": "note-" + group["id"]}, members[0])
            call("message.send", {"to": [members[1]], "text": "direct-" + group["id"]}, members[0])

        # Permission checks use each real assigned credential through the CLI,
        # rather than assuming the owner's omniscient activity view is an agent view.
        for group in groups:
            for name in names:
                member = name in group["members"]
                call("memory.get", {"namespace": group["memory"], "key": "design"}, name, denied=not member)
                inbox = call("message.list", {}, name)["messages"]
                assert any(m.get("board") == group["board"] for m in inbox) == member, (name, group)
                boundaries = call("boundaries.get", {}, name)
                listed = {v["name"] for v in boundaries["memory"]}
                assert (group["memory"] in listed) == member, (name, boundaries)
        call("memory.put", {"namespace": "private", "key": "personal", "value": "a1-only", "version": 0}, "a1")
        private_other = call("memory.list", {"namespace": "private"}, "a2")
        assert private_other["entries"] == [], private_other
        call("message.send", {"to": ["a7"], "text": "unrelated session"}, "a1", denied=True)
        assert call("task.list", {})["tasks"] == [], "native notes unexpectedly became managed tasks"

        # Isolating a pair revokes future coordination, while native PTYs remain
        # interactive and unrelated scoped teams keep their existing access.
        run("isolate", "a1", "a2", "--state", state)
        call("memory.get", {"namespace": groups[0]["memory"], "key": "design"}, "a1", denied=True)
        call("memory.get", {"namespace": groups[0]["memory"], "key": "design"}, "a2", denied=True)
        call("memory.get", {"namespace": groups[3]["memory"], "key": "design"}, "a2", denied=True)
        call("message.send", {"to": ["a2"], "text": "disconnected"}, "a1", denied=True)
        for name in names:
            human_input(name, "after-isolate-" + name)
        call("memory.get", {"namespace": groups[1]["memory"], "key": "design"}, "a4")
        call("memory.get", {"namespace": groups[2]["memory"], "key": "design"}, "a6")

        # Exact membership reconnect retains the group's namespace and history.
        run("collaborate", "a2", "a1", "--state", state)
        restored = next(g for g in call("control.get", {})["groups"] if set(g["agents"]) == {"a1", "a2"})
        assert restored["id"] == groups[0]["id"] and restored["memory"] == groups[0]["memory"]
        call("memory.get", {"namespace": restored["memory"], "key": "design"}, "a2")

        # Closing one owned terminal never kills another project/session.
        run("remove", "a10", "--state", state)
        def one_closed():
            manifest = json.loads((state / "runtime.json").read_text())
            return manifest["agents"]["a10"]["state"] == "stopped"
        until(one_closed)
        for name in names[:-1]:
            assert tmux("display-message", "-p", "-t", panes[name], "#{pane_dead}").strip() == "0"
            human_input(name, "after-close-" + name)
        registry = call("agent.list", {})["agents"]
        assert len(registry) == 10 and next(a for a in registry if a["id"] == "a10")["state"] == "revoked"
        report = {"ok": True, "model_calls": 0, "native_sessions": 10, "project_directories": 10,
                  "dynamically_added_native_sessions": 2,
                  "agent_sandbox": options.sandbox, "broker_sandbox": "local",
                  "disjoint_groups": 3, "overlapping_groups": 1, "initially_unrelated_sessions": 4,
                  "credential_scoped_access_checks": 40, "private_memory_isolated": True,
                  "isolate_preserves_native_input": True, "other_groups_unaffected": True,
                  "reconnect_preserves_identity_and_history": True, "close_one_preserves_other_nine": True,
                  "groups": [{"id": g["id"], "agents": g["agents"]} for g in groups]}
        (root / "report.json").write_text(json.dumps(report, indent=2) + "\n")
        print(json.dumps(report, indent=2))
    finally:
        if started or (state / "runtime.json").exists():
            run("managed", "down", "--state", state)
        if options.keep_output:
            print("Private workspace UAT artifacts retained at " + str(root))
        else:
            shutil.rmtree(root)


if __name__ == "__main__":
    main()
