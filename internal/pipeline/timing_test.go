package pipeline

import (
	"testing"
	"time"
)

func TestHumanize(t *testing.T) {
	cases := []struct {
		name string
		d    time.Duration
		want string
	}{
		{"zero", 0, "0s"},
		{"one second", 1 * time.Second, "1s"},
		{"under a minute", 42 * time.Second, "42s"},
		{"just under a minute", 59 * time.Second, "59s"},
		{"exactly a minute", 60 * time.Second, "1m 00s"},
		{"minutes and seconds", 6*time.Minute + 12*time.Second, "6m 12s"},
		{"single digit seconds padded", 6*time.Minute + 5*time.Second, "6m 05s"},
		{"just under an hour", 59*time.Minute + 59*time.Second, "59m 59s"},
		{"exactly an hour", 1 * time.Hour, "1h 00m"},
		{"hour drops seconds", 1*time.Hour + 3*time.Minute, "1h 03m"},
		{"hour drops trailing seconds", 1*time.Hour + 2*time.Minute + 59*time.Second, "1h 02m"},
		{"negative treated as zero", -5 * time.Second, "0s"},
		{"multiple hours", 2*time.Hour + 30*time.Minute, "2h 30m"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Humanize(tc.d)
			if got != tc.want {
				t.Errorf("Humanize(%v) = %q, want %q", tc.d, got, tc.want)
			}
		})
	}
}

func TestDuration(t *testing.T) {
	cases := []struct {
		name         string
		startedAt    string
		completedAt  string
		wantOK       bool
		wantDuration time.Duration
	}{
		{
			name:         "five minutes apart",
			startedAt:    "2024-01-01T00:00:00Z",
			completedAt:  "2024-01-01T00:05:00Z",
			wantOK:       true,
			wantDuration: 5 * time.Minute,
		},
		{
			name:         "same instant",
			startedAt:    "2024-01-01T00:00:00Z",
			completedAt:  "2024-01-01T00:00:00Z",
			wantOK:       true,
			wantDuration: 0,
		},
		{
			name:        "malformed started",
			startedAt:   "not-a-timestamp",
			completedAt: "2024-01-01T00:05:00Z",
			wantOK:      false,
		},
		{
			name:        "malformed completed",
			startedAt:   "2024-01-01T00:00:00Z",
			completedAt: "not-a-timestamp",
			wantOK:      false,
		},
		{
			name:        "empty inputs",
			startedAt:   "",
			completedAt: "",
			wantOK:      false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotDuration, gotOK := Duration(tc.startedAt, tc.completedAt)
			if gotOK != tc.wantOK {
				t.Fatalf("Duration(%q, %q) ok = %v, want %v", tc.startedAt, tc.completedAt, gotOK, tc.wantOK)
			}
			if tc.wantOK && gotDuration != tc.wantDuration {
				t.Errorf("Duration(%q, %q) = %v, want %v", tc.startedAt, tc.completedAt, gotDuration, tc.wantDuration)
			}
		})
	}
}

func TestIdleGap(t *testing.T) {
	cases := []struct {
		name            string
		prevCompletedAt string
		nextStartedAt   string
		wantOK          bool
		wantDuration    time.Duration
	}{
		{
			name:            "two minute gap",
			prevCompletedAt: "2024-01-01T00:00:00Z",
			nextStartedAt:   "2024-01-01T00:02:00Z",
			wantOK:          true,
			wantDuration:    2 * time.Minute,
		},
		{
			name:            "malformed timestamp",
			prevCompletedAt: "garbage",
			nextStartedAt:   "2024-01-01T00:02:00Z",
			wantOK:          false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotDuration, gotOK := IdleGap(tc.prevCompletedAt, tc.nextStartedAt)
			if gotOK != tc.wantOK {
				t.Fatalf("IdleGap(%q, %q) ok = %v, want %v", tc.prevCompletedAt, tc.nextStartedAt, gotOK, tc.wantOK)
			}
			if tc.wantOK && gotDuration != tc.wantDuration {
				t.Errorf("IdleGap(%q, %q) = %v, want %v", tc.prevCompletedAt, tc.nextStartedAt, gotDuration, tc.wantDuration)
			}
		})
	}
}

func TestHumanWaitSteps(t *testing.T) {
	if !HumanWaitSteps["await-remote-review"] {
		t.Error(`expected HumanWaitSteps["await-remote-review"] to be true`)
	}
	if HumanWaitSteps["run-tests"] {
		t.Error(`expected HumanWaitSteps["run-tests"] to be false (absent)`)
	}
}
