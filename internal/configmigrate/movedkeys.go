package configmigrate

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/rnagrodzki/sdlc-plugin/internal/fsx"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
)

// moveTarget is the local.toml section and key a moved key lands in.
type moveTarget struct {
	section, key string
}

// movedLocalKeys maps project-section keys that moved to local.toml to their
// new local section and key.
var movedLocalKeys = map[string]map[string]moveTarget{
	"pr": {"expectedAccount": {section: "github", key: "expectedAccount"}},
	"execute": {
		"auto":                {section: "executePrefs", key: "auto"},
		"quality":             {section: "executePrefs", key: "quality"},
		"highRiskAutoApprove": {section: "executePrefs", key: "highRiskAutoApprove"},
	},
}

const (
	movedKeysConfigRel = paths.DataDir + "/" + paths.ConfigFile
	movedKeysLocalRel  = paths.DataDir + "/" + paths.LocalConfigFile

	movedKeysSuggestion = "Move each listed key into " + movedKeysLocalRel +
		" under the new section by hand, delete it from " + movedKeysConfigRel +
		", then run the command again."
)

// MovedKeysErr reports stale keys that could not be moved automatically.
type MovedKeysErr struct {
	Reason string   // why the automatic move was not safe
	Lines  []string // sorted, e.g. "pr.expectedAccount -> [github] expectedAccount"
}

// Error renders the header with Reason, then one indented line per key.
func (e *MovedKeysErr) Error() string {
	var b strings.Builder
	b.WriteString("Cannot move personal settings from " + movedKeysConfigRel +
		" to " + movedKeysLocalRel + " automatically: " + e.Reason)
	for _, l := range e.Lines {
		b.WriteString("\n  " + l)
	}
	return b.String()
}

// Suggestion tells the user how to fix the config by hand.
func (e *MovedKeysErr) Suggestion() string { return movedKeysSuggestion }

// MovedKeysWarning renders the user-facing warning for a successful move.
// Shared by pr_prepare and resolve-config.
func MovedKeysWarning(moved []string) string {
	var b strings.Builder
	b.WriteString("Moved personal settings from " + movedKeysConfigRel + " to " + movedKeysLocalRel + ":\n")
	for _, l := range moved {
		b.WriteString("  " + l + "\n")
	}
	b.WriteString(movedKeysConfigRel + " is now modified. Commit that change so teammates stop inheriting these settings.")
	return b.String()
}

// movedKey is one stale key found in config.toml.
type movedKey struct {
	oldSect, oldKey string
	newSect, newKey string
	value           any  // raw value from toml.Unmarshal (not fsx-normalized)
	inLocal         bool // local.toml already holds the same value
}

func (m movedKey) line() string {
	return fmt.Sprintf("%s.%s -> [%s] %s", m.oldSect, m.oldKey, m.newSect, m.newKey)
}

// headerRe matches a single-bracket table header line, with optional spaces
// inside the brackets and an optional trailing comment. Array-of-tables
// headers ([[x]]) do not match because the name may not contain brackets.
var headerRe = regexp.MustCompile(`^\s*\[([^\[\]]*)\]\s*(#.*)?$`)

// tableHeader reports whether line is any table header ([x] or [[x]]) and,
// for a single-bracket header, returns its trimmed name. A commented-out
// header ("# [github]") is not a header.
func tableHeader(line string) (name string, isHeader bool) {
	trimmed := strings.TrimRight(line, "\r\n")
	if !strings.HasPrefix(strings.TrimSpace(trimmed), "[") {
		return "", false
	}
	if m := headerRe.FindStringSubmatch(trimmed); m != nil {
		return strings.TrimSpace(m[1]), true
	}
	return "", true // [[x]] or something unusual: a header, but never ours
}

