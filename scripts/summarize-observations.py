#!/usr/bin/env python3
"""Summarize bounded, sampled `amx observe --watch --json` metadata offline.

No input paths, identifiers, free-form errors or unknown fields are published.
Gauges describe retained state at samples, never task throughput or model quality.
"""
import argparse
import datetime as dt
import hashlib
import json
import math
import re
import sys


MAX_LINE_BYTES = 2 * 1024 * 1024
MAX_ITEMS = 200
MAX_EPISODES = 16
MAX_INTEGER = 2**63 - 1
TASK_STATES = ("proposed", "running", "submitted", "verified", "failed", "cancelled")
DELIVERIES = ("pending", "dispatching", "acknowledged", "failed", "uncertain",
              "cancel_requested", "cancel_uncertain", "stopped")
RUNTIME_STATES = ("starting", "running", "attention", "stopping", "stopped", "failed", "unavailable", "not_recorded")
ERROR_CODES = ("credential_unavailable", "unauthenticated", "forbidden", "timeout", "canceled",
               "transport", "unsupported", "invalid_response", "unavailable", "metadata_unavailable", "unknown")
REASON_CODES = {"operational_read_busy": ("unavailable",), "query_budget_exhausted": ("canceled",),
                "shutdown": ("unavailable", "canceled")}
COMPONENTS = ("broker", "runtime", "native", "storage")
FILES = ("database", "wal", "shared_memory")
SCOPE = "Sampled operational metadata; not uptime, task quality, throughput or confirmed restart counts."
TIMESTAMP = re.compile(r"([0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2})(?:\.([0-9]{1,9}))?(Z|[+-][0-9]{2}:[0-9]{2})\Z")


class InvalidCapture(ValueError):
    """Contains only a fixed, safe error explanation."""


def obj(value):
    if value is None:
        return {}
    if not isinstance(value, dict):
        raise InvalidCapture("invalid object")
    return value


def count(value):
    if value is None:
        return None
    if type(value) is not int or value < 0 or value > MAX_INTEGER:
        raise InvalidCapture("invalid nonnegative integer")
    return value


def flag(value):
    if value is not None and type(value) is not bool:
        raise InvalidCapture("invalid boolean")
    return value


def text(value):
    if value is not None and not isinstance(value, str):
        raise InvalidCapture("invalid string")
    return value


def timing(value):
    if value is None:
        return None
    if type(value) not in (int, float) or not math.isfinite(value) or not 0 <= value <= MAX_INTEGER:
        raise InvalidCapture("invalid duration")
    return value


def items(value):
    if value is None:
        return None
    if not isinstance(value, list) or len(value) > MAX_ITEMS:
        raise InvalidCapture("invalid or oversized inventory")
    for item in value:
        obj(item)
        if item is None:
            raise InvalidCapture("invalid inventory item")
    return value


def timestamp(value):
    match = TIMESTAMP.fullmatch(value) if isinstance(value, str) else None
    if not match:
        raise InvalidCapture("invalid observation time")
    try:
        if match[3] != "Z" and (int(match[3][1:3]) > 23 or int(match[3][4:6]) > 59):
            raise ValueError
        base = dt.datetime.fromisoformat(match[1] + ("+00:00" if match[3] == "Z" else match[3]))
        epoch = dt.datetime(1970, 1, 1, tzinfo=dt.timezone.utc)
        delta = base.astimezone(dt.timezone.utc) - epoch
        nanos = (delta.days * 86400 + delta.seconds) * 10**9 + int((match[2] or "").ljust(9, "0"))
        return nanos
    except (ValueError, OverflowError):
        raise InvalidCapture("invalid observation time") from None


def iso(nanos):
    seconds, fraction = divmod(nanos, 10**9)
    value = dt.datetime(1970, 1, 1, tzinfo=dt.timezone.utc) + dt.timedelta(seconds=seconds)
    base = "%04d-%02d-%02dT%02d:%02d:%02d" % (value.year, value.month, value.day, value.hour, value.minute, value.second)
    return base + (".%09d" % fraction).rstrip("0").rstrip(".") + "Z"


def strict_object(pairs):
    if len(pairs) > 4096:
        raise InvalidCapture("oversized object")
    result = {}
    for key, value in pairs:
        if key in result:
            raise InvalidCapture("duplicate JSON key")
        result[key] = value
    return result


