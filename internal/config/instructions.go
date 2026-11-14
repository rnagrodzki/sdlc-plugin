// instructions.go reads the [instructions] section: follow-up instructions
// that ship, and the standalone skills, run after a step.
//
// Unlike every other section, [instructions] is read from all three config
// files and merged across them: the team file (.sdlc-v2/config.toml), the
// project local file (.sdlc-v2/local.toml) and the user-level file (see
// UserConfigPath). For each key, the team list comes first. The project
// local list follows when that file sets the key, else the user list. An
// empty project local list therefore hides the user list for that key.
//
// The loader reads raw maps, not the section extractor (extractSection drops
// list values), and it checks every file in full, a hidden user list too.

package config

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/rnagrodzki/sdlc-plugin/internal/fsx"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
)

// Instruction keys that callers name directly. Every key is in InstructionKeys.
const (
	// InstructionKeyReview is the list ship runs after its review step.
	InstructionKeyReview = "review"
	// InstructionKeyReceivedReview is the list ship runs after it sequences
	// received-review, which is not a pipeline step.
	InstructionKeyReceivedReview = "received-review"
	// InstructionKeyCommitFixes is the list ship runs after it commits the
	// received-review fixes, which is not a pipeline step.
	InstructionKeyCommitFixes = "commit-fixes"
	// InstructionKeyShip is the end list of a ship run.
	InstructionKeyShip = "ship"
)

// InstructionKeys is the canonical key list, in ship order, plus "ship".
// The step names equal shipmeta.CanonicalSteps plus received-review and
// commit-fixes, which ship sequences after review but which are not pipeline
// steps (TestInstructionKeysCoverCanonicalSteps). Both schema enums of
// $defs.instructionsSection equal this list (TestInstructionKeysMatchSchema).
var InstructionKeys = []string{"execute", "commit", InstructionKeyReview, InstructionKeyReceivedReview,
	InstructionKeyCommitFixes, "verify-openspec", "archive-openspec", "harden", "pr",
	"verify-pipeline", "await-remote-review", "learnings-commit", InstructionKeyShip}

// StandaloneInstructionKeys are the keys a skill may run outside a ship run.
var StandaloneInstructionKeys = []string{"execute", "commit", InstructionKeyReview,
	InstructionKeyReceivedReview, "pr", "harden", "verify-pipeline"}

const (
	// MaxInstructionsPerKey is the most instructions one key may hold in one
	// file, counted after each text array is expanded.
	MaxInstructionsPerKey = 50
	// MaxInstructionText is the most characters one text string may hold.
	MaxInstructionText = 4000
	// MaxInstructionsConfigBytes is the most bytes this loader reads from one
	// config file.
	MaxInstructionsConfigBytes = 1 << 20
)

// Instruction sources, in merge order.
const (
	InstructionSourceTeam         = "team"
	InstructionSourceProjectLocal = "project local"
	InstructionSourceUser         = "user"
)

// Instruction question-not-allowed behaviors.
const (
	OnQuestionNotAllowedSkip   = "skip"
	OnQuestionNotAllowedDecide = "decide"
)

// Error kinds of InstructionsError.
const (
	InstructionsErrorData  = "data"  // bad content
	InstructionsErrorInfra = "infra" // file cannot be read
)

// instructionsWholeFileField is the Field of an error about a whole file
// (unreadable, over the size cap, bad TOML).
const instructionsWholeFileField = "(whole file)"

// instructionAllowedFields lists the fields of one instruction entry, in the
// order the unknown-field fix names them.
var instructionAllowedFields = []string{"text", "normalQuestions", "criticalQuestions", "onQuestionNotAllowed"}

// Instruction is one follow-up instruction after the merge.
type Instruction struct {
	Key                  string `json:"key"`
	Number               int    `json:"number"` // 1-based place in the merged list of Key
	Source               string `json:"source"` // one of the InstructionSource* constants
	Text                 string `json:"text"`
	NormalQuestions      bool   `json:"normalQuestions"`
	CriticalQuestions    bool   `json:"criticalQuestions"`
	OnQuestionNotAllowed string `json:"onQuestionNotAllowed"` // one of the OnQuestionNotAllowed* constants
}

