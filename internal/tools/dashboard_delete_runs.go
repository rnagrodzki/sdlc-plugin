package tools

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/rnagrodzki/sdlc-plugin/internal/history"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
)

// DashboardRunKey names History rows: kind is the runs.jsonl skill,
// endedAt is the raw ts. branch may be "".
type DashboardRunKey struct {
	Kind    string `json:"kind"`
	Branch  string `json:"branch"`
	EndedAt string `json:"endedAt"`
}

// dashboardDeleteRunsMaxKeys is the most keys that one request can send.
const dashboardDeleteRunsMaxKeys = 50

// dashboardDeleteRunsTries is the number of times that the delete reads the
// file twice. The first try can lose to a writer, and the second try is the
// one retry.
const dashboardDeleteRunsTries = 2

// Suggestion texts of the History delete errors, one for each condition.
// The input errors that have no text here use dashboardDeleteReloadSuggestion.
const (
	dashboardDeleteRunsEmptySuggestion   = "Select rows, then try again."
	dashboardDeleteRunsTooManySuggestion = "Delete at most 50 rows at a time."
	dashboardDeleteRunsLargeSuggestion   = "Remove old rows from .sdlc-v2/history/runs.jsonl by hand until it is smaller than " + dashboardDeleteReadMaxText + ", then retry."
	dashboardDeleteRunsReadSuggestion    = "Check read permission on .sdlc-v2/history/runs.jsonl and retry."
	dashboardDeleteRunsWriteSuggestion   = "Check write permission on .sdlc-v2/history/ and free disk space, then retry."
	dashboardDeleteRunsChangedSuggestion = "Another run wrote the History file at the same time. Reload the page and try again."
)

// dashboardRunKeyText names key in a message.
func dashboardRunKeyText(key DashboardRunKey) string {
	return fmt.Sprintf("{kind %q, branch %q, endedAt %q}", key.Kind, key.Branch, key.EndedAt)
}

// dashboardRunKeysError returns the DomainError for a key list that cannot
// name History rows, or nil for a good list. It checks the list only, not
// the file: an empty list, more than dashboardDeleteRunsMaxKeys keys, a key
// with an empty kind or endedAt, and two equal keys.
func dashboardRunKeysError(keys []DashboardRunKey) error {
	domain := func(msg, suggestion string) error {
		return &mcpserver.DomainError{Msg: msg, Suggestion: suggestion}
	}
	if len(keys) == 0 {
		return domain("Select at least one row", dashboardDeleteRunsEmptySuggestion)
	}
	if len(keys) > dashboardDeleteRunsMaxKeys {
		return domain(fmt.Sprintf("The request has %d rows. The limit is %d rows", len(keys), dashboardDeleteRunsMaxKeys),
			dashboardDeleteRunsTooManySuggestion)
	}
	seen := make(map[DashboardRunKey]bool, len(keys))
	for i, key := range keys {
		switch {
		case key.Kind == "":
			return domain(fmt.Sprintf("The kind field of row %d is required", i+1), dashboardDeleteReloadSuggestion)
		case key.EndedAt == "":
			return domain(fmt.Sprintf("The endedAt field of row %d is required", i+1), dashboardDeleteReloadSuggestion)
		case seen[key]:
			return domain(fmt.Sprintf("The key %s appears more than once", dashboardRunKeyText(key)), dashboardDeleteReloadSuggestion)
		}
		seen[key] = true
	}
	return nil
}

// dashboardDropRuns removes from data every line whose skill, branch and raw
// ts equal a key. It works on raw lines, each with its own line end, so a
// line that stays keeps its bytes: a malformed line, a blank line, a line
// with fields that RunRecord does not know, and a last line with no line end.
// It returns the kept bytes and the count of removed lines. unmatched is the
// first key, in key order, that matched no line, or nil when every key did.
func dashboardDropRuns(data []byte, keys []DashboardRunKey) (kept []byte, removed int, unmatched *DashboardRunKey) {
	hits := make(map[DashboardRunKey]int, len(keys))
	for _, key := range keys {
		hits[key] = 0
	}

	var out bytes.Buffer
	out.Grow(len(data))
	for _, line := range bytes.SplitAfter(data, []byte("\n")) {
		var row struct {
			Skill  string `json:"skill"`
			Branch string `json:"branch"`
			TS     string `json:"ts"`
		}
		if err := json.Unmarshal(line, &row); err == nil {
			key := DashboardRunKey{Kind: row.Skill, Branch: row.Branch, EndedAt: row.TS}
			if _, wanted := hits[key]; wanted {
				hits[key]++
				removed++
				continue
			}
		}
		out.Write(line)
	}

	for _, key := range keys {
		if hits[key] == 0 {
			key := key
			return out.Bytes(), removed, &key
		}
	}
	return out.Bytes(), removed, nil
}

