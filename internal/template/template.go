package template

import (
	"fmt"
	"regexp"
	"sort"
)

var placeholderPattern = regexp.MustCompile(`\{\{\s*([a-zA-Z0-9_-]+)\s*\}\}`)

func ExpandString(input string, params map[string]string) (string, error) {
	var missing []string

	out := placeholderPattern.ReplaceAllStringFunc(input, func(match string) string {
		submatches := placeholderPattern.FindStringSubmatch(match)
		if len(submatches) != 2 {
			return match
		}

		value, ok := params[submatches[1]]
		if !ok {
			missing = append(missing, submatches[1])
			return match
		}
		return value
	})

	if len(missing) > 0 {
		sort.Strings(missing)
		return "", fmt.Errorf("missing required params: %v", missing)
	}

	return out, nil
}

func CollectRefs(input string) []string {
	matches := placeholderPattern.FindAllStringSubmatch(input, -1)
	seen := make(map[string]struct{}, len(matches))
	for _, match := range matches {
		if len(match) != 2 {
			continue
		}
		seen[match[1]] = struct{}{}
	}

	refs := make([]string, 0, len(seen))
	for ref := range seen {
		refs = append(refs, ref)
	}
	sort.Strings(refs)
	return refs
}
