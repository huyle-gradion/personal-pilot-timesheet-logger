package plugins

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/evidence"
)

// TypeJournal is the compile-time registry type of the optional local
// read-only journal (Obsidian daily-note) source.
const TypeJournal evidence.SourceKind = "journal"

// Journal config keys. vault and daily_dir are REQUIRED non-secret paths; a
// relative daily_dir is confined inside the vault. fixture is the OPTIONAL
// contract-mode fixed input (test-only, never required at runtime).
const (
	JournalConfigVault    = "vault"
	JournalConfigDailyDir = "daily_dir"
	JournalConfigFixture  = "fixture"
	// JournalConfigFixtureDate pins the contract-mode target date when the
	// fixture carries no frontmatter date. Test-only.
	JournalConfigFixtureDate = "fixture_date"
)

// defaultJournalContractDate is the fixed target date used in contract mode when
// neither a fixture_date nor a frontmatter date is available.
const defaultJournalContractDate = "2026-01-01"

// Stable journal warning/error codes. Warnings NEVER embed raw note text: they
// carry counts or fixed codes only.
const (
	JournalWarnVaultMissing        = "JOURNAL_VAULT_MISSING"
	JournalWarnDailyDirInvalid     = "JOURNAL_DAILY_DIR_INVALID"
	JournalWarnNoteMissing         = "JOURNAL_NOTE_MISSING"
	JournalWarnNoteUnreadable      = "JOURNAL_NOTE_UNREADABLE"
	JournalWarnTargetDateRequired  = "JOURNAL_TARGET_DATE_REQUIRED"
	JournalWarnSourceIDRequired    = "JOURNAL_SOURCE_ID_REQUIRED"
	JournalWarnManagedMalformed    = "JOURNAL_MANAGED_MARKER_MALFORMED"
	JournalWarnExplicitIDInvalid   = "JOURNAL_EXPLICIT_ID_INVALID"
	JournalWarnExplicitIDDuplicate = "JOURNAL_EXPLICIT_ID_DUPLICATE"
	JournalWarnImplicitIDDuplicate = "JOURNAL_IMPLICIT_ID_DUPLICATE"
	JournalWarnDateMismatch        = "JOURNAL_DATE_MISMATCH"
	JournalWarnBoilerplateExcluded = "JOURNAL_BOILERPLATE_EXCLUDED"
	JournalWarnMetricsExcluded     = "JOURNAL_METRICS_EXCLUDED"
)

// Sentinel read errors, mapped to stable warning codes by journalReadCode.
var (
	errJournalVaultMissing    = errors.New("journal: vault is required")
	errJournalDailyDirInvalid = errors.New("journal: daily_dir is invalid")
	errJournalNoteMissing     = errors.New("journal: note not found")
	errJournalNoteUnreadable  = errors.New("journal: note unreadable")
)

// JournalNote is the raw note content and its path relative to the vault root.
type JournalNote struct {
	Data    []byte
	RelPath string
}

// JournalReader loads the note bytes. It is injected so tests avoid disk and so
// contract mode reads a fixed fixture.
type JournalReader func(ctx context.Context, cfg evidence.SourceConfig, req evidence.CollectRequest) (JournalNote, error)

// JournalPlugin collects narrative journal evidence from the owner's daily note.
// It is READ-ONLY: it never writes the note, never classifies, and never assigns
// activity or working time. Every emitted item is UNTIMED (unknown duration).
type JournalPlugin struct {
	Read JournalReader
}

// NewJournalPlugin returns a journal plugin using the default reader.
func NewJournalPlugin() *JournalPlugin {
	return &JournalPlugin{Read: DefaultJournalRead}
}

// RegisterJournal registers the journal plugin on r.
func RegisterJournal(r *evidence.Registry) { r.Register(NewJournalPlugin()) }

// DefaultJournalRead loads the note: the fixed fixture in contract mode, else
// <vault>/<daily_dir>/<date>.md after path-confinement validation.
func DefaultJournalRead(_ context.Context, cfg evidence.SourceConfig, req evidence.CollectRequest) (JournalNote, error) {
	if req.IsContract() {
		path := strings.TrimSpace(cfg.Config[JournalConfigFixture])
		if path == "" {
			return JournalNote{}, errJournalNoteUnreadable
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return JournalNote{}, errJournalNoteUnreadable
		}
		return JournalNote{Data: data, RelPath: filepath.ToSlash(filepath.Base(path))}, nil
	}
	abs, rel, err := ResolveJournalNotePath(
		cfg.Config[JournalConfigVault], cfg.Config[JournalConfigDailyDir], strings.TrimSpace(req.Date))
	if err != nil {
		return JournalNote{}, err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return JournalNote{}, errJournalNoteMissing
		}
		return JournalNote{}, errJournalNoteUnreadable
	}
	return JournalNote{Data: data, RelPath: rel}, nil
}

