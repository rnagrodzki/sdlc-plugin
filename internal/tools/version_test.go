package tools

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rnagrodzki/sdlc-plugin/internal/config"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
)

// writeVersionConfig writes a .sdlc-v2/config.json with the given version
// section content.
func writeVersionConfig(t *testing.T, dir string, versionJSON string) {
	t.Helper()
	sdlcDir := filepath.Join(dir, paths.DataDir)
	if err := os.MkdirAll(sdlcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := `{"version":` + versionJSON + `}`
	if err := os.WriteFile(filepath.Join(sdlcDir, "config.json"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// writePackageJSON writes a package.json with the given version.
func writePackageJSON(t *testing.T, dir, ver string) {
	t.Helper()
	content := `{"name":"test","version":"` + ver + `"}`
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestVersionPrepare_FetchTagsCalled verifies that versionPrepare calls
// FetchTags, making tags from the remote visible locally.
func TestVersionPrepare_FetchTagsCalled(t *testing.T) {
	// Create a bare "remote" repo.
	remote := t.TempDir()
	cmd := exec.Command("git", "init", "--bare")
	cmd.Dir = remote
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("init bare: %s: %v", out, err)
	}

	// Create a working repo that pushes to the bare remote.
	work := t.TempDir()
	initGitFixture(t, work)
	writePackageJSON(t, work, "1.0.0")
	gitCommit(t, work, "init-pkg")

	// Add remote and push.
	for _, args := range [][]string{
		{"git", "remote", "add", "origin", remote},
		{"git", "push", "-u", "origin", "main"},
	} {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = work
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %s: %v", args, out, err)
		}
	}

	// Create a tag and push it to the remote.
	gitTag(t, work, "v1.0.0")
	cmd = exec.Command("git", "push", "--tags")
	cmd.Dir = work
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("push tags: %s: %v", out, err)
	}

	// Delete the tag locally so it only exists on remote.
	cmd = exec.Command("git", "tag", "-d", "v1.0.0")
	cmd.Dir = work
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("delete local tag: %s: %v", out, err)
	}

	// Verify tag is gone locally.
	cmd = exec.Command("git", "tag", "--list")
	cmd.Dir = work
	tagOut, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("list tags: %s: %v", tagOut, err)
	}
	if strings.Contains(string(tagOut), "v1.0.0") {
		t.Fatal("v1.0.0 still present locally before prepare")
	}

	// Run versionPrepare -- FetchTags should restore the tag from remote.
	config.Quiet = true
	defer func() { config.Quiet = false }()

	out, err := versionPrepare(work, work, VersionPrepareIn{SkipConfigCheck: true})
	if err != nil {
		t.Fatalf("versionPrepare: %v", err)
	}

	// The remote tag should now appear.
	found := false
	for _, tag := range out.Tags.All {
		if tag == "v1.0.0" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected v1.0.0 in Tags.All after FetchTags; got %v", out.Tags.All)
	}
}

// TestVersionPrepare_BumpBaseFromRemoteTag verifies that when the highest
// remote tag exceeds the file version, bump options use the tag version as
// the base.
func TestVersionPrepare_BumpBaseFromRemoteTag(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	writePackageJSON(t, dir, "1.0.0")
	gitCommit(t, dir, "init-pkg")

	// Create a tag at v1.2.0 (higher than file's 1.0.0).
	gitTag(t, dir, "v1.2.0")
	gitCommit(t, dir, "post-tag")

	config.Quiet = true
	defer func() { config.Quiet = false }()

	out, err := versionPrepare(dir, dir, VersionPrepareIn{SkipConfigCheck: true})
	if err != nil {
		t.Fatalf("versionPrepare: %v", err)
	}

	// Bump base should be 1.2.0 (from tag), not 1.0.0 (from file).
	for _, opt := range out.BumpOptions {
		if opt.Current != "1.2.0" {
			t.Errorf("bump %s: current=%s, want 1.2.0", opt.Level, opt.Current)
		}
	}

	// Patch should be 1.2.1, not 1.0.1.
	for _, opt := range out.BumpOptions {
		if opt.Level == "patch" && opt.Result != "1.2.1" {
			t.Errorf("patch result=%s, want 1.2.1", opt.Result)
		}
	}
}

