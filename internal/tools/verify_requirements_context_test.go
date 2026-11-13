package tools

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// verifyIn returns a verify_requirements_context input.
func verifyIn(kind, value string) PlanSupportIn {
	return PlanSupportIn{Action: verifyRequirementsContextAction, VerifyKind: kind, VerifyValue: value}
}

// wantVerifyErr checks the class, the message and the Suggestion of err. An
// empty msg skips the message check.
func wantVerifyErr(t *testing.T, err error, class, msg, suggestion string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if got := errorClassOf(err); got != class {
		t.Errorf("error class = %q, want %s (err %v)", got, class, err)
	}
	if msg != "" && err.Error() != msg {
		t.Errorf("err.Error() = %q, want %q", err.Error(), msg)
	}
	if got := suggestionOf(err); got != suggestion {
		t.Errorf("Suggestion = %q, want %q", got, suggestion)
	}
}

// verifyTreeHash returns one hash of every path under root: the relative path,
// the mode, and the bytes of a file or the target of a link. Two equal hashes
// mean that no file was written, created or deleted.
func verifyTreeHash(t *testing.T, root string) string {
	t.Helper()
	var lines []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		line := rel + "|" + info.Mode().String()
		switch {
		case info.Mode()&fs.ModeSymlink != 0:
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			line += "|link:" + target
		case info.Mode().IsRegular():
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			sum := sha256.Sum256(data)
			line += "|" + hex.EncodeToString(sum[:])
		}
		lines = append(lines, line)
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	sort.Strings(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:])
}

// verifyTopicText is a topic file with empty sections and two open questions.
func verifyTopicText(topic string) string {
	return preplanSkeletonFor(topic) + "\n## Open questions\n\n- Who owns the check?\n- What is the size limit?\n"
}

