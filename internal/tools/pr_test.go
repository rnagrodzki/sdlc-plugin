package tools

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/rnagrodzki/sdlc-plugin/internal/config"
	"github.com/rnagrodzki/sdlc-plugin/internal/execx"
	"github.com/rnagrodzki/sdlc-plugin/internal/ghx"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
	"github.com/rnagrodzki/sdlc-plugin/internal/version"
)

// stubGHDispatch installs a fake "gh" script on PATH that dispatches on its
// first argument (and, for "api", its second) to canned output, so a single
// test can drive multiple distinct gh invocations (auth probe, account
// listing, PR lookup, create/edit) the way pr_prepare/pr_apply actually
// call them. It mirrors internal/ghx's stubGH pattern (PATH-prepend +
// restore) but is table-driven instead of single-script, since pr.go's
// handlers issue more than one gh subcommand per call.
//
// rules is evaluated in order; the first rule whose args-prefix matches the
// invocation wins. A rule's Exit non-zero makes the stub `exit <n>` after
// printing Stdout (if any), matching a failing gh command.
type ghRule struct {
	prefix []string
	stdout string
	exit   int
}

// stubGHDispatch installs a fake "gh" script and returns the path to a
// NUL-separated args log file that captures every invocation's arguments.
func stubGHDispatch(t *testing.T, rules []ghRule) string {
	t.Helper()

	dir := t.TempDir()
	name := "gh"
	if runtime.GOOS == "windows" {
		name = "gh.bat"
	}

	logPath := filepath.Join(dir, "gh-calls.log")

	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	// Capture all args NUL-separated for assertion in tests.
	// Use ASCII record separator (0x1E) between invocations since
	// body args can contain newlines.
	b.WriteString("printf '%s\\0' \"$@\" >> " + quoteShellArg(logPath) + "\n")
	b.WriteString("printf '\\036' >> " + quoteShellArg(logPath) + "\n")
	for _, r := range rules {
		cond := make([]string, len(r.prefix))
		for i, p := range r.prefix {
			cond[i] = quoteShellArg(p)
		}
		b.WriteString("if [ \"$#\" -ge " + itoa(len(r.prefix)) + " ]")
		for i, c := range cond {
			b.WriteString(" && [ \"$" + itoa(i+1) + "\" = " + c + " ]")
		}
		b.WriteString("; then\n")
		if r.stdout != "" {
			b.WriteString("  printf '%s\\n' " + quoteShellArg(r.stdout) + "\n")
		}
		b.WriteString("  exit " + itoa(r.exit) + "\n")
		b.WriteString("fi\n")
	}
	b.WriteString("exit 1\n")

	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(b.String()), 0o755); err != nil {
		t.Fatalf("writing stub gh: %v", err)
	}

	origPath := os.Getenv("PATH")
	os.Setenv("PATH", dir+string(os.PathListSeparator)+origPath)
	t.Cleanup(func() { os.Setenv("PATH", origPath) })

	return logPath
}

func quoteShellArg(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := ""
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		digits = string(rune('0'+n%10)) + digits
		n /= 10
	}
	if neg {
		digits = "-" + digits
	}
	return digits
}

// initGitRepoWithBranch inits a git repo at dir with one commit, checked
// out on branch name (default branch is "main" from initGitFixture, then
// checked out to name when different).
func initGitRepoWithBranch(t *testing.T, dir, name string) {
	t.Helper()
	initGitFixture(t, dir)
	gitCommit(t, dir, "init")
	if name != "" && name != "main" {
		cmd := exec.Command("git", "checkout", "-b", name)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git checkout -b %s: %s: %v", name, out, err)
		}
	}
}

// ---------------------------------------------------------------------------
// pr_validate_body
// ---------------------------------------------------------------------------

