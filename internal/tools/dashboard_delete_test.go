package tools

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/rnagrodzki/sdlc-plugin/internal/history"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
)

// dashDelPreplanDir returns the preplan folder of root and creates it.
func dashDelPreplanDir(t *testing.T, root string) string {
	t.Helper()
	dir := filepath.Join(root, paths.DataDir, paths.PreplanSubdir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// dashDelTopic writes the topic file <slug>.md with the given content.
func dashDelTopic(t *testing.T, root, slug, content string) string {
	t.Helper()
	path := filepath.Join(dashDelPreplanDir(t, root), slug+".md")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// dashDelHistoryDir returns the history folder of root and creates it.
func dashDelHistoryDir(t *testing.T, root string) string {
	t.Helper()
	dir := paths.HistoryDir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// dashDelDeferredFile writes deferred.json with the given content and
// returns its path.
func dashDelDeferredFile(t *testing.T, root string, content []byte) string {
	t.Helper()
	path := filepath.Join(dashDelHistoryDir(t, root), "deferred.json")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// dashDelItems writes the issues to deferred.json through the history
// writer, so the bytes are the writer output. It returns the file path.
func dashDelItems(t *testing.T, root string, items ...history.DeferredIssue) string {
	t.Helper()
	w := history.NewFileWriter(paths.HistoryDir(root))
	for _, it := range items {
		if err := w.AddDeferred(it); err != nil {
			t.Fatal(err)
		}
	}
	return w.DeferredPath()
}

// dashDelItem returns a deferred issue with the given id and description.
func dashDelItem(id, desc, status string) history.DeferredIssue {
	return history.DeferredIssue{
		ID:          id,
		Created:     "2026-10-10T08:00:00Z",
		Source:      "review",
		Priority:    "high",
		Description: desc,
		Status:      status,
	}
}

// dashDelReadFile returns the bytes of path.
func dashDelReadFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// dashDelSeam replaces *seam with fn until the test ends.
func dashDelSeam[T any](t *testing.T, seam *T, fn T) {
	t.Helper()
	orig := *seam
	*seam = fn
	t.Cleanup(func() { *seam = orig })
}

// dashDelErrKind returns the class name and the Suggestion of err. The class
// is "domain", "infra" or "data". It fails the test for any other error, and
// for an error with an empty message or an empty Suggestion.
func dashDelErrKind(t *testing.T, err error) (kind, suggestion string) {
	t.Helper()
	var de *mcpserver.DomainError
	var ie *mcpserver.InfraError
	var dae *mcpserver.DataError
	switch {
	case errors.As(err, &de):
		kind, suggestion = "domain", de.Suggestion
	case errors.As(err, &ie):
		kind, suggestion = "infra", ie.Suggestion
	case errors.As(err, &dae):
		kind, suggestion = "data", dae.Suggestion
	default:
		t.Fatalf("error %v is not a DomainError, InfraError or DataError", err)
	}
	if err.Error() == "" {
		t.Error("error message is empty")
	}
	if suggestion == "" {
		t.Error("error Suggestion is empty")
	}
	return kind, suggestion
}

// dashDelWantErr checks that err has the wanted class and Suggestion text.
func dashDelWantErr(t *testing.T, err error, wantKind, wantSuggestion string) {
	t.Helper()
	if err == nil {
		t.Fatalf("want a %s error, got nil", wantKind)
	}
	kind, suggestion := dashDelErrKind(t, err)
	if kind != wantKind {
		t.Errorf("error class = %s, want %s (err: %v)", kind, wantKind, err)
	}
	if suggestion != wantSuggestion {
		t.Errorf("Suggestion = %q, want %q", suggestion, wantSuggestion)
	}
}

func TestDashboardDeletePreplan_DeletesEachStatus(t *testing.T) {
	type tcase struct {
		name    string
		content string
	}
	// One case for each status of PreplanStatuses, so a new status is
	// covered, and one for a file with no status line.
	var cases []tcase
	for _, status := range PreplanStatuses {
		cases = append(cases, tcase{status, "# Preplan: topic\n\n**Status:** " + status + "\n"})
	}
	cases = append(cases, tcase{"no status", "# Preplan: topic\n\nNo status line here.\n"})
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			path := dashDelTopic(t, root, "my_topic", tc.content)
			other := dashDelTopic(t, root, "other", "# Preplan: other\n")

			out, err := DashboardDeletePreplan(root, "my_topic")
			if err != nil {
				t.Fatalf("DashboardDeletePreplan: %v", err)
			}
			if !out.Deleted || out.AlreadyGone || out.Message == "" {
				t.Errorf("out = %+v, want Deleted with a message", out)
			}
			if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("topic file still exists, Lstat err = %v", err)
			}
			if _, err := os.Stat(other); err != nil {
				t.Errorf("other topic file is gone: %v", err)
			}
		})
	}
}

func TestDashboardDeletePreplan_AlreadyGone(t *testing.T) {
	root := t.TempDir()
	dashDelPreplanDir(t, root)

	out, err := DashboardDeletePreplan(root, "missing")
	if err != nil {
		t.Fatalf("DashboardDeletePreplan: %v", err)
	}
	if !out.AlreadyGone || out.Deleted || out.Message == "" {
		t.Errorf("out = %+v, want AlreadyGone with a message", out)
	}
}

func TestDashboardDeletePreplan_AlreadyGoneWithoutPreplanFolder(t *testing.T) {
	out, err := DashboardDeletePreplan(t.TempDir(), "missing")
	if err != nil {
		t.Fatalf("DashboardDeletePreplan: %v", err)
	}
	if !out.AlreadyGone {
		t.Errorf("out = %+v, want AlreadyGone", out)
	}
}

func TestDashboardDeletePreplan_FolderRefused(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(dashDelPreplanDir(t, root), "nested.md")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	inner := filepath.Join(dir, "keep.txt")
	if err := os.WriteFile(inner, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := DashboardDeletePreplan(root, "nested")
	dashDelWantErr(t, err, "domain", "Remove the folder by hand.")
	if out != (DashboardDeleteOut{}) {
		t.Errorf("out = %+v, want zero value", out)
	}
	if _, err := os.Stat(inner); err != nil {
		t.Errorf("folder content is gone: %v", err)
	}
}

func TestDashboardDeletePreplan_BadSlugRefused(t *testing.T) {
	root := t.TempDir()
	keep := dashDelTopic(t, root, "keep", "# Preplan: keep\n")
	outside := filepath.Join(root, paths.DataDir, "outside.md")
	if err := os.WriteFile(outside, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	for slug, message := range map[string]string{
		"":           "The slug field is required",
		".":          `The slug "." is not a file name`,
		"..":         `The slug ".." is not a file name`,
		"a/b":        `The slug "a/b" has a path separator. A slug is a file name in .sdlc-v2/preplan/`,
		"../outside": `The slug "../outside" has a path separator. A slug is a file name in .sdlc-v2/preplan/`,
		"/abs/path":  `The slug "/abs/path" has a path separator. A slug is a file name in .sdlc-v2/preplan/`,
		"keep.md":    `The slug "keep.md" ends in .md. Send the file name without .md`,
	} {
		t.Run(slug, func(t *testing.T) {
			out, err := DashboardDeletePreplan(root, slug)
			dashDelWantErr(t, err, "domain", "Reload the page and try again.")
			if err.Error() != message {
				t.Errorf("message = %q, want %q", err.Error(), message)
			}
			if out != (DashboardDeleteOut{}) {
				t.Errorf("out = %+v, want zero value", out)
			}
		})
	}
	for _, p := range []string{keep, outside} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s is gone: %v", p, err)
		}
	}
}

func TestDashboardDeletePreplan_LstatFailureIsInfra(t *testing.T) {
	root := t.TempDir()
	// The preplan folder is a plain file, so Lstat of a topic path gives ENOTDIR.
	if err := os.MkdirAll(filepath.Join(root, paths.DataDir), 0o755); err != nil {
		t.Fatal(err)
	}
	blocker := filepath.Join(root, paths.DataDir, paths.PreplanSubdir)
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := DashboardDeletePreplan(root, "topic")
	dashDelWantErr(t, err, "infra", "Check read permission on .sdlc-v2/preplan/ and retry.")
	if out != (DashboardDeleteOut{}) {
		t.Errorf("out = %+v, want zero value", out)
	}
}

func TestDashboardDeletePreplan_RemoveFailureIsInfra(t *testing.T) {
	root := t.TempDir()
	path := dashDelTopic(t, root, "topic", "# Preplan: topic\n")
	injected := errors.New("injected remove failure")
	dashDelSeam(t, &dashboardDeleteRemove, func(string) error { return injected })

	out, err := DashboardDeletePreplan(root, "topic")
	dashDelWantErr(t, err, "infra", "Check write permission on .sdlc-v2/preplan/ and retry.")
	if !errors.Is(err, injected) {
		t.Errorf("error does not wrap the injected failure: %v", err)
	}
	if out != (DashboardDeleteOut{}) {
		t.Errorf("out = %+v, want zero value", out)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("topic file is gone: %v", err)
	}
}

func TestDashboardDeletePreplan_RemoveOfGoneFileIsAlreadyGone(t *testing.T) {
	root := t.TempDir()
	dashDelTopic(t, root, "topic", "# Preplan: topic\n")
	dashDelSeam(t, &dashboardDeleteRemove, func(p string) error { return &os.PathError{Op: "remove", Path: p, Err: os.ErrNotExist} })

	out, err := DashboardDeletePreplan(root, "topic")
	if err != nil {
		t.Fatalf("DashboardDeletePreplan: %v", err)
	}
	if !out.AlreadyGone || out.Deleted {
		t.Errorf("out = %+v, want AlreadyGone", out)
	}
}

func TestDashboardDeletePreplan_SymlinkRemovesLinkKeepsTarget(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target.md")
	if err := os.WriteFile(target, []byte("# Preplan: target\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dashDelPreplanDir(t, root), "linked.md")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}

	out, err := DashboardDeletePreplan(root, "linked")
	if err != nil {
		t.Fatalf("DashboardDeletePreplan: %v", err)
	}
	if !out.Deleted {
		t.Errorf("out = %+v, want Deleted", out)
	}
	if _, err := os.Lstat(link); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("link still exists, Lstat err = %v", err)
	}
	if got := dashDelReadFile(t, target); string(got) != "# Preplan: target\n" {
		t.Errorf("target content = %q, want it unchanged", got)
	}
}

func TestDashboardDeletePreplan_DanglingSymlinkIsRemoved(t *testing.T) {
	root := t.TempDir()
	link := filepath.Join(dashDelPreplanDir(t, root), "dangling.md")
	if err := os.Symlink(filepath.Join(root, "nowhere.md"), link); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}

	out, err := DashboardDeletePreplan(root, "dangling")
	if err != nil {
		t.Fatalf("DashboardDeletePreplan: %v", err)
	}
	if !out.Deleted {
		t.Errorf("out = %+v, want Deleted", out)
	}
}

func TestDashboardDeleteDeferred_RemovesFirstMatchKeepsOrder(t *testing.T) {
	root := t.TempDir()
	path := dashDelItems(t, root,
		dashDelItem("a", "first a", history.StatusOpen),
		dashDelItem("b", "only b", history.StatusOpen),
		dashDelItem("a", "second a", history.StatusResolved),
		dashDelItem("c", "only c", history.StatusOpen),
	)

	out, err := DashboardDeleteDeferred(root, "a")
	if err != nil {
		t.Fatalf("DashboardDeleteDeferred: %v", err)
	}
	if !out.Deleted || out.AlreadyGone || out.Message == "" {
		t.Errorf("out = %+v, want Deleted with a message", out)
	}

	// The wanted bytes come from the history writer, so a drift in the format shows here.
	wantRoot := t.TempDir()
	want := dashDelReadFile(t, dashDelItems(t, wantRoot,
		dashDelItem("b", "only b", history.StatusOpen),
		dashDelItem("a", "second a", history.StatusResolved),
		dashDelItem("c", "only c", history.StatusOpen),
	))
	if got := dashDelReadFile(t, path); !bytes.Equal(got, want) {
		t.Errorf("deferred.json = %q, want %q", got, want)
	}
}

func TestDashboardDeleteDeferred_DeletesAnyStatus(t *testing.T) {
	for _, status := range []string{history.StatusOpen, history.StatusResolved} {
		t.Run(status, func(t *testing.T) {
			root := t.TempDir()
			path := dashDelItems(t, root,
				dashDelItem("x", "target", status),
				dashDelItem("y", "other", history.StatusOpen),
			)
			out, err := DashboardDeleteDeferred(root, "x")
			if err != nil || !out.Deleted {
				t.Fatalf("out = %+v, err = %v, want Deleted", out, err)
			}
			items, err := history.NewFileWriter(paths.HistoryDir(root)).ListDeferred()
			if err != nil {
				t.Fatal(err)
			}
			if len(items) != 1 || items[0].ID != "y" {
				t.Errorf("items after delete = %+v, want only y", items)
			}
			if _, err := os.Stat(path); err != nil {
				t.Errorf("deferred.json is gone: %v", err)
			}
		})
	}
}

func TestDashboardDeleteDeferred_LastItemWritesEmptyArray(t *testing.T) {
	root := t.TempDir()
	path := dashDelItems(t, root, dashDelItem("only", "the last", history.StatusOpen))

	out, err := DashboardDeleteDeferred(root, "only")
	if err != nil || !out.Deleted {
		t.Fatalf("out = %+v, err = %v, want Deleted", out, err)
	}
	if got := dashDelReadFile(t, path); string(got) != "[]\n" {
		t.Errorf("deferred.json = %q, want %q", got, "[]\n")
	}
}

func TestDashboardDeleteDeferred_UnknownIDIsAlreadyGone(t *testing.T) {
	root := t.TempDir()
	path := dashDelItems(t, root, dashDelItem("a", "item a", history.StatusOpen))
	before := dashDelReadFile(t, path)

	out, err := DashboardDeleteDeferred(root, "nope")
	if err != nil {
		t.Fatalf("DashboardDeleteDeferred: %v", err)
	}
	if !out.AlreadyGone || out.Deleted {
		t.Errorf("out = %+v, want AlreadyGone", out)
	}
	if want := "No deferred item has the id nope. Another session may have deleted it."; out.Message != want {
		t.Errorf("message = %q, want %q", out.Message, want)
	}
	if got := dashDelReadFile(t, path); !bytes.Equal(got, before) {
		t.Errorf("deferred.json changed: %q", got)
	}
}

func TestDashboardDeleteDeferred_AbsentFileIsAlreadyGone(t *testing.T) {
	root := t.TempDir()

	out, err := DashboardDeleteDeferred(root, "a")
	if err != nil {
		t.Fatalf("DashboardDeleteDeferred: %v", err)
	}
	if !out.AlreadyGone || out.Deleted {
		t.Errorf("out = %+v, want AlreadyGone", out)
	}
	if _, err := os.Stat(history.NewFileWriter(paths.HistoryDir(root)).DeferredPath()); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("deferred.json was created, Stat err = %v", err)
	}
}

