#!/usr/bin/env python3
"""Prepare a bounded native-subscription pilot; preparation never calls models.

Routes are checked locally, not a billing entitlement or hostile-code sandbox.
The outer comparison runner supervises attempts and retains every failure.
"""
import argparse
import fcntl
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import stat
import subprocess
import sys
import time

ROOT = Path(__file__).resolve().parents[2]
PIN = "d52e940c473be6a3b7d7983a704e2bac9547e6de"
DISALLOWED_ENV = re.compile(r"^(OPENAI_.*|AZURE_OPENAI_.*|CODEX_(API_KEY|ACCESS_TOKEN)|ANTHROPIC_.*|CLAUDE_CODE_(OAUTH_TOKEN.*|API_KEY_HELPER.*|USE_.*|PROVIDER_MANAGED_BY_HOST)|AGENTMUX_.*API_KEY)$")


def require(condition, message):
    if not condition:
        raise ValueError(message)


def digest(path):
    result = hashlib.sha256()
    with open(path, "rb") as source:
        for chunk in iter(lambda: source.read(1 << 20), b""):
            result.update(chunk)
    return result.hexdigest()


def private_json(path, value):
    with open(path, "x", encoding="utf-8") as stream:
        os.chmod(path, 0o600)
        json.dump(value, stream, indent=2)
        stream.write("\n")


def checked_environment(environment):
    blocked = sorted(key for key, value in environment.items() if value and DISALLOWED_ENV.fullmatch(key))
    require(not blocked, "subscription pilot refuses API/alternate authentication environment: " + ", ".join(blocked))
    return dict(environment)


def captured(command, *, environment=None):
    result = subprocess.run(command, env=environment, stdin=subprocess.DEVNULL,
                            stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=20)
    require(result.returncode == 0, "native metadata command failed; no model was started")
    require(len(result.stdout) + len(result.stderr) <= 1 << 20, "native metadata output exceeded limit")
    return result.stdout.decode("utf-8", errors="strict"), result.stderr.decode("utf-8", errors="strict")


def load_manifest(path):
    path = Path(path).resolve(strict=True)
    require(stat.S_IMODE(path.parent.stat().st_mode) == 0o700, "study directory must be private mode 0700")
    value = json.loads(path.read_text())
    require(value.get("kind") == "agentmux-subscription-pilot" and value.get("version") == 1, "invalid pilot manifest")
    for filename, expected in value["files_sha256"].items():
        require(digest(filename) == expected, "pinned pilot file changed: " + filename)
    for item in value["tools"].values():
        require(digest(item["path"]) == item["sha256"], "pinned native tool changed")
    return value


