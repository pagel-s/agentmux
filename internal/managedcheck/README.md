# Managed preflight and doctor

`Check(ctx, config, options)` returns a stable sorted report. Rows contain only
agent ID, check name, status (`ok`, `warning`, `error`), and a safe message.
`Report.OK` is false only when an error is present. The row type is
`CheckResult`, because Go cannot name both a type and a function `Check`.

Preflight does not initialize managed state, create agent workspaces, log in,
download a model, or request generation. It checks static agents and every
enabled immutable spawn template, reported as `template:<name>`.

Native help/version commands run with a private temporary home and workdir,
minimal runtime environment, a three-second deadline per command, and bounded
128 KiB output. A native CLI must advertise the adapter's actual required flags.
Pi must be version 1.x or newer, because the maintained RPC protocol emits
`agent_settled`; the deprecated 0.x package does not provide that guarantee.
Some CLIs emit help on stderr, so isolated help/version calls inspect both
streams. Raw command output never enters reports. Custom command harnesses are
checked for executable availability but are never executed by doctor.

Named credential variables are checked for presence without printing values.
Optional `CheckAuth` uses local metadata only: Claude `auth status --json`,
Codex `login status`, and Pi `auth check --json --no-refresh` when a model is
specified. Pi never receives `--credentials`. Auth stderr is discarded, private
JSON fields are ignored, and only readiness is reported. Antigravity currently
has no verified local auth-status command and remains unverified. Local auth
metadata does not prove provider acceptance or access from an isolated home.

With explicit `auth.file`, only the selected native credential fields are
snapshotted. Source ownership, permissions, symlink components, and exposure
through any configured workspace/dependency/default runtime mount are checked.
When initialized state and its socket exist, readiness runs inside the actual
private-home mounts with networking disabled. Values and raw diagnostics never
enter the report. Read-only snapshots do not persist native token refreshes.

Configured resources receive a real harmless enforcement probe before model
tasks can start. Actual cgroup v2 CPU/memory/pid controls are verified, and
unavailable user-systemd delegation or requested process limits fail closed.
An explicit Codex external mode reports that the required outer Bubblewrap
boundary supplies isolation; no native failure selects that mode automatically.

Known standalone Codex 0.165.x package manifests also trigger code-only
companion checks, including code-mode-host, bundled Bubblewrap, and ripgrep.
Help can succeed while these execution dependencies are absent. Unknown
package layouts are reported as unverified rather than guessed.

Explicit model API bases receive a GET model-metadata probe only when their
destination is loopback/private. IP literals are checked directly; localhost
and private `.internal`, `.local`, or `.lan` DNS names must resolve exclusively
to private addresses, which are pinned during dialing. Public/unknown hosts are
not contacted. Proxy environment variables are not used, redirects are not
followed, response bodies are bounded, and no response body is displayed.
An exact Pi endpoint model must be advertised; Anthropic aliases remain an
honest warning when metadata cannot establish their mapping. Metadata success
does not prove tool use or inference quality.

Pi context/output/capability discovery is implemented, rather than deferred to
a model turn. It reads exact `/models` records and, for an Ollama connection,
can use metadata-only `POST /api/show` at the server root. Explicit
`model_metadata` fields override discovered fields, including explicit `false`.
Missing fields use conservative defaults. Pi contexts at or below 4096 tokens
are rejected because the supported runtime reserves that space before output
selection. Doctor reports its discovered selection without rewriting YAML;
setup persists the selected metadata in newly generated configuration. These
checks do not establish inference, tool or modality compatibility.

Workdir/read-only paths must already be resolved against the configuration
file location by the caller. Missing workspaces before initialization are
warnings. Existing declared dependencies must be accessible and contain no
sockets, devices, or pipes. Broad mounts/private coordination state are
rejected for isolated agents. Node CLIs need both a PATH-visible Node runtime
and the complete CLI package directory in declared read-only mounts when these
are outside the system runtime.

Bubblewrap is tested with an actual namespace probe. Once state, the agent
credential, and the broker socket exist, doctor also constructs the real mount
boundary and runs native CLI help inside it with networking disabled. Before
those resources exist, exact mount execution is explicitly deferred; Node
dependency closure is still checked statically. Native HTTP model harnesses
with `network: none` are reported as unrunnable. Trusted `local` execution is
always labeled as providing no isolation.
Allowlist configuration receives a warning explaining that help runs with
`network: none`; the worker validates per-turn destination DNS/proxy/socket
enforcement before execution. A namespace probe or successful help alone does
not prove a provider turn can use that proxy.

Preflight checks the selected interaction mode. Native interactive agents require
the root CLI role/context flags; managed workers require the headless turn flags.
Help-cache identity includes the interaction mode. A successful headless check
does not establish that an interactive adapter is ready, or vice versa.
Interactive `native_only` configuration requires an empty role and omits
Agentmux role/context injection at execution. It still uses native CLI
capability preflight and does not enable managed conversation, trace, disk,
autonomy or allowlist settings in an interactive process.