// Type implements evidence.Plugin.
func (p *JournalPlugin) Type() evidence.SourceKind { return TypeJournal }

// RequiredConfig implements evidence.Plugin. vault and daily_dir are required,
// non-secret paths. fixture is optional (contract mode only).
func (p *JournalPlugin) RequiredConfig() []evidence.ConfigRequirement {
	return []evidence.ConfigRequirement{
		{Key: JournalConfigVault, Secret: false, Required: true},
		{Key: JournalConfigDailyDir, Secret: false, Required: true},
	}
}

// DeclaredEvidenceKinds implements evidence.Plugin.
func (p *JournalPlugin) DeclaredEvidenceKinds() []evidence.SourceKind {
	return []evidence.SourceKind{evidence.SourceJournal}
}

// CheckContract implements evidence.Plugin. It verifies the configured paths are
// present and the daily_dir is a safe RELATIVE path confined under the vault
// (no absolute path, no "..", no escape). It performs no filesystem access.
func (p *JournalPlugin) CheckContract(_ context.Context, cfg evidence.SourceConfig) []evidence.ContractCheck {
	if strings.TrimSpace(cfg.Config[JournalConfigVault]) == "" {
		return []evidence.ContractCheck{{
			Name:   "journal_vault",
			Passed: false,
			Code:   JournalWarnVaultMissing,
			Detail: "journal vault root is required",
		}}
	}
	if _, err := cleanRelativeDir(cfg.Config[JournalConfigDailyDir]); err != nil {
		return []evidence.ContractCheck{{
			Name:   "journal_daily_dir",
			Passed: false,
			Code:   JournalWarnDailyDirInvalid,
			Detail: "journal daily_dir must be a relative path confined to the vault",
		}}
	}
	return []evidence.ContractCheck{{Name: "journal_config", Passed: true}}
}

// Collect implements evidence.Plugin. A missing vault, an unreadable or missing
// note, a malformed managed-marker region, or duplicate explicit IDs makes the
// source visibly INCOMPLETE with a stable warning code (never a raw note dump).
// A present note with no recognized narrative sections is a COMPLETE empty
// source.
func (p *JournalPlugin) Collect(ctx context.Context, cfg evidence.SourceConfig, req evidence.CollectRequest) (evidence.CollectResult, error) {
	read := p.Read
	if read == nil {
		read = DefaultJournalRead
	}
	note, err := read(ctx, cfg, req)
	if err != nil {
		return incompleteJournalResult(journalReadCode(err)), nil
	}

	sourceID := strings.TrimSpace(cfg.ID)
	if sourceID == "" {
		return incompleteJournalResult(JournalWarnSourceIDRequired), nil
	}

	parsed := parseJournalNote(note.Data)
	if parsed.malformedManaged {
		// Fail closed: a malformed managed region must never let exported text
		// feed back as evidence.
		return incompleteJournalResult(JournalWarnManagedMalformed), nil
	}

	targetDate := strings.TrimSpace(req.Date)
	if targetDate == "" {
		if req.IsContract() {
			targetDate = parsed.frontmatterDate
			if targetDate == "" {
				targetDate = strings.TrimSpace(cfg.Config[JournalConfigFixtureDate])
			}
			if targetDate == "" {
				targetDate = defaultJournalContractDate
			}
		} else {
			return incompleteJournalResult(JournalWarnTargetDateRequired), nil
		}
	}

	// Duplicate explicit IDs fail visibly (fail-closed).
	if len(parsed.duplicateExplicitIDs) > 0 {
		return incompleteJournalResult(JournalWarnExplicitIDDuplicate), nil
	}

	observed := time.Now().UTC()
	warnings := make([]string, 0, 4)
	if n := parsed.boilerplateExcluded; n > 0 {
		warnings = append(warnings, JournalWarnBoilerplateExcluded+":"+itoa(n))
	}
	if n := parsed.metricsExcluded; n > 0 {
		warnings = append(warnings, JournalWarnMetricsExcluded+":"+itoa(n))
	}
	if n := parsed.invalidExplicitIDs; n > 0 {
		warnings = append(warnings, JournalWarnExplicitIDInvalid+":"+itoa(n))
	}

	evs := make([]evidence.Evidence, 0, len(parsed.items))
	mismatch := false
	seenID := make(map[string]bool, len(parsed.items))
	for _, item := range parsed.items {
		stated := item.StatedDate
		if stated == "" {
			stated = parsed.frontmatterDate
		}
		if stated != "" && stated != targetDate {
			mismatch = true
		}
		ev := buildJournalEvidence(item, sourceID, targetDate, note.RelPath, stated, parsed.frontmatterProjects, observed)
		// Two items resolving to the SAME identity would be silently upsert-
		// collapsed downstream (data loss). Detect it visibly instead: explicit
		// duplicates are caught earlier, so a remaining duplicate is implicit.
		if seenID[ev.ID] {
			return incompleteJournalResult(JournalWarnImplicitIDDuplicate), nil
		}
		seenID[ev.ID] = true
		evs = append(evs, ev)
	}
	if mismatch {
		// Preserve BOTH the target date and the stated date; the target is the
		// record date, the stated date is retained on the item's SourceRef.
		warnings = append(warnings, JournalWarnDateMismatch+":"+targetDate)
	}
	sort.Strings(warnings)

	return evidence.CollectResult{
		Evidence: evs,
		Result: evidence.SourceResult{
			Status:        evidence.StatusComplete,
			EvidenceCount: len(evs),
			Warnings:      warnings,
		},
	}, nil
}

