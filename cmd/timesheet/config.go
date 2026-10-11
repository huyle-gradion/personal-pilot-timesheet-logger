package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/editorial"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/evidence"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/evidence/plugins"
)

// This file implements the supported interactive configuration flow
// (`timesheet config init|edit`). It authors the EXISTING cliConfig schema only:
// no new config fields are invented, no secret is ever written (credential
// fields hold `keychain:<service>` references), and authoring performs NO live
// network or Keychain call (validation is offline: the registry's ValidateSources
// checks configuration/contract only).
//
// Interaction reuses the EXISTING prompter seam, so the huh TUI and the safe
// line-based `--plain` path share one flow, and tests inject a fake prompter to
// drive the REAL flow without a terminal. A fully scripted path (`--answers FILE`,
// or `--non-interactive`) lets the skill/harness delegate the write with no TTY.
//
// Guided-edit contract: the flow edits the schema fields it COLLECTS and preserves
// uncollected values where it can (the editorial tuning fields and existing
// classification entries are prefilled from the loaded config). Each source's
// `config` map, however, is REBUILT from the collected keys only, so uncollected
// source keys (calendar `source`, `fixture`/`fixture_date`; git `fixture`) are
// dropped — deliberate, since fixtures are test-only. Use `--answers` to author a
// document that carries keys the guided flow does not prompt for.

// errConfigExists reports that the target config exists and overwrite was not
// consented to. It is a REFUSAL, not a write failure: nothing is modified.
var errConfigExists = errors.New("config already exists")

// --- paths ------------------------------------------------------------------

// defaultConfigPath is the default output path for `timesheet config`.
func defaultConfigPath() string {
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".timesheet", "config", "timesheet.json")
	}
	return filepath.Join(".timesheet", "config", "timesheet.json")
}

// expandHome expands a leading "~" or "~/" using the user's home directory. It is
// used for CLI PATHS (--out/--config/--answers) so an unexpanded "~" from an
// agent or an API call still resolves; it never rewrites values INSIDE a config.
func expandHome(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			if p == "~" {
				return home
			}
			return filepath.Join(home, p[2:])
		}
	}
	return p
}

// configWriteTarget resolves the write path: --out wins, else the --config path
// (edit in place), else the default path.
func configWriteTarget(out, configPath string) string {
	if p := expandHome(out); p != "" {
		return p
	}
	if p := expandHome(configPath); p != "" {
		return p
	}
	return defaultConfigPath()
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

// --- mode selection ---------------------------------------------------------

// configRequest is the resolved flag + environment state for config mode
// selection.
type configRequest struct {
	// Interactive is the EXPLICIT alias of the DEFAULT guided mode: it selects the
	// same TTY/CI gate as no flag at all, and never alters it. It exists so a
	// caller can state the intent (`config init --interactive`) rather than rely on
	// the implicit default.
	Interactive    bool
	Plain          bool
	NonInteractive bool
	Answers        bool
	StdinTTY       bool
	StdoutTTY      bool
	CI             bool
}

// resolveConfigMode picks how `config` interacts. `--answers`/`--non-interactive`
// force the scripted path; `--plain` forces the safe line-based path. The default
// is the guided TUI, which requires a terminal on BOTH stdin and stdout and a
// non-CI environment, else it REFUSES (never a silent fallback), naming `--plain`
// / `--answers`.
//
// `req.Interactive` is the explicit alias of that SAME default guided mode: it
// deliberately does NOT change the gate (it is not read here to alter behavior),
// so `--interactive` and no flag select identically.
func resolveConfigMode(req configRequest) (interactionMode, error) {
	if req.Answers || req.NonInteractive {
		return modeScripted, nil
	}
	if req.Plain {
		return modePlain, nil
	}
	if req.StdinTTY && req.StdoutTTY && !req.CI {
		return modeTUI, nil
	}
	return modeScripted, errors.New("config needs a terminal on both stdin and stdout for the guided flow; use --plain for a safe line-based session or --answers FILE for a scripted config")
}

// --- flags + dispatch -------------------------------------------------------

type configFlags struct {
	out            string
	configPath     string
	answersPath    string
	interactive    bool
	plain          bool
	nonInteractive bool
	force          bool
}

func registerConfigFlags(fs *flag.FlagSet, allowConfig bool) *configFlags {
	fl := &configFlags{}
	fs.StringVar(&fl.out, "out", "", "output path (default ~/.timesheet/config/timesheet.json)")
	if allowConfig {
		fs.StringVar(&fl.configPath, "config", "", "existing config to load and prefill (default ~/.timesheet/config/timesheet.json)")
	}
	fs.StringVar(&fl.answersPath, "answers", "", "non-interactive: a complete config JSON document to validate and write")
	fs.BoolVar(&fl.interactive, "interactive", false, "the explicit alias of the default guided terminal UI (same TTY/CI gate)")
	fs.BoolVar(&fl.plain, "plain", false, "guided line-based prompts (safe on a pipe)")
	fs.BoolVar(&fl.nonInteractive, "non-interactive", false, "never prompt (requires --answers FILE)")
	fs.BoolVar(&fl.force, "force", false, "overwrite an existing config without a confirmation")
	return fl
}

func configRequestFor(fl configFlags, stdinTTY, stdoutTTY bool) configRequest {
	return configRequest{
		Interactive:    fl.interactive,
		Plain:          fl.plain,
		NonInteractive: fl.nonInteractive,
		Answers:        strings.TrimSpace(fl.answersPath) != "",
		StdinTTY:       stdinTTY,
		StdoutTTY:      stdoutTTY,
		CI:             ciDetected(),
	}
}

// cmdConfig dispatches `config init` / `config edit`.
func cmdConfig(ctx context.Context, args []string, in io.Reader, stdout, stderr io.Writer, stdinTTY, stdoutTTY bool) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "timesheet: config requires a subcommand: init | edit")
		return exitUsage
	}
	switch args[0] {
	case "init":
		return cmdConfigInit(ctx, args[1:], in, stdout, stderr, stdinTTY, stdoutTTY)
	case "edit":
		return cmdConfigEdit(ctx, args[1:], in, stdout, stderr, stdinTTY, stdoutTTY)
	default:
		fmt.Fprintf(stderr, "timesheet: unknown config subcommand %q\n", args[0])
		return exitUsage
	}
}

