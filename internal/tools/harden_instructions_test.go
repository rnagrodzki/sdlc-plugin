package tools

import (
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rnagrodzki/sdlc-plugin/internal/hardensurfaces"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
)

// hardenInstructionsRoot writes config.toml with the given content under a
// new temp root and returns the root. An empty content writes no file.
func hardenInstructionsRoot(t *testing.T, content string) string {
	t.Helper()
	root := t.TempDir()
	if content != "" {
		writeFile(t, filepath.Join(root, paths.DataDir, "config.toml"), content)
	}
	return root
}

// assertAllInstructionKeysEmpty fails unless got holds exactly the keys of
// ProposalIDs(), each mapped to a non-nil empty list.
func assertAllInstructionKeysEmpty(t *testing.T, got map[string][]string) {
	t.Helper()
	ids := hardensurfaces.ProposalIDs()
	if len(got) != len(ids) {
		t.Fatalf("got %d keys, want %d: %v", len(got), len(ids), got)
	}
	for _, id := range ids {
		list, ok := got[id]
		if !ok {
			t.Errorf("key %q missing from %v", id, got)
			continue
		}
		if list == nil || len(list) != 0 {
			t.Errorf("key %q = %#v, want a non-nil empty list", id, list)
		}
	}
}

// TestLoadHardenInstructions_MissingConfigReturnsEmptyLists asserts that a root
// with no config.toml loads without an error and maps every surface id to an
// empty list.
func TestLoadHardenInstructions_MissingConfigReturnsEmptyLists(t *testing.T) {
	got, err := loadHardenInstructions(hardenInstructionsRoot(t, ""))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertAllInstructionKeysEmpty(t, got)
}

// TestLoadHardenInstructions_MissingSectionReturnsEmptyLists asserts that a
// config.toml with no [harden] section, no instructions table, empty lists or
// only blank items loads without an error and leaves every surface list empty.
func TestLoadHardenInstructions_MissingSectionReturnsEmptyLists(t *testing.T) {
	cases := map[string]string{
		"no harden section":       "[plan]\nenabled = true\n",
		"harden without table":    "[harden]\n",
		"empty instructions":      "[harden.instructions]\n",
		"empty lists":             "[harden.instructions]\nplan-guardrails = []\ncopilot-instructions = []\n",
		"only blank items":        "[harden.instructions]\nreview-dimensions = [\"\", \"   \"]\n",
		"blank config.toml lines": "\n\n",
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := loadHardenInstructions(hardenInstructionsRoot(t, content))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			assertAllInstructionKeysEmpty(t, got)
		})
	}
}

