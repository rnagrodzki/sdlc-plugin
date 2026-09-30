package tools

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/rnagrodzki/sdlc-plugin/internal/branch"
	"github.com/rnagrodzki/sdlc-plugin/internal/config"
	"github.com/rnagrodzki/sdlc-plugin/internal/configmigrate"
	"github.com/rnagrodzki/sdlc-plugin/internal/execx"
	"github.com/rnagrodzki/sdlc-plugin/internal/ghx"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
	"github.com/rnagrodzki/sdlc-plugin/internal/prtemplate"
	"github.com/rnagrodzki/sdlc-plugin/internal/version"
)

// ---------------------------------------------------------------------------
// prRuntime mock helpers
//
// Every test below drives prPrepareCoreWith/prApplyCoreWith directly through
// a hand-built prRuntime — no real filesystem, no "gh"/"git" subprocess.
// Only two exceptions remain, both because the function under test has no
// prRuntime (or any other) dependency-injection seam to mock through — see
// their doc comments for why real FS is unavoidable without a pr.go change,
// which is out of scope for a pr_test.go-only task.
// ---------------------------------------------------------------------------

// mockAddLabelExec returns an execRun stub that succeeds only for the exact
// `gh pr edit --add-label <wantLabel>` invocation prReleaseApplyLabelWith
// issues when there are no stale labels to remove (the create path, and any
// existing-PR case with no other release:* labels present), and fails
// (surfacing as an InfraError) for anything else — the mock-based equivalent
// of the old stubGHDispatch fixtures' narrow prefix-matched rules ("wrong
// label = no match = exit 1").
func mockAddLabelExec(wantLabel string) func(name string, args []string, opts execx.Options) (string, error) {
	return func(name string, args []string, opts execx.Options) (string, error) {
		if name == "gh" && len(args) == 4 && args[0] == "pr" && args[1] == "edit" && args[2] == "--add-label" && args[3] == wantLabel {
			return "", nil
		}
		return "", fmt.Errorf("unexpected exec call: %s %v (want gh pr edit --add-label %s)", name, args, wantLabel)
	}
}

// constVersionDetect returns a versionDetect stub that always reports ver as
// the detected file version, regardless of the root/path/fileType args.
func constVersionDetect(ver string) func(root, path, fileType string) (*version.VersionFile, error) {
	return func(root, path, fileType string) (*version.VersionFile, error) {
		return &version.VersionFile{Version: ver}, nil
	}
}

// releaseTestRuntime builds a prRuntime covering every field
// ensureReleaseLabels/prApplyCoreWith's release path can reach, with
// deterministic no-op defaults: no config overrides
// (tagPrefix defaults to "v"), fileVersion as given, no existing tags, no
// tag collision, all release labels already present, and no existing PR.
// Callers override individual fields per scenario (tags, config, exec,
// PR-create output).
func releaseTestRuntime(fileVersion string) prRuntime {
	return prRuntime{
		configRead:       func(root string) (*config.Config, error) { return nil, nil },
		versionDetect:    constVersionDetect(fileVersion),
		gitFetchTags:     func(dir string) error { return nil },
		gitTagList:       func(dir string) ([]string, error) { return nil, nil },
		gitAllSemverTags: func(dir string) ([]string, error) { return nil, nil },
		ghLabelList:      func(dir string) ([]string, error) { return nil, nil },
		ghLabelCreate:    func(dir, name, color, desc string) error { return nil },
		ghPRForBranch:    func(dir string) ghx.PRMetadata { return ghx.PRMetadata{Exists: false} },
		ghPRCreate:       func(dir, title, body string) (string, error) { return "https://example.com/pull/0", nil },
		// Idle defaults for the push-decision block prApplyCoreWith always
		// runs after release-intent computation: upstream already exists
		// with 0 commits ahead, so no push is attempted and gitPushSetUpstream
		// is never called. gitLogSinceTag defaults to no commits (idle for
		// both the skipReleaseCheck verification gate and the release-notes
		// auto-generation path). Callers exercising push/skip-check/notes
		// behavior override the relevant field(s) explicitly.
		gitHasUpstream:     func(dir string) (bool, error) { return true, nil },
		gitCommitsAhead:    func(dir string) (int, error) { return 0, nil },
		gitPushSetUpstream: func(dir, remote string) error { return nil },
		gitLogSinceTag:     func(dir string) ([]string, error) { return nil, nil },
	}
}

// ---------------------------------------------------------------------------
// pr_validate_body
//
// prValidateBodyCore (pr.go) has no prRuntime — it calls
// prtemplate.Resolve(root) directly against the real filesystem, with no
// injection seam. Adding one would be a pr.go change, which is out of scope
// for this pr_test.go-only task (see task note on documenting cases that
// cannot be fully converted).
// ---------------------------------------------------------------------------

