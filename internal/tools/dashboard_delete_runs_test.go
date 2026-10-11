package tools

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/rnagrodzki/sdlc-plugin/internal/history"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
)

// Rows that the History delete tests write to runs.jsonl. Each row ends with
// a line end. The unknown field in runsDelPlanFeatX proves that a rewrite keeps
// fields that RunRecord does not know.
const (
	runsDelPlanFeatX = `{"ts":"2026-10-01T10:00:00Z","skill":"plan","branch":"feat/x","outcome":"success","duration_ms":1000,"future_field":{"a":[1,2]}}` + "\n"
	runsDelShipFeatX = `{"ts":"2026-10-01T11:00:00Z","skill":"ship","branch":"feat/x","outcome":"success","duration_ms":2000}` + "\n"
	runsDelPlanFeatY = `{"ts":"2026-10-01T10:00:00Z","skill":"plan","branch":"feat/y","outcome":"success","duration_ms":1500}` + "\n"
	runsDelPlanLater = `{"ts":"2026-10-02T10:00:00Z","skill":"plan","branch":"feat/x","outcome":"failure","duration_ms":500}` + "\n"
	runsDelNoBranch  = `{"ts":"2026-10-03T10:00:00Z","skill":"commit","branch":"","outcome":"success","duration_ms":10}` + "\n"
	runsDelMalformed = `{"ts":"2026-10-01T10:00:00Z","skill":"plan","branch":"feat/x"` + "\n"
)

// Keys of the rows above.
var (
	runsDelKeyPlanFeatX = DashboardRunKey{Kind: "plan", Branch: "feat/x", EndedAt: "2026-10-01T10:00:00Z"}
	runsDelKeyShipFeatX = DashboardRunKey{Kind: "ship", Branch: "feat/x", EndedAt: "2026-10-01T11:00:00Z"}
	runsDelKeyNoBranch  = DashboardRunKey{Kind: "commit", Branch: "", EndedAt: "2026-10-03T10:00:00Z"}
)

// runsDelPath returns the runs.jsonl path of root.
func runsDelPath(root string) string {
	return history.NewFileWriter(paths.HistoryDir(root)).RunsPath()
}

// runsDelFile writes runs.jsonl of root with the given content. It returns the
// path and the bytes written.
func runsDelFile(t *testing.T, root, content string) (string, []byte) {
	t.Helper()
	dashDelHistoryDir(t, root)
	path := runsDelPath(root)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path, []byte(content)
}

// runsDelWant checks that runs.jsonl at path holds exactly want.
func runsDelWant(t *testing.T, path string, want []byte) {
	t.Helper()
	if got := dashDelReadFile(t, path); !bytes.Equal(got, want) {
		t.Errorf("runs.jsonl =\n%q\nwant\n%q", got, want)
	}
}

// runsDelAppend appends line to the file at path, as another run would.
func runsDelAppend(t *testing.T, path, line string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(line); err != nil {
		t.Fatal(err)
	}
}

// runsDelNoWrite replaces the write seam with one that fails the test when
// it is called.
func runsDelNoWrite(t *testing.T) {
	t.Helper()
	dashDelSeam(t, &dashboardDeleteWriteBytes, func(p string, _ []byte) error {
		t.Errorf("the delete wrote %s, want no write", p)
		return nil
	})
}

// TestDashboardDeleteRuns_RemovesOnlyTheMatchedRow removes the row of the key and keeps every other line byte for byte.
func TestDashboardDeleteRuns_RemovesOnlyTheMatchedRow(t *testing.T) {
	root := t.TempDir()
	// Same skill and ts with another branch, same skill and branch with another
	// ts, a malformed line with the same text, a blank line, and the row that
	// carries a field unknown to RunRecord.
	path, _ := runsDelFile(t, root,
		runsDelPlanFeatX+runsDelShipFeatX+runsDelPlanFeatY+runsDelPlanLater+runsDelMalformed+"\n"+runsDelNoBranch)

	out, err := DashboardDeleteRuns(root, []DashboardRunKey{runsDelKeyShipFeatX})
	if err != nil {
		t.Fatalf("DashboardDeleteRuns: %v", err)
	}
	want := DashboardDeleteOut{Deleted: true, AlreadyGone: false, Message: "Deleted 1 History row."}
	if out != want {
		t.Errorf("out = %+v, want %+v", out, want)
	}
	runsDelWant(t, path, []byte(runsDelPlanFeatX+runsDelPlanFeatY+runsDelPlanLater+runsDelMalformed+"\n"+runsDelNoBranch))
}

