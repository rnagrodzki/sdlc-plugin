package adf

import (
	"encoding/json"
	"reflect"
	"testing"
)

// normalize round-trips v through JSON marshal/unmarshal into interface{} so
// that structural comparisons via reflect.DeepEqual are unaffected by Go map
// key ordering or by numeric type differences (e.g. int vs float64) between
// a freshly built node and a hand-written golden JSON literal.
func normalize(t *testing.T, v any) any {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	return out
}

// TestConvert_GoldenCorpus checks Convert's output against ADF JSON produced
// by the reference converter (scripts/lib/markdown-to-adf.js in the
// sdlc-utilities plugin) for each markdown construct it supports. Comparison
// is structural (via normalize), not a raw byte comparison, since Convert
// returns a map[string]any and Go's encoding/json does not preserve
// insertion order the way the source's JSON.stringify does.
func TestConvert_GoldenCorpus(t *testing.T) {
	tests := []struct {
		name   string
		md     string
		golden string
	}{
		{
			name: "headings",
			md:   "# Heading 1\n\n## Heading 2\n\n### Heading 3",
			golden: `{
				"version": 1,
				"type": "doc",
				"content": [
					{"type": "heading", "attrs": {"level": 1}, "content": [{"type": "text", "text": "Heading 1"}]},
					{"type": "heading", "attrs": {"level": 2}, "content": [{"type": "text", "text": "Heading 2"}]},
					{"type": "heading", "attrs": {"level": 3}, "content": [{"type": "text", "text": "Heading 3"}]}
				]
			}`,
		},
		{
			name: "unordered_list",
			md:   "- First item\n- Second item\n- Third item",
			golden: `{
				"version": 1,
				"type": "doc",
				"content": [
					{"type": "bulletList", "content": [
						{"type": "listItem", "content": [{"type": "paragraph", "content": [{"type": "text", "text": "First item"}]}]},
						{"type": "listItem", "content": [{"type": "paragraph", "content": [{"type": "text", "text": "Second item"}]}]},
						{"type": "listItem", "content": [{"type": "paragraph", "content": [{"type": "text", "text": "Third item"}]}]}
					]}
				]
			}`,
		},
		{
			name: "ordered_list",
			md:   "1. First item\n2. Second item\n3. Third item",
			golden: `{
				"version": 1,
				"type": "doc",
				"content": [
					{"type": "orderedList", "content": [
						{"type": "listItem", "content": [{"type": "paragraph", "content": [{"type": "text", "text": "First item"}]}]},
						{"type": "listItem", "content": [{"type": "paragraph", "content": [{"type": "text", "text": "Second item"}]}]},
						{"type": "listItem", "content": [{"type": "paragraph", "content": [{"type": "text", "text": "Third item"}]}]}
					]}
				]
			}`,
		},
		{
			name: "fenced_code",
			md:   "```javascript\nconst x = 1;\nconsole.log(x);\n```",
			golden: `{
				"version": 1,
				"type": "doc",
				"content": [
					{"type": "codeBlock", "content": [{"type": "text", "text": "const x = 1;\nconsole.log(x);"}], "attrs": {"language": "javascript"}}
				]
			}`,
		},
		{
			name: "code_with_markdown_degrades_inside_fence",
			md:   "```\n# Not a heading\n**Not bold**\n- Not a list\n> Not a blockquote\n```",
			golden: `{
				"version": 1,
				"type": "doc",
				"content": [
					{"type": "codeBlock", "content": [{"type": "text", "text": "# Not a heading\n**Not bold**\n- Not a list\n> Not a blockquote"}]}
				]
			}`,
		},
		{
			name: "link",
			md:   "Visit [Atlassian](https://atlassian.com) for more info.",
			golden: `{
				"version": 1,
				"type": "doc",
				"content": [
					{"type": "paragraph", "content": [
						{"type": "text", "text": "Visit "},
						{"type": "text", "text": "Atlassian", "marks": [{"type": "link", "attrs": {"href": "https://atlassian.com"}}]},
						{"type": "text", "text": " for more info."}
					]}
				]
			}`,
		},
		{
			name: "bold_italic",
			md:   "This has **bold text** and *italic text* together.",
			golden: `{
				"version": 1,
				"type": "doc",
				"content": [
					{"type": "paragraph", "content": [
						{"type": "text", "text": "This has "},
						{"type": "text", "text": "bold text", "marks": [{"type": "strong"}]},
						{"type": "text", "text": " and "},
						{"type": "text", "text": "italic text", "marks": [{"type": "em"}]},
						{"type": "text", "text": " together."}
					]}
				]
			}`,
		},
		{
			name: "inline_code",
			md:   "Use the `convert()` function to transform markdown.",
			golden: `{
				"version": 1,
				"type": "doc",
				"content": [
					{"type": "paragraph", "content": [
						{"type": "text", "text": "Use the "},
						{"type": "text", "text": "convert()", "marks": [{"type": "code"}]},
						{"type": "text", "text": " function to transform markdown."}
					]}
				]
			}`,
		},
		{
			name: "blockquote",
			md:   "> This is a blockquote with important information.",
			golden: `{
				"version": 1,
				"type": "doc",
				"content": [
					{"type": "blockquote", "content": [{"type": "paragraph", "content": [{"type": "text", "text": "This is a blockquote with important information."}]}]}
				]
			}`,
		},
		{
			name: "horizontal_rule",
			md:   "Above the rule\n\n---\n\nBelow the rule",
			golden: `{
				"version": 1,
				"type": "doc",
				"content": [
					{"type": "paragraph", "content": [{"type": "text", "text": "Above the rule"}]},
					{"type": "rule"},
					{"type": "paragraph", "content": [{"type": "text", "text": "Below the rule"}]}
				]
			}`,
		},
		{
			name: "table",
			md:   "| Name | Status | Priority |\n| --- | --- | --- |\n| Bug fix | Done | High |\n| Feature | In Progress | Medium |",
			golden: `{
				"version": 1,
				"type": "doc",
				"content": [
					{"type": "table", "attrs": {"isNumberColumnEnabled": false, "layout": "default"}, "content": [
						{"type": "tableRow", "content": [
							{"type": "tableHeader", "attrs": {}, "content": [{"type": "paragraph", "content": [{"type": "text", "text": "Name"}]}]},
							{"type": "tableHeader", "attrs": {}, "content": [{"type": "paragraph", "content": [{"type": "text", "text": "Status"}]}]},
							{"type": "tableHeader", "attrs": {}, "content": [{"type": "paragraph", "content": [{"type": "text", "text": "Priority"}]}]}
						]},
						{"type": "tableRow", "content": [
							{"type": "tableCell", "attrs": {}, "content": [{"type": "paragraph", "content": [{"type": "text", "text": "Bug fix"}]}]},
							{"type": "tableCell", "attrs": {}, "content": [{"type": "paragraph", "content": [{"type": "text", "text": "Done"}]}]},
							{"type": "tableCell", "attrs": {}, "content": [{"type": "paragraph", "content": [{"type": "text", "text": "High"}]}]}
						]},
						{"type": "tableRow", "content": [
							{"type": "tableCell", "attrs": {}, "content": [{"type": "paragraph", "content": [{"type": "text", "text": "Feature"}]}]},
							{"type": "tableCell", "attrs": {}, "content": [{"type": "paragraph", "content": [{"type": "text", "text": "In Progress"}]}]},
							{"type": "tableCell", "attrs": {}, "content": [{"type": "paragraph", "content": [{"type": "text", "text": "Medium"}]}]}
						]}
					]}
				]
			}`,
		},
		{
			name: "simple_paragraph",
			md:   "This is a simple paragraph with plain text.",
			golden: `{
				"version": 1,
				"type": "doc",
				"content": [
					{"type": "paragraph", "content": [{"type": "text", "text": "This is a simple paragraph with plain text."}]}
				]
			}`,
		},
		{
			name: "empty_input_produces_empty_paragraph",
			md:   "",
			golden: `{
				"version": 1,
				"type": "doc",
				"content": [
					{"type": "paragraph", "content": [{"type": "text", "text": ""}]}
				]
			}`,
		},
		{
			name: "mixed_realistic",
			md: "## Test Results Summary\n\n" +
				"All tests passed successfully. Key findings:\n\n" +
				"- Unit tests: **42 passed**, 0 failed\n" +
				"- Integration tests: **12 passed**, 0 failed\n\n" +
				"```bash\nnpm test -- --coverage\n```\n\n" +
				"| Suite | Pass | Fail |\n| --- | --- | --- |\n| Unit | 42 | 0 |\n| Integration | 12 | 0 |",
			golden: `{
				"version": 1,
				"type": "doc",
				"content": [
					{"type": "heading", "attrs": {"level": 2}, "content": [{"type": "text", "text": "Test Results Summary"}]},
					{"type": "paragraph", "content": [{"type": "text", "text": "All tests passed successfully. Key findings:"}]},
					{"type": "bulletList", "content": [
						{"type": "listItem", "content": [{"type": "paragraph", "content": [
							{"type": "text", "text": "Unit tests: "},
							{"type": "text", "text": "42 passed", "marks": [{"type": "strong"}]},
							{"type": "text", "text": ", 0 failed"}
						]}]},
						{"type": "listItem", "content": [{"type": "paragraph", "content": [
							{"type": "text", "text": "Integration tests: "},
							{"type": "text", "text": "12 passed", "marks": [{"type": "strong"}]},
							{"type": "text", "text": ", 0 failed"}
						]}]}
					]},
					{"type": "codeBlock", "content": [{"type": "text", "text": "npm test -- --coverage"}], "attrs": {"language": "bash"}},
					{"type": "table", "attrs": {"isNumberColumnEnabled": false, "layout": "default"}, "content": [
						{"type": "tableRow", "content": [
							{"type": "tableHeader", "attrs": {}, "content": [{"type": "paragraph", "content": [{"type": "text", "text": "Suite"}]}]},
							{"type": "tableHeader", "attrs": {}, "content": [{"type": "paragraph", "content": [{"type": "text", "text": "Pass"}]}]},
							{"type": "tableHeader", "attrs": {}, "content": [{"type": "paragraph", "content": [{"type": "text", "text": "Fail"}]}]}
						]},
						{"type": "tableRow", "content": [
							{"type": "tableCell", "attrs": {}, "content": [{"type": "paragraph", "content": [{"type": "text", "text": "Unit"}]}]},
							{"type": "tableCell", "attrs": {}, "content": [{"type": "paragraph", "content": [{"type": "text", "text": "42"}]}]},
							{"type": "tableCell", "attrs": {}, "content": [{"type": "paragraph", "content": [{"type": "text", "text": "0"}]}]}
						]},
						{"type": "tableRow", "content": [
							{"type": "tableCell", "attrs": {}, "content": [{"type": "paragraph", "content": [{"type": "text", "text": "Integration"}]}]},
							{"type": "tableCell", "attrs": {}, "content": [{"type": "paragraph", "content": [{"type": "text", "text": "12"}]}]},
							{"type": "tableCell", "attrs": {}, "content": [{"type": "paragraph", "content": [{"type": "text", "text": "0"}]}]}
						]}
					]}
				]
			}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Convert(tc.md)
			if err != nil {
				t.Fatalf("Convert: unexpected error: %v", err)
			}

			var want any
			if err := json.Unmarshal([]byte(tc.golden), &want); err != nil {
				t.Fatalf("invalid golden JSON: %v", err)
			}

			if gotNorm, wantNorm := normalize(t, got), want; !reflect.DeepEqual(gotNorm, wantNorm) {
				gotJSON, _ := json.MarshalIndent(gotNorm, "", "  ")
				wantJSON, _ := json.MarshalIndent(wantNorm, "", "  ")
				t.Errorf("Convert(%q) mismatch:\ngot:\n%s\nwant:\n%s", tc.md, gotJSON, wantJSON)
			}
		})
	}
}

// TestConvert_UnsupportedMarkdownDegradesToPlainText checks that markdown
// constructs Convert does not recognize (strikethrough, raw HTML) never
// produce an error and instead pass through as plain paragraph text.
func TestConvert_UnsupportedMarkdownDegradesToPlainText(t *testing.T) {
	md := "Some ~~strikethrough~~ and a <div>html</div> tag."

	doc, err := Convert(md)
	if err != nil {
		t.Fatalf("Convert: unexpected error: %v", err)
	}

	want := map[string]any{
		"version": 1,
		"type":    "doc",
		"content": []any{
			map[string]any{
				"type": "paragraph",
				"content": []any{
					map[string]any{"type": "text", "text": "Some ~~strikethrough~~ and a <div>html</div> tag."},
				},
			},
		},
	}

	if !reflect.DeepEqual(normalize(t, doc), normalize(t, want)) {
		gotJSON, _ := json.MarshalIndent(doc, "", "  ")
		t.Errorf("Convert(%q) did not degrade to plain text, got:\n%s", md, gotJSON)
	}
}

// TestConvert_NeverErrors sanity-checks a handful of malformed or edge-case
// inputs to confirm Convert's error return is always nil, per its contract.
func TestConvert_NeverErrors(t *testing.T) {
	inputs := []string{
		"",
		"\n\n\n",
		"```unterminated fence\nno closing fence here",
		"| broken | table",
		">",
		"# ",
		"random \x00 control byte",
	}
	for _, in := range inputs {
		if _, err := Convert(in); err != nil {
			t.Errorf("Convert(%q): expected nil error, got %v", in, err)
		}
	}
}
