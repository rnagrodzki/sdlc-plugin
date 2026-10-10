package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/attention"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
	"github.com/rnagrodzki/sdlc-plugin/internal/state"
)

// Pipeline status values of a dashboard pipeline. Other dashboard code uses
// these constants, never new literals.
const (
	PipelineRunning   = "running"
	PipelineStalled   = "stalled"
	PipelineCompleted = "completed"
	PipelineFailed    = "failed"
)

// Step status values of a dashboard step: the same set as steps[].status in
// plugins/sdlc/schemas/ship-state.schema.json. Second copy:
// internal/dashboard/web/static/view.js (stepGlyph). Tests check both
// against the schema.
const (
	StepPending    = "pending"
	StepInProgress = "in_progress"
	StepCompleted  = "completed"
	StepSkipped    = "skipped"
	StepFailed     = "failed"
)

const (
	// dashboardStallAfter is the age of updatedAt after which a running
	// pipeline shows as stalled.
	dashboardStallAfter = 30 * time.Minute
	// dashboardHistoryWindow is how long a completed or failed pipeline
	// stays on the dashboard after its updatedAt.
	dashboardHistoryWindow = 24 * time.Hour
	// dashboardReviewPrefix starts the folder name of a review run under
	// runs/ledger/.
	dashboardReviewPrefix = "review-"
	// dashboardFilenameTimeLayout is the timestamp layout of a state file
	// name (<prefix>-<slug>-<ts>.json).
	dashboardFilenameTimeLayout = "20060102T150405Z"
	// dashboardReviewTimeLayout is the timestamp layout of a review run
	// folder name (review-<ts>).
	dashboardReviewTimeLayout = "2006-01-02T15-04-05Z"
)

// dashboardActivity is the per-repo activity collector that
// CollectDashboardSnapshot calls. It is a variable so tests can count calls.
var dashboardActivity = collectActivity

// DashboardSnapshot is the JSON body of GET /api/snapshot.
type DashboardSnapshot struct {
	GeneratedAt string          `json:"generatedAt"`
	Version     string          `json:"version"`
	Repos       []DashboardRepo `json:"repos"`
}

// DashboardRepo is one registered repo of a snapshot. Error is set when the
// repo could not be read; every list is then empty.
type DashboardRepo struct {
	Root      string              `json:"root"`
	Name      string              `json:"name"`
	Error     string              `json:"error"`
	Pipelines []DashboardPipeline `json:"pipelines"`
	Sessions  []DashboardSession  `json:"sessions"`
	Learnings []DashboardLearning `json:"learnings"`
	Deferred  []DashboardDeferred `json:"deferred"`
	History   []DashboardRun      `json:"history"` // [] until filled, never null
}

// DashboardPipeline is one ship, execute, plan, or review run.
type DashboardPipeline struct {
	ID          string              `json:"id"`
	Kind        string              `json:"kind"`
	Branch      string              `json:"branch"`
	Worktree    string              `json:"worktree"`
	Status      string              `json:"status"`
	StartedAt   string              `json:"startedAt"`
	UpdatedAt   string              `json:"updatedAt"`
	CompletedAt *string             `json:"completedAt"`
	Progress    DashboardProgress   `json:"progress"`
	Steps       []DashboardStep     `json:"steps"`
	Issues      []DashboardIssue    `json:"issues"`
	SessionID   string              `json:"sessionId"`             // "" when unknown
	CommitWaves *bool               `json:"commitWaves,omitempty"` // execute and ship only; absent elsewhere
	Attention   *DashboardAttention `json:"attention,omitempty"`   // absent when no wait is open
	join        dashboardJoinInfo   // not serialized; read by dashboardJoinRuns
}

// DashboardAttention is the open wait of a running pipeline: the run waits
// for a person to answer a question or grant a permission.
type DashboardAttention struct {
	Kind    string `json:"kind"`    // "question" | "permission"
	AskedAt string `json:"askedAt"` // RFC3339 UTC
	Header  string `json:"header"`  // question header, or "Permission"
	Text    string `json:"text"`    // max 120 runes, redacted
}

// DashboardProgress is the progress summary of a pipeline.
type DashboardProgress struct {
	Done    int    `json:"done"`
	Total   int    `json:"total"`
	Current string `json:"current"`
	Label   string `json:"label"`
}

// DashboardStep is one step of a pipeline. Status is one of the Step*
// constants.
type DashboardStep struct {
	Name        string               `json:"name"`
	Status      string               `json:"status"`
	StartedAt   string               `json:"startedAt,omitempty"`   // RFC 3339, copied when it parses
	CompletedAt string               `json:"completedAt,omitempty"` // absent while the step runs
	Detail      *DashboardStepDetail `json:"detail,omitempty"`
}

