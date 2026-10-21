package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/editorial"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/evidence"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/evidence/plugins"
)

// --- helpers ----------------------------------------------------------------

func runConfigArgs(t *testing.T, in io.Reader, stdinTTY, stdoutTTY bool, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := runCLIWithIO(context.Background(), args, in, &stdout, &stderr, stdinTTY, stdoutTTY)
	return code, stdout.String(), stderr.String()
}

func writeConfigDoc(t *testing.T, path string, cfg cliConfig) {
	t.Helper()
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("marshal config doc: %v", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write config doc: %v", err)
	}
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return data
}

// gitLocalConfig is the canonical config the interactive/plain/answers tests all
// reproduce: one local-only git source plus the default storage dir.
func gitLocalConfig() cliConfig {
	cfg := cliConfig{}
	cfg.Evidence.ID = "default"
	cfg.Evidence.Timezone = "UTC"
	cfg.Evidence.Sources = []evidence.SourceConfig{{
		ID:      "git-work",
		Type:    plugins.TypeGit,
		Enabled: true,
		Config: map[string]string{
			plugins.GitConfigMode:       plugins.GitModeLocal,
			plugins.GitConfigLocalRepos: ".",
		},
	}}
	cfg.StorageDir = "~/.timesheet/records"
	return cfg
}

// gitLocalAnswers drives the REAL flow (via the fake prompter) for gitLocalConfig.
func gitLocalAnswers() *fakePrompter {
	return &fakePrompter{
		inputs: []string{"default", "UTC", "", "", "", "", "", "", ""},
		multi:  [][]int{{1}}, // git
		choose: []int{0},     // git mode: local
		// gradion? no; editorial? no; write? yes
		confirms: []bool{false, false, true},
	}
}

// gitLocalPlainInput is the line-based stdin for the SAME config. Each line maps
// to one prompt, in the exact order gatherConfig asks them.
func gitLocalPlainInput() string {
	lines := []string{
		"default", // evidence id
		"UTC",     // timezone
		"2",       // sources: option 2 (git)
		"",        // git source id (blank -> git-work)
		"1",       // git mode: option 1 (local)
		"",        // local_repos (blank -> ".")
		"",        // author
		"",        // project_map
		"",        // storage_dir
		"",        // vault export root
		"n",       // configure Gradion? no
		"n",       // configure editorial? no
		"",        // classification project (blank -> finish)
		"y",       // write config
	}
	return strings.Join(lines, "\n") + "\n"
}

// --- core: writes a valid config --------------------------------------------

func TestConfig_InitAnswers_WritesValidConfig(t *testing.T) {
	dir := t.TempDir()
	answers := filepath.Join(dir, "answers.json")
	out := filepath.Join(dir, "cfg", "timesheet.json")
	writeConfigDoc(t, answers, gitLocalConfig())

	code, stdout, stderr := runConfigArgs(t, strings.NewReader(""), false, false,
		"config", "init", "--answers", answers, "--out", out)
	if code != exitOK {
		t.Fatalf("config init: %d\n%s\n%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "config preview (nothing written yet)") {
		t.Fatalf("preview must be printed before the write:\n%s", stdout)
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatalf("config must be written: %v", err)
	}

	// Round-trips through loadConfig.
	got, err := loadConfig(out)
	if err != nil {
		t.Fatalf("loadConfig round-trip: %v", err)
	}
	if got.Evidence.ID != "default" || got.Evidence.Timezone != "UTC" {
		t.Fatalf("round-trip lost evidence fields: %+v", got.Evidence)
	}
	if len(got.Evidence.Sources) != 1 || got.Evidence.Sources[0].Type != plugins.TypeGit {
		t.Fatalf("round-trip lost the git source: %+v", got.Evidence.Sources)
	}

	// Passes offline validation (timezone + sources + editorial).
	if problems := validateConfigDraft(context.Background(), got); len(problems) != 0 {
		t.Fatalf("written config must validate, got: %v", problems)
	}
}

func TestConfig_InitFakePrompter_DrivesRealFlow(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "timesheet.json")

	var stdout, stderr bytes.Buffer
	code := authorConfig(context.Background(), configAuthoring{
		action:   "init",
		base:     cliConfig{},
		target:   out,
		mode:     modeTUI,
		prompter: gitLocalAnswers(),
	}, strings.NewReader(""), &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("fake-prompter flow: %d\n%s\n%s", code, stdout.String(), stderr.String())
	}

	want, err := marshalConfig(gitLocalConfig())
	if err != nil {
		t.Fatalf("marshal want: %v", err)
	}
	if got := mustReadFile(t, out); !bytes.Equal(got, want) {
		t.Fatalf("fake-prompter config mismatch:\n got: %s\nwant: %s", got, want)
	}
}

