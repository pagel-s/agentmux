#!/usr/bin/env python3
"""Exercise the built workflow CLI in disposable projects; native calls are opt-in.

Native mode uses only Codex and Antigravity with explicitly selected models.
All runs are bounded, retained privately, and never touch a live Agentmux team.
"""
import argparse
from contextlib import contextmanager
import hashlib
import importlib.util
import json
import math
import os
import posixpath
from pathlib import Path
import shutil
import signal
import subprocess
import sys
import tempfile
import time

PROCESS_SOURCE = Path(__file__).with_name("workflow_processes.py").resolve()
_process_spec = importlib.util.spec_from_file_location("workflow_evidence_processes", PROCESS_SOURCE)
PROCESS = importlib.util.module_from_spec(_process_spec)
_process_spec.loader.exec_module(PROCESS)
_review_spec = importlib.util.spec_from_file_location("workflow_review_evidence", Path(__file__).with_name("workflow_review_evidence.py"))
REVIEW = importlib.util.module_from_spec(_review_spec)
_review_spec.loader.exec_module(REVIEW)

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


def persist_manifest(path, value):
    """Replace one evidence record atomically."""
    handle = tempfile.NamedTemporaryFile(mode="w", encoding="utf-8", prefix=".validation-",
                                         dir=path.parent, delete=False)
    temporary = Path(handle.name)
    try:
        with handle:
            json.dump(value, handle, indent=2)
            handle.write("\n")
            handle.flush()
            os.fsync(handle.fileno())
        os.replace(temporary, path)
    except BaseException:
        temporary.unlink(missing_ok=True)
        raise


def persist_validation(output, summary):
    """Publish the sole completion record and retain nonterminal case evidence.

    diagnostic.json contains case-scoped outcomes and names validation.json as
    its completion authority; it never independently publishes terminal flags.
    Failure writes prioritize the canonical record and independently attempt
    case-evidence retention, so a secondary write cannot skip revocation.
    """
    diagnostic = summary.get("diagnostic")
    if summary.get("completed") is True:
        persist_manifest(output / "validation.json", summary)
        return
    errors = []
    for path, value in [(output / "validation.json", summary), *(
            [(output / "diagnostic.json", diagnostic)] if diagnostic is not None else [])]:
        try:
            persist_manifest(path, value)
        except BaseException as error:
            errors.append(error)
    if errors:
        raise errors[0]


class ValidationInterrupted(KeyboardInterrupt):
    pass


def error_detail(error):
    try:
        return (type(error).__name__ + ": " + str(error))[:4096]
    except BaseException:
        return type(error).__name__ + ": diagnostic text unavailable"


class ValidationProcesses:
    """Keep every admitted process reachable until its bounded wait finishes."""
    def __init__(self):
        PROCESS.require_process_support()
        self.active = []
        self.previous_handlers = {}
        self.cancelled = None
        self.deferring = False
        self.cleanup_errors = []
        self.final_mask = None

    def interrupted(self, number, _frame):
        self.cancelled = number
        if not self.deferring:
            self.check_cancelled()

    def check_cancelled(self):
        if self.cancelled == signal.SIGINT:
            raise KeyboardInterrupt()
        if self.cancelled is not None:
            raise ValidationInterrupted("validator interrupted by signal " + str(self.cancelled))

    def install_handlers(self):
        for number in (signal.SIGINT, signal.SIGTERM):
            self.previous_handlers[number] = signal.getsignal(number)
            signal.signal(number, self.interrupted)

    def restore_handlers(self):
        self.block_finalization_signals()
        for number, handler in list(self.previous_handlers.items()):
            signal.signal(number, handler)
            del self.previous_handlers[number]
        self.capture_pending_signals()

    def block_finalization_signals(self):
        if self.final_mask is None:
            self.final_mask = signal.pthread_sigmask(signal.SIG_BLOCK, {signal.SIGINT, signal.SIGTERM})

    def capture_pending_signals(self):
        if self.final_mask is not None:
            for number in PROCESS.consume_pending_signals({signal.SIGINT, signal.SIGTERM}, self.final_mask):
                self.cancelled = number

    def release_finalization_signals(self):
        if self.final_mask is not None:
            previous, self.final_mask = self.final_mask, None
            signal.pthread_sigmask(signal.SIG_SETMASK, previous)

    def launch(self, *args, **kwargs):
        self.check_cancelled()
        # A signal inside Popen must not interrupt the interval between the OS
        # child admission and registration of its Python handle. The child does
        # not inherit blocked signals; cancellation is raised after registration.
        self.deferring = True
        try:
            process = PROCESS.launch_pinned(*args, **kwargs)
            self.active.append(process)
        finally:
            self.deferring = False
        self.check_cancelled()
        return process

    def communicate(self, process, *, timeout, input=None):
        try:
            result = communicate_observed(process, input=input, timeout=timeout)
        except BaseException:
            self.deferring = True
            try:
                stop_process_group(process)
            except BaseException as error:
                self.cleanup_errors.append(error_detail(error))
                raise RuntimeError("admitted process cleanup failed: " + self.cleanup_errors[-1])
            else:
                self.active.remove(process)
            finally:
                self.deferring = False
            raise
        else:
            self.active.remove(process)
            process._validation_tree.close()
            return result

    def cleanup(self):
        errors = []
        previous_deferring, self.deferring = self.deferring, True
        try:
            for process in reversed(self.active[:]):
                try:
                    stop_process_group(process)
                except BaseException as error:
                    errors.append(error_detail(error))
                else:
                    self.active.remove(process)
        finally:
            self.deferring = previous_deferring
        if errors:
            self.cleanup_errors.extend(errors)
            raise RuntimeError("; ".join(errors)[:4096])


@contextmanager
def validation_evidence(output, summary):
    # SIGKILL cannot run a finalizer. Keep the durable manifest nonpassing until
    # all required output/cleanup is done, and compensate a failed success write.
    summary.update(passed=False, completed=False)
    processes = ValidationProcesses()
    phase = "validator-completion"

    def note(error, field="error"):
        summary.update(passed=False, completed=False)
        if field == "error":
            summary.setdefault(field, type(error).__name__)
            summary.setdefault("error_detail", error_detail(error))
        else:
            previous = summary.get(field, "")
            detail = error_detail(error)
            summary[field] = (previous + "; " + detail if previous else detail)[:4096]
        summary["cases"].append({"name": phase, "passed": False,
                                 "error": type(error).__name__})

    try:
        processes.install_handlers()
        persist_validation(output, summary)
        yield processes
        phase = "validator-finalization"
        processes.cleanup()
        processes.check_cancelled()
        print("Evidence: " + str(output / "validation.json"), flush=True)
        summary["finished_at"] = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())
        processes.check_cancelled()
        # Restoring the owner's handlers is required work and precedes the
        # terminal success commit. Block/consume cancellation through that
        # restoration and the write; process death after this commit is outside
        # the completed evidence transaction.
        processes.restore_handlers()
        processes.check_cancelled()
        summary["completed"] = True
        summary["passed"] = (bool(summary["cases"])
                             and all(case.get("passed") is True for case in summary["cases"]))
        persist_validation(output, summary)
        processes.capture_pending_signals()
        processes.check_cancelled()
        processes.release_finalization_signals()
    except BaseException as error:
        # Failure handling has a fixed number of cleanup and persistence
        # attempts. Repeated termination signals cannot interrupt compensation.
        processes.deferring = True
        note(error)
        try:
            processes.cleanup()
        except BaseException as cleanup_error:
            note(cleanup_error, "cleanup_error")
        if processes.cleanup_errors:
            summary["cleanup_error"] = "; ".join(processes.cleanup_errors)[:4096]
        try:
            print("Evidence: " + str(output / "validation.json"), flush=True)
        except BaseException as output_error:
            note(output_error, "output_error")
        try:
            summary["finished_at"] = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())
        except BaseException as timestamp_error:
            note(timestamp_error, "finalization_error")
        try:
            persist_validation(output, summary)
        except BaseException as evidence_error:
            note(evidence_error, "evidence_error")
            try:
                persist_validation(output, summary)
            except BaseException as summary_error:
                note(summary_error, "evidence_error")
        # Restore defaults only after the bounded nonpassing write attempts;
        # another termination signal must not interrupt failure compensation.
        try:
            processes.restore_handlers()
        except BaseException as handler_error:
            note(handler_error, "finalization_error")
            try:
                persist_validation(output, summary)
            except BaseException as summary_error:
                note(summary_error, "evidence_error")
        processes.capture_pending_signals()
        processes.release_finalization_signals()
        raise


