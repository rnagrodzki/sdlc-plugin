package tools

import (
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"testing"
)

// ---------------------------------------------------------------------------
// parseDependsOnRefs
//
// parseDependsOnRefs (validators.go) is the single shared parser for a plan
// task's **Depends on:** field, used by both checkPF4 (plan validation) and
// waveComputeParseDependsOn (wave scheduling). These tests exercise the
// parser directly against the field value; TestWaveComputeAndPF4AgreeOnDependsOn
// below confirms both call sites agree on the same input.
// ---------------------------------------------------------------------------

func TestParseDependsOnRefs(t *testing.T) {
	cases := []struct {
		name  string
		field string
		want  []int
	}{
		{"tasks list of three", "Tasks 6, 7, 8", []int{6, 7, 8}},
		{"task list of two", "Task 2, 3", []int{2, 3}},
		{"and joined", "Task 2 and Task 3", []int{2, 3}},
		{"parenthetical stops extraction", "Task 2 (needs Foo from line 42)", []int{2}},
		{"none", "none", nil},
		{"empty", "", nil},
		{"single task", "Task 5", []int{5}},
		{"tasks with oxford and", "Tasks 6, 7, and 8", []int{6, 7, 8}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseDependsOnRefs(tc.field)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("parseDependsOnRefs(%q) = %v, want %v", tc.field, got, tc.want)
			}
		})
	}
}

// nonexistentTaskRefRe extracts the referenced task number out of checkPF4's
// "Task %d: depends on nonexistent Task %d" issue message.
var nonexistentTaskRefRe = regexp.MustCompile(`depends on nonexistent Task (\d+)`)

// sortedInts normalizes a possibly-nil []int into a sorted, non-nil slice so
// reflect.DeepEqual compares two ref lists by content only, not by
// nil-vs-empty representation or order.
func sortedInts(in []int) []int {
	out := append([]int{}, in...)
	sort.Ints(out)
	return out
}

// TestWaveComputeAndPF4AgreeOnDependsOn confirms checkPF4 and
// waveComputeParseDependsOn - which both delegate to parseDependsOnRefs -
// extract identical task-number references from the same **Depends on:**
// field, for every form exercised in TestParseDependsOnRefs. Task 1 is the
// only task in the fixture, so checkPF4 flags every parsed reference as
// "nonexistent" - its issue messages reveal exactly which numbers it parsed.
func TestWaveComputeAndPF4AgreeOnDependsOn(t *testing.T) {
	fields := []string{
		"Tasks 6, 7, 8",
		"Task 2, 3",
		"Task 2 and Task 3",
		"Task 2 (needs Foo from line 42)",
		"none",
		"Task 5",
		"Tasks 6, 7, and 8",
	}

	for _, field := range fields {
		body := "**Depends on:** " + field + "\n"

		check := checkPF4([]planTask{{Number: 1, Title: "Solo", Body: body}})
		var pf4Refs []int
		for _, m := range nonexistentTaskRefRe.FindAllStringSubmatch(check.message, -1) {
			n, _ := strconv.Atoi(m[1])
			pf4Refs = append(pf4Refs, n)
		}

		waveRefs := waveComputeParseDependsOn(body)

		if got, want := sortedInts(pf4Refs), sortedInts(waveRefs); !reflect.DeepEqual(got, want) {
			t.Errorf("field %q: checkPF4 refs = %v, waveComputeParseDependsOn refs = %v", field, got, want)
		}
	}
}
