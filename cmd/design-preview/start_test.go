package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/execx"
)

// startShippedFiles is the shipped page of every test repo, by path relative to shippedPath.
var startShippedFiles = map[string]string{
	"index.html": "<!doctype html>\n<link rel=\"stylesheet\" href=\"static/app.css\">\n",
	"app.css":    "body { color: black; }\n",
	"js/app.js":  "console.log('v1');\n",
}

// startRequestLog is an empty request log: the header of the table and no row.
const startRequestLog = "# Design requests\n\n" +
	"| ID | Request | Why | Draft change | Deps | Status |\n" +
	"|---|---|---|---|---|---|\n"

// startClearGitEnv removes every GIT_* variable for the test, so child git
// processes work on the test repo and not on a repo a git hook points to.
func startClearGitEnv(t *testing.T) {
	t.Helper()
	for _, kv := range os.Environ() {
		key, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(key, "GIT_") {
			t.Setenv(key, "") // registers the restore
			os.Unsetenv(key)
		}
	}
}

// startGitOut runs git in dir with a fixed identity and no signing, and returns
// its output. It fails the test on error.
func startGitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	full := append([]string{"-c", "user.name=start-test", "-c", "user.email=start-test@example.com", "-c", "commit.gpgsign=false"}, args...)
	cmd := exec.Command("git", full...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}

	return strings.TrimSpace(string(out))
}

// startWrite writes content to rel under dir and creates the parent folders.
func startWrite(t *testing.T, dir, rel, content string) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", rel, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

// startRepo creates a git repo under t.TempDir with the shipped page and a
// .gitignore for .DS_Store in one commit, and returns its real path.
func startRepo(t *testing.T) string {
	t.Helper()
	startClearGitEnv(t)
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("eval symlinks: %v", err)
	}
	startGitOut(t, dir, "init", "-q")
	startWrite(t, dir, ".gitignore", ".DS_Store\n")
	for rel, content := range startShippedFiles {
		startWrite(t, dir, shippedPath+"/"+rel, content)
	}
	startGitOut(t, dir, "add", "-A")
	startGitOut(t, dir, "commit", "-q", "-m", "v1")

	return dir
}

// startCommitShipped changes one shipped file and commits it.
func startCommitShipped(t *testing.T, dir, rel, content string) {
	t.Helper()
	startWrite(t, dir, shippedPath+"/"+rel, content)
	// Add only the shipped page: a real draft folder is not tracked.
	startGitOut(t, dir, "add", "--", shippedPath)
	startGitOut(t, dir, "commit", "-q", "-m", "change "+rel)
}

// startHead returns the HEAD sha of dir.
func startHead(t *testing.T, dir string) string {
	t.Helper()
	return startGitOut(t, dir, "rev-parse", "HEAD")
}

// startSnapshot maps each path under dir, except .git, to its bytes. A
// folder maps to "<dir>".
func startSnapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	snap := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		if rel == ".git" {
			return filepath.SkipDir
		}
		if d.IsDir() {
			snap[rel] = "<dir>"
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		snap[rel] = string(data)
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	return snap
}

// startAssertUnchanged fails when the tree under dir differs from before.
func startAssertUnchanged(t *testing.T, dir string, before map[string]string) {
	t.Helper()
	if after := startSnapshot(t, dir); !reflect.DeepEqual(before, after) {
		t.Errorf("files changed\nbefore: %v\nafter:  %v", before, after)
	}
}

// startRun calls StartRule and fails the test on an error.
func startRun(t *testing.T, dir string, mode Mode) Outcome {
	t.Helper()
	out, err := StartRule(dir, mode)
	if err != nil {
		t.Fatalf("StartRule(%s): %v", mode, err)
	}

	return out
}

// startAssertOutcome compares an outcome with the wanted case, lines and Serve.
func startAssertOutcome(t *testing.T, got Outcome, wantCase CaseID, wantServe bool, wantLines ...string) {
	t.Helper()
	if got.Case != wantCase || got.Serve != wantServe {
		t.Errorf("case/serve = %q/%v, want %q/%v", got.Case, got.Serve, wantCase, wantServe)
	}
	if !reflect.DeepEqual(got.Lines, wantLines) {
		t.Errorf("lines\ngot:  %q\nwant: %q", got.Lines, wantLines)
	}
}

// startAssertErr runs StartRule, wants the error text want and no file change.
func startAssertErr(t *testing.T, dir string, mode Mode, want string) {
	t.Helper()
	before := startSnapshot(t, dir)
	_, err := StartRule(dir, mode)
	if err == nil || err.Error() != want {
		t.Errorf("error = %v, want %q", err, want)
	}
	startAssertUnchanged(t, dir, before)
}

// startReadBaseFile decodes design/dashboard/base.json of dir.
func startReadBaseFile(t *testing.T, dir string) baseRecord {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, draftDir, "base.json"))
	if err != nil {
		t.Fatalf("read base.json: %v", err)
	}
	var rec baseRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		t.Fatalf("decode base.json: %v", err)
	}

	return rec
}

// startAssertCopyTree checks the draft folder after a copy: static/ holds the
// shipped page at HEAD, dependencies.json is [], base.json holds HEAD, and
// nothing else is there.
func startAssertCopyTree(t *testing.T, dir string) {
	t.Helper()
	draft := filepath.Join(dir, draftDir)
	entries, err := os.ReadDir(draft)
	if err != nil {
		t.Fatalf("read draft folder: %v", err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if want := []string{"base.json", "dependencies.json", "static"}; !reflect.DeepEqual(names, want) {
		t.Errorf("draft folder = %v, want %v", names, want)
	}

	if got := startReadBaseFile(t, dir); got != (baseRecord{BaseCommit: startHead(t, dir), ShippedPath: shippedPath}) {
		t.Errorf("base.json = %+v, want HEAD %s", got, startHead(t, dir))
	}
	if data, _ := os.ReadFile(filepath.Join(draft, "dependencies.json")); string(data) != "[]\n" {
		t.Errorf("dependencies.json = %q, want %q", data, "[]\n")
	}

	tracked := strings.Split(startGitOut(t, dir, "ls-tree", "-r", "--name-only", "HEAD", "--", shippedPath), "\n")
	want := map[string]string{}
	for _, name := range tracked {
		data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
		if err != nil {
			t.Fatalf("read shipped %s: %v", name, err)
		}
		want[strings.TrimPrefix(name, shippedPath+"/")] = string(data)
	}
	got := map[string]string{}
	for rel, content := range startSnapshot(t, filepath.Join(draft, "static")) {
		if content != "<dir>" {
			got[filepath.ToSlash(rel)] = content
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("static/ = %v, want %v", got, want)
	}
}

// startDraftWithChange creates a draft (case a), then commits a shipped
// change, so the next auto run sees a changed shipped page. It returns the
// base sha of the draft.
func startDraftWithChange(t *testing.T, dir string) string {
	t.Helper()
	startRun(t, dir, ModeAuto)
	base := startHead(t, dir)
	startCommitShipped(t, dir, "app.css", "body { color: red; }\n")

	return base
}

// startCaseDLines gives the marker lines of case d for base.
func startCaseDLines(t *testing.T, dir, base string) []string {
	t.Helper()
	lines := []string{"design preview: case d — shipped page changed since " + base[:7] + ", and the draft has own work. Nothing changed."}
	lines = append(lines, strings.Split(startGitOut(t, dir, "log", "--oneline", "--no-color", base+"..HEAD", "--", shippedPath), "\n")...)

	return append(lines, nextLine)
}

// startSkipIfRoot skips a test that needs file permissions to block a write.
func startSkipIfRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root ignores file permissions")
	}
}

// startChmod sets mode on rel under dir and restores 0o755 at cleanup, so
// t.TempDir can remove the tree.
func startChmod(t *testing.T, dir, rel string, mode os.FileMode) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.Chmod(path, mode); err != nil {
		t.Fatalf("chmod %s: %v", rel, err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o755) })
}

// startExists reports whether rel exists under dir.
func startExists(dir, rel string) bool {
	_, err := os.Lstat(filepath.Join(dir, filepath.FromSlash(rel)))
	return err == nil
}

