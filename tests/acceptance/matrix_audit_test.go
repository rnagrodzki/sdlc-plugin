// Package acceptance is Task 49's capstone verification: it does not port
// any more source behavior, it audits that the migration as a whole has a
// Go test standing behind every acceptance surface identified during
// discovery and planning.
//
// Two independent surfaces are audited here, each as its own table-driven
// test so a failure in one never masks the other:
//
//  1. TestPromptfooDatasetMatrix — every *-exec.yaml dataset in the source
//     repo's promptfoo deterministic-execution tier (sdlc-marketplace), one
//     row per dataset, sourced from the frozen manifest at
//     testdata/promptfoo-datasets.txt (Task 49 Open Question 3). The
//     manifest is committed so this package never depends on
//     sdlc-marketplace as a runtime path.
//
//  2. TestKeyDecisionAudit — the 17 Key Decisions (KD1-KD17) and 4
//     Contradictions from the migration's planning documents, one row
//     each. See the comment above ttKeyDecisionRows for why these 21 are
//     the audited surface and not the full 253-finding discovery corpus.
//
// Both tables resolve to one of two outcomes per row:
//
//   - covered: testRef names a real `func TestXxx` somewhere in the repo's
//     internal/ or cmd/ trees, confirmed by an in-process source scan
//     equivalent to `grep -rl '^func TestXxx('` (Task 49 Open Question 4 —
//     existence check only, no reflection, no subprocess-per-row runner,
//     matching the internal/skillcheck package's grep-based idiom).
//   - cut: rationale explains why no Go test covers this row — either the
//     underlying behavior was deliberately not ported, or it is out of
//     the shipped plugin's runtime scope. A cut row asserts nothing about
//     Go source; it exists so the row is accounted for at all.
//
// A row that is neither covered by a real test nor explicitly cut is a
// bug in this file, not a gap in the migration — that is what "fail
// loudly" means here.
package acceptance

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
)

// ---------------------------------------------------------------------
// Shared repo-source scan: builds the set of every `func TestXxx` defined
// anywhere under internal/ and cmd/, keyed by function name. This mirrors
// `grep -rl '^func TestXxx('` per Open Question 4 but does it once, in
// process, for every row instead of shelling out per row.
// ---------------------------------------------------------------------

var (
	testFuncsOnce sync.Once
	testFuncsErr  error
	// testFuncFiles maps a Go test function name (e.g. "TestBump") to the
	// file it is defined in, relative to the repo root.
	testFuncFiles map[string]string
)

var testFuncDeclRe = regexp.MustCompile(`(?m)^func\s+(Test[A-Za-z0-9_]+)\s*\(`)

