// pr.go implements the pr skill's MCP tools: pr_prepare (gh-auth +
// config-check + branch-guard + JIRA-detection + template-load preflight)
// and pr_apply (KD14 executor: gh pr create/edit for the current branch).
// It also keeps prValidateBodyCore (PR body vs. template section-presence
// check), which used to back a standalone pr_validate_body tool here and
// is now reused by validate's "pr_body" action (internal/tools/validators.go).
//
// Scope fence (task 25): pr_prepare mirrors only scripts/skill/pr.js's
// gh-auth-preflight + config-check + branch-guard + JIRA-detection +
// template-load slice (main()'s Steps up to and including Step 4). It does
// NOT port getCommitsStructured, getDiffStat/getDiffContent,
// getRemoteState/pushToRemote, fetchRepoLabels, or base-branch resolution
// (Step 5 onward) — none of those primitives exist elsewhere in this Go
// port, and building them was ruled out of scope for this task.
package tools

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/rnagrodzki/sdlc-plugin/internal/branch"
	"github.com/rnagrodzki/sdlc-plugin/internal/config"
	"github.com/rnagrodzki/sdlc-plugin/internal/configmigrate"
	"github.com/rnagrodzki/sdlc-plugin/internal/execx"
	"github.com/rnagrodzki/sdlc-plugin/internal/ghx"
	"github.com/rnagrodzki/sdlc-plugin/internal/gitx"
	"github.com/rnagrodzki/sdlc-plugin/internal/jirakeys"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/prtemplate"
	"github.com/rnagrodzki/sdlc-plugin/internal/version"
	"github.com/rnagrodzki/sdlc-plugin/internal/worktree"
)

// ---------------------------------------------------------------------------
// pr_prepare
// ---------------------------------------------------------------------------

// PRPrepareIn is the input for pr_prepare.
//
// ExpectedBranch is a disclosed addition beyond the fact sheet's literal
// Contract example (PRPrepareIn{SkipConfigCheck bool}): the branch-guard
// hard gate is explicitly in-scope for this task, and
// branch.ValidateExpectedBranch is dead code without something to compare
// the current branch against — this mirrors pr.js's --expected-branch flag.
type PRPrepareIn struct {
	SkipConfigCheck bool   `json:"skipConfigCheck"`
	ExpectedBranch  string `json:"expectedBranch,omitempty"`
}

// PRAccountRow is a JSON-friendly projection of ghx.Account (which carries
// no JSON tags of its own).
type PRAccountRow struct {
	Login  string `json:"login"`
	Active bool   `json:"active"`
}

// PRAuthDiagnostics carries the recovery-shaped diagnostic fields pr_prepare
// embeds whenever the gh-auth preflight fails (fact sheet Gap 2b, KD16
// "recover fold"). These field names are a direct synthesis — modeled on
// lib/git.js's recoverGhAccountForRepo output shape (candidate accounts, a
// matched account, switch/login hints) — not a literal port, because
// recoverGhAccountForRepo only runs post-failure against a live `gh pr
// create` permission-error string, which hasn't happened yet at prepare
// time. See buildAuthDiagnosticsWith.
type PRAuthDiagnostics struct {
	Owner          string         `json:"owner,omitempty"`
	Candidates     []PRAccountRow `json:"candidates,omitempty"`
	MatchedAccount string         `json:"matchedAccount,omitempty"`
	SwitchHint     string         `json:"switchHint,omitempty"`
	LoginHint      string         `json:"loginHint,omitempty"`
}

// PRTemplateOut is a JSON-friendly projection of prtemplate.Template.
type PRTemplateOut struct {
	Path     string   `json:"path,omitempty"`
	Legacy   bool     `json:"legacy,omitempty"`
	Headings []string `json:"headings,omitempty"`
	Content  string   `json:"content,omitempty"`
}

// PRPrepareOut is the output of pr_prepare. Every JS writeOutput(...,1)
// early-return in pr.js's main() becomes a (PRPrepareOut, nil) return here
// (KD5/KD3: domain-level preflight failures are payload, not a non-nil Go
// error) — a non-nil error is reserved for infra failures. Unlike pr.js,
// which writes a fresh, minimal JSON object literal at each early return,
// this port's PRPrepareOut keeps every field already computed before the
// failure point (e.g. an account-mismatch failure still carries
// GHAuthenticated/ActiveAccount) — a disclosed, additive difference from
// the source's per-branch payload shapes; omitempty keeps the wire size
// close to what each branch would emit.
type PRPrepareOut struct {
	OK             bool     `json:"ok"`
	Errors         []string `json:"errors,omitempty"`
	Warnings       []string `json:"warnings,omitempty"`
	NeedsMigration bool     `json:"needsMigration,omitempty"`

	GHAuthenticated bool   `json:"ghAuthenticated"`
	ActiveAccount   string `json:"activeAccount,omitempty"`
	ExpectedAccount string `json:"expectedAccount,omitempty"`
	AccountMismatch bool   `json:"accountMismatch"`
	// TokenExpired is always false: distinguishing an expired token from
	// "never logged in" would require inspecting gh auth status's stderr
	// text, which ghx.AuthProbe cannot do (see its doc comment). Field is
	// kept for output-shape parity with pr.js.
	TokenExpired bool `json:"tokenExpired"`

	RepoAccessProbed bool  `json:"repoAccessProbed"`
	RepoAccessible   *bool `json:"repoAccessible,omitempty"`
	RepoAccessStatus *int  `json:"repoAccessStatus,omitempty"`

	Diagnostics *PRAuthDiagnostics `json:"diagnostics,omitempty"`

	CurrentBranch      string                    `json:"currentBranch,omitempty"`
	UncommittedChanges bool                      `json:"uncommittedChanges,omitempty"`
	DirtyFiles         []string                  `json:"dirtyFiles,omitempty"`
	BranchGuard        *branch.BranchGuardResult `json:"branchGuard,omitempty"`

	JiraTicket string         `json:"jiraTicket,omitempty"`
	Template   *PRTemplateOut `json:"template,omitempty"`
}

