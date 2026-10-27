// Package skillcheck cross-checks the harden skill
// (plugins/sdlc/skills/harden/SKILL.md) against the live MCP registry and
// against itself: every tool and action it names must exist, and the flag
// list in its frontmatter description must agree with the flag list in its
// Step 0 body. Other skills dispatch harden with flags (received-review passes
// --auto), so a flag that only one of these places mentions is a dispatch that
// fails at runtime.
//
// The shared matching and registry helpers live in skillcheck_flags_test.go.
// Every unexported identifier below is prefixed hardenSkills, and the one
// declared Test function is TestHardenSkillParity.
package skillcheck

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// hardenSkillsFlagSentenceRe finds the two Step 0 sentences that list flags
// as a run of inline code spans: "Required flag: `--skill` ..." and
// "Optional: `--step`, `--operation`, ...".
var hardenSkillsFlagSentenceRe = regexp.MustCompile(`\b(?:Required flag|Optional):`)

// hardenSkillsProseFlags returns the flags in the code-span runs that follow
// "Required flag:" and "Optional:" in the SKILL.md body.
func hardenSkillsProseFlags(body string) map[string]bool {
	set := map[string]bool{}
	for _, m := range hardenSkillsFlagSentenceRe.FindAllStringIndex(body, -1) {
		for _, sp := range flagParitySpanChain(body, m[1]) {
			for f := range flagParityFlagSet(body[sp[0]:sp[1]]) {
				set[f] = true
			}
		}
	}
	return set
}

// hardenSkillsHaikuPinRe matches a model pin that names haiku, in the Agent
// call form (model: "haiku") and in the bullet form (`model`: `haiku`).
var hardenSkillsHaikuPinRe = regexp.MustCompile(`model\W{1,6}haiku`)