// TestLoadHardenInstructions_ReturnsTrimmedItemsInOrder asserts that the loader
// trims each item, drops blank items, keeps the item order, and maps a surface
// with no list to an empty list.
func TestLoadHardenInstructions_ReturnsTrimmedItemsInOrder(t *testing.T) {
	root := hardenInstructionsRoot(t, ""+
		"[harden.instructions]\n"+
		"plan-guardrails = [\"  Prefer error severity.  \", \"\", \"   \", \"Name every rule.\"]\n"+
		"copilot-instructions = [\"Use table-driven tests.\"]\n")

	got, err := loadHardenInstructions(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := map[string][]string{
		"plan-guardrails":      {"Prefer error severity.", "Name every rule."},
		"execute-guardrails":   {},
		"review-dimensions":    {},
		"copilot-instructions": {"Use table-driven tests."},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

// TestLoadHardenInstructions_ItemLimits asserts the item limits of one surface
// list. An item of exactly hardenInstructionMaxChars raw characters loads,
// padded or not. A padded item over the limit in raw characters returns a
// DomainError with a Suggestion, even when its trimmed text fits. A list of
// exactly hardenInstructionMaxItems items loads.
func TestLoadHardenInstructions_ItemLimits(t *testing.T) {
	atMaxChars := strings.Repeat("a", hardenInstructionMaxChars)
	atMaxRunes := strings.Repeat("é", hardenInstructionMaxChars) // 2 bytes each: counts in characters, not bytes
	// The limit applies to the raw item: 2 spaces + 1020 characters + 2 spaces is exactly 1024.
	paddedAtMax := "  " + strings.Repeat("a", hardenInstructionMaxChars-4) + "  "
	// Trimmed text is exactly 1024 characters, but the raw item is 1028.
	paddedOverMax := "  " + atMaxChars + "  "

	t.Run("item at the limit loads", func(t *testing.T) {
		for name, item := range map[string]string{"ascii": atMaxChars, "multibyte": atMaxRunes, "padded": paddedAtMax} {
			root := hardenInstructionsRoot(t, fmt.Sprintf("[harden.instructions]\nplan-guardrails = [%q]\n", item))
			got, err := loadHardenInstructions(root)
			if err != nil {
				t.Fatalf("%s: unexpected error: %v", name, err)
			}
			if want := strings.TrimSpace(item); len(got["plan-guardrails"]) != 1 || got["plan-guardrails"][0] != want {
				t.Fatalf("%s: item changed or dropped", name)
			}
		}
	})

	t.Run("padded item over the limit in raw characters is rejected", func(t *testing.T) {
		root := hardenInstructionsRoot(t, fmt.Sprintf("[harden.instructions]\nplan-guardrails = [%q]\n", paddedOverMax))
		got, err := loadHardenInstructions(root)
		if err == nil {
			t.Fatalf("expected a DomainError, got nil and %v", got)
		}
		var domainErr *mcpserver.DomainError
		if !errors.As(err, &domainErr) {
			t.Fatalf("expected *mcpserver.DomainError, got %T: %v", err, err)
		}
		if want := "harden.instructions.plan-guardrails item 1 has 1028 characters (max 1024)"; domainErr.Msg != want {
			t.Errorf("Msg = %q, want %q", domainErr.Msg, want)
		}
		if domainErr.Suggestion == "" {
			t.Error("Suggestion is empty, want a recovery text")
		}
	})

	t.Run("count at the limit loads", func(t *testing.T) {
		items := make([]string, hardenInstructionMaxItems)
		for i := range items {
			items[i] = fmt.Sprintf("%q", fmt.Sprintf("item %d", i))
		}
		root := hardenInstructionsRoot(t, "[harden.instructions]\nreview-dimensions = ["+strings.Join(items, ", ")+"]\n")
		got, err := loadHardenInstructions(root)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got["review-dimensions"]) != hardenInstructionMaxItems {
			t.Fatalf("got %d items, want %d", len(got["review-dimensions"]), hardenInstructionMaxItems)
		}
	})
}

// TestLoadHardenInstructions_InvalidInputReturnsDomainError asserts that each
// invalid input (unknown key, item over the character limit, too many items,
// non-string item, value that is not a list, instructions that is not a table)
// returns a nil map and a DomainError with the expected message and Suggestion.
func TestLoadHardenInstructions_InvalidInputReturnsDomainError(t *testing.T) {
	tooLong := strings.Repeat("a", hardenInstructionMaxChars+1)
	tooLongRunes := strings.Repeat("é", hardenInstructionMaxChars+1)
	elevenItems := make([]string, hardenInstructionMaxItems+1)
	for i := range elevenItems {
		elevenItems[i] = fmt.Sprintf("%q", fmt.Sprintf("item %d", i))
	}

	cases := []struct {
		name       string
		config     string
		wantMsg    string
		wantSuggst string
	}{
		{
			name:       "unknown key",
			config:     "[harden.instructions]\nx = [\"a\"]\n",
			wantMsg:    `harden.instructions has unknown key "x"`,
			wantSuggst: "Use one of: " + strings.Join(hardensurfaces.ProposalIDs(), ", ") + ".",
		},
		{
			name:       "known key beside an unknown key",
			config:     "[harden.instructions]\nplan-guardrails = [\"a\"]\nerror-report-skill = [\"a\"]\n",
			wantMsg:    `harden.instructions has unknown key "error-report-skill"`,
			wantSuggst: "Use one of: " + strings.Join(hardensurfaces.ProposalIDs(), ", ") + ".",
		},
		{
			name:       "item over the character limit",
			config:     fmt.Sprintf("[harden.instructions]\nexecute-guardrails = [\"ok\", %q]\n", tooLong),
			wantMsg:    "harden.instructions.execute-guardrails item 2 has 1025 characters (max 1024)",
			wantSuggst: "Shorten the item to 1024 characters or split it.",
		},
		{
			name:       "multibyte item over the character limit",
			config:     fmt.Sprintf("[harden.instructions]\nplan-guardrails = [%q]\n", tooLongRunes),
			wantMsg:    "harden.instructions.plan-guardrails item 1 has 1025 characters (max 1024)",
			wantSuggst: "Shorten the item to 1024 characters or split it.",
		},
		{
			name:       "more than the item limit",
			config:     "[harden.instructions]\nreview-dimensions = [" + strings.Join(elevenItems, ", ") + "]\n",
			wantMsg:    "harden.instructions.review-dimensions has 11 items (max 10)",
			wantSuggst: "Keep at most 10 items for each surface.",
		},
		{
			name:       "non-string item",
			config:     "[harden.instructions]\ncopilot-instructions = [\"ok\", 7]\n",
			wantMsg:    "harden.instructions.copilot-instructions item 2 is not a string",
			wantSuggst: "Write each item as a quoted TOML string.",
		},
		{
			name:       "nested list item",
			config:     "[harden.instructions]\nplan-guardrails = [[\"a\"]]\n",
			wantMsg:    "harden.instructions.plan-guardrails item 1 is not a string",
			wantSuggst: "Write each item as a quoted TOML string.",
		},
		{
			name:       "value is not a list",
			config:     "[harden.instructions]\nplan-guardrails = \"Prefer error severity.\"\n",
			wantMsg:    "harden.instructions.plan-guardrails must be a list of strings",
			wantSuggst: "Write each item as a quoted TOML string, inside square brackets.",
		},
		{
			name:       "instructions is not a table",
			config:     "[harden]\ninstructions = \"x\"\n",
			wantMsg:    "harden.instructions must be a table",
			wantSuggst: "Write [harden.instructions] as a TOML table with one list for each surface.",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := loadHardenInstructions(hardenInstructionsRoot(t, tc.config))
			if err == nil {
				t.Fatalf("expected a DomainError, got nil and %v", got)
			}
			if got != nil {
				t.Errorf("expected a nil map with the error, got %v", got)
			}
			var domainErr *mcpserver.DomainError
			if !errors.As(err, &domainErr) {
				t.Fatalf("expected *mcpserver.DomainError, got %T: %v", err, err)
			}
			if domainErr.Msg != tc.wantMsg {
				t.Errorf("Msg = %q, want %q", domainErr.Msg, tc.wantMsg)
			}
			if domainErr.Suggestion != tc.wantSuggst {
				t.Errorf("Suggestion = %q, want %q", domainErr.Suggestion, tc.wantSuggst)
			}
		})
	}
}

// TestLoadHardenInstructions_ReadErrorReturnsInfraError asserts that a
// config.toml with a syntax error returns a nil map and an InfraError that
// carries a Suggestion, a message and the read error as its cause.
func TestLoadHardenInstructions_ReadErrorReturnsInfraError(t *testing.T) {
	root := hardenInstructionsRoot(t, "[harden.instructions\nplan-guardrails = [\n")

	got, err := loadHardenInstructions(root)
	if err == nil {
		t.Fatalf("expected an InfraError, got nil and %v", got)
	}
	if got != nil {
		t.Errorf("expected a nil map with the error, got %v", got)
	}
	var infraErr *mcpserver.InfraError
	if !errors.As(err, &infraErr) {
		t.Fatalf("expected *mcpserver.InfraError, got %T: %v", err, err)
	}
	if want := "Fix the permission or syntax of .sdlc-v2/config.toml, then run harden again."; infraErr.Suggestion != want {
		t.Errorf("Suggestion = %q, want %q", infraErr.Suggestion, want)
	}
	if infraErr.Cause == nil {
		t.Error("Cause is nil, want the config read error")
	}
	if infraErr.Msg == "" {
		t.Error("Msg is empty")
	}
}