func TestPrValidateBody_NoTemplate_AlwaysOK(t *testing.T) {
	// No real directory is created: prtemplate.Resolve treats a path with
	// no pr-template.md at either the canonical or legacy location as
	// "no template" (found=false, no error) — a nonexistent root satisfies
	// that exactly as well as an empty temp dir would, without t.TempDir().
	root := filepath.Join(os.TempDir(), "pr-test-nonexistent-root", "does-not-exist")
	out, err := prValidateBodyCore(root, PRValidateBodyIn{Body: "anything at all"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !out.OK || len(out.Errors) != 0 {
		t.Fatalf("expected OK with no errors when no template exists, got %+v", out)
	}
}

// TestPrValidateBody_TemplateFixtureMatrix is the one surviving real-FS test
// in this file. prValidateBodyCore has no dependency-injection seam (see
// package doc comment above) — prtemplate.Resolve(root) reads
// <root>/.sdlc-v2/pr-template.md straight off disk with no way to swap in a
// mock without editing pr.go, which is out of scope here. t.TempDir() /
// os.MkdirAll / os.WriteFile are kept deliberately for this test only.
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
// stripAttribution
//
// Pure string function — no prRuntime involved, no FS.
// ---------------------------------------------------------------------------

func TestStripAttribution(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "body without attribution is unchanged",
			in:   "## Summary\nDid the thing.\n",
			want: "## Summary\nDid the thing.\n",
		},
		{
			name: "trailing Claude Code footer stripped",
			in:   "## Summary\nDid the thing.\n\n🤖 Generated with [Claude Code](https://claude.com/claude-code)\n",
			want: "## Summary\nDid the thing.\n",
		},
		{
			name: "Co-Authored-By Claude line stripped",
			in:   "## Summary\nDid the thing.\n\nCo-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>\n",
			want: "## Summary\nDid the thing.\n",
		},
		{
			name: "attribution mid-content stripped, surrounding content kept",
			in:   "## Summary\nGenerated by Claude for this change.\nDid the thing.\n",
			want: "## Summary\n\nDid the thing.\n",
		},
		{
			name: "multiple attribution lines all stripped",
			in:   "## Summary\nCreated with Claude.\nDid the thing.\nPowered by Claude tooling.\n🤖 Generated with [Claude Code](https://claude.com/claude-code)\n",
			want: "## Summary\n\nDid the thing.\n",
		},
		{
			name: "does not corrupt release markers",
			in:   "## Summary\nDid the thing.\n\n---\n<!-- release-level:patch -->\n<!-- release-notes-start -->\n<!-- release-notes-end -->\n",
			want: "## Summary\nDid the thing.\n\n---\n<!-- release-level:patch -->\n<!-- release-notes-start -->\n<!-- release-notes-end -->\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripAttribution(tt.in)
			if got != tt.want {
				t.Errorf("stripAttribution(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// pr_prepare
//
// prPrepareCoreWith takes a prRuntime (pr.go), so every test below drives it
// directly with hand-built mocks instead of a real git repo + stubbed gh
// binary. branchValidate/jiraExtract are wired to the real, pure
// (no I/O) branch.ValidateExpectedBranch / detectJiraTicket implementations
// rather than re-mocked, since there is nothing to fake about them.
// ---------------------------------------------------------------------------

func TestPrPrepare_ConfigNeedsMigration_ShortCircuits(t *testing.T) {
	rt := prRuntime{
		configMigrateVerify: func(root string) error {
			return errors.New("config schemaVersion 1 is stale (needs migration)")
		},
	}

	out, err := prPrepareCoreWith("/mock/root", "/mock/root", PRPrepareIn{}, rt)
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
	if out.Next != "Fix the errors above, then call pr_prepare again." {
		t.Errorf("Next: got %q", out.Next)
	}
}

func TestPrPrepare_ConfigMoveKeysFails_ShortCircuits(t *testing.T) {
	rt := prRuntime{
		configMigrateVerify: func(root string) error { return nil },
		configMoveKeys: func(root string) ([]string, error) {
			return nil, &configmigrate.MovedKeysErr{
				Reason: "local.toml already has a different value for [github] expectedAccount",
				Lines:  []string{"pr.expectedAccount -> [github] expectedAccount"},
			}
		},
	}

	out, err := prPrepareCoreWith("/mock/root", "/mock/root", PRPrepareIn{}, rt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.OK {
		t.Fatalf("expected OK=false when configMoveKeys fails, got %+v", out)
	}
	if !out.NeedsMigration {
		t.Fatalf("expected NeedsMigration=true, got %+v", out)
	}
	if !strings.Contains(strings.Join(out.Errors, " "), "[github] expectedAccount") {
		t.Errorf("expected an error mentioning [github] expectedAccount, got %v", out.Errors)
	}
	// Error and suggestion are separate entries, not one spliced string.
	if len(out.Errors) != 2 || !strings.HasPrefix(out.Errors[1], "Move each listed key") {
		t.Errorf("expected [error, suggestion], got %q", out.Errors)
	}
	if out.Next != "Fix the errors above, then call pr_prepare again." {
		t.Errorf("Next: got %q", out.Next)
	}
}

func TestPrPrepare_ConfigMoveKeysMoved_WarnsAndContinues(t *testing.T) {
	rt := prRuntime{
		configMigrateVerify: func(root string) error { return nil },
		configMoveKeys: func(root string) ([]string, error) {
			return []string{"pr.expectedAccount -> [github] expectedAccount"}, nil
		},
		ghAuthProbe: func(dir, host string) ghx.AuthProbeResult {
			return ghx.AuthProbeResult{Authenticated: true, ActiveAccount: "someone"}
		},
		configReadSection: func(root, section string) (map[string]any, error) { return nil, nil },
		configRead:        func(root string) (*config.Config, error) { return nil, nil },
		execRun: func(name string, args []string, opts execx.Options) (string, error) {
			return "", errors.New("fatal: no such remote 'origin'")
		},
		gitCurrentBranch: func(dir string) (string, error) { return "feat/thing", nil },
		gitStatus:        func(dir string) (string, error) { return "", nil },
		gitDefaultBranch: func(dir string) (string, error) { return "main", nil },
		gitHasUpstream:   func(dir string) (bool, error) { return true, nil },
		gitCommitsAhead:  func(dir string) (int, error) { return 0, nil },
		branchValidate:   branch.ValidateExpectedBranch,
		jiraExtract:      func(branchName string) string { return "" },
		templateResolve:  func(root string) (*prtemplate.Template, error) { return nil, nil },
	}

	out, err := prPrepareCoreWith("/mock/root", "/mock/work", PRPrepareIn{}, rt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !out.OK {
		t.Fatalf("expected OK=true, got %+v", out)
	}
	if !strings.Contains(strings.Join(out.Warnings, " "), "Moved personal settings") {
		t.Errorf("expected a warning mentioning \"Moved personal settings\", got %v", out.Warnings)
	}
}

// expectedAccountRuntime is an authenticated, clean-tree runtime whose
// [github] section read and origin remote are supplied by the caller.
func expectedAccountRuntime(readSection func(root, section string) (map[string]any, error), originURL string) prRuntime {
	return prRuntime{
		ghAuthProbe: func(dir, host string) ghx.AuthProbeResult {
			return ghx.AuthProbeResult{Authenticated: true, ActiveAccount: "someone"}
		},
		ghRepoAccessProbe: func(dir, owner, repo, host string) ghx.RepoAccessResult {
			ok := true
			return ghx.RepoAccessResult{Accessible: &ok}
		},
		configReadSection: readSection,
		configRead:        func(root string) (*config.Config, error) { return nil, nil },
		execRun: func(name string, args []string, opts execx.Options) (string, error) {
			if originURL == "" {
				return "", errors.New("fatal: no such remote 'origin'")
			}
			return originURL, nil
		},
		gitCurrentBranch: func(dir string) (string, error) { return "feat/thing", nil },
		gitStatus:        func(dir string) (string, error) { return "", nil },
		gitDefaultBranch: func(dir string) (string, error) { return "main", nil },
		gitHasUpstream:   func(dir string) (bool, error) { return true, nil },
		gitCommitsAhead:  func(dir string) (int, error) { return 0, nil },
		branchValidate:   branch.ValidateExpectedBranch,
		jiraExtract:      func(branchName string) string { return "" },
		templateResolve:  func(root string) (*prtemplate.Template, error) { return nil, nil },
	}
}

func notFoundSection(root, section string) (map[string]any, error) {
	return nil, fmt.Errorf("config: section %q: %w", section, config.ErrNotFound)
}

func TestPrPrepare_NoExpectedAccount_NoRemote_Warns(t *testing.T) {
	rt := expectedAccountRuntime(notFoundSection, "")
	out, err := prPrepareCoreWith("/mock/root", "/mock/work", PRPrepareIn{SkipConfigCheck: true}, rt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "Could not resolve expected gh account (no [github] expectedAccount in .sdlc-v2/local.toml, no origin remote). Skipping active-account check."
	if !slices.Contains(out.Warnings, want) {
		t.Errorf("expected warning %q, got %v", want, out.Warnings)
	}
	if out.RepoAccessProbed {
		t.Errorf("repo access must not be probed without a remote")
	}
}

func TestPrPrepare_NoExpectedAccount_WithRemote_ProbesRepoAccess(t *testing.T) {
	rt := expectedAccountRuntime(notFoundSection, "git@github.com:acme/widgets.git")
	out, err := prPrepareCoreWith("/mock/root", "/mock/work", PRPrepareIn{SkipConfigCheck: true}, rt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !out.RepoAccessProbed {
		t.Errorf("expected the repo access probe to run when a remote exists")
	}
	for _, w := range out.Warnings {
		if strings.Contains(w, "Could not resolve expected gh account") {
			t.Errorf("unexpected no-remote warning with a remote: %q", w)
		}
	}
}

func TestPrPrepare_GithubSectionUnreadable_Warns(t *testing.T) {
	rt := expectedAccountRuntime(func(root, section string) (map[string]any, error) {
		return nil, errors.New("config: toml: expected newline")
	}, "")
	out, err := prPrepareCoreWith("/mock/root", "/mock/work", PRPrepareIn{SkipConfigCheck: true}, rt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	found := false
	for _, w := range out.Warnings {
		if strings.Contains(w, "local.toml [github] section unreadable") && strings.Contains(w, "expected newline") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected an unreadable-section warning, got %v", out.Warnings)
	}
}

func TestPrPrepare_GithubSectionNotFound_NoUnreadableWarning(t *testing.T) {
	rt := expectedAccountRuntime(notFoundSection, "")
	out, err := prPrepareCoreWith("/mock/root", "/mock/work", PRPrepareIn{SkipConfigCheck: true}, rt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, w := range out.Warnings {
		if strings.Contains(w, "unreadable") {
			t.Errorf("ErrNotFound must not produce an unreadable warning: %q", w)
		}
	}
}

func TestPrPrepare_BrokenAuth_EmbedsLoginDiagnostics(t *testing.T) {
	// gh api user (the AuthProbe command) fails — simulates "not logged in".
	rt := prRuntime{
		ghAuthProbe: func(dir, host string) ghx.AuthProbeResult {
			return ghx.AuthProbeResult{
				Authenticated: false,
				ErrorMessage:  "Not logged in to github.com. Run: gh auth login --hostname github.com",
			}
		},
		configReadSection: func(root, section string) (map[string]any, error) { return nil, nil },
		ghGetAccounts:     func(dir, host string) ([]ghx.Account, error) { return nil, nil },
	}

	out, err := prPrepareCoreWith("/mock/root", "/mock/work", PRPrepareIn{SkipConfigCheck: true}, rt)
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
	if out.Next != "Fix the errors above, then call pr_prepare again." {
		t.Errorf("Next: got %q", out.Next)
	}
}

func TestPrPrepare_AccountMismatch_EmbedsAccountDiagnostics(t *testing.T) {
	// Active account ("wronguser") differs from local.toml's [github]
	// expectedAccount ("correctuser"), which is itself among the locally
	// logged-in accounts — this is exactly the scenario
	// pr-recover-gh-account.js's standalone diagnostics target. The stub
	// below only answers for section=="github", proving expectedAccount is
	// read from [github], not the old [pr] section.
	rt := prRuntime{
		ghAuthProbe: func(dir, host string) ghx.AuthProbeResult {
			return ghx.AuthProbeResult{Authenticated: true, ActiveAccount: "wronguser"}
		},
		configReadSection: func(root, section string) (map[string]any, error) {
			if section != "github" {
				return nil, nil
			}
			return map[string]any{"expectedAccount": "correctuser"}, nil
		},
		ghGetAccounts: func(dir, host string) ([]ghx.Account, error) {
			return []ghx.Account{
				{Login: "wronguser", Active: true},
				{Login: "correctuser", Active: false},
			}, nil
		},
	}

	out, err := prPrepareCoreWith("/mock/root", "/mock/work", PRPrepareIn{SkipConfigCheck: true}, rt)
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
	if out.Next != "Switch GitHub account, then call pr_prepare again." {
		t.Errorf("Next: got %q", out.Next)
	}
}

func TestPrPrepare_BranchGuardMismatch_HardGate(t *testing.T) {
	rt := prRuntime{
		ghAuthProbe: func(dir, host string) ghx.AuthProbeResult {
			return ghx.AuthProbeResult{Authenticated: true, ActiveAccount: "someone"}
		},
		configReadSection: func(root, section string) (map[string]any, error) { return nil, nil },
		execRun: func(name string, args []string, opts execx.Options) (string, error) {
			return "", errors.New("fatal: no such remote 'origin'")
		},
		gitCurrentBranch: func(dir string) (string, error) { return "feat/actual-branch", nil },
		gitStatus:        func(dir string) (string, error) { return "", nil },
		branchValidate:   branch.ValidateExpectedBranch,
	}

	out, err := prPrepareCoreWith("/mock/root", "/mock/work", PRPrepareIn{
		SkipConfigCheck: true,
		ExpectedBranch:  "feat/expected-branch",
	}, rt)
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
	if out.Next != "Fix the errors above, then call pr_prepare again." {
		t.Errorf("Next: got %q", out.Next)
	}
}

func TestPrPrepare_HappyPath_JiraAndTemplate(t *testing.T) {
	rt := prRuntime{
		ghAuthProbe: func(dir, host string) ghx.AuthProbeResult {
			return ghx.AuthProbeResult{Authenticated: true, ActiveAccount: "someone"}
		},
		configReadSection: func(root, section string) (map[string]any, error) { return nil, nil },
		configRead:        func(root string) (*config.Config, error) { return nil, nil },
		execRun: func(name string, args []string, opts execx.Options) (string, error) {
			return "", errors.New("fatal: no such remote 'origin'")
		},
		gitCurrentBranch: func(dir string) (string, error) { return "feat/PROJ-123-add-thing", nil },
		gitStatus:        func(dir string) (string, error) { return "", nil },
		gitDefaultBranch: func(dir string) (string, error) { return "main", nil },
		// Idle upstream: already caught up, so NeedsPush computation resolves
		// without either field's error path.
		gitHasUpstream:  func(dir string) (bool, error) { return true, nil },
		gitCommitsAhead: func(dir string) (int, error) { return 0, nil },
		branchValidate:  branch.ValidateExpectedBranch,
		jiraExtract:     func(branchName string) string { return detectJiraTicket(branchName, nil) },
		templateResolve: func(root string) (*prtemplate.Template, error) {
			return &prtemplate.Template{
				Path:     filepath.Join(root, paths.DataDir, "pr-template.md"),
				Content:  "## Summary\n\n## Testing\n",
				Headings: []string{"Summary", "Testing"},
			}, nil
		},
	}

	out, err := prPrepareCoreWith("/mock/root", "/mock/work", PRPrepareIn{SkipConfigCheck: true}, rt)
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
	if out.Next != "Call pr_apply with title, body, and release fields." {
		t.Errorf("Next: got %q", out.Next)
	}
}

func TestPrPrepare_ProtectedBranch_Rejected(t *testing.T) {
	rt := prRuntime{
		ghAuthProbe: func(dir, host string) ghx.AuthProbeResult {
			return ghx.AuthProbeResult{Authenticated: true, ActiveAccount: "someone"}
		},
		configReadSection: func(root, section string) (map[string]any, error) { return nil, nil },
		execRun: func(name string, args []string, opts execx.Options) (string, error) {
			return "", errors.New("fatal: no such remote 'origin'")
		},
		gitCurrentBranch: func(dir string) (string, error) { return "main", nil },
		gitStatus:        func(dir string) (string, error) { return "", nil },
		branchValidate:   branch.ValidateExpectedBranch,
	}

	out, err := prPrepareCoreWith("/mock/root", "/mock/work", PRPrepareIn{SkipConfigCheck: true}, rt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.OK {
		t.Fatalf("expected OK=false on main branch, got %+v", out)
	}
	if !strings.Contains(strings.Join(out.Errors, " "), "main") {
		t.Errorf("expected a protected-branch error mentioning main, got %v", out.Errors)
	}
	if out.Next != "Fix the errors above, then call pr_prepare again." {
		t.Errorf("Next: got %q", out.Next)
	}
}

// ---------------------------------------------------------------------------
// pr_prepare — version diagnostics
// ---------------------------------------------------------------------------

func TestPrPrepare_IncludesVersionDiagnostics(t *testing.T) {
	rt := prRuntime{
		ghAuthProbe: func(dir, host string) ghx.AuthProbeResult {
			return ghx.AuthProbeResult{Authenticated: true, ActiveAccount: "someone"}
		},
		configReadSection: func(root, section string) (map[string]any, error) { return nil, nil },
		configRead: func(root string) (*config.Config, error) {
			return &config.Config{
				Version: &config.VersionSection{
					PreReleasePolicy: "continue-rc",
					Method:           "semver",
					Tag:              config.VersionTagConfig{Enabled: true, Prefix: "v"},
					VersionFile:      config.VersionFileConfig{Enabled: true, Path: "version.txt", FileType: "text"},
					Changelog:        config.VersionChangelogConfig{Enabled: false},
				},
			}, nil
		},
		execRun: func(name string, args []string, opts execx.Options) (string, error) {
			// Dispatch: git remote get-url origin vs git log
			if name == "git" && len(args) > 0 && args[0] == "log" {
				return "abc1234 feat: add feature X\ndef5678 fix: correct bug Y", nil
			}
			return "", errors.New("fatal: no such remote 'origin'")
		},
		gitCurrentBranch: func(dir string) (string, error) { return "feat/my-feature", nil },
		gitStatus:        func(dir string) (string, error) { return "", nil },
		gitHasUpstream:   func(dir string) (bool, error) { return true, nil },
		gitCommitsAhead:  func(dir string) (int, error) { return 0, nil },
		branchValidate:   branch.ValidateExpectedBranch,
		jiraExtract:      func(branchName string) string { return "" },
		templateResolve:  func(root string) (*prtemplate.Template, error) { return nil, nil },
		versionDetect: func(root, path, fileType string) (*version.VersionFile, error) {
			return &version.VersionFile{Path: "version.txt", Type: "text", Version: "1.2.0"}, nil
		},
		gitFetchTags:     func(dir string) error { return nil },
		gitTagList:       func(dir string) ([]string, error) { return []string{"v1.2.0"}, nil },
		gitAllSemverTags: func(dir string) ([]string, error) { return []string{"v1.3.0-rc1", "v1.2.0"}, nil },
		gitDefaultBranch: func(dir string) (string, error) { return "main", nil },
		gitTagsAtHead:    func(dir string) ([]string, error) { return nil, nil },
	}

	out, err := prPrepareCoreWith("/mock/root", "/mock/work", PRPrepareIn{SkipConfigCheck: true}, rt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !out.OK {
		t.Fatalf("expected OK=true, got errors: %v", out.Errors)
	}

	// VersionSource
	if out.VersionSource == nil {
		t.Fatal("expected VersionSource to be populated")
	}
	if out.VersionSource.Version != "1.2.0" {
		t.Errorf("VersionSource.Version: got %q, want %q", out.VersionSource.Version, "1.2.0")
	}

	// BumpOptions
	if len(out.BumpOptions) != 3 {
		t.Fatalf("expected 3 BumpOptions, got %d", len(out.BumpOptions))
	}

	// Tags
	if out.Tags == nil {
		t.Fatal("expected Tags to be populated")
	}
	if len(out.Tags.All) == 0 {
		t.Error("expected Tags.All to be non-empty")
	}

	// CommitsSinceTag
	if len(out.CommitsSinceTag) == 0 {
		t.Error("expected CommitsSinceTag to be non-empty")
	}

	// ConventionalSummary
	if out.ConventionalSummary == nil {
		t.Fatal("expected ConventionalSummary to be populated")
	}
	if out.ConventionalSummary.Feat != 1 {
		t.Errorf("ConventionalSummary.Feat: got %d, want 1", out.ConventionalSummary.Feat)
	}
	if out.ConventionalSummary.Fix != 1 {
		t.Errorf("ConventionalSummary.Fix: got %d, want 1", out.ConventionalSummary.Fix)
	}

	// ExistingRCs — seeded with v1.3.0-rc1
	if out.ExistingRCs == nil {
		t.Fatal("expected ExistingRCs to be populated")
	}
	if rcs, ok := out.ExistingRCs["1.3.0"]; !ok || len(rcs) == 0 {
		t.Errorf("expected ExistingRCs[\"1.3.0\"] to contain rc tags, got %v", out.ExistingRCs)
	}

	// VersionConfig
	if out.VersionConfig == nil {
		t.Fatal("expected VersionConfig to be populated")
	}

	// DefaultBranch
	if out.DefaultBranch != "main" {
		t.Errorf("DefaultBranch: got %q, want %q", out.DefaultBranch, "main")
	}
	if out.OnDefaultBranch {
		t.Error("expected OnDefaultBranch=false on feature branch")
	}
}

func TestPrPrepare_CommitsSinceBase_Populated(t *testing.T) {
	rt := prRuntime{
		ghAuthProbe: func(dir, host string) ghx.AuthProbeResult {
			return ghx.AuthProbeResult{Authenticated: true, ActiveAccount: "someone"}
		},
		configReadSection: func(root, section string) (map[string]any, error) { return nil, nil },
		configRead:        func(root string) (*config.Config, error) { return nil, nil },
		execRun: func(name string, args []string, opts execx.Options) (string, error) {
			if name == "git" && len(args) > 0 && args[0] == "log" && slices.Contains(args, "--reverse") {
				return "aaa1111 feat: first commit\nbbb2222 fix: second commit\nccc3333 chore: third commit", nil
			}
			return "", errors.New("fatal: no such remote 'origin'")
		},
		gitCurrentBranch: func(dir string) (string, error) { return "feat/multi-commit", nil },
		gitStatus:        func(dir string) (string, error) { return "", nil },
		gitDefaultBranch: func(dir string) (string, error) { return "main", nil },
		gitHasUpstream:   func(dir string) (bool, error) { return true, nil },
		gitCommitsAhead:  func(dir string) (int, error) { return 0, nil },
		branchValidate:   branch.ValidateExpectedBranch,
		jiraExtract:      func(branchName string) string { return "" },
		templateResolve:  func(root string) (*prtemplate.Template, error) { return nil, nil },
	}

	out, err := prPrepareCoreWith("/mock/root", "/mock/work", PRPrepareIn{SkipConfigCheck: true}, rt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !out.OK {
		t.Fatalf("expected OK=true, got errors: %v", out.Errors)
	}
	want := []string{
		"aaa1111 feat: first commit",
		"bbb2222 fix: second commit",
		"ccc3333 chore: third commit",
	}
	if len(out.CommitsSinceBase) != len(want) {
		t.Fatalf("CommitsSinceBase: got %d entries, want %d (%v)", len(out.CommitsSinceBase), len(want), out.CommitsSinceBase)
	}
	for i, w := range want {
		if out.CommitsSinceBase[i] != w {
			t.Errorf("CommitsSinceBase[%d]: got %q, want %q", i, out.CommitsSinceBase[i], w)
		}
	}
}

func TestPrPrepare_NoVersionConfig_OmitsVersionFields(t *testing.T) {
	rt := prRuntime{
		ghAuthProbe: func(dir, host string) ghx.AuthProbeResult {
			return ghx.AuthProbeResult{Authenticated: true, ActiveAccount: "someone"}
		},
		configReadSection: func(root, section string) (map[string]any, error) { return nil, nil },
		configRead:        func(root string) (*config.Config, error) { return nil, nil },
		execRun: func(name string, args []string, opts execx.Options) (string, error) {
			return "", errors.New("fatal: no such remote 'origin'")
		},
		gitCurrentBranch: func(dir string) (string, error) { return "feat/no-version", nil },
		gitStatus:        func(dir string) (string, error) { return "", nil },
		gitDefaultBranch: func(dir string) (string, error) { return "main", nil },
		gitHasUpstream:   func(dir string) (bool, error) { return true, nil },
		gitCommitsAhead:  func(dir string) (int, error) { return 0, nil },
		branchValidate:   branch.ValidateExpectedBranch,
		jiraExtract:      func(branchName string) string { return "" },
		templateResolve:  func(root string) (*prtemplate.Template, error) { return nil, nil },
	}

	out, err := prPrepareCoreWith("/mock/root", "/mock/work", PRPrepareIn{SkipConfigCheck: true}, rt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !out.OK {
		t.Fatalf("expected OK=true, got errors: %v", out.Errors)
	}

	// All version fields must be nil/zero when no version config exists.
	if out.VersionSource != nil {
		t.Errorf("expected nil VersionSource, got %+v", out.VersionSource)
	}
	if out.BumpOptions != nil {
		t.Errorf("expected nil BumpOptions, got %+v", out.BumpOptions)
	}
	if out.Tags != nil {
		t.Errorf("expected nil Tags, got %+v", out.Tags)
	}
	if out.CommitsSinceTag != nil {
		t.Errorf("expected nil CommitsSinceTag, got %+v", out.CommitsSinceTag)
	}
	if out.ConventionalSummary != nil {
		t.Errorf("expected nil ConventionalSummary, got %+v", out.ConventionalSummary)
	}
	if out.VersionConfig != nil {
		t.Errorf("expected nil VersionConfig, got %+v", out.VersionConfig)
	}
	if out.DefaultBranch != "" {
		t.Errorf("expected empty DefaultBranch, got %q", out.DefaultBranch)
	}
}

func TestPrPrepare_VersionDetectionFails_WarningNotError(t *testing.T) {
	rt := prRuntime{
		ghAuthProbe: func(dir, host string) ghx.AuthProbeResult {
			return ghx.AuthProbeResult{Authenticated: true, ActiveAccount: "someone"}
		},
		configReadSection: func(root, section string) (map[string]any, error) { return nil, nil },
		configRead: func(root string) (*config.Config, error) {
			return &config.Config{
				Version: &config.VersionSection{
					Method:      "semver",
					Tag:         config.VersionTagConfig{Enabled: true, Prefix: "v"},
					VersionFile: config.VersionFileConfig{Enabled: true, Path: "version.txt", FileType: "text"},
				},
			}, nil
		},
		execRun: func(name string, args []string, opts execx.Options) (string, error) {
			return "", errors.New("fatal: no such remote 'origin'")
		},
		gitCurrentBranch: func(dir string) (string, error) { return "feat/broken-version", nil },
		gitStatus:        func(dir string) (string, error) { return "", nil },
		gitHasUpstream:   func(dir string) (bool, error) { return true, nil },
		gitCommitsAhead:  func(dir string) (int, error) { return 0, nil },
		branchValidate:   branch.ValidateExpectedBranch,
		jiraExtract:      func(branchName string) string { return "" },
		templateResolve:  func(root string) (*prtemplate.Template, error) { return nil, nil },
		versionDetect: func(root, path, fileType string) (*version.VersionFile, error) {
			return nil, errors.New("version file not found")
		},
		gitFetchTags:     func(dir string) error { return nil },
		gitTagList:       func(dir string) ([]string, error) { return nil, nil },
		gitAllSemverTags: func(dir string) ([]string, error) { return nil, nil },
		gitDefaultBranch: func(dir string) (string, error) { return "main", nil },
		gitTagsAtHead:    func(dir string) ([]string, error) { return nil, nil },
	}

	out, err := prPrepareCoreWith("/mock/root", "/mock/work", PRPrepareIn{SkipConfigCheck: true}, rt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Must still be OK — version detection failure is a warning, not an error.
	if !out.OK {
		t.Fatalf("expected OK=true despite version detection failure, got errors: %v", out.Errors)
	}
	if len(out.Errors) > 0 {
		t.Errorf("expected no errors, got %v", out.Errors)
	}

	// Must have a warning about the version detection failure.
	found := false
	for _, w := range out.Warnings {
		if strings.Contains(w, "version detection failed") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected a warning containing 'version detection failed', got %v", out.Warnings)
	}

	// Nil slices must be normalized to empty (BumpOptions, CommitsSinceTag
	// are initialized in prVersionDiagnosticsWith even on early return).
	if out.BumpOptions == nil {
		t.Error("expected BumpOptions to be non-nil empty slice")
	}
	if out.CommitsSinceTag == nil {
		t.Error("expected CommitsSinceTag to be non-nil empty slice")
	}
	if out.Tags == nil {
		t.Fatal("expected Tags to be non-nil")
	}
	if out.Tags.All == nil {
		t.Error("expected Tags.All to be non-nil empty slice")
	}
	if out.Tags.AtHead == nil {
		t.Error("expected Tags.AtHead to be non-nil empty slice")
	}
}

// ---------------------------------------------------------------------------
// pr_prepare — NeedsPush
// ---------------------------------------------------------------------------

// prepareNeedsPushRuntime builds a prRuntime that reaches the NeedsPush
// computation (past the config/auth/branch-guard/protected-branch checks),
// with hasUpstream/commitsAhead as the only scenario-varying fields.
func prepareNeedsPushRuntime(hasUpstream func(dir string) (bool, error), commitsAhead func(dir string) (int, error)) prRuntime {
	return prRuntime{
		ghAuthProbe: func(dir, host string) ghx.AuthProbeResult {
			return ghx.AuthProbeResult{Authenticated: true, ActiveAccount: "someone"}
		},
		configReadSection: func(root, section string) (map[string]any, error) { return nil, nil },
		configRead:        func(root string) (*config.Config, error) { return nil, nil },
		execRun: func(name string, args []string, opts execx.Options) (string, error) {
			return "", errors.New("fatal: no such remote 'origin'")
		},
		gitCurrentBranch: func(dir string) (string, error) { return "feat/x", nil },
		gitStatus:        func(dir string) (string, error) { return "", nil },
		gitDefaultBranch: func(dir string) (string, error) { return "main", nil },
		gitHasUpstream:   hasUpstream,
		gitCommitsAhead:  commitsAhead,
		branchValidate:   branch.ValidateExpectedBranch,
		jiraExtract:      func(branchName string) string { return "" },
		templateResolve:  func(root string) (*prtemplate.Template, error) { return nil, nil },
	}
}

func TestPrPrepare_NeedsPush(t *testing.T) {
	t.Run("no upstream configured", func(t *testing.T) {
		rt := prepareNeedsPushRuntime(
			func(dir string) (bool, error) { return false, nil },
			func(dir string) (int, error) {
				t.Fatal("gitCommitsAhead should not be called when there is no upstream")
				return 0, nil
			},
		)
		out, err := prPrepareCoreWith("/mock/root", "/mock/work", PRPrepareIn{SkipConfigCheck: true}, rt)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !out.NeedsPush {
			t.Error("expected NeedsPush=true when no upstream is configured")
		}
	})

	t.Run("upstream exists with commits ahead", func(t *testing.T) {
		rt := prepareNeedsPushRuntime(
			func(dir string) (bool, error) { return true, nil },
			func(dir string) (int, error) { return 3, nil },
		)
		out, err := prPrepareCoreWith("/mock/root", "/mock/work", PRPrepareIn{SkipConfigCheck: true}, rt)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !out.NeedsPush {
			t.Error("expected NeedsPush=true when upstream is behind HEAD")
		}
	})

	t.Run("upstream exists with zero commits ahead", func(t *testing.T) {
		rt := prepareNeedsPushRuntime(
			func(dir string) (bool, error) { return true, nil },
			func(dir string) (int, error) { return 0, nil },
		)
		out, err := prPrepareCoreWith("/mock/root", "/mock/work", PRPrepareIn{SkipConfigCheck: true}, rt)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if out.NeedsPush {
			t.Error("expected NeedsPush=false when upstream is already caught up")
		}
	})

	t.Run("upstream check failure degrades to a warning plus fail-safe NeedsPush", func(t *testing.T) {
		rt := prepareNeedsPushRuntime(
			func(dir string) (bool, error) { return false, errors.New("boom") },
			func(dir string) (int, error) {
				t.Fatal("gitCommitsAhead should not be called after an upstream-check error")
				return 0, nil
			},
		)
		out, err := prPrepareCoreWith("/mock/root", "/mock/work", PRPrepareIn{SkipConfigCheck: true}, rt)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !out.NeedsPush {
			t.Error("expected fail-safe NeedsPush=true after an upstream-check error")
		}
		if !strings.Contains(strings.Join(out.Warnings, " "), "upstream check") {
			t.Errorf("expected a warning mentioning 'upstream check', got %v", out.Warnings)
		}
	})

	t.Run("commits ahead check failure degrades to warning plus fail-safe NeedsPush", func(t *testing.T) {
		rt := prepareNeedsPushRuntime(
			func(dir string) (bool, error) { return true, nil },
			func(dir string) (int, error) { return 0, errors.New("rev-list failed") },
		)
		out, err := prPrepareCoreWith("/mock/root", "/mock/work", PRPrepareIn{SkipConfigCheck: true}, rt)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !out.NeedsPush {
			t.Error("expected NeedsPush=true when commits-ahead check fails (fail-safe)")
		}
	})
}

// ---------------------------------------------------------------------------
// pr_apply
// ---------------------------------------------------------------------------

func TestPrApply_NoExistingPR_Creates(t *testing.T) {
	rt := prRuntime{
		ghPRForBranch: func(dir string) ghx.PRMetadata { return ghx.PRMetadata{Exists: false} },
		ghPRCreate: func(dir, title, body string) (string, error) {
			return "https://github.com/o/r/pull/9", nil
		},
		// SkipReleaseCheck triggers the verification gate (gitLogSinceTag);
		// idle upstream means the push-decision block never calls
		// gitPushSetUpstream.
		gitLogSinceTag:     func(dir string) ([]string, error) { return nil, nil },
		gitHasUpstream:     func(dir string) (bool, error) { return true, nil },
		gitCommitsAhead:    func(dir string) (int, error) { return 0, nil },
		gitPushSetUpstream: func(dir, remote string) error { return nil },
	}

	out, err := prApplyCoreWith("/mock/root", "/mock/work", PRApplyIn{Title: "Add thing", Body: "Body text", SkipReleaseCheck: true}, rt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !out.Created {
		t.Errorf("expected Created=true, got %+v", out)
	}
	if out.URL != "https://github.com/o/r/pull/9" {
		t.Errorf("URL: got %q", out.URL)
	}
	if !strings.Contains(out.Next, "PR created") {
		t.Errorf("Next: got %q", out.Next)
	}
}

func TestPrApply_ExistingPR_Updates(t *testing.T) {
	rt := prRuntime{
		ghPRForBranch: func(dir string) ghx.PRMetadata {
			return ghx.PRMetadata{Number: 9, Title: "old", URL: "https://github.com/o/r/pull/9", State: "OPEN", Exists: true}
		},
		ghPREdit: func(dir string, num int, title, body string) (string, error) {
			return "https://github.com/o/r/pull/9", nil
		},
		gitLogSinceTag:     func(dir string) ([]string, error) { return nil, nil },
		gitHasUpstream:     func(dir string) (bool, error) { return true, nil },
		gitCommitsAhead:    func(dir string) (int, error) { return 0, nil },
		gitPushSetUpstream: func(dir, remote string) error { return nil },
	}

	out, err := prApplyCoreWith("/mock/root", "/mock/work", PRApplyIn{Title: "Updated title", Body: "Body text", SkipReleaseCheck: true}, rt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Created {
		t.Errorf("expected Created=false (update path), got %+v", out)
	}
	if out.URL != "https://github.com/o/r/pull/9" {
		t.Errorf("URL: got %q", out.URL)
	}
	if !strings.Contains(out.Next, "PR updated") {
		t.Errorf("Next: got %q", out.Next)
	}
}

func TestPrApply_MissingTitle_DomainError(t *testing.T) {
	// The empty-title check is the very first thing prApplyCoreWith does —
	// no rt field is ever invoked, so defaultPRRuntime (via prApplyCore) is
	// safe to use here with fake, never-touched root/work paths.
	_, err := prApplyCore("/mock/root", "/mock/work", PRApplyIn{Title: "  ", Body: "x"})
	if err == nil {
		t.Fatal("expected an error for empty title")
	}
}

// TestPrApply_NoReleaseLevel_NoSkip_DomainError (task 2) — the release-intent
// gate rejects an empty ReleaseLevel unless SkipReleaseCheck is set. Runs
// through defaultPRRuntime (via prApplyCore): the gate short-circuits before
// any rt.* call, same reasoning as TestPrApply_MissingTitle_DomainError above.
func TestPrApply_NoReleaseLevel_NoSkip_DomainError(t *testing.T) {
	_, err := prApplyCore("/mock/root", "/mock/work", PRApplyIn{Title: "T", Body: "B"})
	if err == nil {
		t.Fatal("expected an error when releaseLevel is empty and skipReleaseCheck is false")
	}
	var de *mcpserver.DomainError
	if !errors.As(err, &de) {
		t.Fatalf("expected *mcpserver.DomainError, got %T: %v", err, err)
	}
	if !strings.Contains(de.Msg, "releaseLevel is empty") {
		t.Errorf("Msg: got %q, want it to reference releaseLevel being empty", de.Msg)
	}
	if !strings.Contains(de.Suggestion, "releaseLevel") {
		t.Errorf("Suggestion missing releaseLevel hint: %q", de.Suggestion)
	}
	if !strings.Contains(de.Suggestion, "skipReleaseCheck: true") {
		t.Errorf("Suggestion missing skipReleaseCheck hint: %q", de.Suggestion)
	}
}

// TestPrApply_SkipReleaseCheck_Verification covers the skipReleaseCheck
// verification gate: the flag alone cannot authorize skipping a release when
// commits since the last tag are release-worthy (feat/fix/breaking) — it is
// verified against rt.gitLogSinceTag + analyzeConventionalCommits.
func TestPrApply_SkipReleaseCheck_Verification(t *testing.T) {
	t.Run("autoMode hard-rejects the skip when release-worthy commits exist", func(t *testing.T) {
		rt := releaseTestRuntime("1.0.0")
		rt.gitLogSinceTag = func(dir string) ([]string, error) {
			return []string{"aaa1111 feat: add widget"}, nil
		}

		_, err := prApplyCoreWith("/mock/root", "/mock/work", PRApplyIn{
			Title: "T", Body: "B", SkipReleaseCheck: true, AutoMode: true,
		}, rt)
		var de *mcpserver.DomainError
		if !errors.As(err, &de) {
			t.Fatalf("expected *mcpserver.DomainError, got %T: %v", err, err)
		}
		if !strings.Contains(de.Msg, "not allowed unattended") {
			t.Errorf("Msg: got %q", de.Msg)
		}
	})

	t.Run("interactive mode requires a non-empty skipReleaseReason", func(t *testing.T) {
		rt := releaseTestRuntime("1.0.0")
		rt.gitLogSinceTag = func(dir string) ([]string, error) {
			return []string{"aaa1111 fix: correct bug"}, nil
		}

		_, err := prApplyCoreWith("/mock/root", "/mock/work", PRApplyIn{
			Title: "T", Body: "B", SkipReleaseCheck: true,
		}, rt)
		var de *mcpserver.DomainError
		if !errors.As(err, &de) {
			t.Fatalf("expected *mcpserver.DomainError, got %T: %v", err, err)
		}
		if !strings.Contains(de.Msg, "skipReleaseReason") {
			t.Errorf("Msg: got %q", de.Msg)
		}
	})

	t.Run("interactive mode with a skipReleaseReason passes despite release-worthy commits", func(t *testing.T) {
		rt := releaseTestRuntime("1.0.0")
		rt.gitLogSinceTag = func(dir string) ([]string, error) {
			return []string{"aaa1111 feat: add widget"}, nil
		}
		rt.ghPRCreate = func(dir, title, body string) (string, error) {
			return "https://github.com/o/r/pull/30", nil
		}

		_, err := prApplyCoreWith("/mock/root", "/mock/work", PRApplyIn{
			Title: "T", Body: "B", SkipReleaseCheck: true,
			SkipReleaseReason: "docs-only follow-up, release tracked separately",
		}, rt)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("commit history classified purely as other passes silently", func(t *testing.T) {
		rt := releaseTestRuntime("1.0.0")
		rt.gitLogSinceTag = func(dir string) ([]string, error) {
			return []string{"aaa1111 chore: tidy up"}, nil
		}
		rt.ghPRCreate = func(dir, title, body string) (string, error) {
			return "https://github.com/o/r/pull/31", nil
		}

		_, err := prApplyCoreWith("/mock/root", "/mock/work", PRApplyIn{
			Title: "T", Body: "B", SkipReleaseCheck: true,
		}, rt)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("gitLogSinceTag failure surfaces as InfraError", func(t *testing.T) {
		rt := releaseTestRuntime("1.0.0")
		rt.gitLogSinceTag = func(dir string) ([]string, error) { return nil, errors.New("boom") }

		_, err := prApplyCoreWith("/mock/root", "/mock/work", PRApplyIn{
			Title: "T", Body: "B", SkipReleaseCheck: true,
		}, rt)
		var ie *mcpserver.InfraError
		if !errors.As(err, &ie) {
			t.Fatalf("expected *mcpserver.InfraError, got %T: %v", err, err)
		}
	})
}

// TestPrApply_GitLogSinceTagError_SinglePrefix runs the production
// prGitLogSinceTag against a directory that does not exist, so git fails
// before touching any repository. Both callers in prApplyCoreWith add the
// "gitLogSinceTag: " prefix, so the helper's own error must not carry it
// again.
func TestPrApply_GitLogSinceTagError_SinglePrefix(t *testing.T) {
	missingDir := filepath.Join(t.TempDir(), "does-not-exist")
	cases := []struct {
		name string
		in   PRApplyIn
	}{
		{"skipReleaseCheck verification", PRApplyIn{Title: "T", Body: "B", SkipReleaseCheck: true}},
		{"release notes auto-generation", PRApplyIn{Title: "T", Body: "B", ReleaseLevel: "patch", ReleaseSource: "user"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rt := releaseTestRuntime("1.0.0")
			rt.gitLogSinceTag = prGitLogSinceTag

			_, err := prApplyCoreWith("/mock/root", missingDir, tc.in, rt)
			var ie *mcpserver.InfraError
			if !errors.As(err, &ie) {
				t.Fatalf("expected *mcpserver.InfraError, got %T: %v", err, err)
			}
			if n := strings.Count(ie.Msg, "gitLogSinceTag:"); n != 1 {
				t.Errorf("Msg carries the gitLogSinceTag prefix %d times, want 1: %q", n, ie.Msg)
			}
			if n := strings.Count(ie.Msg, "tag list:"); n != 1 {
				t.Errorf("Msg carries the tag list prefix %d times, want 1: %q", n, ie.Msg)
			}
		})
	}
}

// TestPrApply_Push covers the push-decision block: pr_apply pushes the
// current branch before create/edit when there is no upstream, or when the
// upstream exists but HEAD has moved ahead of it; it skips the push when the
// upstream is already caught up, and surfaces upstream/commits-ahead/push
// failures as InfraError.
func TestPrApply_Push(t *testing.T) {
	t.Run("no upstream configured triggers a push", func(t *testing.T) {
		var pushed bool
		rt := releaseTestRuntime("1.0.0")
		rt.execRun = mockAddLabelExec("release:patch")
		rt.gitHasUpstream = func(dir string) (bool, error) { return false, nil }
		rt.gitCommitsAhead = func(dir string) (int, error) {
			t.Fatal("gitCommitsAhead should not be called when there is no upstream")
			return 0, nil
		}
		rt.gitPushSetUpstream = func(dir, remote string) error {
			pushed = true
			if remote != "origin" {
				t.Errorf("remote: got %q, want %q", remote, "origin")
			}
			return nil
		}

		_, err := prApplyCoreWith("/mock/root", "/mock/work", PRApplyIn{
			Title: "T", Body: "B", ReleaseLevel: "patch", ReleaseNotes: "notes", ReleaseSource: "user",
		}, rt)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !pushed {
			t.Error("expected gitPushSetUpstream to be called when no upstream is configured")
		}
	})

	t.Run("upstream behind HEAD triggers a push", func(t *testing.T) {
		var pushed bool
		rt := releaseTestRuntime("1.0.0")
		rt.execRun = mockAddLabelExec("release:patch")
		rt.gitHasUpstream = func(dir string) (bool, error) { return true, nil }
		rt.gitCommitsAhead = func(dir string) (int, error) { return 2, nil }
		rt.gitPushSetUpstream = func(dir, remote string) error { pushed = true; return nil }

		_, err := prApplyCoreWith("/mock/root", "/mock/work", PRApplyIn{
			Title: "T", Body: "B", ReleaseLevel: "patch", ReleaseNotes: "notes", ReleaseSource: "user",
		}, rt)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !pushed {
			t.Error("expected gitPushSetUpstream to be called when upstream is behind HEAD")
		}
	})

	t.Run("upstream already caught up skips the push", func(t *testing.T) {
		rt := releaseTestRuntime("1.0.0")
		rt.execRun = mockAddLabelExec("release:patch")
		rt.gitHasUpstream = func(dir string) (bool, error) { return true, nil }
		rt.gitCommitsAhead = func(dir string) (int, error) { return 0, nil }
		rt.gitPushSetUpstream = func(dir, remote string) error {
			t.Fatal("gitPushSetUpstream should not be called when upstream has 0 commits ahead")
			return nil
		}

		_, err := prApplyCoreWith("/mock/root", "/mock/work", PRApplyIn{
			Title: "T", Body: "B", ReleaseLevel: "patch", ReleaseNotes: "notes", ReleaseSource: "user",
		}, rt)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("push failure surfaces as InfraError", func(t *testing.T) {
		rt := releaseTestRuntime("1.0.0")
		rt.execRun = mockAddLabelExec("release:patch")
		rt.gitHasUpstream = func(dir string) (bool, error) { return false, nil }
		rt.gitPushSetUpstream = func(dir, remote string) error { return errors.New("boom") }

		_, err := prApplyCoreWith("/mock/root", "/mock/work", PRApplyIn{
			Title: "T", Body: "B", ReleaseLevel: "patch", ReleaseNotes: "notes", ReleaseSource: "user",
		}, rt)
		var ie *mcpserver.InfraError
		if !errors.As(err, &ie) {
			t.Fatalf("expected *mcpserver.InfraError, got %T: %v", err, err)
		}
	})

	t.Run("upstream-check failure surfaces as InfraError", func(t *testing.T) {
		rt := releaseTestRuntime("1.0.0")
		rt.execRun = mockAddLabelExec("release:patch")
		rt.gitHasUpstream = func(dir string) (bool, error) { return false, errors.New("boom") }

		_, err := prApplyCoreWith("/mock/root", "/mock/work", PRApplyIn{
			Title: "T", Body: "B", ReleaseLevel: "patch", ReleaseNotes: "notes", ReleaseSource: "user",
		}, rt)
		var ie *mcpserver.InfraError
		if !errors.As(err, &ie) {
			t.Fatalf("expected *mcpserver.InfraError, got %T: %v", err, err)
		}
	})

	t.Run("commits-ahead check failure surfaces as InfraError", func(t *testing.T) {
		rt := releaseTestRuntime("1.0.0")
		rt.execRun = mockAddLabelExec("release:patch")
		rt.gitHasUpstream = func(dir string) (bool, error) { return true, nil }
		rt.gitCommitsAhead = func(dir string) (int, error) { return 0, errors.New("boom") }

		_, err := prApplyCoreWith("/mock/root", "/mock/work", PRApplyIn{
			Title: "T", Body: "B", ReleaseLevel: "patch", ReleaseNotes: "notes", ReleaseSource: "user",
		}, rt)
		var ie *mcpserver.InfraError
		if !errors.As(err, &ie) {
			t.Fatalf("expected *mcpserver.InfraError, got %T: %v", err, err)
		}
	})
}

// ---------------------------------------------------------------------------
// prEnrichPermissionError (task 1) — auth-enriched permission errors from
// ghPRCreate/ghPREdit.
// ---------------------------------------------------------------------------

// originRemoteExec returns an execRun stub that answers `git remote get-url
// origin` with remoteURL and fails any other call.
func originRemoteExec(remoteURL string) func(name string, args []string, opts execx.Options) (string, error) {
	return func(name string, args []string, opts execx.Options) (string, error) {
		if name == "git" && len(args) == 3 && args[0] == "remote" && args[1] == "get-url" && args[2] == "origin" {
			return remoteURL, nil
		}
		return "", fmt.Errorf("unexpected exec call: %s %v", name, args)
	}
}

func TestPrApply_PermissionError_EnrichedWithAuthHints(t *testing.T) {
	rt := prRuntime{
		ghPRForBranch: func(dir string) ghx.PRMetadata { return ghx.PRMetadata{Exists: false} },
		ghPRCreate: func(dir, title, body string) (string, error) {
			return "", errors.New("HTTP 403: Must be a collaborator to create pull requests")
		},
		execRun: originRemoteExec("https://github.com/acme/widgets.git"),
		ghGetAccounts: func(dir, host string) ([]ghx.Account, error) {
			return []ghx.Account{{Login: "other-user", Active: false}}, nil
		},
		ghAuthProbe: func(dir, host string) ghx.AuthProbeResult {
			return ghx.AuthProbeResult{Authenticated: true, ActiveAccount: "me"}
		},
		gitLogSinceTag:     func(dir string) ([]string, error) { return nil, nil },
		gitHasUpstream:     func(dir string) (bool, error) { return true, nil },
		gitCommitsAhead:    func(dir string) (int, error) { return 0, nil },
		gitPushSetUpstream: func(dir, remote string) error { return nil },
	}

	_, err := prApplyCoreWith("/mock/root", "/mock/work", PRApplyIn{Title: "Add thing", Body: "Body text", SkipReleaseCheck: true}, rt)
	if err == nil {
		t.Fatal("expected an error")
	}
	var ie *mcpserver.InfraError
	if !errors.As(err, &ie) {
		t.Fatalf("expected *mcpserver.InfraError, got %T: %v", err, err)
	}
	if !strings.Contains(ie.Msg, "gh pr create:") {
		t.Errorf("Msg: got %q, want it to reference gh pr create", ie.Msg)
	}
	if !strings.Contains(ie.Suggestion, "gh auth switch --user other-user") {
		t.Errorf("Suggestion missing switch hint: %q", ie.Suggestion)
	}
	if !strings.Contains(ie.Suggestion, "acme/widgets") {
		t.Errorf("Suggestion missing owner/repo: %q", ie.Suggestion)
	}
	if !strings.Contains(ie.Suggestion, "call pr_apply again with the same arguments") {
		t.Errorf("Suggestion missing retry instruction: %q", ie.Suggestion)
	}
}

func TestPrApply_PermissionError_FromEdit_EnrichedSameWay(t *testing.T) {
	rt := prRuntime{
		ghPRForBranch: func(dir string) ghx.PRMetadata {
			return ghx.PRMetadata{Exists: true, Number: 9, URL: "https://github.com/acme/widgets/pull/9"}
		},
		ghPREdit: func(dir string, num int, title, body string) (string, error) {
			return "", errors.New("HTTP 403: Resource not accessible by integration")
		},
		execRun: originRemoteExec("git@github.com:acme/widgets.git"),
		ghGetAccounts: func(dir, host string) ([]ghx.Account, error) {
			return []ghx.Account{{Login: "other-user", Active: false}}, nil
		},
		ghAuthProbe: func(dir, host string) ghx.AuthProbeResult {
			return ghx.AuthProbeResult{Authenticated: true, ActiveAccount: "me"}
		},
		gitLogSinceTag:     func(dir string) ([]string, error) { return nil, nil },
		gitHasUpstream:     func(dir string) (bool, error) { return true, nil },
		gitCommitsAhead:    func(dir string) (int, error) { return 0, nil },
		gitPushSetUpstream: func(dir, remote string) error { return nil },
	}

	_, err := prApplyCoreWith("/mock/root", "/mock/work", PRApplyIn{Title: "Updated title", Body: "Body text", SkipReleaseCheck: true}, rt)
	if err == nil {
		t.Fatal("expected an error")
	}
	var ie *mcpserver.InfraError
	if !errors.As(err, &ie) {
		t.Fatalf("expected *mcpserver.InfraError, got %T: %v", err, err)
	}
	if !strings.Contains(ie.Msg, "gh pr edit:") {
		t.Errorf("Msg: got %q, want it to reference gh pr edit", ie.Msg)
	}
	if !strings.Contains(ie.Suggestion, "gh auth switch --user other-user") {
		t.Errorf("Suggestion missing switch hint: %q", ie.Suggestion)
	}
}

func TestPrApply_NonPermissionError_PassesThroughUnenriched(t *testing.T) {
	rt := prRuntime{
		ghPRForBranch: func(dir string) ghx.PRMetadata { return ghx.PRMetadata{Exists: false} },
		ghPRCreate: func(dir, title, body string) (string, error) {
			return "", errors.New("connection reset by peer")
		},
		// execRun/ghGetAccounts/ghAuthProbe are deliberately left nil: since
		// isPermissionError short-circuits before any of them would be
		// called, a nil-func panic here would itself prove enrichment ran
		// where it shouldn't have.
		gitLogSinceTag:     func(dir string) ([]string, error) { return nil, nil },
		gitHasUpstream:     func(dir string) (bool, error) { return true, nil },
		gitCommitsAhead:    func(dir string) (int, error) { return 0, nil },
		gitPushSetUpstream: func(dir, remote string) error { return nil },
	}

	_, err := prApplyCoreWith("/mock/root", "/mock/work", PRApplyIn{Title: "Add thing", Body: "Body text", SkipReleaseCheck: true}, rt)
	if err == nil {
		t.Fatal("expected an error")
	}
	var ie *mcpserver.InfraError
	if !errors.As(err, &ie) {
		t.Fatalf("expected *mcpserver.InfraError, got %T: %v", err, err)
	}
	if ie.Suggestion != prGHRetrySuggestion {
		t.Errorf("Suggestion: got %q, want the generic retry hint (no account-switch guidance)", ie.Suggestion)
	}
	if !strings.Contains(ie.Msg, "connection reset by peer") {
		t.Errorf("Msg: got %q, want original error text preserved", ie.Msg)
	}
}

func TestPrApply_NonPermissionError_FromEdit_GenericSuggestion(t *testing.T) {
	rt := prRuntime{
		ghPRForBranch: func(dir string) ghx.PRMetadata {
			return ghx.PRMetadata{Exists: true, Number: 9, URL: "https://github.com/acme/widgets/pull/9"}
		},
		ghPREdit: func(dir string, num int, title, body string) (string, error) {
			return "", errors.New("connection reset by peer")
		},
		// execRun/ghGetAccounts/ghAuthProbe left nil on purpose — see
		// TestPrApply_NonPermissionError_PassesThroughUnenriched.
		gitLogSinceTag:     func(dir string) ([]string, error) { return nil, nil },
		gitHasUpstream:     func(dir string) (bool, error) { return true, nil },
		gitCommitsAhead:    func(dir string) (int, error) { return 0, nil },
		gitPushSetUpstream: func(dir, remote string) error { return nil },
	}

	_, err := prApplyCoreWith("/mock/root", "/mock/work", PRApplyIn{Title: "T", Body: "B", SkipReleaseCheck: true}, rt)
	var ie *mcpserver.InfraError
	if !errors.As(err, &ie) {
		t.Fatalf("expected *mcpserver.InfraError, got %T: %v", err, err)
	}
	if !strings.HasPrefix(ie.Msg, "gh pr edit: ") {
		t.Errorf("Msg: got %q, want it to start with gh pr edit", ie.Msg)
	}
	if ie.Suggestion != prGHRetrySuggestion {
		t.Errorf("Suggestion: got %q, want the generic retry hint", ie.Suggestion)
	}
}

func TestPrApply_PermissionError_NoOriginRemote_FallsBackToGeneric(t *testing.T) {
	rt := prRuntime{
		ghPRForBranch: func(dir string) ghx.PRMetadata { return ghx.PRMetadata{Exists: false} },
		ghPRCreate: func(dir, title, body string) (string, error) {
			return "", errors.New("HTTP 403: must be a collaborator")
		},
		execRun: func(name string, args []string, opts execx.Options) (string, error) {
			return "", errors.New("fatal: no such remote 'origin'")
		},
		gitLogSinceTag:     func(dir string) ([]string, error) { return nil, nil },
		gitHasUpstream:     func(dir string) (bool, error) { return true, nil },
		gitCommitsAhead:    func(dir string) (int, error) { return 0, nil },
		gitPushSetUpstream: func(dir, remote string) error { return nil },
	}

	_, err := prApplyCoreWith("/mock/root", "/mock/work", PRApplyIn{Title: "Add thing", Body: "Body text", SkipReleaseCheck: true}, rt)
	if err == nil {
		t.Fatal("expected an error")
	}
	var ie *mcpserver.InfraError
	if !errors.As(err, &ie) {
		t.Fatalf("expected *mcpserver.InfraError, got %T: %v", err, err)
	}
	if ie.Suggestion != prGHRetrySuggestion {
		t.Errorf("Suggestion: got %q, want the generic retry hint when enrichment cannot resolve a remote", ie.Suggestion)
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
		ghLabelList:      func(dir string) ([]string, error) { return nil, nil },
		ghLabelCreate:    func(dir, name, color, desc string) error { return nil },
		ghPRForBranch:    func(dir string) ghx.PRMetadata { return ghx.PRMetadata{Exists: false} },
		ghPRCreate:       func(dir, title, body string) (string, error) { return "https://example.com/pull/1", nil },
		execRun:          func(name string, args []string, opts execx.Options) (string, error) { return "", nil },
		// Same idle defaults as releaseTestRuntime — see its comment.
		gitHasUpstream:     func(dir string) (bool, error) { return true, nil },
		gitCommitsAhead:    func(dir string) (int, error) { return 0, nil },
		gitPushSetUpstream: func(dir, remote string) error { return nil },
		gitLogSinceTag:     func(dir string) ([]string, error) { return nil, nil },
	}
}

func TestReleaseSourceValidation(t *testing.T) {
	t.Run("missing releaseSource with releaseLevel set is rejected", func(t *testing.T) {
		_, err := prApplyCore("", "", PRApplyIn{Title: "T", Body: "B", ReleaseLevel: "patch", ReleaseNotes: "notes"})
		if err == nil {
			t.Fatal("expected an error for missing releaseSource")
		}
		if !strings.Contains(err.Error(), "releaseSource") {
			t.Errorf("error should mention releaseSource, got: %v", err)
		}
	})

	t.Run("invalid releaseSource value is rejected", func(t *testing.T) {
		_, err := prApplyCore("", "", PRApplyIn{Title: "T", Body: "B", ReleaseLevel: "patch", ReleaseNotes: "notes", ReleaseSource: "llm"})
		if err == nil {
			t.Fatal("expected an error for an invalid releaseSource value")
		}
		if !strings.Contains(err.Error(), "releaseSource") {
			t.Errorf("error should mention releaseSource, got: %v", err)
		}
	})

	t.Run("auto mode rejects releaseSource=user", func(t *testing.T) {
		_, err := prApplyCore("", "", PRApplyIn{
			Title: "T", Body: "B", ReleaseLevel: "patch", ReleaseNotes: "notes", ReleaseSource: "user", AutoMode: true,
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
		out, err := prApplyCoreWith("", "", PRApplyIn{Title: "T", Body: "B", AutoMode: true, SkipReleaseCheck: true}, rt)
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
			Title: "T", Body: "B", ReleaseLevel: "patch", ReleaseNotes: "notes", ReleaseSource: "user",
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
			Title: "T", Body: "B", ReleaseLevel: "minor", ReleaseNotes: "notes", ReleaseSource: "config", AutoMode: true,
		}, rt)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if out.ReleaseIntent == nil {
			t.Fatal("expected ReleaseIntent to be populated")
		}
	})

	t.Run("releaseSource=pipeline is rejected as an invalid enum value", func(t *testing.T) {
		_, err := prApplyCore("", "", PRApplyIn{
			Title: "T", Body: "B", ReleaseLevel: "major", ReleaseNotes: "notes", ReleaseSource: "pipeline", AutoMode: true,
		})
		if err == nil {
			t.Fatal("expected an error for releaseSource=pipeline")
		}
		if !strings.Contains(err.Error(), "releaseSource") {
			t.Errorf("error should mention releaseSource, got: %v", err)
		}
	})
}

// TestPrApply_EmptyReleaseNotes_AutoGenerated covers the release-notes
// auto-generation path: releaseLevel set with empty (or whitespace-only)
// releaseNotes is no longer rejected — notes are auto-generated from commits
// since the last release tag (generateReleaseNotes) and folded into the PR
// body, since this is tool-authoritative content derived deterministically
// from git history, not something the calling LLM needs to draft.
func TestPrApply_EmptyReleaseNotes_AutoGenerated(t *testing.T) {
	t.Run("empty releaseNotes is auto-generated from commit history", func(t *testing.T) {
		var capturedBody string
		rt := releaseTestRuntime("1.0.0")
		rt.gitLogSinceTag = func(dir string) ([]string, error) {
			return []string{
				"aaa1111 feat: add widget",
				"bbb2222 fix: correct bug",
			}, nil
		}
		rt.ghPRCreate = func(dir, title, body string) (string, error) {
			capturedBody = body
			return "https://github.com/o/r/pull/20", nil
		}
		rt.execRun = mockAddLabelExec("release:patch")

		out, err := prApplyCoreWith("/mock/root", "/mock/work", PRApplyIn{
			Title: "T", Body: "B", ReleaseLevel: "patch", ReleaseNotes: "", ReleaseSource: "user",
		}, rt)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if out.ReleaseIntent == nil || !out.ReleaseIntent.NotesInBody {
			t.Fatal("expected auto-generated release notes to land in the body")
		}
		if !strings.Contains(capturedBody, "add widget") || !strings.Contains(capturedBody, "correct bug") {
			t.Errorf("body missing auto-generated release notes content, got: %s", capturedBody)
		}
	})

	t.Run("whitespace-only releaseNotes is treated as empty and auto-generated", func(t *testing.T) {
		rt := releaseTestRuntime("1.0.0")
		rt.gitLogSinceTag = func(dir string) ([]string, error) { return nil, nil }
		rt.ghPRCreate = func(dir, title, body string) (string, error) {
			return "https://github.com/o/r/pull/21", nil
		}
		rt.execRun = mockAddLabelExec("release:patch")

		out, err := prApplyCoreWith("/mock/root", "/mock/work", PRApplyIn{
			Title: "T", Body: "B", ReleaseLevel: "patch", ReleaseNotes: "   \n\t", ReleaseSource: "user",
		}, rt)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if out.ReleaseIntent == nil || !out.ReleaseIntent.NotesInBody {
			t.Fatal("expected auto-generated notes to land in the body even for whitespace-only input")
		}
	})
}

// ---------------------------------------------------------------------------
// prCommitGroups classification
// ---------------------------------------------------------------------------

func TestPrCommitGroups(t *testing.T) {
	t.Run("bang-colon marks commit as breaking", func(t *testing.T) {
		breaking, feat, fix, other := prCommitGroups([]string{"abc123 feat!: drop legacy API"})
		if len(breaking) != 1 || breaking[0] != "feat!: drop legacy API" {
			t.Errorf("expected breaking=[feat!: drop legacy API], got breaking=%v feat=%v fix=%v other=%v", breaking, feat, fix, other)
		}
	})

	t.Run("BREAKING CHANGE in subject marks commit as breaking", func(t *testing.T) {
		breaking, feat, fix, other := prCommitGroups([]string{"def456 refactor: BREAKING CHANGE in auth module"})
		if len(breaking) != 1 || breaking[0] != "refactor: BREAKING CHANGE in auth module" {
			t.Errorf("expected breaking=[refactor: BREAKING CHANGE in auth module], got breaking=%v feat=%v fix=%v other=%v", breaking, feat, fix, other)
		}
	})

	t.Run("normal fix is not breaking", func(t *testing.T) {
		breaking, feat, fix, other := prCommitGroups([]string{"aaa111 fix: normal bugfix"})
		if len(breaking) != 0 {
			t.Errorf("expected no breaking commits, got %v", breaking)
		}
		if len(fix) != 1 || fix[0] != "fix: normal bugfix" {
			t.Errorf("expected fix=[fix: normal bugfix], got fix=%v feat=%v other=%v", fix, feat, other)
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
// pr_apply release intent tests — all prRuntime mocks, no FS/gh involved.
// ---------------------------------------------------------------------------

func TestPRApply_WithRelease_LabelAdded(t *testing.T) {
	// Create path: no existing PR. Expect --add-label release:minor called;
	// mockAddLabelExec fails the test (via a returned error surfacing as an
	// InfraError) if any other label/args combination is issued.
	rt := releaseTestRuntime("1.2.0")
	rt.ghPRCreate = func(dir, title, body string) (string, error) {
		return "https://github.com/o/r/pull/10", nil
	}
	rt.execRun = mockAddLabelExec("release:minor")

	out, err := prApplyCoreWith("/mock/root", "/mock/work", PRApplyIn{
		Title:         "Release label test",
		Body:          "Some body",
		ReleaseLevel:  "minor",
		ReleaseNotes:  "Release notes for the label test.",
		ReleaseSource: "user",
	}, rt)
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
	var capturedBody string
	rt := releaseTestRuntime("2.0.0")
	rt.ghPRCreate = func(dir, title, body string) (string, error) {
		capturedBody = body
		return "https://github.com/o/r/pull/11", nil
	}
	rt.execRun = mockAddLabelExec("release:patch")

	out, err := prApplyCoreWith("/mock/root", "/mock/work", PRApplyIn{
		Title:         "Notes test",
		Body:          "Original body",
		ReleaseLevel:  "patch",
		ReleaseNotes:  "Fixed the bug in auth module.",
		ReleaseSource: "user",
	}, rt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.ReleaseIntent == nil || !out.ReleaseIntent.NotesInBody {
		t.Fatal("expected NotesInBody=true")
	}
	// Verify release markers are present in the body passed to gh pr create.
	if !strings.Contains(capturedBody, "<!-- release-notes-start -->") {
		t.Errorf("body missing release-notes-start marker")
	}
	if !strings.Contains(capturedBody, "<!-- release-level:patch -->") {
		t.Errorf("body missing release-level marker")
	}
	if !strings.Contains(capturedBody, "Fixed the bug in auth module.") {
		t.Errorf("body missing release notes text")
	}
	if !strings.Contains(capturedBody, "## [Unreleased]") {
		t.Errorf("body missing version-agnostic header, got: %s", capturedBody)
	}
	if strings.Contains(capturedBody, "2.0.1") {
		t.Errorf("body must not pin a version, got: %s", capturedBody)
	}
}

// TestPRApply_WithRelease_NoVersionInBody proves pr_apply no longer computes
// or pins a version number in the PR body even when tags exist that a
// version-computing implementation would have bumped from/collided with.
func TestPRApply_WithRelease_NoVersionInBody(t *testing.T) {
	var capturedBody string
	rt := releaseTestRuntime("1.4.9")
	rt.gitTagList = func(dir string) ([]string, error) { return []string{"v1.4.9"}, nil }
	rt.gitAllSemverTags = func(dir string) ([]string, error) { return []string{"v1.4.10-rc1", "v1.4.9"}, nil }
	rt.ghPRCreate = func(dir, title, body string) (string, error) {
		capturedBody = body
		return "https://github.com/o/r/pull/12", nil
	}
	rt.execRun = mockAddLabelExec("release:patch-rc")

	out, err := prApplyCoreWith("/mock/root", "/mock/work", PRApplyIn{
		Title:             "No version in body test",
		Body:              "body",
		ReleaseLevel:      "patch",
		ReleaseNotes:      "No version in body release notes.",
		ReleasePreRelease: "rc",
		ReleaseSource:     "user",
	}, rt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.ReleaseIntent == nil {
		t.Fatal("expected ReleaseIntent")
	}
	if !strings.Contains(capturedBody, "## [Unreleased]") {
		t.Errorf("body missing version-agnostic header, got: %s", capturedBody)
	}
	if strings.Contains(capturedBody, "1.4.10") {
		t.Errorf("body must not pin a version, got: %s", capturedBody)
	}
	if strings.Contains(capturedBody, "-rc2") {
		t.Errorf("body must not pin an RC number, got: %s", capturedBody)
	}
}

// TestPRApply_WithRelease_ExistingTagDoesNotBlock proves an existing tag for
// the old would-be computed version no longer blocks pr_apply: prReleaseIntent
// is pure and never reads tags. Version-collision detection now happens in CI
// (verify-release-intent.cjs) at merge time, not here.
func TestPRApply_WithRelease_ExistingTagDoesNotBlock(t *testing.T) {
	rt := releaseTestRuntime("1.2.0")
	rt.configRead = func(root string) (*config.Config, error) {
		return &config.Config{Version: &config.VersionSection{
			Tag:         config.VersionTagConfig{Enabled: true, Prefix: "rel-"},
			VersionFile: config.VersionFileConfig{Enabled: true},
		}}, nil
	}
	rt.gitTagList = func(dir string) ([]string, error) { return []string{"rel-1.3.0"}, nil }
	rt.ghPRCreate = func(dir, title, body string) (string, error) {
		return "https://github.com/o/r/pull/13", nil
	}
	rt.execRun = mockAddLabelExec("release:minor")

	_, err := prApplyCoreWith("/mock/root", "/mock/work", PRApplyIn{
		Title:         "Existing tag test",
		Body:          "body",
		ReleaseLevel:  "minor",
		ReleaseNotes:  "Existing tag test release notes.",
		ReleaseSource: "user",
	}, rt)
	if err != nil {
		t.Fatalf("expected no error despite an existing tag, got: %v", err)
	}
}

// TestPRApply_WithRelease_VersionDetectErrorDoesNotBlock proves a broken or
// missing version file no longer blocks pr_apply: prReleaseIntent never
// reads the version file. Version resolution now happens in CI at merge
// time, not here.
func TestPRApply_WithRelease_VersionDetectErrorDoesNotBlock(t *testing.T) {
	rt := releaseTestRuntime("")
	rt.configRead = func(root string) (*config.Config, error) {
		return &config.Config{Version: &config.VersionSection{
			VersionFile: config.VersionFileConfig{Enabled: true, Path: "version.txt", FileType: "text"},
		}}, nil
	}
	rt.versionDetect = func(root, path, fileType string) (*version.VersionFile, error) {
		return nil, errors.New("version file not found")
	}
	rt.ghPRCreate = func(dir, title, body string) (string, error) {
		return "https://github.com/o/r/pull/14", nil
	}
	rt.execRun = mockAddLabelExec("release:patch")

	_, err := prApplyCoreWith("/mock/root", "/mock/work", PRApplyIn{
		Title:         "Version detect error test",
		Body:          "body",
		ReleaseLevel:  "patch",
		ReleaseNotes:  "Version detect error test release notes.",
		ReleaseSource: "user",
	}, rt)
	if err != nil {
		t.Fatalf("expected no error despite a version-detect failure, got: %v", err)
	}
}

// TestPRReleaseIntent covers prReleaseIntent directly: a pure function that
// builds release intent (level, pre-release, label) with no rt calls.
func TestPRReleaseIntent(t *testing.T) {
	cases := []struct {
		name       string
		level      string
		preRelease string
		wantLabel  string
	}{
		{"patch, no pre-release", "patch", "", "release:patch"},
		{"minor, rc", "minor", "rc", "release:minor-rc"},
		{"major, no pre-release", "major", "", "release:major"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			intent := prReleaseIntent(tc.level, tc.preRelease)
			if intent.Level != tc.level {
				t.Errorf("Level: got %q, want %q", intent.Level, tc.level)
			}
			if intent.PreRelease != tc.preRelease {
				t.Errorf("PreRelease: got %q, want %q", intent.PreRelease, tc.preRelease)
			}
			if intent.LabelApplied != tc.wantLabel {
				t.Errorf("LabelApplied: got %q, want %q", intent.LabelApplied, tc.wantLabel)
			}
		})
	}
}

func TestPRApply_WithoutRelease_Unchanged(t *testing.T) {
	// No releaseLevel: intent stays nil, so prReleaseApplyLabelWith (and thus
	// execRun) must never be invoked — the mock fails the test if it is.
	rt := prRuntime{
		ghPRForBranch: func(dir string) ghx.PRMetadata { return ghx.PRMetadata{Exists: false} },
		ghPRCreate: func(dir, title, body string) (string, error) {
			return "https://github.com/o/r/pull/13", nil
		},
		execRun: func(name string, args []string, opts execx.Options) (string, error) {
			t.Fatalf("execRun should not be called when releaseLevel is unset, got: %s %v", name, args)
			return "", nil
		},
		// SkipReleaseCheck triggers the verification gate (gitLogSinceTag);
		// idle upstream keeps the push-decision block from calling
		// gitPushSetUpstream (which would need its own mock here).
		gitLogSinceTag:  func(dir string) ([]string, error) { return nil, nil },
		gitHasUpstream:  func(dir string) (bool, error) { return true, nil },
		gitCommitsAhead: func(dir string) (int, error) { return 0, nil },
	}

	out, err := prApplyCoreWith("/mock/root", "/mock/work", PRApplyIn{
		Title:            "No release",
		Body:             "Just a normal PR",
		SkipReleaseCheck: true,
	}, rt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.ReleaseIntent != nil {
		t.Errorf("expected nil ReleaseIntent, got %+v", out.ReleaseIntent)
	}
}

// TestPRApply_WithRC_NoRCNumberPinned proves pr_apply no longer computes or
// pins an RC number in the PR body, even with existing RC tags present that
// a version-computing implementation would have scanned to pick the next
// one. RC-number sequencing now happens in CI at merge time.
func TestPRApply_WithRC_NoRCNumberPinned(t *testing.T) {
	var capturedBody string
	rt := releaseTestRuntime("3.0.0")
	// Existing RC tags on what a bumped base (minor bump of 3.0.0 = 3.1.0)
	// would have been.
	rt.gitAllSemverTags = func(dir string) ([]string, error) {
		return []string{"v3.1.0-rc1", "v3.1.0-rc2"}, nil
	}
	rt.ghPRCreate = func(dir, title, body string) (string, error) {
		capturedBody = body
		return "https://github.com/o/r/pull/14", nil
	}
	rt.execRun = mockAddLabelExec("release:minor-rc")

	out, err := prApplyCoreWith("/mock/root", "/mock/work", PRApplyIn{
		Title:             "RC next test",
		Body:              "body",
		ReleaseLevel:      "minor",
		ReleaseNotes:      "RC next test release notes.",
		ReleasePreRelease: "rc",
		ReleaseSource:     "user",
	}, rt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.ReleaseIntent == nil {
		t.Fatal("expected ReleaseIntent")
	}
	if strings.Contains(capturedBody, "3.1.0") {
		t.Errorf("body must not pin a version, got: %s", capturedBody)
	}
	if strings.Contains(capturedBody, "rc3") {
		t.Errorf("body must not pin an RC number, got: %s", capturedBody)
	}
	if out.ReleaseIntent.LabelApplied != "release:minor-rc" {
		t.Errorf("LabelApplied: got %q, want %q", out.ReleaseIntent.LabelApplied, "release:minor-rc")
	}
}

func TestPRApply_WithRC_LabelFormat(t *testing.T) {
	rt := releaseTestRuntime("1.0.0")
	rt.ghPRCreate = func(dir, title, body string) (string, error) {
		return "https://github.com/o/r/pull/15", nil
	}
	rt.execRun = mockAddLabelExec("release:patch-rc")

	out, err := prApplyCoreWith("/mock/root", "/mock/work", PRApplyIn{
		Title:             "RC label test",
		Body:              "body",
		ReleaseLevel:      "patch",
		ReleaseNotes:      "RC label test release notes.",
		ReleasePreRelease: "rc",
		ReleaseSource:     "user",
	}, rt)
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
	var capturedBody string
	rt := releaseTestRuntime("2.0.0")
	rt.ghPRCreate = func(dir, title, body string) (string, error) {
		capturedBody = body
		return "https://github.com/o/r/pull/16", nil
	}
	rt.execRun = mockAddLabelExec("release:minor-rc")

	out, err := prApplyCoreWith("/mock/root", "/mock/work", PRApplyIn{
		Title:             "RC marker test",
		Body:              "body",
		ReleaseLevel:      "minor",
		ReleaseNotes:      "RC marker test release notes.",
		ReleasePreRelease: "rc",
		ReleaseSource:     "user",
	}, rt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.ReleaseIntent == nil {
		t.Fatal("expected ReleaseIntent")
	}
	if out.ReleaseIntent.PreRelease != "rc" {
		t.Errorf("PreRelease: got %q, want %q", out.ReleaseIntent.PreRelease, "rc")
	}
	// Verify release-pre marker is present in the body passed to gh.
	if !strings.Contains(capturedBody, "<!-- release-pre:rc -->") {
		t.Errorf("body missing release-pre:rc marker")
	}
	if !strings.Contains(capturedBody, "<!-- release-level:minor -->") {
		t.Errorf("body missing release-level:minor marker")
	}
}

// TestPRApply_Next_MergeTimeVersionHint covers prApplyNext directly: with
// release intent set, the hint must mention merge-time version resolution
// so the caller does not report a version number for this PR; with no
// intent, the hint is unchanged from the pre-existing text.
func TestPRApply_Next_MergeTimeVersionHint(t *testing.T) {
	t.Run("no intent: unchanged (created)", func(t *testing.T) {
		got := prApplyNext(true, nil)
		want := "PR created. If verify-pipeline is configured, call verify_pipeline_classify next."
		if got != want {
			t.Errorf("prApplyNext = %q, want %q", got, want)
		}
	})

	t.Run("no intent: unchanged (updated)", func(t *testing.T) {
		got := prApplyNext(false, nil)
		want := "PR updated. If verify-pipeline is configured, call verify_pipeline_classify next."
		if got != want {
			t.Errorf("prApplyNext = %q, want %q", got, want)
		}
	})

	t.Run("intent set: mentions merge time", func(t *testing.T) {
		intent := &ReleaseIntentInfo{Level: "minor", PreRelease: "rc", LabelApplied: "release:minor-rc"}
		got := prApplyNext(true, intent)
		if !strings.Contains(got, "merge time") {
			t.Errorf("prApplyNext = %q, want it to mention merge time", got)
		}
		if !strings.Contains(got, "release:minor-rc") {
			t.Errorf("prApplyNext = %q, want it to mention the applied label", got)
		}
		if !strings.Contains(got, "do not report a version number") {
			t.Errorf("prApplyNext = %q, want it to warn against reporting a version number", got)
		}
	})

	t.Run("intent set: updated", func(t *testing.T) {
		intent := &ReleaseIntentInfo{Level: "patch", LabelApplied: "release:patch"}
		got := prApplyNext(false, intent)
		want := "PR updated. Release intent recorded as release:patch; CI computes the concrete version at merge time from the tags present then, so do not report a version number for this PR. If verify-pipeline is configured, call verify_pipeline_classify next."
		if got != want {
			t.Errorf("prApplyNext = %q, want %q", got, want)
		}
	})
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

// TestPrPrepareNext_IncludesVersionContext verifies that prPrepareNext folds
// version diagnostics into the Next hint (plan Task 1, step 3), covering the
// idempotency, off-default-branch, and conventional-suggestion cases on top
// of the pre-existing account-mismatch/error/plain-success cases.
func TestPrPrepareNext_IncludesVersionContext(t *testing.T) {
	t.Run("account mismatch takes priority", func(t *testing.T) {
		out := PRPrepareOut{AccountMismatch: true}
		if got := prPrepareNext(out); got != "Switch GitHub account, then call pr_prepare again." {
			t.Errorf("prPrepareNext = %q", got)
		}
	})

	t.Run("errors take priority", func(t *testing.T) {
		out := PRPrepareOut{Errors: []string{"boom"}}
		if got := prPrepareNext(out); got != "Fix the errors above, then call pr_prepare again." {
			t.Errorf("prPrepareNext = %q", got)
		}
	})

	t.Run("no version config falls back to plain success hint", func(t *testing.T) {
		out := PRPrepareOut{}
		if got := prPrepareNext(out); got != "Call pr_apply with title, body, and release fields." {
			t.Errorf("prPrepareNext = %q", got)
		}
	})

	t.Run("already bumped at HEAD", func(t *testing.T) {
		out := PRPrepareOut{
			VersionConfig: &VersionConfigInfo{},
			Idempotency:   &VersionIdempotency{AlreadyBumped: true, TagAtHead: "v1.2.3"},
		}
		want := "Call pr_apply with title, body, and release fields. Version already bumped at HEAD (tag v1.2.3); omit release fields."
		if got := prPrepareNext(out); got != want {
			t.Errorf("prPrepareNext = %q, want %q", got, want)
		}
	})

	t.Run("not on default branch", func(t *testing.T) {
		out := PRPrepareOut{
			VersionConfig:   &VersionConfigInfo{},
			OnDefaultBranch: false,
			DefaultBranch:   "main",
		}
		got := prPrepareNext(out)

		// Verify the guidance encourages passing release fields, not discourages it
		if strings.Contains(got, "informational only") {
			t.Errorf("prPrepareNext should not say 'informational only' on feature branch, got: %q", got)
		}
		if !strings.Contains(got, "release fields") {
			t.Errorf("prPrepareNext should mention 'release fields', got: %q", got)
		}
		if !strings.Contains(got, "main") {
			t.Errorf("prPrepareNext should mention target branch, got: %q", got)
		}

		// Verify the exact new guidance
		want := "Call pr_apply with title, body, and release fields. PR targets main — release fields must be forwarded to ship for the release label to be applied on merge."
		if got != want {
			t.Errorf("prPrepareNext = %q, want %q", got, want)
		}
	})

	t.Run("conventional suggestion surfaced", func(t *testing.T) {
		out := PRPrepareOut{
			VersionConfig:       &VersionConfigInfo{},
			OnDefaultBranch:     true,
			ConventionalSummary: &VersionConventionalSummary{Suggest: "minor"},
		}
		want := "Call pr_apply with title, body, and release fields. Suggested release level: minor."
		if got := prPrepareNext(out); got != want {
			t.Errorf("prPrepareNext = %q, want %q", got, want)
		}
	})
}

// TestPrPrepare_ReleaseMarkerTemplateConflict covers the early gate in
// prPrepareCoreWith: a custom PR template that already carries a release
// marker must fail before any body is drafted, because pr_apply injects the
// markers itself. Only internal/prtemplate tested ValidateReleaseCompat
// before; nothing proved pr_prepare acts on its verdict.
func TestPrPrepare_ReleaseMarkerTemplateConflict(t *testing.T) {
	const tmplPath = "/mock/root/.sdlc-v2/pr-template.md"

	rt := prRuntime{
		ghAuthProbe: func(dir, host string) ghx.AuthProbeResult {
			return ghx.AuthProbeResult{Authenticated: true, ActiveAccount: "someone"}
		},
		configReadSection: func(root, section string) (map[string]any, error) { return nil, nil },
		configRead:        func(root string) (*config.Config, error) { return nil, nil },
		execRun: func(name string, args []string, opts execx.Options) (string, error) {
			return "", errors.New("fatal: no such remote 'origin'")
		},
		gitCurrentBranch: func(dir string) (string, error) { return "feat/add-thing", nil },
		gitStatus:        func(dir string) (string, error) { return "", nil },
		gitDefaultBranch: func(dir string) (string, error) { return "main", nil },
		gitHasUpstream:   func(dir string) (bool, error) { return true, nil },
		gitCommitsAhead:  func(dir string) (int, error) { return 0, nil },
		branchValidate:   branch.ValidateExpectedBranch,
		jiraExtract:      func(branchName string) string { return detectJiraTicket(branchName, nil) },
		templateResolve: func(root string) (*prtemplate.Template, error) {
			return &prtemplate.Template{
				Path:     tmplPath,
				Content:  "## Summary\n\n<!-- release-level: minor -->\n\n## Testing\n",
				Headings: []string{"Summary", "Testing"},
			}, nil
		},
	}

	_, err := prPrepareCoreWith("/mock/root", "/mock/work", PRPrepareIn{SkipConfigCheck: true}, rt)
	if err == nil {
		t.Fatal("expected an error for a template carrying a release marker")
	}

	var domainErr *mcpserver.DomainError
	if !errors.As(err, &domainErr) {
		t.Fatalf("expected *mcpserver.DomainError, got %T: %v", err, err)
	}
	if !strings.Contains(domainErr.Msg, tmplPath) {
		t.Errorf("Msg = %q, want it to name the template path", domainErr.Msg)
	}
	if !strings.Contains(domainErr.Msg, "release-level") {
		t.Errorf("Msg = %q, want it to name the conflicting marker", domainErr.Msg)
	}
	if domainErr.Suggestion == "" {
		t.Error("Suggestion must not be empty")
	}
	if domainErr.Cause == nil {
		t.Error("Cause must carry the ValidateReleaseCompat error")
	}
}

// ---------------------------------------------------------------------------
// prReleaseStaleLabels / prReleaseApplyLabelWith (task 2, issue #66) — a
// re-apply on an existing PR must replace stale release:* labels instead of
// accumulating them.
// ---------------------------------------------------------------------------

func TestPRReleaseStaleLabels(t *testing.T) {
	tests := []struct {
		name     string
		existing []string
		keep     string
		want     []string
	}{
		{"nil existing returns empty non-nil", nil, "release:minor-rc", []string{}},
		{"no release labels present", []string{"bug", "enhancement"}, "release:minor-rc", []string{}},
		{"keep label excluded", []string{"release:minor-rc", "bug"}, "release:minor-rc", []string{}},
		{"single stale label", []string{"release:patch-rc", "bug"}, "release:minor-rc", []string{"release:patch-rc"}},
		{"multiple stale labels sorted", []string{"release:patch-rc", "release:major", "bug"}, "release:minor-rc", []string{"release:major", "release:patch-rc"}},
		{"non-release label release:foo is never removed", []string{"release:foo", "bug"}, "release:minor-rc", []string{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := prReleaseStaleLabels(tc.existing, tc.keep)
			if got == nil {
				t.Fatal("expected a non-nil slice")
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestPRApply_ExistingPR_ReplacesStaleReleaseLabel(t *testing.T) {
	t.Run("single stale label removed", func(t *testing.T) {
		var gotArgs []string
		rt := releaseTestRuntime("1.0.0")
		rt.ghPRForBranch = func(dir string) ghx.PRMetadata {
			return ghx.PRMetadata{Exists: true, Number: 9, URL: "https://github.com/o/r/pull/9", Labels: []string{"release:patch-rc", "bug"}}
		}
		rt.ghPREdit = func(dir string, num int, title, body string) (string, error) {
			return "https://github.com/o/r/pull/9", nil
		}
		rt.execRun = func(name string, args []string, opts execx.Options) (string, error) {
			gotArgs = args
			return "", nil
		}

		out, err := prApplyCoreWith("/mock/root", "/mock/work", PRApplyIn{
			Title: "T", Body: "B", ReleaseLevel: "minor", ReleasePreRelease: "rc", ReleaseNotes: "n", ReleaseSource: "user",
		}, rt)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := []string{"pr", "edit", "--add-label", "release:minor-rc", "--remove-label", "release:patch-rc"}
		if !slices.Equal(gotArgs, want) {
			t.Errorf("exec args: got %v, want %v", gotArgs, want)
		}
		if out.ReleaseIntent == nil {
			t.Fatal("expected ReleaseIntent to be populated")
		}
		if !slices.Equal(out.ReleaseIntent.LabelsRemoved, []string{"release:patch-rc"}) {
			t.Errorf("LabelsRemoved: got %v, want [release:patch-rc]", out.ReleaseIntent.LabelsRemoved)
		}
	})

	t.Run("two stale labels removed in one call, sorted", func(t *testing.T) {
		var gotArgs []string
		rt := releaseTestRuntime("1.0.0")
		rt.ghPRForBranch = func(dir string) ghx.PRMetadata {
			return ghx.PRMetadata{Exists: true, Number: 9, URL: "https://github.com/o/r/pull/9", Labels: []string{"release:patch-rc", "release:minor", "bug"}}
		}
		rt.ghPREdit = func(dir string, num int, title, body string) (string, error) {
			return "https://github.com/o/r/pull/9", nil
		}
		rt.execRun = func(name string, args []string, opts execx.Options) (string, error) {
			gotArgs = args
			return "", nil
		}

		out, err := prApplyCoreWith("/mock/root", "/mock/work", PRApplyIn{
			Title: "T", Body: "B", ReleaseLevel: "major", ReleaseNotes: "n", ReleaseSource: "user",
		}, rt)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := []string{"pr", "edit", "--add-label", "release:major", "--remove-label", "release:minor,release:patch-rc"}
		if !slices.Equal(gotArgs, want) {
			t.Errorf("exec args: got %v, want %v", gotArgs, want)
		}
		if !slices.Equal(out.ReleaseIntent.LabelsRemoved, []string{"release:minor", "release:patch-rc"}) {
			t.Errorf("LabelsRemoved: got %v, want [release:minor release:patch-rc]", out.ReleaseIntent.LabelsRemoved)
		}
	})
}

func TestPRApply_ExistingPR_SameReleaseLabel_NoRemove(t *testing.T) {
	rt := releaseTestRuntime("1.0.0")
	rt.ghPRForBranch = func(dir string) ghx.PRMetadata {
		return ghx.PRMetadata{Exists: true, Number: 9, URL: "https://github.com/o/r/pull/9", Labels: []string{"release:minor-rc", "bug"}}
	}
	rt.ghPREdit = func(dir string, num int, title, body string) (string, error) {
		return "https://github.com/o/r/pull/9", nil
	}
	// mockAddLabelExec fails the test if a --remove-label flag is sent.
	rt.execRun = mockAddLabelExec("release:minor-rc")

	out, err := prApplyCoreWith("/mock/root", "/mock/work", PRApplyIn{
		Title: "T", Body: "B", ReleaseLevel: "minor", ReleasePreRelease: "rc", ReleaseNotes: "n", ReleaseSource: "user",
	}, rt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.ReleaseIntent == nil {
		t.Fatal("expected ReleaseIntent to be populated")
	}
	if out.ReleaseIntent.LabelsRemoved == nil {
		t.Fatal("LabelsRemoved must be non-nil")
	}
	if len(out.ReleaseIntent.LabelsRemoved) != 0 {
		t.Errorf("LabelsRemoved: got %v, want empty", out.ReleaseIntent.LabelsRemoved)
	}
}

func TestPRApply_CreatePath_LabelsRemovedEmpty(t *testing.T) {
	rt := releaseTestRuntime("1.0.0")
	rt.ghPRCreate = func(dir, title, body string) (string, error) {
		return "https://github.com/o/r/pull/40", nil
	}
	// mockAddLabelExec fails the test if a --remove-label flag is sent — the
	// create path never has stale labels to remove.
	rt.execRun = mockAddLabelExec("release:patch")

	out, err := prApplyCoreWith("/mock/root", "/mock/work", PRApplyIn{
		Title: "T", Body: "B", ReleaseLevel: "patch", ReleaseNotes: "n", ReleaseSource: "user",
	}, rt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.ReleaseIntent == nil {
		t.Fatal("expected ReleaseIntent to be populated")
	}
	if out.ReleaseIntent.LabelsRemoved == nil {
		t.Fatal("LabelsRemoved must be non-nil")
	}
	if len(out.ReleaseIntent.LabelsRemoved) != 0 {
		t.Errorf("LabelsRemoved: got %v, want empty", out.ReleaseIntent.LabelsRemoved)
	}
}

func TestPRApply_LabelEditError_SuggestionNamesLabels(t *testing.T) {
	rt := releaseTestRuntime("1.0.0")
	rt.ghPRForBranch = func(dir string) ghx.PRMetadata {
		return ghx.PRMetadata{Exists: true, Number: 9, URL: "https://github.com/o/r/pull/9", Labels: []string{"release:patch-rc", "bug"}}
	}
	rt.ghPREdit = func(dir string, num int, title, body string) (string, error) {
		return "https://github.com/o/r/pull/9", nil
	}
	rt.execRun = func(name string, args []string, opts execx.Options) (string, error) {
		return "", errors.New("HTTP 422: Label does not exist")
	}

	_, err := prApplyCoreWith("/mock/root", "/mock/work", PRApplyIn{
		Title: "T", Body: "B", ReleaseLevel: "minor", ReleasePreRelease: "rc", ReleaseNotes: "n", ReleaseSource: "user",
	}, rt)

	var ie *mcpserver.InfraError
	if !errors.As(err, &ie) {
		t.Fatalf("expected *mcpserver.InfraError, got %T: %v", err, err)
	}
	if !strings.Contains(ie.Suggestion, "--add-label release:minor-rc") {
		t.Errorf("Suggestion missing add-label hint: %q", ie.Suggestion)
	}
	if !strings.Contains(ie.Suggestion, "--remove-label release:patch-rc") {
		t.Errorf("Suggestion missing remove-label hint: %q", ie.Suggestion)
	}
	if !strings.HasPrefix(ie.Msg, "gh pr edit --add-label --remove-label: ") {
		t.Errorf("Msg: got %q, want it to name both flags that were sent", ie.Msg)
	}
}

// TestPRApply_LabelEditError_PermissionError_Enriched pins that a gh
// permission error on the label call gets the same account-switch guidance
// as a permission error from gh pr create/edit.
func TestPRApply_LabelEditError_PermissionError_Enriched(t *testing.T) {
	rt := releaseTestRuntime("1.0.0")
	rt.ghPRForBranch = func(dir string) ghx.PRMetadata {
		return ghx.PRMetadata{Exists: true, Number: 9, URL: "https://github.com/acme/widgets/pull/9", Labels: []string{"release:patch-rc"}}
	}
	rt.ghPREdit = func(dir string, num int, title, body string) (string, error) {
		return "https://github.com/acme/widgets/pull/9", nil
	}
	rt.execRun = func(name string, args []string, opts execx.Options) (string, error) {
		if name == "git" && slices.Equal(args, []string{"remote", "get-url", "origin"}) {
			return "https://github.com/acme/widgets.git", nil
		}
		if name == "gh" && len(args) >= 2 && args[0] == "pr" && args[1] == "edit" {
			return "", errors.New("HTTP 403: Resource not accessible by integration")
		}
		return "", fmt.Errorf("unexpected exec call: %s %v", name, args)
	}
	rt.ghGetAccounts = func(dir, host string) ([]ghx.Account, error) {
		return []ghx.Account{{Login: "other-user", Active: false}}, nil
	}
	rt.ghAuthProbe = func(dir, host string) ghx.AuthProbeResult {
		return ghx.AuthProbeResult{Authenticated: true, ActiveAccount: "me"}
	}

	_, err := prApplyCoreWith("/mock/root", "/mock/work", PRApplyIn{
		Title: "T", Body: "B", ReleaseLevel: "minor", ReleasePreRelease: "rc", ReleaseNotes: "n", ReleaseSource: "user",
	}, rt)

	var ie *mcpserver.InfraError
	if !errors.As(err, &ie) {
		t.Fatalf("expected *mcpserver.InfraError, got %T: %v", err, err)
	}
	if !strings.HasPrefix(ie.Msg, "gh pr edit --add-label --remove-label: ") {
		t.Errorf("Msg: got %q, want it to name the label command", ie.Msg)
	}
	if !strings.Contains(ie.Suggestion, "gh auth switch --user other-user") {
		t.Errorf("Suggestion missing switch hint: %q", ie.Suggestion)
	}
	if !strings.Contains(ie.Suggestion, "acme/widgets") {
		t.Errorf("Suggestion missing owner/repo: %q", ie.Suggestion)
	}
}

// TestPRApply_LabelEditError_CreatePath_MsgOmitsRemoveLabel pins that the
// error Msg names only the flags actually sent: the create path never sends
// --remove-label, so the Msg must not claim it did.
func TestPRApply_LabelEditError_CreatePath_MsgOmitsRemoveLabel(t *testing.T) {
	rt := releaseTestRuntime("1.0.0")
	rt.execRun = func(name string, args []string, opts execx.Options) (string, error) {
		return "", errors.New("HTTP 422: Label does not exist")
	}

	_, err := prApplyCoreWith("/mock/root", "/mock/work", PRApplyIn{
		Title: "T", Body: "B", ReleaseLevel: "patch", ReleaseNotes: "n", ReleaseSource: "user",
	}, rt)

	var ie *mcpserver.InfraError
	if !errors.As(err, &ie) {
		t.Fatalf("expected *mcpserver.InfraError, got %T: %v", err, err)
	}
	if !strings.HasPrefix(ie.Msg, "gh pr edit --add-label: ") {
		t.Errorf("Msg: got %q, want it to start with gh pr edit --add-label", ie.Msg)
	}
	if strings.Contains(ie.Msg, "--remove-label") || strings.Contains(ie.Suggestion, "--remove-label") {
		t.Errorf("no --remove-label was sent, but the error names it: Msg=%q Suggestion=%q", ie.Msg, ie.Suggestion)
	}
}

// TestPRApply_ReapplyChangedIntent_SingleReleaseLabel proves the fix for
// issue #66 end to end: two sequential pr_apply calls against one fake PR
// (first patch+rc, then minor+rc) leave exactly one release:* label on the
// PR, instead of accumulating both.
func TestPRApply_ReapplyChangedIntent_SingleReleaseLabel(t *testing.T) {
	labels := map[string]bool{}
	rt := releaseTestRuntime("1.0.0")
	rt.ghPRForBranch = func(dir string) ghx.PRMetadata {
		ls := make([]string, 0, len(labels))
		for l := range labels {
			ls = append(ls, l)
		}
		slices.Sort(ls)
		return ghx.PRMetadata{Exists: true, Number: 9, URL: "https://github.com/o/r/pull/9", Labels: ls}
	}
	rt.ghPREdit = func(dir string, num int, title, body string) (string, error) {
		return "https://github.com/o/r/pull/9", nil
	}
	rt.execRun = func(name string, args []string, opts execx.Options) (string, error) {
		if name != "gh" || len(args) < 4 || args[0] != "pr" || args[1] != "edit" || args[2] != "--add-label" {
			return "", fmt.Errorf("unexpected exec call: %s %v", name, args)
		}
		labels[args[3]] = true
		switch len(args) {
		case 4:
			// add-label only
		case 6:
			if args[4] != "--remove-label" {
				return "", fmt.Errorf("unexpected exec call: %s %v", name, args)
			}
			for _, l := range strings.Split(args[5], ",") {
				delete(labels, l)
			}
		default:
			return "", fmt.Errorf("unexpected exec call: %s %v", name, args)
		}
		return "", nil
	}

	_, err := prApplyCoreWith("/mock/root", "/mock/work", PRApplyIn{
		Title: "T", Body: "B", ReleaseLevel: "patch", ReleasePreRelease: "rc", ReleaseNotes: "n", ReleaseSource: "user",
	}, rt)
	if err != nil {
		t.Fatalf("first apply: unexpected error: %v", err)
	}

	out2, err := prApplyCoreWith("/mock/root", "/mock/work", PRApplyIn{
		Title: "T", Body: "B", ReleaseLevel: "minor", ReleasePreRelease: "rc", ReleaseNotes: "n", ReleaseSource: "user",
	}, rt)
	if err != nil {
		t.Fatalf("second apply: unexpected error: %v", err)
	}

	var releaseLabelsLeft []string
	for l := range labels {
		if isReleaseLabel(l) {
			releaseLabelsLeft = append(releaseLabelsLeft, l)
		}
	}
	if !slices.Equal(releaseLabelsLeft, []string{"release:minor-rc"}) {
		t.Errorf("release:* labels left on the PR: got %v, want [release:minor-rc]", releaseLabelsLeft)
	}
	if out2.ReleaseIntent == nil || !slices.Equal(out2.ReleaseIntent.LabelsRemoved, []string{"release:patch-rc"}) {
		t.Errorf("second apply LabelsRemoved: got %v, want [release:patch-rc]", out2.ReleaseIntent.LabelsRemoved)
	}
}