func cmdConfigInit(ctx context.Context, args []string, in io.Reader, stdout, stderr io.Writer, stdinTTY, stdoutTTY bool) int {
	fs := flag.NewFlagSet("timesheet config init", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fl := registerConfigFlags(fs, false)
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	mode, modeErr := resolveConfigMode(configRequestFor(*fl, stdinTTY, stdoutTTY))
	if modeErr != nil {
		fmt.Fprintln(stderr, "timesheet:", modeErr)
		return exitUsage
	}
	// `config init` never loads an existing config; the scripted path supplies a
	// fresh document via --answers.
	return authorConfig(ctx, configAuthoring{
		action: "init",
		base:   cliConfig{},
		target: configWriteTarget(fl.out, ""),
		flags:  *fl,
		mode:   mode,
	}, in, stdout, stderr)
}

func cmdConfigEdit(ctx context.Context, args []string, in io.Reader, stdout, stderr io.Writer, stdinTTY, stdoutTTY bool) int {
	fs := flag.NewFlagSet("timesheet config edit", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fl := registerConfigFlags(fs, true)
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	mode, modeErr := resolveConfigMode(configRequestFor(*fl, stdinTTY, stdoutTTY))
	if modeErr != nil {
		fmt.Fprintln(stderr, "timesheet:", modeErr)
		return exitUsage
	}
	base := cliConfig{}
	if mode != modeScripted {
		loadPath := expandHome(fl.configPath)
		if loadPath == "" {
			loadPath = defaultConfigPath()
		}
		loaded, err := loadConfig(loadPath)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				fmt.Fprintf(stderr, "timesheet: no existing config at %s; run `timesheet config init` first\n", loadPath)
				return exitError
			}
			fmt.Fprintln(stderr, "timesheet:", err)
			return exitError
		}
		base = loaded
	}
	return authorConfig(ctx, configAuthoring{
		action: "edit",
		base:   base,
		target: configWriteTarget(fl.out, fl.configPath),
		flags:  *fl,
		mode:   mode,
	}, in, stdout, stderr)
}

type configAuthoring struct {
	action string
	base   cliConfig
	target string
	flags  configFlags
	mode   interactionMode
	// prompter overrides the prompter built from mode. It is a TEST seam so the
	// REAL flow can be driven without a terminal; production callers leave it nil.
	prompter prompter
}

