package plugins

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/evidence"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/export"
)

func journalFixtureSource(name string) evidence.SourceConfig {
	return evidence.SourceConfig{
		ID:      "journal-work",
		Type:    TypeJournal,
		Enabled: true,
		Config: map[string]string{
			JournalConfigVault:    "vault-root",
			JournalConfigDailyDir: "02 - Daily & Logs",
			JournalConfigFixture:  filepath.Join("testdata", "journal", name),
		},
	}
}

func collectJournalFixture(t *testing.T, name string) evidence.CollectResult {
	t.Helper()
	res, err := NewJournalPlugin().Collect(context.Background(),
		journalFixtureSource(name),
		evidence.CollectRequest{Date: "2026-10-08", Mode: evidence.ModeContract})
	if err != nil {
		t.Fatalf("journal Collect: %v", err)
	}
	return res
}

func journalFind(t *testing.T, evs []evidence.Evidence, substr string) evidence.Evidence {
	t.Helper()
	for _, e := range evs {
		if strings.Contains(e.Description, substr) {
			return e
		}
	}
	t.Fatalf("no journal evidence containing %q in %d items", substr, len(evs))
	return evidence.Evidence{}
}

func hasWarning(warnings []string, prefix string) bool {
	for _, w := range warnings {
		if w == prefix || strings.HasPrefix(w, prefix+":") {
			return true
		}
	}
	return false
}

// TestJournal_ActualShapes proves the parser handles the real note shapes:
// frontmatter, decorative headings, nested ancestry, multiline bullets, stated
// dates and clocks, inline/frontmatter project hints, and UNTIMED evidence. It
// also proves checkbox tasks, unrecognized sections, metrics-only entries, and
// narrow authority boilerplate are skipped while substantive (non-code) work is
// retained.
func TestJournal_ActualShapes(t *testing.T) {
	res := collectJournalFixture(t, "daily.md")
	if res.Result.Status != evidence.StatusComplete {
		t.Fatalf("expected complete, got %+v", res.Result)
	}
	if len(res.Evidence) != 5 {
		t.Fatalf("expected 5 narrative items, got %d", len(res.Evidence))
	}

	// Every item is UNTIMED: zero start/end and unknown duration.
	for _, e := range res.Evidence {
		if e.Kind != evidence.SourceJournal {
			t.Errorf("unexpected kind %q", e.Kind)
		}
		if !e.Start.IsZero() || !e.End.IsZero() {
			t.Errorf("journal item must carry no time: %+v", e)
		}
		if !e.Duration.IsUnknown() {
			t.Errorf("journal item duration must be unknown: %+v", e)
		}
	}

	wired := journalFind(t, res.Evidence, "Wired the journal source")
	if !strings.Contains(wired.Description, "validated the contract-check path") {
		t.Errorf("multiline bullet continuation was not retained: %q", wired.Description)
	}
	if len(wired.SourceRefs) != 1 {
		t.Fatalf("expected one source ref, got %d", len(wired.SourceRefs))
	}
	ref := wired.SourceRefs[0]
	if ref.SourceID != "journal-work" || ref.Section != "work journal" || ref.NotePath != "daily.md" {
		t.Errorf("unexpected provenance ref: %+v", ref)
	}
	if ref.Clock != "09:15" {
		t.Errorf("expected clock 09:15, got %q", ref.Clock)
	}
	if ref.StatedDate != "2026-10-08" {
		t.Errorf("expected stated date, got %q", ref.StatedDate)
	}
	if !strings.HasSuffix(ref.HeadingPath, "Work journal") || !strings.Contains(ref.HeadingPath, " / ") {
		t.Errorf("expected full heading ancestry ending in 'Work journal', got %q", ref.HeadingPath)
	}
	if ref.LineStart == 0 || ref.LineEnd < ref.LineStart {
		t.Errorf("expected a valid inclusive line range, got %d..%d", ref.LineStart, ref.LineEnd)
	}
	// Union of frontmatter focus_projects and the inline wikilink.
	if !containsAll(wired.ProjectHints, "Personal Pilot", "Timesheet Logger") {
		t.Errorf("expected unioned project hints, got %v", wired.ProjectHints)
	}

	reviewed := journalFind(t, res.Evidence, "Reviewed the pull request")
	if reviewed.SourceRefs[0].Section != "quick capture" || reviewed.SourceRefs[0].Clock != "14:30" {
		t.Errorf("unexpected quick-capture item: %+v", reviewed.SourceRefs[0])
	}
	backfilled := journalFind(t, res.Evidence, "backfilled yesterday")
	if backfilled.SourceRefs[0].StatedDate != "2026-10-07" {
		t.Errorf("inline stated date was not retained: %q", backfilled.SourceRefs[0].StatedDate)
	}

	// Substantive non-code work is retained.
	if !strings.Contains(journalFind(t, res.Evidence, "multiline bullet parser").Description, "documented the research findings") {
		t.Error("substantive research documentation was dropped")
	}

	// Skips: checkbox tasks, priorities/todo sections, unrecognized sections.
	for _, e := range res.Evidence {
		if strings.Contains(e.Description, "Ship the release candidate") ||
			strings.Contains(e.Description, "Reply to the scheduling email") ||
			strings.Contains(e.Description, "unrecognized section") ||
			strings.Contains(e.Description, "Fold this into tomorrow") {
			t.Errorf("skipped content leaked into evidence: %q", e.Description)
		}
	}

	// Narrow boilerplate and metrics-only entries are excluded with counts.
	if !hasWarning(res.Result.Warnings, JournalWarnBoilerplateExcluded) {
		t.Errorf("expected boilerplate exclusion warning, got %v", res.Result.Warnings)
	}
	if !hasWarning(res.Result.Warnings, JournalWarnMetricsExcluded) {
		t.Errorf("expected metrics exclusion warning, got %v", res.Result.Warnings)
	}
	if !hasWarning(res.Result.Warnings, JournalWarnDateMismatch) {
		t.Errorf("expected date-mismatch warning, got %v", res.Result.Warnings)
	}
	// A non-exact boilerplate-like item is NOT excluded.
	if !hasExactBoilerplateRetained(t) {
		t.Error("expected a boilerplate-like substantive item to be retained")
	}
}

