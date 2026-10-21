package tests

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// canonicalSkillDir is the skill's one physical home in this repository. Harness
// directories (.claude/skills/timesheet, and the home-level ~/.agents entry) reach
// it through a symlink rather than holding a copy. Every test in this package reads
// the skill through this constant (package cmd/timesheet cannot import it and
// repeats the same path literal in TestClassifySkillParity), and
// TestAgentsSkill_CanonicalLocationAndHarnessSymlink pins the layout itself.
const canonicalSkillDir = ".agents/skills/timesheet"

// TestClaudeSkill_PackagingAndContracts verifies the skill packaging, its
// harness-agnostic command sequence, and that the skill delegates to the shared
// CLI rather than reimplementing accounting/submission. It also asserts the
// retired binaries/packages are gone.
func TestClaudeSkill_PackagingAndContracts(t *testing.T) {
	repoRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("resolving repo root: %v", err)
	}

	skillMDPath := filepath.Join(repoRoot, canonicalSkillDir, "SKILL.md")
	workflowMDPath := filepath.Join(repoRoot, canonicalSkillDir, "workflow.md")

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

	// 12. The unresolved-write STOP and the recovery-authority rules are stated.
	for _, want := range []string{
		"STOPS the operational",
		"content-unverified",
		"SEPARATE owner authority",
		"self-adoption",
		"source hot-patching",
		`conversational "go"`,
	} {
		if !strings.Contains(combined, want) {
			t.Errorf("skill docs missing unresolved-write STOP/recovery rule %q", want)
		}
	}
}

// TestClaudeSkill_ExecutionSurfaceRequirement pins the execution-surface
// requirement (issue #74): every step requires running the `timesheet` binary, so
// a harness with no shell cannot start the workflow and must say so and STOP
// instead of improvising or reading the implementation. Deleting the requirement
// block — or rewording it into an "improvise" allowance — fails here.
//
// The positive phrases are reflow-SENSITIVE: each must sit on one source line, so
// rewrapping the block fails the test and a rewrap therefore has to re-check the
// pins. A presence-only pin is deliberate: a sentence-window pattern for a
// contradicting fallback cannot tell "no shell means STOP" from "no shell, read
// the source", and this package already carries the requirement plus the
// Boundary's stop rule, so the test does not police prose it cannot read.
func TestClaudeSkill_ExecutionSurfaceRequirement(t *testing.T) {
	repoRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("resolving repo root: %v", err)
	}
	skillMDPath := filepath.Join(repoRoot, canonicalSkillDir, "SKILL.md")
	skillBytes, err := os.ReadFile(skillMDPath)
	if err != nil {
		t.Fatalf("failed to read SKILL.md at %s: %v", skillMDPath, err)
	}
	skillContent := string(skillBytes)

	// The capability statement, the unavailable-and-STOP instruction, the
	// no-improvisation rule, and the "no MCP server surface" fact.
	for _, want := range []string{
		"## Requirement: the harness must execute commands",
		"shell (command execution)",
		"the workflow is unavailable",
		"**STOP** — do not read the implementation",
		"do not improvise",
		"MCP **server** surface exists today",
		"ships only an MCP client",
	} {
		if !strings.Contains(skillContent, want) {
			t.Errorf("SKILL.md missing the execution-surface requirement %q "+
				"(a harness without a shell cannot run the workflow; it must be stated and STOP, not improvised)", want)
		}
	}
}

