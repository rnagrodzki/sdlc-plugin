// Package version detects, reads, bumps, and writes version strings in
// multiple project file formats (package.json, plugin.json, cargo.toml,
// pyproject.toml, pubspec.yaml, VERSION).
package version

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// VersionFile represents a detected version file.
type VersionFile struct {
	Path    string // absolute path
	Type    string // "package.json", "plugin.json", "cargo.toml", "pyproject.toml", "pubspec.yaml", "version-file"
	Version string // current version string (semver)
}

// ApplyReport summarizes what Apply did.
type ApplyReport struct {
	PreviousVersion string
	NewVersion      string
	VersionFile     string // path that was updated
	ChangelogFile   string // path that was updated (empty if no changelog)
	Changed         bool   // false if idempotent no-op
}

// probeOrder is the sequence of files Detect checks.
var probeOrder = []struct {
	name string
	typ  string
}{
	{"package.json", "package.json"},
	{"plugin.json", "plugin.json"},
	{"Cargo.toml", "cargo.toml"},
	{"pyproject.toml", "pyproject.toml"},
	{"pubspec.yaml", "pubspec.yaml"},
	{"VERSION", "version-file"},
}

// Detect finds the version file in the project root.
// Checks in order: package.json, plugin.json, Cargo.toml, pyproject.toml,
// pubspec.yaml, VERSION.
func Detect(root string) (*VersionFile, error) {
	for _, p := range probeOrder {
		abs := filepath.Join(root, p.name)
		if _, err := os.Stat(abs); err != nil {
			continue
		}
		ver, err := readVersion(abs, p.typ)
		if err != nil {
			return nil, fmt.Errorf("version: read %s: %w", p.name, err)
		}
		return &VersionFile{
			Path:    abs,
			Type:    p.typ,
			Version: ver,
		}, nil
	}
	return nil, fmt.Errorf("version: no version file found in %s", root)
}

// ---------- semver parsing & bumping ----------

// semver holds the parsed parts of a semver string.
type semver struct {
	Major      int
	Minor      int
	Patch      int
	Prerelease string // everything after the first '-', if any
}

