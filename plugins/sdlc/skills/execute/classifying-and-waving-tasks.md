# Classifying Tasks and Building Waves

Reference for the `execute` skill — Step 2 (CLASSIFY).

## Complexity Classification Heuristics

| Signal | Complexity |
|---|---|
| Task mentions "rename", "update config", "change value", "add import" | Trivial |
| Task body is < 3 sentences with a single clear action | Trivial |
| Task creates or modifies 1 file with a well-defined output at a single location | Trivial |
| Task edits multiple distinct locations in a single file (e.g., struct + interface + init + getter) | Standard |
| Task creates or modifies 2–4 files with clear outputs | Standard |
| Task implements a feature, writes tests, adds a component | Standard |
| Task involves > 5 files or cross-cutting concerns | Complex |
| Task requires understanding multiple existing modules to implement correctly | Complex |
| Task involves architectural patterns or systemic changes | Complex |

When signals are mixed, round up (prefer higher complexity class).

## Risk Classification Heuristics

| Signal | Risk Level |
|---|---|
| Task touches only test files | Low |
| Task touches only documentation or comments | Low |
| Task creates new internal-only modules with no public surface | Low |
| Task modifies a public API or function signatures called by other modules | Medium |
| Task changes database schemas, migrations, or data models | Medium |
| Task modifies authentication, authorization, or session handling | High |
| Task changes infrastructure (Docker, k8s, CI/CD, deploy scripts) | High |
| Task involves credential management, secrets, or encryption | High |
| Task deletes files or removes existing functionality | High |
| Task touches shared state accessible by multiple services | High |

When signals are mixed, round up (prefer higher risk level).

## Model Assignment

Model assignment derives from the complexity class. The three presets in Step 4 apply these mappings across all tasks.

| Complexity | Default Model | Rationale |
|---|---|---|
| Trivial | `haiku` | Fast, cheap; frees main context for orchestration. Single trivial → execute inline. Two or more trivials in the same phase → dispatch as one batch agent. |
| Standard | `sonnet` | Capable, cost-efficient |
| Complex | `opus` | Most capable; needed for architectural work |

### Override signals

Assign `opus` to a Standard task when:
- The task involves unfamiliar or poorly documented code
- The task requires nuanced judgment (choosing between multiple valid approaches)
- A prior `sonnet` attempt on a similar task in this project failed

Assign `sonnet` to a Complex task when:
- The task is complex only because it touches many files, but each individual change is mechanical
- The changes are fully specified with exact code to write (no design judgment needed)

### Model Presets

Always present 3 presets in Step 4, regardless of plan size:

| Preset | Trivial | Standard | Complex | Best when |
|---|---|---|---|---|
| **Speed** | haiku | haiku | sonnet | Plan is well-specified, changes are mechanical |
| **Balanced** | haiku | sonnet | opus | Default — matches complexity to capability |
| **Quality** | sonnet | opus | opus | Codebase is unfamiliar, tasks are ambiguous |

### Model Dispatch Enforcement

The `model:` parameter is REQUIRED on every Agent tool dispatch — no exception. Omitting it causes the agent to inherit opus from the parent context, defeating the preset system's cost optimization.

## Wave-Building Algorithm

Wave assignment is computed by the `execute_state` MCP tool's `wave-compute` action — it is not derived by hand. The action is stateless: it parses the plan file directly (no execution-state file access) and applies the dependency graph, same-file, wave-size-cap, risk-spreading, and pre-wave extraction rules that used to be spelled out step-by-step here.

```
execute_state({ action: "wave-compute", planPath: "<PLAN_FILE>", extraDepsJson: "<json>" })
→ { route, preWave, waves: [ { number, tasks: [...], expectedFiles: [...], verificationHint } ] }
```

- **`route`** — `"direct"` when the plan qualifies for small-plan direct execution (≤ 3 tasks, all Trivial/Standard, no High risk); `"waves"` otherwise. Step 2b (ROUTE) in `execute/SKILL.md` consumes this directly rather than re-deriving the condition.
- **`preWave`** — trivial, dependency-free tasks that have downstream dependents, extracted to run before Wave 1. Execute inline if there is 1; dispatch as a single batch agent if there are 2+ (see Worker dispatch prompt below).
- **`waves[].tasks`** — the wave's task list, already ordered (critical-path length descending, then task number ascending).
- **`waves[].expectedFiles`** — the deterministic union of every task's declared `Files:` paths in the wave. Feed this straight into Step 5c-bis's expectedFiles cross-check — no manual union computation needed.
- **`waves[].verificationHint`** — set only when every task in the wave shares the identical `Verify:` value (scope hint included); omitted otherwise.

**`extraDepsJson`** merges implicit dependencies into the graph alongside each task's explicit `Depends on:` field. It is a JSON array of `{ "task": <number>, "dependsOn": <number>, "reason": "<why>" }` entries — Common Dependency Patterns (below) is the guide for spotting these before calling wave-compute. Pass `"[]"` (or omit the field) when there are none.

The tool mechanically guarantees what Step 3 (CRITIQUE) used to check by hand:
- **Dependency integrity** — topological ordering from `Depends on:` plus `extraDepsJson`; a cycle or unknown reference fails the call outright.
- **Same-file constraint** — two tasks touching the same file are never placed in the same wave.
- **Risk spreading** — at most one High-risk task per wave.
- **Adaptive wave size cap** — see below.
- **Pre-wave trivial aggregation** — trivial, dependency-free tasks with downstream dependents land in `preWave` automatically.

