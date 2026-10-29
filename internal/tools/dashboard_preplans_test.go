package tools

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
)

// preplanDir returns the preplan folder of root.
func preplanDir(root string) string {
	return filepath.Join(root, paths.DataDir, paths.PreplanSubdir)
}

// preplanWrite writes <slug>.md with content to the preplan folder of root and
// sets its modification time to mtime. It returns the file path.
func preplanWrite(t *testing.T, root, slug, content string, mtime time.Time) string {
	t.Helper()
	p := filepath.Join(preplanDir(root), slug+".md")
	writeFile(t, p, content)
	if err := os.Chtimes(p, mtime, mtime); err != nil {
		t.Fatalf("chtimes %s: %v", p, err)
	}
	return p
}

// preplanTopicFile returns the head of a topic file with the given topic and
// status.
func preplanTopicFile(topic, status string) string {
	return "# Preplan: " + topic + "\n\n**Status:** " + status + "\n\n## Goal\n"
}

// preplanSlugs returns the slugs of list in order.
func preplanSlugs(list []DashboardPreplan) []string {
	out := make([]string, 0, len(list))
	for _, p := range list {
		out = append(out, p.Slug)
	}
	return out
}

// TestDashboardPreplans_FolderAbsent checks that a missing preplan folder
// gives an empty list, no warning, and a list that is not nil.
func TestDashboardPreplans_FolderAbsent(t *testing.T) {
	list, warnings := dashboardPreplans(t.TempDir())
	if list == nil || len(list) != 0 {
		t.Errorf("list = %#v, want empty and not nil", list)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %v, want none", warnings)
	}
}

// TestDashboardPreplans_FolderDanglingLink checks that a preplan folder that
// is a link to a missing target counts as absent.
func TestDashboardPreplans_FolderDanglingLink(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, paths.DataDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "missing-target"), preplanDir(root)); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}
	list, warnings := dashboardPreplans(root)
	if len(list) != 0 || len(warnings) != 0 {
		t.Errorf("list = %v, warnings = %v, want both empty", list, warnings)
	}
}

// TestDashboardPreplans_FolderNotReadable checks that a ReadDir error gives an
// empty list and the "Preplan folder not read" warning. A regular file at the
// folder path makes ReadDir fail with an error that is not ErrNotExist.
func TestDashboardPreplans_FolderNotReadable(t *testing.T) {
	root := t.TempDir()
	writeFile(t, preplanDir(root), "not a folder")
	list, warnings := dashboardPreplans(root)
	if list == nil || len(list) != 0 {
		t.Errorf("list = %#v, want empty and not nil", list)
	}
	if len(warnings) != 1 || !strings.HasPrefix(warnings[0], "Preplan folder not read: ") {
		t.Errorf("warnings = %v, want one \"Preplan folder not read: ...\"", warnings)
	}
}

// TestDashboardPreplans_SkipsOtherEntries checks that a non-.md file, a file
// named only ".md" (its slug would be empty, and the delete route refuses an
// empty slug), a sub-folder and a folder named like a topic file are skipped
// with no warning.
func TestDashboardPreplans_SkipsOtherEntries(t *testing.T) {
	root := t.TempDir()
	preplanWrite(t, root, "keep", preplanTopicFile("keep", preplanStatusPaused), dashNow)
	writeFile(t, filepath.Join(preplanDir(root), "notes.txt"), "text")
	writeFile(t, filepath.Join(preplanDir(root), ".md"), "# Preplan: no slug\n")
	writeFile(t, filepath.Join(preplanDir(root), "sub", "inner.md"), "# Preplan: inner\n")
	if err := os.MkdirAll(filepath.Join(preplanDir(root), "folder.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	list, warnings := dashboardPreplans(root)
	if got, want := preplanSlugs(list), []string{"keep"}; !reflect.DeepEqual(got, want) {
		t.Errorf("slugs = %v, want %v", got, want)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %v, want none", warnings)
	}
}

// TestDashboardPreplans_StatFails checks that a topic file whose Stat fails (a
// dangling link) is skipped with the "Preplan <slug> not read" warning, while
// the other files stay in the list.
func TestDashboardPreplans_StatFails(t *testing.T) {
	root := t.TempDir()
	preplanWrite(t, root, "good", preplanTopicFile("good", preplanStatusInProgress), dashNow)
	if err := os.Symlink(filepath.Join(root, "missing-target"), filepath.Join(preplanDir(root), "broken.md")); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}
	list, warnings := dashboardPreplans(root)
	if got, want := preplanSlugs(list), []string{"good"}; !reflect.DeepEqual(got, want) {
		t.Errorf("slugs = %v, want %v", got, want)
	}
	if len(warnings) != 1 || !strings.HasPrefix(warnings[0], "Preplan broken not read: ") {
		t.Errorf("warnings = %v, want one \"Preplan broken not read: ...\"", warnings)
	}
}

// TestDashboardPreplans_HeadReadFails checks that a topic file that Stat finds
// but the head read cannot open is skipped with the "Preplan <slug> not read"
// warning.
func TestDashboardPreplans_HeadReadFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("a file mode does not stop the root user")
	}
	root := t.TempDir()
	preplanWrite(t, root, "good", preplanTopicFile("good", preplanStatusInProgress), dashNow)
	locked := preplanWrite(t, root, "locked", preplanTopicFile("locked", preplanStatusPaused), dashNow)
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o644) })

	list, warnings := dashboardPreplans(root)
	if got, want := preplanSlugs(list), []string{"good"}; !reflect.DeepEqual(got, want) {
		t.Errorf("slugs = %v, want %v", got, want)
	}
	if len(warnings) != 1 || !strings.HasPrefix(warnings[0], "Preplan locked not read: ") {
		t.Errorf("warnings = %v, want one \"Preplan locked not read: ...\"", warnings)
	}
}

// TestDashboardPreplans_CapsAtLimitNewestFirst checks that more than
// dashboardPreplanLimit files give the newest dashboardPreplanLimit and one
// warning that counts the files left out.
func TestDashboardPreplans_CapsAtLimitNewestFirst(t *testing.T) {
	root := t.TempDir()
	total := dashboardPreplanLimit + 7
	for i := 0; i < total; i++ {
		slug := fmt.Sprintf("topic-%03d", i)
		preplanWrite(t, root, slug, preplanTopicFile(slug, preplanStatusInProgress), dashNow.Add(time.Duration(i)*time.Minute))
	}
	list, warnings := dashboardPreplans(root)
	if len(list) != dashboardPreplanLimit {
		t.Fatalf("rows = %d, want %d", len(list), dashboardPreplanLimit)
	}
	if want := fmt.Sprintf("topic-%03d", total-1); list[0].Slug != want {
		t.Errorf("first slug = %s, want %s", list[0].Slug, want)
	}
	if want := fmt.Sprintf("topic-%03d", total-dashboardPreplanLimit); list[len(list)-1].Slug != want {
		t.Errorf("last slug = %s, want %s", list[len(list)-1].Slug, want)
	}
	if want := []string{"7 older preplan topic files not shown"}; !reflect.DeepEqual(warnings, want) {
		t.Errorf("warnings = %v, want %v", warnings, want)
	}
}

// TestDashboardPreplans_TopicFallsBackToSlug checks that a file with no
// "# Preplan:" line, or with an empty one, shows its slug as the topic.
func TestDashboardPreplans_TopicFallsBackToSlug(t *testing.T) {
	root := t.TempDir()
	preplanWrite(t, root, "my_topic", "just notes\n\n**Status:** paused\n", dashNow)
	preplanWrite(t, root, "empty-topic", "# Preplan:   \n\n**Status:** paused\n", dashNow)
	list, warnings := dashboardPreplans(root)
	if len(warnings) != 0 {
		t.Errorf("warnings = %v, want none", warnings)
	}
	got := map[string]string{}
	for _, p := range list {
		got[p.Slug] = p.Topic
	}
	want := map[string]string{"my_topic": "my_topic", "empty-topic": "empty-topic"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("topics = %v, want %v", got, want)
	}
}