// TestJournal_IdenticalAndReorderStable proves identical recollection and a
// reordering of the same bullets yield the same ID set, and identical text in two
// different sections stays DISTINCT.
func TestJournal_IdenticalAndReorderStable(t *testing.T) {
	first := collectJournalFixture(t, "daily.md")
	second := collectJournalFixture(t, "daily.md")
	if !sameJournalIDs(first.Evidence, second.Evidence) {
		t.Fatal("identical recollection produced a differing ID set")
	}

	// The same text in two DIFFERENT sections stays DISTINCT (journal vs impact).
	crossSection := writeJournalDoc(t, `## Work journal
- Same sentence.
## Daily impact
- Same sentence.
`)
	cross := collectInline(t, crossSection)
	if len(cross.Evidence) != 2 || cross.Evidence[0].ID == cross.Evidence[1].ID {
		t.Fatalf("identical text across sections must stay distinct, got %d items", len(cross.Evidence))
	}

	// Reordering the same bullets does not change the ID set.
	reordered := writeJournalDoc(t, `---
date: 2026-10-08
---

## Work journal
- Second item.
- First item.
`)
	orig := writeJournalDoc(t, `---
date: 2026-10-08
---

## Work journal
- First item.
- Second item.
`)
	if !sameJournalIDs(collectInline(t, orig).Evidence, collectInline(t, reordered).Evidence) {
		t.Fatal("reordering bullets changed the journal ID set")
	}
}

// TestJournal_ManagedMarkers proves the exported managed region is skipped and a
// malformed (unterminated) managed region fails closed so exported text never
// feeds back.
func TestJournal_ManagedMarkers(t *testing.T) {
	ok := collectJournalFixture(t, "managed.md")
	if ok.Result.Status != evidence.StatusComplete {
		t.Fatalf("expected complete, got %+v", ok.Result)
	}
	if len(ok.Evidence) != 2 {
		t.Fatalf("expected the two real items around the managed block, got %d", len(ok.Evidence))
	}
	for _, e := range ok.Evidence {
		if strings.Contains(e.Description, "Exported row text") || strings.Contains(e.Description, "reviewed-not-submitted") {
			t.Errorf("managed/exported text fed back as evidence: %q", e.Description)
		}
	}

	bad := collectJournalFixture(t, "malformed.md")
	if bad.Result.Status != evidence.StatusIncomplete {
		t.Fatalf("a malformed managed region must fail closed, got %+v", bad.Result)
	}
	if !hasWarning(bad.Result.Warnings, JournalWarnManagedMalformed) {
		t.Errorf("expected malformed-marker warning, got %v", bad.Result.Warnings)
	}
	if len(bad.Evidence) != 0 {
		t.Errorf("a malformed managed region must yield no evidence, got %d", len(bad.Evidence))
	}
}