// --- the three paths agree on the produced bytes ----------------------------

func TestConfig_TUI_Plain_Answers_Agree(t *testing.T) {
	dir := t.TempDir()
	want, err := marshalConfig(gitLocalConfig())
	if err != nil {
		t.Fatalf("marshal want: %v", err)
	}

	// 1. Fake prompter (the TUI's flow, driven headlessly).
	outTUI := filepath.Join(dir, "tui.json")
	var so, se bytes.Buffer
	if code := authorConfig(context.Background(), configAuthoring{
		action: "init", base: cliConfig{}, target: outTUI, mode: modeTUI, prompter: gitLocalAnswers(),
	}, strings.NewReader(""), &so, &se); code != exitOK {
		t.Fatalf("fake-prompter path: %d\n%s\n%s", code, so.String(), se.String())
	}

	// 2. The plain line-based path over a pipe.
	outPlain := filepath.Join(dir, "plain.json")
	if code, so, se := runConfigArgs(t, strings.NewReader(gitLocalPlainInput()), false, false,
		"config", "init", "--plain", "--out", outPlain); code != exitOK {
		t.Fatalf("plain path: %d\n%s\n%s", code, so, se)
	}

	// 3. The non-interactive --answers path.
	answers := filepath.Join(dir, "answers.json")
	writeConfigDoc(t, answers, gitLocalConfig())
	outAnswers := filepath.Join(dir, "answers-out.json")
	if code, so, se := runConfigArgs(t, strings.NewReader(""), false, false,
		"config", "init", "--answers", answers, "--out", outAnswers); code != exitOK {
		t.Fatalf("answers path: %d\n%s\n%s", code, so, se)
	}

	for name, path := range map[string]string{"tui": outTUI, "plain": outPlain, "answers": outAnswers} {
		if got := mustReadFile(t, path); !bytes.Equal(got, want) {
			t.Fatalf("%s path produced different bytes:\n got: %s\nwant: %s", name, got, want)
		}
	}
}

// --- mode gating ------------------------------------------------------------

func TestConfig_InteractiveWithoutTTYRefuses(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "timesheet.json")
	code, _, stderr := runConfigArgs(t, strings.NewReader(""), false, false,
		"config", "init", "--interactive", "--out", out)
	if code != exitUsage {
		t.Fatalf("--interactive without a TTY must refuse, got %d (%s)", code, stderr)
	}
	if _, err := os.Stat(out); err == nil {
		t.Fatalf("a refused --interactive run must write nothing")
	}
}

func TestConfig_InteractiveUnderCIRefuses(t *testing.T) {
	t.Setenv("CI", "true")
	dir := t.TempDir()
	out := filepath.Join(dir, "timesheet.json")
	code, _, _ := runConfigArgs(t, strings.NewReader(""), true, true,
		"config", "init", "--interactive", "--out", out)
	if code != exitUsage {
		t.Fatalf("--interactive under CI must refuse, got %d", code)
	}
}

func TestConfig_DefaultWithoutTTYRefuses(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "timesheet.json")
	// No mode flag, no terminal: the guided default REFUSES (never a silent
	// fallback), pointing at --plain / --answers.
	code, _, stderr := runConfigArgs(t, strings.NewReader(""), false, false,
		"config", "init", "--out", out)
	if code != exitUsage {
		t.Fatalf("a non-TTY default must refuse, got %d (%s)", code, stderr)
	}
	if !strings.Contains(stderr, "--plain") || !strings.Contains(stderr, "--answers") {
		t.Fatalf("the refusal must point at --plain/--answers:\n%s", stderr)
	}
	if _, err := os.Stat(out); err == nil {
		t.Fatalf("a refused run must write nothing")
	}
}

func TestConfig_PlainEOFAbortsWritesNothing(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "timesheet.json")
	// An EMPTY stdin: the first prompt hits EOF, which is an ABORT, never a default.
	code, _, stderr := runConfigArgs(t, strings.NewReader(""), false, false,
		"config", "init", "--plain", "--out", out)
	if code != exitError {
		t.Fatalf("a plain EOF must abort with an error, got %d (%s)", code, stderr)
	}
	if _, err := os.Stat(out); err == nil {
		t.Fatalf("a plain EOF abort must write nothing")
	}
}

