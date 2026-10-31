#!/usr/bin/env python3
"""Model-free scale/failure fixture: real native PTYs plus clearly synthetic registry rows.

Build first with `make build`. Examples:
  python3 scripts/smoke-managed-scale.py --live-agents 40 --registry-agents 200
  python3 scripts/smoke-managed-scale.py --live-agents 40 --serve --port 44535

Serving mode accepts `status`, `measure`, `expand 200`, and `quit` on stdin.
Only the configured live agents launch processes. Added registry-only external
records are synthetic load fixtures, not actual native sessions or providers.
All fixture state and a snapshot of the controller live in a private temporary
root. No provider executable, model prompt, or model endpoint is used.
"""

import argparse
import concurrent.futures
import json
import os
from pathlib import Path
import select
import shutil
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request


class Fixture:
    def __init__(self, options):
        self.options = options
        self.root = Path(tempfile.mkdtemp(prefix="amx-scale-"))
        self.state = self.root / "s"
        self.binary = self.root / "controller" / "agentmux"
        self.binary.parent.mkdir(mode=0o700)
        shutil.copyfile(Path(options.binary).resolve(strict=True), self.binary)
        self.binary.chmod(0o700)
        self.web = None
        self.runtime = None
        self.web_url = ""
        self.report = {"ok": False, "model_calls": 0, "live_pty_agents": options.live_agents,
                       "synthetic_registry_only": 0, "agent_sandbox": options.sandbox,
                       "broker_sandbox": "local", "measurements": [],
                       "scope": "Local model-free fixtures; not a provider throughput or production capacity benchmark."}

    def run(self, *args, timeout=60):
        process = subprocess.run([str(self.binary), *map(str, args)],
                                 capture_output=True, text=True, timeout=timeout)
        if process.returncode:
            raise RuntimeError(f"CLI {args[0]} failed ({process.returncode}): {process.stdout} {process.stderr}")
        return json.loads(process.stdout)["result"]

    def call(self, operation, args=None, agent=None, expected_fault=None, state=None):
        path = state or self.state
        credential = path / (f"credentials/{agent}.json" if agent else "owner.json")
        process = subprocess.run([str(self.binary), "managed", "call", operation,
                                  "--credential-file", str(credential), "--json", json.dumps(args or {})],
                                 capture_output=True, text=True, timeout=15)
        envelope = json.loads(process.stdout)
        if expected_fault:
            if process.returncode == 0 or envelope.get("error", {}).get("code") != expected_fault:
                raise AssertionError(f"{operation} expected {expected_fault}: {envelope}")
            return envelope["error"]
        if process.returncode or not envelope.get("ok"):
            raise RuntimeError(f"{operation}: {envelope.get('error')} {process.stderr}")
        return envelope["result"]

    def tmux(self, *args):
        return subprocess.check_output(["tmux", "-S", str(self.state / "tmux.sock"), *map(str, args)],
                                       text=True, timeout=10)

    def registry(self, state=None):
        agents, cursor = [], ""
        while True:
            page = self.call("agent.list", {"limit": 200, "after": cursor}, state=state)
            agents.extend(page["agents"])
            if not page["agents"] or page.get("cursor") in (None, "", cursor):
                return agents
            cursor = page["cursor"]

    def live_pty_count(self):
        if not self.runtime:
            return 0
        rows = self.tmux("list-panes", "-s", "-t", self.runtime["session"], "-F",
                         "#{pane_dead}\t#{@agentmux_runtime}\t#{@agentmux_role}\t#{@agentmux_agent}")
        return len({fields[3] for row in rows.splitlines() if len(fields := row.split("\t")) == 4
                    and fields[0] == "0" and fields[1] == self.runtime["attempt_id"] and fields[2] == "worker"})

    def save_report(self):
        path = self.root / "report.json"
        path.write_text(json.dumps(self.report, indent=2, sort_keys=True) + "\n")
        path.chmod(0o600)

    def emit(self, event, **values):
        print(json.dumps({"event": event, **values}, sort_keys=True), flush=True)

    def start(self):
        started = time.monotonic()
        native = r'''test -t 0 && test -t 1 || exit 7
printf 'MODEL_FREE_TTY_READY %s\n' "$1"
printf 'Scale fixture only. No provider or model calls. Native input remains interactive.\n'
while IFS= read -r line; do printf 'HUMAN_INPUT:%s\n' "$line"; done
'''
        agents = {}
        for number in range(1, self.options.live_agents + 1):
            name = f"fixture{number:03d}"
            project = self.root / "projects" / name
            project.mkdir(parents=True, mode=0o700)
            agents[name] = {"harness": "command", "interaction": "interactive",
                            "role": "Model-free foreground terminal fixture; no automatic model work.",
                            "command": ["/bin/sh", "-c", native, "fixture", name],
                            "workdir": str(project), "sandbox": self.options.sandbox,
                            "network": "none" if self.options.sandbox == "bubblewrap" else "host"}
        config = {"version": 2, "mode": "managed", "session": "scale-fixture", "interactive_workspace": True,
                  "agents": agents, "boards": {"team": {"readers": [], "writers": []}},
                  "memory": {"project": {"readers": [], "writers": []}}, "policy": {"default": "deny"}}
        path = self.root / "team.json"
        path.write_text(json.dumps(config)); path.chmod(0o600)
        self.runtime = self.run("managed", "up", path, "--state", self.state, "--broker-sandbox", "local")
        deadline = time.monotonic() + 30
        pending = set(agents)
        while pending:
            latest = json.loads((self.state / "runtime.json").read_text())
            if latest["state"] not in ("starting", "running"):
                raise RuntimeError(f"runtime stopped during fixture startup: {latest['state']}")
            for name in list(pending):
                pane = latest.get("agents", {}).get(name, {}).get("pane_id")
                if pane and f"MODEL_FREE_TTY_READY {name}" in self.tmux("capture-pane", "-p", "-t", pane):
                    pending.remove(name)
            if time.monotonic() > deadline:
                raise RuntimeError(f"native PTYs not ready: {sorted(pending)}")
            if pending:
                time.sleep(.1)
        self.report["startup_seconds"] = round(time.monotonic() - started, 3)
        self.report["verified_live_ptys"] = len(agents)
        for number, name in enumerate(agents, 1):
            self.call("agent.label", {"id": name, "title": f"Model-free task {number:02d}", "version": 0})
        pairs = [list(agents)[:2], list(agents)[2:4]]
        for index, pair in enumerate(pairs, 1):
            if len(pair) < 2:
                continue
            control = self.call("control.get")
            result = self.call("control.update", {"version": control["version"], "kind": "collaboration",
                                                  "agents": pair, "enabled": True})
            group = result["group"]
            self.call("board.post", {"board": group["board"], "text": f"Pair {index}: isolated model-free collaboration board."})
            self.call("memory.put", {"namespace": group["memory"], "key": "proposal",
                                     "value": f"Pair {index}: separate memory, same key.", "version": 0})
        self.web = subprocess.Popen([str(self.binary), "web", "--state", str(self.state), "--port", str(self.options.port)],
                                    stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        deadline = time.monotonic() + 10
        startup_output = ""
        while time.monotonic() < deadline:
            ready, _, _ = select.select([self.web.stdout], [], [], 1)
            if ready:
                startup_output += os.read(self.web.stdout.fileno(), 4096).decode()
                links = [line.strip() for line in startup_output.splitlines() if line.startswith("http://")]
                if links:
                    self.web_url = links[0]
                    break
            if self.web.poll() is not None:
                raise RuntimeError(f"web stopped: {self.web.stderr.read()}")
        if not self.web_url:
            raise RuntimeError("web did not print a private URL")
        parsed = urllib.parse.urlsplit(self.web_url)
        self.base_url, self.token = f"{parsed.scheme}://{parsed.netloc}", parsed.fragment
        self.expand(self.options.registry_agents)
        self.measure()
        self.report["ok"] = True
        self.save_report()
        self.emit("ready", state=str(self.state), root=str(self.root), private_url=self.web_url,
                  live_ptys=self.options.live_agents, registry=len(self.registry()),
                  synthetic_registry_only=self.report["synthetic_registry_only"],
                  commands=["status", "measure", "expand 200", "quit"])

    def expand(self, target):
        registry = self.registry()
        if target < len(registry) or target > 200:
            raise ValueError("registry target must be between its current size and 200")
        started = time.monotonic()
        for number in range(len(registry) + 1, target + 1):
            name = f"registry{number:03d}"
            project = self.root / "synthetic" / name
            project.mkdir(parents=True, mode=0o700)
            self.call("agent.add", {"id": name, "title": f"Synthetic registry row {number:03d} — no process",
                                    "external": True, "external_ref": f"scale-fixture:synthetic:{number}",
                                    "config": {"harness": "command", "interaction": "interactive", "command": ["/bin/true"],
                                               "workdir": str(project), "sandbox": "local", "network": "host"}})
        actual = len(self.registry())
        if actual != target:
            raise AssertionError(f"requested {target} registry definitions, got {actual}")
        self.report["registry_definitions"] = actual
        self.report["synthetic_registry_only"] = actual - self.options.live_agents
        if actual == 200:
            project = self.root / "rejected-project"; project.mkdir(exist_ok=True, mode=0o700)
            self.call("agent.add", {"id": "rejected201", "external": True, "external_ref": "scale-fixture:rejected",
                                    "config": {"harness": "command", "interaction": "interactive", "command": ["/bin/true"],
                                               "workdir": str(project), "sandbox": "local", "network": "host"}}, expected_fault="limit")
            if (self.state / "credentials" / "rejected201.json").exists() or len(self.registry()) != 200:
                raise AssertionError("201st rejection mutated registry or created credentials")
            self.report["registry_201st_rejected_without_mutation"] = True
        self.save_report()
        self.emit("expanded", registry=actual, live_ptys=self.options.live_agents,
                  synthetic_registry_only=self.report["synthetic_registry_only"], seconds=round(time.monotonic()-started, 3))

    def measure(self):
        values = []
        size = 0
        expected_count = len(self.registry())
        for _ in range(5):
            started = time.monotonic()
            request = urllib.request.Request(self.base_url + "/api/snapshot?agent=fixture001",
                                             headers={"Authorization": "Bearer " + self.token})
            with urllib.request.urlopen(request, timeout=15) as response:
                data = response.read()
            value = json.loads(data)
            if not value.get("ok") or len(value["agents"]) != expected_count:
                raise AssertionError("web snapshot lost registry definitions")
            values.append(round((time.monotonic() - started)*1000, 1));size = len(data)
        measurement = {"registry_definitions": len(self.registry()), "live_ptys": self.live_pty_count(),
                       "snapshot_ms": values, "snapshot_bytes": size,
                       "graph_display_cap": 40, "graph_cap_source": "web frontend documented cap; browser rendering measured separately"}
        self.report["measurements"].append(measurement)
        self.save_report();self.emit("measurement", **measurement)

    def admission_after_static_revoke(self):
        """Separate process-free broker: verify actual unique IDs at admission."""
        state = self.root / "admission"
        project = self.root / "admission-project";project.mkdir(mode=0o700)
        config = {"version": 2, "mode": "managed", "session": "admission", "interactive_workspace": True,
                  "agents": {name: {"harness": "command", "interaction": "interactive", "command": ["/bin/cat"]}
                             for name in ("staticone", "statictwo")}}
        path = self.root / "admission.json";path.write_text(json.dumps(config))
        self.run("managed", "init", path, "--state", state)
        broker = subprocess.Popen([str(self.binary), "managed", "serve", "--state", str(state)],
                                  stdout=subprocess.DEVNULL, stderr=subprocess.PIPE, text=True)
        try:
            deadline = time.monotonic() + 5
            while not (state / "broker.sock").exists():
                if broker.poll() is not None:raise RuntimeError(broker.stderr.read())
                if time.monotonic()>deadline:raise RuntimeError("admission broker not ready")
                time.sleep(.02)
            for name in ("staticone", "statictwo"):
                self.call("agent.label", {"id": name, "title": "Admission fixture", "version": 0}, state=state)
            self.call("agent.stop", {"id": "staticone"}, state=state)
            definition = {"harness": "command", "interaction": "interactive", "command": ["/bin/true"],
                          "workdir": str(project), "sandbox": "local", "network": "host"}
            for number in range(197):
                self.call("agent.add", {"id": f"admission{number:03d}", "external": True,
                                        "external_ref": f"admission:{number}", "config": definition}, state=state)
            def competing_add(number):
                name = f"lastslot{number}"
                process = subprocess.run([str(self.binary), "managed", "call", "agent.add", "--credential-file", str(state / "owner.json"),
                                          "--json", json.dumps({"id": name,"external": True,"external_ref":name,"config":definition})],
                                         capture_output=True,text=True,timeout=15)
                return name,json.loads(process.stdout)
            with concurrent.futures.ThreadPoolExecutor(max_workers=2) as pool:
                outcomes = list(pool.map(competing_add, (1, 2)))
            succeeded = [name for name, value in outcomes if value.get("ok")]
            rejected = [name for name, value in outcomes if value.get("error",{}).get("code")=="limit"]
            count = len(self.registry(state))
            if len(succeeded)!=1 or len(rejected)!=1 or count!=200:
                raise AssertionError(f"static-revoke admission counted IDs incorrectly: count={count}, success={succeeded}, limit={rejected}")
            if (state / "credentials" / (rejected[0]+".json")).exists():
                raise AssertionError("atomic admission rejection retained credentials")
            self.report["revoked_static_unique_admission"] = {"registry_definitions":count,"concurrent_accepts":1,"concurrent_rejections":1,"actual_native_processes":0}
            self.save_report();self.emit("admission_checked", **self.report["revoked_static_unique_admission"])
        finally:
            if broker.poll() is None:broker.terminate()
            broker.communicate(timeout=10)

    def close(self):
        if self.web and self.web.poll() is None:
            self.web.terminate()
            try:self.web.communicate(timeout=5)
            except subprocess.TimeoutExpired:self.web.kill();self.web.communicate()
        if (self.state / "runtime.json").exists():
            try:self.run("managed", "down", "--state", self.state, timeout=45)
            except Exception as error:self.report["cleanup_error"] = str(error)
        self.save_report()
        if self.options.keep_output:
            self.emit("artifacts_retained", root=str(self.root), report=str(self.root / "report.json"))
        elif "cleanup_error" not in self.report:
            shutil.rmtree(self.root)


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--binary", default="bin/agentmux")
    parser.add_argument("--live-agents", type=int, default=40)
    parser.add_argument("--registry-agents", type=int, default=None)
    parser.add_argument("--sandbox", choices=("bubblewrap", "local"), default="bubblewrap")
    parser.add_argument("--port", type=int, default=0)
    parser.add_argument("--serve", action="store_true")
    parser.add_argument("--check-admission", action="store_true", help="also test200 uniqueIDs after static revocation, with no native processes")
    parser.add_argument("--keep-output", action="store_true")
    options = parser.parse_args()
    if not 1 <= options.live_agents <= 40:parser.error("--live-agents must be between1 and40")
    if options.registry_agents is None:options.registry_agents=options.live_agents
    if not options.live_agents <= options.registry_agents <= 200:parser.error("registry count must be between live count and200")
    if not 0<=options.port<=65535:parser.error("invalid web port")
    fixture = Fixture(options)
    try:
        fixture.start()
        if options.check_admission:fixture.admission_after_static_revoke()
        if options.serve:
            for line in sys.stdin:
                parts=line.split()
                if not parts:continue
                try:
                    if parts==["quit"]:break
                    if parts==["status"]:fixture.emit("status",state=str(fixture.state),registry=len(fixture.registry()),live_ptys=fixture.live_pty_count())
                    elif parts==["measure"]:fixture.measure()
                    elif len(parts)==2 and parts[0]=="expand":fixture.expand(int(parts[1]));fixture.measure()
                    else:fixture.emit("command_error",message="Use status, measure, expand N, or quit. Broker failure injection is handled separately; the supervisor intentionally stops workers on prolonged broker loss.")
                except Exception as error:fixture.emit("command_error",message=str(error))
    except KeyboardInterrupt:
        pass
    except Exception as error:
        fixture.report["ok"]=False;fixture.report["error"]=str(error);fixture.save_report()
        raise
    finally:
        fixture.close()


if __name__ == "__main__":
    main()
