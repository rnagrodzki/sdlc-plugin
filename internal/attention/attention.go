// Package attention stores open waits for a user answer as one small JSON
// file per wait, under <mainRoot>/.sdlc-v2/evidence/attention/. Hooks write
// and delete the files; the dashboard snapshot lists them.
//
// The record shape lives in this leaf package because both sides need it.
// The package must not import internal/tools: tools imports attention.
package attention

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/fsx"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
	"github.com/rnagrodzki/sdlc-plugin/internal/telemetry"
)

const (
	// KindQuestion is a wait for the answer to an AskUserQuestion call.
	KindQuestion = "question"
	// KindPermission is a wait for a tool permission decision.
	KindPermission = "permission"
	// MaxTextRunes caps Header and Text. A cut value gets a trailing "…"
	// (telemetry.TruncateRunes), so the stored value holds at most
	// MaxTextRunes+1 runes.
	MaxTextRunes = 120
)

// recordVersion is the only Record.Version that Write produces and List reads.
const recordVersion = 1

// subdir is the folder name under <DataDir>/<EvidenceSubdir>.
const subdir = "attention"

// permissionSuffix names the single permission file of a session.
const permissionSuffix = "permission"

// unsafeRe matches a character that is not safe in a file name segment. It is
// a local copy of tools.reviewBranchUnsafeRe (internal/tools/review.go).
var unsafeRe = regexp.MustCompile(`[^a-zA-Z0-9_-]`)

// Record is one open wait for a user answer.
//
// File: <mainRoot>/.sdlc-v2/evidence/attention/<sid>-<toolUseId>.json (question)
//
//	<mainRoot>/.sdlc-v2/evidence/attention/<sid>-permission.json  (permission)
//
// sid and toolUseId: every char outside [A-Za-z0-9_-] becomes '-'.
type Record struct {
	Version   int    `json:"version"`
	Kind      string `json:"kind"`
	SessionID string `json:"sessionId"`
	ToolUseID string `json:"toolUseId,omitempty"` // question only
	Branch    string `json:"branch"`
	Header    string `json:"header"`
	Text      string `json:"text"`
	AskedAt   string `json:"askedAt"` // RFC3339 UTC
}

// Dir returns the folder that holds the attention records of a project.
func Dir(mainRoot string) string {
	return filepath.Join(mainRoot, paths.DataDir, paths.EvidenceSubdir, subdir)
}

// safe replaces every character outside [A-Za-z0-9_-] in s with '-', so the
// result is a valid file name segment that cannot climb out of the folder.
func safe(s string) string { return unsafeRe.ReplaceAllString(s, "-") }

// questionFile returns the file name of the question record for one tool-use
// ID of a session: "<sid>-<toolUseId>.json" with both parts made safe.
func questionFile(sessionID, toolUseID string) string {
	return safe(sessionID) + "-" + safe(toolUseID) + ".json"
}

// permissionFile returns the file name of the permission record of a session:
// "<sid>-permission.json" with the session ID made safe.
func permissionFile(sessionID string) string {
	return safe(sessionID) + "-" + permissionSuffix + ".json"
}

// validate checks the fields that decide the file name and the kind.
func validate(r Record) error {
	if r.SessionID == "" {
		return errors.New("attention: empty session ID")
	}
	switch r.Kind {
	case KindQuestion:
		if r.ToolUseID == "" {
			return errors.New("attention: question record needs a tool-use ID")
		}
	case KindPermission:
	default:
		return fmt.Errorf("attention: unknown kind %q", r.Kind)
	}
	return nil
}

// Write stores r atomically. It redacts Header and Text (telemetry.Redact),
// then caps each at MaxTextRunes runes. It sets Version to 1. It sets AskedAt
// to the current UTC time when AskedAt is empty. A permission record has no
// tool-use ID; Write clears the field. Write returns an error for an unknown
// kind, an empty session ID, or a question with no tool-use ID.
func Write(mainRoot string, r Record) error {
	if err := validate(r); err != nil {
		return err
	}
	r.Version = recordVersion
	r.Header = telemetry.TruncateRunes(telemetry.Redact(r.Header), MaxTextRunes)
	r.Text = telemetry.TruncateRunes(telemetry.Redact(r.Text), MaxTextRunes)
	if r.AskedAt == "" {
		r.AskedAt = time.Now().UTC().Format(time.RFC3339)
	}

	name := permissionFile(r.SessionID)
	if r.Kind == KindQuestion {
		name = questionFile(r.SessionID, r.ToolUseID)
	} else {
		r.ToolUseID = ""
	}

	dir := Dir(mainRoot)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("attention: create %s: %w", dir, err)
	}
	return fsx.AtomicWriteJSON(filepath.Join(dir, name), r)
}