func TestConfig_NonInteractiveRequiresAnswers(t *testing.T) {
	dir := t.TempDir()
	code, _, stderr := runConfigArgs(t, strings.NewReader(""), false, false,
		"config", "init", "--non-interactive", "--out", filepath.Join(dir, "x.json"))
	if code != exitUsage {
		t.Fatalf("--non-interactive without --answers must be a usage error, got %d (%s)", code, stderr)
	}
}

// --- validation rejects invalid input with zero writes ----------------------

func TestConfig_RejectsInvalidTimezone(t *testing.T) {
	dir := t.TempDir()
	cfg := gitLocalConfig()
	cfg.Evidence.Timezone = "Not/AZone"
	answers := filepath.Join(dir, "answers.json")
	writeConfigDoc(t, answers, cfg)
	out := filepath.Join(dir, "timesheet.json")

	code, _, stderr := runConfigArgs(t, strings.NewReader(""), false, false,
		"config", "init", "--answers", answers, "--out", out)
	if code != exitError {
		t.Fatalf("an invalid timezone must be rejected, got %d", code)
	}
	if !strings.Contains(stderr, "Not/AZone") {
		t.Fatalf("the error must name the invalid timezone:\n%s", stderr)
	}
	if _, err := os.Stat(out); err == nil {
		t.Fatalf("a rejected config must write nothing")
	}
}

func TestConfig_RejectsRawSecretInCredentialRef(t *testing.T) {
	const raw = "https://secret.example/private-feed.ics"
	dir := t.TempDir()
	cfg := gitLocalConfig()
	cfg.Evidence.Sources = []evidence.SourceConfig{{
		ID: "cal-work", Type: evidence.SourceCalendar, Enabled: true,
		Config: map[string]string{plugins.CalendarConfigURLRef: raw},
	}}
	answers := filepath.Join(dir, "answers.json")
	writeConfigDoc(t, answers, cfg)
	out := filepath.Join(dir, "timesheet.json")

	code, stdout, stderr := runConfigArgs(t, strings.NewReader(""), false, false,
		"config", "init", "--answers", answers, "--out", out)
	if code != exitError {
		t.Fatalf("a raw value in url_ref must be rejected, got %d", code)
	}
	if _, err := os.Stat(out); err == nil {
		t.Fatalf("a rejected config must write nothing")
	}
	// The raw value must NEVER be echoed.
	if strings.Contains(stdout+stderr, raw) {
		t.Fatalf("the raw value must not be echoed:\n%s\n%s", stdout, stderr)
	}
	if !strings.Contains(stderr, "keychain:<service>") {
		t.Fatalf("the error should point at a keychain reference:\n%s", stderr)
	}
}

func TestConfig_RejectsRawGradionCredentialRef(t *testing.T) {
	const raw = "raw-bearer-token-value"
	dir := t.TempDir()
	cfg := gitLocalConfig()
	cfg.Gradion = gradionConfig{Endpoint: "https://mcp.example/mcp", CredentialRef: raw}
	answers := filepath.Join(dir, "answers.json")
	writeConfigDoc(t, answers, cfg)
	out := filepath.Join(dir, "timesheet.json")

	code, stdout, stderr := runConfigArgs(t, strings.NewReader(""), false, false,
		"config", "init", "--answers", answers, "--out", out)
	if code != exitError {
		t.Fatalf("a raw gradion credential_ref must be rejected, got %d", code)
	}
	if strings.Contains(stdout+stderr, raw) {
		t.Fatalf("the raw value must not be echoed:\n%s\n%s", stdout, stderr)
	}
	if _, err := os.Stat(out); err == nil {
		t.Fatalf("a rejected config must write nothing")
	}
}

func TestConfig_RejectsRawEditorialCredentialRef(t *testing.T) {
	const raw = "raw-editorial-secret"
	dir := t.TempDir()
	cfg := gitLocalConfig()
	cfg.Editorial = editorial.Config{Enabled: true, Adapter: "command", Command: []string{"/bin/true"}, CredentialRef: raw}
	answers := filepath.Join(dir, "answers.json")
	writeConfigDoc(t, answers, cfg)
	out := filepath.Join(dir, "timesheet.json")

	code, stdout, stderr := runConfigArgs(t, strings.NewReader(""), false, false,
		"config", "init", "--answers", answers, "--out", out)
	if code != exitError {
		t.Fatalf("a raw editorial credential_ref must be rejected, got %d", code)
	}
	if strings.Contains(stdout+stderr, raw) {
		t.Fatalf("the raw value must not be echoed:\n%s\n%s", stdout, stderr)
	}
}

