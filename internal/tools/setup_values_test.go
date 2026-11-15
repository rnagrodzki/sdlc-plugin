// Package tools: tests for the value check of setup form fields.
package tools

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rnagrodzki/sdlc-plugin/internal/fsx"
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
			{Section: "version", Field: "pushAuth.secretName", Path: "version.pushAuth.secretName", Message: `"1TOKEN" is not a valid GitHub Actions secret name. Use only letters, digits and underscores, and do not start with a digit or with GITHUB_ (for example RELEASE_TOKEN).`}}},
		{name: "secret name GITHUB_", json: `{"version":{"pushAuth.secretName":"GITHUB_TOKEN"}}`, want: []SetupFieldError{
			{Section: "version", Field: "pushAuth.secretName", Path: "version.pushAuth.secretName", Message: `"GITHUB_TOKEN" is not a valid GitHub Actions secret name. Use only letters, digits and underscores, and do not start with a digit or with GITHUB_ (for example RELEASE_TOKEN).`}}},
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
		if !errors.Is(err, fsx.ErrParse) {
			t.Errorf("err %v does not wrap fsx.ErrParse", err)
		}
		if de.Cause == nil {
			t.Fatal("Cause = nil, want the parse error")
		}
		if want := fmt.Sprintf("setup_write_sections: %s is not valid TOML: %s", path, de.Cause.Error()); de.Msg != want {
			t.Errorf("Msg =\n%s\nwant\n%s", de.Msg, want)
		}
		if want := "Fix the TOML syntax of " + path + " by hand, then retry. Nothing was written."; de.Suggestion != want {
			t.Errorf("Suggestion = %q, want %q", de.Suggestion, want)
		}
	})

	t.Run("empty file", func(t *testing.T) {
		path := filepath.Join(dir, "empty.toml")
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		doc, err := readSetupFile(path)
		if err != nil || doc == nil || len(doc) != 0 {
			t.Errorf("got %v (nil: %t), %v; want an empty map and no error", doc, doc == nil, err)
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
		if ie.Cause == nil {
			t.Fatal("Cause = nil, want the read error")
		}
		if want := "setup_write_sections: read " + path + ": " + ie.Cause.Error(); ie.Msg != want {
			t.Errorf("Msg =\n%s\nwant\n%s", ie.Msg, want)
		}
		if want := "Check that " + path + " is a readable file, then retry. Nothing was written."; ie.Suggestion != want {
			t.Errorf("Suggestion = %q, want %q", ie.Suggestion, want)
		}
	})

	t.Run("parent is a regular file", func(t *testing.T) {
		parent := filepath.Join(dir, "plain")
		if err := os.WriteFile(parent, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(parent, "local.toml")
		_, err := readSetupFile(path)
		var ie *mcpserver.InfraError
		if !errors.As(err, &ie) {
			t.Fatalf("err = %T %v, want *mcpserver.InfraError", err, err)
		}
		var pe *fs.PathError
		if !errors.As(ie.Cause, &pe) || pe.Op != "stat" {
			t.Fatalf("Cause = %T %v, want the *fs.PathError of os.Stat", ie.Cause, ie.Cause)
		}
		if errors.Is(ie.Cause, os.ErrNotExist) {
			t.Errorf("Cause %v matches os.ErrNotExist; the test needs the other stat branch", ie.Cause)
		}
		if want := "setup_write_sections: read " + path + ": " + ie.Cause.Error(); ie.Msg != want {
			t.Errorf("Msg =\n%s\nwant\n%s", ie.Msg, want)
		}
		if want := "Check that " + path + " is a readable file, then retry. Nothing was written."; ie.Suggestion != want {
			t.Errorf("Suggestion = %q, want %q", ie.Suggestion, want)
		}
	})

	t.Run("read-only reader has no tail", func(t *testing.T) {
		path := filepath.Join(dir, "bad-state.toml")
		if err := os.WriteFile(path, []byte("[ship\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := readCheckedTOML(path, setupStateReader)
		var de *mcpserver.DataError
		if !errors.As(err, &de) {
			t.Fatalf("err = %T %v, want *mcpserver.DataError", err, err)
		}
		if want := fmt.Sprintf("dashboard setup: %s is not valid TOML: %s", path, de.Cause.Error()); de.Msg != want {
			t.Errorf("Msg =\n%s\nwant\n%s", de.Msg, want)
		}
		if want := "Fix the TOML syntax of " + path + " by hand, then retry."; de.Suggestion != want {
			t.Errorf("Suggestion = %q, want %q", de.Suggestion, want)
		}
	})
}

// TestSetupQuote covers the cut of a long text at maxSetupQuoteBytes: no cut
// at the limit, a cut one byte over it, a cut that would end inside a
// multi-byte rune, and text that is not valid UTF-8.
func TestSetupQuote(t *testing.T) {
	a := func(n int) string { return strings.Repeat("a", n) }
	if maxSetupQuoteBytes != 64 {
		t.Fatalf("maxSetupQuoteBytes = %d; the cases below expect 64", maxSetupQuoteBytes)
	}
	cases := []struct {
		name string
		in   string
		want string
	}{
		{name: "empty", in: "", want: `""`},
		{name: "short", in: "abc", want: `"abc"`},
		{name: "quote and backslash are escaped", in: `a"b\c`, want: `"a\"b\\c"`},
		{name: "exactly 64 bytes", in: a(64), want: `"` + a(64) + `"`},
		{name: "65 bytes", in: a(65), want: `"` + a(64) + `" (cut)`},
		{name: "64 bytes ending in a 2-byte rune", in: a(62) + "é", want: `"` + a(62) + `é"`},
		{name: "cut inside a 2-byte rune", in: a(63) + "é", want: `"` + a(63) + `" (cut)`},
		{name: "cut inside a 3-byte rune", in: a(62) + "€" + a(5), want: `"` + a(62) + `" (cut)`},
		{name: "cut after a whole 3-byte rune", in: a(61) + "€" + a(5), want: `"` + a(61) + `€" (cut)`},
		{name: "short invalid UTF-8", in: "a\xffb", want: `"a\xffb"`},
		{name: "long text with an invalid byte at the end of the cut", in: a(63) + "\xff" + a(5), want: `"` + a(63) + `" (cut)`},
		{name: "long text with an invalid byte early", in: a(10) + "\xff" + a(60), want: `"` + a(10) + `" (cut)`},
		{name: "long text of invalid bytes only", in: strings.Repeat("\xff", 70), want: `"" (cut)`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := setupQuote(tc.in); got != tc.want {
				t.Errorf("setupQuote(%q) =\n%s\nwant\n%s", tc.in, got, tc.want)
			}
		})
	}
}

// TestSetupTypeName pins the type name of each JSON type in a message.
func TestSetupTypeName(t *testing.T) {
	cases := []struct {
		in   any
		want string
	}{
		{"x", "a text"},
		{"", "a text"},
		{true, "a boolean"},
		{float64(3), "a number"},
		{[]any{"x"}, "a list"},
		{map[string]any{"k": "v"}, "a table"},
		{int64(3), "a int64"},
	}
	for _, tc := range cases {
		if got := setupTypeName(tc.in); got != tc.want {
			t.Errorf("setupTypeName(%#v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestSetupValueText pins how a message prints each value, with the exponent
// form for a number of 21 or more digits.
func TestSetupValueText(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want string
	}{
		{name: "text", in: "yes", want: `"yes"`},
		{name: "long text is cut", in: strings.Repeat("b", 65), want: `"` + strings.Repeat("b", 64) + `" (cut)`},
		{name: "true", in: true, want: "true"},
		{name: "false", in: false, want: "false"},
		{name: "zero", in: float64(0), want: "0"},
		{name: "fraction", in: 0.5, want: "0.5"},
		{name: "negative fraction", in: -0.1, want: "-0.1"},
		{name: "one over maxSetupNumber", in: float64(2147483648), want: "2147483648"},
		{name: "20 digits", in: 1e20, want: "100000000000000000000"},
		{name: "21 digits", in: 1e21, want: "1e+21"},
		{name: "1e300", in: 1e300, want: "1e+300"},
		{name: "-1e300", in: -1e300, want: "-1e+300"},
		{name: "list", in: []any{"a", "b"}, want: "a list"},
		{name: "table", in: map[string]any{"k": 1.0}, want: "a table"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := setupValueText(tc.in); got != tc.want {
				t.Errorf("setupValueText(%#v) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestSetupRefusedText pins the count text for 0, 1 and 2 refused values.
func TestSetupRefusedText(t *testing.T) {
	for n, want := range map[int]string{
		0: "0 field values are refused",
		1: "1 field value is refused",
		2: "2 field values are refused",
	} {
		if got := SetupRefusedText(n); got != want {
			t.Errorf("SetupRefusedText(%d) = %q, want %q", n, got, want)
		}
	}
}

// TestSetupFieldErrorsText pins the full text for 0, 1 and 2 refused values.
func TestSetupFieldErrorsText(t *testing.T) {
	cases := []struct {
		name string
		errs []SetupFieldError
		want string
	}{
		{name: "none", errs: nil, want: "0 field values are refused. "},
		{name: "one", errs: []SetupFieldError{
			{Section: "ship", Field: "draft", Path: "ship.draft", Message: `true or false is required, got "yes".`},
		}, want: `1 field value is refused. ship.draft: true or false is required, got "yes".`},
		{name: "two", errs: []SetupFieldError{
			{Section: "review", Field: "scope", Path: "review.scope", Message: `"everything" is not an option. Options: all, committed, staged, working, worktree.`},
			{Section: "ship", Field: "draft", Path: "ship.draft", Message: `true or false is required, got "yes".`},
		}, want: `2 field values are refused. review.scope: "everything" is not an option. Options: all, committed, staged, working, worktree. ship.draft: true or false is required, got "yes".`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := setupFieldErrorsText(tc.errs); got != tc.want {
				t.Errorf("setupFieldErrorsText =\n%q\nwant\n%q", got, tc.want)
			}
		})
	}
}

// TestNormalizeSetupRates covers each form of a rate field value that
// normalizeSetupRates changes to a number, and the values it leaves as they
// are.
func TestNormalizeSetupRates(t *testing.T) {
	cases := []struct {
		name    string
		key     string
		section string // JSON of the section value
		want    string // JSON of the section after the call
	}{
		{name: "nested form", key: "automation",
			section: `{"drift":{"maxErrorRate":"0.3","maxWarningRate":"0.5"}}`,
			want:    `{"drift":{"maxErrorRate":0.3,"maxWarningRate":0.5}}`},
		{name: "dotted form", key: "automation",
			section: `{"drift.maxErrorRate":"0.3","drift.maxWarningRate":"0.4"}`,
			want:    `{"drift.maxErrorRate":0.3,"drift.maxWarningRate":0.4}`},
		{name: "text with spaces", key: "automation",
			section: `{"drift":{"maxErrorRate":" 0.3 "}}`,
			want:    `{"drift":{"maxErrorRate":0.3}}`},
		{name: "key is the drift table", key: "automation.drift",
			section: `{"maxErrorRate":"0.25","maxWarningRate":"1"}`,
			want:    `{"maxErrorRate":0.25,"maxWarningRate":1}`},
		{name: "number stays a number", key: "automation",
			section: `{"drift":{"maxErrorRate":0.3}}`,
			want:    `{"drift":{"maxErrorRate":0.3}}`},
		{name: "text out of range is still converted", key: "automation",
			section: `{"drift":{"maxErrorRate":"1.5"}}`,
			want:    `{"drift":{"maxErrorRate":1.5}}`},
		{name: "text that is not a number stays", key: "automation",
			section: `{"drift":{"maxErrorRate":"banana","maxWarningRate":""}}`,
			want:    `{"drift":{"maxErrorRate":"banana","maxWarningRate":""}}`},
		{name: "infinity and NaN text stay", key: "automation",
			section: `{"drift":{"maxErrorRate":"Inf","maxWarningRate":"NaN"}}`,
			want:    `{"drift":{"maxErrorRate":"Inf","maxWarningRate":"NaN"}}`},
		{name: "other drift fields stay", key: "automation",
			section: `{"drift":{"minErrorFloor":"2","maxErrorRate":"0.1"}}`,
			want:    `{"drift":{"maxErrorRate":0.1,"minErrorFloor":"2"}}`},
		{name: "other section stays", key: "ship",
			section: `{"maxErrorRate":"0.3","drift":{"maxErrorRate":"0.3"}}`,
			want:    `{"drift":{"maxErrorRate":"0.3"},"maxErrorRate":"0.3"}`},
		{name: "drift not a table", key: "automation",
			section: `{"drift":"0.3"}`,
			want:    `{"drift":"0.3"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var section map[string]any
			if err := json.Unmarshal([]byte(tc.section), &section); err != nil {
				t.Fatal(err)
			}
			normalizeSetupRates(tc.key, section)
			got, _ := json.Marshal(section)
			if string(got) != tc.want {
				t.Errorf("normalizeSetupRates(%q, %s) = %s, want %s", tc.key, tc.section, got, tc.want)
			}
		})
	}

	t.Run("nil section", func(t *testing.T) {
		normalizeSetupRates("automation", nil)
		normalizeSetupRates("automation.drift", nil)
	})
}

// TestCheckSetupValues_RateFields covers the value check of the two rate
// fields, after normalizeSetupRates as setupWriteSections runs it.
func TestCheckSetupValues_RateFields(t *testing.T) {
	refused := func(field, got string) []SetupFieldError {
		return []SetupFieldError{{Section: "automation", Field: field, Path: "automation." + field,
			Message: "a number from 0 to 1 is required, got " + got + "."}}
	}
	cases := []struct {
		name string
		json string
		want []SetupFieldError // nil: the check accepts the call
	}{
		{name: "zero", json: `{"automation":{"drift":{"maxErrorRate":0}}}`},
		{name: "one", json: `{"automation":{"drift":{"maxErrorRate":1}}}`},
		{name: "fraction", json: `{"automation":{"drift":{"maxErrorRate":0.15}}}`},
		{name: "decimal text", json: `{"automation":{"drift":{"maxErrorRate":"0.15"}}}`},
		{name: "decimal text with spaces, dotted", json: `{"automation":{"drift.maxWarningRate":" 0.4 "}}`},
		{name: "decimal text, key is the drift table", json: `{"automation.drift":{"maxWarningRate":"0.6"}}`},
		{name: "over one", json: `{"automation":{"drift":{"maxErrorRate":1.5}}}`, want: refused("drift.maxErrorRate", "1.5")},
		{name: "over one as text", json: `{"automation":{"drift":{"maxErrorRate":"1.5"}}}`, want: refused("drift.maxErrorRate", "1.5")},
		{name: "under zero", json: `{"automation":{"drift":{"maxWarningRate":-0.1}}}`, want: refused("drift.maxWarningRate", "-0.1")},
		{name: "text not a number", json: `{"automation":{"drift":{"maxErrorRate":"banana"}}}`, want: refused("drift.maxErrorRate", `"banana"`)},
		{name: "boolean", json: `{"automation":{"drift":{"maxErrorRate":true}}}`, want: refused("drift.maxErrorRate", "true")},
		{name: "list", json: `{"automation":{"drift":{"maxErrorRate":[0.1]}}}`, want: refused("drift.maxErrorRate", "a list")},
		{name: "large number", json: `{"automation":{"drift":{"maxErrorRate":1e300}}}`, want: refused("drift.maxErrorRate", "1e+300")},
		{name: "both refused, sorted by path", json: `{"automation":{"drift":{"maxWarningRate":2,"maxErrorRate":3}}}`, want: []SetupFieldError{
			refused("drift.maxErrorRate", "3")[0], refused("drift.maxWarningRate", "2")[0]}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var sections map[string]map[string]any
			if err := json.Unmarshal([]byte(tc.json), &sections); err != nil {
				t.Fatal(err)
			}
			for key := range sections {
				normalizeSetupRates(key, sections[key])
			}
			got, err := checkSetupValues(sections, func(string) (map[string]any, error) { return map[string]any{}, nil })
			if err != nil {
				t.Fatal(err)
			}
			gotJSON, _ := json.Marshal(got)
			wantJSON, _ := json.Marshal(tc.want)
			if string(gotJSON) != string(wantJSON) {
				t.Errorf("checkSetupValues(%s)\n got: %s\nwant: %s", tc.json, gotJSON, wantJSON)
			}
		})
	}

	t.Run("text without normalizeSetupRates is refused", func(t *testing.T) {
		got, _ := runSetupCheck(t, `{"automation":{"drift":{"maxErrorRate":"0.3"}}}`, nil)
		gotJSON, _ := json.Marshal(got)
		wantJSON, _ := json.Marshal(refused("drift.maxErrorRate", `"0.3"`))
		if string(gotJSON) != string(wantJSON) {
			t.Errorf("got %s, want %s", gotJSON, wantJSON)
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
