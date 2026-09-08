package pipeline

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestNarrationMarshal(t *testing.T) {
	cases := []struct {
		name string
		n    Narration
		want string
	}{
		{
			name: "summary and display only",
			n:    Narration{Summary: "wave 1 done", Display: "## Wave 1\n\ndone\n"},
			want: `{"summary":"wave 1 done","display":"## Wave 1\n\ndone\n"}`,
		},
		{
			name: "with timing and next",
			n: Narration{
				Summary: "wave 1 done",
				Display: "## Wave 1\n",
				Timing:  &TimingInfo{StepSeconds: 30, Human: "30s"},
				Next:    &NextAction{ID: "wave-2", Instruction: "run wave 2", EtaSeconds: 120, EtaBasis: "median of 3 runs"},
			},
			want: `{"summary":"wave 1 done","display":"## Wave 1\n","timing":{"stepSeconds":30,"human":"30s"},"next":{"id":"wave-2","instruction":"run wave 2","etaSeconds":120,"etaBasis":"median of 3 runs"}}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(tc.n)
			if err != nil {
				t.Fatalf("Marshal() error = %v", err)
			}
			if got := string(raw); got != tc.want {
				t.Errorf("Marshal() = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestNarrationMarshalOmitsNilFields(t *testing.T) {
	raw, err := json.Marshal(Narration{Summary: "s", Display: "d"})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	got := string(raw)
	if strings.Contains(got, `"timing"`) {
		t.Errorf("Marshal() = %s, want no \"timing\" key when Timing is nil", got)
	}
	if strings.Contains(got, `"next"`) {
		t.Errorf("Marshal() = %s, want no \"next\" key when Next is nil", got)
	}
}

func TestPipelineStateIssueMarshal(t *testing.T) {
	iss := StateIssue{
		Wave:      2,
		Step:      "review",
		TaskID:    "5",
		Severity:  "high",
		Category:  "correctness",
		Summary:   "missing nil check",
		Detail:    "ts can be nil",
		Timestamp: "2026-03-28T14:30:00Z",
	}
	want := `{"wave":2,"step":"review","taskId":"5","severity":"high","category":"correctness","summary":"missing nil check","detail":"ts can be nil","timestamp":"2026-03-28T14:30:00Z"}`

	raw, err := json.Marshal(iss)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if got := string(raw); got != want {
		t.Errorf("Marshal() = %s, want %s", got, want)
	}
}

func TestPipelineStateIssueMarshalOmitsEmptyOptionalFields(t *testing.T) {
	iss := StateIssue{
		Severity:  "medium",
		Category:  "style",
		Summary:   "minor nit",
		Timestamp: "2026-03-28T14:30:00Z",
	}
	want := `{"wave":0,"severity":"medium","category":"style","summary":"minor nit","timestamp":"2026-03-28T14:30:00Z"}`

	raw, err := json.Marshal(iss)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if got := string(raw); got != want {
		t.Errorf("Marshal() = %s, want %s", got, want)
	}
}

func TestStepProgressBlock(t *testing.T) {
	t.Run("no timing store, mixed statuses, no footer when all terminal", func(t *testing.T) {
		steps := []StepRow{
			{Name: "execute", Status: "completed", StartedAt: "2024-01-01T00:00:00Z", CompletedAt: "2024-01-01T00:05:00Z"},
			{Name: "commit", Status: "completed", StartedAt: "2024-01-01T00:05:00Z", CompletedAt: "2024-01-01T00:05:30Z"},
		}
		want := "## Pipeline Steps\n\n" +
			"✓ execute (5m 00s)\n" +
			"✓ commit (30s)\n"
		if got := StepProgressBlock(steps, nil); got != want {
			t.Errorf("StepProgressBlock() = %q, want %q", got, want)
		}
	})

	t.Run("next step with no estimate omits ETA", func(t *testing.T) {
		steps := []StepRow{
			{Name: "execute", Status: "completed", StartedAt: "2024-01-01T00:00:00Z", CompletedAt: "2024-01-01T00:05:00Z"},
			{Name: "review", Status: "pending"},
		}
		want := "## Pipeline Steps\n\n" +
			"✓ execute (5m 00s)\n" +
			"○ review\n" +
			"\nNext: review\n"
		if got := StepProgressBlock(steps, nil); got != want {
			t.Errorf("StepProgressBlock() = %q, want %q", got, want)
		}
	})

	t.Run("next step with recorded estimate includes ETA", func(t *testing.T) {
		root := t.TempDir()
		ts := NewTimingsStore(root)
		for _, d := range []time.Duration{100 * time.Second, 100 * time.Second} {
			if err := ts.Record("review", d); err != nil {
				t.Fatalf("Record() error = %v", err)
			}
		}

		steps := []StepRow{
			{Name: "execute", Status: "completed", StartedAt: "2024-01-01T00:00:00Z", CompletedAt: "2024-01-01T00:05:00Z"},
			{Name: "review", Status: "in_progress"},
		}
		want := "## Pipeline Steps\n\n" +
			"✓ execute (5m 00s)\n" +
			"▶ review\n" +
			"\nNext: review — ETA ~1m 40s (median of 2 runs)\n"
		if got := StepProgressBlock(steps, ts); got != want {
			t.Errorf("StepProgressBlock() = %q, want %q", got, want)
		}
	})

	t.Run("missing timestamps never render 0s", func(t *testing.T) {
		steps := []StepRow{
			{Name: "execute", Status: "failed"},
			{Name: "commit", Status: "failed", StartedAt: "not-a-timestamp", CompletedAt: "2024-01-01T00:05:00Z"},
			{Name: "review", Status: "skipped"},
		}
		want := "## Pipeline Steps\n\n" +
			"✗ execute\n" +
			"✗ commit\n" +
			"⊘ review\n"
		got := StepProgressBlock(steps, nil)
		if got != want {
			t.Errorf("StepProgressBlock() = %q, want %q", got, want)
		}
		if strings.Contains(got, "0s") {
			t.Errorf("StepProgressBlock() = %q, must never render 0s for unknown duration", got)
		}
	})

	t.Run("empty steps", func(t *testing.T) {
		want := "## Pipeline Steps\n\n"
		if got := StepProgressBlock(nil, nil); got != want {
			t.Errorf("StepProgressBlock(nil, nil) = %q, want %q", got, want)
		}
	})

	t.Run("unknown status renders question-mark glyph", func(t *testing.T) {
		steps := []StepRow{{Name: "mystery", Status: "bogus"}}
		want := "## Pipeline Steps\n\n? mystery\n"
		if got := StepProgressBlock(steps, nil); got != want {
			t.Errorf("StepProgressBlock() = %q, want %q", got, want)
		}
	})
}

func TestWaveStartBlock(t *testing.T) {
	t.Run("no timing store omits ETA", func(t *testing.T) {
		w := WaveInfo{
			Number: 2,
			Tasks: []WaveTask{
				{ID: 3, Name: "Implement auth middleware", Model: "sonnet"},
				{ID: 4, Name: "Add rate limiting", Model: "sonnet"},
			},
		}
		want := "## Wave 2\n\n" +
			"- Task 3: Implement auth middleware (sonnet)\n" +
			"- Task 4: Add rate limiting (sonnet)\n"
		if got := WaveStartBlock(w, nil); got != want {
			t.Errorf("WaveStartBlock() = %q, want %q", got, want)
		}
	})

	t.Run("with recorded wave estimate", func(t *testing.T) {
		root := t.TempDir()
		ts := NewTimingsStore(root)
		for _, d := range []time.Duration{300 * time.Second, 300 * time.Second, 300 * time.Second} {
			if err := ts.Record(waveTimingKey, d); err != nil {
				t.Fatalf("Record() error = %v", err)
			}
		}

		w := WaveInfo{
			Number: 1,
			Tasks:  []WaveTask{{ID: 1, Name: "Set up database schema", Model: "haiku"}},
		}
		want := "## Wave 1\n\n" +
			"- Task 1: Set up database schema (haiku)\n" +
			"\nETA: ~5m 00s (median of 3 runs)\n"
		if got := WaveStartBlock(w, ts); got != want {
			t.Errorf("WaveStartBlock() = %q, want %q", got, want)
		}
	})

	t.Run("no tasks", func(t *testing.T) {
		want := "## Wave 0\n\n"
		if got := WaveStartBlock(WaveInfo{Number: 0}, nil); got != want {
			t.Errorf("WaveStartBlock() = %q, want %q", got, want)
		}
	})
}

func TestWaveEndBlock(t *testing.T) {
	t.Run("duration known, no estimate", func(t *testing.T) {
		w := WaveInfo{
			Number:      2,
			Tasks:       []WaveTask{{ID: 3, Name: "Implement auth middleware", Model: "sonnet"}},
			StartedAt:   "2024-01-01T00:00:00Z",
			CompletedAt: "2024-01-01T00:06:12Z",
		}
		want := "## Wave 2\n\n" +
			"- Task 3: Implement auth middleware (sonnet)\n" +
			"\nDuration: 6m 12s\n"
		if got := WaveEndBlock(w, nil); got != want {
			t.Errorf("WaveEndBlock() = %q, want %q", got, want)
		}
	})

	t.Run("duration and estimate both present", func(t *testing.T) {
		root := t.TempDir()
		ts := NewTimingsStore(root)
		for _, d := range []time.Duration{360 * time.Second, 360 * time.Second} {
			if err := ts.Record(waveTimingKey, d); err != nil {
				t.Fatalf("Record() error = %v", err)
			}
		}

		w := WaveInfo{
			Number:      1,
			Tasks:       []WaveTask{{ID: 1, Name: "Set up database schema", Model: "haiku"}},
			StartedAt:   "2024-01-01T00:00:00Z",
			CompletedAt: "2024-01-01T00:06:12Z",
		}
		want := "## Wave 1\n\n" +
			"- Task 1: Set up database schema (haiku)\n" +
			"\nDuration: 6m 12s (estimated ~6m 00s, median of 2 runs)\n"
		if got := WaveEndBlock(w, ts); got != want {
			t.Errorf("WaveEndBlock() = %q, want %q", got, want)
		}
	})

	t.Run("missing timestamps omit duration line, never render 0s", func(t *testing.T) {
		w := WaveInfo{
			Number: 3,
			Tasks:  []WaveTask{{ID: 5, Name: "Add caching layer", Model: "opus"}},
		}
		want := "## Wave 3\n\n" +
			"- Task 5: Add caching layer (opus)\n"
		got := WaveEndBlock(w, nil)
		if got != want {
			t.Errorf("WaveEndBlock() = %q, want %q", got, want)
		}
		if strings.Contains(got, "0s") {
			t.Errorf("WaveEndBlock() = %q, must never render 0s for unknown duration", got)
		}
	})
}

func TestResumeBriefing(t *testing.T) {
	t.Run("full bearings", func(t *testing.T) {
		b := Bearings{
			LastCompletedWave: 2,
			LastCompletedStep: "review",
			ElapsedSeconds:    872,
			GitCrossCheck:     "clean — matches recorded state",
			WillRedo:          []string{"commit-fixes"},
			WillSkip:          []string{"execute", "review"},
		}
		want := "## Resuming Execution\n\n" +
			"Last completed: Wave 2, step \"review\"\n" +
			"Elapsed: 14m 32s\n" +
			"Git cross-check: clean — matches recorded state\n" +
			"\nWill redo:\n" +
			"- commit-fixes\n" +
			"\nWill skip:\n" +
			"- execute\n" +
			"- review\n"
		if got := ResumeBriefing(b); got != want {
			t.Errorf("ResumeBriefing() = %q, want %q", got, want)
		}
	})

	t.Run("nothing completed yet, unknown elapsed and git status", func(t *testing.T) {
		want := "## Resuming Execution\n\n" +
			"Last completed: none\n" +
			"Git cross-check: unknown\n"
		if got := ResumeBriefing(Bearings{}); got != want {
			t.Errorf("ResumeBriefing() = %q, want %q", got, want)
		}
	})
}

func TestPipelineTable(t *testing.T) {
	t.Run("mixed model and optional steps", func(t *testing.T) {
		steps := []ConfigStep{
			{Name: "execute", Description: "Run the execution wave loop", Optional: false},
			{Name: "review", Description: "Run automated code review", Model: "sonnet", Optional: true},
		}
		want := "| Step | Description | Model | Optional |\n" +
			"|---|---|---|---|\n" +
			"| execute | Run the execution wave loop | — | no |\n" +
			"| review | Run automated code review | sonnet | yes |\n"
		if got := PipelineTable(steps); got != want {
			t.Errorf("PipelineTable() = %q, want %q", got, want)
		}
	})

	t.Run("no steps", func(t *testing.T) {
		want := "| Step | Description | Model | Optional |\n|---|---|---|---|\n"
		if got := PipelineTable(nil); got != want {
			t.Errorf("PipelineTable(nil) = %q, want %q", got, want)
		}
	})
}

func TestMigrationReport(t *testing.T) {
	t.Run("changes and backup path", func(t *testing.T) {
		changes := []string{
			"progress.json split into progress/<taskId>.json",
			"issues[] accumulator added to state schema",
		}
		want := "## Migration Report\n\n" +
			"- progress.json split into progress/<taskId>.json\n" +
			"- issues[] accumulator added to state schema\n" +
			"\nBackup: /repo/.sdlc-v2/backups/20260328T143000Z\n"
		if got := MigrationReport(changes, "/repo/.sdlc-v2/backups/20260328T143000Z"); got != want {
			t.Errorf("MigrationReport() = %q, want %q", got, want)
		}
	})

	t.Run("no changes, no backup path", func(t *testing.T) {
		want := "## Migration Report\n\nNo changes applied.\n"
		if got := MigrationReport(nil, ""); got != want {
			t.Errorf("MigrationReport(nil, \"\") = %q, want %q", got, want)
		}
	})
}

func TestIssueSummaryBlock(t *testing.T) {
	t.Run("no issues", func(t *testing.T) {
		want := "No issues recorded.\n"
		if got := IssueSummaryBlock(nil); got != want {
			t.Errorf("IssueSummaryBlock(nil) = %q, want %q", got, want)
		}
	})

	t.Run("issues with and without context fields", func(t *testing.T) {
		issues := []StateIssue{
			{Severity: "high", Category: "correctness", Summary: "nil pointer risk", Wave: 2, TaskID: "5", Step: "review"},
			{Severity: "low", Category: "style", Summary: "unused import"},
		}
		want := "## Issues (2)\n\n" +
			"- [high] nil pointer risk (wave 2, task 5, step review)\n" +
			"- [low] unused import\n"
		if got := IssueSummaryBlock(issues); got != want {
			t.Errorf("IssueSummaryBlock() = %q, want %q", got, want)
		}
	})
}
