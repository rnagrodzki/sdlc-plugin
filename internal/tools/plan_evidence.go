package tools

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/rnagrodzki/sdlc-plugin/internal/fsx"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/state"
)

// plan_evidence.go implements plan_support's evidence_record,
// evidence_digest and evidence_get actions: a per-run evidence store under
// <root>/.sdlc-v2/runs/<runId>.evidence/, one <writerId>.json file per
// writer (explorer, lane, lens, reviewer, main session) plus the main
// session's brief.md. The store lets /sdlc:plan resume after a context
// compaction without re-running its research.
//
// Path safety: runId is validated only by state.LoadRun and writerId only by
// writerIDRe (plan.go). Both checks run before any path join, so no caller
// can read or write outside <runId>.evidence/.

const (
	evidenceMaxWriters     = 32
	evidenceMaxEntries     = 200
	evidenceMaxBytes       = 65536
	evidenceMaxSummary     = 200
	evidenceMaxRef         = 500
	evidenceDefaultTimeout = 1800
	evidenceMinTimeout     = 60
	evidenceMaxTimeout     = 86400
	evidenceIndexMaxRows   = 200

	evidenceMainWriter = "main"
	evidenceBriefFile  = "brief.md"
	evidenceGuardrails = "guardrails.md"

	evidenceStatusRunning    = "running"
	evidenceStatusDone       = "done"
	evidenceStatusUnreadable = "unreadable"

	// Markers for empty render:"raw" fields. A raw field renders verbatim,
	// so an empty string would print as a blank bullet, not "(none)".
	evidenceNoWriters  = "(no writers recorded)"
	evidenceNoItems    = "(no items recorded)"
	evidenceNoneFound  = "(no items found)"
	evidenceNoneInline = "(none)"
)

// evidenceItemIDRe validates an item id (EvidenceItem.ID and each ids entry).
var evidenceItemIDRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// evidenceNow is the evidence store's clock; tests replace it to control
// updatedAt and the stalled-writer age.
var evidenceNow = time.Now

// evidenceWriterFile is the on-disk shape of <runId>.evidence/<writerId>.json.
type evidenceWriterFile struct {
	WriterID  string         `json:"writerId"`
	Status    string         `json:"status"`
	UpdatedAt string         `json:"updatedAt"`
	Items     []EvidenceItem `json:"items"`
}

// evidenceWriter is one writer file as read from disk. unreadable is true
// when the file exists but is not valid JSON; file is then the zero value.
type evidenceWriter struct {
	id         string
	path       string
	file       evidenceWriterFile
	unreadable bool
}

// ---------------------------------------------------------------------------
// Shared validation and errors
// ---------------------------------------------------------------------------

func evidenceDomainErr(msg, suggestion string) error {
	return &mcpserver.DomainError{Msg: msg, Suggestion: suggestion}
}

// evidenceInfraErr builds the InfraError for an OS read or write failure.
// op is "read" or "write".
func evidenceInfraErr(op, path, runID string, cause error) error {
	return &mcpserver.InfraError{
		Msg:        fmt.Sprintf("evidence %s failed: %s", op, path),
		Suggestion: fmt.Sprintf("make sure %s.evidence/ is a writable directory, then retry the call", runID),
		Cause:      cause,
	}
}

func evidenceRequireRunID(action, runID string) error {
	if runID == "" {
		return evidenceDomainErr("runId is required for "+action,
			"pass runId from plan_prepare's runId output")
	}
	return nil
}

// evidenceLoadRun resolves runID to its evidence directory. state.LoadRun
// is the only runID check (it rejects any path-traversal form before
// touching the filesystem); a rejected or missing run is "not found". A
// valid run whose state file cannot be read or decoded is an InfraError, so a
// corrupt file is not misreported as a missing run.
func evidenceLoadRun(mainRoot, runID string) (*state.State, string, error) {
	st, err := state.LoadRun(mainRoot, runID)
	if err != nil && !errors.Is(err, state.ErrInvalidRunID) {
		return nil, "", &mcpserver.InfraError{
			Msg:        fmt.Sprintf("plan run %s state read failed", runID),
			Suggestion: "the plan state file is unreadable or corrupt: fix its permissions or delete it, then call plan_prepare again",
			Cause:      err,
		}
	}
	if err != nil || st == nil {
		return nil, "", evidenceDomainErr(fmt.Sprintf("plan run %s not found", runID),
			"call plan_prepare({resume:true, resolveTemplate:true}) and use its runId")
	}
	return st, state.EvidenceDir(mainRoot, runID), nil
}

