#!/usr/bin/env python3
"""Provider-free mutation controls for the independent guided-skip oracle."""
import copy
import importlib.util
import json
from pathlib import Path
import unittest

SPEC = importlib.util.spec_from_file_location("review_evidence", Path(__file__).with_name("workflow_review_evidence.py"))
REVIEW = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(REVIEW)


def fixture():
    revision = {"tree": "a" * 40, "content_sha256": "b" * 64}
    plan = {"kind": "work", "mode": "mission", "review_policy": REVIEW.POLICY, "steps": []}
    report = {"kind": "work", "mode": "mission", "review_policy": REVIEW.POLICY, "review_context": "fresh",
              "output": "/fixture/run", "worktree": "/fixture/worktree", "steps": [],
              "early_approval": dict(revision, version=1, implementation_step="implement", review_step="review"),
              "final_snapshot": dict(revision), "verification": dict(revision)}
    verdict = {"decision": "approve", "summary": "Inspected the exact checked files.", "findings": []}
    for index, identity in enumerate(REVIEW.IDS):
        definition = {"id": identity, "depends_on": REVIEW.DEPENDENCIES[index],
                      "verify": index in (0, 2, 4), "read_only": index in (1, 3),
                      "review": index in (1, 3), "gate_review": index == 3, "require_result": index in (1, 3)}
        if index != 4:
            definition["candidate"] = "reviewer" if index in (1, 3) else "coder"
        plan["steps"].append(definition)
        row = {key: definition[key] for key in ("id", "depends_on", "read_only", "gate_review")}
        if index in (2, 3):
            row.update(state="skipped", skip_reason=REVIEW.REASON, harness="", attempts=0, duration_millis=0,
                       worktree=report["worktree"], evidence_path="/fixture/run/steps/" + identity + "/skipped.json",
                       started_at="2026-11-04T00:00:00Z", finished_at="2026-11-04T00:00:00Z")
        elif index == 4:
            row.update(state="verified", harness="", attempts=0, verification=dict(revision))
        else:
            row.update(state="verified" if index == 0 else "completed", harness="command", attempts=1,
                       candidate_id=definition["candidate"], profile_sha256="d" * 64,
                       input_sha256="c" * 64, starting_snapshot=dict(revision), final_snapshot=dict(revision),
                       evidence_path="/fixture/run/steps/" + identity + "/attempt-1.json")
            if index == 0:
                row["verification"] = dict(revision)
            else:
                row.update(review_context="fresh", review=verdict, result=json.dumps(verdict))
        report["steps"].append(row)
    return report, plan


class GuidedApprovalEvidenceTests(unittest.TestCase):
    def test_exact_original_approval_allows_only_two_documented_skips(self):
        report, plan = fixture()
        self.assertEqual(REVIEW.early_skips(report, plan), {"address-review", "final-review"})

    def test_skipped_phases_cannot_claim_execution_or_missing_identity(self):
        report, plan = fixture()
        changes = [("state", "completed"), ("attempts", 1), ("attempts", False), ("duration_millis", 1),
                   ("duration_millis", False), ("harness", "command"), ("skip_reason", "other"),
                   ("evidence_path", "/elsewhere/skipped.json"), ("worktree", "/other"), ("started_at", ""),
                   ("usage", {}), ("result", "approved"), ("execution", {}), ("verification", {}),
                   ("review", {}), ("starting_snapshot", {}), ("error", ""), ("model", ""), ("input_sha256", "c" * 64)]
        for index in (2, 3):
            for key, value in changes:
                candidate = copy.deepcopy(report);candidate["steps"][index][key] = value
                with self.subTest(phase=index, key=key, value=value), self.assertRaises(ValueError):
                    REVIEW.early_skips(candidate, plan)
            for key in report["steps"][index]:
                if key in ("read_only", "gate_review") and not report["steps"][index][key]:
                    continue  # Normal Go JSON omission of false roles is valid.
                candidate = copy.deepcopy(report);candidate["steps"][index].pop(key)
                with self.subTest(phase=index, missing=key), self.assertRaises(ValueError):
                    REVIEW.early_skips(candidate, plan)

    def test_original_execution_and_review_must_remain_real_fresh_and_exact(self):
        report, plan = fixture()
        mutations = [lambda r: r.pop("early_approval"), lambda r: r.update(review_context="reused"),
                     lambda r: r["early_approval"].update(tree="f" * 40),
                     lambda r: r["final_snapshot"].update(content_sha256="f" * 64),
                     lambda r: r["steps"][4]["verification"].update(tree="f" * 40),
                     lambda r: r["steps"][1].update(review_context="reused"),
                     lambda r: r["steps"][1].update(result='{"decision":"approve","summary":"x","findings":[],"decision":"approve"}'),
                     lambda r: r["steps"][1].update(result=json.dumps({"decision":"request_changes","summary":"x","findings":[]})),
                     lambda r: r["steps"][1]["review"].update(summary="different structured claim")]
        for index in (0, 1):
            for key, value in (("attempts", 0), ("attempts", True), ("harness", ""), ("evidence_path", "/fake"),
                               ("input_sha256", "wrong"), ("state", "skipped"), ("skip_reason", REVIEW.REASON),
                               ("candidate_id", "other"), ("profile_sha256", "wrong")):
                mutations.append(lambda r, index=index, key=key, value=value: r["steps"][index].update({key:value}))
        for key in ("starting_snapshot", "final_snapshot"):
            mutations.append(lambda r, key=key: r["steps"][1][key].update(content_sha256="f" * 64))
        for key in ("truncated", "review_truncated"):
            mutations.append(lambda r, key=key: r["steps"][1].update({key:True}))
        for key, value in (("candidate_id", "coder"), ("profile_sha256", "d" * 64)):
            mutations.append(lambda r, key=key, value=value: r["steps"][4].update({key:value}))
        for mutation in mutations:
            candidate = copy.deepcopy(report);mutation(candidate)
            with self.subTest(mutation=mutation), self.assertRaises(ValueError):
                REVIEW.early_skips(candidate, plan)

    def test_plan_cannot_remove_or_change_gates_to_legitimize_skips(self):
        report, plan = fixture()
        for index, step in enumerate(plan["steps"]):
            for key in ("verify", "read_only", "review", "gate_review", "require_result"):
                for replacement in (not step[key], int(step[key])):
                    candidate = copy.deepcopy(plan);candidate["steps"][index][key] = replacement
                    with self.subTest(phase=index, flag=key, value=replacement), self.assertRaises(ValueError):
                        REVIEW.early_skips(report, candidate)
        candidate = copy.deepcopy(plan);candidate["steps"][1]["candidate"] = "coder"
        with self.assertRaises(ValueError):REVIEW.early_skips(report, candidate)

    def test_legacy_and_policy_runs_without_promotion_cannot_claim_skips(self):
        self.assertEqual(REVIEW.early_skips({"steps":[{"id":"ordinary","state":"completed"}]}, {}), set())
        with self.assertRaises(ValueError):REVIEW.early_skips({"steps":[{"state":"skipped"}]}, {})
        report, plan = fixture()
        report.pop("early_approval")
        with self.assertRaises(ValueError):REVIEW.early_skips(report, plan)
        report["steps"] = [dict(row, state="completed") for row in report["steps"]]
        for row in report["steps"]:row.pop("skip_reason", None)
        for index in (2, 3):
            report["steps"][index].update(candidate_id=plan["steps"][index]["candidate"], profile_sha256="d" * 64)
        self.assertEqual(REVIEW.early_skips(report, plan), set())


if __name__ == "__main__":
    unittest.main()