// TestStartRule covers each outcome, each error and the copy failures of
// StartRule in a real git repo.
func TestStartRule(t *testing.T) {
	t.Run("case a copies the shipped page", func(t *testing.T) {
		dir := startRepo(t)
		out := startRun(t, dir, ModeAuto)
		startAssertOutcome(t, out, "a", true,
			"design preview: case a — no draft. Copied the shipped page. Base commit "+startHead(t, dir)+".")
		startAssertCopyTree(t, dir)
	})

	t.Run("case b serves the draft and changes nothing", func(t *testing.T) {
		dir := startRepo(t)
		startRun(t, dir, ModeAuto)
		startWrite(t, dir, draftDir+"/static/app.css", "body { color: blue; }\n")
		before := startSnapshot(t, dir)
		out := startRun(t, dir, ModeAuto)
		startAssertOutcome(t, out, "b", true,
			"design preview: case b — shipped page unchanged since "+startHead(t, dir)[:7]+". Serving the draft.")
		startAssertUnchanged(t, dir, before)
	})

	t.Run("case c copies the changed shipped page", func(t *testing.T) {
		dir := startRepo(t)
		startDraftWithChange(t, dir)
		out := startRun(t, dir, ModeAuto)
		startAssertOutcome(t, out, "c", true,
			"design preview: case c — shipped page changed, the draft has no own work. Copied the shipped page. Base commit "+startHead(t, dir)+".")
		startAssertCopyTree(t, dir)
	})

	t.Run("an ignored extra file gives case c", func(t *testing.T) {
		dir := startRepo(t)
		startDraftWithChange(t, dir)
		startWrite(t, dir, draftDir+"/static/.DS_Store", "finder")
		out := startRun(t, dir, ModeAuto)
		if out.Case != "c" {
			t.Fatalf("case = %q, want c", out.Case)
		}
		startAssertCopyTree(t, dir)
	})

	t.Run("an ignored file in the shipped folder is not copied", func(t *testing.T) {
		dir := startRepo(t)
		startWrite(t, dir, shippedPath+"/.DS_Store", "finder")
		startWrite(t, dir, shippedPath+"/js/.DS_Store", "finder")
		out := startRun(t, dir, ModeAuto)
		if out.Case != "a" {
			t.Fatalf("case = %q, want a", out.Case)
		}
		for _, rel := range []string{"static/.DS_Store", "static/js/.DS_Store"} {
			if _, err := os.Lstat(filepath.Join(dir, draftDir, filepath.FromSlash(rel))); !os.IsNotExist(err) {
				t.Errorf("%s was copied from the shipped folder: err=%v", rel, err)
			}
		}
		startAssertCopyTree(t, dir)
	})

	ownWork := []struct {
		name  string
		setup func(t *testing.T, dir string)
	}{
		{"changed file", func(t *testing.T, dir string) {
			startWrite(t, dir, draftDir+"/static/index.html", "<!doctype html>\n<p>draft</p>\n")
		}},
		{"whitespace-only change", func(t *testing.T, dir string) {
			// Only a trailing blank line differs. A text compare of trimmed git
			// output would call the file equal; a blob id compare does not.
			startWrite(t, dir, draftDir+"/static/app.css", startShippedFiles["app.css"]+"\n")
		}},
		{"extra file", func(t *testing.T, dir string) {
			startWrite(t, dir, draftDir+"/static/js/new.js", "console.log('new');\n")
		}},
		{"missing file", func(t *testing.T, dir string) {
			if err := os.Remove(filepath.Join(dir, draftDir, "static", "js", "app.js")); err != nil {
				t.Fatal(err)
			}
		}},
		{"one dependency record", func(t *testing.T, dir string) {
			startWrite(t, dir, draftDir+"/dependencies.json",
				`[{"id":"D1","kind":"data","need":"Snapshot gives the wait time.","elements":[]}]`)
		}},
		{"one request row", func(t *testing.T, dir string) {
			startWrite(t, dir, draftDir+"/requests.md", startRequestLog+
				"| R1 | Show the total time of a run. | — | The head shows a time. | — | dropped |\n")
		}},
	}
	for _, tc := range ownWork {
		t.Run("case d own work: "+tc.name, func(t *testing.T) {
			dir := startRepo(t)
			base := startDraftWithChange(t, dir)
			tc.setup(t, dir)
			before := startSnapshot(t, dir)
			out, err := StartRule(dir, ModeAuto)
			if err != nil {
				t.Fatalf("StartRule: %v", err)
			}
			startAssertOutcome(t, out, "d", false, startCaseDLines(t, dir, base)...)
			startAssertUnchanged(t, dir, before)
		})
	}

	t.Run("an empty request log is not own work", func(t *testing.T) {
		dir := startRepo(t)
		startDraftWithChange(t, dir)
		startWrite(t, dir, draftDir+"/requests.md", startRequestLog+"\nNote: R1 is a plan for later.\n")
		out := startRun(t, dir, ModeAuto)
		if out.Case != "c" {
			t.Fatalf("case = %q, want c", out.Case)
		}
		startAssertCopyTree(t, dir)
	})

	badLogs := []struct {
		name  string
		setup func(t *testing.T, dir string)
	}{
		{"unreadable", func(t *testing.T, dir string) {
			startWrite(t, dir, draftDir+"/requests.md/keep", "x")
		}},
		{"over the size limit", func(t *testing.T, dir string) {
			startWrite(t, dir, draftDir+"/requests.md", strings.Repeat("x", maxLogBytes+1))
		}},
	}
	for _, tc := range badLogs {
		t.Run("a request log that is "+tc.name+" returns its error and changes nothing", func(t *testing.T) {
			dir := startRepo(t)
			startDraftWithChange(t, dir)
			tc.setup(t, dir)
			before := startSnapshot(t, dir)
			_, err := StartRule(dir, ModeAuto)
			if err == nil || !strings.HasPrefix(err.Error(), requestLogFile+": ") {
				t.Errorf("error = %v, want prefix %q", err, requestLogFile+": ")
			}
			startAssertUnchanged(t, dir, before)
		})
	}

	t.Run("a bad dependencies.json returns its load error", func(t *testing.T) {
		dir := startRepo(t)
		startDraftWithChange(t, dir)
		startWrite(t, dir, draftDir+"/dependencies.json", "{")
		path := filepath.Join(dir, draftDir, "dependencies.json")
		_, wantErr := LoadDependencies(path)
		if wantErr == nil {
			t.Fatal("LoadDependencies accepted the bad file")
		}
		startAssertErr(t, dir, ModeAuto, wantErr.Error())
	})

	t.Run("fresh discards the draft", func(t *testing.T) {
		dir := startRepo(t)
		startRun(t, dir, ModeAuto)
		startWrite(t, dir, draftDir+"/static/extra.js", "x\n")
		startWrite(t, dir, draftDir+"/requirements.md", "# needs\n")
		startWrite(t, dir, draftDir+"/requests.md", startRequestLog+
			"| R1 | Show the total time of a run. | — | The head shows a time. | — | applied |\n")
		startWrite(t, dir, draftDir+"/dependencies.json",
			`[{"id":"D1","kind":"flow","need":"Open a run.","elements":[]}]`)
		startCommitShipped(t, dir, "app.css", "body { color: green; }\n")
		out := startRun(t, dir, ModeFresh)
		startAssertOutcome(t, out, "fresh", true,
			"design preview: fresh — copied the shipped page. Base commit "+startHead(t, dir)+".")
		startAssertCopyTree(t, dir)
	})

	t.Run("continue keeps the draft and moves the base to HEAD", func(t *testing.T) {
		dir := startRepo(t)
		startRun(t, dir, ModeAuto)
		startWrite(t, dir, draftDir+"/static/extra.js", "x\n")
		startWrite(t, dir, draftDir+"/requirements.md", "# needs\n")
		startWrite(t, dir, draftDir+"/requests.md", startRequestLog+
			"| R1 | Show the total time of a run. | — | The head shows a time. | — | applied |\n")
		deps := `[{"id":"D1","kind":"flow","need":"Open a run.","elements":[]}]`
		startWrite(t, dir, draftDir+"/dependencies.json", deps)
		startCommitShipped(t, dir, "app.css", "body { color: green; }\n")
		before := startSnapshot(t, dir)

		out := startRun(t, dir, ModeContinue)
		head := startHead(t, dir)
		startAssertOutcome(t, out, "continue", true,
			"design preview: continue — kept the draft. Base commit "+head+".")
		if got := startReadBaseFile(t, dir); got != (baseRecord{BaseCommit: head, ShippedPath: shippedPath}) {
			t.Errorf("base.json = %+v, want HEAD %s", got, head)
		}
		after := startSnapshot(t, dir)
		baseKey := filepath.Join(draftDir, "base.json")
		delete(before, baseKey)
		delete(after, baseKey)
		if !reflect.DeepEqual(before, after) {
			t.Errorf("continue changed a file other than base.json\nbefore: %v\nafter:  %v", before, after)
		}
		// The next auto run serves the kept draft.
		if out := startRun(t, dir, ModeAuto); out.Case != "b" {
			t.Errorf("auto after continue: case = %q, want b", out.Case)
		}
	})

	t.Run("dirty shipped page is an error", func(t *testing.T) {
		dir := startRepo(t)
		startRun(t, dir, ModeAuto)
		startWrite(t, dir, shippedPath+"/app.css", "body { color: dirty; }\n")
		for _, mode := range []Mode{ModeAuto, ModeFresh, ModeContinue, ModeMerge} {
			startAssertErr(t, dir, mode, "the shipped page has uncommitted changes. Commit or stash them, then run task design again.")
		}
	})

	t.Run("missing base.json is an error", func(t *testing.T) {
		dir := startRepo(t)
		startRun(t, dir, ModeAuto)
		if err := os.Remove(filepath.Join(dir, draftDir, "base.json")); err != nil {
			t.Fatal(err)
		}
		startAssertErr(t, dir, ModeAuto, "design/dashboard/base.json is missing. Run task design:fresh (discard the draft) or task design:continue (keep the draft).")
	})

	badBase := []struct {
		name    string
		content func(t *testing.T, dir string) string
		cause   string // a part of the cause in the message
	}{
		{"short sha", func(*testing.T, string) string { return `{"baseCommit":"abc123","shippedPath":"x"}` },
			`"abc123" is not a 40-character lowercase sha`},
		{"upper-case sha", func(t *testing.T, dir string) string {
			return `{"baseCommit":"` + strings.ToUpper(startHead(t, dir)) + `"}`
		}, "is not a 40-character lowercase sha"},
		{"unknown sha", func(*testing.T, string) string {
			return `{"baseCommit":"` + strings.Repeat("deadbeef", 5) + `"}`
		}, "git cat-file exit "},
		{"sha of a blob", func(t *testing.T, dir string) string {
			return `{"baseCommit":"` + startGitOut(t, dir, "rev-parse", "HEAD:"+shippedPath+"/app.css") + `"}`
		}, "git cat-file exit "},
		{"bad JSON", func(*testing.T, string) string { return `{"baseCommit":` }, "unexpected end of JSON input"},
		{"over 4 KiB", func(t *testing.T, dir string) string {
			return `{"baseCommit":"` + startHead(t, dir) + `","pad":"` + strings.Repeat("x", 4096) + `"}`
		}, "larger than 4 KiB"},
	}
	for _, tc := range badBase {
		t.Run("bad base.json: "+tc.name, func(t *testing.T) {
			dir := startRepo(t)
			startRun(t, dir, ModeAuto)
			startWrite(t, dir, draftDir+"/base.json", tc.content(t, dir))
			before := startSnapshot(t, dir)
			_, err := StartRule(dir, ModeAuto)
			if !errors.Is(err, errBadBase) {
				t.Fatalf("error = %v, want errBadBase", err)
			}
			// The message names the cause between the sentinel and the recovery text.
			const prefix = "design/dashboard/base.json: baseCommit is not a known commit ("
			const suffix = "). Run task design:fresh (discard the draft) or task design:continue (keep the draft)."
			msg := err.Error()
			if !strings.HasPrefix(msg, prefix) || !strings.HasSuffix(msg, suffix) || !strings.Contains(msg, tc.cause) {
				t.Errorf("error = %q, want %q<cause with %q>%q", msg, prefix, tc.cause, suffix)
			}
			startAssertUnchanged(t, dir, before)
		})
	}

	t.Run("continue without a draft is an error", func(t *testing.T) {
		dir := startRepo(t)
		startAssertErr(t, dir, ModeContinue, "no draft in design/dashboard/static/. Run task design first.")
	})

	t.Run("a read failure names the path", func(t *testing.T) {
		dir := startRepo(t)
		startWrite(t, dir, "design/dashboard", "not a folder")
		for _, mode := range []Mode{ModeAuto, ModeFresh, ModeContinue, ModeMerge} {
			before := startSnapshot(t, dir)
			_, err := StartRule(dir, mode)
			if err == nil || !strings.HasPrefix(err.Error(), "design/dashboard/static: ") {
				t.Errorf("%s: error = %v, want prefix %q", mode, err, "design/dashboard/static: ")
			}
			startAssertUnchanged(t, dir, before)
		}
	})

	t.Run("unknown mode is an error", func(t *testing.T) {
		dir := startRepo(t)
		startAssertErr(t, dir, Mode("other"), `unknown mode "other". Allowed: auto, fresh, continue, merge.`)
	})

	t.Run("copy failure: base.json cannot be removed", func(t *testing.T) {
		startSkipIfRoot(t)
		dir := startRepo(t)
		startRun(t, dir, ModeAuto)
		startChmod(t, dir, draftDir, 0o555)
		before := startSnapshot(t, dir)
		_, err := StartRule(dir, ModeFresh)
		if err == nil || !strings.HasPrefix(err.Error(), "design/dashboard/base.json: ") {
			t.Errorf("error = %v, want a base.json error", err)
		}
		startAssertUnchanged(t, dir, before)
	})

	t.Run("copy failure: .static.tmp cannot be created", func(t *testing.T) {
		startSkipIfRoot(t)
		dir := startRepo(t)
		startRun(t, dir, ModeAuto)
		if err := os.Remove(filepath.Join(dir, draftDir, "base.json")); err != nil {
			t.Fatal(err)
		}
		startChmod(t, dir, draftDir, 0o555)
		before := startSnapshot(t, dir)
		_, err := StartRule(dir, ModeFresh)
		if err == nil || !strings.HasPrefix(err.Error(), "design/dashboard/.static.tmp: ") {
			t.Errorf("error = %v, want a .static.tmp error", err)
		}
		startAssertUnchanged(t, dir, before)
	})

	t.Run("copy failure: static cannot be removed", func(t *testing.T) {
		startSkipIfRoot(t)
		dir := startRepo(t)
		startRun(t, dir, ModeAuto)
		startWrite(t, dir, draftDir+"/static/app.css", "body { color: draft; }\n")
		startChmod(t, dir, draftDir+"/static", 0o555)
		_, err := StartRule(dir, ModeFresh)
		if err == nil || !strings.HasPrefix(err.Error(), "design/dashboard/static: ") {
			t.Errorf("error = %v, want a static error", err)
		}
		if startExists(dir, draftDir+"/base.json") {
			t.Error("base.json exists after the failed copy")
		}
		if !startExists(dir, draftDir+"/.static.tmp/js/app.js") {
			t.Error(".static.tmp/ is not complete after the failed copy")
		}
		if data, _ := os.ReadFile(filepath.Join(dir, draftDir, "static", "app.css")); string(data) != "body { color: draft; }\n" {
			t.Errorf("static/app.css = %q, want the draft text", data)
		}
		// The interrupted copy has no base.json, so the next auto run stops.
		_, err = StartRule(dir, ModeAuto)
		if err != errMissingBase {
			t.Errorf("auto after the failed copy: error = %v, want the missing-base error", err)
		}
	})

	t.Run("copy failure: dependencies.json cannot be written", func(t *testing.T) {
		dir := startRepo(t)
		startRun(t, dir, ModeAuto)
		if err := os.Remove(filepath.Join(dir, draftDir, "dependencies.json")); err != nil {
			t.Fatal(err)
		}
		startWrite(t, dir, draftDir+"/dependencies.json/keep", "x")
		_, err := StartRule(dir, ModeFresh)
		if err == nil || !strings.HasPrefix(err.Error(), "design/dashboard/dependencies.json: ") {
			t.Errorf("error = %v, want a dependencies.json error", err)
		}
		if startExists(dir, draftDir+"/base.json") {
			t.Error("base.json exists after the failed copy")
		}
		if startExists(dir, draftDir+"/.static.tmp") || !startExists(dir, draftDir+"/static/js/app.js") {
			t.Error("static/ is not in place after the failed copy")
		}
	})

	t.Run("copy failure: requirements.md cannot be removed", func(t *testing.T) {
		dir := startRepo(t)
		startRun(t, dir, ModeAuto)
		startWrite(t, dir, draftDir+"/requirements.md/keep", "x")
		_, err := StartRule(dir, ModeFresh)
		if err == nil || !strings.HasPrefix(err.Error(), "design/dashboard/requirements.md: ") {
			t.Errorf("error = %v, want a requirements.md error", err)
		}
		if startExists(dir, draftDir+"/base.json") {
			t.Error("base.json exists after the failed copy")
		}
		if data, _ := os.ReadFile(filepath.Join(dir, draftDir, "dependencies.json")); string(data) != "[]\n" {
			t.Errorf("dependencies.json = %q, want %q", data, "[]\n")
		}
	})

	t.Run("copy failure: requests.md cannot be removed", func(t *testing.T) {
		dir := startRepo(t)
		startRun(t, dir, ModeAuto)
		startWrite(t, dir, draftDir+"/requests.md/keep", "x")
		_, err := StartRule(dir, ModeFresh)
		if err == nil || !strings.HasPrefix(err.Error(), requestLogFile+": ") {
			t.Errorf("error = %v, want a requests.md error", err)
		}
		if startExists(dir, draftDir+"/base.json") {
			t.Error("base.json exists after the failed copy")
		}
		if data, _ := os.ReadFile(filepath.Join(dir, draftDir, "dependencies.json")); string(data) != "[]\n" {
			t.Errorf("dependencies.json = %q, want %q", data, "[]\n")
		}
	})
}

