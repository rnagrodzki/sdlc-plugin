package tools

import (
	"encoding/json"
	stderrors "errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/config"
	"github.com/rnagrodzki/sdlc-plugin/internal/configmigrate"
	"github.com/rnagrodzki/sdlc-plugin/internal/execx"
	"github.com/rnagrodzki/sdlc-plugin/internal/ghx"
	"github.com/rnagrodzki/sdlc-plugin/internal/gitx"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
	"github.com/rnagrodzki/sdlc-plugin/internal/pipeline"
	"github.com/rnagrodzki/sdlc-plugin/internal/shipmeta"
	"github.com/rnagrodzki/sdlc-plugin/internal/state"
	"github.com/rnagrodzki/sdlc-plugin/internal/worktree"
)

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

// validQuality mirrors the accepted values for ship.js's --quality flag.
var validQuality = []string{"full", "balanced", "minimal"}

// preReleaseLabelRe mirrors PRE_RELEASE_LABEL_RE in scripts/lib/version.js.
// Used to validate a configured version.preRelease label before letting it
// override the resolved --bump value.
var preReleaseLabelRe = regexp.MustCompile(`^[a-z][a-z0-9]*$`)

// shipStepSideEffects mirrors STEP_SIDE_EFFECTS in scripts/skill/ship.js: the
// map of pipeline step name to the side-effect kind that proves it landed.
// Covers "pr" (an open PR for the branch) and "commit" (HEAD has advanced to
// a known sha) — the universal side-effect journal (sideEffects in ship
// state) covers both kinds uniformly; see shipVerifySideEffect below. Kind
// values here must stay in sync with ship-state.schema.json's
// sideEffects.*.kind enum.
//
// A step with no entry here (e.g. "version": its release diagnostics now
// run inside the pr step's pr_prepare call, not as a standalone step) has
// no side effect to verify — shipVerifySideEffect reports it landed with
// reason "no-side-effect".
var shipStepSideEffects = map[string]string{
	"pr":     "pr",
	"commit": "sha",
}

// ---------------------------------------------------------------------------
// Input / Output types
// ---------------------------------------------------------------------------

// ShipPrepareIn is the input for the ship_prepare tool. Field names follow
// parseArgs's return shape in scripts/skill/ship.js, minus flags that were
// hard-removed there (--preset, --skip, --workspace/--branch/--tree,
// --verify-pipeline, --await-review — these are rejected by the CLI parser
// and have no corresponding data field to port).
type ShipPrepareIn struct {
	SkipConfigCheck bool `json:"skipConfigCheck" jsonschema_description:"Skips the config-version auto-migration gate normally run before preflight checks. Set only when the caller has already verified or migrated the config."`

	HasPlan            bool     `json:"hasPlan" jsonschema_description:"Whether a plan already exists for this pipeline run. When true and planFile is empty while the execute step will run, this is a validation error — a plan file must be supplied."`
	Auto               bool     `json:"auto" sdlcconfig:"ship.auto" jsonschema_description:"Run the pipeline unattended (no human available to confirm anything right now). Optional. Defaults to config ship.auto. Pass only to override."`
	Steps              []string `json:"steps" sdlcconfig:"ship.steps" jsonschema_description:"Explicit ordered list of pipeline step names to run, overriding the quick-derived step list. Takes precedence over quick when non-empty. Optional. Defaults to config ship.steps. Pass only to override."`
	Quick              bool     `json:"quick" jsonschema_description:"Use the abbreviated \"quick\" step list instead of the full pipeline, when steps is not explicitly supplied."`
	Quality            string   `json:"quality" jsonschema:"enum=full,enum=balanced,enum=minimal" jsonschema_description:"Quality gate level to merge into the resolved pipeline config."`
	Bump               string   `json:"bump" sdlcconfig:"ship.bump" jsonschema_description:"Version bump level (e.g. \"patch\"/\"minor\"/\"major\") to merge into the resolved pipeline config. Optional. Defaults to config ship.bump. Pass only to override."`
	Draft              bool     `json:"draft" sdlcconfig:"ship.draft" jsonschema_description:"Create the PR as a draft. Optional. Defaults to config ship.draft. Pass only to override."`
	DryRun             bool     `json:"dryRun" jsonschema_description:"Validate and initialize state without performing any side-effecting pipeline actions."`
	Resume             bool     `json:"resume" jsonschema_description:"Resume a previously initialized ship run from its persisted state instead of starting a new one."`
	Rebase             string   `json:"rebase" sdlcconfig:"ship.rebase" jsonschema_description:"Rebase strategy/target branch to merge into the resolved pipeline config. Optional. Defaults to config ship.rebase. Pass only to override."`
	OpenspecChange     string   `json:"openspecChange" jsonschema_description:"Name of the openspec change this ship run is associated with, when applicable."`
	HookActivePipeline bool     `json:"hookActivePipeline" jsonschema_description:"Whether a hook reported an already-active pipeline for this session, recorded into the initialized state."`
	PlanModeBlocked    bool     `json:"planModeBlocked" jsonschema_description:"Whether plan mode was blocked for this session, recorded into the initialized state."`
	PlanFile           string   `json:"planFile" jsonschema_description:"Path to the plan file for this run. Required whenever hasPlan is true and the execute step is part of the resolved pipeline."`

	// Gc, when true, short-circuits shipPrepare into the --gc on-demand
	// pruning branch (ship.js's R39 `if (cli.gc)` block): normal flag-merge/
	// step-validation/state-init is skipped entirely. TtlDays mirrors
	// --ttl-days; nil means "not supplied on the CLI" (0 is a legal explicit
	// value, so this cannot be a plain int with a zero-means-unset
	// convention).
	Gc      bool `json:"gc" jsonschema_description:"Short-circuits into the --gc on-demand pruning branch: normal flag-merge/step-validation/state-init is skipped entirely and stale state files/tempdirs are pruned instead."`
	TtlDays *int `json:"ttlDays,omitempty" jsonschema_description:"Time-to-live in days for gc pruning, mirroring --ttl-days. Omit to use the default TTL; 0 is a legal explicit value meaning no grace period."`

	// SessionID is stamped into the initialized state's sessionId field
	// (matching lib/state.js's initState CLAUDE_CODE_SESSION_ID stamp).
	SessionID string `json:"sessionId" jsonschema_description:"Session identifier stamped into the initialized ship state's sessionId field, for correlating this run with the calling session."`
}

// ShipGCBucket holds the deleted/kept paths for one state-file prefix (or
// the exploreTempdirs sweep), matching state.GCReport's flat path-list
// convention. This is less rich than ship.js's gcStateFiles/gcTempdirs,
// which annotate every entry with a prune reason ("ttl-fresh",
// "branch-exists", "stale+branch-gone", "unparseable-name") — state.GC (Task
// 9/10) does not track per-file reasons, so this is a deliberate, already
// existing constraint on any Go caller, not something introduced here.
// Separately, ship.js's gcStateFiles also puts unparseable-name files
// (including dot-prefixed sidecars like .compact-recovery-*.json) into
// "kept" for every prefix bucket before filtering; state.GC skips them
// entirely (see gc.go), so they never appear in any bucket here. Neither
// side ever deletes them — only the reporting differs.
type ShipGCBucket struct {
	Deleted []string `json:"deleted"`
	Kept    []string `json:"kept"`
}

