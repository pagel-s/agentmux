#!/usr/bin/env python3
"""Offline regression controls for sampled operational reports. No provider calls."""
import copy
import hashlib
import importlib.util
import io
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import time
import tracemalloc
import unittest


SCRIPT = Path(__file__).with_name("summarize-observations.py")
SPEC = importlib.util.spec_from_file_location("observations", SCRIPT)
observations = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(observations)


def sample(second=0):
    at = "2026-11-04T12:00:%02dZ" % second
    return {"schema_version": 1, "observer_version": "fixture", "observed_at": at,
            "state": "/private/workspace/state", "workspace": "private-session", "complete": True,
            "broker": "connected", "snapshot": {
                "observed_at": at, "tasks": 5, "task_states": {"verified": 2, "running": 1, "failed": 2},
                "deliveries": {"acknowledged": 2, "dispatching": 1, "failed": 2},
                "effective_deliveries": {"acknowledged": 2, "dispatching": 1, "failed": 2},
                "recovery_eligible": 0, "attention_count": 2, "active_count": 1,
                "attention": [], "attention_more": True, "active": [], "active_more": True,
                "capacity": {"records": 50, "max_records": 1000, "requests": 20, "max_requests": 500,
                             "messages": 3, "max_messages": 100, "tasks": 5, "max_tasks": 50}},
            "runtime": {"state": "running", "attempt_id": "private-attempt", "agents": {}},
            "native": [{"agent": "private-agent", "connected": True, "readiness_observed_at": at, "pending_count": 0}],
            "native_more": False,
            "storage": {"files": {"database": {"state": "present", "bytes": 100},
                                  "wal": {"state": "present", "bytes": 40},
                                  "shared_memory": {"state": "absent", "bytes": 0}}}}


def encoded(*samples):
    return b"".join(json.dumps(value, separators=(",", ":")).encode() + b"\n" for value in samples)


def report(*samples, gap=5):
    return observations.summarize(io.BytesIO(encoded(*samples)), gap * 10**9)