func TestConfig_RejectsMalformedSourceConfig(t *testing.T) {
	dir := t.TempDir()
	cfg := gitLocalConfig()
	// mode includes remote but repos is empty -> GIT_REPOS_REQUIRED.
	cfg.Evidence.Sources = []evidence.SourceConfig{{
		ID: "git-work", Type: plugins.TypeGit, Enabled: true,
		Config: map[string]string{plugins.GitConfigMode: plugins.GitModeRemote},
	}}
	answers := filepath.Join(dir, "answers.json")
	writeConfigDoc(t, answers, cfg)
	out := filepath.Join(dir, "timesheet.json")

	code, _, stderr := runConfigArgs(t, strings.NewReader(""), false, false,
		"config", "init", "--answers", answers, "--out", out)
	if code != exitError {
		t.Fatalf("a malformed git source must be rejected, got %d", code)
	}
	if !strings.Contains(stderr, "GIT_REPOS_REQUIRED") {
		t.Fatalf("the error must carry the stable code:\n%s", stderr)
	}
	if _, err := os.Stat(out); err == nil {
		t.Fatalf("a rejected config must write nothing")
	}
}

// --- preview + confirmation -------------------------------------------------

func TestConfig_DeclinedConfirmationWritesNothing(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "timesheet.json")
	fp := gitLocalAnswers()
	fp.confirms = []bool{false, false, false} // write? no

	var stdout, stderr bytes.Buffer
	code := authorConfig(context.Background(), configAuthoring{
		action: "init", base: cliConfig{}, target: out, mode: modeTUI, prompter: fp,
	}, strings.NewReader(""), &stdout, &stderr)
	if code != exitError {
		t.Fatalf("a declined confirmation must not succeed, got %d", code)
	}
	if !strings.Contains(stdout.String(), "config preview (nothing written yet)") {
		t.Fatalf("the preview must still be shown:\n%s", stdout.String())
	}
	if _, err := os.Stat(out); err == nil {
		t.Fatalf("a declined confirmation must write nothing")
	}
}

// --- overwrite consent ------------------------------------------------------

func TestConfig_OverwriteRefusedWithoutConsent(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "timesheet.json")
	original := []byte("{\n  \"evidence\": {\"id\": \"original\"}\n}\n")
	if err := os.WriteFile(out, original, 0o600); err != nil {
		t.Fatal(err)
	}
	answers := filepath.Join(dir, "answers.json")
	writeConfigDoc(t, answers, gitLocalConfig())

	code, _, stderr := runConfigArgs(t, strings.NewReader(""), false, false,
		"config", "init", "--answers", answers, "--out", out)
	if code != exitBlocked {
		t.Fatalf("an existing config must be refused without consent, got %d (%s)", code, stderr)
	}
	if got := mustReadFile(t, out); !bytes.Equal(got, original) {
		t.Fatalf("a refused overwrite must leave the file byte-identical, got: %s", got)
	}
}

func TestConfig_ForcedOverwriteReplacesCleanly(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "timesheet.json")
	if err := os.WriteFile(out, []byte("{\"evidence\":{\"id\":\"original\"}}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	answers := filepath.Join(dir, "answers.json")
	writeConfigDoc(t, answers, gitLocalConfig())

	code, _, stderr := runConfigArgs(t, strings.NewReader(""), false, false,
		"config", "init", "--answers", answers, "--out", out, "--force")
	if code != exitOK {
		t.Fatalf("a forced overwrite must succeed, got %d (%s)", code, stderr)
	}
	want, err := marshalConfig(gitLocalConfig())
	if err != nil {
		t.Fatal(err)
	}
	if got := mustReadFile(t, out); !bytes.Equal(got, want) {
		t.Fatalf("a forced overwrite must replace the file cleanly, got: %s", got)
	}
}

func TestConfig_InteractiveOverwriteNeedsConsent(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "timesheet.json")
	original := []byte("{\"evidence\":{\"id\":\"original\"}}\n")
	if err := os.WriteFile(out, original, 0o600); err != nil {
		t.Fatal(err)
	}
	// Answers mirror gitLocalAnswers, but the overwrite consent is DECLINED.
	fp := &fakePrompter{
		inputs:   []string{"default", "UTC", "", "", "", "", "", "", ""},
		multi:    [][]int{{1}},
		choose:   []int{0},
		confirms: []bool{false, false /*gradion,editorial*/, false /*overwrite*/, true /*write*/},
	}
	var stdout, stderr bytes.Buffer
	code := authorConfig(context.Background(), configAuthoring{
		action: "edit", base: gitLocalConfig(), target: out, mode: modeTUI, prompter: fp,
	}, strings.NewReader(""), &stdout, &stderr)
	if code != exitBlocked {
		t.Fatalf("declining overwrite consent must refuse, got %d", code)
	}
	if got := mustReadFile(t, out); !bytes.Equal(got, original) {
		t.Fatalf("declining overwrite must leave the file byte-identical, got: %s", got)
	}
}

