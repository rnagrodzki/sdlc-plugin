// Package configmigrate provides schema versioning and migration for the
// SDLC plugin's configuration files. It handles migrating from legacy
// per-file layouts through numbered schema versions up to the current v5
// section-based format (which carries no schemaVersion marker).
package configmigrate

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/rnagrodzki/sdlc-plugin/internal/fsx"
)

// ---------------------------------------------------------------------------
// Migration step type
// ---------------------------------------------------------------------------

type migrationStep struct {
	from     int
	to       int
	run      func(ctx *migrationContext) error
	rollback func(ctx *migrationContext) error
}

type migrationContext struct {
	mainRoot   string
	configPath string // .sdlc/config.json
	localPath  string // .sdlc/local.json
	legacyPath string // .claude/sdlc.json
}

// ---------------------------------------------------------------------------
// Preset→steps mapping (ported from JS config-migrations.js)
// ---------------------------------------------------------------------------

var presetToSteps = map[string][]string{
	"full":     {"execute", "commit", "review", "version", "archive-openspec", "pr", "learnings-commit"},
	"balanced": {"execute", "commit", "review", "archive-openspec", "pr", "learnings-commit"},
	"minimal":  {"execute", "commit", "pr", "learnings-commit"},
	"A":        {"execute", "commit", "review", "version", "archive-openspec", "pr", "learnings-commit"},
	"B":        {"execute", "commit", "review", "archive-openspec", "pr", "learnings-commit"},
	"C":        {"execute", "commit", "pr", "learnings-commit"},
}

var allSteps = []string{"execute", "commit", "review", "version", "archive-openspec", "pr", "learnings-commit"}

// ---------------------------------------------------------------------------
// Project migration registry
// ---------------------------------------------------------------------------

var projectMigrations = []migrationStep{
	{from: 0, to: 3, run: relocateProjectConfig, rollback: cleanupRelocation},
	{from: 3, to: 4, run: noopProjectV3ToV4},
	{from: 4, to: 5, run: removeProjectSchemaVersion},
}

// relocateProjectConfig (v0→v3): copies .claude/sdlc.json to .sdlc/config.json.
// Ported from JS relocateProjectConfig. Drops $schema; idempotent when the
// target already has content.
func relocateProjectConfig(ctx *migrationContext) error {
	if ctx.legacyPath == "" {
		return ensureConfigExists(ctx.configPath)
	}

	var legacyData map[string]any
	err := fsx.ReadJSON(ctx.legacyPath, &legacyData)
	if err != nil {
		if errors.Is(err, fsx.ErrNotFound) {
			return ensureConfigExists(ctx.configPath)
		}
		return err
	}

	// Defence-in-depth: if config.json already has content, prefer it.
	var existing map[string]any
	if err := fsx.ReadJSON(ctx.configPath, &existing); err == nil && len(existing) > 0 {
		return nil
	}

	// Copy legacy → new, dropping $schema.
	delete(legacyData, "$schema")

	if err := os.MkdirAll(filepath.Dir(ctx.configPath), 0o755); err != nil {
		return err
	}
	return fsx.AtomicWriteJSON(ctx.configPath, legacyData)
}

func ensureConfigExists(configPath string) error {
	if _, err := os.Stat(configPath); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		return err
	}
	return fsx.AtomicWriteJSON(configPath, map[string]any{})
}

// cleanupRelocation is the rollback for relocateProjectConfig.
func cleanupRelocation(ctx *migrationContext) error {
	if ctx.legacyPath == "" {
		return nil
	}
	if _, err := os.Stat(ctx.legacyPath); err != nil {
		return nil // no legacy → can't safely undo
	}
	if _, err := os.Stat(ctx.configPath); err != nil {
		return nil
	}
	if err := os.Remove(ctx.configPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("rollback: remove %s: %w", ctx.configPath, err)
	}
	return nil
}

// noopProjectV3ToV4 (v3→v4): stamps schemaVersion=4 on config.json.
// The real v3→v4 work is in the local registry; the project registry
// needs this step so the version cursor advances.
func noopProjectV3ToV4(ctx *migrationContext) error {
	var data map[string]any
	err := fsx.ReadJSON(ctx.configPath, &data)
	if err != nil {
		if errors.Is(err, fsx.ErrNotFound) {
			return nil
		}
		return err
	}
	data["schemaVersion"] = float64(4)
	return fsx.AtomicWriteJSON(ctx.configPath, data)
}

