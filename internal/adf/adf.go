// Package adf converts a constrained subset of Markdown into Atlassian
// Document Format (ADF) v1 documents. It is a Go port of the reference
// converter at scripts/lib/markdown-to-adf.js in the sdlc-utilities plugin
// and is intentionally frozen at that converter's capability: headings
// (h1-h3), bold, italic, inline code, fenced code blocks, unordered/ordered
// lists, links, tables, blockquotes, and horizontal rules. Markdown
// constructs it does not recognize degrade to plain-text paragraph nodes
// rather than causing an error.
package adf

import (
	"regexp"
	"strings"
)

// node is a JSON-serializable ADF node, keyed by field name.
type node = map[string]any

var (
	// inlineRe tokenizes a line of text into links, inline code, bold,
	// italic, or plain-text runs. Order matters: bold is tried before
	// italic so that "**x**" is not misread as two "*" runs.
	inlineRe = regexp.MustCompile("(\\[([^\\]]+)\\]\\(([^)]+)\\))|(`([^`]+)`)|(\\*\\*(.+?)\\*\\*)|(\\*(.+?)\\*)|([^\\[`*]+)")

	fenceStartRe = regexp.MustCompile("^```(\\w*)\\s*$")
	fenceEndRe   = regexp.MustCompile("^```\\s*$")
	ruleRe       = regexp.MustCompile(`^(---|\*\*\*|___)\s*$`)
	headingRe    = regexp.MustCompile(`^(#{1,3})\s+(.+)$`)
	blockquoteRe = regexp.MustCompile(`^>\s?`)
	bulletRe     = regexp.MustCompile(`^[-*]\s+`)
	orderedRe    = regexp.MustCompile(`^\d+\.\s+`)
	tableLineRe  = regexp.MustCompile(`^\|`)
	separatorRe  = regexp.MustCompile(`^\|?[\s:]*-{2,}[\s:]*(\|[\s:]*-{2,}[\s:]*)*\|?\s*$`)
)

// tokenizeInline parses inline markdown (links, bold, italic, inline code)
// within text into a slice of ADF text nodes. Unrecognized runs pass through
// as plain text nodes; if nothing matches at all, the whole input is
// returned as a single plain-text node.
func tokenizeInline(text string) []node {
	matches := inlineRe.FindAllStringSubmatch(text, -1)
	nodes := make([]node, 0, len(matches))
	for _, m := range matches {
		switch {
		case m[1] != "":
			// Link: [text](url)
			nodes = append(nodes, node{
				"type": "text",
				"text": m[2],
				"marks": []node{
					{"type": "link", "attrs": node{"href": m[3]}},
				},
			})
		case m[4] != "":
			// Inline code: `code`
			nodes = append(nodes, node{
				"type":  "text",
				"text":  m[5],
				"marks": []node{{"type": "code"}},
			})
		case m[6] != "":
			// Bold: **text**
			nodes = append(nodes, node{
				"type":  "text",
				"text":  m[7],
				"marks": []node{{"type": "strong"}},
			})
		case m[8] != "":
			// Italic: *text*
			nodes = append(nodes, node{
				"type":  "text",
				"text":  m[9],
				"marks": []node{{"type": "em"}},
			})
		case m[10] != "":
			// Plain text
			nodes = append(nodes, node{"type": "text", "text": m[10]})
		}
	}
	if len(nodes) == 0 {
		return []node{{"type": "text", "text": text}}
	}
	return nodes
}

// parseTableRow splits a pipe-delimited table line into trimmed cells,
// dropping the empty cell produced by a leading or trailing pipe.
func parseTableRow(line string) []string {
	cells := strings.Split(line, "|")
	for i, c := range cells {
		cells[i] = strings.TrimSpace(c)
	}
	if len(cells) > 0 && cells[0] == "" {
		cells = cells[1:]
	}
	if len(cells) > 0 && cells[len(cells)-1] == "" {
		cells = cells[:len(cells)-1]
	}
	return cells
}

// isSeparatorRow reports whether line is a table header separator such as
// "| --- | --- |" or "|:---:|---:|".
func isSeparatorRow(line string) bool {
	return separatorRe.MatchString(line)
}

// buildTable turns a contiguous run of pipe-delimited lines into an ADF
// table node, treating the first line as the header row and, when present,
// skipping a separator row before the body. It returns nil if there are
// fewer than two lines to build a table from.
func buildTable(tableLines []string) node {
	if len(tableLines) < 2 {
		return nil
	}
	headerCells := parseTableRow(tableLines[0])
	bodyStart := 1
	if isSeparatorRow(tableLines[1]) {
		bodyStart = 2
	}

	rows := make([]node, 0, len(tableLines))

	headerContent := make([]node, 0, len(headerCells))
	for _, cell := range headerCells {
		headerContent = append(headerContent, node{
			"type":  "tableHeader",
			"attrs": node{},
			"content": []node{
				{"type": "paragraph", "content": tokenizeInline(cell)},
			},
		})
	}
	rows = append(rows, node{"type": "tableRow", "content": headerContent})

	for i := bodyStart; i < len(tableLines); i++ {
		cells := parseTableRow(tableLines[i])
		rowContent := make([]node, 0, len(cells))
		for _, cell := range cells {
			rowContent = append(rowContent, node{
				"type":  "tableCell",
				"attrs": node{},
				"content": []node{
					{"type": "paragraph", "content": tokenizeInline(cell)},
				},
			})
		}
		rows = append(rows, node{"type": "tableRow", "content": rowContent})
	}

	return node{
		"type": "table",
		"attrs": node{
			"isNumberColumnEnabled": false,
			"layout":                "default",
		},
		"content": rows,
	}
}

