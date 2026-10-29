package tools

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
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

// PlanSupportIn carries the merged input for the actions of the plan_support
// tool (planSupportActions). Each field is consumed by one or more actions
// (noted in comments).
type PlanSupportIn struct {
	Action string `json:"action" jsonschema:"enum=merge_results,enum=material_snapshot,enum=material_compare,enum=openspec_appendix,enum=openspec_instructions,enum=openspec_stage,enum=evidence_record,enum=evidence_digest,enum=evidence_get,enum=preplan_context,enum=preplan_check,enum=preplan_write" jsonschema_description:"Selects the operation: \"merge_results\", \"material_snapshot\", \"material_compare\", \"openspec_appendix\", \"openspec_instructions\", \"openspec_stage\", \"evidence_record\", \"evidence_digest\", \"evidence_get\", \"preplan_context\", \"preplan_check\", or \"preplan_write\". Each action reads only the subset of fields listed in the tool description; unlisted fields are ignored, except section, content and topicStatus: an action other than preplan_write that gets one of them returns DomainError."`

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
	PlanPath string               `json:"planPath,omitempty" jsonschema_description:"openspec_stage only: absolute plan file path, saved in stage.json. It is also the owner key of the staging: a later openspec_stage call for the same changeName with a different non-empty planPath is refused. The owner check is skipped, and the staging is replaced, when this planPath is empty or when the existing stage.json is missing, unreadable, unparseable or has no planPath. Example: /Users/me/.claude/plans/add-widget.md"`

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

	// preplan_context, preplan_check, preplan_write
	Topic string `json:"topic,omitempty" jsonschema_description:"preplan_context, preplan_check and preplan_write (required): plain-text topic name, one line, 1-50 characters after outer spaces are trimmed, with at least one ASCII letter or digit (a-z, A-Z, 0-9). The slug is the topic lowercased, with each run of other characters turned into one hyphen, so two topics can share one slug (\"Auth flow\" and \"auth-flow\" both give auth-flow). An existing file named <slug>.md or YYYY-MM-DD-<slug>.md is reused, whatever its date. preplan_context adds each absent section heading to it. With no such file, preplan_context creates YYYY-MM-DD-<slug>.md with the UTC date of today. preplan_check and preplan_write never create a file: a topic with no file is an error. Two such files return an error and write nothing. Example: auth flow"`

	// preplan_check
	PassCount    *int `json:"passCount,omitempty" jsonschema_description:"preplan_check only (required): integer 0 to 10000. Live proposals (Decisions rows that start with neither Dropped: nor Moved:) whose Reason holds no Kept with warning: note. Each live proposal is in passCount or keptCount, never both. Absent, negative or more than 10000 is rejected. Example: 6"`
	KeptCount    *int `json:"keptCount,omitempty" jsonschema_description:"preplan_check only (required): integer 0 to 10000. Live proposals whose Reason holds a Kept with warning: note after the check. Absent, negative or more than 10000 is rejected. Example: 1"`
	DroppedCount *int `json:"droppedCount,omitempty" jsonschema_description:"preplan_check only (required): integer 0 to 10000. Decisions rows whose Decision cell starts with Dropped:. Absent, negative or more than 10000 is rejected. Example: 0"`

	// preplan_write. Another action that gets one of these fields returns
	// DomainError (preplanWriteFieldError).
	Section     string  `json:"section,omitempty" jsonschema:"enum=Goal,enum=Problem and benefit,enum=Users and effect,enum=Risk if wrong,enum=Non-goals,enum=Flows,enum=Decisions,enum=Open questions,enum=Guardrail check" jsonschema_description:"preplan_write only. One of: Goal, Problem and benefit, Users and effect, Risk if wrong, Non-goals, Flows, Decisions, Open questions, Guardrail check. Omit it to write only the status. Example: Decisions"`
	Content     *string `json:"content,omitempty" jsonschema_description:"preplan_write only. Required with section, not allowed without section. The full new body of section, at most 65536 bytes; a larger body is rejected by rule P1; the body is stored without its final line ends and with the line ends of the file. An empty string clears the body, and deletes the Open questions or Guardrail check section. Outside a code fence no line starts with # or ## (P2), and each code fence closes (P3). Decisions is blank, or the two lines | # | Decision | Reason | and |---|---|---|, then rows of 3 cells, then at most one Smallest cut: line (P4-P7). Non-goals is None stated. or - bullets (P8). Open questions is - bullets (P9). Flows is No flow change. or a mermaid fence (P10). Guardrail check takes only an empty string (P11). Example: Ship the login page first."`
	TopicStatus string  `json:"topicStatus,omitempty" jsonschema:"enum=in progress,enum=ready for plan,enum=paused" jsonschema_description:"preplan_write only. The new Status line value: in progress, ready for plan or paused. Example: ready for plan"`
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
	StartedAt string `json:"startedAt"` // UTC RFC 3339: the writer's first stamped write (set by this call when the file had none)
	UpdatedAt string `json:"updatedAt"` // UTC RFC 3339: the time of this call
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
	StagingDir     string                `json:"stagingDir,omitempty" jsonschema_description:"openspec_stage only: the staging folder relative to stagingRoot, in the form the plan's **OpenSpec-Staging:** header uses. Absent on other actions. Example: .sdlc-v2/openspec-staging/add-widget/"`
	StagingRoot    string                `json:"stagingRoot,omitempty" jsonschema_description:"openspec_stage only: absolute path of the root that holds stagingDir. It is the main worktree, or the active worktree when the main worktree could not be resolved (the summary then holds a warning). Absent on other actions. Example: /Users/me/repo"`
	StagedFiles    []openspec.StagedFile `json:"files,omitempty" jsonschema_description:"openspec_stage only: each staged file with its path relative to stagingDir and its SHA-256. Absent on other actions."`
	Valid          *bool                 `json:"valid,omitempty" jsonschema_description:"openspec_stage only: true when openspec validate --strict passed and no diagram contrast problem was found. Absent on other actions."` // pointer so false still renders
	ValidateOutput string                `json:"validateOutput,omitempty" jsonschema_description:"openspec_stage only: the openspec validate output, plus one diagram contrast line for each problem. Absent when empty."`
	// StagingReplaced is true when the call replaced an existing staging dir,
	// false when it created one. Pointer so false still renders.
	StagingReplaced *bool `json:"stagingReplaced,omitempty" jsonschema_description:"openspec_stage only: true when the call replaced an existing staging folder, false when it created one. Absent on other actions."`
	// StagedPlanPath is the planPath written to stage.json, the owner key of
	// the staging. Empty (omitted) when the call passed no planPath.
	StagedPlanPath string `json:"stagedPlanPath,omitempty" jsonschema_description:"openspec_stage only: the planPath written to stage.json, the owner key of the staging. Absent when the call passed no planPath."`

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
	PreplanFile    string `json:"preplanFile,omitempty" jsonschema_description:"preplan_context, preplan_check and preplan_write: absolute path of the topic file, <main worktree>/.sdlc-v2/preplan/<slug>.md or YYYY-MM-DD-<slug>.md. Absent on other actions."`
	PreplanCreated *bool  `json:"preplanCreated,omitempty"` // true only when this call created the file
	// AddedSections is a pointer so an empty list still renders (none) for
	// preplan_context while every other action omits it.
	AddedSections *[]string `json:"addedSections,omitempty" jsonschema_description:"preplan_context only: the ## section headings that this call added to an existing topic file, in skeleton order (Goal, Problem and benefit, Users and effect, Risk if wrong, Non-goals, Flows, Decisions). (none) when the call created the file or the file already had every heading."`

	// preplan_check (PreplanFile above is also set by this action).
	GuardrailCheckLine string `json:"guardrailCheckLine,omitempty"` // the check line as written, with no line end
	// EmptySections is a pointer so an empty list still renders (none) for
	// preplan_check while every other action omits it.
	EmptySections *[]string `json:"emptySections,omitempty" jsonschema_description:"preplan_check only: the ## headings of the skeleton sections that are absent or hold only blank lines, in skeleton order (Goal, Problem and benefit, Users and effect, Risk if wrong, Non-goals, Flows, Decisions). Open questions is not a skeleton section. Decisions is empty when its table has no row: no line that starts with | (after leading spaces) below the header and separator rows. (none) when every section has content."`

	// preplan_write (PreplanFile above is also set by this action). Each is a
	// pointer so an empty list or 0 still renders for preplan_write while
	// every other action omits it.
	Written         *[]string `json:"written,omitempty" jsonschema_description:"preplan_write only: the parts that this call wrote, the section name and/or Status. (none) on a no-op. Absent on other actions. Example: [Decisions, Status]"`
	ReadyTopicFiles *[]string `json:"readyTopicFiles,omitempty" jsonschema_description:"preplan_write only, with topicStatus ready for plan: absolute paths of this topic file first, then up to 3 other ready topic files, newest first. Never empty. Absent otherwise. Example: [/repo/.sdlc-v2/preplan/2026-10-29-auth-flow.md]"`
	ReadyLeftOut    *int      `json:"readyLeftOut,omitempty" jsonschema_description:"preplan_write only, with topicStatus ready for plan: the count of other ready topic files that are not in readyTopicFiles. 0 still renders. Absent otherwise. Example: 0"`
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

Pass "action" to select an operation. Each action uses a subset of the input fields (unlisted fields are ignored, except section, content and topicStatus: an action other than preplan_write that gets one of them returns DomainError):

- merge_results: Merge lane/lens review results. Requires at least one of laneResults or lensResults. Optional: expectedGates, isRedispatch. Lane status must be pass or fail, and every issue severity must be blocking or advisory; any other value fails with a DomainError and nothing is merged. Every issue in allIssues carries an "id" field: "f-" plus the first 8 hex chars of sha256(gateId|lower(trim(summary))). The same gate and summary always give the same id. An "id" in the input is ignored.
- material_snapshot: Snapshot plan material for change detection. Requires filePath. Returns snapshotPath.
- material_compare: Compare current plan material against a snapshot. Requires filePath, snapshotPath (from material_snapshot).
- openspec_appendix: Generate an openspec appendix. Requires changeName. Optional: proposalPath, designPath, specPaths, planTasks.
- openspec_instructions: Return the artifact templates, instructions, rules and the active plan guardrails for a new OpenSpec change, from a temp copy of openspec/config.yaml. Requires changeName. Returns schemaName, artifacts, guardrails. Writes nothing in the repository; a missing openspec CLI returns InfraError.
- openspec_stage: Write the authored artifacts to `+stagingFolderText("<changeName>")+` (replaces the whole directory), so a run in any worktree of the repo finds them, and validate them in a temp copy of the active worktree. When the main worktree cannot be resolved, it writes under the active worktree and the summary holds a warning; a git output over the size cap while it resolves the main worktree is an InfraError. Requires changeName, files. Optional: planPath. Returns stagingDir (relative to stagingRoot), stagingRoot (absolute path of the main worktree, or of the fallback root), files, valid, validateOutput, stagingReplaced (true when an existing staging dir was replaced, false when it was created) and stagedPlanPath (the planPath written to stage.json; absent when none was passed). The summary names the absolute staging folder. A bad name or path, or a current spec under openspec/specs/ over 1 MiB, returns DomainError and writes nothing. A changeName whose staging dir holds a stage.json with another planPath returns DomainError 'staged by another plan' and writes nothing; a missing, unreadable or unparseable stage.json, or an empty planPath, skips this check. A current spec that cannot be read returns InfraError.
- evidence_record: Store a writer's status and items (upsert by id) in the plan run's evidence directory. Each write sets updatedAt (UTC). A write to a writer file that has no startedAt sets startedAt to that updatedAt; later writes keep it. A file written before startedAt existed, or an unreadable file that this call replaces, gets startedAt at this later write, so its startedAt can be later than the writer's real start. Requires runId, writerId. Optional: status, items, brief (writerId main only). Returns record (writerId, status, itemCount, fileBytes, briefPath, startedAt, updatedAt). Invalid input or a limit breach returns DomainError and writes nothing; an OS read/write failure returns InfraError.
- evidence_digest: Compact run summary for resume and polling; never returns item bodies. Requires runId. Optional: expectedWriters, timeoutSeconds, statusOnly. Returns writers, plus digest unless statusOnly. Invalid input or a limit breach returns DomainError and writes nothing; an OS read/write failure returns InfraError.
- evidence_get: Full item bodies. Requires runId and at least one of ids or writerIds. Returns get. Invalid input or a limit breach returns DomainError and writes nothing; an OS read/write failure returns InfraError.
`+preplanToolDescription(),
		mcpserver.Annotations{
			Title:      "Plan support and evidence store",
			ReadOnly:   true,
			Idempotent: true,
			OpenWorld:  false,
		},
		func(ctx mcpserver.Ctx, in PlanSupportIn) (PlanSupportOut, error) {
			// An unresolved active worktree stays empty: the openspec_*
			// actions that read the active worktree refuse. openspec_stage
			// writes the staging folder under the main root.
			contentRoot, err := worktree.ActiveRoot()
			if err != nil {
				contentRoot = ""
			}
			root, warning, err := planSupportMainRoot(contentRoot)
			if err != nil {
				return PlanSupportOut{}, err
			}
			out, err := planSupportCore(root, contentRoot, in)
			// Only openspec_stage reports the fallback: a later run in
			// another worktree looks for its staging folder under the main
			// worktree, so the caller must know it was written elsewhere.
			if err == nil && warning != "" && in.Action == "openspec_stage" {
				out.Summary = strings.TrimSpace(out.Summary + " Warning: " + warning)
			}
			return out, err
		},
	)
}

// planSupportMainRoot resolves the main root of plan_support. A git output
// over the execx cap is an InfraError (see mainRootOrWarning). When the main
// worktree cannot be resolved for any other reason, the root is contentRoot
// (the active worktree), or the current directory when contentRoot is empty,
// and warning names the error and the root used. The caller adds warning to
// the Summary of openspec_stage, which then reports that it wrote the
// staging folder under that root and not under the main worktree.
func planSupportMainRoot(contentRoot string) (root, warning string, err error) {
	root, warning, err = mainRootOrWarning("plan_support")
	if err != nil || warning == "" {
		return root, "", err
	}
	root = contentRoot
	if root == "" {
		cwd, cwdErr := os.Getwd()
		if cwdErr != nil {
			return "", "", &mcpserver.InfraError{
				Msg:        "plan_support: resolve root: " + warning + " " + cwdErr.Error(),
				Suggestion: "Run plan_support from inside a git repository (or a worktree of one) so the main root can be resolved, then retry.",
				Cause:      cwdErr,
			}
		}
		root = cwd
	}
	return root, warning + " Used " + root + " as the main root.", nil
}

// ---------------------------------------------------------------------------
// Core dispatcher
// ---------------------------------------------------------------------------

// planSupportCore dispatches to the correct action handler. mainRoot anchors
// filesystem lookups; contentRoot anchors worktree-relative operations and is
// empty when the active worktree could not be resolved.
func planSupportCore(mainRoot, contentRoot string, in PlanSupportIn) (PlanSupportOut, error) {
	if err := preplanWriteFieldError(in); err != nil {
		return PlanSupportOut{}, err
	}
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
		return openspecStage(mainRoot, contentRoot, in)
	case "evidence_record":
		return evidenceRecord(mainRoot, in)
	case "evidence_digest":
		return evidenceDigest(mainRoot, in)
	case "evidence_get":
		return evidenceGet(mainRoot, in)
	case preplanContextAction:
		return planPreplanContext(mainRoot, in.Topic)
	case preplanCheckAction:
		return planPreplanCheck(mainRoot, in)
	case preplanWriteAction:
		return planPreplanWrite(mainRoot, in)
	default:
		last := len(planSupportActions) - 1
		return PlanSupportOut{}, unknownActionError("action", in.Action,
			" — valid actions: "+strings.Join(planSupportActions, ", "),
			"call plan_support again with action set to exactly one of "+
				strings.Join(planSupportActions[:last], ", ")+" or "+planSupportActions[last])
	}
}

// planSupportActions lists the actions of plan_support in the order of the
// action enum tag of PlanSupportIn. The unknown-action error names them, and
// TestPlanSupportActionList pins the enum and description tags to it.
var planSupportActions = []string{
	"merge_results", "material_snapshot", "material_compare",
	"openspec_appendix", "openspec_instructions", "openspec_stage",
	"evidence_record", "evidence_digest", "evidence_get",
	preplanContextAction, preplanCheckAction, preplanWriteAction,
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
func mapOpenspecError(action, mainRoot string, in PlanSupportIn, err error) error {
	var staged *openspec.StagedByOtherPlanError
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
	case errors.As(err, &staged):
		other := staged.PlanPath
		return &mcpserver.DomainError{
			Msg: fmt.Sprintf("%s: change %q is staged by another plan: %s. If this call names the same plan file under another path, call again with planPath set to %s.",
				action, in.ChangeName, other, other),
			Suggestion: fmt.Sprintf("If %s is the plan file of this call under another path, call openspec_stage again with planPath set to %s. "+
				"If %s is another plan, pick another change name: replace %s in every header line that holds it (**Source:**, **OpenSpec-Create:** and **OpenSpec-Staging:**), then run Create-flow authoring again. "+
				"If that other plan is abandoned, ask the user, then delete %s and call openspec_stage again.",
				other, other, other, in.ChangeName, filepath.Join(mainRoot, filepath.FromSlash(openspec.StagingDirRel(in.ChangeName)))),
			Cause: err,
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
		return PlanSupportOut{}, mapOpenspecError(action, mainRoot, in, err)
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

// openspecStage writes the authored artifacts to the main worktree's
// staging dir and validates a temp copy of them.
func openspecStage(mainRoot, contentRoot string, in PlanSupportIn) (PlanSupportOut, error) {
	const action = "openspec_stage"
	if err := requireActiveRoot(action, contentRoot); err != nil {
		return PlanSupportOut{}, err
	}
	if !openspec.ValidChangeName(in.ChangeName) {
		return PlanSupportOut{}, mapOpenspecError(action, mainRoot, in, openspec.ErrInvalidChangeName)
	}
	if len(in.Files) == 0 {
		return PlanSupportOut{}, &mcpserver.DomainError{
			Msg:        action + ": files is required",
			Suggestion: "Pass files as a JSON array of {path, content}, one entry per artifact file from openspec_instructions.",
		}
	}
	res, err := openspec.Stage(contentRoot, mainRoot, in.ChangeName, in.Files, in.PlanPath, nil)
	if err != nil {
		return PlanSupportOut{}, mapOpenspecError(action, mainRoot, in, err)
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
		StagingDir:      res.StagingDir,
		StagingRoot:     mainRoot,
		StagedFiles:     res.Files,
		Valid:           &valid,
		ValidateOutput:  validateOutput,
		StagingReplaced: &res.Replaced,
		StagedPlanPath:  res.PlanPath,
	}
	absDir := filepath.Join(mainRoot, filepath.FromSlash(res.StagingDir)) + string(filepath.Separator)
	if valid {
		out.Summary = fmt.Sprintf("Staged %d file(s) for OpenSpec change %q in %s; validation passed.", len(res.Files), in.ChangeName, absDir)
		out.Next = "Staged and valid. Add the **OpenSpec-Staging:** header to the plan."
	} else {
		out.Summary = fmt.Sprintf("Staged %d file(s) for OpenSpec change %q in %s; validation failed.", len(res.Files), in.ChangeName, absDir)
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

// The values of the "**Status:**" line of a preplan topic file. The preplan
// skill (plugins/sdlc/skills/preplan/SKILL.md, "Topic file status") lists the
// same values, and TestDashboardPreplans_StatusParity pins the two lists.
const (
	preplanStatusInProgress   = "in progress"
	preplanStatusReadyForPlan = "ready for plan"
	preplanStatusPaused       = "paused"
)

// PreplanStatuses is the canonical list of preplan statuses, in order. It is
// exported for the tests of other packages: TestPreplanStatusParity pins the
// PREPLAN_STATUSES chips of the dashboard page (view.js) to it, and the
// end-to-end test writes topic files with its values.
var PreplanStatuses = []string{preplanStatusInProgress, preplanStatusReadyForPlan, preplanStatusPaused}

// preplanSections is the canonical order of the "## " sections of a topic
// file. The skeleton, the heading repair of preplan_context and the
// emptySections list of preplan_check read it.
var preplanSections = []string{
	preplanGoalSection, "Problem and benefit", "Users and effect", "Risk if wrong",
	preplanNonGoalsSection, preplanFlowsSection, preplanDecisionsSection,
}

// The section names that code reads by name: the skeleton sections that the
// format checker (preplan_format.go) gives its own rules, and the two
// sections that are not in the skeleton. preplanSections, preplanWriteSections
// and the checker read them, so each name has one definition.
const (
	preplanGoalSection      = "Goal"
	preplanNonGoalsSection  = "Non-goals"
	preplanFlowsSection     = "Flows"
	preplanDecisionsSection = "Decisions" // its body starts with the table header

	preplanOpenQuestionsSection  = "Open questions"
	preplanGuardrailCheckSection = "Guardrail check"
)

// preplanTitlePrefix starts the title line of a topic file.
const preplanTitlePrefix = "# "

// preplanHeadingPrefix starts each section heading line of a topic file.
const preplanHeadingPrefix = "## "

// preplanDecisionsHeader is the table header of ## Decisions, with LF line ends.
const preplanDecisionsHeader = "| # | Decision | Reason |\n|---|---|---|\n"

// preplanSectionBlock returns one empty section with eol line ends:
// "## <name>" eol eol, then preplanDecisionsHeader (eol line ends) for Decisions.
func preplanSectionBlock(name, eol string) string {
	block := preplanHeadingPrefix + name + eol + eol
	if name == preplanDecisionsSection {
		block += strings.ReplaceAll(preplanDecisionsHeader, "\n", eol)
	}
	return block
}

// buildPreplanSkeletonTail joins the empty section blocks of preplanSections.
// A blank line separates the Started line from the first section.
func buildPreplanSkeletonTail() string {
	tail := "\n"
	for _, s := range preplanSections {
		tail += preplanSectionBlock(s, "\n")
	}
	return tail
}

// preplanToolDescription returns the preplan_context, preplan_check and
// preplan_write lines of the plan_support tool description. Each number and
// the section order come from the constants that the code uses
// (preplanSections, preplanDirMax, dashboardDeleteReadMaxText,
// dashboardPreplanHeadMax, preplanCountMax), so the text cannot go stale;
// TestPreplanToolDescription pins it. The preplan_write line comes from
// preplanWriteDescription. It is called once, at registration.
func preplanToolDescription() string {
	sections := strconv.Itoa(len(preplanSections))
	order := strings.Join(preplanSections, ", ")
	dirMax := strconv.Itoa(preplanDirMax)
	return "- preplan_context: Return the plan guardrails and the topic file path. Requires topic. No optional fields. " +
		"It lists the names in <main-worktree>/.sdlc-v2/preplan/ (at most " + dirMax + ") and uses the one file named exactly <slug>.md or YYYY-MM-DD-<slug>.md. " +
		"With no such file it creates YYYY-MM-DD-<slug>.md (the UTC date of today, mode 0644, O_EXCL so it never replaces a file) with the " + sections + "-section skeleton (the ## sections " + order + ", in this skeleton order) under a **Started:** line (UTC, RFC 3339). " +
		"With such a file it adds each skeleton heading that the file lacks, after its nearest earlier section, and replaces the file by a temp file and a rename that keeps the file mode. " +
		"When a code fence (``` or ~~~) is still open at the end of the file, it first adds a closing fence line there, so the added headings are not code, and summary names that line. " +
		"It writes nothing when every heading is there, so a second call writes nothing. " +
		"The repair needs no **Status:** or **Started:** line, because it only inserts headings; preplan_check needs one of those lines as the place of its check line, so it refuses such a file with DataError. " +
		"A bad topic returns DomainError and writes nothing. A folder that cannot be listed (not a missing folder), or that holds more than " + dirMax + " names, returns InfraError. " +
		"Two or more such files return DataError that names them; nothing is written. A failed create returns InfraError. " +
		"A dangling symlink on the path to the topic file returns InfraError that names the link and its missing target; nothing is written. " +
		"Its Suggestion for a folder link is \"Start a new session so the session-start hook repairs the link, or run: mkdir -p <target>\"; for a topic-file link it is \"Remove the link, then try again: rm <link>\". " +
		"Before the read it refuses a live link, a link loop, a folder, a pipe or a device at the path with the same DomainError and Suggestion as preplan_check. " +
		"A file over " + dashboardDeleteReadMaxText + ", or a repair that would make it larger than " + dashboardDeleteReadMaxText + ", returns DataError and writes nothing. " +
		"A file that went away after the folder list returns DomainError. A failed Lstat, read or write returns InfraError and leaves the file as it was. Every error carries a Suggestion. " +
		"Returns guardrails, preplanFile (absolute path), preplanCreated (true when this call created the file, false when it already existed), addedSections (the headings this call added, in skeleton order, (none) when it added none), summary, next.\n" +
		"- preplan_check: Write the guardrail check line of an existing topic file. Call it once at the end of each preplan Step 4, after the skill counts the proposals. " +
		"Requires topic, passCount, keptCount, droppedCount (each a whole number from 0 to " + strconv.Itoa(preplanCountMax) + "). No optional fields. " +
		"It finds the file as preplan_context does, never creates one, and refuses a file that is not a regular file. " +
		"It reads at most " + dashboardDeleteReadMaxText + ", loads the plan guardrails for the count, and replaces the first **Guardrail check:** line in the first " + strconv.Itoa(dashboardPreplanHeadMax) + " bytes, else adds one after the **Started:** line, else after the **Status:** line. " +
		"The line is \"**Guardrail check:** <guardrails> guardrails, <sum> proposals: <passCount> pass, <keptCount> kept, <droppedCount> dropped. Last check: <UTC time, RFC 3339>.\" " +
		"When the plan config cannot be read, the call still succeeds: the line ends with \" Guardrails unavailable: <warning>.\" and summary names the warning. " +
		"It writes a temp file and renames it, and the file keeps its mode. " +
		"Returns preplanFile, guardrailCheckLine, emptySections (the ## headings of the " + sections + " skeleton sections, " + order + ", that are absent or hold only blank lines, in skeleton order; Decisions is empty when its table has no row below the header and separator rows, where a row is a line that starts with | after its leading spaces; (none) when every section has content), " +
		"summary (added or replaced, the old line, the count of other check lines that stay in the file, the empty sections, and a code fence that is still open at the end of the file, which makes each ## heading after it count as absent), next. " +
		"A bad count or topic, a missing file, or a link, a folder or another file that is not regular returns DomainError and writes nothing; two matching files, a file over " + dashboardDeleteReadMaxText + " or no place for the line returns DataError; a list, Lstat, read or write failure returns InfraError and leaves the file as it was. A dangling link at the preplan folder returns InfraError.\n" +
		preplanWriteDescription()
}

