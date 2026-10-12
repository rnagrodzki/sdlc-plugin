//go:build integration

package tools

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rnagrodzki/sdlc-plugin/internal/ghx"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
)

// errorReportCallArgs returns the arguments of a prepare_orchestrator call in
// error_report mode for skill.
func errorReportCallArgs(skill string) map[string]any {
	return map[string]any{
		"mode":      "error_report",
		"skill":     skill,
		"step":      "step-1",
		"operation": "do-thing",
		"errorText": "boom",
	}
}

// TestIntegration_PrepareOrchestratorErrorReport_ReturnsSimilarIssues calls
// the registered prepare_orchestrator tool in error_report mode with a fake
// search that returns two issues. The tool output lists only the issue whose
// title starts with the skill tag, and next holds the offer sentence for 1
// issue. The manifest on disk is written under the redirected temp dir.
func TestIntegration_PrepareOrchestratorErrorReport_ReturnsSimilarIssues(t *testing.T) {
	tmpDir := redirectTempDir(t)
	newOrchestratorRepo(t, "")
	calls := stubErrorReportIssueList(t, []ghx.Issue{
		{Number: 91, Title: "[harden] pre-flight stop", URL: "https://example.test/91"},
		{Number: 40, Title: "[plan] unrelated", URL: "https://example.test/40"},
	}, nil)

	res, text := callRegisteredPrepareOrchestratorText(t, errorReportCallArgs("harden"))
	if res.IsError {
		t.Fatalf("prepare_orchestrator returned a tool error:\n%s", text)
	}

	manifestPath := manifestPathFromText(t, text)
	if !strings.HasPrefix(manifestPath, tmpDir+string(filepath.Separator)) {
		t.Errorf("manifestPath %q is not under the redirected temp dir %q", manifestPath, tmpDir)
	}
	manifest := readErrorReportManifest(t, manifestPath)
	if manifest["skill"] != "harden" || manifest["operation"] != "do-thing" {
		t.Errorf("unexpected manifest content: %+v", manifest)
	}

	wantNext := prepareNextErrorReport + " After Step 6, Step 6b offers the 1 similar open issue(s) before a new issue."
	wantEntry := "## similarIssues[0]\n- number: 91\n- title: [harden] pre-flight stop\n- url: https://example.test/91"
	for _, want := range []string{wantEntry, "**Next:** " + wantNext + "\n"} {
		if !strings.Contains(text, want) {
			t.Errorf("output missing %q:\n%s", want, text)
		}
	}
	for _, unwanted := range []string{"[plan] unrelated", "similarIssuesNote"} {
		if strings.Contains(text, unwanted) {
			t.Errorf("output must not contain %q:\n%s", unwanted, text)
		}
	}
	if len(*calls) != 1 || !reflect.DeepEqual((*calls)[0].labels, []string{"tooling-error", "harden"}) || (*calls)[0].limit != 10 {
		t.Errorf("gh issue list calls = %+v, want one call with labels tooling-error,harden and limit 10", *calls)
	}
}

// TestIntegration_PrepareOrchestratorErrorReport_NoMatchOmitsNote calls the
// registered tool with a fake search that finds nothing. The call succeeds,
// the output has no issue entries and no note, and next is the base text.
func TestIntegration_PrepareOrchestratorErrorReport_NoMatchOmitsNote(t *testing.T) {
	redirectTempDir(t)
	newOrchestratorRepo(t, "")
	stubErrorReportIssueList(t, nil, nil)

	res, text := callRegisteredPrepareOrchestratorText(t, errorReportCallArgs("harden"))
	if res.IsError {
		t.Fatalf("prepare_orchestrator returned a tool error:\n%s", text)
	}

	if !strings.Contains(text, "**Next:** "+prepareNextErrorReport+"\n") {
		t.Errorf("next is not exactly the base text %q:\n%s", prepareNextErrorReport, text)
	}
	if strings.Contains(text, "Step 6b") || strings.Contains(text, "similarIssuesNote") || strings.Contains(text, "Duplicate search skipped") {
		t.Errorf("output must carry neither the offer sentence nor a note:\n%s", text)
	}
	if !strings.Contains(text, "## similarIssues\n(none)") {
		t.Errorf("output must show similarIssues as an empty list:\n%s", text)
	}
}