def auth_environment(manifest):
    original = checked_environment(os.environ)
    allowed = {"PATH", "HOME", "USER", "LOGNAME", "LANG", "TERM", "COLORTERM", "TMPDIR", "TMP", "TEMP", "PWD",
               "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME", "SSL_CERT_FILE", "SSL_CERT_DIR",
               "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "all_proxy", "no_proxy"}
    environment = {key: value for key, value in original.items() if key in allowed or key.startswith(("LC_", "GIT_"))}
    environment["PATH"] = str(Path(manifest["tools"]["go"]["path"]).parent) + os.pathsep + environment.get("PATH", "")
    environment["CODEX_HOME"] = manifest["native_auth"]["codex_home"]
    environment["CLAUDE_CONFIG_DIR"] = manifest["native_auth"]["claude_config_dir"]
    environment["GOCACHE"] = str(Path(manifest["directory"]) / "go-cache")
    environment["GOMODCACHE"] = manifest["go_mod_cache"]
    return environment


def auth_status(manifest, environment, provider):
    executable = manifest["tools"][provider]["path"]
    if provider == "codex":
        auth = Path(environment["CODEX_HOME"]) / "auth.json"
        descriptor = os.open(auth, os.O_RDONLY | os.O_NOFOLLOW)
        try:
            information = os.fstat(descriptor)
            require(stat.S_ISREG(information.st_mode) and information.st_uid == os.getuid()
                    and not information.st_mode & 0o077 and information.st_size <= 1 << 20,
                    "Codex auth must be an owned private regular file")
            with os.fdopen(descriptor, "r", closefd=False) as source:
                selected = json.load(source)
        finally:
            os.close(descriptor)
        require(selected.get("auth_mode") == "chatgpt" and not selected.get("OPENAI_API_KEY"),
                "Codex stored authentication is not subscription-only ChatGPT")
        stdout, stderr = captured([executable, "-c", 'forced_login_method="chatgpt"',
                                   "-c", 'model_provider="openai"', "login", "status"], environment=environment)
        require("logged in using chatgpt" in (stdout + stderr).lower(), "Codex did not confirm ChatGPT authentication")
        return {"method": "chatgpt", "api_key": False}
    stdout, _ = captured([executable, "--safe-mode", "--setting-sources", "", "auth", "status", "--json"], environment=environment)
    selected = json.loads(stdout)
    require(selected.get("loggedIn") is True and selected.get("authMethod") == "claude.ai"
            and selected.get("apiProvider") == "firstParty" and selected.get("subscriptionType") in {"pro", "max"},
            "Claude did not confirm native first-party Pro/Max subscription authentication")
    return {"method": "claude.ai", "provider": "firstParty", "subscription": selected["subscriptionType"], "api_key": False}


def preflight(manifest, providers=("codex", "claude")):
    environment = auth_environment(manifest)
    return {provider: auth_status(manifest, environment, provider) for provider in providers}


def strip_model(arguments):
    result, index = [], 0
    while index < len(arguments):
        value = arguments[index]
        if value in {"--model", "-m"}:
            require(index + 1 < len(arguments), "missing model value")
            index += 2
            continue
        if value.startswith("--model="):
            index += 1
            continue
        result.append(value)
        index += 1
    return result


def strip_forced_booleans(arguments, flags):
    """The wrapper emits these enabled flags once; preserve literal prompt data."""
    result, literal = [], False
    for value in arguments:
        if value == "--":
            literal = True
        if not literal:
            require(not any(value.startswith(flag + "=") for flag in flags),
                    "pinned boolean options accept only their forced enabled form")
            if value in flags:
                continue
        result.append(value)
    return result


def native_arguments(provider, arguments, manifest):
    arguments = strip_model(arguments)
    if provider == "codex":
        require(arguments and arguments[0] == "exec", "pilot Codex wrapper accepts only exec")
        blocked = ("-c", "--config", "-p", "--profile", "--oss", "--local-provider", "--dangerously-bypass-approvals-and-sandbox", "--approve-for-me")
        require(not any(value == flag or value.startswith(flag + "=") for value in arguments for flag in blocked),
                "Codex invocation attempted to override the pinned subscription profile")
        require(not any(value.startswith(("-c", "-p", "-m")) and not value.startswith("--") and len(value) > 2 for value in arguments),
                "compact Codex profile overrides are refused")
        for index, value in enumerate(arguments):
            if value == "--sandbox":
                require(index + 1 < len(arguments) and arguments[index + 1] in {"workspace-write", "read-only"}, "pilot requires native sandbox")
            if value.startswith("--sandbox="):
                require(value.split("=", 1)[1] in {"workspace-write", "read-only"}, "pilot requires native sandbox")
        forced = ("--ignore-user-config", "--ignore-rules", "--ephemeral")
        arguments = strip_forced_booleans(arguments[1:], forced)
        return ["exec", *forced,
                "-c", 'forced_login_method="chatgpt"', "-c", 'model_provider="openai"',
                "-c", 'approval_policy="never"', "-c", 'model_reasoning_effort="medium"',
                "-c", "features.multi_agent=false", "--model", manifest["models"]["codex"], *arguments]
    forbidden = ("--bare", "--settings", "--setting-sources", "--fallback-model", "--betas", "--plugin-dir", "--mcp-config", "--resume", "--continue", "--agents")
    require(not any(value == flag or value.startswith(flag + "=") for value in arguments for flag in forbidden),
            "Claude invocation attempted to override the pinned subscription profile")
    arguments = strip_forced_booleans(arguments, ("--safe-mode", "--strict-mcp-config"))
    return ["--safe-mode", "--setting-sources", "", "--strict-mcp-config", "--mcp-config", '{"mcpServers":{}}',
            "--model", manifest["models"]["claude"], "--effort", "medium", *arguments]


def native(args):
    require(args.allow_models, "native pilot wrapper requires an authorized live arm")
    manifest = load_manifest(args.manifest)
    environment = auth_environment(manifest)
    output = Path(args.output).resolve(strict=True)
    require(output.is_relative_to(Path(manifest["directory"])), "attempt output is outside its prepared study")
    arguments = native_arguments(args.provider, args.arguments, manifest)
    auth = auth_status(manifest, environment, args.provider)
    counter = output / "native-invocations.jsonl"
    with open(counter, "a+", encoding="utf-8") as stream:
        os.chmod(counter, 0o600)
        fcntl.flock(stream, fcntl.LOCK_EX)
        stream.seek(0)
        records = [json.loads(line) for line in stream]
        maximum = 1 if args.kind == "native-codex" else 4
        require(len(records) < maximum, "declared native invocation bound reached; no retry or payment fallback")
        stream.write(json.dumps({"provider": args.provider, "model": manifest["models"][args.provider],
                                 "effort": "medium", "auth": auth, "started_at_unix": time.time(),
                                 "cli_sha256": manifest["tools"][args.provider]["sha256"]}) + "\n")
        stream.flush()
        os.fsync(stream.fileno())
    executable = manifest["tools"][args.provider]["path"]
    os.execve(executable, [executable, *arguments], environment)


def arm(args):
    require(args.allow_models, "live pilot arm requires --allow-models")
    manifest = load_manifest(args.manifest)
    auth = preflight(manifest)
    output = Path(args.output).resolve(strict=True)
    require(output.is_relative_to(Path(manifest["directory"])), "arm output is outside its prepared study")
    environment = auth_environment(manifest)
    shim_dir = output / "native-bin"
    shim_dir.mkdir(mode=0o700)
    shim_hashes = {}
    for provider in ("codex", "claude"):
        shim = shim_dir / provider
        prefix = [sys.executable, str(Path(__file__).resolve()), "native", "--manifest", str(Path(args.manifest).resolve()),
                  "--provider", provider, "--output", str(output), "--kind", args.kind, "--allow-models", "--"]
        shim.write_text("#!" + sys.executable + "\nimport os,sys\nos.execv(" + repr(sys.executable) + ", " + repr(prefix) + "+sys.argv[1:])\n")
        shim.chmod(0o700)
        shim_hashes[provider] = digest(shim)
    environment.update(PATH=str(shim_dir) + os.pathsep + str(Path(manifest["tools"]["go"]["path"]).parent) + os.pathsep + environment.get("PATH", ""),
                       GOCACHE=str(Path(manifest["directory"]) / "go-cache"), GOMODCACHE=manifest["go_mod_cache"])
    repository = Path(args.repo).resolve(strict=True)
    task = json.loads(Path(args.task).read_text())
    if args.kind == "native-codex":
        prompt = task["prompt"] + "\n\nRun this disclosed development check before finishing: " + json.dumps(task["development_check"])
        command = [str(shim_dir / "codex"), "exec", "--json", "--color", "never",
                   "--sandbox", "workspace-write", "--cd", str(repository), prompt]
    else:
        command = [sys.executable, str(ROOT / "examples/evaluation/run-agentmux.py"), "--binary", manifest["tools"]["amx"]["path"],
                   "--repo", str(repository), "--task", str(Path(args.task).resolve()), "--output", str(output),
                   "--coder", "codex", "--reviewer", "claude", "--attempts", "1", "--timeout", "7m15s", "--allow-models"]
    private_json(output / "arm-launch.json", {"kind": args.kind, "argv": command, "auth": auth, "native_shims_sha256": shim_hashes,
                 "billing": "native subscription route; provider-side extra usage entitlement not exposed by auth status"})
    started = time.monotonic()
    with open(output / "native-arm.stdout", "xb") as stdout, open(output / "native-arm.stderr", "xb") as stderr:
        os.chmod(stdout.name, 0o600)
        os.chmod(stderr.name, 0o600)
        result = subprocess.run(command, cwd=repository, env=environment, stdin=subprocess.DEVNULL, stdout=stdout, stderr=stderr)
    private_json(output / "arm-result.json", {"exit_code": result.returncode, "elapsed_seconds": time.monotonic() - started})
    return result.returncode


def prepare(args):
    directory = Path(args.output).absolute()
    directory.mkdir(mode=0o700)
    directory = directory.resolve(strict=True)
    checked_environment(os.environ)
    source = Path(args.repository).resolve(strict=True)
    commit = captured(["git", "-C", str(source), "rev-parse", args.commit + "^{commit}"])[0].strip()
    require(commit == args.commit, "source commit must be a full exact object ID")
    tools = {}
    for name in ("codex", "claude", "amx", "go"):
        selected = getattr(args, name)
        path = Path(shutil.which(selected) or selected).resolve(strict=True)
        require(path.is_file() and os.access(path, os.X_OK), "missing executable: " + name)
        version = captured([str(path), "version" if name == "go" else "--version"])[0].strip()
        tools[name] = {"path": str(path), "sha256": digest(path), "version": version}
    grading_source = ROOT / "examples/evaluation/pilot-grading"
    require((grading_source / "grade-pilot.py").is_file(), "independent grader must be prepared before this pilot")
    grading = directory / "grading"
    grading.mkdir(mode=0o700)
    grading_files = []
    for source_path in sorted(grading_source.iterdir()):
        if source_path.suffix == ".py" or source_path.name.endswith(".go.txt"):
            target = grading / source_path.name
            shutil.copyfile(source_path, target)
            target.chmod(0o600)
            grading_files.append(target)
    support = grading / "workflow_processes.py"
    shutil.copyfile(ROOT / "scripts/workflow_processes.py", support)
    support.chmod(0o600)
    grading_files.append(support)
    grader = grading / "grade-pilot.py"
    pinned = [Path(__file__).resolve(), ROOT / "examples/evaluation/run-agentmux.py", ROOT / "examples/evaluation/pilot-tasks.json",
              ROOT / "scripts/compare-workflows.py", ROOT / "scripts/workflow_processes.py", *[p for p in grading_files if p.is_file()]]
    manifest_path = directory / "pilot.json"
    module_cache = captured([tools["go"]["path"], "env", "GOMODCACHE"])[0].strip()
    manifest = {"version": 1, "kind": "agentmux-subscription-pilot", "directory": str(directory),
                "source_commit": commit, "tools": tools, "models": {"codex": args.coder_model, "claude": args.reviewer_model},
                "native_auth": {"codex_home": str(Path(os.environ.get("CODEX_HOME", str(Path.home() / ".codex"))).resolve()),
                                "claude_config_dir": str(Path(os.environ.get("CLAUDE_CONFIG_DIR", str(Path.home() / ".claude"))).resolve())},
                "go_mod_cache": module_cache,
                "files_sha256": {str(path): digest(path) for path in pinned},
                "limits": {"attempt_seconds": 480, "baseline_native_invocations": 1, "agentmux_native_invocations": 4, "agentmux_attempts": 1},
                "interpretation": "Whole-workflow subscription pilot, 3 selected repository tasks, one repeat. Not equal-inference, blinded human grading, usability evidence, or a general ranking.",
                "billing": "API credential routes are refused. Native auth does not expose provider-side billing/extra-usage entitlement; this is not a financial cap."}
    private_json(manifest_path, manifest)
    tasks = json.loads((ROOT / "examples/evaluation/pilot-tasks.json").read_text())
    evaluator_files = [str(p) for p in pinned if p == ROOT / "scripts/workflow_processes.py" or p in grading_files]
    for task in tasks:
        task["development_check"] = [tools["go"]["path"], "test", "./internal/managed", "./cmd/agentmux", "-run", "TestWorkerResult|TestTrace", "-count=1", "-timeout=60s"]
        task["checks"] = [{"id": "independent-contract", "argv": [sys.executable, str(grader), "--task", task["id"], "--repo", "{repo}", "--go", tools["go"]["path"], "--process-support", str(support)],
                          "evaluator_files": evaluator_files, "timeout_seconds": 120}]
    arms = [{"id": kind, "argv": [sys.executable, str(Path(__file__).resolve()), "arm", "--manifest", str(manifest_path), "--kind", kind,
                                 "--repo", "{repo}", "--task", "{task}", "--output", "{output}", "--allow-models"], "timeout_seconds": 480}
            for kind in ("native-codex", "agentmux-codex-claude")]
    config = {"version": 1, "mode": "live", "source": {"repository": str(source), "commit": commit}, "repeats": 1,
              "total_timeout_seconds": 3600, "protocol": manifest, "tasks": tasks, "arms": arms}
    private_json(directory / "plan.json", config)
    print(json.dumps({"prepared": True, "model_calls": 0, "directory": str(directory), "plan": str(directory / "plan.json"), "manifest_sha256": digest(manifest_path)}))
    return 0


def main(argv=None):
    os.umask(0o077)
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="operation", required=True)
    setup = commands.add_parser("prepare")
    setup.add_argument("--output", required=True)
    setup.add_argument("--repository", default=str(ROOT))
    setup.add_argument("--commit", default=PIN)
    for name, default in (("codex", "codex"), ("claude", "claude"), ("amx", str(ROOT / "bin/amx")), ("go", "go")):
        setup.add_argument("--" + name, default=default)
    setup.add_argument("--coder-model", default="gpt-6.1-sol")
    setup.add_argument("--reviewer-model", default="claude-sonnet-5-5")
    check = commands.add_parser("preflight")
    check.add_argument("--manifest", required=True)
    run = commands.add_parser("arm")
    run.add_argument("--manifest", required=True)
    run.add_argument("--kind", choices=("native-codex", "agentmux-codex-claude"), required=True)
    for name in ("repo", "task", "output"):
        run.add_argument("--" + name, required=True)
    run.add_argument("--allow-models", action="store_true")
    child = commands.add_parser("native")
    child.add_argument("--manifest", required=True)
    child.add_argument("--provider", choices=("codex", "claude"), required=True)
    child.add_argument("--output", required=True)
    child.add_argument("--kind", choices=("native-codex", "agentmux-codex-claude"), required=True)
    child.add_argument("--allow-models", action="store_true")
    child.add_argument("arguments", nargs=argparse.REMAINDER)
    args = parser.parse_args(argv)
    try:
        if args.operation == "prepare":
            return prepare(args)
        if args.operation == "preflight":
            print(json.dumps({"auth": preflight(load_manifest(args.manifest)), "model_calls": 0}))
            return 0
        if args.operation == "native":
            if args.arguments[:1] == ["--"]:
                args.arguments = args.arguments[1:]
            return native(args)
        return arm(args)
    except (OSError, ValueError, subprocess.SubprocessError) as error:
        print("Subscription pilot refused: " + str(error), file=sys.stderr)
        return 2


if __name__ == "__main__":
    raise SystemExit(main())
