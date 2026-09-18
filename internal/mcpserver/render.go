package mcpserver

import (
	"encoding"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

// render.go renders a successful tool result as Markdown. Every tool's output
// passes through renderOK, so the walker is generic: it reflects over any
// value, including the bare map[string]any returned by the tools that have no
// *Out struct at all.
//
// Walker rules, in precedence order:
//  1. root string field OR root map key named "next" -> "**Next:** <v>" line,
//     omitted from Fields. The hoist happens only at the root and only for a
//     non-empty string, so pipeline.Narration.Next (a *NextAction) and a
//     depth-2 "next" both render in place.
//  2. root string field named "summary"    -> "## Summary" section.
//  3. root []string field named "warnings" -> "## Warnings" bullet list.
//  4. remaining root scalars               -> "## Fields" bullet list, JSON key verbatim.
//  5. struct / map                         -> "## <key>", then "### <parent>.<key>", ...
//  6. []struct                             -> "## <key>[0]", "## <key>[1]", ... (index in
//     the heading, siblings at the same level).
//  7. []scalar                             -> indented bullet list under the key.
//  8. empty collection                     -> "(none)". Fires on a nil or len == 0 slice
//     or map, on a nil pointer, and on an empty string. A field that would be a bullet
//     renders "- <key>: (none)"; a field that owns a section keeps its heading and has
//     "(none)" as its whole body.
//  9. string containing "\n"               -> fenced block, fence = maxRun+1, min 3.
// 10. field tagged `render:"raw"`          -> emitted verbatim, never fenced.
// 11. map                                  -> keys sorted with sort.Strings.
// 12. depth > renderMaxDepth, or a cycle   -> flattened to "dotted.path: value" bullets
//     (a cycle renders "(cycle)" and stops).
// 13. non-nil pointer                      -> dereferenced and rendered by these same
//     rules; a *T never prints as an address. A nil pointer is rule 8.
// 14. json tag carries omitempty (or omitzero) AND the value is the zero value ->
//     the field is omitted entirely, reproducing what encoding/json does today.
// 15. interface element (an any inside []any or map[string]any) -> unwrapped, then
//     rendered by these same rules.
//
// Fenced and raw blocks are written at column 0, never indented under their
// bullet: indenting would change the bytes of a field whose contract is that it
// is reproduced verbatim (a git diff, a pre-rendered narration).

const (
	// renderMaxDepth is the section nesting depth beyond which the walker stops
	// emitting headings and flattens the rest of the subtree to dotted-path
	// bullets (rule 12).
	renderMaxDepth = 6
	// renderMaxHeadingLevel caps the "#" run; Markdown has no level 7.
	renderMaxHeadingLevel = 6
	// renderMaxFlattenDepth bounds the flattening walk itself. Cycles are caught
	// by the identity guard; this is the backstop for anything it cannot see.
	renderMaxFlattenDepth = 32
	// renderMaxIndirect bounds pointer/interface unwrapping of a single value.
	renderMaxIndirect = 32

	renderNone  = "(none)"
	renderCycle = "(cycle)"
)

var textMarshalerType = reflect.TypeFor[encoding.TextMarshaler]()

// renderEntry is one key/value pair to render, already resolved to its JSON
// key. raw records the `render:"raw"` tag (rule 10); map entries are never raw
// because only struct fields carry tags.
type renderEntry struct {
	key string
	val reflect.Value
	raw bool
}

// cycleKey identifies one pointer or map on the current walk path.
type cycleKey struct {
	ptr uintptr
	typ reflect.Type
}

type renderer struct {
	b    strings.Builder
	seen map[cycleKey]bool

	empty     bool
	blankLast bool
}

// renderOK renders a successful tool result as the Markdown text that becomes
// the tool's content[0].text. tool is the registered tool name; out is the
// handler's return value, which may be a struct, a pointer to one, or a bare
// map[string]any.
func renderOK(tool string, out any) string {
	r := &renderer{seen: make(map[cycleKey]bool), empty: true}
	r.line("# " + tool + " — ok")

	root, release, cycle := r.resolve(reflect.ValueOf(out))
	defer release()
	if cycle || !root.IsValid() {
		return r.b.String()
	}

	entries, ok := entriesOf(root)
	if !ok {
		// A scalar result: there is no key to name it by.
		r.blank()
		r.heading(2, "Fields")
		r.renderBullet("value", root, false, 0)
		return r.b.String()
	}

	var (
		nextText    string
		summaryText string
		warnings    *renderEntry
		fields      []renderEntry
		sections    []renderEntry
	)

	for _, entry := range entries {
		if nextText == "" && entry.key == "next" && !entry.raw {
			if s := plainString(entry.val); s != "" {
				nextText = s
				continue
			}
		}
		if summaryText == "" && entry.key == "summary" && !entry.raw {
			if s := plainString(entry.val); s != "" {
				summaryText = s
				continue
			}
		}
		if warnings == nil && entry.key == "warnings" && !entry.raw && isScalarSlice(entry.val) {
			warned := entry
			warnings = &warned
			continue
		}
		if ownsSection(entry) {
			sections = append(sections, entry)
			continue
		}
		fields = append(fields, entry)
	}

	if nextText != "" {
		r.blank()
		r.line("**Next:** " + nextText)
	}
	if summaryText != "" {
		r.blank()
		r.heading(2, "Summary")
		r.writeBlock(summaryText)
	}
	if warnings != nil {
		r.blank()
		r.heading(2, "Warnings")
		r.renderItems(renderIndirect(warnings.val), 0)
	}
	if len(fields) > 0 {
		r.blank()
		r.heading(2, "Fields")
		for _, entry := range fields {
			r.renderBullet(entry.key, entry.val, entry.raw, 0)
		}
	}
	for _, entry := range sections {
		r.renderSection(entry.key, entry.val, 1)
	}

	return r.b.String()
}

// renderSection emits one "## path" (or deeper) section for a value that owns
// one: a struct, a map, or a slice of either. path is the dotted key path from
// the root, which is what the heading shows.
func (r *renderer) renderSection(path string, v reflect.Value, depth int) {
	level := depth + 1
	if level > renderMaxHeadingLevel {
		level = renderMaxHeadingLevel
	}

	resolved, release, cycle := r.resolve(v)
	defer release()

	if cycle {
		r.blank()
		r.heading(level, path)
		r.line(renderCycle)
		return
	}
	if !resolved.IsValid() {
		r.blank()
		r.heading(level, path)
		r.line(renderNone)
		return
	}

	if kind := resolved.Kind(); kind == reflect.Slice || kind == reflect.Array {
		if resolved.Len() == 0 {
			r.blank()
			r.heading(level, path)
			r.line(renderNone)
			return
		}
		// Rule 6: the index lives in the heading and the elements are siblings,
		// not children.
		for i := range resolved.Len() {
			r.renderSection(fmt.Sprintf("%s[%d]", path, i), resolved.Index(i), depth)
		}
		return
	}

	r.blank()
	r.heading(level, path)

	entries, ok := entriesOf(resolved)
	if !ok {
		// An `any` element that turned out to hold a scalar.
		r.line(scalarText(resolved))
		return
	}
	if len(entries) == 0 {
		r.line(renderNone)
		return
	}

	var fields, subs []renderEntry
	for _, entry := range entries {
		if ownsSection(entry) {
			subs = append(subs, entry)
			continue
		}
		fields = append(fields, entry)
	}
	for _, entry := range fields {
		r.renderBullet(entry.key, entry.val, entry.raw, 0)
	}
	for _, entry := range subs {
		if depth+1 > renderMaxDepth {
			// Rule 12: too deep for another heading. The subtree collapses into
			// dotted-path bullets inside this section's body.
			r.renderFlat(entry.key, entry.val, 0)
			continue
		}
		r.renderSection(path+"."+entry.key, entry.val, depth+1)
	}
}

// renderFlat writes a subtree as "- dotted.path: value" bullets. The path is
// relative to the enclosing section, whose heading already carries the prefix.
func (r *renderer) renderFlat(path string, v reflect.Value, depth int) {
	resolved, release, cycle := r.resolve(v)
	defer release()

	switch {
	case cycle:
		r.bullet(0, path, renderCycle)
		return
	case !resolved.IsValid():
		r.bullet(0, path, renderNone)
		return
	case depth > renderMaxFlattenDepth:
		r.bullet(0, path, renderCycle)
		return
	}

	if kind := resolved.Kind(); (kind == reflect.Slice || kind == reflect.Array) && valueOwnsSection(resolved) {
		if resolved.Len() == 0 {
			r.bullet(0, path, renderNone)
			return
		}
		for i := range resolved.Len() {
			r.renderFlat(fmt.Sprintf("%s[%d]", path, i), resolved.Index(i), depth+1)
		}
		return
	}

	entries, ok := entriesOf(resolved)
	if !ok {
		r.renderBullet(path, resolved, false, 0)
		return
	}
	if len(entries) == 0 {
		r.bullet(0, path, renderNone)
		return
	}
	for _, entry := range entries {
		r.renderFlat(path+"."+entry.key, entry.val, depth+1)
	}
}

// renderBullet writes one "- key: value" bullet for a value that does not own a
// section.
func (r *renderer) renderBullet(key string, v reflect.Value, raw bool, indent int) {
	resolved := renderIndirect(v)
	if !resolved.IsValid() {
		r.bullet(indent, key, renderNone)
		return
	}

	if raw {
		// Rule 10: verbatim, never fenced.
		text := scalarText(resolved)
		if !strings.Contains(text, "\n") {
			r.bullet(indent, key, text)
			return
		}
		r.bulletKey(indent, key)
		r.writeBlock(text)
		return
	}

	switch resolved.Kind() {
	case reflect.String:
		s := resolved.String()
		switch {
		case s == "":
			r.bullet(indent, key, renderNone)
		case strings.Contains(s, "\n"):
			r.bulletKey(indent, key)
			r.writeFenced(s)
		default:
			r.bullet(indent, key, s)
		}
	case reflect.Slice, reflect.Array:
		if resolved.Len() == 0 {
			r.bullet(indent, key, renderNone)
			return
		}
		r.bulletKey(indent, key)
		r.renderItems(resolved, indent+1)
	default:
		r.bullet(indent, key, scalarText(resolved))
	}
}

// renderItems writes a scalar slice as a bullet list (rule 7). An invalid value
// or an empty slice renders "(none)" as the whole body.
func (r *renderer) renderItems(v reflect.Value, indent int) {
	if !v.IsValid() || v.Len() == 0 {
		r.line(strings.Repeat("  ", indent) + renderNone)
		return
	}
	for i := range v.Len() {
		item := renderIndirect(v.Index(i))
		prefix := strings.Repeat("  ", indent)
		switch {
		case !item.IsValid():
			r.line(prefix + "- " + renderNone)
		case item.Kind() == reflect.String && strings.Contains(item.String(), "\n"):
			r.line(prefix + "-")
			r.writeFenced(item.String())
		default:
			r.line(prefix + "- " + scalarText(item))
		}
	}
}

// --- value classification ---

// entriesOf returns the renderable key/value pairs of a struct or a map, in the
// order they should be rendered. ok is false for every other kind.
func entriesOf(v reflect.Value) (entries []renderEntry, ok bool) {
	switch v.Kind() {
	case reflect.Struct:
		if isTextScalar(v) {
			return nil, false
		}
		return structEntries(v), true
	case reflect.Map:
		return mapEntries(v), true
	default:
		return nil, false
	}
}

// structEntries walks a struct's fields the way encoding/json does: JSON names
// from fieldJSONInfo, anonymous structs inlined, omitempty/omitzero zero values
// dropped (rule 14).
func structEntries(v reflect.Value) []renderEntry {
	t := v.Type()
	entries := make([]renderEntry, 0, t.NumField())

	for i := range t.NumField() {
		field := t.Field(i)
		info := fieldJSONInfo(field)
		if info.omit {
			continue
		}
		value := v.Field(i)

		if field.Anonymous && !info.explicitName {
			inner := renderIndirect(value)
			if !inner.IsValid() && isNilable(value) {
				// An embedded nil pointer contributes no keys, same as json.
				continue
			}
			if inner.IsValid() && inner.Kind() == reflect.Struct && !isTextScalar(inner) {
				entries = append(entries, structEntries(inner)...)
				continue
			}
		}

		if (info.settings["omitempty"] && isJSONEmpty(value)) ||
			(info.settings["omitzero"] && value.IsZero()) {
			continue
		}

		entries = append(entries, renderEntry{
			key: info.name,
			val: value,
			raw: strings.TrimSpace(field.Tag.Get("render")) == "raw",
		})
	}

	return entries
}

// mapEntries returns a map's pairs with the keys sorted (rule 11), so the model
// sees the same order on every call.
func mapEntries(v reflect.Value) []renderEntry {
	if v.IsNil() || v.Len() == 0 {
		return nil
	}

	keys := make([]string, 0, v.Len())
	values := make(map[string]reflect.Value, v.Len())
	for _, key := range v.MapKeys() {
		name := mapKeyText(key)
		keys = append(keys, name)
		values[name] = v.MapIndex(key)
	}
	sort.Strings(keys)

	entries := make([]renderEntry, 0, len(keys))
	for _, key := range keys {
		entries = append(entries, renderEntry{key: key, val: values[key]})
	}
	return entries
}

func mapKeyText(key reflect.Value) string {
	if key.Kind() == reflect.String {
		return key.String()
	}
	return scalarText(key)
}

// ownsSection reports whether an entry renders as its own "## key" section
// rather than as a bullet. A raw field is always a bullet.
func ownsSection(entry renderEntry) bool {
	if entry.raw {
		return false
	}
	return valueOwnsSection(renderIndirect(entry.val))
}

// valueOwnsSection reports whether a resolved value renders as a section: a
// struct, a map, or a slice whose elements are themselves sections.
func valueOwnsSection(v reflect.Value) bool {
	if !v.IsValid() {
		return false
	}
	switch v.Kind() {
	case reflect.Struct:
		return !isTextScalar(v)
	case reflect.Map:
		return true
	case reflect.Slice, reflect.Array:
		return elemOwnsSection(v)
	default:
		return false
	}
}

// elemOwnsSection classifies a slice by its element type, falling back to the
// dynamic kind of the elements when the static type is an interface. It never
// recurses into nested slices, so a []any containing itself cannot loop here.
func elemOwnsSection(v reflect.Value) bool {
	elem := v.Type().Elem()
	for elem.Kind() == reflect.Pointer {
		elem = elem.Elem()
	}
	switch elem.Kind() {
	case reflect.Struct:
		return !elem.Implements(textMarshalerType) && !reflect.PointerTo(elem).Implements(textMarshalerType)
	case reflect.Map:
		return true
	case reflect.Interface:
		for i := range v.Len() {
			item := renderIndirect(v.Index(i))
			if !item.IsValid() {
				continue
			}
			switch item.Kind() {
			case reflect.Struct:
				if !isTextScalar(item) {
					return true
				}
			case reflect.Map:
				return true
			}
		}
		return false
	default:
		return false
	}
}

// isScalarSlice reports whether v is a slice or array that renders as a bullet
// list (rule 7). It is what qualifies a "warnings" field for rule 3.
func isScalarSlice(v reflect.Value) bool {
	resolved := renderIndirect(v)
	if !resolved.IsValid() {
		return false
	}
	kind := resolved.Kind()
	if kind != reflect.Slice && kind != reflect.Array {
		return false
	}
	return !elemOwnsSection(resolved)
}

// plainString returns the string held by v, unwrapping interfaces but not
// pointers: the root "next" hoist (rule 1) fires only on a plain string, so a
// *NextAction keeps its own section.
func plainString(v reflect.Value) string {
	for range renderMaxIndirect {
		if !v.IsValid() || v.Kind() != reflect.Interface {
			break
		}
		if v.IsNil() {
			return ""
		}
		v = v.Elem()
	}
	if v.IsValid() && v.Kind() == reflect.String {
		return v.String()
	}
	return ""
}

// renderIndirect unwraps interfaces and pointers (rules 13 and 15). A nil
// pointer or interface resolves to the zero Value, which every caller renders
// as "(none)".
func renderIndirect(v reflect.Value) reflect.Value {
	for range renderMaxIndirect {
		if !v.IsValid() {
			return reflect.Value{}
		}
		switch v.Kind() {
		case reflect.Interface, reflect.Pointer:
			if v.IsNil() {
				return reflect.Value{}
			}
			v = v.Elem()
		default:
			return v
		}
	}
	return reflect.Value{}
}

func isNilable(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Interface, reflect.Pointer, reflect.Map, reflect.Slice, reflect.Chan, reflect.Func:
		return true
	default:
		return false
	}
}

