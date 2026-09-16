package tools

import (
	"context"
	"os/exec"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
)

// annotationPolicy captures the expected annotation values for a tool.
// A tool is either read-only (in which destructive and idempotent are
// meaningless and left at zero) or a writer (all four bools explicit).
type annotationPolicy struct {
	title       string
	readOnly    bool
	destructive bool
	idempotent  bool
	openWorld   bool
	reason      string // write target / evidence; says "conservative default" for every cell
}

// toolAnnotations is the golden map: every registered MCP tool must have an
// entry. Values are checked against the live Annotations on each tool at
// runtime. The map guards both drift (tool annotation changed without
// updating this map) and zero-value hazards (a forgotten Destructive:true
// that defaults to false).
var toolAnnotations = map[string]annotationPolicy{
	// READ-ONLY (16 rows)
	"validate": {
		title:      "Validate SDLC artifacts",
		readOnly:   true,
		idempotent: true,
		openWorld:  false,
		reason:     "pure validation; no os.WriteFile/AtomicWrite/telemetry",
	},
	"plan_support": {
		title:      "Read plan support data",
		readOnly:   true,
		idempotent: true,
		openWorld:  false,
		reason:     "only os.ReadFile/os.Stat",
	},
	"verify_pipeline_classify": {
		title:      "Classify CI failure logs",
		readOnly:   true,
		idempotent: true,
		openWorld:  false,
		reason:     "ClassifyLogs(in.Logs) — pure string classification, zero I/O",
	},
	"verify_tag_ancestry": {
		title:      "Check release tag ancestry",
		readOnly:   true,
		idempotent: true,
		openWorld:  false,
		reason:     "git rev-parse --verify + merge-base --is-ancestor, local only",
	},
	"commit_prepare": {
		title:      "Prepare commit context",
		readOnly:   true,
		idempotent: true,
		openWorld:  false,
		reason:     "local git reads; commit.go imports neither os nor fsx",
	},
	"links_validate": {
		title:      "Check documentation links",
		readOnly:   true,
		idempotent: true,
		openWorld:  true,
		reason:     "links.Validate does HTTP(S) reachability",
	},
	"received_review_prepare": {
		title:      "Fetch PR review feedback",
		readOnly:   true,
		idempotent: true,
		openWorld:  true,
		reason:     "ghx.PRView + ghx.PRChecks",
	},
	"received_review_verify": {
		title:      "Verify review replies posted",
		readOnly:   true,
		idempotent: true,
		openWorld:  true,
		reason:     "ghx.PRReviewComments",
	},
	"plan_mark": {
		title:      "Record plan progress marker",
		readOnly:   true,
		idempotent: true,
		openWorld:  false,
		reason:     "state.Write → .sdlc-v2/runs/ (gitignored)",
	},
	"plan_explore_prepare": {
		title:      "Prepare plan exploration pack",
		readOnly:   true,
		idempotent: true,
		openWorld:  false,
		reason:     "os.MkdirTemp(os.TempDir(), …)",
	},
	"learnings_log": {
		title:      "Append to learnings log",
		readOnly:   true,
		idempotent: true,
		openWorld:  false,
		reason:     ".sdlc-v2/learnings/log.md (gitignored); append",
	},
	"mcp_failure_record": {
		title:      "Record MCP tool failure",
		readOnly:   true,
		idempotent: true,
		openWorld:  false,
		reason:     ".sdlc-v2/learnings/log.md + .sdlc-v2/state/ (gitignored)",
	},
	"ship_verify_side_effect": {
		title:      "Verify ship step side effect",
		readOnly:   true,
		idempotent: true,
		openWorld:  true,
		reason:     "only write is state.Write → gitignored .sdlc-v2/runs/; reads gh via shipPRForBranch",
	},
	"plan_prepare": {
		title:      "Prepare plan state and template",
		readOnly:   true,
		idempotent: true,
		openWorld:  false,
		reason:     "Task 3 relocated its one tracked-file write (openspec tasks.md ref stamp) into execute_state's init handler; plan_prepare itself now only reads",
	},
	"review_prepare": {
		title:      "Prepare code review payload",
		readOnly:   true,
		idempotent: true,
		openWorld:  false,
		reason:     "every write lands in os.MkdirTemp(\"\", \"sdlc-review-\") — per-dimension .diff/.slice.json and manifest.json; no input field redirects that path; diffs come from local git diff",
	},
	"setup_prepare": {
		title:      "Prepare SDLC setup context",
		readOnly:   true,
		idempotent: true,
		openWorld:  false,
		reason:     "no writes at all: configmigrate.Verify, setupmeta.Sections, gitx.DefaultBranch, git remote get-url, ciScriptDrift are read-only; the managed-section os.WriteFile belongs to setup_write_sections/setup_init, not setupPrepare",
	},

	// WRITER (15 rows)
	"commit_apply": {
		title:       "Create a git commit",
		readOnly:    false,
		destructive: true,
		idempotent:  false,
		openWorld:   false,
		reason:      "git add -A + git commit; no ghx/http in commit.go",
	},
	"pr_prepare": {
		title:       "Prepare pull request context",
		readOnly:    false,
		destructive: true,
		idempotent:  true,
		openWorld:   true,
		reason:      "git fetch --tags --force writes .git/refs/tags + hits remote; --force can move an existing local tag ref, but repeating the call is still idempotent",
	},
	"pr_apply": {
		title:       "Create or update pull request",
		readOnly:    false,
		destructive: true,
		idempotent:  false,
		openWorld:   true,
		reason:      "ghPRCreate, ghPREdit, ensureReleaseLabels",
	},
	"execute_state": {
		title:       "Read or update execute run state",
		readOnly:    false,
		destructive: true,
		idempotent:  false,
		openWorld:   false,
		reason:      "configmigrate.MigrateWithBackup → tracked .sdlc-v2/config.toml",
	},
	"ship_state": {
		title:       "Read or update ship run state",
		readOnly:    false,
		destructive: true,
		idempotent:  false,
		openWorld:   false,
		reason:      "configmigrate.MigrateWithBackup → tracked config.toml; openWorld:false because ship_state.go has zero ghx references",
	},
	"ship_prepare": {
		title:       "Prepare ship pipeline run",
		readOnly:    false,
		destructive: true,
		idempotent:  false,
		openWorld:   true,
		reason:      "MigrateWithBackup, state.Init, state.Write, shipGC",
	},
	"poll_await": {
		title:       "Await CI or PR completion",
		readOnly:    false,
		destructive: true,
		idempotent:  false,
		openWorld:   true,
		reason:      "caller-redirectable state_file; ghx.PRView, ghx.PRChecksWithExitCode",
	},
	"openspec_enrich": {
		title:       "Write OpenSpec config block",
		readOnly:    false,
		destructive: true,
		idempotent:  true,
		openWorld:   false,
		reason:      "os.WriteFile → tracked openspec/config.yaml; managed block rewritten in place",
	},
	"jira": {
		title:       "Manage local Jira cache",
		readOnly:    false,
		destructive: true,
		idempotent:  false,
		openWorld:   false,
		reason:      "os.WriteFile, AtomicWriteJSON, os.Remove. No network — Atlassian calls go through the Atlassian MCP server, not this tool",
	},
	"migrate": {
		title:       "Migrate SDLC config",
		readOnly:    false,
		destructive: true,
		idempotent:  true,
		openWorld:   false,
		reason:      "fsx.AtomicWriteTOML over tracked config.toml",
	},
	"scaffold_ci": {
		title:       "Scaffold CI workflow files",
		readOnly:    false,
		destructive: true,
		idempotent:  true,
		openWorld:   false,
		reason:      "os.Remove, os.WriteFile; ghx.ParseRemoteOwner is a pure string parse",
	},
	"prepare_orchestrator": {
		title:       "Write orchestrator manifest",
		readOnly:    false,
		destructive: true,
		idempotent:  true,
		openWorld:   false,
		reason:      "os.WriteFile manifest",
	},
	"dimensions_render_instructions": {
		title:       "Render review dimension files",
		readOnly:    false,
		destructive: true,
		idempotent:  true,
		openWorld:   false,
		reason:      "os.WriteFile over tracked .sdlc-v2/review-dimensions/*.md",
	},
	"setup_init": {
		title:       "Initialize SDLC config files",
		readOnly:    false,
		destructive: true,
		idempotent:  true,
		openWorld:   false,
		reason:      "os.WriteFile; .sdlc-v2/.gitignore",
	},
	"setup_write_sections": {
		title:       "Write SDLC config sections",
		readOnly:    false,
		destructive: true,
		idempotent:  false,
		openWorld:   false,
		reason:      "config.WriteSection replaces a section wholesale; auto-triggers scaffoldCI on version",
	},
}

