package tools

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
)

// TestGuardrailsPreflight_SecondReadErrorStopsRun pins the second config
// read of guardrailsPreflight: the first read (validateGuardrailsAction)
// succeeds and finds a bad guardrail, then the read of the descriptions
// fails. The run stops with an InfraError and returns no findings.
func TestGuardrailsPreflight_SecondReadErrorStopsRun(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, paths.DataDir, "config.toml"), ""+
		"[plan.guardrails.bad-rule]\n"+
		"description = \"\"\n")

	readErr := errors.New("disk gone")
	orig := guardrailDescReadSection
	guardrailDescReadSection = func(string, string) (map[string]any, error) { return nil, readErr }
	t.Cleanup(func() { guardrailDescReadSection = orig })

	findings, err := guardrailsPreflight(root)
	if findings != nil {
		t.Errorf("findings = %+v, want nil", findings)
	}
	var infra *mcpserver.InfraError
	if !errors.As(err, &infra) {
		t.Fatalf("want *mcpserver.InfraError, got %T: %v", err, err)
	}
	if !errors.Is(err, readErr) {
		t.Errorf("error %v does not wrap the read error", err)
	}
	if want := "read plan guardrails section: disk gone"; infra.Msg != want {
		t.Errorf("Msg = %q, want %q", infra.Msg, want)
	}
	if !strings.Contains(infra.Suggestion, "[plan]") {
		t.Errorf("Suggestion = %q, want it to name the [plan] section", infra.Suggestion)
	}
}