// preplanSkeletonTail is the text of a new topic file that follows the
// "**Started:**" line (see preplanSkeleton).
var preplanSkeletonTail = buildPreplanSkeletonTail()

// preplanSpan is one "## " heading line outside a fence: its name, the
// byte offsets of the heading line and of the body after it.
type preplanSpan struct {
	name                 string
	lineStart, bodyStart int
	bodyEnd              int // start of the next "## " line outside a fence, or len(data)
}

// preplanSectionSpans returns every "## " heading of data outside a fence,
// in file order, and openFence: the fence run (for example "```" or "~~~~")
// that is still open at the end of data ("" when no fence is open). The
// readers that cut a topic file into sections use it, so they agree about
// where a section starts and ends.
//
// The name of a span is the text after "## " with its trailing spaces, tabs
// and CR removed. A "###" line is not a heading: it is body of the section
// above it.
//
// A fence opens at a column-0 run of 3 or more backticks or tildes
// (preplanFenceRun). It closes at a line that holds only a run of the same
// character, at least as long as the opening run, then spaces or tabs. A fence
// that never closes stays open to the end of data. This rule differs from
// fenceLines (validators.go) and anyFenceLineRe (plan_gates.go) on purpose: a
// topic file is Markdown that a person edits and a renderer shows, so the rule
// follows the renderer. A renderer opens a fence with "~~~" too, and it shows
// every line after an unclosed fence as code. fenceLines treats an unclosed
// fence as no fence, so it would count a "## " line that the reader sees as
// code as a heading.
func preplanSectionSpans(data []byte) (spans []preplanSpan, openFence string) {
	for start := 0; start < len(data); {
		end := min(preplanLineEnd(data, start)+1, len(data)) // after the line feed
		line := string(data[start:end])
		wasOpen := openFence != ""
		openFence = preplanFenceStep(openFence, line)
		switch {
		case wasOpen || openFence != "":
			// a line inside a fence, or the line that opens or closes it
		case strings.HasPrefix(line, preplanHeadingPrefix):
			if n := len(spans); n > 0 {
				spans[n-1].bodyEnd = start
			}
			spans = append(spans, preplanSpan{
				name:      strings.TrimRight(strings.TrimPrefix(line, preplanHeadingPrefix), " \t\r\n"),
				lineStart: start,
				bodyStart: end,
				bodyEnd:   len(data),
			})
		}
		start = end
	}
	return spans, openFence
}

