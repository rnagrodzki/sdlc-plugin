# PR Labels Sub-Flow

Configure how `/pr` chooses labels for a project. Writes the
`pr.labels` block in `.sdlc-v2/config.json`. Three modes are supported:

- `off` (default) — no automatic labels; only forced labels via `--label` apply
- `rules` — deterministic evaluation of user-defined `{ label, when }` rules
- `llm` — legacy fuzzy matching by the model (opt-in only)

This sub-flow is invoked by `setup` via the `delegatedTo: 'setup-pr-labels'`
section descriptor (`pr-labels` row, `internal/setupmeta/sections.go`).

> **Port Notes** (Task 44 KD9 rewrite): config reads use the Read tool
> directly against `.sdlc-v2/config.json` (no `readSection` MCP tool is
> exposed); writes use `setup_write_sections`, which replaces a top-level
> config key wholesale — since `pr.labels` is a nested sub-key sharing the
> `pr` object with `titlePattern`/`allowedTypes`/etc., Step 5 below always
> reads the current `pr` section first and spreads it before writing (see
> Gotcha 1). There is no Go schema validator for `pr.labels`'s nested shape
> (`config.WriteSection` only checks top-level key names) — this sub-flow's
> own collection-time checks (Step 4) are the only validation; the Quality
> Gates section reflects this.

---

## Scan Input

This sub-flow loads everything it needs at runtime — no scan input from the
parent is required. It calls `gh label list` itself and reads the existing
`pr.labels` block (if any) from `.sdlc-v2/config.json`.

---

## Arguments

None.

---

## Workflow

### Step 1 — Prerequisite: Repo labels

Run:

```bash
gh label list --json name,description --limit 100
```

- **Exit 0:** parse the JSON array into `repoLabels = [{ name, description }, ...]`.
- **Auth or remote failure (any non-zero exit):** print:

  > `gh label list` failed. The pr-labels sub-flow needs an authenticated `gh`
  > and a GitHub remote. Run `gh auth login` (or `gh auth status` to check) and
  > re-run `setup --only pr-labels`. No changes were written.

  Exit cleanly without writing anything to `.sdlc-v2/config.json`.

If `repoLabels` is empty (repo has no custom labels yet), warn the user and
continue — `off` is still a valid choice; `rules` will require creating labels
in GitHub first; `llm` will produce no suggestions.

### Step 2 — Idempotency check

Read `.sdlc-v2/config.json` (Read tool). If the `pr.labels` key exists, present
the current state and ask:

```
Current pr.labels:
  mode:  <off|rules|llm>
  rules: N entries (when applicable)
```

Use AskUserQuestion:

> `pr.labels` is already configured. What do you want to do?

Options (only show options that make sense for the current mode):

- **keep** — exit without changes
- **replace** — wipe the current block and start fresh (Step 3)
- **append** — only when current `mode = 'rules'`: add rules to the existing
  list (skip Step 3 mode prompt; jump to the rules loop in Step 4 with the
  existing rules pre-loaded)

If `pr.labels` is absent, skip this step and go to Step 3.

### Step 3 — Mode selection

Use AskUserQuestion:

> How should `/pr` choose labels?

Options:

- **off** — never auto-add labels (default; `--label` CLI overrides still work)
- **rules** — apply deterministic rules I define below
- **llm** — let the model decide using fuzzy matching against repo labels
- **cancel** — abort without writing

Branch on the choice:

- `off` → Step 5 with `{ mode: 'off' }` (no rules)
- `llm` → Step 5 with `{ mode: 'llm' }` (no rules)
- `rules` → Step 4
- `cancel` → exit cleanly, no write

### Step 4 — Rules loop (mode = `rules` only)

Maintain an in-memory `rules: []` array. If `append` was selected in Step 2,
seed it with the existing `pr.labels.rules`.

Iterate:

