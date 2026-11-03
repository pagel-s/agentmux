#!/usr/bin/env python3
"""Real tmux PTY + isolated broker/terminals + live controls; no model calls."""
import argparse
import json
import fcntl
import pty
import struct
import termios
import os
from pathlib import Path
import signal
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", default="bin/amx")
    parser.add_argument("--keep-output", action="store_true")
    options = parser.parse_args()
    binary = str(Path(options.binary).resolve(strict=True))
    root = tempfile.TemporaryDirectory(prefix="amx-interactive-")
    state = Path(root.name) / "s"
    web = None
    attached = None
    terminal_fd = None
    extra_clients = []
    foreign_session = False
    started = False

    def run(*args, expected=0, env=None):
        process = subprocess.run([binary, *map(str, args)], capture_output=True, text=True, timeout=40, env=env)
        if process.returncode != expected:
            raise RuntimeError(f"CLI {args[0]} returned {process.returncode}: {process.stdout} {process.stderr}")
        return process.stdout

    def cli(*args):
        return json.loads(run("managed", *args))["result"]

    def call(operation, arguments, expected=0):
        output = run("managed", "call", operation, "--credential-file", state / "owner.json", "--json", json.dumps(arguments), expected=expected)
        return json.loads(output)

    def tmux(*args):
        return subprocess.check_output(["tmux", "-S", str(state / "tmux.sock"), *args], text=True, timeout=5)

    def until(predicate):
        deadline = time.monotonic() + 10
        while time.monotonic() < deadline:
            if predicate():
                return
            time.sleep(.05)
        raise RuntimeError("interactive condition was not observed")

    script = r'''test -t 0 && test -t 1 || exit 7
test ! -e "$1/owner.json" || exit 8
printf 'NATIVE_TTY_READY\n'
while IFS= read -r line; do
  case "$line" in
    inbox) "$AGENTMUX_CLI" managed call message.list ;;
    peer) if "$AGENTMUX_CLI" managed call message.send --json '{"to":["worker"],"text":"peer note"}' >/dev/null; then printf 'PEER_ALLOWED\n'; else printf 'PEER_DENIED\n'; fi ;;
    *) printf 'HUMAN_RECEIVED:%s\n' "$line" ;;
  esac
done
'''
    config = {"version": 2, "mode": "managed", "session": "interactive-smoke",
        "agents": {name: {"harness": "command", "interaction": "interactive", "sandbox": "bubblewrap",
                          "network": "none", "command": ["/bin/sh", "-c", script, name, str(state)]}
                   for name in ("lead", "worker")},
        "boards": {"team": {"readers": ["*"], "writers": ["*"]}},
        "memory": {"project": {"readers": ["*"], "writers": ["*"]}},
        "policy": {"default": "deny", "grants": [{"from": ["lead"], "to": ["worker"], "actions": ["message.send"]}]},
        "limits": {"max_runtime_seconds": 120}}
    config_path = Path(root.name) / "team.json"
    config_path.write_text(json.dumps(config))
    try:
        runtime = cli("up", config_path, "--state", state, "--broker-sandbox", "bubblewrap")
        started = True
        panes = {name: runtime["agents"][name]["pane_id"] for name in ("lead", "worker")}
        capture = lambda name: tmux("capture-pane", "-p", "-t", panes[name], "-S", "-100")
        try:
            for name in panes:
                until(lambda name=name: "NATIVE_TTY_READY" in capture(name))
        except Exception:
            # These panes run only the synthetic fixture above. Preserve its
            # actual startup error before ordered cleanup removes the panes.
            for name, pane in panes.items():
                print(f"Native fixture startup diagnostics: {name} ({pane})", file=sys.stderr)
                try:
                    print(capture(name), file=sys.stderr)
                except Exception as error:
                    print(f"Could not capture fixture: {error}", file=sys.stderr)
            raise
        run("send", "worker", "INBOX_ONLY_SENTINEL", "--state", state)
        assert "INBOX_ONLY_SENTINEL" not in capture("worker"), "message was injected into the native terminal"
        assert call("task.list", {})["result"]["tasks"] == [], "interactive message created a managed task"
        tmux("send-keys", "-t", panes["worker"], "-l", "human typed input")
        tmux("send-keys", "-t", panes["worker"], "Enter")
        until(lambda: "HUMAN_RECEIVED:human typed input" in capture("worker"))
        tmux("send-keys", "-t", panes["worker"], "-l", "inbox")
        tmux("send-keys", "-t", panes["worker"], "Enter")
        until(lambda: "INBOX_ONLY_SENTINEL" in capture("worker"))
        for expected, allow, version in (("PEER_ALLOWED", None, 1), ("PEER_DENIED", False, 1), ("PEER_ALLOWED", True, 2)):
            if allow is not None:
                call("control.update", {"version": version, "kind": "edge", "from": "lead", "to": "worker", "action": "message.send", "allow": allow})
            previous = capture("lead").count(expected)
            tmux("send-keys", "-t", panes["lead"], "-l", "peer")
            tmux("send-keys", "-t", panes["lead"], "Enter")
            until(lambda expected=expected, previous=previous: capture("lead").count(expected) > previous)
        env = dict(os.environ, TMUX=str(state / "tmux.sock") + ",1,0")
        run("attach", "lead", "--state", state, env=env)
        assert tmux("display-message", "-p", "#{window_name}").strip() == "agent-lead"
        web = subprocess.Popen([binary, "web", "--state", str(state)], stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        web.stdout.readline()
        url = web.stdout.readline().strip()
        parsed = urllib.parse.urlsplit(url)
        base = f"{parsed.scheme}://{parsed.netloc}"
        def request(path, data=None, authenticated=True):
            headers = {"Authorization": "Bearer " + parsed.fragment} if authenticated else {}
            if data is not None:
                headers["Content-Type"] = "application/json"
                data = json.dumps(data).encode()
            req = urllib.request.Request(base + path, data=data, headers=headers)
            with urllib.request.urlopen(req, timeout=10) as response:
                return response.read()
        assert b"agentmux" in request("/", authenticated=False)
        snapshot = json.loads(request("/api/snapshot?agent=worker"))
        assert snapshot["ok"] and snapshot["boundaries"]["version"] == 3
        try:
            request("/api/snapshot", authenticated=False)
        except urllib.error.HTTPError as error:
            assert error.code == 401
        else:
            raise AssertionError("web API accepted an unauthenticated caller")
        response = json.loads(request("/api/call", {"operation": "control.update", "args": {"version": 3, "kind": "memory", "agent": "worker", "namespace": "project", "read": False, "write": False}}))
        assert response["ok"] and response["result"]["version"] == 4
        # Exercise the same key sequences a human uses in a real tmux client.
        terminal_fd, slave = pty.openpty()
        fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 36, 120, 0, 0))
        first_tty = os.ttyname(slave)
        attached = subprocess.Popen([binary, "attach", "lead", "--state", str(state)],
            stdin=slave, stdout=slave, stderr=slave, env=dict(os.environ, TERM="xterm-256color"))
        os.close(slave)
        os.set_blocking(terminal_fd, False)
        def drain():
            try:
                while os.read(terminal_fd, 65536):
                    pass
            except BlockingIOError:
                pass
        def client_ready():
            drain()
            return bool(tmux("list-clients", "-F", "#{client_name}").strip())
        until(client_ready)
        def client_rows():
            return [line.split("\t") for line in tmux("list-clients", "-F", "#{client_tty}\t#{client_session}").splitlines()]
        first_view = next(session for tty, session in client_rows() if tty == first_tty)
        assert first_view != runtime["session"] and first_view.startswith("view_")
        second_fd, second_slave = pty.openpty()
        fcntl.ioctl(second_slave, termios.TIOCSWINSZ, struct.pack("HHHH", 36, 120, 0, 0))
        second_tty = os.ttyname(second_slave)
        second = subprocess.Popen([binary, "attach", "worker", "--state", str(state)],
            stdin=second_slave, stdout=second_slave, stderr=second_slave, env=dict(os.environ, TERM="xterm-256color"))
        os.close(second_slave)
        os.set_blocking(second_fd, False)
        extra_clients.append((second, second_fd))
        until(lambda: any(tty == second_tty for tty, _ in client_rows()))
        second_view = next(session for tty, session in client_rows() if tty == second_tty)
        assert first_view != second_view and second_view != runtime["session"], "terminal clients shared a selected-window view"
        def current_window(view):
            return tmux("display-message", "-p", "-t", view + ":", "#{window_name}").strip()
        assert current_window(first_view) == "agent-lead"
        assert current_window(second_view) == "agent-worker"
        os.write(terminal_fd, b"\x1bOP")  # F1
        until(lambda: current_window(first_view) == "dashboard")
        assert current_window(second_view) == "agent-worker", "F1 moved another terminal client"
        until(lambda: "AGENTMUX" in tmux("capture-pane", "-p", "-t", runtime["dashboard_pane"]))
        first_session_id = tmux("display-message", "-p", "-t", first_view, "#{session_id}").strip().lstrip("$")
        first_env = dict(os.environ, TMUX=str(state / "tmux.sock") + ",1," + first_session_id)
        run("attach", "lead", "--state", state, env=first_env)
        assert current_window(first_view) == "agent-lead", "contextual attach focused the wrong client"
        assert current_window(second_view) == "agent-worker", "contextual attach moved another terminal client"
        drain()
        os.write(terminal_fd, b"\x02b")  # prefix, board popup
        # Popup contents are in the client screen, outside normal window panes.
        popup_output = bytearray()
        def popup_ready():
            try:
                popup_output.extend(os.read(terminal_fd, 65536))
            except BlockingIOError:
                pass
            return b"AGENTMUX" in popup_output and b"Board" in popup_output and b"peer note" in popup_output
        try:
            until(popup_ready)
        except RuntimeError:
            (Path(root.name) / "popup-screen.bin").write_bytes(popup_output)
            raise
        os.write(terminal_fd, b"b")
        # Wait for composer before typing; the owner view uses asynchronous polling.
        popup_output.clear()
        def composer_ready():
            try:
                popup_output.extend(os.read(terminal_fd, 65536))
            except BlockingIOError:
                pass
            return b"Enter submits" in popup_output
        until(composer_ready)
        os.write(terminal_fd, b"POPUP_BOARD_SENTINEL\r")
        until(lambda: any(m["text"] == "POPUP_BOARD_SENTINEL" for m in call("activity", {"limit":40})["result"]["messages"]))
        popup_output.clear()
        os.write(terminal_fd, b"q")
        def native_visible():
            try:
                popup_output.extend(os.read(terminal_fd, 65536))
            except BlockingIOError:
                pass
            return b"NATIVE_TTY_READY" in popup_output
        until(native_visible)
        assert current_window(first_view) == "agent-lead"
        for key, marker in ((b"\x02P", b"Workspace groups"), (b"\x02m", b"Shared boundaries"), (b"\x02t", b"No managed tasks yet")):
            drain()
            popup_output.clear()
            os.write(terminal_fd, key)
            def other_popup_ready(marker=marker):
                try:
                    popup_output.extend(os.read(terminal_fd, 65536))
                except BlockingIOError:
                    pass
                return marker in popup_output
            until(other_popup_ready)
            assert current_window(second_view) == "agent-worker", "popup moved the other terminal client"
            popup_output.clear()
            os.write(terminal_fd, b"q")
            until(native_visible)
        os.write(terminal_fd, b"\x1bOP")
        until(lambda: current_window(first_view) == "dashboard")
        os.write(terminal_fd, b"q")
        until(lambda: attached.poll() is not None)
        until(lambda: first_view not in tmux("list-sessions", "-F", "#{session_name}").splitlines())
        assert current_window(second_view) == "agent-worker", "detaching one client removed another view"
        assert tmux("display-message", "-p", "-t", runtime["dashboard_pane"], "#{pane_dead}").strip() == "0", "detaching killed the workbench"
        # Down must remove surviving owned view groups and their shared workers,
        # while preserving unrelated sessions on this same dedicated server.
        tmux("new-session", "-d", "-s", "unrelated-owner-shell", "exec sleep 300")
        tmux("set-option", "-t", "unrelated-owner-shell", "@agentmux_view", "foreign-attempt")
        foreign_session = True
        def process_identity(pid):
            try:
                fields = Path(f"/proc/{pid}/stat").read_text().rsplit(") ", 1)[1].split()
                return fields[19], fields[0]  # Linux start time and process state.
            except (FileNotFoundError, ProcessLookupError):
                return None
        worker_processes = {}
        pending_pids = [int(tmux("display-message", "-p", "-t", pane, "#{pane_pid}")) for pane in panes.values()]
        while pending_pids:
            pid = pending_pids.pop()
            identity = process_identity(pid)
            if identity is None or pid in worker_processes:
                continue
            worker_processes[pid] = identity[0]
            try:
                pending_pids.extend(map(int, Path(f"/proc/{pid}/task/{pid}/children").read_text().split()))
            except FileNotFoundError:
                pass
        assert len(worker_processes) >= len(panes), "could not observe owned worker processes"
        cli("down", "--state", state)
        started = False
        until(lambda: second.poll() is not None)
        remaining = tmux("list-sessions", "-F", "#{session_name}").splitlines()
        assert remaining == ["unrelated-owner-shell"], "down left owned views or removed a foreign session"
        remaining_panes = tmux("list-panes", "-a", "-F", "#{pane_id}").splitlines()
        assert not any(pane in remaining_panes for pane in panes.values()), "down left a shared worker pane alive"
        def owned_processes_stopped():
            for pid, start_time in worker_processes.items():
                identity = process_identity(pid)
                if identity is not None and identity[0] == start_time and identity[1] != "Z":
                    return False
            return True
        until(owned_processes_stopped)
        tmux("kill-session", "-t", "unrelated-owner-shell")
        foreign_session = False
        print(json.dumps({"ok": True, "model_calls": 0, "agent_sandboxes": 2, "broker_sandbox": "bubblewrap",
            "native_tty_input": True, "inbox_does_not_inject": True, "live_permission_revoke_and_restore": True,
            "attach_focus": True, "independent_terminal_clients": True, "contextual_focus": True, "owned_view_cleanup": True, "owned_process_tree_stopped": True,
            "terminal_dashboard": True, "workbench_hotkey": True, "all_popup_shortcuts": True, "board_popup_composition": True, "detach_keeps_workbench": True, "authenticated_web_controls": True}, indent=2))
    finally:
        if attached and attached.poll() is None:
            attached.terminate()
            attached.wait(timeout=5)
        if terminal_fd is not None:
            os.close(terminal_fd)
        for client, fd in extra_clients:
            if client.poll() is None:
                client.terminate()
                client.wait(timeout=5)
            os.close(fd)
        if web:
            web.send_signal(signal.SIGTERM)
            web.communicate(timeout=10)
        if started or (state / "runtime.json").exists():
            cli("down", "--state", state)
        if foreign_session:
            tag = subprocess.run(["tmux", "-S", str(state / "tmux.sock"), "show-options", "-v", "-t", "unrelated-owner-shell", "@agentmux_view"], capture_output=True, text=True)
            if tag.returncode == 0 and tag.stdout.strip() == "foreign-attempt":
                tmux("kill-session", "-t", "unrelated-owner-shell")
        if options.keep_output:
            root._finalizer.detach()
            print("Private interactive artifacts retained at " + root.name)
        else:
            root.cleanup()


if __name__ == "__main__":
    main()
