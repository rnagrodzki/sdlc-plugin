package tools

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/rnagrodzki/sdlc-plugin/internal/branch"
	"github.com/rnagrodzki/sdlc-plugin/internal/commstyle"
	"github.com/rnagrodzki/sdlc-plugin/internal/fsx"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/openspec"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
	"github.com/rnagrodzki/sdlc-plugin/internal/worktree"
)

// ---------------------------------------------------------------------------
// Input / Output types
// ---------------------------------------------------------------------------

// Merged review statuses. merge_results returns one of them as
// mergedStatus, and the plan_mark "review-round" marker accepts only these
// two values for mergedStatus and for each lens verdict.
const (
	planStatusApproved    = "Approved"
	planStatusIssuesFound = "Issues Found"
)

// PlanSupportIn carries the merged input for the plan_support tool's 10
// actions. Each field is consumed by one or more actions (noted in comments).
type PlanSupportIn struct {
	Action string `json:"action" jsonschema:"enum=merge_results,enum=material_snapshot,enum=material_compare,enum=openspec_appendix,enum=openspec_instructions,enum=openspec_stage,enum=evidence_record,enum=evidence_digest,enum=evidence_get,enum=preplan_context" jsonschema_description:"Selects the operation: \"merge_results\", \"material_snapshot\", \"material_compare\", \"openspec_appendix\", \"openspec_instructions\", \"openspec_stage\", \"evidence_record\", \"evidence_digest\", \"evidence_get\", or \"preplan_context\". Each action reads only the subset of fields listed in the tool description; unlisted fields are ignored."`

	// merge_results
	LaneResults   []LaneResult `json:"laneResults,omitempty" jsonschema_description:"merge_results only: outcomes from each review lane to merge. At least one of laneResults or lensResults is required."`
	LensResults   []LensResult `json:"lensResults,omitempty" jsonschema_description:"merge_results only: outcomes from each review lens to merge. At least one of laneResults or lensResults is required."`
	ExpectedGates []string     `json:"expectedGates,omitempty" jsonschema_description:"merge_results only: gate IDs expected to be covered by the merged lane/lens results, used to compute coverage gaps."`
	IsRedispatch  bool         `json:"isRedispatch,omitempty" jsonschema_description:"merge_results only: true when these results come from a redispatch (re-run) of lanes/lenses, affecting how merged status is computed."`

	// material_snapshot / material_compare
	FilePath     string `json:"filePath,omitempty" jsonschema_description:"material_snapshot / material_compare only: path to the plan file to snapshot or compare."`
	SnapshotPath string `json:"snapshotPath,omitempty" jsonschema_description:"material_compare only: path to the snapshot file returned by material_snapshot's snapshotPath field, to compare the current plan file against."`

	// openspec_appendix / openspec_instructions / openspec_stage
	ChangeName   string   `json:"changeName,omitempty" jsonschema_description:"openspec_appendix, openspec_instructions, openspec_stage (required): name of the openspec change. openspec_instructions and openspec_stage accept only lowercase letters, digits and single hyphens. Example: add-widget"`
	ProposalPath string   `json:"proposalPath,omitempty" jsonschema_description:"openspec_appendix only: path to the change's proposal.md, included in the generated appendix."`
	DesignPath   string   `json:"designPath,omitempty" jsonschema_description:"openspec_appendix only: path to the change's design.md, included in the generated appendix."`
	SpecPaths    []string `json:"specPaths,omitempty" jsonschema_description:"openspec_appendix only: paths to the change's spec files, included in the generated appendix."`
	PlanTasks    []string `json:"planTasks,omitempty" jsonschema_description:"openspec_appendix only: plan task identifiers to cross-reference in the generated appendix."`

	// openspec_stage
	Files    []openspec.StageFile `json:"files,omitempty" jsonschema_description:"openspec_stage only: JSON array of {path, content}; path relative to the change dir. Example: [{\"path\":\"proposal.md\",\"content\":\"# Proposal\"}]"`
	PlanPath string               `json:"planPath,omitempty" jsonschema_description:"openspec_stage only: absolute plan file path, saved in stage.json. Example: /Users/me/.claude/plans/add-widget.md"`

	// evidence_record / evidence_digest / evidence_get
	RunID           string         `json:"runId,omitempty" jsonschema_description:"Plain text. evidence_* only (required): plan run ID from plan_prepare's runId output. Example: plan-main-20260929T114125Z."`
	WriterID        string         `json:"writerId,omitempty" jsonschema_description:"Plain text. evidence_record only (required): writer that owns the evidence file; letters, digits, '.', '_', '-', max 64. Examples: explore-auth-flow, lane-static-structural-r1, lens-risk-r2, reviewer-r1, gate-a, main."`
	Status          string         `json:"status,omitempty" jsonschema:"enum=running,enum=done" jsonschema_description:"Plain text, one of running or done. evidence_record only. Omit to keep the stored status; a new writer starts as running. Example: done when the writer has finished."`
	Items           []EvidenceItem `json:"items,omitempty" jsonschema_description:"JSON array, max 200. evidence_record only: items to upsert by id. Example: [{\"id\":\"F-auth-1\",\"summary\":\"token check skips expiry\",\"ref\":\"internal/auth.go:42\",\"body\":\"…\"}]."`
	Brief           string         `json:"brief,omitempty" jsonschema_description:"Markdown text, max 65536 bytes. evidence_record with writerId main only: the discovery brief, stored as brief.md. Example: \"# Discovery Brief\\n\\n## Findings\\n\\nF-auth-1: internal/auth.go:42 — token check skips expiry\"."`
	IDs             []string       `json:"ids,omitempty" jsonschema_description:"JSON array of item IDs, max 200. evidence_get only. Example: [\"F-auth-1\",\"R3\"]."`
	WriterIDs       []string       `json:"writerIds,omitempty" jsonschema_description:"JSON array of writer IDs, max 32. evidence_get only: return every item of these writers. Example: [\"main\",\"explore-auth-flow\"]."`
	ExpectedWriters []string       `json:"expectedWriters,omitempty" jsonschema_description:"JSON array of writer IDs, max 32. evidence_digest only: writers the caller dispatched. Default: the checkpoint's expectedWriters. Example: [\"lane-static-structural-r1\"]."`
	TimeoutSeconds  int            `json:"timeoutSeconds,omitempty" jsonschema_description:"Integer 60-86400. evidence_digest only: seconds since the last update after which a running writer counts as stalled. 0 or absent = 1800. Example: 1800."`
	StatusOnly      bool           `json:"statusOnly,omitempty" jsonschema_description:"Boolean. evidence_digest only: true returns only the writers section (poll mode). Example: true in the Step 1 POLL loop."`

	// preplan_context
	Topic string `json:"topic,omitempty" jsonschema_description:"preplan_context only (required): plain-text topic name, one line, 1-50 characters after outer spaces are trimmed, with at least one ASCII letter or digit (a-z, A-Z, 0-9). The file name is the topic lowercased, with each run of other characters turned into one hyphen, so two topics can share one file (\"Auth flow\" and \"auth-flow\" both give auth-flow.md). Example: auth flow"`
}

// EvidenceItem is one evidence entry of a writer, stored in
// <runId>.evidence/<writerId>.json and upserted by id.
type EvidenceItem struct {
	ID      string `json:"id" jsonschema_description:"Plain text, unique within its writer; letters, digits, '.', '_', '-', max 128. Example: F-auth-1, R3, lane-content-coverage-r1-result."`
	Summary string `json:"summary" jsonschema_description:"Plain text, one line, max 200 characters. Shown in the digest index. Example: token check skips expiry."`
	Ref     string `json:"ref,omitempty" jsonschema_description:"Plain text, one line, max 500 characters: path:line or URL of the evidence. Example: internal/tools/plan.go:1185-1200."`
	Body    string `json:"body,omitempty" jsonschema_description:"Markdown text. Full content; returned only by evidence_get. Example: \"validateToken returns early before the exp claim is read.\"."`
}

// EvidenceRecordOut is evidence_record's result.
type EvidenceRecordOut struct {
	WriterID  string `json:"writerId"`
	Status    string `json:"status"`
	ItemCount int    `json:"itemCount"`
	FileBytes int    `json:"fileBytes"`
	BriefPath string `json:"briefPath"` // empty (renders "(none)") unless this call stored a brief
}