func TestConfig_FailedValidationLeavesExistingFileIdentical(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "timesheet.json")
	original := []byte("{\"evidence\":{\"id\":\"keep-me\"}}\n")
	if err := os.WriteFile(out, original, 0o600); err != nil {
		t.Fatal(err)
	}
	bad := gitLocalConfig()
	bad.Evidence.Timezone = "Not/AZone"
	answers := filepath.Join(dir, "answers.json")
	writeConfigDoc(t, answers, bad)

	code, _, _ := runConfigArgs(t, strings.NewReader(""), false, false,
		"config", "init", "--answers", answers, "--out", out, "--force")
	if code != exitError {
		t.Fatalf("a failed validation must not write, got %d", code)
	}
	if got := mustReadFile(t, out); !bytes.Equal(got, original) {
		t.Fatalf("a failed validation must leave the file byte-identical, got: %s", got)
	}
}

// --- idempotence ------------------------------------------------------------

func TestConfig_EditIdempotent(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "timesheet.json")

	// init via the flow
	var so, se bytes.Buffer
	if code := authorConfig(context.Background(), configAuthoring{
		action: "init", base: cliConfig{}, target: out, mode: modeTUI, prompter: gitLocalAnswers(),
	}, strings.NewReader(""), &so, &se); code != exitOK {
		t.Fatalf("init: %d\n%s\n%s", code, so.String(), se.String())
	}
	afterInit := mustReadFile(t, out)

	// edit the SAME file with unchanged answers: the file already exists, so the
	// flow asks for overwrite consent, then writes the identical bytes.
	base, err := loadConfig(out)
	if err != nil {
		t.Fatal(err)
	}
	fp := &fakePrompter{
		inputs:   []string{"", "", "", "", "", "", "", "", ""},
		multi:    [][]int{{1}},
		choose:   []int{0},
		confirms: []bool{false, false /*gradion,editorial*/, true /*overwrite*/, true /*write*/},
	}
	so.Reset()
	se.Reset()
	if code := authorConfig(context.Background(), configAuthoring{
		action: "edit", base: base, target: out, mode: modeTUI, prompter: fp,
	}, strings.NewReader(""), &so, &se); code != exitOK {
		t.Fatalf("edit: %d\n%s\n%s", code, so.String(), se.String())
	}
	if got := mustReadFile(t, out); !bytes.Equal(got, afterInit) {
		t.Fatalf("editing with unchanged answers must be byte-stable:\n got: %s\nwant: %s", got, afterInit)
	}
}

// --- path handling ----------------------------------------------------------

func TestConfig_CreatesMissingParentDirs(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "a", "b", "c", "timesheet.json")
	answers := filepath.Join(dir, "answers.json")
	writeConfigDoc(t, answers, gitLocalConfig())

	code, _, stderr := runConfigArgs(t, strings.NewReader(""), false, false,
		"config", "init", "--answers", answers, "--out", out)
	if code != exitOK {
		t.Fatalf("missing parent dirs must be created, got %d (%s)", code, stderr)
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatalf("config not written under a fresh tree: %v", err)
	}
}

func TestConfig_ExpandsTildeOutPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	answers := filepath.Join(home, "answers.json")
	writeConfigDoc(t, answers, gitLocalConfig())

	code, _, stderr := runConfigArgs(t, strings.NewReader(""), false, false,
		"config", "init", "--answers", answers, "--out", "~/cfg/timesheet.json")
	if code != exitOK {
		t.Fatalf("a tilde out path must expand, got %d (%s)", code, stderr)
	}
	if _, err := os.Stat(filepath.Join(home, "cfg", "timesheet.json")); err != nil {
		t.Fatalf("~ was not expanded to the home dir: %v", err)
	}
}

// --- the calendar branch (and the keychain-ref re-prompt) -------------------

