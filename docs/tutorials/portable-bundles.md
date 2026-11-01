# Portable layout bundles

This page covers original v1 layout `.amx` files. It does not package managed
credentials, a live coordination store or provider-native history. For portable
managed evidence use [trace bundles](../traces-and-recovery.md), and for reusable
memory use [memory export/import](../memory-lifecycle.md).

When you build a great agentic team, you will want to share it. However, if your agents rely on local Python scripts or text files for prompts, just sending the YAML configuration isn't enough.

Agentmux solves this with **Portable Bundles** (`.amx` files) and the `{{config_dir}}` variable.

## 1. Using `{{config_dir}}`

The `{{config_dir}}` variable is automatically injected by Agentmux during configuration load. It represents the absolute path to the directory containing your `yaml` file.

You should **always** use this variable when referencing local files.

**Bad:**
```yaml
agents:
  researcher:
    command: "python3 ./scripts/scrape.py" # Breaks if launched from another folder!
```

**Good:**
```yaml
agents:
  researcher:
    command: "python3 {{config_dir}}/scripts/scrape.py"
```

## 2. Packing a Bundle

Once you have organized your team into a folder (with the YAML and any scripts), you can create a self-contained bundle.

```bash
# Assuming you have a folder: my-team/session.yaml, my-team/script.sh
agentmux pack my-team/session.yaml session.amx
```

This creates `session.amx` in the current directory. Without the second path, the
default name comes from the YAML's `session`, not its filename. The ZIP contains
the YAML's directory tree, excluding root `.git`, `.cache`, `.tmp`, `bin`, other
`.amx` files and the output itself. Review the directory before packing; the
bundle contains the remaining files rather than a minimal dependency manifest.

## 3. Distributing the Bundle

You can now send `session.amx` to anyone. They don't need to extract it manually. Agentmux handles it transparently:

```bash
agentmux load session.amx
```

### Remote Loading

Because `.amx` files are self-contained, they are perfect for HTTP distribution. You can host them on GitHub Releases, S3, or your own server, and users can launch them with a single command:

```bash
agentmux load https://your-server.com/downloads/my-team.amx
```

Agentmux downloads/unpacks the bundle into a temporary directory and launches
its configured commands, using `{{config_dir}}` to resolve local paths. Loading
a bundle executes those commands; it is not a sandbox or a signature-verification
workflow.
