// Command vaultgen renders the read-only vault mirror of this repository's
// orchestration status: a SET of three generated files, each with its own
// independent stamp and its own per-file hand-edit refusal.
//
// It reads three repository sources:
//
//   - docs/orchestration/status.yaml  the hand-maintained machine-readable manifest
//   - docs/lessons-learned.md         the curated lessons list
//   - docs/adr/*.md                   frontmatter only: id, status, date, title
//
// and writes exactly these three files inside the vault:
//
//   - <vault>/01 - Projects/assignment-1 (generated).md
//     the project summary: milestone table, links, ADR index. NO lessons.
//   - <vault>/06 - Knowledge & Wiki/assignment-1 lessons (generated).md
//     the lessons digest, moved out of the project summary.
//   - <vault>/01 - Projects/assignment-1 (generated index).md
//     the set's freshness at a glance: every generated file with its path, its
//     source, its source_commit and its freshness.
//
// Each is a NEW file beside an authored note, which is NEVER touched. The
// generator writes nowhere else: not the authored project note, not the vault
// ADRs, not the dashboard, not a daily note, and not any repository file.
//
// Standard library only. Every generated BODY is deterministic: no wall-clock
// value appears in any body, so only the header's three wall-clock fields
// (generated_at, source_date, and the do-not-edit comment's date) vary between
// runs, and a body changes only when its source changes. --check excludes exactly
// those three fields, and does so only inside the header, so a file generated on
// one day still checks OK on the next.
//
// Usage:
//
//	vaultgen --write [--vault DIR]   regenerate the whole set, refusing to clobber a hand edit
//	vaultgen --check [--vault DIR]   verify the whole set without writing
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Repository-relative source paths and the vault paths this tool owns.
const (
	statusPath  = "docs/orchestration/status.yaml"
	lessonsPath = "docs/lessons-learned.md"
	adrDirPath  = "docs/adr"
	ledgerPath  = "docs/orchestration/state.md"
	specPath    = "assignment-1-personal-pilot-timesheet.md"
	repoID      = "github.com/huyle-gradion/personal-pilot-timesheet-logger"

	// The three generated vault files. Each carries its OWN independent stamp
	// and its OWN per-file hand-edit refusal.
	projectRel = "01 - Projects/assignment-1 (generated).md"
	lessonsRel = "06 - Knowledge & Wiki/assignment-1 lessons (generated).md"
	indexRel   = "01 - Projects/assignment-1 (generated index).md"

	// indexSource is a synthetic, non-path marker. The index is derived from the
	// generated set's own stamps rather than from one repository source, so it
	// deliberately does not pretend to be a repository path.
	indexSource = "(generated set)"

	generatorID = "scripts/vaultgen"
)

// The freshness vocabulary. Exactly these four values appear in the index's
// freshness column, and each is derived from a file's STAMP FACTS alone.
const (
	freshnessOK         = "OK"
	freshnessStale      = "STALE"
	freshnessHandEdited = "HAND-EDITED"
	freshnessUnreadable = "UNREADABLE STAMP"
)

// The closed vocabularies the manifest must use. A typo fails the generator
// loudly instead of producing a plausible-looking wrong row.
var (
	validKinds  = map[string]bool{"slice": true, "merge": true, "decision": true}
	validStates = map[string]bool{"planned": true, "implemented": true, "source-accepted": true, "merged": true, "blocked": true}
)

// artifact identifies one generated vault file and the repository source it is
// derived from. indexSource marks the index, which is derived from the generated
// set's stamps rather than from a repository path.
type artifact struct {
	rel    string
	source string
}

// generatedSet is the whole set the generator owns, in listing order. The order
// is fixed and deterministic; the index lists exactly these paths in this order.
var generatedSet = []artifact{
	{rel: projectRel, source: statusPath},
	{rel: lessonsRel, source: lessonsPath},
	{rel: indexRel, source: indexSource},
}

// Item is one manifest entry.
type Item struct {
	ID      string
	Kind    string
	State   string
	Commits []string
	PR      *int
	Issues  []int
	Date    string
	OneLine string
}

// ADR is one architecture decision record, read from frontmatter and heading.
type ADR struct {
	ID     string
	Title  string
	Status string
	Date   string
}

// sources is the parsed repository input, read once per run.
type sources struct {
	items   []Item
	lessons []string
	adrs    []ADR
}