// semverRe matches a basic semver string: major.minor.patch(-prerelease)?
var semverRe = regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+)(?:-(.+))?$`)

func parseSemver(s string) (semver, error) {
	m := semverRe.FindStringSubmatch(s)
	if m == nil {
		return semver{}, fmt.Errorf("version: %q is not a valid semver string", s)
	}
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	patch, _ := strconv.Atoi(m[3])
	return semver{Major: major, Minor: minor, Patch: patch, Prerelease: m[4]}, nil
}

func (v semver) String() string {
	base := fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
	if v.Prerelease != "" {
		return base + "-" + v.Prerelease
	}
	return base
}

// Bump calculates the next version from current, given a bump level.
// Levels: "major", "minor", "patch", "premajor", "preminor", "prepatch", "prerelease".
// level may also be an explicit target semver string (e.g. "1.2.3"), mirroring
// `npm version <version|major|minor|...>` — in that case the target is
// returned as-is regardless of the current version.
func Bump(vf *VersionFile, level string) (string, error) {
	sv, err := parseSemver(vf.Version)
	if err != nil {
		return "", err
	}

	// Explicit target version: if level itself parses as semver, use it
	// directly instead of computing a bump.
	if _, err := parseSemver(level); err == nil {
		return level, nil
	}

	switch level {
	case "major":
		sv.Major++
		sv.Minor = 0
		sv.Patch = 0
		sv.Prerelease = ""
	case "minor":
		sv.Minor++
		sv.Patch = 0
		sv.Prerelease = ""
	case "patch":
		sv.Patch++
		sv.Prerelease = ""
	case "premajor":
		sv.Major++
		sv.Minor = 0
		sv.Patch = 0
		sv.Prerelease = "0"
	case "preminor":
		sv.Minor++
		sv.Patch = 0
		sv.Prerelease = "0"
	case "prepatch":
		sv.Patch++
		sv.Prerelease = "0"
	case "prerelease":
		if sv.Prerelease != "" {
			sv.Prerelease = incrementPrerelease(sv.Prerelease)
		} else {
			sv.Patch++
			sv.Prerelease = "0"
		}
	default:
		return "", fmt.Errorf("version: unknown bump level %q", level)
	}
	return sv.String(), nil
}

// incrementPrerelease increments the numeric suffix of a prerelease tag.
// "0" → "1", "alpha.3" → "alpha.4", "beta" → "beta.0" (appends .0 if
// the last segment is not numeric).
func incrementPrerelease(pre string) string {
	parts := strings.Split(pre, ".")
	last := parts[len(parts)-1]
	n, err := strconv.Atoi(last)
	if err == nil {
		parts[len(parts)-1] = strconv.Itoa(n + 1)
		return strings.Join(parts, ".")
	}
	// Last segment is not numeric — append ".0".
	return pre + ".0"
}

// Apply performs a bump AND writes a changelog entry in one call.
// level accepts a bump keyword or an explicit target semver string (see
// Bump). Idempotent per version: a second call at the same target version
// changes no files — e.g. Apply(root, "1.2.3", notes) called twice: the
// first call bumps to 1.2.3, the second is a no-op since current already
// equals target.
func Apply(root, level, notes string) (*ApplyReport, error) {
	vf, err := Detect(root)
	if err != nil {
		return nil, err
	}

	newVer, err := Bump(vf, level)
	if err != nil {
		return nil, err
	}

	report := &ApplyReport{
		PreviousVersion: vf.Version,
		NewVersion:      newVer,
		VersionFile:     vf.Path,
	}

	// Idempotency: if the version already matches, do nothing.
	if vf.Version == newVer {
		report.Changed = false
		return report, nil
	}

	// Write the bumped version into the file.
	if err := writeVersion(vf.Path, vf.Type, newVer); err != nil {
		return nil, fmt.Errorf("version: write %s: %w", vf.Path, err)
	}

	// Write changelog entry.
	clPath := filepath.Join(root, "CHANGELOG.md")
	if notes != "" {
		if err := prependChangelog(clPath, newVer, notes); err != nil {
			return nil, fmt.Errorf("version: changelog: %w", err)
		}
		report.ChangelogFile = clPath
	}

	report.Changed = true
	return report, nil
}

// ---------- file format readers ----------

// readVersion extracts the version string from a file of the given type.
func readVersion(path, typ string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	switch typ {
	case "package.json", "plugin.json":
		return readJSONVersion(data)
	case "cargo.toml":
		return readTOMLVersion(data, false)
	case "pyproject.toml":
		return readTOMLVersion(data, true)
	case "pubspec.yaml":
		return readYAMLVersion(data)
	case "version-file":
		return strings.TrimSpace(string(data)), nil
	}
	return "", fmt.Errorf("unknown version file type %q", typ)
}

func readJSONVersion(data []byte) (string, error) {
	var obj map[string]any
	if err := json.Unmarshal(data, &obj); err != nil {
		return "", err
	}
	v, ok := obj["version"]
	if !ok {
		return "", fmt.Errorf("no 'version' field in JSON")
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("'version' field is not a string")
	}
	return s, nil
}

// readTOMLVersion does a line-based extraction of version = "..." from a
// TOML file. For pyproject.toml (pyproject=true) it looks in [tool.poetry]
// or [project] sections; for Cargo.toml it looks for the first top-level
// version line.
func readTOMLVersion(data []byte, pyproject bool) (string, error) {
	lines := strings.Split(string(data), "\n")
	versionRe := regexp.MustCompile(`^\s*version\s*=\s*"([^"]+)"`)

	if !pyproject {
		// Cargo.toml: first version = "..." before any section header.
		for _, line := range lines {
			if strings.HasPrefix(strings.TrimSpace(line), "[") && !strings.HasPrefix(strings.TrimSpace(line), "[package]") {
				break
			}
			if m := versionRe.FindStringSubmatch(line); m != nil {
				return m[1], nil
			}
		}
		// Also check inside [package] section.
		inPackage := false
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "[") {
				inPackage = trimmed == "[package]"
				continue
			}
			if inPackage {
				if m := versionRe.FindStringSubmatch(line); m != nil {
					return m[1], nil
				}
			}
		}
		return "", fmt.Errorf("no version found in Cargo.toml")
	}

	// pyproject.toml: look in [tool.poetry] or [project].
	targetSections := []string{"[tool.poetry]", "[project]"}
	inTarget := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") {
			inTarget = false
			for _, sec := range targetSections {
				if trimmed == sec {
					inTarget = true
					break
				}
			}
			continue
		}
		if inTarget {
			if m := versionRe.FindStringSubmatch(line); m != nil {
				return m[1], nil
			}
		}
	}
	return "", fmt.Errorf("no version found in pyproject.toml")
}