// TestVerifyRequirementsContextInputs covers each row of the input table.
func TestVerifyRequirementsContextInputs(t *testing.T) {
	const kindMsg = "verify_requirements_context: verifyKind must be one of topic, file, jira, issue"
	const kindSuggestion = "Set verifyKind to the kind of the input: topic, file, jira or issue."
	const valueMsg = "verify_requirements_context: verifyValue is required"
	const valueSuggestion = "Set verifyValue to the topic name, the file path, the Jira key or the issue number."
	const sizeSuggestion = "Pass a shorter value: a topic name, a path, a Jira key or an issue number."
	const jiraSuggestion = "Pass a key such as INT-4571: the project key, a hyphen and the issue number."

	for _, tc := range []struct {
		name                     string
		in                       PlanSupportIn
		class, msg, wantSuggestn string
	}{
		{"verifyKind omitted", verifyIn("", "INT-1"), "domain", kindMsg, kindSuggestion},
		{"verifyKind not in the enum", verifyIn("ticket", "INT-1"), "domain", kindMsg, kindSuggestion},
		{"verifyKind in another case", verifyIn("Topic", "auth flow"), "domain", kindMsg, kindSuggestion},
		{"verifyValue omitted", verifyIn("topic", ""), "domain", valueMsg, valueSuggestion},
		{"verifyValue blank", verifyIn("jira", " \t\n "), "domain", valueMsg, valueSuggestion},
		{"verifyValue over the limit", verifyIn("topic", strings.Repeat("a", verifyValueMaxBytes+1)), "domain",
			fmt.Sprintf("verify_requirements_context: verifyValue is %d bytes; the limit is 4096 bytes", verifyValueMaxBytes+1), sizeSuggestion},
		// A value of exactly the limit passes the size check and reaches the
		// key check.
		{"verifyValue of exactly the limit", verifyIn("jira", strings.Repeat("A", verifyValueMaxBytes)), "domain",
			"verify_requirements_context: " + strings.Repeat("A", verifyValueMaxBytes) + " is not a Jira key", jiraSuggestion},
		{"jira key in lower case", verifyIn("jira", "int4571"), "domain", "verify_requirements_context: int4571 is not a Jira key", jiraSuggestion},
		{"jira key with a leading zero", verifyIn("jira", "INT-0571"), "domain", "verify_requirements_context: INT-0571 is not a Jira key", jiraSuggestion},
		{"jira key with a lower-case project", verifyIn("jira", "int-4571"), "domain", "verify_requirements_context: int-4571 is not a Jira key", jiraSuggestion},
		{"jira key with an 11-digit number", verifyIn("jira", "INT-12345678901"), "domain", "verify_requirements_context: INT-12345678901 is not a Jira key", jiraSuggestion},
		{"file kind before Task 3", verifyIn("file", "docs/req.md"), "domain", "verify_requirements_context: verifyKind file is not built yet", "Use verifyKind topic or jira."},
		{"issue kind before Task 4", verifyIn("issue", "42"), "domain", "verify_requirements_context: verifyKind issue is not built yet", "Use verifyKind topic, file or jira."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			_, err := planSupportCore(root, root, tc.in)
			wantVerifyErr(t, err, tc.class, tc.msg, tc.wantSuggestn)
		})
	}

	t.Run("jira key", func(t *testing.T) {
		root := t.TempDir()
		out, err := planSupportCore(root, root, verifyIn("jira", "INT-4571"))
		if err != nil {
			t.Fatalf("verify_requirements_context: %v", err)
		}
		if out.VerifyKind != "jira" || out.VerifySource != "INT-4571" || out.JiraProject != "INT" {
			t.Errorf("verifyKind, verifySource, jiraProject = %q, %q, %q, want jira, INT-4571, INT", out.VerifyKind, out.VerifySource, out.JiraProject)
		}
		if out.StructureChecks != "not run: the input is a Jira ticket" {
			t.Errorf("structureChecks = %q", out.StructureChecks)
		}
		if out.StructureFindings != nil || out.OpenQuestions != nil {
			t.Errorf("structureFindings and openQuestions must be absent for jira: %v, %v", out.StructureFindings, out.OpenQuestions)
		}
		const next = "Call jira with action load and key INT. Then call mcp__atlassian__getJiraIssue with the cached cloudId, issueKey INT-4571, fields summary and description, and responseContentFormat markdown. Then run the guardrail, meaning and OpenSpec checks."
		if out.Next != next {
			t.Errorf("next = %q, want %q", out.Next, next)
		}
		if out.GuardrailsStatus != "loaded: 0" {
			t.Errorf("guardrailsStatus = %q, want loaded: 0", out.GuardrailsStatus)
		}
	})

	t.Run("jira key of the largest size", func(t *testing.T) {
		root := t.TempDir()
		key := "A" + strings.Repeat("Z", 31) + "-9999999999"
		out, err := planSupportCore(root, root, verifyIn("jira", key))
		if err != nil {
			t.Fatalf("verify_requirements_context: %v", err)
		}
		if out.JiraProject != key[:32] {
			t.Errorf("jiraProject = %q, want %q", out.JiraProject, key[:32])
		}
	})

	t.Run("topic", func(t *testing.T) {
		root := t.TempDir()
		root = writeGuardrailsRoot(t, root, 3)
		content := verifyTopicText("idea verification")
		file := writePreplanTopic(t, root, "2026-11-13-idea-verification.md", content)
		out, err := planSupportCore(root, root, verifyIn("topic", "  idea verification  "))
		if err != nil {
			t.Fatalf("verify_requirements_context: %v", err)
		}
		if out.VerifyKind != "topic" || out.VerifySource != file || out.StructureChecks != "ran" {
			t.Errorf("verifyKind, verifySource, structureChecks = %q, %q, %q, want topic, %s, ran", out.VerifyKind, out.VerifySource, out.StructureChecks, file)
		}
		want := preplanStructureFindings([]byte(content))
		if len(want) == 0 {
			t.Fatal("the test topic file must give structure findings")
		}
		if out.StructureFindings == nil || !reflect.DeepEqual(*out.StructureFindings, want) {
			t.Errorf("structureFindings = %v, want %v", out.StructureFindings, want)
		}
		wantQuestions := []string{"Who owns the check?", "What is the size limit?"}
		if out.OpenQuestions == nil || !reflect.DeepEqual(*out.OpenQuestions, wantQuestions) {
			t.Errorf("openQuestions = %v, want %v", out.OpenQuestions, wantQuestions)
		}
		if out.GuardrailsStatus != "loaded: 3" || len(out.Guardrails) != 3 {
			t.Errorf("guardrailsStatus = %q, guardrails = %d, want loaded: 3 and 3", out.GuardrailsStatus, len(out.Guardrails))
		}
		const next = "Read verifySource. Copy structureFindings and openQuestions into the report, then run the guardrail, meaning and OpenSpec checks."
		if out.Next != next {
			t.Errorf("next = %q, want %q", out.Next, next)
		}
		if out.PreplanFile != "" {
			t.Errorf("preplanFile = %q, want it absent", out.PreplanFile)
		}
	})

	t.Run("topic with no open questions section", func(t *testing.T) {
		root := t.TempDir()
		writePreplanTopic(t, root, "auth-flow.md", preplanSkeletonFor("auth flow"))
		out, err := planSupportCore(root, root, verifyIn("topic", "auth flow"))
		if err != nil {
			t.Fatalf("verify_requirements_context: %v", err)
		}
		if out.OpenQuestions == nil || len(*out.OpenQuestions) != 0 {
			t.Errorf("openQuestions = %v, want an empty list that renders (none)", out.OpenQuestions)
		}
	})
}