// TestEveryToolMatchesItsAnnotationDecision verifies that every registered tool
// has an entry in toolAnnotations and that all five annotation values match.
// This guards both drift (tool annotation changed) and zero-value hazards.
func TestEveryToolMatchesItsAnnotationDecision(t *testing.T) {
	s := mcpserver.New("test", "0.0.0-test")

	// Mirrors cmd/sdlc/main.go's runMCP registration list exactly, so this
	// test covers the same tool surface the real server exposes.
	RegisterPlanTools(s)
	RegisterPlanExploreTools(s)
	RegisterLinksTools(s)
	RegisterMCPFailureTools(s)
	RegisterReviewTools(s)
	RegisterExecuteStateTools(s)
	RegisterReceivedReviewTools(s)
	RegisterCommitTools(s)
	RegisterScaffoldTools(s)
	RegisterSetupTools(s)
	RegisterSetupWriteTools(s)
	RegisterPRTools(s)
	RegisterPrepareOrchestratorTools(s)
	RegisterOpenspecTools(s)
	RegisterDimensionsRenderTools(s)
	RegisterValidateTools(s)
	RegisterShipStateTools(s)
	RegisterJiraTools(s)
	RegisterPollingTools(s)
	RegisterMigrateTools(s)
	RegisterShipTools(s)
	RegisterPlanSupportTools(s)
	RegisterLearningsTools(s)

	ctx := context.Background()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	if _, err := s.MCPServer().Connect(ctx, serverTransport, nil); err != nil {
		t.Fatalf("server Connect: %v", err)
	}

	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0.0.0"}, nil)
	c, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client Connect: %v", err)
	}
	t.Cleanup(func() { c.Close() })

	resp, err := c.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(resp.Tools) == 0 {
		t.Fatal("ListTools: no tools registered")
	}

	// Verify the count matches exactly.
	if len(resp.Tools) != len(toolAnnotations) {
		t.Errorf("tool count mismatch: got %d tools, toolAnnotations has %d entries",
			len(resp.Tools), len(toolAnnotations))
	}

	// Check each tool.
	for _, tool := range resp.Tools {
		policy, ok := toolAnnotations[tool.Name]
		if !ok {
			t.Errorf("%s: no entry in toolAnnotations -- classify this tool against the boundary rule in docs/mcp-tool-annotations.md and add a line. readOnly:false is the safe default; set readOnly:true only if the tool can write NOTHING that git tracks AND the caller cannot choose the write path.",
				tool.Name)
			continue
		}

		// Check tool has Annotations (the schema is optional in MCP, but
		// Register requires it per Task 2).
		if tool.Annotations == nil {
			t.Errorf("%s: tool.Annotations is nil", tool.Name)
			continue
		}

		// Check Title.
		if tool.Title != policy.title {
			t.Errorf("%s: Title mismatch: got %q, want %q",
				tool.Name, tool.Title, policy.title)
		}

		// Check all five annotation values.
		if tool.Annotations.Title != policy.title {
			t.Errorf("%s: Annotations.Title mismatch: got %q, want %q",
				tool.Name, tool.Annotations.Title, policy.title)
		}

		if tool.Annotations.ReadOnlyHint != policy.readOnly {
			t.Errorf("%s: ReadOnly mismatch: got %v, want %v",
				tool.Name, tool.Annotations.ReadOnlyHint, policy.readOnly)
		}

		if tool.Annotations.DestructiveHint == nil {
			t.Errorf("%s: DestructiveHint is nil (go-sdk trap) — mcpserver.Register must always set it", tool.Name)
		} else if *tool.Annotations.DestructiveHint != policy.destructive {
			t.Errorf("%s: Destructive mismatch: got %v, want %v",
				tool.Name, *tool.Annotations.DestructiveHint, policy.destructive)
		}

		if tool.Annotations.IdempotentHint != policy.idempotent {
			t.Errorf("%s: Idempotent mismatch: got %v, want %v",
				tool.Name, tool.Annotations.IdempotentHint, policy.idempotent)
		}

		if tool.Annotations.OpenWorldHint == nil {
			t.Errorf("%s: OpenWorldHint is nil (go-sdk trap) — mcpserver.Register must always set it", tool.Name)
		} else if *tool.Annotations.OpenWorldHint != policy.openWorld {
			t.Errorf("%s: OpenWorld mismatch: got %v, want %v",
				tool.Name, *tool.Annotations.OpenWorldHint, policy.openWorld)
		}
	}
}

