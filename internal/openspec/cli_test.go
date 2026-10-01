package openspec

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// cliStub pairs the stdout an `openspec` stub invocation prints with the
// exit code it returns.
type cliStub struct {
	stdout string
	exit   int
}

// stubOpenspecDispatch writes an `openspec` stub whose output and exit code
// depend on its arguments, keyed by the space-joined argument list (e.g.
// "list --json", "status --change add-widget --json"). Mirrors
// stubGitDispatch's argument-dispatch style (openspec_test.go:48), extended
// to also control exit code: this file's error detection must work for both
// a plain non-zero exit and the CLI's observed exit-0-with-error-status
// shape (F-openspec-cli-stage-materialize-6), so tests need to control both
// independently of stdout content.
func stubOpenspecDispatch(t *testing.T, dir string, cases map[string]cliStub) {
	t.Helper()
	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	b.WriteString("args=\"$*\"\n")
	b.WriteString("case \"$args\" in\n")
	for pattern, stub := range cases {
		escaped := strings.ReplaceAll(stub.stdout, "'", `'\''`)
		fmt.Fprintf(&b, "  '%s') printf '%%s\\n' '%s'; exit %d ;;\n", pattern, escaped, stub.exit)
	}
	b.WriteString("  *) exit 127 ;;\n")
	b.WriteString("esac\n")
	path := filepath.Join(dir, "openspec")
	if err := os.WriteFile(path, []byte(b.String()), 0o755); err != nil {
		t.Fatalf("write openspec dispatch stub: %v", err)
	}
}

// ---------------------------------------------------------------------------
// List
// ---------------------------------------------------------------------------

func TestCLIWrappers_List_ChangesAndWarnings(t *testing.T) {
	dir := withStubPath(t)
	stubOpenspecDispatch(t, dir, map[string]cliStub{
		"list --json": {
			stdout: `{
  "changes": [
    {"name":"grp","completedTasks":0,"totalTasks":0,"status":"no-tasks","nested":["grp/demo"]},
    {"name":"add-widget","completedTasks":1,"totalTasks":3,"status":"in-progress"}
  ],
  "warnings": [
    {"code":"nested_change_directory","name":"grp","nested":["grp/demo"],"message":"\"grp\" is not a change: it is a folder wrapping openspec/changes/grp/demo/."}
  ],
  "root": {"path":"/repo","source":"nearest"}
}`,
			exit: 0,
		},
	})
	root := t.TempDir()

	result, err := List(root)
	if err != nil {
		t.Fatalf("List: unexpected error: %v", err)
	}
	if len(result.Changes) != 2 {
		t.Fatalf("List: got %d changes, want 2", len(result.Changes))
	}
	if result.Changes[0].Name != "grp" || result.Changes[1].Name != "add-widget" {
		t.Fatalf("List: Changes = %+v, want order [grp, add-widget]", result.Changes)
	}
	if result.Changes[1].CompletedTasks != 1 || result.Changes[1].TotalTasks != 3 || result.Changes[1].Status != "in-progress" {
		t.Fatalf("List: Changes[1] = %+v, want CompletedTasks=1 TotalTasks=3 Status=in-progress", result.Changes[1])
	}
	if len(result.Warnings) != 1 {
		t.Fatalf("List: got %d warnings, want 1", len(result.Warnings))
	}
	w := result.Warnings[0]
	if w.Code != "nested_change_directory" || w.Name != "grp" || len(w.Nested) != 1 || w.Nested[0] != "grp/demo" {
		t.Fatalf("List: Warnings[0] = %+v, want nested_change_directory for grp -> [grp/demo]", w)
	}
}

func TestCLIWrappers_List_NoWarnings(t *testing.T) {
	dir := withStubPath(t)
	stubOpenspecDispatch(t, dir, map[string]cliStub{
		"list --json": {stdout: `{"changes":[],"root":{"path":"/repo","source":"nearest"}}`, exit: 0},
	})
	root := t.TempDir()

	result, err := List(root)
	if err != nil {
		t.Fatalf("List: unexpected error: %v", err)
	}
	if len(result.Changes) != 0 {
		t.Fatalf("List: Changes = %+v, want empty", result.Changes)
	}
	if len(result.Warnings) != 0 {
		t.Fatalf("List: Warnings = %+v, want empty (key absent from CLI output)", result.Warnings)
	}
}