// authorConfig runs one config authoring. It gathers (guided) or loads (scripted)
// a cliConfig, validates it BEFORE any write, prints the exact JSON preview,
// obtains explicit consent, and writes atomically. Nothing is written until every
// gate passes.
func authorConfig(ctx context.Context, a configAuthoring, in io.Reader, stdout, stderr io.Writer) int {
	var (
		cfg cliConfig
		p   prompter
	)
	if a.mode == modeScripted {
		if strings.TrimSpace(a.flags.answersPath) == "" {
			fmt.Fprintf(stderr, "timesheet: config %s --non-interactive requires --answers FILE\n", a.action)
			return exitUsage
		}
		loaded, err := loadConfig(expandHome(a.flags.answersPath))
		if err != nil {
			fmt.Fprintln(stderr, "timesheet:", err)
			return exitError
		}
		cfg = loaded
	} else {
		if a.prompter != nil {
			p = a.prompter
		} else if a.mode == modeTUI {
			p = newTUIPrompter(in, stdout)
		} else {
			p = newPlainPrompter(in, stderr)
		}
		gathered, err := gatherConfig(ctx, a.base, p, stdout)
		if err != nil {
			if errors.Is(err, errAborted) {
				fmt.Fprintln(stderr, "timesheet: config aborted; no file was written")
				return exitError
			}
			fmt.Fprintln(stderr, "timesheet:", err)
			return exitError
		}
		cfg = gathered
	}

	// Validation BEFORE any write, reusing the existing validators (offline).
	if problems := validateConfigDraft(ctx, cfg); len(problems) > 0 {
		fmt.Fprintln(stderr, "timesheet: config validation failed; no file was written:")
		for _, prob := range problems {
			fmt.Fprintln(stderr, "  - "+prob)
		}
		return exitError
	}

	data, err := marshalConfig(cfg)
	if err != nil {
		fmt.Fprintln(stderr, "timesheet:", err)
		return exitError
	}

	// Preview the EXACT bytes that will be written. The document holds references
	// only (never a resolved secret).
	fmt.Fprintln(stdout, "config preview (nothing written yet)")
	fmt.Fprintf(stdout, "target: %s\n", a.target)
	fmt.Fprintf(stdout, "%s", string(data))

	// Overwrite gate + explicit confirmation.
	exists := fileExists(a.target)
	overwriteConsented := false
	if a.mode == modeScripted {
		if exists && !a.flags.force {
			fmt.Fprintf(stderr, "timesheet: %s already exists; refusing to overwrite (pass --force to overwrite)\n", a.target)
			return exitBlocked
		}
	} else {
		if exists && !a.flags.force {
			ok, cerr := p.Confirm(ctx, fmt.Sprintf("Overwrite the existing config at %s?", a.target), false)
			if cerr != nil {
				fmt.Fprintln(stderr, "timesheet: config aborted; no file was written")
				return exitError
			}
			if !ok {
				fmt.Fprintf(stderr, "timesheet: refused to overwrite %s; no file was written (pass --force to overwrite)\n", a.target)
				return exitBlocked
			}
			overwriteConsented = true
		}
		ok, cerr := p.Confirm(ctx, fmt.Sprintf("Write this config to %s?", a.target), false)
		if cerr != nil {
			fmt.Fprintln(stderr, "timesheet: config aborted; no file was written")
			return exitError
		}
		if !ok {
			fmt.Fprintln(stderr, "timesheet: config not written (declined)")
			return exitError
		}
	}

	// allowOverwrite is true only for --force or a consented overwrite. A file
	// that APPEARS between the check above and the write is still refused
	// (writeConfigAtomic re-checks) unless consent was already given.
	allowOverwrite := a.flags.force || overwriteConsented
	if err := writeConfigAtomic(a.target, data, allowOverwrite); err != nil {
		if errors.Is(err, errConfigExists) {
			fmt.Fprintf(stderr, "timesheet: %s already exists; refusing to overwrite (pass --force to overwrite)\n", a.target)
			return exitBlocked
		}
		fmt.Fprintln(stderr, "timesheet:", err)
		return exitError
	}
	fmt.Fprintf(stdout, "wrote %s\n", a.target)
	return exitOK
}

// --- serialization + atomic write -------------------------------------------

// marshalConfig renders the config in the one canonical JSON form used for BOTH
// the preview and the write, so the previewed bytes ARE the written bytes. HTML
// escaping is disabled so the document reads like the shipped templates (a literal
// "&" rather than "\u0026").
func marshalConfig(cfg cliConfig) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(cfg); err != nil {
		return nil, fmt.Errorf("encode config: %w", err)
	}
	return buf.Bytes(), nil
}

// writeConfigAtomic writes data to path via a same-directory temp file and a
// rename, so a partial or failed write never corrupts an existing config. When
// overwrite is false and the file exists, it refuses (errConfigExists) — the
// final existence re-check narrows the preview-to-write window. Missing parent
// directories are created (0700); the file is written 0600.
func writeConfigAtomic(path string, data []byte, overwrite bool) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	if !overwrite {
		if _, err := os.Stat(path); err == nil {
			return errConfigExists
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("check target: %w", err)
		}
	}
	tmp, err := os.CreateTemp(dir, ".timesheet-config-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()
	// Best-effort cleanup: on success the rename consumes the temp file.
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("install config: %w", err)
	}
	return nil
}

// --- validation -------------------------------------------------------------