// EvidenceWritersOut is evidence_digest's writer status section.
type EvidenceWritersOut struct {
	Table          string   `json:"table" render:"raw"` // | writer | status | items | updatedAt | stalled |
	MissingWriters []string `json:"missingWriters"`
	StalledWriters []string `json:"stalledWriters"`
	// UnreadableWriters lists every writer whose evidence file is corrupt or
	// unreadable, expected or not; re-dispatch them or accept the loss.
	UnreadableWriters []string `json:"unreadableWriters"`
}

// EvidenceDigestOut is evidence_digest's run summary (omitted when
// statusOnly). It never carries item bodies.
type EvidenceDigestOut struct {
	RunID          string          `json:"runId"`
	PlanFilePath   string          `json:"planFilePath"`
	UserPrompt     string          `json:"userPrompt"`
	GuardrailsFile string          `json:"guardrailsFile"`
	BriefPath      string          `json:"briefPath"`
	Instructions   []string        `json:"instructions"`
	Checkpoint     *PlanCheckpoint `json:"checkpoint"`
	Index          string          `json:"index" render:"raw"` // | id | writer | ref | summary |
}

// EvidenceGetOut is evidence_get's result: rendered item bodies plus the
// requested ids or writers that were not found.
type EvidenceGetOut struct {
	Evidence string   `json:"evidence" render:"raw"`
	NotFound []string `json:"notFound"`
}

// PlanSupportOut is the unified output for the plan_support tool.
type PlanSupportOut struct {
	// LLM-contract fields (all actions populate these)
	Summary string `json:"summary"` // natural language result description
	Next    string `json:"next"`    // action hint for SKILL.md

	// merge_results
	AllIssues       []Issue  `json:"allIssues,omitempty"`
	CoverageGaps    []string `json:"coverageGaps,omitempty"`
	LaneFailures    []string `json:"laneFailures,omitempty"`
	MergedStatus    string   `json:"mergedStatus,omitempty"`
	BlockingCount   *int     `json:"blockingCount,omitempty" jsonschema_description:"Integer, merge_results only: always set there, 0 too. Absent on other actions. Count of blocking issues after the merge. Example: 3"` // pointer so 0 still renders
	Recommendations []string `json:"recommendations,omitempty"`

	// material_snapshot
	SnapshotPath string `json:"snapshotPath,omitempty" jsonschema_description:"material_snapshot only: path to the snapshot file just written to disk. Pass this to material_compare's snapshotPath field."`

	// material_compare
	//
	// Neither field carries omitempty: plan/SKILL.md Step 6 reads `material`
	// as a boolean and `triggers` as a list, and an omitted field is
	// indistinguishable from false/empty in the rendered output. The cost is
	// two extra lines ("material: false", "triggers: (none)") on the three
	// actions that do not populate them.
	Material bool     `json:"material"`
	Triggers []string `json:"triggers"`

	// openspec_appendix
	AppendixMarkdown string `json:"appendixMarkdown,omitempty"`

	// openspec_stage
	StagingDir     string                `json:"stagingDir,omitempty"`
	StagedFiles    []openspec.StagedFile `json:"files,omitempty"`
	Valid          *bool                 `json:"valid,omitempty"` // pointer so false still renders
	ValidateOutput string                `json:"validateOutput,omitempty"`

	// openspec_instructions
	SchemaName string                   `json:"schemaName,omitempty"`
	Artifacts  []openspec.ArtifactGuide `json:"artifacts,omitempty"`
	// Guardrails is loadGuardrails(mainRoot): the same list plan_prepare
	// returns. Empty (never nil) when none are configured; omitempty drops it
	// from the rendered output then, so Summary states the count.
	Guardrails []map[string]any `json:"guardrails,omitempty"`

	// evidence_* (nil and omitted for every other action)
	Record  *EvidenceRecordOut  `json:"record,omitempty"`  // evidence_record
	Writers *EvidenceWritersOut `json:"writers,omitempty"` // evidence_digest (always)
	Digest  *EvidenceDigestOut  `json:"digest,omitempty"`  // evidence_digest unless statusOnly
	Get     *EvidenceGetOut     `json:"get,omitempty"`     // evidence_get

	// preplan_context (Guardrails above is also set by this action).
	// PreplanCreated is a pointer so false still renders for preplan_context
	// while every other action omits it.
	PreplanFile    string `json:"preplanFile,omitempty"`    // absolute <mainRoot>/.sdlc-v2/preplan/<slug>.md
	PreplanCreated *bool  `json:"preplanCreated,omitempty"` // true only when this call created the file
}

// LaneResult represents the outcome of a single review lane.
type LaneResult struct {
	Name    string   `json:"name" jsonschema_description:"Name of the review lane that produced this result."`
	Status  string   `json:"status" jsonschema_description:"Outcome of the lane: \"pass\" or \"fail\"."` // "pass"|"fail"
	Issues  []Issue  `json:"issues,omitempty" jsonschema_description:"Findings raised by this lane."`
	Passes  []string `json:"passes,omitempty" jsonschema_description:"Descriptions of checks this lane explicitly passed."`
	GateIDs []string `json:"gateIds,omitempty" jsonschema_description:"Gate IDs this lane covers, used to compute coverage against expectedGates."`
}

// LensResult represents the outcome of a single review lens.
type LensResult struct {
	Name            string   `json:"name" jsonschema_description:"Name of the review lens that produced this result."`
	Status          string   `json:"status" jsonschema_description:"Outcome of the lens, as the lens wrote it: \"Approved\" or \"Issues Found\". Letter case and outer spaces are ignored; any value other than approved counts as not approved."`
	Issues          []Issue  `json:"issues,omitempty" jsonschema_description:"Findings raised by this lens."`
	Recommendations []string `json:"recommendations,omitempty" jsonschema_description:"Recommendations raised by this lens."`
}

// Issue represents a single finding from a lane or lens.
type Issue struct {
	ID       string `json:"id,omitempty" jsonschema_description:"Output only. Stable finding ID: f- plus 8 hex chars of sha256(gateId|lower(trim(summary))). Example: f-3a9c1e07. merge_results ignores any value sent here and always writes the computed one."`
	GateID   string `json:"gateId,omitempty" jsonschema_description:"Gate ID this finding relates to, if any."`
	Severity string `json:"severity" jsonschema_description:"Severity of the finding: \"blocking\" or \"advisory\"."` // "blocking"|"advisory"
	Summary  string `json:"summary" jsonschema_description:"Human-readable summary of the finding."`
	Source   string `json:"source,omitempty" jsonschema_description:"Name of the lane or lens that raised this finding."` // lane or lens name
}

// PlanSnapshot captures the 7 structural dimensions of a plan file for
// material-change detection (R64).
type PlanSnapshot struct {
	TaskCount           int                 `json:"taskCount" jsonschema_description:"Number of tasks in the plan at the time of the snapshot."`
	DeviationsRows      []string            `json:"deviationsRows,omitempty" jsonschema_description:"Rows from the plan's \"Deviations & assumptions\" section at the time of the snapshot."`
	FilesSet            map[string][]string `json:"filesSet,omitempty" jsonschema_description:"Per-task set of files listed in the plan's \"Files:\" fields at the time of the snapshot."`
	Contracts           map[string]string   `json:"contracts,omitempty" jsonschema_description:"Per-task \"Contract:\" field content at the time of the snapshot."`
	DependsOn           map[string]string   `json:"dependsOn,omitempty" jsonschema_description:"Per-task \"Depends on\" field content at the time of the snapshot."`
	KeyDecisions        []string            `json:"keyDecisions,omitempty" jsonschema_description:"Entries from the plan's \"Key Decisions\" section at the time of the snapshot."`
	OpenspecTaskMapping map[string]string   `json:"openspecTaskMapping,omitempty" jsonschema_description:"Per-task openspec ref mapping (from \"**openspec-task:**\" blocks) at the time of the snapshot."`
}

// ---------------------------------------------------------------------------
// plan_support regexes (package-private)
// ---------------------------------------------------------------------------

// psDeviationsHeadingRe matches the "## Deviations & assumptions" heading.
var psDeviationsHeadingRe = regexp.MustCompile(`(?im)^##\s+Deviations\s*&\s*assumptions`)