func main() {
	fs := flag.NewFlagSet("vaultgen", flag.ExitOnError)
	write := fs.Bool("write", false, "regenerate the generated vault set (refuses to clobber a hand edit)")
	check := fs.Bool("check", false, "verify the generated vault set without writing")
	vault := fs.String("vault", defaultVaultRoot(), "vault root directory")
	fs.Parse(os.Args[1:])

	if *write == *check {
		fmt.Fprintln(os.Stderr, "usage: vaultgen --write|--check [--vault DIR]")
		os.Exit(2)
	}
	if err := validateVaultRoot(*vault); err != nil {
		fmt.Fprintf(os.Stderr, "usage: vaultgen --write|--check --vault DIR: %v\n", err)
		os.Exit(2)
	}

	repoRoot, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "vaultgen: cannot determine repository root: %v\n", err)
		os.Exit(2)
	}
	commit, err := headCommit(repoRoot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "vaultgen: cannot resolve source commit: %v\n", err)
		os.Exit(2)
	}

	if *write {
		os.Exit(runWrite(repoRoot, *vault, time.Now(), commit, os.Stdout))
	}
	os.Exit(runCheck(repoRoot, *vault, time.Now(), commit, os.Stdout))
}

func defaultVaultRoot() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "ai-database"
	}
	return filepath.Join(home, "ai-database")
}

// headCommit resolves the short SHA every stamp names as its source.
func headCommit(repoRoot string) (string, error) {
	out, err := exec.Command("git", "-C", repoRoot, "rev-parse", "--short", "HEAD").Output()
	if err != nil {
		return "", err
	}
	sha := strings.TrimSpace(string(out))
	if sha == "" {
		return "", errors.New("git rev-parse returned an empty SHA")
	}
	return sha, nil
}

// readSources reads and parses every repository input once.
func readSources(repoRoot string) (sources, error) {
	statusData, err := os.ReadFile(filepath.Join(repoRoot, statusPath))
	if err != nil {
		return sources{}, fmt.Errorf("read %s: %w", statusPath, err)
	}
	items, err := parseStatus(statusData)
	if err != nil {
		return sources{}, fmt.Errorf("%s: %w", statusPath, err)
	}

	lessonsData, err := os.ReadFile(filepath.Join(repoRoot, lessonsPath))
	if err != nil {
		return sources{}, fmt.Errorf("read %s: %w", lessonsPath, err)
	}

	adrs, err := loadADRs(filepath.Join(repoRoot, adrDirPath))
	if err != nil {
		return sources{}, fmt.Errorf("read %s: %w", adrDirPath, err)
	}

	return sources{items: items, lessons: parseLessons(lessonsData), adrs: adrs}, nil
}

// expectedSet builds all three files in memory: the header/frontmatter stamp plus
// the deterministic body, keyed by vault-relative path. The index's freshness
// column is rendered from the facts it is handed, so --write (fresh facts) and
// --check (on-disk facts) share one renderer.
func expectedSet(s sources, now time.Time, commit string, facts []fileFact) map[string]string {
	project := renderProjectBody(s.items, s.adrs, commit)
	lessons := renderLessonsBody(s.lessons, commit)
	index := renderIndexBody(facts)
	return map[string]string{
		projectRel: renderContent(now, commit, statusPath, project),
		lessonsRel: renderContent(now, commit, lessonsPath, lessons),
		indexRel:   renderContent(now, commit, indexSource, index),
	}
}

// renderContent builds one whole file: the stamp plus the body.
func renderContent(now time.Time, commit, source, body string) string {
	return renderHeader(now, commit, source, sha256Hex(body)) + body
}

// renderHeader builds the do-not-edit comment and the YAML frontmatter block.
// Everything here is header; body_sha256 covers only what follows the closing
// frontmatter delimiter.
func renderHeader(now time.Time, commit, source, bodySHA string) string {
	date := now.Format("2006-01-02")
	var b strings.Builder
	b.WriteString("<!--\n")
	b.WriteString("DO NOT EDIT — this file is generated and will be overwritten.\n")
	fmt.Fprintf(&b, "source: %s\n", source)
	fmt.Fprintf(&b, "source_commit: %s\n", commit)
	fmt.Fprintf(&b, "date: %s\n", date)
	fmt.Fprintf(&b, "regenerate: ./scripts/sync-vault.sh --write\n")
	b.WriteString("-->\n")
	b.WriteString("---\n")
	b.WriteString("generated: true\n")
	fmt.Fprintf(&b, "generator: %s\n", generatorID)
	fmt.Fprintf(&b, "source: %s\n", source)
	fmt.Fprintf(&b, "source_commit: %s\n", commit)
	fmt.Fprintf(&b, "source_date: %s\n", date)
	fmt.Fprintf(&b, "generated_at: %s\n", now.UTC().Format(time.RFC3339))
	fmt.Fprintf(&b, "body_sha256: %s\n", bodySHA)
	b.WriteString("---\n")
	return b.String()
}