// DashboardIssue is one problem of a pipeline. Source is "step", "wave",
// "review", "state", "task", or "pipeline".
type DashboardIssue struct {
	Source   string `json:"source"`
	Severity string `json:"severity"`
	Text     string `json:"text"`
	File     string `json:"file"`
	Line     string `json:"line"` // "" | "42" | "12-14"
	Ref      string `json:"ref"`  // step, wave, dimension, or task of the issue
}

// Kind values of a DashboardStepDetail: a closed set. Every detail literal
// sets Kind from one of these constants, never from a new literal.
const (
	dashboardKindWaves      = "waves"
	dashboardKindDimensions = "dimensions"
	dashboardKindExplorers  = "explorers"
	dashboardKindRounds     = "rounds"
	dashboardKindFindings   = "findings"
	dashboardKindGuardrails = "guardrails" // plan setup station
	dashboardKindResult     = "result"     // one result line of a ship step
	dashboardKindFixes      = "fixes"      // received-review station of a ship run
)

// DashboardStepDetail is what a pipeline did inside one step. Kind tells
// which list is filled. Every slice field is [] when empty, never nil; the
// omitempty fields are absent, not null.
type DashboardStepDetail struct {
	Kind         string                    `json:"kind"` // a dashboardKind* value; never ""
	Waves        []DashboardWave           `json:"waves,omitempty"`
	Queued       []DashboardTask           `json:"queued,omitempty"` // planned, wave not started
	Dimensions   []DashboardDimension      `json:"dimensions,omitempty"`
	ReviewTotals *DashboardReviewTotals    `json:"reviewTotals,omitempty"`
	Explorers    []DashboardExplorer       `json:"explorers,omitempty"`
	Rounds       []DashboardRound          `json:"rounds,omitempty"`
	MaxRounds    int                       `json:"maxRounds,omitempty"`
	Findings     []DashboardReviewFinding  `json:"findings,omitempty"`    // kind findings: one dimension of a standalone review
	Guardrails   *DashboardGuardrailCounts `json:"guardrails,omitempty"`  // kind guardrails
	Result       string                    `json:"result,omitempty"`      // kind result
	RoundTotals  *DashboardRoundTotals     `json:"roundTotals,omitempty"` // kind rounds
	RepairLimit  bool                      `json:"repairLimit,omitempty"` // kind rounds
	Outcomes     []DashboardFindingOutcome `json:"outcomes,omitempty"`    // kind rounds
	ReviewPlan   *DashboardReviewPlan      `json:"reviewPlan,omitempty"`  // kind dimensions
	Fixes        []DashboardFix            `json:"fixes,omitempty"`       // kind fixes
}

// DashboardFix is one finding that received-review took. Line 0 means no
// line.
type DashboardFix struct {
	Title    string `json:"title"` // dashboardPreview: redacted, max 120 runes
	Severity string `json:"severity"`
	File     string `json:"file"`
	Line     int    `json:"line,omitempty"`
	Status   string `json:"status"` // one of healingFixStatuses
}

// DashboardGuardrailCounts is the guardrail count of a plan setup station.
type DashboardGuardrailCounts struct {
	Total   int `json:"total"`
	Error   int `json:"error"`
	Warning int `json:"warning"`
}

// DashboardRoundTotals is the total of the plan review rounds.
type DashboardRoundTotals struct {
	Iterations int  `json:"iterations"` // rounds recorded
	Violations int  `json:"violations"`
	Fixes      int  `json:"fixes"`
	Distinct   bool `json:"distinct"` // true: counted by finding ID; false: sum of per-round counts
}

// DashboardFindingOutcome is the choice of a person on one plan review
// finding.
type DashboardFindingOutcome struct {
	ID     string `json:"id"`
	Text   string `json:"text"`
	Choice string `json:"choice"` // "accepted" | "rejected" | "stop"
	Reason string `json:"reason"`
}

// DashboardReviewPlan is the run plan of a ship review step: what the plan
// listed and how much of it ran.
type DashboardReviewPlan struct {
	WavesPlanned      int `json:"wavesPlanned"`
	WavesRun          int `json:"wavesRun"`
	DimensionsPlanned int `json:"dimensionsPlanned"`
	DimensionsRun     int `json:"dimensionsRun"`
	NeverStarted      int `json:"neverStarted"`
}

// DashboardReviewFinding is one finding of a review dimension.
type DashboardReviewFinding struct {
	Text     string `json:"text"`     // rationale
	Severity string `json:"severity"` // dashboardSeverity value
	File     string `json:"file"`
	Line     string `json:"line"`
}

