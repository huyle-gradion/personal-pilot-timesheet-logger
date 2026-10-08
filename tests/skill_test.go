package tests

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestClaudeSkill_PackagingAndContracts(t *testing.T) {
	// Locate repository root
	repoRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("resolving repo root: %v", err)
	}

	skillMDPath := filepath.Join(repoRoot, ".claude", "skills", "timesheet", "SKILL.md")
	workflowMDPath := filepath.Join(repoRoot, ".claude", "skills", "timesheet", "workflow.md")

	// 1. Verify SKILL.md exists
	skillBytes, err := os.ReadFile(skillMDPath)
	if err != nil {
		t.Fatalf("failed to read SKILL.md at %s: %v", skillMDPath, err)
	}
	skillContent := string(skillBytes)

	// 2. Verify YAML Frontmatter
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

	// 3. Verify trigger phrases in SKILL.md
	triggers := []string{"/timesheet", "log timesheet", "daily timesheet"}
	for _, tr := range triggers {
		if !strings.Contains(skillContent, tr) {
			t.Errorf("SKILL.md missing required trigger phrase %q", tr)
		}
	}

	// 4. Verify workflow.md exists
	wfBytes, err := os.ReadFile(workflowMDPath)
	if err != nil {
		t.Fatalf("failed to read workflow.md at %s: %v", workflowMDPath, err)
	}
	wfContent := string(wfBytes)

	// 5. Verify explicit [Script] and [AI] step annotations
	requiredSteps := []struct {
		stepNum   string
		marker    string
		nameRegex string
	}{
		{"Step 1", "[Script]", `(?i)Step 1:.*\[Script\]`},
		{"Step 2", "[Script]", `(?i)Step 2:.*\[Script\]`},
		{"Step 3", "[Script]", `(?i)Step 3:.*\[Script\]`},
		{"Step 4", "[AI]", `(?i)Step 4:.*\[AI\]`},
		{"Step 5", "[Script]", `(?i)Step 5:.*\[Script\]`},
		{"Step 6", "[Script]", `(?i)Step 6:.*\[Script\]`},
		{"Step 7", "[Script]", `(?i)Step 7:.*\[Script\]`},
	}

	for _, step := range requiredSteps {
		re := regexp.MustCompile(step.nameRegex)
		if !re.MatchString(wfContent) {
			t.Errorf("workflow.md missing expected step heading matching %q", step.nameRegex)
		}
	}

	// 6. Verify token constraints and prohibitions in Step 4
	if !strings.Contains(wfContent, "300") || !strings.Contains(wfContent, "150") {
		t.Errorf("workflow.md missing explicit token limits (< 300 input, < 150 output)")
	}
	if !strings.Contains(strings.ToLower(wfContent), "conversational") {
		t.Errorf("workflow.md missing prohibition on conversational filler")
	}

	// 7. Verify all referenced Go binary paths exist in the codebase
	expectedBinDirs := []string{
		"cmd/fetch-calendar",
		"cmd/fetch-git",
		"cmd/review-timesheet",
		"cmd/sync-timesheet",
		"cmd/benchmark",
	}

	for _, binDir := range expectedBinDirs {
		fullPath := filepath.Join(repoRoot, binDir, "main.go")
		if _, statErr := os.Stat(fullPath); statErr != nil {
			t.Errorf("referenced binary entrypoint does not exist: %s (%v)", fullPath, statErr)
		}
	}
}
