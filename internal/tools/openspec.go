package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/rnagrodzki/sdlc-plugin/internal/mcpserver"
	"github.com/rnagrodzki/sdlc-plugin/internal/openspec"
	"github.com/rnagrodzki/sdlc-plugin/internal/worktree"
)

// enrichVersion is the managed-block version stamped into openspec/config.yaml.
const enrichVersion = 2

// enrichBeginRe matches the opening sentinel of the managed block with its
// version capture group.
var enrichBeginRe = regexp.MustCompile(`(?m)^# BEGIN MANAGED BY sdlc-v2 \(v(\d+)\)$`)

// enrichEndRe matches the closing sentinel.
var enrichEndRe = regexp.MustCompile(`(?m)^# END MANAGED BY sdlc-v2 \(v\d+\)$`)

// enrichBlockTemplate is the full managed block injected into
// openspec/config.yaml. It mirrors the BLOCK_TEMPLATE constant in the JS
// source (openspec-enrich.js).
var enrichBlockTemplate = fmt.Sprintf(`# BEGIN MANAGED BY sdlc-v2 (v%d)
context: |
  SDLC workflow managed by sdlc-v2. Do not edit this block manually.
  To update: /setup --openspec-enrich. To remove: /setup --remove-openspec.

  Contributor workflow:
    1. /plan --from-openspec <change-name>  — create an implementation plan from the change
    2. /execute                              — execute the plan in waves
    3. /ship                                 — commit, review, version, and open a PR

  Do not invoke `+"`"+`openspec archive`+"`"+` directly — /ship handles archival
  as a conditional pipeline step after validation passes.
# END MANAGED BY sdlc-v2 (v%d)`, enrichVersion, enrichVersion)

// --- Input / Output types ---

// OpenspecEnrichIn is the input for the openspec_enrich tool.
type OpenspecEnrichIn struct {
	// Change optionally names an openspec change to match against. When
	// non-empty, Detect() is called to resolve the change and include its
	// status in the output.
	Change string `json:"change" jsonschema_description:"Optional openspec change name to match against. When non-empty, the change is resolved and its status is included in the output."`
	// Remove, when true, removes the managed block instead of adding it.
	Remove bool `json:"remove" jsonschema_description:"When true, removes the managed block from openspec/config.yaml instead of adding/updating it."`
}

// OpenspecEnrichOut is the output for the openspec_enrich tool.
type OpenspecEnrichOut struct {
	OK      bool   `json:"ok"`
	Action  string `json:"action"`
	Version int    `json:"version"`
	Path    string `json:"path"`
	Changed bool   `json:"changed"`
	Warning string `json:"warning,omitempty"`
	Error   string `json:"error,omitempty"`

	// MatchedChange is populated only when the input Change is non-empty and
	// a matching openspec change was found.
	MatchedChange *openspec.Change `json:"matchedChange,omitempty"`
}

// RegisterOpenspecTools registers openspec-related tools on the server.
func RegisterOpenspecTools(s *mcpserver.Server) {
	mcpserver.Register(s, "openspec_enrich",
		"INTERNAL — called by sdlc skills only. Idempotent enrichment of openspec/config.yaml with a managed block pointing contributors to sdlc-utilities skills.",
		func(ctx mcpserver.Ctx, in OpenspecEnrichIn) (OpenspecEnrichOut, error) {
			root, err := worktree.MainRoot()
			if err != nil {
				return OpenspecEnrichOut{}, &mcpserver.InfraError{Msg: fmt.Sprintf("resolve project root: %s", err.Error()), Cause: err}
			}
			return enrichConfig(root, in)
		},
	)
}