// remove deletes one file. A missing file is not an error.
func remove(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("attention: remove %s: %w", path, err)
	}
	return nil
}

// DeleteQuestion deletes the question record of one tool-use ID. When
// toolUseID is empty, it deletes every question record of the session.
// A missing record is not an error.
func DeleteQuestion(mainRoot, sessionID, toolUseID string) error {
	if sessionID == "" {
		return errors.New("attention: empty session ID")
	}
	if toolUseID != "" {
		return remove(filepath.Join(Dir(mainRoot), questionFile(sessionID, toolUseID)))
	}
	return deleteSessionRecords(mainRoot, sessionID, KindQuestion)
}

// DeletePermission deletes the permission record of the session. A missing
// record is not an error.
func DeletePermission(mainRoot, sessionID string) error {
	if sessionID == "" {
		return errors.New("attention: empty session ID")
	}
	return remove(filepath.Join(Dir(mainRoot), permissionFile(sessionID)))
}

// DeleteSession deletes every record of the session, of any kind. A missing
// folder is not an error.
func DeleteSession(mainRoot, sessionID string) error {
	if sessionID == "" {
		return errors.New("attention: empty session ID")
	}
	return deleteSessionRecords(mainRoot, sessionID, "")
}

// deleteSessionRecords deletes the records of one session. kind "" matches
// every kind. A file name prefix "<sid>-" can also belong to another session
// whose ID starts with "<sid>-", so a readable record must carry the exact
// session ID. A file that cannot be parsed is deleted when its name fits.
// A directory with a fitting name is not special-cased: os.Remove deletes an
// empty one and fails on a non-empty one. A remove error does not stop the
// loop; the other records are still deleted and the first error is returned.
func deleteSessionRecords(mainRoot, sessionID, kind string) error {
	dir := Dir(mainRoot)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("attention: read %s: %w", dir, err)
	}

	prefix := safe(sessionID) + "-"
	permName := permissionFile(sessionID)
	var firstErr error
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, ".json") {
			continue
		}
		fileKind := KindQuestion
		if name == permName {
			fileKind = KindPermission
		} else {
			var rec Record
			if err := fsx.ReadJSON(filepath.Join(dir, name), &rec); err == nil {
				if rec.SessionID != sessionID {
					continue
				}
				fileKind = rec.Kind
			}
		}
		if kind != "" && fileKind != kind {
			continue
		}
		if err := remove(filepath.Join(dir, name)); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// List returns the open records, oldest first. It skips a file that cannot be
// read or parsed, a file of another version, a record that fails the Write
// checks, and a record older than maxAge at now. A maxAge of zero or less
// turns the age check off. A missing folder gives an empty list and no error.
// The result is never nil.
func List(mainRoot string, now time.Time, maxAge time.Duration) ([]Record, error) {
	dir := Dir(mainRoot)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []Record{}, nil
		}
		return nil, fmt.Errorf("attention: read %s: %w", dir, err)
	}

	type dated struct {
		rec Record
		at  time.Time
	}
	var found []dated
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var rec Record
		if err := json.Unmarshal(data, &rec); err != nil {
			continue
		}
		if rec.Version != recordVersion || validate(rec) != nil {
			continue
		}
		asked, err := time.Parse(time.RFC3339, rec.AskedAt)
		if err != nil {
			continue
		}
		if maxAge > 0 && now.Sub(asked) > maxAge {
			continue
		}
		found = append(found, dated{rec: rec, at: asked})
	}

	sort.SliceStable(found, func(i, j int) bool {
		a, b := found[i], found[j]
		if !a.at.Equal(b.at) {
			return a.at.Before(b.at)
		}
		if a.rec.SessionID != b.rec.SessionID {
			return a.rec.SessionID < b.rec.SessionID
		}
		return a.rec.ToolUseID < b.rec.ToolUseID
	})
	out := make([]Record, 0, len(found))
	for _, d := range found {
		out = append(out, d.rec)
	}
	return out, nil
}