// ShipGCReport mirrors the report shape ship.js's --gc branch assembles:
// {ttlDays, ship, execute, plan, commit, exploreTempdirs}.
type ShipGCReport struct {
	TtlDays         int          `json:"ttlDays"`
	Ship            ShipGCBucket `json:"ship"`
	Execute         ShipGCBucket `json:"execute"`
	Plan            ShipGCBucket `json:"plan"`
	Commit          ShipGCBucket `json:"commit"`
	ExploreTempdirs ShipGCBucket `json:"exploreTempdirs"`
}

// ShipPrepareOut is the output for the ship_prepare tool.
type ShipPrepareOut struct {
	Errors   []string `json:"errors"`
	Warnings []string `json:"warnings"`

	// Action and Report are populated only on the --gc short-circuit path
	// (Action == "gc"), mirroring ship.js's {action:"gc", report, errors,
	// warnings} output. Report is nil when gc failed before producing one
	// (arg errors or an exception during the sweep itself).
	Action string        `json:"action,omitempty"`
	Report *ShipGCReport `json:"report,omitempty"`

	// Flags is the fully merged (cli > config > default) flag set, matching
	// the shape written into the initialized state's `flags` field.
	Flags map[string]any `json:"flags"`
	// Sources records, per flag key, which precedence tier won ("cli",
	// "config", "config (version.preRelease)",
	// "config (version.preReleasePolicy)",
	// "config (version.preReleasePolicy enforced over cli)",
	// "quick", or "default").
	Sources map[string]string `json:"sources"`

	Branch    string `json:"branch"`
	Worktree  string `json:"worktree"`
	StateFile string `json:"stateFile,omitempty"`

	// PrunedOrphans lists pre-existing ship-<slug>-*.json state files removed
	// as a side effect of initializing the new one, mirroring cmdInit's
	// prunedOrphans in scripts/state/ship.js.
	PrunedOrphans []string `json:"prunedOrphans"`

	// PipelineDisplay is a pipeline.PipelineTable render of the seeded step
	// scaffold (state.Data["steps"], in configured order) — populated only
	// once state init actually happens (empty on the --gc or errors path).
	PipelineDisplay string `json:"pipelineDisplay,omitempty"`

	// Migration is populated when the KD5 gate found the config outdated
	// and auto-migrated it in place (configmigrate.MigrateWithBackup). Nil
	// when the config was already current — no backup was written and no
	// migration ran.
	Migration *MigrationReport `json:"migration,omitempty"`

	Next string `json:"next"`
}

// MigrationReport describes an inline config auto-migration performed by
// the KD5 gate (configmigrate.MigrateWithBackup) before ship_prepare's or
// execute_state's "init" normal work runs.
type MigrationReport struct {
	// Changes lists the migration step labels applied, combining
	// configmigrate.Report's StepsApplied and LegacyIngested.
	Changes []string `json:"changes"`
	// BackupPath is the .bak file written before migrating config.json in
	// place.
	BackupPath string `json:"backupPath"`
}

// shipPrepareNext derives next-step guidance for a ShipPrepareOut, mirroring
// prPrepareNext's pattern. Keyed on outcome: errors (fix and retry), gc
// (complete, no follow-on), or success (proceed to ship_state).
func shipPrepareNext(out ShipPrepareOut) string {
	if out.Action == "gc" {
		if len(out.Errors) > 0 {
			return "GC failed. Fix the errors above and retry ship_prepare with gc:true."
		}
		return "GC complete. No further action needed."
	}
	if len(out.Errors) > 0 {
		return "Fix the errors above, then call ship_prepare again."
	}
	return "Confirm release level, then call ship_state with action:\"begin-step\" for the first step in flags.steps."
}

// ShipVerifySideEffectIn is the input for the ship_verify_side_effect tool.
type ShipVerifySideEffectIn struct {
	Step     string `json:"step" jsonschema_description:"Pipeline step name to verify the side effect for."`
	Expected string `json:"expected" jsonschema_description:"Expected side-effect value (PR number or commit sha) to check landed, matching the step."`
}

// ShipVerifySideEffectOut is the output for the ship_verify_side_effect tool,
// mirroring the payload shapes emitted by verifySideEffect in
// scripts/skill/ship.js.
type ShipVerifySideEffectOut struct {
	Step       string  `json:"step"`
	SideEffect string  `json:"sideEffect,omitempty"`
	Landed     bool    `json:"landed"`
	Expected   *string `json:"expected,omitempty"`
	Reason     string  `json:"reason,omitempty"`
	Next       string  `json:"next"`
}

// MarshalJSON branches on which of the two shapes ship.js's verifySideEffect
// emit() calls actually produces — plain struct tags can't express this,
// since the field set differs per branch, not just field values:
//
//   - no-side-effect (Reason set): {step, landed, reason} — no "expected" or
//     "sideEffect" key at all, matching `emit({ step, landed, reason:
//     'no-side-effect' }, 0)`.
//   - has-side-effect (Reason empty): {step, sideEffect, landed, expected} —
//     "expected" is ALWAYS present, as a JSON string or null, matching
//     `emit({ step, sideEffect, landed, expected: expected || null }, ...)`.
//     `omitempty` on Expected *string alone would wrongly drop the key here
//     too when Expected is nil (e.g. verifying a step with no --expected
//     tag), which is exactly the bug this method fixes.
func (o ShipVerifySideEffectOut) MarshalJSON() ([]byte, error) {
	if o.Reason != "" {
		return json.Marshal(struct {
			Step   string `json:"step"`
			Landed bool   `json:"landed"`
			Reason string `json:"reason"`
			Next   string `json:"next"`
		}{Step: o.Step, Landed: o.Landed, Reason: o.Reason, Next: o.Next})
	}
	return json.Marshal(struct {
		Step       string  `json:"step"`
		SideEffect string  `json:"sideEffect"`
		Landed     bool    `json:"landed"`
		Expected   *string `json:"expected"`
		Next       string  `json:"next"`
	}{Step: o.Step, SideEffect: o.SideEffect, Landed: o.Landed, Expected: o.Expected, Next: o.Next})
}

// ---------------------------------------------------------------------------
// ship_prepare
// ---------------------------------------------------------------------------