// preplanFenceRun returns the run of 3 or more backticks or 3 or more tildes
// that starts line, or "" when line starts with no such run.
func preplanFenceRun(line string) string {
	if line == "" || (line[0] != '`' && line[0] != '~') {
		return ""
	}
	n := 1
	for n < len(line) && line[n] == line[0] {
		n++
	}
	if n < 3 {
		return ""
	}
	return line[:n]
}

// addPreplanHeadings returns data with a heading block for each
// preplanSections name that has no heading line, and the added headings
// ("## <name>") in preplanSections order. closed is the fence run of the
// closing line that it added, or "" when it added none. With no absent name
// it returns data itself, nil and "". It never changes a byte of data outside
// the inserted text.
//
// The names go in one at a time, in list order, into the updated text. An
// absent name goes at the end of the body of its nearest earlier present
// name, else before the first heading, else at the end of the text. The
// place is never before the end of the last head line (Status, Started,
// Guardrail check) in preplanHead. A fence still open at the end of the text
// gets a closing line before blocks that go there, so the blocks are not
// code. The inserted text uses the first line end of data (CRLF or LF, LF
// when data has none).
func addPreplanHeadings(data []byte) (next []byte, added []string, closed string) {
	eol := preplanFileEOL(data)
	next = data
	for idx, name := range preplanSections {
		spans, openFence := preplanSectionSpans(next)
		if preplanFindSpan(spans, name) >= 0 {
			continue
		}
		at := len(next)
		if len(spans) > 0 {
			at = spans[0].lineStart
		}
		for j := idx - 1; j >= 0; j-- {
			if k := preplanFindSpan(spans, preplanSections[j]); k >= 0 {
				at = spans[k].bodyEnd
				break
			}
		}
		if h := preplanHeadEnd(next); at < h {
			at = h
		}

		before := string(next[:at])
		var insert string
		if at == len(next) && openFence != "" {
			if before != "" && !strings.HasSuffix(before, "\n") {
				insert += eol
			}
			insert += openFence + eol
			closed = openFence
		}
		insert += preplanSpacing(before+insert, eol) + preplanSectionBlock(name, eol)

		out := make([]byte, 0, len(next)+len(insert))
		out = append(out, next[:at]...)
		out = append(out, insert...)
		out = append(out, next[at:]...)
		next = out
		added = append(added, preplanHeadingPrefix+name)
	}
	return next, added, closed
}

