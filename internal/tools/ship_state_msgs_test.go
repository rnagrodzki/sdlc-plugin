package tools

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
)

// TestShipState_InputErrorMsgsStartWithAction pins the Msg shape for a missing
// or invalid input: "<action>: <what is wrong>". The rendered error heading
// already names the tool, so a leading "ship_state " would only repeat it.
func TestShipState_InputErrorMsgsStartWithAction(t *testing.T) {
	cases := []struct {
		name string
		in   ShipStateIn
		want string
	}{
		{"start", ShipStateIn{Action: "start"}, "start: step is required"},
		{"complete", ShipStateIn{Action: "complete"}, "complete: step is required"},
		{"begin-step", ShipStateIn{Action: "begin-step"}, "begin-step: step is required"},
		{"complete-step", ShipStateIn{Action: "complete-step"}, "complete-step: step is required"},
		{"skip", ShipStateIn{Action: "skip"}, "skip: step is required"},
		{"fail", ShipStateIn{Action: "fail"}, "fail: step is required"},
		{"decide", ShipStateIn{Action: "decide"}, "decide: step is required"},
		{"defer", ShipStateIn{Action: "defer"}, "defer: severity, file, and title are required"},
		{"migrate", ShipStateIn{Action: "migrate"}, "migrate: from and to are required"},
		{"history_record no detail", ShipStateIn{Action: "history_record"}, "history_record: detail with run record fields is required"},
		{"history_record no skill", ShipStateIn{Action: "history_record", Detail: map[string]any{"outcome": "success"}}, "history_record: detail.skill is required"},
		{"deferred_add no detail", ShipStateIn{Action: "deferred_add"}, "deferred_add: detail with issue fields is required"},
		{"deferred_resolve no detail", ShipStateIn{Action: "deferred_resolve"}, "deferred_resolve: detail with id field is required"},
		{
			"complete-step bad outcome",
			ShipStateIn{Action: "complete-step", Step: "execute", Detail: map[string]any{"outcome": "bogus"}},
			`complete-step: outcome must be "success" or "failure"`,
		},
		{
			"gc bad dryRun",
			ShipStateIn{Action: "gc", Detail: map[string]any{"dryRun": "true"}},
			"gc: detail.dryRun must be a boolean",
		},
		{
			"begin-step bad detail level",
			ShipStateIn{Action: "begin-step", Step: "execute", Detail: map[string]any{"detail": "loud"}},
			`begin-step: detail must be "concise" or "full"`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			_, err := shipState(root, root, tc.in, fixedNow(time.Now()))
			var domainErr *mcpserver.DomainError
			if !errors.As(err, &domainErr) {
				t.Fatalf("error = %v (%T), want *mcpserver.DomainError", err, err)
			}
			if !strings.HasPrefix(domainErr.Msg, tc.want) {
				t.Errorf("Msg = %q, want prefix %q", domainErr.Msg, tc.want)
			}
		})
	}
}