// MigrateMovedKeys moves stale personal keys from config.toml to local.toml.
// moved lists the moved keys in the same "old -> [new] key" form, sorted.
//
// The edit is textual so comments and layout survive; every edit is checked
// by re-parsing and comparing against the expected data, and nothing is
// written unless both checks pass. local.toml is written before config.toml,
// so a failure between the two writes never loses a value. No backup is made.
//
// A non-nil err is always a *MovedKeysErr; use errors.As to reach its
// Suggestion.
func MigrateMovedKeys(mainRoot string) (moved []string, err error) {
	cfgPath := filepath.Join(mainRoot, paths.DataDir, paths.ConfigFile)
	localPath := filepath.Join(mainRoot, paths.DataDir, paths.LocalConfigFile)

	// Step 1: missing or unparsable config.toml is left to normal readers.
	cfgText, rerr := os.ReadFile(cfgPath)
	if rerr != nil {
		return nil, nil
	}
	var cfgA map[string]any
	if toml.Unmarshal(cfgText, &cfgA) != nil {
		return nil, nil
	}

	// Step 2: find stale keys.
	var keys []movedKey
	for sect, inner := range movedLocalKeys {
		tbl, ok := cfgA[sect].(map[string]any)
		if !ok {
			continue
		}
		for key, dst := range inner {
			v, ok := tbl[key]
			if !ok {
				continue
			}
			keys = append(keys, movedKey{oldSect: sect, oldKey: key, newSect: dst.section, newKey: dst.key, value: v})
		}
	}
	if len(keys) == 0 {
		return nil, nil
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].line() < keys[j].line() })
	lines := make([]string, len(keys))
	for i, k := range keys {
		lines[i] = k.line()
	}
	fail := func(reason string) *MovedKeysErr {
		return &MovedKeysErr{Reason: reason, Lines: lines}
	}

	// Step 3: local.toml must parse if it exists.
	localText, rerr := os.ReadFile(localPath)
	if rerr != nil && !errors.Is(rerr, os.ErrNotExist) {
		return nil, fail(fmt.Sprintf("cannot read %s: %v", movedKeysLocalRel, rerr))
	}
	localOld := map[string]any{}
	if rerr == nil {
		if toml.Unmarshal(localText, &localOld) != nil {
			return nil, fail(movedKeysLocalRel + " is not valid TOML")
		}
	}

	// Step 4: a different value already in local.toml is a conflict.
	for i, k := range keys {
		tbl, ok := localOld[k.newSect].(map[string]any)
		if !ok {
			continue
		}
		existing, ok := tbl[k.newKey]
		if !ok {
			continue
		}
		if !reflect.DeepEqual(existing, k.value) {
			return nil, fail(fmt.Sprintf("%s already has a different value for [%s] %s", movedKeysLocalRel, k.newSect, k.newKey))
		}
		keys[i].inLocal = true
	}

	// Step 5: drop the moved key lines from config.toml.
	newCfg := dropMovedLines(string(cfgText))

	// Step 6: the edited config must equal the original minus the moved keys.
	var cfgB, cfgWant map[string]any
	if toml.Unmarshal([]byte(newCfg), &cfgB) != nil {
		return nil, fail(movedKeysConfigRel + " layout not supported for automatic edit")
	}
	_ = toml.Unmarshal(cfgText, &cfgWant) // fresh copy of A; parsed fine above
	for _, k := range keys {
		delete(cfgWant[k.oldSect].(map[string]any), k.oldKey)
	}
	if !reflect.DeepEqual(cfgB, cfgWant) {
		return nil, fail(movedKeysConfigRel + " layout not supported for automatic edit")
	}

	// Step 7: add missing keys to local.toml.
	newLocal := string(localText)
	bySect := map[string][]movedKey{}
	var sects []string
	for _, k := range keys {
		if k.inLocal {
			continue
		}
		if _, seen := bySect[k.newSect]; !seen {
			sects = append(sects, k.newSect)
		}
		bySect[k.newSect] = append(bySect[k.newSect], k)
	}
	sort.Strings(sects)
	for _, sect := range sects {
		var body strings.Builder
		for _, k := range bySect[sect] {
			// Marshal the raw toml.Unmarshal value, never an fsx.ReadTOML
			// normalized one: normalization turns int64 into float64, which
			// would write "1.0" for an integer setting.
			b, merr := toml.Marshal(map[string]any{k.newKey: k.value})
			if merr != nil {
				return nil, fail(fmt.Sprintf("cannot encode %s: %v", k.line(), merr))
			}
			body.Write(b)
		}
		newLocal = insertUnderSection(newLocal, sect, body.String())
	}

	// Step 8: the edited local must equal the old local plus the new keys.
	var localB map[string]any
	if toml.Unmarshal([]byte(newLocal), &localB) != nil {
		return nil, fail(movedKeysLocalRel + " layout not supported for automatic edit")
	}
	localWant := map[string]any{}
	if len(localText) > 0 {
		_ = toml.Unmarshal(localText, &localWant) // parsed fine in step 3
	}
	for _, k := range keys {
		if k.inLocal {
			continue
		}
		tbl, ok := localWant[k.newSect].(map[string]any)
		if !ok {
			if _, exists := localWant[k.newSect]; exists {
				return nil, fail(movedKeysLocalRel + " layout not supported for automatic edit")
			}
			tbl = map[string]any{}
			localWant[k.newSect] = tbl
		}
		tbl[k.newKey] = k.value
	}
	if !reflect.DeepEqual(localB, localWant) {
		return nil, fail(movedKeysLocalRel + " layout not supported for automatic edit")
	}

	// Step 9: write local.toml first, then config.toml.
	if newLocal != string(localText) {
		if werr := fsx.AtomicWriteBytes(localPath, []byte(newLocal)); werr != nil {
			return nil, fail(werr.Error())
		}
	}
	if werr := fsx.AtomicWriteBytes(cfgPath, []byte(newCfg)); werr != nil {
		return nil, fail(werr.Error())
	}

	return lines, nil
}

// dropMovedLines removes uncommented "<key> =" lines for the moved keys when
// they sit directly under their exact [pr] / [execute] header.
func dropMovedLines(text string) string {
	var out strings.Builder
	current := "" // root table
	for _, line := range strings.SplitAfter(text, "\n") {
		if name, isHeader := tableHeader(line); isHeader {
			current = name
			out.WriteString(line)
			continue
		}
		if inner, ok := movedLocalKeys[current]; ok && isKeyLine(line, inner) {
			continue
		}
		out.WriteString(line)
	}
	return out.String()
}

// isKeyLine reports whether line assigns one of the keys in inner.
func isKeyLine(line string, inner map[string]moveTarget) bool {
	trimmed := strings.TrimLeft(line, " \t")
	for key := range inner {
		rest, ok := strings.CutPrefix(trimmed, key)
		if !ok {
			continue
		}
		if strings.HasPrefix(strings.TrimLeft(rest, " \t"), "=") {
			return true
		}
	}
	return false
}

// insertUnderSection inserts body right after the first uncommented [sect]
// header in text, or appends a new "[sect]" block at the end when there is
// no such header.
func insertUnderSection(text, sect, body string) string {
	lines := strings.SplitAfter(text, "\n")
	for i, line := range lines {
		if name, isHeader := tableHeader(line); isHeader && name == sect {
			if !strings.HasSuffix(line, "\n") {
				lines[i] = line + "\n"
			}
			return strings.Join(lines[:i+1], "") + body + strings.Join(lines[i+1:], "")
		}
	}
	if text == "" {
		return "[" + sect + "]\n" + body
	}
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	return text + "\n[" + sect + "]\n" + body
}