func TestDashboardDeleteDeferred_EmptyFileIsAlreadyGone(t *testing.T) {
	for name, content := range map[string]string{"zero bytes": "", "white space": " \n\t\n"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			path := dashDelDeferredFile(t, root, []byte(content))

			out, err := DashboardDeleteDeferred(root, "a")
			if err != nil {
				t.Fatalf("DashboardDeleteDeferred: %v", err)
			}
			if !out.AlreadyGone || out.Deleted {
				t.Errorf("out = %+v, want AlreadyGone", out)
			}
			if got := dashDelReadFile(t, path); string(got) != content {
				t.Errorf("deferred.json changed: %q", got)
			}
		})
	}
}

func TestDashboardDeleteDeferred_EmptyIDRefused(t *testing.T) {
	root := t.TempDir()
	path := dashDelItems(t, root, dashDelItem("a", "item a", history.StatusOpen))
	before := dashDelReadFile(t, path)

	out, err := DashboardDeleteDeferred(root, "")
	dashDelWantErr(t, err, "domain", "Reload the page and try again.")
	if err.Error() != "The id field is required" {
		t.Errorf("message = %q", err.Error())
	}
	if out != (DashboardDeleteOut{}) {
		t.Errorf("out = %+v, want zero value", out)
	}
	if got := dashDelReadFile(t, path); !bytes.Equal(got, before) {
		t.Errorf("deferred.json changed: %q", got)
	}
}