func TestConfig_InteractiveCalendarRepromptsRawRef(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "timesheet.json")

	const raw = "raw-feed-value.example/feed.ics"
	fp := &fakePrompter{
		inputs: []string{
			"default", "UTC", // evidence id, timezone
			"",                           // calendar source id -> cal-work
			raw,                          // url_ref (INVALID: not a keychain ref)
			"keychain:calendar_feed_url", // url_ref retry (valid)
			"", "", "",                   // user_email, calendar timezone, cache_path
			"", // storage_dir
			"", // vault export root
			"", // classification project (finish)
		},
		multi:    [][]int{{0}},                      // calendar
		confirms: []bool{false, false, false, true}, // no_cache, gradion, editorial, write
	}
	var stdout, stderr bytes.Buffer
	code := authorConfig(context.Background(), configAuthoring{
		action: "init", base: cliConfig{}, target: out, mode: modeTUI, prompter: fp,
	}, strings.NewReader(""), &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("calendar flow: %d\n%s\n%s", code, stdout.String(), stderr.String())
	}
	// The invalid value is rejected by re-prompting, never silently written...
	if !strings.Contains(stdout.String(), "must be a keychain:<service> reference") {
		t.Fatalf("the raw url_ref should be re-prompted:\n%s", stdout.String())
	}
	// ...and the raw value is never echoed.
	if strings.Contains(stdout.String()+stderr.String(), raw) {
		t.Fatalf("the raw value must not be echoed:\n%s\n%s", stdout.String(), stderr.String())
	}
	got, err := loadConfig(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Evidence.Sources) != 1 {
		t.Fatalf("want 1 source, got %+v", got.Evidence.Sources)
	}
	if ref := got.Evidence.Sources[0].Config[plugins.CalendarConfigURLRef]; ref != "keychain:calendar_feed_url" {
		t.Fatalf("want the valid keychain ref written, got %q", ref)
	}
	if problems := validateConfigDraft(context.Background(), got); len(problems) != 0 {
		t.Fatalf("written calendar config must validate: %v", problems)
	}
}

// --- F1: --interactive is the explicit alias of the default guided mode -------

func TestConfig_InteractiveIsAliasOfDefault(t *testing.T) {
	// With a real terminal (non-CI), --interactive and no flag select the SAME TUI.
	for _, req := range []configRequest{
		{Interactive: true, StdinTTY: true, StdoutTTY: true},
		{StdinTTY: true, StdoutTTY: true},
	} {
		mode, err := resolveConfigMode(req)
		if err != nil || mode != modeTUI {
			t.Fatalf("a real terminal must select the TUI, got mode=%v err=%v", mode, err)
		}
	}
	// Without a terminal, --interactive and no flag REFUSE identically (the alias
	// never changes the gate).
	for _, req := range []configRequest{
		{Interactive: true},
		{},
		{Interactive: true, StdinTTY: true}, // stdout not a terminal
		{StdinTTY: true, StdoutTTY: true, CI: true}, // CI
		{Interactive: true, StdinTTY: true, StdoutTTY: true, CI: true},
	} {
		mode, err := resolveConfigMode(req)
		if err == nil || mode != modeScripted {
			t.Fatalf("without a usable terminal the guided mode must refuse, got mode=%v err=%v", mode, err)
		}
	}
}

// --- F2: guided edit preserves uncollected editorial tuning fields -----------