// TestDashboardPreplans_NoStatusLine checks that a file with no "**Status:**"
// line in its head has status "", also when the line sits past the head.
func TestDashboardPreplans_NoStatusLine(t *testing.T) {
	root := t.TempDir()
	preplanWrite(t, root, "bare", "# Preplan: bare\n\n## Goal\n", dashNow)
	past := "# Preplan: past\n\n" + strings.Repeat("x", dashboardPreplanHeadMax) + "\n**Status:** paused\n"
	preplanWrite(t, root, "past", past, dashNow)
	list, warnings := dashboardPreplans(root)
	if len(warnings) != 0 {
		t.Errorf("warnings = %v, want none", warnings)
	}
	if len(list) != 2 {
		t.Fatalf("rows = %d, want 2", len(list))
	}
	for _, p := range list {
		if p.Status != "" {
			t.Errorf("%s status = %q, want empty", p.Slug, p.Status)
		}
	}
}

// TestDashboardPreplans_MapsFieldsNewestFirst checks every field of a row, the
// newest-first order, and the order of equal times by slug.
func TestDashboardPreplans_MapsFieldsNewestFirst(t *testing.T) {
	root := t.TempDir()
	same := time.Date(2026, 10, 10, 9, 0, 0, 0, time.UTC)
	preplanWrite(t, root, "auth-flow", preplanTopicFile("auth flow", preplanStatusInProgress)+"\n**Started:** 2026-10-10T07:40:00Z\n", same)
	preplanWrite(t, root, "zeta", preplanTopicFile("zeta", preplanStatusReadyForPlan), same.Add(-time.Hour))
	preplanWrite(t, root, "beta", preplanTopicFile("beta", preplanStatusPaused), same)
	preplanWrite(t, root, "alpha", preplanTopicFile("alpha", preplanStatusPaused), same)
	preplanWrite(t, root, "newest", preplanTopicFile("newest", "other words"), same.Add(time.Hour))

	list, warnings := dashboardPreplans(root)
	if len(warnings) != 0 {
		t.Errorf("warnings = %v, want none", warnings)
	}
	if got, want := preplanSlugs(list), []string{"newest", "alpha", "auth-flow", "beta", "zeta"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
	want := DashboardPreplan{
		Slug:       "auth-flow",
		Topic:      "auth flow",
		Status:     "in progress",
		Path:       ".sdlc-v2/preplan/auth-flow.md",
		UpdatedAt:  "2026-10-10T09:00:00Z",
		DurationMs: 4800000,
	}
	if !reflect.DeepEqual(list[2], want) {
		t.Errorf("auth-flow row = %+v, want %+v", list[2], want)
	}
	if list[0].Status != "other words" {
		t.Errorf("newest status = %q, want the raw text %q", list[0].Status, "other words")
	}
	for _, p := range list {
		if p.Slug != "auth-flow" && p.DurationMs != 0 {
			t.Errorf("%s DurationMs = %d, want 0 for a file with no Started line", p.Slug, p.DurationMs)
		}
	}
}

// TestDashboardPreplans_DurationFromStartedLine checks the duration of a topic
// file for each kind of Started line: a start before the change time gives the
// difference in milliseconds, a start at or after the change time and a missing
// line give 0 and no warning, a text that is not RFC 3339 gives 0 and one
// warning that quotes the text, and a line past the head read gives 0.
func TestDashboardPreplans_DurationFromStartedLine(t *testing.T) {
	mod := time.Date(2026, 10, 10, 9, 0, 0, 0, time.UTC)
	head := preplanTopicFile("auth flow", preplanStatusInProgress)
	tests := []struct {
		name         string
		content      string
		wantDuration int64
		wantWarnings []string
	}{
		{"start before change", head + "\n**Started:** 2026-10-10T07:40:00Z\n", 4800000, nil},
		{"start with an offset", head + "\n**Started:** 2026-10-10T09:40:00+02:00\n", 4800000, nil},
		{"start with fraction", head + "\n**Started:** 2026-10-10T08:59:59.5Z\n", 500, nil},
		{"start equals change", head + "\n**Started:** 2026-10-10T09:00:00Z\n", 0, nil},
		{"start after change", head + "\n**Started:** 2026-10-10T10:00:00Z\n", 0, nil},
		{"no Started line", head, 0, nil},
		{"CR LF line ends", strings.ReplaceAll(head+"\n**Started:** 2026-10-10T07:40:00Z\n", "\n", "\r\n"), 4800000, nil},
		{"text that is not a time", head + "\n**Started:** yesterday\n", 0, []string{`Preplan auth-flow start time not read: "yesterday" (expected RFC 3339; duration shown as 0)`}},
		{"empty value", head + "\n**Started:**\n", 0, []string{`Preplan auth-flow start time not read: "" (expected RFC 3339; duration shown as 0)`}},
		{"date with no time", head + "\n**Started:** 2026-10-10\n", 0, []string{`Preplan auth-flow start time not read: "2026-10-10" (expected RFC 3339; duration shown as 0)`}},
		{"line past the head read", head + strings.Repeat("x", dashboardPreplanHeadMax) + "\n**Started:** 2026-10-10T07:40:00Z\n", 0, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			preplanWrite(t, root, "auth-flow", tc.content, mod)
			list, warnings := dashboardPreplans(root)
			if len(list) != 1 {
				t.Fatalf("rows = %v, want 1 (a bad Started line keeps the row)", list)
			}
			if list[0].DurationMs != tc.wantDuration {
				t.Errorf("DurationMs = %d, want %d", list[0].DurationMs, tc.wantDuration)
			}
			if !reflect.DeepEqual(warnings, tc.wantWarnings) {
				t.Errorf("warnings = %v, want %v", warnings, tc.wantWarnings)
			}
		})
	}
}

// TestDashboardPreplans_StartedWarningIsRedacted checks that a text that the
// redactor covers (an email address has no space, so the one-word Started
// value holds it whole) never reaches the warning text of a bad Started line.
func TestDashboardPreplans_StartedWarningIsRedacted(t *testing.T) {
	root := t.TempDir()
	const secret = "alice@example.com"
	preplanWrite(t, root, "leak", preplanTopicFile("leak", preplanStatusPaused)+"\n**Started:** "+secret+"\n", dashNow)
	list, warnings := dashboardPreplans(root)
	if len(list) != 1 {
		t.Fatalf("rows = %d, want 1", len(list))
	}
	if len(warnings) != 1 || !strings.HasPrefix(warnings[0], "Preplan leak start time not read: ") {
		t.Fatalf("warnings = %v, want one \"Preplan leak start time not read: ...\"", warnings)
	}
	if strings.Contains(warnings[0], secret) {
		t.Errorf("warning = %q, want the secret redacted", warnings[0])
	}
}

// TestDashboardPreplans_TopicAndStatusAreRedacted checks that a secret in the
// topic line or the status line of a topic file never reaches the snapshot.
// Topic files are free user text.
func TestDashboardPreplans_TopicAndStatusAreRedacted(t *testing.T) {
	root := t.TempDir()
	const secret = "verysecrettoken123"
	preplanWrite(t, root, "leak", preplanTopicFile("call the API with Bearer "+secret, "Bearer "+secret), dashNow)
	list, warnings := dashboardPreplans(root)
	if len(warnings) != 0 {
		t.Errorf("warnings = %v, want none", warnings)
	}
	if len(list) != 1 {
		t.Fatalf("rows = %d, want 1", len(list))
	}
	if strings.Contains(list[0].Topic, secret) || strings.Contains(list[0].Status, secret) {
		t.Errorf("row = %+v, want the secret redacted from the topic and the status", list[0])
	}
	if !strings.HasPrefix(list[0].Topic, "call the API with ") {
		t.Errorf("topic = %q, want the text around the secret kept", list[0].Topic)
	}
}

