#!/usr/bin/env python3
"""Install a checksum-verified local release and complete its model-free first run.

Creates a new private evidence directory. Never calls a provider or updates an
existing installation. All four archives are checked; only the host binary runs.
"""
import argparse
import hashlib
import importlib.util
import io
import json
import os
from pathlib import Path
import platform
import re
import selectors
import signal
import stat
import subprocess
import sys
import tarfile
import time

spec = importlib.util.spec_from_file_location("release_installer", Path(__file__).with_name("install-release.py"))
installer = importlib.util.module_from_spec(spec)
spec.loader.exec_module(installer)
ARCHIVES = {f"agentmux_{system}_{arch}.tar.gz" for system in ("linux", "darwin") for arch in ("x86_64", "arm64")}
REQUIRED_FILES = {"amx", "README.md", "CONTRIBUTING.md", "SECURITY.md", "docs/getting-started/first-result.md", "scripts/install-release.py"}


def check_archives(directory):
    manifest = installer.read_bounded(directory / "checksums.txt", 1024 * 1024)
    entries = {}
    for line in manifest.decode("ascii").splitlines():
        fields = line.split()
        if len(fields) != 2 or not re.fullmatch(r"[a-fA-F0-9]{64}", fields[0]):
            raise ValueError("invalid checksum manifest")
        name = fields[1].removeprefix("*")
        if name in entries or name not in ARCHIVES:
            raise ValueError("unexpected or duplicate release asset in manifest")
        entries[name] = fields[0].lower()
    if set(entries) != ARCHIVES:
        raise ValueError("manifest must name all four platform archives")
    for name, expected in entries.items():
        payload = installer.read_bounded(directory / name, 128 * 1024 * 1024)
        if hashlib.sha256(payload).hexdigest() != expected:
            raise ValueError("release checksum mismatch: " + name)
        with tarfile.open(fileobj=io.BytesIO(installer.bounded_tar(payload)), mode="r:") as archive:
            files = {}
            for member in archive:
                if member.name in files:
                    raise ValueError("duplicate archive member: " + member.name)
                files[member.name] = member
            if not REQUIRED_FILES.issubset(files) or any(not files[name].isfile() for name in REQUIRED_FILES):
                raise ValueError("archive lacks required executable or first-run documentation: " + name)
    return entries, manifest


def run_command(argv, cwd, timeout=120):
    """Bound both diagnostic output and descendant lifetime for the smoke command."""
    process = subprocess.Popen(argv, cwd=cwd, stdout=subprocess.PIPE, stderr=subprocess.PIPE, start_new_session=True)
    selector = selectors.DefaultSelector()
    selector.register(process.stdout, selectors.EVENT_READ)
    selector.register(process.stderr, selectors.EVENT_READ)
    output = bytearray()
    deadline = time.monotonic() + timeout
    try:
        while selector.get_map():
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                raise ValueError("installed command timed out")
            for key, _ in selector.select(remaining):
                block = os.read(key.fileobj.fileno(), 8192)
                if not block:
                    selector.unregister(key.fileobj)
                    continue
                output.extend(block)
                if len(output) > 1024 * 1024:
                    raise ValueError("installed command output exceeds bound")
        try:
            code = process.wait(timeout=max(0.001, deadline - time.monotonic()))
        except subprocess.TimeoutExpired:
            raise ValueError("installed command timed out") from None
        if code:
            raise ValueError("installed command failed: " + " ".join(argv[1:]) + "\n" + output.decode("utf-8", "replace"))
        return output.decode("utf-8", "replace")
    finally:
        try:
            os.killpg(process.pid, signal.SIGKILL)
        except ProcessLookupError:
            pass
        process.wait(timeout=5)
        selector.close()
        process.stdout.close()
        process.stderr.close()