def invalid_constant(_):
    raise InvalidCapture("non-finite JSON number")


def finite_float(value):
    result = float(value)
    if not math.isfinite(result):
        raise InvalidCapture("non-finite JSON number")
    return result


class Gauge:
    def __init__(self):
        self.known = self.unknown = 0
        self.first = self.last = self.minimum = self.peak = None

    def add(self, value):
        if self.known + self.unknown == 0:
            self.first = value
        self.last = value
        if value is None:
            self.unknown += 1
        else:
            self.known += 1
            self.minimum = value if self.minimum is None else min(self.minimum, value)
            self.peak = value if self.peak is None else max(self.peak, value)

    def report(self):
        return {"known_samples": self.known, "unknown_samples": self.unknown,
                "first": self.first, "last": self.last, "minimum_known": self.minimum, "peak_known": self.peak}


class Summary:
    def __init__(self, max_gap_ns):
        self.max_gap_ns = max_gap_ns
        self.samples = 0
        self.identity = self.previous_time = self.first_time = self.last_time = None
        self.minimum_time = self.maximum_time = None
        self.maximum_gap = self.gaps = self.backwards = self.repeated = 0
        self.gap_episodes = []
        self.completeness = {key: 0 for key in ("complete", "partial", "unknown")}
        self.availability = {name: {key: 0 for key in ("observed", "unavailable", "not_recorded", "not_attempted", "unknown")} for name in COMPONENTS}
        self.previous_availability = {}
        self.transitions = []
        self.transition_count = 0
        self.durations = {name: Gauge() for name in ("collection", *COMPONENTS)}
        self.codes = {name: {key: 0 for key in (*ERROR_CODES, "other", "none", "not_recorded")} for name in COMPONENTS}
        self.reasons = {name: {key: 0 for key in (*REASON_CODES, "other", "not_recorded")} for name in COMPONENTS}
        self.gauges = {name: Gauge() for name in ("retained_tasks", "recovery_eligible", "attention", "active_execution_leases")}
        self.maps = {name: {key: Gauge() for key in (*keys, "other")} for name, keys in (
            ("task_states", TASK_STATES), ("recorded_deliveries", DELIVERIES), ("effective_deliveries", DELIVERIES))}
        self.capacities = {name: {"count": Gauge(), "ceiling": Gauge(), "ceiling_changes": 0,
                                 "previous_ceiling": None, "known_pairs": 0, "peak_ratio": None,
                                 "above_ceiling_samples": 0} for name in ("records", "requests", "messages", "tasks")}
        self.storage = {name: {"states": {key: 0 for key in ("present", "absent", "unknown")},
                              "first": None, "last": None, "bytes": Gauge(), "previous_known_bytes": None,
                              "increases": 0, "decreases": 0} for name in FILES}
        self.runtime_states = {key: 0 for key in (*RUNTIME_STATES, "unknown")}
        self.previous_attempt = None
        self.attempt_changes = self.attempt_known = self.attempt_unknown = 0
        self.native = {name: Gauge() for name in ("agents_listed", "reporting", "not_reporting", "readiness_unknown",
                                                  "known_pending_sum", "unknown_pending_agents", "pending_total")}
        self.truncated = {name: {key: 0 for key in ("yes", "no", "unknown")} for name in ("attention", "active", "native")}

    def add(self, sample):
        sample = obj(sample)
        if type(sample.get("schema_version")) is not int or sample["schema_version"] != 1:
            raise InvalidCapture("unsupported observation schema")
        identity = text(sample.get("state"))
        if not identity or len(identity) > 16384:
            raise InvalidCapture("missing or invalid exact state identity")
        if self.identity is not None and identity != self.identity:
            raise InvalidCapture("mixed exact state identities")
        self.identity = identity
        at = timestamp(sample.get("observed_at"))
        self.samples += 1
        if self.first_time is None:
            self.first_time = at
        self.last_time = at
        self.minimum_time = at if self.minimum_time is None else min(at, self.minimum_time)
        self.maximum_time = at if self.maximum_time is None else max(at, self.maximum_time)
        if self.previous_time is not None:
            gap = at - self.previous_time
            self.backwards += gap < 0
            self.repeated += gap == 0
            self.maximum_gap = max(self.maximum_gap, gap)
            if gap > self.max_gap_ns:
                self.gaps += 1
                if len(self.gap_episodes) < MAX_EPISODES:
                    self.gap_episodes.append({"after_sample": self.samples - 1, "gap_seconds": gap / 10**9})
        self.previous_time = at
        complete = flag(sample.get("complete"))
        self.completeness["unknown" if complete is None else "complete" if complete else "partial"] += 1
        snapshot = obj(sample.get("snapshot"))
        runtime = obj(sample.get("runtime"))
        native = items(sample.get("native"))
        files = obj(obj(sample.get("storage")).get("files"))
        legacy = self.legacy_availability(sample, snapshot, runtime, native, files)
        available = self.add_diagnostics(sample.get("diagnostics"), legacy)
        broker_known = available["broker"] == "observed"
        for output, key in (("retained_tasks", "tasks"), ("recovery_eligible", "recovery_eligible"),
                            ("attention", "attention_count"), ("active_execution_leases", "active_count")):
            value = count(snapshot.get(key))
            self.gauges[output].add(value if broker_known else None)
        for output, key in (("task_states", "task_states"), ("recorded_deliveries", "deliveries"),
                            ("effective_deliveries", "effective_deliveries")):
            values = snapshot.get(key)
            if values is not None and len(obj(values)) > 64:
                raise InvalidCapture("oversized gauge map")
            selected = self.maps[output]
            other = 0
            for label, value in obj(values).items():
                value = count(value)
                if value is None:
                    raise InvalidCapture("invalid gauge map value")
                if label not in selected or label == "other":
                    other += value
            for label, gauge in selected.items():
                value = None if values is None else other if label == "other" else count(values.get(label, 0))
                gauge.add(value if broker_known else None)
        self.add_capacity(obj(snapshot.get("capacity")), broker_known)
        for name in ("attention", "active"):
            items(snapshot.get(name))
            value = flag(snapshot.get(name + "_more"))
            if not broker_known:
                value = None
            self.truncated[name]["unknown" if value is None else "yes" if value else "no"] += 1
        self.add_storage(files)
        state = text(runtime.get("state"))
        self.runtime_states[state if state in RUNTIME_STATES else "unknown"] += 1
        attempt = text(runtime.get("attempt_id"))
        if attempt:
            self.attempt_known += 1
            self.attempt_changes += self.previous_attempt is not None and attempt != self.previous_attempt
        else:
            self.attempt_unknown += 1
        # Missing observations break adjacency; do not infer a restart through a gap.
        self.previous_attempt = attempt or None
        self.add_native(native, flag(sample.get("native_more")), available["native"])

    @staticmethod
    def legacy_availability(sample, snapshot, runtime, native, files):
        errors = {name: bool(text(sample.get(name + "_error"))) for name in ("broker", "runtime", "native")}
        broker = text(sample.get("broker"))
        storage_states = [text(obj(files.get(name)).get("state")) for name in FILES]
        return {
            "broker": "unavailable" if errors["broker"] or broker == "unavailable" else "observed" if broker == "connected" and snapshot else "unknown",
            "runtime": "unavailable" if errors["runtime"] else "not_recorded" if runtime.get("state") == "not_recorded" else "observed" if runtime and runtime.get("state") not in (None, "", "unknown") else "unknown",
            "native": "unavailable" if errors["native"] else "not_attempted" if broker == "unavailable" else "observed" if native is not None and broker == "connected" else "unknown",
            "storage": "unavailable" if "unknown" in storage_states else "observed" if all(state in ("present", "absent") for state in storage_states) else "unknown",
        }

    def add_diagnostics(self, value, legacy):
        diagnostics = obj(value)
        components = obj(diagnostics.get("components"))
        self.durations["collection"].add(timing(diagnostics.get("collection_duration_ms")))
        available = {}
        for name in COMPONENTS:
            component = obj(components.get(name))
            state = text(component.get("state"))
            state = state if state in self.availability[name] else legacy[name] if not component else "unknown"
            self.availability[name][state] += 1
            available[name] = state
            previous = self.previous_availability.get(name)
            if previous is not None and previous != state:
                self.transition_count += 1
                if len(self.transitions) < MAX_EPISODES:
                    self.transitions.append({"sample": self.samples, "component": name, "from": previous, "to": state})
            self.previous_availability[name] = state
            self.durations[name].add(timing(component.get("duration_ms")))
            code = text(component.get("error_code"))
            bucket = "not_recorded" if "error_code" not in component else "none" if code is None else code if code in ERROR_CODES else "other"
            self.codes[name][bucket] += 1
            reason = text(component.get("reason_code"))
            # These are reported subcauses, not proof of availability. Missing
            # or null reasons never assert success; untrusted labels and known
            # labels in incompatible components/states/codes stay bounded.
            compatible = (name in ("broker", "native") and state == "unavailable"
                          and reason in REASON_CODES and code in REASON_CODES[reason])
            bucket = "not_recorded" if reason is None else reason if compatible else "other"
            self.reasons[name][bucket] += 1
        return available

    def add_capacity(self, values, known):
        for name, aggregate in self.capacities.items():
            current, ceiling = count(values.get(name)), count(values.get("max_" + name))
            if not known:
                current = ceiling = None
            aggregate["count"].add(current)
            aggregate["ceiling"].add(ceiling)
            previous = aggregate["previous_ceiling"]
            if ceiling is not None:
                aggregate["ceiling_changes"] += previous is not None and ceiling != previous
            aggregate["previous_ceiling"] = ceiling
            if current is not None and ceiling is not None:
                aggregate["known_pairs"] += 1
                aggregate["above_ceiling_samples"] += current > ceiling
                if ceiling > 0:
                    ratio = current / ceiling
                    aggregate["peak_ratio"] = ratio if aggregate["peak_ratio"] is None else max(ratio, aggregate["peak_ratio"])

    def add_storage(self, files):
        for name, aggregate in self.storage.items():
            entry = obj(files.get(name))
            state = text(entry.get("state"))
            size = count(entry.get("bytes"))
            if state not in ("present", "absent", "unknown"):
                state, size = "unknown", None
            if state == "absent" and size != 0 or state == "present" and size is None or state == "unknown" and size is not None:
                raise InvalidCapture("inconsistent storage metadata")
            point = {"state": state, "bytes": size}
            if aggregate["first"] is None:
                aggregate["first"] = point
            aggregate["last"] = point
            aggregate["states"][state] += 1
            aggregate["bytes"].add(size)
            previous = aggregate["previous_known_bytes"]
            if previous is not None and size is not None:
                aggregate["increases"] += size > previous
                aggregate["decreases"] += size < previous
            aggregate["previous_known_bytes"] = size

    def add_native(self, native, more, availability):
        known = native is not None and availability == "observed"
        observed_more = more if known else None
        self.truncated["native"]["unknown" if observed_more is None else "yes" if observed_more else "no"] += 1
        values = {name: None for name in self.native}
        for entry in native or []:
            count(entry.get("pending_count"))
            flag(entry.get("connected"))
            ready = text(entry.get("readiness_observed_at"))
            if ready:
                timestamp(ready)
        if known:
            values = {name: 0 for name in self.native}
            values["agents_listed"] = len(native)
            for entry in native:
                pending = count(entry.get("pending_count"))
                if pending is None:
                    values["unknown_pending_agents"] += 1
                else:
                    values["known_pending_sum"] += pending
                ready = text(entry.get("readiness_observed_at"))
                connected = flag(entry.get("connected"))
                label = "readiness_unknown" if not ready or connected is None else "reporting" if connected else "not_reporting"
                values[label] += 1
            values["pending_total"] = values["known_pending_sum"] if not values["unknown_pending_agents"] and more is False else None
        for name, gauge in self.native.items():
            gauge.add(values[name])

    def report(self, sha256, input_bytes):
        return {
            "report_schema_version": 1, "scope": SCOPE,
            "input": {"sha256": sha256, "bytes": input_bytes, "observation_schema_version": 1,
                      "state_identity": "one exact state; identifier omitted", "max_line_bytes": MAX_LINE_BYTES},
            "samples": self.samples,
            "time": {"first_observed_at": iso(self.first_time), "last_observed_at": iso(self.last_time),
                     "span_seconds": (self.maximum_time - self.minimum_time) / 10**9,
                     "span_definition": "maximum minus minimum observed timestamp; first/last follow file order",
                     "max_gap_seconds": self.max_gap_ns / 10**9, "maximum_forward_adjacent_gap_seconds": self.maximum_gap / 10**9,
                     "gaps_over_limit": self.gaps, "backward_adjacent_timestamps": self.backwards,
                     "repeated_adjacent_timestamps": self.repeated, "gap_episodes": self.gap_episodes,
                     "gap_episodes_omitted": max(0, self.gaps - len(self.gap_episodes))},
            "completeness_samples": self.completeness, "component_availability_samples": self.availability,
            "availability_transitions": self.transitions,
            "availability_transitions_omitted": max(0, self.transition_count - len(self.transitions)),
            "diagnostic_error_code_samples": self.codes,
            "diagnostic_reason_code_samples": self.reasons,
            "collection_duration_ms": {name: gauge.report() for name, gauge in self.durations.items()},
            "duration_scope": "local acquisition and decoding; concurrent component durations are not additive or model latency",
            "gauges": {name: gauge.report() for name, gauge in self.gauges.items()},
            "retained_state_gauges": {name: {key: gauge.report() for key, gauge in values.items()} for name, values in self.maps.items()},
            "capacity": {name: {"count": value["count"].report(), "ceiling": value["ceiling"].report(),
                                "adjacent_known_ceiling_changes": value["ceiling_changes"], "known_pair_samples": value["known_pairs"],
                                "peak_known_utilization_ratio": value["peak_ratio"], "above_ceiling_samples": value["above_ceiling_samples"]}
                         for name, value in self.capacities.items()},
            "storage": {name: {"state_samples": value["states"], "first": value["first"], "last": value["last"],
                               "bytes": value["bytes"].report(), "adjacent_known_increases": value["increases"],
                               "adjacent_known_decreases": value["decreases"]} for name, value in self.storage.items()},
            "storage_scope": "database/WAL/shared-memory file sizes only; decreases may be resets or checkpointing, not negative throughput",
            "runtime_state_samples": self.runtime_states,
            "runtime_attempt_observations": {"known_samples": self.attempt_known, "unknown_samples": self.attempt_unknown,
                                             "adjacent_known_attempt_changes": self.attempt_changes,
                                             "scope": "identifier changes observed between adjacent samples; not actual restart counts"},
            "native_gauges": {name: gauge.report() for name, gauge in self.native.items()},
            "truncated_inventory_samples": self.truncated,
        }


