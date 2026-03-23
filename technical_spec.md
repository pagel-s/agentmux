Agentmux Technical Specification v1 Baseline
Purpose

Document status

This file freezes the original v1 contract that established Agentmux as a reliable config-driven tmux workspace launcher.

Current `main` has moved beyond this baseline with:

- exact grid layout support
- window-level environment overrides
- stable pane names
- named-pane runtime commands such as `status`, `focus`, `restart`, `rerun`, `stop`, `capture`, and `send`

Treat this file as the v1 baseline specification, not the full reference for current `main`. For current behavior, see `README.md`. For next steps, see `roadmap.md`.

Agentmux is a tmux-native CLI that reads a declarative config file and creates a reproducible tmux workspace for AI-heavy developers. The system treats agents as first-class entities, supports shareable configs, and keeps execution transparent.

1. Scope of v1
In scope
Load a local YAML config
Validate schema and references
Resolve params and environment variables
Create tmux session, windows, and panes
Run either raw commands or named agents in panes
Set pane titles and basic metadata
Provide a small CLI surface
Out of scope
Background daemon
Agent-to-agent messaging
Remote config registry
Plugin SDK
Rich state synchronization
GUI
2. Design Principles
Terminal-first
Declarative configuration
Explicit over magical
Provider-agnostic core
Shareable and reproducible configs
Safe defaults
Easy to inspect and debug
3. System Model

A config defines:

metadata
params
agents
layout
optional hooks

Core entities:

Project config: the full YAML document
Agent: a reusable execution definition
Window: a tmux window
Pane: a tmux pane containing either a command or an agent
Param: user-supplied or default template value
Hook: optional shell commands run before or after session creation
4. Config File Format
File type

YAML

File extension

Preferred:

.agentmux.yaml
.agentmux.yml
.yaml
.yml
Minimum example
version: 1
session: ai-dev

agents:
  coder:
    provider: command
    command: "ollama run codellama"

layout:
  - window: main
    panes:
      - agent: coder
5. Top-Level Schema
Top-level fields
version: 1
name: optional-human-readable-name
session: required-tmux-session-name
description: optional-description

params: {}
env: {}
agents: {}
layout: []
hooks: {}
Field definitions
version
Type: integer
Required: yes
Allowed values in v1: 1
name
Type: string
Required: no
Human-readable label
session
Type: string
Required: yes
tmux session name to create
description
Type: string
Required: no
params
Type: map
Required: no
Key-value defaults used for template substitution
env
Type: map
Required: no
Session-wide environment variables available to all panes unless overridden
agents
Type: map of agent-name → agent-definition
Required: no, unless panes reference agents
layout
Type: list of window definitions
Required: yes
Must contain at least one window
hooks
Type: object
Required: no
Optional pre/post commands
6. Parameter Model

Params allow configs to be reused across projects.

Syntax
params:
  project_dir: "."
  branch: "main"
Template interpolation

Supported in string fields via:

{{project_dir}}
{{branch}}
Resolution order
CLI param override
Config default in params
Validation error if missing
CLI form
agentmux load dev.yaml --param project_dir=~/code/app --param branch=feature-x
v1 rule

Only string substitution is supported. No expressions, conditionals, loops, or nested templating.

7. Environment Variable Model

Environment variables can exist at three levels:

session-level env
agent-level env
pane-level env

Precedence:

pane env
agent env
session env
Example
env:
  PROJECT_ROOT: "{{project_dir}}"

agents:
  reviewer:
    provider: command
    command: "my-review-agent"
    env:
      MODEL: "gpt-5"

layout:
  - window: review
    panes:
      - agent: reviewer
        env:
          MODEL: "gpt-5-mini"
External shell variable passthrough

A literal value like ${OPENAI_API_KEY} should be preserved and expanded by the shell at runtime, not by Agentmux.

8. Agent Definition Schema
v1 supported provider types

Agentmux core should normalize everything to shell execution. In v1, providers are metadata plus execution config.

Supported providers:

command
shell
ssh (basic wrapper)
openai-cli or other vendor-specific wrappers only if implemented as shell command templates

To keep v1 simple, the engine should require a final executable command string.

Agent schema
agents:
  agent_name:
    provider: command
    command: "ollama run codellama"
    cwd: "{{project_dir}}"
    env:
      FOO: bar
    title: "Coder"
    shell: "/bin/bash"
