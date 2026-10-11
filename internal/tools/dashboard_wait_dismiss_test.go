package tools

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/attention"
)

// dashDismissSeed writes one wait record under root through the attention
// package, so the file name is the one the hooks produce.
func dashDismissSeed(t *testing.T, root, kind, sid, toolUseID string) {
	t.Helper()
	rec := attention.Record{
		Kind:      kind,
		SessionID: sid,
		ToolUseID: toolUseID,
		Branch:    "feat/x",
		Header:    "Header",
		Text:      "Text",
		AskedAt:   time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC).Format(time.RFC3339),
	}
	if err := attention.Write(root, rec); err != nil {
		t.Fatalf("seed %s wait: %v", kind, err)
	}
}

// dashDismissFiles returns the sorted entry names in the attention folder of
// root, or nil when the folder does not exist.
func dashDismissFiles(t *testing.T, root string) []string {
	t.Helper()
	entries, err := os.ReadDir(attention.Dir(root))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names
}

// dashDismissWantFiles fails the test unless the attention folder of root
// holds exactly the named entries.
func dashDismissWantFiles(t *testing.T, root string, want ...string) {
	t.Helper()
	got := dashDismissFiles(t, root)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("attention folder = %v, want %v", got, want)
	}
}

func TestDashboardDismissWait_QuestionRemovesOnlyThatFile(t *testing.T) {
	root := t.TempDir()
	dashDismissSeed(t, root, attention.KindQuestion, "s1", "t1")
	dashDismissSeed(t, root, attention.KindQuestion, "s1", "t2")
	dashDismissSeed(t, root, attention.KindPermission, "s1", "")

	out, err := DashboardDismissWait(root, "question", "s1", "t1")
	if err != nil {
		t.Fatalf("DashboardDismissWait: %v", err)
	}
	want := DashboardDeleteOut{Deleted: true, Message: "The wait is dismissed."}
	if out != want {
		t.Errorf("out = %+v, want %+v", out, want)
	}
	dashDismissWantFiles(t, root, "s1-permission.json", "s1-t2.json")
}

func TestDashboardDismissWait_PermissionRemovesOnlyThatFile(t *testing.T) {
	root := t.TempDir()
	dashDismissSeed(t, root, attention.KindQuestion, "s1", "t1")
	dashDismissSeed(t, root, attention.KindPermission, "s1", "")
	dashDismissSeed(t, root, attention.KindPermission, "s2", "")

	out, err := DashboardDismissWait(root, "permission", "s1", "")
	if err != nil {
		t.Fatalf("DashboardDismissWait: %v", err)
	}
	want := DashboardDeleteOut{Deleted: true, Message: "The wait is dismissed."}
	if out != want {
		t.Errorf("out = %+v, want %+v", out, want)
	}
	dashDismissWantFiles(t, root, "s1-t1.json", "s2-permission.json")
}

func TestDashboardDismissWait_AlreadyGone(t *testing.T) {
	cases := []struct {
		name, kind, tool string
	}{
		{"question", "question", "t1"},
		{"permission", "permission", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name+" with no folder", func(t *testing.T) {
			root := t.TempDir()
			out, err := DashboardDismissWait(root, tc.kind, "s1", tc.tool)
			if err != nil {
				t.Fatalf("DashboardDismissWait: %v", err)
			}
			want := DashboardDeleteOut{AlreadyGone: true, Message: "The wait is already gone."}
			if out != want {
				t.Errorf("out = %+v, want %+v", out, want)
			}
			dashDismissWantFiles(t, root)
		})
		t.Run(tc.name+" with other records", func(t *testing.T) {
			root := t.TempDir()
			dashDismissSeed(t, root, attention.KindQuestion, "s2", "t9")
			out, err := DashboardDismissWait(root, tc.kind, "s1", tc.tool)
			if err != nil {
				t.Fatalf("DashboardDismissWait: %v", err)
			}
			if !out.AlreadyGone || out.Deleted {
				t.Errorf("out = %+v, want AlreadyGone only", out)
			}
			dashDismissWantFiles(t, root, "s2-t9.json")
		})
	}
}

