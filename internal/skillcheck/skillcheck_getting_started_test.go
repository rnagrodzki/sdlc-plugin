package skillcheck

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/rnagrodzki/sdlc-plugin/internal/setupmeta"
)

const (
	gettingStartedOwnerStart = "<!-- setup-owner-table:start -->"
	gettingStartedOwnerEnd   = "<!-- setup-owner-table:end -->"
	gettingStartedBothOwners = "Setup tab and `/sdlc:setup`"
	gettingStartedSkillOnly  = "`/sdlc:setup` only"
)

var gettingStartedOwnerRowRe = regexp.MustCompile("^\\| `([^`]+)` \\| (.+?) \\|$")

// checkOwnerTable checks guide text against sections and returns one message
// for each problem (empty = pass). It is pure, so sub-tests can feed bad text:
//   - the start and end markers are present, in that order
//   - each id appears exactly once, and no other id appears
//   - owner == "Setup tab and `/sdlc:setup`" when DelegatedTo == "", else "`/sdlc:setup` only"
//   - the sentence "The Setup tab shows the <F> form sections (<N> fields). Only `/sdlc:setup` changes the other <D> sections." uses the computed F, N, D
//
// A message names the id, the marker or the sentence.
func checkOwnerTable(doc string, sections []setupmeta.Section) []string {
	var msgs []string

	formSections, formFields := 0, 0
	for _, s := range sections {
		if s.DelegatedTo == "" {
			formSections++
			formFields += len(s.Fields)
		}
	}
	sentence := fmt.Sprintf("The Setup tab shows the %d form sections (%d fields). Only `/sdlc:setup` changes the other %d sections.",
		formSections, formFields, len(sections)-formSections)
	if !strings.Contains(doc, sentence) {
		msgs = append(msgs, fmt.Sprintf("count sentence is missing or wrong, want: %q", sentence))
	}

	start := strings.Index(doc, gettingStartedOwnerStart)
	end := strings.Index(doc, gettingStartedOwnerEnd)
	if start < 0 {
		msgs = append(msgs, fmt.Sprintf("start marker %s is missing", gettingStartedOwnerStart))
	}
	if end < 0 {
		msgs = append(msgs, fmt.Sprintf("end marker %s is missing", gettingStartedOwnerEnd))
	}
	if start < 0 || end < 0 {
		return msgs
	}
	if end < start {
		return append(msgs, "end marker comes before start marker")
	}

	owners := map[string]string{}
	counts := map[string]int{}
	var order []string
	for _, line := range strings.Split(doc[start:end], "\n") {
		m := gettingStartedOwnerRowRe.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		if counts[m[1]] == 0 {
			order = append(order, m[1])
			owners[m[1]] = m[2]
		}
		counts[m[1]]++
	}

	known := map[string]bool{}
	for _, s := range sections {
		known[s.ID] = true
		switch counts[s.ID] {
		case 0:
			msgs = append(msgs, fmt.Sprintf("section %q has no row in the owner table", s.ID))
			continue
		case 1:
		default:
			msgs = append(msgs, fmt.Sprintf("section %q has %d rows in the owner table, want 1", s.ID, counts[s.ID]))
		}
		want := gettingStartedBothOwners
		if s.DelegatedTo != "" {
			want = gettingStartedSkillOnly
		}
		if owners[s.ID] != want {
			msgs = append(msgs, fmt.Sprintf("section %q owner is %q, want %q", s.ID, owners[s.ID], want))
		}
	}
	for _, id := range order {
		if !known[id] {
			msgs = append(msgs, fmt.Sprintf("owner table row %q is not a section id in setupmeta.Sections()", id))
		}
	}
	return msgs
}