// TestDashboardDeleteRuns_KeepsUnknownFieldsAndMalformedLines removes the plan row and keeps the malformed line and the unknown fields of the other rows.
func TestDashboardDeleteRuns_KeepsUnknownFieldsAndMalformedLines(t *testing.T) {
	root := t.TempDir()
	// The unknown-field row stays here, and the malformed twin of the key stays.
	rest := runsDelPlanFeatX + runsDelMalformed
	path, _ := runsDelFile(t, root, runsDelPlanFeatY+rest)

	out, err := DashboardDeleteRuns(root, []DashboardRunKey{{Kind: "plan", Branch: "feat/y", EndedAt: "2026-10-01T10:00:00Z"}})
	if err != nil {
		t.Fatalf("DashboardDeleteRuns: %v", err)
	}
	if !out.Deleted {
		t.Errorf("out = %+v, want Deleted", out)
	}
	runsDelWant(t, path, []byte(rest))
}

// TestDashboardDeleteRuns_RemovesEveryMatchingLine removes every line of a key and counts the lines in the message.
func TestDashboardDeleteRuns_RemovesEveryMatchingLine(t *testing.T) {
	root := t.TempDir()
	path, _ := runsDelFile(t, root, runsDelPlanFeatX+runsDelShipFeatX+runsDelPlanFeatX+runsDelPlanLater)

	out, err := DashboardDeleteRuns(root, []DashboardRunKey{runsDelKeyPlanFeatX})
	if err != nil {
		t.Fatalf("DashboardDeleteRuns: %v", err)
	}
	if !out.Deleted || out.Message != "Deleted 2 History rows." {
		t.Errorf("out = %+v, want Deleted with the message %q", out, "Deleted 2 History rows.")
	}
	runsDelWant(t, path, []byte(runsDelShipFeatX+runsDelPlanLater))
}

// TestDashboardDeleteRuns_SeveralKeys removes the rows of several keys in one write and counts all of them.
func TestDashboardDeleteRuns_SeveralKeys(t *testing.T) {
	root := t.TempDir()
	path, _ := runsDelFile(t, root, runsDelPlanFeatX+runsDelShipFeatX+runsDelPlanFeatY+runsDelNoBranch)
	writes := 0
	dashDelSeam(t, &dashboardDeleteWriteBytes, func(p string, data []byte) error {
		writes++
		return os.WriteFile(p, data, 0o644)
	})

	out, err := DashboardDeleteRuns(root, []DashboardRunKey{runsDelKeyPlanFeatX, runsDelKeyNoBranch})
	if err != nil {
		t.Fatalf("DashboardDeleteRuns: %v", err)
	}
	if out.Message != "Deleted 2 History rows." {
		t.Errorf("message = %q", out.Message)
	}
	if writes != 1 {
		t.Errorf("writes = %d, want 1", writes)
	}
	runsDelWant(t, path, []byte(runsDelShipFeatX+runsDelPlanFeatY))
}

// TestDashboardDeleteRuns_EmptyBranchKeyMatchesRowWithoutBranch matches an empty branch key to a row whose branch is empty or absent.
func TestDashboardDeleteRuns_EmptyBranchKeyMatchesRowWithoutBranch(t *testing.T) {
	root := t.TempDir()
	absent := `{"ts":"2026-10-03T10:00:00Z","skill":"commit","outcome":"success"}` + "\n"
	path, _ := runsDelFile(t, root, runsDelPlanFeatX+runsDelNoBranch+absent)

	out, err := DashboardDeleteRuns(root, []DashboardRunKey{runsDelKeyNoBranch})
	if err != nil {
		t.Fatalf("DashboardDeleteRuns: %v", err)
	}
	if out.Message != "Deleted 2 History rows." {
		t.Errorf("message = %q, want both rows without a branch removed", out.Message)
	}
	runsDelWant(t, path, []byte(runsDelPlanFeatX))
}

