// Package links validates URLs by class: GitHub issue/PR links, Atlassian
// Jira browse links, and generic HTTP(S) URLs. It ports
// scripts/lib/links.js to Go.
//
// Three URL classes:
//  1. github.com/<owner>/<repo>/(issues|pull)/<n> — identity check against
//     the current repo remote, then existence check via gh CLI.
//  2. *.atlassian.net/browse/<KEY-N> — host match against Jira cache site.
//  3. Any other http(s):// URL — HEAD with 5s timeout, GET fallback on
//     405/501.
//
// Skip-list hosts (linkedin, x, twitter, medium) are reported as "skipped".
// Offline mode short-circuits network-dependent checks.
package links

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/execx"
	"github.com/rnagrodzki/sdlc-plugin/internal/ghx"
)

// ── Types ──────────────────────────────────────────────────────────────────

// Ctx carries validation context.
type Ctx struct {
	// Offline skips network-dependent checks (generic reachability, GitHub
	// existence via gh CLI). Structural checks (identity match, Atlassian
	// host match) still run.
	Offline bool
	// JiraCacheDir overrides the default ~/.sdlc-cache/jira directory used
	// to discover the cached Jira site host.
	JiraCacheDir string
	// RepoDir is the directory of the git repo, used to resolve the
	// expected owner/repo for GitHub identity checks.
	RepoDir string
}

// Result is the outcome of validating a single URL.
type Result struct {
	URL    string // the input URL
	Status string // "ok", "violation", "skipped"
	Reason string // e.g. "github-not-found", "skip-list", "offline", ""
	Detail string // human-readable detail, may be empty
}

// ── Built-in skip hosts ────────────────────────────────────────────────────

var builtInSkipHosts = map[string]bool{
	"linkedin.com":     true,
	"www.linkedin.com": true,
	"x.com":            true,
	"www.x.com":        true,
	"twitter.com":      true,
	"www.twitter.com":  true,
	"medium.com":       true,
	"www.medium.com":   true,
}

// ── Classification regexes ─────────────────────────────────────────────────

// githubPathRe matches /<owner>/<repo>/(issues|pull)/<number>.
var githubPathRe = regexp.MustCompile(`^/([^/]+)/([^/]+)/(issues|pull)/(\d+)\b`)

// atlassianBrowseRe matches /browse/<KEY-N> using the links.js pattern.
var atlassianBrowseRe = regexp.MustCompile(`^/browse/([A-Z][A-Z0-9_]+-\d+)\b`)

// ── Internal classification ────────────────────────────────────────────────

type urlClass int

const (
	classInvalid urlClass = iota
	classGitHub
	classAtlassian
	classGeneric
)

type classified struct {
	class  urlClass
	parsed *url.URL
	host   string
	// GitHub-specific
	owner  string
	repo   string
	ghType string // "issues" or "pull"
	number string
	// Atlassian-specific
	key string
}

func classify(rawURL string) classified {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return classified{class: classInvalid}
	}
	host := strings.ToLower(u.Hostname())

	// GitHub issues/PRs
	if host == "github.com" || host == "www.github.com" {
		if m := githubPathRe.FindStringSubmatch(u.Path); m != nil {
			return classified{
				class:  classGitHub,
				parsed: u,
				host:   host,
				owner:  m[1],
				repo:   m[2],
				ghType: m[3],
				number: m[4],
			}
		}
	}

	// Atlassian Jira browse links
	if strings.HasSuffix(host, ".atlassian.net") {
		if m := atlassianBrowseRe.FindStringSubmatch(u.Path); m != nil {
			return classified{
				class:  classAtlassian,
				parsed: u,
				host:   host,
				key:    m[1],
			}
		}
	}

	return classified{
		class:  classGeneric,
		parsed: u,
		host:   host,
	}
}

// ── GitHub checks ──────────────────────────────────────────────────────────

type expectedRepo struct {
	owner string
	repo  string
}