// TestClaudeSkill_StoragePathNamesWorkflowDir pins the storage-path wording so a
// legacy record file can never again be mistaken for a day's state (issue #75).
//
// The live store reads <root>/workflow/<date>.json only
// (internal/workflow.JSONFileStore.recordPath). A legacy <root>/<date>.json is
// the ADR-0005-superseded internal/storage shape and no cmd/timesheet command
// reads it. Deleting either phrase — or restoring the old bare "workflow records
// (default ~/.timesheet/records)" line — makes this test fail.
func TestClaudeSkill_StoragePathNamesWorkflowDir(t *testing.T) {
	repoRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("resolving repo root: %v", err)
	}
	skillMDPath := filepath.Join(repoRoot, canonicalSkillDir, "SKILL.md")
	skillBytes, err := os.ReadFile(skillMDPath)
	if err != nil {
		t.Fatalf("failed to read SKILL.md at %s: %v", skillMDPath, err)
	}
	skillContent := string(skillBytes)

	// The precise live path, the explicit superseded-legacy statement, and the
	// owner-data instruction (leave the legacy files alone, never a migration
	// backlog) that stops an agent from "fixing" them.
	for _, want := range []string{
		"`~/.timesheet/records/workflow/<date>.json`",
		"`~/.timesheet/records/<date>.json`",
		"SUPERSEDED",
		"is not the day's state",
		"never as a migration backlog",
	} {
		if !strings.Contains(skillContent, want) {
			t.Errorf("SKILL.md must disambiguate the records path: missing %q "+
				"(live state is <root>/workflow/<date>.json; a root-level <date>.json is superseded)", want)
		}
	}

	// The old wording that let an agent read the legacy file must not come back.
	// Matched as a whitespace-tolerant pattern so a reflowed or re-punctuated
	// resurrection — "workflow records (default `...`)" in any wrap or ending —
	// still fails, while the positive phrases above keep the paragraphs in place.
	staleLine := regexp.MustCompile("workflow records\\s*\\(default\\s*`~/\\.timesheet/records`")
	if loc := staleLine.FindString(skillContent); loc != "" {
		t.Errorf("SKILL.md restored the ambiguous storage-directory wording %q: "+
			"call the setting the records ROOT and name the live <root>/workflow/<date>.json path", loc)
	}
}