// ---------------------------------------------------------------------------
// prRuntime — dependency-injection struct for prPrepareCore / prApplyCore
// ---------------------------------------------------------------------------

// prRuntime holds function fields wrapping every external dependency called by
// prPrepareCore, prApplyCore, and their helpers. Tests inject mocks through
// prRuntime; production code uses defaultPRRuntime.
type prRuntime struct {
	ghPRForBranch       func(dir string) ghx.PRMetadata
	ghPRCreate          func(dir, title, body string) (string, error)
	ghPREdit            func(dir string, num int, title, body string) (string, error)
	ghLabelList         func(dir string) ([]string, error)
	ghLabelCreate       func(dir, name, color, desc string) error
	ghAuthProbe         func(dir, host string) ghx.AuthProbeResult
	ghRepoAccessProbe   func(dir, owner, repo, host string) ghx.RepoAccessResult
	ghGetAccounts       func(dir, host string) ([]ghx.Account, error)
	gitCurrentBranch    func(dir string) (string, error)
	gitStatus           func(dir string) (string, error)
	gitFetchTags        func(dir string) error
	gitTagList          func(dir string) ([]string, error)
	gitAllSemverTags    func(dir string) ([]string, error)
	gitTagExists        func(dir, name string) (bool, error)
	execRun             func(name string, args []string, opts execx.Options) (string, error)
	configRead          func(root string) (*config.Config, error)
	configReadSection   func(root, section string) (map[string]any, error)
	versionDetect       func(root, path, fileType string) (*version.VersionFile, error)
	configMigrateVerify func(root string) error
	branchValidate      func(current, expected string) branch.BranchGuardResult
	jiraExtract         func(branchName string) string
	templateResolve     func(root string) (*prtemplate.Template, error)
}

// defaultPRRuntime wires prRuntime to the real package-level implementations.
var defaultPRRuntime = prRuntime{
	ghPRForBranch:       ghx.PRForBranch,
	ghPRCreate:          ghx.PRCreate,
	ghPREdit:            ghx.PREdit,
	ghLabelList:         ghx.LabelList,
	ghLabelCreate:       ghx.LabelCreate,
	ghAuthProbe:         ghx.AuthProbe,
	ghRepoAccessProbe:   ghx.RepoAccessProbe,
	ghGetAccounts:       ghx.GetAccounts,
	gitCurrentBranch:    gitx.CurrentBranch,
	gitStatus:           gitx.Status,
	gitFetchTags:        gitx.FetchTags,
	gitTagList:          gitx.TagList,
	gitAllSemverTags:    gitx.AllSemverTags,
	gitTagExists:        gitx.TagExists,
	execRun:             execx.Run,
	configRead:          config.Read,
	configReadSection:   config.ReadSection,
	versionDetect:       version.DetectAt,
	configMigrateVerify: configmigrate.Verify,
	branchValidate:      branch.ValidateExpectedBranch,
	jiraExtract: func(branchName string) string {
		return detectJiraTicket(branchName, nil)
	},
	templateResolve: prtemplate.Resolve,
}

// buildAuthDiagnosticsWith synthesizes the PRAuthDiagnostics block for a
// gh-auth failure. wantLogin, when non-empty, is the login we'd like an
// already-authenticated local account to match (the configured
// expectedAccount for a mismatch, nothing for an access-denial); owner is
// the repo owner (access-denial case only); suggested is a fallback account
// list already gathered by a probe (e.g. RepoAccessResult.SuggestedAccounts)
// used only if GetAccounts itself comes back empty.
func buildAuthDiagnosticsWith(rt prRuntime, workDir, wantLogin, owner string, suggested []string) *PRAuthDiagnostics {
	// ghx.GetAccounts, unlike ghx.AuthProbe/ghx.RepoAccessProbe, does not
	// default an empty host to "github.com" itself (it looks up the raw
	// `gh auth status --json hosts` map by the exact key given) — default
	// it here so this call is consistent with the other two probes.
	accounts, _ := rt.ghGetAccounts(workDir, "github.com")
	rows := make([]PRAccountRow, 0, len(accounts))
	for _, a := range accounts {
		rows = append(rows, PRAccountRow{Login: a.Login, Active: a.Active})
	}
	if len(rows) == 0 {
		for _, login := range suggested {
			rows = append(rows, PRAccountRow{Login: login})
		}
	}

	diag := &PRAuthDiagnostics{Owner: owner, Candidates: rows}
	if wantLogin != "" {
		if match := ghx.SelectAccountForOwner(wantLogin, accounts); match != nil {
			diag.MatchedAccount = match.Login
		}
		diag.SwitchHint = fmt.Sprintf("gh auth switch --user %s", wantLogin)
	}
	if diag.MatchedAccount == "" {
		diag.LoginHint = "gh auth login --hostname github.com"
	}
	return diag
}

// detectJiraTicket ports pr.js's detectJiraTicket(branchName, commits):
// the branch name wins if it contains a JIRA key; otherwise the first
// commit subject (in encounter order) that contains one wins.
//
// pr_prepare's in-scope slice (ruling 4) has no commit-gathering primitive
// (getCommitsStructured is out of scope), so its only caller always passes
// commitSubjects == nil and the commit-fallback arm below is exercised only
// by this function's own unit tests — a disclosed structurally-unreachable
// path in production, kept because it costs nothing to keep faithful and
// documents the composition pr.js actually performs.
func detectJiraTicket(branchName string, commitSubjects []string) string {
	if keys := jirakeys.Extract(branchName); len(keys) > 0 {
		return keys[0]
	}
	for _, subject := range commitSubjects {
		if keys := jirakeys.Extract(subject); len(keys) > 0 {
			return keys[0]
		}
	}
	return ""
}