// startSwap sets *seam to fake for the test and restores it at cleanup.
func startSwap[T any](t *testing.T, seam *T, fake T) {
	t.Helper()
	orig := *seam
	*seam = fake
	t.Cleanup(func() { *seam = orig })
}

// errStartInjected is the error of every injected failure.
var errStartInjected = errors.New("injected failure")

// startFailGitRun makes the gitRun call fail whose arguments match.
func startFailGitRun(t *testing.T, match func(args []string) bool) {
	t.Helper()
	real := gitRun
	startSwap(t, &gitRun, func(cmd string, args []string, opt execx.Options) (string, error) {
		if match(args) {
			return "", errStartInjected
		}
		return real(cmd, args, opt)
	})
}

// startFakeGitRun replaces the output of the gitRun call whose arguments
// match with edit(real output).
func startFakeGitRun(t *testing.T, match func(args []string) bool, edit func(out string) string) {
	t.Helper()
	real := gitRun
	startSwap(t, &gitRun, func(cmd string, args []string, opt execx.Options) (string, error) {
		out, err := real(cmd, args, opt)
		if err == nil && match(args) {
			out = edit(out)
		}
		return out, err
	})
}

// startFailGitAllowExit makes the gitRunAllowExit call with subcommand sub
// fail: code < 0 returns errStartInjected, any other code returns that exit
// code with the stderr text "boom".
func startFailGitAllowExit(t *testing.T, sub string, code int) {
	t.Helper()
	real := gitRunAllowExit
	startSwap(t, &gitRunAllowExit, func(cmd string, args []string, opt execx.Options) (string, string, int, error) {
		if args[0] != sub {
			return real(cmd, args, opt)
		}
		if code < 0 {
			return "", "", 0, errStartInjected
		}
		return "", "boom", code, nil
	})
}

// startSub matches git arguments by subcommand.
func startSub(sub string) func([]string) bool {
	return func(args []string) bool { return args[0] == sub }
}

// startLsTree matches the ls-tree call of the copy (nameOnly true) or of the
// blob list (nameOnly false).
func startLsTree(nameOnly bool) func([]string) bool {
	return func(args []string) bool {
		if args[0] != "ls-tree" {
			return false
		}
		for _, a := range args {
			if a == "--name-only" {
				return nameOnly
			}
		}
		return !nameOnly
	}
}

