package attention

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// testNow is the fixed clock that every List call in these tests uses.
var testNow = time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)

// askedAgo returns an RFC3339 UTC timestamp that lies d before testNow.
func askedAgo(d time.Duration) string {
	return testNow.Add(-d).Format(time.RFC3339)
}

// question builds a valid question record for the given session and tool-use ID.
func question(sid, toolUseID string) Record {
	return Record{
		Kind:      KindQuestion,
		SessionID: sid,
		ToolUseID: toolUseID,
		Branch:    "feat/x",
		Header:    "Guardrail",
		Text:      "Approve the splice_test.go change?",
		AskedAt:   askedAgo(time.Minute),
	}
}

// permission builds a valid permission record for the given session.
func permission(sid string) Record {
	return Record{
		Kind:      KindPermission,
		SessionID: sid,
		Branch:    "feat/x",
		Header:    "Permission",
		Text:      "Bash: go test ./...",
		AskedAt:   askedAgo(time.Minute),
	}
}

// mustWrite writes r under root and fails the test when Write returns an error.
func mustWrite(t *testing.T, root string, r Record) {
	t.Helper()
	if err := Write(root, r); err != nil {
		t.Fatalf("Write(%+v) failed: %v", r, err)
	}
}

// fileNames returns the sorted names in the attention folder, or nil when the
// folder does not exist.
func fileNames(t *testing.T, root string) []string {
	t.Helper()
	entries, err := os.ReadDir(Dir(root))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("ReadDir failed: %v", err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// assertFiles fails the test unless the attention folder holds exactly the
// named entries.
func assertFiles(t *testing.T, root string, want ...string) {
	t.Helper()
	got := fileNames(t, root)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("files = %v, want %v", got, want)
	}
}

// mustList lists the records under root with testNow and a one-hour maximum
// age, and fails the test when List returns an error.
func mustList(t *testing.T, root string) []Record {
	t.Helper()
	got, err := List(root, testNow, time.Hour)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	return got
}

// blockAttentionDir makes the attention folder path a regular file, so any
// folder operation on it fails. It returns the content of that file.
func blockAttentionDir(t *testing.T, root string) []byte {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(Dir(root)), 0755); err != nil {
		t.Fatalf("create parent folder: %v", err)
	}
	content := []byte("not a folder")
	if err := os.WriteFile(Dir(root), content, 0644); err != nil {
		t.Fatalf("create blocker file: %v", err)
	}
	return content
}

// makeNonEmptyDir creates a directory with one file inside at path, so
// os.Remove on it fails.
func makeNonEmptyDir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0755); err != nil {
		t.Fatalf("create dir %s: %v", path, err)
	}
	if err := os.WriteFile(filepath.Join(path, "inner.txt"), []byte("x"), 0644); err != nil {
		t.Fatalf("create inner file: %v", err)
	}
}

// TestDir checks that Dir joins the data folder, the evidence folder, and the
// attention folder under the root.
func TestDir(t *testing.T) {
	root := t.TempDir()
	want := filepath.Join(root, ".sdlc-v2", "evidence", "attention")
	if got := Dir(root); got != want {
		t.Errorf("Dir = %q, want %q", got, want)
	}
}

// TestWrite_QuestionFileAndFields checks the file name and the stored fields
// of a question record, including the forced Version of 1.
func TestWrite_QuestionFileAndFields(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, root, question("s1", "toolu_9"))

	path := filepath.Join(Dir(root), "s1-toolu_9.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read record file: %v", err)
	}
	var got Record
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	want := question("s1", "toolu_9")
	want.Version = 1
	if got != want {
		t.Errorf("record = %+v, want %+v", got, want)
	}
	assertFiles(t, root, "s1-toolu_9.json")
}

