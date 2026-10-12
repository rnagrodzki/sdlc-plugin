// Package history provides an append-only JSONL run-history store and a
// deferred-issue manager that survive state-file GC. Storage lives under
// .sdlc-v2/history/ — outside the execution/ directory that GC sweeps.
package history

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"

	"github.com/rnagrodzki/sdlc-plugin/internal/fsx"
)

// ---------------------------------------------------------------------------
// Domain types
// ---------------------------------------------------------------------------

// RunRecord is one pipeline-completion entry appended to runs.jsonl.
//
// PlanFile, StartedAt and LastModifiedAt are written by the plan skill's
// "done" marker (internal/tools/plan.go's appendPlanRunRecord): the plan
// run's resolved plan file path, and its timing window (run start to the
// plan file's last edit — never the "done" call time). All three are
// omitempty so every other producer's records, and every record written
// before these fields existed, keep parsing and serializing exactly as
// before.
//
// Ship rows also carry PlanFile, with PlanStartedAt and PlanDurationMs,
// copied from the ship state linkedPlan (internal/tools/ship_state.go's
// shipHistoryPlanFields): the linked plan file, its start time and its start
// to done duration. PlanStartedAt and PlanDurationMs are omitempty for the
// same reason as the three fields above.
type RunRecord struct {
	Timestamp      string   `json:"ts"`
	Skill          string   `json:"skill"`
	Branch         string   `json:"branch"`
	Outcome        string   `json:"outcome"`
	DurationMs     int64    `json:"duration_ms"`
	Steps          []string `json:"steps,omitempty"`
	GuardrailHits  []string `json:"guardrail_hits,omitempty"`
	DeferredIssues []string `json:"deferred_issues,omitempty"`
	Version        string   `json:"version,omitempty"`
	PlanFile       string   `json:"plan_file,omitempty"`
	PlanStartedAt  string   `json:"plan_started_at,omitempty"`  // ship rows: linked plan start
	PlanDurationMs int64    `json:"plan_duration_ms,omitempty"` // ship rows: plan start to plan done
	StartedAt      string   `json:"started_at,omitempty"`
	LastModifiedAt string   `json:"last_modified_at,omitempty"`
}

// DeferredIssue is a problem deferred from a pipeline run for later triage.
//
// Severity, File, Line and Reason are optional: they carry the structured
// detail a review finding already has, so a triage step can draft a GitHub
// issue body without unpacking it back out of Description. All four are
// omitempty, so a deferred.json written before they existed still parses —
// encoding/json leaves absent fields at their zero value.
type DeferredIssue struct {
	ID          string `json:"id"`
	Created     string `json:"created"`
	Source      string `json:"source"`
	Priority    string `json:"priority"`
	Description string `json:"description"`
	Status      string `json:"status"`
	Severity    string `json:"severity,omitempty"`
	File        string `json:"file,omitempty"`
	Line        int    `json:"line,omitempty"`
	Reason      string `json:"reason,omitempty"`
}

// Accepted values for DeferredIssue.Reason — why the item was deferred
// instead of fixed. This is the single definition of the set: callers
// validate through ValidDeferredReason and render through DeferredReasons
// rather than restating the literals.
const (
	// ReasonBelowThreshold — the finding's severity was under the
	// configured review threshold, so the pipeline never routed it to a fix.
	ReasonBelowThreshold = "below-threshold"
	// ReasonNeedsDirection — two or more candidate approaches exist and a
	// human has to pick one (KD-13).
	ReasonNeedsDirection = "needs-direction"
	// ReasonDisagree — the finding was judged wrong, with the reasoning
	// recorded for a human to check.
	ReasonDisagree = "disagree"
	// ReasonWontFix — the finding is accepted but deliberately not fixed.
	ReasonWontFix = "wont-fix"
)

// Accepted values for DeferredIssue.Status. OpenDeferred,
// DeferredByPriority and ResolveDeferred all key off these exact strings,
// so a writer that spells one of them differently silently disappears from
// every triage view — hence constants rather than repeated literals.
const (
	// StatusOpen — the item is still awaiting triage.
	StatusOpen = "open"
	// StatusResolved — the item has been dealt with.
	StatusResolved = "resolved"
)

