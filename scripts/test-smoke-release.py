#!/usr/bin/env python3
"""Release smoke oracle regressions; no network or provider calls."""
import hashlib
import importlib.util
import io
import json
from pathlib import Path
import tarfile
import tempfile
import unittest

spec = importlib.util.spec_from_file_location("release_smoke", Path(__file__).with_name("smoke-release.py"))
smoke = importlib.util.module_from_spec(spec)
spec.loader.exec_module(smoke)


def archives(root, missing=None):
    data = io.BytesIO()
    with tarfile.open(fileobj=data, mode="w:gz") as archive:
        for name in sorted(smoke.REQUIRED_FILES - {missing}):
            payload = b'#!/bin/sh\nprintf "amx 0.12.0\\n"\n' if name == "amx" else b"fixture\n"
            entry = tarfile.TarInfo(name)
            entry.size = len(payload)
            entry.mode = 0o755 if name == "amx" else 0o644
            archive.addfile(entry, io.BytesIO(payload))
    payload = data.getvalue()
    manifest = []
    for name in sorted(smoke.ARCHIVES):
        (root / name).write_bytes(payload)
        manifest.append(hashlib.sha256(payload).hexdigest() + "  " + name)
    (root / "checksums.txt").write_text("\n".join(manifest) + "\n")


class SmokeTests(unittest.TestCase):
    def test_corrupt_platform_archive_cannot_hide_behind_working_host_binary(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            archives(root)
            smoke.check_archives(root)
            (root / "agentmux_darwin_arm64.tar.gz").write_bytes(b"wrong bytes")
            with self.assertRaisesRegex(ValueError, "checksum mismatch"):
                smoke.check_archives(root)

    def test_missing_first_run_docs_and_ambiguous_manifest_fail(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            archives(root, "docs/getting-started/first-result.md")
            with self.assertRaisesRegex(ValueError, "first-run documentation"):
                smoke.check_archives(root)
            archives(root)
            path = root / "checksums.txt"
            path.write_text(path.read_text() * 2)
            with self.assertRaisesRegex(ValueError, "duplicate"):
                smoke.check_archives(root)

    def test_license_is_required_except_for_immutable_final_v0120(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            archives(root, "LICENSE")
            for version in ("v0.12.0", "0.12.0"):
                smoke.check_archives(root, version)
            for version in (None, "v0.12.0-next", "v0.12.1", "v1.0.0"):
                with self.subTest(version=version), self.assertRaisesRegex(ValueError, "license"):
                    smoke.check_archives(root, version)

    def test_dependency_notices_are_required_except_for_immutable_final_v0120(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            archives(root, "docs/third-party-notices.txt")
            for version in ("v0.12.0", "0.12.0"):
                smoke.check_archives(root, version)
            for version in (None, "v0.12.0-next", "v0.13.0", "v1.0.0"):
                with self.subTest(version=version), self.assertRaisesRegex(ValueError, "license"):
                    smoke.check_archives(root, version)

    def test_successful_process_without_workflow_evidence_is_failure(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            archives(root)
            output = root / "evidence"
            with self.assertRaises(FileNotFoundError):
                smoke.main(["--artifact-dir", str(root), "--version", "v0.12.0", "--output", str(output)])
            evidence = json.loads((output / "smoke.json").read_text())
            self.assertEqual(evidence["state"], "failed")
            self.assertIn("error", evidence)

    def test_reused_evidence_is_never_replaced(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            sentinel = root / "smoke.json"
            sentinel.write_text("keep")
            with self.assertRaises(FileExistsError):
                smoke.main(["--artifact-dir", str(root), "--version", "v0.12.0", "--output", str(root)])
            self.assertEqual(sentinel.read_text(), "keep")

    def test_hanging_command_is_bounded_even_after_parent_exits(self):
        with tempfile.TemporaryDirectory() as directory:
            with self.assertRaisesRegex(ValueError, "timed out"):
                smoke.run_command(["/bin/sh", "-c", "sleep 30 & exit 0"], directory, timeout=0.1)

    def test_stale_revision_or_uncertain_application_cannot_pass(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "run").mkdir()
            patch = b"retained fixture patch"
            digest = hashlib.sha256(patch).hexdigest()
            patch_path = root / "run" / "candidates" / "fixture" / "changes.patch"
            patch_path.parent.mkdir(parents=True)
            patch_path.write_bytes(patch)
            for directory in (root / "repository", patch_path.parent / "worktree"):
                directory.mkdir()
                (directory / "hello.txt").write_bytes(b"Agentmux demo: checked change applied.\n")
            (root / "run" / "report.html").write_text("fixture report")
            (root / "demo.json").write_text(json.dumps({"state": "completed", "phase": "finished", "applied": True, "repository": str(root / "repository"), "run": str(root / "run")}))
            candidate = {"id": "fixture", "state": "verified", "verification": {"passed": True, "tree": "a", "ending_tree": "a", "content_sha256": "c", "ending_content_sha256": "c", "checks": [{"name": "expected fixture text", "passed": True, "exit_code": 0}]}, "final_snapshot": {"tree": "a", "content_sha256": "c"}, "patch_sha256": digest, "patch": str(patch_path)}
            report = {"state": "completed", "candidates": [candidate]}
            receipt = {"applied": True, "uncertain": False, "patch_sha256": digest}
            report_path = root / "run" / "report.json"
            receipt_path = root / "run" / "applied-1.json"
            report_path.write_text(json.dumps(report))
            receipt_path.write_text(json.dumps(receipt))
            smoke.verify_demo(root)
            candidate["verification"]["ending_tree"] = "different"
            report_path.write_text(json.dumps(report))
            with self.assertRaisesRegex(ValueError, "revision-bound"):
                smoke.verify_demo(root)
            candidate["verification"]["ending_tree"] = "a"
            report_path.write_text(json.dumps(report))
            receipt["uncertain"] = True
            receipt_path.write_text(json.dumps(receipt))
            with self.assertRaisesRegex(ValueError, "confirmed receipt"):
                smoke.verify_demo(root)
            receipt["uncertain"] = False
            receipt_path.write_text(json.dumps(receipt))
            candidate["verification"]["checks"] = []
            report_path.write_text(json.dumps(report))
            with self.assertRaisesRegex(ValueError, "independently passing"):
                smoke.verify_demo(root)
            candidate["verification"]["checks"] = [{"name": "expected fixture text", "passed": True, "exit_code": 1}]
            report_path.write_text(json.dumps(report))
            with self.assertRaisesRegex(ValueError, "independently passing"):
                smoke.verify_demo(root)
            candidate["verification"]["checks"][0]["exit_code"] = 0
            report_path.write_text(json.dumps(report))
            patch_path.write_bytes(b"changed patch")
            with self.assertRaisesRegex(ValueError, "patch bytes"):
                smoke.verify_demo(root)
            patch_path.write_bytes(patch)
            (root / "repository" / "hello.txt").write_bytes(b"wrong artifact")
            with self.assertRaisesRegex(ValueError, "fixture bytes"):
                smoke.verify_demo(root)


if __name__ == "__main__":
    unittest.main()