class ReportTests(unittest.TestCase):
    def test_v13_metadata_and_original_byte_provenance(self):
        raw = json.dumps(sample(), ensure_ascii=False, indent=None).encode() + b"\r\n"
        result = observations.summarize(io.BytesIO(raw), 5 * 10**9)
        self.assertEqual(result["input"]["sha256"], hashlib.sha256(raw).hexdigest())
        self.assertEqual(result["input"]["bytes"], len(raw))
        self.assertEqual(result["samples"], 1)
        self.assertEqual(result["gauges"]["retained_tasks"]["last"], 5)
        self.assertEqual(result["retained_state_gauges"]["task_states"]["verified"]["last"], 2)
        self.assertEqual(result["retained_state_gauges"]["task_states"]["proposed"]["last"], 0)
        self.assertEqual(result["collection_duration_ms"]["collection"]["known_samples"], 0)
        self.assertIsNone(result["collection_duration_ms"]["collection"]["peak_known"])
        self.assertEqual(result["native_gauges"]["pending_total"]["last"], 0)

    def test_unknown_missing_and_partial_do_not_become_zero(self):
        partial = {"schema_version": 1, "observed_at": "2026-11-04T12:00:01Z",
                   "state": sample()["state"], "complete": False, "broker": "unavailable"}
        result = report(partial, sample(2), partial)
        self.assertEqual(result["completeness_samples"], {"complete": 1, "partial": 2, "unknown": 0})
        gauge = result["gauges"]["retained_tasks"]
        self.assertIsNone(gauge["first"])
        self.assertIsNone(gauge["last"])
        self.assertEqual((gauge["known_samples"], gauge["unknown_samples"], gauge["peak_known"]), (1, 2, 5))
        self.assertEqual(result["component_availability_samples"]["native"]["not_attempted"], 2)
        self.assertEqual(result["storage"]["shared_memory"]["state_samples"], {"present": 0, "absent": 1, "unknown": 2})
        self.assertIsNone(result["storage"]["shared_memory"]["first"]["bytes"])
        self.assertEqual(result["native_gauges"]["pending_total"]["unknown_samples"], 2)
        missing = report({"schema_version": 1, "observed_at": "2026-11-04T12:00:00Z", "state": sample()["state"]})
        self.assertEqual(missing["completeness_samples"]["unknown"], 1)

    def test_clock_gaps_backwards_duplicates_and_nanosecond_precision(self):
        samples = [sample(0), sample(1), sample(20), sample(10), sample(10)]
        result = report(*samples, gap=3)
        self.assertEqual(result["time"]["span_seconds"], 20)
        self.assertEqual(result["time"]["gaps_over_limit"], 1)
        self.assertEqual(result["time"]["maximum_forward_adjacent_gap_seconds"], 19)
        self.assertEqual(result["time"]["backward_adjacent_timestamps"], 1)
        self.assertEqual(result["time"]["repeated_adjacent_timestamps"], 1)
        self.assertEqual(result["time"]["last_observed_at"], "2026-11-04T12:00:10Z")
        first, second = sample(), sample()
        first["observed_at"] = "2026-11-04T13:00:00.000000001+01:00"
        second["observed_at"] = "2026-11-04T12:00:00.000000002Z"
        result = report(first, second)
        self.assertEqual(result["time"]["span_seconds"], 1e-9)
        self.assertEqual(result["time"]["first_observed_at"], "2026-11-04T12:00:00.000000001Z")

    def test_bounded_gap_and_component_transition_episodes(self):
        samples = []
        for index in range(80):
            value = sample()
            value["observed_at"] = "2026-11-04T12:%02d:%02dZ" % divmod(index * 10, 60)
            value["broker"] = "connected" if index % 2 == 0 else "unavailable"
            samples.append(value)
        result = report(*samples)
        self.assertEqual(result["time"]["gaps_over_limit"], 79)
        self.assertEqual(len(result["time"]["gap_episodes"]), observations.MAX_EPISODES)
        self.assertEqual(result["time"]["gap_episodes_omitted"], 79 - observations.MAX_EPISODES)
        self.assertEqual(len(result["availability_transitions"]), observations.MAX_EPISODES)
        self.assertGreater(result["availability_transitions_omitted"], 0)

    def test_exact_state_identity_required_and_mixed_state_rejected(self):
        for identity in (None, "", 12):
            value = sample()
            value["state"] = identity
            with self.subTest(identity=identity), self.assertRaises(observations.InvalidCapture):
                report(value)
        second = sample(1)
        second["state"] += "/../state"
        with self.assertRaisesRegex(observations.InvalidCapture, "mixed exact state"):
            report(sample(), second)

    def test_gauges_are_not_counter_deltas_or_attempt_correlations(self):
        first, second, third = sample(0), sample(1), sample(2)
        second["snapshot"]["tasks"] = 2
        second["snapshot"]["task_states"]["verified"] = 0
        second["runtime"]["attempt_id"] = "new-private-attempt"
        second["snapshot"]["active"] = [{"id": "same-task", "progress": {"event_count": 0}}]
        first["snapshot"]["active"] = [{"id": "same-task", "progress": {"event_count": 9999}}]
        third["runtime"].pop("attempt_id")
        result = report(first, second, third)
        self.assertEqual(result["gauges"]["retained_tasks"]["minimum_known"], 2)
        self.assertEqual(result["gauges"]["retained_tasks"]["peak_known"], 5)
        self.assertEqual(result["runtime_attempt_observations"]["adjacent_known_attempt_changes"], 1)
        published = json.dumps(result)
        for forbidden in ("throughput", "success_rate", "event_count", "same-task", "private-attempt"):
            if forbidden == "throughput":
                self.assertIn("not uptime, task quality, throughput", result["scope"])
            else:
                self.assertNotIn(forbidden, published)

    def test_unknown_attempt_breaks_change_adjacency(self):
        first, middle, last = sample(0), sample(1), sample(2)
        middle["runtime"]["attempt_id"] = ""
        last["runtime"]["attempt_id"] = "different"
        self.assertEqual(report(first, middle, last)["runtime_attempt_observations"]["adjacent_known_attempt_changes"], 0)

    def test_successful_runtime_inspection_can_observe_an_unavailable_runtime(self):
        value = sample()
        value["runtime"]["state"] = "unavailable"
        result = report(value)
        self.assertEqual(result["runtime_state_samples"]["unavailable"], 1)
        self.assertEqual(result["component_availability_samples"]["runtime"]["observed"], 1)

    def test_dynamic_capacity_ceilings_do_not_use_current_ceiling_for_past_samples(self):
        first, second, third = sample(0), sample(1), sample(2)
        first["snapshot"]["capacity"].update(records=90, max_records=100)
        second["snapshot"]["capacity"].update(records=100, max_records=1000)
        third["snapshot"]["capacity"].update(records=500, max_records=200)
        value = report(first, second, third)["capacity"]["records"]
        self.assertEqual(value["peak_known_utilization_ratio"], 2.5)
        self.assertEqual(value["adjacent_known_ceiling_changes"], 2)
        self.assertEqual(value["above_ceiling_samples"], 1)
        self.assertEqual((value["ceiling"]["first"], value["ceiling"]["last"]), (100, 200))

    def test_storage_absence_unknown_and_reset_remain_distinct(self):
        first, second, third, fourth = (sample(index) for index in range(4))
        second["storage"]["files"]["wal"] = {"state": "absent", "bytes": 0}
        third["storage"]["files"]["wal"] = {"state": "unknown", "bytes": None, "problem": "private raw problem"}
        fourth["storage"]["files"]["wal"] = {"state": "present", "bytes": 80}
        value = report(first, second, third, fourth)["storage"]["wal"]
        self.assertEqual(value["state_samples"], {"present": 2, "absent": 1, "unknown": 1})
        self.assertEqual(value["adjacent_known_decreases"], 1)
        self.assertEqual(value["adjacent_known_increases"], 0)
        self.assertEqual(value["bytes"]["peak_known"], 80)
        self.assertEqual(value["bytes"]["unknown_samples"], 1)

    def test_native_nullable_pending_and_truncated_inventory_never_mean_total_zero(self):
        first, second, third = sample(0), sample(1), sample(2)
        first["native"][0]["pending_count"] = None
        second["native"][0]["pending_count"] = 7
        second["native_more"] = True
        third["native"] = []
        result = report(first, second, third)
        pending = result["native_gauges"]["pending_total"]
        self.assertEqual((pending["known_samples"], pending["unknown_samples"], pending["last"]), (1, 2, 0))
        self.assertEqual(result["native_gauges"]["known_pending_sum"]["peak_known"], 7)
        self.assertEqual(result["native_gauges"]["unknown_pending_agents"]["peak_known"], 1)
        self.assertEqual(result["truncated_inventory_samples"]["native"]["yes"], 1)

    def test_additive_diagnostics_timings_and_error_code_whitelist(self):
        value = sample()
        value["diagnostics"] = {"collection_duration_ms": 12.5, "components": {
            "broker": {"state": "observed", "error_code": None, "duration_ms": 10},
            "runtime": {"state": "not_recorded", "error_code": None, "duration_ms": 1},
            "native": {"state": "unavailable", "error_code": "timeout", "duration_ms": 2},
            "storage": {"state": "unavailable", "error_code": "private injected code", "duration_ms": 0.5}}}
        result = report(sample(), value)
        self.assertEqual(result["collection_duration_ms"]["collection"]["last"], 12.5)
        self.assertEqual(result["collection_duration_ms"]["collection"]["unknown_samples"], 1)
        self.assertEqual(result["diagnostic_error_code_samples"]["native"]["timeout"], 1)
        self.assertEqual(result["diagnostic_error_code_samples"]["storage"]["other"], 1)
        self.assertIsNone(result["native_gauges"]["pending_total"]["last"])
        self.assertEqual(result["truncated_inventory_samples"]["native"]["unknown"], 1)
        self.assertEqual(result["component_availability_samples"]["runtime"]["not_recorded"], 1)
        self.assertNotIn("private injected", json.dumps(result))

    def test_reported_reason_codes_are_bounded_and_do_not_imply_health(self):
        values = []
        cases = [
            ("broker", "unavailable", "unavailable", "operational_read_busy", "operational_read_busy"),
            ("native", "unavailable", "canceled", "query_budget_exhausted", "query_budget_exhausted"),
            ("broker", "unavailable", "canceled", "shutdown", "shutdown"),
            ("native", "unavailable", "unavailable", "shutdown", "shutdown"),
            ("broker", "unavailable", "unavailable", None, "not_recorded"),
            ("broker", "unavailable", "unavailable", "private reason sentinel", "other"),
            ("broker", "unavailable", "forbidden", "query_budget_exhausted", "other"),
            ("broker", "observed", None, "operational_read_busy", "other"),
            ("native", "not_attempted", None, "shutdown", "other"),
            ("runtime", "unavailable", "canceled", "shutdown", "other"),
            ("storage", "unavailable", "unavailable", "operational_read_busy", "other"),
        ]
        expected = {name: {key: 0 for key in (*observations.REASON_CODES, "other", "not_recorded")} for name in observations.COMPONENTS}
        for index, (name, state, code, reason, bucket) in enumerate(cases):
            value = sample(index)
            value["diagnostics"] = {"components": {name: {
                "state": state, "error_code": code, "reason_code": reason}}}
            values.append(value)
            for component in expected:
                expected[component][bucket if component == name else "not_recorded"] += 1
        # A legacy capture has neither diagnostics nor subcause metadata.
        values.append(sample(len(values)))
        for component in expected:
            expected[component]["not_recorded"] += 1
        result = report(*values)
        self.assertEqual(result["diagnostic_reason_code_samples"], expected)
        self.assertNotIn("private reason sentinel", json.dumps(result))
        self.assertNotIn("none", result["diagnostic_reason_code_samples"]["broker"])
        self.assertEqual(result["component_availability_samples"]["native"]["not_attempted"], 1)
        self.assertEqual(result["component_availability_samples"]["broker"]["unavailable"], 5)
        self.assertIn("diagnostic timings/codes", observations.human(result))

    def test_unavailable_broker_does_not_publish_partial_snapshot_as_known(self):
        value = sample()
        value["broker_error"] = "private raw failure"
        result = report(value)
        self.assertIsNone(result["gauges"]["retained_tasks"]["last"])
        self.assertIsNone(result["capacity"]["records"]["count"]["last"])

    def test_unknown_map_labels_are_aggregated_without_being_published(self):
        value = sample()
        value["snapshot"]["task_states"]["private-unrecognized-state"] = 3
        result = report(value)
        self.assertEqual(result["retained_state_gauges"]["task_states"]["other"]["last"], 3)
        self.assertNotIn("private-unrecognized-state", json.dumps(result))

    def test_privacy_injected_paths_ids_macs_errors_bodies_unknown_fields(self):
        value = sample()
        marker = "SENSITIVE_SENTINEL_d9f7"
        value.update(state="/" + marker, workspace=marker, observer_version=marker, source=marker,
                     broker_error=marker, runtime_error=marker, native_error=marker,
                     arbitrary={marker: [marker]})
        value["runtime"].update(attempt_id=marker, controller=marker, problem=marker,
                                agents={marker: {"pane_id": marker}})
        value["snapshot"]["attention"] = [{"id": marker, "to": marker, "reason": marker,
                                              "trace": {"mac": marker}, "text": marker}]
        value["native"][0].update(agent=marker, adapter=marker, active={"id": marker, "evidence": marker})
        result = report(value)
        self.assertNotIn(marker, json.dumps(result))
        self.assertNotIn(marker, observations.human(result))
        for arguments in (("-", "--json"), ("-",)):
            process = subprocess.run([sys.executable, str(SCRIPT), *arguments], input=encoded(value), capture_output=True)
            self.assertEqual(process.returncode, 0, process.stderr)
            self.assertNotIn(marker.encode(), process.stdout + process.stderr)

    def test_duplicate_keys_nonfinite_numbers_bad_utf8_and_truncation_are_rejected(self):
        good = encoded(sample())
        cases = (b"", b"\n", b"{}\n", b"[1]\n", good[:-1], good[:-8],
                 good.replace(b'"schema_version":1', b'"schema_version":1,"schema_version":1'),
                 good.replace(b'"pending_count":0', b'"pending_count":NaN'),
                 good.replace(b'"pending_count":0', b'"pending_count":Infinity'),
                 good.replace(b'"pending_count":0', b'"pending_count":1e999'),
                 good.replace(b'"fixture"', b'"\xff"'))
        for raw in cases:
            with self.subTest(raw=raw[:30]), self.assertRaises(observations.InvalidCapture):
                observations.summarize(io.BytesIO(raw), 5 * 10**9)
        nested_duplicate = good.replace(b'"pending_count":0', b'"pending_count":0,"pending_count":1')
        with self.assertRaisesRegex(observations.InvalidCapture, "duplicate"):
            observations.summarize(io.BytesIO(nested_duplicate), 5 * 10**9)

    def test_unsupported_schema_invalid_types_dates_and_storage_fail_closed(self):
        changes = (("schema_version", True), ("schema_version", 2), ("complete", "yes"),
                   ("observed_at", "2026-11-04T12:00:00+00:99"), ("observed_at", "private invalid timestamp"),
                   ("snapshot", []), ("runtime", []), ("native", {}), ("storage", []))
        for key, invalid in changes:
            value = sample()
            value[key] = invalid
            with self.subTest(key=key, value=invalid), self.assertRaises(observations.InvalidCapture):
                report(value)
        for invalid in (True, -1, "private", 1.5, 2**64):
            value = sample()
            value["snapshot"]["tasks"] = invalid
            with self.subTest(count=invalid), self.assertRaises(observations.InvalidCapture):
                report(value)
        for invalid in ({"state": "absent", "bytes": None}, {"state": "absent", "bytes": 10},
                        {"state": "unknown", "bytes": 0}, {"state": "present", "bytes": None}):
            value = sample()
            value["storage"]["files"]["wal"] = invalid
            with self.subTest(storage=invalid), self.assertRaises(observations.InvalidCapture):
                report(value)

    def test_inventory_and_line_cardinality_bounds(self):
        for name in ("attention", "active", "native"):
            value = sample()
            target = value if name == "native" else value["snapshot"]
            target[name] = [{}] * 201
            with self.subTest(inventory=name), self.assertRaisesRegex(observations.InvalidCapture, "oversized inventory"):
                report(value)
            target[name] = [{}] * 200
            report(value)
        value = sample()
        value["unused"] = "x" * observations.MAX_LINE_BYTES
        with self.assertRaisesRegex(observations.InvalidCapture, "oversized JSONL"):
            report(value)
        value = sample()
        value["snapshot"]["task_states"] = {"key%d" % index: 1 for index in range(65)}
        with self.assertRaisesRegex(observations.InvalidCapture, "oversized gauge map"):
            report(value)

    def test_large_maximum_known_inventory_with_bounded_metadata_is_accepted(self):
        value = sample()
        task = {"id": "task_" + "x" * 64, "to": "agent", "reason": "x" * 1000,
                "trace": {"mac": "x" * 64, "bytes": 200, "records": 2},
                "progress": {"worker_observed_at": value["observed_at"], "event_count": 100}}
        value["snapshot"]["attention"] = [task] * 200
        value["snapshot"]["active"] = [task] * 200
        value["native"] *= 200
        self.assertLess(len(encoded(value)), observations.MAX_LINE_BYTES)
        self.assertEqual(report(value)["samples"], 1)

    def test_cli_errors_never_echo_input_path_raw_error_or_input_fragment(self):
        marker = "PRIVATE_PATH_SENTINEL_4f6b"
        cases = ((["/missing/" + marker], b""), (["-", "--json"], ('{"' + marker + '":NaN}\n').encode()),
                 (["-", "--max-gap", marker], b""), (["-", "--" + marker], b""))
        for arguments, raw in cases:
            with self.subTest(arguments=arguments):
                process = subprocess.run([sys.executable, str(SCRIPT), *arguments], input=raw, capture_output=True)
                self.assertNotEqual(process.returncode, 0)
                self.assertEqual(process.stdout, b"")
                self.assertNotIn(marker.encode(), process.stderr)
                self.assertNotIn(b"Traceback", process.stderr)

    def test_custom_gap_duration_and_human_cli(self):
        process = subprocess.run([sys.executable, str(SCRIPT), "-", "--max-gap", "250ms", "--json"],
                                 input=encoded(sample(0), sample(1)), capture_output=True)
        self.assertEqual(process.returncode, 0, process.stderr)
        self.assertEqual(json.loads(process.stdout)["time"]["gaps_over_limit"], 1)
        for text in ("0s", "-1s", "1", "NaN", "1d", "1e5s"):
            with self.subTest(duration=text), self.assertRaises(Exception):
                observations.duration(text)

    def test_streaming_capture_has_constant_memory_and_bounded_output(self):
        raw = encoded(sample())

        class RepeatedLines:
            def __init__(self, repeats):
                self.remaining = repeats

            def readline(self, limit):
                self.assertion = limit
                self.remaining -= 1
                return raw if self.remaining >= 0 else b""

        def measure(repeats):
            stream = RepeatedLines(repeats)
            tracemalloc.start()
            result = observations.summarize(stream, 5 * 10**9)
            _, peak = tracemalloc.get_traced_memory()
            tracemalloc.stop()
            self.assertEqual(stream.assertion, observations.MAX_LINE_BYTES + 1)
            self.assertEqual(result["samples"], repeats)
            self.assertEqual(result["input"]["bytes"], len(raw) * repeats)
            self.assertLess(len(json.dumps(result)), 30000)
            return peak

        small, large = measure(30), measure(4000)
        self.assertLess(large, small + 1024 * 1024)