// DashboardWave is one execute wave with its tasks.
type DashboardWave struct {
	Number       int             `json:"number"`
	Status       string          `json:"status"`
	CommittedSHA string          `json:"committedSha"` // "" = not committed
	Tasks        []DashboardTask `json:"tasks"`
}

// DashboardTask is one execute task.
type DashboardTask struct {
	ID     string `json:"id"`
	Name   string `json:"name"`   // "" -> page shows the id only
	Status string `json:"status"` // pending | in_progress | completed | failed
}

// DashboardDimension is one review dimension of a ship review step.
type DashboardDimension struct {
	Name         string                   `json:"name"`
	Status       string                   `json:"status"`
	Findings     int                      `json:"findings"`
	Worst        string                   `json:"worst"`            // highest severity, "" when none
	Wave         int                      `json:"wave,omitempty"`   // 1-based wave of the review plan; absent when the dimension is not planned
	Reason       string                   `json:"reason,omitempty"` // "stalled" | "missing" | "unstopped" when Status is skipped
	FindingItems []DashboardReviewFinding `json:"findingItems"`     // same rows as kind findings; [] when the dimension has no finding
}

// DashboardReviewTotals is the finding ledger of a ship review step.
type DashboardReviewTotals struct {
	Found       int `json:"found"`       // ShipReviewLedger.Total
	Fixed       int `json:"fixed"`       // ShipReviewLedger.Fixed
	Deferred    int `json:"deferred"`    // sum of ShipReviewLedger.DeferredByReason
	Unaccounted int `json:"unaccounted"` // ShipReviewLedger.Unaccounted
}

// DashboardExplorer is one plan explorer with its first findings.
type DashboardExplorer struct {
	Name     string             `json:"name"`
	Status   string             `json:"status"`
	Total    int                `json:"total"`
	Findings []DashboardFinding `json:"findings"` // first 5
}

// DashboardFinding is one plan explorer finding.
type DashboardFinding struct {
	Summary string `json:"summary"`
	Ref     string `json:"ref"`
}

// DashboardRound is one plan review round.
type DashboardRound struct {
	N      int             `json:"n"`
	Status string          `json:"status"` // planStatusApproved | planStatusIssuesFound
	Found  int             `json:"found"`
	Fixed  int             `json:"fixed"`
	Lenses []DashboardLens `json:"lenses"`
}

// DashboardLens is the verdict of one review lens in a plan review round.
type DashboardLens struct {
	Name    string `json:"name"`
	Verdict string `json:"verdict"`
}

// DashboardRun is one finished run of a repo's history.
type DashboardRun struct {
	Kind       string `json:"kind"`
	Branch     string `json:"branch"`
	Outcome    string `json:"outcome"` // success | failure | partial (history_record values) | done (plan_mark done)
	StartedAt  string `json:"startedAt"`
	EndedAt    string `json:"endedAt"`
	DurationMs int64  `json:"durationMs"`
}

// dashboardJoinInfo carries join keys between collectors. Never serialized.
type dashboardJoinInfo struct {
	execDetail  *DashboardStepDetail    // full execute detail for the ship execute step
	shipRunID   string                  // review run.meta shipRunId
	startedAt   time.Time               // run start, for the join window
	stepWindows map[string][2]time.Time // ship step name -> [startedAt, completedAt]
	reviewDims  []DashboardDimension    // review rows for the ship review step
	reviewPlan  *DashboardReviewPlan    // review plan totals; nil when run.meta plans no dimension
	members     []string                // nested execute state id and nested review ledger name (review-<ts>), filled by the join
}

// DashboardSession is one Claude Code session seen in the evidence files of
// a repo. Timeline holds only the newest dashboardSessionTimelineMax events.
// CommandGroups counts every command of the session, and is [] when the
// session has no command, never null.
type DashboardSession struct {
	ID        string `json:"id"`
	Active    bool   `json:"active"`
	Branch    string `json:"branch"`
	FirstSeen string `json:"firstSeen"`
	LastSeen  string `json:"lastSeen"`
	Counts    struct {
		Prompts  int `json:"prompts"`
		Commands int `json:"commands"`
		MCPCalls int `json:"mcpCalls"`
	} `json:"counts"`
	Timeline      []DashboardEvent        `json:"timeline"`
	CommandGroups []DashboardCommandGroup `json:"commandGroups"`
}

// DashboardEvent is one timeline entry of a session. Kind is "prompt",
// "command", or "mcp".
type DashboardEvent struct {
	At   string `json:"at"`
	Kind string `json:"kind"`
	Text string `json:"text"`
}