// TestWrite_PermissionFileAndNoToolUseID checks that a permission record goes
// to "<sid>-permission.json" and drops any tool-use ID.
func TestWrite_PermissionFileAndNoToolUseID(t *testing.T) {
	root := t.TempDir()
	r := permission("s1")
	r.ToolUseID = "toolu_ignored"
	mustWrite(t, root, r)

	assertFiles(t, root, "s1-permission.json")
	data, err := os.ReadFile(filepath.Join(Dir(root), "s1-permission.json"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.Contains(string(data), "toolUseId") {
		t.Errorf("permission record must not carry toolUseId, got %s", data)
	}
}

// TestWrite_SanitizesFileName checks that unsafe characters in the session ID
// and the tool-use ID become '-' in the file name.
func TestWrite_SanitizesFileName(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, root, question("../evil/sid", "tool use.id"))
	assertFiles(t, root, "---evil-sid-tool-use-id.json")
}

// TestWrite_OverwritesSameWait checks that a second Write for the same wait
// replaces the first record and leaves one file.
func TestWrite_OverwritesSameWait(t *testing.T) {
	root := t.TempDir()
	r := question("s1", "toolu_9")
	mustWrite(t, root, r)
	r.Text = "second text"
	mustWrite(t, root, r)

	assertFiles(t, root, "s1-toolu_9.json")
	got := mustList(t, root)
	if len(got) != 1 || got[0].Text != "second text" {
		t.Errorf("List = %+v, want one record with the second text", got)
	}
}

// TestWrite_RedactsThenCaps checks that Write redacts an email in Header and
// caps a long multi-byte Text at MaxTextRunes runes plus the ellipsis.
func TestWrite_RedactsThenCaps(t *testing.T) {
	root := t.TempDir()
	r := question("s1", "toolu_9")
	r.Header = "mail bob@example.com now"
	r.Text = strings.Repeat("é", 300)
	mustWrite(t, root, r)

	got := mustList(t, root)
	if len(got) != 1 {
		t.Fatalf("List = %+v, want one record", got)
	}
	if strings.Contains(got[0].Header, "bob@example.com") {
		t.Errorf("Header not redacted: %q", got[0].Header)
	}
	text := []rune(got[0].Text)
	if len(text) != MaxTextRunes+1 { // +1 for the trailing "…" rune
		t.Fatalf("Text has %d runes, want %d", len(text), MaxTextRunes+1)
	}
	if text[len(text)-1] != '…' {
		t.Errorf("last rune = %q, want '…'", text[len(text)-1])
	}
	if string(text[:MaxTextRunes]) != strings.Repeat("é", MaxTextRunes) {
		t.Errorf("Text body is not the first %d runes of the input", MaxTextRunes)
	}
}

// TestWrite_ShortTextUnchanged checks that text at exactly MaxTextRunes runes
// is stored verbatim, with no ellipsis.
func TestWrite_ShortTextUnchanged(t *testing.T) {
	root := t.TempDir()
	r := question("s1", "toolu_9")
	r.Text = strings.Repeat("a", MaxTextRunes)
	mustWrite(t, root, r)

	got := mustList(t, root)
	if len(got) != 1 {
		t.Fatalf("List = %+v, want one record", got)
	}
	if got[0].Text != r.Text {
		t.Errorf("Text at the cap must stay verbatim, got %q", got[0].Text)
	}
}

// TestWrite_DefaultsAskedAt checks that an empty AskedAt becomes the current
// UTC time in RFC3339 form.
func TestWrite_DefaultsAskedAt(t *testing.T) {
	root := t.TempDir()
	r := question("s1", "toolu_9")
	r.AskedAt = ""
	before := time.Now().UTC().Add(-time.Second)
	mustWrite(t, root, r)

	got, err := List(root, time.Now().UTC(), time.Hour)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("List = %+v, want one record", got)
	}
	asked, err := time.Parse(time.RFC3339, got[0].AskedAt)
	if err != nil {
		t.Fatalf("AskedAt %q is not RFC3339: %v", got[0].AskedAt, err)
	}
	if asked.Before(before) {
		t.Errorf("AskedAt %v is before the write call %v", asked, before)
	}
}

