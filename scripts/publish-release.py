#!/usr/bin/env python3
"""Publish a draft only when its assets match a passing local release smoke run."""
import argparse
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import re
import sys
import urllib.error
import urllib.request

spec = importlib.util.spec_from_file_location("release_gate", Path(__file__).with_name("check-release.py"))
gate = importlib.util.module_from_spec(spec)
spec.loader.exec_module(gate)


def verified_draft(release, tag, artifact_dir, evidence):
    if (evidence.get("state") != "passed" or evidence.get("release") != tag
            or set(evidence.get("assets", {})) != gate.ASSETS - {"checksums.txt"}
            or evidence.get("installed_asset") not in evidence["assets"]):
        raise ValueError("a passing smoke run for this exact release is required")
    if (not release or release.get("tag_name") != tag or release.get("draft") is not True
            or not isinstance(release.get("id"), int)):
        raise ValueError("expected an existing unpublished draft for this tag")
    digests = {}
    for name in gate.ASSETS:
        hasher = hashlib.sha256()
        with (artifact_dir / name).open("rb") as stream:
            for block in iter(lambda: stream.read(1024 * 1024), b""):
                hasher.update(block)
        digest = hasher.hexdigest()
        expected = evidence.get("manifest_sha256") if name == "checksums.txt" else evidence["assets"].get(name)
        if expected != digest:
            raise ValueError("local release bytes changed since smoke verification")
        digests[name] = "sha256:" + digest
    assets = release.get("assets", [])
    if len(assets) != len(gate.ASSETS) or {asset.get("name") for asset in assets} != gate.ASSETS:
        raise ValueError("draft must contain exactly the five expected release assets")
    for asset in assets:
        if (asset.get("state") != "uploaded" or asset.get("size", 0) <= 0
                or asset.get("digest") != digests[asset["name"]]):
            raise ValueError("draft assets differ from the verified release bytes")
    return release["id"]


def publish(repository, release_id):
    token = os.environ.get("GITHUB_TOKEN") or os.environ.get("GH_TOKEN")
    if not token:
        raise ValueError("GITHUB_TOKEN is required to publish a verified draft")
    endpoint = os.environ.get("GITHUB_API_URL", "https://api.github.com").rstrip("/")
    request = urllib.request.Request(endpoint + "/repos/" + repository + "/releases/" + str(release_id),
                                     data=json.dumps({"draft": False}).encode(), method="PATCH", headers={
                                         "Authorization": "Bearer " + token, "Accept": "application/vnd.github+json",
                                         "Content-Type": "application/json", "X-GitHub-Api-Version": "2022-11-28"})
    try:
        with urllib.request.urlopen(request, timeout=30) as response:
            result = json.load(response)
    except (urllib.error.URLError, TimeoutError, json.JSONDecodeError):
        raise ValueError("publication did not confirm completion; inspect the release before retrying") from None
    if result.get("draft") is not False or not result.get("published_at") or result.get("id") != release_id:
        raise ValueError("publication result is uncertain; inspect the release before retrying")
    return result


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--repository", required=True)
    parser.add_argument("--tag", required=True)
    parser.add_argument("--commit", required=True)
    parser.add_argument("--artifact-dir", type=Path, required=True)
    parser.add_argument("--smoke-evidence", type=Path, required=True)
    args = parser.parse_args(argv)
    if not re.fullmatch(r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+", args.repository):
        parser.error("expected owner/repository")
    gate.verify_source(args.tag, args.commit)
    with args.smoke_evidence.open() as stream:
        evidence = json.load(stream)
    release = gate.fetch_release(args.repository, args.tag)
    release_id = verified_draft(release, args.tag, args.artifact_dir, evidence)
    # Recheck the tag after artifact inspection and immediately before publication.
    gate.verify_source(args.tag, args.commit)
    result = publish(args.repository, release_id)
    print("Published verified release: " + result.get("html_url", args.tag))
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except (ValueError, OSError) as error:
        print("error: " + str(error), file=sys.stderr)
        sys.exit(1)
