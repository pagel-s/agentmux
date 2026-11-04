#!/usr/bin/env python3
"""Negative controls for captured-documentation consistency."""
import copy
import hashlib
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest

spec = importlib.util.spec_from_file_location("doc_assets", Path(__file__).with_name("check-doc-assets.py"))
assets = importlib.util.module_from_spec(spec)
spec.loader.exec_module(assets)


class CaptureTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.root = Path(self.directory.name)
        self.guide = self.root / assets.GUIDE
        self.guide.mkdir(parents=True)
        self.image = self.guide / "terminal-board.jpg"
        self.image.write_bytes(b"disposable image bytes; decoding is outside this guard")
        self.digest = hashlib.sha256(self.image.read_bytes()).hexdigest()
        self.manifest = {
            "schema": 1, "source_commit": "a" * 40,
            "binary_version": "amx 0.13.0 (commit: aaaaaaa, built: 2026-11-03)",
            "binary_sha256": "b" * 64,
            "captured_at_utc": "2026-11-03T21:00:00+00:00", "model_calls": 0,
            "cleanup": {key: True for key in assets.CLEANUP_PROOFS},
            "images": [{"file": self.image.name, "sha256": self.digest, "width": 1440, "height": 960}],
        }
        self.write_manifest()
        self.visual = self.root / "docs/visual-guide.md"
        self.visual.write_text(
            "Screenshots were captured on **2026-11-03** from source **aaaaaaaaaaaa**, "
            "using **amx 0.13.0**, in disposable fixtures.\n"
            f"![Capture](assets/guide/{self.image.name}?v={self.digest[:12]})\n", encoding="utf-8")
        self.readme = self.root / "README.md"
        self.readme.write_text(f"[Full image](docs/assets/guide/{self.image.name}?v={self.digest[:12]})\n", encoding="utf-8")
        self.markdown = [Path("README.md"), Path("docs/visual-guide.md")]

    def write_manifest(self):
        (self.guide / "capture-provenance.json").write_text(json.dumps(self.manifest), encoding="utf-8")

    def check(self):
        return assets.check(self.root, self.markdown)

    def test_current_capture_and_both_image_and_plain_links_pass(self):
        self.assertEqual(self.check(), (1, 2))

    def test_changed_image_bytes_cannot_reuse_manifest(self):
        self.image.write_bytes(b"altered screenshot")
        with self.assertRaisesRegex(ValueError, "image hash mismatch"):
            self.check()

    def test_changed_manifest_digest_does_not_match_delivered_bytes(self):
        self.manifest["images"][0]["sha256"] = "c" * 64
        self.write_manifest()
        with self.assertRaisesRegex(ValueError, "image hash mismatch"):
            self.check()

    def test_stale_cache_key_on_plain_readme_link_is_rejected(self):
        self.readme.write_text(self.readme.read_text().replace(self.digest[:12], "deadbeef0000"))
        with self.assertRaisesRegex(ValueError, "cache key mismatch"):
            self.check()

    def test_missing_cache_key_is_rejected(self):
        self.visual.write_text(self.visual.read_text().replace("?v=" + self.digest[:12], ""))
        with self.assertRaisesRegex(ValueError, "cache key mismatch"):
            self.check()

    def test_titles_and_reference_definitions_cannot_bypass_cache_check(self):
        for target in (
            f'[Titled](docs/assets/guide/{self.image.name}?v=stale "Full size")\n',
            f'[capture]: <docs/assets/guide/{self.image.name}?v=stale> "Full size"\n',
        ):
            with self.subTest(target=target):
                self.readme.write_text(target)
                with self.assertRaisesRegex(ValueError, "cache key mismatch"):
                    self.check()

    def test_stale_source_date_and_version_labels_are_each_rejected(self):
        original = self.visual.read_text()
        for old, new in (("aaaaaaaaaaaa", "725ef1e"), ("2026-11-03", "2026-11-02"), ("amx 0.13.0", "amx 0.12.0")):
            with self.subTest(field=old):
                self.visual.write_text(original.replace(old, new))
                with self.assertRaisesRegex(ValueError, "capture label must match"):
                    self.check()
        self.visual.write_text(original)

    def test_missing_or_false_cleanup_proof_is_rejected(self):
        original = copy.deepcopy(self.manifest)
        for value in (False, 1, "true", None):
            with self.subTest(value=value):
                self.manifest = copy.deepcopy(original)
                self.manifest["cleanup"]["owned_tmux_servers_stopped"] = value
                self.write_manifest()
                with self.assertRaisesRegex(ValueError, "cleanup proofs"):
                    self.check()
        self.manifest = copy.deepcopy(original)
        del self.manifest["cleanup"]["owned_tmux_servers_stopped"]
        self.write_manifest()
        with self.assertRaisesRegex(ValueError, "cleanup proofs"):
            self.check()

    def test_unknown_or_nonzero_model_calls_are_rejected(self):
        for value in (1, None, False, "0"):
            with self.subTest(value=value):
                self.manifest["model_calls"] = value
                self.write_manifest()
                with self.assertRaisesRegex(ValueError, "zero model_calls"):
                    self.check()

    def test_empty_source_version_or_mismatched_binary_commit_is_rejected(self):
        original = copy.deepcopy(self.manifest)
        for field, value in (("source_commit", ""), ("source_commit", "a" * 7), ("binary_version", ""), ("binary_version", "amx 0.13.0 (commit: ccccccc, built: 2026-11-03)")):
            with self.subTest(field=field, value=value):
                self.manifest = dict(original, **{field: value})
                self.write_manifest()
                with self.assertRaises(ValueError):
                    self.check()

    def test_new_or_duplicate_manifest_image_is_rejected(self):
        self.manifest["images"].append(dict(self.manifest["images"][0]))
        self.write_manifest()
        with self.assertRaisesRegex(ValueError, "unique"):
            self.check()

    def test_unrecorded_guide_image_and_unreferenced_capture_are_rejected(self):
        extra = self.guide / "unrecorded.jpg"
        extra.write_bytes(b"old capture")
        with self.assertRaisesRegex(ValueError, "JPG files and capture manifest differ"):
            self.check()
        extra.unlink()
        self.visual.write_text(self.visual.read_text().split("![Capture]")[0])
        self.readme.write_text("No capture links.\n")
        with self.assertRaisesRegex(ValueError, "tracked Markdown reference"):
            self.check()

    def test_external_images_are_ignored_but_broken_local_guide_path_is_rejected(self):
        self.readme.write_text(self.readme.read_text() + "![External](https://example.test/docs/assets/guide/a.jpg)\n")
        self.assertEqual(self.check(), (1, 2))
        self.readme.write_text(self.readme.read_text() + f"[Broken](assets/guide/{self.image.name}?v={self.digest[:12]})\n")
        with self.assertRaisesRegex(ValueError, "outside capture directory"):
            self.check()


if __name__ == "__main__":
    unittest.main()