// TestDashboardPreplans_HeadCutInsideRuneIsValidUTF8 checks that a head cut
// at dashboardPreplanHeadMax bytes inside a multi-byte character drops the
// broken bytes, so the topic and the status are valid UTF-8.
func TestDashboardPreplans_HeadCutInsideRuneIsValidUTF8(t *testing.T) {
	root := t.TempDir()
	// "# Preplan: " is 11 bytes and "é" is 2 bytes, so an odd byte limit of
	// the head from the topic start ends inside an "é".
	topic := strings.Repeat("é", dashboardPreplanHeadMax)
	preplanWrite(t, root, "wide", "# Preplan: "+topic+"\n", dashNow)
	head, err := dashboardPreplanHead(filepath.Join(preplanDir(root), "wide.md"))
	if err != nil {
		t.Fatalf("dashboardPreplanHead: %v", err)
	}
	if !utf8.ValidString(head) {
		t.Errorf("head is not valid UTF-8")
	}
	if len(head) != dashboardPreplanHeadMax-1 {
		t.Errorf("head length = %d bytes, want %d (one broken byte dropped)", len(head), dashboardPreplanHeadMax-1)
	}
	list, _ := dashboardPreplans(root)
	if len(list) != 1 || !utf8.ValidString(list[0].Topic) {
		t.Errorf("list = %+v, want one row with a valid UTF-8 topic", list)
	}
}

// TestDashboardPreplans_FileGoneDuringSnapshotGivesNoWarning checks that a
// topic file that is deleted after ReadDir is skipped with no warning, while
// a dangling link keeps its warning (TestDashboardPreplans_StatFails).
func TestDashboardPreplans_FileGoneDuringSnapshotGivesNoWarning(t *testing.T) {
	root := t.TempDir()
	gone := preplanWrite(t, root, "gone", preplanTopicFile("gone", preplanStatusPaused), dashNow)
	statErr := &os.PathError{Op: "stat", Path: gone, Err: os.ErrNotExist}
	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}
	if !dashboardPreplanGone(gone, statErr) {
		t.Errorf("dashboardPreplanGone(removed file) = false, want true")
	}
	if dashboardPreplanGone(gone, os.ErrPermission) {
		t.Errorf("dashboardPreplanGone(permission error) = true, want false")
	}
	link := filepath.Join(preplanDir(root), "dangling.md")
	if err := os.Symlink(filepath.Join(root, "missing-target"), link); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}
	if dashboardPreplanGone(link, statErr) {
		t.Errorf("dashboardPreplanGone(dangling link) = true, want false")
	}
}

// TestDashboardPreplans_TopicIsTruncated checks that a long topic is cut like
// the text of a timeline event.
func TestDashboardPreplans_TopicIsTruncated(t *testing.T) {
	root := t.TempDir()
	long := strings.Repeat("a", dashboardPreviewTextMax+30)
	preplanWrite(t, root, "long", preplanTopicFile(long, preplanStatusInProgress), dashNow)
	list, _ := dashboardPreplans(root)
	if len(list) != 1 {
		t.Fatalf("rows = %d, want 1", len(list))
	}
	if want := strings.Repeat("a", dashboardPreviewTextMax) + "…"; list[0].Topic != want {
		t.Errorf("topic = %q, want %d runes plus the ellipsis", list[0].Topic, dashboardPreviewTextMax)
	}
}

// TestDashboardPreplans_ListsFileThatPreplanContextWrites checks that the
// skeleton that preplan_context writes shows in the list with the dated slug,
// the topic, the status "in progress" and the duration from its Started line.
func TestDashboardPreplans_ListsFileThatPreplanContextWrites(t *testing.T) {
	setPreplanNow(t, time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC))
	root := newOpenspecStageFixture(t, "")
	if _, err := planSupportCore(root, root, PlanSupportIn{Action: "preplan_context", Topic: "auth flow"}); err != nil {
		t.Fatalf("preplan_context: %v", err)
	}
	file := filepath.Join(root, ".sdlc-v2", "preplan", "2026-10-12-auth-flow.md")
	changed := time.Date(2026, 10, 12, 13, 20, 0, 0, time.UTC)
	if err := os.Chtimes(file, changed, changed); err != nil {
		t.Fatalf("set the change time: %v", err)
	}
	list, warnings := dashboardPreplans(root)
	if len(warnings) != 0 {
		t.Errorf("warnings = %v, want none", warnings)
	}
	if len(list) != 1 {
		t.Fatalf("rows = %v, want 1", list)
	}
	if list[0].Slug != "2026-10-12-auth-flow" || list[0].Topic != "auth flow" || list[0].Status != preplanStatusInProgress {
		t.Errorf("row = %+v, want slug 2026-10-12-auth-flow, topic \"auth flow\", status %q", list[0], preplanStatusInProgress)
	}
	if list[0].DurationMs != 4800000 {
		t.Errorf("DurationMs = %d, want 4800000 (Started 12:00, changed 13:20)", list[0].DurationMs)
	}
}

// TestDashboardPreplans_SnapshotRepoFields checks that collectDashboardRepo
// fills Preplans, and that a repo with no preplan folder encodes both new
// lists as [] and never as null.
func TestDashboardPreplans_SnapshotRepoFields(t *testing.T) {
	t.Run("lists the topic files", func(t *testing.T) {
		root := dashRoot(t)
		preplanWrite(t, root, "auth-flow", preplanTopicFile("auth flow", preplanStatusInProgress), dashNow)
		repo := collectDashboardRepo(root, dashNow)
		if repo.Error != "" {
			t.Errorf("Error = %q, want empty", repo.Error)
		}
		if got, want := preplanSlugs(repo.Preplans), []string{"auth-flow"}; !reflect.DeepEqual(got, want) {
			t.Errorf("preplans = %v, want %v", got, want)
		}
		if len(repo.Warnings) != 0 {
			t.Errorf("Warnings = %v, want none", repo.Warnings)
		}
	})
	for name, setup := range map[string]func(t *testing.T, root string){
		"no folder":         func(t *testing.T, root string) {},
		"unreadable folder": func(t *testing.T, root string) { writeFile(t, preplanDir(root), "not a folder") },
	} {
		t.Run("encodes empty lists: "+name, func(t *testing.T) {
			root := dashRoot(t)
			setup(t, root)
			repo := collectDashboardRepo(root, dashNow)
			b, err := json.Marshal(repo)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(b), `"preplans":[]`) {
				t.Errorf("json has no \"preplans\":[]: %s", b)
			}
			if name == "no folder" && !strings.Contains(string(b), `"warnings":[]`) {
				t.Errorf("json has no \"warnings\":[]: %s", b)
			}
			if strings.Contains(string(b), `"preplans":null`) || strings.Contains(string(b), `"warnings":null`) {
				t.Errorf("json holds null for a new list: %s", b)
			}
		})
	}
}

// TestDashboardPreplans_UnreadableFolderKeepsArchiveWorking checks that an
// unreadable preplan folder adds a warning, leaves repo.Error empty,
// and ArchiveRun of a run in the same repo still succeeds.
func TestDashboardPreplans_UnreadableFolderKeepsArchiveWorking(t *testing.T) {
	root := archShipFixture(t, PipelineCompleted, dashJoinFresh)
	writeFile(t, preplanDir(root), "not a folder")

	repo := collectDashboardRepo(root, dashNow)
	if repo.Error != "" {
		t.Errorf("repo.Error = %q, want empty", repo.Error)
	}
	if len(repo.Warnings) != 1 || !strings.HasPrefix(repo.Warnings[0], "Preplan folder not read: ") {
		t.Errorf("repo.Warnings = %v, want one \"Preplan folder not read: ...\"", repo.Warnings)
	}
	if repo.Preplans == nil || len(repo.Preplans) != 0 {
		t.Errorf("repo.Preplans = %#v, want empty and not nil", repo.Preplans)
	}

	if _, err := archRun(root, archShipID, false); err != nil {
		t.Fatalf("ArchiveRun with an unreadable preplan folder: %v", err)
	}
	if _, err := os.Stat(archDir(root, archShipID)); err != nil {
		t.Errorf("archive folder missing after the archive: %v", err)
	}
}

