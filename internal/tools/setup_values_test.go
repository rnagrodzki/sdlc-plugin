// Package tools: tests for the value check of setup form fields.
package tools

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
)

// runSetupCheck decodes sectionsJSON as setupWriteSections does and runs
// checkSetupValues with held as the tables of the target files. It returns the
// field errors and the keys that the check read.
func runSetupCheck(t *testing.T, sectionsJSON string, held map[string]map[string]any) ([]SetupFieldError, []string) {
	t.Helper()
	var sections map[string]map[string]any
	if err := json.Unmarshal([]byte(sectionsJSON), &sections); err != nil {
		t.Fatalf("bad test JSON %s: %v", sectionsJSON, err)
	}
	var reads []string
	errs, err := checkSetupValues(sections, func(key string) (map[string]any, error) {
		reads = append(reads, key)
		if table, ok := held[key]; ok {
			return table, nil
		}
		return map[string]any{}, nil
	})
	if err != nil {
		t.Fatalf("checkSetupValues(%s): %v", sectionsJSON, err)
	}
	return errs, reads
}

// setupTextList is a JSON list of n texts, each of size bytes.
func setupTextList(n, size int) string {
	items := make([]string, n)
	for i := range items {
		items[i] = `"` + strings.Repeat("a", size) + `"`
	}
	return "[" + strings.Join(items, ",") + "]"
}