// TestIntegration_PrepareOrchestratorErrorReport_SearchFailureStillSucceeds
// calls the registered tool with a fake search that fails the way gh does
// without authentication. The call succeeds, the manifest is written, and the
// output carries the exact note text with no issue entries. Next ends with the
// note, so the skipped search does not read as "no match".
func TestIntegration_PrepareOrchestratorErrorReport_SearchFailureStillSucceeds(t *testing.T) {
	redirectTempDir(t)
	newOrchestratorRepo(t, "")
	stubErrorReportIssueList(t, nil, &ghx.ExitError{Command: "gh issue list", ExitCode: 4, Stderr: "To get started with GitHub CLI, please run:  gh auth login\nsecond line"})

	res, text := callRegisteredPrepareOrchestratorText(t, errorReportCallArgs("harden"))
	if res.IsError {
		t.Fatalf("a search failure must not make the call fail:\n%s", text)
	}

	manifest := readErrorReportManifest(t, manifestPathFromText(t, text))
	if manifest["skill"] != "harden" {
		t.Errorf("manifest skill = %v, want harden", manifest["skill"])
	}
	wantNote := "duplicate search skipped: gh issue list failed: To get started with GitHub CLI, please run:  gh auth login"
	if !strings.Contains(text, "- similarIssuesNote: "+wantNote) {
		t.Errorf("output missing the note bullet %q:\n%s", wantNote, text)
	}
	if strings.Contains(text, "second line") || strings.Contains(text, "Step 6b") {
		t.Errorf("output must carry only the first stderr line and the base next:\n%s", text)
	}
	if !strings.Contains(text, "## similarIssues\n(none)") {
		t.Errorf("output must show similarIssues as an empty list:\n%s", text)
	}
	wantNext := prepareNextErrorReport + " Duplicate search skipped: gh issue list failed: To get started with GitHub CLI, please run:  gh auth login"
	if !strings.Contains(text, "**Next:** "+wantNext+"\n") {
		t.Errorf("next is not the base text followed by the skip note %q:\n%s", wantNext, text)
	}
}

// TestIntegration_PrepareOrchestratorHarden_HasNoSimilarIssues calls the
// registered tool in harden mode. The harden output must not carry
// similarIssues or similarIssuesNote, and the duplicate search must not run.
func TestIntegration_PrepareOrchestratorHarden_HasNoSimilarIssues(t *testing.T) {
	redirectTempDir(t)
	newOrchestratorRepo(t, ""+
		"[execute.guardrails.good-rule]\n"+
		"description = \"Every task names its files.\"\n")
	calls := stubErrorReportIssueList(t, []ghx.Issue{{Number: 1, Title: "[ship] x", URL: "u"}}, nil)

	res, text := callRegisteredPrepareOrchestratorText(t, map[string]any{
		"mode":            "harden",
		"skill":           "ship",
		"failureText":     "boom",
		"skipConfigCheck": true,
	})
	if res.IsError {
		t.Fatalf("prepare_orchestrator returned a tool error:\n%s", text)
	}

	if strings.Contains(text, "similarIssues") {
		t.Errorf("harden output must not mention similarIssues:\n%s", text)
	}
	if len(*calls) != 0 {
		t.Errorf("harden mode ran the duplicate search: %+v", *calls)
	}
}

// TestIntegration_PrepareOrchestrator_DescriptionNamesSimilarIssues lists the
// registered tools over an in-memory MCP session and checks that the
// prepare_orchestrator description holds the sentences about the duplicate
// search.
func TestIntegration_PrepareOrchestrator_DescriptionNamesSimilarIssues(t *testing.T) {
	s := mcpserver.New("test", "0.0.0-test")
	RegisterPrepareOrchestratorTools(s)

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

	list, err := c.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	var description string
	for _, tool := range list.Tools {
		if tool.Name == "prepare_orchestrator" {
			description = tool.Description
		}
	}
	want := "error_report mode also runs gh issue list once (labels tooling-error and the skill, open issues, limit 10) and returns similarIssues: the issues whose title starts with [<skill>]. A gh failure does not fail the call: similarIssues is [] and similarIssuesNote says why. When the search was skipped, next ends with that note."
	if !strings.Contains(description, want) {
		t.Errorf("prepare_orchestrator description does not contain %q:\n%s", want, description)
	}
}
