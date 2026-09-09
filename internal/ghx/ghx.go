// Package ghx wraps the GitHub CLI (gh) for the operations the SDLC plugin
// needs. All commands go through execx.Run so output capping is inherited
// from the single process-execution chokepoint. Retry is available via
// execx.Retry but is not used by any function in this package; each gh
// invocation is single-shot.
package ghx

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"github.com/rnagrodzki/sdlc-plugin/internal/execx"
)

// ErrGHNotFound is returned when the gh binary cannot be located on PATH.
// Callers can use errors.Is(err, ErrGHNotFound) to distinguish a missing
// tool from other failures.
var ErrGHNotFound = errors.New("ghx: gh CLI not found; install from https://cli.github.com")

// ghCmd is the name of the binary passed to execx.Run. It is a package
// variable so tests can verify error-wrapping behavior without needing a
// real gh installation.
var ghCmd = "gh"

// run executes gh with the given args inside dir and returns trimmed stdout.
// If the error indicates the binary was not found it is wrapped as
// ErrGHNotFound so callers can classify the failure.
func run(dir string, args ...string) (string, error) {
	out, err := execx.Run(ghCmd, args, execx.Options{Dir: dir})
	if err != nil {
		if isBinaryNotFound(err) {
			return "", fmt.Errorf("%w: %w", ErrGHNotFound, err)
		}
		return "", err
	}
	return out, nil
}

// isBinaryNotFound returns true when the error chain indicates that the
// target binary could not be found on PATH.
func isBinaryNotFound(err error) bool {
	if err == nil {
		return false
	}
	// exec.ErrNotFound is set by exec.LookPath / exec.Command when the
	// binary is absent from PATH.
	if errors.Is(err, exec.ErrNotFound) {
		return true
	}
	// The Go standard library embeds "executable file not found" in the
	// error message across platforms.
	return strings.Contains(err.Error(), "executable file not found")
}

// validatePositionalArg rejects positional arguments that start with "-" to
// prevent them being interpreted as flags by the gh CLI.
func validatePositionalArg(arg, context string) error {
	if strings.HasPrefix(arg, "-") {
		return fmt.Errorf("ghx: %s: argument %q looks like a flag (starts with '-')", context, arg)
	}
	return nil
}

// PRView returns the output of `gh pr view <n>` run inside dir.
func PRView(dir string, n int) (string, error) {
	if n <= 0 {
		return "", fmt.Errorf("ghx: PRView: invalid PR number %d", n)
	}
	return run(dir, "pr", "view", fmt.Sprint(n))
}

// PRChecks returns the output of `gh pr checks <n>` run inside dir.
func PRChecks(dir string, n int) (string, error) {
	if n <= 0 {
		return "", fmt.Errorf("ghx: PRChecks: invalid PR number %d", n)
	}
	return run(dir, "pr", "checks", fmt.Sprint(n))
}

// PRChecksWithExitCode behaves like PRChecks but preserves stdout and
// reports the process exit code even on a non-zero exit, since gh pr
// checks' exit code is itself meaningful (0 pass, 1 some failed, 8 some
// pending) — see internal/tools/polling.go's verifyPipelineAwait for why
// that data must not be discarded the way PRChecks discards it.
func PRChecksWithExitCode(dir string, n int) (stdout string, exitCode int, err error) {
	if n <= 0 {
		return "", 0, fmt.Errorf("ghx: PRChecksWithExitCode: invalid PR number %d", n)
	}
	stdout, exitCode, err = execx.RunAllowExit(ghCmd, []string{"pr", "checks", fmt.Sprint(n)}, execx.Options{Dir: dir})
	if err != nil && isBinaryNotFound(err) {
		return "", 0, fmt.Errorf("%w: %w", ErrGHNotFound, err)
	}
	return stdout, exitCode, err
}

// IssueView returns the output of `gh issue view <key>` run inside dir.
func IssueView(dir string, key string) (string, error) {
	if err := validatePositionalArg(key, "IssueView"); err != nil {
		return "", err
	}
	return run(dir, "issue", "view", key)
}

// AuthStatus returns the output of `gh auth status` run inside dir.
func AuthStatus(dir string) (string, error) {
	return run(dir, "auth", "status")
}

