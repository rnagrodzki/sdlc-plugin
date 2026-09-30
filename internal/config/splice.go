// splice.go: in-place text splicing for WriteSection.
//
// WriteSection used to re-marshal the whole file from parsed data, which
// deleted every comment in the commented config.toml/local.toml templates on
// the first write. Splicing replaces only the text of the section being
// written and leaves every other byte of the file as it was.
//
// Comment rule: comment and blank lines ABOVE a section's table header stay,
// and so do comment and blank lines AFTER a table's last key (up to the next
// header). Comment lines between a replaced header and its last key are lost,
// because the section body is re-encoded with go-toml.
package config

import (
	"bytes"
	"errors"
	"os"
	"reflect"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"github.com/pelletier/go-toml/v2/unstable"

	"github.com/rnagrodzki/sdlc-plugin/internal/fsx"
)

// errNoSplice reports a file layout the splicer does not handle, e.g. the
// section lives inside an inline table or an array of tables.
var errNoSplice = errors.New("config: section cannot be spliced")

// tomlItem is one top-level TOML expression: a table header or a key/value.
type tomlItem struct {
	header bool     // [table] or [[array table]] header
	array  bool     // [[array table]] header
	path   []string // absolute key path (header path, or table path + key)
	first  int      // first line, 0-based
	last   int      // last line, 0-based, inclusive
}

// scanTOML parses data with the go-toml parser and returns its table headers
// and key/values with their line ranges. Using the real parser means a "["
// inside a multi-line string or array, or inside a comment, is never taken
// for a table header.
func scanTOML(data []byte) ([]tomlItem, error) {
	var p unstable.Parser
	p.Reset(data)
	var items []tomlItem
	var table []string
	for p.NextExpression() {
		e := p.Expression()
		switch e.Kind {
		case unstable.Table, unstable.ArrayTable:
			keys, start := keyParts(e)
			line := lineOf(data, start)
			table = keys
			items = append(items, tomlItem{
				header: true,
				array:  e.Kind == unstable.ArrayTable,
				path:   keys,
				first:  line,
				last:   line,
			})
		case unstable.KeyValue:
			keys, _ := keyParts(e)
			path := append(append([]string{}, table...), keys...)
			end := int(e.Raw.Offset + e.Raw.Length)
			items = append(items, tomlItem{
				path:  path,
				first: lineOf(data, int(e.Raw.Offset)),
				last:  lineOf(data, end-1),
			})
		}
	}
	if err := p.Error(); err != nil {
		return nil, err
	}
	return items, nil
}

// keyParts returns the (unquoted) key parts of a header or key/value node and
// the byte offset of its first key part.
func keyParts(e *unstable.Node) ([]string, int) {
	var parts []string
	start := -1
	it := e.Key()
	for it.Next() {
		n := it.Node()
		if start < 0 {
			start = int(n.Raw.Offset)
		}
		parts = append(parts, string(n.Data))
	}
	return parts, start
}

// lineOf returns the 0-based line index of byte offset off in data.
func lineOf(data []byte, off int) int {
	return bytes.Count(data[:off], []byte("\n"))
}

// hasPathPrefix reports whether path equals prefix or extends it.
func hasPathPrefix(path, prefix []string) bool {
	if len(path) < len(prefix) {
		return false
	}
	for i := range prefix {
		if path[i] != prefix[i] {
			return false
		}
	}
	return true
}

// isStrictPathPrefix reports whether p is a proper prefix of path.
func isStrictPathPrefix(p, path []string) bool {
	return len(p) < len(path) && hasPathPrefix(path, p)
}

// isBlankLine reports whether line holds only whitespace.
func isBlankLine(line []byte) bool {
	return len(bytes.TrimSpace(line)) == 0
}