// TestDashboardDeleteRuns_LastLineWithoutLineEnd removes the last line when it has no line end and keeps a last line that has none.
func TestDashboardDeleteRuns_LastLineWithoutLineEnd(t *testing.T) {
	t.Run("remove the last line", func(t *testing.T) {
		root := t.TempDir()
		path, _ := runsDelFile(t, root, runsDelPlanFeatX+strings.TrimSuffix(runsDelShipFeatX, "\n"))

		if _, err := DashboardDeleteRuns(root, []DashboardRunKey{runsDelKeyShipFeatX}); err != nil {
			t.Fatalf("DashboardDeleteRuns: %v", err)
		}
		runsDelWant(t, path, []byte(runsDelPlanFeatX))
	})
	t.Run("keep the last line", func(t *testing.T) {
		root := t.TempDir()
		last := strings.TrimSuffix(runsDelShipFeatX, "\n")
		path, _ := runsDelFile(t, root, runsDelPlanFeatX+last)

		if _, err := DashboardDeleteRuns(root, []DashboardRunKey{runsDelKeyPlanFeatX}); err != nil {
			t.Fatalf("DashboardDeleteRuns: %v", err)
		}
		runsDelWant(t, path, []byte(last))
	})
}

// TestDashboardDeleteRuns_CRLFLinesKeepTheirEnds matches a row that ends in CRLF and keeps the CRLF of the other rows.
func TestDashboardDeleteRuns_CRLFLinesKeepTheirEnds(t *testing.T) {
	root := t.TempDir()
	crlf := func(s string) string { return strings.TrimSuffix(s, "\n") + "\r\n" }
	path, _ := runsDelFile(t, root, crlf(runsDelPlanFeatX)+crlf(runsDelShipFeatX))

	if _, err := DashboardDeleteRuns(root, []DashboardRunKey{runsDelKeyPlanFeatX}); err != nil {
		t.Fatalf("DashboardDeleteRuns: %v", err)
	}
	runsDelWant(t, path, []byte(crlf(runsDelShipFeatX)))
}

// TestDashboardDeleteRuns_AllRowsRemovedLeavesEmptyFile leaves an empty file when every row is removed.
func TestDashboardDeleteRuns_AllRowsRemovedLeavesEmptyFile(t *testing.T) {
	root := t.TempDir()
	path, _ := runsDelFile(t, root, runsDelPlanFeatX+runsDelShipFeatX)

	out, err := DashboardDeleteRuns(root, []DashboardRunKey{runsDelKeyPlanFeatX, runsDelKeyShipFeatX})
	if err != nil {
		t.Fatalf("DashboardDeleteRuns: %v", err)
	}
	if !out.Deleted {
		t.Errorf("out = %+v, want Deleted", out)
	}
	runsDelWant(t, path, []byte{})
}

// TestDashboardDeleteRuns_WritesThroughSeamWithRebuiltBytes checks that the write seam receives the runs.jsonl path and the kept bytes.
func TestDashboardDeleteRuns_WritesThroughSeamWithRebuiltBytes(t *testing.T) {
	root := t.TempDir()
	path, before := runsDelFile(t, root, runsDelPlanFeatX+runsDelShipFeatX)
	var gotPath string
	var gotData []byte
	dashDelSeam(t, &dashboardDeleteWriteBytes, func(p string, data []byte) error {
		gotPath, gotData = p, data
		return nil
	})

	if _, err := DashboardDeleteRuns(root, []DashboardRunKey{runsDelKeyPlanFeatX}); err != nil {
		t.Fatalf("DashboardDeleteRuns: %v", err)
	}
	if gotPath != path {
		t.Errorf("write path = %q, want %q", gotPath, path)
	}
	if string(gotData) != runsDelShipFeatX {
		t.Errorf("write data = %q, want %q", gotData, runsDelShipFeatX)
	}
	runsDelWant(t, path, before) // the seam wrote nothing
}