func TestConfig_EditPreservesEditorialTuning(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "timesheet.json")

	base := cliConfig{}
	base.Evidence.ID = "personal"
	base.Evidence.Timezone = "UTC"
	base.Evidence.Sources = []evidence.SourceConfig{}
	base.StorageDir = "~/.timesheet/records"
	base.Editorial = editorial.Config{
		Enabled:       true,
		SoftTarget:    1000,
		MaxAttempts:   3,
		Adapter:       "command",
		Command:       []string{"/bin/true"},
		Endpoint:      "https://editorial.example/mcp",
		Model:         "m-1",
		CredentialRef: "keychain:editorial",
	}
	seed, err := marshalConfig(base)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(out, seed, 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadConfig(out)
	if err != nil {
		t.Fatal(err)
	}

	// The flow asks nothing about the tuning fields; it must carry them through.
	fp := &fakePrompter{
		inputs: []string{"", "", "", "", "", "", ""}, // id, tz, storage, vault, cmd, cred-ref, cls
		multi:  [][]int{{}},                          // no sources
		choose: []int{1},                             // editorial adapter: command
		// gradion? no; editorial? yes; overwrite? yes; write? yes
		confirms: []bool{false, true, true, true},
	}
	var stdout, stderr bytes.Buffer
	code := authorConfig(context.Background(), configAuthoring{
		action: "edit", base: loaded, target: out, mode: modeTUI, prompter: fp,
	}, strings.NewReader(""), &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("edit: %d\n%s\n%s", code, stdout.String(), stderr.String())
	}
	got, err := loadConfig(out)
	if err != nil {
		t.Fatal(err)
	}
	if got.Editorial.SoftTarget != 1000 || got.Editorial.MaxAttempts != 3 {
		t.Fatalf("editorial tuning must be preserved, got soft_target=%d max_attempts=%d",
			got.Editorial.SoftTarget, got.Editorial.MaxAttempts)
	}
	if got.Editorial.Endpoint != "https://editorial.example/mcp" || got.Editorial.Model != "m-1" {
		t.Fatalf("editorial endpoint/model must be preserved, got endpoint=%q model=%q",
			got.Editorial.Endpoint, got.Editorial.Model)
	}
	if !got.Editorial.Enabled || got.Editorial.Adapter != "command" {
		t.Fatalf("the collected editorial fields must still be written, got %+v", got.Editorial)
	}
	if !strings.Contains(string(mustReadFile(t, out)), `"soft_target": 1000`) {
		t.Fatalf("the written file must carry soft_target:\n%s", mustReadFile(t, out))
	}
}

// --- F4: the guided daily_dir check mirrors the journal plugin's ------------

func TestConfig_CheckRelativeDirMirrorsJournalPlugin(t *testing.T) {
	invalid := []string{".", "..", "../x", "a/../../b", "/abs", "\x00", "a\x00b"}
	for _, d := range invalid {
		if err := checkRelativeDir(d); err == nil {
			t.Errorf("checkRelativeDir(%q) must be rejected (journal cleanRelativeDir rejects it)", d)
		}
	}
	valid := []string{"02 - Daily & Logs", "daily", "a/b", " a/b "}
	for _, d := range valid {
		if err := checkRelativeDir(d); err != nil {
			t.Errorf("checkRelativeDir(%q) must be accepted, got %v", d, err)
		}
	}
}

func TestConfig_GuidedJournalDailyDirRepromptsOnDot(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "timesheet.json")

	fp := &fakePrompter{
		inputs: []string{
			"default", "UTC", // evidence id, timezone
			"",                  // journal source id -> journal-work
			"/v",                // journal vault root
			".",                 // journal daily_dir (INVALID -> re-prompt)
			"02 - Daily & Logs", // journal daily_dir retry (valid)
			"", "",              // storage_dir, vault export root
			"", // classification project (finish)
		},
		multi:    [][]int{{2}},               // journal
		confirms: []bool{false, false, true}, // gradion, editorial, write
	}
	var stdout, stderr bytes.Buffer
	code := authorConfig(context.Background(), configAuthoring{
		action: "init", base: cliConfig{}, target: out, mode: modeTUI, prompter: fp,
	}, strings.NewReader(""), &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("journal flow: %d\n%s\n%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "must be a real subdirectory") {
		t.Fatalf(`a "." daily_dir must be re-prompted, got:\n%s`, stdout.String())
	}
	got, err := loadConfig(out)
	if err != nil {
		t.Fatal(err)
	}
	if dd := got.Evidence.Sources[0].Config[plugins.JournalConfigDailyDir]; dd != "02 - Daily & Logs" {
		t.Fatalf("want the valid daily_dir written, got %q", dd)
	}
	if problems := validateConfigDraft(context.Background(), got); len(problems) != 0 {
		t.Fatalf("written journal config must validate: %v", problems)
	}
}

// --- dispatch ---------------------------------------------------------------

func TestConfig_UnknownSubcommand(t *testing.T) {
	code, _, _ := runConfigArgs(t, strings.NewReader(""), false, false, "config")
	if code != exitUsage {
		t.Fatalf("a bare config must be a usage error, got %d", code)
	}
	code, _, stderr := runConfigArgs(t, strings.NewReader(""), false, false, "config", "bogus")
	if code != exitUsage || !strings.Contains(stderr, "unknown config subcommand") {
		t.Fatalf("an unknown config subcommand must be a usage error, got %d (%s)", code, stderr)
	}
}

func TestConfig_EditMissingConfigErrors(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home) // default config path does not exist
	code, _, stderr := runConfigArgs(t, strings.NewReader(gitLocalPlainInput()), false, false,
		"config", "edit", "--plain")
	if code != exitError {
		t.Fatalf("editing a missing config must error, got %d", code)
	}
	if !strings.Contains(stderr, "no existing config") {
		t.Fatalf("the error should point at `config init`:\n%s", stderr)
	}
}