func evidenceCheckWriterID(field, v string) error {
	if !writerIDRe.MatchString(v) {
		return evidenceDomainErr(fmt.Sprintf("%s %q is invalid", field, v),
			"use letters, digits, '.', '_' or '-' (max 64), e.g. lane-static-structural-r1")
	}
	return nil
}

func evidenceCheckWriterList(field string, list []string) error {
	if len(list) > evidenceMaxWriters {
		return evidenceDomainErr(fmt.Sprintf("%s has %d entries, max %d", field, len(list), evidenceMaxWriters),
			"split the request into calls of at most 32 writers")
	}
	for i, v := range list {
		if err := evidenceCheckWriterID(fmt.Sprintf("%s[%d]", field, i), v); err != nil {
			return err
		}
	}
	return nil
}

func evidenceCheckEntryCount(field string, n int) error {
	if n > evidenceMaxEntries {
		return evidenceDomainErr(fmt.Sprintf("%s has %d entries, max %d", field, n, evidenceMaxEntries),
			"split the request into calls of at most 200 entries")
	}
	return nil
}

func evidenceCheckItemID(field, v string) error {
	if !evidenceItemIDRe.MatchString(v) {
		return evidenceDomainErr(fmt.Sprintf("%s %q is invalid", field, v),
			"use letters, digits, '.', '_' or '-' (max 128), e.g. F-auth-1")
	}
	return nil
}

func evidenceIsOneLine(s string, max int) bool {
	return utf8.RuneCountInString(s) <= max && !strings.ContainsAny(s, "\r\n")
}

// ---------------------------------------------------------------------------
// Writer file I/O
// ---------------------------------------------------------------------------

// evidenceReadWriter reads one writer file. exists is false when the file
// does not exist. A file that is not valid JSON is returned with
// unreadable=true and no error; any other OS error is an InfraError.
func evidenceReadWriter(path, writerID, runID string) (w evidenceWriter, exists bool, err error) {
	w = evidenceWriter{id: writerID, path: path}
	raw, rerr := os.ReadFile(path)
	if rerr != nil {
		if errors.Is(rerr, fs.ErrNotExist) {
			return w, false, nil
		}
		return w, false, evidenceInfraErr("read", path, runID, rerr)
	}
	if jerr := json.Unmarshal(raw, &w.file); jerr != nil {
		w.file = evidenceWriterFile{}
		w.unreadable = true
	}
	return w, true, nil
}

// evidenceListWriters returns every writer file in dir, sorted by writer
// ID. A missing dir is an empty store. Only *.json files whose stem is a
// valid writer ID count as writers (brief.md and guardrails.md share dir).
func evidenceListWriters(dir, runID string) ([]evidenceWriter, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, evidenceInfraErr("read", dir, runID, err)
	}
	var writers []evidenceWriter
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		id := strings.TrimSuffix(name, ".json")
		if !writerIDRe.MatchString(id) {
			continue
		}
		w, exists, rerr := evidenceReadWriter(filepath.Join(dir, name), id, runID)
		if rerr != nil {
			return nil, rerr
		}
		if exists {
			writers = append(writers, w)
		}
	}
	sort.Slice(writers, func(i, j int) bool { return writers[i].id < writers[j].id })
	return writers, nil
}

// evidenceUpsert replaces each incoming item whose id already exists in
// place (the whole item, so an omitted body clears the old one) and appends
// new ids in input order.
func evidenceUpsert(existing, incoming []EvidenceItem) []EvidenceItem {
	out := make([]EvidenceItem, 0, len(existing)+len(incoming))
	out = append(out, existing...)
	pos := make(map[string]int, len(out))
	for i, it := range out {
		pos[it.ID] = i
	}
	for _, it := range incoming {
		if i, ok := pos[it.ID]; ok {
			out[i] = it
			continue
		}
		pos[it.ID] = len(out)
		out = append(out, it)
	}
	return out
}