// TestJournal_DateUnstatedIsReported proves an EXISTING note with no frontmatter
// `date` cannot certify its date: the source stays complete, but it reports
// JOURNAL_DATE_UNSTATED instead of attributing items (or a zero) in silence.
func TestJournal_DateUnstatedIsReported(t *testing.T) {
	// Narrative items with no frontmatter at all.
	withItems := collectInline(t, writeJournalDoc(t, "## Work journal\n- Shipped the parser.\n"))
	if withItems.Result.Status != evidence.StatusComplete || len(withItems.Evidence) != 1 {
		t.Fatalf("unexpected result: %+v", withItems)
	}
	if !hasWarning(withItems.Result.Warnings, JournalWarnDateUnstated) {
		t.Fatalf("an uncertified note date must be reported, got %v", withItems.Result.Warnings)
	}

	// The fresh export-only shape: a managed block, no frontmatter, no sections.
	freshDoc := "<!-- timesheet:begin v1 -->\n## Timesheet — 2026-10-08\n\nStatus: `reviewed-not-submitted`\n\n<!-- timesheet:end -->\n"
	fresh := collectInline(t, writeJournalDoc(t, freshDoc))
	if fresh.Result.Status != evidence.StatusComplete {
		t.Fatalf("a present note must be complete, got %+v", fresh.Result)
	}
	if len(fresh.Evidence) != 0 || fresh.Result.EvidenceCount != 0 {
		t.Fatalf("the managed block is never evidence, got %+v", fresh)
	}
	if len(fresh.Result.Warnings) != 1 || fresh.Result.Warnings[0] != JournalWarnDateUnstated {
		t.Fatalf("a complete note with no stated date must carry exactly %s, got %v",
			JournalWarnDateUnstated, fresh.Result.Warnings)
	}

	// A stated date (plain or quoted) certifies the note.
	for _, fm := range []string{"date: 2026-10-08", `date: "2026-10-08"`} {
		res := collectInline(t, writeJournalDoc(t, "---\n"+fm+"\n---\n\n## Work journal\n- An item.\n"))
		if res.Result.Status != evidence.StatusComplete || len(res.Evidence) != 1 {
			t.Fatalf("unexpected result for %q: %+v", fm, res)
		}
		if hasWarning(res.Result.Warnings, JournalWarnDateUnstated) {
			t.Errorf("a stated date must not raise %s, got %v", JournalWarnDateUnstated, res.Result.Warnings)
		}
	}

	// A stated date that disagrees still raises ONLY the existing mismatch.
	mismatch := collectInline(t, writeJournalDoc(t, "---\ndate: 2026-10-07\n---\n\n## Work journal\n- An item.\n"))
	if !hasWarning(mismatch.Result.Warnings, JournalWarnDateMismatch) {
		t.Errorf("expected %s, got %v", JournalWarnDateMismatch, mismatch.Result.Warnings)
	}
	if hasWarning(mismatch.Result.Warnings, JournalWarnDateUnstated) {
		t.Errorf("a stated date must not raise %s, got %v", JournalWarnDateUnstated, mismatch.Result.Warnings)
	}
}