// TestHardenSkillParity checks the harden skill against the MCP registry,
// against its own flag lists and against the orchestrator agent file it
// dispatches.
func TestHardenSkillParity(t *testing.T) {
	t.Run("tools_and_actions_resolve", func(t *testing.T) {
		flagParityAssertToolsResolve(t, "harden",
			"prepare_orchestrator",
			"learnings_log:read", "learnings_log:append", "learnings_log:remove",
			"validate:guardrails", "validate:dimensions",
			"dimensions_render_instructions",
			"setup_write_sections",
			"ship_state:deferred_add",
		)
	})

	t.Run("flag_surface_agrees", func(t *testing.T) {
		content := executeSkillsReadFile(t, filepath.Join(executeSkillsRepoRoot(t), "skills", "harden", "SKILL.md"))
		body := flagParityBody(content)

		// The description names every flag harden accepts. Step 0 names the
		// same flags in a table and in two sentences. Both lists must match.
		description := flagParityFlagSet(flagParityFrontmatterField(content, "description"))
		step0 := flagParityTableFlags(body)
		for f := range hardenSkillsProseFlags(body) {
			step0[f] = true
		}
		if len(step0) == 0 {
			t.Fatal("found no flags in the Step 0 table or the Required flag / Optional sentences, so the flag-list matching no longer fits this skill's format")
		}
		flagParityAssertSameSet(t, "harden description flags (got) vs Step 0 flags (want)", description, step0)

		// argument-hint is the short form of the same list.
		flagParityAssertSubset(t, "harden argument-hint", flagParityHintFlags(content), description)

		// --auto is the flag other skills dispatch to harden (issue #51). The
		// flag lint reads the hint and the table, so both must carry it.
		if !flagParityHintFlags(content)["--auto"] {
			t.Error("harden argument-hint does not list --auto, which received-review dispatches to it")
		}
		if !flagParityTableFlags(body)["--auto"] {
			t.Error("harden Step 0 flag table has no --auto row, which received-review dispatches to it")
		}
	})

	// The agent file is the contract for the proposal JSON that the harden
	// skill dispatches the orchestrator for. The agent file must keep the model
	// pin, the full action set, the skipped.drafts list, the failureClass
	// field of the proposal shape and the oldText rule of a merge. The harden skill must pin the same model in
	// both of its Agent dispatches.
	t.Run("orchestrator_contract", func(t *testing.T) {
		agent := executeSkillsReadFile(t, filepath.Join(executeSkillsRepoRoot(t), "agents", "harden-orchestrator.md"))
		for _, want := range []string{
			"model: sonnet",
			"repair | remove | merge",
			`"drafts"`,
			// The proposal-shape row. The bare field name occurs in several
			// other places, so it cannot pin the shape.
			`"failureClass": "one sentence`,
			// The skill compares the oldText of a merge with the removed
			// rule, so the agent must draft it from that rule.
			"For a `merge`, it is the full old text of the rule in `removeIds`",
		} {
			if !strings.Contains(agent, want) {
				t.Errorf("harden-orchestrator.md lacks %q", want)
			}
		}

		skill := executeSkillsReadFile(t, filepath.Join(executeSkillsRepoRoot(t), "skills", "harden", "SKILL.md"))
		for _, want := range []string{
			// The Agent({...}) call in the triage loop.
			`model: "sonnet",`,
			// The model bullet of the single-failure dispatch.
			"- `model`: `sonnet`",
		} {
			if !strings.Contains(skill, want) {
				t.Errorf("harden/SKILL.md lacks the orchestrator model pin %q", want)
			}
		}
		if m := hardenSkillsHaikuPinRe.FindString(skill); m != "" {
			t.Errorf("harden/SKILL.md still pins the orchestrator to haiku: %q", m)
		}
	})

	// The harden skill repairs pre-flight findings, deletes rules with a JSON
	// null in setup_write_sections, and under --auto stores the edits that
	// need a person as one deferred item for each section. The deferred
	// triage skill replays such an item with the failure text
	// "Pending rule edits: <id>". This subtest reads SKILL.md only. Each pin
	// is a full line or a phrase that occurs once in SKILL.md, and the subtest
	// asserts that count, so the deletion of one site cannot pass because a
	// copy of the same text stays elsewhere.
	t.Run("repair_remove_pending_contract", func(t *testing.T) {
		skill := executeSkillsReadFile(t, filepath.Join(executeSkillsRepoRoot(t), "skills", "harden", "SKILL.md"))
		for _, want := range []string{
			// Step 0 and Step 5: the replay command of a pending item.
			"`/sdlc:harden --failure-text \"Pending rule edits: <id>\" --skill deferred`.",
			"`/sdlc:harden --failure-text \"Pending rule edits: <id>\" --skill deferred`,",
			// Step 1: the pre-flight print line. Step 2: the line of the
			// plugin-defect hint path, where no orchestrator drafts repairs.
			"Pre-flight findings: <preflightFindingCount> (repair proposals come first)",
			"Pre-flight findings not repaired: the plugin-defect hint skips the orchestrator.",
			// Step 3: the foreground dispatch needs no TaskStop.
			"agent ends with its result, so no `TaskStop` is needed.",
			// Step 4: the four ordered routes.
			"| (a) | `RESULT.proposals` holds at least one `repair` proposal | Step 5. For `plugin-defect`, Step 5 holds only the repairs, and Step 6 runs after 5e |",
			"| (b) | `RESULT.classification == \"plugin-defect\"` | **Step 6 — PLUGIN-DEFECT ROUTE**. Skip Step 5: no other surface edit is right for a plugin defect |",
			"| (c) | `RESULT.proposals` is not empty | Step 5 |",
			"| (d) | any other result (`user-code` or `ambiguous` with no proposal) |",
			"Report `No actionable hardening proposals — the failure signal does not point at any of the loaded surfaces.`",
			// Step 5: skips before the approval gate.
			"it under `Skipped` in the 5d summary as `malformed merge`.",
			"in the 5d summary as `_common.md is not a dimension`.",
			"summary as `not repairable: no rule id`.",
			"5d summary as `stale oldText`.",
			// Step 5: the --auto table rows that wait for a person.
			"| remove, `removalReason: \"covered-by\"` | ask | pending |",
			"| merge | ask | pending |",
			// Step 5: an --auto repair or removal applies only when the
			// manifest proves its evidence for the same rule.
			"| repair, `evidence: \"preflight\"` | ask | apply only when it matches a pre-flight finding, else pending |",
			"| remove, `removalReason: \"dead-reference\"` | ask | apply only when each token is proven for its rule, else pending |",
			// Step 5: a pending line with an unsafe id is not stored.
			"with the reason `unsafe id: <value>`.",
			// Step 5: the preview shows the old text in full.
			"{oldText, in full}",
			// Step 5: the pending item ids, one for each section, the write
			// call with its source and merge flag, and the returned id.
			"| `plan-guardrails` | `plan guardrails` | `harden-pending-plan` |",
			"| `execute-guardrails` | `execute guardrails` | `harden-pending-execute` |",
			"| `review-dimensions` | `review dimensions` | `harden-pending-dimensions` |",
			"| `copilot-instructions` | `copilot instructions` | `harden-pending-copilot` |",
			`ship_state({action:"deferred_add", detail:{id:"harden-pending-plan", source:"harden-pending", merge:true,`,
			"Use the returned `id` as `<id>` in the 5d",
			// 5a guardrail path: scoped checks, remove candidates and null deletes.
			"| add, strengthen, consolidate, repair | `validate guardrails`, `candidatesJson:[<entries>]` | `setup_write_sections`, `{\"<section>.guardrails.<id>\": {...}}` | `validate guardrails`, `scopeIds:[<ids>]` |",
			"| split (repair of an oversize rule) | `candidatesJson:[<id>-1, <id>-2, {\"id\":\"<id>\",\"remove\":true}]` | `{\"…<id>-1\": {...}, \"…<id>-2\": {...}, \"…<id>\": null}` | `scopeIds:[\"<id>-1\",\"<id>-2\"]` |",
			"| remove | `candidatesJson:[{\"id\":\"<id>\",\"remove\":true}]` | `{\"…<id>\": null}` | none: no id is left to check |",
			"| merge | `candidatesJson:[<merged entry>, {\"id\":\"<old>\",\"remove\":true}]` | `{\"…<merged>\": {...}, \"…<old>\": null}` | `scopeIds:[\"<merged>\"]` |",
			`"<section>.guardrails.<removed-id>": null,`,
			// 5a dimension and Copilot path: the three action rows.
			"| add, repair, strengthen, consolidate | none | Edit `targetFile` with `patch` | `validate` action `dimensions` with `file`. On a finding, revert |",
			"| remove a whole file | none: nothing is written | delete `targetFile` | `validate` action `dimensions`. On a finding, write `oldText` back to `targetFile` |",
			"| merge | Edit `mergedInto` with `patch`. Then `validate` action `dimensions` with `file: mergedInto`. On a finding, revert `mergedInto` and delete nothing | delete the one file in `removeIds` | `validate` action `dimensions`. On a finding, write `oldText` back to the deleted file |",
			// 5a dimension and Copilot path: the five failure rows.
			"| Edit of `mergedInto` fails | delete nothing | unchanged | `skipped (write failed): <error>` |",
			"| check of `mergedInto` has a finding | revert `mergedInto`. Delete nothing | unchanged | `reverted: <finding>` |",
			"| delete fails | revert `mergedInto` (merge only) | unchanged | `reverted: could not delete <file>: <error>` |",
			"| check after the delete has a finding | write `oldText` back to the deleted file. Revert `mergedInto` (merge only) | unchanged | `reverted: <finding>` |",
			"| write-back of `oldText` fails | stop the loop (the 5b halt). Print `oldText` in full | the file is gone | `NOT restored: <file> — copy the text above back by hand` |",
			// 5b: the mirror name check and the four mirror rows.
			"`Copilot mirror skipped: unsafe name <name>`.",
			"| add | render the mirror (`dimensions_render_instructions`) |",
			"| repair, strengthen, consolidate | render the mirror again when `.github/instructions/<name>.instructions.md` exists. Otherwise do nothing |",
			"| remove, or the removed file of a merge |",
			"| mirror render fails with any other error, or mirror delete fails | the halt in step 2 below, for each row |",
			// 5d summary.
			"harden --auto: {A} auto-accepted, {R} reverted, {S} skipped, {P} pending, {U} not processed",
			"Pending (needs a person — stored as deferred item {id}):",
			"Pending (NOT stored — copy them by hand):",
			// Step 7: the removed ids and the close command of a replay run.
			"Removed: <comma-separated rule ids and file names deleted>",
			"`Every pending rule edit of <id> is applied or rejected. Close the item with /sdlc:deferred --resolve <id>.`",
			"`Deferred item <id>: no rule edit was drafted. Check the id with /sdlc:deferred --list.`",
			// The no-relax wording that replaced strengthen-only.
			"Never relaxes a rule. Removes a rule only in three cases: a dead reference, a covering rule, or the split of an oversize rule.",
			"Propose relaxing a rule, or removing a rule outside the three removal cases (Step 0).",
		} {
			hardenSkillsAssertOnce(t, "harden/SKILL.md", skill, want)
		}
		if strings.Contains(strings.ToLower(skill), "strengthen-only") {
			t.Error("harden/SKILL.md still says strengthen-only, but harden now removes rules in three cases")
		}
		if strings.Contains(skill, "two removal cases") {
			t.Error("harden/SKILL.md still says two removal cases, but a pre-flight split repair is the third case")
		}
	})

	// The skill-recommendation entries are not a proposal surface, but the
	// orchestrator turns them into proposals with evidence "learning" on one
	// of the four real surfaces, and Step 5 applies those proposals. The
	// pins run on the text with whitespace folded, because the sentences wrap
	// across lines in SKILL.md. Each pin occurs once.
	t.Run("skill_recommendation_contract", func(t *testing.T) {
		skill := executeSkillsReadFile(t, filepath.Join(executeSkillsRepoRoot(t), "skills", "harden", "SKILL.md"))
		flat := strings.Join(strings.Fields(skill), " ")
		for _, want := range []string{
			// Step 1: the array is context that feeds proposals.
			"It is never a proposal surface.",
			"`targetFile` to edit, so Step 5 never applies an entry of this array.",
			"into a proposal with `evidence: \"learning\"` on one of the four real surfaces. Step 5 applies that proposal like any other proposal.",
			// Step 5a: a proposal with the advisory surface is still skipped.
			"Its entries feed proposals with `evidence: \"learning\"`, and those proposals name one of the four surfaces above.",
			"skip it without applying (do not Edit/Write, do not validate, do not check)",
		} {
			hardenSkillsAssertOnce(t, "harden/SKILL.md", flat, want)
		}
		if strings.Contains(flat, "never reads `surfaces.skillRecommendations`") {
			t.Error("harden/SKILL.md says the orchestrator never reads surfaces.skillRecommendations, but it drafts learning proposals from it")
		}
		agent := executeSkillsReadFile(t, filepath.Join(executeSkillsRepoRoot(t), "agents", "harden-orchestrator.md"))
		for _, want := range []string{
			"becomes a proposal with `evidence: \"learning\"`",
			"When no rule covers the pattern, draft a proposal with `evidence: \"learning\"`.",
		} {
			hardenSkillsAssertOnce(t, "harden-orchestrator.md", agent, want)
		}
	})

	// The orchestrator drafts repair, remove, merge and replay proposals in
	// Step 3a to Step 3d, and the skill applies them as they come. Each pin
	// states one rule of those steps and occurs once in the agent file, so the
	// deletion of one sentence cannot pass because a copy stays elsewhere. The
	// pins run on the text with whitespace folded.
	t.Run("orchestrator_step3_contract", func(t *testing.T) {
		agent := executeSkillsReadFile(t, filepath.Join(executeSkillsRepoRoot(t), "agents", "harden-orchestrator.md"))
		flat := strings.Join(strings.Fields(agent), " ")
		for _, want := range []string{
			// Step 2 and Step 3a: one repair for each pre-flight finding, first.
			"list all `repair` proposals for `preflightFindings` first, in the order of `preflightFindings`.",
			"Draft one `repair` proposal for each entry of `preflightFindings`. Do this for every classification, `plugin-defect` included.",
			// Step 3b: a remove names only listed dead tokens, and the two
			// covered-by cases.
			"Name only tokens that appear in `surfaces.deadReferences`. Never remove a rule for a token that is not in the list.",
			"When another rule covers the whole class, draft a `remove` with `removalReason: \"covered-by\"`. Name the covering rule in `rationale` and in `extends`.",
			"When another rule covers most of the class, draft a `merge`. `mergedInto` is the id that stays. `removalReason` is `covered-by`. `removeIds` names exactly one removed id. The `patch` and `guardrails[]` hold the new text of the id that stays.",
			// The oldText row of a merge: the removed rule, not the kept one.
			"For a `merge`, it is the full old text of the rule in `removeIds`, not of the rule that keeps the text.",
			// Step 3c: the replay of a pending item.
			"find the item with that `id` in `history.openDeferred`. Draft again each edit that its `description` lists.",
			"When no item has that `id`, draft nothing for it and say so in `classificationRationale`.",
			"When `history.readErrors` is set, say that the deferred store could not be read, not that no item has that `id`.",
			// Step 3d: the generic-rule gate.
			"Apply the gate to each draft from Step 3c. The gate does not apply to Step 3a or Step 3b proposals.",
			"When a draft matches the class of an existing rule, it becomes a `consolidate` (guardrail surfaces) or a `strengthen` (other surfaces). Set `extends` to that rule's id or file.",
			"When no class can be found, drop the draft. Add one entry to `skipped.drafts`: `{\"failureClass\": \"...\", \"reason\": \"...\"}`. The `reason` says why the draft failed the gate.",
		} {
			hardenSkillsAssertOnce(t, "harden-orchestrator.md", flat, want)
		}
	})

	// Step 5e sends the ship state record of a harden run: the rationale and
	// the written patch of each applied proposal, and the held list. Each pin
	// states one rule of that record and occurs once, so the deletion of one
	// site cannot pass because a copy stays elsewhere. The pins run on the
	// text with whitespace folded, because the sentences wrap across lines.
	t.Run("ship_state_record_contract", func(t *testing.T) {
		// The rows of the held exit table. SKILL.md and docs/skills/harden.md
		// hold the same six rows.
		heldExitRows := []string{
			"| `--auto` | normal path (after 5c and 5d) | `pending-stored` with the returned `deferredId` for each \"Pending (needs a person — stored …)\" item. `pending-not-stored` for each \"Pending (NOT stored …)\" item. | Step 6 when `plugin-defect`, else Step 7 |",
			"| `--auto` | 5a write-back halt or 5b halt | The rows of the line above, plus `not-processed` for each \"Not processed (halt)\" item | `rm -f \"<manifestPath>\"`, then stop. No Step 7. |",
			"| interactive | normal path | `[]`. A person decided each proposal. | Step 6 when `plugin-defect`, else Step 7 |",
			"| interactive | 5a write-back halt or 5b halt | `not-processed` for each proposal after the halted proposal in `RESULT.proposals` order | `rm -f \"<manifestPath>\"`, then stop. No Step 7. |",
			"| interactive | `cancel` at the approval gate | `[]`. A person chose `cancel`. | Abort, `rm -f \"<manifestPath>\"`. No Step 7. |",
			"| any | Step 4 route (b) through Step 6, or route (d) | `[]`. Step 5 did not run. | Step 7 |",
		}

		skill := executeSkillsReadFile(t, filepath.Join(executeSkillsRepoRoot(t), "skills", "harden", "SKILL.md"))
		flat := strings.Join(strings.Fields(skill), " ")
		for _, want := range append([]string{
			// Per-iteration contract, rule 3: the one exception.
			"One exception: keep the `writtenPatch` of each applied proposal for the 5e record. It is a record of a done write, not a change to write.",
			// Step 5b: a repaired guardrail candidate sets writtenPatch.
			"When 5a or `consolidate` repaired a guardrail candidate before this write, set the proposal's `writtenPatch` to the TOML entry that this write added.",
			// Step 5e: the entry of applied, the rationale rule, the patch rule.
			"one `{surface, action, targetFile, rationale, patch}` entry, built from the matching proposal,",
			"Send `rationale` only when the proposal `rationale` is a non-empty string; otherwise leave the key out.",
			"`patch` is the proposal `writtenPatch` when it is set, else the proposal `patch`, else `\"\"`.",
			"healing_record cuts a patch over 1536 bytes and sets `patchCut`. Do not send `patchCut`.",
			// Step 5e: the entry of held, the deferredId rule, the empty list.
			"`held` — one `{surface, action, targetFile, reason, deferredId}` entry for each proposal that this run left for a person (the held exit table below).",
			"Send `deferredId` only with reason `pending-stored`:",
			"Send `held: []` when no row of the held exit table gives an entry.",
			// Step 5e: each held reason.
			"| `pending-stored` | `--auto` stored the proposal as a deferred item. `deferredId` is the id of that item |",
			"| `pending-not-stored` | `--auto` could not store the proposal: the `deferred_add` call failed, or the id check failed |",
			"| `not-processed` | A halt stopped the loop before this proposal |",
			// Step 5e: the call sends held between applied and skipped.
			"applied: [<entries built above>], held: [<entries built above>], skipped: <count built above>,",
		}, heldExitRows...) {
			hardenSkillsAssertOnce(t, "harden/SKILL.md", flat, want)
		}
		if strings.Contains(flat, "{surface, action, targetFile}") {
			t.Error("harden/SKILL.md still names the old 5e entry {surface, action, targetFile}, without rationale and patch")
		}

		// The docs section lists the record fields, each held reason, each
		// exit of the held table and the causes of an interrupted record.
		docsPath := filepath.Join(filepath.Dir(filepath.Dir(executeSkillsRepoRoot(t))), "docs", "skills", "harden.md")
		docs := executeSkillsReadFile(t, docsPath)
		hardenSkillsAssertOnce(t, "docs/skills/harden.md", docs, "\n## Ship state record\n")
		docsFlat := strings.Join(strings.Fields(docs), " ")
		for _, want := range append([]string{
			"| `applied[].rationale` | The rationale of the proposal. The key is absent when the proposal has no rationale |",
			"| `held[].reason` | `pending-stored`, `pending-not-stored` or `not-processed` |",
			"| The session ends between step 5-pre and step 5e | the `started` record |",
			"| The step 5e call fails (for example, a field is not valid) | the `started` record |",
		}, heldExitRows...) {
			hardenSkillsAssertOnce(t, "docs/skills/harden.md", docsFlat, want)
		}
	})
}

// hardenSkillsAssertOnce reports an error unless text holds want exactly
// once. A missing pin means its site was deleted. A pin found more than once
// is reported too, because one copy could then hide the deletion of another.
func hardenSkillsAssertOnce(t *testing.T, label, text, want string) {
	t.Helper()
	switch n := strings.Count(text, want); n {
	case 1:
	case 0:
		t.Errorf("%s lacks %q", label, want)
	default:
		t.Errorf("%s holds %q %d times, want exactly once", label, want, n)
	}
}