// ParseRemoteOwner extracts the owner and repository name from a Git remote
// URL. It handles the three common forms:
//
//   - https://github.com/owner/repo[.git]
//   - git@github.com:owner/repo[.git]
//   - ssh://git@github.com/owner/repo[.git]
func ParseRemoteOwner(rawURL string) (owner, repo string, err error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return "", "", fmt.Errorf("ghx: empty remote URL")
	}

	var path string

	switch {
	case strings.HasPrefix(rawURL, "git@"):
		// git@github.com:owner/repo.git
		// Split on the first colon that separates host from path.
		idx := strings.Index(rawURL, ":")
		if idx < 0 {
			return "", "", fmt.Errorf("ghx: malformed git@ URL: %s", rawURL)
		}
		path = rawURL[idx+1:]

	case strings.HasPrefix(rawURL, "ssh://"):
		// ssh://git@github.com/owner/repo.git
		u, parseErr := url.Parse(rawURL)
		if parseErr != nil {
			return "", "", fmt.Errorf("ghx: malformed ssh URL %q: %w", rawURL, parseErr)
		}
		path = strings.TrimPrefix(u.Path, "/")

	case strings.HasPrefix(rawURL, "https://") || strings.HasPrefix(rawURL, "http://"):
		u, parseErr := url.Parse(rawURL)
		if parseErr != nil {
			return "", "", fmt.Errorf("ghx: malformed URL %q: %w", rawURL, parseErr)
		}
		path = strings.TrimPrefix(u.Path, "/")

	default:
		return "", "", fmt.Errorf("ghx: unsupported remote URL scheme: %s", rawURL)
	}

	// Strip trailing .git suffix.
	path = strings.TrimSuffix(path, ".git")

	parts := strings.SplitN(path, "/", 3)
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("ghx: cannot extract owner/repo from %q", rawURL)
	}

	return parts[0], parts[1], nil
}

// --- gh-auth / account / PR-lookup primitives (Task 25) -------------------
//
// These additions port the account-matching and PR-metadata primitives from
// lib/git.js (probeGhAuth, getGhAccounts, selectAccountForOwner,
// probeRepoAccess, formatAccountMismatch, formatAccessDenied,
// fetchPrMetadata) so pr_prepare/pr_apply can reuse them. They follow run()'s
// existing soft-fail style.
//
// Known fidelity gap: run() (via execx.Run) captures only stdout on success
// and discards ALL output (both streams) on any non-zero exit, returning a
// generic wrapped error with no stderr text. The real `gh auth status` and
// `gh api ... -i` commands rely on inspecting output that is only available
// via a merged stdout+stderr stream (the source uses `2>&1` explicitly), and
// `gh api` signals HTTP 403/404 via a non-zero exit code while still having
// printed the status line callers need. Neither is reproducible through
// run()'s current contract without changing execx.Run itself, which is out
// of scope here. AuthProbe below sidesteps the auth-status half of this gap
// by using `gh api user --jq .login` (a data command, not a human-status
// command) to determine authentication, at the cost of not being able to
// distinguish an expired token from "never logged in". RepoAccessProbe
// cannot sidestep it: a 403/404 response is indistinguishable from a
// network failure and both surface as an unknown (nil) result below.

// Account is one gh CLI account entry for a host, as reported by
// `gh auth status --json hosts`.
type Account struct {
	Login  string
	Active bool
}

// GetAccounts lists the gh accounts logged in to host, mirroring
// lib/git.js's getGhAccounts. Any failure to run or parse `gh auth status
// --json hosts` — including gh not being installed — is treated as a silent
// skip (nil, nil), matching the source's "gh not installed — silent skip"
// comment; only a JSON-parse failure on non-empty output is surfaced as an
// error.
func GetAccounts(dir, host string) ([]Account, error) {
	raw, err := run(dir, "auth", "status", "--json", "hosts")
	if err != nil {
		if errors.Is(err, execx.ErrOutputCap) {
			return nil, fmt.Errorf("ghx: GetAccounts: %w", err)
		}
		return nil, nil
	}
	if raw == "" {
		return nil, nil
	}

	var parsed struct {
		Hosts map[string][]struct {
			State  string `json:"state"`
			Login  string `json:"login"`
			Active bool   `json:"active"`
		} `json:"hosts"`
	}
	if jsonErr := json.Unmarshal([]byte(raw), &parsed); jsonErr != nil {
		return nil, fmt.Errorf("ghx: could not parse gh auth status output: %w", jsonErr)
	}

	entries, ok := parsed.Hosts[host]
	if !ok || len(entries) == 0 {
		return nil, nil
	}

	accounts := make([]Account, 0, len(entries))
	for _, e := range entries {
		if e.State != "success" {
			continue
		}
		accounts = append(accounts, Account{Login: e.Login, Active: e.Active})
	}
	return accounts, nil
}

// SelectAccountForOwner returns the account whose login case-insensitively
// matches owner, or nil if none match. It is a pure port of lib/git.js's
// selectAccountForOwner — no gh invocation.
func SelectAccountForOwner(owner string, accounts []Account) *Account {
	if owner == "" || len(accounts) == 0 {
		return nil
	}
	ownerLower := strings.ToLower(owner)
	for _, a := range accounts {
		if strings.ToLower(a.Login) == ownerLower {
			match := a
			return &match
		}
	}
	return nil
}