Fields
provider
Type: string
Required: yes
v1 allowed: command, shell, ssh
command
Type: string
Required: yes
Final shell command to run in pane
cwd
Type: string
Required: no
Working directory before command execution
env
Type: map
Required: no
title
Type: string
Required: no
Pane title override
shell
Type: string
Required: no
Shell used to invoke command
Default: user login shell or /bin/sh
Basic ssh provider convention
agents:
  prod-shell:
    provider: ssh
    command: "ssh user@prod.example.com"

In v1, ssh is informational unless you add helper behavior later.

9. Layout Schema

The layout is an ordered list of windows.

Window schema
layout:
  - window: dev
    root: "{{project_dir}}"
    layout: even-horizontal
    panes:
      - command: "nvim"
      - agent: coder
Window fields
window
Type: string
Required: yes
tmux window name
root
Type: string
Required: no
Default working directory for panes in this window
layout
Type: string
Required: no
tmux layout preset, such as:
even-horizontal
even-vertical
main-horizontal
main-vertical
tiled
panes
Type: list
Required: yes
Must contain at least one pane
10. Pane Schema

A pane must define exactly one execution source:

command
agent
Pane examples
Raw command pane
- command: "npm test --watch"
  title: "Tests"
Agent pane
- agent: reviewer
  title: "Review"
Full pane schema
- agent: coder
  command: optional-override-command
  title: "Coder"
  cwd: "{{project_dir}}"
  env:
    DEBUG: "1"
  focus: true
Fields
agent
Type: string
Required: yes if command absent
Must reference an existing agent
command
Type: string
Required: yes if agent absent
If both agent and command are present, command overrides the agent command in v1 only if explicitly allowed by implementation. Recommended: reject in v1 for clarity.
title
Type: string
Required: no
cwd
Type: string
Required: no
env
Type: map
Required: no
focus
Type: boolean
Required: no
Marks pane as selected after creation
Only one pane across the whole config should end as focused; if multiple are true, last created wins
v1 validation rule

A pane must specify exactly one of:

agent
command
11. Hook Schema

Hooks are optional shell commands.

hooks:
  pre_session: "echo preparing"
  post_session: "tmux display-message 'Agentmux ready'"
Fields
pre_session
Runs before tmux session creation
post_session
Runs after full session creation
v1 constraints
String command only
Runs on host shell
Non-zero exit behavior should be configurable later; in v1 recommended behavior:
pre_session failure aborts session creation
post_session failure warns but does not destroy session
12. Execution Semantics
Resolution pipeline
Read YAML
Validate schema shape
Validate semantic references
Resolve params
Resolve derived strings
Build execution plan
Execute hooks
Create tmux session
Create windows
Create panes
Apply pane titles/layout/focus
Run post hook
Working directory precedence
pane cwd
referenced agent cwd
window root
session launch directory
Environment precedence
pane env
agent env
session env
inherited process environment
Pane title precedence
pane title
agent title
agent name
command-derived fallback
13. Validation Rules
Structural validation
version must exist and equal 1
session must exist
layout must be a non-empty list
every window must have window and panes
every pane list must be non-empty
Semantic validation
every agent reference must exist
agent names must be unique
window names should be unique within session
pane must specify exactly one of agent or command
params referenced via {{name}} must exist after CLI overrides are merged
Safety validation
forbid empty command strings
reject invalid tmux identifiers if needed
optionally warn on duplicate pane titles
14. CLI Specification
Primary commands
agentmux load <config>

Loads config, validates, and creates tmux session.

Examples:

agentmux load dev.yaml
agentmux load dev.yaml --param project_dir=~/code/app
agentmux load dev.yaml --attach
agentmux load dev.yaml --no-attach
agentmux validate <config>

Validates schema and references without launching tmux.

agentmux validate dev.yaml
agentmux render <config>

Prints resolved config or execution plan.

agentmux render dev.yaml

Useful for debugging and sharing.

agentmux list

Lists known sessions or optionally lists config-discoverable entries later.

For v1, simplest meaning:

list tmux sessions created or visible
agentmux doctor

Optional but useful. Checks:

tmux installed
shell available
config parse health
15. CLI Flags
Common flags
--param key=value

Overrides config param

--attach

Attach to session after creation

--no-attach

Do not attach after creation

--dry-run

Print planned tmux operations without executing

--verbose

Show detailed execution steps

--strict

Treat warnings as errors

16. Error Model

Errors should be human-readable and actionable.

Categories
parse errors
schema errors
semantic reference errors
template resolution errors
tmux execution errors
hook execution errors
Example messages
error: pane 2 in window "review" references unknown agent "critic"
error: missing required param "project_dir"
hint: pass --param project_dir=/path/to/repo
Exit codes

Suggested:

0 success
1 general runtime error
2 validation error
3 config parse error
4 dependency error such as tmux missing
17. Discovery Rules

