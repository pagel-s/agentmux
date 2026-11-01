package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestConsoleNavigationKeepsActiveViewVisible(t *testing.T) {
	labels := []string{"1 Board", "2 Agents", "3 Links", "4 Memory", "5 Tasks", "6 Sessions", "7 Teams", "8 Events"}
	for _, width := range []int{60, 74, 80, 110} {
		for tab, label := range labels {
			t.Run(fmt.Sprintf("%dx16/view%d", width, tab+1), func(t *testing.T) {
				m := console{width: width, height: 16, tab: tab}
				navigation := ansi.Strip(strings.Split(m.View(), "\n")[1])
				if width >= 100 && tab == 2 {
					label = "3 Connections"
				}
				if !strings.Contains(navigation, label) {
					t.Fatalf("current view %q is hidden: %q", label, navigation)
				}
				if ansi.StringWidth(navigation) > width {
					t.Fatalf("navigation exceeds terminal width: %q", navigation)
				}
				if !strings.Contains(navigation, "1 Board") && !strings.HasPrefix(navigation, "‹ ") {
					t.Fatalf("missing left overflow cue: %q", navigation)
				}
				if !strings.Contains(navigation, "8 Events") && !strings.HasSuffix(strings.TrimRight(navigation, " "), " ›") {
					t.Fatalf("missing right overflow cue: %q", navigation)
				}
				if width == 110 && (strings.Contains(navigation, "‹") || strings.Contains(navigation, "›")) {
					t.Fatalf("full-width navigation unexpectedly hides destinations: %q", navigation)
				}
			})
		}
	}
}
