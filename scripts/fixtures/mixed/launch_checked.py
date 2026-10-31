"""Run trusted boundary assertions inside the actual harness sandbox first."""
import os
import sys

import coord


def main():
    if len(sys.argv) < 2:
        raise SystemExit("native harness executable is required")
    # Agentmux's nongenerative metadata probes run on the host with a private
    # HOME. They do not have broker identity and must not publish fixture data.
    arguments = sys.argv[2:]
    status_probe = any(arguments[index:index + 2] in (["auth", "status"], ["login", "status"])
                       for index in range(len(arguments) - 1))
    if status_probe or any(flag in arguments for flag in ("--help", "-h", "--version", "-V")):
        os.execv(sys.argv[1], sys.argv[1:])
    try:
        coord.isolation_check()
    except Exception:
        print("Mixed-team sandbox boundary check failed.", file=sys.stderr)
        raise SystemExit(1) from None
    os.execv(sys.argv[1], sys.argv[1:])


if __name__ == "__main__":
    main()
