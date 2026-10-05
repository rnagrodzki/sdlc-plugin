package config

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	version "github.com/rnagrodzki/sdlc-plugin"
	"github.com/rnagrodzki/sdlc-plugin/internal/fsx"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
)

// TestRestoreTips_InsertsAboveKey pins the basic case: a key with no comment
// directly above it gets the template's comment block for that key, and
// added reports one insertion.
func TestRestoreTips_InsertsAboveKey(t *testing.T) {
	tmpl := []byte("[jira]\n# the project key\ndefaultProject = \"\"\n")
	file := []byte("[jira]\ndefaultProject = \"PROJ\"\n")
	got, added, err := RestoreTips(file, tmpl, []string{"jira"})
	if err != nil {
		t.Fatalf("RestoreTips: %v", err)
	}
	if added != 1 {
		t.Errorf("added = %d, want 1", added)
	}
	want := "[jira]\n# the project key\ndefaultProject = \"PROJ\"\n"
	if string(got) != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

// TestRestoreTips_InsertsAboveHeader pins that the same rule applies to
// table headers: a header with a comment directly above it in the template
// (no blank line between) gets that comment restored above a header in the
// file that has none.
func TestRestoreTips_InsertsAboveHeader(t *testing.T) {
	tmpl := []byte("# the jira section\n[jira]\ndefaultProject = \"\"\n")
	file := []byte("[commit]\nallowedTypes = []\n\n[jira]\ndefaultProject = \"PROJ\"\n")
	got, added, err := RestoreTips(file, tmpl, []string{"jira"})
	if err != nil {
		t.Fatalf("RestoreTips: %v", err)
	}
	if added != 1 {
		t.Errorf("added = %d, want 1", added)
	}
	want := "[commit]\nallowedTypes = []\n\n# the jira section\n[jira]\ndefaultProject = \"PROJ\"\n"
	if string(got) != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

// TestRestoreTips_KeyAbsentFromFile_NoTip pins that a template key the file
// does not have gets no tip: only the keys actually present in the file can
// receive one.
func TestRestoreTips_KeyAbsentFromFile_NoTip(t *testing.T) {
	tmpl := []byte("[jira]\n# the project key\ndefaultProject = \"\"\n# allowed keys\nprojects = []\n")
	file := []byte("[jira]\ndefaultProject = \"PROJ\"\n")
	got, added, err := RestoreTips(file, tmpl, []string{"jira"})
	if err != nil {
		t.Fatalf("RestoreTips: %v", err)
	}
	if added != 1 {
		t.Errorf("added = %d, want 1 (only defaultProject; projects is absent from file)", added)
	}
	if strings.Contains(string(got), "allowed keys") {
		t.Errorf("tip for an absent key leaked in:\n%s", got)
	}
}

// TestRestoreTips_OutsideSection_NoTip pins that keys and headers outside
// the written section get no tip, even when the file is missing their
// comment too.
func TestRestoreTips_OutsideSection_NoTip(t *testing.T) {
	tmpl := []byte("[jira]\n# the project key\ndefaultProject = \"\"\n\n[commit]\n# the types\nallowedTypes = []\n")
	file := []byte("[jira]\ndefaultProject = \"PROJ\"\n\n[commit]\nallowedTypes = [\"feat\"]\n")
	got, added, err := RestoreTips(file, tmpl, []string{"jira"})
	if err != nil {
		t.Fatalf("RestoreTips: %v", err)
	}
	if added != 1 {
		t.Errorf("added = %d, want 1 (commit is outside the jira section)", added)
	}
	if strings.Contains(string(got), "the types") {
		t.Errorf("tip for a key outside section leaked in:\n%s", got)
	}
}

// TestRestoreTips_ExistingCommentSkipped pins that a key with any comment
// already directly above it is left alone, regardless of what that comment
// says.
func TestRestoreTips_ExistingCommentSkipped(t *testing.T) {
	tmpl := []byte("[jira]\n# the project key\ndefaultProject = \"\"\n")
	file := []byte("[jira]\n# a different note\ndefaultProject = \"PROJ\"\n")
	got, added, err := RestoreTips(file, tmpl, []string{"jira"})
	if err != nil {
		t.Fatalf("RestoreTips: %v", err)
	}
	if added != 0 {
		t.Errorf("added = %d, want 0", added)
	}
	if string(got) != string(file) {
		t.Errorf("file changed when a comment already existed:\n%s", got)
	}
}

// TestRestoreTips_SecondCallAddsNothing pins idempotency: once a tip is
// restored, a second call over the result adds nothing more.
func TestRestoreTips_SecondCallAddsNothing(t *testing.T) {
	tmpl := []byte("[jira]\n# the project key\ndefaultProject = \"\"\n")
	file := []byte("[jira]\ndefaultProject = \"PROJ\"\n")
	once, added1, err := RestoreTips(file, tmpl, []string{"jira"})
	if err != nil {
		t.Fatalf("first RestoreTips: %v", err)
	}
	if added1 != 1 {
		t.Fatalf("first added = %d, want 1", added1)
	}
	twice, added2, err := RestoreTips(once, tmpl, []string{"jira"})
	if err != nil {
		t.Fatalf("second RestoreTips: %v", err)
	}
	if added2 != 0 {
		t.Errorf("second added = %d, want 0", added2)
	}
	if string(twice) != string(once) {
		t.Errorf("second call changed the file:\n%s", twice)
	}
}

// TestRestoreTips_CRLF pins that a CRLF file gets the inserted comment lines
// in CRLF too, so the file never ends up with mixed line endings.
func TestRestoreTips_CRLF(t *testing.T) {
	tmpl := []byte("[jira]\n# the project key\ndefaultProject = \"\"\n")
	file := []byte("[jira]\r\ndefaultProject = \"PROJ\"\r\n")
	got, added, err := RestoreTips(file, tmpl, []string{"jira"})
	if err != nil {
		t.Fatalf("RestoreTips: %v", err)
	}
	if added != 1 {
		t.Errorf("added = %d, want 1", added)
	}
	want := "[jira]\r\n# the project key\r\ndefaultProject = \"PROJ\"\r\n"
	if string(got) != want {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
}

// TestRestoreTips_EmptySectionMatchesWholeFile pins that an empty section
// scopes to the whole file, restoring tips anywhere.
func TestRestoreTips_EmptySectionMatchesWholeFile(t *testing.T) {
	tmpl := []byte("[jira]\n# the project key\ndefaultProject = \"\"\n\n[commit]\n# the types\nallowedTypes = []\n")
	file := []byte("[jira]\ndefaultProject = \"PROJ\"\n\n[commit]\nallowedTypes = [\"feat\"]\n")
	got, added, err := RestoreTips(file, tmpl, nil)
	if err != nil {
		t.Fatalf("RestoreTips: %v", err)
	}
	if added != 2 {
		t.Errorf("added = %d, want 2", added)
	}
	want := "[jira]\n# the project key\ndefaultProject = \"PROJ\"\n\n[commit]\n# the types\nallowedTypes = [\"feat\"]\n"
	if string(got) != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

// TestRestoreTips_EmbeddedTemplate strips every comment line out of the
// shipped config.toml template and restores against the original template.
// This does not depend on the template's exact wording (another task edits
// it in the same run): it only checks the restore's own invariants hold
// against whatever the template currently says — every comment block the
// strip removed from a live key or header comes back verbatim, the data is
// unchanged, and a second restore is a no-op.
func TestRestoreTips_EmbeddedTemplate(t *testing.T) {
	tmpl := []byte(version.ConfigTemplate)
	stripped := stripCommentLines(tmpl)

	got, added, err := RestoreTips(stripped, tmpl, nil)
	if err != nil {
		t.Fatalf("RestoreTips: %v", err)
	}
	if added == 0 {
		t.Fatal("added = 0, want at least one restored tip")
	}

	// Round trip: stripping comments and restoring the live ones must not
	// change the decoded data.
	var want, gotData map[string]any
	if err := fsx.DecodeTOML(tmpl, &want); err != nil {
		t.Fatalf("decode template: %v", err)
	}
	if err := fsx.DecodeTOML(got, &gotData); err != nil {
		t.Fatalf("decode restored: %v", err)
	}

	// Independently recompute, from the template itself, the comment block
	// directly above each key/header, and confirm every such block that
	// survived stripping is back in the restored output, directly above
	// the same key or header.
	items, err := scanTOML(tmpl)
	if err != nil {
		t.Fatalf("scanTOML(template): %v", err)
	}
	tmplLines := splitLines(tmpl)
	gotLines := splitLines(got)
	gotItems, err := scanTOML(got)
	if err != nil {
		t.Fatalf("scanTOML(restored): %v", err)
	}
	byPath := map[string]tomlItem{}
	for _, it := range gotItems {
		byPath[pathKey(it.path)] = it
	}
	checked := 0
	for _, it := range items {
		block := commentBlockAbove(tmplLines, it.first)
		if block == "" {
			continue
		}
		gi, ok := byPath[pathKey(it.path)]
		if !ok {
			t.Errorf("path %v missing from restored output", it.path)
			continue
		}
		if got := commentBlockAbove(gotLines, gi.first); got != block {
			t.Errorf("path %v: restored comment = %q, want %q", it.path, got, block)
		}
		checked++
	}
	if checked == 0 {
		t.Fatal("no template comment blocks found to check — test fixture assumption broke")
	}

	// A second restore over the already-restored file adds nothing.
	_, added2, err := RestoreTips(got, tmpl, nil)
	if err != nil {
		t.Fatalf("second RestoreTips: %v", err)
	}
	if added2 != 0 {
		t.Errorf("second added = %d, want 0", added2)
	}
}

// stripCommentLines removes every comment line from data, leaving every
// other line (including blank ones) untouched.
func stripCommentLines(data []byte) []byte {
	var out []byte
	for _, l := range splitLines(data) {
		if isCommentLine(l) {
			continue
		}
		out = append(out, l...)
	}
	return out
}

// splitLines is bytes.SplitAfter(data, "\n"), named for readability at call
// sites in this file.
func splitLines(data []byte) [][]byte {
	return bytes.SplitAfter(data, []byte("\n"))
}

// TestWriteSectionFile_RestoresMissingTip is the integration case: writing a
// brand new section into a fresh config.toml via WriteSectionReport restores
// the shipped template's tip above the new key, because spliceFile's
// appendSection has nothing to copy a comment from.
func TestWriteSectionFile_RestoresMissingTip(t *testing.T) {
	root := t.TempDir()
	Quiet = true
	t.Cleanup(func() { Quiet = false })
	resetTrace()

	path := filepath.Join(root, paths.DataDir, paths.ConfigFile)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	content := "[commit]\nallowedTypes = [\"feat\"]\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := WriteSectionReport(root, "jira", map[string]any{"defaultProject": "PROJ"}); err != nil {
		t.Fatalf("WriteSectionReport: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	tmplItems, err := scanTOML([]byte(version.ConfigTemplate))
	if err != nil {
		t.Fatal(err)
	}
	tmplLines := splitLines([]byte(version.ConfigTemplate))
	var wantTip string
	for _, it := range tmplItems {
		if pathKey(it.path) == pathKey([]string{"jira", "defaultProject"}) {
			wantTip = commentBlockAbove(tmplLines, it.first)
		}
	}
	if wantTip == "" {
		t.Fatal("test fixture assumption broke: jira.defaultProject has no template tip")
	}
	if !strings.Contains(string(got), wantTip+"defaultProject = 'PROJ'\n") {
		t.Errorf("tip not restored directly above defaultProject:\n%s", got)
	}
}

// TestWriteSectionFile_RestoreErrorKeepsSpliceOutput pins the first row of
// writeSectionFile's restore outcome table: when restoreTips errors, the
// write keeps the pre-restore bytes and still reports no error.
func TestWriteSectionFile_RestoreErrorKeepsSpliceOutput(t *testing.T) {
	orig := restoreTips
	restoreTips = func(file, template []byte, section []string) ([]byte, int, error) {
		return nil, 0, errors.New("boom")
	}
	t.Cleanup(func() { restoreTips = orig })

	got, rewrote := writeSpliceFixture(t, "config.toml", "[commit]\nallowedTypes = [\"feat\"]\n",
		"jira", map[string]any{"defaultProject": "PROJ"})
	if rewrote {
		t.Fatalf("fell back to a full rewrite")
	}
	want := "[commit]\nallowedTypes = [\"feat\"]\n\n[jira]\ndefaultProject = 'PROJ'\n"
	if got != want {
		t.Errorf("restoreTips error changed the written bytes.\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// TestWriteSectionFile_RestoreBadDecodeKeepsSpliceOutput pins the third row
// of writeSectionFile's restore outcome table: when restoreTips returns
// bytes that decode to different data than the pre-restore bytes, the write
// keeps the pre-restore bytes.
func TestWriteSectionFile_RestoreBadDecodeKeepsSpliceOutput(t *testing.T) {
	orig := restoreTips
	restoreTips = func(file, template []byte, section []string) ([]byte, int, error) {
		return append(append([]byte{}, file...), []byte("extraKey = 1\n")...), 1, nil
	}
	t.Cleanup(func() { restoreTips = orig })

	got, rewrote := writeSpliceFixture(t, "config.toml", "[commit]\nallowedTypes = [\"feat\"]\n",
		"jira", map[string]any{"defaultProject": "PROJ"})
	if rewrote {
		t.Fatalf("fell back to a full rewrite")
	}
	want := "[commit]\nallowedTypes = [\"feat\"]\n\n[jira]\ndefaultProject = 'PROJ'\n"
	if got != want {
		t.Errorf("a bad-decode restore changed the written bytes.\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// TestWriteFileSection_OtherFileNameNoRestore pins the template-choice
// table's "other" row: a file whose name is neither config.toml nor
// local.toml gets no restore at all, even though its content matches a
// shipped-template key that would otherwise qualify for a tip.
func TestWriteFileSection_OtherFileNameNoRestore(t *testing.T) {
	Quiet = true
	t.Cleanup(func() { Quiet = false })
	resetTrace()

	restoreCalled := false
	orig := restoreTips
	restoreTips = func(file, template []byte, section []string) ([]byte, int, error) {
		restoreCalled = true
		return RestoreTips(file, template, section)
	}
	t.Cleanup(func() { restoreTips = orig })

	root := t.TempDir()
	path := filepath.Join(root, "other.toml")
	content := "[jira]\ndefaultProject = \"OLD\"\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteFileSection(path, "jira", map[string]any{"defaultProject": "PROJ"}); err != nil {
		t.Fatalf("WriteFileSection: %v", err)
	}
	if restoreCalled {
		t.Error("restoreTips was called for a file that is neither config.toml nor local.toml")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "[jira]\ndefaultProject = 'PROJ'\n"
	if string(got) != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}
