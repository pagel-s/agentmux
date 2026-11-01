# Provider validation

These are measured integration results from 2026-10-31, using the installed
versions below. They establish the specific paths exercised, not a reliability
rate, model-quality ranking, or compatibility guarantee for future versions.
Setup and doctor perform nongenerative checks; model acceptance tests require
explicit opt-in. Private credentials, native transcripts, raw prompts and
machine identifiers are excluded from this summary.

This is the historical 31 October matrix. The later
[1 November provider acceptance](provider-acceptance.md) records fresh bounded
Claude/Codex/Antigravity/Pi checks, native PTY startups and installed-Codex custom
Responses endpoint conformance. It explicitly distinguishes genuine local
Transformers/Ollama inference from the earlier vLLM-specific evidence below.
Neither table is a guarantee for an untested account, tool/MCP or model version.

| Harness or service | Measured version | Structured protocol | Persistence evidence | Tool evidence and boundary |
| --- | --- | --- | --- | --- |
| Claude Code | 2.1.316 | Print mode, stream-JSON input/output; result/permission-denial inspection | Three confirmed turns in one private native conversation, across worker processes and a broker restart | Real coding and authenticated team coordination inside Bubblewrap; explicit `Read,Write,Edit,Bash` allowlist, `safe-mode`, `dontAsk`; independent owner verification |
| Codex CLI | 0.165.0 | `exec --json`; completed-turn and failure events | Two confirmed turns using exact `exec resume` references and private native history, including broker restart | Real code edits, development tests and scoped artifact publication inside Bubblewrap; owner verification in a separate credential-free sandbox |
| Pi | 1.4.0, maintained `@earendil-works/pi-coding-agent` | RPC `get_state`, `prompt`, assistant/tool events, `agent_settled` | Real two-process private-session tests against fake SSE, real Ollama and real Antigravity inference | Real sandbox-only tool execution against a controlled fake bridge response; real local-model mixed-team review was analysis-only |
| Ollama via Pi | 0.20.5; cached `qwen2.5:14b` | OpenAI-compatible `/v1/chat/completions`; metadata from `/api/show` | Passed: two separate Bubblewrap Pi processes recovered a private first-turn marker using the same session ID | Tools disabled for this engine acceptance; no claim of Ollama tool-calling validation |
| Antigravity native | 1.4.12; `gemini-3.8-flash-low` | Stream-JSON steps and terminal result; soft permission-denial inspection | Fresh trusted-local turn passed; private native resume/storage boundary remains unverified | All documented native tool namespaces denied; exact fixed reply, zero native tool steps |
| Antigravity bridge via Pi | Antigravity 1.4.12 + Pi 1.4.0; same Flash model | Authenticated local OpenAI chat/SSE compatibility bridge; native agy remains its inference backend | Passed: exactly two real Flash calls through separate Bubblewrap Pi processes, with exact private marker recovery | Real Flash persistence test had tools disabled. Separate real-Pi/fake-native test executed one validated `bash` call only inside the Pi sandbox; bridge never executes host tools |
| vLLM via Pi | vLLM 0.32.0; CPU PyTorch 2.13.0; cached `Qwen2.5-0.5B-Instruct` | Authenticated OpenAI `/v1/models` metadata and `/v1/chat/completions` SSE | Passed: two separate Bubblewrap Pi processes recovered the exact private marker using the same session ID | Tools disabled for this engine acceptance; no vLLM tool-calling validation claimed |

## What the developer workflows established

The initial bounded Claude and Codex coding checks used a sandboxed broker and
trusted local workers. Codex's result passed five unit tests and 1,000 independent
randomized checks. Claude's interval-merging result passed 11 unit tests and a
separate graph-component oracle over 3,642 valid cases and three invalid cases.
Those checks established working native coding access, not worker isolation.

The later [mixed-harness acceptance](mixed-harness-acceptance.md) placed the
broker, Claude, Codex and Pi in separate Bubblewrap sandboxes. It verified scoped
board/memory/instruction handoffs, hidden peer and owner credentials, denied
reverse authority, and a separately tested artifact. Each worker had enforced
2 GiB memory, one CPU and 256-task cgroup bounds, plus CPU-time/open-file limits.
Pi's cached local Qwen 1.5B reviewer received the artifact explicitly; its own
verdict did not determine verification.

The [five-turn autonomous acceptance](autonomous-workflows.md) then used one
initial owner goal, three persistent Claude turns, two persistent Codex turns
and two observation wakeups. A broker restart preserved a queued ping and its
cursor/version plus the confirmed native session reference. All five tasks were
owner-verified, both agents had no remaining work, and shutdown completed.
Uncertain or failed turns are not silently resubmitted or resumed.

## Authentication and isolation

For isolated Claude/Codex login reuse, the owner selects a supported private
credential file. A filtered per-turn snapshot is mounted read-only into a fresh
private HOME. The original source, peer credentials and provider history outside
the selected session generation are not mounted. A selected API-key environment
variable is another explicit choice; values are excluded from argv and generated
YAML. Read-only native OAuth refresh can fail and must be reported honestly.