// --- GAP-13 (#126): the metrics section is validated at LOAD and at AUTHORING ---

// TestConfig_RejectsInvalidMetricsSectionAtAuthoring (D3/AC3) asserts the metrics
// section is validated OFFLINE at authoring time: a URL catalog and a blank
// transcript value are both rejected with no file written.
func TestConfig_RejectsInvalidMetricsSectionAtAuthoring(t *testing.T) {
	cases := []struct {
		name    string
		metrics metricsConfig
		want    string
	}{
		{
			name:    "url-catalog",
			metrics: metricsConfig{ModelCatalog: "https://example.com/models-store.json"},
			want:    "metrics.model_catalog",
		},
		{
			name:    "blank-transcript-value",
			metrics: metricsConfig{Transcripts: map[string]string{"pi": "   "}},
			want:    "metrics.transcripts.pi",
		},
		{
			name:    "nul-in-transcript-value",
			metrics: metricsConfig{Transcripts: map[string]string{"claude": "~/.claude\x00/projects"}},
			want:    "NUL",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			cfg := gitLocalConfig()
			cfg.Metrics = tc.metrics
			answers := filepath.Join(dir, "answers.json")
			writeConfigDoc(t, answers, cfg)
			out := filepath.Join(dir, "timesheet.json")

			code, _, stderr := runConfigArgs(t, strings.NewReader(""), false, false,
				"config", "init", "--answers", answers, "--out", out)
			if code != exitError {
				t.Fatalf("an invalid metrics section must be rejected, got %d (%s)", code, stderr)
			}
			if !strings.Contains(stderr, tc.want) {
				t.Fatalf("the error must name the field %q:\n%s", tc.want, stderr)
			}
			if _, err := os.Stat(out); err == nil {
				t.Fatalf("a rejected config must write nothing")
			}
		})
	}
}

// TestConfig_AcceptsMetricsSection (D3/AC3) asserts a valid, harness-AGNOSTIC metrics
// section is authored as written: free-form harness keys are NOT validated against
// the adapter registry, and no existence check runs on the paths.
func TestConfig_AcceptsMetricsSection(t *testing.T) {
	dir := t.TempDir()
	cfg := gitLocalConfig()
	cfg.Metrics = metricsConfig{
		ModelCatalog: "/nowhere/models-store.json",
		Transcripts: map[string]string{
			"pi":                   "~/.pi/agent/sessions",
			"claude":               "~/.claude/projects",
			"antigravity":          "~/.gemini/antigravity/brain",
			"unknown-harness-name": "/somewhere/else",
		},
	}
	answers := filepath.Join(dir, "answers.json")
	writeConfigDoc(t, answers, cfg)
	out := filepath.Join(dir, "timesheet.json")

	code, _, stderr := runConfigArgs(t, strings.NewReader(""), false, false,
		"config", "init", "--answers", answers, "--out", out)
	if code != exitOK {
		t.Fatalf("a valid metrics section must be accepted, got %d:\n%s", code, stderr)
	}
	written := string(mustReadFile(t, out))
	for _, want := range []string{`"model_catalog": "/nowhere/models-store.json"`, `"unknown-harness-name": "/somewhere/else"`} {
		if !strings.Contains(written, want) {
			t.Fatalf("written config missing %s:\n%s", want, written)
		}
	}
}

// TestLoadConfig_RejectsInvalidMetricsSection (D3/AC3) asserts the SAME validator
// runs on the fatal load path, beside editorial.
func TestLoadConfig_RejectsInvalidMetricsSection(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "timesheet.json")
	if err := os.WriteFile(path, []byte(`{"metrics":{"model_catalog":"https://example.com/store.json"}}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if _, err := loadConfig(path); err == nil || !strings.Contains(err.Error(), "metrics.model_catalog") {
		t.Fatalf("loadConfig error = %v, want a metrics.model_catalog rejection", err)
	}

	ok := filepath.Join(dir, "ok.json")
	if err := os.WriteFile(ok, []byte(`{"metrics":{"transcripts":{"pi":"~/.pi/agent/sessions"}}}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := loadConfig(ok)
	if err != nil {
		t.Fatalf("a valid metrics section must load: %v", err)
	}
	if cfg.Metrics.Transcripts["pi"] != "~/.pi/agent/sessions" {
		t.Fatalf("metrics section not loaded: %+v", cfg.Metrics)
	}
}
