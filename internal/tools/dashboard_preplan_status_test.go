package tools

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
)

const setStatusPickSuggestion = "Pick one of: in progress, ready for plan, paused."

// setStatusBody is a topic file with a Status line in the middle and more text
// before and after it. The body repeats the Status prefix at the start of a
// later line, to show that only the first Status line changes.
func setStatusBody(statusLine string) string {
	return "# Preplan: topic\n\n" + statusLine + "\n\n## Goal\n\nShip it.\n\n**Status:** later line stays\n\n## Open questions\n"
}

// setStatusNoWrite replaces the write seam with one that fails the test.
func setStatusNoWrite(t *testing.T) {
	t.Helper()
	dashDelSeam(t, &dashboardDeleteWriteBytes, func(p string, _ []byte) error {
		t.Errorf("unexpected write to %s", p)
		return nil
	})
}

// setStatusZero checks that out is the zero value.
func setStatusZero(t *testing.T, out DashboardPreplanStatusOut) {
	t.Helper()
	if out != (DashboardPreplanStatusOut{}) {
		t.Errorf("out = %+v, want zero value", out)
	}
}

func TestDashboardSetPreplanStatus_ReplacesOnlyTheFirstStatusLine(t *testing.T) {
	for _, from := range PreplanStatuses {
		for _, to := range PreplanStatuses {
			if from == to {
				continue
			}
			t.Run(from+" to "+to, func(t *testing.T) {
				root := t.TempDir()
				path := dashDelTopic(t, root, "topic", setStatusBody("**Status:** "+from))
				other := dashDelTopic(t, root, "other", setStatusBody("**Status:** "+from))

				out, err := DashboardSetPreplanStatus(root, "topic", to)
				if err != nil {
					t.Fatalf("DashboardSetPreplanStatus: %v", err)
				}
				if !out.Changed || out.Status != to || out.Message == "" {
					t.Errorf("out = %+v, want Changed, Status %q and a message", out, to)
				}
				if got, want := string(dashDelReadFile(t, path)), setStatusBody("**Status:** "+to); got != want {
					t.Errorf("topic file = %q, want %q", got, want)
				}
				if got, want := string(dashDelReadFile(t, other)), setStatusBody("**Status:** "+from); got != want {
					t.Errorf("other topic file = %q, want it unchanged", got)
				}
			})
		}
	}
}

func TestDashboardSetPreplanStatus_FileEdgeCases(t *testing.T) {
	cases := []struct {
		name   string
		before string
		after  string
	}{
		{"no final newline", "# Preplan: topic\n\n**Status:** in progress", "# Preplan: topic\n\n**Status:** paused"},
		{"CR LF line ends", "# Preplan: topic\r\n\r\n**Status:** in progress\r\nBody\r\n", "# Preplan: topic\r\n\r\n**Status:** paused\r\nBody\r\n"},
		{"no space after the prefix", "# Preplan: topic\n**Status:**in progress\n", "# Preplan: topic\n**Status:** paused\n"},
		{"tab and trailing space", "# Preplan: topic\n**Status:**\tin progress  \nBody\n", "# Preplan: topic\n**Status:** paused\nBody\n"},
		{"unknown stored status", "# Preplan: topic\n**Status:** done\nBody\n", "# Preplan: topic\n**Status:** paused\nBody\n"},
		{"empty stored status", "# Preplan: topic\n**Status:**\nBody\n", "# Preplan: topic\n**Status:** paused\nBody\n"},
		{"multi-byte text around the line", "# Preplan: zażółć\n**Status:** in progress\nGęślą jaźń\n", "# Preplan: zażółć\n**Status:** paused\nGęślą jaźń\n"},
		{"status line first", "**Status:** in progress\n# Preplan: topic\n", "**Status:** paused\n# Preplan: topic\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			path := dashDelTopic(t, root, "topic", tc.before)

			out, err := DashboardSetPreplanStatus(root, "topic", "paused")
			if err != nil {
				t.Fatalf("DashboardSetPreplanStatus: %v", err)
			}
			if !out.Changed || out.Status != "paused" {
				t.Errorf("out = %+v, want Changed with Status paused", out)
			}
			if got := string(dashDelReadFile(t, path)); got != tc.after {
				t.Errorf("topic file = %q, want %q", got, tc.after)
			}
		})
	}
}

