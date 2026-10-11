package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/evidence"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/evidence/plugins"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/run"
)

func journalConfig(t *testing.T, vault, dailyDir string) cliConfig {
	t.Helper()
	return cliConfig{
		Config: run.Config{Evidence: evidence.Config{
			Timezone: "UTC",
			Sources: []evidence.SourceConfig{{
				ID: "journal-work", Type: plugins.TypeJournal, Enabled: true,
				Config: map[string]string{
					plugins.JournalConfigVault:    vault,
					plugins.JournalConfigDailyDir: dailyDir,
				},
			}},
		}},
		StorageDir: filepath.Join(t.TempDir(), "records"),
	}
}

// TestCLI_JournalReviewPrintsUntimedNarrative proves the CLI registry recognizes
// the journal source and `review` prints an UNTIMED narrative with unknown
// duration (never a fabricated time).
func TestCLI_JournalReviewPrintsUntimedNarrative(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "test-token")
	dir := t.TempDir()
	vault := filepath.Join(dir, "vault")
	daily := "02 - Daily & Logs"
	if err := os.MkdirAll(filepath.Join(vault, daily), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vault, daily, testDate+".md"),
		[]byte("## Work journal\n- Wired the journal source into the registry.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := writeConfig(t, dir, journalConfig(t, vault, daily))

	code, out, errOut := runArgs(t, "review", "--config", path, "--date", testDate, "--no-editorial")
	if code != 0 {
		t.Fatalf("review exit=%d stderr=%s", code, errOut)
	}
	if !strings.Contains(out, "narrative") || !strings.Contains(out, "duration=unknown") {
		t.Fatalf("expected an untimed narrative line, got:\n%s", out)
	}
}

// TestCLI_JournalPreflight proves the journal source passes runtime configuration
// preflight (registry + contract check) without a fixture.
func TestCLI_JournalPreflight(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "test-token")
	dir := t.TempDir()
	path := writeConfig(t, dir, journalConfig(t, filepath.Join(dir, "vault"), "02 - Daily & Logs"))

	code, out, errOut := runArgs(t, "preflight", "--config", path)
	if code != 0 {
		t.Fatalf("preflight exit=%d stderr=%s", code, errOut)
	}
	if !strings.Contains(out, "ok   configuration") {
		t.Fatalf("expected a passing configuration check, got:\n%s", out)
	}
}

// TestCLI_DetachedHistoryIsHumanVisible proves a detached journal item is printed
// with human-readable provenance (reason/title/note/heading/lines/date) in the
// existing review output, not as an opaque id or a bare count.
func TestCLI_DetachedHistoryIsHumanVisible(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "test-token")
	dir := t.TempDir()
	vault := filepath.Join(dir, "vault")
	daily := "02 - Daily & Logs"
	if err := os.MkdirAll(filepath.Join(vault, daily), 0o750); err != nil {
		t.Fatal(err)
	}
	notePath := filepath.Join(vault, daily, testDate+".md")
	if err := os.WriteFile(notePath, []byte("## Work journal\n- Alpha task\n- Beta task\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := writeConfig(t, dir, journalConfig(t, vault, daily))

	if code, _, errOut := runArgs(t, "review", "--config", path, "--date", testDate, "--no-editorial"); code != 0 {
		t.Fatalf("review 1 exit=%d stderr=%s", code, errOut)
	}
	// Remove Alpha: the next collection detaches it.
	if err := os.WriteFile(notePath, []byte("## Work journal\n- Beta task\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := runArgs(t, "review", "--config", path, "--date", testDate, "--no-editorial")
	if code != 0 {
		t.Fatalf("review 2 exit=%d stderr=%s", code, errOut)
	}
	if !strings.Contains(out, "reason=detached") {
		t.Fatalf("detached history must be visible with a reason, got:\n%s", out)
	}
	for _, want := range []string{"excluded", "heading=", "note=", "title=Alpha task"} {
		if !strings.Contains(out, want) {
			t.Errorf("detached advisory line missing %q in:\n%s", want, out)
		}
	}
}

// TestCLI_HelpUnchangedNoNewSubcommand proves the journal source is config-driven
// and adds no new CLI surface (the skill and CLI keep sharing one core).
func TestCLI_HelpUnchangedNoNewSubcommand(t *testing.T) {
	code, out, _ := runArgs(t, "help")
	if code != 0 {
		t.Fatalf("help exit=%d", code)
	}
	if strings.Contains(out, "journal") {
		t.Errorf("the journal source must not add a CLI subcommand:\n%s", out)
	}
}
