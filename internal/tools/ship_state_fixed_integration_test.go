//go:build integration

package tools

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rnagrodzki/sdlc-plugin/internal/history"
	"github.com/rnagrodzki/sdlc-plugin/internal/state"
)

// shipStateDeferredEntries reads data.deferredFindings of the ship state of
// branch under dir straight from disk.
func shipStateDeferredEntries(t *testing.T, dir, branch string) []map[string]any {
	t.Helper()
	st, err := state.Find(dir, "ship", branch)
	if err != nil || st == nil {
		t.Fatalf("state.Find = %v, %v — want the ship state", st, err)
	}
	raw, _ := st.Data["deferredFindings"].([]any)
	entries := make([]map[string]any, 0, len(raw))
	for _, r := range raw {
		m, ok := r.(map[string]any)
		if !ok {
			t.Fatalf("deferredFindings entry = %#v, want an object", r)
		}
		entries = append(entries, m)
	}
	return entries
}

// TestIntegration_ShipStateDeferThenFixedResolvesRow calls the registered
// ship_state tool for defer and then for healing_record kind fixed with the
// deferredId that defer stored on the run entry. The run entry must name the
// deferred.json row, the fixed call must resolve that row and set
// fixedInRun:true, and a repeat call must report the row as already resolved.
func TestIntegration_ShipStateDeferThenFixedResolvesRow(t *testing.T) {
	const branch = "feat/fixed-int"
	dir := shipStateIntegrationRepo(t, branch)
	if res, text := callRegisteredShipStateText(t, map[string]any{"action": "init"}); res.IsError {
		t.Fatalf("init: %s", text)
	}
	res, text := callRegisteredShipStateText(t, map[string]any{
		"action": "defer",
		"detail": map[string]any{"severity": "low", "file": "a.go", "line": 12, "title": "t"},
	})
	if res.IsError {
		t.Fatalf("defer returned a tool error:\n%s", text)
	}
	rows := readDeferredFile(t, dir)
	if len(rows) != 1 || rows[0].Status != history.StatusOpen {
		t.Fatalf("deferred.json rows = %+v, want one open row", rows)
	}
	id := rows[0].ID
	for _, want := range []string{"(id " + id + ")", "added deferred item " + id + ". Continue."} {
		if !strings.Contains(text, want) {
			t.Errorf("defer output missing %q:\n%s", want, text)
		}
	}
	entries := shipStateDeferredEntries(t, dir, branch)
	if len(entries) != 1 || entries[0]["deferredId"] != id {
		t.Fatalf("deferredFindings = %v, want one entry with deferredId %q", entries, id)
	}

	fixed := func() string {
		t.Helper()
		res, text := callRegisteredShipStateText(t, map[string]any{
			"action": "healing_record",
			"step":   "received-review",
			"detail": map[string]any{
				"kind": "fixed", "origin": "local-review", "file": "a.go", "line": 12,
				"title": "t", "severity": "low", "deferredId": id,
			},
		})
		if res.IsError {
			t.Fatalf("healing_record returned a tool error:\n%s", text)
		}
		return text
	}

	first := fixed()
	if want := "deferred item " + id + " resolved. Continue the fix pass."; !strings.Contains(first, want) {
		t.Errorf("first output missing %q:\n%s", want, first)
	}
	if rows := readDeferredFile(t, dir); len(rows) != 1 || rows[0].Status != history.StatusResolved {
		t.Errorf("deferred.json rows after the fix = %+v, want the row resolved", rows)
	}
	if entries := shipStateDeferredEntries(t, dir, branch); entries[0]["fixedInRun"] != true {
		t.Errorf("deferredFindings[0] = %v, want fixedInRun true", entries[0])
	}

	second := fixed()
	if want := "deferred item " + id + " was already resolved. Continue the fix pass."; !strings.Contains(second, want) {
		t.Errorf("repeat output missing %q:\n%s", want, second)
	}
}