// writeGuardrailsRoot writes a config.toml with n plan guardrails under root
// and returns root.
func writeGuardrailsRoot(t *testing.T, root string, n int) string {
	t.Helper()
	writeFile(t, filepath.Join(root, ".sdlc-v2", "config.toml"), preplanGuardrailsToml(n))
	return root
}

// TestVerifyRequirementsContextCombinations covers each row of the
// combination table: fields of other actions.
func TestVerifyRequirementsContextCombinations(t *testing.T) {
	skeleton := preplanSkeletonFor("auth flow")

	t.Run("another action with verifyKind", func(t *testing.T) {
		noPreplanCheckWrite(t)
		root := t.TempDir()
		file := writePreplanTopic(t, root, "auth-flow.md", skeleton)
		in := preplanCheckIn("auth flow", 1, 0, 0)
		in.VerifyKind = "topic"
		_, err := planSupportCore(root, root, in)
		wantVerifyErr(t, err, "domain", "preplan_check: verifyKind is a verify_requirements_context field",
			"Remove verifyKind and verifyValue, or use action verify_requirements_context.")
		wantPreplanBytes(t, file, skeleton)
	})

	t.Run("another action with verifyValue", func(t *testing.T) {
		root := t.TempDir()
		_, err := planSupportCore(root, root, PlanSupportIn{Action: "evidence_get", VerifyValue: "auth flow"})
		wantVerifyErr(t, err, "domain", "evidence_get: verifyValue is a verify_requirements_context field",
			"Remove verifyKind and verifyValue, or use action verify_requirements_context.")
	})

	t.Run("verify_requirements_context with topic", func(t *testing.T) {
		root := t.TempDir()
		writePreplanTopic(t, root, "auth-flow.md", skeleton)
		in := verifyIn("topic", "auth flow")
		in.Topic = "auth flow"
		_, err := planSupportCore(root, root, in)
		wantVerifyErr(t, err, "domain", "verify_requirements_context: topic is not used by this action",
			"Put the topic name in verifyValue and set verifyKind to topic.")
	})

	for _, tc := range []struct {
		field string
		set   func(*PlanSupportIn)
	}{
		{"section", func(in *PlanSupportIn) { in.Section = "Goal" }},
		{"content", func(in *PlanSupportIn) { in.Content = preplanContentPtr("") }},
		{"topicStatus", func(in *PlanSupportIn) { in.TopicStatus = preplanStatusPaused }},
	} {
		t.Run("verify_requirements_context with "+tc.field, func(t *testing.T) {
			root := t.TempDir()
			in := verifyIn("jira", "INT-1")
			tc.set(&in)
			_, err := planSupportCore(root, root, in)
			wantVerifyErr(t, err, "domain", "verify_requirements_context: "+tc.field+" is a preplan_write field",
				"Remove "+tc.field+", or use action preplan_write.")
		})
	}

	t.Run("verify_requirements_context with other fields", func(t *testing.T) {
		root := t.TempDir()
		pass := 3
		in := verifyIn("jira", "INT-1")
		in.RunID = "plan-main-20260929T114125Z"
		in.Items = []EvidenceItem{{ID: "F-1", Summary: "s"}}
		in.PassCount = &pass
		in.ChangeName = "add-widget"
		out, err := planSupportCore(root, root, in)
		if err != nil {
			t.Fatalf("verify_requirements_context: %v", err)
		}
		if out.VerifySource != "INT-1" {
			t.Errorf("verifySource = %q, want INT-1", out.VerifySource)
		}
	})
}