// DashboardDeleteRuns removes every runs.jsonl row that matches a key.
// It reads the file with dashboardDeleteReadFile, drops the rows, reads
// the file again, and writes with dashboardDeleteWriteBytes only when the
// two reads match. A mismatch gives one full retry.
//
// A row matches a key when its skill equals Kind, its branch equals Branch
// and its raw ts equals EndedAt. Each key removes every row that matches it,
// and no other row. The rest of the file keeps its bytes. The message names
// the count of removed rows. The file is rewritten through a temp file and a
// rename, so the new file has mode 0600 from os.CreateTemp: the same as the
// deferred and learnings deletes.
//
// No lock guards the file. A writer that appends between the second read and
// the rename can still lose its row. This risk is accepted.
//
// Errors: an empty list, more than 50 keys, a key with an empty kind or
// endedAt, two equal keys, a key that matches no row and a missing file give
// a DomainError, and the file stays unchanged. A file of more than
// dashboardDeleteReadMax bytes gives a DataError. A failed Stat, read or
// write gives an InfraError. Two tries in which the second read differs from
// the first also give an InfraError.
func DashboardDeleteRuns(root string, keys []DashboardRunKey) (DashboardDeleteOut, error) {
	if err := dashboardRunKeysError(keys); err != nil {
		return DashboardDeleteOut{}, err
	}

	path := history.NewFileWriter(paths.HistoryDir(root)).RunsPath()
	read := func() (data []byte, missing bool, err error) {
		return dashboardReadCapped(path, "the History file runs.jsonl",
			dashboardDeleteRunsLargeSuggestion, dashboardDeleteRunsReadSuggestion)
	}

	for try := 0; try < dashboardDeleteRunsTries; try++ {
		first, missing, err := read()
		if err != nil {
			return DashboardDeleteOut{}, err
		}
		if missing {
			return DashboardDeleteOut{}, &mcpserver.DomainError{
				Msg:        "The History file runs.jsonl does not exist",
				Suggestion: dashboardDeleteReloadSuggestion,
			}
		}

		kept, removed, unmatched := dashboardDropRuns(first, keys)
		if unmatched != nil {
			return DashboardDeleteOut{}, &mcpserver.DomainError{
				Msg:        fmt.Sprintf("No History row matches the key %s. Another session may have deleted it", dashboardRunKeyText(*unmatched)),
				Suggestion: dashboardDeleteReloadSuggestion,
			}
		}

		// A file that went away, or a second read that differs, means a writer
		// touched the file after the first read. Read and drop again.
		second, missing, err := read()
		if err != nil {
			return DashboardDeleteOut{}, err
		}
		if missing || !bytes.Equal(first, second) {
			continue
		}

		if err := dashboardDeleteWriteBytes(path, kept); err != nil {
			return DashboardDeleteOut{}, &mcpserver.InfraError{
				Msg:        fmt.Sprintf("Could not write runs.jsonl: %s", err),
				Suggestion: dashboardDeleteRunsWriteSuggestion,
				Cause:      err,
			}
		}
		noun := "rows"
		if removed == 1 {
			noun = "row"
		}
		return DashboardDeleteOut{Deleted: true, Message: fmt.Sprintf("Deleted %d History %s.", removed, noun)}, nil
	}

	return DashboardDeleteOut{}, &mcpserver.InfraError{
		Msg:        "The History file runs.jsonl changed during each of two tries, so nothing was deleted",
		Suggestion: dashboardDeleteRunsChangedSuggestion,
	}
}