// TestDashboardDeleteRuns_DeletesEachSnapshotRow deletes the rows that the snapshot lists with the keys that it sends.
func TestDashboardDeleteRuns_DeletesEachSnapshotRow(t *testing.T) {
	root := t.TempDir()
	path, _ := runsDelFile(t, root, runsDelPlanFeatX+runsDelShipFeatX+runsDelMalformed+runsDelNoBranch)

	rows := dashboardRecentRuns(root)
	if len(rows) != 3 {
		t.Fatalf("snapshot rows = %d, want 3 (%+v)", len(rows), rows)
	}
	keys := make([]DashboardRunKey, 0, len(rows))
	for _, row := range rows {
		keys = append(keys, DashboardRunKey{Kind: row.Kind, Branch: row.Branch, EndedAt: row.EndedAt})
	}
	out, err := DashboardDeleteRuns(root, keys)
	if err != nil {
		t.Fatalf("DashboardDeleteRuns: %v", err)
	}
	if out.Message != "Deleted 3 History rows." {
		t.Errorf("message = %q", out.Message)
	}
	if left := dashboardRecentRuns(root); len(left) != 0 {
		t.Errorf("rows left in the snapshot: %+v", left)
	}
	runsDelWant(t, path, []byte(runsDelMalformed))
}

// TestDashboardDeleteRuns_EmptyKeysRefused refuses a missing or empty key list with a DomainError and does not touch the file.
func TestDashboardDeleteRuns_EmptyKeysRefused(t *testing.T) {
	for name, keys := range map[string][]DashboardRunKey{"nil": nil, "empty": {}} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			path, before := runsDelFile(t, root, runsDelPlanFeatX)
			runsDelNoWrite(t)

			out, err := DashboardDeleteRuns(root, keys)
			dashDelWantErr(t, err, "domain", "Select rows, then try again.")
			if err.Error() != "Select at least one row" {
				t.Errorf("message = %q", err.Error())
			}
			if out != (DashboardDeleteOut{}) {
				t.Errorf("out = %+v, want zero value", out)
			}
			runsDelWant(t, path, before)
		})
	}
}

// TestDashboardDeleteRuns_MoreThanFiftyKeysRefused refuses 51 keys with a DomainError and accepts exactly 50 keys.
func TestDashboardDeleteRuns_MoreThanFiftyKeysRefused(t *testing.T) {
	var content strings.Builder
	var keys []DashboardRunKey
	for i := 0; i < 51; i++ {
		ts := fmt.Sprintf("2026-10-01T10:%02d:00Z", i)
		fmt.Fprintf(&content, `{"ts":%q,"skill":"plan","branch":"feat/x"}`+"\n", ts)
		keys = append(keys, DashboardRunKey{Kind: "plan", Branch: "feat/x", EndedAt: ts})
	}

	t.Run("51 keys", func(t *testing.T) {
		root := t.TempDir()
		path, before := runsDelFile(t, root, content.String())
		runsDelNoWrite(t)

		out, err := DashboardDeleteRuns(root, keys)
		dashDelWantErr(t, err, "domain", "Delete at most 50 rows at a time.")
		if out != (DashboardDeleteOut{}) {
			t.Errorf("out = %+v, want zero value", out)
		}
		runsDelWant(t, path, before)
	})
	t.Run("50 keys", func(t *testing.T) {
		root := t.TempDir()
		path, _ := runsDelFile(t, root, content.String())

		out, err := DashboardDeleteRuns(root, keys[:50])
		if err != nil {
			t.Fatalf("DashboardDeleteRuns: %v", err)
		}
		if out.Message != "Deleted 50 History rows." {
			t.Errorf("message = %q", out.Message)
		}
		runsDelWant(t, path, []byte(`{"ts":"2026-10-01T10:50:00Z","skill":"plan","branch":"feat/x"}`+"\n"))
	})
}

// TestDashboardDeleteRuns_EmptyKindOrEndedAtRefused refuses a key with an empty kind or endedAt with a DomainError.
func TestDashboardDeleteRuns_EmptyKindOrEndedAtRefused(t *testing.T) {
	cases := []struct {
		name    string
		keys    []DashboardRunKey
		message string
	}{
		{"empty kind", []DashboardRunKey{runsDelKeyPlanFeatX, {Kind: "", Branch: "feat/x", EndedAt: "2026-10-01T11:00:00Z"}}, "The kind field of row 2 is required"},
		{"empty endedAt", []DashboardRunKey{{Kind: "ship", Branch: "feat/x", EndedAt: ""}}, "The endedAt field of row 1 is required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			path, before := runsDelFile(t, root, runsDelPlanFeatX+runsDelShipFeatX)
			runsDelNoWrite(t)

			out, err := DashboardDeleteRuns(root, tc.keys)
			dashDelWantErr(t, err, "domain", "Reload the page and try again.")
			if err.Error() != tc.message {
				t.Errorf("message = %q, want %q", err.Error(), tc.message)
			}
			if out != (DashboardDeleteOut{}) {
				t.Errorf("out = %+v, want zero value", out)
			}
			runsDelWant(t, path, before)
		})
	}
}