// TestJournal_ExportCreatedNoteIsCertified proves the day-note lifecycle: a note
// the export CREATES carries the shared frontmatter, so collecting that same day
// afterwards is complete, yields no evidence from the managed block, and raises no
// date warning. (Before the seeding, the created file was the file that failed the
// collector's own precondition.)
func TestJournal_ExportCreatedNoteIsCertified(t *testing.T) {
	vault := t.TempDir()
	dir := "02 - Daily & Logs"
	if _, err := export.NewNoteExporter(vault, dir).Export(context.Background(), export.Note{
		Date: "2026-10-08", Status: export.StatusReviewedNotSubmitted,
	}); err != nil {
		t.Fatalf("export: %v", err)
	}

	res, err := NewJournalPlugin().Collect(context.Background(),
		evidence.SourceConfig{ID: "j", Type: TypeJournal, Enabled: true,
			Config: map[string]string{JournalConfigVault: vault, JournalConfigDailyDir: dir}},
		evidence.CollectRequest{Date: "2026-10-08"})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if res.Result.Status != evidence.StatusComplete || len(res.Evidence) != 0 {
		t.Fatalf("an export-created note must be a complete empty source: %+v", res)
	}
	if hasWarning(res.Result.Warnings, JournalWarnDateUnstated) || hasWarning(res.Result.Warnings, JournalWarnDateMismatch) {
		t.Fatalf("an export-created note must certify its date, got %v", res.Result.Warnings)
	}
}

// TestJournal_CodeFenceSkipped proves bullets inside a fenced code block are not
// evidence.
func TestJournal_CodeFenceSkipped(t *testing.T) {
	res := collectJournalFixture(t, "fence.md")
	if res.Result.Status != evidence.StatusComplete || len(res.Evidence) != 2 {
		t.Fatalf("expected 2 items outside the fence, got %+v", res)
	}
	for _, e := range res.Evidence {
		if strings.Contains(e.Description, "inside a fenced code block") {
			t.Errorf("fenced-code bullet leaked: %q", e.Description)
		}
	}
}

// TestJournal_ExplicitID proves an explicit id is stable across an edit and a
// move, is independent of section, and that duplicate explicit ids fail visibly.
func TestJournal_ExplicitID(t *testing.T) {
	base := writeJournalDoc(t, `## Work journal
- Alpha task <!-- evidence_id: a1 -->
`)
	edited := writeJournalDoc(t, `## Daily impact
- Alpha task edited and moved <!-- evidence_id: a1 -->
`)
	idBase := collectInline(t, base).Evidence
	idEdit := collectInline(t, edited).Evidence
	if len(idBase) != 1 || len(idEdit) != 1 {
		t.Fatalf("expected one item each, got %d/%d", len(idBase), len(idEdit))
	}
	if idBase[0].ID != idEdit[0].ID {
		t.Errorf("explicit id must be stable across edit+move: %q != %q", idBase[0].ID, idEdit[0].ID)
	}
	if strings.Contains(idBase[0].Description, "evidence_id") {
		t.Errorf("the explicit id marker must be stripped from the text: %q", idBase[0].Description)
	}

	dup := writeJournalDoc(t, `## Work journal
- One <!-- evidence_id: a1 -->
- Two <!-- evidence_id: a1 -->
`)
	dupRes := collectInline(t, dup)
	if dupRes.Result.Status != evidence.StatusIncomplete {
		t.Fatalf("duplicate explicit ids must fail visibly, got %+v", dupRes.Result)
	}
	if !hasWarning(dupRes.Result.Warnings, JournalWarnExplicitIDDuplicate) {
		t.Errorf("expected duplicate-id warning, got %v", dupRes.Result.Warnings)
	}
}

// TestJournal_ImplicitEditChangesIdentity proves editing an implicit item's text
// changes its identity (so history can detach) while an unchanged item is stable.
func TestJournal_ImplicitEditChangesIdentity(t *testing.T) {
	a := collectInline(t, writeJournalDoc(t, "## Work journal\n- Alpha task\n"))
	b := collectInline(t, writeJournalDoc(t, "## Work journal\n- Alpha task edited\n"))
	if a.Evidence[0].ID == b.Evidence[0].ID {
		t.Error("editing an implicit item must change its identity")
	}
}