// TestReadOnlyToolsWriteNothingTracked verifies that every read-only tool
// leaves the git working tree clean after execution. This is a backstop
// against refactors that inadvertently push a write into a read-only tool.
// Tools with external dependencies (links_validate, received_review_*) are
// skipped with a recorded reason.
func TestReadOnlyToolsWriteNothingTracked(t *testing.T) {
	readOnlyTools := make([]string, 0)
	for name, policy := range toolAnnotations {
		if policy.readOnly {
			readOnlyTools = append(readOnlyTools, name)
		}
	}

	if len(readOnlyTools) == 0 {
		t.Fatal("no read-only tools found")
	}

	for _, toolName := range readOnlyTools {
		t.Run(toolName, func(t *testing.T) {
			// Skip tools with external dependencies.
			switch toolName {
			case "links_validate":
				t.Skip("links_validate requires HTTP reachability; skipped in fixtures")
			case "received_review_prepare":
				t.Skip("received_review_prepare requires GitHub API access; skipped in fixtures")
			case "received_review_verify":
				t.Skip("received_review_verify requires GitHub API access; skipped in fixtures")
			}

			// Create a minimal fixture repo, seed it via setup_init (creates
			// .sdlc-v2/config.toml, tracked, plus the managed .gitignore
			// blocks that keep .sdlc-v2/local.toml and other scratch state
			// out of git status), then commit that baseline. Only after this
			// baseline commit is `git status --porcelain` a meaningful signal
			// for "did the tool under test write anything tracked" — without
			// it, setup_init's own tracked file would show up as untracked
			// noise unrelated to the tool being exercised.
			root := t.TempDir()
			initGitFixture(t, root)

			if _, err := setupInit(root, SetupInitIn{}); err != nil {
				t.Fatalf("setupInit fixture seed: %v", err)
			}
			runGit(t, root, "add", "-A")
			runGit(t, root, "commit", "-m", "baseline")

			// Directly invoke the tool's core function against the fixture
			// root. The returned result/error is intentionally ignored except
			// where noted: a domain error from e.g. plan_mark (no prior plan
			// state) or verify_tag_ancestry (unknown tag) is a legitimate,
			// side-effect-free code path, not a test failure — what this test
			// checks is the git working tree, not the call's outcome.
			switch toolName {
			case "validate":
				_, _ = validate(root, ValidateIn{Action: "discovery"})
			case "plan_support":
				_, _ = planSupportCore(root, root, PlanSupportIn{
					Action:      "merge_results",
					LensResults: []LensResult{{Name: "lens-1", Status: "approved"}},
				})
			case "verify_pipeline_classify":
				_ = ClassifyLogs("error: build failed")
			case "verify_tag_ancestry":
				_, _ = verifyTagAncestry(root, "v0.0.0-does-not-exist")
			case "commit_prepare":
				_, _ = commitPrepare(root, root, CommitPrepareIn{SkipConfigCheck: true})
			case "plan_mark":
				_, _ = planMark(root, root, PlanMarkIn{Marker: "guardrailsEvaluated"})
			case "plan_explore_prepare":
				_ = buildExplorePack(root, root, "", "investigate the widget rendering pipeline")
			case "learnings_log":
				_, _ = learningsLog(root, LearningsLogIn{Action: "append", Entry: "## test entry\nsingle line body, no blank lines"})
			case "mcp_failure_record":
				_, _ = mcpFailureRecord(root, MCPFailureRecordIn{Tool: "test_tool", HTTPStatus: 401, ErrorMessage: "unauthorized access"})
			case "ship_verify_side_effect":
				_, _ = shipVerifySideEffect(root, root, ShipVerifySideEffectIn{Step: "review"}, time.Now)
			case "plan_prepare":
				_, _ = planPrepareCore(root, root, PlanPrepareIn{SkipConfigCheck: true})
			case "setup_prepare":
				_, _ = setupPrepare(root, SetupPrepareIn{})
			case "review_prepare":
				// Writes only into os.MkdirTemp("", "sdlc-review-"), never
				// into root — so the fixture tree must stay clean even when
				// the call succeeds and produces a full manifest.
				_, _ = reviewPrepare(root, root, ReviewPrepareIn{SkipConfigCheck: true})
			default:
				t.Fatalf("no direct-call wiring for read-only tool %q — add one to this switch", toolName)
			}

			// Check git status is clean.
			cmd := exec.Command("git", "status", "--porcelain")
			cmd.Dir = root
			output, err := cmd.Output()
			if err != nil {
				t.Fatalf("git status: %v", err)
			}

			if len(output) > 0 {
				t.Errorf("git working tree is not clean after %s:\n%s",
					toolName, string(output))
			}
		})
	}
}