// prPrepareCore is pr_prepare's core logic, separated from the MCP handler
// for testability: mainRoot anchors config/sdlc-v2 state (worktree.MainRoot),
// workDir anchors git/gh operations (worktree.ActiveRoot).
func prPrepareCore(mainRoot, workDir string, in PRPrepareIn) (PRPrepareOut, error) {
	return prPrepareCoreWith(mainRoot, workDir, in, defaultPRRuntime)
}

// prPrepareCoreWith is the parameterized form of prPrepareCore. All external
// calls go through rt, enabling mock injection in tests.
func prPrepareCoreWith(mainRoot, workDir string, in PRPrepareIn, rt prRuntime) (PRPrepareOut, error) {
	var errs, warnings []string

	// KD5 gate: hard-abort with a minimal errors-only payload on config
	// migration failure, mirroring pr.js's ensureConfigVersion short-circuit
	// (and plan.go/setup.go's own KD5 gate).
	if !in.SkipConfigCheck {
		if err := rt.configMigrateVerify(mainRoot); err != nil {
			errs = append(errs, fmt.Sprintf("config-version: %s", err.Error()))
			return PRPrepareOut{Errors: errs, NeedsMigration: true}, nil
		}
	}

	// gh-auth + active-account preflight (pr.js issues #234/#380).
	authProbe := rt.ghAuthProbe(workDir, "")
	out := PRPrepareOut{
		GHAuthenticated: authProbe.Authenticated,
		ActiveAccount:   authProbe.ActiveAccount,
	}

	prSection, _ := rt.configReadSection(mainRoot, "pr")
	expectedAccount := ""
	if v, ok := prSection["expectedAccount"].(string); ok {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			expectedAccount = trimmed
		}
	}
	out.ExpectedAccount = expectedAccount

	if !authProbe.Authenticated {
		errs = append(errs, authProbe.ErrorMessage)
		out.Errors = errs
		// AC2 asks for the same account diagnostics the standalone recover
		// script produced; build them here too (not just on mismatch) so an
		// unauthenticated failure still surfaces any configured candidates.
		// wantLogin=="" means buildAuthDiagnosticsWith never finds a match,
		// so it falls through to its own default LoginHint.
		out.Diagnostics = buildAuthDiagnosticsWith(rt, workDir, "", "", nil)
		return out, nil
	}

	accountMismatch := expectedAccount != "" && authProbe.ActiveAccount != "" &&
		!strings.EqualFold(authProbe.ActiveAccount, expectedAccount)
	out.AccountMismatch = accountMismatch
	if accountMismatch {
		errs = append(errs, ghx.FormatAccountMismatch(expectedAccount, authProbe.ActiveAccount))
		out.Errors = errs
		out.Diagnostics = buildAuthDiagnosticsWith(rt, workDir, expectedAccount, "", nil)
		return out, nil
	}

	// Remote owner/repo, best-effort — a missing origin remote is not an
	// error, matching pr.js's parseRemoteOwner() returning null.
	owner, repo, hasRemote := "", "", false
	if originURL, err := rt.execRun("git", []string{"remote", "get-url", "origin"}, execx.Options{Dir: workDir}); err == nil && originURL != "" {
		if o, r, parseErr := ghx.ParseRemoteOwner(originURL); parseErr == nil {
			owner, repo, hasRemote = o, r, true
		}
	}

	switch {
	case expectedAccount == "" && hasRemote:
		probe := rt.ghRepoAccessProbe(workDir, owner, repo, "")
		out.RepoAccessProbed = true
		out.RepoAccessible = probe.Accessible
		out.RepoAccessStatus = probe.StatusCode
		if probe.Accessible != nil && !*probe.Accessible {
			errs = append(errs, ghx.FormatAccessDenied(authProbe.ActiveAccount, owner, repo, probe.SuggestedAccounts))
			out.Errors = errs
			out.Diagnostics = buildAuthDiagnosticsWith(rt, workDir, "", owner, probe.SuggestedAccounts)
			return out, nil
		}
		if probe.Accessible == nil {
			msg := probe.ErrorMessage
			if msg == "" {
				msg = "network error"
			}
			warnings = append(warnings, fmt.Sprintf("Repo access probe failed (%s) — proceeding without access verification.", msg))
		}
	case expectedAccount == "" && !hasRemote:
		warnings = append(warnings, "Could not resolve expected gh account (no pr.expectedAccount, no origin remote). Skipping active-account check.")
	}

	// Git state: current branch + uncommitted-changes, mirroring
	// checkGitState's currentBranch/uncommittedChanges/dirtyFiles.
	currentBranch, err := rt.gitCurrentBranch(workDir)
	if err != nil {
		errs = append(errs, err.Error())
		out.Errors = errs
		out.Warnings = warnings
		return out, nil
	}
	out.CurrentBranch = currentBranch

	if statusRaw, statusErr := rt.gitStatus(workDir); statusErr == nil {
		var dirty []string
		for _, line := range strings.Split(statusRaw, "\n") {
			if line == "" {
				continue
			}
			if len(line) > 3 {
				dirty = append(dirty, line[3:])
			}
		}
		out.UncommittedChanges = len(dirty) > 0
		out.DirtyFiles = dirty
	}

	// Branch-guard HARD GATE (issues #347-349). Must run before the
	// protected-branch check, matching pr.js's exact ordering. JS reports
	// this as exit code 3; the KD3 envelope has no analogous concept, so it
	// is represented here as a normal (non-error) payload carrying the
	// BranchGuard result — pr.js itself still emits a full JSON payload
	// (not a bare error) at this point.
	guard := rt.branchValidate(currentBranch, in.ExpectedBranch)
	out.BranchGuard = &guard
	if guard.Active && !guard.OK {
		errs = append(errs, guard.Message)
		out.Errors = errs
		out.Warnings = warnings
		return out, nil
	}

	if currentBranch == "main" || currentBranch == "master" {
		errs = append(errs, fmt.Sprintf("You are on the %s branch. Switch to a feature branch before creating a PR.", currentBranch))
		out.Errors = errs
		out.Warnings = warnings
		return out, nil
	}

	if out.UncommittedChanges {
		warnings = append(warnings, fmt.Sprintf("Uncommitted changes detected (%d file(s)). They will NOT be included in the PR.", len(out.DirtyFiles)))
	}

	// JIRA ticket detection — branch-name-only in this port (see
	// detectJiraTicket's doc comment).
	out.JiraTicket = rt.jiraExtract(currentBranch)

	// PR template resolution (shared with task 20's prtemplate package). A
	// resolution failure is non-fatal — it becomes a warning, not an error,
	// since it does not block the rest of the preflight's diagnostic value.
	if tmpl, tmplErr := rt.templateResolve(mainRoot); tmplErr != nil {
		warnings = append(warnings, fmt.Sprintf("PR template resolution failed: %s", tmplErr.Error()))
	} else if tmpl != nil {
		// Fail early, before any PR body is drafted, when the custom
		// template conflicts with the release markers pr_apply injects
		// automatically (prReleaseInjectMarkers below).
		if compatErr := prtemplate.ValidateReleaseCompat(tmpl.Content); compatErr != nil {
			return PRPrepareOut{}, &mcpserver.DomainError{Msg: compatErr.Error()}
		}
		out.Template = &PRTemplateOut{
			Path:     tmpl.Path,
			Legacy:   tmpl.Legacy,
			Headings: tmpl.Headings,
			Content:  tmpl.Content,
		}
	}

	out.Errors = errs
	out.Warnings = warnings
	out.OK = len(errs) == 0
	return out, nil
}