// psKeyDecisionsHeadingRe matches the "## Key Decisions" heading.
var psKeyDecisionsHeadingRe = regexp.MustCompile(`(?im)^##\s+Key\s+Decisions`)

// psSectionHeadingRe matches any heading level 2+ (## or ### etc.) used as
// a section boundary. This ensures sections end before tasks (### Task N:).
var psSectionHeadingRe = regexp.MustCompile(`(?m)^#{2,}\s+`)

// psTableRowRe matches a markdown table row and captures the first cell.
var psTableRowRe = regexp.MustCompile(`(?m)^\|\s*([^|]+?)\s*\|`)

// psFilesBlockStartRe matches the **Files:** field marker.
var psFilesBlockStartRe = regexp.MustCompile(`(?m)^\*\*Files:\*\*`)

// psContractBlockStartRe matches the **Contract:** field marker.
var psContractBlockStartRe = regexp.MustCompile(`(?m)^\*\*Contract:\*\*`)

// psNextFieldLineRe matches the start of the next "**<Field>:**" line. It
// ends a **Contract:** block, whose body may hold bold text but never a
// field marker at the start of a line.
var psNextFieldLineRe = regexp.MustCompile(`\n\*\*[^*\n]+:\*\*`)

// psOpenspecTaskStartRe matches the **openspec-task:** block marker.
var psOpenspecTaskStartRe = regexp.MustCompile(`(?m)^\*\*openspec-task:\*\*`)

// psOpenspecRefValueRe extracts the ref value from a "- ref: <value>" line.
var psOpenspecRefValueRe = regexp.MustCompile(`(?m)^-\s+ref:\s*(.+)$`)

// psFileLineBulletRe matches a bullet-list line that looks like a file path.
var psFileLineBulletRe = regexp.MustCompile(`(?m)^[-*]\s+(.+)$`)

// psTableSepRowRe matches a markdown table separator row (e.g., |---|---|).
var psTableSepRowRe = regexp.MustCompile(`(?m)^[|][-\s:|]+[|]$`)

// psHeaderSepRe matches a markdown table separator row after a header.
var psHeaderSepRe = regexp.MustCompile(`(?m)^[|][-\s:]+[|]`)

// psBulletEntryRe matches a bullet-list entry and captures the leading phrase.
var psBulletEntryRe = regexp.MustCompile(`(?m)^[-*]\s+\*?\*?(.+?)(?:\*?\*?\s*[—–:]+|$)`)

// ---------------------------------------------------------------------------
// Registration
// ---------------------------------------------------------------------------

// RegisterPlanSupportTools registers the plan_support tool.
func RegisterPlanSupportTools(s *mcpserver.Server) {
	mcpserver.Register(s, "plan_support",
		`INTERNAL — called by sdlc skills only. Plan support utilities.

Pass "action" to select an operation. Each action uses a subset of the input fields (unlisted fields are ignored):

- merge_results: Merge lane/lens review results. Requires at least one of laneResults or lensResults. Optional: expectedGates, isRedispatch. Lane status must be pass or fail, and every issue severity must be blocking or advisory; any other value fails with a DomainError and nothing is merged. Every issue in allIssues carries an "id" field: "f-" plus the first 8 hex chars of sha256(gateId|lower(trim(summary))). The same gate and summary always give the same id. An "id" in the input is ignored.
- material_snapshot: Snapshot plan material for change detection. Requires filePath. Returns snapshotPath.
- material_compare: Compare current plan material against a snapshot. Requires filePath, snapshotPath (from material_snapshot).
- openspec_appendix: Generate an openspec appendix. Requires changeName. Optional: proposalPath, designPath, specPaths, planTasks.
- openspec_instructions: Return the artifact templates, instructions, rules and the active plan guardrails for a new OpenSpec change, from a temp copy of openspec/config.yaml. Requires changeName. Returns schemaName, artifacts, guardrails. Writes nothing in the repository; a missing openspec CLI returns InfraError.
- openspec_stage: Write the authored artifacts to <active-worktree>/.sdlc-v2/openspec-staging/<changeName>/ (replaces the whole directory) and validate them in a temp copy. Requires changeName, files. Optional: planPath. Returns stagingDir, files, valid, validateOutput. A bad name or path, or a current spec under openspec/specs/ over 1 MiB, returns DomainError and writes nothing. A current spec that cannot be read returns InfraError.
- evidence_record: Store a writer's status and items (upsert by id) in the plan run's evidence directory. Requires runId, writerId. Optional: status, items, brief (writerId main only). Returns record. Invalid input or a limit breach returns DomainError and writes nothing; an OS read/write failure returns InfraError.
- evidence_digest: Compact run summary for resume and polling; never returns item bodies. Requires runId. Optional: expectedWriters, timeoutSeconds, statusOnly. Returns writers, plus digest unless statusOnly. Invalid input or a limit breach returns DomainError and writes nothing; an OS read/write failure returns InfraError.
- evidence_get: Full item bodies. Requires runId and at least one of ids or writerIds. Returns get. Invalid input or a limit breach returns DomainError and writes nothing; an OS read/write failure returns InfraError.
- preplan_context: Return the plan guardrails and the topic file path. Requires topic. No optional fields. The topic file is <main-worktree>/.sdlc-v2/preplan/<slug>.md. Creates it with a skeleton when it is absent, never overwrites it. Returns guardrails, preplanFile (absolute path), preplanCreated (true when this call created the file, false when it already existed), summary, next. A bad topic returns DomainError and writes nothing. A failed create returns InfraError. Both carry a Suggestion. A dangling symlink on the path to the topic file returns InfraError that names the link and its missing target; nothing is written. Its Suggestion for a folder link is "Start a new session so the session-start hook repairs the link, or run: mkdir -p <target>"; for a topic-file link it is "Remove the link, then try again: rm <link>".`,
		mcpserver.Annotations{
			Title:      "Plan support and evidence store",
			ReadOnly:   true,
			Idempotent: true,
			OpenWorld:  false,
		},
		func(ctx mcpserver.Ctx, in PlanSupportIn) (PlanSupportOut, error) {
			root, err := worktree.MainRoot()
			if err != nil {
				cwd, cwdErr := os.Getwd()
				if cwdErr != nil {
					return PlanSupportOut{}, &mcpserver.InfraError{
						Msg:        "resolve root: " + err.Error(),
						Suggestion: "Run plan_support from inside a git repository (or a worktree of one) so the main root can be resolved, then retry.",
						Cause:      err,
					}
				}
				root = cwd
			}
			// An unresolved active worktree stays empty: the openspec_*
			// actions that write under it refuse instead of falling back to
			// the main worktree.
			contentRoot, err := worktree.ActiveRoot()
			if err != nil {
				contentRoot = ""
			}
			return planSupportCore(root, contentRoot, in)
		},
	)
}

// ---------------------------------------------------------------------------
// Core dispatcher
// ---------------------------------------------------------------------------

// planSupportCore dispatches to the correct action handler. mainRoot anchors
// filesystem lookups; contentRoot anchors worktree-relative operations and is
// empty when the active worktree could not be resolved.
func planSupportCore(mainRoot, contentRoot string, in PlanSupportIn) (PlanSupportOut, error) {
	switch in.Action {
	case "merge_results":
		return mergeResults(in)
	case "material_snapshot":
		return materialSnapshot(in)
	case "material_compare":
		return materialCompare(in)
	case "openspec_appendix":
		return openspecAppendix(mainRoot, in)
	case "openspec_instructions":
		return openspecInstructions(mainRoot, contentRoot, in)
	case "openspec_stage":
		return openspecStage(contentRoot, in)
	case "evidence_record":
		return evidenceRecord(mainRoot, in)
	case "evidence_digest":
		return evidenceDigest(mainRoot, in)
	case "evidence_get":
		return evidenceGet(mainRoot, in)
	case "preplan_context":
		return planPreplanContext(mainRoot, in.Topic)
	default:
		return PlanSupportOut{}, unknownActionError("action", in.Action,
			" — valid actions: merge_results, material_snapshot, material_compare, openspec_appendix, openspec_instructions, openspec_stage, evidence_record, evidence_digest, evidence_get, preplan_context",
			"call plan_support again with action set to exactly one of merge_results, material_snapshot, material_compare, openspec_appendix, openspec_instructions, openspec_stage, evidence_record, evidence_digest, evidence_get or preplan_context")
	}
}

