package sessioninventory

import "sort"

func sortSessions(s []Session) { sort.Slice(s, func(i, j int) bool { return s[i].ID < s[j].ID }) }