// TestCheckSetupValues covers the rule table and the limit table of the
// check, the four key forms, and the values that pass with no check.
func TestCheckSetupValues(t *testing.T) {
	text1024 := strings.Repeat("a", 1024)
	text1025 := strings.Repeat("a", 1025)
	cases := []struct {
		name string
		json string
		held map[string]map[string]any
		want []SetupFieldError // nil: the check accepts the call
	}{
		// boolean
		{name: "boolean true", json: `{"ship":{"draft":true}}`},
		{name: "boolean false", json: `{"ship":{"draft":false}}`},
		{name: "boolean text", json: `{"ship":{"draft":"yes"}}`, want: []SetupFieldError{
			{Section: "ship", Field: "draft", Path: "ship.draft", Message: `true or false is required, got "yes".`}}},
		// enum
		{name: "enum option", json: `{"review":{"scope":"staged"}}`},
		{name: "enum not an option", json: `{"review":{"scope":"everything"}}`, want: []SetupFieldError{
			{Section: "review", Field: "scope", Path: "review.scope", Message: `"everything" is not an option. Options: all, committed, staged, working, worktree.`}}},
		{name: "enum number", json: `{"review":{"scope":3}}`, want: []SetupFieldError{
			{Section: "review", Field: "scope", Path: "review.scope", Message: `one of the options is required, got 3. Options: all, committed, staged, working, worktree.`}}},
		// multi-enum
		{name: "multi-enum options", json: `{"receivedReview":{"alwaysFixSeverities":["critical","high"]}}`},
		{name: "multi-enum empty list", json: `{"receivedReview":{"alwaysFixSeverities":[]}}`},
		{name: "multi-enum not an option", json: `{"receivedReview":{"alwaysFixSeverities":["urgent"]}}`, want: []SetupFieldError{
			{Section: "received-review", Field: "alwaysFixSeverities", Path: "receivedReview.alwaysFixSeverities", Message: `"urgent" is not an option. Options: critical, high, medium, low, info.`}}},
		{name: "multi-enum repeat", json: `{"receivedReview":{"alwaysFixSeverities":["high","high"]}}`, want: []SetupFieldError{
			{Section: "received-review", Field: "alwaysFixSeverities", Path: "receivedReview.alwaysFixSeverities", Message: `"high" is in the list more than once. Each option can show one time.`}}},
		{name: "multi-enum text", json: `{"receivedReview":{"alwaysFixSeverities":"high"}}`, want: []SetupFieldError{
			{Section: "received-review", Field: "alwaysFixSeverities", Path: "receivedReview.alwaysFixSeverities", Message: `a list of options is required, got a text. Options: critical, high, medium, low, info.`}}},
		// number with Max
		{name: "number at min", json: `{"ship":{"verifyPipelineMaxIterations":1}}`},
		{name: "number at max", json: `{"ship":{"verifyPipelineMaxIterations":10}}`},
		{name: "number under min", json: `{"ship":{"verifyPipelineMaxIterations":0}}`, want: []SetupFieldError{
			{Section: "ship", Field: "verifyPipelineMaxIterations", Path: "ship.verifyPipelineMaxIterations", Message: `a whole number from 1 to 10 is required, got 0.`}}},
		{name: "number over max", json: `{"ship":{"verifyPipelineMaxIterations":11}}`, want: []SetupFieldError{
			{Section: "ship", Field: "verifyPipelineMaxIterations", Path: "ship.verifyPipelineMaxIterations", Message: `a whole number from 1 to 10 is required, got 11.`}}},
		{name: "number with fraction", json: `{"ship":{"verifyPipelineMaxIterations":1.5}}`, want: []SetupFieldError{
			{Section: "ship", Field: "verifyPipelineMaxIterations", Path: "ship.verifyPipelineMaxIterations", Message: `a whole number from 1 to 10 is required, got 1.5.`}}},
		{name: "number text", json: `{"ship":{"verifyPipelineMaxIterations":"5"}}`, want: []SetupFieldError{
			{Section: "ship", Field: "verifyPipelineMaxIterations", Path: "ship.verifyPipelineMaxIterations", Message: `a whole number from 1 to 10 is required, got "5".`}}},
		// number with no Max
		{name: "number at maxSetupNumber", json: `{"ship":{"verifyPipelineTimeout":2147483647}}`},
		{name: "number over maxSetupNumber", json: `{"ship":{"verifyPipelineTimeout":2147483648}}`, want: []SetupFieldError{
			{Section: "ship", Field: "verifyPipelineTimeout", Path: "ship.verifyPipelineTimeout", Message: `a whole number from 30 to 2147483647 is required, got 2147483648.`}}},
		// string
		{name: "text of 1024 bytes", json: `{"jira":{"defaultProject":"` + text1024 + `"}}`},
		{name: "text of 1025 bytes", json: `{"jira":{"defaultProject":"` + text1025 + `"}}`, want: []SetupFieldError{
			{Section: "jira", Field: "defaultProject", Path: "jira.defaultProject", Message: `the text has 1025 bytes, over the limit of 1024 bytes.`}}},
		{name: "text of 1100 bytes in a dotted field", json: `{"version":{"tag.prefix":"` + strings.Repeat("v", 1100) + `"}}`, want: []SetupFieldError{
			{Section: "version", Field: "tag.prefix", Path: "version.tag.prefix", Message: `the text has 1100 bytes, over the limit of 1024 bytes.`}}},
		{name: "text field with a number", json: `{"jira":{"defaultProject":3}}`, want: []SetupFieldError{
			{Section: "jira", Field: "defaultProject", Path: "jira.defaultProject", Message: `a text value is required, got a number.`}}},
		{name: "empty text", json: `{"jira":{"defaultProject":""}}`},
		// list
		{name: "list of 64 entries", json: `{"style":{"technicalTerms":` + setupTextList(64, 1024) + `}}`},
		{name: "list of 65 entries", json: `{"style":{"technicalTerms":` + setupTextList(65, 1) + `}}`, want: []SetupFieldError{
			{Section: "communication-style", Field: "technicalTerms", Path: "style.technicalTerms", Message: `the list has 65 entries, over the limit of 64.`}}},
		{name: "list entry of 1025 bytes", json: `{"style":{"technicalTerms":["ok",` + `"` + text1025 + `"]}}`, want: []SetupFieldError{
			{Section: "communication-style", Field: "technicalTerms", Path: "style.technicalTerms", Message: `entry 2 has 1025 bytes, over the limit of 1024 bytes.`}}},
		{name: "list entry not a text", json: `{"style":{"technicalTerms":["ok",true]}}`, want: []SetupFieldError{
			{Section: "communication-style", Field: "technicalTerms", Path: "style.technicalTerms", Message: `entry 2 must be a text value, got a boolean.`}}},
		{name: "list as text", json: `{"style":{"technicalTerms":"PR"}}`, want: []SetupFieldError{
			{Section: "communication-style", Field: "technicalTerms", Path: "style.technicalTerms", Message: `a list of text values is required, got a text.`}}},
		// secret name
		{name: "secret name valid", json: `{"version":{"pushAuth":{"secretName":"RELEASE_TOKEN"}}}`},
		{name: "secret name empty", json: `{"version":{"pushAuth.secretName":""}}`},
		{name: "secret name starts with a digit", json: `{"version":{"pushAuth.secretName":"1TOKEN"}}`, want: []SetupFieldError{
			{Section: "version", Field: "pushAuth.secretName", Path: "version.pushAuth.secretName", Message: `version.pushAuth.secretName "1TOKEN" is not a valid GitHub Actions secret name`}}},
		{name: "secret name GITHUB_", json: `{"version":{"pushAuth.secretName":"GITHUB_TOKEN"}}`, want: []SetupFieldError{
			{Section: "version", Field: "pushAuth.secretName", Path: "version.pushAuth.secretName", Message: `version.pushAuth.secretName "GITHUB_TOKEN" is not a valid GitHub Actions secret name`}}},
		// key forms
		{name: "dotted key", json: `{"plan.tasks":{"contractShape":"x"}}`, want: []SetupFieldError{
			{Section: "plan-tasks", Field: "contractShape", Path: "plan.tasks.contractShape", Message: `"x" is not an option. Options: full, minimal, none.`}}},
		{name: "parent key", json: `{"plan":{"tasks":{"contractShape":"x"}}}`, want: []SetupFieldError{
			{Section: "plan-tasks", Field: "contractShape", Path: "plan.tasks.contractShape", Message: `"x" is not an option. Options: full, minimal, none.`}}},
		{name: "parent key with a dotted name", json: `{"plan":{"tasks.contractShape":"x"}}`, want: []SetupFieldError{
			{Section: "plan-tasks", Field: "contractShape", Path: "plan.tasks.contractShape", Message: `"x" is not an option. Options: full, minimal, none.`}}},
		{name: "dotted field name", json: `{"version":{"tag.enabled":"yes"}}`, want: []SetupFieldError{
			{Section: "version", Field: "tag.enabled", Path: "version.tag.enabled", Message: `true or false is required, got "yes".`}}},
		{name: "nested field", json: `{"version":{"tag":{"enabled":"yes"}}}`, want: []SetupFieldError{
			{Section: "version", Field: "tag.enabled", Path: "version.tag.enabled", Message: `true or false is required, got "yes".`}}},
		{name: "key inside a section", json: `{"version.tag":{"enabled":"yes"}}`, want: []SetupFieldError{
			{Section: "version", Field: "tag.enabled", Path: "version.tag.enabled", Message: `true or false is required, got "yes".`}}},
		{name: "form section id differs from the config path", json: `{"style":{"tone":"loud"}}`, want: []SetupFieldError{
			{Section: "communication-style", Field: "tone", Path: "style.tone", Message: `"loud" is not an option. Options: direct, neutral.`}}},
		{name: "two keys name one field", json: `{"plan":{"tasks":{"contractShape":"x"}},"plan.tasks":{"contractShape":"x"}}`, want: []SetupFieldError{
			{Section: "plan-tasks", Field: "contractShape", Path: "plan.tasks.contractShape", Message: `"x" is not an option. Options: full, minimal, none.`}}},
		{name: "sorted by path", json: `{"ship":{"draft":"yes"},"review":{"scope":"everything"}}`, want: []SetupFieldError{
			{Section: "review", Field: "scope", Path: "review.scope", Message: `"everything" is not an option. Options: all, committed, staged, working, worktree.`},
			{Section: "ship", Field: "draft", Path: "ship.draft", Message: `true or false is required, got "yes".`}}},
		// values with no check
		{name: "null value", json: `{"ship":{"draft":null}}`},
		{name: "null section", json: `{"ship":null}`},
		{name: "key with no form field", json: `{"ship":{"nope":"x"}}`},
		{name: "section with no form field", json: `{"commit":{"allowedTypes":3}}`},
		{name: "flag-set value", json: `{"ship":{"steps":{"execute":true}}}`},
		{name: "section value not a table at the field path", json: `{"plan":{"tasks":"x"}}`},
		// value that the target file holds
		{name: "held bump label", json: `{"ship":{"bump":"rc"}}`, held: map[string]map[string]any{"ship": {"bump": "rc"}}},
		{name: "new bump label", json: `{"ship":{"bump":"rc"}}`, want: []SetupFieldError{
			{Section: "ship", Field: "bump", Path: "ship.bump", Message: `"rc" is not an option. Options: patch, minor, major.`}}},
		{name: "held rebase true", json: `{"ship":{"rebase":true}}`, held: map[string]map[string]any{"ship": {"rebase": true}}},
		{name: "new rebase true", json: `{"ship":{"rebase":true}}`, want: []SetupFieldError{
			{Section: "ship", Field: "rebase", Path: "ship.rebase", Message: `one of the options is required, got true. Options: auto, skip, prompt.`}}},
		{name: "held empty fileType", json: `{"version":{"versionFile":{"fileType":""}}}`, held: map[string]map[string]any{"version": {"versionFile": map[string]any{"fileType": ""}}}},
		{name: "new empty fileType", json: `{"version":{"versionFile.fileType":""}}`, want: []SetupFieldError{
			{Section: "version", Field: "versionFile.fileType", Path: "version.versionFile.fileType", Message: `"" is not an option. Options: package.json, cargo.toml, pyproject.toml, pubspec.yaml, plugin.json, version-file.`}}},
		{name: "held number decoded as int64", json: `{"ship":{"verifyPipelineMaxIterations":20}}`, held: map[string]map[string]any{"ship": {"verifyPipelineMaxIterations": int64(20)}}},
		{name: "held number decoded as float64", json: `{"ship":{"verifyPipelineMaxIterations":20}}`, held: map[string]map[string]any{"ship": {"verifyPipelineMaxIterations": float64(20)}}},
		{name: "held value differs", json: `{"ship":{"bump":"beta"}}`, held: map[string]map[string]any{"ship": {"bump": "rc"}}, want: []SetupFieldError{
			{Section: "ship", Field: "bump", Path: "ship.bump", Message: `"beta" is not an option. Options: patch, minor, major.`}}},
		{name: "plan example", json: `{"ship":{"bump":"rc","draft":"yes"}}`, held: map[string]map[string]any{"ship": {"bump": "rc"}}, want: []SetupFieldError{
			{Section: "ship", Field: "draft", Path: "ship.draft", Message: `true or false is required, got "yes".`}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := runSetupCheck(t, tc.json, tc.held)
			gotJSON, _ := json.Marshal(got)
			wantJSON, _ := json.Marshal(tc.want)
			if string(gotJSON) != string(wantJSON) {
				t.Errorf("checkSetupValues(%.200s)\n got: %s\nwant: %s", tc.json, gotJSON, wantJSON)
			}
		})
	}
}