// InstructionsError is a config error of the [instructions] section. Every
// error names the file, the field, the problem and the fix.
type InstructionsError struct {
	Kind    string // one of the InstructionsError* kind constants
	File    string // ".sdlc-v2/config.toml" | ".sdlc-v2/local.toml" | user-level path
	Field   string // e.g. "instructions.pr[2].onQuestionNotAllowed"; "(whole file)" for a file error
	Problem string
	Fix     string
	Cause   error // the read error of an infra-kind error; nil for a data-kind error
}

// Error returns "File: …\nField: …\nProblem: …\nFix: …".
func (e *InstructionsError) Error() string {
	return fmt.Sprintf("File: %s\nField: %s\nProblem: %s\nFix: %s", e.File, e.Field, e.Problem, e.Fix)
}

// Unwrap returns Cause, so errors.Is sees the read error (for example
// fs.ErrPermission) through an infra-kind error.
func (e *InstructionsError) Unwrap() error { return e.Cause }

// instructionsFile is one config file read by ReadInstructions.
type instructionsFile struct {
	path   string // absolute path
	label  string // the File value of its errors
	source string // the Source value of its instructions
}

// parsedInstructions holds the lists of one file, before numbering.
type parsedInstructions struct {
	lists   map[string][]Instruction
	present map[string]bool // keys the file sets, an empty list included
}

// ReadInstructions reads the three files under root and returns the merged
// lists by key. A key with no instruction is absent from the map.
//
// root is the main worktree root. A missing file is an empty list, never an
// error. The first error in file order wins: team, then project local, then
// user. Within one file, keys are checked in sorted order, and the entries
// of a key in file order. The n in a field such as "instructions.pr[n]"
// counts the entries of that key in that file from 1.
func ReadInstructions(root string) (map[string][]Instruction, error) {
	files := []instructionsFile{
		{
			path:   filepath.Join(root, paths.DataDir, paths.ConfigFile),
			label:  filepath.ToSlash(filepath.Join(paths.DataDir, paths.ConfigFile)),
			source: InstructionSourceTeam,
		},
		{
			path:   filepath.Join(root, paths.DataDir, paths.LocalConfigFile),
			label:  filepath.ToSlash(filepath.Join(paths.DataDir, paths.LocalConfigFile)),
			source: InstructionSourceProjectLocal,
		},
	}
	if userPath, ok := UserConfigPath(); ok {
		files = append(files, instructionsFile{path: userPath, label: userPath, source: InstructionSourceUser})
	}

	parsed := make([]parsedInstructions, len(files))
	for i, f := range files {
		p, err := readInstructionsFile(f)
		if err != nil {
			return nil, err
		}
		parsed[i] = p
	}

	team := parsed[0]
	projectLocal := parsed[1]
	var user parsedInstructions
	if len(parsed) > 2 {
		user = parsed[2]
	}

	out := make(map[string][]Instruction)
	for _, key := range InstructionKeys {
		merged := append([]Instruction{}, team.lists[key]...)
		if projectLocal.present[key] {
			merged = append(merged, projectLocal.lists[key]...)
		} else {
			merged = append(merged, user.lists[key]...)
		}
		if len(merged) == 0 {
			continue
		}
		for i := range merged {
			merged[i].Number = i + 1
		}
		out[key] = merged
	}
	return out, nil
}

