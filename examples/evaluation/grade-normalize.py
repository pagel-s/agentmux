#!/usr/bin/env python3
"""Evaluator-owned functional contract, outside the attempted repository."""
from pathlib import Path
import runpy
import sys

normalize = runpy.run_path(str(Path(sys.argv[1], "normalize.py")))["normalize"]
for value, expected in (("  Hello WORLD  ", "hello-world"), (" a\tb\nc ", "a-b-c"), ("", ""), (" x   y ", "x-y")):
    actual = normalize(value)
    if actual != expected:
        raise AssertionError(f"{value!r}: expected {expected!r}, got {actual!r}")
print("Independent normalization contract passed")
