package tools

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/rnagrodzki/sdlc-plugin/internal/config"
	"github.com/rnagrodzki/sdlc-plugin/internal/hardensurfaces"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
)

// hardenInstructionMaxItems is the most items one surface list in
// [harden.instructions] may hold.
// hardenInstructionMaxChars is the longest item, counted in characters
// (utf8.RuneCountInString, the unit of JSON Schema maxLength).
// Both values match the hardenInstructionList limits in
// plugins/sdlc/schemas/sdlc-config.schema.json.
const hardenInstructionMaxItems, hardenInstructionMaxChars = 10, 1024

// loadHardenInstructions reads the [harden.instructions] table from
// <contentRoot>/.sdlc-v2/config.toml, the same root harden reads its
// guardrails from. It always returns one list for each id of
// hardensurfaces.ProposalIDs(); a surface with no list maps to an empty
// list. A missing config.toml, a missing [harden] section and a missing
// [harden.instructions] table are not errors: all four lists are empty.
//
// The loader trims each item and drops blank items. A config read failure
// other than not-found returns an InfraError. Each invalid input returns a
// DomainError that names the key: an unknown key under [harden] (the only
// valid key is instructions, so a typo such as [harden.instruction] fails
// instead of dropping the lists), an unknown key under [harden.instructions],
// a value that is not a list,
// more than hardenInstructionMaxItems items, a non-string item, or an item
// over hardenInstructionMaxChars characters. The loader counts the characters
// of the raw item, before it trims the item, so a padded item over the limit
// is rejected.
func loadHardenInstructions(contentRoot string) (map[string][]string, error) {
	ids := hardensurfaces.ProposalIDs()
	out := make(map[string][]string, len(ids))
	for _, id := range ids {
		out[id] = []string{}
	}

	section, err := config.ReadSection(contentRoot, "harden")
	if err != nil {
		if errors.Is(err, config.ErrNotFound) {
			return out, nil
		}
		return nil, &mcpserver.InfraError{
			Msg:        fmt.Sprintf("read harden instructions from .sdlc-v2/config.toml: %v", err),
			Suggestion: "Fix the permission or syntax of .sdlc-v2/config.toml, then run harden again.",
			Cause:      err,
		}
	}

	if err := rejectUnknownHardenKeys(section); err != nil {
		return nil, err
	}

	rawInstructions, ok := section[hardenInstructionsKey]
	if !ok {
		return out, nil
	}
	table, ok := rawInstructions.(map[string]any)
	if !ok {
		return nil, &mcpserver.DomainError{
			Msg:        "harden.instructions must be a table",
			Suggestion: "Write [harden.instructions] as a TOML table with one list for each surface.",
		}
	}

	if err := rejectUnknownInstructionKeys(table, ids); err != nil {
		return nil, err
	}

	for _, id := range ids {
		raw, present := table[id]
		if !present {
			continue
		}
		items, err := parseInstructionList(id, raw)
		if err != nil {
			return nil, err
		}
		out[id] = items
	}
	return out, nil
}

// hardenInstructionsKey is the only valid key of the [harden] section.
const hardenInstructionsKey = "instructions"

// rejectUnknownHardenKeys returns a DomainError for the first key of the
// [harden] section (in sorted order, so the message is stable) that is not
// hardenInstructionsKey.
func rejectUnknownHardenKeys(section map[string]any) error {
	keys := make([]string, 0, len(section))
	for key := range section {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if key != hardenInstructionsKey {
			return &mcpserver.DomainError{
				Msg:        fmt.Sprintf("harden: unknown key %q under [harden]", key),
				Suggestion: "Use the only valid key under [harden]: " + hardenInstructionsKey + ". Write it as [harden.instructions] in .sdlc-v2/config.toml, then run harden again.",
			}
		}
	}
	return nil
}

// rejectUnknownInstructionKeys returns a DomainError for the first key of
// table (in sorted order, so the message is stable) that is not one of the
// proposal surface ids. The Suggestion lists the valid ids from
// hardensurfaces.ProposalIDs.
func rejectUnknownInstructionKeys(table map[string]any, ids []string) error {
	known := make(map[string]bool, len(ids))
	for _, id := range ids {
		known[id] = true
	}
	keys := make([]string, 0, len(table))
	for key := range table {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if !known[key] {
			return &mcpserver.DomainError{
				Msg:        fmt.Sprintf("harden.instructions has unknown key %q", key),
				Suggestion: "Use one of: " + strings.Join(ids, ", ") + ".",
			}
		}
	}
	return nil
}

// parseInstructionList validates the list for one surface and returns its
// trimmed, non-blank items. The item-count check uses the raw list length and
// the length check uses the raw item, before the trim. The returned slice is
// never nil. Errors name the key harden.instructions.<id> and, for an item, its
// 1-based position.
func parseInstructionList(id string, raw any) ([]string, error) {
	key := "harden.instructions." + id
	list, ok := raw.([]any)
	if !ok {
		return nil, &mcpserver.DomainError{
			Msg:        fmt.Sprintf("%s must be a list of strings", key),
			Suggestion: "Write each item as a quoted TOML string, inside square brackets.",
		}
	}
	if len(list) > hardenInstructionMaxItems {
		return nil, &mcpserver.DomainError{
			Msg:        fmt.Sprintf("%s has %d items (max %d)", key, len(list), hardenInstructionMaxItems),
			Suggestion: fmt.Sprintf("Keep at most %d items for each surface.", hardenInstructionMaxItems),
		}
	}

	items := make([]string, 0, len(list))
	for i, entry := range list {
		text, ok := entry.(string)
		if !ok {
			return nil, &mcpserver.DomainError{
				Msg:        fmt.Sprintf("%s item %d is not a string", key, i+1),
				Suggestion: "Write each item as a quoted TOML string.",
			}
		}
		// The length check runs on the raw item, before the trim, because the
		// JSON Schema maxLength limit counts the raw string.
		if n := utf8.RuneCountInString(text); n > hardenInstructionMaxChars {
			return nil, &mcpserver.DomainError{
				Msg:        fmt.Sprintf("%s item %d has %d characters (max %d)", key, i+1, n, hardenInstructionMaxChars),
				Suggestion: fmt.Sprintf("Shorten the item to %d characters or split it.", hardenInstructionMaxChars),
			}
		}
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		items = append(items, text)
	}
	return items, nil
}