// readInstructionsFile reads one config file and checks its [instructions]
// section. A missing file returns empty lists and no error.
func readInstructionsFile(f instructionsFile) (parsedInstructions, error) {
	empty := parsedInstructions{lists: map[string][]Instruction{}, present: map[string]bool{}}

	data, found, err := readInstructionsBytes(f)
	if err != nil || !found {
		return empty, err
	}

	var raw map[string]any
	if err := fsx.DecodeTOML(data, &raw); err != nil {
		return empty, &InstructionsError{
			Kind:    InstructionsErrorData,
			File:    f.label,
			Field:   instructionsWholeFileField,
			Problem: strings.TrimPrefix(err.Error(), fsx.ErrParse.Error()+": "),
			Fix:     "fix the file.",
		}
	}

	section, exists := raw["instructions"]
	if !exists {
		return empty, nil
	}
	table, ok := section.(map[string]any)
	if !ok {
		return empty, dataErr(f, "instructions",
			"the value is not a table.",
			"use [[instructions.<key>]] entries.")
	}

	keys := make([]string, 0, len(table))
	for k := range table {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	out := parsedInstructions{lists: map[string][]Instruction{}, present: map[string]bool{}}
	for _, key := range keys {
		list, err := parseInstructionList(f, key, table[key])
		if err != nil {
			return empty, err
		}
		out.present[key] = true
		if len(list) > 0 {
			out.lists[key] = list
		}
	}
	return out, nil
}

// readInstructionsBytes reads at most MaxInstructionsConfigBytes+1 bytes of
// the file. found is false, with no error, for a missing file.
func readInstructionsBytes(f instructionsFile) (data []byte, found bool, err error) {
	fh, err := os.Open(f.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, infraErr(f, err)
	}
	defer fh.Close()

	data, err = io.ReadAll(io.LimitReader(fh, MaxInstructionsConfigBytes+1))
	if err != nil {
		return nil, false, infraErr(f, err)
	}
	if len(data) > MaxInstructionsConfigBytes {
		size := int64(len(data))
		if info, statErr := fh.Stat(); statErr == nil {
			size = info.Size()
		}
		return nil, false, &InstructionsError{
			Kind:    InstructionsErrorData,
			File:    f.label,
			Field:   instructionsWholeFileField,
			Problem: fmt.Sprintf("the file has %d bytes. The limit is %d.", size, MaxInstructionsConfigBytes),
			Fix:     "shorten the file.",
		}
	}
	return data, true, nil
}

// parseInstructionList checks the list of one key and expands each text
// array into one instruction per string.
func parseInstructionList(f instructionsFile, key string, value any) ([]Instruction, error) {
	field := "instructions." + key
	if !slices.Contains(InstructionKeys, key) {
		return nil, dataErr(f, field,
			fmt.Sprintf("%q is not a ship step name and not \"ship\".", key),
			"use one of: "+strings.Join(InstructionKeys, ", ")+".")
	}

	entries, ok := asEntryList(value)
	if !ok {
		return nil, dataErr(f, field,
			"the value is not an array of tables.",
			fmt.Sprintf("use [[instructions.%s]] headers.", key))
	}

	var out []Instruction
	for i, entry := range entries {
		expanded, err := parseInstructionEntry(f, key, fmt.Sprintf("%s[%d]", field, i+1), entry)
		if err != nil {
			return nil, err
		}
		out = append(out, expanded...)
	}
	if len(out) > MaxInstructionsPerKey {
		return nil, dataErr(f, field,
			fmt.Sprintf("the list has %d instructions after text expansion. The limit is %d.", len(out), MaxInstructionsPerKey),
			"split or shorten the list.")
	}
	return out, nil
}

// asEntryList returns value as a list of tables. ok is false when value is
// not a list, or when one item is not a table.
func asEntryList(value any) ([]map[string]any, bool) {
	switch v := value.(type) {
	case []map[string]any:
		return v, true
	case []any:
		out := make([]map[string]any, 0, len(v))
		for _, item := range v {
			m, ok := item.(map[string]any)
			if !ok {
				return nil, false
			}
			out = append(out, m)
		}
		return out, true
	default:
		return nil, false
	}
}

// parseInstructionEntry checks one entry and returns one instruction for
// each of its text strings.
func parseInstructionEntry(f instructionsFile, key, field string, entry map[string]any) ([]Instruction, error) {
	names := make([]string, 0, len(entry))
	for name := range entry {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if !slices.Contains(instructionAllowedFields, name) {
			return nil, dataErr(f, field+"."+name,
				fmt.Sprintf("%q is not a known field.", name),
				"remove it. Allowed: "+strings.Join(instructionAllowedFields, ", ")+".")
		}
	}

	texts, err := parseInstructionText(f, field+".text", entry)
	if err != nil {
		return nil, err
	}

	normal, err := parseInstructionBool(f, field, entry, "normalQuestions")
	if err != nil {
		return nil, err
	}
	critical, err := parseInstructionBool(f, field, entry, "criticalQuestions")
	if err != nil {
		return nil, err
	}

	onNotAllowed := OnQuestionNotAllowedSkip
	if raw, ok := entry["onQuestionNotAllowed"]; ok {
		s, isStr := raw.(string)
		if !isStr || (s != OnQuestionNotAllowedSkip && s != OnQuestionNotAllowedDecide) {
			return nil, dataErr(f, field+".onQuestionNotAllowed",
				fmt.Sprintf("the value %s is not %q or %q.", formatTOMLValue(raw), OnQuestionNotAllowedSkip, OnQuestionNotAllowedDecide),
				fmt.Sprintf("use %q or %q.", OnQuestionNotAllowedSkip, OnQuestionNotAllowedDecide))
		}
		onNotAllowed = s
	}

	out := make([]Instruction, 0, len(texts))
	for _, text := range texts {
		out = append(out, Instruction{
			Key:                  key,
			Source:               f.source,
			Text:                 text,
			NormalQuestions:      normal,
			CriticalQuestions:    critical,
			OnQuestionNotAllowed: onNotAllowed,
		})
	}
	return out, nil
}

// parseInstructionText returns the text strings of one entry, trimmed. The
// character count uses the raw string, before the trim.
func parseInstructionText(f instructionsFile, field string, entry map[string]any) ([]string, error) {
	const fix = "set a non-blank string or a non-empty array of strings."
	raw, ok := entry["text"]
	if !ok {
		return nil, dataErr(f, field, "the text is missing.", fix)
	}

	checkOne := func(s, problemPrefix string) (string, error) {
		if n := utf8.RuneCountInString(s); n > MaxInstructionText {
			return "", dataErr(f, field,
				fmt.Sprintf("%sthe text has %d characters. The limit is %d.", problemPrefix, n, MaxInstructionText),
				"shorten the text or split it into 2 instructions.")
		}
		trimmed := strings.TrimSpace(s)
		if trimmed == "" {
			return "", dataErr(f, field, problemPrefix+"the text is blank.", fix)
		}
		return trimmed, nil
	}

	switch v := raw.(type) {
	case string:
		s, err := checkOne(v, "")
		if err != nil {
			return nil, err
		}
		return []string{s}, nil
	case []any:
		if len(v) == 0 {
			return nil, dataErr(f, field, "the text is an empty array.", fix)
		}
		out := make([]string, 0, len(v))
		for i, item := range v {
			prefix := fmt.Sprintf("item %d: ", i+1)
			str, isStr := item.(string)
			if !isStr {
				return nil, dataErr(f, field, prefix+"the item is not a string.", fix)
			}
			s, err := checkOne(str, prefix)
			if err != nil {
				return nil, err
			}
			out = append(out, s)
		}
		return out, nil
	default:
		return nil, dataErr(f, field, "the text is not a string or an array of strings.", fix)
	}
}

// parseInstructionBool returns the boolean flag name of one entry, false
// when the entry does not set it.
func parseInstructionBool(f instructionsFile, field string, entry map[string]any, name string) (bool, error) {
	raw, ok := entry[name]
	if !ok {
		return false, nil
	}
	b, isBool := raw.(bool)
	if !isBool {
		return false, dataErr(f, field+"."+name,
			fmt.Sprintf("the value %s is not a boolean.", formatTOMLValue(raw)),
			"use true or false.")
	}
	return b, nil
}

// formatTOMLValue renders a decoded value for an error message: a string in
// quotes, any other value as Go prints it.
func formatTOMLValue(v any) string {
	if s, ok := v.(string); ok {
		return fmt.Sprintf("%q", s)
	}
	return fmt.Sprintf("%v", v)
}

// dataErr returns a data-kind *InstructionsError as an error.
func dataErr(f instructionsFile, field, problem, fix string) error {
	return &InstructionsError{Kind: InstructionsErrorData, File: f.label, Field: field, Problem: problem, Fix: fix}
}

// infraErr returns an infra-kind *InstructionsError as an error. It keeps err
// as Cause.
func infraErr(f instructionsFile, err error) error {
	return &InstructionsError{
		Kind:    InstructionsErrorInfra,
		File:    f.label,
		Field:   instructionsWholeFileField,
		Problem: err.Error(),
		Fix:     "make the file readable.",
		Cause:   err,
	}
}