def summarize(stream, max_gap_ns):
    summary = Summary(max_gap_ns)
    digest, total, line_number = hashlib.sha256(), 0, 0
    while True:
        raw = stream.readline(MAX_LINE_BYTES + 1)
        if not raw:
            break
        line_number += 1
        try:
            if len(raw) > MAX_LINE_BYTES:
                raise InvalidCapture("oversized JSONL record")
            if not raw.endswith(b"\n"):
                raise InvalidCapture("unterminated JSONL record")
            parsed = json.loads(raw.decode("utf-8", errors="strict"), object_pairs_hook=strict_object,
                                parse_constant=invalid_constant, parse_float=finite_float)
            summary.add(parsed)
        except InvalidCapture as error:
            raise InvalidCapture("sample %d: %s" % (line_number, error)) from None
        except (UnicodeError, ValueError, OverflowError, RecursionError):
            raise InvalidCapture("sample %d: malformed JSON or encoding" % line_number) from None
        digest.update(raw)
        total += len(raw)
    if not summary.samples:
        raise InvalidCapture("capture contains no observations")
    return summary.report(digest.hexdigest(), total)


def duration(value):
    match = re.fullmatch(r"([0-9]+(?:\.[0-9]{1,9})?)(ms|s|m|h)", value)
    if not match:
        raise argparse.ArgumentTypeError("invalid duration")
    whole, _, fraction = match[1].partition(".")
    if len(whole) > 12:
        raise argparse.ArgumentTypeError("invalid duration")
    numerator = int(whole + fraction) * {"ms": 10**6, "s": 10**9, "m": 60 * 10**9, "h": 3600 * 10**9}[match[2]]
    amount, remainder = divmod(numerator, 10**len(fraction))
    if remainder or not 1 <= amount <= 365 * 86400 * 10**9:
        raise argparse.ArgumentTypeError("invalid duration")
    return amount


