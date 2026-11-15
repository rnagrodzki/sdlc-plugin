// Package tools: value check of setup form fields for setup_write_sections.
//
// checkSetupValues checks each new value of a setup form field (the field
// list of setupmeta.Sections, which setup_prepare returns) before any write.
// A value equal to the value that the target file already holds is not
// checked, so a repo with a hand-written value can still save.

package tools

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/rnagrodzki/sdlc-plugin/internal/config"
	"github.com/rnagrodzki/sdlc-plugin/internal/fsx"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/setupmeta"
)

// Limits of one form field value. The check refuses a value over a limit.
const (
	maxSetupTextBytes = 1024       // one text value, or one entry of a list
	maxSetupListItems = 64         // entries of one list value
	maxSetupNumber    = 2147483647 // largest number of a field with no Max
)

// The limits as text, for the tool description of setup_write_sections.
var (
	setupTextBytesText = strconv.Itoa(maxSetupTextBytes)
	setupListItemsText = strconv.Itoa(maxSetupListItems)
	setupNumberText    = strconv.Itoa(maxSetupNumber)
)

// maxSetupQuoteBytes bounds a text value that an error message quotes, so a
// long refused text does not fill the message.
const maxSetupQuoteBytes = 64

// setupRateFields are the config paths of the string fields that hold a
// fraction from 0 to 1. config.parseAutomation reads them as numbers only, so
// normalizeSetupRates stores a decimal text as a number, and the value check
// accepts only a number from 0 to 1.
var setupRateFields = map[string]bool{
	"automation.drift.maxErrorRate":   true,
	"automation.drift.maxWarningRate": true,
}

// normalizeSetupRates changes, in place, each decimal text of a rate field in
// section to a number. key is the config path of section, as in
// checkSetupValues. A text that is not a number stays as it is, so the value
// check refuses it. A nil section changes nothing.
func normalizeSetupRates(key string, section map[string]any) {
	for path := range setupRateFields {
		if rel, ok := strings.CutPrefix(path, key+"."); ok {
			setupConvertRate(section, strings.Split(rel, "."))
		}
	}
}

// setupConvertRate converts the text at the path parts in m to a number. It
// finds the value in the nested form ({"drift":{"maxErrorRate":...}}) and in
// the dotted form ({"drift.maxErrorRate":...}).
func setupConvertRate(m map[string]any, parts []string) {
	joined := strings.Join(parts, ".")
	if s, ok := m[joined].(string); ok {
		if n, err := strconv.ParseFloat(strings.TrimSpace(s), 64); err == nil && !math.IsInf(n, 0) && !math.IsNaN(n) {
			m[joined] = n
		}
	}
	if len(parts) > 1 {
		if child, ok := m[parts[0]].(map[string]any); ok {
			setupConvertRate(child, parts[1:])
		}
	}
}

// SetupFieldError names one field value that the check refuses.
type SetupFieldError struct {
	Section string `json:"section"` // form section id, for example "plan-tasks"
	Field   string `json:"field"`   // field name in that section, for example "contractShape"
	Path    string `json:"path"`    // config path of the field, for example "plan.tasks.contractShape"
	Message string `json:"message"` // what is wrong and what the field accepts
}

// checkSetupValues checks each form field value in sections against the
// field list. sections maps a key of sectionsJson to its value, after the
// flag-set conversion. The key is a config path, not a form section id.
// The check finds the form section by its ConfigPath, as
// normalizeFlagSetFields does. current returns the table that the target
// file holds now at that key. A field whose value equals the value in
// current is not checked. The result is sorted by Path.
//
// The check reads each field of the field list, not the submitted keys: a
// submitted key with no form field is left to the write code. A key may be
// the config path of a form section (ship, plan.tasks), a parent of it
// (plan), or a path inside it (version.tag). Each section value is expanded
// with expandDottedKeys on a copy, so sections is not changed. current is
// called at most once for each key, and only for a key that holds a field
// value to check.
func checkSetupValues(sections map[string]map[string]any,
	current func(key string) (map[string]any, error)) ([]SetupFieldError, error) {
	keys := make([]string, 0, len(sections))
	for key := range sections {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var out []SetupFieldError
	for _, key := range keys {
		raw := sections[key]
		if raw == nil {
			continue // a JSON null on the whole key: nothing to check
		}
		value := expandDottedKeys(raw)
		var held map[string]any
		heldRead := false
		for _, sec := range setupmeta.Sections() {
			if sec.ConfigPath == "" || len(sec.Fields) == 0 {
				continue
			}
			for _, field := range sec.Fields {
				if field.Type == "flag-set" {
					continue // normalizeFlagSetFields checks it
				}
				path := sec.ConfigPath + "." + field.Name
				rel, inKey := strings.CutPrefix(path, key+".")
				if !inKey {
					continue
				}
				parts := strings.Split(rel, ".")
				got, present := setupLookup(value, parts)
				if !present || got == nil {
					continue
				}
				if !heldRead {
					var err error
					if held, err = current(key); err != nil {
						return nil, err
					}
					heldRead = true
				}
				if prev, ok := setupLookup(held, parts); ok && setupEqual(prev, got) {
					continue
				}
				if msg := checkSetupField(sec, field, got); msg != "" {
					out = append(out, SetupFieldError{Section: sec.ID, Field: field.Name, Path: path, Message: msg})
				}
			}
		}
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Message < out[j].Message
	})
	// Two keys of one call (plan and plan.tasks) can name the same field.
	return slices.Compact(out), nil
}