// shipPrepare mirrors the pure, git/config-only subset of ship.js's main():
// flag merge (mergeFlags), pure validation (runValidation, minus gh-auth and
// openspec-aware computeSteps checks), and ship-state initialization
// (scripts/state/ship.js's cmdInit).
//
// Deliberate deviations from source (documented for Task 35/40 follow-up):
//   - KD5 config-version failure returns a nil Go error with a minimal
//     {errors, warnings:[], flags:{}, sources:{}, prunedOrphans:[]} payload,
//     matching plan.go's early-return-with-minimal-payload soft-gate style
//     rather than ship.js's own bespoke {errors, warnings,
//     flags:{skipConfigCheck}, migration} partial shape. commit.go's KD5 gate
//     takes a different control-flow shape (it appends to Errors and falls
//     through to compute every other field rather than returning early), so
//     it is not a second instance of this same convention — it shares only
//     the "nil Go error" part, not the early-return structure.
//   - gh-auth/account-mismatch checks are not ported: internal/ghx.AuthStatus
//     and ParseRemoteOwner already exist, but wiring gh-auth/account-mismatch
//     validation into ship_prepare is not covered by this task's Contract/
//     ACs (which name the flag-merge, validation, and state-init surface,
//     not gh-auth) — not a missing dependency, just out of this task's scope.
//   - The openspec-aware computeSteps() is not ported: internal/openspec.Detect
//     already exists, but this task's ACs steer the step-list surface toward
//     shipmeta (CanonicalSteps/ValidSteps/ReservedSteps), not an
//     openspec-aware "will run" recomputation — out of this task's scope,
//     not blocked on unfinished work.
//   - missingPlanFile's structured {id, message} error is flattened to a
//     plain string, matching the codebase-wide []string convention.
//   - --gc/--ttl-days IS ported (see shipGC below), but its report entries
//     are flat deleted/kept path lists rather than ship.js's per-entry
//     {file/dir, branch, reason} records, since state.GC (Task 9/10) does
//     not track per-file prune reasons — a pre-existing constraint of the
//     underlying primitive, not something this task introduces.
//   - state.GC's file-retention semantics differ from ship.js's gcStateFiles
//     at two edges (both pre-existing, Task 9/10 design choices, not touched
//     here): state.GC deletes a live branch's non-newest files once past TTL
//     (source keeps every file of a live branch regardless of age), and
//     deletes ALL of a dead branch's files including TTL-fresh ones (source
//     keeps a dead branch's TTL-fresh files too, deleting only
//     stale+branch-gone ones). Flagged for Task 35/40 follow-up.
//   - State is only initialized when validation produces zero errors; ship.js
//     itself never initializes state on its normal path (only the
//     plan-mode-blocked branch does) — SKILL.md orchestrates a separate init
//     call after a clean ship-prepare. This tool consolidates both steps.
//   - Flags/Sources additionally carry "hookActivePipeline" and
//     "skipConfigCheck" (mirroring the corresponding ShipPrepareIn fields),
//     which ship.js's mergeFlags does not include in its {merged, sources}
//     return value at all. Additive only — every key JS's mergeFlags does
//     produce is still present with the same value and source — so this is
//     not a contract break, just a previously-undisclosed extra pair of keys
//     any strict-shape consumer should tolerate.
func shipPrepare(cfgRoot, activeRoot string, in ShipPrepareIn) (ShipPrepareOut, error) {
	// KD5 gate: config version check. An outdated config is auto-migrated
	// in place (configmigrate.MigrateWithBackup writes a .bak backup before
	// rewriting config.json) rather than hard-failing. Only a genuinely
	// missing config (project never ran /setup) or a too-new schema still
	// short-circuits, using the same soft style as before (matches plan.go's
	// early-return convention specifically, not commit.go's continue-past-
	// append one — see the deviations note above): nil Go error, minimal
	// errors-only payload, no further processing.
	var migrationReport *MigrationReport
	if !in.SkipConfigCheck {
		changes, backupPath, err := configmigrate.MigrateWithBackup(cfgRoot)
		if err != nil {
			out := ShipPrepareOut{
				Errors:        []string{fmt.Sprintf("config-version: %s", err.Error())},
				Warnings:      []string{},
				Flags:         map[string]any{},
				Sources:       map[string]string{},
				PrunedOrphans: []string{},
			}
			out.Next = shipPrepareNext(out)
			return out, nil
		}
		if backupPath != "" {
			migrationReport = &MigrationReport{Changes: changes, BackupPath: backupPath}
		}
	}

	// --gc short-circuit (R39): matches ship.js's main(), which checks
	// cli.gc only after the KD5 gate above has already passed — gc mode does
	// NOT bypass config-staleness gating. Skips all normal flag-merge/
	// step-validation/state-init below.
	if in.Gc {
		return shipGC(cfgRoot, activeRoot, in, migrationReport), nil
	}

	shipCfg, _ := config.ReadSection(cfgRoot, "ship")
	if shipCfg == nil {
		shipCfg = map[string]any{}
	}
	versionCfg, versionCfgErr := config.ReadSection(cfgRoot, "version")
	if versionCfg == nil {
		versionCfg = map[string]any{}
	}
	automationCfg, _ := config.ReadSection(cfgRoot, "automation")
	if automationCfg == nil {
		automationCfg = map[string]any{}
	}

	merged, sources := mergeShipFlags(in, shipCfg, versionCfg, automationCfg)

	errors := []string{}
	warnings := []string{}

	// Surface non-benign version config read errors (corrupted file, I/O).
	// A missing section is expected and already handled by the nil default above.
	if versionCfgErr != nil && !stderrors.Is(versionCfgErr, config.ErrNotFound) {
		errors = append(errors, fmt.Sprintf("version config: %v", versionCfgErr))
	}

	// Warn when always-rc enforcement overrode an explicit CLI --bump.
	if sources["bump"] == "config (version.preReleasePolicy enforced over cli)" {
		warnings = append(warnings, fmt.Sprintf(
			"preReleasePolicy %q overrode explicit CLI --bump %q to %q",
			"always-rc", in.Bump, merged["bump"]))
	}

	stepsList, _ := merged["steps"].([]string)

	// Step-name validity (RESERVED_STEPS always errors; unrecognized names
	// error when the CLI supplied --steps, warn when they came from config).
	for _, st := range stepsList {
		if sliceContainsStr(shipmeta.ReservedSteps, st) {
			errors = append(errors, fmt.Sprintf(
				"%q is a reserved terminal step appended automatically by the pipeline. Remove it from --steps and ship.steps[].", st))
			continue
		}
		if !sliceContainsStr(shipmeta.ValidSteps, st) {
			msg := fmt.Sprintf("Unrecognized step %q in %s. Valid values: %s",
				st, stepsFieldLabel(sources["steps"]), strings.Join(shipmeta.ValidSteps, ", "))
			if sources["steps"] == "cli" {
				errors = append(errors, msg)
			} else {
				warnings = append(warnings, msg)
			}
		}
	}

	// --quality validity.
	if q, ok := merged["quality"].(string); ok && q != "" {
		if !sliceContainsStr(validQuality, q) {
			errors = append(errors, fmt.Sprintf(
				"Invalid --quality %q. Valid values: %s", q, strings.Join(validQuality, ", ")))
		}
	}

	// At least one step must run.
	if len(stepsList) == 0 {
		errors = append(errors, "All steps are skipped. At least one step must run.")
	}

	// execute-without-plan. Keyed on the RESOLVED step list containing an
	// execute step that will actually run (hasPlan AND "execute" in
	// stepsList) — not on the raw --has-plan flag alone — matching
	// ship.js's R72/C19 (#505) rationale. This mirrors computeSteps' will_run
	// gating for "execute" specifically (`!flags.hasPlan || !isIn('execute')`
	// => skipped); it does not require porting the rest of computeSteps,
	// since "execute" is not openspec-gated.
	executeWillRun := in.HasPlan && sliceContainsStr(stepsList, "execute")
	if in.PlanFile == "" && executeWillRun {
		errors = append(errors, "ship cannot run the \"execute\" step without a plan document. "+
			"Fix: re-run with --plan <path-to-plan.md>. Why: plan autodiscovery was removed (#505). "+
			"It picked the most recently modified *.md in ~/.claude/plans/, which is shared across "+
			"repositories — it could hand this repo a plan written for a different one and implement "+
			"it here. If you did not mean to run execute, drop --has-plan (or omit execute from "+
			"--steps) and the pipeline will skip it.")
	}

	// --bump without a pr step (version diagnostics now run inside pr_prepare).
	if bumpVal, _ := merged["bump"].(string); bumpVal != "" && sources["bump"] == "cli" && !sliceContainsStr(stepsList, "pr") {
		errors = append(errors, fmt.Sprintf(
			"--bump %q specified but pr step is skipped — resolve by removing --bump or adding \"pr\" to ship.steps[].", bumpVal))
	}

	// --quick + --steps conflict.
	if in.Quick && sources["steps"] == "cli" {
		errors = append(errors, "--quick + --steps not allowed: use --quick or --steps, not both")
	}

	// --quick with no configured profile.
	if in.Quick && sources["steps"] == "quick" && len(stepsList) == 0 {
		errors = append(errors, "No quick profile defined. Run `ship --init-config` to set one.")
	}

	// execute.commitWaves configured with a non-boolean value.
	if invalid, _ := merged["commitWavesInvalidType"].(bool); invalid {
		warnings = append(warnings, "execute.commitWaves in ship config is not a boolean — value ignored, "+
			"defaulting to false. Set it to true or false explicitly.")
	}

	// Unconditional review-pause notice.
	warnings = append(warnings, "If review finds critical/high issues, pipeline will pause for fix approval")

	// Not-on-default-branch notice (pure git, no gh needed).
	currentBranch, _ := gitx.CurrentBranch(activeRoot)
	defaultBranch, _ := gitx.DefaultBranch(activeRoot)
	if defaultBranch != "" && currentBranch != "" && currentBranch == defaultBranch {
		warnings = append(warnings, fmt.Sprintf(
			"You are on the default branch %q. Ship pipelines should run on feature branches.", defaultBranch))
	}

	// KD-1 hard gate: pushing to a default branch (main/master) is never
	// allowed, regardless of automation.push config. Unlike the warning
	// above (informational, fires for any step config, driven by actual git
	// config via gitx.DefaultBranch), this blocks outright — but only when
	// the resolved steps actually include "pr" (the step that pushes the
	// branch); a run with no "pr" step never pushes, so there is nothing to
	// gate. isDefaultBranch is intentionally independent of git config
	// (hardcoded main/master), per the task contract.
	if isDefaultBranch(currentBranch) && sliceContainsStr(stepsList, "pr") {
		return ShipPrepareOut{}, &mcpserver.DomainError{
			Msg:        fmt.Sprintf("ship cannot run the \"pr\" step on default branch %q — pushing to main/master is never auto-approved", currentBranch),
			Suggestion: "Switch to a feature branch, or remove \"pr\" from --steps/ship.steps[] if you don't intend to push.",
		}
	}

	out := ShipPrepareOut{
		Errors:        errors,
		Warnings:      warnings,
		Flags:         merged,
		Sources:       sources,
		Branch:        currentBranch,
		Worktree:      activeRoot,
		PrunedOrphans: []string{},
		Migration:     migrationReport,
	}

	if len(errors) > 0 {
		out.Next = shipPrepareNext(out)
		return out, nil
	}

	branchSlug := state.SlugifyBranch(currentBranch)
	pruned, err := existingShipStateFiles(cfgRoot, branchSlug)
	if err != nil {
		return ShipPrepareOut{}, &mcpserver.InfraError{
			Msg:   fmt.Sprintf("scan existing ship state files: %s", err.Error()),
			Cause: err,
		}
	}

	st, err := state.Init(cfgRoot, "ship", currentBranch, in.SessionID)
	if err != nil {
		return ShipPrepareOut{}, &mcpserver.InfraError{
			Msg:   fmt.Sprintf("init ship state: %s", err.Error()),
			Cause: err,
		}
	}
	st.Data["version"] = 1
	st.Data["startedAt"] = time.Now().UTC().Format(time.RFC3339)
	st.Data["branch"] = currentBranch
	st.Data["worktree"] = activeRoot
	st.Data["flags"] = merged
	scaffold := shipmeta.InitialShipStepsFromConfig(stepsList)
	st.Data["steps"] = scaffold
	st.Data["decisions"] = []any{}
	st.Data["deferredFindings"] = []any{}

	if err := state.Write(st); err != nil {
		return ShipPrepareOut{}, &mcpserver.InfraError{
			Msg:   fmt.Sprintf("write ship state: %s", err.Error()),
			Cause: err,
		}
	}

	out.StateFile = st.Path
	out.PrunedOrphans = pruned
	out.PipelineDisplay = pipeline.PipelineTable(configStepsFromScaffold(scaffold))
	out.Next = shipPrepareNext(out)
	return out, nil
}