// removeProjectSchemaVersion (v4→v5): removes the schemaVersion field and
// strips local-only sections (ship, review) from config.json, moving them
// to local.json with ship migrations applied. This covers the case where
// v0→v3 relocation copied a legacy sdlc.json that contained ship/review
// into config.json — those keys are invalid under the v5 schema
// (additionalProperties: false).
func removeProjectSchemaVersion(ctx *migrationContext) error {
	var data map[string]any
	err := fsx.ReadJSON(ctx.configPath, &data)
	if err != nil {
		if errors.Is(err, fsx.ErrNotFound) {
			return nil
		}
		return err
	}
	delete(data, "schemaVersion")

	if v, ok := data["version"]; ok {
		data["version"] = migrateVersionShape(v)
	}

	// Extract local-only sections from config.json.
	var localSections map[string]any
	for _, key := range []string{"ship", "review"} {
		if v, ok := data[key]; ok {
			if m, ok := v.(map[string]any); ok {
				if localSections == nil {
					localSections = make(map[string]any)
				}
				localSections[key] = m
			}
			delete(data, key)
		}
	}

	if err := fsx.AtomicWriteJSON(ctx.configPath, data); err != nil {
		return err
	}

	// Move extracted local sections to local.json if it doesn't already
	// have them. Apply ship migrations (preset→steps, awaitReview→steps)
	// since this local data was never run through the local migration chain.
	if localSections == nil {
		return nil
	}

	var localData map[string]any
	if err := fsx.ReadJSON(ctx.localPath, &localData); err != nil {
		if !errors.Is(err, fsx.ErrNotFound) {
			return err
		}
		localData = make(map[string]any)
	}

	for key, section := range localSections {
		if _, exists := localData[key]; exists {
			continue // local.json already has this section; keep it
		}
		if key == "ship" {
			if ship, ok := section.(map[string]any); ok {
				applyShipPresetToSteps(ship)
				applyShipAwaitReview(ship)
			}
		}
		localData[key] = section
	}

	if len(localData) > 0 {
		if err := os.MkdirAll(filepath.Dir(ctx.localPath), 0o755); err != nil {
			return err
		}
		return fsx.AtomicWriteJSON(ctx.localPath, localData)
	}
	return nil
}

// migrateVersionShape rewrites an old flat version section (top-level
// mode/versionFile string/tagPrefix/changelogMethod/changelog bool/
// changelogFile/rcAutoContinue/ticketPrefix) into the new nested
// tag/versionFile/changelog shape that internal/config's parseVersionSection
// requires. Old mode "tag" maps to tag-only, mode "file" (or unset,
// old default) maps to versionFile-only — the single path the project
// actually declared, not both, so migration never starts creating tags or
// GitHub Releases for a project that never had them. ticketPrefix has no
// new-shape equivalent and is dropped. Already-new-shape or unrecognized
// input (not a map, or none of the old-shape markers present) is returned
// unchanged.
func migrateVersionShape(v any) any {
	raw, ok := v.(map[string]any)
	if !ok || !isOldVersionShape(raw) {
		return v
	}

	out := map[string]any{}

	mode, _ := raw["mode"].(string)
	if mode == "tag" {
		tag := map[string]any{"enabled": true}
		if p, ok := raw["tagPrefix"].(string); ok {
			tag["prefix"] = p
		}
		out["tag"] = tag
	} else {
		vf := map[string]any{"enabled": true}
		if p, ok := raw["versionFile"].(string); ok {
			vf["path"] = p
		}
		if ft, ok := raw["fileType"].(string); ok {
			vf["fileType"] = ft
		}
		out["versionFile"] = vf
	}

	// changelogMethod ("pr"/"push"/"skip") takes precedence; legacy boolean
	// changelog (true→push, false→skip) is the fallback, matching the old
	// parseVersionSection's own backward-compat reading order.
	changelogMethod, hasMethod := raw["changelogMethod"].(string)
	if !hasMethod || changelogMethod == "" {
		if b, ok := raw["changelog"].(bool); ok {
			if b {
				changelogMethod = "push"
			} else {
				changelogMethod = "skip"
			}
		} else {
			changelogMethod = "skip"
		}
	}
	if changelogMethod != "skip" {
		cl := map[string]any{"enabled": true}
		if f, ok := raw["changelogFile"].(string); ok {
			cl["file"] = f
		}
		out["changelog"] = cl
		// Old changelogMethod "pr" is the only signal the old shape ever
		// carried for PR-style delivery. The new "method" field applies
		// uniformly to versionFile and changelog, so this is the closest
		// available mapping — a project that PR'd its changelog now also
		// PRs its version-file write, which works strictly better under
		// branch protection than the old always-push file write.
		if changelogMethod == "pr" {
			out["method"] = "pr"
		}
	}

	if s, ok := raw["preRelease"].(string); ok {
		out["preRelease"] = s
	}
	if s, ok := raw["preReleasePolicy"].(string); ok && s != "" {
		out["preReleasePolicy"] = s
	} else if b, ok := raw["rcAutoContinue"].(bool); ok {
		if b {
			out["preReleasePolicy"] = "continue-rc"
		} else {
			out["preReleasePolicy"] = "never"
		}
	}

	return out
}