// ---------------------------------------------------------------------------
// pr_body core (reused by validate's "pr_body" action; no longer a
// standalone MCP tool of its own — see internal/tools/validators.go)
// ---------------------------------------------------------------------------

// PRValidateBodyIn is the input for prValidateBodyCore.
type PRValidateBodyIn struct {
	Body string `json:"body"`
}

// PRValidateBodyOut is the output of prValidateBodyCore.
type PRValidateBodyOut struct {
	OK     bool     `json:"ok"`
	Errors []string `json:"errors,omitempty"`
}

// prValidateBodyCore resolves the project's PR template and checks Body
// against it via prtemplate.ValidateBody — the pr SKILL.md's
// section-presence contract, NOT pr.js's real --validate-body link
// validation (that lives in internal/links and is explicitly out of scope
// for this tool per the task's ruling). Called directly by validate's
// "pr_body" action (internal/tools/validators.go); no longer wired to its
// own MCP tool registration.
func prValidateBodyCore(root string, in PRValidateBodyIn) (PRValidateBodyOut, error) {
	tmpl, err := prtemplate.Resolve(root)
	if err != nil {
		return PRValidateBodyOut{}, &mcpserver.InfraError{Msg: "resolve pr template: " + err.Error(), Cause: err}
	}
	issues := prtemplate.ValidateBody(in.Body, tmpl)
	return PRValidateBodyOut{OK: len(issues) == 0, Errors: issues}, nil
}

// ---------------------------------------------------------------------------
// pr_apply (KD14 executor)
// ---------------------------------------------------------------------------

// PRApplyIn is the input for pr_apply.
type PRApplyIn struct {
	Title             string `json:"title"`
	Body              string `json:"body"`
	ReleaseLevel      string `json:"releaseLevel,omitempty"`
	ReleasePreRelease string `json:"releasePreRelease,omitempty"`
	ReleaseNotes      string `json:"releaseNotes,omitempty"`
	// ReleaseSource records who decided ReleaseLevel: "user" (explicit
	// interactive choice), "config" (a project/ship-config default), or
	// "pipeline" (computed deterministically by /ship's version step, not
	// chosen by anyone). Required whenever ReleaseLevel is set — see the
	// releaseSource validation block in prApplyCoreWith.
	ReleaseSource string `json:"releaseSource,omitempty"`
	// AutoMode signals an unattended call (no human available to confirm
	// anything right now — e.g. /ship or /pr run with --auto). Disclosed
	// addition beyond the fact sheet's literal contract example: task 8
	// mentions "detectable via ctx or a new AutoMode bool field" and,
	// since mcpserver.Ctx carries no such signal (see register.go), this
	// mirrors the existing `Auto bool` field convention on CommitFlags
	// (commit.go) / ShipApplyIn (ship.go) rather than inventing a second
	// competing mechanism.
	AutoMode bool `json:"autoMode"`
}

// PRApplyOut is the output for pr_apply.
type PRApplyOut struct {
	URL           string             `json:"url"`
	Created       bool               `json:"created"`
	ReleaseIntent *ReleaseIntentInfo `json:"releaseIntent,omitempty"`
}

// ReleaseIntentInfo carries version metadata computed when releaseLevel is set.
type ReleaseIntentInfo struct {
	Level           string `json:"level"`
	PreRelease      string `json:"preRelease,omitempty"`
	PreviousVersion string `json:"previousVersion"`
	ComputedVersion string `json:"computedVersion"`
	TagName         string `json:"tagName"`
	LabelApplied    string `json:"labelApplied"`
	NotesInBody     bool   `json:"notesInBody"`
}