// incompleteJournalResult builds an incomplete source result carrying one stable
// warning code and no evidence.
func incompleteJournalResult(code string) evidence.CollectResult {
	return evidence.CollectResult{
		Result: evidence.SourceResult{
			Status:   evidence.StatusIncomplete,
			Warnings: []string{code},
		},
	}
}

// journalReadCode maps a sentinel reader error to a stable warning code.
func journalReadCode(err error) string {
	switch {
	case errors.Is(err, errJournalVaultMissing):
		return JournalWarnVaultMissing
	case errors.Is(err, errJournalDailyDirInvalid):
		return JournalWarnDailyDirInvalid
	case errors.Is(err, errJournalNoteMissing):
		return JournalWarnNoteMissing
	default:
		return JournalWarnNoteUnreadable
	}
}

// --- path confinement -------------------------------------------------------

var journalDatePattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// ResolveJournalNotePath returns the absolute and vault-relative path of
// <vault>/<daily_dir>/<date>.md, refusing an absolute or escaping daily_dir, an
// invalid date, a missing/non-directory vault, or a resolved path that leaves
// the vault through a symlink.
func ResolveJournalNotePath(vault, dailyDir, date string) (absPath, relPath string, err error) {
	vault = strings.TrimSpace(vault)
	if vault == "" {
		return "", "", errJournalVaultMissing
	}
	if !journalDatePattern.MatchString(strings.TrimSpace(date)) {
		return "", "", errJournalDailyDirInvalid
	}
	rel, err := cleanRelativeDir(dailyDir)
	if err != nil {
		return "", "", err
	}
	root, err := filepath.Abs(vault)
	if err != nil {
		return "", "", errJournalVaultMissing
	}
	info, statErr := os.Stat(root)
	if statErr != nil || !info.IsDir() {
		return "", "", errJournalVaultMissing
	}
	noteRel := filepath.Join(rel, date+".md")
	abs := filepath.Join(root, noteRel)
	if !withinDir(root, abs) {
		return "", "", errJournalDailyDirInvalid
	}
	if err := verifySymlinkConfinement(root, abs); err != nil {
		return "", "", err
	}
	return abs, filepath.ToSlash(noteRel), nil
}

// cleanRelativeDir validates a daily-note directory: non-empty, relative, with no
// "..", NUL, or absolute component. It returns the cleaned relative path.
func cleanRelativeDir(dir string) (string, error) {
	d := strings.TrimSpace(dir)
	if d == "" {
		return "", errJournalDailyDirInvalid
	}
	if strings.ContainsRune(d, 0) || filepath.IsAbs(d) {
		return "", errJournalDailyDirInvalid
	}
	cleaned := filepath.Clean(d)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", errJournalDailyDirInvalid
	}
	for _, seg := range strings.Split(cleaned, string(filepath.Separator)) {
		if seg == ".." {
			return "", errJournalDailyDirInvalid
		}
	}
	return cleaned, nil
}