// TestWrite_Errors checks that Write rejects an unknown or empty kind, an
// empty session ID, and a question with no tool-use ID, and writes nothing.
func TestWrite_Errors(t *testing.T) {
	tests := []struct {
		name string
		rec  Record
	}{
		{"unknown kind", Record{Kind: "other", SessionID: "s1", ToolUseID: "t"}},
		{"empty kind", Record{SessionID: "s1", ToolUseID: "t"}},
		{"empty session", Record{Kind: KindQuestion, ToolUseID: "t"}},
		{"empty session permission", Record{Kind: KindPermission}},
		{"question without tool-use ID", Record{Kind: KindQuestion, SessionID: "s1"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			if err := Write(root, tt.rec); err == nil {
				t.Fatalf("Write(%+v) returned nil, want error", tt.rec)
			}
			if names := fileNames(t, root); len(names) != 0 {
				t.Errorf("a failed Write left files: %v", names)
			}
		})
	}
}

// TestWrite_MkdirFailure checks the first step of Write: when the attention
// folder path is a regular file, Write returns a "create" error, the blocker
// file keeps its content, and nothing else appears in the evidence folder.
func TestWrite_MkdirFailure(t *testing.T) {
	root := t.TempDir()
	blocker := blockAttentionDir(t, root)

	err := Write(root, question("s1", "toolu_9"))
	if err == nil {
		t.Fatal("Write returned nil, want an error when the folder cannot be created")
	}
	if !strings.HasPrefix(err.Error(), "attention: create ") {
		t.Errorf("error = %q, want prefix %q", err, "attention: create ")
	}

	got, rerr := os.ReadFile(Dir(root))
	if rerr != nil {
		t.Fatalf("blocker file is gone: %v", rerr)
	}
	if string(got) != string(blocker) {
		t.Errorf("blocker content = %q, want %q", got, blocker)
	}
	entries, rerr := os.ReadDir(filepath.Dir(Dir(root)))
	if rerr != nil {
		t.Fatalf("read evidence folder: %v", rerr)
	}
	if len(entries) != 1 || entries[0].Name() != "attention" {
		t.Errorf("evidence folder holds %d entries, want only the blocker file", len(entries))
	}
}

// TestWrite_AtomicWriteFailure checks the second step of Write: when the
// record path is an existing directory, the rename fails, Write returns the
// error, the directory stays, no temp file is left, and a record written
// before the failure still lists.
func TestWrite_AtomicWriteFailure(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, root, question("s1", "other"))
	target := filepath.Join(Dir(root), "s1-toolu_9.json")
	if err := os.Mkdir(target, 0755); err != nil {
		t.Fatalf("create blocker dir: %v", err)
	}

	err := Write(root, question("s1", "toolu_9"))
	if err == nil {
		t.Fatal("Write returned nil, want an error when the record path is a directory")
	}
	if !strings.Contains(err.Error(), "fsx:") {
		t.Errorf("error = %q, want it to come from the atomic write", err)
	}

	info, serr := os.Stat(target)
	if serr != nil || !info.IsDir() {
		t.Errorf("record path must stay a directory, got info=%v err=%v", info, serr)
	}
	assertFiles(t, root, "s1-other.json", "s1-toolu_9.json") // no temp file left
	got := mustList(t, root)
	if len(got) != 1 || got[0].ToolUseID != "other" {
		t.Errorf("List = %+v, want only the record written before the failure", got)
	}
}

// TestDeleteQuestion_ExactAndMissing checks that DeleteQuestion removes one
// record by tool-use ID and treats a second delete as success.
func TestDeleteQuestion_ExactAndMissing(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, root, question("s1", "toolu_1"))
	mustWrite(t, root, question("s1", "toolu_2"))

	if err := DeleteQuestion(root, "s1", "toolu_1"); err != nil {
		t.Fatalf("DeleteQuestion: %v", err)
	}
	assertFiles(t, root, "s1-toolu_2.json")

	if err := DeleteQuestion(root, "s1", "toolu_1"); err != nil {
		t.Errorf("deleting a missing record must not fail: %v", err)
	}
}

