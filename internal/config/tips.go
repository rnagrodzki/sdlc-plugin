// tips.go: restoring template tip comments after a key-level splice write.
//
// WriteSection's key-level splice (see splice.go) and its full-rewrite
// fallback both touch only the keys of the section being written. Neither
// one can add a comment that was never in the file: a key spliced in for
// the first time, or copied in by a full rewrite, lands with no tip at all,
// even when the shipped template documents that key right above it.
// RestoreTips copies that documentation back in, one comment block per key
// or table header the file is missing it for.

package config

import (
	"bytes"
	"strings"
)

// RestoreTips returns file with each comment block of template inserted
// again directly above the matching key or table header when file has no
// comment line directly above it. A comment block is the run of '#' lines
// directly above a line in template, with no blank line between. added
// counts the inserted blocks. Paths are matched with scanTOML on both
// inputs. Only keys and headers at or under section get a tip; an empty
// section matches the whole file.
func RestoreTips(file, template []byte, section []string) (out []byte, added int, err error) {
	fileItems, err := scanTOML(file)
	if err != nil {
		return nil, 0, err
	}
	tmplItems, err := scanTOML(template)
	if err != nil {
		return nil, 0, err
	}

	tips := templateTips(template, tmplItems, section)
	if len(tips) == 0 {
		return file, 0, nil
	}

	eol := "\n"
	if usesCRLF(file) {
		eol = "\r\n"
	}

	lines := bytes.SplitAfter(file, []byte("\n"))
	insert := make(map[int]string, len(tips))
	for _, it := range fileItems {
		if !hasPathPrefix(it.path, section) {
			continue
		}
		tip, ok := tips[pathKey(it.path)]
		if !ok || hasCommentAbove(lines, it.first) {
			continue
		}
		insert[it.first] = tip
	}
	if len(insert) == 0 {
		return file, 0, nil
	}

	var buf bytes.Buffer
	for i, l := range lines {
		if tip, ok := insert[i]; ok {
			buf.WriteString(crlfText(tip, eol))
		}
		buf.Write(l)
	}
	return buf.Bytes(), len(insert), nil
}

// restoreTips is the seam writeSectionFile calls through. Tests replace it
// to simulate a restore failure or a bad decode, then restore the original
// with t.Cleanup.
var restoreTips = RestoreTips

// templateTips returns, for each item in items at or under section, the
// comment block directly above it in template, keyed by its path. An item
// whose immediately preceding line is not a comment line is left out.
//
// template also documents keys that are never live there: an optional
// setting shipped commented out, such as "# secretName = \"RELEASE_TOKEN\""
// under a commented "# [version.pushAuth]" header. items (from scanTOML)
// never includes those — they are comment text, not TOML — so they are
// found separately, by parseCommentedExample and parseCommentedHeader, and
// given a tip the same way: the plain comment block directly above the
// commented line. A commented key's path is rooted at the nearest live or
// commented header above it in file order.
func templateTips(template []byte, items []tomlItem, section []string) map[string]string {
	lines := bytes.SplitAfter(template, []byte("\n"))
	tips := make(map[string]string)
	for _, it := range items {
		if !hasPathPrefix(it.path, section) {
			continue
		}
		if block := commentBlockAbove(lines, it.first); block != "" {
			tips[pathKey(it.path)] = block
		}
	}
	for _, cp := range commentedTemplatePaths(lines, items) {
		if !hasPathPrefix(cp.path, section) {
			continue
		}
		key := pathKey(cp.path)
		if _, ok := tips[key]; ok {
			continue
		}
		// A commented example's tip is the comment block above it, then its
		// own trailing comment (the text after the value, e.g. the
		// "# technical | ..." of "# audience = \"x\"  # technical | ...") as
		// one more "#" line. Either part can be empty; both empty means no
		// tip for this key.
		tip := commentedItemBlockAbove(lines, cp.line)
		if trailing := strings.TrimSpace(cp.tip); trailing != "" {
			tip += trailing + "\n"
		}
		if tip != "" {
			tips[key] = tip
		}
	}
	return tips
}