// renderProjectBody builds the project summary body: status line, milestone
// table, links and ADR index. It carries NO lessons section — those moved to the
// lessons file (lessonsRel). It contains no wall-clock value.
func renderProjectBody(items []Item, adrs []ADR, commit string) string {
	var b strings.Builder
	b.WriteString("\n# assignment-1 (generated)\n\n")
	fmt.Fprintf(&b, "Status: generated from `%s` at commit `%s`; %d tracked items.\n",
		statusPath, commit, len(items))

	b.WriteString("\n## Milestones\n\n")
	b.WriteString("| id | state | date | commits | PR | issues | one line |\n")
	b.WriteString("| --- | --- | --- | --- | --- | --- | --- |\n")
	for _, it := range items {
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s | %s |\n",
			esc(it.ID), esc(it.State), esc(it.Date), commitsCell(it.Commits),
			prCell(it.PR), issuesCell(it.Issues), esc(it.OneLine))
	}

	b.WriteString("\n## Links\n\n")
	fmt.Fprintf(&b, "- Repository: `%s`\n", repoID)
	fmt.Fprintf(&b, "- Specification: `%s`\n", specPath)
	fmt.Fprintf(&b, "- Architecture decisions: `%s/`\n", adrDirPath)
	fmt.Fprintf(&b, "- Orchestration ledger: `%s`\n", ledgerPath)

	b.WriteString("\n## Architecture decisions\n\n")
	if len(adrs) == 0 {
		b.WriteString("- none\n")
	}
	for _, a := range adrs {
		fmt.Fprintf(&b, "- %s (%s, %s) — %s\n", a.ID, esc(a.Status), esc(a.Date), esc(a.Title))
	}
	return b.String()
}

// renderLessonsBody builds the lessons digest body from the curated lessons
// source. It carries no wall-clock value.
func renderLessonsBody(lessons []string, commit string) string {
	var b strings.Builder
	b.WriteString("\n# assignment-1 lessons (generated)\n\n")
	fmt.Fprintf(&b, "Status: generated from `%s` at commit `%s`; %d lessons.\n",
		lessonsPath, commit, len(lessons))

	b.WriteString("\n## Lessons\n\n")
	if len(lessons) == 0 {
		b.WriteString("- none\n")
	}
	for _, l := range lessons {
		fmt.Fprintf(&b, "- %s\n", l)
	}
	return b.String()
}

