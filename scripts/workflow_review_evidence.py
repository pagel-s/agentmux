"""Independent JSON oracle for the guided early-approval evidence contract."""
import json
from pathlib import Path

POLICY = "early-approval-v1"
REASON = "initial_fresh_review_approved"
IDS = ["implement", "review", "address-review", "final-review", "final-check"]
DEPENDENCIES = [[], ["implement"], ["implement", "review"],
                ["implement", "review", "address-review"], ["final-review"]]


def require(condition, message):
    if not condition:
        raise ValueError("guided approval evidence: " + message)


def revision(value, expected):
    return isinstance(value, dict) and isinstance(expected, dict) and all(
        value.get(key) == expected.get(key) and isinstance(value.get(key), str)
        and len(value[key]) in ((40, 64) if key == "tree" else (64,))
        and all(char in "0123456789abcdef" for char in value[key])
        for key in ("tree", "content_sha256"))


def unique_pairs(pairs):
    value = {}
    for key, item in pairs:
        require(key not in value, "duplicate raw review field")
        value[key] = item
    return value


def early_skips(report, plan):
    """Return exactly the skipped IDs; callers still verify checks and artifacts.

    The supplied plan is retained independently of the report. This checks local
    consistency, not authenticity against an owner rewriting all artifacts.
    """
    require(isinstance(report, dict) and isinstance(plan, dict), "report or plan is not an object")
    require(report.get("review_policy", "") == plan.get("review_policy", ""), "plan policy differs")
    policy = plan.get("review_policy", "")
    require(isinstance(policy, str), "policy is not a string")
    steps = report.get("steps", [])
    require(isinstance(steps, list) and all(isinstance(row, dict) for row in steps), "invalid phase records")
    promotion = report.get("early_approval")
    if not policy:
        require(promotion is None and not any(row.get("state") == "skipped" or row.get("skip_reason")
                                            for row in steps), "legacy run claims skipped review")
        return set()
    require(policy == POLICY and report.get("kind") == "work" and report.get("mode") == "mission",
            "unknown or misplaced policy")
    planned = plan.get("steps")
    require(plan.get("kind") == "work" and plan.get("mode") == "mission"
            and isinstance(planned, list) and len(planned) == 5
            and all(isinstance(row, dict) for row in planned)
            and [row.get("id") for row in planned] == IDS, "plan lacks canonical guided phases")
    coder, reviewer = planned[0].get("candidate"), planned[1].get("candidate")
    require(isinstance(coder, str) and bool(coder) and isinstance(reviewer, str)
            and bool(reviewer) and coder != reviewer, "plan lacks separate coder and reviewer")
    for index, row in enumerate(planned):
        require(row.get("depends_on", []) == DEPENDENCIES[index]
                and row.get("candidate") == [coder, reviewer, coder, reviewer, None][index]
                and all(row.get(flag, False) is expected for flag, expected in {
                    "verify": index in (0, 2, 4), "read_only": index in (1, 3),
                    "review": index in (1, 3), "gate_review": index == 3,
                    "require_result": index in (1, 3)}.items()), "plan role or dependency differs")
    require(len(steps) == 5 and [row.get("id") for row in steps] == IDS, "missing canonical phases")
    for index, row in enumerate(steps):
        require(row.get("depends_on", []) == DEPENDENCIES[index]
                and row.get("read_only", False) is (index in (1, 3))
                and row.get("gate_review", False) is (index == 3), "phase role or dependency differs")
        if index == 4:
            require("candidate_id" not in row and "profile_sha256" not in row,
                    "verifier-only phase claims candidate execution")
        elif row.get("state") != "skipped":
            profile = row.get("profile_sha256")
            require(row.get("candidate_id") == planned[index]["candidate"]
                    and isinstance(profile, str) and len(profile) == 64
                    and all(char in "0123456789abcdef" for char in profile),
                    "executed phase lacks its planned candidate or profile pin")
    if promotion is None:
        require(not any(row.get("state") == "skipped" or row.get("skip_reason") for row in steps),
                "skip without original approval")
        return set()
    require(isinstance(promotion, dict) and set(promotion) == {
        "version", "implementation_step", "review_step", "tree", "content_sha256"}
        and type(promotion.get("version")) is int and promotion["version"] == 1
        and promotion.get("implementation_step") == "implement" and promotion.get("review_step") == "review",
        "invalid original approval identity")
    require(isinstance(report.get("output"), str) and bool(report["output"])
            and isinstance(report.get("worktree"), str) and bool(report["worktree"]),
            "retained run paths are missing")
    require(report.get("review_context") == "fresh", "review context is not fresh")
    initial, reviewed = steps[:2]
    for row, state in ((initial, "verified"), (reviewed, "completed")):
        require(row.get("state") == state and type(row.get("attempts")) is int and row["attempts"] == 1
                and isinstance(row.get("harness"), str) and bool(row["harness"])
                and row.get("evidence_path") == str(Path(report["output"]) / "steps" / row["id"] / "attempt-1.json")
                and isinstance(row.get("input_sha256"), str) and len(row["input_sha256"]) == 64
                and all(char in "0123456789abcdef" for char in row["input_sha256"])
                and not row.get("skip_reason") and not row.get("error"), "original coding or review execution evidence missing")
    require(reviewed.get("review_context") == "fresh", "original review context is not fresh")
    require(revision(initial.get("final_snapshot"), promotion)
            and revision(initial.get("verification"), promotion)
            and revision(reviewed.get("starting_snapshot"), promotion)
            and revision(reviewed.get("final_snapshot"), promotion)
            and revision(report.get("final_snapshot"), promotion)
            and revision(report.get("verification"), promotion)
            and revision(steps[4].get("verification"), promotion), "approved revision differs")
    raw = reviewed.get("result")
    require(isinstance(raw, str) and len(raw.encode()) <= 128 << 10
            and reviewed.get("truncated", False) is False
            and reviewed.get("review_truncated", False) is False, "original verdict missing or truncated")
    try:
        verdict = json.loads(raw, object_pairs_hook=unique_pairs,
                             parse_constant=lambda _: require(False, "non-finite review"))
    except (ValueError, TypeError):
        raise ValueError("guided approval evidence: invalid original verdict") from None
    require(isinstance(verdict, dict) and set(verdict) == {"decision", "summary", "findings"}
            and verdict == reviewed.get("review") and verdict.get("decision") == "approve"
            and verdict.get("findings") == [] and isinstance(verdict.get("summary"), str)
            and bool(verdict["summary"].strip()) and len(verdict["summary"].encode()) <= 16 << 10,
            "original verdict does not approve")
    allowed = {"id", "depends_on", "read_only", "gate_review", "skip_reason", "state", "harness",
               "worktree", "started_at", "finished_at", "attempts", "duration_millis", "evidence_path"}
    for row in steps[2:4]:
        require(set(row) <= allowed and row.get("state") == "skipped" and row.get("skip_reason") == REASON
                and type(row.get("attempts")) is int and row["attempts"] == 0
                and type(row.get("duration_millis")) is int and row["duration_millis"] == 0
                and row.get("harness") == "" and row.get("worktree") == report.get("worktree")
                and row.get("evidence_path") == str(Path(report["output"]) / "steps" / row["id"] / "skipped.json")
                and all(isinstance(row.get(key), str) and bool(row[key]) for key in ("started_at", "finished_at")),
                "skipped phase contains execution or lacks retained identity")
    return {"address-review", "final-review"}