// repoRoot resolves the repository root from this test package's working
// directory (tests/acceptance), the same two-levels-up convention used by
// tests/integration/pipeline_smoke_test.go.
func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd: %v", err)
	}
	root, err := filepath.Abs(filepath.Join(wd, "..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	return root
}

// loadTestFuncs walks internal/ and cmd/ under root, scanning every
// *_test.go file for top-level `func TestXxx(` declarations.
func loadTestFuncs(root string) (map[string]string, error) {
	funcs := make(map[string]string)
	for _, sub := range []string{"internal", "cmd"} {
		dir := filepath.Join(root, sub)
		err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() || !strings.HasSuffix(path, "_test.go") {
				return nil
			}
			data, rerr := os.ReadFile(path)
			if rerr != nil {
				return rerr
			}
			rel, rerr := filepath.Rel(root, path)
			if rerr != nil {
				rel = path
			}
			for _, m := range testFuncDeclRe.FindAllStringSubmatch(string(data), -1) {
				name := m[1]
				if _, exists := funcs[name]; !exists {
					funcs[name] = rel
				}
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return funcs, nil
}

// funcExists reports whether name is declared as a top-level test function
// somewhere under internal/ or cmd/, and the file it was found in (for
// failure messages).
func funcExists(t *testing.T, name string) (string, bool) {
	t.Helper()
	testFuncsOnce.Do(func() {
		testFuncFiles, testFuncsErr = loadTestFuncs(repoRoot(t))
	})
	if testFuncsErr != nil {
		t.Fatalf("scanning repo for test functions: %v", testFuncsErr)
	}
	file, ok := testFuncFiles[name]
	return file, ok
}

// ---------------------------------------------------------------------
// Section 1: promptfoo dataset matrix (60 rows).
// ---------------------------------------------------------------------

// datasetRow is one *-exec.yaml dataset from the frozen manifest.
//
// Exactly one of testRef / cutReason is set:
//   - testRef: the covering Go test's bare function name (existence
//     verified via funcExists).
//   - cutReason: why no Go test covers this dataset.
type datasetRow struct {
	dataset   string
	testRef   string
	cutReason string
}

// datasetRows is the per-dataset disposition for all 60 files in
// testdata/promptfoo-datasets.txt. Findings below reflect direct source
// verification performed for Task 49, not the discovery brief's Finding 5
// alone — Finding 5 established the 60-file count and the one orphan
// (error-report-prepare-exec.yaml); determining Go-side coverage for the
// other 59 was this task's own job (its Acceptance Criterion 3), and that
// research surfaced more gaps than Finding 5's count breakdown implied.
//
// 51 covered, 9 cut. Of the 9 cuts, 4 are coverage gaps this audit's own
// per-dataset research surfaced (required by AC #3, not settled by Finding
// 5's count alone) and are called out individually below and in the
// task-49 report. Two of the four were originally mis-described in this
// comment/cutReason text (corrected after an independent spec-compliance
// review caught both — see task-49-report.md addendum):
//   - guardrails-prepare-exec.yaml: the JS guardrails.js source function
//     (detectLanguagesAndFrameworks/detectStructure/detectReviewDimensions)
//     that scans a repo to auto-propose guardrails was never ported. Go's
//     loadGuardrails (internal/tools/plan.go) only reads an already-authored
//     guardrails array from config — a materially smaller feature.
//   - version-retag-exec.yaml: no `--retag` equivalent exists anywhere in
//     internal/version or the commit/pr tools (verified: zero "retag" hits
//     outside an unrelated CI payload template name) — but this is NOT a
//     new finding: skills/version-sdlc/SKILL.md's own Port Notes already
//     self-document `--retag` as a known no-op. The gap is real; "found by
//     this audit" was wrong.
//   - received-review-prepare-exec.yaml: the tool is ported and registered
//     (RegisterReceivedReviewTools, internal/tools/received_review.go) but
//     has no dedicated unit test anywhere in the suite.
//   - execute-plan-sdlc-dispatch-resilience-exec.yaml: the sentinel string
//     this dataset checks for IS present verbatim at
//     skills/execute-plan-sdlc/SKILL.md:313 ("Nested Agent dispatch is
//     supported...") — the original cutReason's "absent... anywhere" claim
//     was false. The real, narrower gap: the dataset's second required
//     file, wave-runner-template.md, does not exist in this port because
//     the wave-runner middle-agent was collapsed into flat dispatch (KD15).
var datasetRows = []datasetRow{
	{dataset: "await-remote-review-exec.yaml", testRef: "TestAwaitRemoteReview_PendingThenResume"},
	{dataset: "branch-guard-exec.yaml", testRef: "TestPrPrepare_BranchGuardMismatch_HardGate"},
	{dataset: "branch-name-exec.yaml", testRef: "TestResolve"},
	{dataset: "commit-prepare-exec.yaml", testRef: "TestCommitPrepare_KeySet"},
	{dataset: "compact-recovery-sweep-exec.yaml", testRef: "TestCompactRecoveryPhase_StaleSweep"},
	{dataset: "compact-recovery-write-exec.yaml", testRef: "TestPreCompactSave_SavedAtFromMtimeNotNow"},
	{dataset: "config-load-exec.yaml", testRef: "TestRead_MergesProjectAndLocal"},
	{dataset: "config-resolve-sdlc-root-exec.yaml", testRef: "TestReadAnchorsAtMainWorktreeRoot"},
	{
		dataset: "context-advisory-exec.yaml",
		testRef: "TestRun_RegistryContainsExactlyKnownHooks",
		// Contradiction #3: 3 SKILL.md files describe a live context-stats.js
		// / context-advisory sidecar mechanism, but hooks.json wires no such
		// hook and the file is absent from disk in the source repo itself.
		// The Go hook registry's closed, exactly-8-entry list is the direct
		// Go-side proof that no such wiring was (or should be) ported.
	},
	{dataset: "cwd-drift-exec.yaml", testRef: "TestMainRootIn_LinkedWorktree"},
	{dataset: "derive-workspace-exec.yaml", testRef: "TestDeriveWorkspace_LinkedWorktree"},
	{dataset: "dimension-to-instructions-exec.yaml", testRef: "TestToInstructions_WithCommon"},
	{
		dataset:   "error-report-prepare-exec.yaml",
		cutReason: "orphan in the source repo itself (Finding 5): referenced by zero promptfoo exec config despite matching the -exec.yaml naming convention — a pre-existing source defect, not something the Go port could address.",
	},
	{dataset: "execute-plan-sdlc-context-producer-exec.yaml", testRef: "TestExecState_SummarizePriorWaveContext"},
	{
		dataset:   "execute-plan-sdlc-dispatch-resilience-exec.yaml",
		cutReason: "the dataset's sentinel string (\"Nested Agent dispatch is supported...\") IS present verbatim at skills/execute-plan-sdlc/SKILL.md:313 — no gap there. The narrower real gap: the dataset's second required file, wave-runner-template.md, does not exist in this port, because the wave-runner middle-agent this port's KD15 collapsed into flat dispatch. No Go test encodes that collapsed-dispatch behavior as a standalone assertion. Flagged in the task-49 report as follow-up-worthy, not fixed here (out of this task's scope).",
	},
	{dataset: "execute-plan-sdlc-liveness-exec.yaml", testRef: "TestExecState_ResumeReset"},
	{dataset: "execute-plan-sdlc-overflow-exec.yaml", testRef: "TestSplit_RefusesBeyondMaxDepth"},
	{dataset: "git-lib-exec.yaml", testRef: "TestStatus_DirtyTree"},
	{
		dataset:   "guardrails-prepare-exec.yaml",
		cutReason: "genuine gap found by this audit: source guardrails.js's detectLanguagesAndFrameworks/detectStructure/detectReviewDimensions (repo-scanning auto-detection of frameworks/DBs/APIs to propose guardrails, issues #137/#138/#139) has no Go equivalent. Go's loadGuardrails (internal/tools/plan.go) only passes through an already-authored config guardrails array, confirmed via source read — a narrower, already-covered feature (see TestPlanPrepare_Guardrails), not this dataset's actual subject.",
	},
	{dataset: "harden-prepare-exec.yaml", testRef: "TestHardenPrepare_ManifestFieldFidelity"},
	{
		dataset:   "harvest-learnings-exec.yaml",
		cutReason: "repo-maintenance script (harvest-learnings.js) with zero references anywhere in this plugin's runtime tool/hook surface — it operates on the marketplace repo's own contributor workflow, not shipped plugin behavior.",
	},
	{dataset: "hook-stop-plan-integrity-exec.yaml", testRef: "TestStopPlanIntegrity_AllMarkersPresent_ConsumesDeletesSilently"},
	{dataset: "jira-sdlc-exec.yaml", testRef: "TestJiraSaveThenLoadRoundTrip"},
	{
		dataset: "jira-sdlc-guardrail-exec.yaml",
		testRef: "TestRun_RegistryContainsExactlyKnownHooks",
		// Contradiction #2: jira-sdlc/SKILL.md self-contradicts on whether a
		// pre-tool-jira-write-guard hook is live; ground truth (hooks.json,
		// and this Go registry) is that it never was. The guard's helper
		// libs (payload-hash.js etc.) were confirmed dead code in the
		// source itself, required only by the guard's own test harness.
	},
	{dataset: "lib-worktree-exec.yaml", testRef: "TestMainRootIn_SingleWorktree"},
	{dataset: "links-lib-exec.yaml", testRef: "TestValidate_MultipleURLs"},
	{dataset: "markdown-to-adf-exec.yaml", testRef: "TestConvert_GoldenCorpus"},
	{dataset: "migrate-config-exec.yaml", testRef: "TestMigrate_ConfigAction_V4ToV5"},
	{dataset: "migrate-jira-templates-exec.yaml", testRef: "TestMigrate_JiraTemplates_Move"},
	{dataset: "openspec-enrich-exec.yaml", testRef: "TestEnrichConfig_AppendNewBlock"},
	{dataset: "openspec-lib-exec.yaml", testRef: "TestDetect_ChangeList"},
	{dataset: "plan-format-exec.yaml", testRef: "TestValidatePlanFormatAllChecksPass"},
	{dataset: "plan-prepare-exec.yaml", testRef: "TestPlanPrepare_KeySetAndDefaults"},
	{dataset: "plan-state-cleanup-exec.yaml", testRef: "TestWrite_PrunesOldFiles"},
	{dataset: "pr-prepare-exec.yaml", testRef: "TestPrPrepare_HappyPath_JiraAndTemplate"},
	{dataset: "pr-recover-gh-account-exec.yaml", testRef: "TestPrPrepare_AccountMismatch_EmbedsAccountDiagnostics"},
	{
		dataset:   "received-review-prepare-exec.yaml",
		cutReason: "genuine gap found by this audit: received_review_prepare is ported and registered (RegisterReceivedReviewTools, internal/tools/received_review.go) but has zero dedicated unit tests — only incidental markdown-wiring references in internal/skillcheck. Flagged in the task-49 report as a coverage gap, not fixed here (out of this task's scope).",
	},
	{dataset: "review-prepare-exec.yaml", testRef: "TestReviewPrepareFixture"},
	{
		dataset:   "script-runner-stub-bin-exec.yaml",
		cutReason: "exercises promptfoo's own test-harness script-runner provider infrastructure, not plugin behavior — nothing in this repo to cover.",
	},
	{dataset: "sdlc-dimensions-exec.yaml", testRef: "TestLoad"},
	{dataset: "sdlc-plugin-discovery-exec.yaml", testRef: "TestValidateAll_GoodFixture"},
	{dataset: "sdlc-pr-template-exec.yaml", testRef: "TestResolve_Canonical"},
	{
		dataset:   "sdlc-script-resolution-exec.yaml",
		cutReason: "tests a marketplace-repo-only dev-tooling skill (script path consistency across .claude/skills) that has no counterpart in the shipped single-binary plugin — there are no script paths left to resolve.",
	},
	{dataset: "session-start-exec.yaml", testRef: "TestRun_SessionStart_GoldenSources"},
	{dataset: "setup-init-exec.yaml", testRef: "TestSetupInit_V5SchemaCompliant"},
	{dataset: "setup-migrate-dispatch-exec.yaml", testRef: "TestMigrate_UnknownAction"},
	{dataset: "setup-prepare-exec.yaml", testRef: "TestSetupPrepare_ReturnsSections"},
	{
		dataset:   "setup-script-versions-exec.yaml",
		cutReason: "the per-script version-drift concept this dataset checks (are all scripts.js files internally consistent about their own version) does not exist once everything is one Go binary versioned as a single unit.",
	},
	{dataset: "ship-hooks-exec.yaml", testRef: "TestStopPipelineContinue_BlockCountCapThenMarksFailedOnce"},
	{dataset: "ship-prepare-exec.yaml", testRef: "TestShipPrepare_StateInit"},
	{dataset: "ship-sdlc-exec.yaml", testRef: "TestShipState_StartComplete"},
	{dataset: "ship-todos-exec.yaml", testRef: "TestShipState_Todos"},
	{dataset: "state-gc-migrate-exec.yaml", testRef: "TestMigrateBranchSlug"},
	{dataset: "validate-cost-tiers-exec.yaml", testRef: "TestValidateCostTiersAllKinds"},
	{dataset: "verify-pipeline-exec.yaml", testRef: "TestVerifyPipelineAwait_PendingThenGreen"},
	{dataset: "verify-tag-ancestry-exec.yaml", testRef: "TestVerifyTagAncestry_PassOnAncestor"},
	{dataset: "version-lib-exec.yaml", testRef: "TestParseSemver"},
	{dataset: "version-prepare-exec.yaml", testRef: "TestVersionPrepare_Basic"},
	{dataset: "version-prerelease-exec.yaml", testRef: "TestBump"},
	{
		dataset:   "version-retag-exec.yaml",
		cutReason: "no --retag equivalent exists anywhere in internal/version or the commit/pr tools (verified: zero \"retag\" hits outside an unrelated CI payload template literally named retag-release.cjs). Not a new finding: skills/version-sdlc/SKILL.md's own Port Notes already self-document --retag as a known no-op. Flagged in the task-49 report as an already-documented migration gap, not fixed here (out of this task's scope).",
	},
}

// readManifest parses testdata/promptfoo-datasets.txt, skipping the header
// comment block and blank lines, and returns the dataset filenames in
// file order.
func readManifest(t *testing.T, root string) []string {
	t.Helper()
	path := filepath.Join(root, "tests", "acceptance", "testdata", "promptfoo-datasets.txt")
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open manifest %s: %v", path, err)
	}
	defer f.Close()

	var names []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		names = append(names, line)
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan manifest %s: %v", path, err)
	}
	return names
}

// TestPromptfooDatasetMatrix asserts the frozen manifest and datasetRows
// are exactly consistent (every manifest line has a row, every row has a
// manifest line, no duplicates), that there are exactly 60 rows, and that
// every non-cut row's testRef resolves to a real Go test function.
func TestPromptfooDatasetMatrix(t *testing.T) {
	root := repoRoot(t)
	manifest := readManifest(t, root)

	if len(manifest) != 60 {
		t.Fatalf("manifest has %d dataset lines, want exactly 60", len(manifest))
	}
	if len(datasetRows) != 60 {
		t.Fatalf("datasetRows has %d entries, want exactly 60", len(datasetRows))
	}

	manifestSet := make(map[string]bool, len(manifest))
	for _, name := range manifest {
		if manifestSet[name] {
			t.Errorf("manifest lists %q more than once", name)
		}
		manifestSet[name] = true
	}

	rowSet := make(map[string]bool, len(datasetRows))
	for _, row := range datasetRows {
		if rowSet[row.dataset] {
			t.Errorf("datasetRows lists %q more than once", row.dataset)
		}
		rowSet[row.dataset] = true

		if !manifestSet[row.dataset] {
			t.Errorf("datasetRows has %q which is not in testdata/promptfoo-datasets.txt", row.dataset)
		}

		hasCut := row.cutReason != ""
		hasTest := row.testRef != ""
		if hasCut == hasTest {
			t.Errorf("dataset %q must set exactly one of testRef/cutReason (testRef=%q cutReason=%q)", row.dataset, row.testRef, row.cutReason)
			continue
		}

		t.Run(row.dataset, func(t *testing.T) {
			if hasCut {
				t.Skipf("cut: %s", row.cutReason)
				return
			}
			file, ok := funcExists(t, row.testRef)
			if !ok {
				t.Fatalf("dataset %q cites testRef %q but no `func %s(` exists under internal/ or cmd/", row.dataset, row.testRef, row.testRef)
			}
			t.Logf("covered by %s (%s)", row.testRef, file)
		})
	}

	for name := range manifestSet {
		if !rowSet[name] {
			t.Errorf("manifest lists %q but datasetRows has no row for it", name)
		}
	}
}

// ---------------------------------------------------------------------
// Section 2: Key Decision / Contradiction audit (21 rows).
// ---------------------------------------------------------------------

// kdRow is one row of the KD/Contradiction audit table. Same
// exactly-one-of-testRef-or-cutReason contract as datasetRow.
type kdRow struct {
	id        string
	testRef   string
	cutReason string
}

// kdRows is the acceptance surface for Finding 6 of task-49.md: "every
// finding that implies user-visible behavior has at least one
// corresponding Go test." Per this task's binding scope ruling, that
// surface is exactly the 17 Key Decisions (KD1-KD17) from the main plan
// file (~/.claude/plans/ide-is-to-migrate-ethereal-lemur.md, "## Key
// Decisions") plus the 4 numbered Contradictions from the discovery brief
// (~/.claude/plans/ide-is-to-migrate-ethereal-lemur-agent-*.md,
// "## Contradictions") — 21 rows, not the full discovery corpus:
//
//   - G1-G21 are the plan's own documentation-review-lane gate IDs
//     (process/quality checks run on the plan text during authoring).
//     They describe how the plan was reviewed, not what the Go binary
//     does at runtime — there is no corresponding Go behavior to test,
//     so they are not rows here.
//   - R2/R3 are revision-history markers on the plan document. Their
//     substance was fully absorbed into KD2, KD14, KD15, KD16, and KD17
//     by the time the plan was finalized; auditing them as separate rows
//     would duplicate those KD rows rather than cover anything new.
//   - The 253 raw F-<dimension>-<n> findings in the discovery brief are
//     research facts gathered during discovery — the citation basis for
//     the plan's Task Notes, not themselves a separate acceptance
//     surface. Auditing all 253 individually would be disproportionate:
//     the 17 KDs are the synthesized, decision-level distillation of
//     those findings, and the 4 Contradictions are the specific findings
//     that resolved a behavior fork the KDs alone don't fully capture.
//     Together these 21 rows are what is actually testable as
//     acceptance criteria; the F-findings and G/R markers are the
//     paper trail behind them, not additional surface.
//
// 17 covered, 4 cut. None of the 4 cuts are behavior gaps — each is a
// row whose substance is a build-time, naming, or documentation
// convention with no independent runtime branch to unit test (see each
// cutReason below).
var kdRows = []kdRow{
	{
		id:      "KD1",
		testRef: "TestRun_RegistryContainsExactlyKnownHooks",
		// One binary, two modes (`sdlc mcp` / `sdlc hook <name>`). The
		// hook-mode half is proven by this fixed, exactly-8-entry
		// registry (mirrors hooks.json's command-type wiring); the
		// mcp-mode half is exercised end to end by
		// tests/integration/pipeline_smoke_test.go via the built binary.
	},
	{id: "KD2", testRef: "TestMigrate_ProjectV4ToV5"},
	{id: "KD3", testRef: "TestDomainError"},
	{id: "KD4", testRef: "TestReviewPrepareFixture"},
	{id: "KD5", testRef: "TestShipPrepare_NoSessionID"},
	{id: "KD6", testRef: "TestWarningsPerCallAndSessionID"},
	{id: "KD7", testRef: "TestValidateUnknownAction"},
	{id: "KD8", testRef: "TestAwaitRemoteReview_PendingThenResume"},
	{id: "KD9", testRef: "TestCommitSkillsToolReferencesExistInRegistry"},
	{
		id:        "KD10",
		cutReason: "web-finding dispositions (MCP server distribution, hook handler type choice). These are research/architecture decisions realized structurally in .mcp.json and hooks.json, not standalone Go runtime behavior; the one behaviorally-testable subset (hooks stay command-type, not mcp_tool-type) is Contradiction #1 below, covered there.",
	},
	{
		id:        "KD11",
		cutReason: "mirror pointer notation (`src:` shorthand) is a convention for citing source files inside the plan document itself — a documentation authoring aid, not Go runtime behavior.",
	},
	{
		id:        "KD12",
		cutReason: "the two-dependency constraint (mark3labs/mcp-go + yaml.v3, everything else stdlib) is a go.mod/go.sum property enforced by `go build`/`go vet` succeeding, not independent runtime behavior a unit test would exercise.",
	},
	{
		id:        "KD13",
		cutReason: "naming (\"sdlc\" as plugin name, MCP server key, and Go module owner) is fixed by static config (.claude-plugin/plugin.json, .mcp.json, cmd/sdlc/main.go's mcpserver.New(\"sdlc\", ...) call) and implicitly exercised by every other test that talks to the server — there is no independent branch to assert beyond string-literal equality with itself.",
	},
	{id: "KD14", testRef: "TestShipState_Next_HonorsAutomationConfig"},
	{id: "KD15", testRef: "TestExecState_Ledger_RoundTrip"},
	{id: "KD16", testRef: "TestMigrate_ConfigAction_V4ToV5"},
	{id: "KD17", testRef: "TestDeliverSkillsFixLoopConfigFieldsPresent"},
	{
		id:      "Contradiction-1",
		testRef: "TestRun_RegistryContainsExactlyKnownHooks",
		// Hooks stay command-type via the dual-mode binary; mcp_tool-type
		// hook handlers were rejected. There is no second, mcp_tool-type
		// dispatch path anywhere — every hook resolves through this one
		// fixed registry, matching hooks.json's uniform "type": "command"
		// wiring.
	},
	{
		id:      "Contradiction-2",
		testRef: "TestRun_RegistryContainsExactlyKnownHooks",
		// jira-sdlc/SKILL.md self-contradicts on a pre-tool-jira-write-guard
		// hook; ground truth is it was never wired. This registry's closed
		// 8-name list has no such entry.
	},
	{
		id:      "Contradiction-3",
		testRef: "TestRun_RegistryContainsExactlyKnownHooks",
		// 3 SKILL.md files describe a live context-stats.js UserPromptSubmit
		// hook / context-advisory sidecar; ground truth is zero
		// UserPromptSubmit entries exist. Same registry, same proof.
	},
	{id: "Contradiction-4", testRef: "TestVerify_LegacyMarkers"},
	// Config resolution is centralized on .sdlc/config.json as primary
	// with no duplicate-copy drift risk: legacy/alternate config
	// locations are detected and refused (naming the migrate tool) by a
	// single Go package, never silently read as a second live source —
	// the drift the source repo's own CI vs. distributable script copies
	// suffered from structurally cannot recur.
}

// TestKeyDecisionAudit asserts there are exactly 21 KD/Contradiction rows
// (17 Key Decisions + 4 Contradictions, per the scope ruling above) and
// that every non-cut row's testRef resolves to a real Go test function.
func TestKeyDecisionAudit(t *testing.T) {
	if len(kdRows) != 21 {
		t.Fatalf("kdRows has %d entries, want exactly 21 (17 Key Decisions + 4 Contradictions)", len(kdRows))
	}

	seen := make(map[string]bool, len(kdRows))
	kdCount, contradictionCount := 0, 0
	for _, row := range kdRows {
		if seen[row.id] {
			t.Errorf("kdRows lists %q more than once", row.id)
		}
		seen[row.id] = true

		switch {
		case strings.HasPrefix(row.id, "KD"):
			kdCount++
		case strings.HasPrefix(row.id, "Contradiction-"):
			contradictionCount++
		default:
			t.Errorf("kdRows entry %q does not match the KD<n> or Contradiction-<n> naming convention", row.id)
		}

		hasCut := row.cutReason != ""
		hasTest := row.testRef != ""
		if hasCut == hasTest {
			t.Errorf("row %q must set exactly one of testRef/cutReason (testRef=%q cutReason=%q)", row.id, row.testRef, row.cutReason)
			continue
		}

		t.Run(row.id, func(t *testing.T) {
			if hasCut {
				t.Skipf("cut: %s", row.cutReason)
				return
			}
			file, ok := funcExists(t, row.testRef)
			if !ok {
				t.Fatalf("%s cites testRef %q but no `func %s(` exists under internal/ or cmd/", row.id, row.testRef, row.testRef)
			}
			t.Logf("covered by %s (%s)", row.testRef, file)
		})
	}

	if kdCount != 17 {
		t.Errorf("found %d KD rows, want exactly 17", kdCount)
	}
	if contradictionCount != 4 {
		t.Errorf("found %d Contradiction rows, want exactly 4", contradictionCount)
	}
}