// shipStepDescriptions gives each known ship pipeline step name a one-line
// description for ShipPrepareOut.PipelineDisplay. A config-sourced name
// outside this map (config validity is a warning, not an error — see
// shipPrepare's step-name validation above) falls back to its bare name.
var shipStepDescriptions = map[string]string{
	"execute":             "Run the plan's execution waves",
	"commit":              "Commit the executed changes",
	"review":              "Run automated code review",
	"received-review":     "Apply fixes for critical/high review findings",
	"commit-fixes":        "Commit review-fix changes",
	"verify-openspec":     "Verify OpenSpec change docs are in sync",
	"archive-openspec":    "Archive completed OpenSpec change docs",
	"pr":                  "Open the pull request",
	"verify-pipeline":     "Wait for CI/pipeline checks to pass",
	"await-remote-review": "Wait for remote reviewer approval",
	"learnings-commit":    "Commit captured learnings",
}

// configStepsFromScaffold converts a seeded step scaffold into the
// []pipeline.ConfigStep shape PipelineTable renders. Steps are never
// model-driven (Model is always "—" via PipelineTable's own fallback) and
// never individually optional post-#505 (--skip was hard-removed — a step
// either is or isn't in the configured list).
func configStepsFromScaffold(scaffold []shipmeta.ShipStateStep) []pipeline.ConfigStep {
	out := make([]pipeline.ConfigStep, 0, len(scaffold))
	for _, s := range scaffold {
		desc := shipStepDescriptions[s.Name]
		if desc == "" {
			desc = s.Name
		}
		out = append(out, pipeline.ConfigStep{Name: s.Name, Description: desc})
	}
	return out
}