// TestDashboardDeleteRuns_DuplicateKeyRefused refuses two equal keys with a DomainError that names the key.
func TestDashboardDeleteRuns_DuplicateKeyRefused(t *testing.T) {
	root := t.TempDir()
	path, before := runsDelFile(t, root, runsDelPlanFeatX+runsDelShipFeatX)
	runsDelNoWrite(t)

	out, err := DashboardDeleteRuns(root, []DashboardRunKey{runsDelKeyPlanFeatX, runsDelKeyShipFeatX, runsDelKeyPlanFeatX})
	dashDelWantErr(t, err, "domain", "Reload the page and try again.")
	want := `The key {kind "plan", branch "feat/x", endedAt "2026-10-01T10:00:00Z"} appears more than once`
	if err.Error() != want {
		t.Errorf("message = %q, want %q", err.Error(), want)
	}
	if out != (DashboardDeleteOut{}) {
		t.Errorf("out = %+v, want zero value", out)
	}
	runsDelWant(t, path, before)
}

// TestDashboardDeleteRuns_UnknownKeyRejectsTheWholeRequest refuses a request with one unknown key, names the key and keeps the file unchanged.
func TestDashboardDeleteRuns_UnknownKeyRejectsTheWholeRequest(t *testing.T) {
	cases := []struct {
		name string
		key  DashboardRunKey
	}{
		{"unknown kind", DashboardRunKey{Kind: "review", Branch: "feat/x", EndedAt: "2026-10-01T10:00:00Z"}},
		{"unknown branch", DashboardRunKey{Kind: "plan", Branch: "feat/z", EndedAt: "2026-10-01T10:00:00Z"}},
		{"unknown ts", DashboardRunKey{Kind: "plan", Branch: "feat/x", EndedAt: "2026-10-01T10:00:01Z"}},
		{"ts in another format", DashboardRunKey{Kind: "plan", Branch: "feat/x", EndedAt: "2026-10-01T10:00:00.000Z"}},
		{"malformed line is not a row", DashboardRunKey{Kind: "plan", Branch: "feat/x", EndedAt: "2026-10-09T10:00:00Z"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			path, before := runsDelFile(t, root, runsDelPlanFeatX+runsDelShipFeatX+`{"ts":"2026-10-09T10:00:00Z","skill":"plan","branch":"feat/x"`+"\n")
			runsDelNoWrite(t)

			// The first key is valid, so a partial delete would change the file.
			out, err := DashboardDeleteRuns(root, []DashboardRunKey{runsDelKeyPlanFeatX, tc.key})
			dashDelWantErr(t, err, "domain", "Reload the page and try again.")
			wantMsg := fmt.Sprintf("No History row matches the key %s. Another session may have deleted it", dashboardRunKeyText(tc.key))
			if err.Error() != wantMsg {
				t.Errorf("message = %q, want %q", err.Error(), wantMsg)
			}
			if out != (DashboardDeleteOut{}) {
				t.Errorf("out = %+v, want zero value", out)
			}
			runsDelWant(t, path, before)
		})
	}
}

// TestDashboardDeleteRuns_MissingFileIsDomainError gives a DomainError when runs.jsonl does not exist and does not create it.
func TestDashboardDeleteRuns_MissingFileIsDomainError(t *testing.T) {
	root := t.TempDir()
	runsDelNoWrite(t)

	out, err := DashboardDeleteRuns(root, []DashboardRunKey{runsDelKeyPlanFeatX})
	dashDelWantErr(t, err, "domain", "Reload the page and try again.")
	if err.Error() != "The History file runs.jsonl does not exist" {
		t.Errorf("message = %q", err.Error())
	}
	if out != (DashboardDeleteOut{}) {
		t.Errorf("out = %+v, want zero value", out)
	}
	if _, statErr := os.Stat(runsDelPath(root)); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("the delete created runs.jsonl: %v", statErr)
	}
}

