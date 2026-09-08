package links

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// ── Classification unit tests ──────────────────────────────────────────────

func TestClassify(t *testing.T) {
	tests := []struct {
		name      string
		url       string
		wantClass urlClass
		wantOwner string
		wantRepo  string
		wantType  string
		wantNum   string
		wantKey   string
		wantHost  string
	}{
		{
			name:      "github issue",
			url:       "https://github.com/acme/widgets/issues/42",
			wantClass: classGitHub,
			wantOwner: "acme",
			wantRepo:  "widgets",
			wantType:  "issues",
			wantNum:   "42",
		},
		{
			name:      "github PR",
			url:       "https://github.com/acme/widgets/pull/7",
			wantClass: classGitHub,
			wantOwner: "acme",
			wantRepo:  "widgets",
			wantType:  "pull",
			wantNum:   "7",
		},
		{
			name:      "github non-issue path",
			url:       "https://github.com/acme/widgets/blob/main/README.md",
			wantClass: classGeneric,
			wantHost:  "github.com",
		},
		{
			name:      "atlassian browse",
			url:       "https://acme.atlassian.net/browse/PROJ-123",
			wantClass: classAtlassian,
			wantKey:   "PROJ-123",
			wantHost:  "acme.atlassian.net",
		},
		{
			name:      "atlassian non-browse path",
			url:       "https://acme.atlassian.net/wiki/spaces",
			wantClass: classGeneric,
			wantHost:  "acme.atlassian.net",
		},
		{
			name:      "generic URL",
			url:       "https://example.com/page",
			wantClass: classGeneric,
			wantHost:  "example.com",
		},
		{
			name:      "invalid URL",
			url:       "not-a-url",
			wantClass: classInvalid,
		},
		{
			name:      "ftp scheme",
			url:       "ftp://files.example.com/data",
			wantClass: classInvalid,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := classify(tt.url)
			if c.class != tt.wantClass {
				t.Fatalf("class: got %d, want %d", c.class, tt.wantClass)
			}
			if tt.wantOwner != "" && c.owner != tt.wantOwner {
				t.Errorf("owner: got %q, want %q", c.owner, tt.wantOwner)
			}
			if tt.wantRepo != "" && c.repo != tt.wantRepo {
				t.Errorf("repo: got %q, want %q", c.repo, tt.wantRepo)
			}
			if tt.wantType != "" && c.ghType != tt.wantType {
				t.Errorf("ghType: got %q, want %q", c.ghType, tt.wantType)
			}
			if tt.wantNum != "" && c.number != tt.wantNum {
				t.Errorf("number: got %q, want %q", c.number, tt.wantNum)
			}
			if tt.wantKey != "" && c.key != tt.wantKey {
				t.Errorf("key: got %q, want %q", c.key, tt.wantKey)
			}
			if tt.wantHost != "" && c.host != tt.wantHost {
				t.Errorf("host: got %q, want %q", c.host, tt.wantHost)
			}
		})
	}
}

// ── Atlassian cache discovery ──────────────────────────────────────────────

func TestDiscoverJiraSiteFromCache(t *testing.T) {
	t.Run("single site", func(t *testing.T) {
		dir := t.TempDir()
		os.Mkdir(filepath.Join(dir, "acme_atlassian_net"), 0o755)
		d := discoverJiraSiteFromCache(dir)
		if d.site != "acme.atlassian.net" {
			t.Errorf("site: got %q, want %q", d.site, "acme.atlassian.net")
		}
		if d.ambiguous {
			t.Error("expected ambiguous=false")
		}
	})

	t.Run("multiple sites", func(t *testing.T) {
		dir := t.TempDir()
		os.Mkdir(filepath.Join(dir, "acme_atlassian_net"), 0o755)
		os.Mkdir(filepath.Join(dir, "other_atlassian_net"), 0o755)
		d := discoverJiraSiteFromCache(dir)
		if !d.ambiguous {
			t.Error("expected ambiguous=true")
		}
	})

	t.Run("empty dir", func(t *testing.T) {
		dir := t.TempDir()
		d := discoverJiraSiteFromCache(dir)
		if d.site != "" {
			t.Errorf("site: got %q, want empty", d.site)
		}
		if d.ambiguous {
			t.Error("expected ambiguous=false")
		}
	})

	t.Run("nonexistent dir", func(t *testing.T) {
		d := discoverJiraSiteFromCache("/nonexistent/path/that/does/not/exist")
		if d.site != "" {
			t.Errorf("site: got %q, want empty", d.site)
		}
	})
}

