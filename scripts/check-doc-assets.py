#!/usr/bin/env python3
"""Check captured guide bytes and their Markdown labels, without image packages.

This checks consistency of recorded capture evidence, not what an image depicts
or whether a provider ran. The source may precede HEAD: documentation-only
commits normally follow a capture. It checks local JPG link targets and reference
definitions, rather than trying to parse arbitrary Markdown.
"""
import argparse
from datetime import datetime, timedelta
import hashlib
import json
from pathlib import Path
import re
import subprocess
from urllib.parse import unquote, urlsplit


GUIDE = Path("docs/assets/guide")
MAX_TEXT_BYTES = 2 << 20
MAX_IMAGE_BYTES = 16 << 20
CLEANUP_PROOFS = {
    "tracked_web_and_runner_processes_exited",
    "owned_broker_connections_closed",
    "owned_tmux_servers_stopped",
}
LINK_TARGET = re.compile(
    r"\]\(([^\s()]+)(?:[ \t]+[\"'][^)\n]*[\"'])?\)"
    r"|^[ \t]{0,3}\[[^\]\n]+\]:[ \t]*<?([^>\s]+)>?", re.MULTILINE
)
CAPTURE_LABEL = re.compile(
    r"Screenshots were captured on \*\*([^*\n]+)\*\* from source "
    r"\*\*([^*\n]+)\*\*,\s+using \*\*([^*\n]+)\*\*"
)


def read_text(path):
    if path.is_symlink() or not path.is_file() or path.stat().st_size > MAX_TEXT_BYTES:
        raise ValueError(f"expected bounded regular text file: {path}")
    return path.read_text(encoding="utf-8")


def tracked_markdown(root):
    result = subprocess.run(
        ["git", "-C", str(root), "ls-files", "-z", "--", "*.md"],
        check=True, capture_output=True, timeout=10,
    )
    paths = [Path(name) for name in result.stdout.decode("utf-8").split("\0") if name]
    if not paths or len(paths) > 1000:
        raise ValueError("expected 1..1000 tracked Markdown files")
    return paths


def check(root, markdown_paths=None):
    root = Path(root).resolve()
    guide = root / GUIDE
    manifest = json.loads(read_text(guide / "capture-provenance.json"))
    if not isinstance(manifest, dict) or type(manifest.get("schema")) is not int or manifest["schema"] != 1:
        raise ValueError("capture manifest must use schema 1")
    source = manifest.get("source_commit")
    if not isinstance(source, str) or not re.fullmatch(r"[0-9a-f]{40}|[0-9a-f]{64}", source):
        raise ValueError("capture source_commit must be a full Git object ID")
    version = manifest.get("binary_version")
    if not isinstance(version, str) or len(version) > 512 or not re.fullmatch(r"amx v?\d+\.\d+\.\d+[^\r\n]*", version):
        raise ValueError("capture binary_version must be a nonempty amx version")
    build = re.search(r" \(commit: ([0-9a-f]{7,64}), built: [^)]+\)$", version)
    if not build or not source.startswith(build.group(1)):
        raise ValueError("capture binary version commit differs from source_commit")
    version_label = version[:build.start()]
    if not isinstance(manifest.get("binary_sha256"), str) or not re.fullmatch(r"[0-9a-f]{64}", manifest["binary_sha256"]):
        raise ValueError("capture binary_sha256 must be a SHA-256 digest")
    try:
        captured_at = datetime.fromisoformat(manifest["captured_at_utc"].replace("Z", "+00:00"))
        if captured_at.tzinfo is None or captured_at.utcoffset() != timedelta(0):
            raise ValueError("not UTC")
    except (KeyError, TypeError, AttributeError, ValueError) as error:
        raise ValueError("capture timestamp must be an explicit UTC timestamp") from error
    if type(manifest.get("model_calls")) is not int or manifest["model_calls"] != 0:
        raise ValueError("guide captures must record zero model_calls")
    cleanup = manifest.get("cleanup")
    if not isinstance(cleanup, dict) or not CLEANUP_PROOFS.issubset(cleanup) or any(value is not True for value in cleanup.values()):
        raise ValueError("capture cleanup proofs must all be true")

    images = manifest.get("images")
    if not isinstance(images, list) or not 1 <= len(images) <= 200:
        raise ValueError("capture manifest must list 1..200 images")
    hashes = {}
    for image in images:
        if not isinstance(image, dict):
            raise ValueError("capture image entries must be objects")
        name, digest = image.get("file"), image.get("sha256")
        if not isinstance(name, str) or not re.fullmatch(r"[a-z0-9][a-z0-9-]*\.jpg", name) or name in hashes:
            raise ValueError("capture image names must be unique plain JPG filenames")
        if not isinstance(digest, str) or not re.fullmatch(r"[0-9a-f]{64}", digest):
            raise ValueError(f"invalid image hash: {name}")
        path = guide / name
        if path.is_symlink() or not path.is_file() or not 0 < path.stat().st_size <= MAX_IMAGE_BYTES:
            raise ValueError(f"expected bounded regular capture image: {name}")
        if hashlib.sha256(path.read_bytes()).hexdigest() != digest:
            raise ValueError(f"capture image hash mismatch: {name}")
        if any(type(image.get(key)) is not int or image[key] <= 0 for key in ("width", "height")):
            raise ValueError(f"capture dimensions must be positive integers: {name}")
        hashes[name] = digest
    if {path.name for path in guide.glob("*.jpg")} != set(hashes):
        raise ValueError("guide JPG files and capture manifest differ")

    prose = read_text(root / "docs/visual-guide.md")
    labels = CAPTURE_LABEL.findall(prose)
    expected_label = (captured_at.date().isoformat(), source[:12], version_label)
    if labels != [expected_label]:
        raise ValueError(f"visual-guide capture label must match manifest: {expected_label}")

    references = 0
    referenced = set()
    for relative in tracked_markdown(root) if markdown_paths is None else markdown_paths:
        path = root / relative
        for inline, definition in LINK_TARGET.findall(read_text(path)):
            target = inline or definition
            url = urlsplit(target)
            if url.scheme or url.netloc:
                continue
            local = unquote(url.path)
            if not local.lower().endswith(".jpg"):
                continue
            resolved = (path.parent / local).resolve()
            if resolved.parent != guide:
                if "assets/guide/" in local:
                    raise ValueError(f"guide image reference resolves outside capture directory: {relative}: {target}")
                continue
            name = resolved.name
            if name not in hashes:
                raise ValueError(f"unrecorded guide image reference: {relative}: {name}")
            if url.query != "v=" + hashes[name][:12]:
                raise ValueError(f"guide image cache key mismatch: {relative}: {target}")
            references += 1
            referenced.add(name)
    if referenced != set(hashes):
        raise ValueError("every recorded guide capture must have a tracked Markdown reference")
    return len(hashes), references


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", type=Path, default=Path(__file__).resolve().parents[1])
    args = parser.parse_args(argv)
    try:
        images, references = check(args.root)
    except (ValueError, OSError, subprocess.SubprocessError) as error:
        parser.exit(1, f"documentation capture check failed: {error}\n")
    print(f"Documentation captures verified: {images} images, {references} Markdown references")


if __name__ == "__main__":
    main()