// isDefaultBranch reports whether branch is a conventional default branch
// name (main/master). KD-1 hard gate: intentionally independent of git
// config (gitx.DefaultBranch, which reads origin/HEAD or init.defaultBranch)
// — a repo whose default branch happens to be named something else is not
// exempted, and a repo whose git config disagrees is not fooled either.
func isDefaultBranch(branch string) bool {
	return branch == "main" || branch == "master"
}

// stepsFieldLabel renders the source-appropriate name of the steps field for
// error/warning messages ("--steps" for CLI-sourced values, "steps[]" for
// config-sourced ones).
func stepsFieldLabel(source string) string {
	if source == "cli" {
		return "--steps"
	}
	return "steps[]"
}

// mergeShipFlags ports mergeFlags(cli, config) from scripts/skill/ship.js:
// CLI flags win when explicitly set, otherwise config, otherwise
// shipmeta.ShipBuiltInDefaults — except for bump under
// preReleasePolicy: "always-rc", which unconditionally enforces an RC
// bump regardless of source (including CLI).
// Returns the merged flag map plus, per key, which precedence tier
// supplied the value.
func mergeShipFlags(in ShipPrepareIn, cfg map[string]any, versionCfg map[string]any, automationCfg map[string]any) (map[string]any, map[string]string) {
	merged := map[string]any{}
	sources := map[string]string{}

	// steps: cli (non-empty) > quick profile > config (any array, even
	// empty) > default.
	switch {
	case len(in.Steps) > 0:
		merged["steps"] = append([]string{}, in.Steps...)
		sources["steps"] = "cli"
	case in.Quick:
		if quickArr, ok := cfg["quick"].([]any); ok {
			merged["steps"] = anyToStringSlice(quickArr)
		} else {
			merged["steps"] = []string{}
		}
		sources["steps"] = "quick"
	default:
		if cfgArr, ok := cfg["steps"].([]any); ok {
			merged["steps"] = anyToStringSlice(cfgArr)
			sources["steps"] = "config"
		} else {
			merged["steps"] = append([]string{}, shipmeta.ShipBuiltInDefaults.Steps...)
			sources["steps"] = "default"
		}
	}

	// auto, draft: cli (true) > config bool > default.
	mergeShipBool(merged, sources, "auto", in.Auto, cfg, shipmeta.ShipBuiltInDefaults.Auto)
	mergeShipBool(merged, sources, "draft", in.Draft, cfg, shipmeta.ShipBuiltInDefaults.Draft)

	// quality: cli only, else omitted entirely (matches source's intentional
	// omission rather than falling back to a built-in default).
	if in.Quality != "" {
		merged["quality"] = in.Quality
		sources["quality"] = "cli"
	}

	// bump: cli > config string > default.
	if in.Bump != "" {
		merged["bump"] = in.Bump
		sources["bump"] = "cli"
	} else if v, ok := cfg["bump"].(string); ok && v != "" {
		merged["bump"] = v
		sources["bump"] = "config"
	} else {
		merged["bump"] = shipmeta.ShipBuiltInDefaults.Bump
		sources["bump"] = "default"
	}

	// version.preRelease overrides a non-cli bump when it is a valid label.
	if sources["bump"] != "cli" {
		if pr, ok := versionCfg["preRelease"].(string); ok && preReleaseLabelRe.MatchString(pr) {
			merged["bump"] = pr
			sources["bump"] = "config (version.preRelease)"
		}
	}

	// Enforce preReleasePolicy: "always-rc" at ship time.
	// When preReleasePolicy is set to "always-rc", the final bump must result in an RC.
	if policy, _ := versionCfg["preReleasePolicy"].(string); policy == "always-rc" {
		bump, _ := merged["bump"].(string)
		// Valid RC results: "rc" or a valid preRelease label (which implies RC)
		isRC := bump == "rc" || (preReleaseLabelRe.MatchString(bump) && bump != "major" && bump != "minor" && bump != "patch")
		if !isRC {
			// Enforce by overriding to "rc" to ensure preReleasePolicy is not merely informational
			merged["bump"] = "rc"
			if sources["bump"] == "cli" {
				sources["bump"] = "config (version.preReleasePolicy enforced over cli)"
			} else {
				sources["bump"] = "config (version.preReleasePolicy)"
			}
		}
	}

	// reviewThreshold: config string > default (no cli flag).
	if v, ok := cfg["reviewThreshold"].(string); ok && v != "" {
		merged["reviewThreshold"] = v
		sources["reviewThreshold"] = "config"
	} else {
		merged["reviewThreshold"] = shipmeta.ShipBuiltInDefaults.ReviewThreshold
		sources["reviewThreshold"] = "default"
	}

	// pushFeatureBranchAutoApprove (KD-1): automation.push.featureBranchAutoApprove
	// config > mode-derived default. No CLI override — automation is a
	// config-only section. Mirrors config.applyAutomationDefaults'
	// precedence exactly, including its known limitation: an explicit
	// "false" is indistinguishable from "absent" via the bool zero value,
	// so "unattended" mode still forces it true (see
	// internal/config.PushConfig). Consumed by the ship skill doc's pr-step
	// dispatch to decide whether a feature-branch push still needs a
	// manual AskUserQuestion pause; a default-branch push is never
	// auto-approved regardless of this value — see isDefaultBranch below.
	pushAutoApprove := false
	sources["pushFeatureBranchAutoApprove"] = "default"
	if pushRaw, ok := automationCfg["push"].(map[string]any); ok {
		if v, ok := pushRaw["featureBranchAutoApprove"].(bool); ok {
			pushAutoApprove = v
			sources["pushFeatureBranchAutoApprove"] = "config"
		}
	}
	if mode, _ := automationCfg["mode"].(string); mode == "unattended" && !pushAutoApprove {
		pushAutoApprove = true
	}
	merged["pushFeatureBranchAutoApprove"] = pushAutoApprove

	// rebase: cli string > config (bool coerced to "auto"/"skip", string
	// verbatim, or — matching ship.js's unconditional `merged.rebase =
	// cfg.rebase` fallback for any other type — passed through as-is) >
	// default "auto".
	if in.Rebase != "" {
		merged["rebase"] = in.Rebase
		sources["rebase"] = "cli"
	} else if v, exists := cfg["rebase"]; exists {
		switch vv := v.(type) {
		case bool:
			if vv {
				merged["rebase"] = "auto"
			} else {
				merged["rebase"] = "skip"
			}
		case string:
			merged["rebase"] = vv
		default:
			// Malformed config (number, object, array, null, ...): JS passes
			// it through verbatim rather than gating on type, so merged and
			// sources must agree that a value was set even when it's not a
			// usable one.
			merged["rebase"] = vv
		}
		sources["rebase"] = "config"
	} else {
		merged["rebase"] = "auto"
		sources["rebase"] = "default"
	}

	// Numeric timing/iteration knobs: config int > default.
	mergeShipInt(merged, sources, "verifyPipelineTimeout", cfg, shipmeta.ShipBuiltInDefaults.VerifyPipelineTimeout)
	mergeShipInt(merged, sources, "verifyPipelineInterval", cfg, shipmeta.ShipBuiltInDefaults.VerifyPipelineInterval)
	mergeShipInt(merged, sources, "verifyPipelineMaxIterations", cfg, shipmeta.ShipBuiltInDefaults.VerifyPipelineMaxIterations)
	mergeShipInt(merged, sources, "awaitRemoteReviewTimeout", cfg, shipmeta.ShipBuiltInDefaults.AwaitRemoteReviewTimeout)
	mergeShipInt(merged, sources, "awaitRemoteReviewInterval", cfg, shipmeta.ShipBuiltInDefaults.AwaitRemoteReviewInterval)
	mergeShipInt(merged, sources, "executeWaveTimeout", cfg, shipmeta.ShipBuiltInDefaults.ExecuteWaveTimeout)
	mergeShipInt(merged, sources, "executeWaveInterval", cfg, shipmeta.ShipBuiltInDefaults.ExecuteWaveInterval)

	// awaitRemoteReviewers: config non-empty array > default.
	if arr, ok := cfg["awaitRemoteReviewers"].([]any); ok && len(arr) > 0 {
		merged["awaitRemoteReviewers"] = anyToStringSlice(arr)
		sources["awaitRemoteReviewers"] = "config"
	} else {
		merged["awaitRemoteReviewers"] = append([]string{}, shipmeta.ShipBuiltInDefaults.AwaitRemoteReviewers...)
		sources["awaitRemoteReviewers"] = "default"
	}

	// execute.commitWaves: config bool > default false, with an
	// invalid-type marker warning consumed by validation.
	merged["executeCommitWaves"] = false
	sources["executeCommitWaves"] = "default"
	if execCfg, ok := cfg["execute"].(map[string]any); ok {
		if v, exists := execCfg["commitWaves"]; exists {
			if b, ok := v.(bool); ok {
				merged["executeCommitWaves"] = b
				sources["executeCommitWaves"] = "config"
			} else {
				merged["commitWavesInvalidType"] = true
			}
		}
	}

	// Pure passthrough flags (no config/default tier in source).
	merged["hasPlan"] = in.HasPlan
	merged["dryRun"] = in.DryRun
	merged["resume"] = in.Resume
	merged["quick"] = in.Quick
	merged["planModeBlocked"] = in.PlanModeBlocked
	merged["hookActivePipeline"] = in.HookActivePipeline
	merged["skipConfigCheck"] = in.SkipConfigCheck
	if in.OpenspecChange != "" {
		merged["openspecChange"] = in.OpenspecChange
	} else {
		merged["openspecChange"] = nil
	}

	return merged, sources
}