// preplanFileEOL returns the line end that text added to the topic file data
// uses: the line end of the first line of data, CRLF or LF (LF when data has
// no line end).
func preplanFileEOL(data []byte) string {
	return preplanEOL(data, preplanLineEnd(data, 0))
}

// preplanFindSpan returns the index of the first span named name, or -1.
func preplanFindSpan(spans []preplanSpan, name string) int {
	for i, s := range spans {
		if s.name == name {
			return i
		}
	}
	return -1
}

// preplanEmptySections returns the "## <name>" heading of each
// preplanSections name whose heading is absent or whose body holds only
// blank lines, in preplanSections order. Decisions is empty when its
// table has no data row. empty is a non-nil slice. openFence is the fence run
// that is still open at the end of data, or "" (see preplanSectionSpans):
// each heading after an unclosed fence counts as absent, so the caller names
// the open fence.
//
// preplanSectionSpans finds the headings and the fences, so this list and
// addPreplanHeadings agree about a heading. The first heading of a name
// counts. A blank line holds only spaces, tabs and CR.
func preplanEmptySections(data []byte) (empty []string, openFence string) {
	var spans []preplanSpan
	spans, openFence = preplanSectionSpans(data)
	return preplanEmptySectionsOf(data, spans), openFence
}

// preplanEmptySectionsOf is preplanEmptySections for a caller that already
// holds spans, the preplanSectionSpans of data. It returns the same non-nil
// list and scans data no second time.
func preplanEmptySectionsOf(data []byte, spans []preplanSpan) (empty []string) {
	empty = []string{}
	for _, name := range preplanSections {
		i := preplanFindSpan(spans, name)
		if i < 0 {
			empty = append(empty, preplanHeadingPrefix+name)
			continue
		}
		body := data[spans[i].bodyStart:spans[i].bodyEnd]
		blank := len(bytes.TrimLeft(body, " \t\r\n")) == 0
		if blank || (name == preplanDecisionsSection && !preplanHasDecisionRow(body)) {
			empty = append(empty, preplanHeadingPrefix+name)
		}
	}
	return empty
}

