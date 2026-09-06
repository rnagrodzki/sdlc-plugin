package tools

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/rnagrodzki/sdlc-plugin/internal/configmigrate"
	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/worktree"
)

// ---------------------------------------------------------------------------
// migrate tool
// ---------------------------------------------------------------------------

// MigrateIn is the input for the migrate tool.
type MigrateIn struct {
	// Action selects the migration to run: "config", "jira_templates", or
	// "learnings_log".
	Action string `json:"action"`
	// DryRun, when true, reports what would change without writing.
	DryRun bool `json:"dryRun"`
}

// MigrateOut is the output for the migrate tool.
type MigrateOut struct {
	OK      bool     `json:"ok"`
	Action  string   `json:"action"`
	DryRun  bool     `json:"dryRun"`
	Result  string   `json:"result"`
	Changed []string `json:"changed"`
	Errors  []string `json:"errors,omitempty"`
}

// migrate is the core logic, separated from the handler for testability.
func migrate(root string, in MigrateIn) (MigrateOut, error) {
	switch in.Action {
	case "config":
		return migrateConfig(root, in.DryRun)
	case "jira_templates":
		return migrateJiraTemplates(root, in.DryRun)
	case "learnings_log":
		return migrateLearningsLog(root, in.DryRun)
	default:
		return MigrateOut{}, &mcpserver.DomainError{
			Msg: fmt.Sprintf("unknown migrate action %q; must be one of: config, jira_templates, learnings_log", in.Action),
		}
	}
}

// migrateConfig delegates to the configmigrate engine.
func migrateConfig(root string, dryRun bool) (MigrateOut, error) {
	if dryRun {
		// DryRun was removed from configmigrate.Options (Task 8 decision).
		// Resolve at the tool layer: report would-migrate based on Verify.
		err := configmigrate.Verify(root)
		if err == nil {
			return MigrateOut{
				OK:      true,
				Action:  "config",
				DryRun:  true,
				Result:  "up-to-date",
				Changed: []string{},
			}, nil
		}
		return MigrateOut{
			OK:      true,
			Action:  "config",
			DryRun:  true,
			Result:  fmt.Sprintf("would-migrate: %s", err.Error()),
			Changed: []string{},
		}, nil
	}

	report, err := configmigrate.Migrate(root, configmigrate.Options{})
	if err != nil {
		return MigrateOut{}, &mcpserver.InfraError{
			Msg:   fmt.Sprintf("config migration failed: %s", err.Error()),
			Cause: err,
		}
	}

	var changed []string
	if report.Migrated {
		changed = append(changed, ".sdlc/config.json")
		// local.json may also have been written.
		localPath := filepath.Join(root, ".sdlc", "local.json")
		if _, err := os.Stat(localPath); err == nil {
			changed = append(changed, ".sdlc/local.json")
		}
	}

	result := "up-to-date"
	if report.Migrated {
		result = fmt.Sprintf("migrated (steps: %v, legacy ingested: %v)",
			report.StepsApplied, report.LegacyIngested)
	}

	return MigrateOut{
		OK:      true,
		Action:  "config",
		DryRun:  false,
		Result:  result,
		Changed: changed,
	}, nil
}

// migrateJiraTemplates moves .claude/jira-templates/ to .sdlc/jira-templates/.
func migrateJiraTemplates(root string, dryRun bool) (MigrateOut, error) {
	srcDir := filepath.Join(root, ".claude", "jira-templates")
	dstDir := filepath.Join(root, ".sdlc", "jira-templates")

	srcExists := migrateDirExists(srcDir)
	dstExists := migrateDirExists(dstDir)

	// Determine outcome.
	switch {
	case !srcExists && !dstExists:
		return MigrateOut{
			OK:      true,
			Action:  "jira_templates",
			DryRun:  dryRun,
			Result:  "noop: no legacy jira-templates directory found",
			Changed: []string{},
		}, nil

	case !srcExists && dstExists:
		return MigrateOut{
			OK:      true,
			Action:  "jira_templates",
			DryRun:  dryRun,
			Result:  "already-migrated: .sdlc/jira-templates/ exists, no legacy source",
			Changed: []string{},
		}, nil

	case srcExists && dstExists:
		return MigrateOut{
			OK:      true,
			Action:  "jira_templates",
			DryRun:  dryRun,
			Result:  "skip: both .claude/jira-templates/ and .sdlc/jira-templates/ exist; manual resolution needed",
			Changed: []string{},
		}, nil

	default:
		// srcExists && !dstExists — migrate.
		if dryRun {
			return MigrateOut{
				OK:      true,
				Action:  "jira_templates",
				DryRun:  true,
				Result:  "would-move: .claude/jira-templates/ -> .sdlc/jira-templates/",
				Changed: []string{},
			}, nil
		}

		if err := os.MkdirAll(filepath.Dir(dstDir), 0o755); err != nil {
			return MigrateOut{}, &mcpserver.InfraError{
				Msg:   fmt.Sprintf("create .sdlc directory: %s", err.Error()),
				Cause: err,
			}
		}
		if err := os.Rename(srcDir, dstDir); err != nil {
			return MigrateOut{}, &mcpserver.InfraError{
				Msg:   fmt.Sprintf("move jira-templates: %s", err.Error()),
				Cause: err,
			}
		}
		return MigrateOut{
			OK:      true,
			Action:  "jira_templates",
			DryRun:  false,
			Result:  "moved: .claude/jira-templates/ -> .sdlc/jira-templates/",
			Changed: []string{".sdlc/jira-templates/"},
		}, nil
	}
}

