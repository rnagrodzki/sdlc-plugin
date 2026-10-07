package tools

import (
	"crypto/sha256"
	"fmt"
	"os"
	"strings"
	"testing"
)

// TestPayloads_MatchCheckedIn verifies every embedded payload (workflow
// .yml and CI script .cjs alike) is byte-identical to this repo's own
// checked-in copy under .github/. scaffold_ci's drift detection reads a
// version comment (e.g. "# check-changelog-version: N") or a version const
// (e.g. "const CHECK_CHANGELOG_SCRIPT_VERSION = N"), not a content hash — hardening a
// checked-in file (SHA-pinning an action, adding a permissions block,
// tightening a .cjs helper) without also bumping its payload's version
// number desyncs the two silently: already-scaffolded projects would see
// "skipped" instead of "outdated" and never get the fix. This test catches
// that class of drift directly, independent of the version mechanism.
func TestPayloads_MatchCheckedIn(t *testing.T) {
	payloads := Payloads()

	for _, entry := range scaffoldManifest {
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
// reads .sdlc-v2/config.toml exclusively — no legacy .sdlc/config.json,
// .claude/sdlc.json, or stale .sdlc-v2/config.json (the pre-TOML-migration
// current-path, now itself a legacy layout) fallback. Legacy config layouts
// are migration-only territory (the "migrate" tool), never a read path for
// CI scripts.
func TestPayloads_CJSReadsV2ConfigOnly(t *testing.T) {
	payloads := Payloads()

	cjsFiles := []string{
		"check-changelog.cjs",
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

		if !strings.Contains(content, ".sdlc-v2/config.toml") {
			t.Errorf("%s: does not contain .sdlc-v2/config.toml", name)
		}
		if strings.Contains(content, ".sdlc-v2/config.json") {
			t.Errorf("%s: contains stale pre-migration reference .sdlc-v2/config.json", name)
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
		"sdlc-local.schema.json":       "268d258946586a3ec220f2be86e13a4c8c8fb3f82c1b2be6b4c34cc4f6e505d2",
		"execute-state.schema.json":    "87d0cd29d18fce37f88a0113b7a9988ee20e06badd1a4f62f798359fde4caab5",
		"ship-state.schema.json":       "c0fdcd5676a25e505676b90a51b27ef108027c9aef79e18f646b3b724cc71985",
		"review-dimension.schema.json": "5b02617f32c1d1b21b16597e2799cfee795896ae4f1983812e19f65bbc1cec3b",
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
		"check-changelog.yml",
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