// preplanHasDecisionRow reports whether body, the body of ## Decisions, holds
// a table row after the header row and the separator row. A table row is a
// line that starts with "|" after its leading spaces and tabs. The first such
// line is the header row. The second is the separator row when
// tableSeparatorRe (plan_gates.go) matches it: "|", an optional ":", then 3
// or more "-". A shorter separator such as "|-|" counts as a data row. Other
// text is not a row.
func preplanHasDecisionRow(body []byte) bool {
	rows := 0
	for _, line := range bytes.Split(body, []byte("\n")) {
		if !bytes.HasPrefix(bytes.TrimLeft(line, " \t"), []byte("|")) {
			continue
		}
		rows++
		if rows == 2 && tableSeparatorRe.Match(line) {
			continue
		}
		if rows > 1 {
			return true
		}
	}
	return false
}

// preplanHeadEnd returns the offset after the line end of the last head line
// (Status, Started or Guardrail check) that starts in preplanHead(data), or 0
// when the head has none. A head line with no line end ends at len(data).
func preplanHeadEnd(data []byte) int {
	head := preplanHead(data)
	end := 0
	for _, re := range []*regexp.Regexp{dashboardPreplanStatusRe, dashboardPreplanStartedRe, preplanGuardrailCheckRe} {
		for _, loc := range re.FindAllIndex(head, -1) {
			// The line end comes from the full data: a head line can run past
			// the end of head.
			e := min(preplanLineEnd(data, loc[0])+1, len(data))
			if e > end {
				end = e
			}
		}
	}
	return end
}

// preplanSpacing returns the text that goes between before and a new block:
// a line end when before does not end with one, then a blank line when the
// line before the block is not blank. An empty before needs no spacing.
func preplanSpacing(before, eol string) string {
	if before == "" {
		return ""
	}
	var s string
	if !strings.HasSuffix(before, "\n") {
		s = eol
		before += eol
	}
	body := strings.TrimSuffix(before, "\n")
	last := body[strings.LastIndexByte(body, '\n')+1:]
	if strings.TrimRight(last, " \t\r") != "" {
		s += eol
	}
	return s
}

// preplanNow is the clock of preplan_context and preplan_check; tests replace
// it to fix the date of a new topic file, its Started line and the time of
// the Guardrail check line.
var preplanNow = time.Now

// preplanDirMax is the most names findPreplanFiles reads from the preplan
// folder. It is a variable so a test can lower it.
var preplanDirMax = 10000

// errPreplanDirFull reports a preplan folder with more than preplanDirMax names.
var errPreplanDirFull = errors.New("the folder holds too many names")

// preplanDateLayout is the time layout of the date prefix of a new topic file
// name. preplanDatedRe matches the same form.
const preplanDateLayout = "2006-01-02"