// DashboardCommandGroup is the share of a session's commands that run the
// same program. Label is the program, "a + b + c" for a command that runs
// three or more programs, or "(other)" for a command with no program.
// Programs lists the programs the label names, and is [] for "(other)".
// Share is Count divided by all commands of the session, rounded to 2
// decimals. Majority marks the largest group. LastAt is the time of the
// newest command of the group.
type DashboardCommandGroup struct {
	Label    string   `json:"label"`
	Programs []string `json:"programs"`
	Count    int      `json:"count"`
	Share    float64  `json:"share"`
	Majority bool     `json:"majority"`
	LastAt   string   `json:"lastAt"`
}

// DashboardLearning is one learnings entry of a repo.
type DashboardLearning struct {
	Date    string `json:"date"`
	Heading string `json:"heading"`
	RunID   string `json:"runId"`
	Branch  string `json:"branch"`
}

// DashboardDeferred is one open deferred item of a repo. Priority is
// "high", "medium", or "low". Created, Source, Severity, File, Line and
// Reason mirror history.DeferredIssue. None of them has omitempty: a value
// the record does not hold serializes as "" or 0, never null or a missing key.
type DashboardDeferred struct {
	ID          string `json:"id"`
	Priority    string `json:"priority"`
	Description string `json:"description"`
	Created     string `json:"created"`
	Source      string `json:"source"`
	Severity    string `json:"severity"`
	File        string `json:"file"`
	Line        int    `json:"line"`
	Reason      string `json:"reason"`
}

// CollectDashboardSnapshot reads the pipeline state of every repo in roots
// and returns the dashboard snapshot. Roots that name the same repo (a
// linked worktree's runs/ folder is a symlink to the main worktree's) give
// one repo entry under the main root. now decides the stalled and 24 h
// history rules; version is copied into the snapshot.
func CollectDashboardSnapshot(roots []string, now time.Time, version string) DashboardSnapshot {
	snap := DashboardSnapshot{
		GeneratedAt: now.UTC().Format(time.RFC3339),
		Version:     version,
		Repos:       []DashboardRepo{},
	}
	for _, root := range dashboardRepoRoots(roots) {
		snap.Repos = append(snap.Repos, collectDashboardRepo(root, now))
	}
	return snap
}

// dashboardRepoRoots cleans roots, drops empty entries, and keeps one root
// for each repo. A root whose .sdlc-v2/runs is a symlink is a linked
// worktree: it maps to the main root that owns the symlink target, and a
// main root spelled in roots wins over the derived path. Order of first
// appearance is kept.
func dashboardRepoRoots(roots []string) []string {
	type group struct {
		root   string
		linked bool
	}
	var keys []string
	groups := map[string]*group{}
	for _, r := range roots {
		if strings.TrimSpace(r) == "" {
			continue
		}
		root := filepath.Clean(r)
		key, linked := root, false
		runs := filepath.Join(root, paths.DataDir, paths.RunsSubdir)
		if real, err := filepath.EvalSymlinks(runs); err == nil {
			key = real
			if fi, err := os.Lstat(runs); err == nil && fi.Mode()&os.ModeSymlink != 0 {
				linked = true
			}
		}
		g, ok := groups[key]
		if !ok {
			display := root
			if linked {
				display = filepath.Dir(filepath.Dir(key))
			}
			groups[key] = &group{root: display, linked: linked}
			keys = append(keys, key)
			continue
		}
		if g.linked && !linked {
			g.root, g.linked = root, false
		}
	}
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, groups[k].root)
	}
	return out
}

// collectDashboardRepo builds the repo entry of one root.
func collectDashboardRepo(root string, now time.Time) DashboardRepo {
	repo := DashboardRepo{
		Root:      root,
		Name:      filepath.Base(root),
		Pipelines: []DashboardPipeline{},
		Sessions:  []DashboardSession{},
		Learnings: []DashboardLearning{},
		Deferred:  []DashboardDeferred{},
		History:   []DashboardRun{},
	}
	info, err := os.Stat(root)
	if err != nil {
		repo.Error = fmt.Sprintf("repo folder not readable: %v", err)
		return repo
	}
	if !info.IsDir() {
		repo.Error = "repo path is not a folder: " + root
		return repo
	}

	evidence := dashboardEvidenceTimes(root)
	list, err := state.List(root)
	if err != nil {
		repo.Error = err.Error()
	}
	// Open waits are best effort: an unreadable record folder shows no
	// attention and does not fail the snapshot. Unlike a state.List error,
	// it does not set repo.Error, because the pipelines are still right.
	// attention.List returns nil records with its error.
	recs, _ := attention.List(root, now, dashboardHistoryWindow)
	for _, st := range list.States {
		p, updated, ok := dashboardStatePipeline(st, evidence)
		if ok {
			// Before dashboardFinish: the stalled rule reads p.Attention.
			dashboardAttachAttention(&p, recs)
		}
		if ok && dashboardFinish(&p, updated, now) {
			repo.Pipelines = append(repo.Pipelines, p)
		}
	}
	for _, r := range dashboardReviewPipelines(root) {
		if dashboardFinish(&r.pipeline, r.updated, now) {
			repo.Pipelines = append(repo.Pipelines, r.pipeline)
		}
	}
	repo.Pipelines = dashboardJoinRuns(repo.Pipelines)

	sessions, learnings, deferred, history := dashboardActivity(root, now)
	if sessions != nil {
		repo.Sessions = sessions
	}
	if learnings != nil {
		repo.Learnings = learnings
	}
	if deferred != nil {
		repo.Deferred = deferred
	}
	if history != nil {
		repo.History = history
	}
	return repo
}