def verify_demo(root):
    root = root.resolve(strict=True)
    demo = json.loads((root / "demo.json").read_text())
    if (demo.get("state") != "completed" or demo.get("phase") != "finished"
            or demo.get("applied") is not True or demo.get("error")
            or demo.get("repository") != str(root / "repository")
            or demo.get("run") != str(root / "run")):
        raise ValueError("demo lacks completed application evidence")
    report = json.loads((root / "run" / "report.json").read_text())
    candidates = report.get("candidates", [])
    if report.get("state") != "completed" or len(candidates) != 1:
        raise ValueError("demo did not complete one candidate")
    candidate = candidates[0]
    verification = candidate.get("verification") or {}
    snapshot = candidate.get("final_snapshot") or {}
    if (candidate.get("state") != "verified" or verification.get("passed") is not True
            or not verification.get("tree") or verification.get("tree") != verification.get("ending_tree")
            or verification.get("tree") != snapshot.get("tree")
            or not verification.get("content_sha256")
            or verification.get("content_sha256") != verification.get("ending_content_sha256")
            or verification.get("content_sha256") != snapshot.get("content_sha256")
            or candidate.get("id") != "fixture"
            or not re.fullmatch(r"[0-9a-f]{64}", candidate.get("patch_sha256", ""))):
        raise ValueError("demo result lacks passing revision-bound checks")
    checks = verification.get("checks", [])
    if (len(checks) != 1 or checks[0].get("name") != "expected fixture text"
            or checks[0].get("passed") is not True or type(checks[0].get("exit_code")) is not int
            or checks[0]["exit_code"] != 0 or checks[0].get("error")):
        raise ValueError("demo lacks the independently passing fixture check")
    patch_path = root / "run" / "candidates" / "fixture" / "changes.patch"
    if candidate.get("patch") != str(patch_path) or not stat.S_ISREG(patch_path.lstat().st_mode):
        raise ValueError("demo patch does not identify the retained fixture patch")
    patch = installer.read_bounded(patch_path, 65536)
    if not patch or hashlib.sha256(patch).hexdigest() != candidate["patch_sha256"]:
        raise ValueError("demo patch bytes do not match verified evidence")
    for path in (root / "repository" / "hello.txt", root / "run" / "candidates" / "fixture" / "worktree" / "hello.txt"):
        if not stat.S_ISREG(path.lstat().st_mode) or installer.read_bounded(path, 1024) != b"Agentmux demo: checked change applied.\n":
            raise ValueError("demo fixture bytes differ from the expected result")
    if not (root / "run" / "report.html").is_file():
        raise ValueError("demo HTML report is missing")
    receipts = [json.loads(path.read_text()) for path in (root / "run").glob("applied-*.json")]
    if len(receipts) != 1 or receipts[0].get("applied") is not True or receipts[0].get("uncertain"):
        raise ValueError("demo application lacks a confirmed receipt")
    if receipts[0].get("patch_sha256") != candidate["patch_sha256"]:
        raise ValueError("demo receipt does not identify its verified patch")


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--artifact-dir", type=Path, required=True)
    parser.add_argument("--version", required=True)
    parser.add_argument("--output", type=Path, required=True, help="new private evidence directory")
    args = parser.parse_args(argv)
    if not re.fullmatch(r"v?[0-9]+\.[0-9]+\.[0-9]+(?:-[A-Za-z0-9.-]+)?", args.version):
        parser.error("explicit semantic version required")
    root = args.output.absolute()
    root.mkdir(mode=0o700)
    started = time.monotonic()
    evidence = {"version": 1, "state": "failed", "release": args.version, "coverage": "deterministic installed-artifact check; no model calls", "platform": platform.system(), "architecture": platform.machine()}
    try:
        assets, manifest = check_archives(args.artifact_dir)
        system = {"Linux": "linux", "Darwin": "darwin"}.get(platform.system())
        arch = {"x86_64": "x86_64", "AMD64": "x86_64", "aarch64": "arm64", "arm64": "arm64"}.get(platform.machine())
        if not system or not arch:
            raise ValueError("unsupported host platform for release execution")
        name = f"agentmux_{system}_{arch}.tar.gz"
        binary = root / "amx"
        installer.install(installer.read_bounded(args.artifact_dir / name, 128 * 1024 * 1024), manifest, name, binary, False, args.version)
        evidence["assets"] = assets
        evidence["manifest_sha256"] = hashlib.sha256(manifest).hexdigest()
        evidence["installed_asset"] = name
        for index, command in enumerate((["version"], ["--help"], ["help", "all"], ["help", "automation"], ["help", "legacy"], ["work", "--help"], ["flow", "--help"], ["demo", "--output", str(root / "demo")])):
            output = run_command([str(binary), *command], root)
            (root / f"command-{index}.log").write_text(output)
        verify_demo(root / "demo")
        evidence["state"] = "passed"
    except Exception as error:
        evidence["error"] = str(error)
        raise
    finally:
        evidence["elapsed_seconds"] = round(time.monotonic() - started, 3)
        with (root / "smoke.json").open("x") as stream:
            json.dump(evidence, stream, indent=2)
            stream.write("\n")
    print(json.dumps(evidence, indent=2))
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except (ValueError, OSError, tarfile.TarError, EOFError) as error:
        print("error: " + str(error), file=sys.stderr)
        sys.exit(1)