// AuthSwitch runs `gh auth switch --user <login>`, mirroring the switch step
// of lib/git.js's ensureGhAccount. It is exposed as its own function so
// callers control whether/when to perform the (persistent, global) switch;
// no ghx function switches accounts implicitly.
func AuthSwitch(dir, login string) error {
	_, err := run(dir, "auth", "switch", "--user", login)
	return err
}

// AuthProbeResult reports gh's authentication state for a host. See the
// package-level fidelity-gap note above for why Expired is not available.
type AuthProbeResult struct {
	Authenticated bool
	ActiveAccount string
	ErrorMessage  string
}

// AuthProbe reports whether gh is authenticated to host, mirroring
// lib/git.js's probeGhAuth's authenticated/activeAccount fields. It
// determines authentication via `gh api user --jq .login` rather than
// text-matching `gh auth status` (see fidelity-gap note above), so it
// cannot distinguish an expired token from never having logged in; both
// report Authenticated:false with a generic login hint.
func AuthProbe(dir, host string) AuthProbeResult {
	if host == "" {
		host = "github.com"
	}
	login, err := run(dir, "api", "user", "--jq", ".login", "--hostname", host)
	if err != nil {
		if errors.Is(err, execx.ErrOutputCap) {
			return AuthProbeResult{
				ErrorMessage: fmt.Sprintf("gh api user output exceeded cap: %s", err.Error()),
			}
		}
		return AuthProbeResult{
			ErrorMessage: fmt.Sprintf("Not logged in to %s. Run: gh auth login --hostname %s", host, host),
		}
	}
	if login == "" {
		return AuthProbeResult{
			ErrorMessage: fmt.Sprintf("Not logged in to %s. Run: gh auth login --hostname %s", host, host),
		}
	}
	return AuthProbeResult{Authenticated: true, ActiveAccount: login}
}

// httpStatusLineRe matches the first line of `gh api -i` output, e.g.
// "HTTP/2.0 200" or "HTTP/1.1 404 Not Found".
var httpStatusLineRe = regexp.MustCompile(`HTTP/[\d.]+ (\d{3})`)

// RepoAccessResult reports whether the active gh account can access a repo.
// Accessible/StatusCode are nil when the probe could not determine an
// answer (see fidelity-gap note above) — never guessed.
type RepoAccessResult struct {
	Accessible        *bool
	StatusCode        *int
	ErrorMessage      string
	SuggestedAccounts []string
}

// RepoAccessProbe probes whether the active gh account can access
// owner/repo on host, mirroring lib/git.js's probeRepoAccess for the
// success (200) case. Because run() discards output on any non-zero exit
// (see fidelity-gap note above) and `gh api` exits non-zero for 403/404
// responses, a denied-access response is indistinguishable here from a
// network failure: both report Accessible:nil, not Accessible:false.
func RepoAccessProbe(dir, owner, repo, host string) RepoAccessResult {
	if host == "" {
		host = "github.com"
	}
	if err := validatePositionalArg(owner, "RepoAccessProbe owner"); err != nil {
		return RepoAccessResult{ErrorMessage: err.Error()}
	}
	if err := validatePositionalArg(repo, "RepoAccessProbe repo"); err != nil {
		return RepoAccessResult{ErrorMessage: err.Error()}
	}

	accounts, acctErr := GetAccounts(dir, host)
	if acctErr != nil {
		return RepoAccessResult{ErrorMessage: fmt.Sprintf("GetAccounts: %s", acctErr.Error())}
	}
	logins := make([]string, 0, len(accounts))
	for _, a := range accounts {
		logins = append(logins, a.Login)
	}

	raw, err := run(dir, "api", fmt.Sprintf("repos/%s/%s", owner, repo), "--hostname", host, "-i", "--silent")
	if err != nil {
		if errors.Is(err, execx.ErrOutputCap) {
			return RepoAccessResult{
				ErrorMessage:      fmt.Sprintf("gh api output exceeded cap: %s", err.Error()),
				SuggestedAccounts: logins,
			}
		}
		return RepoAccessResult{
			ErrorMessage:      "gh api returned no output",
			SuggestedAccounts: logins,
		}
	}
	if raw == "" {
		return RepoAccessResult{
			ErrorMessage:      "gh api returned no output",
			SuggestedAccounts: logins,
		}
	}

	firstLine := strings.SplitN(raw, "\n", 2)[0]
	m := httpStatusLineRe.FindStringSubmatch(firstLine)
	if m == nil {
		return RepoAccessResult{
			ErrorMessage:      fmt.Sprintf("unexpected gh api output: %s", firstLine),
			SuggestedAccounts: logins,
		}
	}

	code, _ := strconv.Atoi(m[1])
	accessible := code == 200
	return RepoAccessResult{Accessible: &accessible, StatusCode: &code, SuggestedAccounts: logins}
}