// preplanStatusesFromSkill returns the distinct values of the "To" column of
// the "Topic file status" table in the text of the preplan SKILL.md, sorted.
func preplanStatusesFromSkill(t *testing.T, md string) []string {
	t.Helper()
	_, section, ok := strings.Cut(md, "### Topic file status")
	if !ok {
		t.Fatal("SKILL.md has no \"### Topic file status\" heading")
	}
	seen := map[string]bool{}
	for _, line := range strings.Split(section, "\n") {
		line = strings.TrimSpace(line)
		if line == "---" || strings.HasPrefix(line, "## ") {
			break
		}
		if !strings.HasPrefix(line, "|") {
			continue
		}
		cells := strings.Split(strings.Trim(line, "|"), "|")
		last := strings.TrimSpace(cells[len(cells)-1])
		if last == "To" || strings.Trim(last, "-") == "" {
			continue
		}
		seen[strings.Trim(last, "`")] = true
	}
	out := make([]string, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// TestDashboardPreplans_StatusParity checks that the status list of the
// preplan SKILL.md equals PreplanStatuses, and that the check sees a status
// that is changed, removed or added in the SKILL.md text.
func TestDashboardPreplans_StatusParity(t *testing.T) {
	b, err := os.ReadFile("../../plugins/sdlc/skills/preplan/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	want := append([]string(nil), PreplanStatuses...)
	sort.Strings(want)

	if got := preplanStatusesFromSkill(t, string(b)); !reflect.DeepEqual(got, want) {
		t.Errorf("SKILL.md statuses = %v, PreplanStatuses = %v", got, want)
	}

	md := string(b)
	for name, drifted := range map[string]string{
		"status renamed": strings.ReplaceAll(md, "`paused`", "`stopped`"),
		"status added":   strings.Replace(md, "| `paused` or `ready for plan` | Step 2 on a new run | `in progress` |", "| `paused` or `ready for plan` | Step 2 on a new run | `in progress` |\n| `in progress` | archive | `archived` |", 1),
	} {
		if drifted == md {
			t.Fatalf("%s: the drift edit changed nothing; update the test to the SKILL.md table", name)
		}
		if got := preplanStatusesFromSkill(t, drifted); reflect.DeepEqual(got, want) {
			t.Errorf("%s: the parity check did not see the change", name)
		}
	}
}

// ---------------------------------------------------------------------------
// Topic-file summary (dashboardPreplanSummaries)
// ---------------------------------------------------------------------------

// summaryTestSlug is the slug of the topic file that the summary tests write.
const summaryTestSlug = "auth-flow"

// summaryTestCheckLine is a Guardrail check line whose Last check time is
// 2026-10-29T07:58:25Z.
const summaryTestCheckLine = "**Guardrail check:** 12 guardrails, 5 proposals: 3 pass, 1 kept, 1 dropped. Last check: 2026-10-29T07:58:25Z.\n"

// summaryTestGoal is the ## Goal body of summaryTopic. Its first sentence is
// "Start the dashboard when the user logs in."
const summaryTestGoal = "Start the dashboard when the user logs in. Then show the pipelines.\n"

// summaryTestOpenQuestions is the ## Open questions section of summaryTopic,
// with 2 bullets. It is the last section of the file.
const summaryTestOpenQuestions = "## Open questions\n\n- Which port?\n- Which user?\n"

// summaryTestDecisions is a valid ## Decisions body: 3 live rows (row 2 is
// kept), 1 dropped row, 1 moved row and the line "Smallest cut: #1.".
var summaryTestDecisions = formatRows(
	"| 1 | pick A | because |",
	"| 2 | pick B | Kept with warning: x breaks y |",
	"| 3 | pick C | because |",
	"| 4 | Dropped: pick D | Dropped by: guardrail z |",
	"| 5 | Moved: pick E | moved to another topic |",
) + "\nSmallest cut: #1.\n"

// summaryTopic returns a valid topic file: the topic line, the Status line,
// summaryTestCheckLine, every skeleton section (summaryTestGoal,
// summaryTestDecisions, an empty ## Flows, "text" elsewhere, then the bodies
// of over), and summaryTestOpenQuestions at the end.
func summaryTopic(over map[string]string) string {
	bodies := map[string]string{
		preplanGoalSection:      summaryTestGoal,
		preplanDecisionsSection: summaryTestDecisions,
		preplanFlowsSection:     "",
	}
	maps.Copy(bodies, over)
	text := preplanTopicWithBodies(bodies)
	text = strings.Replace(text, "**Status:** in progress\n", "**Status:** in progress\n"+summaryTestCheckLine, 1)
	return "# Preplan: auth flow\n\n" + text + summaryTestOpenQuestions
}

// summaryTopicOfSize returns summaryTopic with the ## Problem and benefit
// body padded so that the file is exactly n bytes. That section has no rule
// of its own, so the padding breaks no rule.
func summaryTopicOfSize(t *testing.T, n int) string {
	t.Helper()
	const mark = "@pad@"
	base := summaryTopic(map[string]string{"Problem and benefit": mark + "\n"})
	pad := n - (len(base) - len(mark))
	if pad < 0 {
		t.Fatalf("a topic file of %d bytes is too small for the base text of %d bytes", n, len(base))
	}
	return strings.Replace(base, mark, strings.Repeat("x", pad), 1)
}

// summaryTopicWithout returns summaryTopic(over) without the text cut. It
// fails the test when cut is not in the file.
func summaryTopicWithout(t *testing.T, over map[string]string, cut string) string {
	t.Helper()
	text := summaryTopic(over)
	if !strings.Contains(text, cut) {
		t.Fatalf("the topic file holds no %q", cut)
	}
	return strings.Replace(text, cut, "", 1)
}

// summaryTestRow returns the head-only row of summaryTestSlug, as
// dashboardPreplans gives it.
func summaryTestRow() DashboardPreplan {
	return DashboardPreplan{Slug: summaryTestSlug, Path: path.Join(paths.DataDir, paths.PreplanSubdir, summaryTestSlug+".md")}
}

// summaryRun creates the preplan folder of a new root, calls setup with the
// topic file path, then runs dashboardPreplanSummaries on summaryTestRow. It
// returns the filled row, the warnings and the file path.
func summaryRun(t *testing.T, setup func(t *testing.T, file string)) (row DashboardPreplan, warnings []string, file string) {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(preplanDir(root), 0o755); err != nil {
		t.Fatal(err)
	}
	file = filepath.Join(preplanDir(root), summaryTestSlug+".md")
	setup(t, file)
	list := []DashboardPreplan{summaryTestRow()}
	warnings = dashboardPreplanSummaries(root, list)
	return list[0], warnings, file
}

// summaryWrite returns a setup that writes text to the topic file.
func summaryWrite(text string) func(t *testing.T, file string) {
	return func(t *testing.T, file string) { writeFile(t, file, text) }
}

// summaryWarning returns the summary warning of summaryTestSlug for cause.
func summaryWarning(cause string) string {
	return "Preplan " + summaryTestSlug + " summary not read: " + cause
}

// summaryNotRegularWarning is the warning of a link, a folder, a pipe or a
// device at the topic file path.
func summaryNotRegularWarning(*testing.T, string) string {
	return summaryWarning("not a regular file.")
}

// summaryNoWarning is the warning func of a case that gives no warning.
func summaryNoWarning(*testing.T, string) string { return "" }

// summaryLink returns a setup that puts a link at the topic file path. With
// live true the link points at a valid topic file, else at a missing file.
func summaryLink(live bool) func(t *testing.T, file string) {
	return func(t *testing.T, file string) {
		target := filepath.Join(t.TempDir(), "target.md")
		if live {
			writeFile(t, target, summaryTopic(nil))
		}
		if err := os.Symlink(target, file); err != nil {
			t.Fatal(err)
		}
	}
}

// summaryFolder is a setup that puts a folder at the topic file path. A pipe
// or a device takes the same !IsRegular branch; this test file builds on
// every platform, so it uses a folder only.
func summaryFolder(t *testing.T, file string) {
	if err := os.Mkdir(file, 0o755); err != nil {
		t.Fatal(err)
	}
}

// summaryGone is a setup that leaves the topic file path empty: the file
// went away after the head read.
func summaryGone(*testing.T, string) {}

// summaryGoneWarning is the warning of a topic file that went away: the
// os.Lstat error.
func summaryGoneWarning(t *testing.T, file string) string {
	_, err := os.Lstat(file)
	if err == nil {
		t.Fatalf("Lstat %s found a file, want none", file)
	}
	return summaryWarning(err.Error())
}

// summaryOverLimit is a setup that writes a valid topic file of
// dashboardPreplanSummaryMax + 1 bytes.
func summaryOverLimit(t *testing.T, file string) {
	writeFile(t, file, summaryTopicOfSize(t, dashboardPreplanSummaryMax+1))
}

// summaryOverLimitWarning is the size warning of summaryOverLimit.
func summaryOverLimitWarning(*testing.T, string) string {
	return summaryWarning(fmt.Sprintf("the file is %d bytes, the limit is %d bytes.", dashboardPreplanSummaryMax+1, dashboardPreplanSummaryMax))
}

// summaryUnreadContract is the literal list of the unread names, in the
// order that a row gives them. The dashboard page reads these strings, so the
// tests pin the text here and not through the production constants.
var summaryUnreadContract = []string{"goal", "proposals", "smallestCut", "lastCheck", "openQuestions", "gaps"}

// summaryAllUnread checks the row of a topic file that was not read: all six
// names of summaryUnreadContract unread and every summary field at its
// unread value.
func summaryAllUnread(t *testing.T, row DashboardPreplan) {
	t.Helper()
	want := summaryTestRow()
	want.Gaps, want.SmallestCut, want.Unread = []string{}, dashboardPreplanCutNotSet, summaryUnreadContract
	if !reflect.DeepEqual(row, want) {
		t.Errorf("row = %+v, want %+v", row, want)
	}
}

// summaryLstatMode returns a setup that writes a valid topic file and makes
// the preplanLstat seam report mode for it, for example a pipe or a device.
// The seam serves preplan_check and the summary, and the case needs no
// Mkfifo, so it runs on every platform.
func summaryLstatMode(mode fs.FileMode) func(t *testing.T, file string) {
	return func(t *testing.T, file string) {
		writeFile(t, file, summaryTopic(nil))
		swapPreplanSeam(t, &preplanLstat, func(string) (fs.FileInfo, error) { return preplanFakeInfo{mode: mode}, nil })
	}
}

// summaryLstatError is a setup that writes a valid topic file and makes the
// preplanLstat seam fail with errInjectedPreplan.
func summaryLstatError(t *testing.T, file string) {
	writeFile(t, file, summaryTopic(nil))
	swapPreplanSeam(t, &preplanLstat, func(string) (fs.FileInfo, error) { return nil, errInjectedPreplan })
}

// summaryReadError is a setup that writes a valid topic file and makes the
// preplanReadFile seam fail with errInjectedPreplan, after a real Lstat.
func summaryReadError(t *testing.T, file string) {
	writeFile(t, file, summaryTopic(nil))
	swapPreplanSeam(t, &preplanReadFile, func(string) ([]byte, error) { return nil, errInjectedPreplan })
}

// summaryInjectedWarning is the warning of summaryLstatError and
// summaryReadError.
func summaryInjectedWarning(*testing.T, string) string {
	return summaryWarning(errInjectedPreplan.Error())
}

// summaryDistinctDecisions is a valid ## Decisions body with distinct counts
// and no Smallest cut line: 3 live rows (2 kept) and 3 dropped rows.
var summaryDistinctDecisions = formatRows(
	"| 1 | pick A | because |",
	"| 2 | pick B | Kept with warning: x |",
	"| 3 | pick C | Kept with warning: y |",
	"| 4 | Dropped: pick D | Dropped by: guardrail z |",
	"| 5 | Dropped: pick E | Dropped by: guardrail z |",
	"| 6 | Dropped: pick F | Dropped by: guardrail z |",
)

// summaryCase is one row of a summary test table. setup makes the file
// state at the topic file path. unread is the want of row.Unread. warning
// returns the one warning the case gives, or "" for none. check, when set,
// checks more fields of the row.
type summaryCase struct {
	name    string
	setup   func(t *testing.T, file string)
	unread  []string
	warning func(t *testing.T, file string) string
	check   func(t *testing.T, row DashboardPreplan)
}

// summaryConditionCases holds one case for each file state and section state
// that changes the unread names or the warning: a file over, and exactly at,
// dashboardPreplanSummaryMax bytes; a link, a folder, a pipe, a device; a file
// that went away; a failed Lstat or read; each section that breaks its rule;
// an absent check line or section; an open fence; a file with no final line
// feed. The head read failure has its own test
// (TestDashboardPreplanSummariesHeadReadFailsDropsRow), and so does the
// window of the check line (TestDashboardPreplanSummariesCheckLineHeadWindow).
var summaryConditionCases = []summaryCase{
	{"file over the limit", summaryOverLimit, summaryUnreadContract, summaryOverLimitWarning, summaryAllUnread},
	{"file of exactly the limit", func(t *testing.T, file string) {
		writeFile(t, file, summaryTopicOfSize(t, dashboardPreplanSummaryMax))
	}, []string{}, summaryNoWarning, func(t *testing.T, row DashboardPreplan) {
		if row.Live != 3 || row.OpenQuestions != 2 {
			t.Errorf("live %d, openQuestions %d, want 3 and 2: the whole file is read", row.Live, row.OpenQuestions)
		}
	}},
	{"live link", summaryLink(true), summaryUnreadContract, summaryNotRegularWarning, summaryAllUnread},
	{"dangling link", summaryLink(false), summaryUnreadContract, summaryNotRegularWarning, summaryAllUnread},
	{"folder", summaryFolder, summaryUnreadContract, summaryNotRegularWarning, summaryAllUnread},
	{"pipe", summaryLstatMode(fs.ModeNamedPipe | 0o644), summaryUnreadContract, summaryNotRegularWarning, summaryAllUnread},
	{"device", summaryLstatMode(fs.ModeDevice | 0o644), summaryUnreadContract, summaryNotRegularWarning, summaryAllUnread},
	{"file went away", summaryGone, summaryUnreadContract, summaryGoneWarning, summaryAllUnread},
	{"Lstat error", summaryLstatError, summaryUnreadContract, summaryInjectedWarning, summaryAllUnread},
	{"full read I/O error", summaryReadError, summaryUnreadContract, summaryInjectedWarning, summaryAllUnread},
	{"Goal breaks P2", summaryWrite(summaryTopic(map[string]string{preplanGoalSection: "# A heading\nText.\n"})),
		[]string{dashboardPreplanUnreadGoal}, summaryNoWarning, func(t *testing.T, row DashboardPreplan) {
			if row.Goal != "" {
				t.Errorf("goal = %q, want empty for an unread Goal", row.Goal)
			}
		}},
	// An unclosed fence in the Goal body runs to the end of the file: every
	// later heading is code, so the Goal body breaks P3 and the fence is
	// still open at the end. The Decisions and Open questions headings are
	// hidden in the fence, so their names and gaps are unread too.
	{"Goal breaks P3", summaryWrite(summaryTopic(map[string]string{preplanGoalSection: "```\nopen fence\n"})),
		[]string{dashboardPreplanUnreadGoal, dashboardPreplanUnreadProposals, dashboardPreplanUnreadSmallestCut,
			dashboardPreplanUnreadOpenQuestions, dashboardPreplanUnreadGaps}, summaryNoWarning, nil},
	{"fence open before Decisions hides Decisions and Open questions",
		summaryWrite(summaryTopic(map[string]string{"Problem and benefit": "```\ncode\n"})),
		[]string{dashboardPreplanUnreadProposals, dashboardPreplanUnreadSmallestCut, dashboardPreplanUnreadOpenQuestions, dashboardPreplanUnreadGaps},
		summaryNoWarning, func(t *testing.T, row DashboardPreplan) {
			if row.Goal == "" || row.Live != 0 || row.SmallestCut != dashboardPreplanCutNotSet || row.OpenQuestions != 0 {
				t.Errorf("row = %+v, want the goal shown, live 0, smallestCut %q, openQuestions 0", row, dashboardPreplanCutNotSet)
			}
		}},
	{"fence open after Decisions hides only Open questions",
		summaryWrite(strings.Replace(summaryTopic(nil), summaryTestOpenQuestions, "## Notes\n\n```\ncode\n"+summaryTestOpenQuestions, 1)),
		[]string{dashboardPreplanUnreadOpenQuestions, dashboardPreplanUnreadGaps}, summaryNoWarning, func(t *testing.T, row DashboardPreplan) {
			if row.Live != 3 || row.SmallestCut != dashboardPreplanCutSet || row.OpenQuestions != 0 {
				t.Errorf("row = %+v, want live 3, smallestCut %q, openQuestions 0", row, dashboardPreplanCutSet)
			}
		}},
	{"fence open in the head hides Goal",
		summaryWrite(strings.Replace(summaryTopic(nil), summaryTestCheckLine, summaryTestCheckLine+"```\n", 1)),
		[]string{dashboardPreplanUnreadGoal, dashboardPreplanUnreadProposals, dashboardPreplanUnreadSmallestCut,
			dashboardPreplanUnreadOpenQuestions, dashboardPreplanUnreadGaps}, summaryNoWarning, func(t *testing.T, row DashboardPreplan) {
			if row.Goal != "" || row.LastCheckAt == "" {
				t.Errorf("goal %q, lastCheckAt %q, want no goal and the check time read from the head", row.Goal, row.LastCheckAt)
			}
		}},
	{"Decisions with distinct counts and no Smallest cut line", summaryWrite(summaryTopic(map[string]string{preplanDecisionsSection: summaryDistinctDecisions})),
		[]string{}, summaryNoWarning, func(t *testing.T, row DashboardPreplan) {
			got := [4]any{row.Live, row.Kept, row.Dropped, row.SmallestCut}
			if want := [4]any{3, 2, 3, dashboardPreplanCutNotSet}; got != want {
				t.Errorf("live, kept, dropped, smallestCut = %v, want %v", got, want)
			}
		}},
	{"Decisions breaks P4", summaryWrite(summaryTopic(map[string]string{preplanDecisionsSection: "not a table\n"})),
		[]string{dashboardPreplanUnreadProposals, dashboardPreplanUnreadSmallestCut}, summaryNoWarning, func(t *testing.T, row DashboardPreplan) {
			if row.Live != 0 || row.SmallestCut != dashboardPreplanCutNotSet {
				t.Errorf("live %d, smallestCut %q, want 0 and %q", row.Live, row.SmallestCut, dashboardPreplanCutNotSet)
			}
		}},
	{"no check line", summaryWrite(strings.Replace(summaryTopic(nil), summaryTestCheckLine, "", 1)),
		[]string{dashboardPreplanUnreadLastCheck}, summaryNoWarning, nil},
	{"legacy Guardrail check section", summaryWrite(summaryTopic(nil) + "\n## Guardrail check\n\n- 12 guardrails, all pass\n"),
		[]string{dashboardPreplanUnreadLastCheck}, summaryNoWarning, func(t *testing.T, row DashboardPreplan) {
			if row.LastCheckAt != "" {
				t.Errorf("lastCheckAt = %q, want empty beside a legacy section", row.LastCheckAt)
			}
		}},
	{"Open questions breaks P9", summaryWrite(strings.Replace(summaryTopic(nil), "- Which user?", "Which user?", 1)),
		[]string{dashboardPreplanUnreadOpenQuestions}, summaryNoWarning, nil},
	{"code fence open at the end of the file", summaryWrite(summaryTopic(nil) + "\n## Notes\n\n```\ncode\n"),
		[]string{dashboardPreplanUnreadGaps}, summaryNoWarning, func(t *testing.T, row DashboardPreplan) {
			if row.Gaps == nil || len(row.Gaps) != 0 || row.OpenQuestions != 2 {
				t.Errorf("gaps %#v, openQuestions %d, want [] and 2", row.Gaps, row.OpenQuestions)
			}
		}},
	{"no Decisions heading", func(t *testing.T, file string) {
		writeFile(t, file, summaryTopicWithout(t, nil, "## Decisions\n\n"+summaryTestDecisions+"\n"))
	}, []string{}, summaryNoWarning, func(t *testing.T, row DashboardPreplan) {
		if !reflect.DeepEqual(row.Gaps, []string{preplanFlowsSection, preplanDecisionsSection}) ||
			row.Live+row.Kept+row.Dropped != 0 || row.SmallestCut != dashboardPreplanCutNotSet {
			t.Errorf("row = %+v, want gaps [Flows Decisions], counts 0, smallestCut %q", row, dashboardPreplanCutNotSet)
		}
	}},
	{"no Goal heading", func(t *testing.T, file string) {
		writeFile(t, file, summaryTopicWithout(t, nil, "## Goal\n\n"+summaryTestGoal+"\n"))
	}, []string{}, summaryNoWarning, func(t *testing.T, row DashboardPreplan) {
		if row.Goal != "" || !reflect.DeepEqual(row.Gaps, []string{preplanGoalSection, preplanFlowsSection}) {
			t.Errorf("goal %q, gaps %v, want empty and [Goal Flows]", row.Goal, row.Gaps)
		}
	}},
	{"no Open questions section", summaryWrite(strings.TrimSuffix(summaryTopic(nil), summaryTestOpenQuestions)),
		[]string{}, summaryNoWarning, func(t *testing.T, row DashboardPreplan) {
			if row.OpenQuestions != 0 {
				t.Errorf("openQuestions = %d, want 0 with no section", row.OpenQuestions)
			}
		}},
	{"Decisions table with no data row", summaryWrite(summaryTopic(map[string]string{preplanDecisionsSection: preplanDecisionsHeader})),
		[]string{}, summaryNoWarning, func(t *testing.T, row DashboardPreplan) {
			if !reflect.DeepEqual(row.Gaps, []string{preplanFlowsSection, preplanDecisionsSection}) || row.Live != 0 ||
				row.SmallestCut != dashboardPreplanCutNotSet {
				t.Errorf("gaps %v, live %d, smallestCut %q, want [Flows Decisions], 0 and %q", row.Gaps, row.Live, row.SmallestCut, dashboardPreplanCutNotSet)
			}
		}},
	{"CRLF line ends", summaryWrite(strings.ReplaceAll(summaryTopic(nil), "\n", "\r\n")),
		[]string{}, summaryNoWarning, func(t *testing.T, row DashboardPreplan) {
			if want := summaryAllFieldsRow(); !reflect.DeepEqual(row, want) {
				t.Errorf("row = %+v, want the LF row %+v", row, want)
			}
		}},
	{"last section has no final line feed", summaryWrite(strings.TrimSuffix(summaryTopic(nil), "\n")),
		[]string{}, summaryNoWarning, func(t *testing.T, row DashboardPreplan) {
			if row.OpenQuestions != 2 {
				t.Errorf("openQuestions = %d, want 2: the last bullet is on the last line", row.OpenQuestions)
			}
		}},
	{"Goal heading on the first line", summaryWrite("## Goal\n\n" + summaryTestGoal),
		[]string{dashboardPreplanUnreadLastCheck}, summaryNoWarning, func(t *testing.T, row DashboardPreplan) {
			if row.Goal != "Start the dashboard when the user logs in." {
				t.Errorf("goal = %q, want the first sentence", row.Goal)
			}
		}},
}

// TestDashboardPreplanSummariesConditions runs each case of
// summaryConditionCases: the unread names, the warning and the fields of
// the row.
func TestDashboardPreplanSummariesConditions(t *testing.T) {
	for _, c := range summaryConditionCases {
		t.Run(c.name, func(t *testing.T) {
			row, warnings, file := summaryRun(t, c.setup)
			if !reflect.DeepEqual(row.Unread, c.unread) {
				t.Errorf("unread = %#v, want %#v", row.Unread, c.unread)
			}
			if row.Gaps == nil || row.Unread == nil {
				t.Errorf("gaps %#v, unread %#v, want lists that are not nil", row.Gaps, row.Unread)
			}
			for _, name := range row.Unread {
				if !slices.Contains(summaryUnreadContract, name) {
					t.Errorf("unread name %q is not in the contract list %q", name, summaryUnreadContract)
				}
			}
			var want []string
			if w := c.warning(t, file); w != "" {
				want = []string{w}
			}
			if !reflect.DeepEqual(warnings, want) {
				t.Errorf("warnings = %q, want %q", warnings, want)
			}
			if c.check != nil {
				c.check(t, row)
			}
		})
	}
}

// TestDashboardPreplanSummariesReadSeamError checks that a failed full read
// (the seam dashboardPreplanReadSummary) marks all six names unread and
// gives the warning with the error text.
func TestDashboardPreplanSummariesReadSeamError(t *testing.T) {
	swapPreplanSeam(t, &dashboardPreplanReadSummary, func(string, int64) ([]byte, error) { return nil, errInjectedPreplan })
	row, warnings, _ := summaryRun(t, summaryWrite(summaryTopic(nil)))
	summaryAllUnread(t, row)
	if want := []string{summaryWarning(errInjectedPreplan.Error())}; !reflect.DeepEqual(warnings, want) {
		t.Errorf("warnings = %q, want %q", warnings, want)
	}
}

// TestDashboardPreplanSummariesReadSummaryErrors checks the errors of
// dashboardPreplanReadSummary: the size error carries the Stat size and the
// limit, and a folder gives errPreplanSummaryNotRegular.
func TestDashboardPreplanSummariesReadSummaryErrors(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "big.md")
	writeFile(t, file, strings.Repeat("x", 11))
	_, err := dashboardPreplanReadSummary(file, 10)
	var big *preplanSummaryTooBigError
	if !errors.As(err, &big) || big.size != 11 || big.max != 10 {
		t.Errorf("err = %#v, want *preplanSummaryTooBigError{size: 11, max: 10}", err)
	}
	if data, err := dashboardPreplanReadSummary(file, 11); err != nil || len(data) != 11 {
		t.Errorf("read at the limit = %d bytes, %v, want 11 bytes and no error", len(data), err)
	}
	if _, err := dashboardPreplanReadSummary(dir, 10); !errors.Is(err, errPreplanSummaryNotRegular) {
		t.Errorf("err = %v, want errPreplanSummaryNotRegular", err)
	}
}

// summaryAllFieldsRow returns the row of summaryTopic(nil): the Goal
// sentence, 3 live rows (1 kept), 1 dropped, a Smallest cut line, the check
// time, 2 open questions, an empty ## Flows and no unread name.
func summaryAllFieldsRow() DashboardPreplan {
	want := summaryTestRow()
	want.Goal = "Start the dashboard when the user logs in."
	want.Live, want.Kept, want.Dropped = 3, 1, 1
	want.SmallestCut = dashboardPreplanCutSet
	want.LastCheckAt = "2026-10-29T07:58:25Z"
	want.OpenQuestions = 2
	want.Gaps = []string{preplanFlowsSection}
	want.Unread = []string{}
	return want
}

// TestDashboardPreplanSummariesAllFields reads one valid topic file and
// checks every summary field: a Goal sentence, 3 live rows (1 kept), 1
// dropped and 1 moved row, a Smallest cut line, the check time, 2 open
// questions, an empty ## Flows and no unread name.
func TestDashboardPreplanSummariesAllFields(t *testing.T) {
	row, warnings, _ := summaryRun(t, summaryWrite(summaryTopic(nil)))
	if len(warnings) != 0 {
		t.Errorf("warnings = %q, want none", warnings)
	}
	if want := summaryAllFieldsRow(); !reflect.DeepEqual(row, want) {
		t.Errorf("row = %+v\nwant  %+v", row, want)
	}
}

// TestDashboardPreplanSummariesUnreadNames pins the text and the order of
// the unread names that the dashboard page reads.
func TestDashboardPreplanSummariesUnreadNames(t *testing.T) {
	if !reflect.DeepEqual(dashboardPreplanUnreadAll, summaryUnreadContract) {
		t.Errorf("dashboardPreplanUnreadAll = %q, want %q", dashboardPreplanUnreadAll, summaryUnreadContract)
	}
}

// summaryCheckLineAt returns summaryTopic(nil) with its check line moved so
// that the line starts at byte start of the file. A line of "x" after the
// Status line fills the bytes before it.
func summaryCheckLineAt(t *testing.T, start int) string {
	t.Helper()
	head, rest, ok := strings.Cut(summaryTopic(nil), summaryTestCheckLine)
	if !ok {
		t.Fatal("summaryTopic holds no check line")
	}
	head += "\n"
	pad := start - len(head) - 1
	if pad < 0 {
		t.Fatalf("the check line cannot start at byte %d: the head before it is %d bytes", start, len(head))
	}
	return head + strings.Repeat("x", pad) + "\n" + summaryTestCheckLine + rest
}

// summaryCheckLineLen is the length of summaryTestCheckLine without its line
// feed.
var summaryCheckLineLen = len(strings.TrimSuffix(summaryTestCheckLine, "\n"))

// summaryCheckWindowCases place the check line at the edge of the
// dashboardPreplanHeadMax head. at is the want of LastCheckAt ("" when
// lastCheck is unread). found is whether preplan_check finds the same line
// (setGuardrailCheckLine replaces it): it finds a line that starts in the
// head, also a line that the head cuts.
var summaryCheckWindowCases = []struct {
	name  string
	start int
	at    string
	found bool
}{
	{"line ends at the last byte of the head", dashboardPreplanHeadMax - summaryCheckLineLen, "2026-10-29T07:58:25Z", true},
	{"line starts at the first byte after the head", dashboardPreplanHeadMax, "", false},
	{"line starts just past the head", dashboardPreplanHeadMax + 1, "", false},
	{"head cuts the line inside the time", dashboardPreplanHeadMax - summaryCheckLineLen + 2, "", true},
}

// TestDashboardPreplanSummariesCheckLineHeadWindow checks that the summary
// reads the check line from the first dashboardPreplanHeadMax bytes only, at
// each edge of that window, and compares each case with how preplan_check
// finds the line.
func TestDashboardPreplanSummariesCheckLineHeadWindow(t *testing.T) {
	for _, c := range summaryCheckWindowCases {
		t.Run(c.name, func(t *testing.T) {
			text := summaryCheckLineAt(t, c.start)
			if got := strings.Index(text, summaryTestCheckLine); got != c.start {
				t.Fatalf("check line at byte %d, want %d", got, c.start)
			}
			row, _, _ := summaryRun(t, summaryWrite(text))
			unread := slices.Contains(row.Unread, dashboardPreplanUnreadLastCheck)
			if row.LastCheckAt != c.at || unread != (c.at == "") {
				t.Errorf("lastCheckAt %q, unread %q, want %q (unread %v)", row.LastCheckAt, row.Unread, c.at, c.at == "")
			}
			if _, prior, ok := setGuardrailCheckLine([]byte(text), "**Guardrail check:** new"); !ok || (prior != "") != c.found {
				t.Errorf("preplan_check found the line = %v (ok %v), want %v", prior != "", ok, c.found)
			}
		})
	}
}

// summaryGoalCases are the Goal sentences of
// TestDashboardPreplanSummariesGoalPreview and the goal each one gives. The
// 120-rune and 121-rune sentences use a 2-byte rune, so the cut counts
// runes, not bytes.
var summaryGoalCases = []struct{ name, sentence, want string }{
	{"120 runes stay whole", strings.Repeat("ä", 119) + ".", strings.Repeat("ä", 119) + "."},
	{"121 runes are cut", strings.Repeat("ä", 120) + ".", strings.Repeat("ä", 120) + "…"},
}

// TestDashboardPreplanSummariesGoalPreview checks that the Goal sentence goes
// through dashboardPreview: a secret-like token is redacted, a sentence of
// dashboardPreviewTextMax (120) runes stays whole, and one of 121 runes is cut.
func TestDashboardPreplanSummariesGoalPreview(t *testing.T) {
	t.Run("secret is redacted", func(t *testing.T) {
		const secret = "verysecrettoken123"
		row, _, _ := summaryRun(t, summaryWrite(summaryTopic(map[string]string{
			preplanGoalSection: "Call the API with Bearer " + secret + ". More text.\n",
		})))
		if strings.Contains(row.Goal, secret) || !strings.HasPrefix(row.Goal, "Call the API with ") {
			t.Errorf("goal = %q, want the secret redacted and the text around it kept", row.Goal)
		}
	})
	for _, c := range summaryGoalCases {
		t.Run(c.name, func(t *testing.T) {
			row, _, _ := summaryRun(t, summaryWrite(summaryTopic(map[string]string{preplanGoalSection: c.sentence + "\n"})))
			if row.Goal != c.want {
				t.Errorf("goal = %q (%d runes), want %q", row.Goal, utf8.RuneCountInString(row.Goal), c.want)
			}
		})
	}
}

// summaryParityCases holds one case for each file state that preplan_check
// refuses or reads: a live link, a dangling link, a folder, a pipe, a
// device, a file that went away, a file over dashboardPreplanSummaryMax and
// under 8 MiB, a failed Lstat and a failed read. class is the error class of
// readPreplanTopic for preplan_check ("" when it succeeds). The seam cases
// inject one error through preplanLstat or preplanReadFile, which both
// readers call.
var summaryParityCases = []struct {
	summaryCase
	class string
}{
	{summaryCase{"live link", summaryLink(true), summaryUnreadContract, summaryNotRegularWarning, summaryAllUnread}, "domain"},
	{summaryCase{"dangling link", summaryLink(false), summaryUnreadContract, summaryNotRegularWarning, summaryAllUnread}, "domain"},
	{summaryCase{"folder", summaryFolder, summaryUnreadContract, summaryNotRegularWarning, summaryAllUnread}, "domain"},
	{summaryCase{"pipe", summaryLstatMode(fs.ModeNamedPipe | 0o644), summaryUnreadContract, summaryNotRegularWarning, summaryAllUnread}, "domain"},
	{summaryCase{"device", summaryLstatMode(fs.ModeDevice | 0o644), summaryUnreadContract, summaryNotRegularWarning, summaryAllUnread}, "domain"},
	{summaryCase{"file went away", summaryGone, summaryUnreadContract, summaryGoneWarning, summaryAllUnread}, "domain"},
	{summaryCase{"over 256 KiB, under 8 MiB", summaryOverLimit, summaryUnreadContract, summaryOverLimitWarning, summaryAllUnread}, ""},
	{summaryCase{"Lstat I/O error", summaryLstatError, summaryUnreadContract, summaryInjectedWarning, summaryAllUnread}, "infra"},
	{summaryCase{"read I/O error", summaryReadError, summaryUnreadContract, summaryInjectedWarning, summaryAllUnread}, "infra"},
}

// TestDashboardPreplanSummariesFileStateParity puts each file state of
// summaryParityCases at the topic file path, then checks the result of the
// preplan_check read (readPreplanTopic) and of the summary for that state.
func TestDashboardPreplanSummariesFileStateParity(t *testing.T) {
	for _, c := range summaryParityCases {
		t.Run(c.name, func(t *testing.T) {
			row, warnings, file := summaryRun(t, c.setup)
			_, err := readPreplanTopic(preplanCheckAction, file)
			if got := errorClassOf(err); got != c.class || (c.class != "" && err == nil) {
				t.Errorf("preplan_check read: class %q (err %v), want %q", got, err, c.class)
			}
			c.check(t, row)
			if want := []string{c.warning(t, file)}; !reflect.DeepEqual(warnings, want) {
				t.Errorf("warnings = %q, want %q", warnings, want)
			}
		})
	}
}

// TestDashboardPreplanSummariesGapsMatchPreplanCheck checks that the gaps of
// the summary are the emptySections of preplan_check for the same file,
// without "## ".
func TestDashboardPreplanSummariesGapsMatchPreplanCheck(t *testing.T) {
	for name, over := range map[string]map[string]string{
		"every section has text": {preplanFlowsSection: formatMermaid},
		"empty Flows":            nil,
		"Decisions with no row":  {preplanDecisionsSection: preplanDecisionsHeader, "Risk if wrong": "  \n"},
	} {
		t.Run(name, func(t *testing.T) {
			row, _, file := summaryRun(t, summaryWrite(summaryTopic(over)))
			root := filepath.Dir(filepath.Dir(filepath.Dir(file)))
			out, err := planSupportCore(root, root, preplanCheckIn("auth flow", 3, 1, 1))
			if err != nil {
				t.Fatalf("preplan_check: %v", err)
			}
			want := []string{}
			for _, heading := range *out.EmptySections {
				want = append(want, strings.TrimPrefix(heading, preplanHeadingPrefix))
			}
			if !reflect.DeepEqual(row.Gaps, want) {
				t.Errorf("gaps = %#v, want the preplan_check emptySections %#v", row.Gaps, want)
			}
		})
	}
}

// TestDashboardPreplanSummariesHeadReadFailsDropsRow checks that a topic
// file whose head read fails stays out of the snapshot with the head warning
// only, as before the summary: the summary pass never reads its file. The
// dashboardPreplanHead seam fails the head read, so the test also runs for
// the root user.
func TestDashboardPreplanSummariesHeadReadFailsDropsRow(t *testing.T) {
	root := dashRoot(t)
	good := preplanWrite(t, root, "good", summaryTopic(nil), dashNow)
	locked := preplanWrite(t, root, "locked", summaryTopic(nil), dashNow)
	realHead := dashboardPreplanHead
	swapPreplanSeam(t, &dashboardPreplanHead, func(p string) (string, error) {
		if p == locked {
			return "", errInjectedPreplan
		}
		return realHead(p)
	})
	realRead := dashboardPreplanReadSummary
	var read []string
	swapPreplanSeam(t, &dashboardPreplanReadSummary, func(p string, max int64) ([]byte, error) {
		read = append(read, p)
		return realRead(p, max)
	})

	repo := collectDashboardRepo(root, dashNow)
	if got, want := preplanSlugs(repo.Preplans), []string{"good"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("slugs = %v, want %v", got, want)
	}
	if !reflect.DeepEqual(read, []string{good}) {
		t.Errorf("summary read %q, want only %q", read, good)
	}
	if len(repo.Warnings) != 1 || !strings.HasPrefix(repo.Warnings[0], "Preplan locked not read: ") {
		t.Errorf("warnings = %q, want only the head warning \"Preplan locked not read: ...\"", repo.Warnings)
	}
	if repo.Preplans[0].Live != 3 || !reflect.DeepEqual(repo.Preplans[0].Unread, []string{}) {
		t.Errorf("good row = %+v, want the summary filled", repo.Preplans[0])
	}
}
