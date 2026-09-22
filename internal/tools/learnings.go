package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
	"github.com/rnagrodzki/sdlc-plugin/internal/worktree"
)

// ---------------------------------------------------------------------------
// learnings_log tool
// ---------------------------------------------------------------------------

// learningsLogHeader is written once, when the log file does not yet exist.
const learningsLogHeader = "# SDLC Execution Learnings\n"

// learningsLogPath returns the absolute path to the learnings log file.
func learningsLogPath(root string) string {
	return filepath.Join(root, paths.DataDir, "learnings", "log.md")
}

// LearningsLogIn is the input for the learnings_log tool.
type LearningsLogIn struct {
	// Action selects the operation: "append", "read", "remove", or "stats".
	Action string `json:"action" jsonschema:"enum=append,enum=read,enum=remove,enum=stats" jsonschema_description:"Selects the operation: \"append\", \"read\", \"remove\", or \"stats\"."`
	// Entry is the markdown block to append (required for "append"). It is
	// written verbatim, separated from surrounding content by one blank
	// line; do not include a leading or trailing blank line. Must not
	// contain a blank line ("\n\n") — that is the entry delimiter.
	Entry string `json:"entry,omitempty" jsonschema_description:"Markdown block to append (required for action \"append\"). Written verbatim, separated from surrounding content by one blank line; do not include a leading or trailing blank line. Must not contain a blank line (the entry delimiter)."`
	// TailLines, for "read", limits the returned content to the last N
	// lines. Zero (default) returns the whole file.
	TailLines int `json:"tailLines,omitempty" jsonschema_description:"For action \"read\", limits the returned content to the last N lines. Zero (default) returns the whole file."`
	// Indices, for "remove", selects which entries to delete.
	Indices []int `json:"indices,omitempty" jsonschema_description:"1-indexed entry numbers to remove (required for action \"remove\"). Entries are blocks separated by blank lines, header excluded."`
	// RunID, for "append", tags the entry for later linkage to an execution run.
	RunID string `json:"runId,omitempty" jsonschema_description:"Execution run ID to tag this entry for later linkage."`
	// Branch, for "append", tags the entry with the branch name for later linkage.
	Branch string `json:"branch,omitempty" jsonschema_description:"Branch name to tag this entry for later linkage."`
}

// LearningsLogOut is the output for the learnings_log tool.
type LearningsLogOut struct {
	OK      bool               `json:"ok"`
	Action  string             `json:"action"`
	Path    string             `json:"path"`
	Exists  bool               `json:"exists"`
	Changed bool               `json:"changed"`
	Content string             `json:"content,omitempty"`
	Next    string             `json:"next"`
	Stats   *LearningsStatsOut `json:"stats,omitempty"`
}

// LearningsStatsOut is the aggregated result of the "stats" action: entry
// counts by inferred category and skill, the top recurring mined lessons,
// and a recent-failure count. Never an error — an empty or missing log
// simply comes back with zero counts.
type LearningsStatsOut struct {
	TotalEntries   int              `json:"totalEntries"`
	ByCategory     map[string]int   `json:"byCategory"`
	BySkill        map[string]int   `json:"bySkill"`
	TopPatterns    []PatternSummary `json:"topPatterns"`
	RecentFailures int              `json:"recentFailures"`
	LastUpdated    string           `json:"lastUpdated"`
}

// PatternSummary describes one recurring "Rule: ..." lesson mined from
// entries in the learnings log.
type PatternSummary struct {
	Pattern  string `json:"pattern"`
	Count    int    `json:"count"`
	LastSeen string `json:"lastSeen"`
}

// learningsLog is the core logic, separated from the handler for testability.
// root is always the MAIN worktree root (resolved by the registered handler)
// so that entries written from a feature worktree still land in the one
// persistent log — a feature worktree's local .sdlc-v2/learnings/ is never
// git-tracked and is lost the moment the worktree is removed.
func learningsLog(root string, in LearningsLogIn) (LearningsLogOut, error) {
	rel := paths.DataDir + "/learnings/log.md"
	path := learningsLogPath(root)

	switch in.Action {
	case "append":
		return learningsAppend(path, rel, in.Entry, in.RunID, in.Branch)
	case "read":
		return learningsRead(path, rel, in.TailLines)
	case "remove":
		return learningsRemove(path, rel, in.Indices)
	case "stats":
		return learningsStats(path, rel)
	default:
		return LearningsLogOut{}, unknownActionError("learnings_log action", in.Action,
			"; must be one of: append, read, remove, stats",
			"set action to one of: \"append\", \"read\", \"remove\", \"stats\"")
	}
}

