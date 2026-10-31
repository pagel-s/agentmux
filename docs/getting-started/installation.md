# Installation

## Prerequisites

Agentmux requires **tmux** to be installed on your system. We recommend version `3.2` or higher for the best experience with pane titles.

If you are building Agentmux from source, use **Go 1.24 or newer**. The repo `Makefile` will prefer a compatible toolchain under `.tools/` when present, then fall back to `go` from your `PATH`.

```bash
# macOS
brew install tmux

# Ubuntu/Debian
sudo apt install tmux
```

## Installing Agentmux

### Build the current source (recommended for the managed preview)

To try the current managed runtime, build from the repository. You need Git,
Make and Go 1.24 or newer, in addition to tmux:

```bash
git clone https://github.com/pagel-s/agentmux.git
cd agentmux
make build
./bin/agentmux version
./bin/agentmux doctor
```

If you already have a checkout, run `make build` from its root. The build creates
`bin/agentmux` with the current Git version, commit and build date embedded.

To use `agentmux` without the `./bin/` prefix in your current shell:

```bash
export PATH="$PWD/bin:$PATH"
```

Remote `go install github.com/pagel-s/agentmux/cmd/agentmux@latest` is currently
unsupported: `go.mod` declares the module path as `agentmux`, so Go rejects the
GitHub module path. It also selects the stable v0.9.0 release rather than the
current managed-preview source.

### Install a specific published binary

From a repository checkout, use Python 3 to install a specific release with its
published SHA256 checked:

```bash
python3 scripts/install-release.py --version v0.10.0-rc.1 \
  --destination /your/existing/bin/agentmux
```

This example installs the tagged v0.10.0-rc.1 preview; it does not include later
changes on `main`. Use the source build above for the current hardening changes.

The destination parent must exist. Replacing an existing executable requires
`--replace`; stop managed workers first and follow the [upgrade/recovery
guide](../traces-and-recovery.md). The installer supports published Linux/macOS
amd64/arm64 archives. Strict Bubblewrap isolation requires Linux; macOS retains
the existing tmux workflow and explicitly trusted local execution. A checksum
checks consistency with the release manifest, not an independent signature.

## Verifying Installation

Run the built-in diagnostic tool to ensure everything is set up correctly:

```bash
./bin/agentmux doctor
```

For a binary installed on your `PATH`, use `agentmux doctor` instead. The diagnostic
reports tmux availability; passing a config path also checks provider CLI
compatibility for configured native models.

On Linux with working Bubblewrap namespaces, try the sandbox smoke test from
the checkout:

```bash
python3 scripts/smoke-managed.py
```

It checks communication between isolated agents and an isolated broker, shared
memory, and instruction permissions. It makes no model calls and cleans up its
temporary runtime. For a live team, continue with the
[managed setup wizard](../managed-setup.md).