// validateConfigDraft validates a config BEFORE any write, reusing the existing
// validators where possible. It makes NO live network or Keychain call: the
// registry path (ValidateSources) checks configuration requirements and each
// plugin's declared contract only. Every problem is actionable and secret-free.
func validateConfigDraft(ctx context.Context, cfg cliConfig) []string {
	var problems []string

	// IANA timezone (empty means UTC, matching run.loadLocation).
	if tz := strings.TrimSpace(cfg.Evidence.Timezone); tz != "" {
		if _, err := time.LoadLocation(tz); err != nil {
			problems = append(problems, fmt.Sprintf("evidence.timezone %q is not a valid IANA timezone name", tz))
		}
	}

	// Source configuration: required keys, the git mode/repos/project_map
	// contract, journal vault/daily_dir, calendar url_ref + source id, agent path,
	// and unknown plugin types. A raw value in a declared credential-reference
	// field (calendar url_ref) is REJECTED here as CONTRACT_CONFIG_SECRET_NOT_KEYCHAIN_REF.
	if results, err := buildRegistry("").ValidateSources(ctx, cfg.Evidence.Sources); err != nil {
		problems = append(problems, "source validation error: "+err.Error())
	} else {
		for _, res := range results {
			if res.Status == evidence.StatusComplete || res.Status == evidence.StatusSkipped {
				continue
			}
			problems = append(problems, sourceProblem(res))
		}
	}

	// Credential-REFERENCE format checks on the top-level fields not covered by
	// the registry (gradion + editorial).
	if ref := strings.TrimSpace(cfg.Gradion.CredentialRef); ref != "" {
		if err := checkKeychainRef(ref); err != nil {
			problems = append(problems, "gradion.credential_ref: "+err.Error())
		}
	}
	if ep := strings.TrimSpace(cfg.Gradion.Endpoint); ep != "" && strings.TrimSpace(cfg.Gradion.CredentialRef) == "" {
		problems = append(problems, "gradion.endpoint is set but gradion.credential_ref is missing (use keychain:<service>)")
	}

	// Editorial: reuse the shipped validator (credential_ref format, soft_target,
	// max_attempts).
	if err := cfg.Editorial.Validate(); err != nil {
		problems = append(problems, err.Error())
	}

	// The managed-export daily_dir must stay relative to the vault.
	if dd := strings.TrimSpace(cfg.DailyDir); dd != "" {
		if err := checkRelativeDir(dd); err != nil {
			problems = append(problems, "daily_dir: "+err.Error())
		}
	}
	return problems
}

// sourceProblem renders one failed source result as an actionable message that
// names the source and its stable code, never a secret value.
func sourceProblem(res evidence.SourceResult) string {
	head := fmt.Sprintf("source %q", res.SourceID)
	if res.Type != "" {
		head += fmt.Sprintf(" (%s)", string(res.Type))
	}
	detail := describeContractCode(res.ErrorCode)
	if res.ErrorCode == "" {
		return head + ": " + detail
	}
	return fmt.Sprintf("%s: %s [%s]", head, detail, res.ErrorCode)
}

// describeContractCode maps a stable contract/error code to an actionable,
// secret-free explanation.
func describeContractCode(code string) string {
	switch code {
	case evidence.CodeUnknownPluginType:
		return "unknown source type; not enabled"
	case evidence.CodeConfigRequiredMissing:
		return "a required config key is missing"
	case evidence.CodeConfigSecretNotRef:
		return "a credential-reference field must be keychain:<service>, never a raw value"
	case plugins.CodeGitModeInvalid:
		return "git mode must be one of local, remote, both"
	case plugins.CodeGitReposRequired:
		return "repos is required (host-less owner/repo) when the git mode includes remote"
	case plugins.CodeGitProjectMapInvalid:
		return "project_map must be a JSON object of repo -> project"
	case plugins.JournalWarnVaultMissing:
		return "journal vault root is required"
	case plugins.JournalWarnDailyDirInvalid:
		return `journal daily_dir must be a relative path confined to the vault`
	case "CALENDAR_SOURCE_ID_REQUIRED":
		return "calendar source id is required"
	case "CALENDAR_SOURCE_ID_UNSAFE":
		return "calendar source id must be a non-secret identifier, not a URL or filesystem path"
	case "AGENT_PATH_REQUIRED":
		return "agent session path is required"
	default:
		return "failed configuration validation"
	}
}

// checkKeychainRef verifies a credential-reference field is a `keychain:<service>`
// reference with a non-empty service. A raw value is rejected.
func checkKeychainRef(ref string) error {
	if !strings.HasPrefix(ref, keychainRefPref) {
		return errors.New("must be a keychain:<service> reference, never a raw value")
	}
	if strings.TrimSpace(strings.TrimPrefix(ref, keychainRefPref)) == "" {
		return errors.New("keychain reference has an empty service")
	}
	return nil
}

// checkRelativeDir verifies a directory that is joined under a vault root. It
// mirrors the journal plugin's cleanRelativeDir (internal/evidence/plugins/
// journal.go): reject a NUL, an absolute path, ".", "..", a ".." prefix, and any
// ".." segment — so a guided prompt fails clearly and re-prompts instead of
// passing the prompt and failing later validation. An empty value is allowed here
// (callers guard for non-empty).
func checkRelativeDir(dir string) error {
	d := strings.TrimSpace(dir)
	if d == "" {
		return nil
	}
	if strings.ContainsRune(d, 0) {
		return errors.New("must not contain a NUL character")
	}
	if filepath.IsAbs(d) {
		return errors.New("must be a directory RELATIVE to the vault, not an absolute path")
	}
	cleaned := filepath.Clean(d)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return errors.New(`must be a real subdirectory of the vault (not "." and not an escape via "..")`)
	}
	for _, seg := range strings.Split(cleaned, string(filepath.Separator)) {
		if seg == ".." {
			return errors.New(`must not escape the vault with ".."`)
		}
	}
	return nil
}