// TestVerifyRequirementsContextParity calls preplan_check and the topic kind
// of verify_requirements_context for each input state of the parity table.
// Each "same" row gives the same class, the same message and the same
// Suggestion. The retry text at the end of a Suggestion names the action
// (preplanRetry), so a preplan_check Suggestion that ends with
// preplanCheckAgain ends with verifyRunAgain for the new action.
func TestVerifyRequirementsContextParity(t *testing.T) {
	skeleton := preplanSkeletonFor("auth flow")
	sameSuggestion := func(check string) string {
		return strings.ReplaceAll(check, preplanCheckAgain, verifyRunAgain)
	}
	sameMsg := func(check string) string {
		return strings.ReplaceAll(check, preplanCheckAction, verifyRequirementsContextAction)
	}
	// The two calls run in two roots, so a preplan_check write cannot change
	// the verify call. lastSwap turns a path under the root of the last
	// preplan_check call into the same path under the root of the last
	// verify call.
	var lastSwap func(string) string
	// run sets up two roots with setup, then calls each action in its own
	// root.
	run := func(t *testing.T, topic string, setup func(t *testing.T, root string)) (checkErr, verifyErr error) {
		t.Helper()
		checkRoot, verifyRoot := t.TempDir(), t.TempDir()
		setup(t, checkRoot)
		setup(t, verifyRoot)
		lastSwap = func(s string) string { return strings.ReplaceAll(s, checkRoot, verifyRoot) }
		_, checkErr = planSupportCore(checkRoot, checkRoot, preplanCheckIn(topic, 1, 0, 0))
		_, verifyErr = planSupportCore(verifyRoot, verifyRoot, verifyIn("topic", topic))
		return checkErr, verifyErr
	}
	// wantSame checks that verifyErr is checkErr of the last run with the
	// action name, the root and the retry text of the new action.
	wantSame := func(t *testing.T, class string, checkErr, verifyErr error) {
		t.Helper()
		if got := errorClassOf(checkErr); got != class {
			t.Fatalf("preplan_check error class = %q, want %s (err %v)", got, class, checkErr)
		}
		wantVerifyErr(t, verifyErr, class, lastSwap(sameMsg(checkErr.Error())), lastSwap(sameSuggestion(suggestionOf(checkErr))))
	}
	noFolder := func(*testing.T, string) {}

	for _, tc := range []struct{ name, topic string }{
		{"over 50 characters", strings.Repeat("a", 51)},
		{"no letter or digit", "--- ---"},
		{"a line break", "auth\nflow"},
	} {
		t.Run("bad topic: "+tc.name, func(t *testing.T) {
			checkErr, verifyErr := run(t, tc.topic, noFolder)
			wantSame(t, "domain", checkErr, verifyErr)
		})
	}

	// A blank topic is a blank verifyValue: the input check of the action
	// refuses it before the topic check, with the Suggestion of the input
	// table. The class is the same.
	t.Run("bad topic: blank", func(t *testing.T) {
		checkErr, verifyErr := run(t, "   ", noFolder)
		if errorClassOf(checkErr) != "domain" || errorClassOf(verifyErr) != "domain" {
			t.Errorf("error classes = %q, %q, want domain, domain", errorClassOf(checkErr), errorClassOf(verifyErr))
		}
	})

	t.Run("no matching file", func(t *testing.T) {
		checkErr, verifyErr := run(t, "auth flow", func(t *testing.T, root string) {
			writePreplanTopic(t, root, "other.md", skeleton)
		})
		wantVerifyErr(t, checkErr, "domain", "", preplanNoFileSuggestion)
		wantVerifyErr(t, verifyErr, "domain", lastSwap(sameMsg(checkErr.Error())), verifyNoFileSuggestion)
		if verifyNoFileSuggestion != "Check the topic name against the files in .sdlc-v2/preplan/. For a file outside that folder, run /sdlc:verify-requirements --file <path>." {
			t.Errorf("verifyNoFileSuggestion = %q", verifyNoFileSuggestion)
		}
	})

	t.Run("no preplan folder", func(t *testing.T) {
		checkErr, verifyErr := run(t, "auth flow", noFolder)
		wantVerifyErr(t, checkErr, "domain", "", preplanNoFileSuggestion)
		wantVerifyErr(t, verifyErr, "domain", lastSwap(sameMsg(checkErr.Error())), verifyNoFileSuggestion)
	})

	// The file goes away after the folder listing: preplan_check gives
	// preplanNoFileSuggestion, the find step gives preplanGoneSuggestion to
	// every other action, and the new action replaces it.
	t.Run("file gone after the listing", func(t *testing.T) {
		swapPreplanSeam(t, &preplanLstat, func(name string) (fs.FileInfo, error) {
			return nil, &fs.PathError{Op: "lstat", Path: name, Err: fs.ErrNotExist}
		})
		checkErr, verifyErr := run(t, "auth flow", func(t *testing.T, root string) {
			writePreplanTopic(t, root, "auth-flow.md", skeleton)
		})
		wantVerifyErr(t, checkErr, "domain", "", preplanNoFileSuggestion)
		wantVerifyErr(t, verifyErr, "domain", "", verifyNoFileSuggestion)
	})

	t.Run("2 matching files", func(t *testing.T) {
		checkErr, verifyErr := run(t, "auth flow", func(t *testing.T, root string) {
			writePreplanTopic(t, root, "auth-flow.md", skeleton)
			writePreplanTopic(t, root, "2026-10-01-auth-flow.md", skeleton)
		})
		wantSame(t, "data", checkErr, verifyErr)
	})

	t.Run("dangling folder link", func(t *testing.T) {
		checkErr, verifyErr := run(t, "auth flow", func(t *testing.T, root string) {
			dir := filepath.Join(root, ".sdlc-v2", "preplan")
			if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Join(root, "missing"), dir); err != nil {
				t.Fatal(err)
			}
		})
		wantSame(t, "infra", checkErr, verifyErr)
	})

	topicPath := func(root string) string {
		return filepath.Join(root, ".sdlc-v2", "preplan", "auth-flow.md")
	}
	mkPreplanDir := func(t *testing.T, root string) {
		if err := os.MkdirAll(filepath.Dir(topicPath(root)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, root string)
	}{
		{"a link to a file", func(t *testing.T, root string) {
			mkPreplanDir(t, root)
			target := filepath.Join(root, "target.md")
			writeFile(t, target, skeleton)
			if err := os.Symlink(target, topicPath(root)); err != nil {
				t.Fatal(err)
			}
		}},
		{"a folder", func(t *testing.T, root string) {
			mkPreplanDir(t, root)
			if err := os.Mkdir(topicPath(root), 0o755); err != nil {
				t.Fatal(err)
			}
		}},
		{"a pipe", func(t *testing.T, root string) {
			writePreplanTopic(t, root, "auth-flow.md", skeleton)
		}},
		{"a device", func(t *testing.T, root string) {
			writePreplanTopic(t, root, "auth-flow.md", skeleton)
		}},
	} {
		t.Run(tc.name+" at the path", func(t *testing.T) {
			switch tc.name {
			case "a pipe":
				swapPreplanSeam(t, &preplanLstat, func(string) (fs.FileInfo, error) {
					return preplanFakeInfo{mode: fs.ModeNamedPipe | 0o644}, nil
				})
			case "a device":
				swapPreplanSeam(t, &preplanLstat, func(string) (fs.FileInfo, error) {
					return preplanFakeInfo{mode: fs.ModeDevice | 0o644}, nil
				})
			}
			checkRoot, verifyRoot := t.TempDir(), t.TempDir()
			tc.setup(t, checkRoot)
			tc.setup(t, verifyRoot)
			_, checkErr := planSupportCore(checkRoot, checkRoot, preplanCheckIn("auth flow", 1, 0, 0))
			_, verifyErr := planSupportCore(verifyRoot, verifyRoot, verifyIn("topic", "auth flow"))
			if errorClassOf(checkErr) != "domain" {
				t.Fatalf("preplan_check error class = %q, want domain (%v)", errorClassOf(checkErr), checkErr)
			}
			// The path differs per root: compare with the root swapped.
			swap := func(s string) string { return strings.ReplaceAll(s, checkRoot, verifyRoot) }
			wantVerifyErr(t, verifyErr, "domain", swap(sameMsg(checkErr.Error())), swap(sameSuggestion(suggestionOf(checkErr))))
		})
	}

	t.Run("file over 8 MiB", func(t *testing.T) {
		head := "**Status:** in progress\n"
		content := head + preplanFiller(dashboardDeleteReadMax+1-len(head))
		checkRoot, verifyRoot := t.TempDir(), t.TempDir()
		writePreplanTopic(t, checkRoot, "auth-flow.md", content)
		writePreplanTopic(t, verifyRoot, "auth-flow.md", content)
		_, checkErr := planSupportCore(checkRoot, checkRoot, preplanCheckIn("auth flow", 1, 0, 0))
		_, verifyErr := planSupportCore(verifyRoot, verifyRoot, verifyIn("topic", "auth flow"))
		if errorClassOf(checkErr) != "data" {
			t.Fatalf("preplan_check error class = %q, want data (%v)", errorClassOf(checkErr), checkErr)
		}
		swap := func(s string) string { return strings.ReplaceAll(s, checkRoot, verifyRoot) }
		wantVerifyErr(t, verifyErr, "data", swap(sameMsg(checkErr.Error())), sameSuggestion(suggestionOf(checkErr)))
		if want := "Make the topic file smaller than 8 MiB by hand, then run /sdlc:verify-requirements again."; suggestionOf(verifyErr) != want {
			t.Errorf("Suggestion = %q, want %q", suggestionOf(verifyErr), want)
		}
	})

	t.Run("code fence open at the end", func(t *testing.T) {
		content := skeleton + "\n```mermaid\nflowchart TD\n"
		checkRoot, verifyRoot := t.TempDir(), t.TempDir()
		writePreplanTopic(t, checkRoot, "auth-flow.md", content)
		writePreplanTopic(t, verifyRoot, "auth-flow.md", content)
		checkOut, err := planSupportCore(checkRoot, checkRoot, preplanCheckIn("auth flow", 1, 0, 0))
		if err != nil {
			t.Fatalf("preplan_check: %v", err)
		}
		verifyOut, err := planSupportCore(verifyRoot, verifyRoot, verifyIn("topic", "auth flow"))
		if err != nil {
			t.Fatalf("verify_requirements_context: %v", err)
		}
		warning := preplanOpenFenceWarning("```")
		if !strings.Contains(checkOut.Summary, warning) || !strings.Contains(verifyOut.Summary, warning) {
			t.Errorf("summaries must both hold %q:\npreplan_check: %s\nverify: %s", warning, checkOut.Summary, verifyOut.Summary)
		}
		if verifyOut.StructureFindings == nil || len(*verifyOut.StructureFindings) == 0 || (*verifyOut.StructureFindings)[0].Rule != preplanRuleOpenFence {
			t.Errorf("structureFindings = %v, want open-fence first", verifyOut.StructureFindings)
		}
	})

	t.Run("guardrails unreadable", func(t *testing.T) {
		checkErr, verifyErr := run(t, "auth flow", func(t *testing.T, root string) {
			writeFile(t, filepath.Join(root, ".sdlc-v2", "config.toml"), "[plan\nguardrails = [\n")
			writePreplanTopic(t, root, "auth-flow.md", skeleton)
		})
		if checkErr != nil || verifyErr != nil {
			t.Errorf("both calls must succeed: %v / %v", checkErr, verifyErr)
		}
	})
}