1. **Add rule?** Use AskUserQuestion:

   > Add a label rule? (current count: <N>)

   Options:
   - **add** — define another rule (continue to step 4.2)
   - **review** — show the current rule list and stay in the loop
   - **done** — write `{ mode: 'rules', rules: [...] }` and exit (Step 5)
   - **cancel** — abort without writing

   On `review`: print the current `rules` array in human-readable form
   (`label → when.<signal>: [values]`) then re-ask.

2. **Pick the target label.** Use AskUserQuestion with options drawn from
   `repoLabels` (alphabetized). When `repoLabels.length > 10`, paginate the
   options and add a **search** option that takes a substring filter and
   re-presents the list. Reject any free-text label that is not in
   `repoLabels[].name` — the user must pick from the list.

3. **Pick the signal type.** Use AskUserQuestion:

   > Which signal triggers this rule?

   Options:
   - **branchPrefix** — match if the current branch starts with one of these prefixes (e.g. `fix/`, `feat/`)
   - **commitType** — match if any commit subject begins with `<type>:` or `<type>(scope):`
   - **pathGlob** — match if every changed file matches one of these globs (e.g. `**/*.md`)
   - **jiraType** — match if `jiraTicket.type` is in the list (e.g. `Bug`, `Story`)
   - **diffSizeUnder** — match if total lines changed is below this threshold

4. **Enter the value(s).** Use AskUserQuestion (free text):

   - For `branchPrefix`, `commitType`, `pathGlob`, `jiraType`:
     prompt for a comma-separated list. Trim whitespace, drop empties, dedupe.
     Reject empty input — at least one value is required.
   - For `diffSizeUnder`: prompt for a single positive integer. Reject
     non-integer or zero/negative input and re-ask.

5. **Append and confirm.** Build the rule object:

   ```js
   { label: <chosen>, when: { <signalKey>: <values> } }
   ```

   Append to `rules`, then loop back to step 4.1.

### Step 5 — Write

Build the final block:

- `off` → `{ mode: 'off' }`
- `llm` → `{ mode: 'llm' }`
- `rules` → `{ mode: 'rules', rules: [...] }`

Read `.sdlc-v2/config.json` (Read tool) to get the current `pr` section (empty
object if absent). Merge the labels block in **without clobbering**
`titlePattern`, `allowedTypes`, or any other sibling key, then write the full
`pr` section back:

```
setup_write_sections({
  sectionsJson: JSON.stringify({
    pr: { ...currentPrSection, labels: <BLOCK> }
  })
}) → { ok, written, errors }
```

`<BLOCK>` is the object built above. `...currentPrSection` MUST come from the
Read in this step, not from any earlier cached copy — the `pr` section may
have changed since Step 1.

### Step 6 — Confirm

Print a one-line summary:

```
Wrote pr.labels: mode=<mode>[, rules=<N>] to .sdlc-v2/config.json
This block is consumed by /pr Step 2b (Infer Labels).
```

---

## Quality Gates

Before marking complete, verify:

- The mode chosen is exactly one of `off`, `rules`, or `llm`
- When `mode = 'rules'`, every rule has exactly one signal key in `when` and at
  least one value
- Every rule's `label` exists in the scanned `repoLabels`
- No partial writes occurred when the user cancelled or `gh` failed
- No sibling `pr.*` key was lost (spot-check by reading `.sdlc-v2/config.json`
  after the write and confirming `titlePattern`/`allowedTypes`/etc. survived,
  if they were present before)

---

## Error Recovery

> **Flow**: detect → diagnose → auto-recover (retry once if transient) → invoke `error-report` for persistent actionable failures.

| Error | Recovery | Invoke error-report? |
|-------|----------|---------------------------|
| `gh label list` fails (auth/remote) | Print actionable hint, exit cleanly with no write | No — actionable by user |
| `repoLabels` is empty | Warn, allow `off`/`llm`, gate `rules` behind "create labels first" message | No |
| User picks `cancel` at any step | Exit cleanly, do not write partial state | No |
| `setup_write_sections` call errors | Show the error, do not retry — preserve any prior state | Yes |

