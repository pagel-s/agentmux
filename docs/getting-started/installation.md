# Installation

## Prerequisites

Agentmux requires **tmux** to be installed on your system. We recommend version `3.2` or higher for the best experience with pane titles.

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

You should see output indicating that both `agentmux` and `tmux` are found in your path.
