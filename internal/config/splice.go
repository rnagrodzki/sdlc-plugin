// splice.go: in-place text splicing for WriteSection.
//
// WriteSection used to re-marshal the whole file from parsed data, which
// deleted every comment in the commented config.toml/local.toml templates on
// the first write. The key-level splice (spliceKeys) changes only the value
// text of changed keys, removes the lines of removed keys and tables, and
// adds new keys and tables. Every other byte of the file stays as it was.
//
// Comment rule: comment lines are never removed. This includes comment lines
// inside a table, and a trailing "# comment" on a changed single-line value.
package config

import (
	"bytes"
	"errors"
	"os"
	"reflect"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"github.com/pelletier/go-toml/v2/unstable"

	"github.com/rnagrodzki/sdlc-plugin/internal/fsx"
)

// errNoSplice reports a file layout the splicer does not handle, e.g. the
// section lives inside an inline table or an array of tables.
var errNoSplice = errors.New("config: section cannot be spliced")

// ErrWouldDropComments reports that the in-place edit of a config file
// failed and the file has a comment line, so the write was refused instead
// of rewriting the whole file (a full rewrite deletes every comment).
var ErrWouldDropComments = errors.New("config: in-place edit failed and a full rewrite would delete comments")

// tomlItem is one top-level TOML expression: a table header or a key/value.
type tomlItem struct {
	header   bool     // [table] or [[array table]] header
	array    bool     // [[array table]] header
	path     []string // absolute key path (header path, or table path + key)
	first    int      // first line, 0-based
	last     int      // last line, 0-based, inclusive
	valStart int      // key/value only: byte offset of the value's first byte
	valEnd   int      // key/value only: byte offset just after the value
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
				path:     path,
				first:    lineOf(data, int(e.Raw.Offset)),
				last:     lineOf(data, end-1),
				valStart: valueStart(data, int(e.Raw.Offset)),
				valEnd:   end,
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

// valueStart returns the byte offset of the value of the key/value that
// starts at off: the first non-blank byte after the "=" that follows the key.
// A quoted key part can hold "=", so quoted text is skipped. The parser's own
// value range cannot be used: for an array it does not point at the "[".
func valueStart(data []byte, off int) int {
	i := off
	for i < len(data) && data[i] != '=' {
		if q := data[i]; q == '"' || q == '\'' {
			for i++; i < len(data) && data[i] != q; i++ {
				if q == '"' && data[i] == '\\' {
					i++
				}
			}
		}
		i++
	}
	for i++; i < len(data) && (data[i] == ' ' || data[i] == '\t'); i++ {
	}
	return i
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

// usesCRLF reports whether data uses CRLF line endings, judged by its first
// line ending. go-toml writes the fragment with LF only; a string value
// never holds a raw newline in that output (it is escaped), so converting
// every LF in the fragment to CRLF does not change any value.
func usesCRLF(data []byte) bool {
	i := bytes.IndexByte(data, '\n')
	return i > 0 && data[i-1] == '\r'
}

// spliceFile returns the new contents of the TOML file at path with section
// name set to v, built by a key-level edit of the file's text (spliceKeys).
// merged is the whole file's data after the write, as a full rewrite would
// store it. The edit is accepted only when the result decodes to exactly the
// data a full rewrite of merged would decode to; otherwise ok is false.
func spliceFile(path, name string, v, merged map[string]any) (out []byte, ok bool) {
	orig, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, false
	}
	out, err = spliceKeys(orig, strings.Split(name, "."), v)
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

// hasCommentLine reports whether data has a line whose first non-blank
// character is "#".
func hasCommentLine(data []byte) bool {
	for _, l := range bytes.Split(data, []byte("\n")) {
		if bytes.HasPrefix(bytes.TrimSpace(l), []byte("#")) {
			return true
		}
	}
	return false
}

// isCommentLine reports whether line is a comment line.
func isCommentLine(line []byte) bool {
	return bytes.HasPrefix(bytes.TrimSpace(line), []byte("#"))
}

// commentedHeaderEnd returns the index of the line after the comment block
// that holds a commented header for path ("# [ship]", any spacing), or -1.
// A comment block is a run of consecutive comment lines.
func commentedHeaderEnd(lines [][]byte, path []string) int {
	want := "[" + strings.Join(path, ".") + "]"
	for i, l := range lines {
		t := bytes.TrimSpace(l)
		if !bytes.HasPrefix(t, []byte("#")) {
			continue
		}
		body := strings.Join(strings.Fields(string(t[1:])), "")
		if body != want {
			continue
		}
		j := i + 1
		for j < len(lines) && isCommentLine(lines[j]) {
			j++
		}
		return j
	}
	return -1
}

// pathKey joins a key path into a map key. "\x00" cannot occur in a TOML
// key, so different paths never share a map key.
func pathKey(p []string) string { return strings.Join(p, "\x00") }

// childPath returns p with k appended, in a new slice.
func childPath(p []string, k string) []string {
	return append(append([]string{}, p...), k)
}

// tomlBlock is one owned table header and the key/values under it.
type tomlBlock struct {
	hdr       tomlItem
	kvs       []int // indices into keySplicer.items
	end       int   // last line of the block (last key/value line, or header)
	visited   bool  // the block is kept (its header stays)
	keepBlank bool  // when removed, keep the blank line after it
}

// keySplicer holds the state of one spliceKeys call.
type keySplicer struct {
	data      []byte
	lines     [][]byte
	starts    []int // byte offset of each line
	eol       string
	items     []tomlItem
	old       map[string]any // the file's decoded data
	blocks    []*tomlBlock   // owned blocks, in file order
	tables    map[string]*tomlBlock
	handled   []bool           // per item: key/value kept or changed in place
	remove    []bool           // per line
	edits     map[int]int      // first line of a changed key/value -> item index
	newVal    map[int]string   // item index -> new value text
	keyAt     map[int][]string // key lines inserted before a line index
	tableAt   map[int][]string // table texts inserted before a line index
	newTables []string         // new tables that go after the section
}

// spliceKeys returns data with the table at path changed so that it decodes
// to v. Unchanged keys keep their bytes. A changed key keeps its key text
// and indentation. Only the value text changes. A removed key loses its
// key/value lines. Comment lines are never removed. New keys go after the
// table's last key. A sub-tree that is an array of tables, or that changes
// between table and array of tables, is encoded again with sectionFragment
// at the position of its first old block, below the old comment lines.
// Returns errNoSplice for inline tables, dotted keys that define a parent,
// and an array of tables that is a strict ancestor of path.
//
// Other rules:
//   - A removed table loses its header line, its key/value lines and one
//     blank line after it.
//   - A new sub-table goes after the last line of the section. When every
//     old block of the section is removed, new sub-tables go at the first
//     old block.
//   - A key/value outside the owned tables whose full path is at or below
//     path (e.g. "y.z = 1" under [x] when path is x.y) is removed, and its
//     data is written again as new text.
//   - With no owned table, the new section goes directly after the comment
//     block that holds "# [path]" when the file has one. Otherwise it is
//     appended at the end of the file after exactly one blank line.
//   - When data uses CRLF line endings (see usesCRLF), new text uses CRLF.
func spliceKeys(data []byte, path []string, v map[string]any) ([]byte, error) {
	items, err := scanTOML(data)
	if err != nil {
		return nil, err
	}
	s := &keySplicer{
		data:    data,
		lines:   bytes.SplitAfter(data, []byte("\n")),
		eol:     "\n",
		items:   items,
		tables:  map[string]*tomlBlock{},
		handled: make([]bool, len(items)),
		edits:   map[int]int{},
		newVal:  map[int]string{},
		keyAt:   map[int][]string{},
		tableAt: map[int][]string{},
	}
	if usesCRLF(data) {
		s.eol = "\r\n"
	}
	s.remove = make([]bool, len(s.lines))
	s.starts = make([]int, len(s.lines))
	for i, off := 1, 0; i < len(s.lines); i++ {
		off += len(s.lines[i-1])
		s.starts[i] = off
	}
	if err := fsx.DecodeTOML(data, &s.old); err != nil {
		return nil, err
	}

	var cur *tomlBlock
	for i, it := range items {
		if it.header {
			cur = nil
			if it.array && isStrictPathPrefix(it.path, path) {
				return nil, errNoSplice
			}
			if hasPathPrefix(it.path, path) {
				cur = &tomlBlock{hdr: it, end: it.last}
				s.blocks = append(s.blocks, cur)
				if !it.array {
					s.tables[pathKey(it.path)] = cur
				}
			}
			continue
		}
		switch {
		case cur != nil:
			cur.kvs = append(cur.kvs, i)
			cur.end = it.last
		case hasPathPrefix(it.path, path):
			s.removeItem(it)
		case isStrictPathPrefix(it.path, path):
			return nil, errNoSplice
		}
	}

	if len(s.blocks) == 0 {
		return s.appendSection(path, v)
	}
	if s.hasArrayAt(path) {
		frag, err := sectionFragment(path, v)
		if err != nil {
			return nil, err
		}
		s.reencode(path, frag)
	} else if err := s.editTable(path, v); err != nil {
		return nil, err
	}
	s.placeNewTables()
	s.removeUnkept()
	return s.render(), nil
}

// appendSection writes the whole section as new text when the file has no
// owned table for path.
func (s *keySplicer) appendSection(path []string, v map[string]any) ([]byte, error) {
	frag, err := sectionFragment(path, v)
	if err != nil {
		return nil, err
	}
	text := s.crlf(string(frag))
	if at := commentedHeaderEnd(s.lines, path); at >= 0 && !s.insideValue(at-1) {
		s.tableAt[at] = append(s.tableAt[at], text)
		return s.render(), nil
	}
	body := bytes.TrimRight(s.render(), "\r\n")
	res := append([]byte{}, body...)
	if len(body) > 0 {
		res = append(res, s.eol+s.eol...)
	}
	return append(res, text...), nil
}

// insideValue reports whether line i is a continuation line of a multi-line
// key/value (a line of a multi-line string or array).
func (s *keySplicer) insideValue(i int) bool {
	for _, it := range s.items {
		if !it.header && it.first < i && i <= it.last {
			return true
		}
	}
	return false
}

// editTable edits the text of the table at p so that it decodes to d.
func (s *keySplicer) editTable(p []string, d map[string]any) error {
	blk := s.tables[pathKey(p)]
	single := map[string]int{}
	dotted := map[string][]int{}
	if blk != nil {
		blk.visited = true
		for _, i := range blk.kvs {
			rel := s.items[i].path[len(p):]
			if len(rel) == 1 {
				single[rel[0]] = i
			} else {
				dotted[rel[0]] = append(dotted[rel[0]], i)
			}
		}
	}
	pending := map[string]any{}
	for _, k := range sortedKeys(d) {
		val := d[k]
		cp := childPath(p, k)
		sub, isMap := val.(map[string]any)
		switch {
		case isTableArray(val):
			if i, ok := single[k]; ok && s.sameAsOld(cp, val) {
				s.handled[i] = true
				continue
			}
			frag, err := sectionFragment(p, map[string]any{k: val})
			if err != nil {
				return err
			}
			switch {
			case s.hasArrayAt(cp) && s.sameAsOld(cp, val):
				s.keepSubtree(cp)
			case s.hasHeaderAtOrBelow(cp):
				s.reencode(cp, frag)
			default:
				s.newTables = append(s.newTables, s.crlf(string(frag)))
			}
		case isMap:
			switch {
			case s.hasArrayAt(cp):
				frag, err := sectionFragment(cp, sub)
				if err != nil {
					return err
				}
				s.reencode(cp, frag)
			case s.hasHeaderAtOrBelow(cp):
				if err := s.editTable(cp, sub); err != nil {
					return err
				}
			case len(dotted[k]) > 0:
				if err := s.editDotted(blk, p, cp, sub, dotted[k]); err != nil {
					return err
				}
			default:
				if i, ok := single[k]; ok && s.sameAsOld(cp, val) {
					s.handled[i] = true
					continue
				}
				frag, err := sectionFragment(cp, sub)
				if err != nil {
					return err
				}
				s.newTables = append(s.newTables, s.crlf(string(frag)))
			}
		default:
			if i, ok := single[k]; ok {
				if err := s.keepOrChange(i, cp, val); err != nil {
					return err
				}
				continue
			}
			if blk == nil {
				pending[k] = val
				continue
			}
			line, err := s.keyLine([]string{k}, val)
			if err != nil {
				return err
			}
			s.addKeyLine(blk, line)
		}
	}
	if blk == nil && (len(pending) > 0 || len(d) == 0) {
		frag, err := sectionFragment(p, pending)
		if err != nil {
			return err
		}
		s.newTables = append(s.newTables, s.crlf(string(frag)))
	}
	return nil
}

// editDotted edits the dotted key/values (e.g. "tag.enabled = true" under
// [version]) of blk that define the table at cp, so that it decodes to d.
func (s *keySplicer) editDotted(blk *tomlBlock, p, cp []string, d map[string]any, kvs []int) error {
	byPath := map[string]int{}
	for _, i := range kvs {
		byPath[pathKey(s.items[i].path)] = i
	}
	leaves := map[string]any{}
	var order []string
	var walk func(prefix []string, m map[string]any) error
	walk = func(prefix []string, m map[string]any) error {
		for _, k := range sortedKeys(m) {
			lp := childPath(prefix, k)
			if sub, ok := m[k].(map[string]any); ok {
				if len(sub) == 0 {
					return errNoSplice
				}
				if err := walk(lp, sub); err != nil {
					return err
				}
				continue
			}
			if isTableArray(m[k]) {
				return errNoSplice
			}
			leaves[pathKey(lp)] = m[k]
			order = append(order, pathKey(lp))
		}
		return nil
	}
	if err := walk(cp, d); err != nil {
		return err
	}
	for _, key := range order {
		val := leaves[key]
		lp := strings.Split(key, "\x00")
		if i, ok := byPath[key]; ok {
			if err := s.keepOrChange(i, lp, val); err != nil {
				return err
			}
			continue
		}
		line, err := s.keyLine(lp[len(p):], val)
		if err != nil {
			return err
		}
		s.addKeyLine(blk, line)
	}
	return nil
}

// keepOrChange keeps key/value i when its old value equals val, and
// otherwise replaces only its value text.
func (s *keySplicer) keepOrChange(i int, path []string, val any) error {
	s.handled[i] = true
	if s.sameAsOld(path, val) {
		return nil
	}
	text, ok := inlineValue(val)
	if !ok {
		return errNoSplice
	}
	s.edits[s.items[i].first] = i
	s.newVal[i] = text
	return nil
}

// sameAsOld reports whether the file's old value at path equals val after
// both go through the same TOML encode and decode.
func (s *keySplicer) sameAsOld(path []string, val any) bool {
	var cur any = s.old
	for _, k := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			return false
		}
		if cur, ok = m[k]; !ok {
			return false
		}
	}
	b, err := toml.Marshal(map[string]any{"x": val})
	if err != nil {
		return false
	}
	var n map[string]any
	if fsx.DecodeTOML(b, &n) != nil {
		return false
	}
	return reflect.DeepEqual(cur, n["x"])
}