// TestIntegration_ShipStateFixedUnknownDeferredID calls the registered
// healing_record with a deferredId that the run entry does not carry, and
// with one that the entry carries but deferred.json does not hold. Each call
// must return a tool error that names the id and the recovery, and must leave
// the ship state file and deferred.json as they were. A call without
// deferredId, the recovery in both suggestions, must then succeed.
func TestIntegration_ShipStateFixedUnknownDeferredID(t *testing.T) {
	tests := []struct {
		name        string
		branch      string
		emptyStore  bool
		deferredID  string
		wantInError []string
	}{
		{
			name:       "not in deferredFindings",
			branch:     "feat/fixed-int-ghost",
			deferredID: "review-deferred-ghost-9",
			wantInError: []string{
				`deferredId "review-deferred-ghost-9" is not in deferredFindings`,
				"Pass a deferredId from ship_state read deferredFindings",
				"omit detail.deferredId to record the fix without resolving a deferred row",
			},
		},
		{
			name:       "not in deferred.json",
			branch:     "feat/fixed-int-no-row",
			emptyStore: true,
			wantInError: []string{
				"is not in ",
				"Omit detail.deferredId to record the fix without resolving the row",
				"run ship_state deferred_list to check the row ids",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := shipStateIntegrationRepo(t, tt.branch)
			if res, text := callRegisteredShipStateText(t, map[string]any{"action": "init"}); res.IsError {
				t.Fatalf("init: %s", text)
			}
			if res, text := callRegisteredShipStateText(t, map[string]any{
				"action": "defer",
				"detail": map[string]any{"severity": "low", "file": "a.go", "line": 12, "title": "t"},
			}); res.IsError {
				t.Fatalf("defer: %s", text)
			}
			id := readDeferredFile(t, dir)[0].ID
			storePath := filepath.Join(dir, ".sdlc-v2", "history", "deferred.json")
			if tt.emptyStore {
				if err := os.WriteFile(storePath, []byte("[]\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			deferredID := tt.deferredID
			if deferredID == "" {
				deferredID = id
			}
			st, err := state.Find(dir, "ship", tt.branch)
			if err != nil || st == nil {
				t.Fatalf("state.Find = %v, %v — want the ship state", st, err)
			}
			stateBefore := shipErrReadFile(t, st.Path)
			storeBefore := shipErrReadFile(t, storePath)

			fixed := func(detailID string) (bool, string) {
				t.Helper()
				detail := map[string]any{
					"kind": "fixed", "origin": "local-review", "file": "a.go", "line": 12,
					"title": "t", "severity": "low",
				}
				if detailID != "" {
					detail["deferredId"] = detailID
				}
				res, text := callRegisteredShipStateText(t, map[string]any{"action": "healing_record", "detail": detail})
				return res.IsError, text
			}

			isErr, text := fixed(deferredID)
			if !isErr {
				t.Fatalf("healing_record with an unknown deferredId: want a tool error, got:\n%s", text)
			}
			for _, want := range tt.wantInError {
				if !strings.Contains(text, want) {
					t.Errorf("output missing %q:\n%s", want, text)
				}
			}
			if after := shipErrReadFile(t, st.Path); !bytes.Equal(after, stateBefore) {
				t.Error("ship state changed by a rejected call")
			}
			if after := shipErrReadFile(t, storePath); !bytes.Equal(after, storeBefore) {
				t.Error("deferred.json changed by a rejected call")
			}

			if isErr, text := fixed(""); isErr {
				t.Fatalf("healing_record without deferredId (the recovery) returned a tool error:\n%s", text)
			}
			if after := shipErrReadFile(t, storePath); !bytes.Equal(after, storeBefore) {
				t.Error("deferred.json changed by the recovery call")
			}
		})
	}
}

// TestIntegration_ShipStateFixedRejectsNonStringDeferredID calls the
// registered healing_record with a deferredId that is not a string. The tool
// must return an error with the pinned text and its recovery.
func TestIntegration_ShipStateFixedRejectsNonStringDeferredID(t *testing.T) {
	shipStateIntegrationRepo(t, "feat/fixed-int-type")
	if res, text := callRegisteredShipStateText(t, map[string]any{"action": "init"}); res.IsError {
		t.Fatalf("init: %s", text)
	}
	res, text := callRegisteredShipStateText(t, map[string]any{
		"action": "healing_record",
		"detail": map[string]any{
			"kind": "fixed", "origin": "local-review", "file": "a.go",
			"title": "t", "severity": "low", "deferredId": 5,
		},
	})
	if !res.IsError {
		t.Fatalf("healing_record with deferredId 5: want a tool error, got:\n%s", text)
	}
	for _, want := range []string{
		"healing_record: detail.deferredId must be a string",
		"pass the deferredId string from the finding's **Deferred-id:** line, or omit it",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("output missing %q:\n%s", want, text)
		}
	}
}
