// pr.go implements the pr skill's MCP tools: pr_prepare (gh-auth +
// config-check + branch-guard + JIRA-detection + template-load preflight),
// pr_validate_body (PR body vs. template section-presence check), and
// pr_apply (KD14 executor: gh pr create/edit for the current branch).
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
// time. See buildAuthDiagnostics.
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

// buildAuthDiagnostics synthesizes the PRAuthDiagnostics block for a
// gh-auth failure. wantLogin, when non-empty, is the login we'd like an
// already-authenticated local account to match (the configured
// expectedAccount for a mismatch, nothing for an access-denial); owner is
// the repo owner (access-denial case only); suggested is a fallback account
// list already gathered by a probe (e.g. RepoAccessResult.SuggestedAccounts)
// used only if GetAccounts itself comes back empty.
func buildAuthDiagnostics(workDir, wantLogin, owner string, suggested []string) *PRAuthDiagnostics {
	// ghx.GetAccounts, unlike ghx.AuthProbe/ghx.RepoAccessProbe, does not
	// default an empty host to "github.com" itself (it looks up the raw
	// `gh auth status --json hosts` map by the exact key given) — default
	// it here so this call is consistent with the other two probes.
	accounts, _ := ghx.GetAccounts(workDir, "github.com")
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
// for testability: mainRoot anchors config/.sdlc state (worktree.MainRoot),
// workDir anchors git/gh operations (worktree.ActiveRoot).
func prPrepareCore(mainRoot, workDir string, in PRPrepareIn) (PRPrepareOut, error) {
	var errs, warnings []string

	// KD5 gate: hard-abort with a minimal errors-only payload on config
	// migration failure, mirroring pr.js's ensureConfigVersion short-circuit
	// (and plan.go/setup.go's own KD5 gate).
	if !in.SkipConfigCheck {
		if err := configmigrate.Verify(mainRoot); err != nil {
			errs = append(errs, fmt.Sprintf("config-version: %s", err.Error()))
			return PRPrepareOut{Errors: errs, NeedsMigration: true}, nil
		}
	}

	// gh-auth + active-account preflight (pr.js issues #234/#380).
	authProbe := ghx.AuthProbe(workDir, "")
	out := PRPrepareOut{
		GHAuthenticated: authProbe.Authenticated,
		ActiveAccount:   authProbe.ActiveAccount,
	}

	prSection, _ := config.ReadSection(mainRoot, "pr")
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
		// wantLogin=="" means buildAuthDiagnostics never finds a match, so it
		// falls through to its own default LoginHint.
		out.Diagnostics = buildAuthDiagnostics(workDir, "", "", nil)
		return out, nil
	}

	accountMismatch := expectedAccount != "" && authProbe.ActiveAccount != "" &&
		!strings.EqualFold(authProbe.ActiveAccount, expectedAccount)
	out.AccountMismatch = accountMismatch
	if accountMismatch {
		errs = append(errs, ghx.FormatAccountMismatch(expectedAccount, authProbe.ActiveAccount))
		out.Errors = errs
		out.Diagnostics = buildAuthDiagnostics(workDir, expectedAccount, "", nil)
		return out, nil
	}

	// Remote owner/repo, best-effort — a missing origin remote is not an
	// error, matching pr.js's parseRemoteOwner() returning null.
	owner, repo, hasRemote := "", "", false
	if originURL, err := execx.Run("git", []string{"remote", "get-url", "origin"}, execx.Options{Dir: workDir}); err == nil && originURL != "" {
		if o, r, parseErr := ghx.ParseRemoteOwner(originURL); parseErr == nil {
			owner, repo, hasRemote = o, r, true
		}
	}

	switch {
	case expectedAccount == "" && hasRemote:
		probe := ghx.RepoAccessProbe(workDir, owner, repo, "")
		out.RepoAccessProbed = true
		out.RepoAccessible = probe.Accessible
		out.RepoAccessStatus = probe.StatusCode
		if probe.Accessible != nil && !*probe.Accessible {
			errs = append(errs, ghx.FormatAccessDenied(authProbe.ActiveAccount, owner, repo, probe.SuggestedAccounts))
			out.Errors = errs
			out.Diagnostics = buildAuthDiagnostics(workDir, "", owner, probe.SuggestedAccounts)
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
	currentBranch, err := gitx.CurrentBranch(workDir)
	if err != nil {
		errs = append(errs, err.Error())
		out.Errors = errs
		out.Warnings = warnings
		return out, nil
	}
	out.CurrentBranch = currentBranch

	if statusRaw, statusErr := gitx.Status(workDir); statusErr == nil {
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
	guard := branch.ValidateExpectedBranch(currentBranch, in.ExpectedBranch)
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
	out.JiraTicket = detectJiraTicket(currentBranch, nil)

	// PR template resolution (shared with task 20's prtemplate package). A
	// resolution failure is non-fatal — it becomes a warning, not an error,
	// since it does not block the rest of the preflight's diagnostic value.
	if tmpl, tmplErr := prtemplate.Resolve(mainRoot); tmplErr != nil {
		warnings = append(warnings, fmt.Sprintf("PR template resolution failed: %s", tmplErr.Error()))
	} else if tmpl != nil {
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
// pr_validate_body
// ---------------------------------------------------------------------------

// PRValidateBodyIn is the input for pr_validate_body.
type PRValidateBodyIn struct {
	Body string `json:"body"`
}

// PRValidateBodyOut is the output for pr_validate_body.
type PRValidateBodyOut struct {
	OK     bool     `json:"ok"`
	Errors []string `json:"errors,omitempty"`
}

// prValidateBodyCore resolves the project's PR template and checks Body
// against it via prtemplate.ValidateBody — the pr SKILL.md's
// section-presence contract, NOT pr.js's real --validate-body link
// validation (that lives in internal/links and is explicitly out of scope
// for this tool per the task's ruling).
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
	Title string `json:"title"`
	Body  string `json:"body"`
}

// PRApplyOut is the output for pr_apply.
type PRApplyOut struct {
	URL     string `json:"url"`
	Created bool   `json:"created"`
}

// prApplyCore creates a PR for the current branch, or edits the existing
// one, mirroring pr.js's detectPrMode collapsed to its two data-driven
// modes (exists → update, else → create). pr.js's --update-force flag has
// no analog here: PRApplyIn's contract carries only Title/Body, so the
// force-update-without-an-existing-PR and always-create modes are not
// reachable — a disclosed narrowing of detectPrMode's full mode matrix.
func prApplyCore(workDir string, in PRApplyIn) (PRApplyOut, error) {
	if strings.TrimSpace(in.Title) == "" {
		return PRApplyOut{}, &mcpserver.DomainError{Msg: "title is required"}
	}

	meta := ghx.PRForBranch(workDir)
	if meta.Exists {
		url, err := ghx.PREdit(workDir, meta.Number, in.Title, in.Body)
		if err != nil {
			return PRApplyOut{}, &mcpserver.InfraError{Msg: "gh pr edit: " + err.Error(), Cause: err}
		}
		if url == "" {
			url = meta.URL
		}
		return PRApplyOut{URL: url, Created: false}, nil
	}

	url, err := ghx.PRCreate(workDir, in.Title, in.Body)
	if err != nil {
		return PRApplyOut{}, &mcpserver.InfraError{Msg: "gh pr create: " + err.Error(), Cause: err}
	}
	return PRApplyOut{URL: url, Created: true}, nil
}

// ---------------------------------------------------------------------------
// Registration
// ---------------------------------------------------------------------------

// RegisterPRTools registers pr_prepare, pr_validate_body, and pr_apply on
// the server. Registration only — wiring into runMCP's dispatch is Task
// 40's responsibility.
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

	mcpserver.Register(s, "pr_validate_body",
		"Validates a PR body against the resolved PR template's section headings (section-presence check per the pr SKILL.md contract — not pr.js's link-validation --validate-body mode).",
		func(ctx mcpserver.Ctx, in PRValidateBodyIn) (PRValidateBodyOut, error) {
			root, err := worktree.MainRoot()
			if err != nil {
				root, err = os.Getwd()
				if err != nil {
					return PRValidateBodyOut{}, &mcpserver.InfraError{Msg: fmt.Sprintf("resolve project root: %s", err.Error()), Cause: err}
				}
			}
			return prValidateBodyCore(root, in)
		},
	)

	mcpserver.Register(s, "pr_apply",
		"Creates a PR for the current branch, or edits the existing one, via gh pr create/gh pr edit (KD14 executor tool).",
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
			return prApplyCore(workDir, in)
		},
	)
}