def communicate_bounded(proc, *, timeout, input=None):
    """Observe admitted descendants and bound cleanup of every aborted wait."""
    try:
        if not hasattr(proc, "_validation_tree"):
            proc._validation_tree = PROCESS.ProcessTree(proc)
        result = communicate_observed(proc, input=input, timeout=timeout)
    except BaseException:
        stop_process_group(proc)
        raise
    else:
        proc._validation_tree.close()
        return result


def communicate_observed(proc, *, timeout, input=None):
    until = time.monotonic() + timeout
    tree = proc._validation_tree
    while True:
        tree.observe()
        remaining = until - time.monotonic()
        if remaining <= 0:
            raise subprocess.TimeoutExpired(proc.args, timeout)
        try:
            result = proc.communicate(input=input, timeout=min(0.05, remaining))
        except subprocess.TimeoutExpired:
            input = None
            continue
        tree.observe()
        if tree.live(descendants_only=True):
            raise RuntimeError("admitted subprocess left observed descendants after completion")
        return result


def stop_process_group(proc):
    """Stop only pinned admitted identities, with bounded reap attempts.

    The historical name is retained for callers; no numeric group is signaled.
    A reaped Popen without its admission handles cannot authorize any target.
    """
    tree = getattr(proc, "_validation_tree", None)
    if tree is None:
        if proc.returncode is not None:
            return
        tree = proc._validation_tree = PROCESS.ProcessTree(proc)
    errors = []

    def observe():
        try:
            tree.observe()
        except BaseException as error:
            errors.append(error_detail(error))

    def send(number):
        try:
            tree.signal(number)
        except BaseException as error:
            errors.append(error_detail(error))

    observe()
    send(signal.SIGTERM)
    try:
        until = time.monotonic() + 5
        while True:
            observe()
            try:
                proc.communicate(timeout=min(0.05, max(0.001, until - time.monotonic())))
                break
            except subprocess.TimeoutExpired:
                if time.monotonic() >= until:
                    break
    except subprocess.TimeoutExpired:
        pass
    finally:
        observe()
        send(signal.SIGKILL)
    try:
        proc.communicate(timeout=5)
    except subprocess.TimeoutExpired:
        # Escaped processes can retain pipes after pinned tasks stop. Do not
        # let them turn a bounded validator into an unbounded wait.
        for stream in (proc.stdin, proc.stdout, proc.stderr):
            if stream is not None:
                stream.close()
        proc.wait(timeout=5)
    until = time.monotonic() + 5
    while tree.live() and time.monotonic() < until:
        time.sleep(0.01)
    if tree.live():
        errors.append("observed admitted descendants survived bounded pidfd cleanup")
    tree.close()
    if errors:
        raise RuntimeError("; ".join(errors)[:4096])


def strict_json(data):
    """Reject ambiguous fields and non-JSON numbers before interpreting evidence."""
    def unambiguous(pairs):
        result, folded = {}, set()
        for key, value in pairs:
            # Match the workflow decoder's rejection of case aliases, including
            # Unicode aliases of ASCII fields (for example the Kelvin sign).
            # Full Unicode casefold also refuses expanding aliases; evidence
            # does not need distinct fields which differ only by that spelling.
            identity = key.casefold()
            if identity in folded:
                raise ValueError("JSON contains duplicate or case-aliased object fields")
            folded.add(identity)
            result[key] = value
        return result

    def nonfinite(_):
        raise ValueError("JSON contains a nonfinite number")

    def finite_float(literal):
        value = float(literal)
        if not math.isfinite(value):
            return nonfinite(literal)
        return value

    if isinstance(data, bytes):
        data = data.decode("utf-8")
    return json.loads(data, object_pairs_hook=unambiguous,
                      parse_constant=nonfinite, parse_float=finite_float)


def antigravity_diagnostic_evidence(stdout, stderr):
    """Require one successful native terminal result, retaining all frames.

    Keep the answer, session binding and denial rules aligned with the
    Antigravity adapter in internal/harness. A diagnostic also treats explicit
    error signals as failure; an artifact alone cannot resolve them.
    """
    events = []
    for line in stdout.splitlines():
        if not line.strip():
            continue
        record = strict_json(line)
        require(isinstance(record, dict), "diagnostic stream frame must be a JSON object")
        events.append(record)
    terminal_statuses = [(record.get("result") or {}).get("status")
                         if isinstance(record.get("result"), dict) else None
                         for record in events if record.get("event") == "result"]
    evidence = {"events": events, "terminal_statuses": terminal_statuses,
                "protocol_passed": False}
    session = None
    complete = False

    def bind_session(record):
        nonlocal session
        if "conversation_id" in record:
            identity = record["conversation_id"]
            require(isinstance(identity, str) and bool(identity),
                    "diagnostic session identity must be a nonempty string")
            require(session is None or session == identity,
                    "diagnostic session identity changed within one request")
            session = identity

    def no_error(record):
        for key in ("error", "errors"):
            require(record.get(key) in (None, "", [], {}),
                    "diagnostic stream reported an error")
        if "is_error" in record:
            require(record["is_error"] is False,
                    "diagnostic stream reported an invalid or failing error flag")

    def permission_denial(text):
        return any(phrase in text.lower() for phrase in
                   ("soft-denied", "soft denied", "permission denied", "permission_denied",
                    "permissiondenied", "requires approval", "approval required"))

    try:
        for record in events:
            kind = record.get("event")
            require(isinstance(kind, str) and bool(kind),
                    "diagnostic event discriminator must be a nonempty string")
            require(kind not in ("error", "failed", "failure", "aborted", "cancelled", "canceled"),
                    "diagnostic stream reported an error event")
            bind_session(record)
            no_error(record)
            if kind == "step_update":
                step = record.get("step_update")
                require(isinstance(step, dict), "diagnostic step update must be an object")
                require(not (complete and step.get("step_type") == "agent_response"),
                        "diagnostic answer followed the terminal result")
                no_error(step)
                if "tool_info" in step:
                    tool = step["tool_info"]
                    require(isinstance(tool, dict), "diagnostic tool information must be an object")
                    no_error(tool)
            if kind == "result":
                require(not complete, "diagnostic has multiple terminal results")
                complete = True
                result = record.get("result")
                require(isinstance(result, dict) and isinstance(result.get("status"), str)
                        and bool(result["status"]), "diagnostic result must contain its status")
                bind_session(result)
                no_error(result)
                if "denied_actions" in result:
                    actions = result["denied_actions"]
                    require(isinstance(actions, list) and all(isinstance(action, dict)
                            and isinstance(action.get("action"), str) and bool(action["action"])
                            for action in actions), "diagnostic denied actions must identify their actions")
                    require(not actions, "diagnostic reported denied actions")
                require(result["status"] == "SUCCESS", "diagnostic terminal status is not SUCCESS")
                require(isinstance(result.get("response"), str),
                        "successful diagnostic result must contain its own response string")
        require(complete, "diagnostic stream has no terminal result")
        require(not permission_denial(stderr.decode("utf-8", errors="replace")),
                "diagnostic stderr reported a permission denial")
    except ValueError as error:
        evidence["protocol_error"] = str(error)
    else:
        evidence["protocol_passed"] = True
    return evidence