// inlineValue returns the one-line TOML text of val, as go-toml encodes it
// after "key = ". ok is false when go-toml writes val as a table or an
// array of tables.
func inlineValue(val any) (string, bool) {
	b, err := toml.Marshal(map[string]any{"x": val})
	if err != nil {
		return "", false
	}
	t := string(b)
	if !strings.HasPrefix(t, "x = ") || strings.Count(t, "\n") != 1 || !strings.HasSuffix(t, "\n") {
		return "", false
	}
	return t[len("x = ") : len(t)-1], true
}

// keyText returns the TOML text of the dotted key parts, quoted where TOML
// needs it.
func keyText(parts []string) (string, error) {
	var out []string
	for _, k := range parts {
		b, err := toml.Marshal(map[string]any{k: true})
		if err != nil {
			return "", err
		}
		out = append(out, strings.TrimSuffix(string(b), " = true\n"))
	}
	return strings.Join(out, "."), nil
}

// keyLine returns the "key = value" line for a new key, with the file's
// line ending.
func (s *keySplicer) keyLine(parts []string, val any) (string, error) {
	text, ok := inlineValue(val)
	if !ok {
		return "", errNoSplice
	}
	key, err := keyText(parts)
	if err != nil {
		return "", err
	}
	return key + " = " + text + s.eol, nil
}

