// Package tools provides shared tooling helpers for MCP tool implementations.
package tools

import "embed"

//go:embed payloads/*
var payloadsFS embed.FS

// Payloads returns a map of filename → file content for every embedded
// consumer-CI payload (workflow templates and scripts). Keys are bare
// filenames such as "check-changelog.cjs", "retag-release.yml", etc.
func Payloads() map[string][]byte {
	entries, err := payloadsFS.ReadDir("payloads")
	if err != nil {
		// Embed is compiled in; ReadDir cannot fail at runtime.
		panic("scaffold_payloads: embedded FS read failed: " + err.Error())
	}

	out := make(map[string][]byte, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data, err := payloadsFS.ReadFile("payloads/" + e.Name())
		if err != nil {
			panic("scaffold_payloads: embedded file read failed: " + err.Error())
		}
		out[e.Name()] = data
	}
	return out
}
