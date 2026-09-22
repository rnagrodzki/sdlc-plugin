// Package history provides an append-only JSONL run-history store and a
// deferred-issue manager that survive state-file GC. Storage lives under
// .sdlc-v2/history/ — outside the execution/ directory that GC sweeps.
package history

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

// ---------------------------------------------------------------------------
// Domain types
// ---------------------------------------------------------------------------

// RunRecord is one pipeline-completion entry appended to runs.jsonl.
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
	// SourceReviewBelowThreshold — written by ship_state defer.
	SourceReviewBelowThreshold = "review-below-threshold"
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

// ---------------------------------------------------------------------------
// Writer interface
// ---------------------------------------------------------------------------

// Writer abstracts history persistence so tests can use MemWriter.
type Writer interface {
	AppendRun(record RunRecord) error
	AddDeferred(issue DeferredIssue) error
	ListDeferred() ([]DeferredIssue, error)
	ResolveDeferred(id string) error
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
		return fmt.Errorf("history: create dir: %w", err)
	}
	issues, err := w.readDeferred()
	if err != nil {
		return fmt.Errorf("history: read before add: %w", err)
	}
	issues = append(issues, issue)
	return w.writeDeferred(issues)
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

func (w *FileWriter) readDeferred() ([]DeferredIssue, error) {
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
		return nil, fmt.Errorf("history: parse deferred.json: %w", err)
	}
	return issues, nil
}

func (w *FileWriter) writeDeferred(issues []DeferredIssue) error {
	data, err := json.MarshalIndent(issues, "", "  ")
	if err != nil {
		return fmt.Errorf("history: marshal deferred.json: %w", err)
	}
	data = append(data, '\n')
	return os.WriteFile(w.DeferredPath(), data, 0o644)
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
			fmt.Fprintf(&sb, "- [%s] %s (source: %s)\n", item.ID, item.Description, item.Source)
		}
		sb.WriteString("\n")
	}
	return sb.String()
}
