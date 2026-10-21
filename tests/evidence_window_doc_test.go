package tests

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// cliSubcommands is the dispatch set of cmd/timesheet (main.go's switch on
// args[0], plus the `help` branch), hardcoded on purpose: this test pins the
// DOCUMENT, not the CLI, so it must fail when the doc invents a subcommand
// rather than silently re-reading whatever main.go happens to accept.
var cliSubcommands = map[string]bool{
	"preflight": true,
	"review":    true,
	"export":    true,
	"preview":   true,
	"approve":   true,
	"submit":    true,
	"resolve":   true,
	"run":       true,
	"classify":  true,
	"metrics":   true,
	"config":    true,
	"help":      true,
}

// TestEvidenceWindowCorrectionProcedure pins the OWNER-RUN correction procedure
// for days collected under the pre-4e50ae6 window (EVIDENCE-WINDOW-CORRECTION-1).
// It asserts the load-bearing safety sentences, the flags the procedure depends
// on, and that every subcommand the note names really exists.
func TestEvidenceWindowCorrectionProcedure(t *testing.T) {
	repoRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("resolving repo root: %v", err)
	}

	docPath := filepath.Join(repoRoot, "docs", "agents", "evidence-window-correction.md")
	docBytes, err := os.ReadFile(docPath)
	if err != nil {
		t.Fatalf("failed to read correction procedure at %s: %v", docPath, err)
	}
	content := string(docBytes)
	// Markdown wraps prose, so sentence-level assertions run on whitespace-
	// normalized text; command/flag assertions stay on the raw bytes.
	prose := strings.Join(strings.Fields(content), " ")

	// 1. The load-bearing safety phrases. Each one is a claim the operator relies
	// on; dropping it silently changes the procedure's guarantees.
	required := []string{
		"owner-gated",       // corrections are owner-stated, never automatic
		"day-scoped",        // exactly one named date per run
		"No silent rewrite", // private records are never rewritten in place
		"Approval invalidation",
		"LocalFingerprint", // the local binding hash
		"PlanHash",         // the plan content hash
		"no corrective CLI exists",
		"A re-collection alone moves nothing",
		"does not move an approval",
		"0001-01-01T00:00:00Z", // a local-git row has no end; never test it
	}
	for _, phrase := range required {
		if !strings.Contains(prose, phrase) {
			t.Errorf("correction procedure missing load-bearing phrase %q", phrase)
		}
	}

	// 2. The commands and flags the procedure is built on.
	for _, cmd := range []string{
		"timesheet preflight",
		"timesheet review --config",
		"timesheet preview --config",
		"timesheet approve --config",
		"timesheet export --config",
		"--decisions corrections.json",
		"--plan <PLANID",
	} {
		if !strings.Contains(content, cmd) {
			t.Errorf("correction procedure missing existing command or flag %q", cmd)
		}
	}

	// 3. Every subcommand the note names must exist. An invented corrective verb
	// (`timesheet recollect …`) is exactly what this procedure must never imply.
	mention := regexp.MustCompile(`timesheet[ \t]+([a-z][a-z-]*)`)
	for _, m := range mention.FindAllStringSubmatch(content, -1) {
		if !cliSubcommands[m[1]] {
			t.Errorf("correction procedure names a subcommand that does not exist: %q", "timesheet "+m[1])
		}
	}

	// 4. The procedure must not instruct a production submission.
	if strings.Contains(content, "timesheet submit --config") {
		t.Errorf("correction procedure must not run a production submission")
	}

	// 5. It must state the honest provenance gap: there is no persisted window,
	// and the excluded evidence is not hashed into the local fingerprint.
	for _, phrase := range []string{
		"It does **not** hash",
		"`Record.CollectedEvidence`",
		"never persisted",
		"is not exhaustive",
	} {
		if !strings.Contains(prose, phrase) {
			t.Errorf("correction procedure missing provenance claim %q", phrase)
		}
	}
}