// dashboardAttachAttention sets p.Attention from the newest record of recs
// that belongs to p. A record belongs to p when its session ID equals
// p.SessionID and the branch slugs (state.SlugifyBranch) of the record and
// of p are equal. Only a running pipeline with a session ID gets attention.
// Of several matching records the newest askedAt wins; of equal askedAt the
// later record in recs wins. A record whose askedAt does not parse is skipped.
// With no matching record p is unchanged.
func dashboardAttachAttention(p *DashboardPipeline, recs []attention.Record) {
	if p.Status != PipelineRunning || p.SessionID == "" {
		return
	}
	slug := state.SlugifyBranch(p.Branch)
	var best *attention.Record
	var bestAt time.Time
	for i := range recs {
		r := &recs[i]
		if r.SessionID != p.SessionID || state.SlugifyBranch(r.Branch) != slug {
			continue
		}
		asked, ok := dashboardParseTime(r.AskedAt)
		if !ok {
			continue
		}
		if best == nil || !asked.Before(bestAt) {
			best, bestAt = r, asked
		}
	}
	if best == nil {
		return
	}
	p.Attention = &DashboardAttention{
		Kind:    best.Kind,
		AskedAt: dashboardFormatTime(bestAt),
		Header:  best.Header,
		Text:    best.Text,
	}
}

// dashboardFinish stamps updatedAt, applies the stalled rule, and reports
// whether the pipeline shows: a completed or failed pipeline shows only when
// updatedAt is within dashboardHistoryWindow of now. A running pipeline with
// attention waits for a person, so it never turns stalled.
func dashboardFinish(p *DashboardPipeline, updated, now time.Time) bool {
	p.UpdatedAt = dashboardFormatTime(updated)
	if p.Status == PipelineRunning && p.Attention == nil && !updated.IsZero() && now.Sub(updated) > dashboardStallAfter {
		p.Status = PipelineStalled
		dashboardAddStalledIssue(p)
	}
	if p.Status == PipelineCompleted || p.Status == PipelineFailed {
		return !updated.IsZero() && now.Sub(updated) <= dashboardHistoryWindow
	}
	return true
}

// dashboardEvidenceLine is the part of a CLI or user-input evidence line the
// dashboard reads.
type dashboardEvidenceLine struct {
	TS     string `json:"ts"`
	Branch string `json:"branch"`
}

// dashboardEvidenceTimes returns, for each branch slug, the newest ts of the
// CLI and user-input evidence lines of that branch (current and rotated
// files). MCP invocation lines carry no branch and are not read.
func dashboardEvidenceTimes(root string) map[string]time.Time {
	out := map[string]time.Time{}
	files := []string{
		cliEvidencePath(root), cliEvidencePath(root) + ".1",
		userInputPath(root), userInputPath(root) + ".1",
	}
	for _, f := range files {
		lines, _ := readJSONLEntries[dashboardEvidenceLine](f, "dashboard evidence")
		for _, l := range lines {
			if l.Branch == "" {
				continue
			}
			t, ok := dashboardParseTime(l.TS)
			if !ok {
				continue
			}
			slug := state.SlugifyBranch(l.Branch)
			if t.After(out[slug]) {
				out[slug] = t
			}
		}
	}
	return out
}

