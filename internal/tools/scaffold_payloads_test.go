package tools

import (
	"crypto/sha256"
	"fmt"
	"os"
	"strings"
	"testing"
)

// TestPayloads_WorkflowsMatchCheckedIn verifies every embedded workflow
// payload is byte-identical to this repo's own checked-in copy under
// .github/workflows/. scaffold_ci's drift detection reads a version comment
// (e.g. "# retag-release-version: N"), not a content hash — hardening a
// checked-in workflow (SHA-pinning an action, adding a permissions block)
// without also bumping its payload's version number desyncs the two
// silently: already-scaffolded projects would see "skipped" instead of
// "outdated" and never get the fix. This test catches that class of drift
// directly, independent of the version-comment mechanism.
func TestPayloads_WorkflowsMatchCheckedIn(t *testing.T) {
	payloads := Payloads()

	for _, entry := range scaffoldManifest {
		if !strings.HasSuffix(entry.PayloadKey, ".yml") {
			continue
		}

		payload, ok := payloads[entry.PayloadKey]
		if !ok {
			t.Fatalf("expected payload %q not found in Payloads()", entry.PayloadKey)
		}

		checkedInPath := "../../" + entry.Dest
		checkedIn, err := os.ReadFile(checkedInPath)
		if err != nil {
			t.Fatalf("os.ReadFile(%s): %v", checkedInPath, err)
		}

		if string(checkedIn) != string(payload) {
			t.Errorf("%s: checked-in file differs from embedded payload %q — "+
				"sync internal/tools/payloads/%s with %s (and bump its version comment if the checked-in copy was hardened without updating the template)",
				checkedInPath, entry.PayloadKey, entry.PayloadKey, checkedInPath)
		}
	}
}

// TestPayloads_CJSReadsV2ConfigOnly verifies that each embedded .cjs payload
// reads .sdlc-v2/config.json exclusively — no legacy .sdlc/config.json or
// .claude/sdlc.json fallback. Legacy config layouts are migration-only
// territory (the "migrate" tool), never a read path for CI scripts.
func TestPayloads_CJSReadsV2ConfigOnly(t *testing.T) {
	payloads := Payloads()

	cjsFiles := []string{
		"check-changelog.cjs",
		"retag-release.cjs",
		"release-on-main.cjs",
		"promote-release.cjs",
		"verify-release-intent.cjs",
	}
	for _, name := range cjsFiles {
		data, ok := payloads[name]
		if !ok {
			t.Fatalf("expected payload %q not found in Payloads()", name)
		}
		content := string(data)

		if !strings.Contains(content, ".sdlc-v2/config.json") {
			t.Errorf("%s: does not contain .sdlc-v2/config.json", name)
		}
		if strings.Contains(content, ".sdlc/config.json") {
			t.Errorf("%s: contains stale legacy fallback reference .sdlc/config.json", name)
		}
		if strings.Contains(content, ".claude/sdlc.json") {
			t.Errorf("%s: contains stale legacy fallback reference .claude/sdlc.json", name)
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