// sectionFragment encodes v as the TOML text of the table at path, headers
// included. Headers of path's ancestors are dropped (the file may already
// define them), and so is any table header with no keys that is directly
// followed by one of its own sub-tables (the sub-table implies it).
func sectionFragment(path []string, v map[string]any) ([]byte, error) {
	wrapped := map[string]any{}
	setSectionPath(wrapped, strings.Join(path, "."), v)
	data, err := toml.Marshal(wrapped)
	if err != nil {
		return nil, err
	}
	items, err := scanTOML(data)
	if err != nil {
		return nil, err
	}
	lines := bytes.SplitAfter(data, []byte("\n"))
	drop := make([]bool, len(lines))
	for i, it := range items {
		if !it.header || it.array {
			continue
		}
		impliedByNext := i+1 < len(items) && items[i+1].header && isStrictPathPrefix(it.path, items[i+1].path)
		if isStrictPathPrefix(it.path, path) || impliedByNext {
			drop[it.first] = true
		}
	}
	var out bytes.Buffer
	for i, l := range lines {
		if drop[i] || (out.Len() == 0 && isBlankLine(l)) {
			continue
		}
		out.Write(l)
	}
	return out.Bytes(), nil
}

// spliceSection returns data with the section at path replaced by fragment.
//
//   - Every table header equal to path or below it ([x], [x.y], [[x.y]])
//     owns a block: the header line through its last key/value line. The
//     first owned block is replaced by fragment; later owned blocks are
//     deleted together with the blank lines right after them.
//   - A key/value outside an owned block whose full path is at or below path
//     (e.g. "y.z = 1" under [x] when path is x.y) is deleted.
//   - With no owned header, fragment is appended at the end of the file after
//     exactly one blank line.
//   - Returns errNoSplice when path lives inside a value (an inline table or
//     a dotted key that defines an ancestor) or inside an array of tables.
func spliceSection(data []byte, path []string, fragment []byte) ([]byte, error) {
	items, err := scanTOML(data)
	if err != nil {
		return nil, err
	}
	lines := bytes.SplitAfter(data, []byte("\n"))
	remove := make([]bool, len(lines))
	insertAt := -1

	for i := 0; i < len(items); i++ {
		it := items[i]
		if !it.header {
			switch {
			case hasPathPrefix(it.path, path):
				for k := it.first; k <= it.last; k++ {
					remove[k] = true
				}
			case isStrictPathPrefix(it.path, path):
				return nil, errNoSplice
			}
			continue
		}
		if it.array && isStrictPathPrefix(it.path, path) {
			return nil, errNoSplice
		}
		if !hasPathPrefix(it.path, path) {
			continue
		}
		end := it.last
		j := i + 1
		for ; j < len(items) && !items[j].header; j++ {
			end = items[j].last
		}
		for k := it.first; k <= end; k++ {
			remove[k] = true
		}
		if insertAt < 0 {
			insertAt = it.first
		} else {
			for k := end + 1; k < len(lines) && len(lines[k]) > 0 && isBlankLine(lines[k]); k++ {
				remove[k] = true
			}
		}
		i = j - 1
	}

	var out bytes.Buffer
	for i, l := range lines {
		if i == insertAt {
			out.Write(fragment)
		}
		if !remove[i] {
			out.Write(l)
		}
	}
	if insertAt >= 0 {
		return out.Bytes(), nil
	}
	body := bytes.TrimRight(out.Bytes(), "\r\n")
	res := append([]byte{}, body...)
	if len(body) > 0 {
		res = append(res, "\n\n"...)
	}
	return append(res, fragment...), nil
}

// spliceFile returns the new contents of the TOML file at path with section
// name set to v, built by splicing the file's text. merged is the whole file's
// data after the write, as a full rewrite would store it. The splice is
// accepted only when the result decodes to exactly the data a full rewrite of
// merged would decode to; otherwise ok is false and the caller rewrites the
// file from merged.
func spliceFile(path, name string, v, merged map[string]any) (out []byte, ok bool) {
	orig, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, false
	}
	parts := strings.Split(name, ".")
	frag, err := sectionFragment(parts, v)
	if err != nil {
		return nil, false
	}
	out, err = spliceSection(orig, parts, frag)
	if err != nil {
		return nil, false
	}
	full, err := toml.Marshal(merged)
	if err != nil {
		return nil, false
	}
	var want, got map[string]any
	if fsx.DecodeTOML(full, &want) != nil || fsx.DecodeTOML(out, &got) != nil {
		return nil, false
	}
	if !reflect.DeepEqual(want, got) {
		return nil, false
	}
	return out, true
}