// ---------------------------------------------------------------------------
// ListSpecs
// ---------------------------------------------------------------------------

func TestCLIWrappers_ListSpecs(t *testing.T) {
	dir := withStubPath(t)
	stubOpenspecDispatch(t, dir, map[string]cliStub{
		"list --specs --json": {
			stdout: `{"specs":[{"id":"widget","requirementCount":2}],"root":{"path":"/repo","source":"nearest"}}`,
			exit:   0,
		},
	})
	root := t.TempDir()

	specs, err := ListSpecs(root)
	if err != nil {
		t.Fatalf("ListSpecs: unexpected error: %v", err)
	}
	if len(specs) != 1 {
		t.Fatalf("ListSpecs: got %d specs, want 1", len(specs))
	}
	if specs[0].ID != "widget" || specs[0].RequirementCount != 2 {
		t.Fatalf("ListSpecs: specs[0] = %+v, want {ID:widget RequirementCount:2}", specs[0])
	}
}

// ---------------------------------------------------------------------------
// Status
// ---------------------------------------------------------------------------

// statusFixture mirrors `openspec status --change add-widget --json`
// recorded against openspec CLI 1.13.2 in a throwaway project: artifacts[]
// (id, outputPath, requires) and artifactPaths[id].existingOutputPaths
// describe the same artifact from two different places in the payload.
const statusFixture = `{
  "changeName": "add-widget",
  "schemaName": "spec-driven",
  "artifactPaths": {
    "proposal": {"outputPath":"proposal.md","resolvedOutputPath":"/repo/openspec/changes/add-widget/proposal.md","existingOutputPaths":["/repo/openspec/changes/add-widget/proposal.md"]},
    "specs": {"outputPath":"specs/**/*.md","resolvedOutputPath":"/repo/openspec/changes/add-widget/specs/**/*.md","existingOutputPaths":[]},
    "design": {"outputPath":"design.md","resolvedOutputPath":"/repo/openspec/changes/add-widget/design.md","existingOutputPaths":[]},
    "tasks": {"outputPath":"tasks.md","resolvedOutputPath":"/repo/openspec/changes/add-widget/tasks.md","existingOutputPaths":[]}
  },
  "isPlanningComplete": false,
  "isComplete": false,
  "artifacts": [
    {"id":"proposal","outputPath":"proposal.md","status":"ready","requires":[]},
    {"id":"specs","outputPath":"specs/**/*.md","status":"blocked","requires":["proposal"],"missingDeps":["proposal"]},
    {"id":"design","outputPath":"design.md","status":"blocked","requires":["proposal"],"missingDeps":["proposal"]},
    {"id":"tasks","outputPath":"tasks.md","status":"blocked","requires":["specs","design"],"missingDeps":["specs","design"]}
  ],
  "root": {"path":"/repo","source":"nearest"}
}`