// isJSONEmpty mirrors encoding/json's isEmptyValue, so rule 14 removes exactly
// the fields omitempty removes today.
func isJSONEmpty(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Array, reflect.Map, reflect.Slice, reflect.String:
		return v.Len() == 0
	case reflect.Bool:
		return !v.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int() == 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return v.Uint() == 0
	case reflect.Float32, reflect.Float64:
		return v.Float() == 0
	case reflect.Interface, reflect.Pointer:
		return v.IsNil()
	default:
		return false
	}
}

// isTextScalar reports whether a struct renders as a scalar because it knows
// how to write itself as text (time.Time and friends).
func isTextScalar(v reflect.Value) bool {
	t := v.Type()
	return t.Implements(textMarshalerType) || reflect.PointerTo(t).Implements(textMarshalerType)
}

// scalarText renders a leaf value as inline text. An empty string is "(none)"
// so a bullet never ends in a bare colon and trailing whitespace.
func scalarText(v reflect.Value) string {
	if !v.IsValid() {
		return renderNone
	}
	switch v.Kind() {
	case reflect.String:
		if v.String() == "" {
			return renderNone
		}
		return v.String()
	case reflect.Bool:
		return strconv.FormatBool(v.Bool())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(v.Int(), 10)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return strconv.FormatUint(v.Uint(), 10)
	case reflect.Float32, reflect.Float64:
		return strconv.FormatFloat(v.Float(), 'g', -1, 64)
	}
	if !v.CanInterface() {
		return renderNone
	}
	if isTextScalar(v) {
		if marshaler, ok := v.Interface().(encoding.TextMarshaler); ok {
			if text, err := marshaler.MarshalText(); err == nil {
				return string(text)
			}
		}
	}
	return fmt.Sprintf("%v", v.Interface())
}

