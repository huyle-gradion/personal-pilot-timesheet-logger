package submit

import (
	"testing"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/gradion"
)

// row is a fully-populated catalog row.
func row(id, name, code, atype string) gradion.ClassificationOption {
	return gradion.ClassificationOption{ID: id, Name: name, Code: code, ActivityType: atype}
}

// liveCat builds a catalog with the given source and completeness.
func liveCat(source string, complete bool, opts ...gradion.ClassificationOption) gradion.Catalog {
	return gradion.Catalog{Complete: complete, Source: source, Options: opts}
}

func lk() ClassificationLookup {
	return ClassificationLookup{LocalID: "c1", ID: "cls-1", Code: "INTGRADI2610", Name: "Gradion Intern Academy 2026", ActivityType: "internal project"}
}

// A fresh LIVE FULL COMPLETE catalog carrying the exact row proves the choice.
func TestProveClassification_FullExactRowPasses(t *testing.T) {
	cat := liveCat("live", true, row("cls-1", "Gradion Intern Academy 2026", "INTGRADI2610", "internal project"))
	if got := ProveClassification(cat, lk(), true); got != "" {
		t.Fatalf("want proven, got %q", got)
	}
}

// Absence from a full catalog is PROVEN: unknown_classification.
func TestProveClassification_FullAbsenceIsUnknown(t *testing.T) {
	cat := liveCat("live", true, row("other", "Other", "OTHER", "internal project"))
	if got := ProveClassification(cat, lk(), true); got != BlockUnknownClassification {
		t.Fatalf("want %s, got %q", BlockUnknownClassification, got)
	}
}

// A targeted LIVE COMPLETE result carrying the exact row proves the choice.
func TestProveClassification_TargetedCompleteExactRowPasses(t *testing.T) {
	cat := liveCat("live", true, row("cls-1", "Gradion Intern Academy 2026", "INTGRADI2610", "internal project"))
	if got := ProveClassification(cat, lk(), false); got != "" {
		t.Fatalf("want proven, got %q", got)
	}
}

// A PARTIAL targeted page fails closed even when it contains the desired row.
func TestProveClassification_TargetedPartialWithRowBlocks(t *testing.T) {
	cat := liveCat("live", false, row("cls-1", "Gradion Intern Academy 2026", "INTGRADI2610", "internal project"))
	if got := ProveClassification(cat, lk(), false); got != BlockUnverifiedClassification {
		t.Fatalf("a partial page must fail closed, got %q", got)
	}
}

// A non-live (cache) result never proves identity, full or targeted.
func TestProveClassification_NonLiveBlocks(t *testing.T) {
	cat := liveCat("cache", true, row("cls-1", "Gradion Intern Academy 2026", "INTGRADI2610", "internal project"))
	if got := ProveClassification(cat, lk(), true); got != BlockUnverifiedClassification {
		t.Fatalf("a cache must not prove identity, got %q", got)
	}
	if got := ProveClassification(cat, lk(), false); got != BlockUnverifiedClassification {
		t.Fatalf("a cache must not prove identity, got %q", got)
	}
}

func TestProveClassification_DuplicateIDBlocks(t *testing.T) {
	cat := liveCat("live", true,
		row("cls-1", "Gradion Intern Academy 2026", "INTGRADI2610", "internal project"),
		row("cls-1", "Gradion Intern Academy 2026", "INTGRADI2610", "internal project"),
	)
	if got := ProveClassification(cat, lk(), false); got != BlockUnverifiedClassification {
		t.Fatalf("a duplicate id must block, got %q", got)
	}
}

func TestProveClassification_DuplicateCodeBlocks(t *testing.T) {
	// The desired row is present, but a SECOND row shares the exact code: ambiguous.
	cat := liveCat("live", true,
		row("cls-1", "Gradion Intern Academy 2026", "INTGRADI2610", "internal project"),
		row("cls-9", "Other", "INTGRADI2610", "internal project"),
	)
	if got := ProveClassification(cat, lk(), false); got != BlockUnverifiedClassification {
		t.Fatalf("a duplicate code must block, got %q", got)
	}
}

func TestProveClassification_IDMismatchBlocks(t *testing.T) {
	cat := liveCat("live", true, row("cls-2", "Gradion Intern Academy 2026", "INTGRADI2610", "internal project"))
	if got := ProveClassification(cat, lk(), false); got != BlockUnverifiedClassification {
		t.Fatalf("an id mismatch must block, got %q", got)
	}
}