// TestAgentsSkill_CanonicalLocationAndHarnessSymlink pins the skill's packaging
// layout: `.agents/skills/timesheet` is the ONE physical copy, and the Claude Code
// harness directory `.claude/skills/timesheet` is a SYMLINK to it, not a second
// copy. The skill is harness-agnostic, so its canonical home must live where the
// harness-neutral convention looks; a dotdir-only home is the inversion this
// guards against (issue #24's packaging residual, closed by owner decision).
//
// "One physical copy" is asserted by CONTENT, never by filename: a second skill at
// `.claude/skills/<other>/SKILL.md` is a different skill and must pass, while a
// file named SKILL.md whose bytes ARE this skill's must not exist anywhere except
// the canonical path. Issue #24 had two halves — the missing harness-neutral
// location (`skills/timesheet`) and the `.claude` mirror — so the retired top-level
// `skills/` directory is held to the same content rule.
//
// RED mechanisms, verified in a scratch copy of the tree: replace the symlink with
// a real copy (`rm .claude/skills/timesheet && cp -R .agents/skills/timesheet
// .claude/skills/timesheet`) and the test fails on the os.Lstat symlink check;
// copy the canonical SKILL.md to `.claude/skills/other/SKILL.md` (or to
// `skills/timesheet/SKILL.md`) and it fails on the duplicate-content sweep;
// delete a canonical file and the regular-file check fails; point the symlink
// elsewhere and the resolved-path comparison fails; replace the canonical
// directory with a symlink and the canonical-directory check fails. A second skill
// with DIFFERENT content passes.
func TestAgentsSkill_CanonicalLocationAndHarnessSymlink(t *testing.T) {
	repoRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("resolving repo root: %v", err)
	}

	canonical := filepath.Join(repoRoot, canonicalSkillDir)
	harness := filepath.Join(repoRoot, ".claude", "skills", "timesheet")

	// (a) The canonical directory is a REAL directory: the one physical copy must
	// not itself be a link (a link there would mean the content lives elsewhere).
	dirInfo, err := os.Lstat(canonical)
	if err != nil {
		t.Fatalf("canonical skill directory missing: %v", err)
	}
	if !dirInfo.IsDir() {
		t.Fatalf("%s must be a real directory holding the one physical copy, got mode %v "+
			"(a symlink here means the skill's content lives somewhere else)", canonical, dirInfo.Mode())
	}

	// (b) Both canonical files are REGULAR files: real content, not links.
	skillBytes, err := os.ReadFile(filepath.Join(canonical, "SKILL.md"))
	if err != nil {
		t.Fatalf("reading the canonical SKILL.md: %v", err)
	}
	for _, name := range []string{"SKILL.md", "workflow.md"} {
		p := filepath.Join(canonical, name)
		info, statErr := os.Lstat(p)
		if statErr != nil {
			t.Fatalf("canonical skill file missing: %v", statErr)
		}
		if !info.Mode().IsRegular() {
			t.Errorf("%s must be a regular file in the canonical directory, got mode %v", p, info.Mode())
		}
	}

	// (c) The Claude harness path is a SYMLINK. Lstat, not Stat: Stat follows the
	// link and would report the directory, hiding an in-tree copy.
	info, err := os.Lstat(harness)
	if err != nil {
		t.Fatalf("harness path %s must exist as a symlink: %v", harness, err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("harness path %s must be a symlink, not %s "+
			"(a real copy under .claude makes .claude the second physical copy of the skill)",
			harness, info.Mode())
	}

	// (d) It resolves to the canonical directory — and it is a RELATIVE link, so a
	// clone or worktree at any path keeps working.
	linkTarget, err := os.Readlink(harness)
	if err != nil {
		t.Fatalf("reading the harness symlink: %v", err)
	}
	if filepath.IsAbs(linkTarget) {
		t.Errorf("harness symlink must be relative so it survives clones and worktrees, got absolute %q", linkTarget)
	}
	resolvedHarness, err := filepath.EvalSymlinks(harness)
	if err != nil {
		t.Fatalf("resolving the harness symlink: %v", err)
	}
	resolvedCanonical, err := filepath.EvalSymlinks(canonical)
	if err != nil {
		t.Fatalf("resolving the canonical skill directory: %v", err)
	}
	if resolvedHarness != resolvedCanonical {
		t.Errorf("harness symlink resolves to %q, want the canonical directory %q", resolvedHarness, resolvedCanonical)
	}

	// (e) No second copy of THIS skill under .claude. Content decides, not the
	// filename: another harness skill named SKILL.md is fine, a copy of this one is
	// not, and a symlink is a route rather than a copy.
	if copies, walkErr := duplicateSkillCopies(filepath.Join(repoRoot, ".claude"), skillBytes); walkErr != nil {
		t.Fatalf("walking .claude for duplicate skill copies: %v", walkErr)
	} else {
		for _, p := range copies {
			t.Errorf("second physical copy of the skill under .claude: %s "+
				"(the one copy belongs to %s; .claude must only link to it)", p, canonicalSkillDir)
		}
	}

	// (f) The retired top-level `skills/timesheet` of issue #24 must not come back as
	// a second home. `skills/` may simply be absent (the owner chose `.agents`); if
	// it exists, it must hold no copy of this skill's content.
	retired := filepath.Join(repoRoot, "skills")
	if _, statErr := os.Lstat(retired); os.IsNotExist(statErr) {
		// Absent: the harness-neutral home is .agents, so there is nothing to guard.
	} else if statErr != nil {
		t.Fatalf("checking the retired skills/ directory: %v", statErr)
	} else if copies, walkErr := duplicateSkillCopies(retired, skillBytes); walkErr != nil {
		t.Fatalf("walking skills/ for duplicate skill copies: %v", walkErr)
	} else {
		for _, p := range copies {
			t.Errorf("%s holds a second copy of the skill: issue #24's harness-neutral home is "+
				"%s now, and skills/ must not reappear as another one", p, canonicalSkillDir)
		}
	}
}

// duplicateSkillCopies walks root and returns every path holding a REGULAR file
// named SKILL.md whose bytes are identical to canonicalBytes. A SKILL.md with
// different content is a different skill, not a copy of this one; a symlink is a
// route, not a copy. filepath.WalkDir does not follow symlinks, so nothing reached
// through `.claude/skills/timesheet` is reported.
func duplicateSkillCopies(root string, canonicalBytes []byte) ([]string, error) {
	var copies []string
	walkErr := filepath.WalkDir(root, func(p string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.Name() != "SKILL.md" || !d.Type().IsRegular() {
			return nil
		}
		got, readErr := os.ReadFile(p)
		if readErr != nil {
			return readErr
		}
		if bytes.Equal(got, canonicalBytes) {
			copies = append(copies, p)
		}
		return nil
	})
	return copies, walkErr
}