// enrichConfig is the core logic, separated from the handler for testability.
func enrichConfig(root string, in OpenspecEnrichIn) (OpenspecEnrichOut, error) {
	configPath := filepath.Join(root, "openspec", "config.yaml")

	// Resolve optional change input via openspec.Detect.
	var matchedChange *openspec.Change
	if in.Change != "" {
		info, err := openspec.Detect(root)
		if err == nil && info != nil {
			for i := range info.Changes {
				if strings.EqualFold(info.Changes[i].Name, in.Change) {
					matchedChange = &info.Changes[i]
					break
				}
			}
		}
	}

	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return OpenspecEnrichOut{
			OK:            false,
			Action:        "missing",
			Version:       enrichVersion,
			Path:          configPath,
			Changed:       false,
			Error:         "openspec/config.yaml not found",
			MatchedChange: matchedChange,
		}, nil
	}

	content, err := os.ReadFile(configPath)
	if err != nil {
		return OpenspecEnrichOut{}, &mcpserver.InfraError{
			Msg:   fmt.Sprintf("read %s: %s", configPath, err.Error()),
			Cause: err,
		}
	}

	block := enrichDetectBlock(string(content))

	// --remove mode
	if in.Remove {
		if !block.found {
			return OpenspecEnrichOut{
				OK:            true,
				Action:        "removed",
				Version:       enrichVersion,
				Path:          configPath,
				Changed:       false,
				MatchedChange: matchedChange,
			}, nil
		}

		before := strings.TrimRight(string(content[:block.startIdx]), "\n") + "\n"
		after := strings.TrimLeft(string(content[block.endIdx:]), "\n")
		newContent := before + after

		if err := os.WriteFile(configPath, []byte(newContent), 0644); err != nil {
			return OpenspecEnrichOut{}, &mcpserver.InfraError{
				Msg:   fmt.Sprintf("write %s: %s", configPath, err.Error()),
				Cause: err,
			}
		}
		return OpenspecEnrichOut{
			OK:            true,
			Action:        "removed",
			Version:       enrichVersion,
			Path:          configPath,
			Changed:       true,
			MatchedChange: matchedChange,
		}, nil
	}

	text := string(content)

	// No block present → append (unless existing context: key would collide)
	if !block.found {
		if enrichHasExistingContextKey(text, block) {
			return OpenspecEnrichOut{
				OK:            true,
				Action:        "skipped-existing-context",
				Version:       enrichVersion,
				Path:          configPath,
				Changed:       false,
				Warning:       "Top-level context: key already present in openspec/config.yaml. Refusing to inject a duplicate. Manually fold sdlc-utilities guidance into your existing context: value, then re-run --openspec-enrich.",
				MatchedChange: matchedChange,
			}, nil
		}
		separator := "\n"
		if !strings.HasSuffix(text, "\n") {
			separator = "\n\n"
		}
		newContent := text + separator + enrichBlockTemplate + "\n"
		if err := os.WriteFile(configPath, []byte(newContent), 0644); err != nil {
			return OpenspecEnrichOut{}, &mcpserver.InfraError{
				Msg:   fmt.Sprintf("write %s: %s", configPath, err.Error()),
				Cause: err,
			}
		}
		return OpenspecEnrichOut{
			OK:            true,
			Action:        "append",
			Version:       enrichVersion,
			Path:          configPath,
			Changed:       true,
			MatchedChange: matchedChange,
		}, nil
	}

	// Block at higher version → no-op with warning
	if block.version > enrichVersion {
		return OpenspecEnrichOut{
			OK:            true,
			Action:        "unchanged",
			Version:       block.version,
			Path:          configPath,
			Changed:       false,
			Warning:       fmt.Sprintf("Managed block is at v%d, plugin ships v%d. Use --remove to downgrade.", block.version, enrichVersion),
			MatchedChange: matchedChange,
		}, nil
	}

	// Block at current version → no-op
	if block.version == enrichVersion {
		return OpenspecEnrichOut{
			OK:            true,
			Action:        "unchanged",
			Version:       enrichVersion,
			Path:          configPath,
			Changed:       false,
			MatchedChange: matchedChange,
		}, nil
	}

	// Block at lower version → update in place
	if enrichHasExistingContextKey(text, block) {
		return OpenspecEnrichOut{
			OK:            true,
			Action:        "skipped-existing-context",
			Version:       block.version,
			Path:          configPath,
			Changed:       false,
			Warning:       "Top-level context: key already present outside the managed block in openspec/config.yaml. Refusing to update — a duplicate context: key would result. Manually fold sdlc-utilities guidance into your existing context: value, then re-run --openspec-enrich.",
			MatchedChange: matchedChange,
		}, nil
	}

	before := text[:block.startIdx]
	after := text[block.endIdx:]
	newContent := before + enrichBlockTemplate + after
	if err := os.WriteFile(configPath, []byte(newContent), 0644); err != nil {
		return OpenspecEnrichOut{}, &mcpserver.InfraError{
			Msg:   fmt.Sprintf("write %s: %s", configPath, err.Error()),
			Cause: err,
		}
	}
	return OpenspecEnrichOut{
		OK:            true,
		Action:        "update",
		Version:       enrichVersion,
		Path:          configPath,
		Changed:       true,
		MatchedChange: matchedChange,
	}, nil
}

// enrichBlockInfo holds the parse result of detecting the managed block.
type enrichBlockInfo struct {
	found    bool
	version  int
	startIdx int
	endIdx   int
}

// enrichDetectBlock finds the managed block in content, mirroring
// openspec-enrich.js's detectBlock.
func enrichDetectBlock(content string) enrichBlockInfo {
	beginLoc := enrichBeginRe.FindStringSubmatchIndex(content)
	if beginLoc == nil {
		return enrichBlockInfo{}
	}

	versionStr := content[beginLoc[2]:beginLoc[3]]
	version := 1
	fmt.Sscanf(versionStr, "%d", &version)

	startIdx := beginLoc[0]
	rest := content[beginLoc[1]:]

	endLoc := enrichEndRe.FindStringIndex(rest)
	if endLoc == nil {
		return enrichBlockInfo{}
	}

	endIdx := beginLoc[1] + endLoc[1]

	return enrichBlockInfo{
		found:    true,
		version:  version,
		startIdx: startIdx,
		endIdx:   endIdx,
	}
}

// enrichHasExistingContextKey checks whether the file declares a top-level
// context: key OUTSIDE the managed block, mirroring the JS hasExistingContextKey.
func enrichHasExistingContextKey(content string, block enrichBlockInfo) bool {
	outsideBlock := content
	if block.found {
		outsideBlock = content[:block.startIdx] + content[block.endIdx:]
	}
	return regexp.MustCompile(`(?m)^context\s*:`).MatchString(outsideBlock)
}
