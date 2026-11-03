# Managed setup

`amx managed setup --output team.yaml` asks for each agent's role, harness,
model, isolation and authentication choice. It discovers installed executables
and code dependencies, runs bounded preflight checks, creates dedicated private
workspaces, and writes a new mode-0600 validated YAML file. It refuses to
overwrite an existing configuration. Setup never starts a model, logs in,
prints credentials, or changes provider/global settings.

For reproducible setup, supply a JSON specification:

```sh
amx managed setup --spec examples/managed/setup-local-pi.json --output team.yaml
amx managed doctor team.yaml
amx managed up team.yaml --broker-sandbox bubblewrap
```

Edit the model ID and endpoint to match a running server first. A failed
preflight creates no configuration or workspaces. The report keeps unchecked
provider authentication and unsupported endpoint metadata visible as warnings.
Namespace/resource requirements still fail closed when the selected backend
is unavailable. The generated `next` commands retain the broker isolation
choice, which belongs to the launcher rather than the agent YAML.

The specification has `session`, `workspace_root`, `sandbox`, `broker_sandbox`,
`shared_board`, `init_git`, and an `agents` array. Agent fields include `id`,
`role`, `harness`, `model`, optional `binary`, `interaction`, `native_only`, `workdir`, `conversation`, `coding_tools`, `network`, `egress`,
`codex_sandbox`, and explicit authentication/endpoint fields. Relative workspace
and executable selections resolve from the invocation directory. Native auth
paths must reference private files; their contents are never embedded in YAML.
Do not put literal credentials into role text or paths.

Authentication choices are explicit:

| Choice | Meaning |
| --- | --- |
| `file` | Select `auth_file` for a supported Bubblewrap worker. Pi also needs exact `auth_provider`. Only the selected filtered credentials reach its fresh home. |
| `api-env` | Select `api_key_env`, a variable name. Add `base_url` for a custom Pi/Claude endpoint or Codex Responses endpoint. Its value remains outside argv/YAML. |
| `native-local` | Explicitly reuse the native host login in trusted local mode. This does not isolate files, tools, or peer credentials. |
| `none` | No selected auth; useful for a private unauthenticated compatible endpoint. Native local harnesses require `native-local` because they may discover host login. |

`coding_tools: true` gives Claude the explicit native
`Read,Write,Edit,Bash` allowlist. It is broader than a task-specific Bash pattern;
review the YAML before granting coding access. Bubblewrap Codex defaults to its
native workspace sandbox. An explicit `codex_sandbox: external` relies on the
required outer boundary when nested native user namespaces are unavailable.
Setup never changes a host policy or silently falls back to this mode.

Choose `interaction: interactive` for a human-controlled native terminal, or
omit it for a managed task worker. Interactive `native_only: true` requires an
empty role and omits Agentmux prompt injection. Native approvals, customizations
and history remain in the selected CLI; broker messages never become terminal
keystrokes. Managed persistent conversations, wakeups, traces, disk capsules and
allowlist egress are unavailable in interactive mode.

Preset setup is also available:

```sh
amx managed setup --preset coding --session work --project /path/to/project --output coding.yaml
amx managed setup --preset reviewer-pair --session review --output review.yaml
amx managed setup --preset research-team --session study --output study.yaml
```

`coding` creates native Claude/Codex terminals with peer/shared access disabled
until the owner enables collaboration. `reviewer-pair` and `research-team`
create idle managed workers with preset responsibilities; setup queues no task.
Presets default to explicitly trusted local execution; `--sandbox bubblewrap`
selects isolated agent and broker setup and requires its selected private auth
files. Review the resulting paths, roles and permissions before starting.