// setupLookup returns the value at the path parts in m. A missing step, or a
// step whose value is not a table, gives false.
func setupLookup(m map[string]any, parts []string) (any, bool) {
	var cur any = m
	for _, part := range parts {
		table, isTable := cur.(map[string]any)
		if !isTable {
			return nil, false
		}
		next, present := table[part]
		if !present {
			return nil, false
		}
		cur = next
	}
	return cur, true
}

// setupEqual reports whether a and b hold the same value: the code applies
// config.WholeNumbersToInt to both values, then json.Marshal, and compares the
// bytes. A value that does not marshal is never equal.
func setupEqual(a, b any) bool {
	ab, errA := json.Marshal(config.WholeNumbersToInt(a))
	bb, errB := json.Marshal(config.WholeNumbersToInt(b))
	return errA == nil && errB == nil && string(ab) == string(bb)
}

// checkSetupField checks one non-null value of a form field. It returns ""
// for an accepted value, else the text that states what is wrong and what the
// field accepts. A field type with no rule is accepted.
func checkSetupField(sec setupmeta.Section, field setupmeta.Field, v any) string {
	switch field.Type {
	case "boolean":
		if _, ok := v.(bool); !ok {
			return fmt.Sprintf("true or false is required, got %s.", setupValueText(v))
		}
	case "enum":
		options := strings.Join(field.Options, ", ")
		s, ok := v.(string)
		if !ok {
			return fmt.Sprintf("one of the options is required, got %s. Options: %s.", setupValueText(v), options)
		}
		if !slices.Contains(field.Options, s) {
			return fmt.Sprintf("%s is not an option. Options: %s.", setupQuote(s), options)
		}
	case "multi-enum":
		options := strings.Join(field.Options, ", ")
		list, ok := v.([]any)
		if !ok {
			return fmt.Sprintf("a list of options is required, got %s. Options: %s.", setupTypeName(v), options)
		}
		seen := map[string]bool{}
		for _, item := range list {
			s, isText := item.(string)
			if !isText || !slices.Contains(field.Options, s) {
				return fmt.Sprintf("%s is not an option. Options: %s.", setupValueText(item), options)
			}
			if seen[s] {
				return fmt.Sprintf("%s is in the list more than once. Each option can show one time.", setupQuote(s))
			}
			seen[s] = true
		}
	case "number":
		return checkSetupNumber(field, v)
	case "string":
		if setupRateFields[sec.ConfigPath+"."+field.Name] {
			return checkSetupRate(v)
		}
		if msg := checkSetupText(v, "a text value is required"); msg != "" {
			return msg
		}
		if sec.ConfigPath == "version" && field.Name == "pushAuth.secretName" {
			if name, _ := v.(string); name != "" && validateSecretName(name) != nil {
				return fmt.Sprintf("%s is not a valid GitHub Actions secret name. Use only letters, digits and underscores, and do not start with a digit or with GITHUB_ (for example RELEASE_TOKEN).", setupQuote(name))
			}
		}
	case "list":
		list, ok := v.([]any)
		if !ok {
			return fmt.Sprintf("a list of text values is required, got %s.", setupTypeName(v))
		}
		if len(list) > maxSetupListItems {
			return fmt.Sprintf("the list has %d entries, over the limit of %d.", len(list), maxSetupListItems)
		}
		for i, item := range list {
			s, isText := item.(string)
			if !isText {
				return fmt.Sprintf("entry %d must be a text value, got %s.", i+1, setupTypeName(item))
			}
			if len(s) > maxSetupTextBytes {
				return fmt.Sprintf("entry %d has %d bytes, over the limit of %d bytes.", i+1, len(s), maxSetupTextBytes)
			}
		}
	}
	return ""
}

