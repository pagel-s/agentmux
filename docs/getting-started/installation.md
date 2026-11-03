# Installation

Agentmux's CLI is **`amx`**. Native sessions require **tmux 3.2+** and the
respective provider CLIs installed and authenticated separately. Published
binaries need no Go toolchain. Python 3 runs the installer and verification scripts.

Choose the current source build below for [`amx work`](../working.md) and
[`amx flow`](../workflows.md), including private workflow snapshots and full
workflow isolation. The published v0.11.0 binary predates those commands.

## Install a published binary (recommended)

The current release is **[v0.11.0](https://github.com/pagel-s/agentmux/releases/tag/v0.11.0)**,
with Linux/macOS binaries for x86-64 and ARM64. It includes the focused terminal
workbench, native layouts, last-session attachment and the
[illustrated workflows](../visual-guide.md). The managed runtime and workbench
remain experimental.

For publicly accessible release assets, download the versioned installer from
GitHub, inspect it, then run it with Python 3. These commands also require `curl`.
The installer downloads the matching platform archive and checksum manifest
from the selected release. For assets that require authentication, use the
GitHub CLI route below.

```sh
mkdir -p "$HOME/.local/bin"
amx_release_dir="$(mktemp -d)"
curl --fail --location --proto '=https' --tlsv1.2 \
  https://raw.githubusercontent.com/pagel-s/agentmux/v0.11.0/scripts/install-release.py \
  --output "$amx_release_dir/install-release.py"
# Read the downloaded installer before running it.
python3 "$amx_release_dir/install-release.py" --version v0.11.0 \
  --destination "$HOME/.local/bin/amx"
rm -r "$amx_release_dir"
export PATH="$HOME/.local/bin:$PATH"
amx version
amx doctor
```

For access-controlled releases, authenticate GitHub CLI with an account that
has repository access (`gh auth login` if needed), then download the assets and
versioned installer before installation:

```sh
mkdir -p "$HOME/.local/bin"
amx_release_dir="$(mktemp -d)"
gh release download v0.11.0 --repo pagel-s/agentmux \
  --dir "$amx_release_dir" --pattern 'agentmux_*.tar.gz' --pattern checksums.txt
gh api repos/pagel-s/agentmux/contents/scripts/install-release.py \
  --method GET -f ref=v0.11.0 --header 'Accept: application/vnd.github.raw+json' \
  > "$amx_release_dir/install-release.py"
# Read the downloaded installer before running it.
python3 "$amx_release_dir/install-release.py" --version v0.11.0 \
  --artifact-dir "$amx_release_dir" --destination "$HOME/.local/bin/amx"
rm -r "$amx_release_dir"
export PATH="$HOME/.local/bin:$PATH"
amx version
amx doctor
```

**Upgrading:** add `--replace` to the `python3` command. The installer verifies
SHA256 and the executable version before atomically installing `amx`. A previous
`amx → agentmux` shortcut is migrated to a real `amx` executable. Its exact old
executable is retained as an `amx.backup-*` file; the recognized old `agentmux`
path is retired. Unrelated symlinks and commands are never silently replaced.
A fresh installation creates only `amx`.

Archives retain the `agentmux_OS_ARCH.tar.gz` project name and contain `amx`,
docs, examples and scripts. The installer also accepts older archives whose
executable is named `agentmux`. To use already downloaded release assets, add
`--artifact-dir /path/to/release-files`; the directory must include the matching
platform archive and `checksums.txt`. From a checkout, run
`python3 scripts/install-release.py` with the same version and destination options.

A checksum checks consistency with the release manifest, not an independent
signature. The direct HTTPS route works without a login when the release assets
are publicly accessible. For access-controlled repositories, download the
archive and checksum manifest using authenticated GitHub tooling, then use the
checkout's installer with `--artifact-dir` as described above.

Installation does not restart native sessions or replace a workspace's pinned
controller. Reattach with the new `amx` for its updated dashboard. Follow the
[recovery guide](../traces-and-recovery.md) when deliberately upgrading a running
controller; fresh workspaces use the current controller automatically. Existing
external bridges that name a retired public executable may need their setup
printed again with `amx connect`.

Strict Bubblewrap isolation requires Linux with working user namespaces and a
supported Bubblewrap. macOS supports native tmux sessions and explicitly trusted
local execution. A shell split beside an isolated agent is a host shell, not a
new sandbox.

## Build from source

Building needs Git, Make, Python 3, tmux 3.2+ and **Go 1.24+**. The Makefile
prefers a compatible repo-local toolchain under `.tools/`, then Go from PATH.

```sh
# macOS
brew install tmux

# Ubuntu/Debian: install Go 1.24+ separately if needed
sudo apt install git make tmux python3

git clone https://github.com/pagel-s/agentmux.git
cd agentmux
make build
./bin/amx version
./bin/amx doctor
```

In an existing clean checkout on `main`, run `git pull --ff-only` and
`make build`. For a user-local installation:

```sh
make install
export PATH="$HOME/.local/bin:$PATH"
command -v amx
amx version
```

The installer does not edit shell configuration. Add the PATH export to your
shell's startup file if needed, and check `command -v amx` in each new terminal.
After later source updates, run `make install` again. Select another prefix with
`make install PREFIX=/your/prefix`, or use `BINDIR` and optional `DESTDIR` for
packaging. Existing executable replacements retain their original inode in a
uniquely named backup.

Check the source workflow commands after installation:

```sh
amx work --help
amx flow --help
```

`amx work "Describe the change" -- CHECK [ARG...]` previews a checked coding
journey without starting models or creating run files. Execution needs the
selected installed and authenticated provider CLIs plus `--allow-models`.
For a first model-free plan, use the separate temporary project in
[advanced workflows](../workflows.md#try-the-complete-journey-without-model-calls).

Remote `go install github.com/pagel-s/agentmux/cmd/agentmux@latest` is unsupported:
`go.mod` declares module path `agentmux`, so Go rejects the GitHub module path.
Use the published installer or build from the checkout.

## Check the native providers

```sh
claude --version
codex --version
amx doctor
```

Open each provider once directly and complete its normal login/startup prompts.
Agentmux reuses those native logins in trusted local mode; it does not install a
provider or sign you in. `amx doctor CONFIG` also checks configured native CLI
compatibility.

Continue with the [fresh-install walkthrough](quickstart.md) or
[terminal folders and layouts](../terminal-workflow.md). The walkthrough covers
native conversations, opt-in collaboration, shared memory, revocation and web
controls. From a checkout, Linux operators can verify model-free isolation with:

```sh
python3 scripts/smoke-managed.py
python3 scripts/smoke-managed-interactive.py
```

A missing Bubblewrap feature such as `--size`, or unavailable user namespaces,
fails the isolation check; it does not silently select trusted local execution.
For a configured team, use the [managed setup wizard](../managed-setup.md).
