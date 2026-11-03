#!/usr/bin/env python3
"""Deterministic control actor. Never starts a model or a provider CLI."""
from pathlib import Path
import sys
import subprocess
import time

mode, repository, task_file, output = sys.argv[1:]
if mode == "timeout":
    time.sleep(30)
elif mode == "background":
    child = subprocess.Popen([sys.executable, "-c", "import time;time.sleep(30)"])
    Path(output, "child.pid").write_text(str(child.pid))
    time.sleep(0.4)
elif mode == "incorrect":
    Path(repository, "normalize.py").write_text("def normalize(text):\n    return text\n")
else:
    Path(repository, "normalize.py").write_text("def normalize(text):\n    return '-'.join(text.strip().lower().split())\n")
    if mode == "nonzero":
        sys.exit(7)