// Accepted values for DeferredIssue.Priority — the buckets
// DeferredByPriority groups on and FormatDeferredSummary renders in order.
const (
	PriorityHigh   = "high"
	PriorityMedium = "medium"
	PriorityLow    = "low"
)

// Known values for DeferredIssue.Source — which pipeline stage created the
// item. Unlike Reason, this set is not validated: deferred_add accepts a
// caller-supplied source verbatim. The constants exist so the two in-tree
// producers agree with the strings that triage tooling matches on.
const (
	// SourceReviewBelowThreshold — written by ship_state defer's default source.
	SourceReviewBelowThreshold = "review-below-threshold"
	// SourceReceivedReview — written by ship_state defer when the caller is
	// received-review, recording a finding the user marked wont-fix,
	// disagree, or needs-direction.
	SourceReceivedReview = "received-review"
	// SourceExecuteDrift — written by execute_state issue-draft.
	SourceExecuteDrift = "execute-drift"
)

// DeferredReasons returns the accepted DeferredIssue.Reason values in a
// stable order, for error messages and documentation.
func DeferredReasons() []string {
	return []string{ReasonBelowThreshold, ReasonNeedsDirection, ReasonDisagree, ReasonWontFix}
}

// ValidDeferredReason reports whether reason is one of the accepted values.
// The empty string is not valid: callers treat "" as "unset" and skip the
// check rather than passing it here.
func ValidDeferredReason(reason string) bool {
	switch reason {
	case ReasonBelowThreshold, ReasonNeedsDirection, ReasonDisagree, ReasonWontFix:
		return true
	}
	return false
}

// DeferredFileMax is the size limit, in bytes, of deferred.json. A bigger
// file is refused before it is read. The bound lives here because the
// package tools imports this package: dashboardDeleteReadMax in
// internal/tools takes its value from this constant.
const DeferredFileMax = 8 << 20

// Errors of the deferred.json store. A caller maps them with errors.Is.
var (
	// ErrDeferredTooLarge — deferred.json is larger than DeferredFileMax.
	ErrDeferredTooLarge = errors.New("history: deferred.json is larger than 8 MiB")
	// ErrDeferredParse — deferred.json is not valid JSON for a list of items.
	ErrDeferredParse = errors.New("history: deferred.json does not parse")
	// ErrDeferredWrite — the history directory or deferred.json could not be
	// written. deferred.json is unchanged: the write is atomic.
	ErrDeferredWrite = errors.New("history: write deferred.json failed")
)

// ---------------------------------------------------------------------------
// Writer interface
// ---------------------------------------------------------------------------

// Writer abstracts history persistence so tests can use MemWriter.
type Writer interface {
	AppendRun(record RunRecord) error
	AddDeferred(issue DeferredIssue) error
	ListDeferred() ([]DeferredIssue, error)
	ResolveDeferred(id string) error
	// MergeOrAddDeferred stores issue, merging it into an open row when a
	// merge key matches. See mergeDeferred for the keys. stored is the row
	// that holds the text after the call. merged is true when an existing
	// open row received at least one new line. A call that changes nothing
	// writes nothing.
	MergeOrAddDeferred(issue DeferredIssue, mergeByID bool) (stored DeferredIssue, merged bool, err error)
}

// ---------------------------------------------------------------------------
// FileWriter — real filesystem implementation
// ---------------------------------------------------------------------------

// FileWriter implements Writer by persisting to .sdlc-v2/history/.
type FileWriter struct {
	root string // path to .sdlc-v2/history/
}

// NewFileWriter returns a FileWriter rooted at the given history directory.
// The directory is created on first write, not at construction time.
func NewFileWriter(historyDir string) *FileWriter {
	return &FileWriter{root: historyDir}
}

func (w *FileWriter) ensureDir() error {
	return os.MkdirAll(w.root, 0o755)
}

// RunsPath returns the file that AppendRun writes to.
func (w *FileWriter) RunsPath() string {
	return filepath.Join(w.root, "runs.jsonl")
}

// DeferredPath returns the file that AddDeferred and ResolveDeferred write to.
func (w *FileWriter) DeferredPath() string {
	return filepath.Join(w.root, "deferred.json")
}

