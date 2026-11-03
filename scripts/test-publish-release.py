#!/usr/bin/env python3
"""Publication must use the same bytes that passed installed-artifact checks."""
import copy
import hashlib
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest
from unittest import mock

spec = importlib.util.spec_from_file_location("release_publish", Path(__file__).with_name("publish-release.py"))
publish = importlib.util.module_from_spec(spec)
spec.loader.exec_module(publish)


class PublishTests(unittest.TestCase):
    def fixture(self, root):
        assets = []
        digests = {}
        for name in publish.gate.ASSETS:
            (root / name).write_bytes(name.encode())
            digest = hashlib.sha256(name.encode()).hexdigest()
            assets.append({"name": name, "state": "uploaded", "size": len(name), "digest": "sha256:" + digest})
            if name != "checksums.txt":
                digests[name] = digest
        release = {"tag_name": "v0.12.0", "id": 42, "draft": True, "assets": assets}
        evidence = {"state": "passed", "release": "v0.12.0", "assets": digests, "installed_asset": "agentmux_linux_x86_64.tar.gz", "manifest_sha256": hashlib.sha256(b"checksums.txt").hexdigest()}
        return release, evidence

    def test_publish_requires_matching_success_evidence_and_uploaded_bytes(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            release, evidence = self.fixture(root)
            self.assertEqual(publish.verified_draft(release, "v0.12.0", root, evidence), 42)
            for change in ({"state": "failed"}, {"release": "v0.11.0"}, {"assets": {}}, {"installed_asset": "missing"}):
                with self.subTest(change=change):
                    altered = dict(evidence, **change)
                    with self.assertRaises(ValueError):
                        publish.verified_draft(release, "v0.12.0", root, altered)
            altered = copy.deepcopy(release)
            altered["assets"][0]["digest"] = "sha256:" + "a" * 64
            with self.assertRaisesRegex(ValueError, "draft assets differ"):
                publish.verified_draft(altered, "v0.12.0", root, evidence)
            (root / evidence["installed_asset"]).write_bytes(b"changed after passing smoke")
            with self.assertRaisesRegex(ValueError, "changed since smoke"):
                publish.verified_draft(release, "v0.12.0", root, evidence)

    def test_replaced_manifest_cannot_be_published_even_if_remote_matches_it(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            release, evidence = self.fixture(root)
            changed = b"installer-breaking manifest replacement"
            (root / "checksums.txt").write_bytes(changed)
            for asset in release["assets"]:
                if asset["name"] == "checksums.txt":
                    asset["digest"] = "sha256:" + hashlib.sha256(changed).hexdigest()
            with self.assertRaisesRegex(ValueError, "changed since smoke"):
                publish.verified_draft(release, "v0.12.0", root, evidence)

    def test_published_duplicate_and_missing_assets_are_not_mutated(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            release, evidence = self.fixture(root)
            for change in ({"draft": False}, {"assets": release["assets"][:-1]}, {"assets": release["assets"] + [release["assets"][0]]}):
                with self.subTest(change=change), self.assertRaises(ValueError):
                    publish.verified_draft(dict(release, **change), "v0.12.0", root, evidence)

    def test_stale_remote_tag_immediately_before_publication_blocks_patch(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            release, evidence = self.fixture(root)
            path = root / "smoke.json"
            path.write_text(json.dumps(evidence))
            with mock.patch.object(publish.gate, "verify_source", side_effect=[None, ValueError("stale release attempt")]), \
                    mock.patch.object(publish.gate, "fetch_release", return_value=release), \
                    mock.patch.object(publish, "publish") as mutate:
                with self.assertRaisesRegex(ValueError, "stale"):
                    publish.main(["--repository", "pagel-s/agentmux", "--tag", "v0.12.0", "--commit", "a" * 40, "--artifact-dir", str(root), "--smoke-evidence", str(path)])
                mutate.assert_not_called()


if __name__ == "__main__":
    unittest.main()