// ---------------------------------------------------------------------------
// Action: merge_results
// ---------------------------------------------------------------------------

// issueKey returns a dedup key for an Issue (GateID + normalized summary).
func issueKey(iss Issue) string {
	return iss.GateID + "|" + strings.ToLower(strings.TrimSpace(iss.Summary))
}

// findingID returns the stable ID of an Issue: "f-" plus the first 8 hex
// chars of sha256(issueKey). It depends only on GateID and the normalized
// Summary, so the same finding keeps its ID across calls.
func findingID(iss Issue) string {
	sum := sha256.Sum256([]byte(issueKey(iss)))
	return "f-" + hex.EncodeToString(sum[:])[:8]
}

// validateMergeEnums rejects a lane status other than "pass"/"fail" and an
// issue severity other than "blocking"/"advisory". The merge compares these
// values exactly, so an unmapped value ("ok", "error", "") would otherwise
// count as a pass or as advisory and hide the finding. Lens status stays
// free-form: anything other than approved already counts as not approved.
func validateMergeEnums(in PlanSupportIn) error {
	var bad []string
	checkIssues := func(where string, issues []Issue) {
		for j, iss := range issues {
			if iss.Severity != "blocking" && iss.Severity != "advisory" {
				bad = append(bad, fmt.Sprintf("%s.issues[%d] (%q): severity %q", where, j, iss.Summary, iss.Severity))
			}
		}
	}
	for i, lane := range in.LaneResults {
		where := fmt.Sprintf("laneResults[%d] (%q)", i, lane.Name)
		if lane.Status != "pass" && lane.Status != "fail" {
			bad = append(bad, fmt.Sprintf("%s: status %q", where, lane.Status))
		}
		checkIssues(fmt.Sprintf("laneResults[%d]", i), lane.Issues)
	}
	for i, lens := range in.LensResults {
		checkIssues(fmt.Sprintf("lensResults[%d]", i), lens.Issues)
	}
	if len(bad) == 0 {
		return nil
	}
	return &mcpserver.DomainError{
		Msg: "merge_results: lane status must be \"pass\" or \"fail\" and issue severity must be \"blocking\" or \"advisory\"; got " +
			strings.Join(bad, "; "),
		Suggestion: "Map each value before the call: lane status to \"pass\" or \"fail\", issue severity to \"blocking\" or \"advisory\" (plan SKILL.md \"Map lane results to the merge_results shape\"), then call merge_results again.",
	}
}

// mergeResults merges the lane and lens results of a plan review into one
// de-duplicated issue list with stable finding IDs and a combined status.
func mergeResults(in PlanSupportIn) (PlanSupportOut, error) {
	if len(in.LaneResults) == 0 && len(in.LensResults) == 0 {
		return PlanSupportOut{}, &mcpserver.DomainError{
			Msg:        "merge_results requires at least one of laneResults or lensResults to be non-empty",
			Suggestion: "Collect the lane and/or lens reviewer results first, then call merge_results with laneResults, lensResults, or both populated.",
		}
	}
	if err := validateMergeEnums(in); err != nil {
		return PlanSupportOut{}, err
	}

	seen := map[string]bool{}
	var allIssues []Issue
	var laneFailures []string
	coveredGates := map[string]bool{}

	// Process lane results.
	for _, lane := range in.LaneResults {
		if lane.Status == "fail" {
			laneFailures = append(laneFailures, lane.Name)
		}
		for _, gid := range lane.GateIDs {
			coveredGates[gid] = true
		}
		for _, iss := range lane.Issues {
			iss.Source = lane.Name
			// isRedispatch=true makes G17 findings advisory-only.
			if in.IsRedispatch && iss.GateID == "G17" {
				iss.Severity = "advisory"
			}
			key := issueKey(iss)
			if !seen[key] {
				seen[key] = true
				allIssues = append(allIssues, iss)
			}
		}
	}

	// G17 lane failure is always advisory: a failed lane whose only gate
	// coverage is G17 does not reject the merge. Remove G17-only failures
	// from the laneFailures list and synthesize advisory issues for them.
	// A lane with no gateIds covers no gate at all, so it is not G17-only.
	var filteredLaneFailures []string
	for _, lane := range in.LaneResults {
		if lane.Status != "fail" {
			continue
		}
		isG17Only := len(lane.GateIDs) > 0
		for _, gid := range lane.GateIDs {
			if gid != "G17" {
				isG17Only = false
				break
			}
		}
		if isG17Only {
			// G17-only lane failure is advisory, not blocking.
			advisoryIssue := Issue{
				GateID:   "G17",
				Severity: "advisory",
				Summary:  fmt.Sprintf("Lane %q failed but covers only G17 (advisory gate)", lane.Name),
				Source:   lane.Name,
			}
			key := issueKey(advisoryIssue)
			if !seen[key] {
				seen[key] = true
				allIssues = append(allIssues, advisoryIssue)
			}
		} else {
			filteredLaneFailures = append(filteredLaneFailures, lane.Name)
		}
	}
	laneFailures = filteredLaneFailures

	// Process lens results.
	recSeen := map[string]bool{}
	var recommendations []string
	for _, lens := range in.LensResults {
		for _, iss := range lens.Issues {
			iss.Source = lens.Name
			key := issueKey(iss)
			if !seen[key] {
				seen[key] = true
				allIssues = append(allIssues, iss)
			}
		}
		for _, rec := range lens.Recommendations {
			norm := strings.TrimSpace(rec)
			if norm != "" && !recSeen[norm] {
				recSeen[norm] = true
				recommendations = append(recommendations, norm)
			}
		}
	}

	// Coverage gap check: each expected gate must appear in at least one lane.
	var coverageGaps []string
	for _, gid := range in.ExpectedGates {
		if !coveredGates[gid] {
			coverageGaps = append(coverageGaps, gid)
			// Coverage gaps become blocking issues.
			gapIssue := Issue{
				GateID:   gid,
				Severity: "blocking",
				Summary:  fmt.Sprintf("Gate %s was not covered by any lane", gid),
				Source:   "coverage-check",
			}
			key := issueKey(gapIssue)
			if !seen[key] {
				seen[key] = true
				allIssues = append(allIssues, gapIssue)
			}
		}
	}

	// Stamp every issue with its computed ID. This overwrites any ID sent in.
	for i := range allIssues {
		allIssues[i].ID = findingID(allIssues[i])
	}

	// Compute merged status.
	mergedStatus := planStatusApproved

	// Any blocking issue means the merge is not clean.
	for _, iss := range allIssues {
		if iss.Severity == "blocking" {
			mergedStatus = planStatusIssuesFound
			break
		}
	}

	// Approved iff all lens statuses equal planStatusApproved, whether or
	// not laneResults are sent in the same call. The lens prompts write
	// planStatusApproved or planStatusIssuesFound after "**Status:**".
	// Letter case and outer spaces are ignored.
	if len(in.LensResults) > 0 {
		for _, lens := range in.LensResults {
			if !strings.EqualFold(strings.TrimSpace(lens.Status), planStatusApproved) {
				mergedStatus = planStatusIssuesFound
				break
			}
		}
	}

	// For lanes-only or mixed: any lane failure that isn't G17-only means
	// issues found, with or without issues. laneFailures already excludes
	// G17-only lanes, and blocking issues are covered by allIssues above
	// (after any isRedispatch downgrade), so lane.Issues is not read here.
	if len(laneFailures) > 0 {
		mergedStatus = planStatusIssuesFound
	}

	// Build summary and next hint.
	blockingCount := 0
	advisoryCount := 0
	for _, iss := range allIssues {
		if iss.Severity == "blocking" {
			blockingCount++
		} else {
			advisoryCount++
		}
	}

	summary := fmt.Sprintf("Merged %d lane(s) and %d lens(es): %d blocking issue(s), %d advisory issue(s). Status: %s.",
		len(in.LaneResults), len(in.LensResults), blockingCount, advisoryCount, mergedStatus)

	next := "Proceed to the next step."
	if len(coverageGaps) > 0 {
		next = fmt.Sprintf("Re-dispatch lanes for missing gates: %s.", strings.Join(coverageGaps, ", "))
	} else if mergedStatus == planStatusIssuesFound {
		next = "Address blocking issues and re-run the review."
	} else if advisoryCount > 0 {
		next = "Review advisory findings before proceeding."
	}

	return PlanSupportOut{
		Summary:         summary,
		Next:            next,
		AllIssues:       allIssues,
		CoverageGaps:    coverageGaps,
		LaneFailures:    laneFailures,
		MergedStatus:    mergedStatus,
		BlockingCount:   &blockingCount,
		Recommendations: recommendations,
	}, nil
}

