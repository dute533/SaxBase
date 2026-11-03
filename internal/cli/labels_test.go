package cli

import "testing"

func TestDisplayState(t *testing.T) {
	tests := map[string]string{
		"deferred":  "waiting",
		"unchanged": "current",
		"deleted":   "delete",
		"applied":   "applied",
	}
	for state, want := range tests {
		if got := displayState(state); got != want {
			t.Errorf("displayState(%q) = %q, want %q", state, got, want)
		}
	}
}