// renderIndexBody builds the freshness index body: one row per generated file
// with its path, source, source_commit and freshness. It is a pure function of
// the facts it is handed, carries no wall-clock value, and names exactly the
// generated paths — never a path that is not generated, and never a commit other
// than one a file's own stamp already names (or "—" when there is none).
func renderIndexBody(facts []fileFact) string {
	var b strings.Builder
	b.WriteString("\n# assignment-1 generated index\n\n")
	fmt.Fprintf(&b, "The generated set has %d files. Freshness comes from each file's own stamp: `%s` when its `body_sha256` matches its body and its `source_commit` is the current commit, `%s` when its `body_sha256` does not match its own body, `%s` when its `source_commit` is not the current commit, and `%s` when its stamp cannot be parsed.\n",
		len(facts), freshnessOK, freshnessHandEdited, freshnessStale, freshnessUnreadable)
	b.WriteString("\n| path | source | source_commit | freshness |\n")
	b.WriteString("| --- | --- | --- | --- |\n")
	for _, f := range facts {
		sc := f.sourceCommit
		if sc == "" {
			sc = "—"
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s |\n", esc(f.rel), esc(f.source), esc(sc), esc(f.freshness))
	}
	return b.String()
}

// fileFact is one generated file's stamp facts as the index reports them.
type fileFact struct {
	rel          string
	source       string
	sourceCommit string // the commit the file's own stamp names, or "" if unreadable
	freshness    string // OK / STALE / HAND-EDITED / UNREADABLE STAMP
}

// writtenFacts is the set's facts as a --write is about to produce them: every
// file freshly stamped at commit, so every row is OK.
func writtenFacts(commit string) []fileFact {
	facts := make([]fileFact, 0, len(generatedSet))
	for _, a := range generatedSet {
		facts = append(facts, fileFact{rel: a.rel, source: a.source, sourceCommit: commit, freshness: freshnessOK})
	}
	return facts
}

// diskFacts derives each file's freshness from its STAMP FACTS alone, reading the
// on-disk content (contents maps vault-relative path to content; an absent key is
// a missing file). The index is rendered from these facts under --check, so it
// reports the vault's real state rather than a remembered one.
func diskFacts(contents map[string]string, commit string) []fileFact {
	facts := make([]fileFact, 0, len(generatedSet))
	for _, a := range generatedSet {
		f := fileFact{rel: a.rel, source: a.source}
		if content, ok := contents[a.rel]; ok {
			f.sourceCommit, f.freshness = stampFacts(content, commit)
		} else {
			f.freshness = freshnessUnreadable
		}
		facts = append(facts, f)
	}
	return facts
}

// stampFacts reads one file's stamp and classifies it using ONLY that stamp's
// facts. It returns the source_commit the stamp names (empty when the stamp is
// unreadable) and the freshness value.
func stampFacts(content, commit string) (sourceCommit, freshness string) {
	_, body, ok := splitHeader(content)
	if !ok {
		return "", freshnessUnreadable
	}
	declared := frontmatterField(content, "body_sha256")
	sc := frontmatterField(content, "source_commit")
	if !hex64.MatchString(declared) || sc == "" {
		return sc, freshnessUnreadable
	}
	if sha256Hex(body) != declared {
		return sc, freshnessHandEdited
	}
	if sc != commit {
		return sc, freshnessStale
	}
	return sc, freshnessOK
}

// delimIndices locates the frontmatter's opening and closing "---" lines. It
// skips any leading do-not-edit HTML comment, so the delimiters it returns are
// always the real frontmatter pair.
func delimIndices(content string) (open, close int, ok bool) {
	lines := strings.SplitAfter(content, "\n")
	open = -1
	for i, l := range lines {
		if strings.TrimRight(l, "\n") == "---" {
			open = i
			break
		}
	}
	if open < 0 {
		return 0, 0, false
	}
	for j := open + 1; j < len(lines); j++ {
		if strings.TrimRight(lines[j], "\n") == "---" {
			return open, j, true
		}
	}
	return 0, 0, false
}

// splitHeader separates the leading header/frontmatter block from the body.
// The body is everything after the frontmatter's closing "---" line, and is
// exactly what body_sha256 hashes.
func splitHeader(content string) (header, body string, ok bool) {
	lines := strings.SplitAfter(content, "\n")
	_, close, ok := delimIndices(content)
	if !ok {
		return "", "", false
	}
	return strings.Join(lines[:close+1], ""), strings.Join(lines[close+1:], ""), true
}

// frontmatterBlock returns ONLY the text between the two "---" delimiter lines,
// excluding the delimiters themselves and any leading do-not-edit HTML comment.
// The comment is deliberately outside the block: a body_sha256 (or any other
// stamp) placed in the comment must never be read as the real stamp.
func frontmatterBlock(content string) (string, bool) {
	lines := strings.SplitAfter(content, "\n")
	open, close, ok := delimIndices(content)
	if !ok {
		return "", false
	}
	return strings.Join(lines[open+1:close], ""), true
}

// hex64 is the exact shape a body_sha256 must have: 64 lowercase hex chars.
var hex64 = regexp.MustCompile(`^[0-9a-f]{64}$`)

// verifyIntact reports whether a file's body still matches the body_sha256 its
// own frontmatter declares. A mismatch means the file was HAND-EDITED. A stamp
// that is absent or not exactly 64 lowercase hex characters is treated as an
// unreadable stamp and refused, never trusted.
func verifyIntact(content string) error {
	_, body, ok := splitHeader(content)
	if !ok {
		return errors.New("no header/frontmatter block found")
	}
	declared := frontmatterField(content, "body_sha256")
	if declared == "" {
		return errors.New("no body_sha256 declared in the frontmatter")
	}
	if !hex64.MatchString(declared) {
		return fmt.Errorf("unreadable stamp: body_sha256 %q is not 64 lowercase hex characters", declared)
	}
	got := sha256Hex(body)
	if got != declared {
		return fmt.Errorf("hand edit detected: body hashes to %s but the frontmatter declares %s", got, declared)
	}
	return nil
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// frontmatterField reads a scalar from the frontmatter block BETWEEN the two
// "---" delimiters only. The leading do-not-edit comment is ignored, so a
// stamp hidden in the comment cannot shadow the real one.
func frontmatterField(content, key string) string {
	block, ok := frontmatterBlock(content)
	if !ok {
		return ""
	}
	for _, l := range strings.Split(block, "\n") {
		if k, v, ok := splitKV(strings.TrimSpace(l)); ok && k == key {
			return unquote(v)
		}
	}
	return ""
}

// normalizeForCompare blanks the wall-clock fields that legitimately vary
// between runs so a check does not report a false difference. Exactly three
// fields are excluded, and each is blanked only inside the HEADER block:
//
//   - generated_at:   the frontmatter's exact run timestamp (RFC3339)
//   - source_date:    the frontmatter's generation date
//   - date:           the do-not-edit comment's generation date (a third
//     wall-clock field, which changes at midnight and would otherwise make a
//     file generated on day D look STALE when checked on day D+1)
//
// The body is NOT normalized: it contains no wall-clock value, and a body line
// that merely begins with one of these keys must stay untouched so a real body
// change is never masked.
func normalizeForCompare(content string) string {
	header, body, ok := splitHeader(content)
	if !ok {
		// No header block: leave the content as-is so a malformed file still
		// compares (and mismatches) rather than normalizing to a false OK.
		return content
	}
	lines := strings.Split(header, "\n")
	for i, l := range lines {
		switch {
		case strings.HasPrefix(l, "generated_at:"):
			lines[i] = "generated_at: <excluded>"
		case strings.HasPrefix(l, "source_date:"):
			lines[i] = "source_date: <excluded>"
		case strings.HasPrefix(l, "date:"):
			lines[i] = "date: <excluded>"
		}
	}
	return strings.Join(lines, "\n") + body
}

// validateVaultRoot rejects a vault root that is empty or relative. An empty
// value would make filepath.Join(vaultRoot, rel) resolve against the process
// working directory — inside the repository after the wrapper's cd — breaking the
// "never writes a repository file" invariant. A relative path is refused for the
// same reason: its meaning depends on the working directory.
func validateVaultRoot(vaultRoot string) error {
	if vaultRoot == "" {
		return errors.New("--vault must not be empty")
	}
	if !filepath.IsAbs(vaultRoot) {
		return fmt.Errorf("--vault must be an absolute path, got %q", vaultRoot)
	}
	return nil
}

// runWrite regenerates the WHOLE set.
//
// WRITE DISCIPLINE, stated plainly because the packet asks for it explicitly:
// this is ALL-OR-NOTHING ON VALIDATION and PER-FILE ATOMIC ON WRITE.
//
//   - All-or-nothing on validation: every target's existing content is checked
//     for intactness (or absence) BEFORE anything is written. If ANY existing
//     file was hand-edited or carries an unreadable stamp, the run refuses with a
//     non-zero exit and writes NOTHING, so one bad file can never take the other
//     two with it.
//   - Per-file atomic on write: each file is written through a temp file in its
//     own directory plus a rename, so no reader ever sees a half-written file.
//     The write of the set is not itself a single transaction: if the process
//     died between two renames, earlier files would already be updated. That is
//     the honest limit, and it is covered by a test that hand-edits one file and
//     asserts all three stay byte-for-byte unchanged.
func runWrite(repoRoot, vaultRoot string, now time.Time, commit string, out io.Writer) int {
	if err := validateVaultRoot(vaultRoot); err != nil {
		fmt.Fprintf(out, "usage: vaultgen --write|--check --vault DIR: %v\n", err)
		return 2
	}
	s, err := readSources(repoRoot)
	if err != nil {
		fmt.Fprintf(out, "ERROR: %v\n", err)
		return 2
	}
	// The index is rendered from the facts this write is about to produce.
	expected := expectedSet(s, now, commit, writtenFacts(commit))

	// Preflight every file before writing any of them.
	for _, a := range generatedSet {
		target := filepath.Join(vaultRoot, a.rel)
		data, err := os.ReadFile(target)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			fmt.Fprintf(out, "ERROR: cannot read %s: %v\n", target, err)
			return 2
		}
		if err := verifyIntact(string(data)); err != nil {
			fmt.Fprintf(out, "REFUSING to write the set: %s was hand-edited (or its stamp is unreadable).\n", a.rel)
			fmt.Fprintf(out, "  %v\n", err)
			fmt.Fprintf(out, "  No file was written. Restore it from its source, or delete it, then run --write again.\n")
			return 1
		}
	}

	// All targets validated: write each one atomically.
	for _, a := range generatedSet {
		target := filepath.Join(vaultRoot, a.rel)
		if err := writeAtomic(target, []byte(expected[a.rel])); err != nil {
			fmt.Fprintf(out, "ERROR: cannot write %s: %v\n", target, err)
			return 2
		}
		fmt.Fprintf(out, "wrote %s\n", a.rel)
	}
	return 0
}

// runCheck regenerates the whole set in memory and compares each file to disk.
// It enumerates EVERY differing file and names the cause for each: STALE (the
// regenerated output differs — its source changed or its stamp names an older
// commit), HAND-EDITED (body_sha256 does not match the file's own body), or
// UNREADABLE STAMP (the stamp cannot be parsed, or the file is missing).
//
// It exits non-zero if any file differs.
func runCheck(repoRoot, vaultRoot string, now time.Time, commit string, out io.Writer) int {
	if err := validateVaultRoot(vaultRoot); err != nil {
		fmt.Fprintf(out, "usage: vaultgen --write|--check --vault DIR: %v\n", err)
		return 2
	}
	s, err := readSources(repoRoot)
	if err != nil {
		fmt.Fprintf(out, "ERROR: %v\n", err)
		return 2
	}

	contents := make(map[string]string, len(generatedSet))
	readErr := make(map[string]error, len(generatedSet))
	for _, a := range generatedSet {
		data, err := os.ReadFile(filepath.Join(vaultRoot, a.rel))
		if err != nil {
			readErr[a.rel] = err
			continue
		}
		contents[a.rel] = string(data)
	}

	// The index is regenerated from the vault's REAL state so it reports fresh
	// freshness, not a remembered one.
	expected := expectedSet(s, now, commit, diskFacts(contents, commit))

	bad := false
	for _, a := range generatedSet {
		disk, ok := contents[a.rel]
		if !ok {
			if os.IsNotExist(readErr[a.rel]) {
				fmt.Fprintf(out, "%s: %s — the file is missing (no stamp to read)\n", freshnessUnreadable, a.rel)
			} else {
				fmt.Fprintf(out, "ERROR: cannot read %s: %v\n", filepath.Join(vaultRoot, a.rel), readErr[a.rel])
			}
			bad = true
			continue
		}
		switch cause := classify(disk, expected[a.rel]); cause {
		case freshnessOK:
			fmt.Fprintf(out, "OK: %s\n", a.rel)
		case freshnessHandEdited:
			fmt.Fprintf(out, "%s: %s — body_sha256 does not match the file's own body\n", cause, a.rel)
			bad = true
		case freshnessUnreadable:
			fmt.Fprintf(out, "%s: %s — the stamp cannot be parsed\n", cause, a.rel)
			bad = true
		default: // freshnessStale
			fmt.Fprintf(out, "%s: %s — regenerated output differs from disk (its source changed since it was generated)\n", cause, a.rel)
			bad = true
		}
	}
	if bad {
		return 1
	}
	return 0
}

// classify names WHY a file differs from what the generator would write now, or
// returns freshnessOK when it matches. The causes are the four freshness values,
// decided in the same order the index uses: an unreadable stamp first, then a
// body_sha256 mismatch, then any other difference (staleness).
func classify(disk, want string) string {
	if _, _, ok := splitHeader(disk); !ok {
		return freshnessUnreadable
	}
	declared := frontmatterField(disk, "body_sha256")
	if !hex64.MatchString(declared) || frontmatterField(disk, "source_commit") == "" {
		return freshnessUnreadable
	}
	if _, body, _ := splitHeader(disk); sha256Hex(body) != declared {
		return freshnessHandEdited
	}
	if normalizeForCompare(disk) != normalizeForCompare(want) {
		return freshnessStale
	}
	return freshnessOK
}

// writeAtomic writes data to path via a temp file in the same directory and a
// rename, so a reader never sees a half-written file.
func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".vaultgen-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// parseStatus parses the restricted YAML subset status.yaml uses: a top-level
// "items:" list of mappings with scalar fields and inline ([a, b]) or block
// ("- a") lists. It is deliberately small and strict.
func parseStatus(data []byte) ([]Item, error) {
	var (
		items   []Item
		cur     *Item
		curList *[]string
		curInts *[]int
	)
	flush := func() {
		if cur != nil {
			items = append(items, *cur)
			cur = nil
		}
		curList, curInts = nil, nil
	}

	for n, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimRight(raw, "\r")
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") || t == "items:" {
			continue
		}
		if strings.HasPrefix(t, "- ") {
			entry := strings.TrimSpace(t[2:])
			if !strings.Contains(entry, ":") {
				switch {
				case curList != nil:
					*curList = append(*curList, unquote(entry))
					continue
				case curInts != nil:
					v, err := strconv.Atoi(unquote(entry))
					if err != nil {
						return nil, fmt.Errorf("line %d: %w", n+1, err)
					}
					*curInts = append(*curInts, v)
					continue
				}
			}
			flush()
			cur = &Item{}
			key, val, ok := splitKV(entry)
			if !ok {
				return nil, fmt.Errorf("line %d: item must start with a field", n+1)
			}
			if err := setItemField(cur, key, val, &curList, &curInts); err != nil {
				return nil, fmt.Errorf("line %d: %w", n+1, err)
			}
			continue
		}
		if cur == nil {
			return nil, fmt.Errorf("line %d: field outside an item", n+1)
		}
		key, val, ok := splitKV(t)
		if !ok {
			return nil, fmt.Errorf("line %d: expected \"key: value\"", n+1)
		}
		curList, curInts = nil, nil
		if err := setItemField(cur, key, val, &curList, &curInts); err != nil {
			return nil, fmt.Errorf("line %d: %w", n+1, err)
		}
	}
	flush()

	if len(items) == 0 {
		return nil, errors.New("no items found")
	}
	for i, it := range items {
		if it.ID == "" {
			return nil, fmt.Errorf("item %d: missing id", i+1)
		}
		if !validKinds[it.Kind] {
			return nil, fmt.Errorf("item %q: unknown kind %q", it.ID, it.Kind)
		}
		if !validStates[it.State] {
			return nil, fmt.Errorf("item %q: unknown state %q", it.ID, it.State)
		}
		if _, err := time.Parse("2006-01-02", it.Date); err != nil {
			return nil, fmt.Errorf("item %q: date must be YYYY-MM-DD, got %q", it.ID, it.Date)
		}
		if it.OneLine == "" {
			return nil, fmt.Errorf("item %q: missing one_line", it.ID)
		}
	}
	return items, nil
}