// TestDashboardDeleteRuns_TooLargeIsDataError gives a DataError for a file over the read limit and keeps the file bytes.
func TestDashboardDeleteRuns_TooLargeIsDataError(t *testing.T) {
	root := t.TempDir()
	// A matching row, then padding past the limit: without the size check the
	// row would be found and removed.
	big := append([]byte(runsDelPlanFeatX), bytes.Repeat([]byte(" "), dashboardDeleteReadMax)...)
	path, _ := runsDelFile(t, root, "")
	if err := os.WriteFile(path, big, 0o644); err != nil {
		t.Fatal(err)
	}
	runsDelNoWrite(t)

	out, err := DashboardDeleteRuns(root, []DashboardRunKey{runsDelKeyPlanFeatX})
	dashDelWantErr(t, err, "data", "Remove old rows from .sdlc-v2/history/runs.jsonl by hand until it is smaller than 8 MiB, then retry.")
	want := fmt.Sprintf("The History file runs.jsonl is %d bytes, more than the 8388608 bytes (8 MiB) that a dashboard delete reads", len(big))
	if err.Error() != want {
		t.Errorf("message = %q, want %q", err.Error(), want)
	}
	if out != (DashboardDeleteOut{}) {
		t.Errorf("out = %+v, want zero value", out)
	}
	runsDelWant(t, path, big)
}