class SafeParser(argparse.ArgumentParser):
    def error(self, _message):
        self.exit(2, "error: invalid arguments; use --help\n")


def human(report):
    def display(value):
        return "unknown" if value is None else value

    timing_info = report["time"]
    lines = [SCOPE, "Samples: %d; span: %.3fs" % (report["samples"], timing_info["span_seconds"]),
             "First: %s; last: %s" % (timing_info["first_observed_at"], timing_info["last_observed_at"]),
             "Complete/partial/unknown samples: %d/%d/%d" % tuple(report["completeness_samples"][key] for key in ("complete", "partial", "unknown")),
             "Forward gaps > %.3fs: %d; maximum: %.3fs; backward/repeated timestamps: %d/%d" % (
                 timing_info["max_gap_seconds"], timing_info["gaps_over_limit"], timing_info["maximum_forward_adjacent_gap_seconds"],
                 timing_info["backward_adjacent_timestamps"], timing_info["repeated_adjacent_timestamps"])]
    for name, values in report["component_availability_samples"].items():
        lines.append("%s samples: %s" % (name.capitalize(), ", ".join("%s=%d" % pair for pair in values.items())))
    for name, gauge in report["gauges"].items():
        lines.append("%s: first=%s last=%s peak=%s unknown_samples=%d" % (
            name, display(gauge["first"]), display(gauge["last"]), display(gauge["peak_known"]), gauge["unknown_samples"]))
    for name, value in report["capacity"].items():
        lines.append("Capacity %s: count_peak=%s ceiling_first=%s ceiling_last=%s adjacent_ceiling_changes=%d" % (
            name, display(value["count"]["peak_known"]), display(value["ceiling"]["first"]), display(value["ceiling"]["last"]), value["adjacent_known_ceiling_changes"]))
    for name, value in report["storage"].items():
        lines.append("Storage %s: first=%s/%s last=%s/%s peak_bytes=%s" % (
            name, value["first"]["state"], display(value["first"]["bytes"]), value["last"]["state"], display(value["last"]["bytes"]), display(value["bytes"]["peak_known"])))
    pending = report["native_gauges"]["pending_total"]
    lines.append("Native pending total: first=%s last=%s peak=%s unknown_samples=%d" % (
        display(pending["first"]), display(pending["last"]), display(pending["peak_known"]), pending["unknown_samples"]))
    lines.append("Truncated attention/active/native samples: %d/%d/%d" % tuple(
        report["truncated_inventory_samples"][name]["yes"] for name in ("attention", "active", "native")))
    lines.extend(["Adjacent observed attempt changes: %d (not actual restarts)" % report["runtime_attempt_observations"]["adjacent_known_attempt_changes"],
                  "Detailed state gauges, diagnostic timings/codes and bounded episodes: use --json.",
                  "Input SHA256: " + report["input"]["sha256"]])
    return "\n".join(lines) + "\n"


def main(argv=None):
    parser = SafeParser(prog="summarize-observations.py", description=__doc__,
                        epilog="Records must end in a newline. Default max-gap is 5s; choose it for your capture interval. No provider calls or workspace writes.")
    parser.add_argument("capture", help="JSONL capture file, or - for stdin")
    parser.add_argument("--json", action="store_true", help="emit the full privacy-filtered JSON report")
    parser.add_argument("--max-gap", type=duration, default=5 * 10**9, metavar="DURATION", help="gap threshold, e.g. 5s, 250ms, 1m or 2h")
    args = parser.parse_args(argv)
    try:
        if args.capture == "-":
            report = summarize(sys.stdin.buffer, args.max_gap)
        else:
            with open(args.capture, "rb") as capture:
                report = summarize(capture, args.max_gap)
        sys.stdout.write(json.dumps(report, indent=2, allow_nan=False) + "\n" if args.json else human(report))
    except InvalidCapture as error:
        sys.stderr.write("error: " + str(error) + "\n")
        return 1
    except (OSError, ValueError, OverflowError):
        sys.stderr.write("error: capture could not be read or report could not be written\n")
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