// TestGettingStartedOwnerTableMatchesSections reads docs/getting-started.md
// (same path resolve as skillcheck_openspec_save_test.go) and expects no
// message. Its sub-tests feed bad texts and expect the one message that names
// the cause, and feed layout variants (CRLF, an indented row) and expect no
// message.
func TestGettingStartedOwnerTableMatchesSections(t *testing.T) {
	sections := setupmeta.Sections()
	doc := executeSkillsReadFile(t, filepath.Join(openspecSaveSkillsDocsDir(t), "getting-started.md"))

	if msgs := checkOwnerTable(doc, sections); len(msgs) != 0 {
		t.Fatalf("docs/getting-started.md owner table drifted from setupmeta.Sections():\n  %s", strings.Join(msgs, "\n  "))
	}

	firstID := sections[0].ID
	firstRow := fmt.Sprintf("| `%s` | %s |\n", firstID, gettingStartedBothOwners)
	if sections[0].DelegatedTo != "" {
		firstRow = fmt.Sprintf("| `%s` | %s |\n", firstID, gettingStartedSkillOnly)
	}

	cases := []struct {
		name string
		edit func(string) string
		// want is the one expected message; wantPrefix replaces it when the
		// message carries computed text. Both empty: no message expected.
		want, wantPrefix string
	}{
		{"missing_id", func(d string) string { return strings.Replace(d, firstRow, "", 1) },
			fmt.Sprintf("section %q has no row in the owner table", firstID), ""},
		{"duplicate_id", func(d string) string { return strings.Replace(d, firstRow, firstRow+firstRow, 1) },
			fmt.Sprintf("section %q has 2 rows in the owner table, want 1", firstID), ""},
		{"triplicate_id", func(d string) string { return strings.Replace(d, firstRow, firstRow+firstRow+firstRow, 1) },
			fmt.Sprintf("section %q has 3 rows in the owner table, want 1", firstID), ""},
		{"unknown_id", func(d string) string {
			return strings.Replace(d, gettingStartedOwnerEnd, "| `not-a-section` | "+gettingStartedBothOwners+" |\n\n"+gettingStartedOwnerEnd, 1)
		}, `owner table row "not-a-section" is not a section id in setupmeta.Sections()`, ""},
		{"wrong_owner", func(d string) string {
			return strings.Replace(d, "| `commit` | "+gettingStartedSkillOnly+" |", "| `commit` | "+gettingStartedBothOwners+" |", 1)
		}, fmt.Sprintf("section %q owner is %q, want %q", "commit", gettingStartedBothOwners, gettingStartedSkillOnly), ""},
		{"wrong_count_sentence", func(d string) string {
			return strings.Replace(d, " form sections (", " form sections (999 more than ", 1)
		}, "", "count sentence is missing or wrong, want: "},
		{"missing_marker", func(d string) string { return strings.Replace(d, gettingStartedOwnerStart, "", 1) },
			"start marker " + gettingStartedOwnerStart + " is missing", ""},
		{"missing_end_marker", func(d string) string { return strings.Replace(d, gettingStartedOwnerEnd, "", 1) },
			"end marker " + gettingStartedOwnerEnd + " is missing", ""},
		{"reversed_markers", func(d string) string {
			d = strings.Replace(d, gettingStartedOwnerStart, "\x00", 1)
			d = strings.Replace(d, gettingStartedOwnerEnd, gettingStartedOwnerStart, 1)
			return strings.Replace(d, "\x00", gettingStartedOwnerEnd, 1)
		}, "end marker comes before start marker", ""},
		{"crlf", func(d string) string { return strings.ReplaceAll(d, "\n", "\r\n") }, "", ""},
		{"indented_row", func(d string) string { return strings.Replace(d, firstRow, "  "+firstRow, 1) }, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bad := tc.edit(doc)
			if bad == doc {
				t.Fatal("the edit did not change the text: the sub-test is stale")
			}
			msgs := checkOwnerTable(bad, sections)
			if tc.want == "" && tc.wantPrefix == "" {
				if len(msgs) != 0 {
					t.Errorf("got %d messages, want 0: %q", len(msgs), msgs)
				}
				return
			}
			if len(msgs) != 1 {
				t.Fatalf("got %d messages, want 1: %q", len(msgs), msgs)
			}
			if tc.want != "" && msgs[0] != tc.want {
				t.Errorf("message = %q, want %q", msgs[0], tc.want)
			}
			if tc.wantPrefix != "" && !strings.HasPrefix(msgs[0], tc.wantPrefix) {
				t.Errorf("message = %q, want prefix %q", msgs[0], tc.wantPrefix)
			}
		})
	}
}