// ── Validate table test (offline mode — no network) ────────────────────────

func TestValidate_Offline(t *testing.T) {
	// Set up a Jira cache dir with one site.
	cacheDir := t.TempDir()
	os.Mkdir(filepath.Join(cacheDir, "acme_atlassian_net"), 0o755)

	// Set up a git repo with a remote to test identity matching.
	repoDir := t.TempDir()
	gitInit(t, repoDir, "https://github.com/acme/widgets.git")

	ctx := Ctx{
		Offline:      true,
		JiraCacheDir: cacheDir,
		RepoDir:      repoDir,
	}

	tests := []struct {
		name       string
		url        string
		wantStatus string
		wantReason string
	}{
		// GitHub — identity match, offline => ok
		{
			name:       "github matching repo offline",
			url:        "https://github.com/acme/widgets/issues/1",
			wantStatus: "ok",
		},
		// GitHub — identity mismatch, structural => violation
		{
			name:       "github mismatched repo offline",
			url:        "https://github.com/other/repo/pull/5",
			wantStatus: "violation",
			wantReason: "github-context-mismatch",
		},
		// Atlassian — host match => ok
		{
			name:       "atlassian matching site",
			url:        "https://acme.atlassian.net/browse/PROJ-42",
			wantStatus: "ok",
		},
		// Atlassian — host mismatch => violation
		{
			name:       "atlassian mismatched site",
			url:        "https://other.atlassian.net/browse/PROJ-42",
			wantStatus: "violation",
			wantReason: "atlassian-site-mismatch",
		},
		// Skip-list hosts
		{
			name:       "linkedin skipped",
			url:        "https://linkedin.com/in/someone",
			wantStatus: "skipped",
			wantReason: "skip-list",
		},
		{
			name:       "www.linkedin.com skipped",
			url:        "https://www.linkedin.com/in/someone",
			wantStatus: "skipped",
			wantReason: "skip-list",
		},
		{
			name:       "x.com skipped",
			url:        "https://x.com/user",
			wantStatus: "skipped",
			wantReason: "skip-list",
		},
		{
			name:       "twitter.com skipped",
			url:        "https://twitter.com/user",
			wantStatus: "skipped",
			wantReason: "skip-list",
		},
		{
			name:       "medium.com skipped",
			url:        "https://medium.com/article",
			wantStatus: "skipped",
			wantReason: "skip-list",
		},
		// Generic — offline => skipped
		{
			name:       "generic offline skipped",
			url:        "https://example.com/page",
			wantStatus: "skipped",
			wantReason: "offline",
		},
		// Invalid URL
		{
			name:       "invalid URL",
			url:        "not-a-url",
			wantStatus: "violation",
			wantReason: "url-invalid",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results := Validate(ctx, []string{tt.url})
			if len(results) != 1 {
				t.Fatalf("expected 1 result, got %d", len(results))
			}
			r := results[0]
			if r.URL != tt.url {
				t.Errorf("URL: got %q, want %q", r.URL, tt.url)
			}
			if r.Status != tt.wantStatus {
				t.Errorf("Status: got %q, want %q (reason=%q detail=%q)", r.Status, tt.wantStatus, r.Reason, r.Detail)
			}
			if tt.wantReason != "" && r.Reason != tt.wantReason {
				t.Errorf("Reason: got %q, want %q", r.Reason, tt.wantReason)
			}
		})
	}
}

// TestValidate_SkipListBeforeOffline verifies that skip-list check happens
// before the offline check: a linkedin URL while offline yields "skip-list",
// not "offline".
func TestValidate_SkipListBeforeOffline(t *testing.T) {
	results := Validate(Ctx{Offline: true}, []string{"https://linkedin.com/in/someone"})
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Reason != "skip-list" {
		t.Errorf("expected reason 'skip-list', got %q", results[0].Reason)
	}
}

// ── Generic URL checks with httptest ───────────────────────────────────────