// TestVersionPrepare_DivergenceWarning verifies that a warning and
// DivergenceInfo are set when file version < highest tag.
func TestVersionPrepare_DivergenceWarning(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	writePackageJSON(t, dir, "1.0.0")
	gitCommit(t, dir, "init-pkg")
	gitTag(t, dir, "v2.0.0")
	gitCommit(t, dir, "post-tag")

	config.Quiet = true
	defer func() { config.Quiet = false }()

	out, err := versionPrepare(dir, dir, VersionPrepareIn{SkipConfigCheck: true})
	if err != nil {
		t.Fatalf("versionPrepare: %v", err)
	}

	if out.VersionDivergence == nil {
		t.Fatal("expected VersionDivergence to be set")
	}
	if out.VersionDivergence.FileVersion != "1.0.0" {
		t.Errorf("divergence fileVersion=%s, want 1.0.0", out.VersionDivergence.FileVersion)
	}
	if out.VersionDivergence.TagVersion != "2.0.0" {
		t.Errorf("divergence tagVersion=%s, want 2.0.0", out.VersionDivergence.TagVersion)
	}

	// A warning should mention the divergence.
	hasWarning := false
	for _, w := range out.Warnings {
		if strings.Contains(w, "behind") {
			hasWarning = true
			break
		}
	}
	if !hasWarning {
		t.Errorf("expected warning about divergence; got %v", out.Warnings)
	}
}

// TestVersionPrepare_ConfigPresent verifies that when a version config
// section exists, ConfigPresent is true and VersionConfig is populated.
func TestVersionPrepare_ConfigPresent(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	writePackageJSON(t, dir, "1.0.0")
	gitCommit(t, dir, "init-pkg")

	writeVersionConfig(t, dir, `{"mode":"file","versionFile":"package.json","fileType":"package.json","tagPrefix":"v","changelog":true}`)

	config.Quiet = true
	defer func() { config.Quiet = false }()

	out, err := versionPrepare(dir, dir, VersionPrepareIn{SkipConfigCheck: true})
	if err != nil {
		t.Fatalf("versionPrepare: %v", err)
	}

	if !out.ConfigPresent {
		t.Error("expected ConfigPresent=true")
	}
	if out.VersionConfig == nil {
		t.Fatal("expected VersionConfig to be set")
	}
	if out.VersionConfig.Mode != "file" {
		t.Errorf("versionConfig.Mode=%s, want file", out.VersionConfig.Mode)
	}
	if out.VersionConfig.TagPrefix != "v" {
		t.Errorf("versionConfig.TagPrefix=%s, want v", out.VersionConfig.TagPrefix)
	}
	if out.ProposedConfig != nil {
		t.Error("expected ProposedConfig to be nil when config is present")
	}
}

// TestVersionPrepare_ConfigMissing_ProposedConfig verifies that when no
// version config section exists, ProposedConfig is generated from the
// detected version file.
func TestVersionPrepare_ConfigMissing_ProposedConfig(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	writePackageJSON(t, dir, "0.1.0")
	gitCommit(t, dir, "init-pkg")

	config.Quiet = true
	defer func() { config.Quiet = false }()

	out, err := versionPrepare(dir, dir, VersionPrepareIn{SkipConfigCheck: true})
	if err != nil {
		t.Fatalf("versionPrepare: %v", err)
	}

	if out.ConfigPresent {
		t.Error("expected ConfigPresent=false")
	}
	if out.ProposedConfig == nil {
		t.Fatal("expected ProposedConfig to be set")
	}
	if mode, ok := out.ProposedConfig["mode"].(string); !ok || mode != "file" {
		t.Errorf("proposedConfig.mode=%v, want file", out.ProposedConfig["mode"])
	}
	if vf, ok := out.ProposedConfig["versionFile"].(string); !ok || vf != "package.json" {
		t.Errorf("proposedConfig.versionFile=%v, want package.json", out.ProposedConfig["versionFile"])
	}
}