// commentedPath is the full key path of one commented example key/value
// line or commented table header line in a template, and the 0-based index
// of that line.
type commentedPath struct {
	path []string
	line int
	// tip is the raw text after the value on the commented example's own
	// line (parseCommentedExample's tip return), e.g. "      # technical |
	// ..." for "# audience = \"x\"      # technical | ...". "" for a
	// commented header, or an example line with no trailing comment.
	tip string
}

// commentedTemplatePaths returns the full path and line index of every
// commented example key/value line (parseCommentedExample) and commented
// table header line (parseCommentedHeader) in lines. A commented key's path
// is its own key parts under the nearest header above it in file order,
// live or commented; liveHeaders supplies the live ones (from scanTOML),
// since a commented header's own path comes directly from parseCommentedHeader.
func commentedTemplatePaths(lines [][]byte, liveHeaders []tomlItem) []commentedPath {
	liveHeaderAt := make(map[int][]string, len(liveHeaders))
	for _, it := range liveHeaders {
		if it.header {
			liveHeaderAt[it.first] = it.path
		}
	}
	var out []commentedPath
	var cur []string
	for i, l := range lines {
		if p, ok := liveHeaderAt[i]; ok {
			cur = p
			continue
		}
		if p, ok := parseCommentedHeader(l); ok {
			cur = p
			out = append(out, commentedPath{path: p, line: i})
			continue
		}
		if key, tip, ok := parseCommentedExample(l); ok {
			out = append(out, commentedPath{path: append(append([]string{}, cur...), strings.Split(key, ".")...), line: i, tip: tip})
		}
	}
	return out
}

// commentedItemBlockAbove returns the run of plain explanatory comment
// lines directly above line i: consecutive comment lines, stopping at a
// line that is itself a commented example key/value or table header line
// (see isCommentedItemLine). That line, and whatever sits above it,
// documents a different, preceding item, not i — unlike a plain comment
// line, which a commented item shares with the live key or header it
// becomes once uncommented (see commentBlockAbove). "" when line i-1 is not
// a plain comment line, including when i is 0.
func commentedItemBlockAbove(lines [][]byte, i int) string {
	if i == 0 || !isCommentLine(lines[i-1]) || isCommentedItemLine(lines[i-1]) {
		return ""
	}
	start := i - 1
	for start > 0 && isCommentLine(lines[start-1]) && !isCommentedItemLine(lines[start-1]) {
		start--
	}
	var buf bytes.Buffer
	for _, l := range lines[start:i] {
		buf.Write(l)
	}
	return buf.String()
}

// isCommentedItemLine reports whether line is itself a commented example
// key/value line or a commented table header line, rather than plain
// explanatory text that documents the item below it.
func isCommentedItemLine(line []byte) bool {
	if _, _, ok := parseCommentedExample(line); ok {
		return true
	}
	_, ok := parseCommentedHeader(line)
	return ok
}

// commentBlockAbove returns the run of consecutive comment lines directly
// above line i, in file order, or "" when line i-1 is not a comment line
// (including when i is the file's first line).
func commentBlockAbove(lines [][]byte, i int) string {
	if !hasCommentAbove(lines, i) {
		return ""
	}
	start := i - 1
	for start > 0 && isCommentLine(lines[start-1]) {
		start--
	}
	var buf bytes.Buffer
	for _, l := range lines[start:i] {
		buf.Write(l)
	}
	return buf.String()
}

// hasCommentAbove reports whether line i-1 is a comment line. The file's
// first line (i == 0) has no line above it, so it never has one.
func hasCommentAbove(lines [][]byte, i int) bool {
	return i > 0 && isCommentLine(lines[i-1])
}

// crlfText converts the LF line endings of a template comment block to eol.
// Templates are always stored with LF; eol is "\r\n" only when the file
// being restored uses CRLF (see usesCRLF).
func crlfText(text, eol string) string {
	if eol == "\n" {
		return text
	}
	return strings.ReplaceAll(text, "\n", eol)
}