// ---------------------------------------------------------------------------
// Action: material_snapshot
// ---------------------------------------------------------------------------

func materialSnapshot(in PlanSupportIn) (PlanSupportOut, error) {
	if in.FilePath == "" {
		return PlanSupportOut{}, &mcpserver.DomainError{
			Msg:        "material_snapshot requires filePath — provide the path to the plan file to snapshot",
			Suggestion: "Call plan_support again with action=\"material_snapshot\" and a non-empty filePath pointing to the plan file.",
		}
	}

	content, err := readFileFunc(in.FilePath)
	if err != nil {
		return PlanSupportOut{}, &mcpserver.InfraError{
			Msg:        fmt.Sprintf("read plan file %q: %s", in.FilePath, err.Error()),
			Suggestion: fmt.Sprintf("Check that %q exists and is readable, then retry material_snapshot with the corrected filePath.", in.FilePath),
			Cause:      err,
		}
	}

	snap := snapshotPlan(string(content))

	snapshotPath, err := writePlanSnapshot(snap)
	if err != nil {
		return PlanSupportOut{}, err
	}

	return PlanSupportOut{
		Summary:      fmt.Sprintf("Snapshot captured: %d tasks, %d deviation rows, %d key decisions.", snap.TaskCount, len(snap.DeviationsRows), len(snap.KeyDecisions)),
		Next:         "After editing the plan, call plan_support with action=\"material_compare\", the same filePath, and the snapshotPath returned above.",
		SnapshotPath: snapshotPath,
	}, nil
}

// writePlanSnapshot marshals snap to JSON and writes it to a fresh temp file
// via the fsseam, so the caller can pass the file path forward to
// material_compare instead of round-tripping the full snapshot JSON through
// its own context.
func writePlanSnapshot(snap PlanSnapshot) (string, error) {
	snapshotPath, err := writeTempJSON("sdlc-plan-snapshot-", "snapshot", func(string) any { return snap })
	if err != nil {
		return "", &mcpserver.InfraError{
			Msg:        err.Error(),
			Suggestion: "Check available disk space and permissions on the OS temp directory, then retry material_snapshot.",
			Cause:      err,
		}
	}
	return snapshotPath, nil
}

// snapshotPlan extracts the 7 structural dimensions from plan markdown content.
func snapshotPlan(content string) PlanSnapshot {
	stripped := stripFences(content)
	tasks := extractTasks(stripped)

	snap := PlanSnapshot{
		TaskCount:           len(tasks),
		FilesSet:            make(map[string][]string),
		Contracts:           make(map[string]string),
		DependsOn:           make(map[string]string),
		OpenspecTaskMapping: make(map[string]string),
	}

	// Extract per-task dimensions.
	for _, t := range tasks {
		taskRef := "Task " + strconv.Itoa(t.Number)

		// Files: extract bullet-list paths after **Files:** marker.
		filesBlock, filesFound := extractDelimitedBlock(t.Body, psFilesBlockStartRe, []string{"\n**", "\n### ", "\n---", "\n## "})
		if filesFound {
			var paths []string
			for _, m := range psFileLineBulletRe.FindAllStringSubmatch(filesBlock, -1) {
				p := strings.TrimSpace(m[1])
				if p != "" {
					paths = append(paths, p)
				}
			}
			sort.Strings(paths)
			snap.FilesSet[taskRef] = paths
		}

		// Contract: verbatim text after **Contract:** marker, up to the next
		// **<Field>:** line or task boundary.
		contractBlock, contractFound := extractDelimitedBlock(t.Body, psContractBlockStartRe, []string{"\n### ", "\n---", "\n## "})
		if contractFound {
			if loc := psNextFieldLineRe.FindStringIndex(contractBlock); loc != nil {
				contractBlock = contractBlock[:loc[0]]
			}
			snap.Contracts[taskRef] = strings.TrimSpace(contractBlock)
		}

		// Depends on: field value.
		if dep, ok := extractField(t.Body, "Depends on"); ok {
			snap.DependsOn[taskRef] = dep
		}

		// OpenSpec task mapping: extract the "ref" value from the **openspec-task:** block.
		openspecBlock, openspecFound := extractDelimitedBlock(t.Body, psOpenspecTaskStartRe, []string{"\n**", "\n### ", "\n---", "\n## "})
		if openspecFound {
			if m := psOpenspecRefValueRe.FindStringSubmatch(openspecBlock); m != nil {
				snap.OpenspecTaskMapping[taskRef] = strings.TrimSpace(m[1])
			}
		}
	}

	// Extract Deviations & assumptions table row keys.
	snap.DeviationsRows = extractSectionRowKeys(stripped, psDeviationsHeadingRe)

	// Extract Key Decisions entry keys.
	snap.KeyDecisions = extractSectionEntryKeys(stripped, psKeyDecisionsHeadingRe)

	return snap
}

// extractSectionBetweenHeadings extracts content between a heading and the
// next ## heading (or end of document).
func extractSectionBetweenHeadings(content string, headingRe *regexp.Regexp) string {
	loc := headingRe.FindStringIndex(content)
	if loc == nil {
		return ""
	}
	rest := content[loc[1]:]
	nextH2 := psSectionHeadingRe.FindStringIndex(rest)
	if nextH2 != nil {
		return rest[:nextH2[0]]
	}
	return rest
}

// extractSectionRowKeys extracts the first-cell values from markdown table
// rows within a section (for Deviations & assumptions).
func extractSectionRowKeys(content string, headingRe *regexp.Regexp) []string {
	section := extractSectionBetweenHeadings(content, headingRe)
	if section == "" {
		return nil
	}

	var keys []string
	seen := map[string]bool{}
	lines := strings.Split(section, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "|") {
			continue
		}
		// Skip separator rows like |---|---|
		if psTableSepRowRe.MatchString(line) {
			continue
		}
		m := psTableRowRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		key := strings.TrimSpace(m[1])
		// Skip header rows that look like column titles.
		if key == "" {
			continue
		}
		if !seen[key] {
			seen[key] = true
			keys = append(keys, key)
		}
	}

	// Drop the first key if it looks like a table header (heuristic: the
	// second line is a separator). This is approximate but sufficient since
	// we compare before/after snapshots using the same parser.
	if len(keys) > 0 {
		// Check if the section has a separator row right after the first
		// table row — if so, the first key is the header.
		if psHeaderSepRe.MatchString(section) {
			keys = keys[1:] // drop header row key
		}
	}

	sort.Strings(keys)
	return keys
}

// extractSectionEntryKeys extracts entry keys from a Key Decisions section.
// Entries can be bullet-list items (first significant word/phrase) or table
// rows (first cell). We extract whichever format is present.
func extractSectionEntryKeys(content string, headingRe *regexp.Regexp) []string {
	section := extractSectionBetweenHeadings(content, headingRe)
	if section == "" {
		return nil
	}

	var keys []string
	seen := map[string]bool{}
	lines := strings.Split(section, "\n")

	hasTable := false
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "|") {
			hasTable = true
			break
		}
	}

	if hasTable {
		// Table format: extract first-cell values (same as deviations).
		keys = extractSectionRowKeys(content, headingRe)
	} else {
		// Bullet-list format: extract each bullet's leading phrase.
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if m := psBulletEntryRe.FindStringSubmatch(line); m != nil {
				key := strings.TrimSpace(m[1])
				key = strings.TrimRight(key, "*")
				key = strings.TrimSpace(key)
				if key != "" && !seen[key] {
					seen[key] = true
					keys = append(keys, key)
				}
			}
		}
	}

	sort.Strings(keys)
	return keys
}

// ---------------------------------------------------------------------------
// Action: material_compare
// ---------------------------------------------------------------------------

