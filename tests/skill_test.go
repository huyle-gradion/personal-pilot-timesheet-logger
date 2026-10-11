package tests

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestClaudeSkill_PackagingAndContracts verifies the skill packaging, its
// harness-agnostic command sequence, and that the skill delegates to the shared
// CLI rather than reimplementing accounting/submission. It also asserts the
// retired binaries/packages are gone.
func TestClaudeSkill_PackagingAndContracts(t *testing.T) {
	repoRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("resolving repo root: %v", err)
	}

	skillMDPath := filepath.Join(repoRoot, ".claude", "skills", "timesheet", "SKILL.md")
	workflowMDPath := filepath.Join(repoRoot, ".claude", "skills", "timesheet", "workflow.md")

	// 1. SKILL.md exists and starts with YAML frontmatter.
	skillBytes, err := os.ReadFile(skillMDPath)
	if err != nil {
		t.Fatalf("failed to read SKILL.md at %s: %v", skillMDPath, err)
	}
	skillContent := string(skillBytes)
	if !strings.HasPrefix(skillContent, "---\n") {
		t.Fatalf("SKILL.md must start with YAML frontmatter delimiter '---'")
	}
	frontmatterEnd := strings.Index(skillContent[4:], "\n---")
	if frontmatterEnd == -1 {
		t.Fatalf("SKILL.md missing closing YAML frontmatter delimiter '---'")
	}
	frontmatter := skillContent[4 : 4+frontmatterEnd]
	if !strings.Contains(frontmatter, "name: timesheet") {
		t.Errorf("YAML frontmatter missing 'name: timesheet'")
	}
	if !strings.Contains(frontmatter, "description:") {
		t.Errorf("YAML frontmatter missing 'description:' field")
	}

	// 2. Trigger phrases.
	for _, tr := range []string{"/timesheet", "log timesheet", "daily timesheet"} {
		if !strings.Contains(skillContent, tr) {
			t.Errorf("SKILL.md missing required trigger phrase %q", tr)
		}
	}

	// 3. workflow.md exists.
	wfBytes, err := os.ReadFile(workflowMDPath)
	if err != nil {
		t.Fatalf("failed to read workflow.md at %s: %v", workflowMDPath, err)
	}
	wfContent := string(wfBytes)

	// 4. Every step heading carries its [Script]/[AI] marker.
	requiredSteps := []struct {
		nameRegex string
	}{
		{`(?i)Step 1:.*\[Script\]`},
		{`(?i)Step 2:.*\[Script\]`},
		{`(?i)Step 3:.*\[AI\]`},
		{`(?i)Step 4:.*\[Script\]`},
		{`(?i)Step 5:.*\[Script\]`},
		{`(?i)Step 6:.*\[Script\]`},
		{`(?i)Step 7:.*\[Script\]`},
	}
	for _, step := range requiredSteps {
		if !regexp.MustCompile(step.nameRegex).MatchString(wfContent) {
			t.Errorf("workflow.md missing expected step heading matching %q", step.nameRegex)
		}
	}

	// 5. The workflow delegates to the CLI subcommands.
	for _, cmd := range []string{"preflight", "review", "preview", "approve", "submit", "export", "resolve"} {
		if !strings.Contains(wfContent, "timesheet "+cmd) && !strings.Contains(wfContent, "$TS "+cmd) {
			t.Errorf("workflow.md does not invoke the %q subcommand", cmd)
		}
	}

	// 6. The stale infer-time pipeline and the bare -approve gate are gone.
	combined := skillContent + "\n" + wfContent
	for _, stale := range []string{"-approve", "clusterer", "skeleton.json", "refined.json", "cmd/sync-timesheet"} {
		if strings.Contains(combined, stale) {
			t.Errorf("skill still references the retired pipeline element %q", stale)
		}
	}

	// 7. The new CLI entrypoint exists; the retired binary/package are gone.
	if _, statErr := os.Stat(filepath.Join(repoRoot, "cmd", "timesheet", "main.go")); statErr != nil {
		t.Errorf("cmd/timesheet/main.go does not exist: %v", statErr)
	}
	for _, gone := range []string{
		filepath.Join("cmd", "sync-timesheet"),
		filepath.Join("internal", "api"),
	} {
		if _, statErr := os.Stat(filepath.Join(repoRoot, gone)); statErr == nil {
			t.Errorf("retired path still exists: %s", gone)
		}
	}

	// 8. The skill delegates the guided run to the shared core and links the
	// detailed journal/guided docs instead of duplicating their flags.
	for _, want := range []string{
		"run --interactive",
		"run --plain",
		"guided-workflow.md",
		"journal-source.md",
	} {
		if !strings.Contains(combined, want) {
			t.Errorf("skill docs missing guided/journal delegation reference %q", want)
		}
	}

	// 9. The skill states the binding boundaries: no independent accounting, the
	// guided run never approves/submits, and journal notes are read-only evidence.
	for _, want := range []string{
		"no accounting",
		"never approves or submits",
		"read-only narrative evidence",
		"UNTIMED",
	} {
		if !strings.Contains(combined, want) {
			t.Errorf("skill docs missing binding boundary %q", want)
		}
	}

	// 10. The decisions schema documents the actual ReviewDecisions fields.
	for _, field := range []string{
		"block_id", "evidence_id", "work_block_id", "meeting_ev_id",
		"new_start", "mark_empty", "acknowledge", "evidence_ids",
		"classification_id", "classification_code", "classification_name",
	} {
		if !strings.Contains(wfContent, field) {
			t.Errorf("workflow.md decisions schema missing actual field %q", field)
		}
	}

	// 11. The shipped personal example config exists for the guided workflow.
	if _, statErr := os.Stat(filepath.Join(repoRoot, "cmd", "timesheet", "timesheet.personal.example.json")); statErr != nil {
		t.Errorf("cmd/timesheet/timesheet.personal.example.json does not exist: %v", statErr)
	}
}