// --- guided gathering -------------------------------------------------------

// gatherConfig runs the guided question flow and returns the authored config. A
// blank answer keeps the current (prefilled) value. It never resolves a secret.
func gatherConfig(ctx context.Context, base cliConfig, p prompter, out io.Writer) (cliConfig, error) {
	cfg := base

	fmt.Fprintln(out, "Configure the timesheet (blank keeps the value shown in brackets).")

	id, err := p.Input(ctx, "Evidence id", "a non-secret label for this collection", defaultStr(cfg.Evidence.ID, "default"))
	if err != nil {
		return cliConfig{}, err
	}
	cfg.Evidence.ID = strings.TrimSpace(id)

	tz, err := promptTimezone(ctx, p, out, defaultStr(cfg.Evidence.Timezone, localZoneName()))
	if err != nil {
		return cliConfig{}, err
	}
	cfg.Evidence.Timezone = tz

	sources, err := gatherSources(ctx, p, out, cfg.Evidence.Sources)
	if err != nil {
		return cliConfig{}, err
	}
	cfg.Evidence.Sources = sources

	sd, err := p.Input(ctx, "Storage directory", "workflow records", defaultStr(cfg.StorageDir, "~/.timesheet/records"))
	if err != nil {
		return cliConfig{}, err
	}
	cfg.StorageDir = strings.TrimSpace(sd)

	vt, err := p.Input(ctx, "Vault export root", "blank for no managed export", cfg.Vault)
	if err != nil {
		return cliConfig{}, err
	}
	cfg.Vault = strings.TrimSpace(vt)
	if cfg.Vault != "" {
		dd, err := p.Input(ctx, "Daily-note directory (relative to the vault)", `e.g. "02 - Daily & Logs"`, cfg.DailyDir)
		if err != nil {
			return cliConfig{}, err
		}
		cfg.DailyDir = strings.TrimSpace(dd)
	} else {
		cfg.DailyDir = ""
	}

	if err := gatherGradion(ctx, p, out, &cfg); err != nil {
		return cliConfig{}, err
	}
	if err := gatherEditorial(ctx, p, out, &cfg); err != nil {
		return cliConfig{}, err
	}
	cls, err := gatherClassifications(ctx, p, cfg.Classifications)
	if err != nil {
		return cliConfig{}, err
	}
	cfg.Classifications = cls

	return cfg, nil
}

// gatherSources selects the enabled source types and collects each one's config.
// It manages at most ONE source per type; a config with two sources of the same
// type is refused rather than silently collapsed.
func gatherSources(ctx context.Context, p prompter, out io.Writer, existing []evidence.SourceConfig) ([]evidence.SourceConfig, error) {
	byType := map[evidence.SourceKind]evidence.SourceConfig{}
	for _, s := range existing {
		if _, dup := byType[s.Type]; dup {
			return nil, fmt.Errorf("config can edit at most one source per type; this config has more than one %q source — edit it manually", string(s.Type))
		}
		byType[s.Type] = s
	}

	types := []evidence.SourceKind{evidence.SourceCalendar, plugins.TypeGit, plugins.TypeJournal, evidence.SourceAgentSession}
	opts := []promptOption{
		{Label: "Calendar (ICS feed)"},
		{Label: "Git (local checkouts and/or remote GitHub repos)"},
		{Label: "Journal (Obsidian daily note, read-only)"},
		{Label: "Agent session events"},
	}
	var current []string
	for _, t := range types {
		if _, ok := byType[t]; ok {
			current = append(current, string(t))
		}
	}
	title := "Which evidence sources should be enabled? (comma-separated numbers, blank for none)"
	if len(current) > 0 {
		title += " [currently: " + strings.Join(current, ", ") + "]"
	}
	sel, err := p.MultiChoose(ctx, title, opts)
	if err != nil {
		return nil, err
	}
	chosen := map[int]bool{}
	for _, i := range sel {
		if i >= 0 && i < len(types) {
			chosen[i] = true
		}
	}

	sources := make([]evidence.SourceConfig, 0, len(types))
	for i, t := range types {
		if !chosen[i] {
			continue
		}
		ex, has := byType[t]
		src, err := gatherSource(ctx, p, out, t, ex, has)
		if err != nil {
			return nil, err
		}
		sources = append(sources, src)
	}
	return sources, nil
}