// TestJournal_PathConfinement proves an absolute or escaping daily_dir and a
// symlink escape are refused.
func TestJournal_PathConfinement(t *testing.T) {
	vault := t.TempDir()
	if _, _, err := ResolveJournalNotePath(vault, "/etc", "2026-10-08"); err == nil {
		t.Error("absolute daily_dir must be refused")
	}
	if _, _, err := ResolveJournalNotePath(vault, "../outside", "2026-10-08"); err == nil {
		t.Error("escaping daily_dir must be refused")
	}
	if _, _, err := ResolveJournalNotePath(vault, "02 - Daily & Logs", "2026-10-08"); err != nil {
		t.Errorf("valid relative daily_dir refused: %v", err)
	}

	outside := t.TempDir()
	link := filepath.Join(vault, "linked")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, _, err := ResolveJournalNotePath(vault, "linked", "2026-10-08"); err == nil {
		t.Error("a symlinked daily_dir escaping the vault must be refused")
	}
}

// TestJournal_MissingVaultAndNoteIncomplete proves a missing vault or note is
// visibly incomplete, and an empty note is a complete empty source.
func TestJournal_MissingVaultAndNoteIncomplete(t *testing.T) {
	// Missing vault.
	res, err := NewJournalPlugin().Collect(context.Background(),
		evidence.SourceConfig{ID: "j", Type: TypeJournal, Enabled: true,
			Config: map[string]string{JournalConfigVault: filepath.Join(t.TempDir(), "nope"), JournalConfigDailyDir: "02 - Daily & Logs"}},
		evidence.CollectRequest{Date: "2026-10-08"})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if res.Result.Status != evidence.StatusIncomplete || !hasWarning(res.Result.Warnings, JournalWarnVaultMissing) {
		t.Fatalf("missing vault must be incomplete: %+v", res.Result)
	}

	// Missing note.
	vault := t.TempDir()
	res2, err := NewJournalPlugin().Collect(context.Background(),
		evidence.SourceConfig{ID: "j", Type: TypeJournal, Enabled: true,
			Config: map[string]string{JournalConfigVault: vault, JournalConfigDailyDir: "02 - Daily & Logs"}},
		evidence.CollectRequest{Date: "2026-10-08"})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if res2.Result.Status != evidence.StatusIncomplete || !hasWarning(res2.Result.Warnings, JournalWarnNoteMissing) {
		t.Fatalf("missing note must be incomplete: %+v", res2.Result)
	}

	// Present note with no recognized sections is a COMPLETE empty source.
	dir := filepath.Join(vault, "02 - Daily & Logs")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "2026-10-08.md"), []byte("# Nothing here\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	res3, err := NewJournalPlugin().Collect(context.Background(),
		evidence.SourceConfig{ID: "j", Type: TypeJournal, Enabled: true,
			Config: map[string]string{JournalConfigVault: vault, JournalConfigDailyDir: "02 - Daily & Logs"}},
		evidence.CollectRequest{Date: "2026-10-08"})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if res3.Result.Status != evidence.StatusComplete || len(res3.Evidence) != 0 {
		t.Fatalf("an empty note must be complete with no evidence: %+v", res3)
	}
}

// TestJournal_RegistryContractMode proves the journal plugin is a registered,
// contract-checkable source: a fixture passes VerifyContracts deterministically
// and a live note collects through CollectDay.
func TestJournal_RegistryContractMode(t *testing.T) {
	reg := evidence.NewRegistry()
	RegisterJournal(reg)

	results, err := reg.VerifyContracts(context.Background(), []evidence.SourceConfig{journalFixtureSource("daily.md")})
	if err != nil {
		t.Fatalf("VerifyContracts: %v", err)
	}
	if len(results) != 1 || results[0].Status != evidence.StatusComplete {
		t.Fatalf("journal contract verification failed: %+v", results)
	}

	vault := t.TempDir()
	dir := filepath.Join(vault, "02 - Daily & Logs")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "2026-10-08.md"), []byte("## Work journal\n- Live item.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ev, summary, err := reg.CollectDay(context.Background(), []evidence.SourceConfig{{
		ID: "journal-work", Type: TypeJournal, Enabled: true,
		Config: map[string]string{JournalConfigVault: vault, JournalConfigDailyDir: "02 - Daily & Logs"},
	}}, evidence.CollectRequest{Date: "2026-10-08"})
	if err != nil {
		t.Fatalf("CollectDay: %v", err)
	}
	if !summary.Complete || len(ev) != 1 {
		t.Fatalf("live journal collection failed: %+v", summary)
	}
}

