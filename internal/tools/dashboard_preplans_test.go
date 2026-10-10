package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
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
	preplanWrite(t, root, "auth-flow", preplanTopicFile("auth flow", preplanStatusInProgress), same)
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
		Slug:      "auth-flow",
		Topic:     "auth flow",
		Status:    "in progress",
		Path:      ".sdlc-v2/preplan/auth-flow.md",
		UpdatedAt: "2026-10-10T09:00:00Z",
	}
	if list[2] != want {
		t.Errorf("auth-flow row = %+v, want %+v", list[2], want)
	}
	if list[0].Status != "other words" {
		t.Errorf("newest status = %q, want the raw text %q", list[0].Status, "other words")
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
// skeleton that preplan_context writes shows in the list with the topic and
// the status "in progress".
func TestDashboardPreplans_ListsFileThatPreplanContextWrites(t *testing.T) {
	root := newOpenspecStageFixture(t, "")
	if _, err := planSupportCore(root, root, PlanSupportIn{Action: "preplan_context", Topic: "auth flow"}); err != nil {
		t.Fatalf("preplan_context: %v", err)
	}
	list, warnings := dashboardPreplans(root)
	if len(warnings) != 0 {
		t.Errorf("warnings = %v, want none", warnings)
	}
	if len(list) != 1 {
		t.Fatalf("rows = %v, want 1", list)
	}
	if list[0].Slug != "auth-flow" || list[0].Topic != "auth flow" || list[0].Status != preplanStatusInProgress {
		t.Errorf("row = %+v, want slug auth-flow, topic \"auth flow\", status %q", list[0], preplanStatusInProgress)
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