// TestDashboardSetPreplanStatus_LongStatusLineAtTheHeadLimit checks a Status
// line that crosses the 4 KiB limit of the head: the whole old line goes.
func TestDashboardSetPreplanStatus_LongStatusLineAtTheHeadLimit(t *testing.T) {
	root := t.TempDir()
	pad := strings.Repeat("x", dashboardPreplanHeadMax-len("# Preplan: t\n**Status:** ")-10)
	before := "# Preplan: t\n**Status:** " + pad + strings.Repeat("y", 100) + "\nBody\n"
	path := dashDelTopic(t, root, "topic", before)

	out, err := DashboardSetPreplanStatus(root, "topic", "ready for plan")
	if err != nil {
		t.Fatalf("DashboardSetPreplanStatus: %v", err)
	}
	if !out.Changed {
		t.Errorf("out = %+v, want Changed", out)
	}
	if got, want := string(dashDelReadFile(t, path)), "# Preplan: t\n**Status:** ready for plan\nBody\n"; got != want {
		t.Errorf("topic file = %q, want %q", got, want)
	}
}

// TestDashboardSetPreplanStatus_WritesThroughSeamWithRebuiltBytes checks that
// the write seam gets the topic path and the rebuilt bytes.
func TestDashboardSetPreplanStatus_WritesThroughSeamWithRebuiltBytes(t *testing.T) {
	root := t.TempDir()
	path := dashDelTopic(t, root, "topic", setStatusBody("**Status:** in progress"))
	var gotPath string
	var gotData []byte
	calls := 0
	dashDelSeam(t, &dashboardDeleteWriteBytes, func(p string, data []byte) error {
		calls++
		gotPath, gotData = p, data
		return nil
	})

	if _, err := DashboardSetPreplanStatus(root, "topic", "ready for plan"); err != nil {
		t.Fatalf("DashboardSetPreplanStatus: %v", err)
	}
	if calls != 1 {
		t.Errorf("write calls = %d, want 1", calls)
	}
	if gotPath != path {
		t.Errorf("write path = %q, want %q", gotPath, path)
	}
	if want := setStatusBody("**Status:** ready for plan"); string(gotData) != want {
		t.Errorf("write data = %q, want %q", gotData, want)
	}
}