// TestDeleteQuestion_RemoveError checks that DeleteQuestion returns a remove
// error that is not "file missing", here a non-empty directory at the path.
func TestDeleteQuestion_RemoveError(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(Dir(root), "s1-toolu_1.json")
	makeNonEmptyDir(t, path)

	err := DeleteQuestion(root, "s1", "toolu_1")
	if err == nil {
		t.Fatal("DeleteQuestion returned nil, want the remove error")
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("error = %q, want it to name %s", err, path)
	}
}

// TestDeleteQuestion_EmptyToolUseIDDeletesSessionQuestions checks that an
// empty tool-use ID deletes every question of the session and keeps the
// permission record and the records of other sessions, including a session
// whose ID starts with "<sid>-".
func TestDeleteQuestion_EmptyToolUseIDDeletesSessionQuestions(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, root, question("s1", "toolu_1"))
	mustWrite(t, root, question("s1", "toolu_2"))
	mustWrite(t, root, permission("s1"))
	mustWrite(t, root, question("s2", "toolu_3"))
	mustWrite(t, root, question("s1-b", "toolu_4"))

	if err := DeleteQuestion(root, "s1", ""); err != nil {
		t.Fatalf("DeleteQuestion: %v", err)
	}
	assertFiles(t, root, "s1-b-toolu_4.json", "s1-permission.json", "s2-toolu_3.json")
}

// TestDeleteQuestion_EmptyToolUseIDMissingDir checks that deleting by session
// when the folder does not exist is not an error.
func TestDeleteQuestion_EmptyToolUseIDMissingDir(t *testing.T) {
	if err := DeleteQuestion(t.TempDir(), "s1", ""); err != nil {
		t.Errorf("missing folder must not fail: %v", err)
	}
}

// TestDeleteQuestion_EmptyToolUseIDKeepsOtherKinds checks that a readable
// record of another kind is kept by a question delete but removed by
// DeleteSession, even when its file name looks like a question file.
func TestDeleteQuestion_EmptyToolUseIDKeepsOtherKinds(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, root, question("s1", "toolu_1"))
	body := `{"version":1,"kind":"permission","sessionId":"s1","askedAt":"` + askedAgo(time.Minute) + `"}`
	if err := os.WriteFile(filepath.Join(Dir(root), "s1-odd.json"), []byte(body), 0644); err != nil {
		t.Fatalf("write odd record: %v", err)
	}
	mustWrite(t, root, permission("s1"))

	if err := DeleteQuestion(root, "s1", ""); err != nil {
		t.Fatalf("DeleteQuestion: %v", err)
	}
	assertFiles(t, root, "s1-odd.json", "s1-permission.json")

	if err := DeleteSession(root, "s1"); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	assertFiles(t, root)
}

// TestDeletePermission checks that DeletePermission removes only the
// permission record and treats a second delete as success.
func TestDeletePermission(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, root, permission("s1"))
	mustWrite(t, root, question("s1", "toolu_1"))

	if err := DeletePermission(root, "s1"); err != nil {
		t.Fatalf("DeletePermission: %v", err)
	}
	assertFiles(t, root, "s1-toolu_1.json")

	if err := DeletePermission(root, "s1"); err != nil {
		t.Errorf("deleting a missing record must not fail: %v", err)
	}
}

// TestDeletePermission_RemoveError checks that DeletePermission returns a
// remove error that is not "file missing", here a non-empty directory.
func TestDeletePermission_RemoveError(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(Dir(root), "s1-permission.json")
	makeNonEmptyDir(t, path)

	err := DeletePermission(root, "s1")
	if err == nil {
		t.Fatal("DeletePermission returned nil, want the remove error")
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("error = %q, want it to name %s", err, path)
	}
}