// --- cycle guard ---

// resolve unwraps v to the value that will be rendered, recording every pointer
// and map it passes through so a cycle is detected instead of recursed into
// (rule 12). The returned release must be called when the caller is done with
// the subtree; identities are scoped to the current path, so two siblings
// pointing at the same object both render.
func (r *renderer) resolve(v reflect.Value) (resolved reflect.Value, release func(), cycle bool) {
	var keys []cycleKey
	release = func() {
		for _, key := range keys {
			delete(r.seen, key)
		}
	}

	for range renderMaxIndirect {
		if !v.IsValid() {
			return reflect.Value{}, release, false
		}
		switch v.Kind() {
		case reflect.Interface:
			if v.IsNil() {
				return reflect.Value{}, release, false
			}
			v = v.Elem()
		case reflect.Pointer:
			if v.IsNil() {
				return reflect.Value{}, release, false
			}
			key := cycleKey{ptr: v.Pointer(), typ: v.Type()}
			if r.seen[key] {
				return v, release, true
			}
			r.seen[key] = true
			keys = append(keys, key)
			v = v.Elem()
		case reflect.Map:
			if !v.IsNil() {
				key := cycleKey{ptr: v.Pointer(), typ: v.Type()}
				if r.seen[key] {
					return v, release, true
				}
				r.seen[key] = true
				keys = append(keys, key)
			}
			return v, release, false
		default:
			return v, release, false
		}
	}

	return reflect.Value{}, release, false
}