func materialCompare(in PlanSupportIn) (PlanSupportOut, error) {
	if in.FilePath == "" {
		return PlanSupportOut{}, &mcpserver.DomainError{
			Msg:        "material_compare requires filePath — provide the path to the updated plan file",
			Suggestion: "Call plan_support again with action=\"material_compare\" and a non-empty filePath pointing to the updated plan file.",
		}
	}
	if in.SnapshotPath == "" {
		return PlanSupportOut{}, &mcpserver.DomainError{
			Msg:        "material_compare requires snapshotPath — call material_snapshot first and pass its returned snapshotPath",
			Suggestion: "Call plan_support with action=\"material_snapshot\" and the same filePath first, then pass the snapshotPath it returns back verbatim.",
		}
	}

	content, err := readFileFunc(in.FilePath)
	if err != nil {
		return PlanSupportOut{}, &mcpserver.InfraError{
			Msg:        fmt.Sprintf("read plan file %q: %s", in.FilePath, err.Error()),
			Suggestion: fmt.Sprintf("Check that %q exists and is readable, then retry material_compare with the corrected filePath.", in.FilePath),
			Cause:      err,
		}
	}

	before, err := readPlanSnapshot(in.SnapshotPath)
	if err != nil {
		return PlanSupportOut{}, err
	}

	after := snapshotPlan(string(content))
	var triggers []string

	// 1. Task count delta.
	if after.TaskCount != before.TaskCount {
		triggers = append(triggers, fmt.Sprintf("Task count changed: %d -> %d", before.TaskCount, after.TaskCount))
	}

	// 2. Deviations table modified.
	if !sortedStringSliceEqual(before.DeviationsRows, after.DeviationsRows) {
		triggers = append(triggers, "Deviations & assumptions table modified")
	}

	// 3. Files set changed.
	if diffs := diffStringSliceMaps(before.FilesSet, after.FilesSet); len(diffs) > 0 {
		triggers = append(triggers, fmt.Sprintf("Files changed in: %s", strings.Join(diffs, ", ")))
	}

	// 4. Contracts changed.
	if diffs := diffStringMaps(before.Contracts, after.Contracts); len(diffs) > 0 {
		triggers = append(triggers, fmt.Sprintf("Contract changed in: %s", strings.Join(diffs, ", ")))
	}

	// 5. Depends on changed.
	if diffs := diffStringMaps(before.DependsOn, after.DependsOn); len(diffs) > 0 {
		triggers = append(triggers, fmt.Sprintf("Depends on changed in: %s", strings.Join(diffs, ", ")))
	}

	// 6. Key Decisions changed.
	if !sortedStringSliceEqual(before.KeyDecisions, after.KeyDecisions) {
		triggers = append(triggers, "Key Decisions modified")
	}

	// 7. OpenSpec task mapping changed.
	if diffs := diffStringMaps(before.OpenspecTaskMapping, after.OpenspecTaskMapping); len(diffs) > 0 {
		triggers = append(triggers, fmt.Sprintf("OpenSpec task mapping changed in: %s", strings.Join(diffs, ", ")))
	}

	material := len(triggers) > 0

	summary := "No material changes detected — wording/formatting only."
	next := "Proceed to the next step without re-validation."
	if material {
		summary = fmt.Sprintf("Material change detected: %d trigger(s) fired.", len(triggers))
		next = "Material change detected — re-run critique lanes and lenses before proceeding."
	}

	return PlanSupportOut{
		Summary:  summary,
		Next:     next,
		Material: material,
		Triggers: triggers,
	}, nil
}

// readPlanSnapshot reads and validates the snapshot file at path, written
// earlier by material_snapshot via the fsseam. It rejects 5 distinct ways
// the referenced file can fail to be a usable snapshot: missing, unreadable
// for another reason (permission, I/O), not JSON, and well-formed JSON that
// isn't a PlanSnapshot (missing the required taskCount key, or otherwise
// undecodable).
//
// A decoded snapshot with taskCount 0 and no other keys is NOT rejected: it
// is exactly what material_snapshot writes for a plan with no "### Task N:"
// headings, because every collection field on PlanSnapshot is omitempty and
// an empty map is dropped on marshal. The taskCount presence probe above
// already separates non-snapshot JSON from a legitimately empty snapshot.
//
// No error tells the caller to re-snapshot blindly: plan/SKILL.md calls
// material_snapshot BEFORE the plan rewrite, so a snapshot regenerated after
// the rewrite would match the current plan and report material:false,
// silently skipping the R64 re-validation gate. Every recovery text either
// warns that the baseline is lost or limits re-snapshotting to an unedited
// plan.
func readPlanSnapshot(path string) (*PlanSnapshot, error) {
	raw, err := readFileFunc(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, &mcpserver.InfraError{
				Msg:        fmt.Sprintf("snapshotPath %q points to a file that is missing: it was deleted or never written", path),
				Suggestion: "If you have not edited the plan yet, run plan_support with action=\"material_snapshot\" again and pass the new snapshotPath. If you already edited it, do NOT re-snapshot: treat the change as material and run the critique lanes and lenses again.",
				Cause:      err,
			}
		}
		return nil, &mcpserver.InfraError{
			Msg:        fmt.Sprintf("read snapshot file %q: %s", path, err.Error()),
			Suggestion: fmt.Sprintf("Make %q readable for this process, then call material_compare again with the same snapshotPath. If you cannot fix it, treat the change as material and run the critique lanes and lenses again.", path),
			Cause:      err,
		}
	}

	var probe map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, &mcpserver.DataError{
			Msg:        fmt.Sprintf("snapshot file %q is not valid JSON: %s", path, err.Error()),
			Suggestion: "The pre-edit baseline is lost. If the plan has already been rewritten, do NOT re-snapshot — a snapshot taken now matches the current plan and would report material:false; treat this as a material change and re-run the critique lanes and lenses. Only re-run material_snapshot if the plan has not been edited yet.",
			Cause:      err,
		}
	}
	if _, ok := probe["taskCount"]; !ok {
		return nil, &mcpserver.DataError{
			Msg:        fmt.Sprintf("snapshot file %q is not a plan snapshot (missing taskCount field)", path),
			Suggestion: "The pre-edit baseline is lost. If the plan has already been rewritten, do NOT re-snapshot — a snapshot taken now matches the current plan and would report material:false; treat this as a material change and re-run the critique lanes and lenses. Only re-run material_snapshot if the plan has not been edited yet.",
		}
	}

	var snap PlanSnapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		return nil, &mcpserver.DataError{
			Msg:        fmt.Sprintf("snapshot file %q could not be decoded as a plan snapshot: %s", path, err.Error()),
			Suggestion: "The pre-edit baseline is lost. If the plan has already been rewritten, do NOT re-snapshot — a snapshot taken now matches the current plan and would report material:false; treat this as a material change and re-run the critique lanes and lenses. Only re-run material_snapshot if the plan has not been edited yet.",
			Cause:      err,
		}
	}
	return &snap, nil
}

// sortedStringSliceEqual compares two already-sorted string slices for equality.
func sortedStringSliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// diffStringMaps returns keys where string maps differ (added, removed, or
// changed value). Keys are sorted.
func diffStringMaps(before, after map[string]string) []string {
	allKeys := map[string]bool{}
	for k := range before {
		allKeys[k] = true
	}
	for k := range after {
		allKeys[k] = true
	}

	var diffs []string
	for k := range allKeys {
		bv, bok := before[k]
		av, aok := after[k]
		if !bok || !aok || bv != av {
			diffs = append(diffs, k)
		}
	}
	sort.Strings(diffs)
	return diffs
}

// diffStringSliceMaps returns keys where map[string][]string values differ.
// Comparison is order-insensitive (slices are expected to be sorted).
func diffStringSliceMaps(before, after map[string][]string) []string {
	allKeys := map[string]bool{}
	for k := range before {
		allKeys[k] = true
	}
	for k := range after {
		allKeys[k] = true
	}

	var diffs []string
	for k := range allKeys {
		bv, bok := before[k]
		av, aok := after[k]
		if !bok || !aok || !sortedStringSliceEqual(bv, av) {
			diffs = append(diffs, k)
		}
	}
	sort.Strings(diffs)
	return diffs
}

// ---------------------------------------------------------------------------
// Action: openspec_appendix
// ---------------------------------------------------------------------------

