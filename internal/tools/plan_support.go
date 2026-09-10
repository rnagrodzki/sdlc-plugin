package tools

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/worktree"
)

// ---------------------------------------------------------------------------
// Input / Output types
// ---------------------------------------------------------------------------

// PlanSupportIn carries the merged input for the plan_support tool's 4
// actions. Each field is consumed by one or more actions (noted in comments).
type PlanSupportIn struct {
	Action string `json:"action" jsonschema_description:"Selects the operation: \"merge_results\", \"material_snapshot\", \"material_compare\", or \"openspec_appendix\". Each action reads only the subset of fields listed in the tool description; unlisted fields are ignored."` // "merge_results"|"material_snapshot"|"material_compare"|"openspec_appendix"

	// merge_results
	LaneResults   []LaneResult `json:"laneResults,omitempty" jsonschema_description:"merge_results only: outcomes from each review lane to merge. At least one of laneResults or lensResults is required."`
	LensResults   []LensResult `json:"lensResults,omitempty" jsonschema_description:"merge_results only: outcomes from each review lens to merge. At least one of laneResults or lensResults is required."`
	ExpectedGates []string     `json:"expectedGates,omitempty" jsonschema_description:"merge_results only: gate IDs expected to be covered by the merged lane/lens results, used to compute coverage gaps."`
	IsRedispatch  bool         `json:"isRedispatch,omitempty" jsonschema_description:"merge_results only: true when these results come from a redispatch (re-run) of lanes/lenses, affecting how merged status is computed."`

	// material_snapshot / material_compare
	FilePath string        `json:"filePath,omitempty" jsonschema_description:"material_snapshot / material_compare only: path to the plan file to snapshot or compare."`
	Snapshot *PlanSnapshot `json:"snapshot,omitempty" jsonschema_description:"material_compare only: the previously captured snapshot to compare the current plan file against."`

	// openspec_appendix
	ChangeName   string   `json:"changeName,omitempty" jsonschema_description:"openspec_appendix only: name of the openspec change to generate the appendix for. Required."`
	ProposalPath string   `json:"proposalPath,omitempty" jsonschema_description:"openspec_appendix only: path to the change's proposal.md, included in the generated appendix."`
	DesignPath   string   `json:"designPath,omitempty" jsonschema_description:"openspec_appendix only: path to the change's design.md, included in the generated appendix."`
	SpecPaths    []string `json:"specPaths,omitempty" jsonschema_description:"openspec_appendix only: paths to the change's spec files, included in the generated appendix."`
	PlanTasks    []string `json:"planTasks,omitempty" jsonschema_description:"openspec_appendix only: plan task identifiers to cross-reference in the generated appendix."`
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
	Recommendations []string `json:"recommendations,omitempty"`

	// material_snapshot
	Snapshot *PlanSnapshot `json:"snapshot,omitempty"`

	// material_compare
	Material bool     `json:"material,omitempty"`
	Triggers []string `json:"triggers,omitempty"`

	// openspec_appendix
	AppendixMarkdown string `json:"appendixMarkdown,omitempty"`
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
	Status          string   `json:"status" jsonschema_description:"Outcome of the lens: \"approved\", \"rejected\", or \"conditional\"."` // "approved"|"rejected"|"conditional"
	Issues          []Issue  `json:"issues,omitempty" jsonschema_description:"Findings raised by this lens."`
	Recommendations []string `json:"recommendations,omitempty" jsonschema_description:"Recommendations raised by this lens."`
}