// resolveExpectedRepo attempts to determine the expected owner/repo from
// the git remote in dir. Returns nil when the remote cannot be resolved.
func resolveExpectedRepo(dir string) *expectedRepo {
	if dir == "" {
		return nil
	}
	out, err := execx.Run("git", []string{"remote", "get-url", "origin"}, execx.Options{Dir: dir})
	if err != nil {
		return nil
	}
	owner, repo, err := ghx.ParseRemoteOwner(out)
	if err != nil {
		return nil
	}
	return &expectedRepo{owner: owner, repo: repo}
}

// ghViewExists shells to gh to check whether an issue/PR exists on a
// specific owner/repo. It mirrors links.js ghViewExists.
func ghViewExists(owner, repo, ghType, number string) (bool, string) {
	cmd := "issue"
	if ghType == "pull" {
		cmd = "pr"
	}
	_, err := execx.Run("gh", []string{
		cmd, "view", number,
		"-R", owner + "/" + repo,
		"--json", "number",
	}, execx.Options{})
	if err != nil {
		return false, err.Error()
	}
	return true, ""
}

func checkGitHub(c classified, expected *expectedRepo, offline bool) Result {
	if expected == nil {
		// No expected repo — identity unknown.
		if offline {
			return Result{URL: "", Status: "ok"}
		}
		ok, detail := ghViewExists(c.owner, c.repo, c.ghType, c.number)
		if ok {
			return Result{URL: "", Status: "ok"}
		}
		return Result{URL: "", Status: "violation", Reason: "github-not-found", Detail: detail}
	}

	// Identity check — case-insensitive.
	if !strings.EqualFold(c.owner, expected.owner) || !strings.EqualFold(c.repo, expected.repo) {
		return Result{
			URL:    "",
			Status: "violation",
			Reason: "github-context-mismatch",
			Detail: fmt.Sprintf("observed %s/%s, expected %s/%s", c.owner, c.repo, expected.owner, expected.repo),
		}
	}

	if offline {
		return Result{URL: "", Status: "ok"}
	}

	ok, detail := ghViewExists(c.owner, c.repo, c.ghType, c.number)
	if ok {
		return Result{URL: "", Status: "ok"}
	}
	return Result{URL: "", Status: "violation", Reason: "github-not-found", Detail: detail}
}

// ── Atlassian checks ───────────────────────────────────────────────────────

type jiraSiteDiscovery struct {
	site      string // hostname (dots restored)
	ambiguous bool
}

// discoverJiraSiteFromCache reads the Jira cache directory to find a
// single cached site host. Mirrors links.js discoverJiraSiteFromCache.
func discoverJiraSiteFromCache(cacheDir string) jiraSiteDiscovery {
	if cacheDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return jiraSiteDiscovery{}
		}
		cacheDir = filepath.Join(home, ".sdlc-cache", "jira")
	}
	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		return jiraSiteDiscovery{}
	}
	var dirs []string
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, e.Name())
		}
	}
	if len(dirs) == 0 {
		return jiraSiteDiscovery{}
	}
	if len(dirs) > 1 {
		return jiraSiteDiscovery{ambiguous: true}
	}
	// Sanitized site host: underscore restored to dot.
	hostname := strings.ReplaceAll(dirs[0], "_", ".")
	return jiraSiteDiscovery{site: hostname}
}

func checkAtlassian(c classified, ctx Ctx) Result {
	jiraSite := "" // will hold the expected full site URL or hostname

	// Try to determine the expected host.
	discovered := discoverJiraSiteFromCache(ctx.JiraCacheDir)
	if discovered.ambiguous {
		return Result{
			URL:    "",
			Status: "violation",
			Reason: "atlassian-site-ambiguous",
			Detail: "Multiple sites cached in ~/.sdlc-cache/jira/; pass JiraCacheDir to disambiguate.",
		}
	}
	if discovered.site != "" {
		jiraSite = discovered.site
	}

	if jiraSite == "" {
		return Result{
			URL:    "",
			Status: "violation",
			Reason: "atlassian-site-mismatch",
			Detail: fmt.Sprintf("observed %s, expected <none>", c.host),
		}
	}

	// Normalize the expected host — it might be a full URL or just a hostname.
	expectedHost := strings.ToLower(jiraSite)
	if strings.Contains(expectedHost, "://") {
		u, err := url.Parse(expectedHost)
		if err != nil {
			return Result{
				URL:    "",
				Status: "violation",
				Reason: "atlassian-site-mismatch",
				Detail: fmt.Sprintf("invalid jiraSite: %s", jiraSite),
			}
		}
		expectedHost = strings.ToLower(u.Hostname())
	}

	if c.host != expectedHost {
		return Result{
			URL:    "",
			Status: "violation",
			Reason: "atlassian-site-mismatch",
			Detail: fmt.Sprintf("observed %s, expected %s", c.host, expectedHost),
		}
	}

	return Result{URL: "", Status: "ok"}
}

