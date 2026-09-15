package version

import "testing"

func TestResolveBuildInfo_OverrideTakesPriority(t *testing.T) {
	got := resolveBuildInfo("abc1234full", "2026-09-14T15:44:21Z", "vcscommitvalue", "2020-01-01T00:00:00Z")
	if got.Commit != "abc1234" {
		t.Errorf("Commit = %q, want override (truncated to 7 chars) %q", got.Commit, "abc1234")
	}
	if got.Time != "2026-09-14T15:44:21Z" {
		t.Errorf("Time = %q, want override %q", got.Time, "2026-09-14T15:44:21Z")
	}
	if got.PluginVersion != Plugin {
		t.Errorf("PluginVersion = %q, want %q", got.PluginVersion, Plugin)
	}
}

func TestResolveBuildInfo_FallsBackToVCS(t *testing.T) {
	got := resolveBuildInfo("", "", "0123456789abcdef", "2020-01-01T00:00:00Z")
	if got.Commit != "0123456" {
		t.Errorf("Commit = %q, want VCS commit truncated to 7 chars %q", got.Commit, "0123456")
	}
	if got.Time != "2020-01-01T00:00:00Z" {
		t.Errorf("Time = %q, want VCS time %q", got.Time, "2020-01-01T00:00:00Z")
	}
}

func TestResolveBuildInfo_DefaultsWhenNeitherAvailable(t *testing.T) {
	got := resolveBuildInfo("", "", "", "")
	if got.Commit != "dev" {
		t.Errorf("Commit = %q, want default %q", got.Commit, "dev")
	}
	if got.Time != "unknown" {
		t.Errorf("Time = %q, want default %q", got.Time, "unknown")
	}
}

func TestResolveBuildInfo_ShortCommitNotTruncated(t *testing.T) {
	got := resolveBuildInfo("abc12", "", "", "")
	if got.Commit != "abc12" {
		t.Errorf("Commit = %q, want untouched short override %q", got.Commit, "abc12")
	}
}

func TestGetBuildInfo_NeverPanics(t *testing.T) {
	info := GetBuildInfo()
	if info.PluginVersion != Plugin {
		t.Errorf("PluginVersion = %q, want %q", info.PluginVersion, Plugin)
	}
	if info.Commit == "" {
		t.Error("Commit should never be empty")
	}
	if info.Time == "" {
		t.Error("Time should never be empty")
	}
}