func TestCLIWrappers_Status_ArtifactOrderAndMerge(t *testing.T) {
	dir := withStubPath(t)
	stubOpenspecDispatch(t, dir, map[string]cliStub{
		"status --change add-widget --json": {stdout: statusFixture, exit: 0},
	})
	root := t.TempDir()

	result, err := Status(root, "add-widget")
	if err != nil {
		t.Fatalf("Status: unexpected error: %v", err)
	}
	if result.ChangeName != "add-widget" || result.SchemaName != "spec-driven" {
		t.Fatalf("Status: ChangeName/SchemaName = %q/%q, want add-widget/spec-driven", result.ChangeName, result.SchemaName)
	}
	wantOrder := []string{"proposal", "specs", "design", "tasks"}
	if len(result.Artifacts) != len(wantOrder) {
		t.Fatalf("Status: got %d artifacts, want %d", len(result.Artifacts), len(wantOrder))
	}
	for i, id := range wantOrder {
		if result.Artifacts[i].ID != id {
			t.Fatalf("Status: Artifacts[%d].ID = %q, want %q (CLI order)", i, result.Artifacts[i].ID, id)
		}
	}

	proposal := result.Artifacts[0]
	if proposal.OutputPath != "proposal.md" {
		t.Fatalf("Status: proposal.OutputPath = %q, want proposal.md", proposal.OutputPath)
	}
	if len(proposal.ExistingOutputPaths) != 1 || proposal.ExistingOutputPaths[0] != "/repo/openspec/changes/add-widget/proposal.md" {
		t.Fatalf("Status: proposal.ExistingOutputPaths = %v, want the merged artifactPaths entry", proposal.ExistingOutputPaths)
	}

	tasks := result.Artifacts[3]
	if len(tasks.Requires) != 2 || tasks.Requires[0] != "specs" || tasks.Requires[1] != "design" {
		t.Fatalf("Status: tasks.Requires = %v, want [specs design]", tasks.Requires)
	}
	if len(tasks.ExistingOutputPaths) != 0 {
		t.Fatalf("Status: tasks.ExistingOutputPaths = %v, want empty", tasks.ExistingOutputPaths)
	}
}

func TestCLIWrappers_Status_ErrorStatus_NonZeroExit(t *testing.T) {
	dir := withStubPath(t)
	stubOpenspecDispatch(t, dir, map[string]cliStub{
		"status --change grp/demo --json": {
			stdout: `{"status":[{"severity":"error","code":"change_error","message":"Invalid change name 'grp/demo': Change name cannot contain path separators"}]}`,
			exit:   1,
		},
	})
	root := t.TempDir()

	_, err := Status(root, "grp/demo")
	if err == nil {
		t.Fatalf("Status: expected error for grouped change name, got nil")
	}
	if !strings.Contains(err.Error(), "Change name cannot contain path separators") {
		t.Fatalf("Status: error = %v, want message to surface the CLI's status entry", err)
	}
}

// TestCLIWrappers_ErrorStatus_ExitZero covers the acceptance criterion "a
// stdout status error entry becomes a Go error even with exit 0": the CLI
// has been observed to report an error-severity status entry while still
// exiting 0 for `instructions` (F-openspec-cli-stage-materialize-6), so
// error detection must not trust exit code alone.
func TestCLIWrappers_ErrorStatus_ExitZero(t *testing.T) {
	dir := withStubPath(t)
	stubOpenspecDispatch(t, dir, map[string]cliStub{
		"instructions proposal --change add-widget --json": {
			stdout: `{"status":[{"severity":"error","code":"change_error","message":"Artifact 'proposal' not found in schema 'spec-driven'."}]}`,
			exit:   0,
		},
	})
	root := t.TempDir()

	_, err := Instructions(root, "proposal", "add-widget")
	if err == nil {
		t.Fatalf("Instructions: expected error from exit-0 error-status envelope, got nil")
	}
	if !strings.Contains(err.Error(), "not found in schema") {
		t.Fatalf("Instructions: error = %v, want message to surface the CLI's status entry", err)
	}
}

// ---------------------------------------------------------------------------
// Instructions
// ---------------------------------------------------------------------------

