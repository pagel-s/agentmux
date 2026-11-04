package main

import "strings"

// Startup/resume and in-workbench workspace selection share one literal,
// case-insensitive word filter. Display names are never workspace identities.
func matchingWorkspaceRows(rows []workspaceListing, query string) []workspaceListing {
	words := strings.Fields(strings.ToLower(query))
	if len(words) == 0 {
		return rows
	}
	var matches []workspaceListing
	for _, row := range rows {
		text := strings.ToLower(row.Session + " " + row.State + " " + row.Status)
		match := true
		for _, word := range words {
			if !strings.Contains(text, word) {
				match = false
				break
			}
		}
		if match {
			matches = append(matches, row)
		}
	}
	return matches
}