// withinDir reports whether target is root itself or a descendant of root.
func withinDir(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil || filepath.IsAbs(rel) {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// verifySymlinkConfinement resolves symlinks on the vault root and the note's
// nearest existing ancestor, refusing a note whose resolved location escapes the
// resolved vault root.
func verifySymlinkConfinement(root, target string) error {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		realRoot = root
	}
	dir := filepath.Dir(target)
	realDir := resolveExistingAncestor(dir)
	if !withinDir(realRoot, filepath.Join(realDir, filepath.Base(target))) {
		return errJournalDailyDirInvalid
	}
	return nil
}

// resolveExistingAncestor resolves symlinks on the longest existing ancestor of
// path and re-appends the non-existent tail.
func resolveExistingAncestor(path string) string {
	cur := path
	var tail []string
	for {
		if resolved, err := filepath.EvalSymlinks(cur); err == nil {
			return filepath.Join(append([]string{resolved}, reverse(tail)...)...)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return path
		}
		tail = append(tail, filepath.Base(cur))
		cur = parent
	}
}

func reverse(s []string) []string {
	out := make([]string, len(s))
	for i, v := range s {
		out[len(s)-1-i] = v
	}
	return out
}

// --- note parsing -----------------------------------------------------------

// journalParsed is the result of parsing one note.
type journalParsed struct {
	items                []journalItem
	frontmatterDate      string
	frontmatterProjects  []string
	malformedManaged     bool
	boilerplateExcluded  int
	metricsExcluded      int
	invalidExplicitIDs   int
	duplicateExplicitIDs []string
}

// journalHeading is one entry of the heading ancestry stack.
type journalHeading struct {
	level int
	norm  string
	text  string
}

// journalItem is one parsed narrative bullet.
type journalItem struct {
	Section          string
	HeadingPath      string
	CanonicalSection string
	Text             string
	FirstLine        string
	StatedDate       string
	Clock            string
	ProjectLinks     []string
	ExplicitID       string
	LineStart        int
	LineEnd          int
}

var (
	journalHeadingPattern   = regexp.MustCompile(`^(#{1,6})\s+(.*\S)\s*$`)
	journalBulletPattern    = regexp.MustCompile(`^(\s*)([-*+])\s+(.*)$`)
	journalCheckboxPattern  = regexp.MustCompile(`^\[[ xX]\]\s*`)
	journalWikiLinkPattern  = regexp.MustCompile(`\[\[([^\]|]+)(?:\|[^\]]*)?\]\]`)
	journalDateInText       = regexp.MustCompile(`\b(\d{4}-\d{2}-\d{2})\b`)
	journalClockInText      = regexp.MustCompile(`\b([01]?\d|2[0-3]):([0-5]\d)\b`)
	journalExplicitIDMarker = regexp.MustCompile(`<!--\s*evidence_id:\s*([^\s>]+)\s*-->\s*$`)
	journalFencePattern     = regexp.MustCompile("^\\s*(```|~~~)")
	journalNumericToken     = regexp.MustCompile(`^[\d.,:$%+\-/()]+$`)
	// journalGeneratedTimesheetPattern matches ONLY the exact generated section
	// heading shape internal/export writes ("## Timesheet — <date>"), so an
	// arbitrary heading that merely contains "timesheet" is never treated as a
	// generated section.
	journalGeneratedTimesheetPattern = regexp.MustCompile(`^Timesheet\s*[—–-]\s*\d{4}-\d{2}-\d{2}\s*$`)
)

// journalManagedBegin/End are the EXACT internal/export managed-section markers.
// They are duplicated here (not imported) to avoid an evidence/export/workflow
// import cycle; journal_test.go asserts they match export.MarkerBegin/MarkerEnd.
const (
	journalManagedBegin = "<!-- timesheet:begin v1 -->"
	journalManagedEnd   = "<!-- timesheet:end -->"
)

// journalBoilerplateItems is a NARROW, FIXED set of whole-item authority/safety
// boilerplate strings that are never narrative evidence. Matching is exact on the
// trimmed item text, so a substantive item is never excluded. The exact vault
// strings must be confirmed by the owner against the real note; this set is
// deliberately small.
var journalBoilerplateItems = []string{
	"Do not share confidential information.",
	"Follow the safety guidelines.",
}

// journalMetricUnits is the BOUNDED unit vocabulary for metrics-only detection.
// A metrics-only item contains a number and only unit/filler tokens (plus numeric
// separators); any other word (e.g. "reviewed", "benchmark", "shipped") makes it
// substantive and it is RETAINED.
var journalMetricUnits = map[string]bool{
	"token": true, "tokens": true, "tok": true,
	"ms": true, "s": true, "sec": true, "secs": true, "second": true, "seconds": true,
	"min": true, "mins": true, "minute": true, "minutes": true,
	"h": true, "hr": true, "hrs": true, "hour": true, "hours": true, "m": true,
	"call": true, "calls": true,
	"package": true, "packages": true, "pkg": true, "pkgs": true,
	"test": true, "tests": true, "run": true, "runs": true,
	"file": true, "files": true,
	"percent": true, "usd": true, "cost": true, "$": true,
}

// journalMetricFillers is a BOUNDED filler vocabulary that may appear in a
// metrics-only line (e.g. "1234 tokens used", "12 files and 3 runs").
var journalMetricFillers = map[string]bool{
	"used": true, "use": true, "total": true, "totals": true, "spent": true,
	"spend": true, "approx": true, "about": true, "avg": true, "average": true,
	"per": true, "count": true, "counts": true, "remaining": true, "left": true,
	"elapsed": true, "and": true, "of": true, "the": true, "a": true, "to": true,
	"in": true, "with": true, "for": true,
}

// parseJournalNote parses the note into narrative items and warnings. It never
// echoes raw note text.
func parseJournalNote(data []byte) journalParsed {
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	var out journalParsed

	fm, bodyStart := parseJournalFrontmatter(lines)
	out.frontmatterDate = stripScalarQuotes(firstValue(fm["date"]))
	out.frontmatterProjects = extractProjectNames(fm["focus_projects"])

	var stack []journalHeading

	inFence := false
	inManaged := false
	explicitSeen := map[string]bool{}

	i := bodyStart
	for i < len(lines) {
		line := lines[i]

		if journalFencePattern.MatchString(line) {
			inFence = !inFence
			i++
			continue
		}
		if inFence {
			i++
			continue
		}

		if strings.Contains(line, journalManagedBegin) {
			inManaged = true
			i++
			continue
		}
		if strings.Contains(line, journalManagedEnd) {
			inManaged = false
			i++
			continue
		}
		if inManaged {
			i++
			continue
		}

		if m := journalHeadingPattern.FindStringSubmatch(line); m != nil {
			level := len(m[1])
			text := strings.TrimSpace(m[2])
			for len(stack) > 0 && stack[len(stack)-1].level >= level {
				stack = stack[:len(stack)-1]
			}
			stack = append(stack, journalHeading{level: level, norm: normalizeHeading(text), text: text})
			i++
			continue
		}

		if m := journalBulletPattern.FindStringSubmatch(line); m != nil {
			indent := len(m[1])
			body := m[3]
			if journalCheckboxPattern.MatchString(body) {
				// A checkbox task is skipped even when checked.
				i++
				continue
			}
			startLine := i + 1
			text, endIdx := gatherJournalItemText(lines, i, indent)
			family, headingPath, canonicalAncestry, recognized, skip := classifyJournalHeadings(stack)
			if recognized && !skip {
				item := newJournalItem(body, text, headingPath, family, canonicalAncestry, startLine, endIdx+1)
				if item.ExplicitID != "" && !evidence.ValidJournalExplicitID(item.ExplicitID) {
					out.invalidExplicitIDs++
					item.ExplicitID = ""
				}
				if isJournalBoilerplate(item.Text) {
					out.boilerplateExcluded++
				} else if isMetricsOnly(item.Text) {
					out.metricsExcluded++
				} else {
					if item.ExplicitID != "" {
						if explicitSeen[item.ExplicitID] {
							out.duplicateExplicitIDs = append(out.duplicateExplicitIDs, item.ExplicitID)
						} else {
							explicitSeen[item.ExplicitID] = true
						}
					}
					out.items = append(out.items, item)
				}
			}
			i = endIdx + 1
			continue
		}

		i++
	}
	if inManaged {
		out.malformedManaged = true
	}
	return out
}

// gatherJournalItemText collects a bullet's first line plus its continuation
// lines (more indented, non-bullet/heading/fence/marker), returning the joined
// text and the index of the last consumed line.
func gatherJournalItemText(lines []string, start, indent int) (string, int) {
	parts := []string{bulletBody(lines[start])}
	last := start
	var cont []string
	for j := start + 1; j < len(lines); j++ {
		line := lines[j]
		if strings.TrimSpace(line) == "" {
			// A blank line ends the item (do not absorb a following paragraph).
			break
		}
		if journalFencePattern.MatchString(line) || journalHeadingPattern.MatchString(line) {
			break
		}
		if journalBulletPattern.MatchString(line) {
			break
		}
		if strings.Contains(line, journalManagedBegin) || strings.Contains(line, journalManagedEnd) {
			break
		}
		if leadingSpaces(line) <= indent {
			break
		}
		cont = append(cont, line)
		last = j
	}
	if len(cont) > 0 {
		common := minIndent(cont)
		for _, c := range cont {
			parts = append(parts, strings.TrimRight(c[minInt(len(c), common):], " \t"))
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n")), last
}

// bulletBody returns the text after the bullet marker on a line.
func bulletBody(line string) string {
	m := journalBulletPattern.FindStringSubmatch(line)
	if m == nil {
		return strings.TrimSpace(line)
	}
	return strings.TrimSpace(m[3])
}

// newJournalItem builds a parsed item, extracting stated date/clock, inline
// project links, and a trailing explicit id marker. Section is the narrative
// family (display); CanonicalSection is the normalized full heading ancestry used
// as the implicit-identity disambiguator.
func newJournalItem(body, text, headingPath, family, canonicalAncestry string, lineStart, lineEnd int) journalItem {
	item := journalItem{
		Section:          family,
		HeadingPath:      headingPath,
		CanonicalSection: canonicalAncestry,
		Text:             text,
		FirstLine:        strings.TrimSpace(body),
		LineStart:        lineStart,
		LineEnd:          lineEnd,
		ProjectLinks:     extractInlineProjects(text),
	}
	// Explicit id: a trailing HTML-comment marker on the item's LAST line.
	lastLineStart := strings.LastIndex(text, "\n") + 1
	lastLine := text[lastLineStart:]
	if m := journalExplicitIDMarker.FindStringSubmatch(lastLine); m != nil {
		item.ExplicitID = strings.TrimSpace(m[1])
		cleaned := strings.TrimSpace(text[:lastLineStart] + journalExplicitIDMarker.ReplaceAllString(lastLine, ""))
		item.Text = cleaned
		if idx := strings.Index(cleaned, "\n"); idx >= 0 {
			item.FirstLine = strings.TrimSpace(cleaned[:idx])
		} else {
			item.FirstLine = strings.TrimSpace(cleaned)
		}
	}
	if m := journalDateInText.FindStringSubmatch(item.Text); m != nil {
		item.StatedDate = m[1]
	}
	if m := journalClockInText.FindStringSubmatch(item.Text); m != nil {
		item.Clock = padClock(m[1], m[2])
	}
	return item
}

// classifyJournalHeadings inspects the heading stack for the current bullet: a
// skip section (priorities/standup/todo/agent session, or an exact
// legacy/generated timesheet heading) wins, otherwise the deepest recognized
// narrative heading supplies the section FAMILY. The returned canonicalAncestry is
// the NORMALIZED FULL heading ancestry (joined by " / "), used as the
// implicit-identity disambiguator so two different headings that merely share a
// recognized phrase, and nested sections, never collapse to the same identity.
func classifyJournalHeadings(stack []journalHeading) (family, headingPath, canonicalAncestry string, recognized, skip bool) {
	for _, h := range stack {
		// Skip phrases apply to SECTION headings (level >= 2), never to a level-1
		// note title: an arbitrary title that merely contains a skip word must not
		// silently empty the whole note.
		if h.level < 2 {
			continue
		}
		if _, _, isSkip := classifyHeading(h.text, h.norm); isSkip {
			return "", "", "", false, true
		}
	}
	for i := len(stack) - 1; i >= 0; i-- {
		if c, rec, _ := classifyHeading(stack[i].text, stack[i].norm); rec {
			family = c
			break
		}
	}
	if family == "" {
		return "", "", "", false, false
	}
	raw := make([]string, 0, len(stack))
	canon := make([]string, 0, len(stack))
	for _, h := range stack {
		raw = append(raw, h.text)
		canon = append(canon, h.norm)
	}
	return family, strings.Join(raw, " / "), strings.Join(canon, " / "), true, false
}

var journalNarrativePhrases = []string{"work journal", "quick capture", "daily impact"}

// classifyHeading maps a heading (raw text and its normalized form) to a
// narrative family, a skip section, or neither. The generated/legacy timesheet
// skip matches ONLY precise shapes: the exact "clustered timesheet" heading or the
// exact generated "Timesheet — <date>" heading. An arbitrary heading that merely
// CONTAINS "timesheet" (e.g. an H1 note title) is never skipped.
//
// The token skips are CONTAINS matches on the NORMALIZED (lowercased, undecorated)
// heading text, which is what makes them case-insensitive and decoration-proof.
// `agent session` is one of them so an agent's activity can never be read as the
// owner's working time, including when the heading is nested inside a collected
// section: see docs/journal-collection.md.
func classifyHeading(raw, norm string) (canonical string, recognized, skip bool) {
	if norm == "" {
		return "", false, false
	}
	if norm == "clustered timesheet" || journalGeneratedTimesheetPattern.MatchString(strings.TrimSpace(raw)) {
		return "", false, true
	}
	for _, p := range []string{"priorities", "standup", "todo", "agent session"} {
		if norm == p || strings.Contains(norm, p) {
			return "", false, true
		}
	}
	for _, p := range journalNarrativePhrases {
		if norm == p || strings.Contains(norm, p) {
			return p, true, false
		}
	}
	return "", false, false
}

// stripScalarQuotes removes surrounding single/double quotes from a YAML scalar so
// a quoted frontmatter date ("2026-10-08") compares equal to the target date. The
// raw value is never rewritten in the note.
func stripScalarQuotes(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return strings.TrimSpace(s[1 : len(s)-1])
		}
	}
	return s
}

// normalizeHeading lowercases and strips markdown/emoji decoration from a heading
// so a decorative heading ("## 🗓️ Work journal") still matches.
func normalizeHeading(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	for _, r := range s {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r):
			b.WriteRune(r)
		case r == ' ' || r == '(' || r == ')' || r == '&' || r == '/' || r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune(' ')
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// parseJournalFrontmatter reads a leading `---` frontmatter block into a
// key->[]values map and returns the index of the first body line.
func parseJournalFrontmatter(lines []string) (map[string][]string, int) {
	fm := map[string][]string{}
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return fm, 0
	}
	lastKey := ""
	for i := 1; i < len(lines); i++ {
		line := lines[i]
		if strings.TrimSpace(line) == "---" {
			return fm, i + 1
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "- ") {
			if lastKey != "" {
				fm[lastKey] = append(fm[lastKey], strings.TrimSpace(strings.TrimPrefix(trimmed, "- ")))
			}
			continue
		}
		if idx := strings.Index(line, ":"); idx >= 0 {
			key := strings.TrimSpace(line[:idx])
			val := strings.TrimSpace(line[idx+1:])
			lastKey = key
			if val != "" {
				fm[key] = append(fm[key], val)
			}
		}
	}
	return fm, len(lines)
}

// extractProjectNames turns quoted wikilink/plain values into project names.
func extractProjectNames(values []string) []string {
	var out []string
	for _, v := range values {
		v = strings.TrimSpace(v)
		if m := journalWikiLinkPattern.FindStringSubmatch(v); m != nil {
			out = append(out, strings.TrimSpace(m[1]))
			continue
		}
		v = strings.Trim(v, `"'`)
		if v != "" {
			out = append(out, strings.TrimSpace(v))
		}
	}
	return dedupSorted(out)
}

// extractInlineProjects returns the deduped, sorted wikilink targets in text.
func extractInlineProjects(text string) []string {
	var out []string
	for _, m := range journalWikiLinkPattern.FindAllStringSubmatch(text, -1) {
		out = append(out, strings.TrimSpace(m[1]))
	}
	return dedupSorted(out)
}

// buildJournalEvidence constructs one UNTIMED journal evidence item.
func buildJournalEvidence(item journalItem, sourceID, targetDate, notePath, statedDate string, frontmatterProjects []string, observed time.Time) evidence.Evidence {
	hints := dedupSorted(append(append([]string{}, frontmatterProjects...), item.ProjectLinks...))
	contentHash := hashNormalizedBullet(item.Text)
	id := evidence.JournalID(sourceID, targetDate, item.ExplicitID, item.CanonicalSection, contentHash)
	return evidence.Evidence{
		ID:           id,
		Kind:         evidence.SourceJournal,
		Title:        item.FirstLine,
		Description:  item.Text,
		Duration:     evidence.UnknownDuration(),
		ProjectHints: hints,
		SourceRefs: []evidence.SourceRef{{
			Kind:         evidence.SourceJournal,
			SourceID:     sourceID,
			NotePath:     notePath,
			HeadingPath:  item.HeadingPath,
			Section:      item.Section,
			LineStart:    item.LineStart,
			LineEnd:      item.LineEnd,
			StatedDate:   statedDate,
			Clock:        item.Clock,
			ProjectLinks: item.ProjectLinks,
		}},
		ObservedAt: observed,
	}
}

// hashNormalizedBullet hashes a normalized bullet text: whitespace collapsed and
// trimmed, so reordering or reindenting an unchanged item keeps its identity.
func hashNormalizedBullet(text string) string {
	norm := strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
	sum := sha256.Sum256([]byte(norm))
	return hex.EncodeToString(sum[:16])
}

// isJournalBoilerplate reports whether the whole trimmed item text exactly
// matches a fixed authority/safety boilerplate string.
func isJournalBoilerplate(text string) bool {
	t := strings.TrimSpace(text)
	for _, b := range journalBoilerplateItems {
		if t == b {
			return true
		}
	}
	return false
}

// isMetricsOnly reports whether an item is ONLY a bounded metrics entry: it
// contains a number and every token is a numeric token, a bounded metric unit, or
// a bounded metric filler (e.g. "1234 tokens", "12 files and 3 runs", "$0.42 ·
// 12m"). Any other word ("reviewed", "benchmark", "shipped") makes the item
// substantive and it is RETAINED, so real prose is never removed.
func isMetricsOnly(text string) bool {
	t := strings.TrimSpace(text)
	if t == "" {
		return false
	}
	fields := strings.FieldsFunc(t, func(r rune) bool {
		return unicode.IsSpace(r) || r == '·' || r == '|' || r == ',' || r == ';' || r == '/'
	})
	if len(fields) == 0 {
		return false
	}
	hasNumber := false
	for _, f := range fields {
		tok := strings.Trim(f, ".,:%()-\u2013\u2014+")
		if tok == "" {
			continue
		}
		num, metric := classifyMetricToken(tok)
		if !metric {
			return false
		}
		if num {
			hasNumber = true
		}
	}
	return hasNumber
}

// classifyMetricToken reports whether a token is a numeric token (isNumber) and
// whether it is any metric token at all (number, bounded unit, or bounded filler,
// including a numeric token with a unit suffix, e.g. "12m", "500ms").
func classifyMetricToken(tok string) (isNumber, isMetric bool) {
	if journalNumericToken.MatchString(tok) && strings.ContainsAny(tok, "0123456789") {
		return true, true
	}
	low := strings.ToLower(tok)
	for _, u := range journalMetricUnitSuffixes {
		if strings.HasSuffix(low, u) && len(low) > len(u) {
			prefix := low[:len(low)-len(u)]
			if journalNumericToken.MatchString(prefix) && strings.ContainsAny(prefix, "0123456789") {
				return true, true
			}
		}
	}
	if journalMetricUnits[low] || journalMetricFillers[low] {
		return false, true
	}
	return false, false
}

// journalMetricUnitSuffixes is the suffix set used to recognize a number with a
// unit (e.g. "12m", "3h", "500ms"). Longer suffixes are tried first.
var journalMetricUnitSuffixes = []string{"tokens", "packages", "percent", "files", "tests", "runs", "calls", "secs", "mins", "hrs", "sec", "min", "ms", "hr", "m", "h", "s"}

// --- small helpers ----------------------------------------------------------

func firstValue(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return strings.TrimSpace(values[0])
}

func dedupSorted(values []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

func padClock(h, m string) string {
	if len(h) == 1 {
		h = "0" + h
	}
	return h + ":" + m
}

func leadingSpaces(s string) int {
	n := 0
	for n < len(s) && (s[n] == ' ' || s[n] == '\t') {
		n++
	}
	return n
}

func minIndent(lines []string) int {
	min := -1
	for _, l := range lines {
		ind := leadingSpaces(l)
		if min == -1 || ind < min {
			min = ind
		}
	}
	if min < 0 {
		return 0
	}
	return min
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