When invoking `error-report`, provide:
- **Skill**: setup (pr-labels sub-flow)
- **Step**: Step 5 — Write
- **Operation**: `setup_write_sections({ sectionsJson: JSON.stringify({ pr: ... }) })`
- **Error**: full tool error message
- **Suggested investigation**: file permissions on `.sdlc-v2/config.json`; plugin install integrity

---

## Gotchas

1. **Never clobber sibling `pr.*` keys.**
   *Symptom:* `pr.titlePattern` (or any other `pr.*` key) is wiped after the
   sub-flow runs.
   *Root cause:* `setup_write_sections` replaces the entire `pr` section
   wholesale (`config.WriteSection` semantics) — it does not merge.
   *Mitigation:* Always read the current `pr` section (Step 5), spread it,
   and only override the `labels` key.

2. **Empty `repoLabels` looks like a `gh` failure but isn't.**
   *Symptom:* User sees no labels to pick from in `rules` mode and assumes
   their `gh auth` is broken.
   *Root cause:* Brand-new repos may have zero custom labels (only the GitHub
   defaults that some orgs strip).
   *Mitigation:* Distinguish "exit non-zero" (auth/remote) from "exit 0 with
   `[]`" (no labels). The latter is a content state, not an error.

3. **`pathGlob` semantics are stricter than expected.**
   *Symptom:* User adds rule `documentation` when `pathGlob: ["**/*.md"]` and
   later notices the label doesn't get applied to a PR that touched `*.md` and
   one `package.json` file.
   *Root cause:* The evaluator uses **all-changed-files** semantics — every
   changed file must match. This is the same posture as the legacy `*.md`
   inference rule.
   *Mitigation:* Note in the value-entry prompt that `pathGlob` means "every
   changed file matches one of these globs". For "any matches" semantics,
   point users at `commitType` instead.

4. **Append mode and replace mode look similar.**
   *Symptom:* User picks `replace` thinking they will edit the existing rules,
   but the new rule list overwrites everything.
   *Root cause:* Both options exit through the same Step 4 loop; only
   `append` seeds the in-memory list with existing rules.
   *Mitigation:* In Step 2, show the current rule count before asking, and on
   `replace` print "Existing rules will be discarded" before entering Step 3.

5. **`gh label list --limit 100` may truncate.**
   *Symptom:* Repo has more than 100 labels; some labels never appear in the
   pick list.
   *Root cause:* The `--limit 100` cap is hard-coded.
   *Mitigation:* Document the cap in the prompt; if pagination is hit, suggest
   the user create labels with shorter names or use `llm` mode.

---

## DO NOT

- Do NOT write `.sdlc-v2/config.json` on any prompt where the user picks `cancel`.
- Do NOT replace the entire `pr` section without first reading and spreading it — only set/replace the `labels` key.
- Do NOT accept a free-text label that isn't in `repoLabels` — the rule will be
  stripped by `/pr`'s label evaluator later, leaving the user with a silent dead rule.
- Do NOT proceed to the rules loop if `gh label list` failed — `rules` mode
  without a known label set produces unverifiable rules.
- Do NOT prompt for `mode` again when the user picked `append` in Step 2 —
  append implies `rules` mode.

---

## See Also

- `setup --only pr-labels` — parent skill entrypoint
- `setup/setup-pr-template.md` — sibling sub-flow (PR template authoring)
- `setup/setup-guardrails.md` — sibling sub-flow (plan guardrails)
- [`/pr`](../pr/SKILL.md) — consumer; reads `pr.labels` to dispatch the label evaluator
- `schemas/sdlc-config.schema.json#$defs/prLabelsSection` — schema reference (not enforced by an automated Go validator for this section; see Port Notes)
