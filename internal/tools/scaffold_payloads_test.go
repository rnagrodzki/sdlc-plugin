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
		"sdlc-local.schema.json":       "a57ee51d42a914686a0f033290e7c4dbaf0e5afe678a725e90c0fc860ba8b73a",
		"execute-state.schema.json":    "a7d7a39b12fb70a5473548fe93295add51d2858eff389c8c2f237e7b9764ad57",
		"ship-state.schema.json":       "f68360b93ac659492836470f1cac7b54b8ebfaad3dff92960fd44cb7276b1b7a",
		"review-dimension.schema.json": "feb7be29fd8142fe340274a4ecfa55787373b4ca492102085243d81888fa7f0d",
		"plugin.schema.json":           "41cf8d6ff6bd976cab3f841301b138a51d29f2ff2358eaff598d76455a188561",
	}

	for name, wantHex := range expected {
		path := "../../schemas/" + name
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