func learningsAppend(path, rel, entry, runID, branch string) (LearningsLogOut, error) {
	entry = strings.TrimSpace(entry)
	if entry == "" {
		return LearningsLogOut{}, &mcpserver.DomainError{
			Msg:        "entry must not be empty",
			Suggestion: "Provide a non-empty markdown block as the entry.",
		}
	}
	if strings.Contains(entry, "\n\n") {
		return LearningsLogOut{}, &mcpserver.DomainError{
			Msg:        "entry must not contain a blank line (\"\\n\\n\"); blank lines delimit entries in the log",
			Suggestion: "Use single newlines within an entry. Split multi-paragraph content into separate append calls, or join paragraphs with a single newline.",
		}
	}

	if runID != "" {
		entry = fmt.Sprintf("<!-- sdlc:run=%s branch=%s -->\n%s", runID, branch, entry)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return LearningsLogOut{}, &mcpserver.InfraError{
			Msg:   fmt.Sprintf("create learnings directory: %s", err.Error()),
			Cause: err,
		}
	}

	existing, err := os.ReadFile(path)
	exists := err == nil
	if err != nil && !os.IsNotExist(err) {
		return LearningsLogOut{}, &mcpserver.InfraError{
			Msg:   fmt.Sprintf("read learnings log: %s", err.Error()),
			Cause: err,
		}
	}

	var out strings.Builder
	if exists {
		out.Write(existing)
		if !strings.HasSuffix(string(existing), "\n") {
			out.WriteString("\n")
		}
		out.WriteString("\n")
	} else {
		out.WriteString(learningsLogHeader)
		out.WriteString("\n")
	}
	out.WriteString(entry)
	out.WriteString("\n")

	if err := os.WriteFile(path, []byte(out.String()), 0o644); err != nil {
		return LearningsLogOut{}, &mcpserver.InfraError{
			Msg:   fmt.Sprintf("write learnings log: %s", err.Error()),
			Cause: err,
		}
	}

	return LearningsLogOut{
		OK:      true,
		Action:  "append",
		Path:    rel,
		Exists:  true,
		Changed: true,
		Next:    "Call action=\"read\" to see the full log.",
	}, nil
}

func learningsRead(path, rel string, tailLines int) (LearningsLogOut, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return LearningsLogOut{OK: true, Action: "read", Path: rel, Exists: false}, nil
		}
		return LearningsLogOut{}, &mcpserver.InfraError{
			Msg:   fmt.Sprintf("read learnings log: %s", err.Error()),
			Cause: err,
		}
	}

	content := string(data)
	if tailLines > 0 {
		lines := strings.Split(strings.TrimSuffix(content, "\n"), "\n")
		if len(lines) > tailLines {
			lines = lines[len(lines)-tailLines:]
		}
		content = strings.Join(lines, "\n")
	}

	return LearningsLogOut{
		OK:      true,
		Action:  "read",
		Path:    rel,
		Exists:  true,
		Content: content,
	}, nil
}

func learningsRemove(path, rel string, indices []int) (LearningsLogOut, error) {
	if len(indices) == 0 {
		return LearningsLogOut{}, &mcpserver.DomainError{
			Msg:        "indices must not be empty for action \"remove\"",
			Suggestion: "Pass a non-empty indices array with 1-indexed entry numbers to remove.",
		}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return LearningsLogOut{}, &mcpserver.DomainError{
				Msg:        "learnings log does not exist; nothing to remove",
				Suggestion: "Call action=\"read\" first to confirm the log exists before attempting removal.",
			}
		}
		return LearningsLogOut{}, &mcpserver.InfraError{
			Msg:   fmt.Sprintf("read learnings log: %s", err.Error()),
			Cause: err,
		}
	}

	// Entries are blocks separated by a blank line ("\n\n"); the first block
	// is the header and is not a removable entry.
	blocks := strings.Split(string(data), "\n\n")
	header := blocks[0]
	entries := blocks[1:]
	if len(entries) == 0 {
		return LearningsLogOut{}, &mcpserver.DomainError{
			Msg:        "learnings log has no entries to remove",
			Suggestion: "Call action=\"read\" to verify the log has entries before attempting removal.",
		}
	}

	remove := make(map[int]bool, len(indices))
	for _, idx := range indices {
		if idx < 1 || idx > len(entries) {
			return LearningsLogOut{}, &mcpserver.DomainError{
				Msg:        fmt.Sprintf("index %d out of range; log has %d entries", idx, len(entries)),
				Suggestion: "Call action=\"read\" to see current entries, then retry with valid 1-indexed entry numbers.",
			}
		}
		remove[idx] = true
	}

	kept := make([]string, 0, len(entries))
	removed := make([]string, 0, len(indices))
	for i, entry := range entries {
		if remove[i+1] {
			removed = append(removed, strings.TrimRight(entry, "\n"))
			continue
		}
		kept = append(kept, strings.TrimRight(entry, "\n"))
	}

	var out strings.Builder
	out.WriteString(strings.TrimRight(header, "\n"))
	out.WriteString("\n")
	if len(kept) > 0 {
		out.WriteString("\n")
		out.WriteString(strings.Join(kept, "\n\n"))
		out.WriteString("\n")
	}

	if err := os.WriteFile(path, []byte(out.String()), 0o644); err != nil {
		return LearningsLogOut{}, &mcpserver.InfraError{
			Msg:   fmt.Sprintf("write learnings log: %s", err.Error()),
			Cause: err,
		}
	}

	return LearningsLogOut{
		OK:      true,
		Action:  "remove",
		Path:    rel,
		Exists:  true,
		Changed: true,
		Content: strings.Join(removed, "\n\n"),
		Next:    "Call action=\"read\" to verify the updated log contents.",
	}, nil
}

