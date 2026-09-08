package wave

import (
	"math/rand"
	"reflect"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Table-driven ComputeWaves tests
// ---------------------------------------------------------------------------

func TestComputeWaves(t *testing.T) {
	tests := []struct {
		name    string
		input   ComputeInput
		wantErr string // substring match; empty = no error expected
		check   func(t *testing.T, out *ComputeOutput)
	}{
		{
			name:  "empty input returns direct",
			input: ComputeInput{},
			check: func(t *testing.T, out *ComputeOutput) {
				if out.Route != "direct" {
					t.Errorf("Route = %q, want %q", out.Route, "direct")
				}
				if len(out.Waves) != 0 {
					t.Errorf("Waves = %d, want 0", len(out.Waves))
				}
				if len(out.PreWave) != 0 {
					t.Errorf("PreWave = %d, want 0", len(out.PreWave))
				}
			},
		},
		{
			name: "direct route: 3 trivial/standard tasks, no high risk",
			input: ComputeInput{
				Tasks: []TaskInput{
					{Number: 1, Title: "t1", Complexity: "Trivial", Risk: "Low"},
					{Number: 2, Title: "t2", Complexity: "Standard", Risk: "Medium"},
					{Number: 3, Title: "t3", Complexity: "Standard", Risk: "Low"},
				},
			},
			check: func(t *testing.T, out *ComputeOutput) {
				if out.Route != "direct" {
					t.Errorf("Route = %q, want %q", out.Route, "direct")
				}
			},
		},
		{
			name: "3 tasks but High risk forces waves",
			input: ComputeInput{
				Tasks: []TaskInput{
					{Number: 1, Title: "t1", Complexity: "Trivial", Risk: "High"},
					{Number: 2, Title: "t2", Complexity: "Standard", Risk: "Low"},
					{Number: 3, Title: "t3", Complexity: "Standard", Risk: "Low"},
				},
			},
			check: func(t *testing.T, out *ComputeOutput) {
				if out.Route != "waves" {
					t.Errorf("Route = %q, want %q", out.Route, "waves")
				}
			},
		},
		{
			name: "3 tasks but Complex forces waves",
			input: ComputeInput{
				Tasks: []TaskInput{
					{Number: 1, Title: "t1", Complexity: "Complex", Risk: "Low"},
					{Number: 2, Title: "t2", Complexity: "Standard", Risk: "Low"},
					{Number: 3, Title: "t3", Complexity: "Standard", Risk: "Low"},
				},
			},
			check: func(t *testing.T, out *ComputeOutput) {
				if out.Route != "waves" {
					t.Errorf("Route = %q, want %q", out.Route, "waves")
				}
			},
		},
		{
			name: "cycle detection",
			input: ComputeInput{
				Tasks: []TaskInput{
					{Number: 1, Title: "t1", Complexity: "Standard", Risk: "Low", DependsOn: []int{2}},
					{Number: 2, Title: "t2", Complexity: "Standard", Risk: "Low", DependsOn: []int{1}},
				},
			},
			wantErr: "cycle",
		},
		{
			name: "self-dependency error",
			input: ComputeInput{
				Tasks: []TaskInput{
					{Number: 1, Title: "t1", Complexity: "Standard", Risk: "Low", DependsOn: []int{1}},
				},
			},
			wantErr: "self-dependency",
		},
		{
			name: "duplicate task number error",
			input: ComputeInput{
				Tasks: []TaskInput{
					{Number: 1, Title: "t1", Complexity: "Standard", Risk: "Low"},
					{Number: 1, Title: "t1-dup", Complexity: "Standard", Risk: "Low"},
				},
			},
			wantErr: "duplicate",
		},
		{
			name: "unknown dependency error",
			input: ComputeInput{
				Tasks: []TaskInput{
					{Number: 1, Title: "t1", Complexity: "Standard", Risk: "Low", DependsOn: []int{99}},
				},
			},
			wantErr: "unknown task 99",
		},
		{
			name: "ExtraDep unknown task produces soft error",
			input: ComputeInput{
				Tasks: []TaskInput{
					{Number: 1, Title: "t1", Complexity: "Standard", Risk: "Low"},
					{Number: 2, Title: "t2", Complexity: "Standard", Risk: "Low"},
					{Number: 3, Title: "t3", Complexity: "Standard", Risk: "Low"},
					{Number: 4, Title: "t4", Complexity: "Standard", Risk: "Low"},
				},
				ExtraDeps: []ExtraDep{
					{Task: 99, DependsOn: 1, Reason: "ghost"},
				},
			},
			check: func(t *testing.T, out *ComputeOutput) {
				if len(out.Errors) == 0 {
					t.Errorf("expected soft error for unknown ExtraDep task, got none")
				}
				found := false
				for _, e := range out.Errors {
					if strings.Contains(e, "unknown task 99") {
						found = true
					}
				}
				if !found {
					t.Errorf("expected soft error mentioning task 99, got %v", out.Errors)
				}
			},
		},
		{
			name: "ExtraDep changes wave assignment",
			input: ComputeInput{
				Tasks: []TaskInput{
					{Number: 1, Title: "t1", Complexity: "Standard", Risk: "Low"},
					{Number: 2, Title: "t2", Complexity: "Standard", Risk: "Low"},
					{Number: 3, Title: "t3", Complexity: "Standard", Risk: "Low"},
					{Number: 4, Title: "t4", Complexity: "Standard", Risk: "Low"},
				},
				ExtraDeps: []ExtraDep{
					{Task: 2, DependsOn: 1, Reason: "file conflict"},
				},
			},
			check: func(t *testing.T, out *ComputeOutput) {
				if out.Route != "waves" {
					t.Errorf("Route = %q, want %q", out.Route, "waves")
				}
				// Task 2 must be in a later wave than task 1.
				w1 := waveContaining(out, 1)
				w2 := waveContaining(out, 2)
				if w2 <= w1 {
					t.Errorf("task 2 (wave %d) should be after task 1 (wave %d) due to ExtraDep", w2, w1)
				}
			},
		},
		{
			name: "same-file constraint bumps later task",
			input: ComputeInput{
				Tasks: []TaskInput{
					{Number: 1, Title: "t1", Complexity: "Standard", Risk: "Low", Files: []string{"shared.go"}},
					{Number: 2, Title: "t2", Complexity: "Standard", Risk: "Low", Files: []string{"shared.go"}},
					{Number: 3, Title: "t3", Complexity: "Standard", Risk: "Low", Files: []string{"other.go"}},
					{Number: 4, Title: "t4", Complexity: "Standard", Risk: "Low", Files: []string{"another.go"}},
				},
			},
			check: func(t *testing.T, out *ComputeOutput) {
				w1 := waveContaining(out, 1)
				w2 := waveContaining(out, 2)
				if w1 == w2 {
					t.Errorf("tasks 1 and 2 share a file but ended up in the same wave %d", w1)
				}
			},
		},
		{
			name: "cascading same-file bump through dependency",
			input: ComputeInput{
				Tasks: []TaskInput{
					{Number: 1, Title: "t1", Complexity: "Standard", Risk: "Low", Files: []string{"a.go"}},
					{Number: 2, Title: "t2", Complexity: "Standard", Risk: "Low", Files: []string{"a.go"}},
					{Number: 3, Title: "t3", Complexity: "Standard", Risk: "Low", DependsOn: []int{2}, Files: []string{"b.go"}},
					{Number: 4, Title: "t4", Complexity: "Standard", Risk: "Low", Files: []string{"c.go"}},
				},
			},
			check: func(t *testing.T, out *ComputeOutput) {
				// Task 2 bumped to wave 2 (same file as 1).
				// Task 3 depends on 2, so wave 3 at minimum.
				w1 := waveContaining(out, 1)
				w2 := waveContaining(out, 2)
				w3 := waveContaining(out, 3)
				if w2 <= w1 {
					t.Errorf("task 2 (wave %d) should be after task 1 (wave %d) due to same-file", w2, w1)
				}
				if w3 <= w2 {
					t.Errorf("task 3 (wave %d) should be after task 2 (wave %d) due to dependency cascade", w3, w2)
				}
			},
		},
		{
			name: "cap at 4-8 range: 5 tasks capped to 4",
			input: ComputeInput{
				Tasks: []TaskInput{
					{Number: 1, Title: "t1", Complexity: "Standard", Risk: "Low", Files: []string{"a.go"}},
					{Number: 2, Title: "t2", Complexity: "Standard", Risk: "Low", Files: []string{"b.go"}},
					{Number: 3, Title: "t3", Complexity: "Standard", Risk: "Low", Files: []string{"c.go"}},
					{Number: 4, Title: "t4", Complexity: "Standard", Risk: "Low", Files: []string{"d.go"}},
					{Number: 5, Title: "t5", Complexity: "Standard", Risk: "Low", Files: []string{"e.go"}},
				},
			},
			check: func(t *testing.T, out *ComputeOutput) {
				if out.Route != "waves" {
					t.Errorf("Route = %q, want %q", out.Route, "waves")
				}
				if len(out.Waves) < 2 {
					t.Errorf("expected at least 2 waves for 5 tasks with cap 4, got %d", len(out.Waves))
				}
				// First wave should have at most 4 tasks.
				if len(out.Waves[0].Tasks) > 4 {
					t.Errorf("wave 1 has %d tasks, cap should be 4", len(out.Waves[0].Tasks))
				}
			},
		},
		{
			name: "Complex counts as 2 toward cap",
			input: ComputeInput{
				Tasks: []TaskInput{
					{Number: 1, Title: "t1", Complexity: "Complex", Risk: "Low", Files: []string{"a.go"}},
					{Number: 2, Title: "t2", Complexity: "Complex", Risk: "Low", Files: []string{"b.go"}},
					{Number: 3, Title: "t3", Complexity: "Standard", Risk: "Low", Files: []string{"c.go"}},
					{Number: 4, Title: "t4", Complexity: "Standard", Risk: "Low", Files: []string{"d.go"}},
					{Number: 5, Title: "t5", Complexity: "Standard", Risk: "Low", Files: []string{"e.go"}},
				},
			},
			check: func(t *testing.T, out *ComputeOutput) {
				// 5 tasks → cap 4. Two Complex tasks weigh 2 each = 4, filling the cap.
				// The 3 Standard tasks should be deferred.
				if len(out.Waves) < 2 {
					t.Errorf("expected at least 2 waves, got %d", len(out.Waves))
				}
				// Verify Complex tasks are counted as 2 by checking that wave 1
				// has fewer tasks than the cap of 4 in raw count.
				w1Tasks := out.Waves[0].Tasks
				complexInW1 := 0
				for _, wt := range w1Tasks {
					if wt.Complexity == "Complex" {
						complexInW1++
					}
				}
				// If both Complex tasks are in wave 1, that's weight 4, no room for Standard.
				if complexInW1 == 2 && len(w1Tasks) > 2 {
					t.Errorf("wave 1 has 2 Complex tasks (weight 4) but also %d extra tasks", len(w1Tasks)-2)
				}
			},
		},
		{
			name: "cap split keeps chain heads (highest critical path)",
			input: ComputeInput{
				Tasks: []TaskInput{
					// Chain: 1 -> 3 -> 5 (cp of 1 = 3)
					// Chain: 2 -> 4 (cp of 2 = 2)
					// Standalone: 6 (cp = 1)
					// All in wave 1 initially. 6 tasks → cap 5.
					// After cap, lowest CP deferred.
					{Number: 1, Title: "head-long", Complexity: "Standard", Risk: "Low", Files: []string{"a.go"}},
					{Number: 2, Title: "head-short", Complexity: "Standard", Risk: "Low", Files: []string{"b.go"}},
					{Number: 3, Title: "mid", Complexity: "Standard", Risk: "Low", DependsOn: []int{1}, Files: []string{"c.go"}},
					{Number: 4, Title: "tail-short", Complexity: "Standard", Risk: "Low", DependsOn: []int{2}, Files: []string{"d.go"}},
					{Number: 5, Title: "tail-long", Complexity: "Standard", Risk: "Low", DependsOn: []int{3}, Files: []string{"e.go"}},
					{Number: 6, Title: "standalone", Complexity: "Standard", Risk: "Low", Files: []string{"f.go"}},
					{Number: 7, Title: "standalone2", Complexity: "Standard", Risk: "Low", Files: []string{"g.go"}},
					{Number: 8, Title: "standalone3", Complexity: "Standard", Risk: "Low", Files: []string{"h.go"}},
					{Number: 9, Title: "standalone4", Complexity: "Standard", Risk: "Low", Files: []string{"i.go"}},
				},
			},
			check: func(t *testing.T, out *ComputeOutput) {
				// 9 tasks → cap 5. Wave 1 has tasks 1,2,6,7,8,9 (no deps).
				// CP: 1=3, 2=2, 6=1, 7=1, 8=1, 9=1. Cap keeps highest CP first.
				// Tasks 1 (cp3), 2 (cp2), 6 (cp1), 7 (cp1), 8 (cp1) = 5 kept.
				// Task 9 deferred.
				w1 := out.Waves[0]
				numsInW1 := make(map[int]bool)
				for _, wt := range w1.Tasks {
					numsInW1[wt.Number] = true
				}
				// Chain heads 1 and 2 must be in wave 1.
				if !numsInW1[1] {
					t.Errorf("chain head task 1 (cp=3) should be in wave 1")
				}
				if !numsInW1[2] {
					t.Errorf("chain head task 2 (cp=2) should be in wave 1")
				}
			},
		},
		{
			name: "risk spreading: max 1 high-risk per wave",
			input: ComputeInput{
				Tasks: []TaskInput{
					{Number: 1, Title: "t1", Complexity: "Standard", Risk: "High", Files: []string{"a.go"}},
					{Number: 2, Title: "t2", Complexity: "Standard", Risk: "High", Files: []string{"b.go"}},
					{Number: 3, Title: "t3", Complexity: "Standard", Risk: "Low", Files: []string{"c.go"}},
					{Number: 4, Title: "t4", Complexity: "Standard", Risk: "Low", Files: []string{"d.go"}},
				},
			},
			check: func(t *testing.T, out *ComputeOutput) {
				for _, w := range out.Waves {
					highCount := 0
					for _, wt := range w.Tasks {
						if wt.Risk == "High" {
							highCount++
						}
					}
					if highCount > 1 {
						t.Errorf("wave %d has %d high-risk tasks, max 1 allowed", w.Number, highCount)
					}
				}
			},
		},
		{
			name: "pre-wave trivial extracted",
			input: ComputeInput{
				Tasks: []TaskInput{
					{Number: 1, Title: "trivial-dep", Complexity: "Trivial", Risk: "Low", Files: []string{"config.go"}},
					{Number: 2, Title: "depends-on-trivial", Complexity: "Standard", Risk: "Low", DependsOn: []int{1}, Files: []string{"main.go"}},
					{Number: 3, Title: "also-depends", Complexity: "Standard", Risk: "Low", DependsOn: []int{1}, Files: []string{"api.go"}},
					{Number: 4, Title: "independent", Complexity: "Standard", Risk: "Low", Files: []string{"util.go"}},
				},
			},
			check: func(t *testing.T, out *ComputeOutput) {
				if len(out.PreWave) == 0 {
					t.Fatalf("expected pre-wave tasks, got none")
				}
				found := false
				for _, pw := range out.PreWave {
					if pw.Number == 1 {
						found = true
					}
				}
				if !found {
					t.Errorf("task 1 should be in PreWave")
				}
				// Task 1 should NOT appear in any wave.
				for _, w := range out.Waves {
					for _, wt := range w.Tasks {
						if wt.Number == 1 {
							t.Errorf("pre-wave task 1 also appears in wave %d", w.Number)
						}
					}
				}
			},
		},
		{
			name: "trivial with no successors stays in wave 1",
			input: ComputeInput{
				Tasks: []TaskInput{
					{Number: 1, Title: "trivial-no-succ", Complexity: "Trivial", Risk: "Low", Files: []string{"a.go"}},
					{Number: 2, Title: "independent", Complexity: "Standard", Risk: "Low", Files: []string{"b.go"}},
					{Number: 3, Title: "independent2", Complexity: "Standard", Risk: "Low", Files: []string{"c.go"}},
					{Number: 4, Title: "independent3", Complexity: "Standard", Risk: "Low", Files: []string{"d.go"}},
				},
			},
			check: func(t *testing.T, out *ComputeOutput) {
				// Task 1 is Trivial but has no successors → stays in waves, not pre-wave.
				for _, pw := range out.PreWave {
					if pw.Number == 1 {
						t.Errorf("task 1 (Trivial, no successors) should NOT be in PreWave")
					}
				}
				w1 := waveContaining(out, 1)
				if w1 != 1 {
					t.Errorf("task 1 should be in wave 1, got wave %d", w1)
				}
			},
		},
		{
			name: "expectedFiles is sorted deduped union",
			input: ComputeInput{
				Tasks: []TaskInput{
					{Number: 1, Title: "t1", Complexity: "Standard", Risk: "Low", Files: []string{"z.go", "a.go"}},
					{Number: 2, Title: "t2", Complexity: "Standard", Risk: "Low", Files: []string{"a.go", "m.go"}},
					{Number: 3, Title: "t3", Complexity: "Standard", Risk: "Low", Files: []string{"b.go"}},
					{Number: 4, Title: "t4", Complexity: "Standard", Risk: "Low", Files: []string{"c.go"}},
				},
			},
			check: func(t *testing.T, out *ComputeOutput) {
				if len(out.Waves) == 0 {
					t.Fatalf("expected at least 1 wave")
				}
				// Find the wave containing tasks 1 and 2 (both should be in wave 1).
				w1 := out.Waves[0]
				ef := w1.ExpectedFiles
				// Must be sorted.
				for i := 1; i < len(ef); i++ {
					if ef[i] < ef[i-1] {
						t.Errorf("expectedFiles not sorted: %v", ef)
						break
					}
				}
				// Must be deduped (a.go appears in both tasks).
				seen := make(map[string]bool)
				for _, f := range ef {
					if seen[f] {
						t.Errorf("duplicate in expectedFiles: %s", f)
					}
					seen[f] = true
				}
			},
		},
		{
			name: "verificationHint present when all tasks share same verify",
			input: ComputeInput{
				Tasks: []TaskInput{
					{Number: 1, Title: "t1", Complexity: "Standard", Risk: "Low", Files: []string{"a.go"}, Verify: "go test ./..."},
					{Number: 2, Title: "t2", Complexity: "Standard", Risk: "Low", Files: []string{"b.go"}, Verify: "go test ./..."},
					{Number: 3, Title: "t3", Complexity: "Standard", Risk: "Low", Files: []string{"c.go"}, Verify: "go test ./..."},
					{Number: 4, Title: "t4", Complexity: "Standard", Risk: "Low", Files: []string{"d.go"}, Verify: "go test ./..."},
				},
			},
			check: func(t *testing.T, out *ComputeOutput) {
				if len(out.Waves) == 0 {
					t.Fatalf("expected at least 1 wave")
				}
				w1 := out.Waves[0]
				if w1.VerificationHint != "go test ./..." {
					t.Errorf("VerificationHint = %q, want %q", w1.VerificationHint, "go test ./...")
				}
			},
		},
		{
			name: "verificationHint omitted when tasks differ",
			input: ComputeInput{
				Tasks: []TaskInput{
					{Number: 1, Title: "t1", Complexity: "Standard", Risk: "Low", Files: []string{"a.go"}, Verify: "go test ./a/..."},
					{Number: 2, Title: "t2", Complexity: "Standard", Risk: "Low", Files: []string{"b.go"}, Verify: "go test ./b/..."},
					{Number: 3, Title: "t3", Complexity: "Standard", Risk: "Low", Files: []string{"c.go"}, Verify: "go test ./c/..."},
					{Number: 4, Title: "t4", Complexity: "Standard", Risk: "Low", Files: []string{"d.go"}, Verify: "go test ./d/..."},
				},
			},
			check: func(t *testing.T, out *ComputeOutput) {
				if len(out.Waves) == 0 {
					t.Fatalf("expected at least 1 wave")
				}
				w1 := out.Waves[0]
				if w1.VerificationHint != "" {
					t.Errorf("VerificationHint = %q, want empty (tasks have different verify values)", w1.VerificationHint)
				}
			},
		},
		{
			name: "within-wave ordering: critical-path DESC, number ASC",
			input: ComputeInput{
				Tasks: []TaskInput{
					// Task 1 → 3 → 5: cp(1)=3
					// Task 2: cp(2)=1
					// Task 4: cp(4)=1
					{Number: 1, Title: "chain-head", Complexity: "Standard", Risk: "Low", Files: []string{"a.go"}},
					{Number: 2, Title: "standalone-low", Complexity: "Standard", Risk: "Low", Files: []string{"b.go"}},
					{Number: 3, Title: "chain-mid", Complexity: "Standard", Risk: "Low", DependsOn: []int{1}, Files: []string{"c.go"}},
					{Number: 4, Title: "standalone-high-num", Complexity: "Standard", Risk: "Low", Files: []string{"d.go"}},
					{Number: 5, Title: "chain-tail", Complexity: "Standard", Risk: "Low", DependsOn: []int{3}, Files: []string{"e.go"}},
				},
			},
			check: func(t *testing.T, out *ComputeOutput) {
				if len(out.Waves) == 0 {
					t.Fatalf("expected at least 1 wave")
				}
				w1 := out.Waves[0]
				// Wave 1 should contain tasks 1 (cp=3), 2 (cp=1), 4 (cp=1).
				// Order: 1 first (highest cp), then 2 before 4 (same cp, lower number first).
				if len(w1.Tasks) < 3 {
					t.Fatalf("expected at least 3 tasks in wave 1, got %d", len(w1.Tasks))
				}
				if w1.Tasks[0].Number != 1 {
					t.Errorf("wave 1 task[0] = %d, want 1 (highest cp)", w1.Tasks[0].Number)
				}
				// Among the cp=1 tasks, 2 should come before 4.
				idx2, idx4 := -1, -1
				for i, wt := range w1.Tasks {
					if wt.Number == 2 {
						idx2 = i
					}
					if wt.Number == 4 {
						idx4 = i
					}
				}
				if idx2 >= 0 && idx4 >= 0 && idx2 > idx4 {
					t.Errorf("task 2 (idx %d) should come before task 4 (idx %d) — same cp, lower number first", idx2, idx4)
				}
			},
		},
		{
			name: "linear dependency chain produces sequential waves",
			input: ComputeInput{
				Tasks: []TaskInput{
					{Number: 1, Title: "t1", Complexity: "Standard", Risk: "Low", Files: []string{"a.go"}},
					{Number: 2, Title: "t2", Complexity: "Standard", Risk: "Low", DependsOn: []int{1}, Files: []string{"b.go"}},
					{Number: 3, Title: "t3", Complexity: "Standard", Risk: "Low", DependsOn: []int{2}, Files: []string{"c.go"}},
					{Number: 4, Title: "t4", Complexity: "Standard", Risk: "Low", DependsOn: []int{3}, Files: []string{"d.go"}},
				},
			},
			check: func(t *testing.T, out *ComputeOutput) {
				if out.Route != "waves" {
					t.Errorf("Route = %q, want %q", out.Route, "waves")
				}
				if len(out.Waves) != 4 {
					t.Errorf("expected 4 waves for a 4-task linear chain, got %d", len(out.Waves))
				}
				for i, w := range out.Waves {
					if len(w.Tasks) != 1 {
						t.Errorf("wave %d has %d tasks, want 1", i+1, len(w.Tasks))
					}
					if w.Tasks[0].Number != i+1 {
						t.Errorf("wave %d task = %d, want %d", i+1, w.Tasks[0].Number, i+1)
					}
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := ComputeWaves(tt.input)

			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error %q does not contain %q", err.Error(), tt.wantErr)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if out == nil {
				t.Fatalf("output is nil")
			}

			tt.check(t, out)
		})
	}
}

// ---------------------------------------------------------------------------
// Determinism test: permuted input → identical output
// ---------------------------------------------------------------------------

func TestComputeWaves_Determinism(t *testing.T) {
	input := ComputeInput{
		Tasks: []TaskInput{
			{Number: 1, Title: "head", Complexity: "Standard", Risk: "Low", Files: []string{"a.go"}},
			{Number: 2, Title: "mid", Complexity: "Complex", Risk: "High", DependsOn: []int{1}, Files: []string{"b.go"}},
			{Number: 3, Title: "leaf", Complexity: "Trivial", Risk: "Low", DependsOn: []int{1}, Files: []string{"c.go"}},
			{Number: 4, Title: "standalone", Complexity: "Standard", Risk: "Medium", Files: []string{"d.go"}},
			{Number: 5, Title: "depends-on-2", Complexity: "Standard", Risk: "Low", DependsOn: []int{2}, Files: []string{"e.go"}},
			{Number: 6, Title: "fan-in", Complexity: "Standard", Risk: "Low", DependsOn: []int{3, 4}, Files: []string{"f.go"}},
		},
		ExtraDeps: []ExtraDep{
			{Task: 4, DependsOn: 1, Reason: "implicit"},
		},
	}

	// Compute reference output.
	ref, err := ComputeWaves(input)
	if err != nil {
		t.Fatalf("reference ComputeWaves: %v", err)
	}

	// Permute and compare 20 times.
	rng := rand.New(rand.NewSource(42))
	for i := range 20 {
		perm := ComputeInput{
			Tasks:     make([]TaskInput, len(input.Tasks)),
			ExtraDeps: make([]ExtraDep, len(input.ExtraDeps)),
		}
		copy(perm.Tasks, input.Tasks)
		copy(perm.ExtraDeps, input.ExtraDeps)

		// Shuffle tasks.
		rng.Shuffle(len(perm.Tasks), func(a, b int) {
			perm.Tasks[a], perm.Tasks[b] = perm.Tasks[b], perm.Tasks[a]
		})
		// Shuffle extra deps.
		rng.Shuffle(len(perm.ExtraDeps), func(a, b int) {
			perm.ExtraDeps[a], perm.ExtraDeps[b] = perm.ExtraDeps[b], perm.ExtraDeps[a]
		})

		got, err := ComputeWaves(perm)
		if err != nil {
			t.Fatalf("permutation %d: unexpected error: %v", i, err)
		}

		if !reflect.DeepEqual(ref, got) {
			t.Errorf("permutation %d: output differs from reference\nref: %+v\ngot: %+v", i, ref, got)
		}
	}
}

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// waveContaining returns the wave number (1-indexed) containing the given task,
// or -1 if not found in any wave.
func waveContaining(out *ComputeOutput, taskNum int) int {
	for _, w := range out.Waves {
		for _, wt := range w.Tasks {
			if wt.Number == taskNum {
				return w.Number
			}
		}
	}
	return -1
}
