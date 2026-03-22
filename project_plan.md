🚀 Agentmux — Project Plan
🧠 Vision

Agentmux is a tmux-native tool that enables developers to define, share, and reproduce multi-agent terminal workflows using simple, declarative configuration.

“Run your entire AI workflow in tmux—multiple agents, one command.”

🎯 Target Audience
Primary: AI-heavy terminal developers

Inspired by workflows of people like Andrej Karpathy

Characteristics:

Heavy tmux users
Use multiple AI agents simultaneously (local + cloud)
Comfortable with CLI + YAML
Optimize for speed and control

Current pain points:

Manual pane/session setup
Repetitive workflows
No reproducibility
Fragmented multi-agent usage
🔥 Problem Statement

Managing multiple AI agents across terminal sessions is:

repetitive
error-prone
non-reproducile
hard to share
💡 Solution Overview

Agentmux introduces:

Agents as first-class entities
Declarative session configs
One-command reproducibility
Shareable workflows
agentmux load feature-dev.yaml
🧩 Core Concepts
1. Agent

A named execution unit with provider + configuration.

agents:
  coder:
    provider: local
    command: "ollama run codellama"

  reviewer:
    provider: openai
    model: gpt-4o
2. Session

A full tmux workspace.

session: feature-x
3. Layout

Defines windows and panes.

layout:
  - window: dev
    panes:
      - agent: coder
      - agent: reviewer
4. Command vs Agent
command: → raw shell execution
agent: → structured, extensible abstraction
⚙️ Core Principles
Terminal-first (no GUI dependency)
Declarative configuration
Agents as first-class citizens
Composable and modular
Provider-agnostic
Reproducible workflows
Minimal magic, maximum transparency
📦 Config Design (Critical Feature)
Goals:
Portable
Shareable
Parameterizable
Versioned
Example Config
version: 1
session: ai-dev

params:
  project_dir: .

agents:
  coder:
    provider: local
    command: "ollama run codellama"

  reviewer:
    provider: openai
    model: gpt-4o
    env:
      OPENAI_API_KEY: ${OPENAI_API_KEY}

layout:
  - window: editor
    panes:
      - command: "cd {{project_dir}} && nvim"
      - agent: coder

  - window: review
    panes:
      - agent: reviewer
      - command: "npm test"
Parameter Injection
agentmux load ai-dev.yaml --project_dir ~/myapp
Config Composition (Future)
imports:
  - base.yaml
  - agents.yaml
🌍 Sharing & Distribution (Key Differentiator)
Phase 1: Local configs
agentmux load ./session.yaml
Phase 2: Remote configs
agentmux load https://...
agentmux load github:user/repo/session.yaml
Phase 3: Named configs
agentmux load karpathy/dev
agentmux load startup/solo-founder
Long-term Vision: Config Ecosystem
Public repositories of configs
Reusable workflows
Community-driven patterns

👉 Agentmux becomes:
“The runtime for shared AI workflows”

⚙️ MVP Scope
✅ Must Have
1. YAML Config Loader
Parse and validate configs
Support params and env variables
2. tmux Session Generator
Create sessions, windows, panes
Assign commands/agents
3. Agent Abstraction (Basic)
Local commands
Simple API-based agents
Shell fallback
4. CLI Interface
agentmux load <config>
agentmux list
agentmux validate
5. Pane Metadata
Pane titles = agent names
Basic labeling for clarity
❌ Not in MVP
Agent-to-agent communication
Plugin system
GUI dashboards
Complex orchestration
State synchronization
🔄 V1.5 / V2 Features
🧠 Context Sharing
File-based or pipe-based communication
Shared workspace awareness
🔄 Live Reload
agentmux reload
🧪 Experiment Mode
Run same task across multiple agents
Compare outputs
🔌 Plugin System
Custom providers (OpenAI, Anthropic, local, SSH)
Extensible agent types
🧵 Agent Groups
agents:
  reviewers:
    - gpt4
    - claude
🧱 Architecture
Phase 1: Thin Wrapper (MVP)
CLI tool (Go recommended)
YAML → tmux commands
No persistent runtime

Pros:

Fast to build
Simple
Reliable
Phase 2: Agent Runtime (Future)
Background daemon
IPC between agents
Context sharing
🛠️ Tech Stack
Recommended: Go
Fast CLI performance
Single binary distribution
Strong ecosystem
Alternative: Python
Faster prototyping
Easier integrations
🧪 Example Workflow (North Star)
agentmux load bugfix.yaml

Creates:

Window: dev

Pane 1 → coding agent
Pane 2 → reasoning agent

Window: runtime

Pane 3 → logs
Pane 4 → tests
🏆 Differentiation
Tool	Limitation	Agentmux Advantage
tmuxifier	no agent concept	agents as primitives
raw tmux	manual setup	reproducible configs
IDE AI tools	single agent	multi-agent workflows
scripts	brittle	structured + shareable
📈 Adoption Strategy
Step 1: Target power users
AI developers
OSS contributors
terminal-first engineers
Step 2: Share configs publicly
GitHub repos
blog posts
demo workflows
Step 3: Build config ecosystem
curated examples
reusable templates
🔁 Viral Loop
User creates config
Shares config
Others run instantly
They modify and re-share
Ecosystem grows
🚨 Risks & Mitigations
Risk: Overengineering early

→ Keep MVP minimal

Risk: Too much abstraction

→ Keep configs explicit

Risk: Provider lock-in

→ Maintain provider-agnostic design

Risk: Complexity creep

→ Strict scope control

🧭 Success Criteria
Short-term
Can recreate complex workflows with one command
Users replace manual tmux setups
Mid-term
Users share configs publicly
Growing library of workflows
Long-term
Becomes standard for AI-native terminal workflows
Ecosystem of configs + extensions