// addKeyLine puts a new key line after the last key of blk, with the
// indentation of that key.
func (s *keySplicer) addKeyLine(blk *tomlBlock, line string) {
	after := blk.hdr.last
	indent := ""
	if n := len(blk.kvs); n > 0 {
		it := s.items[blk.kvs[n-1]]
		after = it.last
		first := s.lines[it.first]
		indent = string(first[:len(first)-len(bytes.TrimLeft(first, " \t"))])
	}
	s.keyAt[after+1] = append(s.keyAt[after+1], indent+line)
}

// hasArrayAt reports whether an owned [[cp]] header exists.
func (s *keySplicer) hasArrayAt(cp []string) bool {
	for _, b := range s.blocks {
		if b.hdr.array && pathKey(b.hdr.path) == pathKey(cp) {
			return true
		}
	}
	return false
}

// hasHeaderAtOrBelow reports whether an owned header at or below cp exists.
func (s *keySplicer) hasHeaderAtOrBelow(cp []string) bool {
	for _, b := range s.blocks {
		if hasPathPrefix(b.hdr.path, cp) {
			return true
		}
	}
	return false
}

// keepSubtree keeps every owned block at or below cp unchanged.
func (s *keySplicer) keepSubtree(cp []string) {
	for _, b := range s.blocks {
		if hasPathPrefix(b.hdr.path, cp) {
			b.visited = true
			for _, i := range b.kvs {
				s.handled[i] = true
			}
		}
	}
}