// gatherSource collects one source's fields for its type. The returned source's
// `config` map is REBUILT from the collected keys only; an existing source's other
// keys are read for PREFILL but are not carried over (see the guided-edit contract
// in the file header). This is deliberate: uncollected keys are test-only fixtures
// or advanced controls the guided flow does not prompt for.
func gatherSource(ctx context.Context, p prompter, out io.Writer, t evidence.SourceKind, ex evidence.SourceConfig, has bool) (evidence.SourceConfig, error) {
	prev := map[string]string{}
	idDef := defaultSourceID(t)
	if has {
		idDef = defaultStr(ex.ID, idDef)
		for k, v := range ex.Config {
			prev[k] = v
		}
	}
	label := sourceTypeLabel(t)

	id, err := p.Input(ctx, label+" source id", "non-secret identity, never a URL or path", idDef)
	if err != nil {
		return evidence.SourceConfig{}, err
	}
	src := evidence.SourceConfig{ID: strings.TrimSpace(id), Type: t, Enabled: true, Config: map[string]string{}}

	switch t {
	case evidence.SourceCalendar:
		ref, err := promptKeychainRef(ctx, p, out, "Calendar ICS URL reference", defaultStr(prev[plugins.CalendarConfigURLRef], "keychain:calendar_feed_url"))
		if err != nil {
			return evidence.SourceConfig{}, err
		}
		src.Config[plugins.CalendarConfigURLRef] = ref
		email, err := p.Input(ctx, "Calendar owner email (optional)", "used to match invitations", prev[plugins.CalendarConfigUserEmail])
		if err != nil {
			return evidence.SourceConfig{}, err
		}
		if e := strings.TrimSpace(email); e != "" {
			src.Config[plugins.CalendarConfigUserEmail] = e
		}
		if tz, err := promptOptionalTimezone(ctx, p, out, "Calendar timezone (optional)", prev[plugins.CalendarConfigTimezone]); err != nil {
			return evidence.SourceConfig{}, err
		} else if tz != "" {
			src.Config[plugins.CalendarConfigTimezone] = tz
		}
		cp, err := p.Input(ctx, "Calendar cache path (optional)", "blank to omit", prev[plugins.CalendarConfigCachePath])
		if err != nil {
			return evidence.SourceConfig{}, err
		}
		if s := strings.TrimSpace(cp); s != "" {
			src.Config[plugins.CalendarConfigCachePath] = s
		}
		noCache, err := p.Confirm(ctx, "Disable the calendar ICS cache (no_cache)?", truthyValue(prev[plugins.CalendarConfigNoCache]))
		if err != nil {
			return evidence.SourceConfig{}, err
		}
		if noCache {
			src.Config[plugins.CalendarConfigNoCache] = "true"
		}

	case plugins.TypeGit:
		modeDef := defaultStr(prev[plugins.GitConfigMode], plugins.GitModeBoth)
		idx, err := p.Choose(ctx, "Git mode", []promptOption{
			{Label: "local (local checkouts only; no GitHub credential)"},
			{Label: "remote (GitHub owner/repo only)"},
			{Label: "both (local + remote GitHub)"},
		}, gitModeIndex(modeDef))
		if err != nil {
			return evidence.SourceConfig{}, err
		}
		mode := []string{plugins.GitModeLocal, plugins.GitModeRemote, plugins.GitModeBoth}[idx]
		src.Config[plugins.GitConfigMode] = mode
		if mode != plugins.GitModeRemote {
			lr, err := p.Input(ctx, "Local repo paths (comma-separated)", `default "."`, defaultStr(prev[plugins.GitConfigLocalRepos], "."))
			if err != nil {
				return evidence.SourceConfig{}, err
			}
			if s := strings.TrimSpace(lr); s != "" {
				src.Config[plugins.GitConfigLocalRepos] = s
			}
		}
		if mode != plugins.GitModeLocal {
			rp, err := promptRequired(ctx, p, out, "Remote repos (host-less owner/repo, comma-separated)", prev[plugins.GitConfigRepos])
			if err != nil {
				return evidence.SourceConfig{}, err
			}
			src.Config[plugins.GitConfigRepos] = rp
		}
		author, err := p.Input(ctx, "Commit author filter (optional)", "email or name", prev[plugins.GitConfigAuthor])
		if err != nil {
			return evidence.SourceConfig{}, err
		}
		if s := strings.TrimSpace(author); s != "" {
			src.Config[plugins.GitConfigAuthor] = s
		}
		pm, err := promptProjectMap(ctx, p, out, prev[plugins.GitConfigProjectMap])
		if err != nil {
			return evidence.SourceConfig{}, err
		}
		if pm != "" {
			src.Config[plugins.GitConfigProjectMap] = pm
		}

	case plugins.TypeJournal:
		vault, err := promptRequired(ctx, p, out, "Journal vault root", prev[plugins.JournalConfigVault])
		if err != nil {
			return evidence.SourceConfig{}, err
		}
		src.Config[plugins.JournalConfigVault] = vault
		dd, err := promptRelativeDir(ctx, p, out, "Journal daily_dir (relative to the vault)", prev[plugins.JournalConfigDailyDir])
		if err != nil {
			return evidence.SourceConfig{}, err
		}
		src.Config[plugins.JournalConfigDailyDir] = dd

	case evidence.SourceAgentSession:
		path, err := promptRequired(ctx, p, out, "Agent session JSONL path", prev[plugins.AgentConfigPath])
		if err != nil {
			return evidence.SourceConfig{}, err
		}
		src.Config[plugins.AgentConfigPath] = path
	}
	return src, nil
}