// TestDashboardDeleteRuns_SizeAtLimitIsRead reads a file whose size equals the read limit.
func TestDashboardDeleteRuns_SizeAtLimitIsRead(t *testing.T) {
	root := t.TempDir()
	path, _ := runsDelFile(t, root, "")
	exact := append([]byte(runsDelPlanFeatX), bytes.Repeat([]byte(" "), dashboardDeleteReadMax-len(runsDelPlanFeatX))...)
	if err := os.WriteFile(path, exact, 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := DashboardDeleteRuns(root, []DashboardRunKey{runsDelKeyPlanFeatX})
	if err != nil {
		t.Fatalf("DashboardDeleteRuns: %v", err)
	}
	if !out.Deleted {
		t.Errorf("out = %+v, want Deleted", out)
	}
}

// TestDashboardDeleteRuns_ReadFailureIsInfra gives an InfraError when the file cannot be read, from the first read and from the second read.
func TestDashboardDeleteRuns_ReadFailureIsInfra(t *testing.T) {
	const suggestion = "Check read permission on .sdlc-v2/history/runs.jsonl and retry."

	t.Run("path is a folder", func(t *testing.T) {
		root := t.TempDir()
		// Stat succeeds and ReadFile fails.
		if err := os.MkdirAll(runsDelPath(root), 0o755); err != nil {
			t.Fatal(err)
		}
		runsDelNoWrite(t)

		out, err := DashboardDeleteRuns(root, []DashboardRunKey{runsDelKeyPlanFeatX})
		dashDelWantErr(t, err, "infra", suggestion)
		if out != (DashboardDeleteOut{}) {
			t.Errorf("out = %+v, want zero value", out)
		}
	})
	for _, failOn := range []int{1, 2} {
		t.Run(fmt.Sprintf("seam fails on read %d", failOn), func(t *testing.T) {
			root := t.TempDir()
			path, before := runsDelFile(t, root, runsDelPlanFeatX+runsDelShipFeatX)
			injected := errors.New("injected read failure")
			calls := 0
			dashDelSeam(t, &dashboardDeleteReadFile, func(p string) ([]byte, error) {
				calls++
				if calls == failOn {
					return nil, injected
				}
				return os.ReadFile(p)
			})
			runsDelNoWrite(t)

			out, err := DashboardDeleteRuns(root, []DashboardRunKey{runsDelKeyPlanFeatX})
			dashDelWantErr(t, err, "infra", suggestion)
			if !errors.Is(err, injected) {
				t.Errorf("error does not wrap the injected failure: %v", err)
			}
			if out != (DashboardDeleteOut{}) {
				t.Errorf("out = %+v, want zero value", out)
			}
			runsDelWant(t, path, before)
		})
	}
}

// TestDashboardDeleteRuns_WriteFailureIsInfra gives an InfraError when the write fails and keeps the file bytes.
func TestDashboardDeleteRuns_WriteFailureIsInfra(t *testing.T) {
	root := t.TempDir()
	path, before := runsDelFile(t, root, runsDelPlanFeatX+runsDelShipFeatX)
	injected := errors.New("injected write failure")
	dashDelSeam(t, &dashboardDeleteWriteBytes, func(string, []byte) error { return injected })

	out, err := DashboardDeleteRuns(root, []DashboardRunKey{runsDelKeyPlanFeatX})
	dashDelWantErr(t, err, "infra", "Check write permission on .sdlc-v2/history/ and free disk space, then retry.")
	if !errors.Is(err, injected) {
		t.Errorf("error does not wrap the injected failure: %v", err)
	}
	if out != (DashboardDeleteOut{}) {
		t.Errorf("out = %+v, want zero value", out)
	}
	runsDelWant(t, path, before)
}

// TestDashboardDeleteRuns_RetriesOnceWhenTheSecondReadDiffers retries the whole delete once when another run appends a line between the two reads, and keeps the appended line.
func TestDashboardDeleteRuns_RetriesOnceWhenTheSecondReadDiffers(t *testing.T) {
	root := t.TempDir()
	path, _ := runsDelFile(t, root, runsDelPlanFeatX+runsDelShipFeatX)
	calls := 0
	dashDelSeam(t, &dashboardDeleteReadFile, func(p string) ([]byte, error) {
		calls++
		if calls == 2 {
			// The other run appends after the first read and before the second.
			runsDelAppend(t, p, runsDelPlanLater)
		}
		return os.ReadFile(p)
	})

	out, err := DashboardDeleteRuns(root, []DashboardRunKey{runsDelKeyPlanFeatX})
	if err != nil {
		t.Fatalf("DashboardDeleteRuns: %v", err)
	}
	if out.Message != "Deleted 1 History row." {
		t.Errorf("message = %q", out.Message)
	}
	if calls != 4 {
		t.Errorf("reads = %d, want 4 (two tries of two reads)", calls)
	}
	runsDelWant(t, path, []byte(runsDelShipFeatX+runsDelPlanLater))
}

// TestDashboardDeleteRuns_SecondReadReturnsNewBytes gives a retry when the second read seam returns new bytes, and writes the bytes of the retry.
func TestDashboardDeleteRuns_SecondReadReturnsNewBytes(t *testing.T) {
	root := t.TempDir()
	runsDelFile(t, root, runsDelPlanFeatX+runsDelShipFeatX)
	reads := [][]byte{
		[]byte(runsDelPlanFeatX + runsDelShipFeatX),
		[]byte(runsDelPlanFeatX + runsDelShipFeatX + runsDelPlanLater), // differs
		[]byte(runsDelPlanFeatX + runsDelShipFeatX + runsDelPlanLater),
		[]byte(runsDelPlanFeatX + runsDelShipFeatX + runsDelPlanLater),
	}
	calls := 0
	dashDelSeam(t, &dashboardDeleteReadFile, func(string) ([]byte, error) {
		data := reads[calls]
		calls++
		return data, nil
	})
	var written []byte
	dashDelSeam(t, &dashboardDeleteWriteBytes, func(_ string, data []byte) error {
		written = data
		return nil
	})

	if _, err := DashboardDeleteRuns(root, []DashboardRunKey{runsDelKeyPlanFeatX}); err != nil {
		t.Fatalf("DashboardDeleteRuns: %v", err)
	}
	if calls != 4 {
		t.Errorf("reads = %d, want 4", calls)
	}
	if want := runsDelShipFeatX + runsDelPlanLater; string(written) != want {
		t.Errorf("written = %q, want %q", written, want)
	}
}

// TestDashboardDeleteRuns_TwoChangedReadsInARowIsInfra gives an InfraError when each of two tries ends with a changed file, and keeps every appended line.
func TestDashboardDeleteRuns_TwoChangedReadsInARowIsInfra(t *testing.T) {
	root := t.TempDir()
	path, _ := runsDelFile(t, root, runsDelPlanFeatX+runsDelShipFeatX)
	calls := 0
	dashDelSeam(t, &dashboardDeleteReadFile, func(p string) ([]byte, error) {
		calls++
		if calls == 2 || calls == 4 {
			runsDelAppend(t, p, fmt.Sprintf(`{"ts":"2026-10-0%dT10:00:00Z","skill":"commit","branch":"feat/x"}`+"\n", calls))
		}
		return os.ReadFile(p)
	})
	runsDelNoWrite(t)

	out, err := DashboardDeleteRuns(root, []DashboardRunKey{runsDelKeyPlanFeatX})
	dashDelWantErr(t, err, "infra", "Another run wrote the History file at the same time. Reload the page and try again.")
	if out != (DashboardDeleteOut{}) {
		t.Errorf("out = %+v, want zero value", out)
	}
	if calls != 4 {
		t.Errorf("reads = %d, want 4 (no third try)", calls)
	}
	want := runsDelPlanFeatX + runsDelShipFeatX +
		`{"ts":"2026-10-02T10:00:00Z","skill":"commit","branch":"feat/x"}` + "\n" +
		`{"ts":"2026-10-04T10:00:00Z","skill":"commit","branch":"feat/x"}` + "\n"
	runsDelWant(t, path, []byte(want))
}

// TestDashboardDeleteRuns_FileGoneBetweenReadsRetries retries when the file goes away between the two reads and gives a DomainError on the retry.
func TestDashboardDeleteRuns_FileGoneBetweenReadsRetries(t *testing.T) {
	root := t.TempDir()
	path, _ := runsDelFile(t, root, runsDelPlanFeatX)
	calls := 0
	dashDelSeam(t, &dashboardDeleteReadFile, func(p string) ([]byte, error) {
		calls++
		data, err := os.ReadFile(p)
		if calls == 1 {
			os.Remove(p) // gone before the second read
		}
		return data, err
	})
	runsDelNoWrite(t)

	_, err := DashboardDeleteRuns(root, []DashboardRunKey{runsDelKeyPlanFeatX})
	dashDelWantErr(t, err, "domain", "Reload the page and try again.")
	if err.Error() != "The History file runs.jsonl does not exist" {
		t.Errorf("message = %q", err.Error())
	}
	if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("runs.jsonl exists after the delete: %v", statErr)
	}
}

