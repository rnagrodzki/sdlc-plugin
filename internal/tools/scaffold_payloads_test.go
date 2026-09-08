package tools

import (
	"crypto/sha256"
	"fmt"
	"os"
	"strings"
	"testing"
)

// TestPayloads_CJSReadOrder verifies that each embedded .cjs payload reads
// .sdlc/config.json before .claude/sdlc.json (the v5 config-first order).
func TestPayloads_CJSReadOrder(t *testing.T) {
	payloads := Payloads()

	cjsFiles := []string{"check-changelog.cjs", "retag-release.cjs"}
	for _, name := range cjsFiles {
		data, ok := payloads[name]
		if !ok {
			t.Fatalf("expected payload %q not found in Payloads()", name)
		}
		content := string(data)

		sdlcIdx := strings.Index(content, ".sdlc/config.json")
		claudeIdx := strings.Index(content, ".claude/sdlc.json")

		if sdlcIdx < 0 {
			t.Errorf("%s: does not contain .sdlc/config.json", name)
			continue
		}
		if claudeIdx < 0 {
			t.Errorf("%s: does not contain .claude/sdlc.json", name)
			continue
		}
		if sdlcIdx >= claudeIdx {
			t.Errorf("%s: .sdlc/config.json (index %d) must appear before .claude/sdlc.json (index %d)",
				name, sdlcIdx, claudeIdx)
		}
	}
}

// TestPayloads_SchemaChecksums verifies that the 5 ported schemas are
// byte-identical to their source by comparing SHA-256 digests.
func TestPayloads_SchemaChecksums(t *testing.T) {
	// Expected SHA-256 hex digests computed from the source schemas.
	expected := map[string]string{
		"sdlc-local.schema.json":       "e9b58dd50dadc8ccde4b88eba43d5a822380e24b9e838ccf5e75b10fedf35aa2",
		"execute-state.schema.json":    "4e86a84ace4ed6814f1621f2795a7f13730373072efd7513fa90abdd6f407d17",
		"ship-state.schema.json":       "3d24e349d3e26c92ee85100ce20aad89632f6dd2418e36249bc07ff27e8b908f",
		"review-dimension.schema.json": "107a3d573e0e1b45edf7e31b547c5f0b8f5c7ccca193923255ace6f93f8a559d",
		"plugin.schema.json":           "c774282b3c8c54fc7418b767c5043353e11f65138b0be010cef8d57a6270fa67",
	}

	for name, wantHex := range expected {
		path := "../../plugins/sdlc/schemas/" + name
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("os.ReadFile(%s): %v", path, err)
		}
		gotHex := fmt.Sprintf("%x", sha256.Sum256(data))
		if gotHex != wantHex {
			t.Errorf("schema %s: checksum mismatch\n  got:  %s\n  want: %s", name, gotHex, wantHex)
		}
	}
}

// TestPayloads_ContainsExpectedFiles verifies the embedded payload set
// contains all expected files.
func TestPayloads_ContainsExpectedFiles(t *testing.T) {
	payloads := Payloads()

	expectedFiles := []string{
		"check-changelog.cjs",
		"retag-release.cjs",
		"check-changelog.yml",
		"retag-release.yml",
		"release-on-main.cjs",
		"promote-release.cjs",
		"verify-release-intent.cjs",
		"release-on-main.yml",
		"promote-release.yml",
		"verify-release-intent.yml",
	}

	for _, name := range expectedFiles {
		if _, ok := payloads[name]; !ok {
			t.Errorf("expected payload %q not found in Payloads()", name)
		}
		if len(payloads[name]) == 0 {
			t.Errorf("payload %q is empty", name)
		}
	}

	if len(payloads) != len(expectedFiles) {
		t.Errorf("Payloads() returned %d entries, expected %d", len(payloads), len(expectedFiles))
	}
}
