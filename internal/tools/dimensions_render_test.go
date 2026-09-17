package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/rnagrodzki/sdlc-plugin/internal/paths"
)

// ---------------------------------------------------------------------------
// dimensions_render_instructions: listDimensions mode tests
// ---------------------------------------------------------------------------

func TestDimensionsRender_ListDimensions_MissingDir(t *testing.T) {
	root := t.TempDir()

	out, err := dimensionsRenderInstructions(root, DimensionsRenderInstructionsIn{ListDimensions: true})
	if err != nil {
		t.Fatalf("dimensionsRenderInstructions: %v", err)
	}
	if !out.OK {
		t.Error("expected OK=true when review-dimensions/ does not exist yet")
	}
	if out.Dimensions == nil {
		t.Error("expected Dimensions to be a non-nil empty slice, got nil")
	}
	if len(out.Dimensions) != 0 {
		t.Errorf("expected 0 dimensions, got %v", out.Dimensions)
	}
	if out.Count != 0 {
		t.Errorf("expected Count=0, got %d", out.Count)
	}
	if out.Next == "" {
		t.Error("expected Next to be populated")
	}

	// The JSON encoding must show "[]", never "null" — omitempty is not set
	// on Dimensions, and a nil slice with omitempty absent would encode as
	// "null" if the field were ever left nil.
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !jsonHasEmptyArrayField(t, b, "dimensions") {
		t.Errorf(`expected "dimensions":[] in JSON, got %s`, string(b))
	}
}

func TestDimensionsRender_ListDimensions_ExcludesCommonAndNonMarkdown(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, paths.DataDir, "review-dimensions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	files := map[string]string{
		"security.md": "---\nname: security\ntriggers: [\"**/*.go\"]\n---\nBody\n",
		"a11y.md":     "---\nname: a11y\ntriggers: [\"**/*.tsx\"]\n---\nBody\n",
		"_common.md":  "Shared instructions\n",
		"notes.txt":   "not a dimension file\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	out, err := dimensionsRenderInstructions(root, DimensionsRenderInstructionsIn{ListDimensions: true})
	if err != nil {
		t.Fatalf("dimensionsRenderInstructions: %v", err)
	}
	if !out.OK {
		t.Error("expected OK=true")
	}
	if out.Count != 2 {
		t.Errorf("expected Count=2, got %d (%v)", out.Count, out.Dimensions)
	}
	want := []string{"a11y", "security"}
	if len(out.Dimensions) != len(want) {
		t.Fatalf("expected %v, got %v", want, out.Dimensions)
	}
	for i, w := range want {
		if out.Dimensions[i] != w {
			t.Errorf("Dimensions[%d] = %q, want %q (full: %v)", i, out.Dimensions[i], w, out.Dimensions)
		}
	}
	if out.Path != dir {
		t.Errorf("expected Path=%q, got %q", dir, out.Path)
	}
}

// jsonHasEmptyArrayField reports whether the marshaled JSON contains the
// given field encoded as an empty array (never omitted, never "null").
func jsonHasEmptyArrayField(t *testing.T, b []byte, field string) bool {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	v, ok := m[field]
	if !ok {
		return false
	}
	arr, ok := v.([]any)
	return ok && len(arr) == 0
}

// ---------------------------------------------------------------------------
// dimensions_render_instructions: Next field coverage on existing modes
// ---------------------------------------------------------------------------

func TestDimensionsRender_WriteDimension_SetsNext(t *testing.T) {
	root := t.TempDir()

	out, err := dimensionsRenderInstructions(root, DimensionsRenderInstructionsIn{
		WriteDimension: true,
		Name:           "security",
		Content:        "---\nname: security\ntriggers: [\"**/*.go\"]\n---\nBody\n",
	})
	if err != nil {
		t.Fatalf("dimensionsRenderInstructions: %v", err)
	}
	if !out.OK {
		t.Error("expected OK=true")
	}
	if out.Next == "" {
		t.Error("expected Next to be populated on write mode")
	}
	if out.Dimensions == nil {
		t.Error("expected Dimensions to be a non-nil empty slice on write mode")
	}
}