// TestDashboardSetPreplanStatus_WriteUsesTempFileAndRename checks the real
// seam: the file has the new status and no temp file stays in the folder.
func TestDashboardSetPreplanStatus_WriteUsesTempFileAndRename(t *testing.T) {
	root := t.TempDir()
	dashDelTopic(t, root, "topic", setStatusBody("**Status:** in progress"))
	dir := filepath.Join(root, paths.DataDir, paths.PreplanSubdir)

	if _, err := DashboardSetPreplanStatus(root, "topic", "paused"); err != nil {
		t.Fatalf("DashboardSetPreplanStatus: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "topic.md" {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("preplan folder holds %v, want only topic.md", names)
	}
}

func TestDashboardSetPreplanStatus_SameStatusChangesNothing(t *testing.T) {
	for _, status := range PreplanStatuses {
		for _, line := range []string{"**Status:** " + status, "**Status:**\t" + status + "  ", "**Status:**" + status} {
			t.Run(fmt.Sprintf("%s %q", status, line), func(t *testing.T) {
				root := t.TempDir()
				before := setStatusBody(line)
				path := dashDelTopic(t, root, "topic", before)
				setStatusNoWrite(t)

				out, err := DashboardSetPreplanStatus(root, "topic", status)
				if err != nil {
					t.Fatalf("DashboardSetPreplanStatus: %v", err)
				}
				if out.Changed || out.Status != status || out.Message == "" {
					t.Errorf("out = %+v, want not Changed, Status %q and a message", out, status)
				}
				if got := string(dashDelReadFile(t, path)); got != before {
					t.Errorf("topic file = %q, want it unchanged", got)
				}
			})
		}
	}
}

// TestDashboardSetPreplanStatus_LastWriteWins checks that no version check
// runs: two writes in a row leave the second status.
func TestDashboardSetPreplanStatus_LastWriteWins(t *testing.T) {
	root := t.TempDir()
	path := dashDelTopic(t, root, "topic", setStatusBody("**Status:** in progress"))

	for _, status := range []string{"paused", "ready for plan"} {
		out, err := DashboardSetPreplanStatus(root, "topic", status)
		if err != nil {
			t.Fatalf("DashboardSetPreplanStatus %q: %v", status, err)
		}
		if !out.Changed || out.Status != status {
			t.Errorf("out = %+v, want Changed with Status %q", out, status)
		}
	}
	if got, want := string(dashDelReadFile(t, path)), setStatusBody("**Status:** ready for plan"); got != want {
		t.Errorf("topic file = %q, want %q", got, want)
	}
}

// TestDashboardSetPreplanStatus_ListReadsTheNewStatus checks that the page
// list shows the status that the tool wrote.
func TestDashboardSetPreplanStatus_ListReadsTheNewStatus(t *testing.T) {
	root := t.TempDir()
	dashDelTopic(t, root, "topic", setStatusBody("**Status:** in progress"))

	if _, err := DashboardSetPreplanStatus(root, "topic", "paused"); err != nil {
		t.Fatalf("DashboardSetPreplanStatus: %v", err)
	}
	list, warnings := dashboardPreplans(root)
	if len(warnings) != 0 {
		t.Errorf("warnings = %v, want none", warnings)
	}
	if len(list) != 1 || list[0].Slug != "topic" || list[0].Status != "paused" {
		t.Errorf("list = %+v, want one topic with status paused", list)
	}
}

func TestDashboardSetPreplanStatus_BadSlugRefused(t *testing.T) {
	root := t.TempDir()
	keep := dashDelTopic(t, root, "keep", setStatusBody("**Status:** in progress"))
	outside := filepath.Join(root, paths.DataDir, "outside.md")
	if err := os.WriteFile(outside, []byte(setStatusBody("**Status:** in progress")), 0o644); err != nil {
		t.Fatal(err)
	}
	setStatusNoWrite(t)

	for _, slug := range []string{"", ".", "..", "a/b", "../outside", "/abs/path", `a\b`, "keep.md"} {
		t.Run(slug, func(t *testing.T) {
			out, err := DashboardSetPreplanStatus(root, slug, "paused")
			dashDelWantErr(t, err, "domain", "Reload the page and try again.")
			setStatusZero(t, out)
		})
	}
	for _, p := range []string{keep, outside} {
		if got := string(dashDelReadFile(t, p)); got != setStatusBody("**Status:** in progress") {
			t.Errorf("%s changed: %q", p, got)
		}
	}
}

func TestDashboardSetPreplanStatus_BadStatusRefused(t *testing.T) {
	root := t.TempDir()
	before := setStatusBody("**Status:** in progress")
	path := dashDelTopic(t, root, "topic", before)
	setStatusNoWrite(t)

	for _, status := range []string{"", "done", "Paused", " paused", "paused ", "ready  for plan", "in progress\n"} {
		t.Run(fmt.Sprintf("%q", status), func(t *testing.T) {
			out, err := DashboardSetPreplanStatus(root, "topic", status)
			dashDelWantErr(t, err, "domain", setStatusPickSuggestion)
			for _, v := range PreplanStatuses {
				if !strings.Contains(err.Error(), v) {
					t.Errorf("message %q does not name the status %q", err.Error(), v)
				}
			}
			setStatusZero(t, out)
		})
	}
	if got := string(dashDelReadFile(t, path)); got != before {
		t.Errorf("topic file = %q, want it unchanged", got)
	}
}

func TestDashboardSetPreplanStatus_MissingTopicFileIsDomainError(t *testing.T) {
	t.Run("folder exists", func(t *testing.T) {
		root := t.TempDir()
		dashDelPreplanDir(t, root)
		setStatusNoWrite(t)

		out, err := DashboardSetPreplanStatus(root, "topic", "paused")
		dashDelWantErr(t, err, "domain", "Reload the page. The topic file is gone.")
		setStatusZero(t, out)
		if _, err := os.Lstat(filepath.Join(root, paths.DataDir, paths.PreplanSubdir, "topic.md")); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("tool created the topic file, Lstat err = %v", err)
		}
	})
	t.Run("no preplan folder", func(t *testing.T) {
		root := t.TempDir()
		setStatusNoWrite(t)

		out, err := DashboardSetPreplanStatus(root, "topic", "paused")
		dashDelWantErr(t, err, "domain", "Reload the page. The topic file is gone.")
		setStatusZero(t, out)
	})
	t.Run("file gone before read", func(t *testing.T) {
		root := t.TempDir()
		dashDelTopic(t, root, "topic", setStatusBody("**Status:** in progress"))
		dashDelSeam(t, &dashboardDeleteReadFile, func(string) ([]byte, error) {
			return nil, &fs.PathError{Op: "open", Path: "topic.md", Err: fs.ErrNotExist}
		})
		setStatusNoWrite(t)

		out, err := DashboardSetPreplanStatus(root, "topic", "paused")
		dashDelWantErr(t, err, "domain", "Reload the page. The topic file is gone.")
		setStatusZero(t, out)
	})
}

