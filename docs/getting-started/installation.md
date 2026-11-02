# Installation

## Prerequisites

Agentmux requires **tmux 3.2 or newer**. Published binaries need no Go toolchain.
Building the current source also needs Git, Make and **Go 1.24 or newer**.
Python 3 runs `make install`, the acceptance
scripts and the optional release installer. Native model sessions require their respective
CLIs to be installed and logged in separately.

If you are building Agentmux from source, use **Go 1.24 or newer**. The repo `Makefile` will prefer a compatible toolchain under `.tools/` when present, then fall back to `go` from your `PATH`.

```bash
# macOS
brew install tmux

# Ubuntu/Debian: install Go 1.24+ separately if your distribution ships an older Go
sudo apt install git make tmux python3
```

## Installing Agentmux

### Install a published binary (recommended)

The current release is **[v0.10.0](https://github.com/pagel-s/agentmux/releases/tag/v0.10.0)**.
It includes the terminal and web workbench, `amx` shortcuts, native collaboration
and the workflows in the [visual guide](../visual-guide.md). The managed runtime
and workbench remain experimental.

On Linux or macOS, with Python 3 and `curl` installed:

```bash
mkdir -p "$HOME/.local/bin"
amx_installer="$(mktemp)"
curl --fail --location --output "$amx_installer" \
  https://raw.githubusercontent.com/pagel-s/agentmux/v0.10.0/scripts/install-release.py
python3 "$amx_installer" --version v0.10.0 \
  --destination "$HOME/.local/bin/agentmux"
rm "$amx_installer"
export PATH="$HOME/.local/bin:$PATH"
amx version
amx doctor
```

To upgrade an existing installation, add `--replace` to the `python3` command.
The installer downloads the selected platform's archive, verifies its published
SHA256, checks the executable version and creates the sibling `amx → agentmux`
shortcut. Replacing an executable retains an `agentmux.backup-*` recovery copy.
An unrelated existing `amx` command blocks installation even with `--replace`.
The destination parent must exist. Release archives themselves contain the
canonical `agentmux` executable and bundled documentation, examples and scripts.

The installer supports Linux/macOS x86-64 and ARM64. Strict Bubblewrap isolation
requires Linux with working user namespaces; macOS supports native tmux sessions
and explicitly trusted local execution. A checksum checks consistency with the
release manifest, not an independent signature.

Installing a binary does not restart native sessions or replace a running
workspace's pinned controller. Start a fresh workspace for the current features,
or follow the [upgrade/recovery guide](../traces-and-recovery.md) when ready to
upgrade an existing workspace.

### Build the current source

Use a source build for later changes on `main` or local development. You need
Git, Make and Go 1.24 or newer, in addition to tmux and Python 3:

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
make install
export PATH="$HOME/.local/bin:$PATH"
command -v agentmux amx
amx version
amx doctor
```

Add that last `export PATH=...` line to your shell's startup file if the directory
is not already on PATH in new terminals (usually `~/.bashrc` for interactive
Bash). The installer does not edit your shell configuration. Check
`command -v agentmux amx` in each new terminal to avoid using a different installed
version. After later source updates, run `make install` again.

`make install` builds and installs `agentmux` plus a relative `amx → agentmux`
symlink in `~/.local/bin`. Both commands expose the same features; `amx` alone
opens the terminal dashboard, while `agentmux` alone shows help. See the
[everyday command reference](everyday-commands.md) for shorter commands.
Choose another prefix with `make install PREFIX=/your/prefix`, or set `BINDIR`
and optional `DESTDIR` for packaging.

Upgrades replace the executable atomically and retain its previous inode in a
uniquely named `agentmux.backup-*` file beside it. An unrelated existing `amx`
command is never replaced: move it explicitly or select a different installation
directory. Existing shortcuts directly naming `agentmux` or its exact absolute
destination are reused, including during upgrades.
Installing a new binary does not restart your native sessions or replace a
running workspace's pinned controller. Upgrade a live workspace deliberately
using the [recovery guide](../traces-and-recovery.md).

Remote `go install github.com/pagel-s/agentmux/cmd/agentmux@latest` is currently
unsupported: `go.mod` declares the module path as `agentmux`, so Go rejects the
GitHub module path. An `@latest` release selection also does not mean the latest
commit on `main`. Use the published installer above or build from source.

### Install a specific published binary

From a repository checkout, use Python 3 to install a specific release with its
published SHA256 checked:

```bash
python3 scripts/install-release.py --version v0.10.0 \
  --destination /your/existing/bin/agentmux
```

Choose an explicit published tag. Add `--replace` when upgrading; the same backup,
alias and live-workspace behavior described above applies.

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