// dashboardStatePipeline builds the pipeline of one ship, execute, or plan
// state file and returns its updatedAt. ok is false for other prefixes.
func dashboardStatePipeline(st *state.State, evidence map[string]time.Time) (DashboardPipeline, time.Time, bool) {
	data := st.Data
	p := DashboardPipeline{
		ID:     state.RunID(st),
		Kind:   st.Prefix,
		Branch: dashboardStr(data["branch"]),
		Steps:  []DashboardStep{},
		Issues: []DashboardIssue{},
	}
	if p.Branch == "" {
		p.Branch = st.BranchSlug
	}

	updated := dashboardModTime(st.Path)
	if t, ok := evidence[st.BranchSlug]; ok {
		updated = dashboardLatest(updated, t)
	}

	switch st.Prefix {
	case "ship":
		p.Worktree = dashboardStr(data["worktree"])
		dashboardShip(&p, data)
		dashboardShipDetail(&p, st)
		dashboardStateIssues(&p, data)
	case "execute":
		p.Worktree = dashboardStr(data["worktree"])
		updated = dashboardLatest(updated, dashboardExecute(&p, st))
		dashboardExecuteSteps(&p, st)
		dashboardStateIssues(&p, data)
	case "plan":
		dashboardPlan(&p, st)
	default:
		return DashboardPipeline{}, time.Time{}, false
	}

	if p.StartedAt == "" {
		id := p.ID
		if len(id) >= len(dashboardFilenameTimeLayout) {
			if t, err := time.Parse(dashboardFilenameTimeLayout, id[len(id)-len(dashboardFilenameTimeLayout):]); err == nil {
				p.StartedAt = dashboardFormatTime(t)
			}
		}
	}
	return p, updated, true
}

// dashboardShip fills a ship pipeline. A completed or skipped step counts
// as done. A completed commit step whose result starts with
// commitNothingPrefix carries a detail of kind result, so the page shows why
// no commit was made.
func dashboardShip(p *DashboardPipeline, data map[string]any) {
	p.StartedAt = dashboardStr(data["startedAt"])
	failed := false
	for _, raw := range shipStepsSlice(data) {
		s, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		name, status := dashboardStr(s["name"]), dashboardStr(s["status"])
		step := DashboardStep{Name: name, Status: status}
		step.StartedAt, step.CompletedAt = dashboardStepTimes(s)
		result := dashboardStr(s["result"]) // ship_state stores step["result"] on complete-step
		if name == shipCommitStep && status == StepCompleted && strings.HasPrefix(result, commitNothingPrefix) {
			step.Detail = &DashboardStepDetail{Kind: dashboardKindResult, Result: result}
		}
		p.Steps = append(p.Steps, step)
		switch status {
		case StepCompleted, StepSkipped:
			p.Progress.Done++
		case StepFailed:
			failed = true
			text := dashboardStr(s["error"])
			if text == "" {
				text = dashboardStr(s["reason"])
			}
			if text == "" {
				text = "failed"
			}
			p.Issues = append(p.Issues, dashboardStepIssue(name, text))
		}
	}
	p.Progress.Total = len(p.Steps)
	if cur := shipLastActiveStep(data); cur != nil {
		p.Progress.Current = dashboardStr(cur["name"])
	}
	p.Progress.Label = dashboardStepLabel(p.Progress)

	switch {
	case dashboardStr(data["pipelineStatus"]) == "completed":
		p.Status = PipelineCompleted
		p.CompletedAt = dashboardStrPtr(dashboardStr(data["pipelineCompletedAt"]))
	case failed && !shipRunInFlight(data):
		p.Status = PipelineFailed
	default:
		p.Status = PipelineRunning
	}
}

// dashboardExecute fills an execute pipeline: one step for each wave, then one
// pending step for each planned wave that has not started. Done and total
// count tasks. It returns the newest modification time of the task
// progress files, found through waves[].runId.
func dashboardExecute(p *DashboardPipeline, st *state.State) time.Time {
	data := st.Data
	p.StartedAt = dashboardStr(data["startedAt"])

	var newest time.Time
	seenRuns := map[string]bool{}
	seenTasks := map[string]bool{}
	doneTasks := map[string]bool{}
	anyFailed, anyInProgress := false, false

	waves, _ := data["waves"].([]any)
	for _, raw := range waves {
		w, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		name := fmt.Sprintf("wave %d", dashboardInt(w["number"]))
		status := dashboardStr(w["status"])
		stepStatus := status
		switch status {
		case "partial":
			stepStatus = StepFailed
			p.Issues = append(p.Issues, dashboardWaveIssue(name, status))
		case StepFailed:
			anyFailed = true
			p.Issues = append(p.Issues, dashboardWaveIssue(name, status))
		case StepInProgress:
			anyInProgress = true
			p.Progress.Current = name
		}
		step := DashboardStep{Name: name, Status: stepStatus}
		step.StartedAt, step.CompletedAt = dashboardStepTimes(w)
		p.Steps = append(p.Steps, step)

		if runID := dashboardStr(w["runId"]); runID != "" && !seenRuns[runID] {
			seenRuns[runID] = true
			newest = dashboardLatest(newest, dashboardProgressTime(st.Root, runID))
		}

		tasks, _ := w["tasks"].([]any)
		for _, rt := range tasks {
			t, ok := rt.(map[string]any)
			if !ok {
				continue
			}
			id := dashboardStr(t["id"])
			if id == "" {
				continue
			}
			seenTasks[id] = true
			if dashboardStr(t["status"]) == StepCompleted {
				doneTasks[id] = true
			}
		}
	}

	// A planned wave that no waves entry covers yet is a pending step.
	for _, n := range dashboardPendingWaveNumbers(data) {
		p.Steps = append(p.Steps, DashboardStep{Name: fmt.Sprintf("wave %d", n), Status: StepPending})
	}

	total := dashboardInt(data["totalTasks"])
	if total == 0 {
		planned, _ := data["plannedTaskIds"].([]any)
		total = len(planned)
	}
	if total == 0 {
		total = len(seenTasks)
	}
	p.Progress.Done = len(doneTasks)
	p.Progress.Total = total
	p.Progress.Label = fmt.Sprintf("%d of %d tasks", p.Progress.Done, p.Progress.Total)

	switch {
	case dashboardStr(data["runStatus"]) == "completed":
		p.Status = PipelineCompleted
		p.CompletedAt = dashboardStrPtr(dashboardStr(data["runCompletedAt"]))
	case anyFailed && !anyInProgress:
		p.Status = PipelineFailed
	default:
		p.Status = PipelineRunning
	}
	return newest
}