func TestValidate_GenericHTTP(t *testing.T) {
	// Save and restore the package-level httpClient.
	origClient := httpClient
	defer func() { httpClient = origClient }()

	tests := []struct {
		name       string
		handler    http.HandlerFunc
		wantStatus string
		wantReason string
	}{
		{
			name:       "HEAD 200",
			handler:    func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) },
			wantStatus: "ok",
		},
		{
			name: "HEAD 405 then GET 200",
			handler: func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodHead {
					w.WriteHeader(405)
					return
				}
				w.WriteHeader(200)
			},
			wantStatus: "ok",
		},
		{
			name: "HEAD 501 then GET 200",
			handler: func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodHead {
					w.WriteHeader(501)
					return
				}
				w.WriteHeader(200)
			},
			wantStatus: "ok",
		},
		{
			name:       "404",
			handler:    func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(404) },
			wantStatus: "violation",
			wantReason: "url-not-found",
		},
		{
			name:       "500",
			handler:    func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) },
			wantStatus: "violation",
			wantReason: "url-server-error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(tt.handler)
			defer srv.Close()

			httpClient = srv.Client()

			results := Validate(Ctx{}, []string{srv.URL + "/test"})
			if len(results) != 1 {
				t.Fatalf("expected 1 result, got %d", len(results))
			}
			r := results[0]
			if r.Status != tt.wantStatus {
				t.Errorf("Status: got %q, want %q (reason=%q detail=%q)", r.Status, tt.wantStatus, r.Reason, r.Detail)
			}
			if tt.wantReason != "" && r.Reason != tt.wantReason {
				t.Errorf("Reason: got %q, want %q", r.Reason, tt.wantReason)
			}
		})
	}
}

func TestValidate_GenericUnreachable(t *testing.T) {
	// Save and restore the package-level httpClient.
	origClient := httpClient
	defer func() { httpClient = origClient }()

	// Start a server and immediately close it to get a port that refuses.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	closedURL := srv.URL + "/test"
	srv.Close()

	httpClient = &http.Client{Timeout: 1 * time.Second}

	results := Validate(Ctx{}, []string{closedURL})
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	r := results[0]
	if r.Status != "violation" {
		t.Errorf("Status: got %q, want 'violation'", r.Status)
	}
	if r.Reason != "url-unreachable" {
		t.Errorf("Reason: got %q, want 'url-unreachable'", r.Reason)
	}
}

// ── Atlassian ambiguous cache ──────────────────────────────────────────────

func TestValidate_AtlassianAmbiguous(t *testing.T) {
	cacheDir := t.TempDir()
	os.Mkdir(filepath.Join(cacheDir, "acme_atlassian_net"), 0o755)
	os.Mkdir(filepath.Join(cacheDir, "other_atlassian_net"), 0o755)

	results := Validate(Ctx{JiraCacheDir: cacheDir}, []string{
		"https://acme.atlassian.net/browse/PROJ-1",
	})
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Reason != "atlassian-site-ambiguous" {
		t.Errorf("Reason: got %q, want 'atlassian-site-ambiguous'", results[0].Reason)
	}
}

// TestValidate_AtlassianNoCache verifies that when no cache exists, the
// result is a mismatch violation with "expected <none>".
func TestValidate_AtlassianNoCache(t *testing.T) {
	cacheDir := t.TempDir() // empty

	results := Validate(Ctx{JiraCacheDir: cacheDir}, []string{
		"https://acme.atlassian.net/browse/PROJ-1",
	})
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Reason != "atlassian-site-mismatch" {
		t.Errorf("Reason: got %q, want 'atlassian-site-mismatch'", results[0].Reason)
	}
}

// ── Multiple URLs ──────────────────────────────────────────────────────────

func TestValidate_MultipleURLs(t *testing.T) {
	results := Validate(Ctx{Offline: true}, []string{
		"https://linkedin.com/in/someone",
		"https://example.com/page",
		"not-a-url",
	})
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
	if results[0].Status != "skipped" {
		t.Errorf("[0] Status: got %q, want 'skipped'", results[0].Status)
	}
	if results[1].Status != "skipped" {
		t.Errorf("[1] Status: got %q, want 'skipped'", results[1].Status)
	}
	if results[2].Status != "violation" {
		t.Errorf("[2] Status: got %q, want 'violation'", results[2].Status)
	}
}

// ── Helpers ────────────────────────────────────────────────────────────────

// gitInit creates a minimal git repo with a remote "origin" in dir.
func gitInit(t *testing.T, dir, remoteURL string) {
	t.Helper()
	for _, args := range [][]string{
		{"init"},
		{"remote", "add", "origin", remoteURL},
	} {
		c := exec.Command("git", args...)
		c.Dir = dir
		out, err := c.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, out)
		}
	}
}
