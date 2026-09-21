package tools

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
)

// Each case breaks one input or one file on disk, calls the real core
// handler, and checks two things: the concrete error type, and that the
// Suggestion names the failing input or the next tool call. An empty
// Suggestion would render the generic per-class fallback, so it fails here.
//
// Disk failures use a regular file where a folder is needed (ENOTDIR) or a
// folder where a file is needed (EISDIR). Nothing here uses chmod, so the
// result does not depend on which user runs the tests.
//
// Sites that need an unreadable or undeletable file cannot be reached this
// way and are not covered: scaffold.go "check installed version of CI
// script" (ciScriptDrift and scaffoldCI), scaffold.go "remove legacy", and
// jira.go "delete cache file" (jiraClear, both branches).

type toolErrSiteClass string

const (
	toolErrDomain toolErrSiteClass = "domain"
	toolErrInfra  toolErrSiteClass = "infra"
	toolErrData   toolErrSiteClass = "data"
)

// toolErrSiteCase builds the broken fixture in run, calls the core handler,
// and returns the substrings the Suggestion must contain plus the error.
type toolErrSiteCase struct {
	name  string
	class toolErrSiteClass
	run   func(t *testing.T) (want []string, err error)
}

func toolErrSiteRun(t *testing.T, cases []toolErrSiteCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			want, err := tc.run(t)

			var suggestion string
			switch tc.class {
			case toolErrDomain:
				var e *mcpserver.DomainError
				if !errors.As(err, &e) {
					t.Fatalf("want *mcpserver.DomainError, got %T: %v", err, err)
				}
				suggestion = e.Suggestion
			case toolErrInfra:
				var e *mcpserver.InfraError
				if !errors.As(err, &e) {
					t.Fatalf("want *mcpserver.InfraError, got %T: %v", err, err)
				}
				suggestion = e.Suggestion
			case toolErrData:
				var e *mcpserver.DataError
				if !errors.As(err, &e) {
					t.Fatalf("want *mcpserver.DataError, got %T: %v", err, err)
				}
				suggestion = e.Suggestion
			default:
				t.Fatalf("unknown error class %q", tc.class)
			}

			if suggestion == "" {
				t.Fatal("Suggestion is empty, so the renderer would show the generic fallback")
			}
			for _, w := range want {
				if !strings.Contains(suggestion, w) {
					t.Errorf("Suggestion must contain %q, got %q", w, suggestion)
				}
			}
		})
	}
}