// FormatAccountMismatch renders the canonical 3-line account-mismatch
// message, mirroring lib/git.js's formatAccountMismatch.
func FormatAccountMismatch(expected, actual string) string {
	return strings.Join([]string{
		fmt.Sprintf("Expected gh account: %s", expected),
		fmt.Sprintf("Active gh account:   %s", actual),
		fmt.Sprintf("Run: gh auth switch --user %s", expected),
	}, "\n")
}

// FormatAccessDenied renders the canonical access-denied message, mirroring
// lib/git.js's formatAccessDenied.
func FormatAccessDenied(activeAccount, owner, repo string, suggestedAccounts []string) string {
	lines := []string{
		fmt.Sprintf("Active gh account: %s", activeAccount),
		fmt.Sprintf("Cannot access: %s/%s", owner, repo),
	}
	if len(suggestedAccounts) > 0 {
		for _, login := range suggestedAccounts {
			lines = append(lines, fmt.Sprintf("Try: gh auth switch --user %s", login))
		}
	} else {
		lines = append(lines, "Run: gh auth login --hostname github.com")
	}
	return strings.Join(lines, "\n")
}

// PRMetadata describes the pull request (if any) for the current branch.
type PRMetadata struct {
	Exists       bool
	Number       int
	Title        string
	URL          string
	State        string
	Labels       []string
	ErrorMessage string // Non-empty when the probe failed for a reason other than "no PR exists".
}

// PRForBranch reports the PR for the current branch (no PR number needed),
// mirroring lib/git.js's fetchPrMetadata. It never returns a Go error: any
// failure — no PR found for the branch, not authenticated, network error,
// or malformed JSON — collapses to PRMetadata{Exists: false}, matching the
// source's own `if (!prJson) return { exists: false }` / catch-all
// behavior verbatim.
func PRForBranch(dir string) PRMetadata {
	raw, err := run(dir, "pr", "view", "--json", "number,title,url,state,labels")
	if err != nil {
		if errors.Is(err, execx.ErrOutputCap) {
			return PRMetadata{Exists: false, ErrorMessage: fmt.Sprintf("gh pr view output exceeded cap: %s", err.Error())}
		}
		return PRMetadata{Exists: false}
	}
	if raw == "" {
		return PRMetadata{Exists: false}
	}

	var parsed struct {
		Number int    `json:"number"`
		Title  string `json:"title"`
		URL    string `json:"url"`
		State  string `json:"state"`
		Labels []struct {
			Name string `json:"name"`
		} `json:"labels"`
	}
	if jsonErr := json.Unmarshal([]byte(raw), &parsed); jsonErr != nil {
		return PRMetadata{Exists: false}
	}

	labels := make([]string, 0, len(parsed.Labels))
	for _, l := range parsed.Labels {
		labels = append(labels, l.Name)
	}
	return PRMetadata{
		Exists: true,
		Number: parsed.Number,
		Title:  parsed.Title,
		URL:    parsed.URL,
		State:  parsed.State,
		Labels: labels,
	}
}

// PRCreate runs `gh pr create --title <title> --body <body>` and returns
// the created PR's URL (gh's stdout on success).
func PRCreate(dir, title, body string) (string, error) {
	return run(dir, "pr", "create", "--title", title, "--body", body)
}

// LabelList returns the names of every label defined on the repo, via
// `gh label list --json name --limit 200`. Callers needing to know whether
// a specific release:* label already exists (Task 6's ensureReleaseLabels)
// use this to avoid recreating labels that are already present.
func LabelList(dir string) ([]string, error) {
	raw, err := run(dir, "label", "list", "--json", "name", "--limit", "200")
	if err != nil {
		return nil, err
	}
	if raw == "" {
		return nil, nil
	}

	var parsed []struct {
		Name string `json:"name"`
	}
	if jsonErr := json.Unmarshal([]byte(raw), &parsed); jsonErr != nil {
		return nil, fmt.Errorf("ghx: could not parse gh label list output: %w", jsonErr)
	}

	names := make([]string, 0, len(parsed))
	for _, p := range parsed {
		names = append(names, p.Name)
	}
	return names, nil
}

// LabelCreate runs `gh label create <name> --color <color> --description
// <description>` to create a new repo label.
func LabelCreate(dir, name, color, description string) error {
	if err := validatePositionalArg(name, "LabelCreate"); err != nil {
		return err
	}
	_, err := run(dir, "label", "create", name, "--color", color, "--description", description)
	return err
}

// PREdit runs `gh pr edit <number> --title <title> --body <body>` and
// returns gh's stdout (the PR URL) on success.
func PREdit(dir string, number int, title, body string) (string, error) {
	if number <= 0 {
		return "", fmt.Errorf("ghx: PREdit: invalid PR number %d", number)
	}
	return run(dir, "pr", "edit", fmt.Sprint(number), "--title", title, "--body", body)
}