// TestJournal_NarrowBoilerplateAndSubstantiveRetained proves a boilerplate-like
// but substantive item is retained.
func TestJournal_NarrowBoilerplateAndSubstantiveRetained(t *testing.T) {
	doc := writeJournalDoc(t, `## Work journal
- Please do not share confidential information with the vendor.
- Reviewed the benchmark implementation and the research plan.
`)
	res := collectInline(t, doc)
	if len(res.Evidence) != 2 {
		t.Fatalf("expected both substantive items retained, got %d", len(res.Evidence))
	}
}

// --- helpers ----------------------------------------------------------------

// TestJournal_ExportMarkersMatchAndAreSkipped proves the journal reader skips the
// EXACT managed markers internal/export writes (a duplicated literal asserted
// equal here), so an exported timesheet section can never feed back as evidence.
func TestJournal_ExportMarkersMatchAndAreSkipped(t *testing.T) {
	if journalManagedBegin != export.MarkerBegin || journalManagedEnd != export.MarkerEnd {
		t.Fatalf("journal managed markers drifted from internal/export: %q/%q", journalManagedBegin, journalManagedEnd)
	}
	rendered := export.Render(export.Note{
		Date:   "2026-10-08",
		Status: export.StatusReviewedNotSubmitted,
		Confirmed: []export.Row{{
			Start: "09:00", End: "10:00", Project: "Personal Pilot", Description: "Exported description",
		}},
	})
	doc := "## Work journal\n- A real item.\n\n" + rendered + "\n"
	res := collectInline(t, writeJournalDoc(t, doc))
	if res.Result.Status != evidence.StatusComplete || len(res.Evidence) != 1 {
		t.Fatalf("expected only the real item, got %+v", res)
	}
	if strings.Contains(res.Evidence[0].Description, "Exported description") {
		t.Errorf("exported text fed back as evidence: %q", res.Evidence[0].Description)
	}
}

// TestJournal_ImplicitIdentityDistinctAcrossHeadingsSharingPhrase proves two
// different headings that merely SHARE a recognized phrase, and two nested
// sections, never collapse to the same implicit identity (no silent upsert loss).
func TestJournal_ImplicitIdentityDistinctAcrossHeadingsSharingPhrase(t *testing.T) {
	sharing := collectInline(t, writeJournalDoc(t, `## Daily impact
- Same sentence.
## Daily impact and brag bullets (for performance review)
- Same sentence.
`))
	if len(sharing.Evidence) != 2 || sharing.Evidence[0].ID == sharing.Evidence[1].ID {
		t.Fatalf("headings sharing a phrase must not collapse, got %d items", len(sharing.Evidence))
	}

	nested := collectInline(t, writeJournalDoc(t, `## Work journal
### Morning
- Same sentence.
### Evening
- Same sentence.
`))
	if len(nested.Evidence) != 2 || nested.Evidence[0].ID == nested.Evidence[1].ID {
		t.Fatalf("nested sections must not collapse, got %d items", len(nested.Evidence))
	}
}

// TestJournal_DuplicateImplicitIDFailsVisibly proves two identical items in the
// SAME section (which resolve to one implicit identity) fail the source visibly
// instead of being silently upsert-collapsed downstream.
func TestJournal_DuplicateImplicitIDFailsVisibly(t *testing.T) {
	res := collectInline(t, writeJournalDoc(t, "## Work journal\n- Same sentence.\n- Same sentence.\n"))
	if res.Result.Status != evidence.StatusIncomplete {
		t.Fatalf("duplicate implicit ids must fail visibly, got %+v", res.Result)
	}
	if !hasWarning(res.Result.Warnings, JournalWarnImplicitIDDuplicate) {
		t.Errorf("expected implicit-duplicate warning, got %v", res.Result.Warnings)
	}
}

