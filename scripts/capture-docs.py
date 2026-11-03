#!/usr/bin/env python3
"""Capture real current documentation surfaces using disposable command fixtures.

Requires Python 3 with playwright, pillow and rich, a Playwright Chromium install,
tmux, Git and DejaVu Sans Mono. Run after `make build`:

  python scripts/capture-docs.py --binary bin/amx

No provider CLI, authentication or inference is used. All broker changes target
new private temporary fixtures. Screenshots are staged and published together;
owner URLs/credentials are never written into the public provenance manifest.
Terminal images render the actual ANSI tmux capture, without rewritten controls.
Fixtures explicitly use trusted local execution; captures do not test isolation.
"""

import argparse
import datetime
import hashlib
import io
from importlib.metadata import version as package_version
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tempfile
import time

from PIL import Image, ImageDraw, ImageFont
from playwright.sync_api import expect, sync_playwright
from rich.cells import get_character_cell_size
from rich.console import Console
from rich.text import Text


def until(check, label, seconds=30):
    end = time.monotonic() + seconds
    while time.monotonic() < end:
        result = check()
        if result:
            return result
        time.sleep(0.1)
    raise RuntimeError("Timed out: " + label)


class Capture:
    def __init__(self, options):
        self.repo = Path(__file__).resolve().parents[1]
        self.binary = Path(options.binary).resolve()
        self.output = Path(options.output).resolve()
        self.root = Path(tempfile.mkdtemp(prefix="amx-docs-images-"))
        self.root.chmod(0o700)
        self.staged = self.root / "rendered"
        self.staged.mkdir(mode=0o700)
        self.env = {"PATH": os.environ["PATH"], "TERM": "xterm-256color",
                    "SHELL": "/bin/sh", "HOME": str(self.root / "home"),
                    "XDG_STATE_HOME": str(self.root / "xdg"),
                    "XDG_CONFIG_HOME": str(self.root / "config"),
                    "XDG_CACHE_HOME": str(self.root / "cache"),
                    "LC_ALL": "C.UTF-8", "CLICOLOR_FORCE": "1", "FORCE_COLOR": "1"}
        Path(self.env["HOME"]).mkdir(mode=0o700)
        self.processes, self.states, self.images = [], [], []
        self.cleanup = None
        self.state = self.root / "work"
        self.version = self.run("version").strip()
        self.source = subprocess.check_output(["git", "-C", str(self.repo), "rev-parse", "HEAD"], text=True).strip()

    def run(self, *args):
        process = subprocess.run([str(self.binary), *map(str, args)], env=self.env,
                                 text=True, capture_output=True, timeout=60)
        if process.returncode:
            raise RuntimeError("amx " + str(args[0]) + " failed: " + process.stderr.strip())
        return process.stdout

    def call(self, op, values, actor="$owner", state=None):
        state = state or self.state
        credential = state / "owner.json" if actor == "$owner" else state / "credentials" / (actor + ".json")
        return json.loads(self.run("managed", "call", op, "--credential-file", credential,
                                   "--json", json.dumps(values)))["result"]

    def tmux(self, *args, state=None):
        state = state or self.state
        return subprocess.check_output(["tmux", "-S", str(state / "tmux.sock"), *map(str, args)],
                                       env=self.env, text=True, timeout=5)

    def web(self, state):
        prefix = self.root / ("web-" + str(len(self.processes)))
        stdout = prefix.with_suffix(".stdout")
        with stdout.open("w") as output, prefix.with_suffix(".stderr").open("w") as errors:
            process = subprocess.Popen([str(self.binary), "web", "--state", str(state)],
                                       env=self.env, text=True, stdout=output, stderr=errors)
        stdout.chmod(0o600)
        self.processes.append(process)
        # Readiness is bounded. The private owner URL is never copied into the
        # public image provenance (or a screenshot of browser chrome).
        def ready():
            with stdout.open() as output:
                for line in output.read(8192).splitlines():
                    if line.startswith("http://"):
                        return line.strip()
            if process.poll() is not None:
                raise RuntimeError("private fixture web server exited before readiness")
            return None
        return until(ready, "private fixture web server", 15)

    def fixture(self):
        labels = {"api": ("Fix login redirect", "api"), "api-review": ("Review login change", "api"),
                  "poet": ("Poem: opening stanza", "poem"), "editor": ("Poem: second stanza", "poem"),
                  "billing": ("Billing retries", "billing"), "infra": ("Investigate CI latency", "infra"),
                  "notes": ("Research reading", "notes"), "scratch": ("New investigation", "scratch"),
                  "builder": ("Check fixture output", "api"), "verifier": ("Deliberate failure fixture", "api"),
                  "recovery": ("Stopped delivery fixture", "api")}
        agents = {}
        for name, (_, project) in labels.items():
            folder = self.root / project
            folder.mkdir(exist_ok=True)
            agent = {"harness": "command", "sandbox": "local", "network": "host",
                     "workdir": str(folder), "role": "Documentation fixture. No model is running."}
            if name in ("builder", "verifier", "recovery"):
                commands = {"builder": ["/bin/sh", "-c", "input=$(cat); printf '%s\\n' \"$input\"; case \"$input\" in *'LIVE OBSERVATION FIXTURE'*) sleep 600;; esac"], "verifier": ["/bin/sh", "-c", "cat; exit 2"],
                            "recovery": ["/bin/sh", "-c", "cat; sleep 600"]}
                agent["command"] = commands[name]
            else:
                agent.update(interaction="interactive", command=["/bin/sh", "-c", "printf 'Command-only documentation terminal. No model calls.\\n'; cat"])
            agents[name] = agent
        config = {"version": 2, "mode": "managed", "session": "docs-fixture", "agents": agents,
                  "boards": {"announcements": {"readers": list(agents), "writers": list(agents)}},
                  "memory": {"findings": {"readers": ["api", "api-review"], "writers": ["api", "api-review"]}},
                  "policy": {"default": "deny"},
                  "teams": {"enabled": True, "creators": ["api", "poet"], "history": "since-join"},
                  "limits": {"max_runtime_seconds": 900, "max_concurrent_tasks": 4, "max_teams": 6,
                             "max_team_members": 6, "max_records": 10000, "max_requests": 15000,
                             "max_messages": 1000, "max_tasks": 100}}
        path = self.root / "team.json"
        path.write_text(json.dumps(config))
        self.runtime = json.loads(self.run("managed", "up", path, "--state", self.state))["result"]
        self.states.append(self.state)
        for name, (title, _) in labels.items():
            self.call("agent.label", {"id": name, "title": title, "version": 0})
        self.groups = {}
        for name, members in (("api", ["api", "api-review"]), ("poem", ["poet", "editor"])):
            self.run("collaborate", *members, "--state", self.state)
            self.groups[name] = next(g for g in self.call("control.get", {})["groups"] if sorted(g["agents"]) == sorted(members))
        self.run("allow", "poet", "editor", "commands", "--state", self.state)
        self.call("board.post", {"board": "announcements", "text": "COMMAND-ONLY FIXTURE: two review pairs; other sessions remain independent."})
        self.call("board.post", {"board": self.groups["api"]["board"], "text": "Plan: refresh the login cookie after authentication. Keep permission checks and add an expired-cookie regression."}, "api")
        self.call("board.post", {"board": self.groups["api"]["board"], "text": "Review fixture: inspect the final diff and run the declared checks before applying."}, "api-review")
        self.call("board.post", {"board": self.groups["poem"]["board"], "text": "The Second Cup: two stanzas, an evening window and rain. This is owner-provided fixture text."}, "poet")
        self.call("memory.put", {"namespace": self.groups["api"]["memory"], "key": "agreed-plan",
                  "value": "Refresh the cookie only after authentication. Add an expired-cookie test. Review the final diff before applying.", "version": 0, "tags": ["handoff", "fixture"]}, "api")
        self.call("memory.get", {"namespace": self.groups["api"]["memory"], "key": "agreed-plan"}, "api-review")
        team = self.call("team.create", {"name": "API triage", "description": "Scripted fixture: both participants explicitly consented."}, "api")["team"]
        team = self.call("team.invite", {"id": team["id"], "agent": "api-review", "version": team["version"]}, "api")["team"]
        self.call("team.join", {"id": team["id"], "version": team["version"]}, "api-review")
        pending = self.call("team.create", {"name": "Poem circle", "description": "Invitation pending; no automatic membership."}, "poet")["team"]
        self.call("team.invite", {"id": pending["id"], "agent": "editor", "version": pending["version"]}, "poet")
        task = self.call("instruct", {"to": ["builder"], "text": "FIXTURE: echo this input to demonstrate a completed delivery."})["task"]
        finished = until(lambda: (t if (t := self.call("task.get", {"id": task["id"]})["task"])["state"] == "submitted" else None), "submitted fixture task")
        self.call("task.verify", {"id": finished["id"], "version": finished["version"]})
        live = self.call("instruct", {"to": ["builder"], "text": "LIVE OBSERVATION FIXTURE: keep the command alive so the owner's worker check-in remains inspectable. No model is running."})["task"]
        until(lambda: self.call("task.get", {"id": live["id"]})["task"].get("progress", {}).get("worker_observed_at"), "live fixture worker check-in")
        failed = self.call("instruct", {"to": ["verifier"], "text": "FIXTURE: deliberately exit 2 so the recorded failure is visible."})["task"]
        until(lambda: self.call("task.get", {"id": failed["id"]})["task"]["state"] == "failed", "deliberately failed task")
        self.call("instruct", {"to": ["editor"], "text": "FIXTURE queued instruction: contribute a four-line reply stanza."}, "poet")
        recovery = self.call("instruct", {"to": ["recovery"], "text": "STOPPED DELIVERY FIXTURE: inspect the recorded outcome before choosing any retry. This command fixture was stopped by its owner."})["task"]
        until(lambda: self.call("task.get", {"id": recovery["id"]})["task"]["state"] == "running", "claimed recovery fixture")
        self.call("agent.stop", {"id": "recovery"})
        def retire():
            try:
                return self.call("agent.retire", {"id": "recovery"})
            except RuntimeError:
                return None
        self.retired = until(retire, "stopped worker retirement")
        self.recovery_id = recovery["id"]
        task = self.call("task.get", {"id": self.recovery_id})["task"]
        if not task.get("recovery") or task["delivery"] != "uncertain":
            raise RuntimeError("owner stop did not retain authentic uncertain-delivery evidence")
        self.url = self.web(self.state)

    def terminal(self, name, pane, expect):
        until(lambda: expect in self.tmux("capture-pane", "-p", "-t", pane), "terminal " + name)
        time.sleep(0.25)
        raw = self.tmux("capture-pane", "-e", "-p", "-t", pane).rstrip() + "\n"
        (self.root / (name + ".ansi")).write_text(raw)
        parsed = Text.from_ansi(raw)
        normal = ImageFont.truetype("/usr/share/fonts/truetype/dejavu/DejaVuSansMono.ttf", 17)
        bold = ImageFont.truetype("/usr/share/fonts/truetype/dejavu/DejaVuSansMono-Bold.ttf", 17)
        cell, line_height, pad = normal.getlength("M"), 24, 22
        width = 136
        canvas = Image.new("RGB", (int(width * cell + 2 * pad), len(parsed.plain.splitlines()) * line_height + 2 * pad + 32), "#000000")
        draw = ImageDraw.Draw(canvas)
        draw.text((pad, 10), "AGENTMUX · CURRENT SOURCE · COMMAND-ONLY FIXTURE", font=normal, fill="#a993d5")
        draw.line((pad, 37, canvas.width - pad, 37), fill="#342644")
        console = Console(file=io.StringIO(), width=width, color_system="truecolor", force_terminal=True)
        x, y = 0, 0
        for segment in console.render(parsed, console.options.update(width=width)):
            style = segment.style
            fg, bg = (215, 220, 228), (0, 0, 0)
            if style:
                if style.color and not style.color.is_default:
                    fg = style.color.get_truecolor()
                if style.bgcolor and not style.bgcolor.is_default:
                    bg = style.bgcolor.get_truecolor(foreground=False)
                if style.reverse:
                    fg, bg = bg, fg
                if style.dim:
                    fg = tuple(int((f + b) / 2) for f, b in zip(fg, bg))
            for char in segment.text:
                if char == "\n":
                    x, y = 0, y + 1
                    continue
                size = max(0, get_character_cell_size(char))
                left, top = pad + x * cell, 47 + y * line_height
                draw.rectangle((left, top, left + size * cell, top + line_height), fill=bg)
                draw.text((left, top), char, font=bold if style and style.bold else normal, fill=fg)
                x += size
        canvas.save(self.staged / (name + ".jpg"), quality=92, subsampling=0)
        self.images.append({"file": name + ".jpg", "surface": "live tmux ANSI capture", "fixture": "command-only workspace"})

    def terminals(self):
        pane = self.runtime["dashboard_pane"]
        self.tmux("resize-window", "-t", pane, "-x", "136", "-y", "39")
        until(lambda: "1/10" in self.tmux("capture-pane", "-p", "-t", pane), "current active terminal roster")
        self.terminal("terminal-board", pane, "Board")
        self.tmux("send-keys", "-t", pane, "C-p")
        self.tmux("send-keys", "-t", pane, "-l", "Poem:")
        self.terminal("terminal-switcher", pane, "Poem:")
        self.tmux("send-keys", "-t", pane, "Escape")
        self.tmux("send-keys", "-t", pane, "C-k")
        self.tmux("send-keys", "-t", pane, "-l", "Tasks for selected session")
        self.terminal("terminal-actions", pane, "Tasks for selected session")
        self.tmux("send-keys", "-t", pane, "Escape")
        self.tmux("send-keys", "-t", pane, "3")
        self.terminal("terminal-connections", pane, "Connections")
        command = "exec " + " ".join("'" + s.replace("'", "'\"'\"'") + "'" for s in [str(self.binary), "task", self.recovery_id, "--open", "--state", str(self.state)])
        exact = self.tmux("new-window", "-d", "-P", "-F", "#{pane_id}", "-t", self.runtime["session"], "-n", "exact-task", command).strip()
        self.tmux("resize-window", "-t", exact, "-x", "136", "-y", "39")
        self.terminal("terminal-task-recovery", exact, "Recovery: uncertain")
        self.tmux("send-keys", "-t", exact, "q")
        observe = " ".join("'" + s.replace("'", "'\"'\"'") + "'" for s in [str(self.binary), "observe", "--limit", "2", "--state", str(self.state)]) + "; sleep 600"
        observation = self.tmux("new-window", "-d", "-P", "-F", "#{pane_id}", "-t", self.runtime["session"], "-n", "observation", observe).strip()
        self.tmux("resize-window", "-t", observation, "-x", "136", "-y", "64")
        self.terminal("terminal-observability", observation, "Observations describe reporting")

    def shot(self, page, name, fixture, target=None):
        page.wait_for_timeout(250)
        if target:
            page.locator(target).scroll_into_view_if_needed()
        page.screenshot(path=str(self.staged / (name + ".jpg")), type="jpeg", quality=92, full_page=False, animations="disabled")
        self.images.append({"file": name + ".jpg", "surface": "live browser content", "fixture": fixture})

    def browse(self, browser):
        page = browser.new_page(viewport={"width": 1440, "height": 960}, device_scale_factor=1)
        page.goto(self.url, wait_until="networkidle")
        page.locator("#connection").filter(has_text=re.compile("^Connected$")).wait_for()
        page.locator('#agents [data-agent="api"]').click()
        expect(page.locator("#agent-id")).to_have_text("api")
        page.locator('[data-tab="activity"]').click()
        page.locator("#board-filter").select_option("team")
        page.locator("#message-board").select_option(self.groups["api"]["board"])
        page.locator('#message-history-filter button[type="submit"]').click()
        page.locator("#feed").get_by_text("Review fixture:", exact=False).wait_for()
        self.shot(page, "web-board", "command-only owner/agent board posts")
        page.locator('[data-tab="graph"]').click()
        page.locator("#interaction-graph .graph-node").first.wait_for()
        self.shot(page, "web-connections", "current automatic graph arrangement and controls")
        page.locator('[data-tab="teams"]').click()
        self.shot(page, "web-teams", "scripted consented API triage team")
        self.shot(page, "web-groups", "owner-configured independent collaboration pairs", "#owner-groups")
        page.locator('[data-tab="memory"]').click()
        page.locator('#memory-namespace').select_option(self.groups["api"]["memory"])
        page.locator('#load-memory').click()
        page.get_by_role("button", name="agreed-plan", exact=False).click()
        self.shot(page, "web-memory", "real shared memory version and retrieval provenance")
        page.locator('[data-tab="tasks"]').click()
        page.locator("#task-summary-status").filter(has_text="complete retained history").wait_for()
        self.shot(page, "web-tasks", "verified, failed, pending and owner-stopped uncertain command tasks")
        page.locator('#agents [data-agent="builder"]').click()
        expect(page.locator("#agent-id")).to_have_text("builder")
        page.locator('#open-actions').click()
        page.locator('#action-query').fill("Tasks for selected session")
        page.locator("#action-query").press("Enter")
        expect(page.locator("#task-agent-filter")).to_have_value("builder")
        page.locator("#tasks").get_by_text("Worker check-in:", exact=False).wait_for()
        self.shot(page, "web-session-tasks", "direct selected-recipient task history action", "#task-history-filter")
        page.close()

    def research(self, browser):
        output = self.root / "research"
        # This capture profile intentionally selects trusted-local command
        # fixtures. It never retries a required isolation failure with fallback.
        script = str(self.repo / "scripts/fixtures/research/agent.py")
        team = self.root / "research-team.json"
        team.write_text(json.dumps({"version": 2, "mode": "managed", "session": "research-docs-fixture",
            "agents": {actor: {"harness": "command", "command": [script], "sandbox": "local", "network": "host"} for actor in ("alice", "bob", "charlie")},
            "policy": {"default": "deny", "grants": [{"from": ["*"], "to": ["*"], "actions": ["message.send", "ping.send"]}]},
            "limits": {"max_tasks": 100, "max_messages": 200, "max_message_bytes": 65536,
                "max_records": 3000, "max_requests": 5000, "max_runtime_seconds": 600,
                "max_concurrent_tasks": 3, "max_teams": 8, "max_team_members": 3}}))
        manifest = {"version": 1, "id": "documentation-study", "team": str(team),
                    "trials": 1, "seeds": [11], "randomization_seed": 741, "timeout_seconds": 600,
                    "phase_timeout_seconds": 60, "broker_sandbox": "local",
                    "game": {"kind": "public-goods-v1", "rounds": 2, "credits": 8, "per_round_cap": 2, "threshold": 4, "dividend": 3},
                    "conditions": [{"id": "individual-teams", "reward_mode": "individual", "visibility": "direct", "teams": True}]}
        path = self.root / "study.json"
        path.write_text(json.dumps(manifest))
        log = (self.root / "research.log").open("w")
        runner = subprocess.Popen([str(self.binary), "research", "run", str(path), "--output", str(output), "--start-paused"], env=self.env, stdout=log, stderr=subprocess.STDOUT)
        self.processes.append(runner)
        def ready():
            if (output / "live.json").exists():
                return True
            if runner.poll() is not None:
                raise RuntimeError("research fixture exited before readiness; inspect private research.log")
            return None
        until(ready, "research live pointer")
        live = json.loads((output / "live.json").read_text())
        state_value = live.get("state_dir") or live.get("state") or live.get("research", {}).get("progress", {}).get("state_dir")
        if not state_value:
            raise RuntimeError("research live pointer does not identify its exact fixture state")
        state = Path(state_value)
        self.states.append(state)
        status = json.loads(self.run("research", "status", output))["result"]["research"]
        self.run("research", "step", output, "--version", status["control"]["version"], "--reason", "Documentation fixture: inspect one completed coordination phase")
        def paused():
            status = json.loads(self.run("research", "status", output))["result"]["research"]
            return status if status["progress"]["phase"] == "act" and status["progress"]["state"] == "paused" else None
        until(paused, "research paused decision phase", 60)
        url = self.web(state)
        page = browser.new_page(viewport={"width": 1440, "height": 960}, device_scale_factor=1)
        page.goto(url, wait_until="networkidle")
        page.locator("#connection").filter(has_text=re.compile("^Connected$")).wait_for()
        page.locator('[data-tab="research"]').click()
        self.shot(page, "web-research", "actual deterministic public-goods fixture; one completed phase, paused before decisions")
        page.locator('#research-action').select_option("step")
        page.locator('#research-reason').fill("Fixture review only: inspect the next phase before any continuation.")
        page.locator('#review-research').click()
        self.shot(page, "web-research-control", "unsubmitted reasoned intervention review", "#research-review")
        page.locator('[data-tab="teams"]').click()
        self.shot(page, "web-research-teams", "actual scripted Alice/Bob consent; Charlie outside pair")
        page.close()

    def report(self, browser):
        demo = self.root / "checked-demo"
        self.run("demo", "--output", demo)
        record = json.loads((demo / "demo.json").read_text())
        run = Path(record["run"])
        report = self.root / "checked-report.html"
        self.run("flow", "report", run, "--output", report)
        page = browser.new_page(viewport={"width": 1440, "height": 1080}, device_scale_factor=1)
        page.goto(report.as_uri(), wait_until="load")
        self.shot(page, "checked-work-report", "actual amx demo report; deterministic command checks and fixture-only application, no model quality claim")
        page.close()

    def publish(self, browser_version):
        names = [item["file"] for item in self.images]
        if len(names) != 17 or len(set(names)) != 17:
            raise RuntimeError("expected exactly 17 authentic documentation images")
        self.output.mkdir(parents=True, exist_ok=True)
        for item in self.images:
            path = self.staged / item["file"]
            item["sha256"] = hashlib.sha256(path.read_bytes()).hexdigest()
            with Image.open(path) as image:
                item["width"], item["height"] = image.size
            shutil.copyfile(path, self.output / path.name)
        record = {"schema": 1, "captured_at_utc": datetime.datetime.now(datetime.timezone.utc).isoformat(),
                  "source_commit": self.source, "binary_version": self.version,
                  "binary_sha256": hashlib.sha256(self.binary.read_bytes()).hexdigest(),
                  "browser_version": browser_version, "model_calls": 0,
                  "capture_dependencies": {"python": sys.version.split()[0], **{name: package_version(name) for name in ("playwright", "pillow", "rich")}},
                  "data": "Disposable scripted command-only workspace and research fixtures. Owner-supplied text, not model output. No live user workspace was used.",
                  "terminal_rendering": "Actual live tmux ANSI capture; empty trailing rows cropped; fixture label outside the captured app.",
                  "uncertainty": "A real command worker claimed an instruction, was stopped by the owner and retired after exit. Broker-retained uncertain delivery is shown without resolving or retrying.",
                  "live_progress": "A command-only worker remains alive and reports lease-bound check-ins with zero native model events. Actual amx observe output and selected-recipient task history show that metadata; no provider inference is performed.",
                  "retired_recipient": {"id": self.retired["id"], "state": self.retired["state"]},
                  "execution": "Explicit trusted-local command fixtures for workspace, research broker and participants. These captures do not validate filesystem/network isolation.",
                  "checked_report": "amx demo applied only to its generated fixture; amx flow report RUN --output NEWFILE.html then freshly rendered the retained evidence for capture.",
                  "reproduce": "make build; install Python playwright pillow rich and Playwright Chromium; python scripts/capture-docs.py --binary bin/amx",
                  "cleanup": self.cleanup,
                  "images": self.images}
        (self.output / "capture-provenance.json").write_text(json.dumps(record, indent=2) + "\n")
        print(json.dumps({"captured": len(self.images), "source_commit": self.source, "model_calls": 0, "output": str(self.output)}))

    def close(self):
        if self.cleanup is not None:
            return
        for process in reversed(self.processes):
            if process.poll() is None:
                process.terminate()
                try:
                    process.wait(timeout=12)
                except subprocess.TimeoutExpired:
                    process.kill()
                    process.wait(timeout=5)
        for state in reversed(self.states):
            try:
                self.run("managed", "down", "--state", state)
            except (RuntimeError, subprocess.TimeoutExpired):
                pass
        brokers_closed = True
        tmux_stopped = True
        for state in self.states:
            credential = state / "owner.json"
            result = subprocess.run([str(self.binary), "managed", "call", "identity", "--credential-file", str(credential), "--json", "{}"], env=self.env, text=True, capture_output=True, timeout=8)
            brokers_closed = brokers_closed and result.returncode != 0
            result = subprocess.run(["tmux", "-S", str(state / "tmux.sock"), "has-session"], env=self.env, text=True, capture_output=True, timeout=5)
            tmux_stopped = tmux_stopped and result.returncode != 0
        self.cleanup = {"tracked_web_and_runner_processes_exited": all(p.poll() is not None for p in self.processes),
                        "owned_broker_connections_closed": brokers_closed,
                        "owned_tmux_servers_stopped": tmux_stopped}
        if not all(self.cleanup.values()):
            raise RuntimeError("capture fixture cleanup did not pass its liveness checks")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", default="bin/amx")
    parser.add_argument("--output", default="docs/assets/guide")
    options = parser.parse_args()
    capture = Capture(options)
    try:
        capture.fixture()
        capture.terminals()
        with sync_playwright() as playwright:
            browser = playwright.chromium.launch(headless=True)
            capture.browse(browser)
            capture.research(browser)
            capture.report(browser)
            browser_version = browser.version
            browser.close()
        capture.close()
        capture.publish(browser_version)
    finally:
        capture.close()


if __name__ == "__main__":
    main()
