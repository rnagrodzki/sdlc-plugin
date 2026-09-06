// Package jirakeys extracts Jira issue keys from arbitrary text.
//
// It ports scripts/lib/jira-keys.js's extractKeys to Go. The canonical
// pattern is \b[A-Z]{2,10}-\d+\b — 2-10 uppercase letters, a dash, then
// one or more digits, word-bounded on both ends. Results are unique and
// preserve first-occurrence order.
package jirakeys

import "regexp"

// jiraKeyRe is the compiled canonical pattern from jira-keys.js.
// Word boundaries (\b) prevent embedded matches inside longer tokens.
var jiraKeyRe = regexp.MustCompile(`\b([A-Z]{2,10}-\d+)\b`)

// Extract returns all unique Jira issue keys found in text, preserving
// first-occurrence order. It mirrors jira-keys.js extractKeys(text).
func Extract(text string) []string {
	if text == "" {
		return nil
	}
	matches := jiraKeyRe.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(matches))
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		key := m[1]
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, key)
	}
	return out
}