// TestJournal_ArbitraryTimesheetTitleNotSkipped proves a note whose title merely
// contains "timesheet" is NOT emptied: only an exact generated/legacy timesheet
// SECTION heading is skipped.
func TestJournal_ArbitraryTimesheetTitleNotSkipped(t *testing.T) {
	doc := writeJournalDoc(t, `# Timesheet day 2026-10-08

## Work journal
- A real item.
`)
	res := collectInline(t, doc)
	if res.Result.Status != evidence.StatusComplete || len(res.Evidence) != 1 {
		t.Fatalf("an arbitrary timesheet title must not empty the note: %+v", res)
	}

	// A generated section heading (exact shape) is skipped.
	gen := collectInline(t, writeJournalDoc(t, `## Work journal
- Kept item.

## Timesheet — 2026-10-08
- Exported-looking row.
`))
	if len(gen.Evidence) != 1 || strings.Contains(gen.Evidence[0].Description, "Exported-looking row") {
		t.Fatalf("an exact generated timesheet section must be skipped: %+v", gen)
	}
}

// TestJournal_AgentSessionHeadingSkipped proves the collector skips a SECTION
// heading containing the `agent session` token, and states WHERE the discrimination
// actually lives, because the obvious shape cannot fail.
//
// THE NESTING DISCOVERY, measured on the unmodified collector rather than assumed:
// a TOP-LEVEL `## Agent session log` heading ALREADY collects nothing, because an
// unrecognized heading yields no narrative family, so a test on that shape passes
// before and after and defends nothing. The real exposure is NESTING. Inside a
// collected section, `## Work journal` then `### Agent session log`, the deepest
// recognized heading is `Work journal`, so the leg is collected as the owner's WORK
// TIME. That nested pair is the discriminator. The control case shows nesting alone
// suppresses nothing, so a later "not collected" result can only come from the
// token.
func TestJournal_AgentSessionHeadingSkipped(t *testing.T) {
	const bullet = "- 09:00-10:00: session leg recorded as an activity bound"

	// CONTROL, holds on both builds: an ordinary nested heading inside a collected
	// section still collects, so the nesting position is not what suppresses.
	control := collectInline(t, writeJournalDoc(t, "## Work journal\n### Detail\n"+bullet+"\n"))
	if len(control.Evidence) != 1 {
		t.Fatalf("a plain nested heading must still collect (control), got %d item(s): %+v", len(control.Evidence), control.Evidence)
	}

	// DISCRIMINATOR: the SAME bullet, in the SAME nested position, under an `agent
	// session` heading. Collected as work time BEFORE the token, nothing AFTER.
	nested := collectInline(t, writeJournalDoc(t, "## Work journal\n### Agent session log\n"+bullet+"\n"))
	if len(nested.Evidence) != 0 {
		t.Fatalf("a leg under a nested `agent session` heading must NOT be collected as work time, got %d item(s): %+v", len(nested.Evidence), nested.Evidence)
	}

	// PIN with respect to the BASELINE, and labelled so: this heading is
	// unrecognized, so it collects nothing on the UNFIXED build too and cannot
	// discriminate the nesting defect. Its job is a different mutant: it kills a
	// case-SENSITIVE token match, which normalization already makes impossible.
	upper := collectInline(t, writeJournalDoc(t, "## Work journal\n### AGENT SESSION LOG\n"+bullet+"\n"))
	if len(upper.Evidence) != 0 {
		t.Fatalf("the token must match case-insensitively on a nested heading, got %d item(s): %+v", len(upper.Evidence), upper.Evidence)
	}

	// DISCRIMINATOR: a token heading ABOVE recognized-looking content skips the
	// whole subtree, including deeper headings and bullets that would otherwise
	// narrate. Three items before the token, none after.
	ancestor := collectInline(t, writeJournalDoc(t, "## Agent session log\n### Work journal\n- first leg\n#### Daily impact\n- second leg\n"+bullet+"\n"))
	if len(ancestor.Evidence) != 0 {
		t.Fatalf("an `agent session` ANCESTOR must skip the whole subtree, got %d item(s): %+v", len(ancestor.Evidence), ancestor.Evidence)
	}

	// PIN, NOT a discriminator: the plain TOP-LEVEL token heading already yields
	// nothing on the unmodified collector. It is asserted so `agent session` can
	// never quietly become a recognized narrative family, and it is not claimed as
	// evidence of the skipped-heading rule.
	plain := collectInline(t, writeJournalDoc(t, "## Agent session log\n"+bullet+"\n"))
	if len(plain.Evidence) != 0 {
		t.Fatalf("a bullet under a top-level `agent session` heading must NOT be collected, got %d item(s): %+v", len(plain.Evidence), plain.Evidence)
	}

	// PIN, the other direction: the SAME bullet under `Work journal` alone IS
	// collected, so the rule is heading-based and not content-based.
	underWork := collectInline(t, writeJournalDoc(t, "## Work journal\n"+bullet+"\n"))
	if len(underWork.Evidence) != 1 {
		t.Fatalf("the SAME bullet under `Work journal` must be collected, got %d item(s)", len(underWork.Evidence))
	}
	if !strings.Contains(underWork.Evidence[0].Description, "session leg recorded") {
		t.Errorf("wrong item collected under Work journal: %q", underWork.Evidence[0].Description)
	}
}

