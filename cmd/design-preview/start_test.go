package main

import (
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// startShippedFiles is the shipped page of every test repo, by path relative to shippedPath.
var startShippedFiles = map[string]string{
	"index.html": "<!doctype html>\n<link rel=\"stylesheet\" href=\"static/app.css\">\n",
	"app.css":    "body { color: black; }\n",
	"js/app.js":  "console.log('v1');\n",
}

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
func startAssertOutcome(t *testing.T, got Outcome, wantCase string, wantServe bool, wantLines ...string) {
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
		for _, mode := range []Mode{ModeAuto, ModeFresh, ModeContinue} {
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
	}{
		{"short sha", func(*testing.T, string) string { return `{"baseCommit":"abc123","shippedPath":"x"}` }},
		{"upper-case sha", func(t *testing.T, dir string) string {
			return `{"baseCommit":"` + strings.ToUpper(startHead(t, dir)) + `"}`
		}},
		{"unknown sha", func(*testing.T, string) string {
			return `{"baseCommit":"` + strings.Repeat("deadbeef", 5) + `"}`
		}},
		{"sha of a blob", func(t *testing.T, dir string) string {
			return `{"baseCommit":"` + startGitOut(t, dir, "rev-parse", "HEAD:"+shippedPath+"/app.css") + `"}`
		}},
		{"bad JSON", func(*testing.T, string) string { return `{"baseCommit":` }},
		{"over 4 KiB", func(t *testing.T, dir string) string {
			return `{"baseCommit":"` + startHead(t, dir) + `","pad":"` + strings.Repeat("x", 4096) + `"}`
		}},
	}
	for _, tc := range badBase {
		t.Run("bad base.json: "+tc.name, func(t *testing.T) {
			dir := startRepo(t)
			startRun(t, dir, ModeAuto)
			startWrite(t, dir, draftDir+"/base.json", tc.content(t, dir))
			startAssertErr(t, dir, ModeAuto, "design/dashboard/base.json: baseCommit is not a known commit. Run task design:fresh or task design:continue.")
		})
	}

	t.Run("continue without a draft is an error", func(t *testing.T) {
		dir := startRepo(t)
		startAssertErr(t, dir, ModeContinue, "no draft in design/dashboard/static/. Run task design first.")
	})

	t.Run("a read failure names the path", func(t *testing.T) {
		dir := startRepo(t)
		startWrite(t, dir, "design/dashboard", "not a folder")
		for _, mode := range []Mode{ModeAuto, ModeFresh, ModeContinue} {
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
		startAssertErr(t, dir, Mode("other"), `unknown mode "other"`)
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
}