// mergeShipBool merges a boolean flag using cli(true) > config > default
// precedence, matching mergeFlags' handling of `auto`/`draft`.
func mergeShipBool(merged map[string]any, sources map[string]string, key string, cliVal bool, cfg map[string]any, def bool) {
	if cliVal {
		merged[key] = true
		sources[key] = "cli"
		return
	}
	if v, ok := cfg[key].(bool); ok {
		merged[key] = v
		sources[key] = "config"
		return
	}
	merged[key] = def
	sources[key] = "default"
}

// mergeShipInt merges an integer config knob using config > default
// precedence (none of these have a CLI flag in source).
func mergeShipInt(merged map[string]any, sources map[string]string, key string, cfg map[string]any, def int) {
	if v, ok := cfgInt(cfg, key); ok {
		merged[key] = v
		sources[key] = "config"
		return
	}
	merged[key] = def
	sources[key] = "default"
}

// cfgInt reads an integer-valued config field. Config is decoded from JSON,
// so numbers surface as float64; this also accepts a literal int for
// robustness against non-JSON-sourced maps (e.g. test fixtures).
func cfgInt(cfg map[string]any, key string) (int, bool) {
	switch v := cfg[key].(type) {
	case float64:
		return int(v), true
	case int:
		return v, true
	default:
		return 0, false
	}
}

// sliceContainsStr reports whether ss contains s.
func sliceContainsStr(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}

// shipStateFileRe matches a ship-prefixed state file basename, mirroring
// state.go's unexported parseStateFilename grammar
// (^(ship|execute|plan|commit)-(.+)-(\d{8}T\d{6}Z)\.json$) narrowed to the
// "ship" prefix. The greedy (.+) capture must match parseStateFilename's
// exactly, so that the "about to be pruned" set computed here coincides with
// the set state.Write actually prunes (exact slug equality, not a loose
// prefix match — a loose match would over-report files whose slug merely
// starts with branchSlug, e.g. "feat-x-2" when branchSlug is "feat-x").
var shipStateFileRe = regexp.MustCompile(`^ship-(.+)-\d{8}T\d{6}Z\.json$`)