// learningsRecentWindow bounds how many of the most-recently appended
// entries are considered when computing RecentFailures.
const learningsRecentWindow = 20

// learningsTopPatternsLimit caps how many distinct mined patterns "stats"
// returns.
const learningsTopPatternsLimit = 10

var (
	// learningsRunTagRe matches the "<!-- sdlc:run=X branch=Y -->" comment
	// learningsAppend prepends to a tagged entry, capturing the branch name.
	learningsRunTagRe = regexp.MustCompile(`^<!--\s*sdlc:run=\S+\s+branch=(\S*)\s*-->`)
	// learningsSkillHeadingRe matches a "## <date> — <skill>: <title>" entry
	// heading and captures the skill segment.
	learningsSkillHeadingRe = regexp.MustCompile(`(?m)^##\s.*—\s*([A-Za-z][A-Za-z0-9_-]*)\s*:`)
	// learningsRuleRe captures the actionable "Rule: ..." sentence some
	// entries end with — the only structured "lesson" convention already in
	// use across real log entries.
	learningsRuleRe = regexp.MustCompile(`(?i)Rule:\s*(.+)$`)
	// learningsDateRe extracts the first ISO date found in an entry, used as
	// a best-effort "last seen" timestamp for a mined pattern.
	learningsDateRe = regexp.MustCompile(`\d{4}-\d{2}-\d{2}`)
)

// learningsPatternAgg accumulates occurrences of one mined "Rule: ..."
// pattern, keyed case-insensitively.
type learningsPatternAgg struct {
	display  string
	count    int
	lastSeen string
}

// learningsEntryCategory infers a category from an entry's run-tag branch
// prefix (e.g. "feat/x" -> "feat", "fix/y" -> "fix"), matching this repo's
// conventional-commit-style branch naming. Entries with no run tag, or an
// untagged/empty branch, fall back to "uncategorized".
func learningsEntryCategory(entry string) string {
	m := learningsRunTagRe.FindStringSubmatch(entry)
	if m == nil || m[1] == "" {
		return "uncategorized"
	}
	branch := m[1]
	if i := strings.Index(branch, "/"); i > 0 {
		return branch[:i]
	}
	return branch
}

// learningsEntrySkill infers the authoring skill from a
// "## <date> — <skill>: <title>" heading, when the entry has one. Entries
// without such a heading fall back to "unspecified".
func learningsEntrySkill(entry string) string {
	m := learningsSkillHeadingRe.FindStringSubmatch(entry)
	if m == nil {
		return "unspecified"
	}
	return strings.ToLower(m[1])
}

// learningsEntryPattern extracts the trailing "Rule: ..." sentence from an
// entry, if present, along with the first ISO date found anywhere in the
// entry (used as a best-effort last-seen marker). Returns empty strings when
// the entry has no "Rule:" clause.
func learningsEntryPattern(entry string) (pattern, date string) {
	m := learningsRuleRe.FindStringSubmatch(entry)
	if m == nil {
		return "", ""
	}
	pattern = strings.TrimSpace(m[1])
	if r := []rune(pattern); len(r) > 240 {
		pattern = strings.TrimSpace(string(r[:240])) + "..."
	}
	date = learningsDateRe.FindString(entry)
	return pattern, date
}

