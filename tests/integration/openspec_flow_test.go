//go:build integration

package integration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/tools"
)

// openspecStubScript is a stand-in for the openspec CLI. It covers only the
// two calls openspec.Materialize makes (internal/openspec/materialize.go,
// createChange): `openspec new change <name>` and `openspec validate <name>
// --strict`, both run with the active worktree root as cwd. It prints
// nothing on stdout, because cliResult tries to decode stdout as JSON.
const openspecStubScript = `#!/bin/sh
if [ "$1" = "new" ] && [ "$2" = "change" ]; then
  mkdir -p "openspec/changes/$3" || exit 1
  printf 'schema: spec-driven\n' > "openspec/changes/$3/.openspec.yaml"
  exit 0
fi
if [ "$1" = "validate" ]; then
  exit 0
fi
echo "openspec stub: unsupported args: $*" >&2
exit 2
`

// setupOpenspecFlowClient registers the tools this flow drives (ship_prepare,
// ship_state, execute_state) on a fresh in-process MCP server/client pair,
// using the same recipe as setupShipClient.
func setupOpenspecFlowClient(t *testing.T) *mcp.ClientSession {
	t.Helper()
	srv := mcpserver.New("openspec-flow-test", "0.0.0-test")
	tools.RegisterShipTools(srv)
	tools.RegisterShipStateTools(srv)
	tools.RegisterExecuteStateTools(srv)

	ctx := context.Background()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	if _, err := srv.MCPServer().Connect(ctx, serverTransport, nil); err != nil {
		t.Fatalf("server Connect: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "openspec-flow-test", Version: "0.0.0"}, nil)
	c, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client Connect: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

// sectionBulletRE matches one "- key: value" bullet line.
var sectionBulletRE = regexp.MustCompile(`^- (\w+): (.*)$`)

// renderedSection returns the "- key: value" bullets directly under the
// "## <heading>" line of a rendered tool result (render rules 4 and 5 in
// docs/mcp-output-contract.md: root scalars go under "## Fields", a nested
// struct or map gets its own "## <key>" section). Returns nil when the
// heading is absent.
func renderedSection(body, heading string) map[string]string {
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		if line != "## "+heading {
			continue
		}
		fields := map[string]string{}
		for _, l := range lines[i+1:] {
			m := sectionBulletRE.FindStringSubmatch(l)
			if m == nil {
				break
			}
			fields[m[1]] = m[2]
		}
		return fields
	}
	return nil
}

// pluginSchemasDir returns the absolute path of plugins/sdlc/schemas/. It
// resolves against the test's starting cwd (tests/integration, as
// scriptPath does), so call it before any chdir.
func pluginSchemasDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("..", "..", "plugins", "sdlc", "schemas"))
	if err != nil {
		t.Fatalf("abs schemas dir: %v", err)
	}
	return dir
}

// validateAgainstSchema validates the JSON file at statePath against
// schemasDir/schemaFile, the same way internal/tools'
// execute_state_schema_test.go does.
func validateAgainstSchema(t *testing.T, statePath, schemasDir, schemaFile string) {
	t.Helper()
	schemaPath := filepath.Join(schemasDir, schemaFile)
	sch, err := jsonschema.NewCompiler().Compile(schemaPath)
	if err != nil {
		t.Fatalf("compile %s: %v", schemaFile, err)
	}
	raw, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("read %s: %v", statePath, err)
	}
	inst, err := jsonschema.UnmarshalJSON(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatalf("parse %s: %v", statePath, err)
	}
	if err := sch.Validate(inst); err != nil {
		t.Errorf("%s does not match %s: %v\n%s", statePath, schemaFile, err, raw)
	}
}