// TestCheckSetupValues_ReadsOnlyWhenNeeded verifies that the check reads the
// target file only for a key that holds a field value, once for each key, and
// that a failed read stops the check.
func TestCheckSetupValues_ReadsOnlyWhenNeeded(t *testing.T) {
	_, reads := runSetupCheck(t, `{"commit":{"allowedTypes":[]},"ship":{"draft":true,"auto":false},"review":{"scope":null}}`, nil)
	if fmt.Sprint(reads) != "[ship]" {
		t.Errorf("reads = %v, want [ship]", reads)
	}

	readErr := errors.New("boom")
	_, err := checkSetupValues(map[string]map[string]any{"ship": {"draft": true}}, func(string) (map[string]any, error) {
		return nil, readErr
	})
	if !errors.Is(err, readErr) {
		t.Errorf("err = %v, want the read error", err)
	}
}

// TestCheckSetupValues_DoesNotChangeSections verifies that the check expands
// a copy of each section and leaves the input as it was.
func TestCheckSetupValues_DoesNotChangeSections(t *testing.T) {
	sections := map[string]map[string]any{"version": {"tag.enabled": true}}
	if _, err := checkSetupValues(sections, func(string) (map[string]any, error) { return nil, nil }); err != nil {
		t.Fatal(err)
	}
	if _, ok := sections["version"]["tag.enabled"]; !ok || len(sections["version"]) != 1 {
		t.Errorf("sections changed: %v", sections)
	}
}

