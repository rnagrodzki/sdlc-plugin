package tools

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/rnagrodzki/sdlc-plugin/internal/config"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
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

func stubGHDispatch(t *testing.T, rules []ghRule) {
	t.Helper()

	dir := t.TempDir()
	name := "gh"
	if runtime.GOOS == "windows" {
		name = "gh.bat"
	}

	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
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
			sdlcDir := filepath.Join(root, ".sdlc")
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
	sdlcDir := filepath.Join(root, ".sdlc")
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

	sdlcDir := filepath.Join(workDir, ".sdlc")
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
	out, err := prApplyCore(workDir, PRApplyIn{Title: "Add thing", Body: "Body text"})
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
	out, err := prApplyCore(workDir, PRApplyIn{Title: "Updated title", Body: "Body text"})
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
	_, err := prApplyCore(workDir, PRApplyIn{Title: "  ", Body: "x"})
	if err == nil {
		t.Fatal("expected an error for empty title")
	}
}

// ---------------------------------------------------------------------------
// Registration smoke test
// ---------------------------------------------------------------------------

func TestRegisterPRTools_DoesNotPanic(t *testing.T) {
	s := mcpserver.New("test", "0.0.0")
	RegisterPRTools(s)
}