// TestStartRuleEdges covers the git failures, the copy failure points, the
// recovery from a broken draft and the boundary cases of StartRule.
func TestStartRuleEdges(t *testing.T) {
	t.Run("case d own work: renamed file with the same file count", func(t *testing.T) {
		dir := startRepo(t)
		base := startDraftWithChange(t, dir)
		from := filepath.Join(dir, draftDir, "static", "js", "app.js")
		if err := os.Rename(from, filepath.Join(dir, draftDir, "static", "js", "other.js")); err != nil {
			t.Fatal(err)
		}
		before := startSnapshot(t, dir)
		out := startRun(t, dir, ModeAuto)
		startAssertOutcome(t, out, CaseD, false, startCaseDLines(t, dir, base)...)
		startAssertUnchanged(t, dir, before)
	})

	t.Run("case d own work: empty static folder, and no check-ignore call", func(t *testing.T) {
		dir := startRepo(t)
		base := startDraftWithChange(t, dir)
		static := filepath.Join(dir, draftDir, "static")
		if err := os.RemoveAll(static); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(static, 0o755); err != nil {
			t.Fatal(err)
		}
		var subs []string
		real := gitRunAllowExit
		startSwap(t, &gitRunAllowExit, func(cmd string, args []string, opt execx.Options) (string, string, int, error) {
			subs = append(subs, args[0])
			return real(cmd, args, opt)
		})
		before := startSnapshot(t, dir)
		out := startRun(t, dir, ModeAuto)
		startAssertOutcome(t, out, CaseD, false, startCaseDLines(t, dir, base)...)
		startAssertUnchanged(t, dir, before)
		for _, sub := range subs {
			if sub == "check-ignore" {
				t.Error("git check-ignore ran for an empty static folder")
			}
		}
	})

	t.Run("a folder that is not a git repo fails at git status and creates nothing", func(t *testing.T) {
		startClearGitEnv(t)
		dir, err := filepath.EvalSymlinks(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		for _, mode := range []Mode{ModeAuto, ModeFresh, ModeContinue, ModeMerge} {
			_, err := StartRule(dir, mode)
			if err == nil || !strings.HasPrefix(err.Error(), shippedPath+": ") {
				t.Errorf("%s: error = %v, want prefix %q", mode, err, shippedPath+": ")
			}
			if startExists(dir, "design") {
				t.Errorf("%s: design/ was created", mode)
			}
		}
	})

	t.Run("a repo with no commit fails at git rev-parse and creates nothing", func(t *testing.T) {
		startClearGitEnv(t)
		dir, err := filepath.EvalSymlinks(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		startGitOut(t, dir, "init", "-q")
		for _, mode := range []Mode{ModeAuto, ModeFresh, ModeContinue, ModeMerge} {
			_, err := StartRule(dir, mode)
			if err == nil || !strings.HasPrefix(err.Error(), "HEAD: ") {
				t.Errorf("%s: error = %v, want prefix %q", mode, err, "HEAD: ")
			}
			if startExists(dir, "design") {
				t.Errorf("%s: design/ was created", mode)
			}
		}
	})

	gitFailures := []struct {
		name    string
		mode    Mode
		setup   func(t *testing.T, dir string) // the draft state before the failure
		inject  func(t *testing.T)
		wantErr string
	}{
		{"git diff exits 2", ModeAuto, startDraftOnly, func(t *testing.T) { startFailGitAllowExit(t, "diff", 2) },
			shippedPath + ": git diff exit 2: boom"},
		{"git diff cannot run", ModeAuto, startDraftOnly, func(t *testing.T) { startFailGitAllowExit(t, "diff", -1) },
			shippedPath + ": " + errStartInjected.Error()},
		{"git check-ignore exits 128", ModeAuto, startDraftChanged, func(t *testing.T) { startFailGitAllowExit(t, "check-ignore", 128) },
			staticDir + ": git check-ignore exit 128: boom"},
		{"git check-ignore cannot run", ModeAuto, startDraftChanged, func(t *testing.T) { startFailGitAllowExit(t, "check-ignore", -1) },
			staticDir + ": " + errStartInjected.Error()},
		{"git cat-file cannot run", ModeAuto, startDraftOnly, func(t *testing.T) { startFailGitAllowExit(t, "cat-file", -1) },
			baseFile + ": " + errStartInjected.Error()},
		{"git status fails", ModeAuto, startDraftOnly, func(t *testing.T) { startFailGitRun(t, startSub("status")) },
			shippedPath + ": " + errStartInjected.Error()},
		{"git rev-parse fails", ModeFresh, startDraftOnly, func(t *testing.T) { startFailGitRun(t, startSub("rev-parse")) },
			"HEAD: " + errStartInjected.Error()},
		{"git log fails", ModeAuto, startDraftOwnWork, func(t *testing.T) { startFailGitRun(t, startSub("log")) },
			shippedPath + ": " + errStartInjected.Error()},
		{"git hash-object fails", ModeAuto, startDraftChanged, func(t *testing.T) { startFailGitRun(t, startSub("hash-object")) },
			staticDir + ": " + errStartInjected.Error()},
		{"git ls-tree of the blobs fails", ModeAuto, startDraftChanged, func(t *testing.T) { startFailGitRun(t, startLsTree(false)) },
			shippedPath + ": " + errStartInjected.Error()},
		{"git ls-tree of the copy fails", ModeFresh, startDraftOnly, func(t *testing.T) { startFailGitRun(t, startLsTree(true)) },
			shippedPath + ": " + errStartInjected.Error()},
		{"git hash-object gives too few ids", ModeAuto, startDraftChanged, func(t *testing.T) {
			startFakeGitRun(t, startSub("hash-object"), func(string) string { return "0123" })
		}, staticDir + ": git hash-object gave 1 ids for 3 files"},
		{"git ls-tree gives an entry without a tab", ModeAuto, startDraftChanged, func(t *testing.T) {
			startFakeGitRun(t, startLsTree(false), func(string) string { return "100644 blob 0123" })
		}, shippedPath + ": unexpected git ls-tree entry \"100644 blob 0123\""},
	}
	for _, tc := range gitFailures {
		t.Run(tc.name, func(t *testing.T) {
			dir := startRepo(t)
			tc.setup(t, dir)
			tc.inject(t)
			startAssertErr(t, dir, tc.mode, tc.wantErr)
		})
	}

	t.Run("a submodule entry in the shipped tree is skipped", func(t *testing.T) {
		dir := startRepo(t)
		startDraftChanged(t, dir)
		startFakeGitRun(t, startLsTree(false), func(out string) string {
			return out + "\x00160000 commit " + strings.Repeat("ab", 20) + "\t" + shippedPath + "/sub"
		})
		out := startRun(t, dir, ModeAuto)
		if out.Case != CaseC {
			t.Fatalf("case = %q, want c", out.Case)
		}
	})

	t.Run("an unreadable base.json names the file and is not errBadBase", func(t *testing.T) {
		dir := startRepo(t)
		startRun(t, dir, ModeAuto)
		base := filepath.Join(dir, draftDir, "base.json")
		if err := os.Remove(base); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(base, 0o755); err != nil {
			t.Fatal(err)
		}
		before := startSnapshot(t, dir)
		_, err := StartRule(dir, ModeAuto)
		if err == nil || err.Error() != baseFile+": is a directory" {
			t.Errorf("error = %v, want %q", err, baseFile+": is a directory")
		}
		if errors.Is(err, errBadBase) {
			t.Error("a read failure was reported as errBadBase")
		}
		startAssertUnchanged(t, dir, before)
	})

	t.Run("continue: base.json cannot be written", func(t *testing.T) {
		startSkipIfRoot(t)
		dir := startRepo(t)
		startRun(t, dir, ModeAuto)
		startChmod(t, dir, draftDir, 0o555)
		before := startSnapshot(t, dir)
		_, err := StartRule(dir, ModeContinue)
		if err == nil || !strings.HasPrefix(err.Error(), baseFile+": ") {
			t.Errorf("error = %v, want prefix %q", err, baseFile+": ")
		}
		startAssertUnchanged(t, dir, before)
	})

	recovery := []struct {
		name  string
		mode  Mode
		setup func(t *testing.T, dir string)
	}{
		{"continue after base.json is missing", ModeContinue, func(t *testing.T, dir string) {
			startRun(t, dir, ModeAuto)
			if err := os.Remove(filepath.Join(dir, draftDir, "base.json")); err != nil {
				t.Fatal(err)
			}
		}},
		{"continue after base.json is corrupt", ModeContinue, func(t *testing.T, dir string) {
			startRun(t, dir, ModeAuto)
			startWrite(t, dir, baseFile, "{")
		}},
		{"fresh after base.json is corrupt", ModeFresh, func(t *testing.T, dir string) {
			startRun(t, dir, ModeAuto)
			startWrite(t, dir, baseFile, "{")
		}},
		{"fresh with no draft", ModeFresh, func(*testing.T, string) {}},
	}
	for _, tc := range recovery {
		t.Run("recovery: "+tc.name, func(t *testing.T) {
			dir := startRepo(t)
			tc.setup(t, dir)
			startWrite(t, dir, staticDir+"/app.css", "body { color: draft; }\n")
			startRun(t, dir, tc.mode)
			if tc.mode == ModeContinue {
				if data, _ := os.ReadFile(filepath.Join(dir, staticDir, "app.css")); string(data) != "body { color: draft; }\n" {
					t.Errorf("continue changed static/app.css: %q", data)
				}
				if got := startReadBaseFile(t, dir); got != (baseRecord{BaseCommit: startHead(t, dir), ShippedPath: shippedPath}) {
					t.Errorf("base.json = %+v, want HEAD", got)
				}
			} else {
				startAssertCopyTree(t, dir)
			}
			if out := startRun(t, dir, ModeAuto); out.Case != CaseB {
				t.Errorf("next auto run: case = %q, want b", out.Case)
			}
		})
	}

	// The state of static/ after a failed copy.
	const (
		stateDraft   = iota // static/ still holds the draft
		stateGone           // static/ was removed and .static.tmp/ holds the copy
		stateShipped        // static/ holds the shipped page
	)
	copyFailures := []struct {
		name    string
		inject  func(t *testing.T, dir string)
		wantErr string // prefix of the error
		static  int    // the state of static/ after the failure
	}{
		{"old .static.tmp cannot be removed", func(t *testing.T, dir string) {
			startSkipIfRoot(t)
			startWrite(t, dir, tmpStaticDir+"/old.txt", "x")
			startChmod(t, dir, tmpStaticDir, 0o555)
		}, tmpStaticDir + ": ", stateDraft},
		{"a shipped file cannot be read", func(t *testing.T, _ string) {
			real := copyReadFile
			startSwap(t, &copyReadFile, func(path string) ([]byte, error) {
				if filepath.Base(path) == "app.css" {
					return nil, errStartInjected
				}
				return real(path)
			})
		}, shippedPath + "/app.css: ", stateDraft},
		{"a tmp subfolder cannot be created", func(t *testing.T, _ string) {
			real := copyMkdirAll
			startSwap(t, &copyMkdirAll, func(path string, perm os.FileMode) error {
				if filepath.Base(path) == "js" {
					return errStartInjected
				}
				return real(path, perm)
			})
		}, tmpStaticDir + "/js/app.js: ", stateDraft},
		{"a tmp file cannot be written", func(t *testing.T, _ string) {
			startSwap(t, &copyWriteFile, func(string, []byte, os.FileMode) error { return errStartInjected })
		}, tmpStaticDir + "/", stateDraft},
		{"tmp cannot be renamed to static", func(t *testing.T, _ string) {
			startSwap(t, &copyRename, func(string, string) error { return errStartInjected })
		}, staticDir + ": ", stateGone},
		{"the final base.json cannot be written", func(t *testing.T, _ string) {
			startSwap(t, &writeBaseJSON, func(string, any) error { return errStartInjected })
		}, baseFile + ": ", stateShipped},
	}
	for _, tc := range copyFailures {
		t.Run("copy failure: "+tc.name, func(t *testing.T) {
			dir := startRepo(t)
			startRun(t, dir, ModeAuto)
			startWrite(t, dir, staticDir+"/app.css", "body { color: draft; }\n")
			tc.inject(t, dir)

			_, err := StartRule(dir, ModeFresh)
			if err == nil || !strings.HasPrefix(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want prefix %q", err, tc.wantErr)
			}
			if startExists(dir, baseFile) {
				t.Error("base.json exists after the failed copy")
			}
			data, _ := os.ReadFile(filepath.Join(dir, staticDir, "app.css"))
			switch tc.static {
			case stateDraft:
				if string(data) != "body { color: draft; }\n" {
					t.Errorf("static/app.css = %q, want the draft text", data)
				}
			case stateGone:
				if startExists(dir, staticDir) || !startExists(dir, tmpStaticDir+"/js/app.js") {
					t.Error("want static/ removed and .static.tmp/ complete after the failed rename")
				}
				return // with no static/, the next auto run is case a
			case stateShipped:
				if string(data) != startShippedFiles["app.css"] {
					t.Errorf("static/app.css = %q, want the shipped text", data)
				}
			}
			// The interrupted copy has no base.json, so the next auto run stops.
			if _, err := StartRule(dir, ModeAuto); err != errMissingBase {
				t.Errorf("auto after the failed copy: error = %v, want the missing-base error", err)
			}
		})
	}
}

// startDraftOnly creates a draft with case a.
func startDraftOnly(t *testing.T, dir string) {
	t.Helper()
	startRun(t, dir, ModeAuto)
}

// startDraftChanged creates a draft with no own work, then commits a shipped
// change, so the next auto run reaches the own-work check.
func startDraftChanged(t *testing.T, dir string) {
	t.Helper()
	startDraftWithChange(t, dir)
}

// startDraftOwnWork is startDraftChanged plus own work in the draft, so the
// next auto run reaches case d.
func startDraftOwnWork(t *testing.T, dir string) {
	t.Helper()
	startDraftWithChange(t, dir)
	startWrite(t, dir, staticDir+"/index.html", "<!doctype html>\n<p>draft</p>\n")
}

// TestStartHasRequests pins which request log rows count as a request and
// the size limit of the log: a row starts at column 0 with "|", then an
// upper-case R and digits, then "|", with or without spaces.
func TestStartHasRequests(t *testing.T) {
	cases := []struct {
		name string
		rows string
		want bool
	}{
		{"one-digit id with spaces", "| R1 | a | b | c | d | open |\n", true},
		{"two-digit id", "| R12 | a | b | c | d | open |\n", true},
		{"no spaces", "|R3|a|b|c|d|open|\n", true},
		{"tab before the id", "|\tR4\t| a | b | c | d | open |\n", true},
		{"indented row", "  | R5 | a | b | c | d | open |\n", false},
		{"lower-case r", "| r6 | a | b | c | d | open |\n", false},
		{"id with no digits", "| R | a | b | c | d | open |\n", false},
		{"id with a suffix", "| R7a | a | b | c | d | open |\n", false},
		{"header only", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			startWrite(t, dir, requestLogFile, startRequestLog+tc.rows)
			got, err := startHasRequests(dir)
			if err != nil {
				t.Fatalf("startHasRequests: %v", err)
			}
			if got != tc.want {
				t.Errorf("startHasRequests = %v, want %v", got, tc.want)
			}
		})
	}

	t.Run("missing log has no request", func(t *testing.T) {
		got, err := startHasRequests(t.TempDir())
		if err != nil || got {
			t.Errorf("startHasRequests = %v, %v, want false, nil", got, err)
		}
	})

	t.Run("a log of exactly maxLogBytes is read", func(t *testing.T) {
		dir := t.TempDir()
		row := "| R1 | a | b | c | d | open |\n"
		content := startRequestLog + row
		content += strings.Repeat("x", maxLogBytes-len(content))
		startWrite(t, dir, requestLogFile, content)
		got, err := startHasRequests(dir)
		if err != nil || !got {
			t.Errorf("startHasRequests = %v, %v, want true, nil", got, err)
		}
		startWrite(t, dir, requestLogFile, content+"x")
		if _, err := startHasRequests(dir); err == nil || !strings.HasPrefix(err.Error(), requestLogFile+": ") {
			t.Errorf("one byte over: error = %v, want prefix %q", err, requestLogFile+": ")
		}
	})
}

// The three versions of the seven-line file of the merge tests. The draft
// changes line 1 and HEAD changes line 6, so git merge-file merges them clean.
const (
	startLines7Base  = "1\n2\n3\n4\n5\n6\n7\n"
	startLines7Draft = "X\n2\n3\n4\n5\n6\n7\n"
	startLines7Head  = "1\n2\n3\n4\n5\nY\n7\n"
	startLines7Clean = "X\n2\n3\n4\n5\nY\n7\n"
)

// startConflictText is the git merge-file result of draft and head with
// conflict markers, for one-line files.
func startConflictText(draft, head string) string {
	return "<<<<<<< draft\n" + draft + "=======\n" + head + ">>>>>>> HEAD\n"
}

// startStr returns a pointer to s, for an optional file version.
func startStr(s string) *string { return &s }

// startCommitDelete deletes one shipped file and commits it.
func startCommitDelete(t *testing.T, dir, rel string) {
	t.Helper()
	startGitOut(t, dir, "rm", "-q", "--", shippedPath+"/"+rel)
	startGitOut(t, dir, "commit", "-q", "-m", "delete "+rel)
}

// startBackups returns the backup folders of dir, relative to dir, sorted.
func startBackups(t *testing.T, dir string) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, filepath.FromSlash(draftDir), "static.bak-*"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	var rels []string
	for _, m := range matches {
		rels = append(rels, draftDir+"/"+filepath.Base(m))
	}

	return rels
}

// startOneBackup returns the one backup folder of dir. It fails the test when
// there is not exactly one.
func startOneBackup(t *testing.T, dir string) string {
	t.Helper()
	backups := startBackups(t, dir)
	if len(backups) != 1 {
		t.Fatalf("backup folders = %v, want one", backups)
	}

	return backups[0]
}

// startDraftState maps each path under static/ to its bytes, and the key
// "<base.json>" to the bytes of base.json.
func startDraftState(t *testing.T, dir string) map[string]string {
	t.Helper()
	state := startSnapshot(t, filepath.Join(dir, filepath.FromSlash(staticDir)))
	data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(baseFile)))
	if err != nil {
		t.Fatalf("read base.json: %v", err)
	}
	state["<base.json>"] = string(data)

	return state
}