For npm Pi, discovery writes a canonical Node executable plus real JavaScript
entrypoint and explicit package/optional native dependency paths. For known
Codex standalone packages it includes the code-mode host, packaged Bubblewrap,
ripgrep, and public manifest. It never mounts an entire provider home. Native
Claude is self-contained. See the [official Codex CLI installation guide](https://learn.chatgpt.com/docs/codex/cli)
for supported installation methods; setup consumes the installed CLI.

`init_git: true` or `--init-git` initializes Git only in newly created agent
workspaces, with global/system configuration and hooks excluded for that
operation. Existing workspaces and repositories are preserved. Setup makes no
commits and does not set a Git identity. To use an existing project, review
`workdir` and dependency paths deliberately; avoid mounting coordination state
or a whole home. In a multi-agent coding team, use separate checkouts/worktrees
and an explicit integration process for changes.

Spec-based ACLs default to deny. With `shared_board: true`, the `team` board and `project` memory
are shared read/write among the selected agents. Private memory remains scoped
per agent. Setup does not grant cross-agent instructions, spawn rights, or
autonomy implicitly; add reviewed hierarchy, wakeup and template settings using
the [managed runtime](managed-runtime.md) and [dynamic team guide](dynamic-teams.md).
The `coding` preset instead leaves those scopes ungranted until owner
collaboration is enabled. [Live role edits](managed-runtime.md#boards-messages-instructions-and-memory)
and [live permissions/admission controls](live-permissions.md) are separate
versioned overlays; they do not rewrite the saved launch definition.

## Compatible endpoint metadata

Pi endpoints use the exact supplied API base, commonly `/v1`. Discovery sends
metadata requests only to configured loopback/private destinations, pins DNS,
ignores proxy settings, bounds response size/time, and never follows redirects.
Generic/vLLM discovery reads exact model records from `GET /models`; vLLM's
[`max_model_len` field](https://docs.vllm.ai/en/latest/api/vllm/entrypoints/openai/models/serving/)
describes context length. `protocol: ollama` also tries the metadata-only
`POST /api/show` at the corresponding server root and reads declared model
context, runtime `num_ctx`/`num_predict`, and explicit capability metadata.
It never calls generation endpoints during setup/preflight.

Discovery does not infer capabilities from model names. Missing fields retain
8192 context tokens, 2048 output tokens (reduced for smaller declared contexts),
text input, and no declared reasoning support. Tool compatibility remains
unverified when absent. The owner can override advertised metadata:

```yaml
connections:
  local:
    protocol: openai-compatible
    base_url: http://127.0.0.1:8000/v1
    model_metadata:
      context_window: 32768
      max_output: 4096
      input: [text]
      reasoning: false
      tools: true
```

The setup JSON accepts the same `model_metadata` object on each agent. Explicit
fields, including `false`, override discovery. `tools: false` disables Pi tools;
`true` describes expected transport support and grants no Agentmux permissions.
These declarations map to [Pi's supported model configuration](https://pi.dev/docs/latest/models-configuration),
not guarantees that every advertised endpoint accepts each tool or modality.
Doctor reports metadata without rewriting hand-authored YAML. Setup persists
its discovered/overridden selection so ordinary turns do not renegotiate it.
For handwritten YAML, doctor's discovery is advisory; runtime uses the configured
metadata or its defaults. The 8,192-token fallback is not a claim about an
unknown engine's real capacity: configure and verify that engine explicitly.
Pi 1.4.0 reserves 4,096 context tokens before selecting output capacity. Doctor
rejects a reported context of 4,096 or less; it never silently enlarges a known
engine limit. Use a verified larger engine context or a compatible runtime.

## Provider coverage limits

Managed Claude, Codex and Pi support private persistent conversations through
explicit native session references. Interactive sessions use provider-native
history selection instead.

Direct managed Antigravity supports fresh trusted-local turns. Its
[authentication documentation](https://antigravity.google/docs/install?tab=cli)
uses the OS secure keyring/DBus for native login, while its
[conversation guide](https://antigravity.google/docs/cli/conversations) describes
workspace scoping without an independent private storage-root contract that
Agentmux can enforce. Setup rejects isolated native authentication and persistent
conversation settings for that route. Direct interactive Antigravity requires
`native_only: true` and an empty role because its native role-injection interface
is unsupported.

For isolated persistent agents using Antigravity inference, choose
`harness: antigravity-bridge` in setup and follow the
[authenticated local bridge guide](antigravity-bridge.md). Pi provides isolated
tools/history while native authentication remains in a separate owner-controlled
host inference service.

Pi can connect to compatible Ollama/vLLM chat-completions endpoints. Configure
the exact model ID, available context and expected tool support before running.
Metadata and fake-endpoint tests verify configuration and protocol handling;
only a real bounded provider task can check the chosen service/model. Setup
never downloads model weights. See [provider compatibility](provider-compatibility.md)
for the distinct startup, continuation and tool checks.

The real Ollama test is opt-in and expects an existing service/model:

```sh
AGENTMUX_OLLAMA_LIVE=1 AGENTMUX_OLLAMA_URL=http://127.0.0.1:14534/v1 \
AGENTMUX_OLLAMA_MODEL=qwen2.5:14b \
go test -race -v ./internal/harness -run TestPiRealOllamaPrivatePersistentTwoTurns
```