func TestDashboardDeleteDeferred_StatFailureIsInfra(t *testing.T) {
	root := t.TempDir()
	// The history folder is a plain file, so Stat of deferred.json gives ENOTDIR.
	if err := os.MkdirAll(filepath.Join(root, paths.DataDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.HistoryDir(root), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := DashboardDeleteDeferred(root, "a")
	dashDelWantErr(t, err, "infra", "Check read permission on .sdlc-v2/history/deferred.json and retry.")
	if out != (DashboardDeleteOut{}) {
		t.Errorf("out = %+v, want zero value", out)
	}
}

func TestDashboardDeleteDeferred_ReadFailureIsInfra(t *testing.T) {
	root := t.TempDir()
	// deferred.json is a folder: Stat succeeds and ReadFile fails.
	if err := os.MkdirAll(filepath.Join(dashDelHistoryDir(t, root), "deferred.json"), 0o755); err != nil {
		t.Fatal(err)
	}

	out, err := DashboardDeleteDeferred(root, "a")
	dashDelWantErr(t, err, "infra", "Check read permission on .sdlc-v2/history/deferred.json and retry.")
	if out != (DashboardDeleteOut{}) {
		t.Errorf("out = %+v, want zero value", out)
	}
}

func TestDashboardDeleteDeferred_BadJSONIsDataError(t *testing.T) {
	root := t.TempDir()
	bad := []byte(`[{"id": "a", "status": `)
	path := dashDelDeferredFile(t, root, bad)

	out, err := DashboardDeleteDeferred(root, "a")
	dashDelWantErr(t, err, "data", "Fix the JSON syntax in .sdlc-v2/history/deferred.json by hand, then retry.")
	if out != (DashboardDeleteOut{}) {
		t.Errorf("out = %+v, want zero value", out)
	}
	if got := dashDelReadFile(t, path); !bytes.Equal(got, bad) {
		t.Errorf("deferred.json changed: %q", got)
	}
}

func TestDashboardDeleteDeferred_TooLargeIsDataError(t *testing.T) {
	root := t.TempDir()
	// Valid JSON with white space padding: without the size check the file would parse.
	big := append([]byte("[]"), bytes.Repeat([]byte(" "), dashboardDeleteReadMax)...)
	path := dashDelDeferredFile(t, root, big)

	out, err := DashboardDeleteDeferred(root, "a")
	dashDelWantErr(t, err, "data", "Remove resolved items from .sdlc-v2/history/deferred.json by hand until it is smaller than 8 MiB, then retry.")
	want := fmt.Sprintf("The file deferred.json is %d bytes, more than the 8388608 bytes (8 MiB) that a dashboard delete reads", len(big))
	if err.Error() != want {
		t.Errorf("message = %q, want %q", err.Error(), want)
	}
	if out != (DashboardDeleteOut{}) {
		t.Errorf("out = %+v, want zero value", out)
	}
	if got := dashDelReadFile(t, path); !bytes.Equal(got, big) {
		t.Errorf("deferred.json changed, length %d, want %d", len(got), len(big))
	}
}

func TestDashboardDeleteDeferred_SizeAtLimitIsRead(t *testing.T) {
	root := t.TempDir()
	// Exactly dashboardDeleteReadMax bytes is allowed.
	exact := append([]byte("[]"), bytes.Repeat([]byte(" "), dashboardDeleteReadMax-2)...)
	dashDelDeferredFile(t, root, exact)

	out, err := DashboardDeleteDeferred(root, "a")
	if err != nil {
		t.Fatalf("DashboardDeleteDeferred: %v", err)
	}
	if !out.AlreadyGone {
		t.Errorf("out = %+v, want AlreadyGone", out)
	}
}

func TestDashboardDeleteDeferred_WriteFailureIsInfra(t *testing.T) {
	root := t.TempDir()
	path := dashDelItems(t, root,
		dashDelItem("a", "item a", history.StatusOpen),
		dashDelItem("b", "item b", history.StatusOpen),
	)
	before := dashDelReadFile(t, path)
	injected := errors.New("injected write failure")
	dashDelSeam(t, &dashboardDeleteWriteJSON, func(string, any) error { return injected })

	out, err := DashboardDeleteDeferred(root, "a")
	dashDelWantErr(t, err, "infra", "Check write permission on .sdlc-v2/history/ and free disk space, then retry.")
	if !errors.Is(err, injected) {
		t.Errorf("error does not wrap the injected failure: %v", err)
	}
	if out != (DashboardDeleteOut{}) {
		t.Errorf("out = %+v, want zero value", out)
	}
	if got := dashDelReadFile(t, path); !bytes.Equal(got, before) {
		t.Errorf("deferred.json changed: %q", got)
	}
}

// TestDashboardDeleteDeferred_EmptyStoreMessage tells an empty or absent store from
// an unknown id in the message.
func TestDashboardDeleteDeferred_EmptyStoreMessage(t *testing.T) {
	out, err := DashboardDeleteDeferred(t.TempDir(), "a")
	if err != nil {
		t.Fatalf("DashboardDeleteDeferred: %v", err)
	}
	if want := "The deferred store has no items. The deferred item a is already gone."; out.Message != want {
		t.Errorf("message = %q, want %q", out.Message, want)
	}
}

// TestDashboardDeleteDeferred_FileGoneBeforeReadIsAlreadyGone gives already gone,
// not an InfraError, when deferred.json goes away between the size check and
// the read.
func TestDashboardDeleteDeferred_FileGoneBeforeReadIsAlreadyGone(t *testing.T) {
	root := t.TempDir()
	dashDelItems(t, root, dashDelItem("a", "item a", history.StatusOpen))
	dashDelSeam(t, &dashboardDeleteReadFile, func(string) ([]byte, error) {
		return nil, &fs.PathError{Op: "open", Path: "deferred.json", Err: fs.ErrNotExist}
	})

	out, err := DashboardDeleteDeferred(root, "a")
	if err != nil {
		t.Fatalf("DashboardDeleteDeferred: %v", err)
	}
	if !out.AlreadyGone || out.Deleted {
		t.Errorf("out = %+v, want AlreadyGone", out)
	}
}
