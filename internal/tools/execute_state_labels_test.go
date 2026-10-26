package tools

import (
	"reflect"
	"strings"
	"testing"

	"github.com/rnagrodzki/sdlc-plugin/internal/config"
)

// TestLocalFieldDescriptions_NameBothLocalFiles pins that every output field
// description that names the local files uses the config.LocalFilesLabel
// text. A struct tag cannot reference the constant, so this test keeps the
// literal copies equal to it: config.ReadSection reads both files.
func TestLocalFieldDescriptions_NameBothLocalFiles(t *testing.T) {
	tests := []struct {
		typ   reflect.Type
		field string
	}{
		{reflect.TypeOf(ExecuteResolveConfigOut{}), "Auto"},
		{reflect.TypeOf(ExecuteResolveConfigOut{}), "Quality"},
		{reflect.TypeOf(ExecuteResolveConfigOut{}), "HighRiskAutoApprove"},
		{reflect.TypeOf(ExecuteResolveConfigOut{}), "Sources"},
		{reflect.TypeOf(ReceivedReviewOut{}), "AlwaysFixSeverities"},
	}
	for _, tt := range tests {
		t.Run(tt.typ.Name()+"."+tt.field, func(t *testing.T) {
			f, ok := tt.typ.FieldByName(tt.field)
			if !ok {
				t.Fatalf("%s has no field %s", tt.typ.Name(), tt.field)
			}
			desc := f.Tag.Get("jsonschema_description")
			if !strings.Contains(desc, config.LocalFilesLabel) {
				t.Errorf("%s.%s description does not contain %q:\n%s", tt.typ.Name(), tt.field, config.LocalFilesLabel, desc)
			}
		})
	}
}
