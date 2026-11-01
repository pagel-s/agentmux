package main

// Help is an option until --, never the value of a workspace option or literal
// message text after --. Keep this in step with parseWorkOptions.
func workHelpRequested(args []string) bool {
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--":
			return false
		case "--help", "-h":
			return true
		case "--state", "--session", "--config", "--project", "--sandbox", "--view", "--group", "--board", "--legacy-policy":
			i++
		}
	}
	return false
}

func helpArgument(value string) bool { return value == "--help" || value == "-h" }