func TestPrValidateBody_NoTemplate_AlwaysOK(t *testing.T) {
	root := t.TempDir()
	out, err := prValidateBodyCore(root, PRValidateBodyIn{Body: "anything at all"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !out.OK || len(out.Errors) != 0 {
		t.Fatalf("expected OK with no errors when no template exists, got %+v", out)
	}
}

func TestPrValidateBody_TemplateFixtureMatrix(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantOK     bool
		wantErrLen int
	}{
		{
			name:   "all sections present",
			body:   "## Summary\nDid the thing.\n\n## Testing\nRan tests.\n",
			wantOK: true,
		},
		{
			name:       "missing one section",
			body:       "## Summary\nDid the thing.\n",
			wantOK:     false,
			wantErrLen: 1,
		},
		{
			name:       "missing all sections",
			body:       "Just some prose with no headings.",
			wantOK:     false,
			wantErrLen: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			sdlcDir := filepath.Join(root, paths.DataDir)
			if err := os.MkdirAll(sdlcDir, 0o755); err != nil {
				t.Fatal(err)
			}
			tmpl := "## Summary\n<!-- what changed -->\n\n## Testing\n<!-- how verified -->\n"
			if err := os.WriteFile(filepath.Join(sdlcDir, "pr-template.md"), []byte(tmpl), 0o644); err != nil {
				t.Fatal(err)
			}

			out, err := prValidateBodyCore(root, PRValidateBodyIn{Body: tt.body})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if out.OK != tt.wantOK {
				t.Errorf("OK: got %v, want %v (errors=%v)", out.OK, tt.wantOK, out.Errors)
			}
			if len(out.Errors) != tt.wantErrLen {
				t.Errorf("len(Errors): got %d (%v), want %d", len(out.Errors), out.Errors, tt.wantErrLen)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// pr_prepare
// ---------------------------------------------------------------------------

func TestPrPrepare_ConfigNeedsMigration_ShortCircuits(t *testing.T) {
	root := t.TempDir()
	sdlcDir := filepath.Join(root, paths.DataDir)
	if err := os.MkdirAll(sdlcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// schemaVersion below current triggers ErrVersionStale (top-level
	// field — see configmigrate.extractSchemaVersion).
	stale := `{"schemaVersion":1}`
	if err := os.WriteFile(filepath.Join(sdlcDir, "config.json"), []byte(stale), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := prPrepareCore(root, root, PRPrepareIn{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !out.NeedsMigration {
		t.Fatalf("expected NeedsMigration=true, got %+v", out)
	}
	if out.OK {
		t.Fatalf("expected OK=false on migration short-circuit, got %+v", out)
	}
	if len(out.Errors) == 0 {
		t.Fatalf("expected a config-version error message")
	}
}

func TestPrPrepare_BrokenAuth_EmbedsLoginDiagnostics(t *testing.T) {
	// gh api user (the AuthProbe command) fails — simulates "not logged in".
	stubGHDispatch(t, []ghRule{
		{prefix: []string{"api", "user"}, exit: 1},
	})

	root := t.TempDir()
	workDir := t.TempDir()

	out, err := prPrepareCore(root, workDir, PRPrepareIn{SkipConfigCheck: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.GHAuthenticated {
		t.Fatalf("expected GHAuthenticated=false, got %+v", out)
	}
	if out.OK {
		t.Fatalf("expected OK=false on broken auth, got %+v", out)
	}
	if len(out.Errors) == 0 {
		t.Fatalf("expected an auth error message")
	}
	if out.Diagnostics == nil || out.Diagnostics.LoginHint == "" {
		t.Fatalf("expected embedded login diagnostics, got %+v", out.Diagnostics)
	}
}

func TestPrPrepare_AccountMismatch_EmbedsAccountDiagnostics(t *testing.T) {
	// Active account ("wronguser") differs from pr.expectedAccount
	// ("correctuser"), which is itself among the locally logged-in
	// accounts — this is exactly the scenario
	// pr-recover-gh-account.js's standalone diagnostics target.
	stubGHDispatch(t, []ghRule{
		{prefix: []string{"api", "user"}, stdout: "wronguser", exit: 0},
		{prefix: []string{"auth", "status"}, stdout: authStatusJSON(t, map[string]bool{
			"wronguser":   true,
			"correctuser": false,
		}), exit: 0},
	})

	root := t.TempDir()
	if err := config.WriteSection(root, "pr", map[string]any{"expectedAccount": "correctuser"}); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	workDir := t.TempDir()

	out, err := prPrepareCore(root, workDir, PRPrepareIn{SkipConfigCheck: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !out.AccountMismatch {
		t.Fatalf("expected AccountMismatch=true, got %+v", out)
	}
	if out.OK {
		t.Fatalf("expected OK=false on account mismatch, got %+v", out)
	}
	if out.Diagnostics == nil {
		t.Fatalf("expected embedded account diagnostics")
	}
	if out.Diagnostics.MatchedAccount != "correctuser" {
		t.Errorf("MatchedAccount: got %q, want %q", out.Diagnostics.MatchedAccount, "correctuser")
	}
	if !strings.Contains(out.Diagnostics.SwitchHint, "correctuser") {
		t.Errorf("SwitchHint: got %q, want it to mention correctuser", out.Diagnostics.SwitchHint)
	}
	if len(out.Diagnostics.Candidates) != 2 {
		t.Errorf("Candidates: got %d, want 2 (%+v)", len(out.Diagnostics.Candidates), out.Diagnostics.Candidates)
	}
}

func TestPrPrepare_BranchGuardMismatch_HardGate(t *testing.T) {
	stubGHDispatch(t, []ghRule{
		{prefix: []string{"api", "user"}, stdout: "someone", exit: 0},
	})

	workDir := t.TempDir()
	initGitRepoWithBranch(t, workDir, "feat/actual-branch")

	out, err := prPrepareCore(workDir, workDir, PRPrepareIn{
		SkipConfigCheck: true,
		ExpectedBranch:  "feat/expected-branch",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.OK {
		t.Fatalf("expected OK=false on branch-guard mismatch, got %+v", out)
	}
	if out.BranchGuard == nil || out.BranchGuard.OK {
		t.Fatalf("expected an active, failing BranchGuard result, got %+v", out.BranchGuard)
	}
	if !strings.Contains(strings.Join(out.Errors, " "), "Branch mismatch") {
		t.Errorf("expected a branch-mismatch error, got %v", out.Errors)
	}
}

func TestPrPrepare_HappyPath_JiraAndTemplate(t *testing.T) {
	stubGHDispatch(t, []ghRule{
		{prefix: []string{"api", "user"}, stdout: "someone", exit: 0},
	})

	workDir := t.TempDir()
	initGitRepoWithBranch(t, workDir, "feat/PROJ-123-add-thing")

	sdlcDir := filepath.Join(workDir, paths.DataDir)
	if err := os.MkdirAll(sdlcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	tmpl := "## Summary\n\n## Testing\n"
	if err := os.WriteFile(filepath.Join(sdlcDir, "pr-template.md"), []byte(tmpl), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := prPrepareCore(workDir, workDir, PRPrepareIn{SkipConfigCheck: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !out.OK {
		t.Fatalf("expected OK=true on happy path, got %+v", out)
	}
	if out.JiraTicket != "PROJ-123" {
		t.Errorf("JiraTicket: got %q, want %q", out.JiraTicket, "PROJ-123")
	}
	if out.Template == nil || len(out.Template.Headings) != 2 {
		t.Fatalf("expected resolved template with 2 headings, got %+v", out.Template)
	}
	if out.BranchGuard == nil || out.BranchGuard.Active {
		t.Errorf("expected an inactive BranchGuard (no ExpectedBranch configured), got %+v", out.BranchGuard)
	}
}

func TestPrPrepare_ProtectedBranch_Rejected(t *testing.T) {
	stubGHDispatch(t, []ghRule{
		{prefix: []string{"api", "user"}, stdout: "someone", exit: 0},
	})

	workDir := t.TempDir()
	initGitRepoWithBranch(t, workDir, "main")

	out, err := prPrepareCore(workDir, workDir, PRPrepareIn{SkipConfigCheck: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.OK {
		t.Fatalf("expected OK=false on main branch, got %+v", out)
	}
	if !strings.Contains(strings.Join(out.Errors, " "), "main") {
		t.Errorf("expected a protected-branch error mentioning main, got %v", out.Errors)
	}
}

// authStatusJSON builds a `gh auth status --json hosts` fixture for
// github.com with the given login->active map, all reported as
// State:"success".
func authStatusJSON(t *testing.T, accounts map[string]bool) string {
	t.Helper()
	var entries []string
	for login, active := range accounts {
		entries = append(entries, `{"login":"`+login+`","active":`+boolStr(active)+`,"state":"success"}`)
	}
	return `{"hosts":{"github.com":[` + strings.Join(entries, ",") + `]}}`
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// ---------------------------------------------------------------------------
// pr_apply
// ---------------------------------------------------------------------------

func TestPrApply_NoExistingPR_Creates(t *testing.T) {
	stubGHDispatch(t, []ghRule{
		{prefix: []string{"pr", "view"}, exit: 1},
		{prefix: []string{"pr", "create"}, stdout: "https://github.com/o/r/pull/9", exit: 0},
	})

	workDir := t.TempDir()
	out, err := prApplyCore(workDir, workDir, PRApplyIn{Title: "Add thing", Body: "Body text"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !out.Created {
		t.Errorf("expected Created=true, got %+v", out)
	}
	if out.URL != "https://github.com/o/r/pull/9" {
		t.Errorf("URL: got %q", out.URL)
	}
}

func TestPrApply_ExistingPR_Updates(t *testing.T) {
	stubGHDispatch(t, []ghRule{
		{prefix: []string{"pr", "view"}, stdout: `{"number":9,"title":"old","url":"https://github.com/o/r/pull/9","state":"OPEN","labels":[]}`, exit: 0},
		{prefix: []string{"pr", "edit"}, stdout: "https://github.com/o/r/pull/9", exit: 0},
	})

	workDir := t.TempDir()
	out, err := prApplyCore(workDir, workDir, PRApplyIn{Title: "Updated title", Body: "Body text"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Created {
		t.Errorf("expected Created=false (update path), got %+v", out)
	}
	if out.URL != "https://github.com/o/r/pull/9" {
		t.Errorf("URL: got %q", out.URL)
	}
}

func TestPrApply_MissingTitle_DomainError(t *testing.T) {
	workDir := t.TempDir()
	_, err := prApplyCore(workDir, workDir, PRApplyIn{Title: "  ", Body: "x"})
	if err == nil {
		t.Fatal("expected an error for empty title")
	}
}

// ---------------------------------------------------------------------------
// releaseSource provenance gate (task 8) — pure unit tests, no FS.
//
// The negative cases (missing/invalid/auto-rejected releaseSource) all
// short-circuit inside prApplyCoreWith before any rt.* call is made, so
// they're driven straight through prApplyCore with empty/unused
// mainRoot/workDir — nothing ever touches disk or a real "gh"/"git" binary.
// The positive (accepted) cases exercise the rest of prApplyCoreWith too, so
// they inject a fully mocked prRuntime (fakeReleasePRRuntime below) instead
// of using t.TempDir()/stubGHDispatch — same "mocks only" discipline as
// TestEnsureReleaseLabels above.
// ---------------------------------------------------------------------------

// fakeReleasePRRuntime returns a prRuntime whose every function the release
// path can call is a canned, allocation-only stub — no filesystem, no
// subprocess. Used only by TestReleaseSourceValidation's accepted-source
// subtests, which need prApplyCoreWith to run to completion.
func fakeReleasePRRuntime() prRuntime {
	return prRuntime{
		configRead: func(root string) (*config.Config, error) { return nil, nil },
		versionDetect: func(root, path, fileType string) (*version.VersionFile, error) {
			return &version.VersionFile{Version: "1.0.0"}, nil
		},
		gitFetchTags:     func(dir string) error { return nil },
		gitTagList:       func(dir string) ([]string, error) { return nil, nil },
		gitAllSemverTags: func(dir string) ([]string, error) { return nil, nil },
		gitTagExists:     func(dir, name string) (bool, error) { return false, nil },
		ghLabelList:      func(dir string) ([]string, error) { return nil, nil },
		ghLabelCreate:    func(dir, name, color, desc string) error { return nil },
		ghPRForBranch:    func(dir string) ghx.PRMetadata { return ghx.PRMetadata{Exists: false} },
		ghPRCreate:       func(dir, title, body string) (string, error) { return "https://example.com/pull/1", nil },
		execRun:          func(name string, args []string, opts execx.Options) (string, error) { return "", nil },
	}
}

func TestReleaseSourceValidation(t *testing.T) {
	t.Run("missing releaseSource with releaseLevel set is rejected", func(t *testing.T) {
		_, err := prApplyCore("", "", PRApplyIn{Title: "T", Body: "B", ReleaseLevel: "patch"})
		if err == nil {
			t.Fatal("expected an error for missing releaseSource")
		}
		if !strings.Contains(err.Error(), "releaseSource") {
			t.Errorf("error should mention releaseSource, got: %v", err)
		}
	})

	t.Run("invalid releaseSource value is rejected", func(t *testing.T) {
		_, err := prApplyCore("", "", PRApplyIn{Title: "T", Body: "B", ReleaseLevel: "patch", ReleaseSource: "llm"})
		if err == nil {
			t.Fatal("expected an error for an invalid releaseSource value")
		}
		if !strings.Contains(err.Error(), "releaseSource") {
			t.Errorf("error should mention releaseSource, got: %v", err)
		}
	})

	t.Run("auto mode rejects releaseSource=user", func(t *testing.T) {
		_, err := prApplyCore("", "", PRApplyIn{
			Title: "T", Body: "B", ReleaseLevel: "patch", ReleaseSource: "user", AutoMode: true,
		})
		if err == nil {
			t.Fatal("expected an error for a user-sourced releaseLevel under AutoMode")
		}
		if !strings.Contains(err.Error(), "auto mode") {
			t.Errorf("error should mention auto mode, got: %v", err)
		}
	})

	t.Run("no releaseLevel set skips the gate entirely", func(t *testing.T) {
		// AutoMode with no releaseLevel and no releaseSource: nothing to
		// validate — falls through to the ordinary no-release-intent path.
		rt := fakeReleasePRRuntime()
		out, err := prApplyCoreWith("", "", PRApplyIn{Title: "T", Body: "B", AutoMode: true}, rt)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if out.ReleaseIntent != nil {
			t.Errorf("expected nil ReleaseIntent, got %+v", out.ReleaseIntent)
		}
	})

	t.Run("non-auto mode accepts releaseSource=user", func(t *testing.T) {
		rt := fakeReleasePRRuntime()
		out, err := prApplyCoreWith("", "", PRApplyIn{
			Title: "T", Body: "B", ReleaseLevel: "patch", ReleaseSource: "user",
		}, rt)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if out.ReleaseIntent == nil {
			t.Fatal("expected ReleaseIntent to be populated")
		}
	})

	t.Run("auto mode accepts releaseSource=config (config passthrough)", func(t *testing.T) {
		rt := fakeReleasePRRuntime()
		out, err := prApplyCoreWith("", "", PRApplyIn{
			Title: "T", Body: "B", ReleaseLevel: "minor", ReleaseSource: "config", AutoMode: true,
		}, rt)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if out.ReleaseIntent == nil {
			t.Fatal("expected ReleaseIntent to be populated")
		}
	})

	t.Run("auto mode accepts releaseSource=pipeline", func(t *testing.T) {
		rt := fakeReleasePRRuntime()
		out, err := prApplyCoreWith("", "", PRApplyIn{
			Title: "T", Body: "B", ReleaseLevel: "major", ReleaseSource: "pipeline", AutoMode: true,
		}, rt)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if out.ReleaseIntent == nil {
			t.Fatal("expected ReleaseIntent to be populated")
		}
	})
}

// ---------------------------------------------------------------------------
// Registration smoke test
// ---------------------------------------------------------------------------

func TestRegisterPRTools_DoesNotPanic(t *testing.T) {
	s := mcpserver.New("test", "0.0.0")
	RegisterPRTools(s)
}

// ---------------------------------------------------------------------------
// pr_apply release intent tests
// ---------------------------------------------------------------------------

// ghCallsBody extracts the --body value from a gh-calls.log for the first
// invocation whose args contain the given subcommand. The log format is:
// each invocation is NUL-separated args terminated by ASCII record separator
// (0x1E).
func ghCallsBody(t *testing.T, logPath, subcommand string) string {
	t.Helper()
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read gh-calls.log: %v", err)
	}
	for _, record := range strings.Split(string(data), "\x1e") {
		record = strings.TrimSpace(record)
		if record == "" {
			continue
		}
		args := strings.Split(record, "\x00")
		hasSubcmd := false
		for _, a := range args {
			if a == subcommand {
				hasSubcmd = true
				break
			}
		}
		if !hasSubcmd {
			continue
		}
		for i, a := range args {
			if a == "--body" && i+1 < len(args) {
				return args[i+1]
			}
		}
	}
	t.Fatalf("no --body found for %q in gh-calls.log", subcommand)
	return ""
}

// seedVersionFile writes a minimal package.json with the given version into dir.
func seedVersionFile(t *testing.T, dir, ver string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "package.json"),
		[]byte(`{"version":"`+ver+`"}`), 0o644); err != nil {
		t.Fatalf("seed package.json: %v", err)
	}
}

func TestPRApply_WithRelease_LabelAdded(t *testing.T) {
	// Create path: no existing PR. Expect --add-label release:minor called.
	// The label rule uses a 4-element prefix so wrong label = no match = exit 1.
	stubGHDispatch(t, []ghRule{
		{prefix: []string{"pr", "view"}, exit: 1},
		{prefix: []string{"pr", "create"}, stdout: "https://github.com/o/r/pull/10", exit: 0},
		{prefix: []string{"pr", "edit", "--add-label", "release:minor"}, exit: 0},
	})

	workDir := t.TempDir()
	initGitRepoWithBranch(t, workDir, "feat/release-label")
	seedVersionFile(t, workDir, "1.2.0")

	out, err := prApplyCore(workDir, workDir, PRApplyIn{
		Title:         "Release label test",
		Body:          "Some body",
		ReleaseLevel:  "minor",
		ReleaseSource: "user",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.ReleaseIntent == nil {
		t.Fatal("expected ReleaseIntent to be populated")
	}
	if out.ReleaseIntent.LabelApplied != "release:minor" {
		t.Errorf("LabelApplied: got %q, want %q", out.ReleaseIntent.LabelApplied, "release:minor")
	}
}

func TestPRApply_WithRelease_NotesInBody(t *testing.T) {
	logPath := stubGHDispatch(t, []ghRule{
		{prefix: []string{"pr", "view"}, exit: 1},
		{prefix: []string{"pr", "create"}, stdout: "https://github.com/o/r/pull/11", exit: 0},
		{prefix: []string{"pr", "edit", "--add-label", "release:patch"}, exit: 0},
	})

	workDir := t.TempDir()
	initGitRepoWithBranch(t, workDir, "feat/notes-body")
	seedVersionFile(t, workDir, "2.0.0")

	out, err := prApplyCore(workDir, workDir, PRApplyIn{
		Title:         "Notes test",
		Body:          "Original body",
		ReleaseLevel:  "patch",
		ReleaseNotes:  "Fixed the bug in auth module.",
		ReleaseSource: "user",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.ReleaseIntent == nil || !out.ReleaseIntent.NotesInBody {
		t.Fatal("expected NotesInBody=true")
	}
	if out.ReleaseIntent.ComputedVersion != "2.0.1" {
		t.Errorf("ComputedVersion: got %q, want %q", out.ReleaseIntent.ComputedVersion, "2.0.1")
	}
	// Verify release markers are present in body passed to gh pr create.
	body := ghCallsBody(t, logPath, "create")
	if !strings.Contains(body, "<!-- release-notes-start -->") {
		t.Errorf("body missing release-notes-start marker")
	}
	if !strings.Contains(body, "<!-- release-level:patch -->") {
		t.Errorf("body missing release-level marker")
	}
	if !strings.Contains(body, "Fixed the bug in auth module.") {
		t.Errorf("body missing release notes text")
	}
	if !strings.Contains(body, "## [2.0.1]") {
		t.Errorf("body missing version header, got: %s", body)
	}
}

func TestPRApply_WithRelease_VersionComputed(t *testing.T) {
	stubGHDispatch(t, []ghRule{
		{prefix: []string{"pr", "view"}, exit: 1},
		{prefix: []string{"pr", "create"}, stdout: "https://github.com/o/r/pull/12", exit: 0},
		{prefix: []string{"pr", "edit", "--add-label", "release:major"}, exit: 0},
	})

	workDir := t.TempDir()
	initGitRepoWithBranch(t, workDir, "feat/version-compute")
	seedVersionFile(t, workDir, "1.5.3")
	// Add a tag higher than file version — bump base should be the tag.
	gitTag(t, workDir, "v1.6.0")

	out, err := prApplyCore(workDir, workDir, PRApplyIn{
		Title:         "Version compute test",
		Body:          "body",
		ReleaseLevel:  "major",
		ReleaseSource: "user",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.ReleaseIntent == nil {
		t.Fatal("expected ReleaseIntent")
	}
	// max(file=1.5.3, tag=1.6.0) = 1.6.0, major bump = 2.0.0
	if out.ReleaseIntent.ComputedVersion != "2.0.0" {
		t.Errorf("ComputedVersion: got %q, want %q", out.ReleaseIntent.ComputedVersion, "2.0.0")
	}
	if out.ReleaseIntent.PreviousVersion != "1.5.3" {
		t.Errorf("PreviousVersion: got %q, want %q", out.ReleaseIntent.PreviousVersion, "1.5.3")
	}
	if out.ReleaseIntent.TagName != "v2.0.0" {
		t.Errorf("TagName: got %q, want %q", out.ReleaseIntent.TagName, "v2.0.0")
	}
}

func TestPRApply_WithRelease_CollisionError(t *testing.T) {
	// Use a custom tagPrefix ("rel-") so TagList returns nothing (it's
	// prefix-blind), bumpBase = fileVersion, and we create a collision
	// by planting the expected tag beforehand.
	stubGHDispatch(t, []ghRule{
		{prefix: []string{"pr", "view"}, exit: 1},
	})

	workDir := t.TempDir()
	initGitRepoWithBranch(t, workDir, "feat/collision")
	seedVersionFile(t, workDir, "1.2.0")

	// Write config with tagPrefix "rel-".
	if err := config.WriteSection(workDir, "version", map[string]any{
		"tagPrefix": "rel-",
	}); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	// File version is 1.2.0, minor bump => 1.3.0, tag => rel-1.3.0.
	// Plant that tag to cause collision.
	gitTag(t, workDir, "rel-1.3.0")

	_, err := prApplyCore(workDir, workDir, PRApplyIn{
		Title:         "Collision test",
		Body:          "body",
		ReleaseLevel:  "minor",
		ReleaseSource: "user",
	})
	if err == nil {
		t.Fatal("expected collision error")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("error should mention collision, got: %v", err)
	}
}

func TestPRApply_WithoutRelease_Unchanged(t *testing.T) {
	// No --add-label rule: if called, stub exits 1 and test fails.
	stubGHDispatch(t, []ghRule{
		{prefix: []string{"pr", "view"}, exit: 1},
		{prefix: []string{"pr", "create"}, stdout: "https://github.com/o/r/pull/13", exit: 0},
	})

	workDir := t.TempDir()
	out, err := prApplyCore(workDir, workDir, PRApplyIn{
		Title: "No release",
		Body:  "Just a normal PR",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.ReleaseIntent != nil {
		t.Errorf("expected nil ReleaseIntent, got %+v", out.ReleaseIntent)
	}
}

func TestPRApply_WithRC_NextRCComputed(t *testing.T) {
	stubGHDispatch(t, []ghRule{
		{prefix: []string{"pr", "view"}, exit: 1},
		{prefix: []string{"pr", "create"}, stdout: "https://github.com/o/r/pull/14", exit: 0},
		{prefix: []string{"pr", "edit", "--add-label", "release:minor-rc"}, exit: 0},
	})

	workDir := t.TempDir()
	initGitRepoWithBranch(t, workDir, "feat/rc-next")
	seedVersionFile(t, workDir, "3.0.0")
	// Plant existing RC tags. minor bump of 3.0.0 = 3.1.0, so RCs are on 3.1.0.
	gitTag(t, workDir, "v3.1.0-rc1")
	gitTag(t, workDir, "v3.1.0-rc2")

	out, err := prApplyCore(workDir, workDir, PRApplyIn{
		Title:             "RC next test",
		Body:              "body",
		ReleaseLevel:      "minor",
		ReleasePreRelease: "rc",
		ReleaseSource:     "user",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.ReleaseIntent == nil {
		t.Fatal("expected ReleaseIntent")
	}
	// Next RC after rc1 and rc2 should be rc3.
	if out.ReleaseIntent.ComputedVersion != "3.1.0-rc3" {
		t.Errorf("ComputedVersion: got %q, want %q", out.ReleaseIntent.ComputedVersion, "3.1.0-rc3")
	}
	if out.ReleaseIntent.TagName != "v3.1.0-rc3" {
		t.Errorf("TagName: got %q, want %q", out.ReleaseIntent.TagName, "v3.1.0-rc3")
	}
}

func TestPRApply_WithRC_LabelFormat(t *testing.T) {
	stubGHDispatch(t, []ghRule{
		{prefix: []string{"pr", "view"}, exit: 1},
		{prefix: []string{"pr", "create"}, stdout: "https://github.com/o/r/pull/15", exit: 0},
		{prefix: []string{"pr", "edit", "--add-label", "release:patch-rc"}, exit: 0},
	})

	workDir := t.TempDir()
	initGitRepoWithBranch(t, workDir, "feat/rc-label")
	seedVersionFile(t, workDir, "1.0.0")

	out, err := prApplyCore(workDir, workDir, PRApplyIn{
		Title:             "RC label test",
		Body:              "body",
		ReleaseLevel:      "patch",
		ReleasePreRelease: "rc",
		ReleaseSource:     "user",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.ReleaseIntent == nil {
		t.Fatal("expected ReleaseIntent")
	}
	if out.ReleaseIntent.LabelApplied != "release:patch-rc" {
		t.Errorf("LabelApplied: got %q, want %q", out.ReleaseIntent.LabelApplied, "release:patch-rc")
	}
}

func TestPRApply_WithRC_PreReleaseMarker(t *testing.T) {
	logPath := stubGHDispatch(t, []ghRule{
		{prefix: []string{"pr", "view"}, exit: 1},
		{prefix: []string{"pr", "create"}, stdout: "https://github.com/o/r/pull/16", exit: 0},
		{prefix: []string{"pr", "edit", "--add-label", "release:minor-rc"}, exit: 0},
	})

	workDir := t.TempDir()
	initGitRepoWithBranch(t, workDir, "feat/rc-marker")
	seedVersionFile(t, workDir, "2.0.0")

	out, err := prApplyCore(workDir, workDir, PRApplyIn{
		Title:             "RC marker test",
		Body:              "body",
		ReleaseLevel:      "minor",
		ReleasePreRelease: "rc",
		ReleaseSource:     "user",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.ReleaseIntent == nil {
		t.Fatal("expected ReleaseIntent")
	}
	if out.ReleaseIntent.PreRelease != "rc" {
		t.Errorf("PreRelease: got %q, want %q", out.ReleaseIntent.PreRelease, "rc")
	}
	if !strings.Contains(out.ReleaseIntent.ComputedVersion, "-rc") {
		t.Errorf("ComputedVersion should contain -rc, got %q", out.ReleaseIntent.ComputedVersion)
	}
	// Verify release-pre marker is present in body passed to gh.
	body := ghCallsBody(t, logPath, "create")
	if !strings.Contains(body, "<!-- release-pre:rc -->") {
		t.Errorf("body missing release-pre:rc marker")
	}
	if !strings.Contains(body, "<!-- release-level:minor -->") {
		t.Errorf("body missing release-level:minor marker")
	}
}

// ---------------------------------------------------------------------------
// ensureReleaseLabels — prRuntime mocks only, no FS/gh involved.
// ---------------------------------------------------------------------------

func TestEnsureReleaseLabels(t *testing.T) {
	t.Run("no existing labels — creates all six", func(t *testing.T) {
		var created []string
		rt := prRuntime{
			ghLabelList: func(dir string) ([]string, error) { return nil, nil },
			ghLabelCreate: func(dir, name, color, desc string) error {
				created = append(created, name)
				return nil
			},
		}
		if err := ensureReleaseLabels(rt, "/fake/dir"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(created) != len(releaseLabels) {
			t.Fatalf("expected %d labels created, got %d: %v", len(releaseLabels), len(created), created)
		}
		for _, l := range releaseLabels {
			found := false
			for _, name := range created {
				if name == l.Name {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("expected %q to be created, was not", l.Name)
			}
		}
	})

	t.Run("all labels already exist — idempotent, no creates", func(t *testing.T) {
		existing := make([]string, 0, len(releaseLabels))
		for _, l := range releaseLabels {
			existing = append(existing, l.Name)
		}
		rt := prRuntime{
			ghLabelList: func(dir string) ([]string, error) { return existing, nil },
			ghLabelCreate: func(dir, name, color, desc string) error {
				t.Fatalf("ghLabelCreate should not be called for already-existing label %q", name)
				return nil
			},
		}
		if err := ensureReleaseLabels(rt, "/fake/dir"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("some labels exist — only missing ones created", func(t *testing.T) {
		var created []string
		rt := prRuntime{
			ghLabelList: func(dir string) ([]string, error) {
				return []string{"release:patch", "release:minor"}, nil
			},
			ghLabelCreate: func(dir, name, color, desc string) error {
				created = append(created, name)
				return nil
			},
		}
		if err := ensureReleaseLabels(rt, "/fake/dir"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(created) != len(releaseLabels)-2 {
			t.Fatalf("expected %d labels created, got %d: %v", len(releaseLabels)-2, len(created), created)
		}
		for _, name := range created {
			if name == "release:patch" || name == "release:minor" {
				t.Errorf("already-existing label %q should not have been created", name)
			}
		}
	})

	t.Run("ghLabelList failure — best-effort, no creates attempted, nil error", func(t *testing.T) {
		called := false
		rt := prRuntime{
			ghLabelList: func(dir string) ([]string, error) { return nil, errors.New("gh not authenticated") },
			ghLabelCreate: func(dir, name, color, desc string) error {
				called = true
				return nil
			},
		}
		if err := ensureReleaseLabels(rt, "/fake/dir"); err != nil {
			t.Fatalf("expected nil error when ghLabelList fails (best-effort), got %v", err)
		}
		if called {
			t.Fatal("ghLabelCreate should not be called when ghLabelList fails")
		}
	})

	t.Run("ghLabelCreate failure on one label — remaining labels still attempted", func(t *testing.T) {
		var created []string
		rt := prRuntime{
			ghLabelList: func(dir string) ([]string, error) { return nil, nil },
			ghLabelCreate: func(dir, name, color, desc string) error {
				created = append(created, name)
				if name == "release:patch" {
					return errors.New("simulated create failure")
				}
				return nil
			},
		}
		err := ensureReleaseLabels(rt, "/fake/dir")
		if err == nil {
			t.Fatal("expected non-nil error surfaced from the failed create")
		}
		if len(created) != len(releaseLabels) {
			t.Fatalf("expected all %d labels attempted despite one failure, got %d: %v", len(releaseLabels), len(created), created)
		}
	})
}