// checkSetupNumber checks the value of a number field: a whole number inside
// Min and Max. A field with no Max accepts at most maxSetupNumber.
func checkSetupNumber(field setupmeta.Field, v any) string {
	hi := maxSetupNumber
	if field.Max != nil {
		hi = *field.Max
	}
	want := fmt.Sprintf("a whole number of at most %d is required", hi)
	if field.Min != nil {
		want = fmt.Sprintf("a whole number from %d to %d is required", *field.Min, hi)
	}
	n, ok := v.(float64)
	if !ok || n != math.Trunc(n) || n > float64(hi) || (field.Min != nil && n < float64(*field.Min)) {
		return fmt.Sprintf("%s, got %s.", want, setupValueText(v))
	}
	return ""
}

// checkSetupRate checks the value of a rate field (setupRateFields): a number
// from 0 to 1. normalizeSetupRates has already changed a decimal text to a
// number.
func checkSetupRate(v any) string {
	n, ok := v.(float64)
	if !ok || math.IsNaN(n) || n < 0 || n > 1 {
		return fmt.Sprintf("a number from 0 to 1 is required, got %s.", setupValueText(v))
	}
	return ""
}

// checkSetupText checks one text value against maxSetupTextBytes. want starts
// the message for a value that is not a text.
func checkSetupText(v any, want string) string {
	s, ok := v.(string)
	if !ok {
		return fmt.Sprintf("%s, got %s.", want, setupTypeName(v))
	}
	if len(s) > maxSetupTextBytes {
		return fmt.Sprintf("the text has %d bytes, over the limit of %d bytes.", len(s), maxSetupTextBytes)
	}
	return ""
}

// setupTypeName names the JSON type of a decoded value for a message.
func setupTypeName(v any) string {
	switch v.(type) {
	case string:
		return "a text"
	case bool:
		return "a boolean"
	case float64:
		return "a number"
	case []any:
		return "a list"
	case map[string]any:
		return "a table"
	case nil:
		return "null"
	default:
		return fmt.Sprintf("a %T", v)
	}
}

// setupValueText renders a decoded value for a message: a text is quoted, a
// number or a boolean is printed as is, and a list or a table gets its type
// name. A number uses the exponent form when it has 21 or more digits, so a
// large refused number such as 1e300 stays short.
func setupValueText(v any) string {
	switch val := v.(type) {
	case string:
		return setupQuote(val)
	case bool:
		return strconv.FormatBool(val)
	case float64:
		if math.Abs(val) >= 1e21 {
			return strconv.FormatFloat(val, 'e', -1, 64)
		}
		return strconv.FormatFloat(val, 'f', -1, 64)
	default:
		return setupTypeName(v)
	}
}

// setupQuote quotes s for a message, cut to maxSetupQuoteBytes.
func setupQuote(s string) string {
	if len(s) <= maxSetupQuoteBytes {
		return strconv.Quote(s)
	}
	cut := s[:maxSetupQuoteBytes]
	for len(cut) > 0 && !utf8.ValidString(cut) {
		cut = cut[:len(cut)-1] // do not end inside a multi-byte rune
	}
	return strconv.Quote(cut) + " (cut)"
}

// SetupRefusedText states the count of the values that the value check
// refuses, for example "1 field value is refused".
func SetupRefusedText(n int) string {
	if n == 1 {
		return "1 field value is refused"
	}
	return fmt.Sprintf("%d field values are refused", n)
}

// setupFieldErrorsText states the count of the refused values, then the path
// and the message of each one.
func setupFieldErrorsText(errs []SetupFieldError) string {
	parts := make([]string, 0, len(errs))
	for _, e := range errs {
		parts = append(parts, e.Path+": "+e.Message)
	}
	return SetupRefusedText(len(errs)) + ". " + strings.Join(parts, " ")
}

// setupValuesError is the DomainError of a call that the value check refuses.
func setupValuesError(errs []SetupFieldError) error {
	return &mcpserver.DomainError{
		Msg:        "setup_write_sections: " + setupFieldErrorsText(errs),
		Suggestion: "Correct each named field, then call setup_write_sections again. Nothing was written.",
	}
}

