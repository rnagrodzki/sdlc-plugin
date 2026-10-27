package dimensions

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestValidSeverities_MatchLocalSchemaEnum pins the
// receivedReview.alwaysFixSeverities item enum in sdlc-local.schema.json to
// ValidSeverities. received_review_prepare validates the setting against
// ValidSeverities, so a schema enum that drifts from it rejects (or accepts)
// a value the tool treats the other way.
func TestValidSeverities_MatchLocalSchemaEnum(t *testing.T) {
	path := filepath.Join("..", "..", "plugins", "sdlc", "schemas", "sdlc-local.schema.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var doc struct {
		Defs struct {
			ReceivedReviewSection struct {
				Properties struct {
					AlwaysFixSeverities struct {
						Items struct {
							Enum []string `json:"enum"`
						} `json:"items"`
					} `json:"alwaysFixSeverities"`
				} `json:"properties"`
			} `json:"receivedReviewSection"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	got := doc.Defs.ReceivedReviewSection.Properties.AlwaysFixSeverities.Items.Enum
	if len(got) != len(ValidSeverities) {
		t.Fatalf("schema alwaysFixSeverities enum = %v, want ValidSeverities %v", got, ValidSeverities)
	}
	for i := range got {
		if got[i] != ValidSeverities[i] {
			t.Fatalf("schema alwaysFixSeverities enum = %v, want ValidSeverities %v", got, ValidSeverities)
		}
	}
}