func gatherGradion(ctx context.Context, p prompter, out io.Writer, cfg *cliConfig) error {
	has := strings.TrimSpace(cfg.Gradion.Endpoint) != ""
	on, err := p.Confirm(ctx, "Configure a Gradion MCP endpoint (connected/remote mode)?", has)
	if err != nil {
		return err
	}
	if !on {
		cfg.Gradion = gradionConfig{}
		return nil
	}
	ep, err := promptRequired(ctx, p, out, "Gradion MCP endpoint URL", cfg.Gradion.Endpoint)
	if err != nil {
		return err
	}
	ref, err := promptKeychainRef(ctx, p, out, "Gradion credential reference", defaultStr(cfg.Gradion.CredentialRef, "keychain:gradion.workspace.ApiKey"))
	if err != nil {
		return err
	}
	cfg.Gradion = gradionConfig{Endpoint: ep, CredentialRef: ref}
	return nil
}

// gatherEditorial configures the optional editorial layer. On a guided EDIT it
// starts from the loaded config so the tuning fields the flow does not prompt for
// (soft_target, max_attempts, endpoint, model) are PRESERVED, not silently zeroed;
// only the fields this flow collects (enabled, adapter, command, credential_ref)
// are rewritten. Turning editorial OFF clears the block.
func gatherEditorial(ctx context.Context, p prompter, out io.Writer, cfg *cliConfig) error {
	on, err := p.Confirm(ctx, "Configure the optional editorial agent (TEXT ONLY)?", cfg.Editorial.Enabled)
	if err != nil {
		return err
	}
	if !on {
		cfg.Editorial = editorial.Config{}
		return nil
	}
	idx, err := p.Choose(ctx, "Editorial adapter", []promptOption{
		{Label: "none (deterministic text only)"},
		{Label: "command (external agent, TEXT ONLY)"},
	}, adapterIndex(cfg.Editorial.Adapter))
	if err != nil {
		return err
	}
	// Start from the base config to preserve uncollected tuning fields.
	ec := cfg.Editorial
	ec.Enabled = true
	ec.Adapter = []string{"none", "command"}[idx]
	if ec.Adapter == "command" {
		line, err := promptRequired(ctx, p, out, "Editorial command (space-separated argv)", strings.Join(cfg.Editorial.Command, " "))
		if err != nil {
			return err
		}
		ec.Command = strings.Fields(line)
		ref, err := promptOptionalKeychainRef(ctx, p, out, "Editorial credential reference (optional)", cfg.Editorial.CredentialRef)
		if err != nil {
			return err
		}
		ec.CredentialRef = ref
	} else {
		// The command adapter's fields do not apply to a "none" adapter.
		ec.Command = nil
		ec.CredentialRef = ""
	}
	cfg.Editorial = ec
	return nil
}