// learningsStats implements the "stats" action: aggregated entry counts by
// inferred category/skill, top recurring mined patterns, and a
// recent-failure count. Never errors on a missing or empty log.
func learningsStats(path, rel string) (LearningsLogOut, error) {
	stats := LearningsStatsOut{
		ByCategory:  map[string]int{},
		BySkill:     map[string]int{},
		TopPatterns: []PatternSummary{},
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return LearningsLogOut{OK: true, Action: "stats", Path: rel, Exists: false, Stats: &stats}, nil
		}
		return LearningsLogOut{}, &mcpserver.InfraError{
			Msg:   fmt.Sprintf("read learnings log: %s", err.Error()),
			Cause: err,
		}
	}

	// Entries are blocks separated by a blank line ("\n\n"); the first block
	// is the header and is not a real entry.
	blocks := strings.Split(string(data), "\n\n")
	var entries []string
	if len(blocks) > 1 {
		entries = blocks[1:]
	}

	recentStart := 0
	if len(entries) > learningsRecentWindow {
		recentStart = len(entries) - learningsRecentWindow
	}

	patternOrder := make([]string, 0, len(entries))
	patternAggs := map[string]*learningsPatternAgg{}

	for i, raw := range entries {
		entry := strings.TrimSpace(raw)
		if entry == "" {
			continue
		}
		stats.TotalEntries++

		category := learningsEntryCategory(entry)
		stats.ByCategory[category]++
		stats.BySkill[learningsEntrySkill(entry)]++

		if i >= recentStart && category == "fix" {
			stats.RecentFailures++
		}

		if pattern, date := learningsEntryPattern(entry); pattern != "" {
			key := strings.ToLower(pattern)
			agg, ok := patternAggs[key]
			if !ok {
				agg = &learningsPatternAgg{display: pattern}
				patternAggs[key] = agg
				patternOrder = append(patternOrder, key)
			}
			agg.count++
			if date > agg.lastSeen {
				agg.lastSeen = date
			}
		}
	}

	for _, key := range patternOrder {
		agg := patternAggs[key]
		stats.TopPatterns = append(stats.TopPatterns, PatternSummary{
			Pattern:  agg.display,
			Count:    agg.count,
			LastSeen: agg.lastSeen,
		})
	}
	sort.SliceStable(stats.TopPatterns, func(i, j int) bool {
		if stats.TopPatterns[i].Count != stats.TopPatterns[j].Count {
			return stats.TopPatterns[i].Count > stats.TopPatterns[j].Count
		}
		return stats.TopPatterns[i].LastSeen > stats.TopPatterns[j].LastSeen
	})
	if len(stats.TopPatterns) > learningsTopPatternsLimit {
		stats.TopPatterns = stats.TopPatterns[:learningsTopPatternsLimit]
	}

	if info, statErr := os.Stat(path); statErr == nil {
		stats.LastUpdated = info.ModTime().UTC().Format(time.RFC3339)
	}

	return LearningsLogOut{OK: true, Action: "stats", Path: rel, Exists: true, Stats: &stats}, nil
}

// ---------------------------------------------------------------------------
// Registration
// ---------------------------------------------------------------------------

// RegisterLearningsTools registers the learnings_log tool on the server.
func RegisterLearningsTools(s *mcpserver.Server) {
	mcpserver.Register(s, "learnings_log",
		"Appends to, reads, removes, or summarizes entries in "+paths.DataDir+"/learnings/log.md. Always resolves the MAIN git worktree root first (worktree.MainRoot, falling back to cwd) — a feature worktree's own copy of this file is never git-tracked and is lost when that worktree is removed, so every skill must go through this tool instead of Read/Edit-ing the file directly at the current worktree's path. action=\"append\" (entry: markdown block, no leading/trailing blank line, must not contain a blank line — that is the entry delimiter; optional runId and branch tag the entry for later linkage to an execution run — the end-of-run report counts entries matching a given runId) adds it as a new entry separated by one blank line, creating the file with its standard header on first use. action=\"read\" (optional tailLines) returns the current content, or exists=false when nothing has been logged yet. action=\"remove\" (indices: 1-indexed list of entry numbers, header excluded) deletes the specified entries, echoes the removed content in the response, and rewrites the file. action=\"stats\" (no additional input) returns aggregated counts in the response's \"stats\" field: totalEntries; byCategory, inferred from each entry's optional run-tag branch prefix (e.g. \"feat/x\"/\"fix/y\" -> \"feat\"/\"fix\", matching this repo's branch convention — entries with no run tag count as \"uncategorized\"); bySkill, parsed from a \"## <date> — <skill>: <title>\" entry heading when present (else \"unspecified\"); topPatterns, the most-repeated trailing \"Rule: ...\" lessons mined from entry text (deduplicated case-insensitively, sorted by count then recency, capped at 10); and recentFailures, how many of the most recent 20 entries were tagged with a \"fix\" category. Never errors on a missing or empty log — every count simply comes back zero.",
		mcpserver.Annotations{
			Title:      "Append to learnings log",
			ReadOnly:   true,
			Idempotent: true,
			OpenWorld:  false,
		},
		func(ctx mcpserver.Ctx, in LearningsLogIn) (LearningsLogOut, error) {
			root, err := worktree.MainRoot()
			if err != nil {
				root, err = os.Getwd()
				if err != nil {
					return LearningsLogOut{}, &mcpserver.InfraError{
						Msg:   fmt.Sprintf("resolve project root: %s", err.Error()),
						Cause: err,
					}
				}
			}
			return learningsLog(root, in)
		},
	)
}