// An UNRELATED extra row in a targeted page fails closed even though the desired
// row is present.
func TestProveClassification_UnrelatedRowBlocks(t *testing.T) {
	cat := liveCat("live", true,
		row("cls-1", "Gradion Intern Academy 2026", "INTGRADI2610", "internal project"),
		row("cls-99", "Unrelated", "OTHER-CODE", "client project"),
	)
	if got := ProveClassification(cat, lk(), false); got != BlockUnverifiedClassification {
		t.Fatalf("an unrelated extra row must block, got %q", got)
	}
}

func TestProveClassification_CodeMismatchBlocks(t *testing.T) {
	cat := liveCat("live", true, row("cls-1", "Gradion Intern Academy 2026", "OTHER-CODE", "internal project"))
	if got := ProveClassification(cat, lk(), false); got != BlockUnverifiedClassification {
		t.Fatalf("a changed recorded code must block, got %q", got)
	}
}

func TestProveClassification_NameMismatchBlocks(t *testing.T) {
	cat := liveCat("live", true, row("cls-1", "A Different Name", "INTGRADI2610", "internal project"))
	if got := ProveClassification(cat, lk(), false); got != BlockUnverifiedClassification {
		t.Fatalf("a changed recorded name must block, got %q", got)
	}
}

func TestProveClassification_TypeMismatchBlocks(t *testing.T) {
	cat := liveCat("live", true, row("cls-1", "Gradion Intern Academy 2026", "INTGRADI2610", "client project"))
	if got := ProveClassification(cat, lk(), false); got != BlockUnverifiedClassification {
		t.Fatalf("a derived type mismatch must block, got %q", got)
	}
}

// Garbage metadata on the returned row (empty name/code/type) fails closed.
func TestProveClassification_MissingRowMetadataBlocks(t *testing.T) {
	for _, o := range []gradion.ClassificationOption{
		{ID: "cls-1", Code: "INTGRADI2610", ActivityType: "internal project"},                // no name
		{ID: "cls-1", Name: "Gradion Intern Academy 2026", ActivityType: "internal project"}, // no code
		{ID: "cls-1", Name: "Gradion Intern Academy 2026", Code: "INTGRADI2610"},             // no type
	} {
		cat := liveCat("live", true, o)
		if got := ProveClassification(cat, lk(), false); got != BlockUnverifiedClassification {
			t.Fatalf("missing row metadata must block, got %q for %+v", got, o)
		}
	}
}

func TestProveClassification_EmptyLocalTypeBlocks(t *testing.T) {
	cat := liveCat("live", true, row("cls-1", "Gradion Intern Academy 2026", "INTGRADI2610", "internal project"))
	l := lk()
	l.ActivityType = ""
	if got := ProveClassification(cat, l, false); got != BlockUnverifiedClassification {
		t.Fatalf("a missing local type must block, got %q", got)
	}
}

// A LEGACY record (no code/name) can still be proven from a fresh LIVE FULL
// COMPLETE catalog when the returned row's metadata meets safety.
func TestProveClassification_LegacyFullCatalogSafeRowPasses(t *testing.T) {
	cat := liveCat("live", true, row("cls-1", "Gradion Intern Academy 2026", "INTGRADI2610", "internal project"))
	l := ClassificationLookup{LocalID: "c1", ID: "cls-1", ActivityType: "internal project"}
	if got := ProveClassification(cat, l, true); got != "" {
		t.Fatalf("a legacy choice against a safe full catalog must pass, got %q", got)
	}
}

func TestInconsistentIDMetadata_BlocksSharedIDWithDifferentMetadata(t *testing.T) {
	lookups := []ClassificationLookup{
		{LocalID: "c1", ID: "cls-1", Code: "A", Name: "N", ActivityType: "internal project"},
		{LocalID: "c2", ID: "cls-1", Code: "B", Name: "N", ActivityType: "internal project"},
	}
	reasons := InconsistentIDMetadata(lookups)
	if len(reasons) != 2 {
		t.Fatalf("want both ops blocked, got %v", reasons)
	}
}

func TestInconsistentIDMetadata_ConsistentPasses(t *testing.T) {
	lookups := []ClassificationLookup{
		{LocalID: "c1", ID: "cls-1", Code: "A", Name: "N", ActivityType: "internal project"},
		{LocalID: "c2", ID: "cls-1", Code: "A", Name: "N", ActivityType: "Internal Project"},
	}
	if reasons := InconsistentIDMetadata(lookups); len(reasons) != 0 {
		t.Fatalf("a consistent id group must not block, got %v", reasons)
	}
}