func openspecAppendix(mainRoot string, in PlanSupportIn) (PlanSupportOut, error) {
	if in.ChangeName == "" {
		return PlanSupportOut{}, &mcpserver.DomainError{
			Msg:        "openspec_appendix requires changeName — provide the openspec change name",
			Suggestion: "Call plan_support again with action=\"openspec_appendix\" and changeName set to the openspec change directory name (the folder under openspec/changes/).",
		}
	}

	var sb strings.Builder
	sb.WriteString("## OpenSpec Appendix\n\n")
	sb.WriteString(fmt.Sprintf("**Change:** %s\n\n", in.ChangeName))

	// Proposal summary.
	if in.ProposalPath != "" {
		proposalContent, err := os.ReadFile(in.ProposalPath)
		if err != nil {
			sb.WriteString("**Proposal:** _(file not found or unreadable)_\n\n")
		} else {
			// Extract first paragraph or heading as summary.
			summary := extractLeadParagraph(string(proposalContent))
			if summary != "" {
				sb.WriteString(fmt.Sprintf("**Proposal summary:** %s\n\n", summary))
			} else {
				sb.WriteString("**Proposal:** _(empty)_\n\n")
			}
		}
	}

	// Design reference.
	if in.DesignPath != "" {
		if _, err := os.Stat(in.DesignPath); err == nil {
			sb.WriteString(fmt.Sprintf("**Design:** `%s`\n\n", in.DesignPath))
		} else {
			sb.WriteString("**Design:** _(not provided)_\n\n")
		}
	}

	// Spec delta list.
	if len(in.SpecPaths) > 0 {
		sb.WriteString("**Spec deltas:**\n\n")
		for _, sp := range in.SpecPaths {
			sb.WriteString(fmt.Sprintf("- `%s`\n", sp))
		}
		sb.WriteString("\n")
	} else {
		sb.WriteString("**Spec deltas:** _(none)_\n\n")
	}

	// Task mapping.
	if len(in.PlanTasks) > 0 {
		sb.WriteString("**Task mapping:**\n\n")
		for _, task := range in.PlanTasks {
			sb.WriteString(fmt.Sprintf("- %s\n", task))
		}
		sb.WriteString("\n")
	}

	appendix := sb.String()

	return PlanSupportOut{
		Summary:          fmt.Sprintf("Generated OpenSpec appendix for change %q with %d spec(s) and %d task(s).", in.ChangeName, len(in.SpecPaths), len(in.PlanTasks)),
		Next:             "Append this markdown to the plan file's OpenSpec Appendix section.",
		AppendixMarkdown: appendix,
	}, nil
}

// ---------------------------------------------------------------------------
// Actions: openspec_instructions / openspec_stage
// ---------------------------------------------------------------------------

// Suggestion texts for the openspec_* action errors.
const (
	openspecNameSuggestion     = "Use lowercase letters, digits and single hyphens, for example add-widget."
	openspecPathSuggestion     = "Use a path relative to the change dir that matches an outputPath from openspec_instructions, for example specs/<capability>/spec.md."
	openspecCLISuggestion      = "Install the OpenSpec CLI (npm i -g @fission-ai/openspec) and retry, or choose Skip OpenSpec."
	openspecWorktreeSuggestion = "Run the call from inside the git worktree that holds the plan."
)

// requireActiveRoot refuses an openspec_* action when the active worktree
// could not be resolved, rather than writing into the main worktree.
func requireActiveRoot(action, contentRoot string) error {
	if contentRoot != "" {
		return nil
	}
	return &mcpserver.DomainError{
		Msg:        action + ": active worktree not resolved",
		Suggestion: openspecWorktreeSuggestion,
	}
}

// mapOpenspecError turns an internal/openspec error from action into the
// matching DomainError or InfraError.
func mapOpenspecError(action string, in PlanSupportIn, err error) error {
	switch {
	case errors.Is(err, openspec.ErrInvalidChangeName):
		return &mcpserver.DomainError{
			Msg:        fmt.Sprintf("%s: invalid changeName %q", action, in.ChangeName),
			Suggestion: openspecNameSuggestion,
			Cause:      err,
		}
	case errors.Is(err, openspec.ErrPathNotAllowed):
		// The openspec error quotes the offending path with %q; find it
		// among the inputs so the message names it exactly.
		bad := ""
		for _, f := range in.Files {
			if strings.Contains(err.Error(), strconv.Quote(f.Path)) {
				bad = f.Path
				break
			}
		}
		return &mcpserver.DomainError{
			Msg:        fmt.Sprintf("%s: path %q not allowed", action, bad),
			Suggestion: openspecPathSuggestion,
			Cause:      err,
		}
	case errors.Is(err, openspec.ErrCLINotFound):
		return &mcpserver.InfraError{
			Msg:        openspec.ErrCLINotFound.Error(),
			Suggestion: openspecCLISuggestion,
			Cause:      err,
		}
	case errors.Is(err, openspec.ErrTargetSpecTooLarge):
		return &mcpserver.DomainError{
			Msg:        action + ": " + err.Error(),
			Suggestion: "Shrink the named spec under openspec/specs/ below the size limit in the message (for example, split the capability into two specs), then call openspec_stage again.",
			Cause:      err,
		}
	case errors.Is(err, openspec.ErrTargetSpec):
		return &mcpserver.InfraError{
			Msg:        action + ": " + err.Error(),
			Suggestion: "Check read permission on the named spec under openspec/specs/, then call openspec_stage again.",
			Cause:      err,
		}
	default:
		return &mcpserver.InfraError{
			Msg:        action + ": " + err.Error(),
			Suggestion: "Check that openspec/config.yaml exists in the active worktree and that the openspec CLI runs there, then retry.",
			Cause:      err,
		}
	}
}

// openspecInstructions returns the schema name, the ordered artifact
// guidance for a new change, and the plan guardrails from the main worktree's
// config. It writes nothing in the repository.
func openspecInstructions(mainRoot, contentRoot string, in PlanSupportIn) (PlanSupportOut, error) {
	const action = "openspec_instructions"
	if err := requireActiveRoot(action, contentRoot); err != nil {
		return PlanSupportOut{}, err
	}
	schema, guides, err := openspec.PrepareInstructions(contentRoot, in.ChangeName)
	if err != nil {
		return PlanSupportOut{}, mapOpenspecError(action, in, err)
	}
	guardrails, warning := loadGuardrails(mainRoot)
	summary := fmt.Sprintf("OpenSpec change %q uses schema %q with %d artifact(s) and %d guardrail(s).", in.ChangeName, schema, len(guides), len(guardrails))
	if warning != "" {
		summary += " Warning: " + warning
	}
	return PlanSupportOut{
		Summary:    summary,
		Next:       "Author each artifact in order from template, instruction, context and rules; follow guardrails in design and tasks; then call openspec_stage.",
		SchemaName: schema,
		Artifacts:  guides,
		Guardrails: guardrails,
	}, nil
}