// startAssertBackup fails when the backup folder differs from the draft
// snapshot draft.
func startAssertBackup(t *testing.T, dir, backup string, draft map[string]string) {
	t.Helper()
	if got := startSnapshot(t, filepath.Join(dir, filepath.FromSlash(backup))); !reflect.DeepEqual(got, draft) {
		t.Errorf("backup %s\ngot:  %v\nwant: %v", backup, got, draft)
	}
}

// startAssertNoWorkFolders fails when a work folder of the merge is left.
func startAssertNoWorkFolders(t *testing.T, dir string) {
	t.Helper()
	for _, rel := range []string{oldStaticDir, mergeTmpDir, tmpStaticDir} {
		if startExists(dir, rel) {
			t.Errorf("%s is left after the merge", rel)
		}
	}
}

// startMergeWork creates a draft that changes line 1 of f.txt, then commits
// a HEAD that changes line 6 of f.txt, changes app.css and deletes
// js/app.js. A merge of it is clean and merges 3 files. It returns the
// repo and the base sha.
func startMergeWork(t *testing.T) (string, string) {
	t.Helper()
	dir := startRepo(t)
	startCommitShipped(t, dir, "f.txt", startLines7Base)
	startRun(t, dir, ModeAuto)
	base := startHead(t, dir)
	startWrite(t, dir, staticDir+"/f.txt", startLines7Draft)
	startCommitShipped(t, dir, "f.txt", startLines7Head)
	startCommitShipped(t, dir, "app.css", "body { color: red; }\n")
	startCommitDelete(t, dir, "js/app.js")

	return dir, base
}

// startCleanLine is the marker line of a clean merge.
func startCleanLine(n int, head, backup string) string {
	return fmt.Sprintf("design preview: merge — merged %d files. Base commit %s. Backup %s.", n, head, backup)
}

// TestStartMergeFiles runs each row of the per-file merge table through
// StartRule in merge mode, in a real git repo.
func TestStartMergeFiles(t *testing.T) {
	s := startStr
	rows := []struct {
		name              string
		base, draft, head *string // nil: the file is absent
		want              *string // f.txt in the draft after the merge; nil: absent
		conflict          bool
		merged            bool // f.txt counts as a merged file
	}{
		{"unchanged on both sides is kept", s("a\n"), s("a\n"), s("a\n"), s("a\n"), false, false},
		{"changed in HEAD only takes HEAD", s("a\n"), s("a\n"), s("h\n"), s("h\n"), false, true},
		{"added in HEAD only is added", nil, nil, s("h\n"), s("h\n"), false, true},
		{"deleted in HEAD only is deleted", s("a\n"), s("a\n"), nil, nil, false, true},
		{"changed in the draft only is kept", s("a\n"), s("d\n"), s("a\n"), s("d\n"), false, false},
		{"added in the draft only is kept", nil, s("d\n"), nil, s("d\n"), false, false},
		{"deleted in the draft only stays deleted", s("a\n"), nil, s("a\n"), nil, false, false},
		{"the same bytes as HEAD are kept", s("a\n"), s("h\n"), s("h\n"), s("h\n"), false, false},
		{"deleted on both sides stays deleted", s("a\n"), nil, nil, nil, false, false},
		{"text changes in other lines merge clean and keep the last line end", s(startLines7Base), s(startLines7Draft), s(startLines7Head), s(startLines7Clean), false, true},
		{"text changes in the same line conflict", s("a\n"), s("d\n"), s("h\n"), s(startConflictText("d\n", "h\n")), true, false},
		{"added on both sides with other bytes conflicts", nil, s("d\n"), s("h\n"), s(startConflictText("d\n", "h\n")), true, false},
		{"binary on both sides conflicts and keeps the draft", s("a\x00\n"), s("d\x00\n"), s("h\x00\n"), s("d\x00\n"), true, false},
		{"deleted in the draft and changed in HEAD conflicts and stays deleted", s("a\n"), nil, s("h\n"), nil, true, false},
		{"changed in the draft and deleted in HEAD conflicts and keeps the draft", s("a\n"), s("d\n"), nil, s("d\n"), true, false},
	}
	for _, tc := range rows {
		t.Run(tc.name, func(t *testing.T) {
			dir := startRepo(t)
			if tc.base != nil {
				startCommitShipped(t, dir, "f.txt", *tc.base)
			}
			startRun(t, dir, ModeAuto)
			base := startHead(t, dir)
			draftPath := filepath.Join(dir, filepath.FromSlash(staticDir), "f.txt")
			if tc.draft == nil {
				if err := os.Remove(draftPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
					t.Fatal(err)
				}
			} else {
				startWrite(t, dir, staticDir+"/f.txt", *tc.draft)
			}
			switch {
			case tc.head == nil && tc.base != nil:
				startCommitDelete(t, dir, "f.txt")
			case tc.head != nil && (tc.base == nil || *tc.head != *tc.base):
				startCommitShipped(t, dir, "f.txt", *tc.head)
			}
			// A file added in HEAD makes the merge run for every row.
			startCommitShipped(t, dir, "added.txt", "added\n")
			draftBefore := startSnapshot(t, filepath.Join(dir, filepath.FromSlash(staticDir)))

			out := startRun(t, dir, ModeMerge)

			head := startHead(t, dir)
			backup := startOneBackup(t, dir)
			merged := 1
			if tc.merged {
				merged++
			}
			wantLines, wantBase := []string{startCleanLine(merged, head, backup)}, head
			if tc.conflict {
				wantLines = []string{"design preview: merge — 1 conflicts. Backup " + backup + ".", "design preview: conflict — " + staticDir + "/f.txt"}
				wantBase = base
			}
			if !reflect.DeepEqual(out.Lines, wantLines) {
				t.Errorf("lines\ngot:  %q\nwant: %q", out.Lines, wantLines)
			}
			if out.Case != CaseMerge || out.Serve == tc.conflict || out.Conflicts != tc.conflict {
				t.Errorf("case/serve/conflicts = %q/%v/%v, want merge/%v/%v", out.Case, out.Serve, out.Conflicts, !tc.conflict, tc.conflict)
			}

			got, err := os.ReadFile(draftPath)
			switch {
			case tc.want == nil && !errors.Is(err, fs.ErrNotExist):
				t.Errorf("f.txt = %q, %v, want no file", got, err)
			case tc.want != nil && (err != nil || string(got) != *tc.want):
				t.Errorf("f.txt = %q, %v, want %q", got, err, *tc.want)
			}
			if data, _ := os.ReadFile(filepath.Join(dir, filepath.FromSlash(staticDir), "added.txt")); string(data) != "added\n" {
				t.Errorf("added.txt = %q, want the HEAD text", data)
			}
			if rec := startReadBaseFile(t, dir); rec.BaseCommit != wantBase {
				t.Errorf("base commit = %s, want %s", rec.BaseCommit, wantBase)
			}
			startAssertBackup(t, dir, backup, draftBefore)
			startAssertNoWorkFolders(t, dir)
		})
	}

	t.Run("a merged file and a taken HEAD file keep the mode of the draft file", func(t *testing.T) {
		dir, _ := startMergeWork(t)
		for _, rel := range []string{"f.txt", "app.css"} {
			if err := os.Chmod(filepath.Join(dir, filepath.FromSlash(staticDir), rel), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		startRun(t, dir, ModeMerge)
		backup := startOneBackup(t, dir)
		for _, rel := range []string{staticDir + "/f.txt", staticDir + "/app.css", backup + "/f.txt", backup + "/app.css"} {
			info, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel)))
			if err != nil || info.Mode().Perm() != 0o600 {
				t.Errorf("%s: mode %v, %v, want 0600", rel, info.Mode().Perm(), err)
			}
		}
	})

	t.Run("a clean merge leaves the request log and the dependency records, and auto serves the draft next", func(t *testing.T) {
		dir, _ := startMergeWork(t)
		logText := startRequestLog + "| R1 | Show the total time of a run. | — | The head shows a time. | — | applied |\n"
		deps := `[{"id":"D1","kind":"flow","need":"Open a run.","elements":[]}]`
		startWrite(t, dir, requestLogFile, logText)
		startWrite(t, dir, depsFile, deps)
		startWrite(t, dir, requirementsFile, "# needs\n")

		out := startRun(t, dir, ModeMerge)
		startAssertOutcome(t, out, CaseMerge, true, startCleanLine(3, startHead(t, dir), startOneBackup(t, dir)))
		for rel, want := range map[string]string{requestLogFile: logText, depsFile: deps, requirementsFile: "# needs\n"} {
			if data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel))); err != nil || string(data) != want {
				t.Errorf("%s = %q, %v, want %q", rel, data, err, want)
			}
		}
		if startExists(dir, staticDir+"/js/app.js") {
			t.Error("js/app.js is in the draft after HEAD deleted it")
		}
		if out := startRun(t, dir, ModeAuto); out.Case != CaseB {
			t.Errorf("auto after the merge: case = %q, want b", out.Case)
		}
	})

	t.Run("stale work folders are removed and the merge is clean", func(t *testing.T) {
		dir, _ := startMergeWork(t)
		for _, rel := range []string{mergeTmpDir, tmpStaticDir, oldStaticDir} {
			startWrite(t, dir, rel+"/stale.txt", "<<<<<<< stale\n")
		}
		out := startRun(t, dir, ModeMerge)
		startAssertOutcome(t, out, CaseMerge, true, startCleanLine(3, startHead(t, dir), startOneBackup(t, dir)))
		startAssertNoWorkFolders(t, dir)
	})

	t.Run("after a merge with conflicts, auto, merge and continue refuse the draft until the fix", func(t *testing.T) {
		dir := startRepo(t)
		startRun(t, dir, ModeAuto)
		startWrite(t, dir, staticDir+"/app.css", "body { color: blue; }\n")
		startCommitShipped(t, dir, "app.css", "body { color: red; }\n")
		if out := startRun(t, dir, ModeMerge); !out.Conflicts {
			t.Fatalf("merge: conflicts = false, want true")
		}
		want := conflictMarkersError([]string{staticDir + "/app.css"}).Error()
		for _, mode := range []Mode{ModeAuto, ModeMerge, ModeContinue} {
			startAssertErr(t, dir, mode, want)
		}
		startWrite(t, dir, staticDir+"/app.css", "body { color: purple; }\n")
		out := startRun(t, dir, ModeContinue)
		startAssertOutcome(t, out, CaseContinue, true, "design preview: continue — kept the draft. Base commit "+startHead(t, dir)+".")
	})
}