// TestJournal_MetricsUnitVocabulary proves bounded unit-bearing metrics are
// excluded while substantive prose (benchmark/research) is retained.
func TestJournal_MetricsUnitVocabulary(t *testing.T) {
	doc := writeJournalDoc(t, `## Work journal
- 1234 tokens
- 12 files and 3 runs
- 500ms / $0.42
- Reviewed 12 files in the benchmark
- Benchmarked the parser against 12 files.
`)
	res := collectInline(t, doc)
	if len(res.Evidence) != 2 {
		t.Fatalf("expected the two substantive items retained, got %d: %v", len(res.Evidence), descriptions(res.Evidence))
	}
	for _, e := range res.Evidence {
		if strings.HasPrefix(e.Description, "1234") || strings.HasPrefix(e.Description, "12 files and") {
			t.Errorf("a metrics-only line leaked: %q", e.Description)
		}
	}
	if !hasWarning(res.Result.Warnings, JournalWarnMetricsExcluded) {
		t.Errorf("expected a metrics exclusion warning, got %v", res.Result.Warnings)
	}
}

// TestJournal_QuotedFrontmatterDate proves a quoted YAML date is normalized and
// does not raise a spurious mismatch.
func TestJournal_QuotedFrontmatterDate(t *testing.T) {
	res := collectInline(t, writeJournalDoc(t, `---
date: "2026-10-08"
---

## Work journal
- A real item.
`))
	if res.Result.Status != evidence.StatusComplete || len(res.Evidence) != 1 {
		t.Fatalf("unexpected result: %+v", res)
	}
	if hasWarning(res.Result.Warnings, JournalWarnDateMismatch) {
		t.Errorf("a quoted date must not raise a mismatch, got %v", res.Result.Warnings)
	}
	if got := res.Evidence[0].SourceRefs[0].StatedDate; got != "2026-10-08" {
		t.Errorf("quoted date not normalized, got %q", got)
	}
}

func descriptions(evs []evidence.Evidence) []string {
	out := make([]string, 0, len(evs))
	for _, e := range evs {
		out = append(out, e.Description)
	}
	return out
}

func writeJournalDoc(t *testing.T, doc string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "note.md")
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatalf("write note: %v", err)
	}
	return path
}

func collectInline(t *testing.T, fixturePath string) evidence.CollectResult {
	t.Helper()
	res, err := NewJournalPlugin().Collect(context.Background(),
		evidence.SourceConfig{ID: "journal-work", Type: TypeJournal, Enabled: true,
			Config: map[string]string{JournalConfigFixture: fixturePath}},
		evidence.CollectRequest{Date: "2026-10-08", Mode: evidence.ModeContract})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	return res
}

func sameJournalIDs(a, b []evidence.Evidence) bool {
	if len(a) != len(b) {
		return false
	}
	set := map[string]bool{}
	for _, e := range a {
		set[e.ID] = true
	}
	for _, e := range b {
		if !set[e.ID] {
			return false
		}
	}
	return true
}

func containsAll(values []string, want ...string) bool {
	set := map[string]bool{}
	for _, v := range values {
		set[v] = true
	}
	for _, w := range want {
		if !set[w] {
			return false
		}
	}
	return true
}

func hasExactBoilerplateRetained(t *testing.T) bool {
	t.Helper()
	doc := writeJournalDoc(t, "## Work journal\n- Do not share confidential information with the vendor.\n")
	return len(collectInline(t, doc).Evidence) == 1
}