// evidenceMarshalSize returns the byte size fsx.AtomicWriteJSON writes for v.
func evidenceMarshalSize(v any) (int, error) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return 0, err
	}
	return len(data) + 1, nil
}

// ---------------------------------------------------------------------------
// Rendering helpers
// ---------------------------------------------------------------------------

// evidenceCell escapes a markdown table cell: "|" becomes "\|", line breaks
// become spaces, and an empty value becomes "—".
func evidenceCell(s string) string {
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "|", `\|`)
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return s
}

func evidenceList(list []string) string {
	if len(list) == 0 {
		return evidenceNoneInline
	}
	return strings.Join(list, ", ")
}

// evidenceFileIfExists returns path when it is a regular file and "" when
// it does not exist (or is a directory). Any other stat failure is an
// InfraError, so an unreadable file is not reported as "never recorded".
func evidenceFileIfExists(path, runID string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", nil
		}
		return "", evidenceInfraErr("read", path, runID, err)
	}
	if info.IsDir() {
		return "", nil
	}
	return path, nil
}

// evidenceCheckpoint converts st.Data["checkpoint"] (a decoded JSON map)
// into a PlanCheckpoint. A missing value returns (nil, nil). A value that
// does not decode into a PlanCheckpoint is an InfraError: treating it as
// "never checkpointed" would silently resume from step 0.
func evidenceCheckpoint(st *state.State) (*PlanCheckpoint, error) {
	raw, ok := st.Data["checkpoint"]
	if !ok || raw == nil {
		return nil, nil
	}
	data, err := json.Marshal(raw)
	if err == nil {
		var cp PlanCheckpoint
		if err = json.Unmarshal(data, &cp); err == nil {
			return &cp, nil
		}
	}
	return nil, &mcpserver.InfraError{
		Msg:        fmt.Sprintf("plan run %s has a malformed checkpoint in %s", state.RunID(st), st.Path),
		Suggestion: `call plan_mark({marker:"checkpoint", data:{step:"<current step>"}}) to replace it, then retry evidence_digest`,
		Cause:      err,
	}
}

// evidenceTable renders a markdown table from pre-rendered row lines.
func evidenceTable(headers []string, rows []string) string {
	sep := make([]string, len(headers))
	for i := range sep {
		sep[i] = "---"
	}
	return "| " + strings.Join(headers, " | ") + " |\n|" + strings.Join(sep, "|") + "|\n" + strings.Join(rows, "\n")
}

// evidenceWriterTally is the per-writer classification evidence_digest
// reports: table rows plus the done/running/stalled/unreadable breakdown.
type evidenceWriterTally struct {
	rows       []string
	stalled    []string
	unreadable []string
	present    map[string]evidenceWriter
	nDone      int
	nRunning   int
	nItems     int
}

// evidenceClassifyWriters classifies each writer file. An expected writer is
// stalled when its file is unreadable (it can never report done) or when it
// is still running and its updatedAt is older than timeout seconds (or does
// not parse).
func evidenceClassifyWriters(writers []evidenceWriter, expectedSet map[string]bool, now time.Time, timeout int) evidenceWriterTally {
	t := evidenceWriterTally{present: make(map[string]evidenceWriter, len(writers))}
	for _, w := range writers {
		t.present[w.id] = w
		isStalled := false
		if w.unreadable {
			t.unreadable = append(t.unreadable, w.id)
			isStalled = expectedSet[w.id]
			t.rows = append(t.rows, fmt.Sprintf("| %s | %s | — | — | %s |", evidenceCell(w.id), evidenceStatusUnreadable, evidenceYesNo(isStalled)))
		} else {
			switch w.file.Status {
			case evidenceStatusDone:
				t.nDone++
			case evidenceStatusRunning:
				t.nRunning++
				if expectedSet[w.id] {
					updated, perr := time.Parse(time.RFC3339, w.file.UpdatedAt)
					isStalled = perr != nil || now.Sub(updated) > time.Duration(timeout)*time.Second
				}
			}
			t.nItems += len(w.file.Items)
			t.rows = append(t.rows, fmt.Sprintf("| %s | %s | %d | %s | %s |",
				evidenceCell(w.id), evidenceCell(w.file.Status), len(w.file.Items), evidenceCell(w.file.UpdatedAt), evidenceYesNo(isStalled)))
		}
		if isStalled {
			t.stalled = append(t.stalled, w.id)
		}
	}
	return t
}