// TestVersionPrepare_ModeTag_VersionFromTag verifies that mode:"tag" derives
// the current version from the highest semver git tag and computes bump
// options from it.
func TestVersionPrepare_ModeTag_VersionFromTag(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	writePackageJSON(t, dir, "1.0.0")
	gitCommit(t, dir, "init-pkg")

	gitTag(t, dir, "v1.2.0")
	gitCommit(t, dir, "post-tag")

	writeVersionConfig(t, dir, `{"mode":"tag","tagPrefix":"v"}`)

	config.Quiet = true
	defer func() { config.Quiet = false }()

	out, err := versionPrepare(dir, dir, VersionPrepareIn{SkipConfigCheck: true})
	if err != nil {
		t.Fatalf("versionPrepare: %v", err)
	}
	if len(out.Errors) != 0 {
		t.Errorf("expected no errors; got %v", out.Errors)
	}

	if out.VersionSource == nil {
		t.Fatal("expected VersionSource to be set")
	}
	if out.VersionSource.Type != "tag" {
		t.Errorf("versionSource.Type=%s, want tag", out.VersionSource.Type)
	}
	if out.VersionSource.Version != "1.2.0" {
		t.Errorf("versionSource.Version=%s, want 1.2.0", out.VersionSource.Version)
	}

	var patchOpt *VersionBumpOption
	for i := range out.BumpOptions {
		if out.BumpOptions[i].Level == "patch" {
			patchOpt = &out.BumpOptions[i]
			break
		}
	}
	if patchOpt == nil {
		t.Fatal("no patch bump option found")
	}
	if patchOpt.Result != "1.2.1" {
		t.Errorf("patch result=%s, want 1.2.1", patchOpt.Result)
	}

	if out.ProposedConfig != nil {
		t.Errorf("expected ProposedConfig=nil in tag mode; got %v", out.ProposedConfig)
	}
}

// TestVersionPrepare_ModeTag_NoTags verifies that mode:"tag" defaults to
// 0.0.0 with a warning when no semver tags exist.
func TestVersionPrepare_ModeTag_NoTags(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	writePackageJSON(t, dir, "1.0.0")
	gitCommit(t, dir, "init-pkg")

	writeVersionConfig(t, dir, `{"mode":"tag","tagPrefix":"v"}`)

	config.Quiet = true
	defer func() { config.Quiet = false }()

	out, err := versionPrepare(dir, dir, VersionPrepareIn{SkipConfigCheck: true})
	if err != nil {
		t.Fatalf("versionPrepare: %v", err)
	}

	if out.VersionSource == nil {
		t.Fatal("expected VersionSource to be set")
	}
	if out.VersionSource.Version != "0.0.0" {
		t.Errorf("versionSource.Version=%s, want 0.0.0", out.VersionSource.Version)
	}

	var patchOpt *VersionBumpOption
	for i := range out.BumpOptions {
		if out.BumpOptions[i].Level == "patch" {
			patchOpt = &out.BumpOptions[i]
			break
		}
	}
	if patchOpt == nil {
		t.Fatal("no patch bump option found")
	}
	if patchOpt.Result != "0.0.1" {
		t.Errorf("patch result=%s, want 0.0.1", patchOpt.Result)
	}

	hasWarning := false
	for _, w := range out.Warnings {
		if strings.Contains(w, "defaulting to 0.0.0") {
			hasWarning = true
			break
		}
	}
	if !hasWarning {
		t.Errorf("expected warning about defaulting to 0.0.0; got %v", out.Warnings)
	}
}

// TestVersionPrepare_ModeTag_IgnoresVersionFile verifies that mode:"tag"
// derives the version from git tags even when a version file with a
// different version is present.
func TestVersionPrepare_ModeTag_IgnoresVersionFile(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	writePackageJSON(t, dir, "9.9.9")
	gitCommit(t, dir, "init-pkg")

	gitTag(t, dir, "v1.0.0")
	gitCommit(t, dir, "post-tag")

	writeVersionConfig(t, dir, `{"mode":"tag","tagPrefix":"v"}`)

	config.Quiet = true
	defer func() { config.Quiet = false }()

	out, err := versionPrepare(dir, dir, VersionPrepareIn{SkipConfigCheck: true})
	if err != nil {
		t.Fatalf("versionPrepare: %v", err)
	}

	if out.VersionSource == nil {
		t.Fatal("expected VersionSource to be set")
	}
	if out.VersionSource.Version != "1.0.0" {
		t.Errorf("versionSource.Version=%s, want 1.0.0 (from tag, not package.json)", out.VersionSource.Version)
	}
}