// TestVerifyRequirementsContextGuardrailsUnavailable checks that a bad config
// gives guardrailsStatus "unavailable: <warning>" and the call still
// succeeds, for each kind that is built.
func TestVerifyRequirementsContextGuardrailsUnavailable(t *testing.T) {
	for _, in := range []PlanSupportIn{verifyIn("topic", "auth flow"), verifyIn("jira", "INT-4571")} {
		t.Run(in.VerifyKind, func(t *testing.T) {
			root := t.TempDir()
			writeFile(t, filepath.Join(root, ".sdlc-v2", "config.toml"), "[plan\nguardrails = [\n")
			writePreplanTopic(t, root, "auth-flow.md", preplanSkeletonFor("auth flow"))
			_, warning := loadGuardrails(root)
			if warning == "" {
				t.Fatal("loadGuardrails gave no warning for a malformed config")
			}
			out, err := planSupportCore(root, root, in)
			if err != nil {
				t.Fatalf("verify_requirements_context: %v", err)
			}
			want := "unavailable: " + preplanWarningText(warning)
			if out.GuardrailsStatus != want {
				t.Errorf("guardrailsStatus = %q, want %q", out.GuardrailsStatus, want)
			}
			if !strings.HasPrefix(out.GuardrailsStatus, "unavailable: Failed to read plan config: ") {
				t.Errorf("guardrailsStatus = %q, want the loadGuardrails read warning", out.GuardrailsStatus)
			}
			if !strings.Contains(out.Summary, "Guardrails unavailable: "+preplanWarningText(warning)+".") {
				t.Errorf("summary = %q, want it to name the warning", out.Summary)
			}
		})
	}
}