// ---------------------------------------------------------------------------
// Action: evidence_record
// ---------------------------------------------------------------------------

func evidenceRecord(mainRoot string, in PlanSupportIn) (PlanSupportOut, error) {
	if err := evidenceRequireRunID("evidence_record", in.RunID); err != nil {
		return PlanSupportOut{}, err
	}
	if in.Status != "" && in.Status != evidenceStatusRunning && in.Status != evidenceStatusDone {
		return PlanSupportOut{}, evidenceDomainErr(fmt.Sprintf("status %q is not valid", in.Status),
			"pass running or done, or omit status to keep the stored value")
	}
	_, dir, err := evidenceLoadRun(mainRoot, in.RunID)
	if err != nil {
		return PlanSupportOut{}, err
	}
	if err := evidenceCheckWriterID("writerId", in.WriterID); err != nil {
		return PlanSupportOut{}, err
	}
	if err := evidenceCheckEntryCount("items", len(in.Items)); err != nil {
		return PlanSupportOut{}, err
	}
	for i, it := range in.Items {
		if err := evidenceCheckItemID(fmt.Sprintf("items[%d].id", i), it.ID); err != nil {
			return PlanSupportOut{}, err
		}
		if !evidenceIsOneLine(it.Summary, evidenceMaxSummary) {
			return PlanSupportOut{}, evidenceDomainErr(
				fmt.Sprintf("items[%d].summary must be one line of at most 200 characters (got %d)", i, utf8.RuneCountInString(it.Summary)),
				"shorten the summary and move detail to body")
		}
		if !evidenceIsOneLine(it.Ref, evidenceMaxRef) {
			return PlanSupportOut{}, evidenceDomainErr(
				fmt.Sprintf("items[%d].ref must be one line of at most 500 characters", i),
				"keep one path:line or URL in ref")
		}
	}
	if in.Brief != "" {
		if in.WriterID != evidenceMainWriter {
			return PlanSupportOut{}, evidenceDomainErr("brief is accepted only for writerId main",
				`record the brief with writerId "main"`)
		}
		if len(in.Brief) > evidenceMaxBytes {
			return PlanSupportOut{}, evidenceDomainErr(fmt.Sprintf("brief is %d bytes, limit %d", len(in.Brief), evidenceMaxBytes),
				"shorten the brief; keep finding detail in explorer items")
		}
	}

	path := filepath.Join(dir, in.WriterID+".json")
	stored, exists, err := evidenceReadWriter(path, in.WriterID, in.RunID)
	if err != nil {
		return PlanSupportOut{}, err
	}
	replacedUnreadable := exists && stored.unreadable

	wf := evidenceWriterFile{WriterID: in.WriterID, Status: evidenceStatusRunning, Items: []EvidenceItem{}}
	if exists && !stored.unreadable {
		if stored.file.Status != "" {
			wf.Status = stored.file.Status
		}
		if stored.file.Items != nil {
			wf.Items = stored.file.Items
		}
	}
	if in.Status != "" {
		wf.Status = in.Status
	}
	wf.Items = evidenceUpsert(wf.Items, in.Items)
	wf.UpdatedAt = evidenceNow().UTC().Format(time.RFC3339)

	size, err := evidenceMarshalSize(wf)
	if err != nil {
		return PlanSupportOut{}, evidenceInfraErr("write", path, in.RunID, err)
	}
	if size > evidenceMaxBytes {
		return PlanSupportOut{}, evidenceDomainErr(
			fmt.Sprintf("writer %s evidence would be %d bytes, limit %d", in.WriterID, size, evidenceMaxBytes),
			"split the item across more ids or shorten the body")
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return PlanSupportOut{}, evidenceInfraErr("write", dir, in.RunID, err)
	}
	if err := fsx.AtomicWriteJSON(path, wf); err != nil {
		return PlanSupportOut{}, evidenceInfraErr("write", path, in.RunID, err)
	}

	briefPath := ""
	if in.Brief != "" {
		briefPath = filepath.Join(dir, evidenceBriefFile)
		if err := fsx.AtomicWriteBytes(briefPath, []byte(in.Brief)); err != nil {
			return PlanSupportOut{}, evidenceInfraErr("write", briefPath, in.RunID, err)
		}
	}

	summary := fmt.Sprintf("Recorded %d item(s) for writer %s (status %s); the writer file now holds %d item(s), %d bytes.",
		len(in.Items), in.WriterID, wf.Status, len(wf.Items), size)
	if replacedUnreadable {
		summary += fmt.Sprintf(" Replaced the unreadable writer file %s.", path)
	}
	if briefPath != "" {
		summary += fmt.Sprintf(" Stored the brief at %s.", briefPath)
	}

	return PlanSupportOut{
		Summary: summary,
		Next:    fmt.Sprintf("Stored %d items for %s (status %s). Continue your step.", len(in.Items), in.WriterID, wf.Status),
		Record: &EvidenceRecordOut{
			WriterID:  in.WriterID,
			Status:    wf.Status,
			ItemCount: len(wf.Items),
			FileBytes: size,
			BriefPath: briefPath,
		},
	}, nil
}

