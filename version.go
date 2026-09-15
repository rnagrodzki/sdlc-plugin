// Package version is the single source of truth for the sdlc plugin's
// version string. It embeds plugins/sdlc/.claude-plugin/plugin.json at
// compile time, so the version reported by the binary (MCP server,
// hooks.PluginVersion, `sdlc version`) can never drift from the manifest —
// there is nothing left to hand-sync after a release promotion bumps the
// manifest's "version" field.
package version

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"runtime/debug"
)

//go:embed plugins/sdlc/.claude-plugin/plugin.json
var manifestJSON []byte

// Plugin is the sdlc plugin's version, read from plugin.json's "version"
// field at compile time.
var Plugin = mustParsePluginVersion(manifestJSON)

// overrideCommit and overrideTime are set via `-ldflags "-X"` at release
// build time (see .goreleaser.yaml and Taskfile.yml's build task). They are
// empty in an ordinary `go build`, where GetBuildInfo falls back to the VCS
// metadata the Go toolchain embeds automatically when building inside a git
// checkout.
var (
	overrideCommit string
	overrideTime   string
)

// BuildInfo is the sdlc binary's build metadata, surfaced by `sdlc version`
// and the session-start hook.
type BuildInfo struct {
	PluginVersion string `json:"pluginVersion"`
	Commit        string `json:"commit"`
	Time          string `json:"buildTime"`
}

// GetBuildInfo returns the binary's build metadata. Commit and Time are each
// resolved independently, in priority order: an -ldflags override baked in
// at release build time, then the Go toolchain's own VCS stamping
// (runtime/debug.ReadBuildInfo's vcs.revision/vcs.time settings, present for
// `go build` run inside a git checkout), then the literal fallbacks
// "dev"/"unknown" when neither source is available.
func GetBuildInfo() BuildInfo {
	vcsCommit, vcsTime := vcsBuildInfo()
	return resolveBuildInfo(overrideCommit, overrideTime, vcsCommit, vcsTime)
}

// resolveBuildInfo applies the priority/fallback/truncation rules documented
// on GetBuildInfo without touching the process's actual build metadata,
// keeping the resolution logic itself unit-testable.
func resolveBuildInfo(overrideCommit, overrideTime, vcsCommit, vcsTime string) BuildInfo {
	commit := overrideCommit
	if commit == "" {
		commit = vcsCommit
	}
	buildTime := overrideTime
	if buildTime == "" {
		buildTime = vcsTime
	}

	switch {
	case commit == "":
		commit = "dev"
	case len(commit) > 7:
		commit = commit[:7]
	}
	if buildTime == "" {
		buildTime = "unknown"
	}

	return BuildInfo{
		PluginVersion: Plugin,
		Commit:        commit,
		Time:          buildTime,
	}
}

// vcsBuildInfo reads commit/time from the Go toolchain's embedded VCS
// metadata (populated automatically by `go build` run inside a git
// checkout), returning empty strings when unavailable.
func vcsBuildInfo() (commit, buildTime string) {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "", ""
	}
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			commit = s.Value
		case "vcs.time":
			buildTime = s.Value
		}
	}
	return commit, buildTime
}

func mustParsePluginVersion(raw []byte) string {
	var manifest struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		panic(fmt.Sprintf("version: parse plugin.json: %v", err))
	}
	if manifest.Version == "" {
		panic("version: plugin.json has no \"version\" field")
	}
	return manifest.Version
}
