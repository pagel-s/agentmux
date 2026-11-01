# Installation

## Prerequisites

Agentmux requires **tmux 3.2 or newer**. Building the current source also needs
Git, Make and **Go 1.24 or newer**. Python 3 runs the acceptance scripts and
optional release installer. Native model sessions require their respective
CLIs to be installed and logged in separately.

If you are building Agentmux from source, use **Go 1.24 or newer**. The repo `Makefile` will prefer a compatible toolchain under `.tools/` when present, then fall back to `go` from your `PATH`.

```bash
# macOS
brew install tmux

# Ubuntu/Debian: install Go 1.24+ separately if your distribution ships an older Go
sudo apt install git make tmux python3
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

If you already have a clean checkout on `main`, run `git pull --ff-only` and
`make build` from its root. The build creates
`bin/agentmux` with the current Git version, commit and build date embedded.

To use `agentmux` without the `./bin/` prefix in your current shell:

```bash
export PATH="$PWD/bin:$PATH"
```

For a user-local installation that also works outside the checkout:

```bash
mkdir -p "$HOME/.local/bin"
install -m 755 bin/agentmux "$HOME/.local/bin/agentmux"
export PATH="$HOME/.local/bin:$PATH"
command -v agentmux
agentmux version
```

Add that last `export PATH=...` line to your shell's startup file if the directory
is not already on PATH in new terminals (usually `~/.bashrc` for interactive
Bash). Check `command -v agentmux` in each new terminal to avoid using a different
installed version. After later source updates, rebuild and repeat the `install`
command to update this copy.

Remote `go install github.com/pagel-s/agentmux/cmd/agentmux@latest` is currently
unsupported: `go.mod` declares the module path as `agentmux`, so Go rejects the
GitHub module path. An `@latest` release selection also does not mean the latest
commit on `main`. Use the source build for the workbench described in these docs.

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

Check the native CLIs you will use, for example `claude --version` and
`codex --version`, then open each once directly to finish its normal login and
startup prompts. Agentmux reuses those native logins in trusted local mode; it
does not install a provider or sign you in.

For the step-by-step native-terminal acceptance journey, continue with the
[quickstart](quickstart.md). It covers two interactive sessions, opt-in
collaboration, shared memory, isolation and the web control center.

On Linux with a supported Bubblewrap and working user namespaces, run the
model-free sandbox smoke tests from the checkout:

```bash
python3 scripts/smoke-managed.py
python3 scripts/smoke-managed-interactive.py
```

It checks communication between isolated agents and an isolated broker, shared
memory, and instruction permissions. It makes no model calls and cleans up its
temporary runtime. Bubblewrap must support the flags used by the sandbox,
including `--size`; an older distribution package or disabled user namespaces
can fail this check. That failure does not silently select local execution.
For a configured live team, use the [managed setup wizard](../managed-setup.md).
