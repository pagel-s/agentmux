# Workflow examples

These owner-configured plans exercise the `amx flow` commands. `compare.json`,
`mission.json`, `shadow.json` and `checks.json` use only Git, Python and local
command processes. Their scripted output demonstrates orchestration; it is not
evidence of model intelligence or coding quality.

For one coding request, start with [`amx work`](../../docs/working.md); it
previews a coding, fresh-review and acceptance sequence before model opt-in.
The JSON plans here expose the advanced [`amx flow`](../../docs/workflows.md)
controls. Their `execution: local` setting retains trusted host authority unless
full workflow isolation is explicitly configured. A worktree alone is not a
sandbox.

Use the separate temporary Git repository in the [workflow guide](../../docs/workflows.md)
and keep outputs outside the selected repository:

```sh
amx flow run examples/workflows/compare.json --repo /tmp/amx-flow-project --output /tmp/amx-comparison
amx flow mission examples/workflows/mission.json --repo /tmp/amx-flow-project --output /tmp/amx-mission
amx flow watch examples/workflows/shadow.json --repo /tmp/amx-flow-project --output /tmp/amx-watch --interval 2s --max-runs 2 --duration 30s
amx flow lab examples/workflows/compare.json --repo /tmp/amx-flow-project --output /tmp/amx-lab --repetitions 3
```

Each output directory must be new. Replace paths with fresh names if a previous
run is retained. `antigravity-codex.json` is an editable template: configure its goal
and check first, then explicitly allow provider calls with `--allow-models`.
The template deliberately fails its placeholder check until it is configured.

`native-compare.json` supplies a complete small coding task for Antigravity and Codex:
implement a tag normalization function. Its external Python assertions exercise
normalization, invalid elements, generators, fresh results and input preservation.
It requires installed authenticated native CLIs and makes provider calls only
when explicitly enabled:

```sh
amx flow run examples/workflows/native-compare.json --repo /tmp/amx-flow-project --output /tmp/amx-native-comparison --allow-models
```

No real provider execution or outcome is claimed by the command-only fixtures.