// TestStartMergeDecide covers each arm of mergeDecide and the limits of
// the binary sniff and the file size.
func TestStartMergeDecide(t *testing.T) {
	v := func(data string) mergeVersion { return mergeVersion{data: []byte(data), present: true} }
	nulAt := func(i int) string {
		data := []byte(strings.Repeat("x", mergeBinarySniff+10))
		data[i] = 0
		return string(data)
	}
	sized := func(n int) string { return strings.Repeat("x", n) }
	var absent mergeVersion

	rows := []struct {
		name              string
		draft, base, head mergeVersion
		want              mergeAction
	}{
		{"draft same as base takes HEAD", v("a"), v("a"), v("h"), mergeTakeHead},
		{"draft and base both absent takes HEAD", absent, absent, v("h"), mergeTakeHead},
		{"draft same as HEAD keeps", v("h"), v("a"), v("h"), mergeKeep},
		{"draft and HEAD both absent keeps", absent, v("a"), absent, mergeKeep},
		{"draft absent and HEAD changed conflicts", absent, v("a"), v("h"), mergeConflict},
		{"draft changed and HEAD absent conflicts", v("d"), v("a"), absent, mergeConflict},
		{"text on all sides runs merge-file", v("d"), v("a"), v("h"), mergeRun},
		{"added on both sides runs merge-file", v("d"), absent, v("h"), mergeRun},
		{"NUL at mergeBinarySniff-1 in the draft conflicts", v(nulAt(mergeBinarySniff - 1)), v("a"), v("h"), mergeConflict},
		{"NUL at mergeBinarySniff-1 in the base conflicts", v("d"), v(nulAt(mergeBinarySniff - 1)), v("h"), mergeConflict},
		{"NUL at mergeBinarySniff-1 in HEAD conflicts", v("d"), v("a"), v(nulAt(mergeBinarySniff - 1)), mergeConflict},
		{"NUL only at mergeBinarySniff in the draft runs merge-file", v(nulAt(mergeBinarySniff)), v("a"), v("h"), mergeRun},
		{"NUL only at mergeBinarySniff in the base runs merge-file", v("d"), v(nulAt(mergeBinarySniff)), v("h"), mergeRun},
		{"NUL only at mergeBinarySniff in HEAD runs merge-file", v("d"), v("a"), v(nulAt(mergeBinarySniff)), mergeRun},
		{"a draft of maxMergeFileBytes runs merge-file", v(sized(maxMergeFileBytes)), v("a"), v("h"), mergeRun},
		{"a draft of maxMergeFileBytes+1 conflicts", v(sized(maxMergeFileBytes + 1)), v("a"), v("h"), mergeConflict},
		{"a base of maxMergeFileBytes runs merge-file", v("d"), v(sized(maxMergeFileBytes)), v("h"), mergeRun},
		{"a base of maxMergeFileBytes+1 conflicts", v("d"), v(sized(maxMergeFileBytes + 1)), v("h"), mergeConflict},
		{"a HEAD of maxMergeFileBytes runs merge-file", v("d"), v("a"), v(sized(maxMergeFileBytes)), mergeRun},
		{"a HEAD of maxMergeFileBytes+1 conflicts", v("d"), v("a"), v(sized(maxMergeFileBytes + 1)), mergeConflict},
		{"an over-size draft equal to the base is not the same and conflicts", v(sized(maxMergeFileBytes + 1)), v(sized(maxMergeFileBytes + 1)), v("h"), mergeConflict},
	}
	for _, tc := range rows {
		t.Run(tc.name, func(t *testing.T) {
			if got := mergeDecide(tc.draft, tc.base, tc.head); got != tc.want {
				t.Errorf("mergeDecide = %d, want %d", got, tc.want)
			}
		})
	}
}

// TestStartMergeMarkers covers each row of the marker table of
// draftConflictFiles, its errors, and refuseConflictMarkers.
func TestStartMergeMarkers(t *testing.T) {
	const marker = "<<<<<<< draft\n"
	sized := func(n int) string { return marker + strings.Repeat("x", n-len(marker)) }
	nulAt := func(i, n int) string {
		data := []byte(sized(n))
		data[i] = 0
		return string(data)
	}

	rows := []struct {
		name    string
		content string
		want    bool
	}{
		{"a line that starts with <<<<<<< ", "a\n<<<<<<< draft\nb\n", true},
		{"the marker on the first line", "<<<<<<< draft\nb\n", true},
		{"a line that starts with >>>>>>> ", "a\n>>>>>>> HEAD\nb\n", true},
		{"the marker on the last line with no line end", "a\n>>>>>>> HEAD", true},
		{"CRLF line ends", "a\r\n<<<<<<< draft\r\nb\r\n", true},
		{"an unclosed region", "a\n<<<<<<< draft\nb\n=======\n", true},
		{"<<<<<<< inside a line", "a <<<<<<< draft\n", false},
		{"<<<<<<< with no space after it", "<<<<<<<\n", false},
		{"only ======= lines", "=======\n=======\n", false},
		{"an empty file", "", false},
		{"a NUL at mergeBinarySniff-1 is binary and not scanned", nulAt(mergeBinarySniff-1, mergeBinarySniff+10), false},
		{"a NUL only at mergeBinarySniff is text", nulAt(mergeBinarySniff, mergeBinarySniff+10), true},
		{"a text file of exactly maxMergeFileBytes", sized(maxMergeFileBytes), true},
		{"a binary file over maxMergeFileBytes is skipped", nulAt(0, maxMergeFileBytes+10), false},
	}
	for _, tc := range rows {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			startWrite(t, dir, staticDir+"/f.txt", tc.content)
			got, err := draftConflictFiles(dir)
			want := []string{}
			if tc.want {
				want = []string{"f.txt"}
			}
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Errorf("draftConflictFiles = %q, %v, want %q", got, err, want)
			}
		})
	}

	t.Run("the result is sorted and holds only files with markers", func(t *testing.T) {
		dir := t.TempDir()
		for _, rel := range []string{"b.txt", "a/x.txt", "a.txt"} {
			startWrite(t, dir, staticDir+"/"+rel, marker)
		}
		startWrite(t, dir, staticDir+"/c.txt", "clean\n")
		got, err := draftConflictFiles(dir)
		if want := []string{"a.txt", "a/x.txt", "b.txt"}; err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("draftConflictFiles = %q, %v, want %q", got, err, want)
		}
	})

	t.Run("an empty draft folder gives an empty result", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, filepath.FromSlash(staticDir)), 0o755); err != nil {
			t.Fatal(err)
		}
		got, err := draftConflictFiles(dir)
		if err != nil || got == nil || len(got) != 0 {
			t.Errorf("draftConflictFiles = %#v, %v, want an empty result", got, err)
		}
	})

	t.Run("a marker only behind a symlink is not found", func(t *testing.T) {
		dir := t.TempDir()
		startWrite(t, dir, draftDir+"/outside.txt", marker)
		startWrite(t, dir, draftDir+"/outdir/in.txt", marker)
		startWrite(t, dir, staticDir+"/clean.txt", "clean\n")
		static := filepath.Join(dir, filepath.FromSlash(staticDir))
		for link, target := range map[string]string{"link.txt": "../outside.txt", "linkdir": "../outdir"} {
			if err := os.Symlink(target, filepath.Join(static, link)); err != nil {
				t.Fatal(err)
			}
		}
		got, err := draftConflictFiles(dir)
		if err != nil || len(got) != 0 {
			t.Errorf("draftConflictFiles = %q, %v, want an empty result", got, err)
		}
	})

	t.Run("a static folder that is a symlink is not followed", func(t *testing.T) {
		dir := t.TempDir()
		startWrite(t, dir, draftDir+"/real/f.txt", marker)
		if err := os.Symlink("real", filepath.Join(dir, filepath.FromSlash(staticDir))); err != nil {
			t.Fatal(err)
		}
		got, err := draftConflictFiles(dir)
		if err != nil || len(got) != 0 {
			t.Errorf("draftConflictFiles = %q, %v, want an empty result", got, err)
		}
	})

	errRows := []struct {
		name  string
		setup func(t *testing.T, dir string)
		want  string
	}{
		{"a text file over maxMergeFileBytes", func(t *testing.T, dir string) {
			startWrite(t, dir, staticDir+"/big.txt", sized(maxMergeFileBytes+1))
		}, staticDir + "/big.txt is larger than 8 MiB. The marker check cannot read it."},
		{"no static folder", func(*testing.T, string) {}, staticDir + ": not found"},
		{"a static path that is a file", func(t *testing.T, dir string) {
			startWrite(t, dir, staticDir, marker)
		}, staticDir + " is not a folder. The draft must be a folder."},
		{"a draft file that cannot be opened", func(t *testing.T, dir string) {
			startWrite(t, dir, staticDir+"/f.txt", "clean\n")
			startSwap(t, &mergeOpen, func(string) (*os.File, error) { return nil, errStartInjected })
		}, staticDir + "/f.txt: " + errStartInjected.Error()},
	}
	for _, tc := range errRows {
		t.Run("error: "+tc.name, func(t *testing.T) {
			dir := t.TempDir()
			tc.setup(t, dir)
			before := startSnapshotNoFollow(t, dir)
			got, err := draftConflictFiles(dir)
			if err == nil || err.Error() != tc.want {
				t.Errorf("draftConflictFiles = %q, %v, want error %q", got, err, tc.want)
			}
			if after := startSnapshotNoFollow(t, dir); !reflect.DeepEqual(before, after) {
				t.Errorf("files changed\nbefore: %v\nafter:  %v", before, after)
			}
		})
	}

	t.Run("refuseConflictMarkers names each file with markers", func(t *testing.T) {
		dir := t.TempDir()
		startWrite(t, dir, staticDir+"/b.txt", marker)
		startWrite(t, dir, staticDir+"/a.txt", marker)
		want := "the draft has conflict markers in design/dashboard/static/a.txt, design/dashboard/static/b.txt. Fix them, then run task design:continue."
		if err := refuseConflictMarkers(dir); err == nil || err.Error() != want {
			t.Errorf("refuseConflictMarkers = %v, want %q", err, want)
		}
	})

	t.Run("refuseConflictMarkers is nil for a clean draft and passes a check error on", func(t *testing.T) {
		dir := t.TempDir()
		if err := refuseConflictMarkers(dir); err == nil || err.Error() != staticDir+": not found" {
			t.Errorf("no draft: refuseConflictMarkers = %v, want the not-found error", err)
		}
		startWrite(t, dir, staticDir+"/a.txt", "clean\n")
		if err := refuseConflictMarkers(dir); err != nil {
			t.Errorf("clean draft: refuseConflictMarkers = %v, want nil", err)
		}
	})

	t.Run("listDraftFiles refuses a symlink in or as the draft when asked", func(t *testing.T) {
		dir := t.TempDir()
		startWrite(t, dir, staticDir+"/a.txt", "a\n")
		static := filepath.Join(dir, filepath.FromSlash(staticDir))
		if err := os.Symlink("a.txt", filepath.Join(static, "link.txt")); err != nil {
			t.Fatal(err)
		}
		_, err := listDraftFiles(dir, true)
		if !errors.Is(err, errDraftSymlink) || err.Error() != staticDir+"/link.txt: "+errDraftSymlink.Error() {
			t.Errorf("symlink in the draft: error = %v, want errDraftSymlink for link.txt", err)
		}

		other := t.TempDir()
		startWrite(t, other, draftDir+"/real/a.txt", "a\n")
		if err := os.Symlink("real", filepath.Join(other, filepath.FromSlash(staticDir))); err != nil {
			t.Fatal(err)
		}
		_, err = listDraftFiles(other, true)
		if !errors.Is(err, errDraftSymlink) || err.Error() != staticDir+": "+errDraftSymlink.Error() {
			t.Errorf("symlink as the draft: error = %v, want errDraftSymlink for static", err)
		}
	})
}

// startSnapshotNoFollow maps each path under dir, except .git, to its bytes,
// a folder to "<dir>" and any other entry, such as a symlink or a named
// pipe, to its type.
func startSnapshotNoFollow(t *testing.T, dir string) map[string]string {
	t.Helper()
	snap := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		switch {
		case rel == ".git":
			return filepath.SkipDir
		case d.IsDir():
			snap[rel] = "<dir>"
		case d.Type().IsRegular():
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			snap[rel] = string(data)
		default:
			snap[rel] = d.Type().String()
		}
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	return snap
}