// tomlReader names the caller of readCheckedTOML in its error texts.
type tomlReader struct {
	caller string // starts each Msg, for example "setup_write_sections"
	check  string // the check that the byte limit belongs to
	tail   string // ends each Suggestion: " Nothing was written." for a write, "" for a read-only call
}

// The readers of the TOML files of setup_write_sections and of the dashboard
// Setup tab.
var (
	guardrailCheckReader = tomlReader{caller: "setup_write_sections", check: "the guardrail check", tail: " Nothing was written."}
	setupValueReader     = tomlReader{caller: "setup_write_sections", check: "the setup value check", tail: " Nothing was written."}
	setupSaveReader      = tomlReader{caller: "dashboard setup save", check: "the setup value check", tail: " Nothing was written."}
	setupStateReader     = tomlReader{caller: "dashboard setup", check: "the Setup tab"}
)

// readCheckedTOML reads and decodes the TOML file at path. An absent file
// gives a nil map and no error; an empty file gives an empty map. It checks
// the file size against maxCheckedFileBytes before the read. A file over the
// limit or with bad TOML is a DataError; a failed stat or read is an
// InfraError. r names the caller in each error text.
func readCheckedTOML(path string, r tomlReader) (map[string]any, error) {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, r.readError(path, err)
	}
	if info.Size() > maxCheckedFileBytes {
		return nil, &mcpserver.DataError{
			Msg:        fmt.Sprintf("%s: %s is %d bytes, over the %d byte limit of %s", r.caller, path, info.Size(), maxCheckedFileBytes, r.check),
			Suggestion: fmt.Sprintf("Reduce %s to %d bytes or less, then retry.%s", path, maxCheckedFileBytes, r.tail),
		}
	}
	var raw map[string]any
	if err := fsx.ReadTOML(path, &raw); err != nil {
		if errors.Is(err, fsx.ErrParse) {
			return nil, &mcpserver.DataError{
				Msg:        fmt.Sprintf("%s: %s is not valid TOML: %s", r.caller, path, err.Error()),
				Suggestion: "Fix the TOML syntax of " + path + " by hand, then retry." + r.tail,
				Cause:      err,
			}
		}
		return nil, r.readError(path, err)
	}
	if raw == nil {
		raw = map[string]any{}
	}
	return raw, nil
}

// readError is the InfraError of a failed stat or read of path.
func (r tomlReader) readError(path string, cause error) error {
	return &mcpserver.InfraError{
		Msg:        fmt.Sprintf("%s: read %s: %s", r.caller, path, cause.Error()),
		Suggestion: fmt.Sprintf("Check that %s is a readable file, then retry.%s", path, r.tail),
		Cause:      cause,
	}
}

// readSetupFile reads the TOML file at path for the setup value check of
// setup_write_sections (readCheckedTOML).
func readSetupFile(path string) (map[string]any, error) {
	return readCheckedTOML(path, setupValueReader)
}

// setupTable returns the table at the dotted config path key in doc. An
// absent key gives an empty map and true. A value that is not a table
// gives an empty map and false.
func setupTable(doc map[string]any, key string) (map[string]any, bool) {
	cur := doc
	for _, part := range strings.Split(key, ".") {
		next, present := cur[part]
		if !present {
			return map[string]any{}, true
		}
		table, isTable := next.(map[string]any)
		if !isTable {
			return map[string]any{}, false
		}
		cur = table
	}
	if cur == nil {
		return map[string]any{}, true
	}
	return cur, true
}

// readTargetSection returns the table at the config path key in the file
// at path, through readSetupFile and setupTable. An absent file, an absent
// table, or a value that is not a table gives an empty map.
func readTargetSection(path, key string) (map[string]any, error) {
	doc, err := readSetupFile(path)
	if err != nil {
		return nil, err
	}
	table, _ := setupTable(doc, key)
	return table, nil
}

// setupTargetPath returns the absolute path of the file that holds the
// config path key for target ("", "project" or "user"). It calls
// sectionFile and sectionRoot for a project file and returns userPath for
// target user.
func setupTargetPath(contentRoot, stateRoot, userPath, target, key string) string {
	top, _, _ := strings.Cut(key, ".")
	if target == "user" && !config.ProjectSections[top] {
		return userPath
	}
	return filepath.Join(sectionRoot(contentRoot, stateRoot, key), filepath.FromSlash(sectionFile(key, target, userPath)))
}
