package openspec

import (
	"reflect"
	"testing"
)

func TestCheckPlanHeaders(t *testing.T) {
	const filledAppendix = "## OpenSpec Appendix\n**Staging:** `/r/.sdlc-v2/openspec-staging/x/`\n| Requirement | Spec file | Covering task(s) |\n"
	tests := []struct {
		name string
		plan string
		want []PlanHeaderProblem
	}{
		{
			name: "no openspec header",
			plan: "# Plan\n\n**Source:** conversation context\n\n## OpenSpec Appendix\n\nNot applicable — no OpenSpec change\n",
			want: nil,
		},
		{
			name: "source only, appendix TBD",
			plan: "**Source:** openspec/changes/x/\n\n## OpenSpec Appendix\n\n[TBD]\n",
			want: nil,
		},
		{
			name: "create line only",
			plan: "**Source:** openspec/changes/x/\n**OpenSpec-Create:** x\n\n## Goal\n",
			want: []PlanHeaderProblem{{Kind: PlanHeaderCreateNotAuthored, Change: "x", Line: "**OpenSpec-Create:** x"}},
		},
		{
			name: "create line with CRLF",
			plan: "**OpenSpec-Create:** x\r\n\r\n## Goal\r\n",
			want: []PlanHeaderProblem{{Kind: PlanHeaderCreateNotAuthored, Change: "x", Line: "**OpenSpec-Create:** x"}},
		},
		{
			name: "create line, saved line",
			plan: "**OpenSpec-Create:** x\n**OpenSpec-Saved:** openspec/changes/x/ (branch openspec/x)\n",
			want: nil,
		},
		{
			name: "staging with filled appendix",
			plan: "**OpenSpec-Staging:** .sdlc-v2/openspec-staging/x/\n\n" + filledAppendix,
			want: nil,
		},
		{
			name: "staging with TBD appendix",
			plan: "**OpenSpec-Staging:** .sdlc-v2/openspec-staging/x/\n\n## OpenSpec Appendix\n\n[TBD — authored at the end of Step 6]\n\n## Next\n",
			want: []PlanHeaderProblem{{Kind: PlanHeaderAppendixTBD, Change: "x", Line: "[TBD — authored at the end of Step 6]"}},
		},
		{
			name: "create and staging, TBD appendix",
			plan: "**OpenSpec-Create:** y\n**OpenSpec-Staging:** .sdlc-v2/openspec-staging/x/\n\n## OpenSpec Appendix\n[TBD]\n",
			want: []PlanHeaderProblem{{Kind: PlanHeaderAppendixTBD, Change: "x", Line: "[TBD]"}},
		},
		{
			name: "staging with TBD appendix and saved line",
			plan: "**OpenSpec-Staging:** .sdlc-v2/openspec-staging/x/\n**OpenSpec-Saved:** openspec/changes/x/\n\n## OpenSpec Appendix\n[TBD]\n",
			want: nil,
		},
		{
			name: "TBD text in a later section is not the appendix",
			plan: "**OpenSpec-Staging:** .sdlc-v2/openspec-staging/x/\n\n## OpenSpec Appendix\n| a | b |\n\n## Notes\n[TBD]\n",
			want: nil,
		},
		{
			name: "empty appendix body before next heading",
			plan: "**OpenSpec-Staging:** .sdlc-v2/openspec-staging/x/\n\n## OpenSpec Appendix\n\n## Notes\n[TBD]\n",
			want: nil,
		},
		{
			name: "staging without appendix heading",
			plan: "**OpenSpec-Staging:** .sdlc-v2/openspec-staging/x/\n\n## Goal\n[TBD]\n",
			want: nil,
		},
		{
			name: "create line in a backtick fence",
			plan: "# Plan\n\n```\n**OpenSpec-Create:** x\n```\n",
			want: nil,
		},
		{
			name: "create line in a tilde fence",
			plan: "~~~md\n**OpenSpec-Create:** x\n~~~\n",
			want: nil,
		},
		{
			name: "create line in a nested fence",
			plan: "````md\n```\n**OpenSpec-Create:** x\n```\n````\n",
			want: nil,
		},
		{
			name: "create line in an unclosed fence",
			plan: "```\n**OpenSpec-Create:** x\n",
			want: nil,
		},
		{
			name: "create line after a closed fence",
			plan: "```\ncode\n```\n**OpenSpec-Create:** x\n",
			want: []PlanHeaderProblem{{Kind: PlanHeaderCreateNotAuthored, Change: "x", Line: "**OpenSpec-Create:** x"}},
		},
		{
			name: "saved line in a fence does not excuse the create line",
			plan: "**OpenSpec-Create:** x\n\n```\n**OpenSpec-Saved:** openspec/changes/x/\n```\n",
			want: []PlanHeaderProblem{{Kind: PlanHeaderCreateNotAuthored, Change: "x", Line: "**OpenSpec-Create:** x"}},
		},
		{
			name: "TBD appendix inside a fence is ignored",
			plan: "**OpenSpec-Staging:** .sdlc-v2/openspec-staging/x/\n\n## OpenSpec Appendix\n\n```\n[TBD]\n```\n| a | b |\n",
			want: nil,
		},
		{
			name: "header mentioned in prose is not a header",
			plan: "Use **OpenSpec-Create:** x to start.\n",
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CheckPlanHeaders(tt.plan)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("CheckPlanHeaders() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestCreateChangeFromPlan(t *testing.T) {
	tests := []struct {
		name       string
		plan       string
		wantChange string
		wantOK     bool
	}{
		{"plain line", "**Source:** openspec/changes/add-widget/\n**OpenSpec-Create:** add-widget\n", "add-widget", true},
		{"extra spaces and CRLF", "**OpenSpec-Create:**   add-widget  \r\n", "add-widget", true},
		{"no header", "**Source:** conversation context\n", "", false},
		{"staging header only", "**OpenSpec-Staging:** .sdlc-v2/openspec-staging/x/\n", "", false},
		{"line in a fence", "```\n**OpenSpec-Create:** x\n```\n", "", false},
		{"name returned as written", "**OpenSpec-Create:** Bad_Name\n", "Bad_Name", true},
		{"empty name", "**OpenSpec-Create:**\n", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			change, ok := CreateChangeFromPlan(tt.plan)
			if change != tt.wantChange || ok != tt.wantOK {
				t.Errorf("CreateChangeFromPlan() = (%q, %v), want (%q, %v)", change, ok, tt.wantChange, tt.wantOK)
			}
		})
	}
}

func TestSavedHeaderRe(t *testing.T) {
	tests := []struct {
		name       string
		plan       string
		wantChange string
		wantBranch string
		wantMatch  bool
	}{
		{"with branch", "**OpenSpec-Saved:** openspec/changes/x/ (branch openspec/x)\n", "x", "openspec/x", true},
		{"no branch, no slash", "**OpenSpec-Saved:** openspec/changes/x\n", "x", "", true},
		{"CRLF", "**OpenSpec-Saved:** openspec/changes/x/\r\n", "x", "", true},
		{"staging line", "**OpenSpec-Staging:** .sdlc-v2/openspec-staging/x/\n", "", "", false},
		{"trailing prose", "**OpenSpec-Saved:** openspec/changes/x/ done\n", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := SavedHeaderRe.FindStringSubmatch(tt.plan)
			if (m != nil) != tt.wantMatch {
				t.Fatalf("match = %v, want %v", m != nil, tt.wantMatch)
			}
			if m == nil {
				return
			}
			if m[1] != tt.wantChange || m[2] != tt.wantBranch {
				t.Errorf("groups = (%q, %q), want (%q, %q)", m[1], m[2], tt.wantChange, tt.wantBranch)
			}
		})
	}
}