// isOldVersionShape mirrors internal/config's detectOldVersionShape: mode,
// changelogMethod, and rcAutoContinue only ever existed in the old flat
// shape, so their mere presence is conclusive; a string versionFile or
// boolean changelog is that shape's signature too, since the new shape
// always nests both as objects.
func isOldVersionShape(raw map[string]any) bool {
	if _, ok := raw["mode"]; ok {
		return true
	}
	if _, ok := raw["changelogMethod"]; ok {
		return true
	}
	if _, ok := raw["rcAutoContinue"]; ok {
		return true
	}
	if vf, ok := raw["versionFile"]; ok {
		if _, isString := vf.(string); isString {
			return true
		}
	}
	if cl, ok := raw["changelog"]; ok {
		if _, isBool := cl.(bool); isBool {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Local migration registry
// ---------------------------------------------------------------------------

var localMigrations = []migrationStep{
	{from: 1, to: 2, run: shipPresetSkipToSteps},
	{from: 2, to: 3, run: renameVersionToSchemaVersion},
	{from: 3, to: 4, run: migrateAwaitReviewBooleansToSteps},
	{from: 4, to: 5, run: removeLocalSchemaVersion},
}

// shipPresetSkipToSteps (v1→v2): converts ship.preset + ship.skip into
// ship.steps[]. Ported from JS shipPresetSkipToSteps.
func shipPresetSkipToSteps(ctx *migrationContext) error {
	var data map[string]any
	err := fsx.ReadJSON(ctx.localPath, &data)
	if err != nil {
		if errors.Is(err, fsx.ErrNotFound) {
			return nil
		}
		return err
	}

	shipRaw, ok := data["ship"]
	if !ok {
		data["version"] = float64(2)
		return fsx.AtomicWriteJSON(ctx.localPath, data)
	}

	ship, ok := shipRaw.(map[string]any)
	if !ok {
		data["version"] = float64(2)
		return fsx.AtomicWriteJSON(ctx.localPath, data)
	}

	_, hasPreset := ship["preset"]
	_, hasSkip := ship["skip"]

	if !hasPreset && !hasSkip {
		data["version"] = float64(2)
		return fsx.AtomicWriteJSON(ctx.localPath, data)
	}

	applyShipPresetToSteps(ship)
	data["version"] = float64(2)
	return fsx.AtomicWriteJSON(ctx.localPath, data)
}

// applyShipPresetToSteps converts preset/skip to steps[] in-place on a
// ship section map. Used by both step-based migration and legacy ingestion.
func applyShipPresetToSteps(ship map[string]any) {
	presetKey := ""
	if p, ok := ship["preset"].(string); ok {
		presetKey = p
	}

	var steps []string
	if mapped, ok := presetToSteps[presetKey]; ok {
		steps = make([]string, len(mapped))
		copy(steps, mapped)
	} else {
		steps = make([]string, len(allSteps))
		copy(steps, allSteps)
	}

	if skipArr, ok := ship["skip"].([]any); ok {
		skipSet := make(map[string]bool, len(skipArr))
		for _, s := range skipArr {
			if str, ok := s.(string); ok {
				skipSet[str] = true
			}
		}
		filtered := make([]string, 0, len(steps))
		for _, s := range steps {
			if !skipSet[s] {
				filtered = append(filtered, s)
			}
		}
		steps = filtered
	}

	delete(ship, "preset")
	delete(ship, "skip")

	stepsAny := make([]any, len(steps))
	for i, s := range steps {
		stepsAny[i] = s
	}
	ship["steps"] = stepsAny
}

// renameVersionToSchemaVersion (v2→v3): renames the top-level integer
// "version" key to "schemaVersion" in local.json.
// Ported from JS renameVersionToSchemaVersion.
func renameVersionToSchemaVersion(ctx *migrationContext) error {
	var data map[string]any
	err := fsx.ReadJSON(ctx.localPath, &data)
	if err != nil {
		if errors.Is(err, fsx.ErrNotFound) {
			return nil
		}
		return err
	}

	// If schemaVersion already present, just drop legacy version.
	if _, ok := data["schemaVersion"]; ok {
		if _, hasV := data["version"]; hasV {
			if _, isNum := data["version"].(float64); isNum {
				delete(data, "version")
				return fsx.AtomicWriteJSON(ctx.localPath, data)
			}
		}
		return nil
	}

	// Rename version → schemaVersion (only if it is a number).
	if v, ok := data["version"]; ok {
		if _, isNum := v.(float64); isNum {
			data["schemaVersion"] = v
			delete(data, "version")
			return fsx.AtomicWriteJSON(ctx.localPath, data)
		}
	}

	return nil
}

// migrateAwaitReviewBooleansToSteps (v3→v4): rewrites legacy boolean ship
// flags into entries in ship.steps[] and renames awaitReview* tunable keys.
// Ported from JS migrateAwaitReviewBooleansToSteps.
func migrateAwaitReviewBooleansToSteps(ctx *migrationContext) error {
	var data map[string]any
	err := fsx.ReadJSON(ctx.localPath, &data)
	if err != nil {
		if errors.Is(err, fsx.ErrNotFound) {
			return nil
		}
		return err
	}

	shipRaw, ok := data["ship"]
	if !ok {
		data["schemaVersion"] = float64(4)
		return fsx.AtomicWriteJSON(ctx.localPath, data)
	}

	ship, ok := shipRaw.(map[string]any)
	if !ok {
		data["schemaVersion"] = float64(4)
		return fsx.AtomicWriteJSON(ctx.localPath, data)
	}

	applyShipAwaitReview(ship)
	data["schemaVersion"] = float64(4)
	return fsx.AtomicWriteJSON(ctx.localPath, data)
}

// applyShipAwaitReview applies the v3→v4 ship migration in-place.
// Used by both step-based migration and legacy ingestion.
func applyShipAwaitReview(ship map[string]any) {
	var existingSteps []string
	if stepsRaw, ok := ship["steps"].([]any); ok {
		for _, s := range stepsRaw {
			if str, ok := s.(string); ok {
				existingSteps = append(existingSteps, str)
			}
		}
	}

	if vp, ok := ship["verifyPipeline"].(bool); ok && vp {
		if !containsStr(existingSteps, "verify-pipeline") {
			existingSteps = append(existingSteps, "verify-pipeline")
		}
	}
	delete(ship, "verifyPipeline")

	if ar, ok := ship["awaitReview"].(bool); ok && ar {
		if !containsStr(existingSteps, "await-remote-review") {
			existingSteps = append(existingSteps, "await-remote-review")
		}
	}
	delete(ship, "awaitReview")

	if len(existingSteps) > 0 || ship["steps"] != nil {
		stepsAny := make([]any, len(existingSteps))
		for i, s := range existingSteps {
			stepsAny[i] = s
		}
		ship["steps"] = stepsAny
	}

	// Rename awaitReview* → awaitRemoteReview*.
	if v, ok := ship["awaitReviewTimeout"]; ok {
		ship["awaitRemoteReviewTimeout"] = v
		delete(ship, "awaitReviewTimeout")
	}
	if v, ok := ship["awaitReviewInterval"]; ok {
		ship["awaitRemoteReviewInterval"] = v
		delete(ship, "awaitReviewInterval")
	}
	if v, ok := ship["awaitReviewers"]; ok {
		ship["awaitRemoteReviewers"] = v
		delete(ship, "awaitReviewers")
	}
}

// removeLocalSchemaVersion (v4→v5): removes schemaVersion from local.json.
func removeLocalSchemaVersion(ctx *migrationContext) error {
	var data map[string]any
	err := fsx.ReadJSON(ctx.localPath, &data)
	if err != nil {
		if errors.Is(err, fsx.ErrNotFound) {
			return nil
		}
		return err
	}
	delete(data, "schemaVersion")
	return fsx.AtomicWriteJSON(ctx.localPath, data)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func containsStr(ss []string, target string) bool {
	for _, s := range ss {
		if s == target {
			return true
		}
	}
	return false
}

// extractReviewDefaults extracts the review section from legacy review data.
// If the data has a "defaults" key (object), returns that; otherwise strips
// $schema and returns the rest.
func extractReviewDefaults(data map[string]any) map[string]any {
	if defaults, ok := data["defaults"]; ok {
		if m, ok := defaults.(map[string]any); ok {
			return m
		}
	}
	result := make(map[string]any, len(data))
	for k, v := range data {
		if k != "$schema" {
			result[k] = v
		}
	}
	return result
}