// --- output primitives ---

func (r *renderer) line(s string) {
	r.b.WriteString(s)
	r.b.WriteByte('\n')
	r.empty = false
	r.blankLast = s == ""
}

// blank writes a separating blank line, collapsing repeats.
func (r *renderer) blank() {
	if r.empty || r.blankLast {
		return
	}
	r.b.WriteByte('\n')
	r.blankLast = true
}

func (r *renderer) heading(level int, text string) {
	if level < 1 {
		level = 1
	}
	if level > renderMaxHeadingLevel {
		level = renderMaxHeadingLevel
	}
	r.line(strings.Repeat("#", level) + " " + text)
}

func (r *renderer) bullet(indent int, key, value string) {
	r.line(strings.Repeat("  ", indent) + "- " + key + ": " + value)
}

// bulletKey writes the "- key:" line that introduces a block value.
func (r *renderer) bulletKey(indent int, key string) {
	r.line(strings.Repeat("  ", indent) + "- " + key + ":")
}

// writeBlock writes multi-line text at column 0, one line at a time so blank
// line tracking stays correct. Trailing newlines are dropped; nothing else is.
func (r *renderer) writeBlock(text string) {
	for _, l := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		r.line(l)
	}
}

// writeFenced writes text inside a code fence long enough to contain it
// (rule 9): one backtick more than the longest run in the content, never fewer
// than three.
func (r *renderer) writeFenced(text string) {
	length := maxBacktickRun(text) + 1
	if length < 3 {
		length = 3
	}
	fence := strings.Repeat("`", length)
	r.line(fence)
	r.writeBlock(text)
	r.line(fence)
}

func maxBacktickRun(s string) int {
	longest, current := 0, 0
	for _, c := range s {
		if c != '`' {
			current = 0
			continue
		}
		current++
		if current > longest {
			longest = current
		}
	}
	return longest
}