v1 can stay explicit, but if you support implicit discovery, use this order:

exact path passed by user
current directory:
agentmux.yaml
agentmux.yml
.agentmux.yaml
.agentmux.yml

Recommended: do not overdo discovery in v1.

18. tmux Integration Details
Required tmux capabilities
create session
create windows
split panes
send keys / run commands
rename windows
set pane titles where possible
select layout
select pane
Suggested command strategy

Generate explicit tmux commands in order rather than relying on fragile shell chaining.

Idempotency behavior

If session already exists:

default: fail with clear message
future flag: --replace or --attach-if-exists

Recommended v1:

fail unless user passes --replace
19. Logging and Observability
v1 logging levels
default: concise
verbose: show resolution and tmux actions
Helpful debug outputs
resolved params
final commands
cwd per pane
env keys used, but never print secret values unless explicitly requested
20. Security Considerations
Configs execute shell commands; treat them as trusted code
Do not automatically fetch remote configs in v1 without explicit user action
Do not print secrets in logs
Preserve shell env expansion rather than storing secrets in config
Consider a future trust model for remote configs
21. Canonical Example
version: 1
name: AI Dev Workspace
session: ai-dev
description: Multi-agent coding workspace

params:
  project_dir: "."

env:
  PROJECT_DIR: "{{project_dir}}"

agents:
  coder:
    provider: command
    command: "ollama run codellama"
    cwd: "{{project_dir}}"
    title: "Coder"

  reviewer:
    provider: command
    command: "openai api chat.completions.create -m gpt-5"
    cwd: "{{project_dir}}"
    env:
      OPENAI_API_KEY: "${OPENAI_API_KEY}"
    title: "Reviewer"

layout:
  - window: editor
    root: "{{project_dir}}"
    layout: even-horizontal
    panes:
      - command: "nvim"
        title: "Editor"
        focus: true
      - agent: coder

  - window: validation
    root: "{{project_dir}}"
    layout: even-vertical
    panes:
      - command: "npm test --watch"
        title: "Tests"
      - agent: reviewer

hooks:
  pre_session: "test -d {{project_dir}}"
  post_session: "tmux display-message 'Agentmux ready'"
22. Internal Data Model

A clean internal representation helps keep the implementation stable.

Resolved config model
ResolvedConfig
  version: int
  name: string?
  session: string
  description: string?
  params: map[string]string
  env: map[string]string
  agents: map[string]ResolvedAgent
  windows: []ResolvedWindow
  hooks: ResolvedHooks
ResolvedAgent
  name: string
  provider: string
  command: string
  cwd: string?
  env: map[string]string
  title: string?
  shell: string?
ResolvedWindow
  name: string
  root: string?
  layout: string?
  panes: []ResolvedPane
ResolvedPane
  sourceType: "agent" | "command"
  agentName: string?
  command: string
  cwd: string?
  env: map[string]string
  title: string?
  focus: bool
23. Implementation Notes
Recommended package boundaries
config
parse YAML
raw structs
validate
schema and semantic validation
template
param substitution
resolve
merge params, env, cwd, titles, pane commands
tmux
execution plan and tmux command generation
cli
Cobra or similar if using Go
hooks
pre/post hook execution
24. Testing Strategy
Unit tests
YAML parsing
template interpolation
env merging
cwd precedence
pane validation
unknown agent references
Integration tests
create actual tmux session in CI where possible
verify windows/panes count
verify window names
verify session already exists behavior
Fixture tests

Use sample configs:

minimal valid
missing param
bad agent reference
env overrides
multiple windows
focus handling
25. Forward Compatibility

Reserve room for future additions without breaking v1.

Possible future fields:

imports
providers
groups
artifacts
bindings
watch
remote
Compatibility rule
unknown top-level fields should either warn or fail in strict mode
config version must be explicit
26. v1 Deliverables
Spec deliverables
YAML schema definition
validation rules
CLI behavior contract
example configs
Implementation deliverables
load
validate
render
local file support
tmux session creation
docs with 3 to 5 canonical templates
27. Definition of Done for v1

v1 is complete when:

a user can define a multi-window, multi-agent workspace in one YAML file
the config can be validated before execution
params can be overridden from CLI
environment variables and working directories resolve predictably
tmux session launches reliably
the config is readable and shareable
errors are actionable
28. Recommended v1 Non-Goals to Protect Scope

Do not add yet:

dynamic orchestration logic
branching workflow definitions
provider-specific API abstractions inside core
distributed execution
shared memory between agents
automatic remote download and execution

Those can wait until the config format and user behavior are proven.m