func setItemField(it *Item, key, val string, list **[]string, ints **[]int) error {
	switch key {
	case "id":
		it.ID = unquote(val)
	case "kind":
		it.Kind = unquote(val)
	case "state":
		it.State = unquote(val)
	case "date":
		it.Date = unquote(val)
	case "one_line":
		it.OneLine = unquote(val)
	case "pr":
		if val == "" || val == "null" || val == "~" {
			it.PR = nil
			return nil
		}
		n, err := strconv.Atoi(unquote(val))
		if err != nil {
			return fmt.Errorf("pr: %w", err)
		}
		it.PR = &n
	case "commits":
		if val == "" {
			*list = &it.Commits
			return nil
		}
		vs, err := parseFlowList(val)
		if err != nil {
			return err
		}
		it.Commits = vs
	case "issues":
		if val == "" {
			*ints = &it.Issues
			return nil
		}
		vs, err := parseFlowList(val)
		if err != nil {
			return err
		}
		for _, v := range vs {
			n, err := strconv.Atoi(v)
			if err != nil {
				return fmt.Errorf("issues: %w", err)
			}
			it.Issues = append(it.Issues, n)
		}
	default:
		return fmt.Errorf("unknown field %q", key)
	}
	return nil
}

// parseLessons extracts the flat "- " bullet list from the lessons markdown. A
// lesson wrapped across several source lines is joined back into one, so the
// compact never silently truncates a lesson at the wrap.
func parseLessons(data []byte) []string {
	var (
		out []string
		cur strings.Builder
	)
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	for _, raw := range strings.Split(string(data), "\n") {
		t := strings.TrimSpace(strings.TrimRight(raw, "\r"))
		switch {
		case strings.HasPrefix(t, "- "):
			flush()
			cur.WriteString(strings.TrimSpace(t[2:]))
		case t == "":
			flush()
		case cur.Len() > 0:
			cur.WriteString(" ")
			cur.WriteString(t)
		}
	}
	flush()
	return out
}