// AppendRun appends a single RunRecord as one JSON line to runs.jsonl.
func (w *FileWriter) AppendRun(record RunRecord) error {
	if err := w.ensureDir(); err != nil {
		return fmt.Errorf("history: create dir: %w", err)
	}
	line, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("history: marshal run record: %w", err)
	}
	line = append(line, '\n')

	f, err := os.OpenFile(w.RunsPath(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("history: open runs.jsonl: %w", err)
	}
	defer f.Close()

	if _, err := f.Write(line); err != nil {
		return fmt.Errorf("history: write run record: %w", err)
	}
	return nil
}

// AddDeferred adds a deferred issue to deferred.json.
func (w *FileWriter) AddDeferred(issue DeferredIssue) error {
	if err := w.ensureDir(); err != nil {
		return fmt.Errorf("history: create dir: %w: %w", ErrDeferredWrite, err)
	}
	issues, err := w.readDeferred()
	if err != nil {
		return fmt.Errorf("history: read before add: %w", err)
	}
	issues = append(issues, issue)
	return w.writeDeferred(issues)
}

// MergeOrAddDeferred stores issue in deferred.json. It reads the whole file,
// applies mergeDeferred and, when the result differs, writes the file back
// atomically. A read, parse or size failure leaves the file as it was.
func (w *FileWriter) MergeOrAddDeferred(issue DeferredIssue, mergeByID bool) (DeferredIssue, bool, error) {
	if err := w.ensureDir(); err != nil {
		return DeferredIssue{}, false, fmt.Errorf("history: create dir: %w: %w", ErrDeferredWrite, err)
	}
	issues, err := w.readDeferred()
	if err != nil {
		return DeferredIssue{}, false, fmt.Errorf("history: read before merge: %w", err)
	}
	updated, stored, merged, changed := mergeDeferred(issues, issue, mergeByID)
	if !changed {
		return stored, false, nil
	}
	if err := w.writeDeferred(updated); err != nil {
		return DeferredIssue{}, false, err
	}
	return stored, merged, nil
}

// ListDeferred returns all deferred issues.
func (w *FileWriter) ListDeferred() ([]DeferredIssue, error) {
	return w.readDeferred()
}

// ResolveDeferred marks a deferred issue as resolved by ID.
func (w *FileWriter) ResolveDeferred(id string) error {
	issues, err := w.readDeferred()
	if err != nil {
		return err
	}
	found := false
	for i := range issues {
		if issues[i].ID == id {
			issues[i].Status = StatusResolved
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("history: deferred issue %q not found", id)
	}
	return w.writeDeferred(issues)
}

// readDeferred reads deferred.json. A missing or blank file is an empty
// list. A file over DeferredFileMax fails with ErrDeferredTooLarge before
// any byte is read. A file that does not parse fails with ErrDeferredParse.
func (w *FileWriter) readDeferred() ([]DeferredIssue, error) {
	info, err := os.Stat(w.DeferredPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("history: read deferred.json: %w", err)
	}
	if info.Size() > DeferredFileMax {
		return nil, fmt.Errorf("%w (%d bytes)", ErrDeferredTooLarge, info.Size())
	}
	data, err := os.ReadFile(w.DeferredPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("history: read deferred.json: %w", err)
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return nil, nil
	}
	var issues []DeferredIssue
	if err := json.Unmarshal(data, &issues); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrDeferredParse, err)
	}
	return issues, nil
}

func (w *FileWriter) writeDeferred(issues []DeferredIssue) error {
	data, err := json.MarshalIndent(issues, "", "  ")
	if err != nil {
		return fmt.Errorf("history: marshal deferred.json: %w", err)
	}
	data = append(data, '\n')
	if err := fsx.AtomicWriteBytes(w.DeferredPath(), data); err != nil {
		return fmt.Errorf("%w: %w", ErrDeferredWrite, err)
	}
	return nil
}

// ReadRecentRuns reads the last n RunRecords from runs.jsonl.
func (w *FileWriter) ReadRecentRuns(n int) ([]RunRecord, error) {
	data, err := os.ReadFile(w.RunsPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("history: read runs.jsonl: %w", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return nil, nil
	}

	// Take last n lines.
	start := 0
	if len(lines) > n {
		start = len(lines) - n
	}
	lines = lines[start:]

	var records []RunRecord
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var r RunRecord
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			continue // skip malformed lines
		}
		records = append(records, r)
	}
	return records, nil
}