// TestDeleteSession checks that DeleteSession removes every record of one
// session and keeps the records of other sessions.
func TestDeleteSession(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, root, question("s1", "toolu_1"))
	mustWrite(t, root, question("s1", "toolu_2"))
	mustWrite(t, root, permission("s1"))
	mustWrite(t, root, question("s2", "toolu_3"))
	mustWrite(t, root, permission("s1-b"))

	if err := DeleteSession(root, "s1"); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	assertFiles(t, root, "s1-b-permission.json", "s2-toolu_3.json")
}

// TestDeleteSession_MissingDir checks that DeleteSession on a missing folder
// is not an error.
func TestDeleteSession_MissingDir(t *testing.T) {
	if err := DeleteSession(t.TempDir(), "s1"); err != nil {
		t.Errorf("missing folder must not fail: %v", err)
	}
}

// TestDeleteSession_ReadDirError checks that a folder read error that is not
// "missing" (the attention path is a file) is returned by every
// session-wide delete, and the blocker file stays.
func TestDeleteSession_ReadDirError(t *testing.T) {
	root := t.TempDir()
	blocker := blockAttentionDir(t, root)

	if err := DeleteSession(root, "s1"); err == nil {
		t.Error("DeleteSession returned nil, want the folder read error")
	} else if !strings.HasPrefix(err.Error(), "attention: read ") {
		t.Errorf("DeleteSession error = %q, want prefix %q", err, "attention: read ")
	}
	if err := DeleteQuestion(root, "s1", ""); err == nil {
		t.Error("DeleteQuestion by session returned nil, want the folder read error")
	}

	got, err := os.ReadFile(Dir(root))
	if err != nil || string(got) != string(blocker) {
		t.Errorf("blocker file changed: content=%q err=%v", got, err)
	}
}

// TestDeleteSession_UnparseableFile checks the documented arm for a file that
// cannot be parsed: when its name fits the session, a question delete and
// DeleteSession remove it; a file of another session or with another suffix
// stays; the permission file with garbage content is kept by a question
// delete because its name decides its kind.
func TestDeleteSession_UnparseableFile(t *testing.T) {
	root := t.TempDir()
	dir := Dir(root)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	write := func(name string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte("{not json"), 0644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	write("s1-garbage.json")
	write("s1-permission.json")
	write("s1-note.txt")
	write("s2-garbage.json")

	if err := DeleteQuestion(root, "s1", ""); err != nil {
		t.Fatalf("DeleteQuestion: %v", err)
	}
	assertFiles(t, root, "s1-note.txt", "s1-permission.json", "s2-garbage.json")

	if err := DeleteSession(root, "s1"); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	assertFiles(t, root, "s1-note.txt", "s2-garbage.json")
}

// TestDeleteSession_RemoveErrorKeepsDeleting checks that a remove error on one
// entry (a non-empty directory with a record name) does not stop the loop: the
// other records of the session are deleted, the first error is returned, the
// failing entries stay, and records of other sessions are untouched.
func TestDeleteSession_RemoveErrorKeepsDeleting(t *testing.T) {
	root := t.TempDir()
	dir := Dir(root)
	mustWrite(t, root, question("s1", "a"))
	makeNonEmptyDir(t, filepath.Join(dir, "s1-b.json"))
	mustWrite(t, root, question("s1", "c"))
	makeNonEmptyDir(t, filepath.Join(dir, "s1-d.json"))
	mustWrite(t, root, permission("s1"))
	mustWrite(t, root, question("s2", "x"))

	err := DeleteSession(root, "s1")
	if err == nil {
		t.Fatal("DeleteSession returned nil, want the first remove error")
	}
	if !strings.Contains(err.Error(), "s1-b.json") {
		t.Errorf("error = %q, want the first failing entry s1-b.json", err)
	}
	if strings.Contains(err.Error(), "s1-d.json") {
		t.Errorf("error = %q, must report only the first failure", err)
	}
	assertFiles(t, root, "s1-b.json", "s1-d.json", "s2-x.json")
}

// TestDelete_EmptySessionIDErrors checks that every delete function rejects an
// empty session ID and removes nothing.
func TestDelete_EmptySessionIDErrors(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, root, question("s1", "toolu_1"))

	if err := DeleteQuestion(root, "", "toolu_1"); err == nil {
		t.Error("DeleteQuestion with empty session ID: want error")
	}
	if err := DeleteQuestion(root, "", ""); err == nil {
		t.Error("DeleteQuestion with empty IDs: want error")
	}
	if err := DeletePermission(root, ""); err == nil {
		t.Error("DeletePermission with empty session ID: want error")
	}
	if err := DeleteSession(root, ""); err == nil {
		t.Error("DeleteSession with empty session ID: want error")
	}
	assertFiles(t, root, "s1-toolu_1.json")
}