func TestDashboardSetPreplanStatus_SymlinkRefusedAndLinkStays(t *testing.T) {
	root := t.TempDir()
	targetBytes := setStatusBody("**Status:** in progress")
	target := filepath.Join(root, "target.md")
	if err := os.WriteFile(target, []byte(targetBytes), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dashDelPreplanDir(t, root), "linked.md")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}
	setStatusNoWrite(t)

	out, err := DashboardSetPreplanStatus(root, "linked", "paused")
	dashDelWantErr(t, err, "domain", "Change the status by hand in the target file.")
	setStatusZero(t, out)
	info, lerr := os.Lstat(link)
	if lerr != nil || info.Mode()&fs.ModeSymlink == 0 {
		t.Errorf("link is not a symlink any more, Lstat err = %v", lerr)
	}
	if got := string(dashDelReadFile(t, target)); got != targetBytes {
		t.Errorf("target = %q, want it unchanged", got)
	}
}

func TestDashboardSetPreplanStatus_DanglingSymlinkRefused(t *testing.T) {
	root := t.TempDir()
	link := filepath.Join(dashDelPreplanDir(t, root), "dangling.md")
	if err := os.Symlink(filepath.Join(root, "nowhere.md"), link); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}
	setStatusNoWrite(t)

	out, err := DashboardSetPreplanStatus(root, "dangling", "paused")
	dashDelWantErr(t, err, "domain", "Change the status by hand in the target file.")
	setStatusZero(t, out)
	if _, err := os.Lstat(link); err != nil {
		t.Errorf("link is gone: %v", err)
	}
}

func TestDashboardSetPreplanStatus_FolderRefused(t *testing.T) {
	root := t.TempDir()
	folder := filepath.Join(dashDelPreplanDir(t, root), "topic.md")
	if err := os.Mkdir(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	setStatusNoWrite(t)

	out, err := DashboardSetPreplanStatus(root, "topic", "paused")
	dashDelWantErr(t, err, "domain", "Change the status by hand in the target file.")
	setStatusZero(t, out)
	if info, serr := os.Stat(folder); serr != nil || !info.IsDir() {
		t.Errorf("folder is gone, Stat err = %v", serr)
	}
}

func TestDashboardSetPreplanStatus_NoStatusLineIsDataError(t *testing.T) {
	cases := map[string]string{
		"no status line":           "# Preplan: topic\n\nNo status here.\n",
		"empty file":               "",
		"status past 4 KiB":        "# Preplan: topic\n" + strings.Repeat("a\n", dashboardPreplanHeadMax) + "**Status:** in progress\n",
		"prefix not at line start": "# Preplan: topic\n  **Status:** in progress\n",
		"other case":               "# Preplan: topic\n**status:** in progress\n",
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			path := dashDelTopic(t, root, "topic", content)
			setStatusNoWrite(t)

			out, err := DashboardSetPreplanStatus(root, "topic", "paused")
			dashDelWantErr(t, err, "data", "Add a **Status:** line near the top of the topic file by hand.")
			setStatusZero(t, out)
			if got := string(dashDelReadFile(t, path)); got != content {
				t.Errorf("topic file changed, length %d, want %d", len(got), len(content))
			}
		})
	}
}