// ---------------------------------------------------------------------------
// Action: evidence_digest
// ---------------------------------------------------------------------------

const evidenceDigestNextBase = "If the sdlc:plan skill instructions are not in context, invoke the sdlc:plan skill first; its Session recovery rule selects the resume path. " +
	"If lanes and lensReviewers are not in context, call plan_prepare({resume:true, resolveTemplate:true}) first. " +
	"Re-read the plan file, then continue at step %s (iteration %d). Fetch bodies with evidence_get only when the step needs them."

// evidenceNoExpectedWritersNext is the statusOnly next step when neither
// the input nor the run checkpoint names any expected writer.
const evidenceNoExpectedWritersNext = "No expected writers: expectedWriters is empty and the run checkpoint lists none. Pass expectedWriters with the writer IDs you dispatched, then poll again."

func evidenceDigest(mainRoot string, in PlanSupportIn) (PlanSupportOut, error) {
	if err := evidenceRequireRunID("evidence_digest", in.RunID); err != nil {
		return PlanSupportOut{}, err
	}
	st, dir, err := evidenceLoadRun(mainRoot, in.RunID)
	if err != nil {
		return PlanSupportOut{}, err
	}
	if err := evidenceCheckWriterList("expectedWriters", in.ExpectedWriters); err != nil {
		return PlanSupportOut{}, err
	}
	timeout := in.TimeoutSeconds
	if timeout != 0 && (timeout < evidenceMinTimeout || timeout > evidenceMaxTimeout) {
		return PlanSupportOut{}, evidenceDomainErr(fmt.Sprintf("timeoutSeconds %d is out of range 60-86400", timeout),
			"omit timeoutSeconds for the 1800-second default")
	}
	if timeout == 0 {
		timeout = evidenceDefaultTimeout
	}

	checkpoint, err := evidenceCheckpoint(st)
	if err != nil {
		return PlanSupportOut{}, err
	}
	expected := in.ExpectedWriters
	if len(expected) == 0 && checkpoint != nil {
		expected = checkpoint.ExpectedWriters
	}
	expectedSet := make(map[string]bool, len(expected))
	for _, w := range expected {
		expectedSet[w] = true
	}

	writers, err := evidenceListWriters(dir, in.RunID)
	if err != nil {
		return PlanSupportOut{}, err
	}

	tally := evidenceClassifyWriters(writers, expectedSet, evidenceNow(), timeout)
	present, stalled := tally.present, tally.stalled
	var missing []string
	for _, w := range expected {
		if _, ok := present[w]; !ok {
			missing = append(missing, w)
		}
	}

	table := evidenceNoWriters
	if len(tally.rows) > 0 {
		table = evidenceTable([]string{"writer", "status", "items", "updatedAt", "stalled"}, tally.rows)
	}

	style, styleErr := loadPlanStyle(mainRoot)
	step, iteration := "0", 0
	if checkpoint != nil {
		if checkpoint.Step != "" {
			step = checkpoint.Step
		}
		iteration = checkpoint.Iteration
	}

	unreadablePart := ""
	if len(tally.unreadable) > 0 {
		unreadablePart = fmt.Sprintf(", %d unreadable", len(tally.unreadable))
	}
	summary := fmt.Sprintf("%s — step %s, iteration %d; writers: %d done, %d running, %d missing, %d stalled%s; %d items; %d custom instructions.",
		in.RunID, step, iteration, tally.nDone, tally.nRunning, len(missing), len(stalled), unreadablePart, tally.nItems, len(style.Instructions))
	if styleErr != "" {
		summary += fmt.Sprintf(" Warning: %s — custom plan instructions could not be loaded; fix local.toml.", styleErr)
	}

	out := PlanSupportOut{
		Summary: summary,
		Writers: &EvidenceWritersOut{
			Table:             table,
			MissingWriters:    missing,
			StalledWriters:    stalled,
			UnreadableWriters: tally.unreadable,
		},
	}
	lagging := len(missing) > 0 || len(stalled) > 0

	if in.StatusOnly {
		switch {
		case lagging:
			out.Next = fmt.Sprintf("Missing: %s. Stalled: %s. Wait one more poll cycle; if a writer is still listed, force-progress past it (SKILL.md POLL step).",
				evidenceList(missing), evidenceList(stalled))
		case len(expected) == 0:
			// evidenceAllDone is vacuously true for an empty list; do not
			// report "all done" when nothing was expected.
			out.Next = evidenceNoExpectedWritersNext
		case evidenceAllDone(expected, present):
			out.Next = "All expected writers are done. Fetch their results with evidence_get writerIds."
		default:
			out.Next = "Poll again in about 60 seconds."
		}
		return out, nil
	}

	out.Next = fmt.Sprintf(evidenceDigestNextBase, step, iteration)
	if lagging {
		out.Next += fmt.Sprintf(" Missing: %s. Stalled: %s. Wait one poll cycle (evidence_digest statusOnly); if a writer is still listed, re-dispatch it or force-progress past it.",
			evidenceList(missing), evidenceList(stalled))
	}

	userPrompt := ""
	if intent, ok := st.Data["creationIntent"].(map[string]any); ok {
		userPrompt, _ = intent["userPrompt"].(string)
	}
	planFilePath, _ := st.Data["planFilePath"].(string)
	guardrailsFile, err := evidenceFileIfExists(filepath.Join(dir, evidenceGuardrails), in.RunID)
	if err != nil {
		return PlanSupportOut{}, err
	}
	briefPath, err := evidenceFileIfExists(filepath.Join(dir, evidenceBriefFile), in.RunID)
	if err != nil {
		return PlanSupportOut{}, err
	}

	out.Digest = &EvidenceDigestOut{
		RunID:          in.RunID,
		PlanFilePath:   planFilePath,
		UserPrompt:     userPrompt,
		GuardrailsFile: guardrailsFile,
		BriefPath:      briefPath,
		Instructions:   style.Instructions,
		Checkpoint:     checkpoint,
		Index:          evidenceIndex(writers),
	}
	return out, nil
}

func evidenceYesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// evidenceAllDone reports whether every expected writer has a readable file
// with status done. An empty expected set is vacuously done.
func evidenceAllDone(expected []string, present map[string]evidenceWriter) bool {
	for _, id := range expected {
		w, ok := present[id]
		if !ok || w.unreadable || w.file.Status != evidenceStatusDone {
			return false
		}
	}
	return true
}

// evidenceIndex renders the digest's item index: one row per item, writers
// in name order and items in file order, capped at evidenceIndexMaxRows
// rows plus a "… n more" row.
func evidenceIndex(writers []evidenceWriter) string {
	var rows []string
	total := 0
	for _, w := range writers {
		if w.unreadable {
			continue
		}
		for _, it := range w.file.Items {
			total++
			if len(rows) < evidenceIndexMaxRows {
				rows = append(rows, fmt.Sprintf("| %s | %s | %s | %s |",
					evidenceCell(it.ID), evidenceCell(w.id), evidenceCell(it.Ref), evidenceCell(it.Summary)))
			}
		}
	}
	if total == 0 {
		return evidenceNoItems
	}
	if total > evidenceIndexMaxRows {
		rows = append(rows, fmt.Sprintf("| … | — | — | %d more; use evidence_get writerIds |", total-evidenceIndexMaxRows))
	}
	return evidenceTable([]string{"id", "writer", "ref", "summary"}, rows)
}