// openspecStage writes the authored artifacts to the active worktree's
// staging dir and validates a temp copy of them.
func openspecStage(contentRoot string, in PlanSupportIn) (PlanSupportOut, error) {
	const action = "openspec_stage"
	if err := requireActiveRoot(action, contentRoot); err != nil {
		return PlanSupportOut{}, err
	}
	if !openspec.ValidChangeName(in.ChangeName) {
		return PlanSupportOut{}, mapOpenspecError(action, in, openspec.ErrInvalidChangeName)
	}
	if len(in.Files) == 0 {
		return PlanSupportOut{}, &mcpserver.DomainError{
			Msg:        action + ": files is required",
			Suggestion: "Pass files as a JSON array of {path, content}, one entry per artifact file from openspec_instructions.",
		}
	}
	res, err := openspec.Stage(contentRoot, in.ChangeName, in.Files, in.PlanPath, nil)
	if err != nil {
		return PlanSupportOut{}, mapOpenspecError(action, in, err)
	}
	valid := res.Valid
	validateOutput := res.ValidateOutput
	for _, f := range in.Files {
		if !strings.HasSuffix(f.Path, ".md") {
			continue
		}
		for _, h := range commstyle.MermaidContrast(f.Content) {
			valid = false
			validateOutput += fmt.Sprintf("\ndiagram contrast: %s:%d: %s: %s", f.Path, h.Line, h.Reason, h.Text)
		}
	}
	out := PlanSupportOut{
		StagingDir:     res.StagingDir,
		StagedFiles:    res.Files,
		Valid:          &valid,
		ValidateOutput: validateOutput,
	}
	if valid {
		out.Summary = fmt.Sprintf("Staged %d file(s) for OpenSpec change %q in %s; validation passed.", len(res.Files), in.ChangeName, res.StagingDir)
		out.Next = "Staged and valid. Add the **OpenSpec-Staging:** header to the plan."
	} else {
		out.Summary = fmt.Sprintf("Staged %d file(s) for OpenSpec change %q in %s; validation failed.", len(res.Files), in.ChangeName, res.StagingDir)
		out.Next = "Fix the artifacts using validateOutput and call openspec_stage again."
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Action: preplan_context
// ---------------------------------------------------------------------------

// preplanTopicMaxChars is the longest topic preplan_context accepts. It keeps
// branch.Slug, which cuts a slug at 50 characters, from cutting a topic name.
const preplanTopicMaxChars = 50

// Suggestion texts for the preplan_context errors.
const (
	preplanTopicASCIISuggestion = "Pass a topic name with ASCII letters or digits, for example \"auth flow\"."
	preplanTopicLongSuggestion  = "Pass a shorter topic name. Put the detail in the first answer."
	preplanTopicLineSuggestion  = "Pass the topic name on one line."
	preplanCreateSuggestion     = "Check write permission on .sdlc-v2/preplan/, then run the skill again."
)

// preplanSkeletonTail is the text of a new topic file that follows the
// "# Preplan: <topic>" heading line.
const preplanSkeletonTail = `
**Status:** in progress

## Goal

## Users and effect

## Flows

## Decisions

| # | Decision | Reason |
|---|---|---|

## Open questions

## Guardrail check

| Proposal | Guardrail | Severity | Result |
|---|---|---|---|
`

// preplanSlug rejects a topic that cannot name a file and returns branch.Slug
// of the topic. It expects a topic the caller already trimmed with
// strings.TrimSpace. A rejected topic returns a DomainError that carries a
// Suggestion. The checks run before any path join, so the slug is always a
// bare file name made of [a-z0-9-]. A topic with no ASCII letter or digit
// (empty, punctuation only, or only non-ASCII letters) gives an empty slug.
func preplanSlug(topic string) (string, error) {
	const action = "preplan_context"
	slug := branch.Slug(topic)
	if slug == "" {
		return "", &mcpserver.DomainError{
			Msg:        fmt.Sprintf("%s: topic %q has no ASCII letter or digit", action, topic),
			Suggestion: preplanTopicASCIISuggestion,
		}
	}
	if n := utf8.RuneCountInString(topic); n > preplanTopicMaxChars {
		return "", &mcpserver.DomainError{
			Msg:        fmt.Sprintf("%s: topic has %d characters, max %d", action, n, preplanTopicMaxChars),
			Suggestion: preplanTopicLongSuggestion,
		}
	}
	if strings.ContainsAny(topic, "\r\n") {
		return "", &mcpserver.DomainError{
			Msg:        action + ": topic has a line break",
			Suggestion: preplanTopicLineSuggestion,
		}
	}
	return slug, nil
}

// planPreplanContext returns the plan guardrails and the topic file path for
// topic. It creates <mainRoot>/.sdlc-v2/preplan/<slug>.md with the skeleton
// when the file is absent and never overwrites an existing file (O_EXCL). It
// starts no plan run and writes no state. A bad topic returns a DomainError
// and writes nothing; a failed create returns an InfraError. The topic is
// trimmed once here, before preplanSlug and the file heading use it.
func planPreplanContext(mainRoot, topic string) (PlanSupportOut, error) {
	const action = "preplan_context"
	topic = strings.TrimSpace(topic)
	slug, err := preplanSlug(topic)
	if err != nil {
		return PlanSupportOut{}, err
	}
	guardrails, warning := loadGuardrails(mainRoot)

	file := filepath.Join(mainRoot, paths.DataDir, paths.PreplanSubdir, slug+".md")
	created, err := createPreplanFile(file, "# Preplan: "+topic+"\n"+preplanSkeletonTail)
	if err != nil {
		cause := err.Error()
		var dl *fsx.DanglingLinkError
		if errors.As(err, &dl) {
			// The recovery text is the Suggestion, so the message keeps only
			// the problem and the caller does not show the recovery twice.
			cause = dl.Problem()
		}
		return PlanSupportOut{}, &mcpserver.InfraError{
			Msg:        fmt.Sprintf("%s: create %s: %s", action, file, cause),
			Suggestion: fsx.RecoveryOr(err, preplanCreateSuggestion),
			Cause:      err,
		}
	}

	var summary string
	switch {
	case warning != "":
		summary = fmt.Sprintf("%d guardrail(s) loaded. Warning: %s. ", len(guardrails), warning)
	case len(guardrails) == 0:
		summary = "0 guardrail(s) loaded — none configured. "
	default:
		summary = fmt.Sprintf("%d guardrail(s) loaded. ", len(guardrails))
	}
	next := "Read preplanFile, then ask the first question."
	if created {
		summary += fmt.Sprintf("Created topic file %s.md.", slug)
	} else {
		summary += fmt.Sprintf("Topic file %s.md exists.", slug)
		next = "Read preplanFile, then continue with its open questions."
	}
	if warning != "" {
		next += " Record the warning in the Guardrail check section."
	}
	return PlanSupportOut{
		Summary:        summary,
		Next:           next,
		Guardrails:     guardrails,
		PreplanFile:    file,
		PreplanCreated: &created,
	}, nil
}

// preplanWriteString writes content to f for createPreplanFile. A test replaces
// it to make the write fail after the create.
var preplanWriteString = func(f *os.File, content string) error {
	_, err := f.WriteString(content)
	return err
}

// preplanCloseFile closes f for createPreplanFile. A test replaces it to make
// the close fail after a successful write.
var preplanCloseFile = func(f *os.File) error {
	return f.Close()
}

// createPreplanFile creates file with content when it is absent and reports
// whether it created the file. It makes the parent directory first. The create
// uses O_EXCL, so an existing regular file stays unchanged and the call
// reports false with no error. A dangling symlink at file or at any folder on
// the path to file returns a *fsx.DanglingLinkError and writes nothing. A
// path at file that cannot be read as a regular file (a link loop, a folder)
// returns an error. A write that fails after the create removes the partial
// file, so the next call starts from a clean state.
func createPreplanFile(file, content string) (bool, error) {
	if err := fsx.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return false, err
	}
	f, err := os.OpenFile(file, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			// O_EXCL also fails on a dangling link, where file does not exist.
			// The parent folder exists, so the walk starts at file. When file
			// links to another link, removing file fixes the whole chain, so
			// the error names file and not the last link of the chain.
			if last, target, ok := fsx.FindDanglingLink(file); ok {
				return false, &fsx.DanglingLinkError{Link: file, Target: target, Remove: true, Chain: last != file, Err: err}
			}
			// Something exists at file. Only a regular file (or a live link
			// to one) is a topic file the caller can read.
			info, statErr := os.Stat(file)
			if statErr != nil {
				return false, statErr
			}
			if !info.Mode().IsRegular() {
				return false, errors.New("exists and is not a regular file")
			}
			return false, nil
		}
		return false, err
	}
	if err := preplanWriteString(f, content); err != nil {
		// Best-effort cleanup; the original write error is returned.
		_ = f.Close()
		_ = os.Remove(file)
		return false, err
	}
	if err := preplanCloseFile(f); err != nil {
		// Best-effort cleanup; the original close error is returned.
		_ = os.Remove(file)
		return false, err
	}
	return true, nil
}

// extractLeadParagraph returns the first non-empty, non-heading paragraph from
// markdown content as a single-line summary.
func extractLeadParagraph(content string) string {
	lines := strings.Split(content, "\n")
	var para []string
	inPara := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			if inPara {
				break // end of first paragraph
			}
			continue
		}
		if strings.HasPrefix(trimmed, "#") {
			if inPara {
				break
			}
			continue // skip headings
		}
		if strings.HasPrefix(trimmed, "---") || strings.HasPrefix(trimmed, "===") {
			continue
		}
		inPara = true
		para = append(para, trimmed)
	}
	result := strings.Join(para, " ")
	// Truncate long summaries.
	if len(result) > 300 {
		result = result[:297] + "..."
	}
	return result
}