// TestVersionPrepare_IdempotencyDetection verifies that when a semver tag
// exists at HEAD, alreadyBumped=true and tagAtHead is set.
func TestVersionPrepare_IdempotencyDetection(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	writePackageJSON(t, dir, "1.0.0")
	gitCommit(t, dir, "init-pkg")
	gitTag(t, dir, "v1.0.0")

	config.Quiet = true
	defer func() { config.Quiet = false }()

	out, err := versionPrepare(dir, dir, VersionPrepareIn{SkipConfigCheck: true})
	if err != nil {
		t.Fatalf("versionPrepare: %v", err)
	}

	if !out.Idempotency.AlreadyBumped {
		t.Error("expected AlreadyBumped=true")
	}
	if out.Idempotency.TagAtHead != "v1.0.0" {
		t.Errorf("tagAtHead=%s, want v1.0.0", out.Idempotency.TagAtHead)
	}

	// Summary should mention already tagged.
	if !strings.Contains(out.Summary, "already tagged") {
		t.Errorf("expected summary to mention already tagged; got %q", out.Summary)
	}

	// Next should be empty.
	if out.Next != "" {
		t.Errorf("expected next=\"\" when already bumped; got %q", out.Next)
	}
}

// TestVersionPrepare_ExistingRCs verifies that existing RC tags are collected
// and rcNext is computed correctly.
func TestVersionPrepare_ExistingRCs(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	writePackageJSON(t, dir, "1.2.0")
	gitCommit(t, dir, "init-pkg")

	// Create RC tags for 1.3.0 (the expected minor bump target).
	gitTag(t, dir, "v1.3.0-rc1")
	gitTag(t, dir, "v1.3.0-rc2")
	gitCommit(t, dir, "after-rcs")

	config.Quiet = true
	defer func() { config.Quiet = false }()

	out, err := versionPrepare(dir, dir, VersionPrepareIn{SkipConfigCheck: true})
	if err != nil {
		t.Fatalf("versionPrepare: %v", err)
	}

	// Find the minor bump option.
	var minorOpt *VersionBumpOption
	for i := range out.BumpOptions {
		if out.BumpOptions[i].Level == "minor" {
			minorOpt = &out.BumpOptions[i]
			break
		}
	}
	if minorOpt == nil {
		t.Fatal("no minor bump option found")
	}
	if minorOpt.Result != "1.3.0" {
		t.Fatalf("minor result=%s, want 1.3.0", minorOpt.Result)
	}

	// RC next should be rc3 (after rc1, rc2).
	if minorOpt.RCNext != "1.3.0-rc3" {
		t.Errorf("rcNext=%s, want 1.3.0-rc3", minorOpt.RCNext)
	}

	// ExistingRCs should contain the two RC tags.
	rcs, ok := out.ExistingRCs["1.3.0"]
	if !ok {
		t.Fatalf("ExistingRCs missing key 1.3.0; got %v", out.ExistingRCs)
	}
	if len(rcs) != 2 {
		t.Errorf("expected 2 existing RCs for 1.3.0, got %d: %v", len(rcs), rcs)
	}
}

// TestVersionSuggestedPreRelease covers all three PreReleasePolicy values
// against both existing-RCs states. Pure unit test, no filesystem or git
// fixtures — versionSuggestedPreRelease is a pure function.
func TestVersionSuggestedPreRelease(t *testing.T) {
	cases := []struct {
		name           string
		policy         string
		hasExistingRCs bool
		want           string
	}{
		{"always-rc suggests RC with no existing RCs", "always-rc", false, "rc"},
		{"always-rc suggests RC with existing RCs", "always-rc", true, "rc"},
		{"continue-rc suggests RC only when existing RCs found", "continue-rc", true, "rc"},
		{"continue-rc suggests nothing with no existing RCs", "continue-rc", false, ""},
		{"never suggests nothing with existing RCs", "never", true, ""},
		{"never suggests nothing with no existing RCs", "never", false, ""},
		{"unrecognized policy falls through like never", "bogus", true, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := versionSuggestedPreRelease(tc.policy, tc.hasExistingRCs)
			if got != tc.want {
				t.Errorf("versionSuggestedPreRelease(%q, %v) = %q, want %q", tc.policy, tc.hasExistingRCs, got, tc.want)
			}
		})
	}
}