// gatherClassifications collects the optional project → classification-id map as
// repeated key/value pairs. Existing entries are PRESERVED (prefilled), so a
// guided edit never silently drops a mapping; removing one requires a manual edit.
func gatherClassifications(ctx context.Context, p prompter, existing map[string]string) (map[string]string, error) {
	out := map[string]string{}
	for k, v := range existing {
		out[k] = v
	}
	for {
		proj, err := p.Input(ctx, "Classification map: project name (blank to finish)", "", "")
		if err != nil {
			return nil, err
		}
		proj = strings.TrimSpace(proj)
		if proj == "" {
			break
		}
		id, err := p.Input(ctx, "Classification id for "+proj, "", existing[proj])
		if err != nil {
			return nil, err
		}
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		out[proj] = id
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// --- prompt helpers ---------------------------------------------------------

// promptTimezone re-prompts until the value is a valid IANA name.
func promptTimezone(ctx context.Context, p prompter, out io.Writer, initial string) (string, error) {
	for {
		v, err := p.Input(ctx, "Timezone (IANA name)", "e.g. UTC or America/Los_Angeles", initial)
		if err != nil {
			return "", err
		}
		v = strings.TrimSpace(v)
		if _, err := time.LoadLocation(v); err != nil {
			fmt.Fprintf(out, "%q is not a valid IANA timezone; try e.g. UTC or America/Los_Angeles\n", v)
			initial = ""
			continue
		}
		return v, nil
	}
}

// promptOptionalTimezone validates a value only when non-empty.
func promptOptionalTimezone(ctx context.Context, p prompter, out io.Writer, title, initial string) (string, error) {
	for {
		v, err := p.Input(ctx, title, "blank to omit", initial)
		if err != nil {
			return "", err
		}
		v = strings.TrimSpace(v)
		if v == "" {
			return "", nil
		}
		if _, err := time.LoadLocation(v); err != nil {
			fmt.Fprintf(out, "%q is not a valid IANA timezone\n", v)
			initial = ""
			continue
		}
		return v, nil
	}
}

// promptKeychainRef re-prompts until the value is a valid keychain:<service>
// reference (required).
func promptKeychainRef(ctx context.Context, p prompter, out io.Writer, title, initial string) (string, error) {
	for {
		v, err := p.Input(ctx, title, "keychain:<service> (never a raw secret)", initial)
		if err != nil {
			return "", err
		}
		v = strings.TrimSpace(v)
		if err := checkKeychainRef(v); err != nil {
			fmt.Fprintf(out, "%s: %v\n", title, err)
			initial = ""
			continue
		}
		return v, nil
	}
}

// promptOptionalKeychainRef validates a value only when non-empty.
func promptOptionalKeychainRef(ctx context.Context, p prompter, out io.Writer, title, initial string) (string, error) {
	for {
		v, err := p.Input(ctx, title, "blank to omit", initial)
		if err != nil {
			return "", err
		}
		v = strings.TrimSpace(v)
		if v == "" {
			return "", nil
		}
		if err := checkKeychainRef(v); err != nil {
			fmt.Fprintf(out, "%s: %v\n", title, err)
			initial = ""
			continue
		}
		return v, nil
	}
}

// promptRequired re-prompts until a non-empty value is given.
func promptRequired(ctx context.Context, p prompter, out io.Writer, title, initial string) (string, error) {
	for {
		v, err := p.Input(ctx, title, "required", initial)
		if err != nil {
			return "", err
		}
		v = strings.TrimSpace(v)
		if v == "" {
			fmt.Fprintf(out, "%s is required\n", title)
			initial = ""
			continue
		}
		return v, nil
	}
}

// promptRelativeDir re-prompts until a non-empty, vault-confined relative path is
// given.
func promptRelativeDir(ctx context.Context, p prompter, out io.Writer, title, initial string) (string, error) {
	for {
		v, err := p.Input(ctx, title, `e.g. "02 - Daily & Logs"`, initial)
		if err != nil {
			return "", err
		}
		v = strings.TrimSpace(v)
		if v == "" {
			fmt.Fprintf(out, "%s is required\n", title)
			initial = ""
			continue
		}
		if err := checkRelativeDir(v); err != nil {
			fmt.Fprintf(out, "%s: %v\n", title, err)
			initial = ""
			continue
		}
		return v, nil
	}
}

// promptProjectMap re-prompts until the value is blank or a valid JSON object of
// repo -> project.
func promptProjectMap(ctx context.Context, p prompter, out io.Writer, initial string) (string, error) {
	for {
		v, err := p.Input(ctx, "Project map (optional JSON object repo -> project)", `{"owner/repo":"Project"}`, initial)
		if err != nil {
			return "", err
		}
		v = strings.TrimSpace(v)
		if v == "" {
			return "", nil
		}
		var m map[string]string
		if err := json.Unmarshal([]byte(v), &m); err != nil {
			fmt.Fprintf(out, "project map must be a JSON object of repo -> project: %v\n", err)
			initial = ""
			continue
		}
		return v, nil
	}
}

// --- small helpers ----------------------------------------------------------

func defaultStr(v, def string) string {
	if s := strings.TrimSpace(v); s != "" {
		return s
	}
	return def
}

// truthyValue mirrors the calendar plugin's isTruthy ("1"/"true"/"yes"/"on") so a
// prefill confirms correctly.
func truthyValue(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// localZoneName returns the local IANA zone name when it resolves, else UTC.
func localZoneName() string {
	if tz := strings.TrimSpace(os.Getenv("TZ")); tz != "" {
		if _, err := time.LoadLocation(tz); err == nil {
			return tz
		}
	}
	if loc := time.Local; loc != nil {
		if name := loc.String(); name != "" && name != "Local" {
			if _, err := time.LoadLocation(name); err == nil {
				return name
			}
		}
	}
	return "UTC"
}

func sourceTypeLabel(t evidence.SourceKind) string {
	switch t {
	case evidence.SourceCalendar:
		return "Calendar"
	case plugins.TypeGit:
		return "Git"
	case plugins.TypeJournal:
		return "Journal"
	case evidence.SourceAgentSession:
		return "Agent"
	default:
		return string(t)
	}
}

func defaultSourceID(t evidence.SourceKind) string {
	switch t {
	case evidence.SourceCalendar:
		return "cal-work"
	case plugins.TypeGit:
		return "git-work"
	case plugins.TypeJournal:
		return "journal-work"
	case evidence.SourceAgentSession:
		return "agent-session"
	default:
		return "source"
	}
}

func gitModeIndex(mode string) int {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case plugins.GitModeLocal:
		return 0
	case plugins.GitModeRemote:
		return 1
	default:
		return 2
	}
}

func adapterIndex(adapter string) int {
	if strings.EqualFold(strings.TrimSpace(adapter), "command") {
		return 1
	}
	return 0
}