// prApplyCore creates a PR for the current branch, or edits the existing
// one, mirroring pr.js's detectPrMode collapsed to its two data-driven
// modes (exists → update, else → create). pr.js's --update-force flag has
// no analog here: PRApplyIn's contract carries only Title/Body, so the
// force-update-without-an-existing-PR and always-create modes are not
// reachable — a disclosed narrowing of detectPrMode's full mode matrix.
//
// When releaseLevel is set, version metadata is computed and:
//   - release markers are injected into the PR body,
//   - a "release:<level>[-rc]" label is applied via gh pr edit --add-label,
//   - ReleaseIntent is populated on the output.
//
// DECISIONS:
//   - FetchTags error is discarded (best-effort): tests have no remote, and
//     stale local tags could yield a wrong version in multi-dev setups.
//   - Label-add failure after PR exists returns InfraError (URL is lost).
//     A missing release:* label would silently break downstream release, so
//     failing loud is correct.
//   - tagPrefix defaults to "v" when config absent or empty — no default
//     exists in config.applyVersionDefaults, so this is our call.
//   - RC suffix uses "-rc%d" (no dot), per fact sheet. Pre-existing "-rc.N"
//     tags (dotted) are not counted.
//   - TagList/AllSemverTags are prefix-blind: for custom tagPrefix, the
//     "remote tag" half of max() silently degrades to file-version-only.
//     Not fixable here (gitx.go out of scope).
func prApplyCore(mainRoot, workDir string, in PRApplyIn) (PRApplyOut, error) {
	return prApplyCoreWith(mainRoot, workDir, in, defaultPRRuntime)
}

// prApplyCoreWith is the parameterized form of prApplyCore. All external
// calls go through rt, enabling mock injection in tests.
func prApplyCoreWith(mainRoot, workDir string, in PRApplyIn, rt prRuntime) (PRApplyOut, error) {
	if strings.TrimSpace(in.Title) == "" {
		return PRApplyOut{}, &mcpserver.DomainError{Msg: "title is required"}
	}

	// Validate releaseLevel and releasePreRelease when set.
	if in.ReleaseLevel != "" {
		switch in.ReleaseLevel {
		case "major", "minor", "patch":
			// valid
		default:
			return PRApplyOut{}, &mcpserver.DomainError{Msg: fmt.Sprintf("releaseLevel must be major, minor, or patch, got %q", in.ReleaseLevel)}
		}
	}
	if in.ReleasePreRelease != "" && in.ReleasePreRelease != "rc" {
		return PRApplyOut{}, &mcpserver.DomainError{Msg: fmt.Sprintf("releasePreRelease must be \"rc\" or empty, got %q", in.ReleasePreRelease)}
	}

	// releaseSource provenance gate (task 8). Deterministic, MCP-layer
	// enforcement: the calling LLM must never be able to invent a release
	// level and simply omit/misdeclare where it came from.
	if in.ReleaseLevel != "" {
		switch in.ReleaseSource {
		case "user", "config", "pipeline":
			// valid provenance
		case "":
			return PRApplyOut{}, &mcpserver.DomainError{Msg: "releaseSource is required when releaseLevel is set (must be \"user\", \"config\", or \"pipeline\")"}
		default:
			return PRApplyOut{}, &mcpserver.DomainError{Msg: fmt.Sprintf("releaseSource must be \"user\", \"config\", or \"pipeline\", got %q", in.ReleaseSource)}
		}
		// Auto mode: nothing here can verify whether "user" truly traces
		// back to an explicit human decision made upstream (an
		// AskUserQuestion answer, an explicit --releaseLevel CLI arg) or
		// was simply asserted by the calling LLM to slip past this gate —
		// so auto mode refuses "user" unconditionally. Only "config"
		// (a project/ship-config default) and "pipeline" (computed by
		// /ship's version step, not chosen by anyone) are deterministic
		// enough to trust unattended.
		if in.AutoMode && in.ReleaseSource == "user" {
			return PRApplyOut{}, &mcpserver.DomainError{Msg: "releaseLevel in auto mode must come from config or pipeline, not LLM"}
		}
	}

	// Compute release intent before creating/editing PR, so version errors
	// surface before we touch the remote.
	var intent *ReleaseIntentInfo
	body := stripAttribution(in.Body)
	if in.ReleaseLevel != "" {
		var err error
		intent, err = prReleaseComputeIntentWith(rt, mainRoot, workDir, in.ReleaseLevel, in.ReleasePreRelease)
		if err != nil {
			return PRApplyOut{}, err // already wrapped as Domain/Infra
		}
		body = prReleaseInjectMarkers(body, intent.ComputedVersion, intent.Level, intent.PreRelease, in.ReleaseNotes)
		intent.NotesInBody = in.ReleaseNotes != ""

		// Best-effort: make sure every release:* label exists before
		// prReleaseAddLabelWith below applies one via --add-label. Its
		// return is intentionally discarded — see ensureReleaseLabels' doc
		// comment for why label-creation failure must never block the PR.
		_ = ensureReleaseLabels(rt, workDir)
	}

	meta := rt.ghPRForBranch(workDir)
	if meta.Exists {
		url, err := rt.ghPREdit(workDir, meta.Number, in.Title, body)
		if err != nil {
			if enriched := prEnrichPermissionError(rt, workDir, "gh pr edit", err); enriched != nil {
				return PRApplyOut{}, enriched
			}
			return PRApplyOut{}, &mcpserver.InfraError{Msg: "gh pr edit: " + err.Error(), Cause: err}
		}
		if url == "" {
			url = meta.URL
		}
		if intent != nil {
			if err := prReleaseAddLabelWith(rt, workDir, intent.LabelApplied); err != nil {
				return PRApplyOut{}, err
			}
		}
		return PRApplyOut{URL: url, Created: false, ReleaseIntent: intent}, nil
	}

	url, err := rt.ghPRCreate(workDir, in.Title, body)
	if err != nil {
		if enriched := prEnrichPermissionError(rt, workDir, "gh pr create", err); enriched != nil {
			return PRApplyOut{}, enriched
		}
		return PRApplyOut{}, &mcpserver.InfraError{Msg: "gh pr create: " + err.Error(), Cause: err}
	}
	if intent != nil {
		if err := prReleaseAddLabelWith(rt, workDir, intent.LabelApplied); err != nil {
			return PRApplyOut{}, err
		}
	}
	return PRApplyOut{URL: url, Created: true, ReleaseIntent: intent}, nil
}