// buildListItem wraps text in an ADF listItem containing a single paragraph.
func buildListItem(text string) node {
	return node{
		"type": "listItem",
		"content": []node{
			{"type": "paragraph", "content": tokenizeInline(text)},
		},
	}
}

// Convert parses a constrained subset of Markdown (headings, paragraphs,
// bullet and ordered lists, fenced code blocks, links, tables, blockquotes,
// horizontal rules, bold/italic/inline-code spans) and returns an Atlassian
// Document Format (ADF) v1 document as a JSON-serializable map. Convert
// never returns a non-nil error: markdown it does not recognize degrades to
// plain-text paragraph nodes rather than failing.
func Convert(md string) (map[string]any, error) {
	lines := strings.Split(md, "\n")
	content := make([]node, 0)

	flushParagraph := func(text string) {
		trimmed := strings.TrimSpace(text)
		if trimmed != "" {
			content = append(content, node{"type": "paragraph", "content": tokenizeInline(trimmed)})
		}
	}

	i := 0
	for i < len(lines) {
		line := lines[i]

		// --- Fenced code block ---
		if fm := fenceStartRe.FindStringSubmatch(line); fm != nil {
			lang := fm[1]
			var codeLines []string
			i++
			for i < len(lines) && !fenceEndRe.MatchString(lines[i]) {
				codeLines = append(codeLines, lines[i])
				i++
			}
			n := node{"type": "codeBlock", "content": []node{{"type": "text", "text": strings.Join(codeLines, "\n")}}}
			if lang != "" {
				n["attrs"] = node{"language": lang}
			}
			content = append(content, n)
			i++ // skip closing ```
			continue
		}

		// --- Horizontal rule ---
		if ruleRe.MatchString(line) {
			content = append(content, node{"type": "rule"})
			i++
			continue
		}

		// --- Heading ---
		if hm := headingRe.FindStringSubmatch(line); hm != nil {
			level := len(hm[1])
			text := strings.TrimSpace(hm[2])
			content = append(content, node{
				"type":    "heading",
				"attrs":   node{"level": level},
				"content": tokenizeInline(text),
			})
			i++
			continue
		}

		// --- Blockquote ---
		if blockquoteRe.MatchString(line) {
			var quoteLines []string
			for i < len(lines) && blockquoteRe.MatchString(lines[i]) {
				quoteLines = append(quoteLines, blockquoteRe.ReplaceAllString(lines[i], ""))
				i++
			}
			content = append(content, node{
				"type": "blockquote",
				"content": []node{
					{"type": "paragraph", "content": tokenizeInline(strings.TrimSpace(strings.Join(quoteLines, " ")))},
				},
			})
			continue
		}

		// --- Unordered list ---
		if bulletRe.MatchString(line) {
			var items []node
			for i < len(lines) && bulletRe.MatchString(lines[i]) {
				items = append(items, buildListItem(bulletRe.ReplaceAllString(lines[i], "")))
				i++
			}
			content = append(content, node{"type": "bulletList", "content": items})
			continue
		}

		// --- Ordered list ---
		if orderedRe.MatchString(line) {
			var items []node
			for i < len(lines) && orderedRe.MatchString(lines[i]) {
				items = append(items, buildListItem(orderedRe.ReplaceAllString(lines[i], "")))
				i++
			}
			content = append(content, node{"type": "orderedList", "content": items})
			continue
		}

		// --- Table ---
		if tableLineRe.MatchString(line) {
			var tableLines []string
			for i < len(lines) && tableLineRe.MatchString(lines[i]) {
				tableLines = append(tableLines, lines[i])
				i++
			}
			if table := buildTable(tableLines); table != nil {
				content = append(content, table)
			}
			continue
		}

		// --- Empty line (skip) ---
		if strings.TrimSpace(line) == "" {
			i++
			continue
		}

		// --- Paragraph (default) ---
		flushParagraph(line)
		i++
	}

	// If the input was empty, produce a minimal doc with one empty paragraph.
	if len(content) == 0 {
		content = append(content, node{"type": "paragraph", "content": []node{{"type": "text", "text": ""}}})
	}

	return node{"version": 1, "type": "doc", "content": content}, nil
}