// TestStartMergeModes runs each input state of the mode table through each
// mode. A state that gives an error leaves every file as it was.
func TestStartMergeModes(t *testing.T) {
	// modeWant is one cell of the table: a case, an error by errors.Is, or
	// the start of an error text.
	type modeWant struct {
		c    CaseID
		is   error
		text string
	}
	caseOf := func(c CaseID) modeWant { return modeWant{c: c} }
	is := func(err error) modeWant { return modeWant{is: err} }
	text := func(s string) modeWant { return modeWant{text: s} }
	markers := text(conflictMarkersError([]string{staticDir + "/app.css"}).Error())

	draft := func(t *testing.T, dir string) { startRun(t, dir, ModeAuto) }
	states := []struct {
		name  string
		setup func(t *testing.T, dir string)
		want  map[Mode]modeWant
	}{
		{"no static folder", func(*testing.T, string) {}, map[Mode]modeWant{
			ModeAuto: caseOf(CaseA), ModeFresh: caseOf(CaseFresh), ModeContinue: is(errNoDraft), ModeMerge: is(errNoDraft)}},
		{"static with conflict markers", func(t *testing.T, dir string) {
			draft(t, dir)
			startWrite(t, dir, staticDir+"/app.css", "<<<<<<< draft\n")
		}, map[Mode]modeWant{
			ModeAuto: markers, ModeFresh: caseOf(CaseFresh), ModeContinue: markers, ModeMerge: markers}},
		{"static is a symlink to a folder", func(t *testing.T, dir string) {
			draft(t, dir)
			static := filepath.Join(dir, filepath.FromSlash(staticDir))
			if err := os.Rename(static, filepath.Join(dir, filepath.FromSlash(draftDir), "real")); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("real", static); err != nil {
				t.Fatal(err)
			}
		}, map[Mode]modeWant{
			ModeAuto: caseOf(CaseB), ModeFresh: caseOf(CaseFresh), ModeContinue: caseOf(CaseContinue), ModeMerge: is(errDraftSymlink)}},
		{"static holds a symlink", func(t *testing.T, dir string) {
			draft(t, dir)
			if err := os.Symlink("app.css", filepath.Join(dir, filepath.FromSlash(staticDir), "link.css")); err != nil {
				t.Fatal(err)
			}
		}, map[Mode]modeWant{
			ModeAuto: caseOf(CaseB), ModeFresh: caseOf(CaseFresh), ModeContinue: caseOf(CaseContinue), ModeMerge: is(errDraftSymlink)}},
		{"static is a file", func(t *testing.T, dir string) {
			startWrite(t, dir, staticDir, "not a folder")
		}, map[Mode]modeWant{
			ModeAuto: caseOf(CaseA), ModeFresh: caseOf(CaseFresh), ModeContinue: is(errNoDraft), ModeMerge: is(errNoDraft)}},
		{"base.json missing", func(t *testing.T, dir string) {
			draft(t, dir)
			if err := os.Remove(filepath.Join(dir, filepath.FromSlash(baseFile))); err != nil {
				t.Fatal(err)
			}
		}, map[Mode]modeWant{
			ModeAuto: is(errMissingBase), ModeFresh: caseOf(CaseFresh), ModeContinue: caseOf(CaseContinue), ModeMerge: is(errMissingBase)}},
		{"base.json bad", func(t *testing.T, dir string) {
			draft(t, dir)
			startWrite(t, dir, baseFile, "{")
		}, map[Mode]modeWant{
			ModeAuto: is(errBadBase), ModeFresh: caseOf(CaseFresh), ModeContinue: caseOf(CaseContinue), ModeMerge: is(errBadBase)}},
		{"base.json over maxBaseBytes", func(t *testing.T, dir string) {
			draft(t, dir)
			startWrite(t, dir, baseFile, strings.Repeat(" ", maxBaseBytes+1))
		}, map[Mode]modeWant{
			ModeAuto: is(errBadBase), ModeFresh: caseOf(CaseFresh), ModeContinue: caseOf(CaseContinue), ModeMerge: is(errBadBase)}},
		{"base.json is a folder", func(t *testing.T, dir string) {
			draft(t, dir)
			path := filepath.Join(dir, filepath.FromSlash(baseFile))
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(path, 0o755); err != nil {
				t.Fatal(err)
			}
		}, map[Mode]modeWant{
			ModeAuto: text(baseFile + ": is a directory"), ModeFresh: caseOf(CaseFresh), ModeContinue: text(baseFile + ": "), ModeMerge: text(baseFile + ": is a directory")}},
		{"dirty shipped page", func(t *testing.T, dir string) {
			draft(t, dir)
			startWrite(t, dir, shippedPath+"/app.css", "body { color: dirty; }\n")
		}, map[Mode]modeWant{
			ModeAuto: is(errDirty), ModeFresh: is(errDirty), ModeContinue: is(errDirty), ModeMerge: is(errDirty)}},
	}
	for _, st := range states {
		for _, mode := range []Mode{ModeAuto, ModeFresh, ModeContinue, ModeMerge} {
			t.Run(st.name+"/"+string(mode), func(t *testing.T) {
				dir := startRepo(t)
				st.setup(t, dir)
				want := st.want[mode]
				before := startSnapshotNoFollow(t, dir)
				out, err := StartRule(dir, mode)
				switch {
				case want.c != "":
					if err != nil || out.Case != want.c {
						t.Fatalf("StartRule = %q, %v, want case %q", out.Case, err, want.c)
					}
					return
				case want.is != nil:
					if !errors.Is(err, want.is) {
						t.Errorf("error = %v, want %v", err, want.is)
					}
				default:
					if err == nil || !strings.HasPrefix(err.Error(), want.text) {
						t.Errorf("error = %v, want prefix %q", err, want.text)
					}
				}
				if after := startSnapshotNoFollow(t, dir); !reflect.DeepEqual(before, after) {
					t.Errorf("files changed\nbefore: %v\nafter:  %v", before, after)
				}
			})
		}
	}
}

// TestStartMergeExit covers the exit rows of merge mode that the per-file
// and mode tests do not reach: nothing to merge, and the git diff failure.
func TestStartMergeExit(t *testing.T) {
	t.Run("HEAD equals the base: nothing to merge", func(t *testing.T) {
		dir := startRepo(t)
		startRun(t, dir, ModeAuto)
		before := startSnapshot(t, dir)
		out := startRun(t, dir, ModeMerge)
		startAssertOutcome(t, out, CaseMerge, true, "design preview: merge — nothing to merge")
		startAssertUnchanged(t, dir, before)
	})

	t.Run("HEAD differs from the base with an unchanged shipped page: nothing to merge", func(t *testing.T) {
		dir := startRepo(t)
		startRun(t, dir, ModeAuto)
		startWrite(t, dir, "README.md", "readme\n")
		startGitOut(t, dir, "add", "--", "README.md")
		startGitOut(t, dir, "commit", "-q", "-m", "readme")
		before := startSnapshot(t, dir)
		out := startRun(t, dir, ModeMerge)
		startAssertOutcome(t, out, CaseMerge, true, "design preview: merge — nothing to merge")
		startAssertUnchanged(t, dir, before)
	})

	t.Run("a git diff failure is an error and changes nothing", func(t *testing.T) {
		dir, _ := startMergeWork(t)
		startFailGitAllowExit(t, "diff", 2)
		startAssertErr(t, dir, ModeMerge, shippedPath+": git diff exit 2: boom")
	})

	t.Run("a symlink in the draft is refused before the marker check", func(t *testing.T) {
		dir, _ := startMergeWork(t)
		startWrite(t, dir, staticDir+"/app.css", "<<<<<<< draft\n")
		if err := os.Symlink("app.css", filepath.Join(dir, filepath.FromSlash(staticDir), "link.css")); err != nil {
			t.Fatal(err)
		}
		_, err := StartRule(dir, ModeMerge)
		if !errors.Is(err, errDraftSymlink) {
			t.Errorf("error = %v, want errDraftSymlink", err)
		}
	})

	t.Run("the case d next line names task design:merge", func(t *testing.T) {
		dir := startRepo(t)
		startDraftOwnWork(t, dir)
		out := startRun(t, dir, ModeAuto)
		const want = "design preview: next — run task design:merge (merge the shipped changes into the draft), task design:fresh (discard the draft) or task design:continue (keep the draft)."
		if out.Case != CaseD || out.Lines[len(out.Lines)-1] != want {
			t.Errorf("case d lines = %q, want the last line %q", out.Lines, want)
		}
	})
}

// TestStartMergeBackupDir covers the name of the backup folder and its error.
func TestStartMergeBackupDir(t *testing.T) {
	t.Run("two merges in the same second get two folders", func(t *testing.T) {
		dir := t.TempDir()
		startWrite(t, dir, staticDir+"/a.txt", "a\n")
		now := time.Date(2026, 10, 30, 15, 9, 12, 0, time.FixedZone("CET", 3600))
		first, err1 := mergeBackupDir(dir, now)
		second, err2 := mergeBackupDir(dir, now)
		if err1 != nil || err2 != nil {
			t.Fatalf("mergeBackupDir: %v, %v", err1, err2)
		}
		const prefix = draftDir + "/static.bak-20261030T140912Z-"
		if first == second || !strings.HasPrefix(first, prefix) || !strings.HasPrefix(second, prefix) {
			t.Errorf("folders = %q, %q, want two names with the prefix %q", first, second, prefix)
		}
		for _, rel := range []string{first, second} {
			if info, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel))); err != nil || !info.IsDir() {
				t.Errorf("%s is not a folder: %v", rel, err)
			}
		}
	})

	t.Run("a missing draft folder is an error that names it", func(t *testing.T) {
		_, err := mergeBackupDir(t.TempDir(), time.Now())
		if err == nil || err.Error() != draftDir+": not found" {
			t.Errorf("error = %v, want %q", err, draftDir+": not found")
		}
	})
}

// startNth returns a matcher that is true only on the nth call (1-based)
// for which match is true.
func startNth(n int, match func(path string) bool) func(path string) bool {
	count := 0
	return func(path string) bool {
		if !match(path) {
			return false
		}
		count++
		return count == n
	}
}

// startIsPath matches the path of rel under dir, as the merge builds it.
func startIsPath(dir, rel string) func(path string) bool {
	want := mergePath(dir, rel)
	return func(path string) bool { return path == want }
}

// startInBackup matches the file name under a backup folder.
func startInBackup(name string) func(path string) bool {
	return func(path string) bool {
		return strings.Contains(path, "static.bak-") && filepath.Base(path) == name
	}
}

// startIsBackupRoot matches a backup folder itself.
func startIsBackupRoot(path string) bool {
	return strings.HasPrefix(filepath.Base(path), "static.bak-")
}

// startFailWrite makes copyWriteFile fail when fail(path) is true.
func startFailWrite(t *testing.T, fail func(string) bool) {
	t.Helper()
	real := copyWriteFile
	startSwap(t, &copyWriteFile, func(path string, data []byte, perm os.FileMode) error {
		if fail(path) {
			return errStartInjected
		}
		return real(path, data, perm)
	})
}

// startFailMkdir makes copyMkdirAll fail when fail(path) is true.
func startFailMkdir(t *testing.T, fail func(string) bool) {
	t.Helper()
	real := copyMkdirAll
	startSwap(t, &copyMkdirAll, func(path string, perm os.FileMode) error {
		if fail(path) {
			return errStartInjected
		}
		return real(path, perm)
	})
}

// startFailRemove makes mergeRemoveAll fail when fail(path) is true.
func startFailRemove(t *testing.T, fail func(string) bool) {
	t.Helper()
	real := mergeRemoveAll
	startSwap(t, &mergeRemoveAll, func(path string) error {
		if fail(path) {
			return errStartInjected
		}
		return real(path)
	})
}

// startFailRename makes the copyRename calls with the given numbers
// (1-based) fail.
func startFailRename(t *testing.T, calls ...int) {
	t.Helper()
	real := copyRename
	count := 0
	startSwap(t, &copyRename, func(from, to string) error {
		count++
		for _, n := range calls {
			if n == count {
				return errStartInjected
			}
		}
		return real(from, to)
	})
}