// TestSetupValuesError verifies the text of a refused call.
func TestSetupValuesError(t *testing.T) {
	err := setupValuesError([]SetupFieldError{
		{Path: "review.scope", Message: `"everything" is not an option. Options: all, committed, staged, working, worktree.`},
		{Path: "ship.draft", Message: `true or false is required, got "yes".`},
	})
	var de *mcpserver.DomainError
	if !errors.As(err, &de) {
		t.Fatalf("err = %T, want *mcpserver.DomainError", err)
	}
	wantMsg := `setup_write_sections: 2 field values are refused. review.scope: "everything" is not an option. Options: all, committed, staged, working, worktree. ship.draft: true or false is required, got "yes".`
	if de.Msg != wantMsg {
		t.Errorf("Msg =\n%s\nwant\n%s", de.Msg, wantMsg)
	}
	if de.Suggestion != "Correct each named field, then call setup_write_sections again. Nothing was written." {
		t.Errorf("Suggestion = %q", de.Suggestion)
	}
	one := setupValuesError([]SetupFieldError{{Path: "ship.draft", Message: "x."}})
	if !strings.HasPrefix(one.Error(), "setup_write_sections: 1 field value is refused. ship.draft: x.") {
		t.Errorf("one error text = %q", one.Error())
	}
}