// ── Generic checks ─────────────────────────────────────────────────────────

// httpClient is the HTTP client used for generic URL reachability checks.
// It is a package variable so tests can replace it.
var httpClient = &http.Client{
	Timeout: 5 * time.Second,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return http.ErrUseLastResponse
		}
		return nil
	},
}

func checkGeneric(c classified, offline bool) Result {
	// Skip-list check comes before offline (matches JS ordering).
	if builtInSkipHosts[c.host] {
		return Result{URL: "", Status: "skipped", Reason: "skip-list"}
	}
	if offline {
		return Result{URL: "", Status: "skipped", Reason: "offline"}
	}

	rawURL := c.parsed.String()

	req, err := http.NewRequest(http.MethodHead, rawURL, nil)
	if err != nil {
		return Result{URL: "", Status: "violation", Reason: "url-unreachable", Detail: err.Error()}
	}
	req.Header.Set("User-Agent", "sdlc-links-validator/1.0")

	resp, err := httpClient.Do(req)
	if err != nil {
		return Result{URL: "", Status: "violation", Reason: "url-unreachable", Detail: err.Error()}
	}
	resp.Body.Close()

	// Retry with GET on 405 (Method Not Allowed) or 501 (Not Implemented).
	if resp.StatusCode == 405 || resp.StatusCode == 501 {
		req2, err2 := http.NewRequest(http.MethodGet, rawURL, nil)
		if err2 != nil {
			return Result{URL: "", Status: "violation", Reason: "url-unreachable", Detail: err2.Error()}
		}
		req2.Header.Set("User-Agent", "sdlc-links-validator/1.0")
		resp2, err2 := httpClient.Do(req2)
		if err2 != nil {
			return Result{URL: "", Status: "violation", Reason: "url-unreachable", Detail: err2.Error()}
		}
		resp2.Body.Close()
		resp = resp2
	}

	if resp.StatusCode >= 200 && resp.StatusCode < 400 {
		return Result{URL: "", Status: "ok"}
	}
	if resp.StatusCode >= 400 && resp.StatusCode < 500 {
		return Result{URL: "", Status: "violation", Reason: "url-not-found", Detail: fmt.Sprintf("HTTP %d", resp.StatusCode)}
	}
	return Result{URL: "", Status: "violation", Reason: "url-server-error", Detail: fmt.Sprintf("HTTP %d", resp.StatusCode)}
}

// ── Public API ─────────────────────────────────────────────────────────────

// Validate classifies and validates each URL in urls, returning one Result
// per input URL in the same order.
func Validate(ctx Ctx, urls []string) []Result {
	results := make([]Result, len(urls))

	// Resolve expected repo once for all GitHub URLs.
	var expected *expectedRepo
	expectedResolved := false

	for i, rawURL := range urls {
		c := classify(rawURL)

		switch c.class {
		case classInvalid:
			results[i] = Result{URL: rawURL, Status: "violation", Reason: "url-invalid"}

		case classGitHub:
			if !expectedResolved {
				expected = resolveExpectedRepo(ctx.RepoDir)
				expectedResolved = true
			}
			results[i] = checkGitHub(c, expected, ctx.Offline)
			results[i].URL = rawURL

		case classAtlassian:
			results[i] = checkAtlassian(c, ctx)
			results[i].URL = rawURL

		case classGeneric:
			results[i] = checkGeneric(c, ctx.Offline)
			results[i].URL = rawURL
		}
	}

	return results
}