// isPermissionError reports whether err's message indicates gh CLI refused
// pr create/pr edit for a permission reason (not a collaborator, a 403
// response, or GitHub's "Resource not accessible" API message) rather than
// some other infra failure (network, rate limit, malformed input) that
// account-switch guidance wouldn't help with.
func isPermissionError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "must be a collaborator") ||
		strings.Contains(msg, "403") ||
		strings.Contains(msg, "Resource not accessible")
}

// prEnrichPermissionError inspects originalErr for a gh CLI permission
// failure from ghPRCreate/ghPREdit and, when found, returns an
// *mcpserver.InfraError carrying account-switch guidance in its Suggestion
// field — the same diagnostics buildAuthDiagnosticsWith/ghx.FormatAccessDenied
// produce for pr_prepare's preflight, now surfaced at the point pr_apply
// actually hits the failure. Returns nil (never a typed-nil interface,
// callers check for that) when originalErr is not a permission error, or
// when enrichment itself cannot proceed (no origin remote, remote URL
// doesn't parse) — callers fall back to their own generic InfraError in
// that case, matching the "falls back to nil" contract used throughout this
// file's other best-effort helpers (e.g. ensureReleaseLabels).
//
// verb is a disclosed addition beyond the fact sheet's literal 3-arg
// contract example: the same helper backs both the ghPRCreate (line ~605)
// and ghPREdit (line ~590) call sites, whose existing generic-InfraError
// messages are "gh pr create: "/"gh pr edit: " respectively — hardcoding
// "gh pr create: " here would mislabel an edit failure.
func prEnrichPermissionError(rt prRuntime, workDir, verb string, originalErr error) error {
	if !isPermissionError(originalErr) {
		return nil
	}

	originURL, err := rt.execRun("git", []string{"remote", "get-url", "origin"}, execx.Options{Dir: workDir})
	if err != nil || strings.TrimSpace(originURL) == "" {
		return nil
	}
	owner, repo, err := ghx.ParseRemoteOwner(originURL)
	if err != nil {
		return nil
	}

	accounts, _ := rt.ghGetAccounts(workDir, "github.com")
	logins := make([]string, 0, len(accounts))
	for _, a := range accounts {
		logins = append(logins, a.Login)
	}

	activeAccount := ""
	if probe := rt.ghAuthProbe(workDir, ""); probe.Authenticated {
		activeAccount = probe.ActiveAccount
	}

	suggestion := ghx.FormatAccessDenied(activeAccount, owner, repo, logins) +
		"\nAfter switching, call pr_apply again with the same arguments."

	return &mcpserver.InfraError{
		Msg:        verb + ": " + originalErr.Error(),
		Suggestion: suggestion,
		Cause:      originalErr,
	}
}

// ---------------------------------------------------------------------------
// Release intent helpers
// ---------------------------------------------------------------------------

// prReleaseComputeIntentWith resolves version metadata from the project's
// version file and git tags. Returns a fully populated ReleaseIntentInfo.
// All external calls go through rt.
func prReleaseComputeIntentWith(rt prRuntime, mainRoot, workDir, level, preRelease string) (*ReleaseIntentInfo, error) {
	// Read config for version section.
	cfg, _ := rt.configRead(mainRoot) // nil config is handled below.

	var tagPrefix string
	var versionFilePath string
	var fileType string
	var isTagMode bool
	var tagEnabled bool
	if cfg != nil && cfg.Version != nil {
		tagPrefix = cfg.Version.Tag.Prefix
		versionFilePath = cfg.Version.VersionFile.Path
		fileType = cfg.Version.VersionFile.FileType
		isTagMode = !cfg.Version.VersionFile.Enabled
		tagEnabled = cfg.Version.Tag.Enabled
	}
	if tagPrefix == "" {
		tagPrefix = "v"
	}

	// Best-effort fetch tags (no remote in tests, CI may time out).
	_ = rt.gitFetchTags(workDir)

	// Find highest released tag to use as bump base.
	tags, err := rt.gitTagList(workDir)
	if err != nil {
		tags = nil // degrade gracefully
	}
	highestTag := prReleaseHighestTagVersion(tags, tagPrefix)

	// Version source: tag mode derives the current version from the
	// highest semver git tag (no version file to detect), mirroring
	// versionPrepareCore's isTagMode handling.
	var fileVersion string
	if isTagMode {
		fileVersion = highestTag
		if fileVersion == "" {
			fileVersion = "0.0.0"
		}
	} else {
		vf, err := rt.versionDetect(mainRoot, versionFilePath, fileType)
		if err != nil {
			return nil, &mcpserver.DomainError{Msg: "version detection: " + err.Error()}
		}
		fileVersion = vf.Version
	}

	// Bump base = max(fileVersion, highestTag), but only when the tag path
	// is enabled — a project not using tags shouldn't have its bump base
	// skewed by stale/irrelevant tag history.
	bumpBase := fileVersion
	if tagEnabled && highestTag != "" && prReleaseSemverGreater(highestTag, bumpBase) {
		bumpBase = highestTag
	}

	// Compute bumped version.
	syntheticVF := &version.VersionFile{Version: bumpBase}
	bumped, err := version.Bump(syntheticVF, level)
	if err != nil {
		return nil, &mcpserver.DomainError{Msg: "version bump: " + err.Error()}
	}

	computedVersion := bumped
	tagName := tagPrefix + bumped

	// RC handling: scan existing RC tags, pick next number.
	if preRelease == "rc" {
		allTags, err := rt.gitAllSemverTags(workDir)
		if err != nil {
			allTags = nil
		}
		rcNum := prReleaseFindNextRC(allTags, tagPrefix, bumped)
		computedVersion = bumped + "-rc" + strconv.Itoa(rcNum)
		tagName = tagPrefix + computedVersion
	}

	// Check tag collision.
	exists, err := rt.gitTagExists(workDir, tagName)
	if err == nil && exists {
		return nil, &mcpserver.DomainError{Msg: fmt.Sprintf("tag %q already exists — version collision", tagName)}
	}

	// Build label.
	label := "release:" + level
	if preRelease == "rc" {
		label += "-rc"
	}

	return &ReleaseIntentInfo{
		Level:           level,
		PreRelease:      preRelease,
		PreviousVersion: fileVersion,
		ComputedVersion: computedVersion,
		TagName:         tagName,
		LabelApplied:    label,
	}, nil
}