// ---------------------------------------------------------------------------
// MemWriter — in-memory implementation for tests
// ---------------------------------------------------------------------------

// MemWriter implements Writer using in-memory slices. Safe for concurrent use.
type MemWriter struct {
	mu       sync.Mutex
	Runs     []RunRecord
	Deferred []DeferredIssue
}

// AppendRun appends a run record to the in-memory slice.
func (m *MemWriter) AppendRun(record RunRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Runs = append(m.Runs, record)
	return nil
}

// AddDeferred adds a deferred issue to the in-memory slice.
func (m *MemWriter) AddDeferred(issue DeferredIssue) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Deferred = append(m.Deferred, issue)
	return nil
}

// MergeOrAddDeferred stores issue in the in-memory slice with the same rules
// as FileWriter.MergeOrAddDeferred.
func (m *MemWriter) MergeOrAddDeferred(issue DeferredIssue, mergeByID bool) (DeferredIssue, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	updated, stored, merged, changed := mergeDeferred(m.Deferred, issue, mergeByID)
	if changed {
		m.Deferred = updated
	}
	return stored, merged, nil
}

// ListDeferred returns a copy of all deferred issues.
func (m *MemWriter) ListDeferred() ([]DeferredIssue, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]DeferredIssue, len(m.Deferred))
	copy(out, m.Deferred)
	return out, nil
}

// ResolveDeferred marks a deferred issue as resolved by ID.
func (m *MemWriter) ResolveDeferred(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.Deferred {
		if m.Deferred[i].ID == id {
			m.Deferred[i].Status = StatusResolved
			return nil
		}
	}
	return fmt.Errorf("history: deferred issue %q not found", id)
}

// ---------------------------------------------------------------------------
// Merge of a deferred issue into the stored rows
// ---------------------------------------------------------------------------

// mergeDeferred applies issue to rows and returns the new rows. The input
// slice is not changed. stored is the row that holds the text of issue
// afterwards. merged is true when an existing open row received at least one
// new line. changed is true when the new rows differ from rows. The first
// rule that matches wins:
//
//	mergeByID false, a row has the same id (any status): no change. stored is that row.
//	mergeByID true, an open row has the same id:         append the new lines to it.
//	mergeByID true, an open row has id <id>-<n>:         append the new lines to it.
//	reason needs-direction, file set, an open row has
//	  the same file and the same reason:                 append the new lines to it.
//	mergeByID true, only resolved rows have the same id: add a row with id <id>-<n>, n from 2.
//	any other case:                                      add issue as a new row.
//
// The <id>-<n> rule keeps a repeat call from adding one more row each time
// after the first row of an id was resolved. A below-threshold finding never
// matches a file: each one stays its own row, because the fix pass resolves
// one row at a time.
func mergeDeferred(rows []DeferredIssue, issue DeferredIssue, mergeByID bool) (updated []DeferredIssue, stored DeferredIssue, merged, changed bool) {
	idSeen := false
	openByID, openBySuffix, openByFile := -1, -1, -1
	for i, r := range rows {
		switch {
		case r.ID == issue.ID:
			idSeen = true
			if r.Status == StatusOpen && openByID < 0 {
				openByID = i
			}
		case r.Status == StatusOpen && openBySuffix < 0 && isSuffixedID(r.ID, issue.ID):
			openBySuffix = i
		}
		if openByFile < 0 && r.Status == StatusOpen && issue.Reason == ReasonNeedsDirection &&
			issue.File != "" && r.File == issue.File && r.Reason == ReasonNeedsDirection {
			openByFile = i
		}
	}

	if !mergeByID && idSeen {
		for _, r := range rows {
			if r.ID == issue.ID {
				return rows, r, false, false
			}
		}
	}

	target := -1
	switch {
	case mergeByID && openByID >= 0:
		target = openByID
	case mergeByID && openBySuffix >= 0:
		target = openBySuffix
	case openByFile >= 0:
		target = openByFile
	}
	if target >= 0 {
		desc, added := appendNewLines(rows[target].Description, issue.Description)
		if !added {
			return rows, rows[target], false, false
		}
		updated = append([]DeferredIssue(nil), rows...)
		updated[target].Description = desc
		return updated, updated[target], true, true
	}

	if mergeByID && idSeen {
		issue.ID = nextFreeDeferredID(rows, issue.ID)
	}
	updated = append(append([]DeferredIssue(nil), rows...), issue)
	return updated, issue, false, true
}

