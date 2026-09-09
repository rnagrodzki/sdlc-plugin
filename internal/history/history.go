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
type DeferredIssue struct {
	ID          string `json:"id"`
	Created     string `json:"created"`
	Source      string `json:"source"`
	Priority    string `json:"priority"`
	Description string `json:"description"`
	Status      string `json:"status"`
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

func (w *FileWriter) runsPath() string {
	return filepath.Join(w.root, "runs.jsonl")
}

func (w *FileWriter) deferredPath() string {
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

	f, err := os.OpenFile(w.runsPath(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
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
	issues, _ := w.readDeferred() // ignore read error — treat as empty
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
			issues[i].Status = "resolved"
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
	data, err := os.ReadFile(w.deferredPath())
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
	return os.WriteFile(w.deferredPath(), data, 0o644)
}

// ReadRecentRuns reads the last n RunRecords from runs.jsonl.
func (w *FileWriter) ReadRecentRuns(n int) ([]RunRecord, error) {
	data, err := os.ReadFile(w.runsPath())
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
			m.Deferred[i].Status = "resolved"
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
		if issue.Status != "open" {
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
		if issue.Status == "open" {
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

	for _, prio := range []string{"high", "medium", "low"} {
		items := groups[prio]
		if len(items) == 0 {
			continue
		}
		sort.Slice(items, func(i, j int) bool { return items[i].Created < items[j].Created })
		fmt.Fprintf(&sb, "**%s:**\n", strings.Title(prio))
		for _, item := range items {
			fmt.Fprintf(&sb, "- [%s] %s (source: %s)\n", item.ID, item.Description, item.Source)
		}
		sb.WriteString("\n")
	}
	return sb.String()
}