@unittest.skipUnless(os.environ.get("AGENTMUX_INTEGRATION_BINARY"), "set AGENTMUX_INTEGRATION_BINARY to test an actual built CLI")
class ActualCLIIntegrationTests(unittest.TestCase):
    def test_command_only_live_and_stopped_capture_round_trip(self):
        binary = str(Path(os.environ["AGENTMUX_INTEGRATION_BINARY"]).resolve(strict=True))
        root = Path(tempfile.mkdtemp(prefix="amx-observe-report-cli-"))
        state, project = root / "state", root / "project"
        project.mkdir()
        (root / "home").mkdir()
        # Deliberately omit API keys, provider settings and user tmux context.
        environment = {"PATH": os.defpath, "HOME": str(root / "home"), "SHELL": "/bin/sh",
                       "TERM": "xterm-256color", "LC_ALL": "C.UTF-8"}
        source = project / "source.txt"
        source.write_bytes(b"Immutable command-only observation fixture.\n")
        config = root / "config.json"
        config.write_text(json.dumps({"version": 2, "mode": "managed", "session": "observation-report-fixture",
            "agents": {"worker": {"harness": "command", "sandbox": "local", "network": "host",
                "workdir": str(project), "command": ["/bin/sh", "-c",
                    "cat >/dev/null; printf RUNNING > started; while [ ! -f finish ]; do sleep 0.05; done; printf OBSERVATION_FIXTURE_DONE"]}},
            "limits": {"max_runtime_seconds": 180}, "policy": {"default": "deny"}}))
        original_config, original_source = config.read_bytes(), source.read_bytes()

        def run(*arguments, expected=0):
            result = subprocess.run([binary, *map(str, arguments)], env=environment,
                                    capture_output=True, timeout=45)
            self.assertEqual(result.returncode, expected, "command-only integration CLI returned an unexpected status")
            return result.stdout

        def call(operation, body):
            raw = run("managed", "call", operation, "--credential-file", state / "owner.json", "--json", json.dumps(body))
            response = json.loads(raw)
            self.assertIs(response.get("ok"), True)
            return response["result"]

        def task(task_id):
            return call("task.get", {"id": task_id})["task"]

        def until(check):
            deadline = time.monotonic() + 15
            while time.monotonic() < deadline:
                if value := check():
                    return value
                time.sleep(0.05)
            self.fail("command-only integration fixture did not reach its expected state")

        def authority(value):
            return {key: value.get(key) for key in ("id", "to", "state", "delivery", "version", "result")}

        def state_files():
            return {str(path.relative_to(state)): hashlib.sha256(path.read_bytes()).hexdigest()
                    for path in state.rglob("*") if path.is_file() and not path.is_symlink()}

        def owned_tmux_stopped():
            result = subprocess.run(["tmux", "-S", str(state / "tmux.sock"), "has-session"],
                                    env=environment, capture_output=True, timeout=5)
            return result.returncode != 0

        shutdown_ok = False
        try:
            response = json.loads(run("managed", "up", config, "--state", state, "--broker-sandbox", "local"))
            self.assertIs(response.get("ok"), True)
            task_id = call("instruct", {"to": ["worker"], "text": "Run the command-only observation fixture."})["task"]["id"]
            until(lambda: (project / "started").exists())
            before = task(task_id)
            self.assertEqual((before["state"], before["delivery"]), ("running", "dispatching"))
            first = run("observe", "--state", state, "--json")
            live = json.loads(first)
            self.assertIs(live["complete"], True)
            self.assertEqual(live["snapshot"]["active_count"], 1)
            self.assertEqual(live["diagnostics"]["components"]["broker"]["state"], "observed")
            self.assertEqual(authority(task(task_id)), authority(before), "observation changed task authority")
            (project / "finish").write_bytes(b"finish\n")
            completed = until(lambda: (value if (value := task(task_id))["state"] == "submitted" else None))
            self.assertEqual(completed["result"], "OBSERVATION_FIXTURE_DONE")
            second = run("observe", "--state", state, "--json")
            self.assertEqual(authority(task(task_id)), authority(completed), "observation changed completed task authority")
            run("managed", "down", "--state", state)
            shutdown_ok = owned_tmux_stopped()
            self.assertTrue(shutdown_ok, "owned integration tmux runtime remained after shutdown")
            third = run("observe", "--state", state, "--json", expected=1)
            stopped = json.loads(third)
            self.assertIs(stopped["complete"], False)
            self.assertIsNone(stopped.get("snapshot"))
            self.assertEqual(stopped["diagnostics"]["components"]["native"]["state"], "not_attempted")
            raw = first + second + third
            capture = root / "observations.jsonl"
            capture.write_bytes(raw)
            frozen_state = state_files()
            process = subprocess.run([sys.executable, str(SCRIPT), str(capture), "--json", "--max-gap", "30s"],
                                     env=environment, capture_output=True, timeout=15)
            self.assertEqual(process.returncode, 0, "offline report rejected actual CLI capture")
            result = json.loads(process.stdout)
            self.assertEqual(result["samples"], 3)
            self.assertEqual(result["completeness_samples"], {"complete": 2, "partial": 1, "unknown": 0})
            self.assertEqual(result["component_availability_samples"]["broker"]["observed"], 2)
            self.assertEqual(result["component_availability_samples"]["broker"]["unavailable"], 1)
            self.assertEqual(result["collection_duration_ms"]["collection"]["known_samples"], 3)
            self.assertEqual(result["gauges"]["active_execution_leases"]["peak_known"], 1)
            self.assertIsNone(result["gauges"]["retained_tasks"]["last"])
            self.assertEqual(result["input"]["sha256"], hashlib.sha256(raw).hexdigest())
            self.assertNotIn(str(root).encode(), process.stdout + process.stderr)
            self.assertNotIn(task_id.encode(), process.stdout + process.stderr)
            self.assertEqual(capture.read_bytes(), raw)
            self.assertEqual(state_files(), frozen_state, "offline reporting changed stopped fixture state")
            self.assertEqual(config.read_bytes(), original_config)
            self.assertEqual(source.read_bytes(), original_source)
        finally:
            if not shutdown_ok and state.exists():
                try:
                    cleanup = subprocess.run([binary, "managed", "down", "--state", str(state)],
                                             env=environment, capture_output=True, timeout=45)
                    shutdown_ok = cleanup.returncode == 0 and owned_tmux_stopped()
                except (OSError, subprocess.TimeoutExpired):
                    shutdown_ok = False
            if shutdown_ok or not state.exists():
                shutil.rmtree(root)
            else:
                self.fail("owned command-only integration shutdown failed; private fixture retained for inspection")


if __name__ == "__main__":
    unittest.main()
