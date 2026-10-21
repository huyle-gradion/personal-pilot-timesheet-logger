package calendar

import (
	"context"
	"os"
	"strings"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/keychain"
)

// SourceEnvVar is the env var holding the secret iCal URL. It names the
// credential, never its value, so it is safe to log and to report as a
// missing key.
const SourceEnvVar = "GCAL_ICS_URL"

// PreflightStatus conveys whether prerequisites are met to collect calendar
// activity. Source carries the resolved iCal URL or local .ics path and is
// reported only when Ready, so a caller can hand it straight to the fetcher.
type PreflightStatus struct {
	Ready       bool     `json:"ready"`
	MissingKeys []string `json:"missing_keys"`
	Guide       string   `json:"guide"`
	Source      string   `json:"source,omitempty"`
	Error       string   `json:"error,omitempty"`
}

// ResolveSource resolves the iCal source from an explicit argument, the
// environment, or the macOS Keychain.
//
// Only a genuine Keychain NotFound (absent item, or unavailable security
// tooling) falls through to "missing". A timeout, access failure, or any other
// non-NotFound failure stops resolution and is returned as a typed,
// secret-free error, so a real Keychain failure is never silently downgraded
// to "not configured".
func ResolveSource(ctx context.Context, explicitURL string) (string, error) {
	if src := strings.TrimSpace(explicitURL); src != "" {
		return src, nil
	}
	if src := strings.TrimSpace(os.Getenv(SourceEnvVar)); src != "" {
		return src, nil
	}
	// Keychain is the authoritative secret store.
	if src, err := keychain.Lookup(ctx, keychain.ServiceCalendarFeed); err != nil {
		if !keychain.IsNotFound(err) {
			return "", err
		}
	} else if src != "" {
		return src, nil
	}
	return "", nil
}

// CheckPreflight validates prerequisites for calendar collection: a secret
// iCal URL (explicit, env, or Keychain) or a local .ics file for testing.
//
// explicitURL is whatever the CLI already resolved from its own flags (-file
// then -url). A non-empty explicit value is Ready and short-circuits without
// touching the Keychain.
func CheckPreflight(ctx context.Context, explicitURL string) PreflightStatus {
	var missing []string
	var guideParts []string

	source, resolveErr := ResolveSource(ctx, explicitURL)
	if resolveErr != nil {
		// Surface the typed, secret-free Keychain failure instead of masking it
		// as a missing source or silently degrading to another source.
		guideParts = append(guideParts,
			"Calendar iCal source lookup failed:\n"+
				"  "+resolveErr.Error()+"\n"+
				"Resolve the macOS Keychain error (or pass -url/-file for tests/CI) before retrying. "+
				"Resolution does not fall back to the environment after a non-NotFound Keychain failure.",
		)
		return PreflightStatus{
			Ready:       false,
			MissingKeys: []string{SourceEnvVar},
			Guide:       strings.Join(guideParts, "\n\n"),
			Error:       resolveErr.Error(),
		}
	}
	if source == "" {
		missing = append(missing, SourceEnvVar)
		guideParts = append(guideParts,
			"Calendar iCal source is missing or not configured.\n"+
				"To resolve this, please do one of the following:\n"+
				"  1. Store the secret iCal address in the macOS Keychain (recommended):\n"+
				"     security add-generic-password -s calendar_feed_url -a \"$USER\" -l calendar_feed_url -w\n"+
				"     (security prompts for the secret; never place it on the command line)\n\n"+
				"  2. Or export the GCAL_ICS_URL environment variable:\n"+
				"     export GCAL_ICS_URL=\"<secret iCal address>\"\n\n"+
				"  3. Or pass the address for a single run:\n"+
				"     -url=\"<secret iCal address>\"\n\n"+
				"  4. Or pass a local .ics file for testing:\n"+
				"     -file=./event.ics\n\n"+
				"The secret address comes from Google Calendar:\n"+
				"  calendar.google.com -> Settings & Sharing -> Integrate calendar -> Secret address in iCal format",
		)
	}

	ready := len(missing) == 0
	guide := strings.Join(guideParts, "\n\n")

	return PreflightStatus{
		Ready:       ready,
		MissingKeys: missing,
		Guide:       guide,
		Source:      source,
	}
}