// migrateLearningsLog moves .claude/learnings/log.md to .sdlc/learnings/log.md.
func migrateLearningsLog(root string, dryRun bool) (MigrateOut, error) {
	srcFile := filepath.Join(root, ".claude", "learnings", "log.md")
	dstFile := filepath.Join(root, ".sdlc", "learnings", "log.md")

	srcExists := migrateFileExists(srcFile)
	dstExists := migrateFileExists(dstFile)

	switch {
	case !srcExists && !dstExists:
		return MigrateOut{
			OK:      true,
			Action:  "learnings_log",
			DryRun:  dryRun,
			Result:  "noop: no legacy learnings log found",
			Changed: []string{},
		}, nil

	case !srcExists && dstExists:
		return MigrateOut{
			OK:      true,
			Action:  "learnings_log",
			DryRun:  dryRun,
			Result:  "already-migrated: .sdlc/learnings/log.md exists, no legacy source",
			Changed: []string{},
		}, nil

	case srcExists && dstExists:
		return MigrateOut{
			OK:      true,
			Action:  "learnings_log",
			DryRun:  dryRun,
			Result:  "skip: both .claude/learnings/log.md and .sdlc/learnings/log.md exist; manual resolution needed",
			Changed: []string{},
		}, nil

	default:
		// srcExists && !dstExists — migrate.
		if dryRun {
			return MigrateOut{
				OK:      true,
				Action:  "learnings_log",
				DryRun:  true,
				Result:  "would-move: .claude/learnings/log.md -> .sdlc/learnings/log.md",
				Changed: []string{},
			}, nil
		}

		dstDir := filepath.Dir(dstFile)
		if err := os.MkdirAll(dstDir, 0o755); err != nil {
			return MigrateOut{}, &mcpserver.InfraError{
				Msg:   fmt.Sprintf("create .sdlc/learnings directory: %s", err.Error()),
				Cause: err,
			}
		}
		if err := copyFile(srcFile, dstFile); err != nil {
			return MigrateOut{}, &mcpserver.InfraError{
				Msg:   fmt.Sprintf("copy learnings log: %s", err.Error()),
				Cause: err,
			}
		}
		if err := os.Remove(srcFile); err != nil {
			// Non-fatal: file was copied successfully.
			return MigrateOut{
				OK:      true,
				Action:  "learnings_log",
				DryRun:  false,
				Result:  "moved: .claude/learnings/log.md -> .sdlc/learnings/log.md (warning: could not remove source)",
				Changed: []string{".sdlc/learnings/log.md"},
			}, nil
		}
		return MigrateOut{
			OK:      true,
			Action:  "learnings_log",
			DryRun:  false,
			Result:  "moved: .claude/learnings/log.md -> .sdlc/learnings/log.md",
			Changed: []string{".sdlc/learnings/log.md"},
		}, nil
	}
}

// migrateDirExists returns true if path exists and is a directory.
func migrateDirExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir()
}

// migrateFileExists returns true if path exists and is not a directory.
func migrateFileExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

// copyFile copies src to dst by reading and writing content. Used instead
// of os.Rename for learnings log because src and dst may be on different
// filesystems (different mount points).
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}

// ---------------------------------------------------------------------------
// Registration
// ---------------------------------------------------------------------------

// RegisterMigrateTools registers the migrate tool on the server.
func RegisterMigrateTools(s *mcpserver.Server) {
	mcpserver.Register(s, "migrate",
		"Runs a legacy-to-v5 migration. Actions: config (schema migration via configmigrate engine), jira_templates (move .claude/jira-templates/ to .sdlc/), learnings_log (move .claude/learnings/log.md to .sdlc/).",
		func(ctx mcpserver.Ctx, in MigrateIn) (MigrateOut, error) {
			root, err := worktree.MainRoot()
			if err != nil {
				root, err = os.Getwd()
				if err != nil {
					return MigrateOut{}, &mcpserver.InfraError{
						Msg:   fmt.Sprintf("resolve project root: %s", err.Error()),
						Cause: err,
					}
				}
			}
			return migrate(root, in)
		},
	)
}