// reencode puts frag at the first owned block at or below cp. Those blocks
// are not visited, so removeUnkept deletes them.
func (s *keySplicer) reencode(cp []string, frag []byte) {
	for _, b := range s.blocks {
		if hasPathPrefix(b.hdr.path, cp) {
			b.keepBlank = true
			s.tableAt[b.hdr.first] = append(s.tableAt[b.hdr.first], s.crlf(string(frag)))
			return
		}
	}
}

// placeNewTables puts the new tables after the last line of the section, or
// at the first old block when every old block is removed.
func (s *keySplicer) placeNewTables() {
	if len(s.newTables) == 0 {
		return
	}
	text := strings.Join(s.newTables, s.eol)
	last, kept := -1, false
	for _, b := range s.blocks {
		kept = kept || b.visited
		if b.end > last {
			last = b.end
		}
	}
	if !kept {
		first := s.blocks[0]
		first.keepBlank = true
		s.tableAt[first.hdr.first] = append(s.tableAt[first.hdr.first], text)
		return
	}
	s.tableAt[last+1] = append(s.tableAt[last+1], s.eol+text)
}

// removeUnkept removes the key/value lines that were not kept or changed,
// and the header line, key/value lines and one blank line after each
// owned block that is not kept. Comment lines stay.
func (s *keySplicer) removeUnkept() {
	for _, b := range s.blocks {
		for _, i := range b.kvs {
			if !s.handled[i] {
				s.removeItem(s.items[i])
			}
		}
		if b.visited {
			continue
		}
		s.remove[b.hdr.first] = true
		if n := b.end + 1; !b.keepBlank && n < len(s.lines) && len(s.lines[n]) > 0 && isBlankLine(s.lines[n]) {
			s.remove[n] = true
		}
	}
}