// TestStartMergeWriteFailures makes each write step of a merge fail through
// a seam and checks the state of its write-order row.
func TestStartMergeWriteFailures(t *testing.T) {
	injected := errStartInjected.Error()
	// The state after a failure, by its write-order row.
	const (
		writeNothing       = iota // no backup, and no file changed
		writeNoBackup             // no backup; draft and base unchanged
		writePartBackup           // a backup folder exists; draft and base unchanged
		writeFullBackup           // the backup holds the draft; draft and base unchanged
		writeRestoreFailed        // the backup holds the draft; a restore rename failed
	)
	rows := []struct {
		name    string
		inject  func(t *testing.T, dir string)
		wantErr func(backup string) string // the error text; backup is "" without a backup
		state   int                        // the state after the failure
	}{
		{"step 2: a stale .merge.tmp cannot be removed", func(t *testing.T, dir string) {
			startFailRemove(t, startIsPath(dir, mergeTmpDir))
		}, func(string) string { return mergeTmpDir + ": " + injected }, writeNothing},
		{"step 2: a stale .static.old cannot be removed", func(t *testing.T, dir string) {
			startFailRemove(t, startIsPath(dir, oldStaticDir))
		}, func(string) string { return oldStaticDir + ": " + injected }, writeNothing},
		{"step 3: the backup folder cannot be created", func(t *testing.T, _ string) {
			startSwap(t, &mergeMkdirTemp, func(string, string) (string, error) { return "", errStartInjected })
		}, func(string) string { return draftDir + ": " + injected }, writeNoBackup},
		{"step 3: the backup folder cannot be made ready", func(t *testing.T, _ string) {
			startFailMkdir(t, startIsBackupRoot)
		}, func(b string) string { return b + ": " + injected + ". Backup " + b + "." }, writePartBackup},
		{"step 3: a draft file cannot be read", func(t *testing.T, dir string) {
			real := copyReadFile
			want := mergePath(dir, staticDir+"/app.css")
			startSwap(t, &copyReadFile, func(path string) ([]byte, error) {
				if path == want {
					return nil, errStartInjected
				}
				return real(path)
			})
		}, func(b string) string { return staticDir + "/app.css: " + injected + ". Backup " + b + "." }, writePartBackup},
		{"step 3: a backup file cannot be written", func(t *testing.T, _ string) {
			startFailWrite(t, startInBackup("app.css"))
		}, func(b string) string { return b + "/app.css: " + injected + ". Backup " + b + "." }, writePartBackup},
		{"step 4: the base blob list fails", func(t *testing.T, _ string) {
			startFailGitRun(t, startLsTree(false))
		}, func(b string) string { return shippedPath + ": " + injected + ". Backup " + b + "." }, writeFullBackup},
		{"step 4: the HEAD blob list fails", func(t *testing.T, dir string) {
			head := startHead(t, dir)
			startFailGitRun(t, func(args []string) bool { return args[0] == "ls-tree" && args[3] == head })
		}, func(b string) string { return shippedPath + ": " + injected + ". Backup " + b + "." }, writeFullBackup},
		{"step 4: .static.tmp cannot be created", func(t *testing.T, dir string) {
			startFailMkdir(t, startIsPath(dir, tmpStaticDir))
		}, func(b string) string { return tmpStaticDir + ": " + injected + ". Backup " + b + "." }, writeFullBackup},
		{"step 4: a draft file cannot be read for the merge", func(t *testing.T, dir string) {
			real := mergeOpen
			// The first open is the marker check; the second is the merge.
			fail := startNth(2, startIsPath(dir, staticDir+"/app.css"))
			startSwap(t, &mergeOpen, func(path string) (*os.File, error) {
				if fail(path) {
					return nil, errStartInjected
				}
				return real(path)
			})
		}, func(b string) string { return staticDir + "/app.css: " + injected + ". Backup " + b + "." }, writeFullBackup},
		{"step 4: a base blob cannot be read", func(t *testing.T, dir string) {
			real := gitBlob
			head := startHead(t, dir)
			startSwap(t, &gitBlob, func(root, rev, path string) ([]byte, error) {
				if rev != head {
					return nil, errStartInjected
				}
				return real(root, rev, path)
			})
		}, func(b string) string { return shippedPath + "/app.css: " + injected + ". Backup " + b + "." }, writeFullBackup},
		{"step 4: a HEAD blob cannot be read", func(t *testing.T, dir string) {
			real := gitBlob
			head := startHead(t, dir)
			startSwap(t, &gitBlob, func(root, rev, path string) ([]byte, error) {
				if rev == head {
					return nil, errStartInjected
				}
				return real(root, rev, path)
			})
		}, func(b string) string { return shippedPath + "/app.css: " + injected + ". Backup " + b + "." }, writeFullBackup},
		{"step 4: a HEAD file cannot be written to the copy", func(t *testing.T, dir string) {
			// The first write is the copy of the draft; the second takes HEAD.
			startFailWrite(t, startNth(2, startIsPath(dir, tmpStaticDir+"/app.css")))
		}, func(b string) string { return tmpStaticDir + "/app.css: " + injected + ". Backup " + b + "." }, writeFullBackup},
		{"step 4: a file deleted in HEAD cannot be removed from the copy", func(t *testing.T, dir string) {
			startFailRemove(t, startIsPath(dir, tmpStaticDir+"/js/app.js"))
		}, func(b string) string { return tmpStaticDir + "/js/app.js: " + injected + ". Backup " + b + "." }, writeFullBackup},
		{"step 4: the base copy folder cannot be created", func(t *testing.T, dir string) {
			startFailMkdir(t, startIsPath(dir, mergeTmpDir+"/base"))
		}, func(b string) string { return mergeTmpDir + "/base/f.txt: " + injected + ". Backup " + b + "." }, writeFullBackup},
		{"step 4: the base copy cannot be written", func(t *testing.T, dir string) {
			startFailWrite(t, startIsPath(dir, mergeTmpDir+"/base/f.txt"))
		}, func(b string) string { return mergeTmpDir + "/base/f.txt: " + injected + ". Backup " + b + "." }, writeFullBackup},
		{"step 4: the HEAD copy cannot be written", func(t *testing.T, dir string) {
			startFailWrite(t, startIsPath(dir, mergeTmpDir+"/head/f.txt"))
		}, func(b string) string { return mergeTmpDir + "/head/f.txt: " + injected + ". Backup " + b + "." }, writeFullBackup},
		{"step 4: git merge-file exits outside 0 to 127", func(t *testing.T, _ string) {
			startFailGitAllowExit(t, "merge-file", 255)
		}, func(b string) string { return staticDir + "/f.txt: git merge-file exit 255: boom. Backup " + b + "." }, writeFullBackup},
		{"step 4: git merge-file cannot start", func(t *testing.T, _ string) {
			startFailGitAllowExit(t, "merge-file", -1)
		}, func(b string) string { return staticDir + "/f.txt: " + injected + ". Backup " + b + "." }, writeFullBackup},
		{"step 5: static cannot be moved aside", func(t *testing.T, _ string) {
			startFailRename(t, 1)
		}, func(b string) string { return staticDir + ": " + injected + ". Backup " + b + "." }, writeFullBackup},
		{"step 5: the copy cannot be moved in, and the old draft comes back", func(t *testing.T, _ string) {
			startFailRename(t, 2)
		}, func(b string) string { return staticDir + ": " + injected + ". Backup " + b + "." }, writeFullBackup},
		{"step 6: base.json cannot be written, and the old draft comes back", func(t *testing.T, _ string) {
			startSwap(t, &writeBaseJSON, func(string, any) error { return errStartInjected })
		}, func(b string) string { return baseFile + ": " + injected + ". Backup " + b + "." }, writeFullBackup},
		{"restore: the old draft cannot come back after step 5", func(t *testing.T, _ string) {
			startFailRename(t, 2, 3)
		}, func(b string) string {
			return staticDir + ": " + injected + ". The restore failed too: " + injected + ". Restore the draft: copy " + b + " to design/dashboard/static."
		}, writeRestoreFailed},
		{"restore: the merged draft cannot move away after step 6", func(t *testing.T, _ string) {
			startSwap(t, &writeBaseJSON, func(string, any) error { return errStartInjected })
			startFailRename(t, 3)
		}, func(b string) string {
			return baseFile + ": " + injected + ". The restore failed too: " + injected + ". Restore the draft: copy " + b + " to design/dashboard/static."
		}, writeRestoreFailed},
		{"restore: the old draft cannot come back after step 6", func(t *testing.T, _ string) {
			startSwap(t, &writeBaseJSON, func(string, any) error { return errStartInjected })
			startFailRename(t, 4)
		}, func(b string) string {
			return baseFile + ": " + injected + ". The restore failed too: " + injected + ". Restore the draft: copy " + b + " to design/dashboard/static."
		}, writeRestoreFailed},
	}
	for _, tc := range rows {
		t.Run(tc.name, func(t *testing.T) {
			dir, _ := startMergeWork(t)
			startWrite(t, dir, oldStaticDir+"/stale.txt", "stale\n")
			draftBefore := startSnapshot(t, filepath.Join(dir, filepath.FromSlash(staticDir)))
			stateBefore := startDraftState(t, dir)
			treeBefore := startSnapshot(t, dir)
			tc.inject(t, dir)

			_, err := StartRule(dir, ModeMerge)

			backup := ""
			if tc.state == writeNothing || tc.state == writeNoBackup {
				if backups := startBackups(t, dir); len(backups) != 0 {
					t.Errorf("backup folders = %v, want none", backups)
				}
			} else {
				backup = startOneBackup(t, dir)
			}
			if want := tc.wantErr(backup); err == nil || err.Error() != want {
				t.Errorf("error\ngot:  %v\nwant: %s", err, want)
			}
			if tc.state == writeFullBackup || tc.state == writeRestoreFailed {
				startAssertBackup(t, dir, backup, draftBefore)
			}
			switch tc.state {
			case writeNothing:
				startAssertUnchanged(t, dir, treeBefore)
			case writeNoBackup, writePartBackup, writeFullBackup:
				if after := startDraftState(t, dir); !reflect.DeepEqual(after, stateBefore) {
					t.Errorf("draft or base changed\nbefore: %v\nafter:  %v", stateBefore, after)
				}
			}
		})
	}

	t.Run("step 7: a work folder that cannot be removed gives one warning line", func(t *testing.T) {
		dir, _ := startMergeWork(t)
		// Step 2 removes each work folder first; the second remove is step 7.
		fail := map[string]func(string) bool{
			oldStaticDir: startNth(2, startIsPath(dir, oldStaticDir)),
			mergeTmpDir:  startNth(2, startIsPath(dir, mergeTmpDir)),
		}
		startFailRemove(t, func(path string) bool { return fail[oldStaticDir](path) || fail[mergeTmpDir](path) })

		out := startRun(t, dir, ModeMerge)

		head := startHead(t, dir)
		startAssertOutcome(t, out, CaseMerge, true,
			startCleanLine(3, head, startOneBackup(t, dir)),
			"design preview: warning — could not remove "+oldStaticDir+": "+injected+"; "+mergeTmpDir+": "+injected+". The next merge removes the rest.")
		if rec := startReadBaseFile(t, dir); rec.BaseCommit != head {
			t.Errorf("base commit = %s, want HEAD %s", rec.BaseCommit, head)
		}
		if data, _ := os.ReadFile(filepath.Join(dir, filepath.FromSlash(staticDir), "f.txt")); string(data) != startLines7Clean {
			t.Errorf("f.txt = %q, want the merged text", data)
		}
	})

	t.Run("a merge after a step 7 failure removes the rest", func(t *testing.T) {
		dir, _ := startMergeWork(t)
		startWrite(t, dir, oldStaticDir+"/left.txt", "left\n")
		startWrite(t, dir, mergeTmpDir+"/left.txt", "left\n")
		startRun(t, dir, ModeMerge)
		startAssertNoWorkFolders(t, dir)
	})
}

// TestStartMergeBytes checks that merge keeps the raw bytes of a blob,
// with its last line end, where execx would trim them.
func TestStartMergeBytes(t *testing.T) {
	dir, _ := startMergeWork(t)
	data, err := gitBlob(dir, "HEAD", shippedPath+"/f.txt")
	if err != nil || !bytes.Equal(data, []byte(startLines7Head)) {
		t.Errorf("gitBlob = %q, %v, want %q", data, err, startLines7Head)
	}
}