// TestDashboardDeleteRuns_RetryRejectsKeyThatAnotherRunRemoved gives a DomainError on the retry when another run removed the row of a key.
func TestDashboardDeleteRuns_RetryRejectsKeyThatAnotherRunRemoved(t *testing.T) {
	root := t.TempDir()
	path, _ := runsDelFile(t, root, runsDelPlanFeatX+runsDelShipFeatX)
	calls := 0
	dashDelSeam(t, &dashboardDeleteReadFile, func(p string) ([]byte, error) {
		calls++
		if calls == 2 {
			// The other run deletes the row of the key after the first read.
			if err := os.WriteFile(p, []byte(runsDelShipFeatX), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return os.ReadFile(p)
	})
	runsDelNoWrite(t)

	_, err := DashboardDeleteRuns(root, []DashboardRunKey{runsDelKeyPlanFeatX})
	dashDelWantErr(t, err, "domain", "Reload the page and try again.")
	runsDelWant(t, path, []byte(runsDelShipFeatX))
}

// TestDashboardDropRuns checks the line rewrite without a file.
func TestDashboardDropRuns(t *testing.T) {
	cases := []struct {
		name          string
		data          string
		keys          []DashboardRunKey
		want          string
		wantRemoved   int
		wantUnmatched *DashboardRunKey
	}{
		{"empty data, one key", "", []DashboardRunKey{runsDelKeyPlanFeatX}, "", 0, &runsDelKeyPlanFeatX},
		{"first unmatched key is named", runsDelPlanFeatX, []DashboardRunKey{runsDelKeyPlanFeatX, runsDelKeyShipFeatX, runsDelKeyNoBranch}, "", 1, &runsDelKeyShipFeatX},
		{"json null line stays", "null\n" + runsDelPlanFeatX, []DashboardRunKey{runsDelKeyPlanFeatX}, "null\n", 1, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			kept, removed, unmatched := dashboardDropRuns([]byte(tc.data), tc.keys)
			if string(kept) != tc.want || removed != tc.wantRemoved {
				t.Errorf("kept = %q, removed = %d, want %q, %d", kept, removed, tc.want, tc.wantRemoved)
			}
			switch {
			case tc.wantUnmatched == nil && unmatched != nil:
				t.Errorf("unmatched = %+v, want nil", *unmatched)
			case tc.wantUnmatched != nil && (unmatched == nil || *unmatched != *tc.wantUnmatched):
				t.Errorf("unmatched = %v, want %+v", unmatched, *tc.wantUnmatched)
			}
		})
	}
}