// loadADRs reads the ADR directory's frontmatter and headings, sorted by id.
func loadADRs(dir string) ([]ADR, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var adrs []ADR
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		adrs = append(adrs, parseADR(e.Name(), string(data)))
	}
	sort.Slice(adrs, func(i, j int) bool { return adrs[i].ID < adrs[j].ID })
	return adrs, nil
}

// adrDate matches the first YYYY-MM-DD token in a legacy "## Status" line.
var adrDate = regexp.MustCompile(`\d{4}-\d{2}-\d{2}`)

// parseADR reads an ADR's id (from the filename), title (the first heading),
// and status/date. Newer ADRs carry YAML frontmatter; the older ones (0001 to
// 0004) carry a "## Status" section instead, so that section is read as a
// fallback rather than reporting the status as unknown.
func parseADR(name, content string) ADR {
	base := strings.TrimSuffix(name, ".md")
	num := base
	if i := strings.Index(base, "-"); i >= 0 {
		num = base[:i]
	}
	a := ADR{ID: "ADR-" + num, Status: "unknown", Date: "unknown"}

	var statusText []string
	inFrontmatter, seenDelim, inStatus := false, false, false
	for _, raw := range strings.Split(content, "\n") {
		t := strings.TrimSpace(raw)
		if t == "---" {
			if !seenDelim {
				seenDelim, inFrontmatter = true, true
				continue
			}
			if inFrontmatter {
				inFrontmatter = false
				continue
			}
		}
		if inFrontmatter {
			if k, v, ok := splitKV(t); ok {
				switch k {
				case "status":
					a.Status = unquote(v)
				case "date":
					a.Date = unquote(v)
				}
			}
			continue
		}
		if strings.HasPrefix(t, "## ") {
			inStatus = strings.EqualFold(strings.TrimSpace(t[3:]), "Status")
			continue
		}
		if inStatus && t != "" {
			statusText = append(statusText, t)
			continue
		}
		if a.Title == "" && strings.HasPrefix(t, "# ") {
			a.Title = strings.TrimSpace(t[2:])
		}
	}

	if len(statusText) > 0 {
		joined := strings.Join(statusText, " ")
		if a.Date == "unknown" {
			if m := adrDate.FindString(joined); m != "" {
				a.Date = m
			}
		}
		if a.Status == "unknown" {
			if fields := strings.Fields(joined); len(fields) > 0 {
				a.Status = strings.ToLower(strings.Trim(fields[0], "[]()*`"))
			}
		}
	}
	// Drop the redundant "ADR NNNN: " prefix the legacy titles carry.
	a.Title = strings.TrimSpace(strings.TrimPrefix(a.Title, "ADR "+num+": "))
	return a
}

