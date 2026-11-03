# Provider compatibility

Agentmux launches installed provider CLIs or connects Pi to an explicitly
configured inference endpoint. Install and authenticate the selected CLI first;
Agentmux does not create provider accounts, log in or download model weights.
Provider versions, accounts, models and optional tools can change compatibility.

## Choose a route

| Route | Interactive sessions | Managed tasks and history | Boundary |
| --- | --- | --- | --- |
| Claude Code | Native terminal with optional coordination context and delivery | Fresh or persistent structured turns | Trusted local execution or an explicit Bubblewrap profile; coding needs a native tool allowlist |
| Codex CLI | Native terminal with optional coordination context and delivery | Fresh or persistent structured turns; custom endpoints use Responses | Trusted local execution or Bubblewrap; external native sandbox policy requires an enforced outer boundary |
| Pi | Native terminal with optional coordination context and delivery | Fresh or persistent RPC turns; compatible endpoints use Chat Completions | Explicit runtime dependencies, endpoint/model metadata and selected authentication |
| Native Antigravity | `--native-only`, with normal native configuration/history | Fresh trusted-local structured turns | Native keyring/history stay on the host; direct isolated authentication and managed persistence are unsupported |
| Antigravity inference through Pi | Use Pi for the coordinated session | Pi owns isolated tools and persistent history | A separately managed trusted host bridge retains native Antigravity login |
| Generic command harness | Owner-selected command | Fresh command tasks | No inferred native history or provider identity |

The original v1 pane launcher also supports additional launch aliases described
in [pane harnesses](harnesses.md). Those aliases do not imply managed adapter or
native delivery support.

## Configure and inspect

```sh
claude --version
codex --version
pi --version
amx doctor
amx managed setup --output team.yaml
amx managed doctor team.yaml
```

Run only the version commands for CLIs you intend to use. Complete their native
login/startup prompts separately. Setup and doctor perform bounded capability,
authentication/metadata and isolation checks without generating model responses.
Unchecked authentication and endpoint capabilities remain visible as warnings.
Review the generated paths, tool permissions, credentials, network policy and
resource limits before launching. See [managed setup](managed-setup.md).

For npm Pi, use the canonical Node executable and real package entrypoint with
explicit read-only package dependencies. Mounting a launcher symlink alone can
break package-relative resolution even if `--version` succeeds. Codex standalone
installations can also need companion executables; setup discovers known layouts.
Avoid mounting an entire provider home as a dependency.

Custom Codex endpoints must implement Responses. Pi endpoints must implement the
selected Chat Completions contract. For Ollama/vLLM, configure the exact API base,
model ID, real context limit and expected tool support. Missing metadata or an
advertised capability does not establish actual model behavior. Use the
[Antigravity bridge guide](antigravity-bridge.md) for that separate lifecycle.

## Verify the selected integration

Check startup, inference, continuation and tools separately. A successful help
or metadata request does not show that a model can answer; a text answer does
not show that tools work; a continuation check does not establish the correctness
of a review. Use a disposable workspace and explicit model/endpoint selections,
finite deadlines and small turn budgets for provider checks.

Start with model-free coordination and isolation checks:

```sh
make build
python3 scripts/smoke-managed.py --binary bin/amx
python3 scripts/smoke-managed-interactive.py --binary bin/amx
```

These require the host capabilities documented by each script and make no model
calls. The [mixed-harness scenario](mixed-harness-acceptance.md) and
[bounded autonomous workflow](autonomous-workflows.md#reproduce-a-bounded-workflow)
provide opt-in real tasks with independent artifact verification. The
[workflow validation guide](workflow-validation.md) covers `amx work` and
`amx flow`; their isolation is configured separately from managed teams.
Retain private reports for your own installation and inspect failures or uncertain
outcomes before starting another attempt.

Local execution retains host filesystem authority. Requested Bubblewrap and
resource boundaries must fail closed when unavailable. Model services outside
worker sandboxes remain separately trusted, and native turns may contain several
provider requests. Turn limits and reported usage are not hard billing caps.
A successful bounded scenario establishes its configured path and task, not
universal provider/tool compatibility, model quality or long-term reliability.