def workflow_result(envelope, *, list_result=False):
    """Validate consumed CLI protocol shapes before inspecting their fields."""
    if not isinstance(envelope, dict) or type(envelope.get("ok")) is not bool:
        raise ValueError("workflow envelope must be an object with a boolean ok")
    result = envelope.get("result")
    if list_result:
        if not isinstance(result, list) or any(not isinstance(item, dict) for item in result):
            raise ValueError("workflow list result must be an array of objects")
        return result
    if not isinstance(result, dict):
        raise ValueError("workflow result must be an object")
    if "candidates" in result:
        candidates = result["candidates"]
        if not isinstance(candidates, list) or any(not isinstance(item, dict) for item in candidates):
            raise ValueError("workflow candidates must be an array of objects")
        if any("verification" in item and not isinstance(item["verification"], dict)
               for item in candidates):
            raise ValueError("candidate verification must be an object")
    return result

def require(condition, reason):
    if not condition:
        raise ValueError(reason)


def exact_int(value, expected):
    return type(value) is int and value == expected


def empty_error(value):
    return value is None or (isinstance(value, str) and value == "")


def false_flag(record, key):
    return record.get(key, False) is False


def same_json(value, expected):
    return json.dumps(value, sort_keys=True, allow_nan=False) == json.dumps(expected, sort_keys=True, allow_nan=False)


def object_id(value):
    return (isinstance(value, str) and len(value) in (40, 64)
            and all(char in "0123456789abcdefABCDEF" for char in value))


def digest_id(value):
    return (isinstance(value, str) and len(value) == 64
            and all(char in "0123456789abcdef" for char in value))


def revision_evidence(value):
    return (isinstance(value, dict) and object_id(value.get("tree"))
            and digest_id(value.get("content_sha256"))
            and all(key not in value or digest_id(value[key])
                    for key in ("ignore_sha256", "selection_sha256")))


def private_pins(value, *, ignore=None, selection=None):
    return (isinstance(value, dict) and digest_id(value.get("ignore_sha256"))
            and digest_id(value.get("selection_sha256"))
            and (ignore is None or value["ignore_sha256"] == ignore)
            and (selection is None or value["selection_sha256"] == selection))


def objects(value, reason):
    require(isinstance(value, list) and all(isinstance(item, dict) for item in value), reason)
    return value