What the tool does **not** decide — still requires LLM judgment at dispatch time, and stays in Step 3 (CRITIQUE):
- **In-wave trivial batching:** within a `waves[].tasks` list, if 2+ tasks are Trivial, dispatch them together as a single batch agent (see Worker dispatch prompt below); a single Trivial task in a wave still executes inline. `wave-compute` groups tasks into waves but does not decide dispatch batching.
- **Context sufficiency:** whether each task's fact sheet carries enough upstream context to execute without re-deriving information.

### Scoped Verification

A task's `Verify:` field may carry a scope hint in parentheses — `Verify: tests (go test
./internal/tools/ -run TestFoo)` — as defined in `plan-format-reference.md`'s `## Verify Field —
Scoped Hints`. It changes what the *task's own agent* runs; it never changes what gates the wave:

- **Scope hint present:** the task's dispatched Agent runs the scoped command instead of the full
  suite as part of its own verification. This is what lets multiple tasks in the same wave that
  touch the same package run in parallel without every agent re-running — and contending over — the
  full suite.
- **Scope hint absent:** the task's Agent falls back to running the full suite, same as today.
- **Post-wave gate always runs the full suite regardless:** Step 5c's "Verification suite" check in
  `execute/SKILL.md` runs the plan's verification command(s) once, after every task in the wave
  reports done. It always runs the full suite — never a scoped command — no matter which (if any)
  tasks in the wave used a scope hint mid-wave. A scope hint narrows an individual agent's own
  verification; it never narrows the wave's gate.

`verificationHint` (returned per-wave by `wave-compute`, above) is a separate, narrower signal — it is only set when every task in
the wave shares the identical `Verify:` value (scope hint included) — and exists to describe the
wave for reporting/tooling. It does not change the post-wave gate's behavior of always running the
full suite.

## Adaptive Wave Size Cap

Complex tasks count as 2 toward the cap (they consume more context and are more likely to conflict). `wave-compute` enforces this automatically for the initial wave schedule via `budget.StaticCap` (`internal/budget/budget.go`) — the table below is documentation of what it does, not a manual step:

| Total remaining tasks | Cap |
|---|---|
| 1–3 | No cap (dispatch all) |
| 4–8 | 4 |
| 9–15 | 5 |
| 16+ | 6 |

If a wave exceeds the cap, `wave-compute` splits the excess into a later wave — this is already reflected in the `waves[]` array it returns; there is nothing further to compute here.

A separate, byte-budget-based accounting (R-BYTE-BUDGET, #432) is used only for **mid-run recovery**: `execute_state({action:"wave-split"})` recomputes a tighter budget for a sub-wave after a CONTEXT_OVERFLOW failure (see [recovering-from-failures.md](recovering-from-failures.md)). That accounting looks at actual template scaffolding, guardrails-block, fact-sheet, and prior-wave-context byte sizes — it is not used for the initial wave-compute pass, which relies solely on the static table above.

On resource-constrained systems or when tasks share mutable state (databases, caches, singletons), request a tighter 2–3 cap by pre-splitting the plan or via `wave-split`, rather than expecting `wave-compute` to know about runtime resource constraints it has no visibility into.

## Worker dispatch prompt

The wave-runner middle-agent is retired (KD15). The main session dispatches every per-task and
batch Agent directly, and the dispatch prompt itself carries no protocol prose — everything a
worker needs (fact sheet, guardrails, verify guidance, report-back instructions, prior-wave
context) comes back from a single `task-context` call the worker makes itself.

Use this exact two-line prompt for every dispatch — single-task or one task from a batch cluster:

```
You are the implementation worker for task {taskId} of run {runId}.
Call execute_state {action:"task-context", taskId:"{taskId}"} and follow it.
```

Fill `{taskId}` and `{runId}` from the wave's task list and the `runId` returned by `wave-start`.
Nothing else is inlined — no fact-sheet content, no guardrails block, no completion-checklist
template, no heartbeat instructions. All of that lives server-side and comes back from the
`task-context` call:

| `task-context` output field | Replaces |
|---|---|
| `factSheet` | The old inlined "Your Task" / "Files You May Touch" / "Context From Prior Waves" sections — the fact sheet already embeds the plan task's Contract, Acceptance Criteria, and Files list. |
| `priorWaves` | The old "Upstream Surfaces" fact-sheet snapshot, refreshed live at dispatch time (catches sibling tasks in the same wave that finished since `wave-start`). |
| `verify` | The old "VERIFY:" canary instructions. |
| `reportBack` | The old "Progress Reporting" heartbeat instructions and completion-checklist format — including the explicit reminder that the worker does NOT call `task-done`/`task-fail` itself; it reports its status block back to the main session, which records it. |

The response is capped at 1 MiB; if `truncated: true` comes back, the worker proceeds with what
it has rather than treating the call as an error.

**Batch dispatch (2+ Trivial tasks in one Agent):** send one prompt per task in the cluster,
concatenated in order, each following the same two-line form with its own `{taskId}`. The Agent
works through them sequentially, calling `task-context` fresh for each before starting it.

**Model, mode, and background dispatch mechanics are unchanged** — see `execute/SKILL.md`'s
`## Wave loop` section: `model:` is required per task (haiku/sonnet/opus by complexity tier),
`mode: "bypassPermissions"`, `run_in_background: true`, every task/batch of a wave fanned out in
one message.

## Common Dependency Patterns

These implicit dependencies are easy to miss:

| Scenario | Dependency |
|---|---|
| Task A creates a new module; Task B adds it to an index/barrel file | B depends on A |
| Task A defines a TypeScript type; Task B uses that type | B depends on A |
| Task A creates a database table; Task B seeds or queries it | B depends on A |
| Task A adds a config key; Task B reads that config key | B depends on A |
| Task A adds a route handler; Task B adds middleware that wraps all routes | Depends on order — check the framework's middleware registration semantics |
| Task A writes a test fixture; Task B writes tests that use that fixture | B depends on A |