// TestList_MissingDirReturnsEmptyList checks that a missing folder gives a
// non-nil empty slice, no error, and the JSON text "[]".
func TestList_MissingDirReturnsEmptyList(t *testing.T) {
	got, err := List(t.TempDir(), testNow, time.Hour)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("List = %#v, want a non-nil empty slice", got)
	}
	data, _ := json.Marshal(got)
	if string(data) != "[]" {
		t.Errorf("JSON = %s, want []", data)
	}
}

// TestList_ReadDirError checks that a folder read error that is not "missing"
// (the attention path is a file) is returned and the result is nil.
func TestList_ReadDirError(t *testing.T) {
	root := t.TempDir()
	blockAttentionDir(t, root)

	got, err := List(root, testNow, time.Hour)
	if err == nil {
		t.Fatalf("List = %+v, want the folder read error", got)
	}
	if !strings.HasPrefix(err.Error(), "attention: read ") {
		t.Errorf("error = %q, want prefix %q", err, "attention: read ")
	}
	if got != nil {
		t.Errorf("List = %#v on error, want nil", got)
	}
}

// TestList_SkipsStaleUnreadableAndOtherVersions checks that List keeps only a
// fresh valid record and skips stale, unparseable, other-version, invalid,
// badly dated, non-JSON, temp, and directory entries.
func TestList_SkipsStaleUnreadableAndOtherVersions(t *testing.T) {
	root := t.TempDir()
	fresh := question("s1", "fresh")
	mustWrite(t, root, fresh)

	stale := question("s1", "stale")
	stale.AskedAt = askedAgo(3 * time.Hour)
	mustWrite(t, root, stale)

	dir := Dir(root)
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	write("garbage.json", "{not json")
	write("v2.json", `{"version":2,"kind":"question","sessionId":"s1","toolUseId":"v2","askedAt":"`+askedAgo(time.Minute)+`"}`)
	write("v0.json", `{"kind":"question","sessionId":"s1","toolUseId":"v0","askedAt":"`+askedAgo(time.Minute)+`"}`)
	write("badkind.json", `{"version":1,"kind":"other","sessionId":"s1","askedAt":"`+askedAgo(time.Minute)+`"}`)
	write("badtime.json", `{"version":1,"kind":"question","sessionId":"s1","toolUseId":"bt","askedAt":"yesterday"}`)
	write("note.txt", "not a record")
	write("s1-fresh.json.tmp-123", "partial write")
	if err := os.Mkdir(filepath.Join(dir, "sub.json"), 0755); err != nil {
		t.Fatal(err)
	}

	got := mustList(t, root)
	if len(got) != 1 || got[0].ToolUseID != "fresh" {
		t.Fatalf("List = %+v, want only the fresh record", got)
	}
	if got[0].Version != 1 {
		t.Errorf("Version = %d, want 1", got[0].Version)
	}
}

// TestList_SkipsUnreadableFile checks that List skips a record file it cannot
// open (mode 0000) and still returns the readable records.
func TestList_SkipsUnreadableFile(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, root, question("s1", "open"))
	mustWrite(t, root, question("s1", "locked"))
	locked := filepath.Join(Dir(root), "s1-locked.json")
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { os.Chmod(locked, 0644) })
	if f, err := os.Open(locked); err == nil {
		f.Close()
		t.Skip("file mode 0000 is still readable here (running as root)")
	}

	got := mustList(t, root)
	if len(got) != 1 || got[0].ToolUseID != "open" {
		t.Errorf("List = %+v, want only the readable record", got)
	}
}

