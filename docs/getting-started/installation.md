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

### For Users (Recommended)

The easiest way to install the latest version of Agentmux is using Go:

```bash
go install github.com/pagel-s/agentmux/cmd/agentmux@latest
```

This will place the `agentmux` binary in your `$GOPATH/bin` directory (usually `~/go/bin`). Ensure this directory is in your system's `$PATH`.

### For Developers

If you are contributing to Agentmux or want to build from source:

1. Clone the repository.
2. Run the build command:

```bash
make build
```

This will create a `bin/agentmux` binary with the current Git commit and build date embedded.

## Verifying Installation

Run the built-in diagnostic tool to ensure everything is set up correctly:

```bash
agentmux doctor
```

You should see output indicating that `agentmux` and `tmux` are available, and if you pass a config path it will also inspect provider CLI compatibility for any configured native models.