class WorkflowOracle:
    """Check the declared matrix, retained artifacts and independent contract."""
    def __init__(self, output, plan, environment, processes=None):
        self.output, self.plan, self.environment = output, plan, environment
        self.processes = processes
        self.plans = {}
        self.comparison = self.guided = self.guided_plan = None

    def retained(self, path, expected=None):
        require(path.is_file() and not path.is_symlink(), "retained evidence is missing or not a regular file")
        value = strict_json(path.read_bytes())
        if expected is not None:
            require(same_json(value, expected), "CLI result differs from retained evidence")
        return value

    def policy(self, result, plan):
        require(result.get("storage") == plan["storage"], "snapshot storage policy is missing or different")
        require(result.get("process_containment") == plan["process_containment"], "process containment evidence is missing or different")
        isolation = plan.get("isolation")
        if isolation:
            isolation = dict({"home_bytes": 64 << 20, "temp_bytes": 64 << 20, "bridge_max_requests": 32}, **isolation)
        require(same_json(result.get("isolation"), isolation), "isolation policy is missing or different")

    def verification(self, result, checks, worktree, plan, *, store=None, ignore=None):
        require(isinstance(result, dict) and result.get("passed") is True, "expected passing verification is missing")
        require(result.get("repository") == str(worktree), "verification belongs to another worktree")
        require(result.get("storage") == plan["storage"] and result.get("process_containment") == plan["process_containment"], "verification storage or containment evidence differs")
        require(result.get("isolation", "none") == (plan.get("isolation") or {}).get("mode", "none"), "verification isolation evidence differs")
        require(revision_evidence(result) and object_id(result.get("ending_tree"))
                and result["tree"] == result["ending_tree"]
                and digest_id(result.get("ending_content_sha256"))
                and result["content_sha256"] == result["ending_content_sha256"], "verification is not bound to unchanged content")
        actual = objects(result.get("checks"), "verification check outcomes are missing")
        require(len(actual) == len(checks), "verification omitted or added acceptance checks")
        for outcome, check in zip(actual, checks):
            require(outcome.get("name") == check["name"] and outcome.get("command") == check["command"], "verification ran different acceptance checks")
            require(outcome.get("passed") is True and type(outcome.get("exit_code")) is int and outcome["exit_code"] == 0 and empty_error(outcome.get("error")), "acceptance check did not pass")
            require(isinstance(outcome.get("output"), str) and "External normalization assertions passed" in outcome["output"], "acceptance assertion output is missing")
        if plan["storage"] == "private":
            expected_store = store or worktree / ".git"
            require(result.get("store") == str(expected_store) and expected_store.is_dir()
                    and private_pins(result, ignore=ignore), "private verification store or pinned policy/selection evidence is missing or different")

    def artifact(self, item, plan, checks, *, implementation=True, oracle=True, ignore=None):
        worktree = Path(item.get("worktree", ""))
        require(worktree.is_absolute() and worktree.resolve().is_relative_to(self.output.resolve()), "worktree is outside retained validation output")
        self.verification(item.get("verification"), checks, worktree, plan, ignore=ignore)
        final = item.get("final_snapshot")
        self.verified_snapshot(final, item["verification"], plan)
        if "starting_snapshot" in item:
            require(revision_evidence(item["starting_snapshot"]), "candidate starting revision evidence is malformed")
            if plan["storage"] == "private":
                require(private_pins(item["starting_snapshot"], ignore=ignore), "candidate starting policy or selection evidence is missing or different")
        patch = Path(item.get("patch", ""))
        require(patch.is_file() and patch.resolve().is_relative_to(self.output.resolve()) and hashlib.sha256(patch.read_bytes()).hexdigest() == item.get("patch_sha256"), "retained patch is missing or its digest differs")
        source = worktree / "tag_normalizer.py"
        require(source.is_file() and source.resolve().is_relative_to(worktree.resolve()), "normalization implementation is missing")
        if implementation:
            require(b"tag_normalizer.py" in patch.read_bytes(), "implementation patch does not contain the requested file")
        if oracle and not plan.get("isolation"):
            # A separate invocation runs the owner's contract over the actual
            # retained implementation, independent of the CLI's success claims.
            launch = self.processes.launch if self.processes else PROCESS.launch_pinned
            communicate = self.processes.communicate if self.processes else communicate_bounded
            proc = launch([sys.executable, "-I", "-B", "-c", CONTRACT], cwd=worktree,
                          env=self.environment, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                          start_new_session=True)
            stdout, _ = communicate(proc, timeout=10)
            require(proc.returncode == 0 and b"External normalization assertions passed" in stdout, "independent retained-implementation contract failed")
        # Full-isolation mode relies on the bound check evidence and retained
        # patch; executing provider-authored Python on the host would weaken it.

    def verified_snapshot(self, final, verification, plan):
        require(revision_evidence(final) and final["tree"] == verification["tree"]
                and final["content_sha256"] == verification["content_sha256"], "final artifact differs from verified revision")
        if plan["storage"] == "private":
            require(private_pins(final, ignore=verification["ignore_sha256"],
                                 selection=verification["selection_sha256"]), "final artifact pinned policy or selection differs from verification")

    def same_revision(self, observed, expected, plan):
        require(revision_evidence(observed) and revision_evidence(expected)
                and observed["tree"] == expected["tree"]
                and observed["content_sha256"] == expected["content_sha256"], "mission evidence breaks the current revision chain")
        if plan["storage"] == "private":
            require(private_pins(expected) and private_pins(observed, ignore=expected["ignore_sha256"],
                                                            selection=expected["selection_sha256"]),
                    "mission evidence breaks the current ignore or selection chain")

    def structured_review(self, review):
        require(isinstance(review, dict) and set(review) == {"decision", "summary", "findings"}, "structured review fields are incomplete or unexpected")
        require(review["decision"] in ("approve", "request_changes"), "structured review decision is invalid")
        require(isinstance(review["summary"], str) and bool(review["summary"].strip()) and len(review["summary"].encode()) <= 16 << 10, "structured review summary is missing or invalid")
        findings = objects(review["findings"], "structured review findings must be an explicit array of objects")
        require(len(findings) <= 50 and (not findings if review["decision"] == "approve" else bool(findings)), "structured review decision conflicts with findings")
        for finding in findings:
            require(set(finding) == {"file", "line", "message"}, "review finding fields are incomplete or unexpected")
            filename, line, message = finding["file"], finding["line"], finding["message"]
            require(isinstance(filename, str) and bool(filename) and len(filename.encode()) <= 4096
                    and not filename.startswith("/") and not any(char in filename for char in "\\\0\r\n")
                    and posixpath.normpath(filename) not in ("..", ".git")
                    and not posixpath.normpath(filename).startswith(("../", ".git/")), "review finding file is not a project-relative path")
            require(type(line) is int and 1 <= line <= 10_000_000, "review finding line is invalid")
            require(isinstance(message, str) and bool(message.strip()) and len(message.encode()) <= 8192, "review finding message is missing or invalid")

    def report(self, result, plan, directory, *, oracle=True):
        require(result.get("state") == "completed" and empty_error(result.get("error")), "workflow did not complete successfully")
        require(exact_int(result.get("version"), 1) and isinstance(result.get("id"), str)
                and bool(result["id"]) and result.get("execution") == "local" and result.get("mode") == plan["mode"], "workflow identity or mode differs")
        require(result.get("repository") == plan["repository"] and result.get("output") == str(directory), "workflow repository or output differs")
        self.policy(result, plan)
        self.retained(directory / "report.json", result)
        snapshot = result.get("snapshot")
        require(revision_evidence(snapshot), "starting snapshot evidence is missing or malformed")
        if plan["storage"] == "private":
            require(snapshot.get("store") == str(directory / "git") and (directory / "git").is_dir()
                    and private_pins(snapshot), "private starting store or pinned policy/selection evidence is missing")
        ignore = snapshot.get("ignore_sha256") if plan["storage"] == "private" else None
        if plan["mode"] == "compare":
            actual = objects(result.get("candidates"), "comparison candidate outcomes are missing")
            require([item.get("id") for item in actual] == [item["id"] for item in plan["candidates"]], "comparison candidate coverage or order differs")
            for item, candidate in zip(actual, plan["candidates"]):
                require(item.get("harness") == candidate["harness"] and item.get("model", "") == candidate.get("model", "") and item.get("state") == "verified" and type(item.get("attempts")) is int and item["attempts"] == 1 and empty_error(item.get("error")), "candidate profile or successful state differs")
                self.artifact(item, plan, plan["checks"], implementation=not (Path(plan["repository"]) / "tag_normalizer.py").is_file(), oracle=oracle, ignore=ignore)
        else:
            actual = objects(result.get("steps"), "mission step outcomes are missing")
            require([item.get("id") for item in actual] == [item["id"] for item in plan["steps"]], "mission step coverage or order differs")
            profiles = {item["id"]: item for item in plan["candidates"]}
            skipped = REVIEW.early_skips(result, plan)
            current = snapshot
            for item, step in zip(actual, plan["steps"]):
                require(all(type(step.get(flag, False)) is bool
                            for flag in ("verify", "read_only", "review", "gate_review")),
                        "planned mission roles must be booleans")
                if item["id"] in skipped:
                    self.retained(Path(item["evidence_path"]), item)
                    continue
                profile = profiles.get(step.get("candidate"), {})
                require(type(item.get("attempts")) is int and item["attempts"] == (1 if step.get("candidate") else 0), "mission step attempt count differs from its planned admission bound")
                require(item.get("harness", "") == profile.get("harness", "") and item.get("model", "") == profile.get("model", "") and item.get("depends_on", []) == step.get("depends_on", []), "mission step profile or dependency differs")
                require(item.get("state") == ("verified" if step.get("verify") else "completed") and empty_error(item.get("error")), "mission step is unsuccessful")
                read_only = step.get("read_only", False) or step.get("review", False)
                require(item.get("read_only", False) is read_only, "mission step read-only role differs from its plan")
                require(item.get("gate_review", False) is step.get("gate_review", False), "mission step gate role differs from its plan")
                if step.get("candidate"):
                    self.same_revision(item.get("starting_snapshot"), current, plan)
                    final = item.get("final_snapshot")
                    if read_only:
                        self.same_revision(final, current, plan)
                    else:
                        require(revision_evidence(final), "writing step final revision is missing or malformed")
                        if plan["storage"] == "private":
                            require(private_pins(final, ignore=ignore), "writing step private pins are missing or differ")
                        current = final
                else:
                    # Verifier-only steps omit snapshots in current reports;
                    # any explicit snapshots still belong to this same revision.
                    for key in ("starting_snapshot", "final_snapshot"):
                        if key in item:
                            self.same_revision(item[key], current, plan)
                if step.get("verify"):
                    self.verification(item.get("verification"), plan["checks"], Path(result["worktree"]), plan, ignore=ignore)
                    self.same_revision(item["verification"], current, plan)
                if step.get("review"):
                    review = item.get("review")
                    require(item.get("read_only") is True, "independent read-only review evidence is missing")
                    self.structured_review(review)
                    require(result.get("review_context") == "fresh" and item.get("review_context") == "fresh", "review lacks required fresh context evidence")
                    if step.get("gate_review"):
                        require(item.get("gate_review") is True and review["decision"] == "approve", "final review did not approve")
            self.same_revision(result.get("final_snapshot"), current, plan)
            self.same_revision(result.get("verification"), current, plan)
            self.artifact(result, plan, plan["checks"], oracle=oracle, ignore=ignore)

    def preview(self, result):
        plan = result.get("plan")
        require(isinstance(plan, dict), "guided preview plan is missing")
        require(plan.get("mode") == "mission" and plan.get("kind") == "work" and plan.get("repository") == self.plan["repository"], "guided preview identity differs")
        expected_plan = dict(self.plan)
        if self.plan.get("isolation"):
            expected_plan["isolation"] = dict(self.plan["isolation"], read_only_paths=[str(self.output / "fixture-providers")])
        self.policy(plan, expected_plan)
        expected = [("coder", "codex", "gpt-6.1-sol"), ("reviewer", "claude", "sonnet") if self.plan.get("isolation") else ("reviewer", "antigravity", "gemini-3.8-flash-low")]
        candidates = objects(plan.get("candidates"), "guided candidate profiles are missing")
        require([(p.get("id"), p.get("harness"), p.get("model")) for p in candidates] == expected, "guided provider profiles differ")
        steps = objects(plan.get("steps"), "guided planned steps are missing")
        require([s.get("id") for s in steps] == ["implement", "review", "address-review", "final-review", "final-check"], "guided plan step coverage or order differs")
        flags = ("verify", "read_only", "review", "gate_review", "require_result")
        roles = [(True, False, False, False, False), (False, True, True, False, True),
                 (True, False, False, False, False), (False, True, True, True, True), (True, False, False, False, False)]
        require(all(step.get(flag, False) is expected
                    for step, role in zip(steps, roles) for flag, expected in zip(flags, role))
                and [s.get("candidate") for s in steps] == ["coder", "reviewer", "coder", "reviewer", None]
                and [s.get("depends_on", []) for s in steps] == [[], ["implement"], ["implement", "review"], ["implement", "review", "address-review"], ["final-review"]],
                "guided verification or review gates differ")
        require(same_json(plan.get("checks"), [dict(self.plan["checks"][0], name="project-check", timeout_seconds=60)]), "guided acceptance command differs")
        require(type(plan.get("max_attempts")) is int and plan["max_attempts"] == 1
                and exact_int(plan.get("max_parallel"), 1) and exact_int(result.get("max_native_turns"), 4)
                and exact_int(result.get("min_native_turns"), 2) and plan.get("review_policy") == REVIEW.POLICY,
                "guided planned provider admission bound or early-review policy differs")
        self.guided_plan = plan

    def validate(self, name, result, argv, root_command):
        if name == "work-preview":
            self.preview(result)
        elif name in ("comparison", "mission", "isolated-check"):
            plan = self.plans[str(argv[1])]
            directory = Path(argv[argv.index("--output") + 1])
            self.report(result, plan, directory)
            if name == "comparison":
                self.comparison = result
        elif name == "work-run":
            require(self.guided_plan is not None, "guided preview did not validate")
            directory = Path(result.get("output", ""))
            require(directory.is_absolute() and directory.resolve().is_relative_to((self.output / "history").resolve()), "guided result is outside requested history")
            self.report(result, self.guided_plan, directory)
            self.guided = result
        elif name == "status":
            require(self.comparison is not None and same_json(result, self.comparison), "status differs from completed comparison")
            self.report(result, self.plan, Path(result["output"]), oracle=False)
        elif name == "evidence":
            require(self.comparison is not None and result.get("id") == self.comparison["id"] and result.get("state") == "completed", "explanation identity or state differs")
            items = objects(result.get("items"), "explanation items are missing")
            require([item.get("id") for item in items] == [p["id"] for p in self.plan["candidates"]], "explanation candidate coverage differs")
            require(all(item.get("state") == "verified" and item.get("patch_intact") is True and item.get("fresh") is True for item in items), "explanation has failed or stale candidate evidence")
        elif name in ("html-export", "handoff"):
            destination = self.output / ("review.html" if name == "html-export" else "handoff.md")
            require(result.get("report" if name == "html-export" else "brief") == str(destination) and destination.is_file(), "exported artifact is missing")
            if name == "handoff":
                require(result.get("delivered") is False, "handoff unexpectedly reports external delivery")
            data = destination.read_text()
            require(bool(self.comparison) and self.comparison["id"] in data and all(token in data for token in (["html", "tag_normalizer.py"] if name == "html-export" else ["Candidate: " + self.plan["candidates"][0]["id"], "passed=true"])), "exported artifact does not contain the expected completed work")
        elif name in ("watch", "lab"):
            plan = self.plans[str(argv[1])]
            directory = Path(argv[argv.index("--output") + 1])
            self.policy(result, plan)
            require(result.get("state") == "completed" and empty_error(result.get("error")) and result.get("output") == str(directory) and result.get("repository") == plan["repository"], "aggregate workflow did not complete in the requested project")
            self.retained(directory / (name + ".json"), result)
            runs = objects(result.get("runs"), "aggregate run outcomes are missing")
            require(len(runs) == (1 if name == "watch" else 2), "aggregate run coverage differs")
            if name == "lab":
                require(exact_int(result.get("repetitions"), 2), "laboratory repetition coverage differs")
                totals = objects(result.get("candidates"), "laboratory candidate totals are missing")
                require([item.get("id") for item in totals] == [p["id"] for p in plan["candidates"]] and all(exact_int(item.get("runs"), 2) and exact_int(item.get("verified_runs"), 2) and exact_int(item.get("failed_runs"), 0) for item in totals), "laboratory candidate verification coverage differs")
            for number, run in enumerate(runs):
                require(run.get("state") == "completed" and empty_error(run.get("error")) and false_flag(run, "superseded"), "aggregate contains an unsuccessful or superseded run")
                trial_plan = dict(plan)
                if name == "lab":
                    trial_plan["repository"] = result["snapshot"]["repository"]
                    order = plan["candidates"][number:] + plan["candidates"][:number]
                    require(exact_int(run.get("repetition"), number + 1) and run.get("order") == [p["id"] for p in order], "laboratory trial order differs")
                    trial_plan["candidates"] = order
                trial = self.retained(Path(run["output"]) / "report.json")
                self.report(trial, trial_plan, Path(run["output"]))
        elif name in ("apply", "work-apply"):
            require(result.get("applied") is True and false_flag(result, "uncertain") and result.get("phase") == "finished", "source application is incomplete or uncertain")
            receipt = self.retained(Path(result.get("receipt", "")))
            require(same_json(receipt, result), "application receipt differs from returned result")
            require(Path(result.get("repository", "")) == Path(self.plan["repository"]), "application repository differs")
            report = self.guided if name == "work-apply" else self.comparison
            require(report is not None and result.get("run") == report["id"], "application belongs to another run")
            selected = report if name == "work-apply" else report["candidates"][0]
            require(result.get("candidate") == ("mission-result" if name == "work-apply" else selected["id"])
                    and result.get("patch_sha256") == selected["patch_sha256"], "application selected a different candidate or patch")
            source = Path(self.plan["repository"]) / "tag_normalizer.py"
            require(source.is_file() and source.read_bytes() == (Path(selected["worktree"]) / "tag_normalizer.py").read_bytes(), "application did not reproduce the retained implementation")
        elif name in ("inspect-application", "work-recover-before-apply", "work-recover-after-apply"):
            expected = "not_applied" if name == "work-recover-before-apply" else "applied"
            require(result.get("state") == expected and result.get("recoverable") is False and result.get("source_observation") == ("matches_start" if expected == "not_applied" else "matches_final"), "application recovery is uncertain or disagrees with source")
        elif name == "check":
            self.verification(result, self.plan["checks"], Path(self.plan["repository"]), self.plan,
                              store=self.output / "verification.json.git" if self.plan["storage"] == "private" else None)
            self.retained(self.output / "verification.json", result)
        elif name == "work-list":
            require(self.guided is not None and len(result) == 1 and result[0].get("id") == self.guided["id"] and result[0].get("state") == "completed" and result[0].get("directory") == self.guided["output"], "guided history omits the completed run")
        elif name in ("work-status", "work-show", "work-interrupted-status", "work-applied-status"):
            require(self.guided is not None and result.get("id") == self.guided["id"] and result.get("state") == "completed" and exact_int(result.get("checks_total"), 1) and exact_int(result.get("checks_passed"), 1) and result.get("review_state") == "approve" and result.get("result_fresh") is True, "guided status lacks completed approved and checked work")
            require(result.get("storage") == self.plan["storage"] and result.get("process_containment") == self.plan["process_containment"]
                    and result.get("isolation", "none") == (self.plan.get("isolation") or {}).get("mode", "none"), "guided status storage or isolation policy differs")
            phases = objects(result.get("phases"), "guided status phases are missing")
            require([(s.get("id"), s.get("state"), s.get("skip_reason", "")) for s in phases]
                    == [(s["id"], s["state"], s.get("skip_reason", "")) for s in self.guided["steps"]]
                    and same_json(result.get("early_approval"), self.guided.get("early_approval")),
                    "guided status phase coverage or original approval differs")
            expected = "uncertain" if name == "work-interrupted-status" else "applied" if name == "work-applied-status" else "none"
            application = result.get("application")
            require(isinstance(application, dict) and application.get("state") == expected and result.get("can_apply") is (expected == "none"), "guided application status or guidance differs")
        elif name.startswith("work-human-"):
            data = (self.output / (name + ".stdout")).read_text()
            require("tag_normalizer.py" in data and ("diff --git" in data if name == "work-human-show" else "Applied to source" in data), "human output omits the requested patch or application outcome")
        else:
            raise ValueError("scenario has no semantic validation")


