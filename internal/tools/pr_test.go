package tools

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rnagrodzki/sdlc-plugin/internal/branch"
	"github.com/rnagrodzki/sdlc-plugin/internal/config"
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
// `gh pr edit --add-label <wantLabel>` invocation prReleaseAddLabelWith
// issues, and fails (surfacing as an InfraError) for anything else — the
// mock-based equivalent of the old stubGHDispatch fixtures' narrow
// prefix-matched rules ("wrong label = no match = exit 1").
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
// prReleaseComputeIntentWith/ensureReleaseLabels/prApplyCoreWith's release
// path can reach, with deterministic no-op defaults: no config overrides
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
		gitTagExists:     func(dir, name string) (bool, error) { return false, nil },
		ghLabelList:      func(dir string) ([]string, error) { return nil, nil },
		ghLabelCreate:    func(dir, name, color, desc string) error { return nil },
		ghPRForBranch:    func(dir string) ghx.PRMetadata { return ghx.PRMetadata{Exists: false} },
		ghPRCreate:       func(dir, title, body string) (string, error) { return "https://example.com/pull/0", nil },
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
	// Active account ("wronguser") differs from pr.expectedAccount
	// ("correctuser"), which is itself among the locally logged-in
	// accounts — this is exactly the scenario
	// pr-recover-gh-account.js's standalone diagnostics target.
	rt := prRuntime{
		ghAuthProbe: func(dir, host string) ghx.AuthProbeResult {
			return ghx.AuthProbeResult{Authenticated: true, ActiveAccount: "wronguser"}
		},
		configReadSection: func(root, section string) (map[string]any, error) {
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
		branchValidate:   branch.ValidateExpectedBranch,
		jiraExtract:      func(branchName string) string { return detectJiraTicket(branchName, nil) },
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
		branchValidate:   branch.ValidateExpectedBranch,
		jiraExtract:      func(branchName string) string { return "" },
		templateResolve:  func(root string) (*prtemplate.Template, error) { return nil, nil },
		versionDetect: func(root, path, fileType string) (*version.VersionFile, error) {
			return &version.VersionFile{Path: "version.txt", Type: "text", Version: "1.2.0"}, nil
		},
		gitFetchTags:     func(dir string) error { return nil },
		gitTagList:       func(dir string) ([]string, error) { return []string{"v1.2.0"}, nil },
		gitAllSemverTags: func(dir string) ([]string, error) { return []string{"v1.3.0-rc1", "v1.2.0"}, nil },
		gitTagExists:     func(dir, name string) (bool, error) { return false, nil },
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
		branchValidate:   branch.ValidateExpectedBranch,
		jiraExtract:      func(branchName string) string { return "" },
		templateResolve:  func(root string) (*prtemplate.Template, error) { return nil, nil },
		versionDetect: func(root, path, fileType string) (*version.VersionFile, error) {
			return nil, errors.New("version file not found")
		},
		gitFetchTags:     func(dir string) error { return nil },
		gitTagList:       func(dir string) ([]string, error) { return nil, nil },
		gitAllSemverTags: func(dir string) ([]string, error) { return nil, nil },
		gitTagExists:     func(dir, name string) (bool, error) { return false, nil },
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
// pr_apply
// ---------------------------------------------------------------------------

func TestPrApply_NoExistingPR_Creates(t *testing.T) {
	rt := prRuntime{
		ghPRForBranch: func(dir string) ghx.PRMetadata { return ghx.PRMetadata{Exists: false} },
		ghPRCreate: func(dir, title, body string) (string, error) {
			return "https://github.com/o/r/pull/9", nil
		},
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
	if !strings.Contains(de.Suggestion, "/version") {
		t.Errorf("Suggestion missing /version hint: %q", de.Suggestion)
	}
	if !strings.Contains(de.Suggestion, "skipReleaseCheck: true") {
		t.Errorf("Suggestion missing skipReleaseCheck hint: %q", de.Suggestion)
	}
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
	}

	_, err := prApplyCoreWith("/mock/root", "/mock/work", PRApplyIn{Title: "Add thing", Body: "Body text", SkipReleaseCheck: true}, rt)
	if err == nil {
		t.Fatal("expected an error")
	}
	var ie *mcpserver.InfraError
	if !errors.As(err, &ie) {
		t.Fatalf("expected *mcpserver.InfraError, got %T: %v", err, err)
	}
	if ie.Suggestion != "" {
		t.Errorf("expected no Suggestion for a non-permission error, got %q", ie.Suggestion)
	}
	if !strings.Contains(ie.Msg, "connection reset by peer") {
		t.Errorf("Msg: got %q, want original error text preserved", ie.Msg)
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
	}

	_, err := prApplyCoreWith("/mock/root", "/mock/work", PRApplyIn{Title: "Add thing", Body: "Body text", SkipReleaseCheck: true}, rt)
	if err == nil {
		t.Fatal("expected an error")
	}
	var ie *mcpserver.InfraError
	if !errors.As(err, &ie) {
		t.Fatalf("expected *mcpserver.InfraError, got %T: %v", err, err)
	}
	if ie.Suggestion != "" {
		t.Errorf("expected no Suggestion when enrichment cannot resolve a remote, got %q", ie.Suggestion)
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
	if out.ReleaseIntent.ComputedVersion != "2.0.1" {
		t.Errorf("ComputedVersion: got %q, want %q", out.ReleaseIntent.ComputedVersion, "2.0.1")
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
	if !strings.Contains(capturedBody, "## [2.0.1]") {
		t.Errorf("body missing version header, got: %s", capturedBody)
	}
}

func TestPRApply_WithRelease_VersionComputed(t *testing.T) {
	rt := releaseTestRuntime("1.5.3")
	// Tag higher than the file version — bump base should be the tag, but
	// only because tag.enabled is true; that's what makes max() consult it.
	// versionFile.enabled must also be true, so PreviousVersion still comes
	// from the file (not from the tag via isTagMode).
	rt.configRead = func(root string) (*config.Config, error) {
		return &config.Config{Version: &config.VersionSection{
			Tag:         config.VersionTagConfig{Enabled: true},
			VersionFile: config.VersionFileConfig{Enabled: true},
		}}, nil
	}
	rt.gitTagList = func(dir string) ([]string, error) { return []string{"v1.6.0"}, nil }
	rt.ghPRCreate = func(dir, title, body string) (string, error) {
		return "https://github.com/o/r/pull/12", nil
	}
	rt.execRun = mockAddLabelExec("release:major")

	out, err := prApplyCoreWith("/mock/root", "/mock/work", PRApplyIn{
		Title:         "Version compute test",
		Body:          "body",
		ReleaseLevel:  "major",
		ReleaseSource: "user",
	}, rt)
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
	// Custom tagPrefix ("rel-") — gitTagList (prefix-blind) returns nothing,
	// so bumpBase = fileVersion, and gitTagExists reports a collision on the
	// exact tag the minor bump would produce.
	rt := releaseTestRuntime("1.2.0")
	rt.configRead = func(root string) (*config.Config, error) {
		return &config.Config{Version: &config.VersionSection{
			Tag:         config.VersionTagConfig{Enabled: true, Prefix: "rel-"},
			VersionFile: config.VersionFileConfig{Enabled: true},
		}}, nil
	}
	rt.gitTagExists = func(dir, name string) (bool, error) { return name == "rel-1.3.0", nil }

	_, err := prApplyCoreWith("/mock/root", "/mock/work", PRApplyIn{
		Title:         "Collision test",
		Body:          "body",
		ReleaseLevel:  "minor",
		ReleaseSource: "user",
	}, rt)
	if err == nil {
		t.Fatal("expected collision error")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("error should mention collision, got: %v", err)
	}
}

func TestPRReleaseComputeIntent_TagMode(t *testing.T) {
	t.Run("derives version from highest semver tag", func(t *testing.T) {
		rt := releaseTestRuntime("")
		rt.configRead = func(root string) (*config.Config, error) {
			return &config.Config{Version: &config.VersionSection{
				Tag: config.VersionTagConfig{Enabled: true},
			}}, nil
		}
		rt.versionDetect = func(root, path, fileType string) (*version.VersionFile, error) {
			t.Fatal("versionDetect must not be called in tag mode")
			return nil, nil
		}
		rt.gitTagList = func(dir string) ([]string, error) { return []string{"v1.4.0", "v1.2.0"}, nil }

		intent, err := prReleaseComputeIntentWith(rt, "/mock/root", "/mock/work", "minor", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if intent.PreviousVersion != "1.4.0" {
			t.Errorf("PreviousVersion: got %q, want %q", intent.PreviousVersion, "1.4.0")
		}
		if intent.ComputedVersion != "1.5.0" {
			t.Errorf("ComputedVersion: got %q, want %q", intent.ComputedVersion, "1.5.0")
		}
		if intent.TagName != "v1.5.0" {
			t.Errorf("TagName: got %q, want %q", intent.TagName, "v1.5.0")
		}
	})

	t.Run("falls back to 0.0.0 when no semver tags exist", func(t *testing.T) {
		rt := releaseTestRuntime("")
		rt.configRead = func(root string) (*config.Config, error) {
			return &config.Config{Version: &config.VersionSection{
				Tag: config.VersionTagConfig{Enabled: true},
			}}, nil
		}
		rt.versionDetect = func(root, path, fileType string) (*version.VersionFile, error) {
			t.Fatal("versionDetect must not be called in tag mode")
			return nil, nil
		}
		rt.gitTagList = func(dir string) ([]string, error) { return nil, nil }

		intent, err := prReleaseComputeIntentWith(rt, "/mock/root", "/mock/work", "patch", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if intent.PreviousVersion != "0.0.0" {
			t.Errorf("PreviousVersion: got %q, want %q", intent.PreviousVersion, "0.0.0")
		}
		if intent.ComputedVersion != "0.0.1" {
			t.Errorf("ComputedVersion: got %q, want %q", intent.ComputedVersion, "0.0.1")
		}
	})

	t.Run("file mode unchanged: version still derived from version file", func(t *testing.T) {
		rt := releaseTestRuntime("2.3.1")
		rt.configRead = func(root string) (*config.Config, error) {
			return &config.Config{Version: &config.VersionSection{
				VersionFile: config.VersionFileConfig{Enabled: true},
			}}, nil
		}
		rt.gitTagList = func(dir string) ([]string, error) { return nil, nil }

		intent, err := prReleaseComputeIntentWith(rt, "/mock/root", "/mock/work", "patch", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if intent.PreviousVersion != "2.3.1" {
			t.Errorf("PreviousVersion: got %q, want %q", intent.PreviousVersion, "2.3.1")
		}
		if intent.ComputedVersion != "2.3.2" {
			t.Errorf("ComputedVersion: got %q, want %q", intent.ComputedVersion, "2.3.2")
		}
	})

	t.Run("tag.enabled=false: bump base ignores higher tag", func(t *testing.T) {
		rt := releaseTestRuntime("1.5.3")
		rt.configRead = func(root string) (*config.Config, error) {
			return &config.Config{Version: &config.VersionSection{
				VersionFile: config.VersionFileConfig{Enabled: true},
				Tag:         config.VersionTagConfig{Enabled: false},
			}}, nil
		}
		// Tag is higher than file version, but tag path disabled: max()
		// must not consult it. Bump base stays the file version.
		rt.gitTagList = func(dir string) ([]string, error) { return []string{"v1.6.0"}, nil }

		intent, err := prReleaseComputeIntentWith(rt, "/mock/root", "/mock/work", "minor", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if intent.ComputedVersion != "1.6.0" {
			t.Errorf("ComputedVersion: got %q, want %q (bump base should be file version 1.5.3, not tag 1.6.0)", intent.ComputedVersion, "1.6.0")
		}
	})

	t.Run("tag.enabled=true: bump base consults higher tag", func(t *testing.T) {
		rt := releaseTestRuntime("1.5.3")
		rt.configRead = func(root string) (*config.Config, error) {
			return &config.Config{Version: &config.VersionSection{
				VersionFile: config.VersionFileConfig{Enabled: true},
				Tag:         config.VersionTagConfig{Enabled: true},
			}}, nil
		}
		// Same inputs as above, tag path enabled this time: max() must
		// pick the higher tag as the bump base.
		rt.gitTagList = func(dir string) ([]string, error) { return []string{"v1.6.0"}, nil }

		intent, err := prReleaseComputeIntentWith(rt, "/mock/root", "/mock/work", "minor", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if intent.ComputedVersion != "1.7.0" {
			t.Errorf("ComputedVersion: got %q, want %q (bump base should be tag 1.6.0, not file version 1.5.3)", intent.ComputedVersion, "1.7.0")
		}
	})
}

func TestPRApply_WithoutRelease_Unchanged(t *testing.T) {
	// No releaseLevel: intent stays nil, so prReleaseAddLabelWith (and thus
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

func TestPRApply_WithRC_NextRCComputed(t *testing.T) {
	rt := releaseTestRuntime("3.0.0")
	// Existing RC tags on the bumped base (minor bump of 3.0.0 = 3.1.0).
	rt.gitAllSemverTags = func(dir string) ([]string, error) {
		return []string{"v3.1.0-rc1", "v3.1.0-rc2"}, nil
	}
	rt.ghPRCreate = func(dir, title, body string) (string, error) {
		return "https://github.com/o/r/pull/14", nil
	}
	rt.execRun = mockAddLabelExec("release:minor-rc")

	out, err := prApplyCoreWith("/mock/root", "/mock/work", PRApplyIn{
		Title:             "RC next test",
		Body:              "body",
		ReleaseLevel:      "minor",
		ReleasePreRelease: "rc",
		ReleaseSource:     "user",
	}, rt)
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
	rt := releaseTestRuntime("1.0.0")
	rt.ghPRCreate = func(dir, title, body string) (string, error) {
		return "https://github.com/o/r/pull/15", nil
	}
	rt.execRun = mockAddLabelExec("release:patch-rc")

	out, err := prApplyCoreWith("/mock/root", "/mock/work", PRApplyIn{
		Title:             "RC label test",
		Body:              "body",
		ReleaseLevel:      "patch",
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
	if !strings.Contains(out.ReleaseIntent.ComputedVersion, "-rc") {
		t.Errorf("ComputedVersion should contain -rc, got %q", out.ReleaseIntent.ComputedVersion)
	}
	// Verify release-pre marker is present in the body passed to gh.
	if !strings.Contains(capturedBody, "<!-- release-pre:rc -->") {
		t.Errorf("body missing release-pre:rc marker")
	}
	if !strings.Contains(capturedBody, "<!-- release-level:minor -->") {
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
		want := "Call pr_apply with title, body, and release fields. Not on default branch (default: main); version fields are informational only."
		if got := prPrepareNext(out); got != want {
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
