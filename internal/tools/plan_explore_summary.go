package tools

import (
	"strings"

	"github.com/rnagrodzki/sdlc-plugin/internal/state"
)

// plan_explore_summary.go builds a short summary of the plan explorers of
// one run, read from the run's evidence store (plan_evidence.go). Two readers
// share this one builder so their copies have the same shape: the dashboard
// plan collector and the ship cleanup step that stores the summary under
// ship state data.planExploreSummary.

const (
	// exploreWriterPrefix marks an explorer writer ID, e.g. explore-auth-flow.
	// Other writers (main, lane-*, lens-*, reviewer-*, gate-*) are not explorers.
	exploreWriterPrefix = "explore-"

	// exploreSummaryTop is the number of findings kept in
	// ExploreSummaryEntry.Top. Total always holds the full count.
	exploreSummaryTop = 5
)

// ExploreSummaryEntry is one plan explorer: name, status, finding count, first findings.
type ExploreSummaryEntry struct {
	Name   string               `json:"name"`   // writer id without "explore-"
	Status string               `json:"status"` // "running" | "done" | "unreadable"
	Total  int                  `json:"total"`
	Top    []ExploreSummaryItem `json:"top"` // first exploreSummaryTop items, never null
}

// ExploreSummaryItem is one finding of an explorer. It has no body: the body
// stays in the evidence file.
type ExploreSummaryItem struct {
	Summary string `json:"summary"`
	Ref     string `json:"ref"`
}

// planExploreSummary returns one entry for each explore-* writer in the
// evidence store of runID, sorted by name. A missing evidence folder gives an
// empty, non-nil list. A writer file that is not valid JSON gives an entry
// with status "unreadable" and no findings. An OS read error other than
// not-exist is returned as the InfraError from evidenceListWriters.
//
// root is the main worktree root. runID must come from a plan state file
// name: planExploreSummary does not validate it before it joins the path.
func planExploreSummary(root, runID string) ([]ExploreSummaryEntry, error) {
	writers, err := evidenceListWriters(state.EvidenceDir(root, runID), runID)
	if err != nil {
		return nil, err
	}
	entries := []ExploreSummaryEntry{}
	for _, w := range writers {
		if !strings.HasPrefix(w.id, exploreWriterPrefix) {
			continue
		}
		entry := ExploreSummaryEntry{
			Name: strings.TrimPrefix(w.id, exploreWriterPrefix),
			Top:  []ExploreSummaryItem{},
		}
		switch {
		case w.unreadable:
			entry.Status = evidenceStatusUnreadable
		case w.file.Status == evidenceStatusDone:
			entry.Status = evidenceStatusDone
		default:
			entry.Status = evidenceStatusRunning
		}
		if !w.unreadable {
			entry.Total = len(w.file.Items)
			for _, it := range w.file.Items {
				if len(entry.Top) == exploreSummaryTop {
					break
				}
				entry.Top = append(entry.Top, ExploreSummaryItem{Summary: it.Summary, Ref: it.Ref})
			}
		}
		entries = append(entries, entry)
	}
	return entries, nil
}