// prReleaseHighestTagVersion extracts the highest semver core from a sorted
// tag list (descending), stripping tagPrefix. Returns "" if no valid tag.
func prReleaseHighestTagVersion(tags []string, tagPrefix string) string {
	for _, t := range tags {
		v := t
		if tagPrefix != "" {
			v = strings.TrimPrefix(v, tagPrefix)
		}
		// Also strip bare "v" if prefix is something else.
		v = strings.TrimPrefix(v, "v")
		if _, _, _, ok := prReleaseParseSemverNums(v); ok {
			return v
		}
	}
	return ""
}

// prReleaseSemverGreater returns true when a > b (semver core only).
func prReleaseSemverGreater(a, b string) bool {
	aMaj, aMin, aPat, aOK := prReleaseParseSemverNums(a)
	bMaj, bMin, bPat, bOK := prReleaseParseSemverNums(b)
	if !aOK || !bOK {
		return false
	}
	if aMaj != bMaj {
		return aMaj > bMaj
	}
	if aMin != bMin {
		return aMin > bMin
	}
	return aPat > bPat
}

// prReleaseParseSemverNums parses "1.2.3" into (1,2,3,true). Leading "v"
// is stripped. Pre-release suffixes are ignored (only core is compared).
func prReleaseParseSemverNums(s string) (major, minor, patch int, ok bool) {
	s = strings.TrimPrefix(s, "v")
	// Strip pre-release suffix.
	if idx := strings.IndexByte(s, '-'); idx >= 0 {
		s = s[:idx]
	}
	parts := strings.SplitN(s, ".", 3)
	if len(parts) != 3 {
		return 0, 0, 0, false
	}
	var err error
	major, err = strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, 0, false
	}
	minor, err = strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, 0, false
	}
	patch, err = strconv.Atoi(parts[2])
	if err != nil {
		return 0, 0, 0, false
	}
	return major, minor, patch, true
}

// prReleaseFindNextRC scans existing tags for the highest RC number matching
// the target base version, and returns the next number. Format: -rc<N> (no dot).
func prReleaseFindNextRC(tags []string, tagPrefix, targetBase string) int {
	maxRC := 0
	needle := tagPrefix + targetBase + "-rc"
	for _, t := range tags {
		if !strings.HasPrefix(t, needle) {
			continue
		}
		suffix := t[len(needle):]
		n, err := strconv.Atoi(suffix)
		if err != nil {
			continue
		}
		if n > maxRC {
			maxRC = n
		}
	}
	return maxRC + 1
}