// dashboardProgressTime returns the newest modification time of the files
// in <root>/.sdlc-v2/runs/<runID>/progress/. A runID that is not a bare
// folder name is ignored.
func dashboardProgressTime(root, runID string) time.Time {
	if filepath.Base(runID) != runID || runID == "." || runID == ".." {
		return time.Time{}
	}
	dir := filepath.Join(root, paths.DataDir, paths.RunsSubdir, runID, "progress")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return time.Time{}
	}
	var newest time.Time
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if info, err := e.Info(); err == nil {
			newest = dashboardLatest(newest, info.ModTime())
		}
	}
	return newest
}

// dashboardReviewRow is a review pipeline with its updatedAt.
type dashboardReviewRow struct {
	pipeline DashboardPipeline
	updated  time.Time
}

// dashboardReviewDim is the part of a review dimension ledger file the
// dashboard reads.
type dashboardReviewDim struct {
	CheckinAt  string          `json:"checkinAt"`
	CheckoutAt string          `json:"checkoutAt"`
	Findings   json.RawMessage `json:"findings"`
}

// dashboardFinding is one review finding.
type dashboardFinding struct {
	Severity  string `json:"severity"`
	File      string `json:"file"`
	Line      any    `json:"line"`
	Rationale string `json:"rationale"`
}

// dashboardReviewPipelines returns one review pipeline for each
// runs/ledger/review-*/ folder that holds at least one dimension file or a
// run.meta with planned dimensions, newest folder first.
func dashboardReviewPipelines(root string) []dashboardReviewRow {
	dir := ledgerRootDir(root)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), dashboardReviewPrefix) {
			names = append(names, e.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))

	var rows []dashboardReviewRow
	for _, name := range names {
		if row, ok := dashboardReviewPipeline(filepath.Join(dir, name), name); ok {
			rows = append(rows, row)
		}
	}
	return rows
}

