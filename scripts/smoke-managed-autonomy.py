#!/usr/bin/env python3
"""Opt-in five-turn Claude/Codex acceptance from one initial goal, with restart."""
import argparse
import importlib.util
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import time

spec = importlib.util.spec_from_file_location("mixed_acceptance", Path(__file__).with_name("smoke-managed-models.py"))
mixed = importlib.util.module_from_spec(spec)
spec.loader.exec_module(mixed)


class Acceptance(mixed.Acceptance):
    def prepare(self):
        actors = ("lead", "coder")
        self.report = {"ok": False, "scenario": "persistent-autonomous-revision", "broker_sandbox": "bubblewrap"}
        binaries = {"lead": mixed.executable("claude").resolve(), "coder": mixed.executable("codex").resolve()}
        sources = {"lead": mixed.auth_source(self.options.claude_auth_file),
                   "coder": mixed.auth_source(self.options.codex_auth_file)}
        agents = {}
        for actor in actors:
            native = str(binaries[actor])
            agents[actor] = {"harness": "claude" if actor == "lead" else "codex",
                "command": [native] + (["--allowedTools", "Read,Write,Edit,Bash"] if actor == "lead" else []),
                "sandbox": "bubblewrap", "network": "host", "conversation": "persistent",
                "auth": {"file": sources[actor]}, "read_only_paths": mixed.dependencies(binaries[actor]),
                "env": {"AMX_ACCEPTANCE_RUN": self.run_id},
                "autonomy": {"enabled": True, "pings": actor == "lead", "max_turns": 3 if actor == "lead" else 2,
                             "cooldown_seconds": 1, "max_backlog": 8, "batch_size": 1, "max_hops": 8},
                "resources": {"memory_bytes": 2147483648, "cpu_percent": 100, "processes": 256,
                              "open_files": 1024, "cpu_seconds": 120}}
        agents["coder"]["codex_sandbox"] = "external"
        agents["lead"]["role"] = ("Coordinate the bounded implementation/review/revision workflow. "
            "On every authorized task or observation turn run python3 autonomous.py lead exactly once using Bash. "
            "The trusted helper assigns the next phase, reads scoped artifacts, and runs checks. "
            "Return a short summary then wait. Preserve helpers and never complete broker tasks yourself.")
        agents["coder"]["role"] = ("Implement each assigned refinement, run tests, then python3 autonomous.py publish. "
            "Preserve helpers and original tests. Native conversation history is context; current task gives the refinement.")
        config = {"version": 2, "mode": "managed", "session": "autonomous-acceptance", "agents": agents,
            "boards": {"work": {"readers": list(actors), "writers": list(actors)}},
            "memory": {"handoff": {"readers": list(actors), "writers": ["coder"]},
                       "project": {"readers": list(actors), "writers": ["lead"]}},
            "policy": {"default": "deny", "grants": [
                {"from": ["lead"], "to": ["coder"], "actions": ["instruct", "message.send", "ping.send"]},
                {"from": ["coder"], "to": ["lead"], "actions": ["message.send", "ping.send"]}]},
            "limits": {"max_runtime_seconds": self.options.timeout, "max_tasks": 8, "max_messages": 40,
                       "max_records": 1000, "max_requests": 1000, "max_concurrent_tasks": 2}}
        self.config_path = self.root / "team.json"
        mixed.private_json(self.config_path, config)
        self.cli("init", self.config_path, "--state", self.state)
        for actor in actors:
            workspace = self.state / "workspaces" / actor
            for name in ("coord.py", "autonomous.py", "oracle.py"):
                shutil.copyfile(mixed.FIXTURES / name, workspace / name)
            (workspace / "tag_normalizer.py").write_text("def normalize_tags(values):\n    return list(values)\n")
            (workspace / "test_tag_normalizer.py").write_text(
                "import unittest\nfrom tag_normalizer import normalize_tags\n"
                "class Tests(unittest.TestCase):\n"
                " def test_strings(self):\n  self.assertEqual(normalize_tags([' B ', 'a', 'A', '']), ['a', 'b'])\n"
                " def test_generator(self):\n  self.assertEqual(normalize_tags(x for x in ['b', 'A']), ['a', 'b'])\n")
            if actor == "coder":
                for args in (["init", "--quiet"], ["add", "."], ["-c", "user.name=Acceptance", "-c", "user.email=acceptance@localhost",
                             "-c", "commit.gpgsign=false", "commit", "--quiet", "-m", "Fixture"]):
                    subprocess.run(["git", "-C", str(workspace), *args], capture_output=True, check=True, timeout=15)

    def turn(self, actor):
        status = self.call("autonomy.status", {"agent": actor})["status"]
        # Only bounded readiness polling; never send another instruction.
        if not status.get("ready"):
            time.sleep(1.1)
        self.cli("worker", "--state", self.state, "--agent", actor, "--once",
                 timeout=max(10, int(self.deadline - time.monotonic())))
        tasks = self.call("task.list")["tasks"]
        assert not any(t["state"] in ("failed", "cancelled") for t in tasks), "model turn failed; no automatic retry"
        return tasks

    def run(self):
        self.prepare()
        self.preflight_oracle()
        self.start_broker()
        initial = self.call("instruct", {"to": ["lead"], "text":
            "Complete the bounded normalize_tags implementation, review refinement, revision and verification workflow. "
            "Run python3 autonomous.py lead once now, then return and wait for authenticated observations. "
            "Use the helper again on subsequent observation turns until complete."}, request_id=self.run_id + "-one-goal")["task"]
        self.turn("lead")
        self.turn("coder")
        before = self.call("conversation.status", {"agent": "coder"})["conversation"]
        assert before["state"] == "ready" and before["session_id"]
        wake_before = self.call("autonomy.status", {"agent": "lead"})["status"]
        self.stop_broker()
        self.start_broker()
        after = self.call("conversation.status", {"agent": "coder"})["conversation"]
        assert before == after, "confirmed conversation reference did not survive restart"
        wake_after = self.call("autonomy.status", {"agent": "lead"})["status"]
        assert wake_before["pending"] == wake_after["pending"] == 1
        assert wake_before["turns_used"] == wake_after["turns_used"] == 1
        assert wake_before["version"] == wake_after["version"], "restart mutated the unread cursor or budget"
        self.report["wake_queue_before_restart"] = wake_before
        self.report["wake_queue_after_restart"] = wake_after
        self.turn("lead")
        self.turn("coder")
        tasks = self.turn("lead")
        assert len(tasks) == 5 and all(t["state"] == "submitted" for t in tasks)
        assert sum(t["from"] == "$owner" for t in tasks) == 1
        assert sum(t["to"] == "lead" for t in tasks) == 3 and sum(t["to"] == "coder" for t in tasks) == 2
        assert sum(t.get("origin", {}).get("kind") == "observation" for t in tasks) == 2
        assert tasks[0]["id"] == initial["id"]
        artifact = json.loads(self.call("memory.get", {"namespace": "handoff", "key": "implementation"})["entry"]["value"])
        completion = json.loads(self.call("memory.get", {"namespace": "project", "key": "completion"})["entry"]["value"])
        assert artifact["revision"] == 2 and completion["sha256"] == artifact["sha256"]
        verdict = self.verify_artifact(artifact["content"], "independent_oracle")
        for task in tasks:
            self.call("task.verify", {"id": task["id"], "version": task["version"]})
        final_conversations = [self.call("conversation.status", {"agent": actor})["conversation"] for actor in ("lead", "coder")]
        assert all(c["state"] == "ready" and c["generation"] == 1 for c in final_conversations)
        assert final_conversations[1]["session_id"] == before["session_id"]
        self.report.update(ok=True, initial_owner_goals=1, submitted_turns=5, observation_wakeups=2,
            persistent_claude_turns=3, persistent_codex_turns=2, broker_restart=True,
            independent_oracle=verdict, owner_verified_tasks=5, artifact_sha256=artifact["sha256"],
            turn_budgets=[self.call("autonomy.status", {"agent": actor})["status"] for actor in ("lead", "coder")])


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", default="bin/agentmux")
    parser.add_argument("--allow-models", action="store_true")
    parser.add_argument("--claude-auth-file", default=str(Path.home() / ".claude" / ".credentials.json"))
    parser.add_argument("--codex-auth-file", default=str(Path.home() / ".codex" / "auth.json"))
    parser.add_argument("--timeout", type=int, default=600)
    parser.add_argument("--keep-output", action="store_true")
    parser.add_argument("--report")
    options = parser.parse_args()
    if not options.allow_models: parser.error("--allow-models authorizes up to five real cloud turns")
    if not 60 <= options.timeout <= 1200: parser.error("--timeout must be 60..1200 seconds")
    if options.report and Path(options.report).exists(): parser.error("--report must name a new file")
    runner = Acceptance(options)
    try:
        runner.run()
    except Exception:
        runner.report["failure"] = "Acceptance failed; private artifacts retained. No model turns are automatically retried."
        raise
    finally:
        clean = runner.close()
    if not clean: raise RuntimeError("cleanup could not be confirmed")
    print(json.dumps(runner.report, indent=2))


if __name__ == "__main__":
    main()