// toolErrSiteBlock plants a regular file at path, so any MkdirAll below it
// fails with ENOTDIR.
func toolErrSiteBlock(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("blocker"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// toolErrSiteDir plants a folder at path, so writing a file there fails.
func toolErrSiteDir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

// ---------------------------------------------------------------------------
// jira
// ---------------------------------------------------------------------------

func toolErrSiteJiraData(siteURL string) map[string]any {
	return map[string]any{
		"version": float64(1),
		"cloudId": "cloud-1",
		"project": map[string]any{"key": "FOO"},
		"siteUrl": siteURL,
	}
}

func toolErrSiteJira(root string, in JiraIn) error {
	in.SkipConfigCheck = true
	_, err := jiraCore(root, in, true)
	return err
}

func TestToolErrorSites_Jira(t *testing.T) {
	cases := []toolErrSiteCase{
		{
			name:  "save/missing required fields",
			class: toolErrDomain,
			run: func(t *testing.T) ([]string, error) {
				err := toolErrSiteJira(t.TempDir(), JiraIn{
					Action: "save", Key: "FOO",
					Data: map[string]any{"version": float64(1)},
				})
				return []string{"cloudId, project, siteUrl", "jira save"}, err
			},
		},
		{
			name:  "save/site host cannot be derived",
			class: toolErrDomain,
			run: func(t *testing.T) ([]string, error) {
				err := toolErrSiteJira(t.TempDir(), JiraIn{
					Action: "save", Key: "FOO",
					Data: toolErrSiteJiraData(""),
				})
				return []string{"data.siteUrl", "jira save"}, err
			},
		},
		{
			name:  "save/create home cache dir fails",
			class: toolErrInfra,
			run: func(t *testing.T) ([]string, error) {
				home := t.TempDir()
				t.Setenv("HOME", home)
				toolErrSiteBlock(t, filepath.Join(home, ".sdlc-cache"))
				err := toolErrSiteJira(t.TempDir(), JiraIn{
					Action: "save", Key: "FOO",
					Data: toolErrSiteJiraData("https://acme.atlassian.net"),
				})
				siteDir := filepath.Join(home, ".sdlc-cache", "jira", "acme_atlassian_net")
				return []string{siteDir, "cacheDir", "jira save"}, err
			},
		},
		{
			name:  "save/write cache file fails",
			class: toolErrInfra,
			run: func(t *testing.T) ([]string, error) {
				cacheDir := t.TempDir()
				cacheFile := filepath.Join(cacheDir, "FOO.json")
				toolErrSiteDir(t, cacheFile)
				err := toolErrSiteJira(t.TempDir(), JiraIn{
					Action: "save", Key: "FOO", CacheDir: cacheDir,
					Data: toolErrSiteJiraData("https://acme.atlassian.net"),
				})
				return []string{cacheFile, "jira save"}, err
			},
		},
	}

	critiqueData := map[string]any{"initial": "a", "findings": []any{}, "final": "b"}
	for _, action := range []string{"write-critique", "write-approval"} {
		artifact := map[string]string{
			"write-critique": "critique-abc123.json",
			"write-approval": "approval-abc123.token",
		}[action]
		artifactsRel := paths.DataDir + "/state/artifacts"

		cases = append(cases,
			toolErrSiteCase{
				name:  action + "/empty hash",
				class: toolErrDomain,
				run: func(t *testing.T) ([]string, error) {
					err := toolErrSiteJira(t.TempDir(), JiraIn{Action: action, Data: critiqueData})
					return []string{"sha256sum"}, err
				},
			},
			toolErrSiteCase{
				name:  action + "/hash with path separator",
				class: toolErrDomain,
				run: func(t *testing.T) ([]string, error) {
					err := toolErrSiteJira(t.TempDir(), JiraIn{Action: action, Hash: "../evil", Data: critiqueData})
					return []string{"alphanumeric"}, err
				},
			},
			toolErrSiteCase{
				name:  action + "/create artifacts dir fails",
				class: toolErrInfra,
				run: func(t *testing.T) ([]string, error) {
					root := t.TempDir()
					toolErrSiteBlock(t, filepath.Join(root, paths.DataDir))
					err := toolErrSiteJira(root, JiraIn{Action: action, Hash: "abc123", Data: critiqueData})
					return []string{artifactsRel, "jira " + action}, err
				},
			},
			toolErrSiteCase{
				name:  action + "/write artifact fails",
				class: toolErrInfra,
				run: func(t *testing.T) ([]string, error) {
					root := t.TempDir()
					toolErrSiteDir(t, filepath.Join(root, paths.DataDir, "state", "artifacts", artifact))
					err := toolErrSiteJira(root, JiraIn{Action: action, Hash: "abc123", Data: critiqueData})
					return []string{artifactsRel, "jira " + action, "same hash"}, err
				},
			},
		)
	}

	cases = append(cases, toolErrSiteCase{
		name:  "write-critique/no data",
		class: toolErrDomain,
		run: func(t *testing.T) ([]string, error) {
			err := toolErrSiteJira(t.TempDir(), JiraIn{Action: "write-critique", Hash: "abc123"})
			return []string{"{initial, findings, final}", "data"}, err
		},
	})

	toolErrSiteRun(t, cases)
}

// ---------------------------------------------------------------------------
// received_review_verify, review_prepare, dimensions_render_instructions
// ---------------------------------------------------------------------------

func TestToolErrorSites_ReceivedReviewWriteReplyBodies(t *testing.T) {
	verify := func(root string) error {
		_, err := receivedReviewVerify(root, root, ReceivedReviewVerifyIn{WriteReplyBodies: true, Content: "reply"})
		return err
	}
	toolErrSiteRun(t, []toolErrSiteCase{
		{
			name:  "create artifacts dir fails",
			class: toolErrInfra,
			run: func(t *testing.T) ([]string, error) {
				root := t.TempDir()
				toolErrSiteBlock(t, filepath.Join(root, paths.DataDir))
				return []string{paths.DataDir + "/state/artifacts", "received_review_verify", "writeReplyBodies true"}, verify(root)
			},
		},
		{
			name:  "write reply bodies fails",
			class: toolErrInfra,
			run: func(t *testing.T) ([]string, error) {
				root := t.TempDir()
				toolErrSiteDir(t, filepath.Join(root, paths.DataDir, "state", "artifacts", "received-review-reply-bodies.md"))
				return []string{paths.DataDir + "/state/artifacts", "received_review_verify", "same content"}, verify(root)
			},
		},
	})
}

func TestToolErrorSites_ReviewSaveReview(t *testing.T) {
	// The active root does not exist, so the branch falls back to "detached"
	// and the file name does not depend on where TMPDIR lives.
	save := func(root string) error {
		_, err := reviewPrepare(root, filepath.Join(root, "no-such-worktree"), ReviewPrepareIn{SaveReview: true, Content: "review"})
		return err
	}
	toolErrSiteRun(t, []toolErrSiteCase{
		{
			name:  "create reviews dir fails",
			class: toolErrInfra,
			run: func(t *testing.T) ([]string, error) {
				root := t.TempDir()
				toolErrSiteBlock(t, filepath.Join(root, paths.DataDir))
				return []string{paths.DataDir + "/reviews", "review_prepare", "saveReview true"}, save(root)
			},
		},
		{
			name:  "write review file fails",
			class: toolErrInfra,
			run: func(t *testing.T) ([]string, error) {
				root := t.TempDir()
				now := time.Now().UTC()
				// Tomorrow too, so a UTC midnight during the test cannot skip the failure.
				for _, d := range []time.Time{now, now.Add(24 * time.Hour)} {
					toolErrSiteDir(t, filepath.Join(root, paths.DataDir, "reviews", "detached-"+d.Format("2006-01-02")+".md"))
				}
				return []string{paths.DataDir + "/reviews", "review_prepare", "same content"}, save(root)
			},
		},
	})
}

func TestToolErrorSites_DimensionsWriteDimension(t *testing.T) {
	write := func(root string) error {
		_, err := dimensionsRenderInstructions(root, DimensionsRenderInstructionsIn{WriteDimension: true, Name: "security", Content: "# security"})
		return err
	}
	toolErrSiteRun(t, []toolErrSiteCase{
		{
			name:  "create review-dimensions dir fails",
			class: toolErrInfra,
			run: func(t *testing.T) ([]string, error) {
				root := t.TempDir()
				toolErrSiteBlock(t, filepath.Join(root, paths.DataDir))
				return []string{paths.DataDir + "/review-dimensions", "dimensions_render_instructions", "writeDimension true"}, write(root)
			},
		},
		{
			name:  "write dimension file fails",
			class: toolErrInfra,
			run: func(t *testing.T) ([]string, error) {
				root := t.TempDir()
				toolErrSiteDir(t, filepath.Join(root, paths.DataDir, "review-dimensions", "security.md"))
				return []string{paths.DataDir + "/review-dimensions", "same name and content"}, write(root)
			},
		},
	})
}

// ---------------------------------------------------------------------------
// setup_init
// ---------------------------------------------------------------------------

// toolErrSiteShippedTemplates points the plugin lookup at the test fixture,
// so setupWritePlanTemplate can find plan-template-default.md.
func toolErrSiteShippedTemplates(t *testing.T, pluginRoot string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_PLUGIN_ROOT", pluginRoot)
	resetSkillTemplateIndex()
	t.Cleanup(resetSkillTemplateIndex)
}

func TestToolErrorSites_SetupInitWriteTemplates(t *testing.T) {
	fixturePlugin := filepath.Join("testdata", "plugins", "sdlc")
	toolErrSiteRun(t, []toolErrSiteCase{
		{
			name:  "plan template/shipped file not found",
			class: toolErrData,
			run: func(t *testing.T) ([]string, error) {
				toolErrSiteShippedTemplates(t, "")
				_, err := setupInit(t.TempDir(), SetupInitIn{WritePlanTemplate: true})
				return []string{"plan-template-default.md", "setup_init", "writePlanTemplate true"}, err
			},
		},
		{
			name:  "plan template/create data dir fails",
			class: toolErrInfra,
			run: func(t *testing.T) ([]string, error) {
				toolErrSiteShippedTemplates(t, fixturePlugin)
				root := t.TempDir()
				toolErrSiteBlock(t, filepath.Join(root, paths.DataDir))
				_, err := setupInit(root, SetupInitIn{WritePlanTemplate: true})
				return []string{paths.DataDir, "setup_init", "writePlanTemplate true"}, err
			},
		},
		{
			name:  "plan template/write file fails",
			class: toolErrInfra,
			run: func(t *testing.T) ([]string, error) {
				toolErrSiteShippedTemplates(t, fixturePlugin)
				root := t.TempDir()
				toolErrSiteDir(t, filepath.Join(root, paths.DataDir, "plan-template.md"))
				_, err := setupInit(root, SetupInitIn{WritePlanTemplate: true})
				return []string{paths.DataDir + "/plan-template.md", "setup_init", "same content"}, err
			},
		},
		{
			name:  "PR template/create data dir fails",
			class: toolErrInfra,
			run: func(t *testing.T) ([]string, error) {
				root := t.TempDir()
				toolErrSiteBlock(t, filepath.Join(root, paths.DataDir))
				_, err := setupInit(root, SetupInitIn{WritePRTemplate: true, Content: "## PR"})
				return []string{paths.DataDir, "setup_init", "writePRTemplate true"}, err
			},
		},
		{
			name:  "PR template/write file fails",
			class: toolErrInfra,
			run: func(t *testing.T) ([]string, error) {
				root := t.TempDir()
				toolErrSiteDir(t, filepath.Join(root, paths.DataDir, "pr-template.md"))
				_, err := setupInit(root, SetupInitIn{WritePRTemplate: true, Content: "## PR"})
				return []string{paths.DataDir + "/pr-template.md", "setup_init", "same content"}, err
			},
		},
	})
}

// ---------------------------------------------------------------------------
// scaffold_ci and ci_script_drift
// ---------------------------------------------------------------------------

func TestToolErrorSites_Scaffold(t *testing.T) {
	const missingKey = "no-such-payload.cjs"
	swapManifest := func(t *testing.T) {
		t.Helper()
		orig := scaffoldManifest
		scaffoldManifest = []scaffoldManifestEntry{{
			PayloadKey: missingKey,
			Dest:       filepath.Join(".github", "scripts", missingKey),
		}}
		t.Cleanup(func() { scaffoldManifest = orig })
	}

	toolErrSiteRun(t, []toolErrSiteCase{
		{
			name:  "ciScriptDrift/embedded payload missing",
			class: toolErrInfra,
			run: func(t *testing.T) ([]string, error) {
				swapManifest(t)
				_, err := ciScriptDrift(t.TempDir())
				return []string{missingKey, "setup_prepare"}, err
			},
		},
		{
			name:  "scaffoldCI/embedded payload missing",
			class: toolErrInfra,
			run: func(t *testing.T) ([]string, error) {
				swapManifest(t)
				_, err := scaffoldCI(t.TempDir(), false)
				return []string{missingKey, "scaffold_ci"}, err
			},
		},
		{
			name:  "scaffoldCI/create script dir fails",
			class: toolErrInfra,
			run: func(t *testing.T) ([]string, error) {
				root := t.TempDir()
				toolErrSiteBlock(t, filepath.Join(root, ".github"))
				_, err := scaffoldCI(root, false)
				return []string{filepath.Join(root, ".github", "scripts"), "scaffold_ci"}, err
			},
		},
		{
			name:  "scaffoldCI/write script fails",
			class: toolErrInfra,
			run: func(t *testing.T) ([]string, error) {
				root := t.TempDir()
				script := filepath.Join(root, ".github", "scripts", "retag-release.cjs")
				toolErrSiteDir(t, script)
				_, err := scaffoldCI(root, false)
				return []string{script, "scaffold_ci"}, err
			},
		},
	})
}
