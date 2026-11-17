package skillcheck

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// dashboardSkillsText returns the text of skills/dashboard/SKILL.md.
func dashboardSkillsText(t *testing.T) string {
	t.Helper()
	path := filepath.Join(setupSkillsRepoRoot(t), "skills/dashboard/SKILL.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

// TestDashboardSkillStep3OpensT3PreviewTab pins Step 3 of the dashboard skill:
// the heading between Step 2 and Rules, the tool call, and both print rows.
func TestDashboardSkillStep3OpensT3PreviewTab(t *testing.T) {
	text := dashboardSkillsText(t)
	setupSkillsAssertInOrder(t, "skills/dashboard/SKILL.md", text, []string{
		"## Step 2: Call the tool and print the message",
		"## Step 3: Open the T3 preview tab",
		"select:mcp__t3-code__preview_open",
		"mcp__t3-code__preview_open({ url: \"<previewUrl>\" })",
		"`Dashboard: <previewUrl> (T3 preview tab)`",
		"`Warning: T3 preview tab did not open — <reason>.`",
		"## Rules",
	})
	section, err := setupSkillsTextSection(text, "## Step 3", "## Rules")
	if err != nil {
		t.Fatalf("skills/dashboard/SKILL.md: %v", err)
	}
	for _, want := range []string{"Open the T3 preview tab", "never an error", "`--no-open`, `--status` and `--stop`"} {
		if !strings.Contains(section+text, want) {
			t.Errorf("skills/dashboard/SKILL.md: missing %q", want)
		}
	}
}

// TestDashboardSkillExitStatesAndRules pins the T3 exit rows and the changed
// Rules line of the dashboard skill.
func TestDashboardSkillExitStatesAndRules(t *testing.T) {
	text := dashboardSkillsText(t)
	exit, err := setupSkillsTextSection(text, "### Every exit state", "## Rules")
	if err != nil {
		t.Fatalf("skills/dashboard/SKILL.md: %v", err)
	}
	setupSkillsAssertInOrder(t, "skills/dashboard/SKILL.md exit states", exit, []string{
		"**T3, tab opened**",
		"`Dashboard: <previewUrl> (T3 preview tab)`",
		"**T3, tab failed or tool absent**",
		"`Warning: T3 preview tab did not open — <reason>.`",
		"**Error**",
	})
	rules := text[strings.Index(text, "## Rules"):]
	setupSkillsAssertInOrder(t, "skills/dashboard/SKILL.md Rules", rules, []string{
		"This skill makes one dashboard call and at most one mcp__t3-code__preview_open call. It starts no sub-agents.",
		"It is safe to run twice",
	})
	if strings.Contains(text, "This skill is one step.") {
		t.Error("skills/dashboard/SKILL.md: the old Rules line \"This skill is one step.\" must be gone")
	}
	if !strings.Contains(text, "In a T3 Code thread it also opens a T3 preview tab.") {
		t.Error("skills/dashboard/SKILL.md: intro does not name the T3 preview tab")
	}
}
