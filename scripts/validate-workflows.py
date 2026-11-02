#!/usr/bin/env python3
"""Exercise the built workflow CLI in disposable projects; native calls are opt-in.

Native mode uses only Codex and Antigravity with explicitly selected models.
All runs are bounded, retained privately, and never touch a live Agentmux team.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import signal
import subprocess
import sys
import time

IMPLEMENTATION = '''def normalize_tags(tags):
    values = set()
    for tag in tags:
        if not isinstance(tag, str):
            raise TypeError("tags must be strings")
        value = tag.strip().lower()
        if value:
            values.add(value)
    return sorted(values)
'''
CONTRACT = '''import importlib.util
spec = importlib.util.spec_from_file_location('candidate', 'tag_normalizer.py')
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)
f = module.normalize_tags
original = [' B ', 'a', 'A', '', ' b ']
assert f(original) == ['a', 'b']
assert original == [' B ', 'a', 'A', '', ' b ']
assert f(x for x in ['Z', ' z ', 'A']) == ['a', 'z']
assert f([]) == []
left, right = f(['x']), f(['x'])
assert left == right and left is not right
for bad in [[None], [1], ['a', object()]]:
    try:
        f(bad)
    except TypeError:
        pass
    else:
        raise AssertionError('Expected TypeError for non-string item')
print('External normalization assertions passed')
'''
GOAL = ("Create tag_normalizer.py exporting normalize_tags(tags). Accept a finite "
        "iterable of strings, strip whitespace, lowercase, remove empty strings "
        "and duplicates, and return a fresh sorted list. Do not mutate the input. "
        "Raise TypeError for any non-string element. Use your native file tools, "
        "do not delegate or spawn other models, do not access other projects or "
        "modify unrelated files or the acceptance criteria. Return a concise result.")


def private_json(path, value):
    with open(path, "x", encoding="utf-8") as handle:
        os.chmod(path, 0o600)
        json.dump(value, handle, indent=2)
        handle.write("\n")


def rejected_by_contract(result, check):
    """A failing negative control must reach the actual contract assertion."""
    if not isinstance(result, dict) or result.get("passed") is not False:
        return False
    checks = result.get("checks")
    if not isinstance(checks, list) or len(checks) != 1:
        return False
    outcome = checks[0]
    if not isinstance(outcome, dict):
        return False
    return (
        outcome.get("name") == check["name"]
        and outcome.get("command") == check["command"]
        and outcome.get("passed") is False
        and type(outcome.get("exit_code")) is int
        and outcome["exit_code"] == 1
        and isinstance(outcome.get("output", ""), str)
        and isinstance(outcome.get("error", ""), str)
        and "AssertionError" in (outcome.get("output", "") + "\n" + outcome.get("error", ""))
        and bool(result.get("tree"))
        and result["tree"] == result.get("ending_tree")
        and bool(result.get("content_sha256"))
        and result["content_sha256"] == result.get("ending_content_sha256")
    )


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", required=True, type=Path)
    parser.add_argument("--output", required=True, type=Path,
                        help="new private directory with an existing parent")
    parser.add_argument("--native", action="store_true",
                        help="permit up to 9 Codex/Antigravity turns and their provider charges")
    parser.add_argument("--native-comparison", action="store_true",
                        help="permit only the two-turn native comparison and evidence export")
    parser.add_argument("--guided", action="store_true",
                        help="validate the everyday amx work journey; command fixtures unless --native")
    parser.add_argument("--containment", choices=("process-group", "pid-namespace"),
                        default="process-group", help="required workflow process lifetime boundary")
    parser.add_argument("--diagnose-antigravity", action="store_true",
                        help="permit two bounded Antigravity Gemini stream diagnostics")
    args = parser.parse_args()
    if sum(bool(mode) for mode in (args.guided,args.native_comparison,args.diagnose_antigravity))>1:
        parser.error("select only one of --guided, --native-comparison or --diagnose-antigravity")
    if args.diagnose_antigravity and args.containment != "process-group":
        parser.error("Antigravity diagnostics do not provide workflow PID containment; omit --diagnose-antigravity to validate a contained workflow")
    if args.native_comparison:
        args.native=True
    binary = args.binary.resolve(strict=True)
    output = args.output.absolute()
    output.mkdir(mode=0o700)
    frozen = output / "amx-under-test"
    shutil.copyfile(binary, frozen)
    frozen.chmod(0o700)
    summary = {"version": 1, "native": args.native,
               "process_containment": args.containment,
               "binary_sha256": hashlib.sha256(frozen.read_bytes()).hexdigest(),
               "started_at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
               "cases": [], "provider_turn_limit": (6 if args.guided else 2 if args.native_comparison else 9) if args.native else 0}
    # -C does not override GIT_DIR/GIT_WORK_TREE/GIT_INDEX_FILE. Disposable
    # validation must never redirect a Git operation into the caller's project.
    workflow_environment = {key: value for key, value in os.environ.items()
                            if not key.startswith("GIT_")}
    git_environment = dict(workflow_environment, GIT_CONFIG_GLOBAL=os.devnull,
                           GIT_CONFIG_NOSYSTEM="1", GIT_TERMINAL_PROMPT="0")

    def git(repo, *argv):
        proc = subprocess.run(["git", "-C", str(repo), "-c", "core.hooksPath=/dev/null",
                               "-c", "commit.gpgsign=false", *argv],
                              capture_output=True, check=True, timeout=20,
                              env=git_environment)
        return proc.stdout

    def project(name, implementation=False):
        repo = output / name
        repo.mkdir(mode=0o700)
        git(repo, "init", "-q")
        (repo / "README").write_text("Disposable Agentmux validation project\n")
        (repo / ".gitignore").write_text("__pycache__/\n")
        if implementation:
            (repo / "tag_normalizer.py").write_text(IMPLEMENTATION)
        git(repo, "add", ".")
        git(repo, "-c", "user.name=Workflow Validation", "-c",
            "user.email=validation@example.invalid", "commit", "-qm", "Validation fixture")
        return repo

    if args.diagnose_antigravity:
        results=[]
        for explicit_print in (False,True):
            label="explicit-print" if explicit_print else "implicit-stream"
            diagnostic_repo=project(label)
            command=["agy","--model","gemini-3.8-flash-low","--mode","accept-edits",
                     "--print-timeout","60s","--log-file",str(output/(label+".log")),
                     "--input-format","stream-json","--output-format","stream-json",
                     "--disable-slash-commands"]
            if explicit_print:
                command.append("--print")
            prompt="Create greeting.txt containing exactly AG_OK followed by a newline using your native file tools. Do not delegate or use other models. Report what you changed."
            request=json.dumps({"event":"user","message":{"content":prompt}}).encode()+b"\n"
            proc=subprocess.Popen(command,cwd=diagnostic_repo,stdin=subprocess.PIPE,
                                  stdout=subprocess.PIPE,stderr=subprocess.PIPE,start_new_session=True,
                                  env=workflow_environment)
            try:
                stdout,stderr=proc.communicate(request,timeout=70)
            except subprocess.TimeoutExpired:
                os.killpg(proc.pid,signal.SIGTERM)
                try: stdout,stderr=proc.communicate(timeout=5)
                except subprocess.TimeoutExpired:
                    os.killpg(proc.pid,signal.SIGKILL);stdout,stderr=proc.communicate()
            for kind,data in (("stdout",stdout),("stderr",stderr)):
                path=output/(label+"."+kind);path.write_bytes(data);path.chmod(0o600)
            events=[]
            for line in stdout.splitlines():
                try:
                    record=json.loads(line)
                    events.append({k:record.get(k) for k in ("event","result","step_update") if k in record})
                except ValueError: pass
            artifact=diagnostic_repo/"greeting.txt"
            result={"name":label,"exit_code":proc.returncode,"artifact_correct":artifact.exists() and artifact.read_bytes()==b"AG_OK\n","events":events}
            private_json(output/(label+".events.json"),result)
            results.append({k:v for k,v in result.items() if k!="events"})
            print(json.dumps(results[-1]),flush=True)
        private_json(output/"diagnostic.json",{"provider":"antigravity","model":"gemini-3.8-flash-low","cases":results})
        print("Evidence: "+str(output/"diagnostic.json"),flush=True)
        return 0 if all(r["artifact_correct"] for r in results) else 1

    check = {"name": "external-normalization-contract",
             "command": [sys.executable, "-B", "-c", CONTRACT], "timeout_seconds": 10}
    if args.native:
        candidates = [
            {"id": "codex", "harness": "codex", "model": "gpt-6.1-sol",
             "timeout_seconds": 90},
            {"id": "antigravity", "harness": "antigravity",
             "model": "gemini-3.8-flash-low", "timeout_seconds": 90,
             "prompt": "This session approves native read_file/write_file tools. Do not use terminal commands or RunCommand, including ls or pwd. Use native file tools directly in the current working directory for the current task; no directory listing is needed.",
             "command": ["agy", "--mode", "accept-edits", "--print-timeout", "90s"]},
        ]
    else:
        code = "from pathlib import Path; Path('tag_normalizer.py').write_text(" + repr(IMPLEMENTATION) + "); print('Implementation ready')"
        candidates = [{"id": name, "harness": "command",
                       "command": [sys.executable, "-c", code]} for name in ("first", "second")]
    repo = project("project")
    plan = {"version": 1, "repository": str(repo), "execution": "local",
            "process_containment": args.containment,
            "mode": "compare", "goal": GOAL, "timeout_seconds": 240,
            "max_attempts": 1, "max_parallel": 2,
            "candidates": candidates, "checks": [check]}
    plan_file = output / "compare-plan.json"
    private_json(plan_file, plan)

    def flow(name, *argv, expected=0, models=False, root_command="flow", expect_json=True):
        start = time.monotonic()
        command = [str(frozen), root_command, *map(str, argv)]
        if models and args.native:
            command.append("--allow-models")
        try:
            proc = subprocess.Popen(command, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                                    start_new_session=True,env=workflow_environment)
            try:
                stdout, stderr = proc.communicate(timeout=300)
            except subprocess.TimeoutExpired:
                try:
                    os.killpg(proc.pid, signal.SIGTERM)
                except ProcessLookupError:
                    pass
                try:
                    proc.communicate(timeout=5)
                except subprocess.TimeoutExpired:
                    try:
                        os.killpg(proc.pid, signal.SIGKILL)
                    except ProcessLookupError:
                        pass
                    proc.communicate()
                raise
            for kind, data in (("stdout", stdout), ("stderr", stderr)):
                path = output / (name + "." + kind)
                path.write_bytes(data)
                path.chmod(0o600)
            envelope = (json.loads(stdout) if stdout else {}) if expect_json else {"ok":proc.returncode==expected,"result":{}}
            ok = proc.returncode == expected and envelope.get("ok") == (expected == 0)
            entry = {"name": name, "passed": ok, "exit_code": proc.returncode,
                     "duration_seconds": round(time.monotonic() - start, 3)}
            result = envelope.get("result")
            if expected != 0:
                # Rejecting an invalid implementation must mean its actual
                # acceptance checks ran and failed, rather than an unrelated
                # command or infrastructure failure with the same exit code.
                ok = ok and rejected_by_contract(result, check)
                entry["passed"] = ok
            if isinstance(result, dict) and result.get("candidates"):
                if "harness" in result["candidates"][0]:
                    entry["candidates"] = [{k: c.get(k) for k in
                         ("id", "harness", "model", "state", "attempts", "usage")}
                         for c in result["candidates"]]
                else:
                    entry["trial_summaries"] = result["candidates"]
            summary["cases"].append(entry)
            print(name + ": " + ("PASS" if ok else "FAIL"), flush=True)
            return ok, result
        except (OSError, subprocess.TimeoutExpired, json.JSONDecodeError) as error:
            summary["cases"].append({"name": name, "passed": False,
                                      "error": type(error).__name__})
            print(name + ": FAIL (" + type(error).__name__ + ")", flush=True)
            return False, None

    try:
        if args.guided:
            catalog=output/"history"
            if not args.native:
                fixtures=output/"fixture-providers";fixtures.mkdir(mode=0o700)
                for name in ("codex","agy"):
                    fixture=fixtures/name
                    code=("#!"+sys.executable+"\nimport sys,json,uuid\nfrom pathlib import Path\nsys.dont_write_bytecode=True\nprompt=sys.stdin.read()\n")
                    if name=="codex":
                        code+="Path('tag_normalizer.py').write_text("+repr(IMPLEMENTATION)+")\n"
                        code+="print(json.dumps({'type':'thread.started','thread_id':str(uuid.uuid4())}))\nprint(json.dumps({'type':'item.completed','item':{'type':'agent_message','text':'Implemented tag_normalizer.py; the declared checks verify it.'}}))\nprint(json.dumps({'type':'turn.completed','usage':{'input_tokens':0,'output_tokens':0}}))\n"
                    else:
                        code+="assert Path('tag_normalizer.py').is_file()\nverdict={'decision':'approve','summary':'Protocol fixture inspected the retained file; acceptance assertions run separately.','findings':[]}\nprint(json.dumps({'event':'result','result':{'status':'SUCCESS','response':json.dumps(verdict),'usage':{'input_tokens':0,'output_tokens':0}}}))\n"
                    fixture.write_text(code);fixture.chmod(0o700)
                workflow_environment['PATH']=str(fixtures)+os.pathsep+os.environ.get('PATH','')
                summary['fixture_providers']=True
            options=["--repo",repo,"--catalog",catalog,"--timeout","300s","--attempts","1","--containment",args.containment,"--json"]
            flow("work-preview",GOAL,*options,"--",*check['command'],root_command="work")
            summary['cases'].append({'name':'preview-no-catalog-writes','passed':not catalog.exists()})
            worked,report=flow("work-run",GOAL,*options,"--allow-models","--",*check['command'],root_command="work")
            unchanged=git(repo,"status","--porcelain")==b''
            summary['cases'].append({'name':'guided-source-unchanged','passed':unchanged})
            if worked:
                flow("work-status","status","latest", "--repo",repo,"--catalog",catalog,"--json",root_command="work")
                flow("work-show","show","latest","--repo",repo,"--catalog",catalog,"--json",root_command="work")
                flow("work-human-show","show","latest","--repo",repo,"--catalog",catalog,root_command="work",expect_json=False)
                shown=(output/"work-human-show.stdout").read_text()
                summary['cases'].append({'name':'human-can-inspect-patch','passed':'diff --git' in shown and 'tag_normalizer.py' in shown})
                flow("work-list","list","--repo",repo,"--catalog",catalog,"--json",root_command="work")
                # Simulate a durable admission interrupted before source mutation.
                # Hard-kill behavior is covered independently by Go fault tests.
                private_json(Path(report['output'])/'applied-0000000000000000000.json',
                             {'run':report['id'],'candidate':'mission-result','repository':str(repo),
                              'patch_sha256':report['patch_sha256'],'phase':'intent',
                              'applied':False,'uncertain':True,'note':'Disposable interrupted-intent fixture'})
                inspected,inspection=flow("work-interrupted-status","status","latest","--repo",repo,"--catalog",catalog,"--json",root_command="work")
                summary['cases'].append({'name':'interrupted-intent-blocks-application-guidance',
                                         'passed':inspected and inspection.get('can_apply') is False and inspection.get('application',{}).get('state')=='uncertain'})
                recovered,recovery=flow("work-recover-before-apply","recover","latest","--repo",repo,"--catalog",catalog,"--json",root_command="work")
                summary['cases'].append({'name':'recovery-does-not-replay-source-patch',
                                         'passed':recovered and recovery.get('state')=='not_applied' and git(repo,'status','--porcelain')==b''})
                flow("work-apply","apply","latest","--repo",repo,"--catalog",catalog,"--json",root_command="work")
                flow("work-recover-after-apply","recover","latest","--repo",repo,"--catalog",catalog,"--json",root_command="work")
                flow("work-applied-status","status","latest","--repo",repo,"--catalog",catalog,"--json",root_command="work")
                flow("work-human-applied-status","status","latest","--repo",repo,"--catalog",catalog,root_command="work",expect_json=False)
                applied_status=(output/"work-human-applied-status.stdout").read_text()
                summary['cases'].append({'name':'applied-guidance-includes-new-files','passed':'Applied to source' in applied_status and 'status --short' in applied_status})
            return 0 if all(c['passed'] for c in summary['cases']) else 1
        compare_dir = output / "compare"
        compare_ok, _ = flow("comparison", "run", plan_file, "--output", compare_dir, models=True)
        # This is a separate assertion of source preservation, not a check
        # supplied to the candidate that could accidentally validate itself.
        source_unchanged = git(repo, "status", "--porcelain") == b""
        summary["cases"].append({"name": "source-unchanged", "passed": source_unchanged})
        if compare_ok:
            flow("status", "status", compare_dir)
            flow("evidence", "explain", compare_dir)
            flow("html-export", "report", compare_dir, "--output", output / "review.html")
            flow("handoff", "handoff", compare_dir, "--candidate", candidates[0]["id"],
                 "--output", output / "handoff.md")

        if args.native_comparison:
            return 0 if all(case["passed"] for case in summary["cases"]) else 1

        mission = dict(plan, mode="mission", max_parallel=1)
        mission["steps"] = [
            {"id": "implement", "candidate": candidates[0]["id"], "verify": True},
            {"id": "review", "candidate": candidates[1]["id"], "depends_on": ["implement"], "require_result": True,
             "goal": "Review tag_normalizer.py using native read tools. Do not edit files. Report findings and evidence."},
            {"id": "acceptance", "verify": True, "depends_on": ["review"]},
        ]
        private_json(output / "mission-plan.json", mission)
        flow("mission", "mission", output / "mission-plan.json", "--output", output / "mission", models=True)
        flow("lab", "lab", plan_file, "--output", output / "lab", "--repetitions", "2", models=True)

        watch_repo = project("watch-project", implementation=True)
        watch = dict(plan, repository=str(watch_repo), candidates=[candidates[1]], timeout_seconds=120)
        private_json(output / "watch-plan.json", watch)
        flow("watch", "watch", output / "watch-plan.json", "--output", output / "watch",
             "--interval", "1s", "--max-runs", "1", "--duration", "120s", models=True)

        if compare_ok:
            applied, _ = flow("apply", "apply", compare_dir, "--candidate", candidates[0]["id"])
            if applied:
                flow("inspect-application", "recover", compare_dir, "--candidate", candidates[0]["id"])
                private_json(output / "checks.json", [check])
                flow("check", "check", "--repo", repo, "--checks", output / "checks.json",
                     "--output", output / "verification.json", "--containment", args.containment)

        bad_repo = project("bad-project")
        (bad_repo / "tag_normalizer.py").write_text("def normalize_tags(tags):\n    return ['incorrect']\n")
        if not (output / "checks.json").exists():
            private_json(output / "checks.json", [check])
        flow("reject-bad-implementation", "check", "--repo", bad_repo,
             "--checks", output / "checks.json", "--output", output / "bad-verification.json", "--containment", args.containment, expected=1)
    finally:
        summary["finished_at"] = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())
        summary["passed"] = bool(summary["cases"]) and all(c["passed"] for c in summary["cases"])
        private_json(output / "validation.json", summary)
        print("Evidence: " + str(output / "validation.json"), flush=True)
    return 0 if summary["passed"] else 1


if __name__ == "__main__":
    raise SystemExit(main())