func TestDashboardSetPreplanStatus_TooLargeIsDataError(t *testing.T) {
	root := t.TempDir()
	head := "# Preplan: topic\n**Status:** in progress\n"
	big := append([]byte(head), bytes.Repeat([]byte("x"), dashboardDeleteReadMax-len(head)+1)...)
	path := dashDelTopic(t, root, "topic", string(big))
	setStatusNoWrite(t)

	out, err := DashboardSetPreplanStatus(root, "topic", "paused")
	dashDelWantErr(t, err, "data", "Make the topic file smaller than 8 MiB by hand, then retry.")
	want := fmt.Sprintf("The topic file is %d bytes, more than the 8388608 bytes (8 MiB) that a dashboard delete reads", len(big))
	if err.Error() != want {
		t.Errorf("message = %q, want %q", err.Error(), want)
	}
	setStatusZero(t, out)
	if got := dashDelReadFile(t, path); !bytes.Equal(got, big) {
		t.Errorf("topic file changed, length %d, want %d", len(got), len(big))
	}
}

func TestDashboardSetPreplanStatus_SizeAtLimitIsRead(t *testing.T) {
	root := t.TempDir()
	head := "# Preplan: topic\n**Status:** in progress\n"
	exact := append([]byte(head), bytes.Repeat([]byte("x"), dashboardDeleteReadMax-len(head))...)
	dashDelTopic(t, root, "topic", string(exact))
	var got []byte
	dashDelSeam(t, &dashboardDeleteWriteBytes, func(_ string, data []byte) error {
		got = data
		return nil
	})

	out, err := DashboardSetPreplanStatus(root, "topic", "paused")
	if err != nil {
		t.Fatalf("DashboardSetPreplanStatus: %v", err)
	}
	if !out.Changed {
		t.Errorf("out = %+v, want Changed", out)
	}
	if want := append([]byte("# Preplan: topic\n**Status:** paused\n"), exact[len(head):]...); !bytes.Equal(got, want) {
		t.Errorf("write data differs from the wanted bytes, length %d, want %d", len(got), len(want))
	}
}

func TestDashboardSetPreplanStatus_LstatFailureIsInfra(t *testing.T) {
	root := t.TempDir()
	// The preplan folder is a plain file, so Lstat of a topic path gives ENOTDIR.
	if err := os.MkdirAll(filepath.Join(root, paths.DataDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, paths.DataDir, paths.PreplanSubdir), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	setStatusNoWrite(t)

	out, err := DashboardSetPreplanStatus(root, "topic", "paused")
	dashDelWantErr(t, err, "infra", "Check read permission on .sdlc-v2/preplan/topic.md and retry.")
	setStatusZero(t, out)
}

func TestDashboardSetPreplanStatus_ReadFailureIsInfra(t *testing.T) {
	root := t.TempDir()
	before := setStatusBody("**Status:** in progress")
	path := dashDelTopic(t, root, "my_topic", before)
	injected := errors.New("injected read failure")
	dashDelSeam(t, &dashboardDeleteReadFile, func(string) ([]byte, error) { return nil, injected })
	setStatusNoWrite(t)

	out, err := DashboardSetPreplanStatus(root, "my_topic", "paused")
	dashDelWantErr(t, err, "infra", "Check read permission on .sdlc-v2/preplan/my_topic.md and retry.")
	if !errors.Is(err, injected) {
		t.Errorf("error does not wrap the injected failure: %v", err)
	}
	setStatusZero(t, out)
	if got := string(dashDelReadFile(t, path)); got != before {
		t.Errorf("topic file = %q, want it unchanged", got)
	}
}

func TestDashboardSetPreplanStatus_WriteFailureIsInfra(t *testing.T) {
	root := t.TempDir()
	before := setStatusBody("**Status:** in progress")
	path := dashDelTopic(t, root, "topic", before)
	injected := errors.New("injected write failure")
	dashDelSeam(t, &dashboardDeleteWriteBytes, func(string, []byte) error { return injected })

	out, err := DashboardSetPreplanStatus(root, "topic", "paused")
	dashDelWantErr(t, err, "infra", "Check write permission on .sdlc-v2/preplan/ and free disk space, then retry.")
	if !errors.Is(err, injected) {
		t.Errorf("error does not wrap the injected failure: %v", err)
	}
	setStatusZero(t, out)
	if got := string(dashDelReadFile(t, path)); got != before {
		t.Errorf("topic file = %q, want it unchanged", got)
	}
}