// dashboardReviewPipeline builds the review pipeline of one ledger folder
// from readReviewLedgerDir: one step for each row, so a planned dimension
// with no worker file is a pending step. The progress total is the row count;
// done counts the completed and skipped rows. The pipeline is completed when
// no row is pending or in progress. Its completedAt is the newest checkoutAt,
// else the folder update time (every row skipped). updated is the newest
// modification time of run.meta and the worker files, so a folder with only
// run.meta turns stalled. Each problem of readReviewLedger (a worker file or
// run.meta that cannot be used) is a state issue of severity medium, after
// the review findings. ok is false when the folder cannot be read or gives
// no row.
func dashboardReviewPipeline(dir, name string) (dashboardReviewRow, bool) {
	read, err := readReviewLedger(dir)
	rows, totals, updated := read.rows, read.totals, read.updated
	if err != nil || len(rows) == 0 {
		return dashboardReviewRow{}, false
	}
	p := DashboardPipeline{
		ID:     name,
		Kind:   "review",
		Steps:  make([]DashboardStep, 0, len(rows)),
		Issues: []DashboardIssue{},
	}
	p.join.reviewDims = make([]DashboardDimension, 0, len(rows))
	if totals.DimensionsPlanned > 0 {
		p.join.reviewPlan = &DashboardReviewPlan{
			WavesPlanned:      totals.WavesPlanned,
			WavesRun:          totals.WavesRun,
			DimensionsPlanned: totals.DimensionsPlanned,
			DimensionsRun:     totals.DimensionsRun,
			NeverStarted:      totals.NeverStarted,
		}
	}
	var firstCheckin, lastCheckout time.Time
	var firstPending string
	for _, r := range rows {
		switch r.Status {
		case StepCompleted:
			p.Progress.Done++
			lastCheckout = dashboardLatest(lastCheckout, r.checkoutAt)
		case StepSkipped:
			p.Progress.Done++
		case StepInProgress:
			if p.Progress.Current == "" {
				p.Progress.Current = r.Name
			}
		case StepPending:
			if firstPending == "" {
				firstPending = r.Name
			}
		}
		if !r.checkinAt.IsZero() && (firstCheckin.IsZero() || r.checkinAt.Before(firstCheckin)) {
			firstCheckin = r.checkinAt
		}
		p.Steps = append(p.Steps, DashboardStep{Name: r.Name, Status: r.Status})
		p.join.reviewDims = append(p.join.reviewDims, DashboardDimension{
			Name: r.Name, Status: r.Status, Findings: r.Findings, Worst: r.Worst, Wave: r.Wave, Reason: r.Reason,
		})
		for _, fd := range r.findings {
			p.Issues = append(p.Issues, dashboardReviewIssue(r.Name, fd))
		}
	}
	for _, text := range read.problems {
		p.Issues = append(p.Issues, DashboardIssue{Source: dashboardSourceState, Severity: "medium", Text: text})
	}
	if p.Progress.Current == "" {
		p.Progress.Current = firstPending
	}

	p.Progress.Total = len(p.Steps)
	p.Progress.Label = fmt.Sprintf("%d of %d dimensions", p.Progress.Done, p.Progress.Total)
	if p.Progress.Done == p.Progress.Total {
		p.Status = PipelineCompleted
		if lastCheckout.IsZero() {
			lastCheckout = updated
		}
		p.CompletedAt = dashboardStrPtr(dashboardFormatTime(lastCheckout))
	} else {
		p.Status = PipelineRunning
	}
	p.StartedAt = dashboardFormatTime(firstCheckin)
	if p.StartedAt == "" {
		if t, err := time.Parse(dashboardReviewTimeLayout, strings.TrimPrefix(name, dashboardReviewPrefix)); err == nil {
			p.StartedAt = dashboardFormatTime(t)
		}
	}
	dashboardReviewFindings(&p)
	dashboardReviewMeta(dir, &p)
	return dashboardReviewRow{pipeline: p, updated: updated}, true
}

// dashboardParseFindings decodes the findings field of a review dimension
// file: a JSON-encoded string holding a list of findings (a bare JSON list
// is accepted too). Any other content, such as a markdown block, gives no
// findings and isList false. An absent field, JSON null, and an empty or
// blank string give no findings and isList true.
func dashboardParseFindings(raw json.RawMessage) (findings []dashboardFinding, isList bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, true
	}
	list := []byte(raw)
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		if strings.TrimSpace(s) == "" {
			return nil, true
		}
		list = []byte(s)
	}
	var out []dashboardFinding
	if err := json.Unmarshal(list, &out); err != nil {
		return nil, false
	}
	return out, true
}

// dashboardStepLabel returns "step <n> of <total>" while a current step is
// known, else "<done> of <total> steps".
func dashboardStepLabel(pr DashboardProgress) string {
	if pr.Current != "" {
		return fmt.Sprintf("step %d of %d", min(pr.Done+1, pr.Total), pr.Total)
	}
	return fmt.Sprintf("%d of %d steps", pr.Done, pr.Total)
}

// dashboardStr returns v as a string, or "" when v is not a string.
func dashboardStr(v any) string {
	s, _ := v.(string)
	return s
}

// dashboardStrPtr returns a pointer to s, or nil when s is empty.
func dashboardStrPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// dashboardInt returns a JSON number as an int, or 0.
func dashboardInt(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	}
	return 0
}

// dashboardParseTime parses an RFC 3339 time with or without fractional
// seconds.
func dashboardParseTime(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// dashboardStepTime returns s when dashboardParseTime accepts it, else "".
// The snapshot never carries a step time that the page cannot read.
func dashboardStepTime(s string) string {
	if _, ok := dashboardParseTime(s); !ok {
		return ""
	}
	return s
}

// dashboardStepTimes returns the startedAt and completedAt of the ship step
// or execute wave m, each through dashboardStepTime.
func dashboardStepTimes(m map[string]any) (started, completed string) {
	return dashboardStepTime(dashboardStr(m["startedAt"])), dashboardStepTime(dashboardStr(m["completedAt"]))
}

// dashboardFormatTime formats t as RFC 3339 UTC, or "" for the zero time.
func dashboardFormatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// dashboardModTime returns the modification time of path, or the zero time.
func dashboardModTime(path string) time.Time {
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}

// dashboardLatest returns the later of a and b.
func dashboardLatest(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}