// attributionPatterns matches lines that credit an AI tool as the author of
// a PR body — left behind by an LLM that copied its own commit-message
// footer convention into PR body text. Stripped before the body ever
// reaches GitHub, since a human-facing PR description should read as
// authored by the person who opened it.
var attributionPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?m)^.*Generated with \[Claude Code\].*$`),
	regexp.MustCompile(`(?m)^.*🤖\s*Generated with.*$`),
	regexp.MustCompile(`(?m)^.*Created by Claude.*$`),
	regexp.MustCompile(`(?m)^.*Created with Claude.*$`),
	regexp.MustCompile(`(?m)^.*Co-Authored-By:.*Claude.*$`),
	regexp.MustCompile(`(?m)^.*Co-Authored-By:.*Anthropic.*$`),
	regexp.MustCompile(`(?m)^.*Generated by.*Claude.*$`),
	regexp.MustCompile(`(?m)^.*Powered by.*Claude.*$`),
}

// collapseBlankLines collapses runs of 2+ blank lines left behind by
// attributionPatterns removals down to a single blank line.
var blankLineRun = regexp.MustCompile(`\n{3,}`)

func collapseBlankLines(body string) string {
	return blankLineRun.ReplaceAllString(body, "\n\n")
}

// stripAttribution removes AI-tool attribution lines from a PR body.
func stripAttribution(body string) string {
	for _, pat := range attributionPatterns {
		body = pat.ReplaceAllString(body, "")
	}
	return strings.TrimRight(collapseBlankLines(body), "\n") + "\n"
}

// prReleaseInjectMarkers injects release metadata markers into the PR body.
// The markers use HTML comments so they survive rendering and can be parsed
// by downstream CI tasks.
func prReleaseInjectMarkers(body, computedVersion, level, preRelease, notes string) string {
	// Strip any previous release markers.
	body = prReleaseStripMarkers(body)

	var sb strings.Builder
	sb.WriteString(body)
	if !strings.HasSuffix(body, "\n") && body != "" {
		sb.WriteString("\n")
	}
	sb.WriteString("\n---\n")
	sb.WriteString("<!-- release-level:" + level + " -->\n")
	if preRelease != "" {
		sb.WriteString("<!-- release-pre:" + preRelease + " -->\n")
	}
	sb.WriteString("<!-- release-notes-start -->\n")
	if notes != "" {
		sb.WriteString("## [" + computedVersion + "]\n\n")
		sb.WriteString(notes + "\n")
	}
	sb.WriteString("<!-- release-notes-end -->\n")
	return sb.String()
}

// prReleaseStripMarkers removes previous release markers from the body.
func prReleaseStripMarkers(body string) string {
	// Find the last "---" separator that precedes a release marker.
	idx := strings.LastIndex(body, "\n---\n")
	if idx < 0 {
		return body
	}
	after := body[idx:]
	if strings.Contains(after, "<!-- release-level:") || strings.Contains(after, "<!-- release-notes-start") {
		return strings.TrimRight(body[:idx], "\n")
	}
	return body
}

// releaseLabels enumerates every release:* label pr_apply may need to apply,
// with GitHub label colors (6-hex digits, no leading '#') and descriptions.
// ensureReleaseLabels creates all six up front — not just the one the
// current call needs — for forward-compatibility (a later PR may need a
// different level without re-probing gh). Names must keep matching
// /^release:(major|minor|patch)(-rc)?$/, the regex
// .github/scripts/verify-release-intent.cjs and release-on-main.cjs use to
// recognize a release label.
var releaseLabels = []struct {
	Name  string
	Color string
	Desc  string
}{
	{"release:patch", "0E8A16", "Patch release"},
	{"release:minor", "1D76DB", "Minor release"},
	{"release:major", "D93F0B", "Major release"},
	{"release:patch-rc", "BFD4F2", "Patch release candidate"},
	{"release:minor-rc", "C5DEF5", "Minor release candidate"},
	{"release:major-rc", "FCD8D4", "Major release candidate"},
}

// ensureReleaseLabels creates any releaseLabels entries missing from the
// repo (idempotent — labels gh already lists are skipped, not recreated).
//
// It is best-effort end to end: a failure listing labels (no gh, no auth,
// network) is swallowed and reported as nil, matching the shape of the
// existing FetchTags-is-best-effort precedent in prReleaseComputeIntentWith
// above. A failure creating an individual label does not stop the rest of
// the loop from being attempted, but is returned to the caller for test
// observability — production callers (prApplyCoreWith) discard it
// unconditionally: a missing label here is not fatal because
// prReleaseAddLabelWith's own --add-label call fails loud (InfraError) if
// the label genuinely doesn't exist, which is the actual point where a
// missing label must block the PR.
func ensureReleaseLabels(rt prRuntime, workDir string) error {
	existing, err := rt.ghLabelList(workDir)
	if err != nil {
		return nil
	}

	have := make(map[string]bool, len(existing))
	for _, name := range existing {
		have[name] = true
	}

	var firstErr error
	for _, l := range releaseLabels {
		if have[l.Name] {
			continue
		}
		if createErr := rt.ghLabelCreate(workDir, l.Name, l.Color, l.Desc); createErr != nil && firstErr == nil {
			firstErr = createErr
		}
	}
	return firstErr
}

// prReleaseAddLabelWith applies a label to the current branch's PR via
// gh pr edit --add-label. Uses rt.execRun for the gh CLI call.
func prReleaseAddLabelWith(rt prRuntime, workDir, label string) error {
	_, err := rt.execRun("gh", []string{"pr", "edit", "--add-label", label}, execx.Options{Dir: workDir})
	if err != nil {
		return &mcpserver.InfraError{Msg: "gh pr edit --add-label: " + err.Error(), Cause: err}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Registration
// ---------------------------------------------------------------------------

// RegisterPRTools registers pr_prepare and pr_apply on the server.
// pr_validate_body's logic lives on in prValidateBodyCore below, reused by
// validate's "pr_body" action instead of its own tool registration.
// Registration only — wiring into runMCP's dispatch is Task 40's
// responsibility.
func RegisterPRTools(s *mcpserver.Server) {
	mcpserver.Register(s, "pr_prepare",
		"Preflight checks for pr: config-version gate, gh-auth + active-account probe (with recovery-shaped diagnostics on failure), branch-guard hard gate, protected-branch rejection, JIRA ticket detection from the branch name, and PR template resolution.",
		func(ctx mcpserver.Ctx, in PRPrepareIn) (PRPrepareOut, error) {
			mainRoot, err := worktree.MainRoot()
			if err != nil {
				mainRoot, err = os.Getwd()
				if err != nil {
					return PRPrepareOut{}, &mcpserver.InfraError{Msg: fmt.Sprintf("resolve project root: %s", err.Error()), Cause: err}
				}
			}
			workDir, err := worktree.ActiveRoot()
			if err != nil {
				workDir = mainRoot
			}
			return prPrepareCore(mainRoot, workDir, in)
		},
	)

	mcpserver.Register(s, "pr_apply",
		"Creates a PR for the current branch, or edits the existing one, via gh pr create/gh pr edit (KD14 executor tool). "+
			"When releaseLevel is set, releaseSource is required: \"user\" (explicit interactive choice), \"config\" (project/ship-config default), "+
			"or \"pipeline\" (computed by /ship's version step). In autoMode, releaseSource=\"user\" is always rejected — an unattended caller must "+
			"resolve to \"config\" or \"pipeline\"; never invent a release level yourself and label it \"user\" to bypass this. "+
			"A gh CLI permission error (not a collaborator, 403, Resource not accessible) is enriched with account-switch guidance "+
			"(active account, target owner/repo, candidate accounts to switch to) in the error's suggestion field.",
		func(ctx mcpserver.Ctx, in PRApplyIn) (PRApplyOut, error) {
			mainRoot, err := worktree.MainRoot()
			if err != nil {
				mainRoot, err = os.Getwd()
				if err != nil {
					return PRApplyOut{}, &mcpserver.InfraError{Msg: fmt.Sprintf("resolve project root: %s", err.Error()), Cause: err}
				}
			}
			workDir, err := worktree.ActiveRoot()
			if err != nil {
				workDir = mainRoot
			}
			return prApplyCore(mainRoot, workDir, in)
		},
	)
}