def rejected_by_contract(result, check, plan=None):
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
        and (outcome.get("error") is None or isinstance(outcome["error"], str))
        and "AssertionError" in (outcome.get("output", "") + "\n" + (outcome.get("error") or ""))
        and revision_evidence(result)
        and result["tree"] == result.get("ending_tree")
        and object_id(result.get("ending_tree"))
        and digest_id(result.get("ending_content_sha256"))
        and result["content_sha256"] == result.get("ending_content_sha256")
        and ((plan or {}).get("storage", result.get("storage")) != "private" or private_pins(result))
    )


def validation_environment(output, live_login):
    """Grant runtime discovery plus only the explicitly selected login mode.

    Native validation deliberately uses the owner's existing Codex/Antigravity
    login roots. Fixture validation gets a new empty home. Neither mode imports
    arbitrary API keys, shell/Python hooks, Git overrides or agent control flags.
    PATH remains owner-selected executable discovery, not an isolation boundary.
    """
    temporary = output / "tmp"
    temporary.mkdir(mode=0o700)
    environment = {
        "PATH": os.environ.get("PATH", "/usr/bin:/bin"),
        "TMPDIR": str(temporary), "LANG": "C.UTF-8", "TERM": "dumb",
        "TZ": "UTC", "PYTHONDONTWRITEBYTECODE": "1",
        "AGY_CLI_DISABLE_AUTO_UPDATE": "true",
    }
    if live_login:
        for name in ("HOME", "CODEX_HOME", "XDG_CONFIG_HOME", "XDG_DATA_HOME",
                     "XDG_CACHE_HOME", "XDG_STATE_HOME", "XDG_RUNTIME_DIR",
                     "DBUS_SESSION_BUS_ADDRESS", "SSL_CERT_FILE", "SSL_CERT_DIR"):
            if name in os.environ:
                environment[name] = os.environ[name]
        return environment
    home = output / "empty-home"
    home.mkdir(mode=0o700)
    environment["HOME"] = str(home)
    environment["CODEX_HOME"] = str(home / ".codex")
    for kind in ("CONFIG", "DATA", "CACHE", "STATE"):
        environment["XDG_" + kind + "_HOME"] = str(home / kind.lower())
    return environment


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
    parser.add_argument("--storage", choices=("private", "shared"), default="private",
                        help="private independent snapshots (default) or explicit legacy shared storage")
    parser.add_argument("--isolation", choices=("none", "bubblewrap"), default="none",
                        help="require the full filesystem/network boundary for model-free fixtures")
    parser.add_argument("--diagnose-antigravity", action="store_true",
                        help="permit two bounded Antigravity Gemini stream diagnostics")
    args = parser.parse_args()
    try:
        PROCESS.require_process_support()
    except RuntimeError as error:
        parser.error(str(error))
    if sum(bool(mode) for mode in (args.guided,args.native_comparison,args.diagnose_antigravity))>1:
        parser.error("select only one of --guided, --native-comparison or --diagnose-antigravity")
    if args.diagnose_antigravity and args.containment != "process-group":
        parser.error("Antigravity diagnostics do not provide workflow PID containment; omit --diagnose-antigravity to validate a contained workflow")
    if args.native_comparison or args.diagnose_antigravity:
        args.native=True
    if args.isolation == "bubblewrap":
        if args.native or args.diagnose_antigravity:
            parser.error("full-isolation validation uses model-free fixtures; use an explicit work/flow plan for live provider auth and exact inference origins")
        if args.storage != "private":
            parser.error("full workflow isolation requires private snapshot storage")
        args.containment = "pid-namespace"
    binary = args.binary.resolve(strict=True)
    output = args.output.absolute()
    output.mkdir(mode=0o700)
    summary = {"version": 1, "native": args.native,
               "mode": "diagnostic" if args.diagnose_antigravity else "guided" if args.guided else "comparison" if args.native_comparison else "matrix",
               "storage": args.storage, "isolation": args.isolation,
               "process_containment": args.containment,
               "started_at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
               "cases": [], "provider_turn_limit": (4 if args.guided else 2 if args.native_comparison or args.diagnose_antigravity else 9) if args.native else 0}
    with validation_evidence(output, summary) as processes:
        return run_validation(args, binary, output, summary, processes)


