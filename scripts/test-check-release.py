#!/usr/bin/env python3
"""Release retry regressions; no network calls or publication."""
import importlib.util
import io
import json
import os
from pathlib import Path
import unittest
from unittest import mock
import urllib.error

spec = importlib.util.spec_from_file_location("release_gate", Path(__file__).with_name("check-release.py"))
gate = importlib.util.module_from_spec(spec)
spec.loader.exec_module(gate)
SHA = "a" * 40


class ReleaseGateTests(unittest.TestCase):
    def release(self):
        return {"tag_name": "v0.11.0", "published_at": "2026-11-02T00:00:00Z", "draft": False,
                "assets": [{"name": name, "state": "uploaded", "size": 10, "digest": "sha256:" + "b" * 64} for name in gate.ASSETS]}

    def test_new_release_may_publish(self):
        self.assertTrue(gate.should_publish("v0.11.0", None))

    def test_complete_published_release_is_a_successful_noop(self):
        self.assertFalse(gate.should_publish("v0.11.0", self.release()))

    def test_draft_or_incomplete_release_needs_inspection(self):
        for change in ({"draft": True}, {"published_at": None}, {"assets": []}, {"tag_name": "v0.10.0"}):
            with self.subTest(change=change):
                release = self.release(); release.update(change)
                with self.assertRaises(ValueError):
                    gate.should_publish("v0.11.0", release)

    def test_incomplete_upload_or_missing_digest_needs_inspection(self):
        for change in ({"state": "new"}, {"size": 0}, {"digest": None}):
            with self.subTest(change=change):
                release = self.release(); release["assets"][0].update(change)
                with self.assertRaises(ValueError):
                    gate.should_publish("v0.11.0", release)

    def test_lightweight_and_annotated_tags_resolve_to_expected_commit(self):
        for refs in (SHA + "\trefs/tags/v0.11.0", "c" * 40 + "\trefs/tags/v0.11.0\n" + SHA + "\trefs/tags/v0.11.0^{}"):
            with self.subTest(refs=refs), mock.patch.object(gate, "git", side_effect=[SHA, refs]):
                gate.verify_source("v0.11.0", SHA)

    def test_stale_retry_and_missing_remote_tag_cannot_publish(self):
        for refs in ("", "b" * 40 + "\trefs/tags/v0.11.0"):
            with self.subTest(refs=refs), mock.patch.object(gate, "git", side_effect=[SHA, refs]):
                with self.assertRaisesRegex(ValueError, "stale release attempt"):
                    gate.verify_source("v0.11.0", SHA)

    def test_wrong_checkout_or_invalid_tag_cannot_publish(self):
        with mock.patch.object(gate, "git", return_value="b" * 40):
            with self.assertRaisesRegex(ValueError, "checkout"):
                gate.verify_source("v0.11.0", SHA)
        with self.assertRaisesRegex(ValueError, "version tags"):
            gate.verify_source("v0.11.0\nunsafe", SHA)

    @mock.patch.dict(os.environ, {"GITHUB_TOKEN": "private-fixture-token"})
    def test_only_404_means_not_published(self):
        for code in (401, 403, 404, 429, 500):
            error = urllib.error.HTTPError("https://api.github.com", code, "fixture", {}, None)
            with self.subTest(code=code), mock.patch.object(gate.urllib.request, "urlopen", side_effect=error):
                if code == 404:
                    self.assertIsNone(gate.fetch_release("pagel-s/agentmux", "v0.11.0"))
                else:
                    with self.assertRaisesRegex(ValueError, "HTTP " + str(code)) as caught:
                        gate.fetch_release("pagel-s/agentmux", "v0.11.0")
                    self.assertNotIn("private-fixture-token", str(caught.exception))

    @mock.patch.dict(os.environ, {"GITHUB_TOKEN": "private-fixture-token"})
    def test_api_response_is_read(self):
        with mock.patch.object(gate.urllib.request, "urlopen", return_value=io.BytesIO(json.dumps(self.release()).encode())):
            self.assertEqual(gate.fetch_release("pagel-s/agentmux", "v0.11.0")["tag_name"], "v0.11.0")

    def test_stale_source_is_rejected_before_querying_release(self):
        with mock.patch.object(gate, "verify_source", side_effect=ValueError("stale release attempt")), \
                mock.patch.object(gate, "fetch_release") as fetch, mock.patch("sys.stderr", new_callable=io.StringIO):
            self.assertEqual(gate.main(["--repository", "pagel-s/agentmux", "--tag", "v0.11.0", "--commit", SHA]), 1)
            fetch.assert_not_called()


if __name__ == "__main__":
    unittest.main()
