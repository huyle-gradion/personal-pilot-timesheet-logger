package timesheet

import (
	"strings"
	"testing"
)

// TestMissingCredentialErrorNamesTheFix: the bare "is missing" message sent an
// operator hunting through the skill for a defect while the token sat in an env
// file no command had been told to read. Every credential site must name both
// ways to supply it.
func TestMissingCredentialErrorNamesTheFix(t *testing.T) {
	err := missingCredentialError("SYNTHETIC_MISSING_TOKEN")
	msg := err.Error()
	for _, want := range []string{"SYNTHETIC_MISSING_TOKEN", "--env-file", "export it"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("message %q must contain %q", msg, want)
		}
	}
}