// existingShipStateFiles lists the absolute paths of pre-existing
// ship-<slug>-*.json state files for the given branch slug, matching the set
// that state.Write is about to prune. Must be called before state.Init
// creates the new file, so its own path is never included.
func existingShipStateFiles(root, branchSlug string) ([]string, error) {
	dir := filepath.Join(root, paths.DataDir, paths.RunsSubdir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, err
	}

	out := []string{}
	for _, e := range entries {
		name := e.Name()
		m := shipStateFileRe.FindStringSubmatch(name)
		if m != nil && m[1] == branchSlug {
			out = append(out, filepath.Join(dir, name))
		}
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// --gc short-circuit
// ---------------------------------------------------------------------------

// shipGC implements ship.js's --gc short-circuit (R39): on-demand pruning of
// stale ship/execute/plan/commit state files and sdlc-explore-* tempdirs.
// The caller (shipPrepare) invokes this only after the KD5 gate has already
// passed, matching source order.
//
// ship.js also has an `if (errors.length > 0) { ...write and return... }`
// guard here for CLI arg-parse errors (invalid --ttl-days, rejected
// --preset/--skip/--workspace). That guard has no analogue in this Go tool:
// ShipPrepareIn is already-parsed JSON, not raw argv — TtlDays is a typed
// *int (cannot be a NaN-like value), and --preset/--skip/--workspace have no
// corresponding input fields at all (consistent with this file's existing
// hard-removed-flags convention). So there is nothing for this branch to
// guard against; it is intentionally not ported, not a gap.
// migrationReport, when non-nil, is threaded through from the KD5 gate that
// ran (successfully) just before this short-circuit, so a gc-mode response
// still surfaces an auto-migration the same way the normal path does.
func shipGC(cfgRoot, activeRoot string, in ShipPrepareIn, migrationReport *MigrationReport) ShipPrepareOut {
	ttlDays := resolveGCTTLDays(cfgRoot, in.TtlDays)

	branchExists := gcBranchExistsFunc(activeRoot)

	rpt, err := state.GC(cfgRoot, state.GCOptions{
		TTL:          time.Duration(ttlDays) * 24 * time.Hour,
		BranchExists: branchExists,
		TempDir:      os.Getenv("SDLC_EXPLORE_TMPDIR_OVERRIDE"),
	})
	if err != nil {
		out := ShipPrepareOut{
			Action:    "gc",
			Errors:    []string{fmt.Sprintf("gc failed: %s", err.Error())},
			Warnings:  []string{},
			Migration: migrationReport,
		}
		out.Next = shipPrepareNext(out)
		return out
	}

	report := &ShipGCReport{
		TtlDays:         ttlDays,
		Ship:            bucketGCByPrefix(rpt, "ship"),
		Execute:         bucketGCByPrefix(rpt, "execute"),
		Plan:            bucketGCByPrefix(rpt, "plan"),
		Commit:          bucketGCByPrefix(rpt, "commit"),
		ExploreTempdirs: ShipGCBucket{Deleted: nonNilStrings(rpt.TempdirsDeleted), Kept: nonNilStrings(rpt.TempdirsKept)},
	}

	out := ShipPrepareOut{
		Action:    "gc",
		Report:    report,
		Errors:    []string{},
		Warnings:  []string{},
		Migration: migrationReport,
	}
	out.Next = shipPrepareNext(out)
	return out
}

// resolveGCTTLDays resolves --ttl-days per ship.js: CLI value > config
// state.gc.ttlDays (only if a finite number >= 0) > default 7. "state" is
// not a config.ProjectSections entry, so it lives in local.json (same as
// "ship"). A CLI value of 0 is a deliberate "prune immediately" request and
// must be returned verbatim (the nil check below, not a `cliTTL != 0` or
// falsy check, is what makes that possible) — matching ship.js's `typeof
// cli.ttlDays === 'number'` gate, which likewise treats 0 as present, not
// absent.
func resolveGCTTLDays(cfgRoot string, cliTTL *int) int {
	if cliTTL != nil {
		return *cliTTL
	}
	if stateCfg, _ := config.ReadSection(cfgRoot, "state"); stateCfg != nil {
		if gcCfg, ok := stateCfg["gc"].(map[string]any); ok {
			if v, ok := cfgInt(gcCfg, "ttlDays"); ok && v >= 0 {
				return v
			}
		}
	}
	return 7
}

// gcBranchExistsFunc builds a state.GCOptions.BranchExists closure over the
// repo's current local branches, matching ship.js's knownBranches (`git
// branch --list --format='%(refname:short)'`) + slugifyBranch/liveSlugs
// convention. Branch listing is soft-fail: if the shell-out fails, every
// branch is treated as non-existent (nil is NOT used here, since nil means
// "no liveness information, assume live" — the opposite of source's
// behavior when knownBranches ends up empty).
func gcBranchExistsFunc(activeRoot string) func(slug string) bool {
	out, err := execx.Run("git", []string{"branch", "--list", "--format=%(refname:short)"}, execx.Options{Dir: activeRoot})
	live := map[string]bool{}
	if err == nil {
		for _, line := range strings.Split(out, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			live[state.SlugifyBranch(line)] = true
		}
	}
	return func(slug string) bool {
		return live[slug]
	}
}

// bucketGCByPrefix filters a state.GCReport's flat Deleted/Kept path lists
// down to the entries whose basename starts with "<prefix>-", matching
// ship.js's per-prefix gcStateFiles({prefix: ...}) calls.
func bucketGCByPrefix(rpt *state.GCReport, prefix string) ShipGCBucket {
	return ShipGCBucket{
		Deleted: filterPathsByPrefix(rpt.Deleted, prefix),
		Kept:    filterPathsByPrefix(rpt.Kept, prefix),
	}
}

func filterPathsByPrefix(paths []string, prefix string) []string {
	out := []string{}
	needle := prefix + "-"
	for _, p := range paths {
		if strings.HasPrefix(filepath.Base(p), needle) {
			out = append(out, p)
		}
	}
	return out
}

// nonNilStrings coalesces a nil slice to an empty one so JSON marshaling
// emits [] instead of null. gcTempdirs (unlike GC's own Deleted/Kept, which
// it always initializes) returns nil, nil on a ReadDir error and builds its
// results by append from nil otherwise, so a zero-match sweep would
// otherwise serialize as null — ship.js's gcTempdirs always emits {deleted:
// [], kept: []}.
func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// ---------------------------------------------------------------------------
// ship_verify_side_effect
// ---------------------------------------------------------------------------

// shipPRForBranch is a seam over ghx.PRForBranch so tests can stub PR
// lookups without a real gh binary or a live PR.
var shipPRForBranch = ghx.PRForBranch

// shipHeadSHA returns the current HEAD commit sha, matching the `git
// rev-parse HEAD` pattern already used by commit.go's commitApply. gitx has
// no equivalent helper (and is out of this task's edit scope), so this
// shells out directly via execx, the same chokepoint gitx itself uses.
func shipHeadSHA(dir string) (string, error) {
	out, err := execx.Run("git", []string{"rev-parse", "HEAD"}, execx.Options{Dir: dir})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// shipSideEffectEntry returns the sideEffects journal entry for step, if
// data["sideEffects"] holds one.
func shipSideEffectEntry(data map[string]any, step string) (map[string]any, bool) {
	journal, _ := data["sideEffects"].(map[string]any)
	if journal == nil {
		return nil, false
	}
	entry, ok := journal[step].(map[string]any)
	return entry, ok
}

// shipSideEffectRef returns the ref recorded in step's journal entry, if
// any.
func shipSideEffectRef(data map[string]any, step string) (string, bool) {
	entry, ok := shipSideEffectEntry(data, step)
	if !ok {
		return "", false
	}
	ref, _ := entry["ref"].(string)
	return ref, ref != ""
}

// shipRecordSideEffect writes a {kind, ref, verifiedAt} entry into
// data["sideEffects"][step], creating the journal map on first use. Callers
// must only call this when the side effect is confirmed landed: an entry's
// mere presence is the "verified" signal shipStateBeginStep's alreadyDone
// check (ship_state.go) relies on — recording an unverified observation
// here would make that check lie.
func shipRecordSideEffect(data map[string]any, step, kind, ref string, verifiedAt time.Time) {
	journal, _ := data["sideEffects"].(map[string]any)
	if journal == nil {
		journal = map[string]any{}
	}
	journal[step] = map[string]any{
		"kind":       kind,
		"ref":        ref,
		"verifiedAt": verifiedAt.UTC().Format(time.RFC3339),
	}
	data["sideEffects"] = journal
}

// shipSoftFindState resolves the ship state for activeRoot's current branch,
// for sideEffects journal read/write. Unlike shipFindState (ship_state.go),
// an unresolvable branch or a missing state file is not an error here: it
// returns nil, and the caller treats that as "nothing to persist against."
// ship_verify_side_effect must stay usable standalone (e.g. right after
// commit_apply/pr_apply, before any ship_state init has run for this
// branch) — the 4 pre-existing tests for the "tag" kind rely on exactly
// this soft-fail behavior, since none of them set up a ship state fixture.
func shipSoftFindState(root, activeRoot string) *state.State {
	branch, err := gitx.CurrentBranch(activeRoot)
	if err != nil || branch == "" {
		return nil
	}
	st, err := state.Find(root, "ship", branch)
	if err != nil || st == nil {
		return nil
	}
	return st
}

// shipVerifySideEffect ports and generalizes verifySideEffect from
// scripts/skill/ship.js. It deliberately performs no KD5 config-version
// check, matching source (the verify-side-effect subcommand short-circuits
// main() before any config/gh setup).
//
// Beyond the original single "version"->tag check, this also verifies "pr"
// (does an open PR exist for the branch) and "commit" (has HEAD advanced to
// a known sha), and persists every landed observation into the ship state's
// sideEffects journal (root/activeRoot both resolve via RegisterShipTools,
// mirroring ship_prepare's dual-root pattern) so a resumed pipeline can
// tell, via ship_state's begin-step alreadyDone flag, that a step's side
// effect already landed before a crash/restart. A step with no entry in
// shipStepSideEffects (e.g. "version", now folded into the pr step's
// diagnostics) is reported as landed with reason "no-side-effect" via the
// hasSideEffect check below.
//
// "commit" (kind "sha") has no natural caller-supplied comparison value the
// way "tag" does in ship.js — ship.js's tag check always takes an explicit
// --expected tag computed by the version step itself. Two modes are
// supported, chosen by whether Expected is supplied:
//   - Expected given: landed = (HEAD sha == Expected) — the write path,
//     called right after a commit with the sha the caller just produced,
//     mirroring the tag kind exactly.
//   - Expected omitted: landed = (HEAD sha == the previously journaled
//     ref for this step), the literal "checks HEAD sha vs the previously
//     recorded sha" resume-path check. With no journal entry either,
//     there is nothing to confirm against, so landed is false — recording
//     an ambient HEAD sha as "verified" on a bare bootstrap call would let
//     a resumed pipeline believe a commit step landed when it may never
//     have run at all.
//
// A journal entry is written (or refreshed) only when landed is true, for
// both kinds: an entry's presence is meant to mean "verified", and only
// writing on success keeps that invariant, and keeps repeated resume-time
// checks stable once a step is confirmed (no flip-flopping).
func shipVerifySideEffect(root, activeRoot string, in ShipVerifySideEffectIn, now func() time.Time) (ShipVerifySideEffectOut, error) {
	kind, hasSideEffect := shipStepSideEffects[in.Step]
	if !hasSideEffect {
		return ShipVerifySideEffectOut{
			Step:   in.Step,
			Landed: true,
			Reason: "no-side-effect",
			Next:   "No side effect to verify. Proceed to the next pipeline step.",
		}, nil
	}

	st := shipSoftFindState(root, activeRoot)

	var landed bool
	var ref string

	switch kind {
	case "pr":
		meta := shipPRForBranch(activeRoot)
		if meta.Exists {
			landed = true
			ref = fmt.Sprintf("#%d", meta.Number)
		}

	case "sha":
		headSHA, err := shipHeadSHA(activeRoot)
		if err != nil {
			return ShipVerifySideEffectOut{}, &mcpserver.InfraError{
				Msg:   fmt.Sprintf("git rev-parse HEAD: %s", err.Error()),
				Cause: err,
			}
		}
		switch {
		case in.Expected != "":
			landed = headSHA == in.Expected
		case st != nil:
			if prevRef, ok := shipSideEffectRef(st.Data, in.Step); ok {
				landed = headSHA == prevRef
			}
		}
		if landed {
			ref = headSHA
		}
	}

	if landed && st != nil {
		shipRecordSideEffect(st.Data, in.Step, kind, ref, now())
		if err := state.Write(st); err != nil {
			return ShipVerifySideEffectOut{}, &mcpserver.InfraError{
				Msg:   fmt.Sprintf("write ship state: %s", err.Error()),
				Cause: err,
			}
		}
	}

	var expected *string
	if in.Expected != "" {
		expected = &in.Expected
	}

	next := "Side effect not yet landed. Retry or investigate."
	if landed {
		next = "Side effect verified. Proceed to the next pipeline step."
	}

	return ShipVerifySideEffectOut{
		Step:       in.Step,
		SideEffect: kind,
		Landed:     landed,
		Expected:   expected,
		Next:       next,
	}, nil
}

// ---------------------------------------------------------------------------
// Registration
// ---------------------------------------------------------------------------

// RegisterShipTools registers the ship_prepare and ship_verify_side_effect
// tools.
func RegisterShipTools(s *mcpserver.Server) {
	mcpserver.Register(s, "ship_prepare",
		"Merge ship CLI flags with ship config, validate the resolved pipeline (pure checks only — no gh-auth or openspec-aware step computation), and initialize ship execution state.",
		func(ctx mcpserver.Ctx, in ShipPrepareIn) (ShipPrepareOut, error) {
			root, err := worktree.MainRoot()
			if err != nil {
				return ShipPrepareOut{}, &mcpserver.InfraError{
					Msg:   fmt.Sprintf("resolve project root: %s", err.Error()),
					Cause: err,
				}
			}
			activeRoot, err := worktree.ActiveRoot()
			if err != nil {
				activeRoot = root
			}
			return shipPrepare(root, activeRoot, in)
		},
	)

	mcpserver.Register(s, "ship_verify_side_effect",
		"Verify that a ship pipeline step's expected side effect (a PR or commit sha) actually landed, and record it in the ship state's sideEffects journal for idempotent resume.",
		func(ctx mcpserver.Ctx, in ShipVerifySideEffectIn) (ShipVerifySideEffectOut, error) {
			root, err := worktree.MainRoot()
			if err != nil {
				return ShipVerifySideEffectOut{}, &mcpserver.InfraError{
					Msg:   fmt.Sprintf("resolve project root: %s", err.Error()),
					Cause: err,
				}
			}
			activeRoot, err := worktree.ActiveRoot()
			if err != nil {
				activeRoot = root
			}
			return shipVerifySideEffect(root, activeRoot, in, time.Now)
		},
	)
}