def run_validation(args, binary, output, summary, processes):
    frozen = output / "amx-under-test"
    shutil.copyfile(binary, frozen)
    frozen.chmod(0o700)
    summary["binary_sha256"] = hashlib.sha256(frozen.read_bytes()).hexdigest()
    harnesses = {"runner": Path(__file__).resolve(), "process_identity_helper": PROCESS_SOURCE}
    summary["harness_files_sha256"] = {}
    for name, source in harnesses.items():
        content = source.read_bytes()
        summary["harness_files_sha256"][name] = hashlib.sha256(content).hexdigest()
        retained = output / (name + "-under-test.py")
        retained.write_bytes(content)
        retained.chmod(0o600)
    summary["process_cleanup"] = "Linux pidfd signals to admitted controllers and observed descendants; no numeric PID/group signaling"
    live_login = args.native
    summary["environment_profile"] = "explicit-native-login" if live_login else "private-fixtures"
    summary["ambient_provider_keys"] = "excluded"
    # -C does not override GIT_DIR/GIT_WORK_TREE/GIT_INDEX_FILE. Disposable
    # validation must never redirect a Git operation into the caller's project.
    workflow_environment = validation_environment(output, live_login)
    git_environment = dict(workflow_environment, GIT_CONFIG_GLOBAL=os.devnull,
                           GIT_CONFIG_NOSYSTEM="1", GIT_TERMINAL_PROMPT="0", GIT_OPTIONAL_LOCKS="0")
    python = "/usr/bin/python3" if args.isolation == "bubblewrap" else sys.executable

    def git_metadata(repo):
        digest = hashlib.sha256()
        for path in sorted((repo / ".git").rglob("*")):
            relative = path.relative_to(repo / ".git").as_posix()
            if relative == "agentmux-apply.lock":
                continue
            information = path.lstat()
            digest.update(relative.encode() + b"\0" + str(information.st_mode).encode() + b"\0")
            if path.is_file():
                digest.update(path.read_bytes())
        return digest.hexdigest()

    def git(repo, *argv):
        command = ["git", "-C", str(repo), "-c", "core.hooksPath=/dev/null",
                   "-c", "commit.gpgsign=false", *argv]
        proc = processes.launch(command, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                                start_new_session=True, env=git_environment)
        stdout, stderr = processes.communicate(proc, timeout=20)
        if proc.returncode:
            raise subprocess.CalledProcessError(proc.returncode, command, output=stdout, stderr=stderr)
        return stdout

    def project(name, implementation=False):
        repo = output / name
        repo.mkdir(mode=0o700)
        git(repo, "init", "-q")
        # Quiesce fixture-owned Git writers before the first commit. Strict
        # metadata fingerprints must not race detached automatic maintenance.
        git(repo, "config", "--local", "maintenance.auto", "false")
        git(repo, "config", "--local", "gc.auto", "0")
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
        summary["diagnostic"] = {"provider":"antigravity","model":"gemini-3.8-flash-low","native":True,
                                 "provider_turn_limit":2,"cases_completed":False,"cases_passed":False,
                                 "evidence_scope":"diagnostic-case-results",
                                 "completion_record":str(output/"validation.json"),
                                 "environment_profile":"explicit-native-login","ambient_provider_keys":"excluded","cases":results}
        persist_validation(output, summary)
        for explicit_print in (False,True):
            label="explicit-print" if explicit_print else "implicit-stream"
            entry={"name":label,"passed":False}
            summary["cases"].append(entry)
            persist_validation(output, summary)
            diagnostic_repo=project(label)
            command=["agy","--model","gemini-3.8-flash-low","--mode","accept-edits",
                     "--print-timeout","60s","--log-file",str(output/(label+".log")),
                     "--input-format","stream-json","--output-format","stream-json",
                     "--disable-slash-commands"]
            if explicit_print:
                command.append("--print")
            prompt="Create greeting.txt containing exactly AG_OK followed by a newline using your native file tools. Do not delegate or use other models. Report what you changed."
            request=json.dumps({"event":"user","message":{"content":prompt}}).encode()+b"\n"
            proc=processes.launch(command,cwd=diagnostic_repo,stdin=subprocess.PIPE,
                                 stdout=subprocess.PIPE,stderr=subprocess.PIPE,start_new_session=True,
                                 env=workflow_environment)
            stdout,stderr=processes.communicate(proc,input=request,timeout=70)
            for kind,data in (("stdout",stdout),("stderr",stderr)):
                path=output/(label+"."+kind);path.write_bytes(data);path.chmod(0o600)
            try:
                protocol = antigravity_diagnostic_evidence(stdout, stderr)
            except ValueError as error:
                entry["error"] = type(error).__name__
                raise
            artifact=diagnostic_repo/"greeting.txt"
            result={"name":label,"exit_code":proc.returncode,"artifact_correct":artifact.exists() and artifact.read_bytes()==b"AG_OK\n",**protocol}
            private_json(output/(label+".events.json"),result)
            results.append({k:v for k,v in result.items() if k!="events"})
            entry["passed"]=proc.returncode == 0 and result["artifact_correct"] and result["protocol_passed"]
            results[-1]["passed"] = entry["passed"]
            summary["diagnostic"].update(cases_completed=len(results)==2,
                                          cases_passed=len(results)==2 and all(case["passed"] for case in results))
            persist_validation(output, summary)
            print(json.dumps(results[-1]),flush=True)
        persist_validation(output, summary)
        print("Evidence: "+str(output/"diagnostic.json"),flush=True)
        return 0 if all(case["passed"] for case in summary["cases"]) else 1

    check = {"name": "external-normalization-contract",
             "command": [python, "-B", "-c", CONTRACT], "timeout_seconds": 10}
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
                       "command": [python, "-c", code]} for name in ("first", "second")]
    repo = project("project")
    source_git = git_metadata(repo)
    plan = {"version": 1, "repository": str(repo), "execution": "local",
            "storage": args.storage,
            "process_containment": args.containment,
            "mode": "compare", "goal": GOAL, "timeout_seconds": 240,
            "max_attempts": 1, "max_parallel": 2,
            "candidates": candidates, "checks": [check]}
    if args.isolation == "bubblewrap":
        plan["isolation"] = {"mode": "bubblewrap", "network": "none"}
    plan_file = output / "compare-plan.json"
    private_json(plan_file, plan)
    oracle = WorkflowOracle(output, plan, workflow_environment, processes)
    oracle.plans[str(plan_file)] = plan
    summary["semantic_oracle"] = ("retained-artifacts-and-isolated-contract-evidence" if args.isolation == "bubblewrap"
                                  else "retained-artifacts-and-independent-contract")

    def flow(name, *argv, expected=0, models=False, root_command="flow", expect_json=True, nested_verification=False):
        start = time.monotonic()
        command = [str(frozen), root_command, *map(str, argv)]
        if models and args.native:
            command.append("--allow-models")
        # Admission is recorded before launch. Exceptions and interruption must
        # leave the admitted scenario present and nonpassing in retained evidence.
        entry = {"name": name, "passed": False}
        summary["cases"].append(entry)
        persist_validation(output, summary)
        try:
            proc = processes.launch(command, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                                    start_new_session=True, env=workflow_environment, cwd=output)
            stdout, stderr = processes.communicate(proc, timeout=300)
            entry["exit_code"] = proc.returncode
            for kind, data in (("stdout", stdout), ("stderr", stderr)):
                path = output / (name + "." + kind)
                path.write_bytes(data)
                path.chmod(0o600)
            envelope = (strict_json(stdout) if stdout else {}) if expect_json else {"ok": proc.returncode == expected, "result": {}}
            result = workflow_result(envelope, list_result=root_command == "work" and argv[0] == "list")
            ok = proc.returncode == expected and envelope["ok"] is (expected == 0)
            if expected != 0:
                # A negative control must reach its acceptance assertions rather
                # than merely fail with the same exit code for another reason.
                verification = result
                if nested_verification:
                    outcomes = result.get("candidates", [])
                    verification = outcomes[0].get("verification") if len(outcomes) == 1 else None
                ok = ok and rejected_by_contract(verification, check, plan)
            elif ok:
                oracle.validate(name, result, argv, root_command)
            if isinstance(result, dict) and result.get("candidates"):
                if "harness" in result["candidates"][0]:
                    entry["candidates"] = [{k: candidate.get(k) for k in
                         ("id", "harness", "model", "state", "attempts", "usage")}
                         for candidate in result["candidates"]]
                else:
                    entry["trial_summaries"] = result["candidates"]
            print(name + ": " + ("PASS" if ok else "FAIL"), flush=True)
            entry["passed"] = ok
            return ok, result
        except BaseException as error:
            entry.update(passed=False, error=type(error).__name__)
            if isinstance(error, ValueError):
                entry["reason"] = str(error)
            if not isinstance(error, (OSError, subprocess.TimeoutExpired, ValueError)):
                raise
            print(name + ": FAIL (" + type(error).__name__ + ")", flush=True)
            return False, None
        finally:
            entry["duration_seconds"] = round(time.monotonic() - start, 3)
            persist_validation(output, summary)

    if args.guided:
        catalog=output/"history"
        if not args.native:
            fixtures=output/"fixture-providers";fixtures.mkdir(mode=0o700)
            for name in ("codex","agy","claude"):
                fixture=fixtures/name
                code=("#!"+python+"\nimport sys,json,uuid\nfrom pathlib import Path\nsys.dont_write_bytecode=True\nprompt=sys.stdin.read()\n")
                if name=="codex":
                    code+="Path('tag_normalizer.py').write_text("+repr(IMPLEMENTATION)+")\n"
                    code+="print(json.dumps({'type':'thread.started','thread_id':str(uuid.uuid4())}))\nprint(json.dumps({'type':'item.completed','item':{'type':'agent_message','text':'Implemented tag_normalizer.py; the declared checks verify it.'}}))\nprint(json.dumps({'type':'turn.completed','usage':{'input_tokens':0,'output_tokens':0}}))\n"
                else:
                    code+="assert Path('tag_normalizer.py').is_file()\nverdict={'decision':'approve','summary':'Protocol fixture inspected the retained file; acceptance assertions run separately.','findings':[]}\n"
                    if name == "claude":
                        code+="print(json.dumps({'type':'result','subtype':'success','result':json.dumps(verdict),'is_error':False,'permission_denials':[],'usage':{'input_tokens':0,'output_tokens':0}}))\n"
                    else:
                        code+="print(json.dumps({'event':'result','result':{'status':'SUCCESS','response':json.dumps(verdict),'usage':{'input_tokens':0,'output_tokens':0}}}))\n"
                fixture.write_text(code);fixture.chmod(0o700)
            workflow_environment['PATH']=str(fixtures)+os.pathsep+os.environ.get('PATH','')
            summary['fixture_providers']=True
        options=["--repo",repo,"--catalog",catalog,"--timeout","300s","--attempts","1","--storage",args.storage,"--containment",args.containment,"--json"]
        if args.isolation == "bubblewrap":
            options.extend(["--isolation","bubblewrap","--network","none","--read-only-path",fixtures,"--reviewer","claude"])
        flow("work-preview",GOAL,*options,"--",*check['command'],root_command="work")
        summary['cases'].append({'name':'preview-no-catalog-writes','passed':not catalog.exists()})
        worked,report=flow("work-run",GOAL,*options,"--allow-models","--",*check['command'],root_command="work")
        unchanged=git(repo,"status","--porcelain")==b''
        summary['cases'].append({'name':'guided-source-unchanged','passed':unchanged})
        if args.storage == "private":
            summary['cases'].append({'name':'private-source-git-unchanged','passed':git_metadata(repo)==source_git})
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
    if args.storage == "private":
        summary['cases'].append({'name':'private-source-git-unchanged','passed':git_metadata(repo)==source_git})
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
    oracle.plans[str(output / "mission-plan.json")] = mission
    flow("mission", "mission", output / "mission-plan.json", "--output", output / "mission", models=True)
    flow("lab", "lab", plan_file, "--output", output / "lab", "--repetitions", "2", models=True)

    watch_repo = project("watch-project", implementation=True)
    watch_candidate = candidates[1] if args.native else {
        "id": "fixture-reviewer", "harness": "command",
        "command": [python, "-B", "-c", CONTRACT],
    }
    watch = dict(plan, repository=str(watch_repo), candidates=[watch_candidate], timeout_seconds=120,
                 goal="Review the existing normalization implementation against the declared contract without changing any files.")
    private_json(output / "watch-plan.json", watch)
    oracle.plans[str(output / "watch-plan.json")] = watch
    flow("watch", "watch", output / "watch-plan.json", "--output", output / "watch",
         "--interval", "1s", "--max-runs", "1", "--duration", "120s", models=True)

    if compare_ok:
        applied, _ = flow("apply", "apply", compare_dir, "--candidate", candidates[0]["id"])
        if applied:
            flow("inspect-application", "recover", compare_dir, "--candidate", candidates[0]["id"])
            private_json(output / "checks.json", [check])
            if args.isolation == "bubblewrap":
                acceptance = dict(plan, candidates=[{"id":"check-only","harness":"command","command":[python,"-c","print('Independent acceptance fixture')"]}])
                private_json(output/"isolated-check-plan.json",acceptance)
                oracle.plans[str(output/"isolated-check-plan.json")] = acceptance
                flow("isolated-check", "run", output/"isolated-check-plan.json", "--output", output/"isolated-verification")
            else:
                flow("check", "check", "--repo", repo, "--checks", output / "checks.json",
                     "--output", output / "verification.json", "--containment", args.containment, "--storage", args.storage)

    bad_repo = project("bad-project")
    (bad_repo / "tag_normalizer.py").write_text("def normalize_tags(tags):\n    return ['incorrect']\n")
    if not (output / "checks.json").exists():
        private_json(output / "checks.json", [check])
    if args.isolation == "bubblewrap":
        bad = dict(plan, repository=str(bad_repo), candidates=[{"id":"check-only","harness":"command","command":[python,"-c","print('Negative acceptance fixture')"]}])
        private_json(output/"isolated-bad-plan.json",bad)
        flow("reject-bad-implementation", "run", output/"isolated-bad-plan.json", "--output", output/"isolated-bad-verification", expected=1, nested_verification=True)
    else:
        flow("reject-bad-implementation", "check", "--repo", bad_repo,
             "--checks", output / "checks.json", "--output", output / "bad-verification.json", "--containment", args.containment, "--storage", args.storage, expected=1)
    return 0 if all(case["passed"] for case in summary["cases"]) else 1


if __name__ == "__main__":
    raise SystemExit(main())
