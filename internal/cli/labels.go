package cli

// displayState translates internal planning and execution states into the
// shorter terms shown by CLI commands. Internal state names remain stable for
// the deployment logic.
func displayState(state string) string {
	switch state {
	case "deferred":
		return "waiting"
	case "unchanged":
		return "current"
	case "deleted":
		return "delete"
	default:
		return state
	}
}