func TestDashboardDismissWait_InputErrors(t *testing.T) {
	cases := []struct {
		name                    string
		kind, sid, tool, wanted string
	}{
		{"unknown kind", "other", "s1", "t1", `The kind "other" is not "question" or "permission"`},
		{"empty kind", "", "s1", "t1", `The kind "" is not "question" or "permission"`},
		{"empty session ID for a question", "question", "", "t1", "The sessionId field is required"},
		{"empty session ID for a permission", "permission", "", "", "The sessionId field is required"},
		{"question without toolUseId", "question", "s1", "", "A question wait needs its toolUseId"},
		{"permission with toolUseId", "permission", "s1", "t1", "A permission wait has no toolUseId"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			dashDismissSeed(t, root, attention.KindQuestion, "s1", "t1")
			dashDismissSeed(t, root, attention.KindPermission, "s1", "")

			out, err := DashboardDismissWait(root, tc.kind, tc.sid, tc.tool)
			dashDelWantErr(t, err, "domain", "The page sent a malformed request. Report this as a bug of the sdlc dashboard.")
			if err != nil && err.Error() != tc.wanted {
				t.Errorf("message = %q, want %q", err.Error(), tc.wanted)
			}
			if out != (DashboardDeleteOut{}) {
				t.Errorf("out = %+v, want the zero value", out)
			}
			dashDismissWantFiles(t, root, "s1-permission.json", "s1-t1.json")
		})
	}
}

// TestDashboardDismissWait_SlashSessionID checks that a "/" in the session ID
// and in the tool-use ID maps to '-' in the file name, so the dismiss removes
// the file in the attention folder and no file outside it.
func TestDashboardDismissWait_SlashSessionID(t *testing.T) {
	root := t.TempDir()
	dashDismissSeed(t, root, attention.KindQuestion, "a/b", "t/1")
	dashDismissSeed(t, root, attention.KindPermission, "a/b", "")
	path := filepath.Join(attention.Dir(root), "a-b-t-1.json")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the seeded file must be at %s: %v", path, err)
	}

	out, err := DashboardDismissWait(root, "question", "a/b", "t/1")
	if err != nil {
		t.Fatalf("DashboardDismissWait: %v", err)
	}
	if !out.Deleted {
		t.Errorf("out = %+v, want Deleted", out)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("%s must be gone, Stat error = %v", path, err)
	}
	dashDismissWantFiles(t, root, "a-b-permission.json")
}

// TestDashboardDismissWait_RemoveFailure checks that a record that cannot be
// removed gives an InfraError with the write-permission Suggestion. A
// non-empty folder at the record path makes os.Remove fail.
func TestDashboardDismissWait_RemoveFailure(t *testing.T) {
	cases := []struct {
		name, kind, tool, file string
	}{
		{"question", "question", "t1", "s1-t1.json"},
		{"permission", "permission", "", "s1-permission.json"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			blocker := filepath.Join(attention.Dir(root), tc.file)
			if err := os.MkdirAll(blocker, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(blocker, "inner.txt"), []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}

			out, err := DashboardDismissWait(root, tc.kind, "s1", tc.tool)
			dashDelWantErr(t, err, "infra", "Check write permission on .sdlc-v2/evidence/attention/ and retry.")
			if err != nil && !strings.Contains(err.Error(), blocker) {
				t.Errorf("error = %q, want it to name %s", err, blocker)
			}
			if out != (DashboardDeleteOut{}) {
				t.Errorf("out = %+v, want the zero value", out)
			}
			if _, statErr := os.Stat(filepath.Join(blocker, "inner.txt")); statErr != nil {
				t.Errorf("the blocking folder must stay: %v", statErr)
			}
		})
	}
}
