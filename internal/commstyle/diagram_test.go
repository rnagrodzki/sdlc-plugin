package commstyle

import (
	"fmt"
	"strings"
	"testing"
)

func TestContrastRatio(t *testing.T) {
	cases := []struct {
		fill, color string
		want        float64
		ok          bool
	}{
		{"#1f7a3a", "#ffffff", 5.4, true},
		{"#8a6d00", "#ffffff", 4.9, true},
		{"#b8860b", "#ffffff", 3.3, true},
		{"#fff", "#000", 21.0, true},
		{"red", "#ffffff", 0, false},
		{"#ffffff", "rgb(0,0,0)", 0, false},
	}
	for _, c := range cases {
		t.Run(c.fill+"/"+c.color, func(t *testing.T) {
			got, ok := contrastRatio(c.fill, c.color)
			if ok != c.ok {
				t.Fatalf("contrastRatio(%q, %q) ok = %v, want %v", c.fill, c.color, ok, c.ok)
			}
			if !ok {
				return
			}
			rounded := fmt.Sprintf("%.1f", got)
			want := fmt.Sprintf("%.1f", c.want)
			if rounded != want {
				t.Errorf("contrastRatio(%q, %q) = %s, want %s", c.fill, c.color, rounded, want)
			}
		})
	}
}

func TestContrastHitsInLines(t *testing.T) {
	t.Run("fill with no color is a hit", func(t *testing.T) {
		hits := ContrastHitsInLines("classDef new fill:#d4f7d4,stroke:#2a7a2a")
		if len(hits) != 1 {
			t.Fatalf("got %d hits, want 1: %+v", len(hits), hits)
		}
		if hits[0].Reason != "no text color" {
			t.Errorf("Reason = %q, want %q", hits[0].Reason, "no text color")
		}
		if hits[0].Line != 1 {
			t.Errorf("Line = %d, want 1", hits[0].Line)
		}
	})

	t.Run("low contrast fill and color is a hit with the ratio in the reason", func(t *testing.T) {
		hits := ContrastHitsInLines("classDef changed fill:#b8860b,color:#ffffff")
		if len(hits) != 1 {
			t.Fatalf("got %d hits, want 1: %+v", len(hits), hits)
		}
		if want := "contrast 3.3:1 < 4.5:1"; hits[0].Reason != want {
			t.Errorf("Reason = %q, want %q", hits[0].Reason, want)
		}
	})

	t.Run("passing contrast is not a hit", func(t *testing.T) {
		hits := ContrastHitsInLines(ClassDefNew)
		if len(hits) != 0 {
			t.Errorf("got %d hits, want 0: %+v", len(hits), hits)
		}
	})

	t.Run("non-hex color is not a hit (no ratio check)", func(t *testing.T) {
		for _, line := range []string{
			"classDef new fill:#d4f7d4,color:red",
			"style A fill:#d4f7d4,color:rgb(0,0,0)",
		} {
			hits := ContrastHitsInLines(line)
			if len(hits) != 0 {
				t.Errorf("ContrastHitsInLines(%q) = %+v, want no hits", line, hits)
			}
		}
	})

	t.Run("finds a classDef line with no fence around it", func(t *testing.T) {
		hits := ContrastHitsInLines("classDef new fill:#d4f7d4")
		if len(hits) != 1 {
			t.Fatalf("got %d hits, want 1: %+v", len(hits), hits)
		}
	})

	t.Run("ignores prose that mentions style and fill", func(t *testing.T) {
		hits := ContrastHitsInLines("The style of the fill: is bad")
		if len(hits) != 0 {
			t.Errorf("got %d hits, want 0: %+v", len(hits), hits)
		}
	})

	t.Run("result is [] never nil", func(t *testing.T) {
		hits := ContrastHitsInLines("")
		if hits == nil {
			t.Error("ContrastHitsInLines returned nil, want []")
		}
		if len(hits) != 0 {
			t.Errorf("got %d hits, want 0", len(hits))
		}
	})

	t.Run("drift: the shared classDefs never trip the check", func(t *testing.T) {
		hits := ContrastHitsInLines(ClassDefNew + "\n" + ClassDefChanged)
		if len(hits) != 0 {
			t.Errorf("got %d hits for ClassDefNew/ClassDefChanged, want 0: %+v", len(hits), hits)
		}

		guide := Guide(FromSections(nil, nil))
		if !strings.Contains(guide, ClassDefNew) {
			t.Error("Guide(FromSections(nil, nil)) does not contain ClassDefNew verbatim")
		}
		if !strings.Contains(guide, ClassDefChanged) {
			t.Error("Guide(FromSections(nil, nil)) does not contain ClassDefChanged verbatim")
		}
	})
}

func TestMermaidContrast(t *testing.T) {
	t.Run("only lines inside a mermaid fence are checked", func(t *testing.T) {
		content := "classDef new fill:#d4f7d4\n" +
			"```mermaid\n" +
			"graph TD\n" +
			"classDef new fill:#d4f7d4,stroke:#2a7a2a\n" +
			"```\n" +
			"classDef changed fill:#b8860b,color:#ffffff\n"
		hits := MermaidContrast(content)
		if len(hits) != 1 {
			t.Fatalf("got %d hits, want 1: %+v", len(hits), hits)
		}
		if hits[0].Line != 4 {
			t.Errorf("Line = %d, want 4", hits[0].Line)
		}
		if hits[0].Reason != "no text color" {
			t.Errorf("Reason = %q, want %q", hits[0].Reason, "no text color")
		}
	})

	t.Run("a fence tagged with another language is ignored", func(t *testing.T) {
		content := "```go\n" +
			"classDef new fill:#d4f7d4\n" +
			"```\n"
		hits := MermaidContrast(content)
		if len(hits) != 0 {
			t.Errorf("got %d hits, want 0: %+v", len(hits), hits)
		}
	})

	t.Run("the shared classDefs pass inside a mermaid fence", func(t *testing.T) {
		content := "```mermaid\ngraph TD\n" + ClassDefNew + "\n" + ClassDefChanged + "\n```\n"
		hits := MermaidContrast(content)
		if len(hits) != 0 {
			t.Errorf("got %d hits, want 0: %+v", len(hits), hits)
		}
	})

	t.Run("result is [] never nil", func(t *testing.T) {
		hits := MermaidContrast("")
		if hits == nil {
			t.Error("MermaidContrast returned nil, want []")
		}
	})
}