// removeItem marks every line of it for removal.
func (s *keySplicer) removeItem(it tomlItem) {
	for k := it.first; k <= it.last; k++ {
		s.remove[k] = true
	}
}

// crlf converts the LF line endings of text to the file's line ending.
func (s *keySplicer) crlf(text string) string {
	if s.eol == "\n" {
		return text
	}
	return strings.ReplaceAll(text, "\n", s.eol)
}

// render builds the new file text from the lines and the recorded edits.
func (s *keySplicer) render() []byte {
	var out bytes.Buffer
	insert := func(text string) {
		if out.Len() > 0 && out.Bytes()[out.Len()-1] != '\n' {
			out.WriteString(s.eol)
		}
		out.WriteString(text)
	}
	for i := 0; i <= len(s.lines); i++ {
		for _, t := range s.keyAt[i] {
			insert(t)
		}
		if t := s.tableAt[i]; len(t) > 0 {
			insert(strings.Join(t, s.eol))
		}
		if i == len(s.lines) {
			break
		}
		if idx, ok := s.edits[i]; ok {
			it := s.items[idx]
			end := s.starts[it.last] + len(s.lines[it.last])
			out.Write(s.data[s.starts[it.first]:it.valStart])
			out.WriteString(s.newVal[idx])
			out.Write(s.data[it.valEnd:end])
			i = it.last
			continue
		}
		if !s.remove[i] {
			out.Write(s.lines[i])
		}
	}
	return out.Bytes()
}

// isTableArray reports whether go-toml writes val as an array of tables: a
// non-empty slice whose elements are all tables.
func isTableArray(val any) bool {
	switch a := val.(type) {
	case []map[string]any:
		return len(a) > 0
	case []any:
		if len(a) == 0 {
			return false
		}
		for _, e := range a {
			if _, ok := e.(map[string]any); !ok {
				return false
			}
		}
		return true
	}
	return false
}

// sortedKeys returns the keys of m in sorted order.
func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