// writeSizedTOML writes a valid TOML file of exactly size bytes: one comment
// line padded with "a".
func writeSizedTOML(t *testing.T, path string, size int) {
	t.Helper()
	body := "#" + strings.Repeat("a", size-2) + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestReadSetupFile covers the read failure table and the file size limit.
func TestReadSetupFile(t *testing.T) {
	dir := t.TempDir()

	t.Run("absent file", func(t *testing.T) {
		doc, err := readSetupFile(filepath.Join(dir, "none.toml"))
		if err != nil || doc != nil {
			t.Errorf("got %v, %v; want nil, nil", doc, err)
		}
	})

	t.Run("file at the limit", func(t *testing.T) {
		path := filepath.Join(dir, "limit.toml")
		writeSizedTOML(t, path, maxCheckedFileBytes)
		doc, err := readSetupFile(path)
		if err != nil || doc == nil {
			t.Errorf("got %v, %v; want an empty map and no error", doc, err)
		}
	})

	t.Run("file over the limit", func(t *testing.T) {
		path := filepath.Join(dir, "over.toml")
		writeSizedTOML(t, path, maxCheckedFileBytes+1)
		_, err := readSetupFile(path)
		var de *mcpserver.DataError
		if !errors.As(err, &de) {
			t.Fatalf("err = %T %v, want *mcpserver.DataError", err, err)
		}
		for _, want := range []string{path, "1048577", "1048576"} {
			if !strings.Contains(de.Msg, want) {
				t.Errorf("Msg %q does not name %q", de.Msg, want)
			}
		}
		if !strings.Contains(de.Suggestion, path) || !strings.HasSuffix(de.Suggestion, "Nothing was written.") {
			t.Errorf("Suggestion = %q", de.Suggestion)
		}
	})

	t.Run("invalid TOML", func(t *testing.T) {
		path := filepath.Join(dir, "bad.toml")
		if err := os.WriteFile(path, []byte("[ship\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := readSetupFile(path)
		var de *mcpserver.DataError
		if !errors.As(err, &de) {
			t.Fatalf("err = %T %v, want *mcpserver.DataError", err, err)
		}
		if !strings.Contains(de.Msg, path) || !strings.Contains(de.Suggestion, "by hand") || !strings.HasSuffix(de.Suggestion, "Nothing was written.") {
			t.Errorf("error = %q / %q", de.Msg, de.Suggestion)
		}
	})

	t.Run("directory", func(t *testing.T) {
		path := filepath.Join(dir, "local.toml")
		if err := os.Mkdir(path, 0o755); err != nil {
			t.Fatal(err)
		}
		_, err := readSetupFile(path)
		var ie *mcpserver.InfraError
		if !errors.As(err, &ie) {
			t.Fatalf("err = %T %v, want *mcpserver.InfraError", err, err)
		}
		if !strings.Contains(ie.Msg, path) || !strings.HasSuffix(ie.Suggestion, "Nothing was written.") {
			t.Errorf("error = %q / %q", ie.Msg, ie.Suggestion)
		}
	})
}

// TestReadTargetSection covers each form of the section in the target file.
func TestReadTargetSection(t *testing.T) {
	cases := []struct {
		name string
		file string
		key  string
		want string // JSON of the result
	}{
		{name: "table", file: "[ship]\nbump = \"rc\"\n", key: "ship", want: `{"bump":"rc"}`},
		{name: "dotted keys", file: "ship.bump = \"rc\"\n", key: "ship", want: `{"bump":"rc"}`},
		{name: "inline table", file: "ship = { bump = \"rc\" }\n", key: "ship", want: `{"bump":"rc"}`},
		{name: "nested table", file: "[version.tag]\nprefix = \"v\"\n", key: "version", want: `{"tag":{"prefix":"v"}}`},
		{name: "dotted config path", file: "[plan.tasks]\ncontractShape = \"full\"\n", key: "plan.tasks", want: `{"contractShape":"full"}`},
		{name: "empty table", file: "[ship]\n", key: "ship", want: `{}`},
		{name: "empty file", file: "", key: "ship", want: `{}`},
		{name: "no such table", file: "[review]\nscope = \"all\"\n", key: "ship", want: `{}`},
		{name: "not a table", file: "ship = \"x\"\n", key: "ship", want: `{}`},
		{name: "parent not a table", file: "plan = 3\n", key: "plan.tasks", want: `{}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "local.toml")
			if err := os.WriteFile(path, []byte(tc.file), 0o644); err != nil {
				t.Fatal(err)
			}
			got, err := readTargetSection(path, tc.key)
			if err != nil {
				t.Fatal(err)
			}
			gotJSON, _ := json.Marshal(got)
			if string(gotJSON) != tc.want {
				t.Errorf("got %s, want %s", gotJSON, tc.want)
			}
		})
	}

	t.Run("absent file", func(t *testing.T) {
		got, err := readTargetSection(filepath.Join(t.TempDir(), "none.toml"), "ship")
		if err != nil || got == nil || len(got) != 0 {
			t.Errorf("got %v, %v; want an empty map", got, err)
		}
	})

	t.Run("worktree where the two roots differ", func(t *testing.T) {
		contentRoot, stateRoot := t.TempDir(), t.TempDir()
		writeSDLCFile(t, contentRoot, "config.toml", "[version]\nmethod = \"pr\"\n")
		writeSDLCFile(t, contentRoot, "local.toml", "[ship]\nbump = \"wrong-root\"\n")
		writeSDLCFile(t, stateRoot, "local.toml", "[ship]\nbump = \"rc\"\n")
		writeSDLCFile(t, stateRoot, "config.toml", "[version]\nmethod = \"wrong-root\"\n")

		ship, err := readTargetSection(setupTargetPath(contentRoot, stateRoot, "", "", "ship"), "ship")
		if err != nil || ship["bump"] != "rc" {
			t.Errorf("ship = %v, %v; want bump rc from the state root", ship, err)
		}
		version, err := readTargetSection(setupTargetPath(contentRoot, stateRoot, "", "project", "version"), "version")
		if err != nil || version["method"] != "pr" {
			t.Errorf("version = %v, %v; want method pr from the content root", version, err)
		}
	})

	t.Run("target user", func(t *testing.T) {
		root := t.TempDir()
		userPath := filepath.Join(t.TempDir(), "user.toml")
		if err := os.WriteFile(userPath, []byte("[ship]\nbump = \"user\"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		writeSDLCFile(t, root, "local.toml", "[ship]\nbump = \"project\"\n")
		if got := setupTargetPath(root, root, userPath, "user", "ship"); got != userPath {
			t.Errorf("setupTargetPath(user, ship) = %q, want %q", got, userPath)
		}
		ship, err := readTargetSection(setupTargetPath(root, root, userPath, "user", "ship"), "ship")
		if err != nil || ship["bump"] != "user" {
			t.Errorf("ship = %v, %v; want bump user", ship, err)
		}
		if got, want := setupTargetPath(root, root, userPath, "user", "version"), filepath.Join(root, ".sdlc-v2", "config.toml"); got != want {
			t.Errorf("setupTargetPath(user, version) = %q, want %q", got, want)
		}
	})
}