// TestList_AgeBoundary checks that a record exactly maxAge old stays and a
// record one second older goes.
func TestList_AgeBoundary(t *testing.T) {
	root := t.TempDir()
	edge := question("s1", "edge")
	edge.AskedAt = askedAgo(time.Hour)
	mustWrite(t, root, edge)

	got, err := List(root, testNow, time.Hour)
	if err != nil {
		t.Fatalf("List at the boundary: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("record exactly maxAge old must stay, got %+v", got)
	}
	got, err = List(root, testNow.Add(time.Second), time.Hour)
	if err != nil {
		t.Fatalf("List past the boundary: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("record older than maxAge must go, got %+v", got)
	}
}

// TestList_NonPositiveMaxAgeKeepsAll checks that a maxAge of zero turns the
// age check off.
func TestList_NonPositiveMaxAgeKeepsAll(t *testing.T) {
	root := t.TempDir()
	old := question("s1", "old")
	old.AskedAt = askedAgo(1000 * time.Hour)
	mustWrite(t, root, old)

	got, err := List(root, testNow, 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("maxAge 0 must turn the age check off, got %+v", got)
	}
}

// TestList_OldestFirst checks that List orders records by AskedAt, oldest
// first.
func TestList_OldestFirst(t *testing.T) {
	root := t.TempDir()
	newest := question("s1", "c")
	newest.AskedAt = askedAgo(time.Minute)
	oldest := permission("s2")
	oldest.AskedAt = askedAgo(30 * time.Minute)
	middle := question("s1", "a")
	middle.AskedAt = askedAgo(10 * time.Minute)
	for _, r := range []Record{newest, oldest, middle} {
		mustWrite(t, root, r)
	}

	if got := listOrder(mustList(t, root)); got != "s2/,s1/a,s1/c" {
		t.Errorf("order = %s, want s2/,s1/a,s1/c", got)
	}
}

// TestList_OrdersByInstantNotText checks that List compares the parsed time:
// a record with a +02:00 offset that is earlier in UTC sorts first although
// its text sorts after the other.
func TestList_OrdersByInstantNotText(t *testing.T) {
	root := t.TempDir()
	zoned := question("s1", "zoned")
	zoned.AskedAt = "2026-10-09T11:30:00+02:00" // 09:30 UTC
	utc := question("s1", "utc")
	utc.AskedAt = "2026-10-09T10:00:00Z"
	for _, r := range []Record{utc, zoned} {
		mustWrite(t, root, r)
	}

	got, err := List(root, testNow, 3*time.Hour)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if order := listOrder(got); order != "s1/zoned,s1/utc" {
		t.Errorf("order = %s, want s1/zoned,s1/utc", order)
	}
}

// TestList_TieBreakers checks the order of records with the same AskedAt: by
// session ID first, then by tool-use ID.
func TestList_TieBreakers(t *testing.T) {
	root := t.TempDir()
	at := askedAgo(5 * time.Minute)
	for _, r := range []Record{
		question("s2", "a"),
		question("s1", "b"),
		permission("s3"),
		question("s1", "a"),
		// File s1-a-b.json sorts before s1-a.json, so only the ToolUseID
		// tie-breaker can put "a" before "a b".
		question("s1", "a b"),
	} {
		r.AskedAt = at
		mustWrite(t, root, r)
	}

	if got := listOrder(mustList(t, root)); got != "s1/a,s1/a b,s1/b,s2/a,s3/" {
		t.Errorf("order = %s, want s1/a,s1/a b,s1/b,s2/a,s3/", got)
	}
}

// listOrder renders the records as "<session>/<toolUseId>" joined by commas,
// in list order.
func listOrder(records []Record) string {
	parts := make([]string, 0, len(records))
	for _, r := range records {
		parts = append(parts, r.SessionID+"/"+r.ToolUseID)
	}
	return strings.Join(parts, ",")
}
