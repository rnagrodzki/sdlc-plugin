package jirakeys

import (
	"reflect"
	"testing"
)

func TestExtract(t *testing.T) {
	tests := []struct {
		name string
		text string
		want []string
	}{
		{
			name: "empty string",
			text: "",
			want: nil,
		},
		{
			name: "no keys",
			text: "this is plain text without any keys",
			want: nil,
		},
		{
			name: "single key",
			text: "Fixed PROJ-123 in this commit",
			want: []string{"PROJ-123"},
		},
		{
			name: "multiple unique keys",
			text: "PROJ-1 and PROJ-2 and TEAM-99",
			want: []string{"PROJ-1", "PROJ-2", "TEAM-99"},
		},
		{
			name: "duplicates preserve first occurrence",
			text: "PROJ-1 then TEAM-2 then PROJ-1 again",
			want: []string{"PROJ-1", "TEAM-2"},
		},
		{
			name: "two-letter prefix minimum",
			text: "AB-1 is valid",
			want: []string{"AB-1"},
		},
		{
			name: "ten-letter prefix maximum",
			text: "ABCDEFGHIJ-999 is valid",
			want: []string{"ABCDEFGHIJ-999"},
		},
		{
			name: "eleven-letter prefix rejected",
			text: "ABCDEFGHIJK-1 is too long",
			want: nil,
		},
		{
			name: "single-letter prefix rejected",
			text: "A-1 is too short",
			want: nil,
		},
		{
			name: "lowercase rejected",
			text: "proj-123 is lowercase",
			want: nil,
		},
		{
			name: "digits in prefix rejected",
			text: "AB2-1 has a digit in prefix",
			want: nil,
		},
		{
			name: "underscore in prefix rejected",
			text: "A_B-3 has underscore in prefix",
			want: nil,
		},
		{
			name: "embedded in longer token rejected by word boundary",
			text: "xPROJ-123y no match",
			want: nil,
		},
		{
			name: "multiline text",
			text: "line one PROJ-1\nline two TEAM-5\n",
			want: []string{"PROJ-1", "TEAM-5"},
		},
		{
			name: "key at start and end of line",
			text: "PROJ-1 some text TEAM-2",
			want: []string{"PROJ-1", "TEAM-2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Extract(tt.text)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Extract(%q) = %v, want %v", tt.text, got, tt.want)
			}
		})
	}
}