// Issue represents a single finding from a lane or lens.
type Issue struct {
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

- merge_results: Merge lane/lens review results. Requires at least one of laneResults or lensResults. Optional: expectedGates, isRedispatch.
- material_snapshot: Snapshot plan material for change detection. Requires filePath.
- material_compare: Compare current plan material against a snapshot. Requires filePath, snapshot.
- openspec_appendix: Generate an openspec appendix. Requires changeName. Optional: proposalPath, designPath, specPaths, planTasks.`,
		func(ctx mcpserver.Ctx, in PlanSupportIn) (PlanSupportOut, error) {
			root, err := worktree.MainRoot()
			if err != nil {
				cwd, cwdErr := os.Getwd()
				if cwdErr != nil {
					return PlanSupportOut{}, &mcpserver.InfraError{Msg: "resolve root: " + err.Error(), Cause: err}
				}
				root = cwd
			}
			contentRoot, err := worktree.ActiveRoot()
			if err != nil {
				contentRoot = root
			}
			return planSupportCore(root, contentRoot, in)
		},
	)
}

// ---------------------------------------------------------------------------
// Core dispatcher
// ---------------------------------------------------------------------------

// planSupportCore dispatches to the correct action handler. mainRoot anchors
// filesystem lookups; contentRoot anchors worktree-relative operations.
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
	default:
		return PlanSupportOut{}, &mcpserver.DomainError{
			Msg: fmt.Sprintf("unknown action %q — valid actions: merge_results, material_snapshot, material_compare, openspec_appendix", in.Action),
		}
	}
}

// ---------------------------------------------------------------------------
// Action: merge_results
// ---------------------------------------------------------------------------

// issueKey returns a dedup key for an Issue (GateID + normalized summary).
func issueKey(iss Issue) string {
	return iss.GateID + "|" + strings.ToLower(strings.TrimSpace(iss.Summary))
}

func mergeResults(in PlanSupportIn) (PlanSupportOut, error) {
	if len(in.LaneResults) == 0 && len(in.LensResults) == 0 {
		return PlanSupportOut{}, &mcpserver.DomainError{
			Msg: "merge_results requires at least one of laneResults or lensResults to be non-empty",
		}
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
	var filteredLaneFailures []string
	for _, lane := range in.LaneResults {
		if lane.Status != "fail" {
			continue
		}
		isG17Only := true
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

	// Compute merged status.
	mergedStatus := "Approved"

	// Any blocking issue means the merge is not clean.
	for _, iss := range allIssues {
		if iss.Severity == "blocking" {
			mergedStatus = "Issues Found"
			break
		}
	}

	// For lenses-only: Approved iff all lens statuses are "approved" (SKILL.md:690).
	if len(in.LaneResults) == 0 && len(in.LensResults) > 0 {
		for _, lens := range in.LensResults {
			if lens.Status != "approved" {
				mergedStatus = "Issues Found"
				break
			}
		}
	}

	// For lanes-only or mixed: any lane failure that isn't G17-only means issues found.
	if len(in.LaneResults) > 0 {
		for _, lane := range in.LaneResults {
			if lane.Status == "fail" {
				// Check if lane has non-advisory issues.
				for _, iss := range lane.Issues {
					if iss.Severity == "blocking" {
						mergedStatus = "Issues Found"
						break
					}
				}
			}
		}
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
	} else if mergedStatus == "Issues Found" {
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
		Recommendations: recommendations,
	}, nil
}

// ---------------------------------------------------------------------------
// Action: material_snapshot
// ---------------------------------------------------------------------------

func materialSnapshot(in PlanSupportIn) (PlanSupportOut, error) {
	if in.FilePath == "" {
		return PlanSupportOut{}, &mcpserver.DomainError{
			Msg: "material_snapshot requires filePath — provide the path to the plan file to snapshot",
		}
	}

	content, err := os.ReadFile(in.FilePath)
	if err != nil {
		return PlanSupportOut{}, &mcpserver.InfraError{
			Msg:   fmt.Sprintf("read plan file %q: %s", in.FilePath, err.Error()),
			Cause: err,
		}
	}

	snap := snapshotPlan(string(content))

	return PlanSupportOut{
		Summary:  fmt.Sprintf("Snapshot captured: %d tasks, %d deviation rows, %d key decisions.", snap.TaskCount, len(snap.DeviationsRows), len(snap.KeyDecisions)),
		Next:     "Use material_compare after plan modifications to detect material changes.",
		Snapshot: &snap,
	}, nil
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

		// Contract: verbatim text after **Contract:** marker.
		contractBlock, contractFound := extractDelimitedBlock(t.Body, psContractBlockStartRe, []string{"\n### ", "\n---", "\n## "})
		if contractFound {
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
			Msg: "material_compare requires filePath — provide the path to the updated plan file",
		}
	}
	if in.Snapshot == nil {
		return PlanSupportOut{}, &mcpserver.DomainError{
			Msg: "material_compare requires snapshot — provide the prior snapshot from material_snapshot",
		}
	}

	content, err := os.ReadFile(in.FilePath)
	if err != nil {
		return PlanSupportOut{}, &mcpserver.InfraError{
			Msg:   fmt.Sprintf("read plan file %q: %s", in.FilePath, err.Error()),
			Cause: err,
		}
	}

	after := snapshotPlan(string(content))
	before := in.Snapshot
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
			Msg: "openspec_appendix requires changeName — provide the openspec change name",
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
