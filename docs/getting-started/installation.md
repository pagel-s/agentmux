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

The easiest way to install Agentmux is using Go:

```bash
go install github.com/pagel-s/agentmux/cmd/agentmux@latest
```

This will place the `agentmux` binary in your `$GOPATH/bin` directory (usually `~/go/bin`). Ensure this directory is in your system's `$PATH`.

## Verifying Installation

Run the built-in diagnostic tool to ensure everything is set up correctly:

```bash
agentmux doctor
```

You should see output indicating that both `agentmux` and `tmux` are found in your path.
