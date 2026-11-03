#!/usr/bin/env python3
"""Read-only publication gate: never replace an existing release or moved tag."""
import argparse
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import urllib.error
import urllib.parse
import urllib.request


ASSETS = {
    "agentmux_darwin_arm64.tar.gz", "agentmux_darwin_x86_64.tar.gz",
    "agentmux_linux_arm64.tar.gz", "agentmux_linux_x86_64.tar.gz", "checksums.txt",
}


def git(*args):
    result = subprocess.run(["git", *args], capture_output=True, text=True, timeout=30)
    if result.returncode:
        raise ValueError("could not verify the release tag with git; publication was not started")
    return result.stdout.strip()


def verify_source(tag, commit):
    if not re.fullmatch(r"v[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?", tag):
        raise ValueError("release tags must be version tags such as v0.11.0")
    if not re.fullmatch(r"[0-9a-f]{40}", commit) or git("rev-parse", "HEAD") != commit:
        raise ValueError("checkout does not match the workflow commit; publication was not started")
    ref = "refs/tags/" + tag
    refs = {}
    for line in git("ls-remote", "--tags", "origin", ref, ref + "^{}").splitlines():
        sha, name = line.split("\t", 1)
        refs[name] = sha
    current = refs.get(ref + "^{}", refs.get(ref))
    if current != commit:
        raise ValueError("stale release attempt: the current remote tag differs from this workflow's commit; use the run for the current tag")


def fetch_release(repository, tag):
    if not re.fullmatch(r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+", repository):
        raise ValueError("expected an owner/repository name")
    token = os.environ.get("GITHUB_TOKEN") or os.environ.get("GH_TOKEN")
    if not token:
        raise ValueError("GITHUB_TOKEN is required to verify release state")
    endpoint = os.environ.get("GITHUB_API_URL", "https://api.github.com").rstrip("/")
    url = endpoint + "/repos/" + repository + "/releases/tags/" + urllib.parse.quote(tag, safe="")
    request = urllib.request.Request(url, headers={
        "Authorization": "Bearer " + token,
        "Accept": "application/vnd.github+json",
        "X-GitHub-Api-Version": "2022-11-28",
    })
    try:
        with urllib.request.urlopen(request, timeout=30) as response:
            return json.load(response)
    except urllib.error.HTTPError as error:
        if error.code == 404:
            return None
        raise ValueError("could not verify release state (HTTP %s); publication was not started" % error.code) from None
    except (urllib.error.URLError, TimeoutError, json.JSONDecodeError):
        raise ValueError("could not read release state; publication was not started") from None


def should_publish(tag, release):
    if release is None:
        return True
    if release.get("tag_name") != tag or release.get("draft") or not release.get("published_at"):
        raise ValueError("an unfinished release already exists; inspect it before publishing, without replacing assets")
    assets = {asset.get("name"): asset for asset in release.get("assets", [])}
    for name in sorted(ASSETS):
        asset = assets.get(name, {})
        if (asset.get("state") != "uploaded" or asset.get("size", 0) <= 0
                or not re.fullmatch(r"sha256:[0-9a-f]{64}", asset.get("digest") or "")):
            raise ValueError("the existing release has missing or incomplete assets; inspect it before publishing, without replacing assets")
    return False


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--repository", required=True)
    parser.add_argument("--tag", required=True)
    parser.add_argument("--commit", required=True)
    args = parser.parse_args(argv)
    try:
        verify_source(args.tag, args.commit)
        publish = should_publish(args.tag, fetch_release(args.repository, args.tag))
    except ValueError as error:
        print("error:", error, file=sys.stderr)
        return 1
    if os.environ.get("GITHUB_OUTPUT"):
        with Path(os.environ["GITHUB_OUTPUT"]).open("a") as output:
            output.write("publish=" + str(publish).lower() + "\n")
    message = ("Source and release state verified; publication may proceed." if publish else
               args.tag + " is already published with all five assets; skipping duplicate publication.")
    print(message)
    if os.environ.get("GITHUB_STEP_SUMMARY"):
        with Path(os.environ["GITHUB_STEP_SUMMARY"]).open("a") as summary:
            summary.write(message + "\n")
    return 0


if __name__ == "__main__":
    sys.exit(main())