// readYAMLVersion extracts a top-level `version:` field from YAML content
// using simple line matching (avoids pulling in a YAML library for a single
// scalar).
func readYAMLVersion(data []byte) (string, error) {
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "version:") {
			val := strings.TrimSpace(strings.TrimPrefix(trimmed, "version:"))
			val = strings.Trim(val, `"'`)
			if val != "" {
				return val, nil
			}
		}
	}
	return "", fmt.Errorf("no version found in YAML")
}

// ---------- file format writers ----------

// writeVersion writes a new version string into the file of the given type.
func writeVersion(path, typ, newVer string) error {
	switch typ {
	case "package.json", "plugin.json":
		return writeJSONVersion(path, newVer)
	case "cargo.toml":
		return writeTOMLVersion(path, newVer, false)
	case "pyproject.toml":
		return writeTOMLVersion(path, newVer, true)
	case "pubspec.yaml":
		return writeYAMLVersion(path, newVer)
	case "version-file":
		return os.WriteFile(path, []byte(newVer+"\n"), 0o644)
	}
	return fmt.Errorf("unknown version file type %q", typ)
}

func writeJSONVersion(path, newVer string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var obj map[string]any
	if err := json.Unmarshal(data, &obj); err != nil {
		return err
	}
	obj["version"] = newVer
	out, err := json.MarshalIndent(obj, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(out, '\n'), 0o644)
}

func writeTOMLVersion(path, newVer string, pyproject bool) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	lines := strings.Split(string(data), "\n")
	versionRe := regexp.MustCompile(`^(\s*version\s*=\s*)"[^"]+"`)

	if !pyproject {
		// Cargo.toml: replace first version in top-level or [package].
		inPackage := false
		topLevel := true
		for i, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "[") {
				if trimmed == "[package]" {
					inPackage = true
				} else {
					inPackage = false
				}
				topLevel = false
				continue
			}
			if topLevel || inPackage {
				if m := versionRe.FindStringSubmatch(line); m != nil {
					lines[i] = m[1] + `"` + newVer + `"`
					return os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644)
				}
			}
		}
		return fmt.Errorf("no version line found in Cargo.toml")
	}

	// pyproject.toml: replace version in [tool.poetry] or [project].
	targetSections := []string{"[tool.poetry]", "[project]"}
	inTarget := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") {
			inTarget = false
			for _, sec := range targetSections {
				if trimmed == sec {
					inTarget = true
					break
				}
			}
			continue
		}
		if inTarget {
			if m := versionRe.FindStringSubmatch(line); m != nil {
				lines[i] = m[1] + `"` + newVer + `"`
				return os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644)
			}
		}
	}
	return fmt.Errorf("no version line found in pyproject.toml")
}

func writeYAMLVersion(path, newVer string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	lines := strings.Split(string(data), "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "version:") {
			// Preserve indentation.
			indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
			lines[i] = indent + "version: " + newVer
			return os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644)
		}
	}
	return fmt.Errorf("no version line found in YAML")
}