// preplanDatedRe matches the date prefix YYYY-MM-DD- of a topic file name: the
// form of preplanDateLayout, then one hyphen.
var preplanDatedRe = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}-`)

// findPreplanFiles returns the names in dir that are the topic file of slug:
// exactly <slug>.md, or exactly YYYY-MM-DD-<slug>.md. The names are in name
// order. A missing dir gives no names and no error. It reads at most
// preplanDirMax+1 names; more than preplanDirMax gives errPreplanDirFull.
func findPreplanFiles(dir, slug string) ([]string, error) {
	d, err := os.Open(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer d.Close()
	all, err := d.Readdirnames(preplanDirMax + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if len(all) > preplanDirMax {
		return nil, errPreplanDirFull
	}
	want := slug + ".md"
	// prefix is the length of "YYYY-MM-DD-". A name that preplanDatedRe
	// matches is at least that long.
	const prefix = len(preplanDateLayout) + 1
	var names []string
	for _, n := range all {
		if n == want || (preplanDatedRe.MatchString(n) && n[prefix:] == want) {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	return names, nil
}

// preplanTopic is a trimmed topic and the slug that preplanSlug made from it.
type preplanTopic struct {
	name string
	slug string
}

// preplanMatches lists dir and returns the 0 or 1 topic file names for
// topic.slug. A list error or a full folder returns InfraError, two or more
// matches return DataError. Each message starts with action + ":".
func preplanMatches(action, dir string, topic preplanTopic) ([]string, error) {
	names, err := findPreplanFiles(dir, topic.slug)
	if err != nil {
		// An open or list error of the os package already names the folder,
		// so the message does not repeat it. errPreplanDirFull does not name
		// it, so its message adds the folder and the limit.
		msg := fmt.Sprintf("%s: list: %v", action, err)
		suggestion := fmt.Sprintf("Make %s a folder that you can read: remove or rename the file or link at that path, or give your user read permission. Then run /sdlc:preplan again.", dir)
		if errors.Is(err, errPreplanDirFull) {
			msg = fmt.Sprintf("%s: list %s: the folder holds more than %d names", action, dir, preplanDirMax)
			suggestion = fmt.Sprintf("Move old topic files out of %s until it holds at most %d names. Then run /sdlc:preplan again.", dir, preplanDirMax)
		}
		return nil, &mcpserver.InfraError{Msg: msg, Suggestion: suggestion, Cause: err}
	}
	if len(names) > 1 {
		list := strings.Join(names, ", ")
		return nil, &mcpserver.DataError{
			Msg:        fmt.Sprintf("%s: topic %q matches %d topic files in %s: %s", action, topic.name, len(names), dir, list),
			Suggestion: fmt.Sprintf("Rename or delete one of: %s. Then run /sdlc:preplan again.", list),
		}
	}
	return names, nil
}

// preplanSkeleton returns the text of a new topic file: the heading, the
// status line, the Started line (UTC, RFC 3339) and the empty sections.
func preplanSkeleton(topic string, started time.Time) string {
	return preplanTitlePrefix + "Preplan: " + topic + "\n\n**Status:** " + preplanStatusInProgress +
		"\n\n**Started:** " + started.UTC().Format(time.RFC3339) + "\n" + preplanSkeletonTail
}

// preplanSlug rejects a topic that cannot name a file and returns branch.Slug
// of the topic. It expects a topic the caller already trimmed with
// strings.TrimSpace. A rejected topic returns a DomainError that carries a
// Suggestion; its message starts with action + ":". The checks run before any
// path join, so the slug is always a bare file name made of [a-z0-9-]. A topic
// with no ASCII letter or digit (empty, punctuation only, or only non-ASCII
// letters) gives an empty slug.
func preplanSlug(action, topic string) (string, error) {
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
// topic. It lists <mainRoot>/.sdlc-v2/preplan/ and uses the one file named
// exactly <slug>.md or YYYY-MM-DD-<slug>.md (findPreplanFiles). With no such
// file it creates YYYY-MM-DD-<slug>.md (UTC date of preplanNow) with the
// skeleton and its Started line (createPreplanFile, O_EXCL). With such a file
// it runs repairPreplanHeadings, which reads the file (readPreplanTopic),
// adds each absent section heading (addPreplanHeadings) and replaces the file
// by a temp file and a rename. It starts no plan run and writes no state.
//
// Errors, in the order of the steps:
//   - A bad topic: DomainError, nothing written (preplanSlug).
//   - A folder that cannot be listed or holds more than preplanDirMax names:
//     InfraError. Two or more matching files: DataError (preplanMatches).
//   - A failed create, or a dangling link on the path: InfraError.
//   - A topic path that is a link, a folder or another file that is not
//     regular: DomainError (readPreplanTopic, the same text as preplan_check).
//   - A file over dashboardDeleteReadMax, or a repair that would make it
//     larger than that: DataError, nothing written.
//   - A file that went away after the folder listing: DomainError.
//   - A failed Lstat, read or write: InfraError, the file stays as it was.
//
// The topic is trimmed once here, before preplanSlug and the file heading use it.
func planPreplanContext(mainRoot, topic string) (PlanSupportOut, error) {
	const action = preplanContextAction
	topic = strings.TrimSpace(topic)
	slug, err := preplanSlug(action, topic)
	if err != nil {
		return PlanSupportOut{}, err
	}
	guardrails, warning := loadGuardrails(mainRoot)

	dir := filepath.Join(mainRoot, paths.DataDir, paths.PreplanSubdir)
	names, err := preplanMatches(action, dir, preplanTopic{name: topic, slug: slug})
	if err != nil {
		return PlanSupportOut{}, err
	}
	now := preplanNow().UTC()
	name := now.Format(preplanDateLayout) + "-" + slug + ".md"
	if len(names) > 0 {
		name = names[0]
	}
	file := filepath.Join(dir, name)
	created, err := createPreplanFile(file, preplanSkeleton(topic, now))
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
	added := []string{}
	var closed string
	if !created {
		if added, closed, err = repairPreplanHeadings(action, file, dir); err != nil {
			return PlanSupportOut{}, err
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
		summary += fmt.Sprintf("Created topic file %s.", name)
	} else {
		summary += fmt.Sprintf("Topic file %s exists.", name)
		next = "Read preplanFile, then " + preplanFirstGap
		if len(added) > 0 {
			summary += fmt.Sprintf(" Added %d section heading(s): %s.", len(added), strings.Join(added, ", "))
			next = fmt.Sprintf("Read preplanFile: preplan_context added %d section heading(s). Then %s", len(added), preplanFirstGap)
		}
		if closed != "" {
			summary += fmt.Sprintf(" Added a %s line at the end of the file to close the fence that was still open, so the added headings are not code.", closed)
		}
	}
	if warning != "" {
		next += " preplan_check writes the warning into the Guardrail check line at Step 4."
	}
	return PlanSupportOut{
		Summary:        summary,
		Next:           next,
		Guardrails:     guardrails,
		PreplanFile:    file,
		PreplanCreated: &created,
		AddedSections:  &added,
	}, nil
}

// preplanFirstGap ends the Next of a preplan_context call on an existing
// topic file. It names the gap order of the preplan skill (Step 3).
const preplanFirstGap = "continue with its first gap: the first empty section, then the open questions, if any."

// repairPreplanHeadings reads the existing topic file, adds each absent section
// heading (addPreplanHeadings) and writes the result as a temp file that it
// renames over the topic file. It returns the added headings, which is an
// empty non-nil list when the file has every heading; then it writes nothing,
// so a second call on a repaired file writes nothing. closed is the fence run
// of the closing line that it added at the end of the file, or "".
//
// A read error comes from readPreplanTopic. A repaired text over
// dashboardDeleteReadMax is a DataError and nothing is written: the next read
// would refuse the file. A write error is an InfraError, and the file stays as
// it was. dir is the topic folder, named in the write Suggestion.
func repairPreplanHeadings(action, file, dir string) (added []string, closed string, err error) {
	data, err := readPreplanTopic(action, file)
	if err != nil {
		return nil, "", err
	}
	repaired, added, closed := addPreplanHeadings(data)
	if len(added) == 0 {
		return []string{}, "", nil
	}
	if len(repaired) > dashboardDeleteReadMax {
		return nil, "", &mcpserver.DataError{
			Msg: fmt.Sprintf("%s: adding %d section heading(s) makes %s %d bytes, more than the %d bytes (%s) that preplan_context, preplan_check and preplan_write read; nothing was written",
				action, len(added), file, len(repaired), dashboardDeleteReadMax, dashboardDeleteReadMaxText),
			Suggestion: "Make the topic file smaller than " + dashboardDeleteReadMaxText + " by hand, with room for the absent headings" + preplanRetry(action),
		}
	}
	if err := preplanWriteBytes(file, repaired); err != nil {
		return nil, "", preplanWriteFailure(action, dir, err)
	}
	return added, closed, nil
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
// uses O_EXCL, so an existing path stays unchanged and the call reports false
// with no error, whatever the path holds: readPreplanTopic, which the caller
// runs next, refuses a link, a folder, a pipe or another file that is not
// regular with the same DomainError as preplan_check. A dangling symlink at
// file or at any folder on the path to file returns a *fsx.DanglingLinkError
// and writes nothing. A write that fails after the create removes the partial
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
			// Something exists at file. readPreplanTopic checks its kind.
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

// ---------------------------------------------------------------------------
// Action: preplan_check
// ---------------------------------------------------------------------------

// Seams for the reads and writes of a topic file. Tests replace them to force
// an Lstat, read or write failure, or a file mode, without a broken
// filesystem.
var (
	preplanLstat      = os.Lstat
	preplanReadFile   = os.ReadFile
	preplanWriteBytes = writePreplanKeepPerm
)

// writePreplanKeepPerm writes data over the existing topic file at path
// through a temp file and a rename (fsx.AtomicWriteBytesPerm). The new file
// keeps the permission bits of the file it replaces, so a rewrite does not
// change the mode that createPreplanFile or the user gave it. A failed Stat
// writes nothing.
func writePreplanKeepPerm(path string, data []byte) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	return fsx.AtomicWriteBytesPerm(path, data, info.Mode().Perm())
}

// preplanCountMax is the largest value that preplan_check accepts for each of
// passCount, keptCount and droppedCount. The sum of the three stays far from
// an int overflow.
const preplanCountMax = 10000

// preplanGuardrailCheckPrefix is the text that starts the Guardrail check line
// of a topic file.
const preplanGuardrailCheckPrefix = "**Guardrail check:**"

// preplanGuardrailCheckRe matches the Guardrail check line of a topic file,
// capturing the text after the prefix.
var preplanGuardrailCheckRe = regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(preplanGuardrailCheckPrefix) + `[ \t]*(.*)$`)

// preplanCheckAgain ends each preplan_check Suggestion that a second call
// can fix.
const preplanCheckAgain = ", then call preplan_check again."

// preplanWriteAgain ends each preplan_write Suggestion that a second call
// can fix.
const preplanWriteAgain = ", then call preplan_write again."

// preplanNoFileSuggestion is the Suggestion of a preplan_check or
// preplan_write call that finds no topic file.
const preplanNoFileSuggestion = "Run plan_support with action preplan_context and this topic first."

// The names of the preplan actions. planSupportCore dispatches on them, each
// error message starts with one, and preplanRetry and
// preplanNoFileSuggestionFor compare with preplanCheckAction and
// preplanWriteAction.
const (
	preplanContextAction = "preplan_context"
	preplanCheckAction   = "preplan_check"
	preplanWriteAction   = "preplan_write"
)

// preplanRunAgain ends each preplan_context Suggestion that a new run can fix.
const preplanRunAgain = ", then run /sdlc:preplan again."

// preplanGoneSuggestion is the Suggestion of a call, other than preplan_check,
// whose topic file went away after the folder listing.
const preplanGoneSuggestion = "Run /sdlc:preplan again: the topic file went away after the folder listing, and the next run creates a new one."

// preplanRetry returns the end of a Suggestion that a second call can fix:
// preplanCheckAgain for preplan_check, preplanWriteAgain for preplan_write,
// preplanRunAgain for every other action.
func preplanRetry(action string) string {
	switch action {
	case preplanCheckAction:
		return preplanCheckAgain
	case preplanWriteAction:
		return preplanWriteAgain
	}
	return preplanRunAgain
}

// preplanNoFileSuggestionFor returns preplanNoFileSuggestion for preplan_check
// and preplan_write, and preplanGoneSuggestion for every other action.
func preplanNoFileSuggestionFor(action string) string {
	if action == preplanCheckAction || action == preplanWriteAction {
		return preplanNoFileSuggestion
	}
	return preplanGoneSuggestion
}

// preplanFoundTopic is the existing topic file that preplanReadTopicFile found
// and read: its folder, its name, its absolute path and its bytes.
type preplanFoundTopic struct {
	dir, name, file string
	data            []byte
}

// preplanReadTopicFile is the find and read step of preplan_check and
// preplan_write. It trims topic, makes its slug (preplanSlug, DomainError),
// finds exactly one topic file under mainRoot (preplanMatches; no file:
// preplanNoTopicFile), and reads it (readPreplanTopic). Each message starts
// with action + ":".
func preplanReadTopicFile(action, mainRoot, topic string) (preplanFoundTopic, error) {
	topic = strings.TrimSpace(topic)
	slug, err := preplanSlug(action, topic)
	if err != nil {
		return preplanFoundTopic{}, err
	}
	dir := filepath.Join(mainRoot, paths.DataDir, paths.PreplanSubdir)
	names, err := preplanMatches(action, dir, preplanTopic{name: topic, slug: slug})
	if err != nil {
		return preplanFoundTopic{}, err
	}
	if len(names) == 0 {
		return preplanFoundTopic{}, preplanNoTopicFile(action, topic, dir)
	}
	file := filepath.Join(dir, names[0])
	data, err := readPreplanTopic(action, file)
	if err != nil {
		return preplanFoundTopic{}, err
	}
	return preplanFoundTopic{dir: dir, name: names[0], file: file, data: data}, nil
}

// preplanNoTopicFile returns the error of a topic with no file in dir. A
// dangling link at dir or at a folder above it gives an InfraError that names
// the link and its missing target, with the Suggestion of preplan_context for
// a folder link, and the *fsx.DanglingLinkError as its Cause. Any other case
// gives a DomainError with preplanNoFileSuggestion.
func preplanNoTopicFile(action, topic, dir string) error {
	if link, target, ok := fsx.FindDanglingLink(dir); ok {
		dl := &fsx.DanglingLinkError{Link: link, Target: target}
		return &mcpserver.InfraError{
			Msg:        fmt.Sprintf("%s: list %s: %s", action, dir, dl.Problem()),
			Suggestion: dl.Recovery(),
			Cause:      dl,
		}
	}
	return &mcpserver.DomainError{
		Msg:        fmt.Sprintf("%s: no topic file for topic %q in %s", action, topic, dir),
		Suggestion: preplanNoFileSuggestion,
	}
}

// preplanWriteFailure returns the InfraError of a failed write of the topic
// file in dir. The os error names the operation and the path, so the message
// adds only the action. The Suggestion fits the error: fs.ErrNotExist (the
// file went away after the read) gives the Suggestion of a missing topic
// file (preplanNoFileSuggestionFor), fs.ErrPermission points at the write
// permission on dir, and any other error also names the free disk space.
func preplanWriteFailure(action, dir string, err error) error {
	suggestion := "Check write permission on " + dir + " and free disk space" + preplanRetry(action)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		suggestion = preplanNoFileSuggestionFor(action)
	case errors.Is(err, fs.ErrPermission):
		suggestion = "Give your user write permission on " + dir + preplanRetry(action)
	}
	return &mcpserver.InfraError{
		Msg:        fmt.Sprintf("%s: %v", action, err),
		Suggestion: suggestion,
		Cause:      err,
	}
}

// planPreplanCheck writes the guardrail check line of the existing topic
// file for in.Topic. The steps run in this order, and no step before the
// last one writes. Every error message starts with "preplan_check:".
//
//  1. preplanCheckCounts: each count is set, and is 0 to preplanCountMax
//     (DomainError).
//  2. preplanReadTopicFile: the trimmed topic gives a slug (preplanSlug,
//     DomainError), preplanMatches finds exactly one topic file under
//     mainRoot (no file: preplanNoTopicFile; list errors, a full folder, two
//     files: see preplanMatches), and readPreplanTopic checks the file and
//     reads it.
//  3. loadGuardrails counts the guardrails, and setGuardrailCheckLine puts the
//     check line in the head (no place for the line: DataError).
//  4. preplanWriteBytes writes a temp file and renames it over the topic file
//     (InfraError; the old file stays as it was).
//
// The result lists the empty sections of the bytes that step 4 wrote
// (preplanEmptySections).
func planPreplanCheck(mainRoot string, in PlanSupportIn) (PlanSupportOut, error) {
	const action = preplanCheckAction
	pass, kept, dropped, err := preplanCheckCounts(action, in)
	if err != nil {
		return PlanSupportOut{}, err
	}
	topic, err := preplanReadTopicFile(action, mainRoot, in.Topic)
	if err != nil {
		return PlanSupportOut{}, err
	}

	guardrails, warning := loadGuardrails(mainRoot)
	line := preplanCheckLine(len(guardrails), pass, kept, dropped, warning)
	next, prior, ok := setGuardrailCheckLine(topic.data, line)
	if !ok {
		return PlanSupportOut{}, &mcpserver.DataError{
			Msg:        fmt.Sprintf("%s: %s has no **Started:** line and no **Status:** line in its first %d bytes", action, topic.file, dashboardPreplanHeadMax),
			Suggestion: "Add a **Status:** line near the top of the topic file by hand" + preplanCheckAgain,
		}
	}
	if err := preplanWriteBytes(topic.file, next); err != nil {
		return PlanSupportOut{}, preplanWriteFailure(action, topic.dir, err)
	}
	empty, openFence := preplanEmptySections(next)
	return PlanSupportOut{
		Summary:            preplanCheckSummary(topic.name, prior, warning, next, empty, openFence),
		Next:               preplanCheckNext(empty, openFence),
		PreplanFile:        topic.file,
		GuardrailCheckLine: line,
		EmptySections:      &empty,
	}, nil
}

// preplanCheckNext returns the Next of a successful preplan_check call. With
// 1 or more empty sections it adds the count and points at the Step 4 route
// table of the skill. With a fence still open at the end of the file
// (openFence), it says to close the fence first with tool calls only
// (preplan_context, else preplan_write of the last section), as the Step 4
// open-fence repair of the preplan skill does: the headings after it count
// as absent.
func preplanCheckNext(empty []string, openFence string) string {
	next := "Read preplanFile again before your next edit: preplan_check rewrote it. Then follow the Step 4 route table."
	if len(empty) > 0 {
		next += fmt.Sprintf(" emptySections lists %d empty section(s). The Step 4 route table of the skill picks the next step.", len(empty))
	}
	if openFence != "" {
		next += fmt.Sprintf(" First close the open %s fence with tool calls, not a file edit: call plan_support preplan_context with the same topic; when its summary names no closing fence line, send the full body of the last section with preplan_write, with the fence closed. Each ## heading after the fence counts as absent until then.", openFence)
	}
	return next
}

// preplanCheckCounts returns the three counts of a preplan_check call. A
// count that is absent, negative or more than preplanCountMax returns a
// DomainError whose message starts with action + ":".
func preplanCheckCounts(action string, in PlanSupportIn) (pass, kept, dropped int, err error) {
	suggestion := fmt.Sprintf("Pass passCount, keptCount and droppedCount as whole numbers from 0 to %d, counted from the ## Decisions table.", preplanCountMax)
	for _, c := range []struct {
		name  string
		value *int
	}{
		{"passCount", in.PassCount},
		{"keptCount", in.KeptCount},
		{"droppedCount", in.DroppedCount},
	} {
		if c.value == nil {
			return 0, 0, 0, &mcpserver.DomainError{
				Msg:        fmt.Sprintf("%s: %s is required", action, c.name),
				Suggestion: suggestion,
			}
		}
		if *c.value < 0 || *c.value > preplanCountMax {
			return 0, 0, 0, &mcpserver.DomainError{
				Msg:        fmt.Sprintf("%s: %s is %d, must be 0 to %d", action, c.name, *c.value, preplanCountMax),
				Suggestion: suggestion,
			}
		}
	}
	return *in.PassCount, *in.KeptCount, *in.DroppedCount, nil
}

// readPreplanTopic checks that file is a regular file of at most
// dashboardDeleteReadMax bytes, then reads it. Each message starts with
// action + ":". The retry text of each Suggestion comes from action
// (preplanRetry).
//
//   - A file that is gone at the Lstat or the read: DomainError with
//     preplanNoFileSuggestionFor(action).
//   - A symlink, a folder or another file that is not regular: DomainError
//     (preplanNotRegularError).
//   - A file over the size limit: DataError.
//   - Any other Lstat or read failure: InfraError (preplanIOError).
func readPreplanTopic(action, file string) ([]byte, error) {
	info, err := preplanLstat(file)
	if err != nil {
		return nil, preplanIOError(action, file, false, err)
	}
	if !info.Mode().IsRegular() {
		return nil, preplanNotRegularError(action, file, info.Mode())
	}
	if info.Size() > dashboardDeleteReadMax {
		return nil, &mcpserver.DataError{
			Msg: fmt.Sprintf("%s: %s is %d bytes, more than the %d bytes (%s) that %s reads",
				action, file, info.Size(), dashboardDeleteReadMax, dashboardDeleteReadMaxText, action),
			Suggestion: "Make the topic file smaller than " + dashboardDeleteReadMaxText + " by hand" + preplanRetry(action),
		}
	}
	data, err := preplanReadFile(file)
	if err != nil {
		return nil, preplanIOError(action, file, true, err)
	}
	return data, nil
}

// preplanIOError returns the error of a failed Lstat (read false) or read
// (read true) of the topic file. The os error names the operation and the
// path, so the message adds only the action. fs.ErrNotExist gives a
// DomainError: the file went away after the folder listing. Any other error
// gives an InfraError whose Suggestion fits the error: fs.ErrPermission
// points at the permission that the operation needs. The Suggestion text
// comes from action (preplanNoFileSuggestionFor, preplanRetry).
func preplanIOError(action, file string, read bool, err error) error {
	msg := fmt.Sprintf("%s: %v", action, err)
	if errors.Is(err, fs.ErrNotExist) {
		return &mcpserver.DomainError{Msg: msg, Suggestion: preplanNoFileSuggestionFor(action), Cause: err}
	}
	suggestion := "Fix the cause that the message names" + preplanRetry(action)
	if errors.Is(err, fs.ErrPermission) {
		if read {
			suggestion = "Give your user read permission on " + file + preplanRetry(action)
		} else {
			suggestion = "Give your user search permission on " + filepath.Dir(file) + preplanRetry(action)
		}
	}
	return &mcpserver.InfraError{Msg: msg, Suggestion: suggestion, Cause: err}
}

// preplanNotRegularError returns the DomainError for a topic file path whose
// mode is not regular. The Suggestion fits the kind: a link to a file that
// exists can become a copy of that file; a dangling link, a folder or another
// file goes away, and the next /sdlc:preplan run creates a new topic file. A
// link whose target cannot be read (a link loop, a target with no access)
// goes away too; its message names the Stat error, which is the Cause. The
// retry text of the live-link Suggestion comes from action (preplanRetry).
func preplanNotRegularError(action, file string, mode fs.FileMode) error {
	const rerun = preplanRunAgain + " preplan_context then creates a new topic file."
	kind := preplanFileKind(mode)
	if mode&fs.ModeSymlink == 0 {
		msg := fmt.Sprintf("%s: %s is %s", action, file, kind)
		if mode.IsDir() {
			msg += ", not a regular file"
		}
		return &mcpserver.DomainError{Msg: msg, Suggestion: "Remove or rename " + file + rerun}
	}
	_, err := os.Stat(file)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		msg := fmt.Sprintf("%s: %s is a symlink to a missing file", action, file)
		if last, target, ok := fsx.FindDanglingLink(file); ok {
			dl := &fsx.DanglingLinkError{Link: file, Target: target, Remove: true, Chain: last != file}
			msg = action + ": " + dl.Problem()
		}
		return &mcpserver.DomainError{Msg: msg, Suggestion: "Remove the link with rm " + file + rerun}
	case err != nil:
		return &mcpserver.DomainError{
			Msg:        fmt.Sprintf("%s: %s is a symlink whose target cannot be read: %v", action, file, err),
			Suggestion: "Remove the link with rm " + file + rerun,
			Cause:      err,
		}
	}
	return &mcpserver.DomainError{
		Msg:        fmt.Sprintf("%s: %s is %s, not a regular file", action, file, kind),
		Suggestion: "Replace the link " + file + " with a copy of the file it points to" + preplanRetry(action),
	}
}

// preplanCheckLine returns the Guardrail check line, with no line end, for
// the given guardrail count and proposal counts. A guardrail warning adds
// " Guardrails unavailable: <warning>." at the end.
func preplanCheckLine(guardrails, pass, kept, dropped int, warning string) string {
	line := fmt.Sprintf("%s %d guardrails, %d proposals: %d pass, %d kept, %d dropped. %s%s.",
		preplanGuardrailCheckPrefix, guardrails, pass+kept+dropped, pass, kept, dropped,
		preplanLastCheckLabel, preplanNow().UTC().Format(time.RFC3339))
	if warning != "" {
		line += " Guardrails unavailable: " + preplanWarningText(warning) + "."
	}
	return line
}

// preplanCheckSummary returns the Summary of a successful preplan_check call.
// It says whether the line was added or replaced, and echoes the replaced
// line. It names the guardrail warning, if any, the number of other
// Guardrail check lines in next that stay in the file, the empty sections
// (none when the list is empty), and last the fence that is still open at the
// end of the file (openFence), if any.
func preplanCheckSummary(name, prior, warning string, next []byte, empty []string, openFence string) string {
	summary := fmt.Sprintf("Added the Guardrail check line to %s.", name)
	if prior != "" {
		summary = fmt.Sprintf("Replaced the Guardrail check line of %s. Old line: %s", name, prior)
	}
	if warning != "" {
		summary += " Guardrails unavailable: " + preplanWarningText(warning) + "."
	}
	if stale := len(preplanGuardrailCheckRe.FindAllIndex(next, -1)) - 1; stale > 0 {
		summary += fmt.Sprintf(" %d more Guardrail check line(s) stay in the file. No preplan action removes them: tell the user to delete them by hand. Do not edit the file.", stale)
	}
	if len(empty) == 0 {
		summary += " Empty sections: none."
	} else {
		summary += " Empty sections: " + strings.Join(empty, ", ") + "."
	}
	if openFence != "" {
		summary += fmt.Sprintf(" A %s fence is still open at the end of the file, so each ## heading after it counts as absent.", openFence)
	}
	return summary
}

// preplanWarningText makes the guardrail warning fit on the check line: one
// line of text (each run of white space becomes one space) with its own final
// period removed, so the caller can add one period.
func preplanWarningText(warning string) string {
	return strings.TrimSuffix(strings.Join(strings.Fields(warning), " "), ".")
}

// setGuardrailCheckLine puts line, the full Guardrail check line with no line
// end, in data. Each anchor is found only in the first dashboardPreplanHeadMax
// bytes. When the head has a Guardrail check line, replacePreplanHeadLine
// replaces the first one, and prior is that old line with no line end. Else
// the line goes after the Started line, else after the Status line, with one
// blank line before it and the anchor's line end after it, and prior is
// empty. ok is false when the head has none of the three lines. Every other
// byte stays the same, and next is a new slice: data does not change.
func setGuardrailCheckLine(data []byte, line string) (next []byte, prior string, ok bool) {
	if next, prior, found := replacePreplanHeadLine(data, preplanGuardrailCheckRe, line); found {
		return next, prior, true
	}

	head := preplanHead(data)
	loc := dashboardPreplanStartedRe.FindIndex(head)
	if loc == nil {
		loc = dashboardPreplanStatusRe.FindIndex(head)
	}
	if loc == nil {
		return nil, "", false
	}
	var insert string
	at := len(data)
	if nl := preplanLineEnd(data, loc[0]); nl < len(data) {
		eol := preplanEOL(data, nl)
		at = nl + 1
		insert = eol + line + eol
	} else {
		// The anchor is the last line and has no line end.
		insert = "\n\n" + line + "\n"
	}
	next = make([]byte, 0, len(data)+len(insert))
	next = append(next, data[:at]...)
	next = append(next, insert...)
	next = append(next, data[at:]...)
	return next, "", true
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