// TestOpenspecMaterializeFlow drives a staged OpenSpec change from plan to
// base-sync through the real MCP tool surface: ship_prepare materializes
// the staged change ("created"), execute_state init sees it already done
// ("already"), and base-sync merges a new origin/main commit into the
// feature branch after wave 1. Both state files are then checked against
// their JSON schemas.
func TestOpenspecMaterializeFlow(t *testing.T) {
	const (
		branch = "feat/openspec-flow"
		change = "demo"
	)

	schemasDir := pluginSchemasDir(t)

	// --- openspec stub on PATH (prepended, so git stays visible) ---
	binDir := t.TempDir()
	mustWriteFile(t, filepath.Join(binDir, "openspec"), openspecStubScript)
	if err := os.Chmod(filepath.Join(binDir, "openspec"), 0o755); err != nil {
		t.Fatalf("chmod openspec stub: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	// --- working repo on main, pushed to a bare origin ---
	repo := realPath(t, t.TempDir())
	origin := realPath(t, t.TempDir())
	runGit(t, origin, "init", "-q", "--bare")

	runGit(t, repo, "init", "-q")
	runGit(t, repo, "checkout", "-q", "-b", "main")
	runGit(t, repo, "config", "user.email", "integration-test@example.com")
	runGit(t, repo, "config", "user.name", "integration-test")
	// .sdlc-v2/ is ignored from the first commit, so state files, the
	// staging dir, and config.toml never make the tree dirty (base-sync
	// skips on any `git status --porcelain` output).
	mustWriteFile(t, filepath.Join(repo, ".gitignore"), ".sdlc-v2/\n")
	runGit(t, repo, "add", ".gitignore")
	runGit(t, repo, "commit", "-q", "-m", "init")
	runGit(t, repo, "remote", "add", "origin", origin)
	runGit(t, repo, "push", "-q", "-u", "origin", "main")
	runGit(t, repo, "checkout", "-q", "-b", branch)

	mustWriteFile(t, filepath.Join(repo, ".sdlc-v2", "config.toml"),
		"[git]\nbaseBranch = \"main\"\n\n[execute]\nbaseSync = true\n")

	// --- staged change: one file plus stage.json (openspec.StageManifest) ---
	stagingDir := filepath.Join(repo, ".sdlc-v2", "openspec-staging", change)
	proposal := "# Proposal\n\nDemo change for the integration test.\n"
	mustWriteFile(t, filepath.Join(stagingDir, "proposal.md"), proposal)
	sum := sha256.Sum256([]byte(proposal))
	mustWriteFile(t, filepath.Join(stagingDir, "stage.json"),
		`{"change":"`+change+`","schema":"spec-driven","files":[{"path":"proposal.md","sha256":"`+hex.EncodeToString(sum[:])+`"}]}`)

	// --- plan file with the two headers ship_prepare reads ---
	planPath := filepath.Join(repo, ".sdlc-v2", "plan-demo.md")
	mustWriteFile(t, planPath, "# Demo plan\n\n"+
		"**Source:** openspec/changes/"+change+"/\n"+
		"**OpenSpec-Staging:** .sdlc-v2/openspec-staging/"+change+"/\n\n"+
		"### Task 1: demo\n")

	chdir(t, repo)
	c := setupOpenspecFlowClient(t)

	// --- ship_prepare: materializes the staged change ---
	prep := callTool(t, c, "ship_prepare", map[string]any{
		"sessionId": "openspec-flow-session",
		"planFile":  planPath,
	})
	if !prep.OK {
		t.Fatalf("ship_prepare: not ok: code=%s body=%s", prep.Code, prep.Body)
	}
	if got := renderedSection(prep.Body, "openspec"); got["materialized"] != "created" || got["change"] != change {
		t.Fatalf("ship_prepare openspec = %v, want change=%s materialized=created; body:\n%s", got, change, prep.Body)
	}
	shipStatePath := renderedSection(prep.Body, "Fields")["stateFile"]
	if shipStatePath == "" {
		t.Fatalf("ship_prepare: no stateFile in body:\n%s", prep.Body)
	}

	staged := runGit(t, repo, "diff", "--cached", "--name-only")
	if !strings.Contains(staged, "openspec/changes/"+change+"/proposal.md") ||
		!strings.Contains(staged, "openspec/changes/"+change+"/.openspec.yaml") {
		t.Fatalf("git diff --cached --name-only = %q, want openspec/changes/%s/ files", staged, change)
	}
	if _, err := os.Stat(stagingDir); !os.IsNotExist(err) {
		t.Fatalf("staging dir %s still present after materialize (stat err %v)", stagingDir, err)
	}

	// --- execute_state init: same plan, change already materialized ---
	initRes := callTool(t, c, "execute_state", map[string]any{
		"action":   "init",
		"branch":   branch,
		"quality":  "balanced",
		"planPath": planPath,
	})
	if !initRes.OK {
		t.Fatalf("execute_state init: not ok: code=%s body=%s", initRes.Code, initRes.Body)
	}
	if got := renderedSection(initRes.Body, "openspec"); got["materialized"] != "already" || got["change"] != change {
		t.Fatalf("execute_state init openspec = %v, want change=%s materialized=already; body:\n%s", got, change, initRes.Body)
	}
	execStatePath := renderedSection(initRes.Body, "Fields")["filePath"]
	if execStatePath == "" {
		t.Fatalf("execute_state init: no filePath in body:\n%s", initRes.Body)
	}

	// --- wave-1 commit on the feature branch (commits the materialized
	// change, which leaves the tree clean) ---
	mustWriteFile(t, filepath.Join(repo, "wave1.txt"), "wave 1\n")
	runGit(t, repo, "add", "wave1.txt")
	runGit(t, repo, "commit", "-q", "-m", "wave 1")

	// --- advance origin/main from a second clone ---
	other := realPath(t, t.TempDir())
	runGit(t, other, "clone", "-q", "--branch", "main", origin, ".")
	runGit(t, other, "config", "user.email", "integration-test@example.com")
	runGit(t, other, "config", "user.name", "integration-test")
	mustWriteFile(t, filepath.Join(other, "upstream.txt"), "upstream\n")
	runGit(t, other, "add", "upstream.txt")
	runGit(t, other, "commit", "-q", "-m", "upstream change")
	runGit(t, other, "push", "-q", "origin", "main")

	if porcelain := runGit(t, repo, "status", "--porcelain"); porcelain != "" {
		t.Fatalf("working tree not clean before base-sync:\n%s", porcelain)
	}

	// --- base-sync after wave 1 ---
	syncRes := callTool(t, c, "execute_state", map[string]any{
		"action": "base-sync",
		"branch": branch,
		"wave":   1,
	})
	if !syncRes.OK {
		t.Fatalf("execute_state base-sync: not ok: code=%s body=%s", syncRes.Code, syncRes.Body)
	}
	fields := renderedSection(syncRes.Body, "Fields")
	if fields["status"] != "merged" || fields["base"] != "main" {
		t.Fatalf("base-sync fields = %v, want status=merged base=main; body:\n%s", fields, syncRes.Body)
	}
	if _, err := os.Stat(filepath.Join(repo, "upstream.txt")); err != nil {
		t.Fatalf("upstream.txt not merged into %s: %v", branch, err)
	}

	// --- both state files match their JSON schemas ---
	validateAgainstSchema(t, shipStatePath, schemasDir, "ship-state.schema.json")
	validateAgainstSchema(t, execStatePath, schemasDir, "execute-state.schema.json")
}
