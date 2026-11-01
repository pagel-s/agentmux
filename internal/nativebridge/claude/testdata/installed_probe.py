#!/usr/bin/env python3
"""Opt-in real Claude TUI contract, synthetic loopback model, isolated profile.

Invoked by TestInstalledClaudeNativeInboxPreservesDraftAndApprovals. The Go
subprocesses call the production Run and RunHook; no user configuration, real
credentials, real models or already-running sessions are involved.
"""
import fcntl
import http.server
import json
import os
import pathlib
import pty
import select
import shutil
import signal
import socket
import struct
import subprocess
import sys
import termios
import threading
import time

helper, binary, directory = sys.argv[1:]
root = pathlib.Path(directory)
for name in ("home", "config", "project"):
    (root / name).mkdir(mode=0o700)
requests = []


class Handler(http.server.BaseHTTPRequestHandler):
    def log_message(self, *_args):
        pass

    def do_GET(self):
        self.send_response(200)
        self.end_headers()
        self.wfile.write(b"{}")

    def do_POST(self):
        body = json.loads(self.rfile.read(int(self.headers.get("content-length", 0))))
        if "count_tokens" in self.path:
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.end_headers()
            self.wfile.write(b'{"input_tokens":100}')
            return
        if "/messages" not in self.path:
            self.do_GET()
            return
        requests.append(body)
        approval = len(requests) >= 3
        block = ({"type": "tool_use", "id": "tool_fixture_approval", "name": "Bash", "input": {}}
                 if approval else {"type": "text", "text": ""})
        delta = ({"type": "input_json_delta", "partial_json": json.dumps({
            "command": "touch " + str(root / "APPROVAL_MUST_NOT_EXIST"),
            "description": "Fixture requires human approval"})}
                 if approval else {"type": "text_delta", "text": "FIXTURE_REPLY_NATIVE_DELIVERY"})
        stop = "tool_use" if approval else "end_turn"
        message = {"id": "msg_fixture_" + str(len(requests)), "type": "message", "role": "assistant",
                   "model": body.get("model", "fixture"), "content": [], "stop_reason": None,
                   "stop_sequence": None, "usage": {"input_tokens": 100, "output_tokens": 0}}
        events = [
            ("message_start", {"type": "message_start", "message": message}),
            ("content_block_start", {"type": "content_block_start", "index": 0, "content_block": block}),
            ("content_block_delta", {"type": "content_block_delta", "index": 0, "delta": delta}),
            ("content_block_stop", {"type": "content_block_stop", "index": 0}),
            ("message_delta", {"type": "message_delta", "delta": {"stop_reason": stop, "stop_sequence": None},
                               "usage": {"output_tokens": 10}}),
            ("message_stop", {"type": "message_stop"}),
        ]
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.end_headers()
        for kind, event in events:
            self.wfile.write(("event: " + kind + "\ndata: " + json.dumps(event) + "\n\n").encode())
            self.wfile.flush()


api = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Handler)
threading.Thread(target=api.serve_forever, daemon=True).start()
settings = {"permissions": {"defaultMode": "default", "ask": ["Bash"]}}
(root / "settings.json").write_text(json.dumps(settings))
config = {"hasCompletedOnboarding": True, "theme": "dark",
          "customApiKeyResponses": {"approved": ["fixture-key"]},
          "projects": {str(root / "project"): {"hasTrustDialogAccepted": True,
                                                 "hasCompletedProjectOnboarding": True}}}
for path in (root / "config" / ".claude.json", root / "config.json", root / "home" / ".claude.json"):
    path.write_text(json.dumps(config))

# The helper is an executable command plus literal args, including when the Go
# test binary lives in a path containing shell metacharacters.
def quote(text):
    return "'" + text.replace("'", "'\\''") + "'"

wrapper = root / "hook helper"
wrapper.write_text("#!/bin/sh\nexec " + quote(helper) + " -test.run='^TestInstalledClaudeHookHelper$'\n")
wrapper.chmod(0o700)
env = {key: value for key, value in os.environ.items()
       if not key.startswith(("ANTHROPIC_", "CLAUDE_", "AGENTMUX_", "AWS_", "GOOGLE_", "AZURE_"))}
env.update(HOME=str(root / "home"), CLAUDE_CONFIG_DIR=str(root / "config"),
           ANTHROPIC_API_KEY="fixture-key", ANTHROPIC_BASE_URL="http://127.0.0.1:" + str(api.server_port),
           DISABLE_TELEMETRY="1", CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC="1", DISABLE_AUTOUPDATER="1",
           TERM="xterm-256color", NO_PROXY="127.0.0.1,localhost", AGENTMUX_CLI=str(wrapper),
           AGENTMUX_CLAUDE_FIXTURE_RUN="1", AGENTMUX_CLAUDE_FIXTURE_HOOK="1",
           AGENTMUX_CLAUDE_FIXTURE_DIR=str(root), AGENTMUX_CLAUDE_FIXTURE_BINARY=binary,
           AGENTMUX_CLAUDE_FIXTURE_SETTINGS=str(root / "settings.json"))