// isSuffixedID reports whether id is base followed by "-" and digits, the
// form that nextFreeDeferredID gives to the second and later rows of base.
func isSuffixedID(id, base string) bool {
	rest, ok := strings.CutPrefix(id, base+"-")
	if !ok || rest == "" {
		return false
	}
	for _, c := range rest {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// nextFreeDeferredID returns <id>-<n> for the smallest n >= 2 that no row
// uses as an id.
func nextFreeDeferredID(rows []DeferredIssue, id string) string {
	used := make(map[string]bool, len(rows))
	for _, r := range rows {
		used[r.ID] = true
	}
	for n := 2; ; n++ {
		candidate := fmt.Sprintf("%s-%d", id, n)
		if !used[candidate] {
			return candidate
		}
	}
}

// appendNewLines adds each non-blank line of incoming to existing as a
// "- <line>" line, unless a line of existing already holds that text. A
// leading "- " is ignored on both sides of the comparison. added is false
// when every line was already there; desc is then existing, unchanged.
func appendNewLines(existing, incoming string) (desc string, added bool) {
	held := map[string]bool{}
	for _, l := range strings.Split(existing, "\n") {
		held[lineText(l)] = true
	}
	var sb strings.Builder
	sb.WriteString(existing)
	for _, l := range strings.Split(incoming, "\n") {
		text := lineText(l)
		if text == "" || held[text] {
			continue
		}
		held[text] = true
		if sb.Len() > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString("- " + text)
		added = true
	}
	if !added {
		return existing, false
	}
	return sb.String(), true
}

// lineText trims a description line and drops one leading "- " list marker.
func lineText(line string) string {
	t := strings.TrimSpace(line)
	t = strings.TrimPrefix(t, "- ")
	return strings.TrimSpace(t)
}

// ---------------------------------------------------------------------------
// Helpers for grouping deferred issues by priority
// ---------------------------------------------------------------------------

// DeferredByPriority groups open deferred issues by priority (high, medium, low).
func DeferredByPriority(issues []DeferredIssue) map[string][]DeferredIssue {
	groups := map[string][]DeferredIssue{}
	for _, issue := range issues {
		if issue.Status != StatusOpen {
			continue
		}
		groups[issue.Priority] = append(groups[issue.Priority], issue)
	}
	return groups
}

// OpenDeferred filters to only open deferred issues.
func OpenDeferred(issues []DeferredIssue) []DeferredIssue {
	var open []DeferredIssue
	for _, issue := range issues {
		if issue.Status == StatusOpen {
			open = append(open, issue)
		}
	}
	return open
}

// FormatDeferredSummary produces a markdown summary of open deferred issues
// grouped by priority, suitable for display in the ship pipeline summary.
// Each issue shows only the first line of its description.
func FormatDeferredSummary(issues []DeferredIssue) string {
	open := OpenDeferred(issues)
	if len(open) == 0 {
		return ""
	}
	groups := DeferredByPriority(issues)
	var sb strings.Builder
	fmt.Fprintf(&sb, "%d deferred issue(s) from previous runs still open:\n\n", len(open))

	for _, prio := range []string{PriorityHigh, PriorityMedium, PriorityLow} {
		items := groups[prio]
		if len(items) == 0 {
			continue
		}
		sort.Slice(items, func(i, j int) bool { return items[i].Created < items[j].Created })
		fmt.Fprintf(&sb, "**%s:**\n", cases.Title(language.English).String(prio))
		for _, item := range items {
			// A description can span several lines; only its first line fits
			// one list item.
			first, _, _ := strings.Cut(strings.TrimSpace(item.Description), "\n")
			fmt.Fprintf(&sb, "- [%s] %s (source: %s)\n", item.ID, strings.TrimSpace(first), item.Source)
		}
		sb.WriteString("\n")
	}
	return sb.String()
}