// --- small text helpers -----------------------------------------------------

func splitKV(s string) (key, val string, ok bool) {
	i := strings.Index(s, ":")
	if i < 0 {
		return "", "", false
	}
	return strings.TrimSpace(s[:i]), strings.TrimSpace(s[i+1:]), true
}

func unquote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 {
		if s[0] == '"' && s[len(s)-1] == '"' {
			return strings.ReplaceAll(s[1:len(s)-1], `\"`, `"`)
		}
		if s[0] == '\'' && s[len(s)-1] == '\'' {
			return s[1 : len(s)-1]
		}
	}
	return s
}

func parseFlowList(s string) ([]string, error) {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "[") || !strings.HasSuffix(s, "]") {
		return nil, fmt.Errorf("expected a [a, b] list, got %q", s)
	}
	inner := strings.TrimSpace(s[1 : len(s)-1])
	if inner == "" {
		return nil, nil
	}
	var out []string
	for _, p := range strings.Split(inner, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, unquote(p))
		}
	}
	return out, nil
}

// esc makes a value safe inside a markdown table cell.
func esc(s string) string {
	s = strings.ReplaceAll(s, "|", `\|`)
	return strings.ReplaceAll(s, "\n", " ")
}

func commitsCell(commits []string) string {
	if len(commits) == 0 {
		return "—"
	}
	return esc(strings.Join(commits, ", "))
}

func prCell(pr *int) string {
	if pr == nil {
		return "—"
	}
	return "#" + strconv.Itoa(*pr)
}

func issuesCell(issues []int) string {
	if len(issues) == 0 {
		return "—"
	}
	parts := make([]string, len(issues))
	for i, n := range issues {
		parts[i] = "#" + strconv.Itoa(n)
	}
	return strings.Join(parts, ", ")
}