func TestCLIWrappers_Instructions_WithContextAndRules(t *testing.T) {
	dir := withStubPath(t)
	stubOpenspecDispatch(t, dir, map[string]cliStub{
		"instructions proposal --change add-widget --json": {
			stdout: `{
  "changeName": "add-widget",
  "artifactId": "proposal",
  "schemaName": "spec-driven",
  "outputPath": "proposal.md",
  "existingOutputPaths": [],
  "description": "Initial proposal document outlining the change",
  "instruction": "Create the proposal document.",
  "context": "This project ships a CLI; keep proposals implementation-agnostic.",
  "rules": ["Keep it under 2 pages", "Cite the capability section"],
  "template": "# Proposal\n",
  "dependencies": [],
  "unlocks": ["specs", "design"],
  "root": {"path":"/repo","source":"nearest"}
}`,
			exit: 0,
		},
	})
	root := t.TempDir()

	result, err := Instructions(root, "proposal", "add-widget")
	if err != nil {
		t.Fatalf("Instructions: unexpected error: %v", err)
	}
	if result.ArtifactID != "proposal" || result.OutputPath != "proposal.md" {
		t.Fatalf("Instructions: ArtifactID/OutputPath = %q/%q, want proposal/proposal.md", result.ArtifactID, result.OutputPath)
	}
	if result.Template != "# Proposal\n" {
		t.Fatalf("Instructions: Template = %q, want the CLI template verbatim", result.Template)
	}
	if result.Instruction != "Create the proposal document." {
		t.Fatalf("Instructions: Instruction = %q", result.Instruction)
	}
	if result.Context != "This project ships a CLI; keep proposals implementation-agnostic." {
		t.Fatalf("Instructions: Context = %q, want the configured project context", result.Context)
	}
	if len(result.Rules) != 2 || result.Rules[0] != "Keep it under 2 pages" {
		t.Fatalf("Instructions: Rules = %v, want the configured per-artifact rules", result.Rules)
	}
}

func TestCLIWrappers_Instructions_WithoutContextAndRules(t *testing.T) {
	dir := withStubPath(t)
	stubOpenspecDispatch(t, dir, map[string]cliStub{
		"instructions proposal --change add-widget --json": {
			// No "context" or "rules" keys: the CLI omits both when the
			// project's openspec config sets neither.
			stdout: `{
  "changeName": "add-widget",
  "artifactId": "proposal",
  "schemaName": "spec-driven",
  "outputPath": "proposal.md",
  "existingOutputPaths": [],
  "instruction": "Create the proposal document.",
  "template": "# Proposal\n",
  "dependencies": [],
  "unlocks": ["specs", "design"],
  "root": {"path":"/repo","source":"nearest"}
}`,
			exit: 0,
		},
	})
	root := t.TempDir()

	result, err := Instructions(root, "proposal", "add-widget")
	if err != nil {
		t.Fatalf("Instructions: unexpected error: %v", err)
	}
	if result.Context != "" {
		t.Fatalf("Instructions: Context = %q, want empty (CLI omitted the key)", result.Context)
	}
	if result.Rules != nil {
		t.Fatalf("Instructions: Rules = %v, want nil (CLI omitted the key)", result.Rules)
	}
}

// ---------------------------------------------------------------------------
// Missing binary
// ---------------------------------------------------------------------------

func TestCLIWrappers_MissingBinary(t *testing.T) {
	withStubPath(t) // empty PATH: `openspec` does not resolve
	root := t.TempDir()

	if _, err := List(root); !errors.Is(err, ErrCLINotFound) {
		t.Fatalf("List: err = %v, want ErrCLINotFound", err)
	}
	if _, err := ListSpecs(root); !errors.Is(err, ErrCLINotFound) {
		t.Fatalf("ListSpecs: err = %v, want ErrCLINotFound", err)
	}
	if _, err := Status(root, "add-widget"); !errors.Is(err, ErrCLINotFound) {
		t.Fatalf("Status: err = %v, want ErrCLINotFound", err)
	}
	if _, err := Instructions(root, "proposal", "add-widget"); !errors.Is(err, ErrCLINotFound) {
		t.Fatalf("Instructions: err = %v, want ErrCLINotFound", err)
	}
}

// ---------------------------------------------------------------------------
// ValidChangeName
// ---------------------------------------------------------------------------

func TestValidChangeName(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"add-widget", true},
		{"widget", true},
		{"grp/demo", false},
		{"Add_Widget", false},
		{"-x", false},
		{"..", false},
		{"", false},
		{"x-", false},
		{"a--b", false},
	}
	for _, c := range cases {
		if got := ValidChangeName(c.name); got != c.want {
			t.Errorf("ValidChangeName(%q) = %v, want %v", c.name, got, c.want)
		}
	}
}