// TestVerifyRequirementsContextMainRoot checks that the topic kind finds the
// topic file under the main root when the active root differs.
func TestVerifyRequirementsContextMainRoot(t *testing.T) {
	mainRoot, activeRoot := t.TempDir(), t.TempDir()
	file := writePreplanTopic(t, mainRoot, "auth-flow.md", preplanSkeletonFor("auth flow"))
	out, err := planSupportCore(mainRoot, activeRoot, verifyIn("topic", "auth flow"))
	if err != nil {
		t.Fatalf("verify_requirements_context: %v", err)
	}
	if out.VerifySource != file {
		t.Errorf("verifySource = %q, want %q", out.VerifySource, file)
	}
	if _, err := os.Stat(filepath.Join(activeRoot, ".sdlc-v2")); err == nil {
		t.Errorf("the call created a data folder under the active root")
	}
}

// TestVerifyRequirementsContextWritesNothing calls the action with each kind,
// success and error, and checks that no file under t.TempDir() changes.
func TestVerifyRequirementsContextWritesNothing(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   PlanSupportIn
	}{
		{"topic", verifyIn("topic", "auth flow")},
		{"topic with no file", verifyIn("topic", "other topic")},
		{"topic with an open fence", verifyIn("topic", "fenced")},
		{"file", verifyIn("file", "docs/req.md")},
		{"jira", verifyIn("jira", "INT-4571")},
		{"issue", verifyIn("issue", "42")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			noPreplanCheckWrite(t)
			mainRoot, activeRoot := t.TempDir(), t.TempDir()
			writeGuardrailsRoot(t, mainRoot, 2)
			writePreplanTopic(t, mainRoot, "auth-flow.md", verifyTopicText("auth flow"))
			writePreplanTopic(t, mainRoot, "2026-11-13-fenced.md", preplanSkeletonFor("fenced")+"\n```\nopen\n")
			writeFile(t, filepath.Join(activeRoot, "docs", "req.md"), "# Requirements\n")
			beforeMain, beforeActive := verifyTreeHash(t, mainRoot), verifyTreeHash(t, activeRoot)
			_, _ = planSupportCore(mainRoot, activeRoot, tc.in)
			if verifyTreeHash(t, mainRoot) != beforeMain {
				t.Errorf("the call changed a file under the main root")
			}
			if verifyTreeHash(t, activeRoot) != beforeActive {
				t.Errorf("the call changed a file under the active root")
			}
		})
	}
}
