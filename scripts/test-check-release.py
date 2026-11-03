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
        with self.assertRaisesRegex(ValueError, "version tags"):
            gate.verify_source("v0.11.0+unsupported-installer-metadata", SHA)

    @mock.patch.dict(os.environ, {"GITHUB_TOKEN": "private-fixture-token"})
    def test_primary_errors_other_than_404_are_fatal(self):
        for code in (401, 403, 429, 500):
            error = urllib.error.HTTPError("https://api.github.com", code, "fixture", {}, None)
            with self.subTest(code=code), mock.patch.object(gate.urllib.request, "urlopen", side_effect=error):
                with self.assertRaisesRegex(ValueError, "HTTP " + str(code)) as caught:
                    gate.fetch_release("pagel-s/agentmux", "v0.11.0")
                self.assertNotIn("private-fixture-token", str(caught.exception))

    @mock.patch.dict(os.environ, {"GITHUB_TOKEN": "private-fixture-token"})
    def test_api_response_is_read(self):
        with mock.patch.object(gate.urllib.request, "urlopen", return_value=io.BytesIO(json.dumps(self.release()).encode())) as fetch:
            self.assertEqual(gate.fetch_release("pagel-s/agentmux", "v0.11.0")["tag_name"], "v0.11.0")
            self.assertEqual(fetch.call_count, 1)

    def missing(self):
        return urllib.error.HTTPError("https://api.github.com", 404, "private-fixture-token", {}, None)

    def page(self, value):
        return io.BytesIO(json.dumps(value).encode())

    @mock.patch.dict(os.environ, {"GITHUB_TOKEN": "private-fixture-token"})
    def test_tag_404_finds_exact_draft_on_later_page_and_keeps_preflight_guard(self):
        draft = {"id": 432746554, "tag_name": "v0.12.0", "draft": True,
                 "published_at": None, "assets": []}
        unrelated = [{"id": number, "tag_name": "v0.12.0-rc1", "draft": True} for number in range(100)]
        with mock.patch.object(gate.urllib.request, "urlopen", side_effect=[self.missing(), self.page(unrelated), self.page([draft])]) as fetch:
            result = gate.fetch_release("pagel-s/agentmux", "v0.12.0")
        self.assertEqual(result, draft)
        self.assertEqual(fetch.call_count, 3)
        self.assertIn("/releases/tags/v0.12.0", fetch.call_args_list[0].args[0].full_url)
        self.assertTrue(fetch.call_args_list[1].args[0].full_url.endswith("/releases?per_page=100&page=1"))
        self.assertTrue(fetch.call_args_list[2].args[0].full_url.endswith("/releases?per_page=100&page=2"))
        with self.assertRaisesRegex(ValueError, "unfinished release"):
            gate.should_publish("v0.12.0", result)

    @mock.patch.dict(os.environ, {"GITHUB_TOKEN": "private-fixture-token"})
    def test_missing_tag_requires_completed_list_with_no_exact_match(self):
        for pages in ([[]], [[{"tag_name": "v0.12.0-other"}] * 100, []]):
            with self.subTest(pages=len(pages)), mock.patch.object(
                    gate.urllib.request, "urlopen", side_effect=[self.missing(), *(self.page(page) for page in pages)]) as fetch:
                self.assertIsNone(gate.fetch_release("pagel-s/agentmux", "v0.12.0"))
                self.assertEqual(fetch.call_count, 1 + len(pages))

    @mock.patch.dict(os.environ, {"GITHUB_TOKEN": "private-fixture-token"})
    def test_duplicate_exact_tags_on_same_or_later_pages_are_refused(self):
        draft = {"id": 42, "tag_name": "v0.12.0", "draft": True}
        other = {"id": 1, "tag_name": "v0.11.0"}
        for pages in ([[draft, dict(draft, id=43)]], [[draft] + [other] * 99, [dict(draft, id=43)]]):
            with self.subTest(pages=len(pages)), mock.patch.object(
                    gate.urllib.request, "urlopen", side_effect=[self.missing(), *(self.page(page) for page in pages)]):
                with self.assertRaisesRegex(ValueError, "multiple releases"):
                    gate.fetch_release("pagel-s/agentmux", "v0.12.0")

    @mock.patch.dict(os.environ, {"GITHUB_TOKEN": "private-fixture-token"})
    def test_list_failures_never_become_absence_or_expose_diagnostics(self):
        failures = [urllib.error.HTTPError("https://api.github.com", code, "private-fixture-token", {}, None)
                    for code in (401, 403, 404, 429, 500)]
        failures += [urllib.error.URLError("private-fixture-token"), TimeoutError("private-fixture-token"),
                     OSError("private-fixture-token"), RuntimeError("private-fixture-token")]
        for failure in failures:
            with self.subTest(error=type(failure).__name__), mock.patch.object(
                    gate.urllib.request, "urlopen", side_effect=[self.missing(), failure]):
                with self.assertRaises(ValueError) as caught:
                    gate.fetch_release("pagel-s/agentmux", "v0.12.0")
                self.assertNotIn("private-fixture-token", str(caught.exception))
        # Finding a match on an earlier page cannot conceal an unread later page.
        first = [{"tag_name": "v0.12.0", "draft": True}] + [{"tag_name": "other"}] * 99
        with mock.patch.object(gate.urllib.request, "urlopen", side_effect=[self.missing(), self.page(first), TimeoutError("secret")]):
            with self.assertRaises(ValueError):
                gate.fetch_release("pagel-s/agentmux", "v0.12.0")

    @mock.patch.dict(os.environ, {"GITHUB_TOKEN": "private-fixture-token"})
    def test_malformed_and_oversized_api_responses_fail_closed(self):
        payloads = [b"private-fixture-token", b"\xff", b"null", b"{}", b"[null]",
                    b'[{"tag_name":42}]', json.dumps([{"tag_name": "other"}] * 101).encode()]
        for payload in payloads:
            with self.subTest(payload=payload[:20]), mock.patch.object(
                    gate.urllib.request, "urlopen", side_effect=[self.missing(), io.BytesIO(payload)]):
                with self.assertRaises(ValueError) as caught:
                    gate.fetch_release("pagel-s/agentmux", "v0.12.0")
                self.assertNotIn("private-fixture-token", str(caught.exception))
        with mock.patch.object(gate, "RELEASE_RESPONSE_LIMIT", 16), mock.patch.object(
                gate.urllib.request, "urlopen", side_effect=[self.missing(), io.BytesIO(b"x" * 17)]):
            with self.assertRaisesRegex(ValueError, "response exceeds"):
                gate.fetch_release("pagel-s/agentmux", "v0.12.0")
        for malformed in ([], None, {"tag_name": "different"}):
            with self.subTest(primary=malformed), mock.patch.object(gate.urllib.request, "urlopen", return_value=self.page(malformed)):
                with self.assertRaisesRegex(ValueError, "unexpected release response"):
                    gate.fetch_release("pagel-s/agentmux", "v0.12.0")

    @mock.patch.dict(os.environ, {"GITHUB_TOKEN": "private-fixture-token"})
    def test_full_page_limit_cannot_claim_absence_or_uniqueness(self):
        for matching in (False, True):
            page = [{"tag_name": "unrelated"}] * gate.RELEASE_PAGE_SIZE
            pages = [self.page(page) for _ in range(gate.RELEASE_PAGE_LIMIT)]
            if matching:
                first = [{"tag_name": "v0.12.0", "draft": True}] + page[1:]
                pages[0] = self.page(first)
            with self.subTest(matching=matching), mock.patch.object(
                    gate.urllib.request, "urlopen", side_effect=[self.missing(), *pages]) as fetch:
                with self.assertRaisesRegex(ValueError, "pagination exceeds"):
                    gate.fetch_release("pagel-s/agentmux", "v0.12.0")
                self.assertEqual(fetch.call_count, 1 + gate.RELEASE_PAGE_LIMIT)

    def test_stale_source_is_rejected_before_querying_release(self):
        with mock.patch.object(gate, "verify_source", side_effect=ValueError("stale release attempt")), \
                mock.patch.object(gate, "fetch_release") as fetch, mock.patch("sys.stderr", new_callable=io.StringIO):
            self.assertEqual(gate.main(["--repository", "pagel-s/agentmux", "--tag", "v0.11.0", "--commit", SHA]), 1)
            fetch.assert_not_called()


if __name__ == "__main__":
    unittest.main()