Codex normally retains native `workspace-write`. The measured host rejected
nested user namespaces, so the isolated acceptance explicitly selected
`codex_sandbox: external`: native `danger-full-access` runs only inside the
required verified outer Bubblewrap boundary. There is no local-mode fallback or
approval-bypass flag. Known standalone Codex packages require their code-mode
host and code-only companion files; doctor checks their visibility before work.

Direct native Antigravity uses its existing host login and fresh trusted-local
mode. Its documented keyring authentication and workspace conversation lookup
do not establish an independently isolated native storage root. Agentmux does
not expose a host session bus or whole native home to claim otherwise. The
[bridge](antigravity-bridge.md) instead keeps that authentication in a separate
owner-controlled host inference service and gives sandboxed Pi its own tools
and persistent history. The host service is trusted and its native CLI may retain
the submitted prompts. Its endpoint requires a separate key and a numeric
loopback listener; it is not a publicly exposed service.

## Real local-engine checks

Ollama validation used an existing service and cached weights through a temporary
SSH loopback forward. Metadata discovery and exactly two tools-off generations
passed; each requested at most 64 output tokens. No service restart, setting
change or model download was needed. The observed server context remained
32,768 tokens. Pi metadata does not change an engine's runtime configuration.
The forward was closed afterward.

vLLM validation installed its official CPU wheel and dependencies into a new
private environment, without root privileges, driver changes or interference
with the existing Ollama service. Already cached public weights were copied
explicitly. The loopback API used an ephemeral private key, one sequence,
32,768-token context and four CPU cores. Cold startup took about 39 seconds and
the final startup about 31 seconds; observed
summed process-group RSS was about 7.06 GB. A raw bounded request completed in
0.43 seconds with three generated tokens and a normal stop. This was a smoke
measurement, not a throughput benchmark or an enforced 7.06 GB memory bound.

Initial Pi requests failed with a length result because the test incorrectly
declared a 4,096-token context. Offline real-Pi/fake-SSE reproduction proved that
Pi 1.4.0 subtracts a fixed 4,096-token safety reserve: a declared output allowance
of 256 became an actual one-token request. A larger output allowance alone did
not fix that context mismatch. This was a fixture defect, not evidence of model
inability. The adapter correctly refused the truncated turn.

After both the engine and Pi used the cached model's verified native 32,768-token
context, the real two-process private-session acceptance passed in about 3.06
seconds. Each actual streaming request selected at most 64 output tokens; the
two responses used 53 input/four output and 86 input/seven output tokens.
This finite fixture selected a minimal system prompt and zero-temperature
sampling in its private test proxy; it does not benchmark Pi's default developer
prompt or model instruction-following quality.
The exact private marker and native Pi session reference survived the process
boundary. Exactly two upstream requests ran, with tools disabled. Doctor now
rejects reported contexts of 4,096 or less for this Pi compatibility path rather
than enlarging the metadata or accepting one-token truncation as useful output.
The validation server/process group stopped, its temporary forward closed and
both copies of its ephemeral API key were removed.

The version and environment choices follow the official
[vLLM CPU installation guide](https://docs.vllm.ai/en/latest/getting_started/installation/cpu/).
Model metadata is advisory and uses exact IDs, conservative defaults and explicit
owner overrides; see [setup](managed-setup.md). Advertising a tool or image
capability is not evidence that a particular model successfully exercised it.

## Reproduction and limits

Use the linked acceptance scripts and opt-in tests with your own installed
harnesses and explicitly selected endpoints. Ordinary tests use controlled fake
protocols and make no paid provider calls. The real Ollama/vLLM tests are
`TestPiRealOllamaPrivatePersistentTwoTurns` and
`TestPiRealVLLMPrivatePersistentTwoTurns` in `internal/harness`; they admit at most
two upstream requests, with a default requested maximum of 64 output tokens per
request. An explicit local fixture override permits at most 256. The final
successful Ollama/vLLM acceptances both used 64. The no-model
`TestPiCLISmallContextOutputReserve` regression records actual outbound output
limits for 4,096- and 8,192-token context declarations.
The real Antigravity bridge test admits at most two native Flash turns.

Structured completion, process exit, task completion and owner verification
are distinct evidence. Stream events and optional private traces do not expose
every hidden provider context or internal inference step. The native research
pilot also identified a Codex commentary/final-answer aggregation confound;
its frozen scores remain unchanged and the parser regression is tracked in the
[research report](research-study.md).

Agentmux can enforce configured local process, admission, network and disk
boundaries. Provider-reported usage and owner-configured turn allowances are
observations/admission controls, not hard billing caps. A native turn can make
multiple model requests, providers may report incomplete usage, and aborting a
local process cannot guarantee that remote billing stops immediately.