// ---------------------------------------------------------------------------
// Action: evidence_get
// ---------------------------------------------------------------------------

func evidenceGet(mainRoot string, in PlanSupportIn) (PlanSupportOut, error) {
	if err := evidenceRequireRunID("evidence_get", in.RunID); err != nil {
		return PlanSupportOut{}, err
	}
	_, dir, err := evidenceLoadRun(mainRoot, in.RunID)
	if err != nil {
		return PlanSupportOut{}, err
	}
	if err := evidenceCheckWriterList("writerIds", in.WriterIDs); err != nil {
		return PlanSupportOut{}, err
	}
	if err := evidenceCheckEntryCount("ids", len(in.IDs)); err != nil {
		return PlanSupportOut{}, err
	}
	for i, id := range in.IDs {
		if err := evidenceCheckItemID(fmt.Sprintf("ids[%d]", i), id); err != nil {
			return PlanSupportOut{}, err
		}
	}
	if len(in.IDs) == 0 && len(in.WriterIDs) == 0 {
		return PlanSupportOut{}, evidenceDomainErr("evidence_get needs ids or writerIds",
			"pass ids from the digest index or writerIds from the writers table")
	}

	writers, err := evidenceListWriters(dir, in.RunID)
	if err != nil {
		return PlanSupportOut{}, err
	}
	byID := make(map[string]evidenceWriter, len(writers))
	for _, w := range writers {
		if !w.unreadable {
			byID[w.id] = w
		}
	}

	var blocks []string
	var notFound []string
	seen := map[string]bool{}
	add := func(writerID string, it EvidenceItem) {
		key := writerID + "\x00" + it.ID
		if seen[key] {
			return
		}
		seen[key] = true
		blocks = append(blocks, evidenceBlock(writerID, it))
	}

	// ids: every match, in writer-name order (item ids are unique only
	// within one writer).
	for _, id := range in.IDs {
		found := false
		for _, w := range writers {
			if w.unreadable {
				continue
			}
			for _, it := range w.file.Items {
				if it.ID == id {
					add(w.id, it)
					found = true
				}
			}
		}
		if !found {
			notFound = append(notFound, id)
		}
	}
	for _, wid := range in.WriterIDs {
		w, ok := byID[wid]
		if !ok {
			notFound = append(notFound, wid)
			continue
		}
		for _, it := range w.file.Items {
			add(w.id, it)
		}
	}

	evidence := evidenceNoneFound
	if len(blocks) > 0 {
		evidence = strings.Join(blocks, "\n")
	}
	next := fmt.Sprintf("Bodies returned for %d items.", len(blocks))
	if len(notFound) > 0 {
		next = fmt.Sprintf("Not found: %s. Check the digest index for valid ids.", strings.Join(notFound, ", "))
	}

	return PlanSupportOut{
		Summary: fmt.Sprintf("Returned %d item(s) for run %s; %d requested id(s) or writer(s) not found.", len(blocks), in.RunID, len(notFound)),
		Next:    next,
		Get: &EvidenceGetOut{
			Evidence: evidence,
			NotFound: notFound,
		},
	}, nil
}

// evidenceBlock renders one item for get.evidence.
func evidenceBlock(writerID string, it EvidenceItem) string {
	ref := it.Ref
	if ref == "" {
		ref = "—"
	}
	body := it.Body
	if strings.TrimSpace(body) == "" {
		body = "(no body)"
	}
	return fmt.Sprintf("### %s — %s\n- ref: %s\n- summary: %s\n\n%s\n", it.ID, writerID, ref, it.Summary, strings.TrimRight(body, "\n"))
}