master, slave = pty.openpty()
fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 35, 120, 0, 0))


def terminal_session():
    os.setsid()
    fcntl.ioctl(slave, termios.TIOCSCTTY, 0)


process = subprocess.Popen([helper, "-test.run=^TestInstalledClaudePTYHelper$"],
                           stdin=slave, stdout=slave, stderr=slave, cwd=root / "project",
                           env=env, preexec_fn=terminal_session)
os.close(slave)
raw = bytearray()


def drain(seconds):
    end = time.monotonic() + seconds
    while time.monotonic() < end:
        if select.select([master], [], [], .05)[0]:
            try:
                chunk = os.read(master, 65536)
            except OSError:
                break
            raw.extend(chunk)
            if b"\x1b[6n" in chunk:
                os.write(master, b"\x1b[1;1R")


def until(predicate, message, timeout=10):
    deadline = time.monotonic() + timeout
    while not predicate():
        if process.poll() is not None or time.monotonic() > deadline:
            raise AssertionError(message + "\n" + raw[-10000:].decode(errors="replace"))
        drain(.1)


def post(content, identifier):
    info = json.loads((root / "hook-private.json").read_text())
    with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as peer:
        peer.connect(info["socket"])
        peer.sendall((json.dumps({"type": "auth", "token": info["token"]}) + "\n").encode())
        peer.sendall((json.dumps({"type": "user", "session_id": info["session_id"], "uuid": identifier,
                                 "priority": "later", "message": {"role": "user", "content": content}}) + "\n").encode())


try:
    until(lambda: (root / "bridge-events.jsonl").exists(), "production SessionStart bridge did not register")
    until(lambda: b"manual" in raw.lower(), "native terminal did not reach its manual prompt")
    os.write(master, b"HUMAN_DRAFT_RETAIN")
    drain(.4)
    (root / "allow-delivery").touch()
    until(lambda: len(requests) >= 1 and b"FIXTURE_REPLY_NATIVE_DELIVERY" in raw,
          "production native delivery did not wake the idle conversation")
    assert "AGENTMUX_INBOUND_FIXTURE_1" in json.dumps(requests[0])
    assert "HUMAN_DRAFT_RETAIN" not in json.dumps(requests[0]), "delivery submitted the human's unsent draft"
    drain(.5)
    events = [json.loads(line) for line in (root / "bridge-events.jsonl").read_text().splitlines()]
    phases = [event["phase"] for event in events if "phase" in event]
    assert phases == ["submitted"], "socket write invented a model acknowledgement/completion: " + str(phases)
    os.write(master, b"\r")
    until(lambda: len(requests) >= 2, "native human draft was lost after peer delivery")
    assert "HUMAN_DRAFT_RETAIN" in json.dumps(requests[1]), "Enter did not submit the original human draft"
    drain(1)
    # Exercise the same native queue independently while a real native tool
    # permission prompt is open. No permission response is ever submitted.
    post("AGENTMUX_REQUIRE_PERMISSION_FIXTURE", "9b397f26-7ffb-51c3-b2f3-94d4fcb18328")
    until(lambda: len(requests) >= 3 and b"APPROVAL_MUST_NOT_EXIST" in raw and b"Yes" in raw,
          "fixture did not reach a real native approval prompt")
    before = len(requests)
    post("AGENTMUX_QUEUED_WHILE_APPROVAL_PENDING", "d54c7c40-2cc6-522c-b96b-2a739cb197df")
    drain(2)
    assert not (root / "APPROVAL_MUST_NOT_EXIST").exists(), "native delivery approved a pending tool call"
    assert len(requests) == before, "queued delivery displaced a pending approval"
    print("Real Claude TUI: production hook registered; native peer turn woke idle; human draft survived; pending tool approval stayed unanswered; submission was not misreported as completion.")
finally:
    try:
        os.killpg(process.pid, signal.SIGTERM)
    except ProcessLookupError:
        pass
    try:
        process.wait(timeout=4)
    except subprocess.TimeoutExpired:
        os.killpg(process.pid, signal.SIGKILL)
        process.wait()
    os.close(master)
    api.shutdown()
    # A SIGTERM of the whole test process group can bypass Go defers. Remove
    # only the invocation directory recorded by this test's private hook.
    private = root / "hook-private.json"
    if private.exists():
        bridge = pathlib.Path(json.loads(private.read_text()).get("bridge_dir", ""))
        if bridge.is_absolute() and bridge.name.startswith("amx-claude-"):
            shutil.rmtree(bridge, ignore_errors=True)
