package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Fixed inputs so a golden run is byte-reproducible. The commit is a literal,
// never resolved from git, so the test never depends on the repository state.
var testNow = time.Date(2026, 10, 14, 9, 30, 0, 0, time.UTC)

const testCommit = "abc1234"

// goldenSet maps each generated vault path to its committed golden fixture.
var goldenSet = map[string]string{
	projectRel: "testdata/want/assignment-1 (generated).md",
	lessonsRel: "testdata/want/assignment-1 lessons (generated).md",
	indexRel:   "testdata/want/assignment-1 (generated index).md",
}

// mustExpectedSet builds the whole set for a repository root using the fresh
// write-time facts (the index lists every file as OK).
func mustExpectedSet(t *testing.T, repoRoot string, now time.Time, commit string) map[string]string {
	t.Helper()
	s, err := readSources(repoRoot)
	if err != nil {
		t.Fatalf("readSources(%s): %v", repoRoot, err)
	}
	return expectedSet(s, now, commit, writtenFacts(commit))
}

// TestGoldenSet pins the WHOLE generated set — header, frontmatter and body of
// all THREE files — for the committed testdata inputs. A wrong generator fails
// here rather than silently writing plausible files. Regenerate with:
//
//	UPDATE_GOLDEN=1 go test ./scripts/vaultgen/
func TestGoldenSet(t *testing.T) {
	got := mustExpectedSet(t, "testdata", testNow, testCommit)

	if os.Getenv("UPDATE_GOLDEN") == "1" {
		for _, a := range generatedSet {
			if err := os.MkdirAll(filepath.Dir(goldenSet[a.rel]), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(goldenSet[a.rel], []byte(got[a.rel]), 0o644); err != nil {
				t.Fatal(err)
			}
			t.Logf("golden updated: %s", goldenSet[a.rel])
		}
		return
	}

	for _, a := range generatedSet {
		want, err := os.ReadFile(goldenSet[a.rel])
		if err != nil {
			t.Fatalf("read golden for %s: %v", a.rel, err)
		}
		if got[a.rel] != string(want) {
			t.Errorf("golden mismatch for %s (%s)\n--- got ---\n%s\n--- want ---\n%s",
				a.rel, goldenSet[a.rel], got[a.rel], want)
		}
	}
}

// TestDeterminismApartFromGeneratedAt pins the "only the wall-clock fields vary"
// rule across the whole set: two runs on the same day differ in generated_at and
// in nothing else.
func TestDeterminismApartFromGeneratedAt(t *testing.T) {
	a := mustExpectedSet(t, "testdata", testNow, testCommit)
	b := mustExpectedSet(t, "testdata", testNow.Add(7*time.Hour+13*time.Minute), testCommit)
	for _, art := range generatedSet {
		if a[art.rel] == b[art.rel] {
			t.Errorf("%s: expected the two runs to differ in generated_at", art.rel)
		}
		if normalizeForCompare(a[art.rel]) != normalizeForCompare(b[art.rel]) {
			t.Errorf("%s: two runs on the same day must be byte-identical apart from the wall-clock fields", art.rel)
		}
	}
}

// TestHandEditRefusal pins the hash check itself: a freshly generated file
// verifies, and one with any appended byte does not.
func TestHandEditRefusal(t *testing.T) {
	content := mustExpectedSet(t, "testdata", testNow, testCommit)[projectRel]
	if err := verifyIntact(content); err != nil {
		t.Fatalf("a freshly generated file must verify intact: %v", err)
	}
	if err := verifyIntact(content + "a hand-added line\n"); err == nil {
		t.Fatal("a hand-edited file must be refused")
	}
	if err := verifyIntact("no stamp here at all\n"); err == nil {
		t.Fatal("a file without a stamp must be refused")
	}
}

// TestCheckPassesThenFails pins --check: it passes on a freshly generated set
// even when only the wall-clock fields differ, then fails as HAND-EDITED after a
// tamper to ONE file, then fails as STALE when the source commit moves.
func TestCheckPassesThenFails(t *testing.T) {
	vault := t.TempDir()
	set := mustExpectedSet(t, "testdata", testNow, testCommit)
	writeSet(t, vault, set)

	// A later generated_at (same day) must NOT be reported as a difference.
	if code := runCheck("testdata", vault, testNow.Add(time.Hour), testCommit, io.Discard); code != 0 {
		t.Errorf("check must pass when only the wall-clock fields differ, got exit %d", code)
	}

	// A hand edit of ONE file must fail, and the reason must be a hand edit that
	// names that file.
	writeFile(t, filepath.Join(vault, lessonsRel), set[lessonsRel]+"a hand-added line\n")
	var sb strings.Builder
	if code := runCheck("testdata", vault, testNow, testCommit, &sb); code == 0 {
		t.Fatal("check must fail on a hand edit")
	}
	if !strings.Contains(sb.String(), "HAND-EDITED") {
		t.Errorf("check must name the hand edit as the cause, said: %q", sb.String())
	}
	if !strings.Contains(sb.String(), lessonsRel) {
		t.Errorf("check must name the hand-edited file, said: %q", sb.String())
	}

	// A moved source commit is staleness, not a hand edit.
	writeFile(t, filepath.Join(vault, lessonsRel), set[lessonsRel])
	sb.Reset()
	if code := runCheck("testdata", vault, testNow, "deadbee", &sb); code == 0 {
		t.Fatal("check must fail when the source moved")
	}
	if !strings.Contains(sb.String(), "STALE") {
		t.Errorf("check must name staleness as the cause, said: %q", sb.String())
	}
}

// TestWriteRefusesHandEditedFile pins the loud, non-destructive refusal for EACH
// file independently: hand-editing any one of the three makes --write exit
// non-zero and leaves that file byte-for-byte untouched.
func TestWriteRefusesHandEditedFile(t *testing.T) {
	for _, a := range generatedSet {
		t.Run(a.rel, func(t *testing.T) {
			vault := t.TempDir()
			set := mustExpectedSet(t, "testdata", testNow, testCommit)
			writeSet(t, vault, set)

			edited := set[a.rel] + "a hand-added line\n"
			writeFile(t, filepath.Join(vault, a.rel), edited)

			var sb strings.Builder
			if code := runWrite("testdata", vault, testNow, testCommit, &sb); code == 0 {
				t.Fatalf("write must refuse a hand-edited %s", a.rel)
			}
			if !strings.Contains(sb.String(), "REFUSING") {
				t.Errorf("refusal must say so, said: %q", sb.String())
			}
			after, err := os.ReadFile(filepath.Join(vault, a.rel))
			if err != nil {
				t.Fatal(err)
			}
			if string(after) != edited {
				t.Errorf("write must not have overwritten the hand-edited %s", a.rel)
			}
		})
	}
}

// TestWriteAllOrNothingOnHandEdit is the packet's mandated proof of the write
// discipline: the set write is ALL-OR-NOTHING ON VALIDATION. The vault holds an
// intact-but-stale set at one commit; ONE file is hand-edited; --write is run at
// a DIFFERENT commit, so a non-refusing write would change the other two files'
// bytes. It must instead exit non-zero and leave ALL THREE byte-for-byte
// unchanged, so one hand-edited file can never take the other two with it.
func TestWriteAllOrNothingOnHandEdit(t *testing.T) {
	vault := t.TempDir()
	oldCommit := "old1111"
	set := mustExpectedSet(t, "testdata", testNow, oldCommit)
	writeSet(t, vault, set)

	edited := set[lessonsRel] + "a hand-added line\n"
	writeFile(t, filepath.Join(vault, lessonsRel), edited)

	before := readSet(t, vault)

	var sb strings.Builder
	if code := runWrite("testdata", vault, testNow, "new2222", &sb); code == 0 {
		t.Fatalf("write must refuse when one file is hand-edited, said: %q", sb.String())
	}
	after := readSet(t, vault)
	for _, a := range generatedSet {
		if after[a.rel] != before[a.rel] {
			t.Errorf("%s changed: all-or-nothing on validation was violated", a.rel)
		}
	}
	if after[lessonsRel] != edited {
		t.Errorf("the hand-edited file must be byte-for-byte untouched")
	}
}

// TestWriteCreatesWhenAbsent pins the normal path: an absent set is written and
// every file verifies intact, and --check then passes on it.
func TestWriteCreatesWhenAbsent(t *testing.T) {
	vault := t.TempDir()

	if code := runWrite("testdata", vault, testNow, testCommit, io.Discard); code != 0 {
		t.Fatalf("write must succeed on an absent set, got exit %d", code)
	}
	for rel, content := range readSet(t, vault) {
		if err := verifyIntact(content); err != nil {
			t.Errorf("written %s must verify intact: %v", rel, err)
		}
	}
	if code := runCheck("testdata", vault, testNow, testCommit, io.Discard); code != 0 {
		t.Errorf("check must pass on a freshly written set, got exit %d", code)
	}
}

// TestCheckPassesAcrossCalendarDay pins the cross-day rule across the whole set:
// a file generated on one day must still --check OK when the wall-clock fields
// are advanced by a FULL DAY, because every wall-clock field — the frontmatter's
// generated_at and source_date AND the do-not-edit comment's date — is excluded
// from the comparison.
func TestCheckPassesAcrossCalendarDay(t *testing.T) {
	vault := t.TempDir()
	writeSet(t, vault, mustExpectedSet(t, "testdata", testNow, testCommit))

	var sb strings.Builder
	if code := runCheck("testdata", vault, testNow.Add(24*time.Hour), testCommit, &sb); code != 0 {
		t.Errorf("check must pass across a calendar day (only wall-clock fields differ), got exit %d: %s", code, sb.String())
	}
}

// TestVaultRootRejected pins that an empty or relative --vault is refused: it
// must exit non-zero, name the vault usage error, and create NO file in the set.
// An empty root would otherwise resolve the targets against the working
// directory — inside the repository — breaking the "never writes a repository
// file" invariant.
func TestVaultRootRejected(t *testing.T) {
	for _, tc := range []struct{ name, vault string }{
		{"empty", ""},
		{"relative", "relative-vault"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// If a buggy build writes a file, clean it out of the checkout.
			defer func() {
				for _, a := range generatedSet {
					os.RemoveAll(filepath.Dir(filepath.Join(tc.vault, a.rel)))
				}
			}()

			var sb strings.Builder
			if code := runWrite("testdata", tc.vault, testNow, testCommit, &sb); code != 2 {
				t.Fatalf("--write with a %s --vault must exit 2, got %d, said: %q", tc.name, code, sb.String())
			}
			if !strings.Contains(strings.ToLower(sb.String()), "vault") {
				t.Errorf("refusal must name the vault usage error, said: %q", sb.String())
			}
			for _, a := range generatedSet {
				if _, err := os.Stat(filepath.Join(tc.vault, a.rel)); !os.IsNotExist(err) {
					t.Errorf("a refused --write must create no file, but %s exists", a.rel)
				}
			}

			var sb2 strings.Builder
			if code := runCheck("testdata", tc.vault, testNow, testCommit, &sb2); code != 2 {
				t.Fatalf("--check with a %s --vault must exit 2, got %d, said: %q", tc.name, code, sb2.String())
			}
		})
	}
}

// TestVaultRootInsideRepoRejected pins F1: an ABSOLUTE --vault that resolves to
// the repository root, or to a path under it, is refused with exit 2 and writes
// NOTHING. Without this, `vaultgen --write --vault "$PWD"` would write the three
// generated files INTO the checkout, breaking the "never writes a repository
// file" invariant. The ".." case proves the path is RESOLVED rather than
// compared textually.
func TestVaultRootInsideRepoRejected(t *testing.T) {
	const repoRoot = "testdata"
	absRepo, err := filepath.Abs(repoRoot)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, vault string }{
		{"equal to repo root", absRepo},
		{"under repo root", filepath.Join(absRepo, "nested", "vault")},
		{"dot-dot into repo", absRepo + "/want/../nested"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Safety net: if a buggy build writes, clean the generated leaf
			// directories out of the checkout. Never remove tc.vault itself —
			// in the "equal" case that IS the repository root.
			for _, a := range generatedSet {
				defer os.RemoveAll(filepath.Dir(filepath.Join(tc.vault, a.rel)))
			}

			var sb strings.Builder
			if code := runWrite(repoRoot, tc.vault, testNow, testCommit, &sb); code != 2 {
				t.Fatalf("--write with a vault %s (%s) must exit 2, got %d, said: %q",
					tc.name, tc.vault, code, sb.String())
			}
			if !strings.Contains(sb.String(), "vault") {
				t.Errorf("refusal must name the vault usage error, said: %q", sb.String())
			}
			for _, a := range generatedSet {
				if _, err := os.Stat(filepath.Join(tc.vault, a.rel)); !os.IsNotExist(err) {
					t.Errorf("a refused --write must create no file, but %s exists", filepath.Join(tc.vault, a.rel))
				}
			}

			var sb2 strings.Builder
			if code := runCheck(repoRoot, tc.vault, testNow, testCommit, &sb2); code != 2 {
				t.Fatalf("--check with a vault %s must exit 2, got %d, said: %q", tc.name, code, sb2.String())
			}
		})
	}
}

// TestVaultRootCaseVariantRejected pins F7: on a case-INSENSITIVE filesystem
// (macOS's default APFS), a --vault that differs from the repository root only
// by case resolves to the SAME directory, so it must still be refused. A string
// prefix check misses it; the check must decide by filesystem identity. The test
// first PROBES case-sensitivity and SKIPS on a case-sensitive volume, where the
// case-variant is a genuinely different directory and writing there is legitimate.
func TestVaultRootCaseVariantRejected(t *testing.T) {
	const repoRoot = "testdata"
	absRepo, err := filepath.Abs(repoRoot)
	if err != nil {
		t.Fatal(err)
	}
	variant := strings.ToUpper(absRepo)
	if variant == absRepo {
		t.Skip("repository path has no cased letters to vary")
	}

	// Probe: does the case-variant resolve to the SAME directory?
	vInfo, vErr := os.Stat(variant)
	rInfo, rErr := os.Stat(absRepo)
	if vErr != nil || rErr != nil || !os.SameFile(vInfo, rInfo) {
		t.Skipf("filesystem is case-sensitive here: %q does not resolve to the repository root", variant)
	}

	for _, v := range []string{variant, variant + "/nested/vault"} {
		// Safety net: if a buggy build writes, clean the generated leaf dirs.
		for _, a := range generatedSet {
			defer os.RemoveAll(filepath.Dir(filepath.Join(v, a.rel)))
		}

		var sb strings.Builder
		if code := runWrite(repoRoot, v, testNow, testCommit, &sb); code != 2 {
			t.Fatalf("--write with the case-variant vault %q must exit 2, got %d, said: %q", v, code, sb.String())
		}
		if !strings.Contains(sb.String(), "vault") {
			t.Errorf("refusal must name the vault usage error, said: %q", sb.String())
		}
		for _, a := range generatedSet {
			if _, err := os.Stat(filepath.Join(v, a.rel)); !os.IsNotExist(err) {
				t.Errorf("a refused --write must create no file, but %s exists", filepath.Join(v, a.rel))
			}
		}

		var sb2 strings.Builder
		if code := runCheck(repoRoot, v, testNow, testCommit, &sb2); code != 2 {
			t.Fatalf("--check with the case-variant vault %q must exit 2, got %d, said: %q", v, code, sb2.String())
		}
	}
}

// TestStampInCommentIsIgnored pins that only the frontmatter BETWEEN the two
// "---" delimiters supplies body_sha256, for EVERY file in the set. A hand-edited
// file that hides a MATCHING stamp in the do-not-edit comment must be refused,
// because the comment is not part of the frontmatter. A malformed or absent
// stamp must be treated as unreadable and refused too.
func TestStampInCommentIsIgnored(t *testing.T) {
	set := mustExpectedSet(t, "testdata", testNow, testCommit)
	for _, a := range generatedSet {
		content := set[a.rel]
		edited := content + "a hand-added body line\n"
		_, editedBody, ok := splitHeader(edited)
		if !ok {
			t.Fatalf("%s: test setup: no header block", a.rel)
		}
		forged := strings.Replace(edited,
			"regenerate: ./scripts/sync-vault.sh --write\n",
			"body_sha256: "+sha256Hex(editedBody)+"\nregenerate: ./scripts/sync-vault.sh --write\n", 1)
		if forged == edited {
			t.Fatalf("%s: test setup: comment marker not found", a.rel)
		}
		if err := verifyIntact(forged); err == nil {
			t.Errorf("%s: a body_sha256 in the comment must not be trusted", a.rel)
		}

		// A malformed stamp (short, uppercase, non-hex, too long) is unreadable.
		_, body, _ := splitHeader(content)
		real := sha256Hex(body)
		for _, bad := range []string{"deadbeef", strings.ToUpper(real), real[:63], real + "0"} {
			tampered := strings.Replace(content, "body_sha256: "+real, "body_sha256: "+bad, 1)
			if tampered == content {
				t.Fatalf("%s: test setup: frontmatter stamp not found for %q", a.rel, bad)
			}
			if err := verifyIntact(tampered); err == nil {
				t.Errorf("%s: malformed stamp %q must be refused", a.rel, bad)
			}
		}
	}

	// And a --write must refuse the forged file, byte-untouched.
	vault := t.TempDir()
	writeSet(t, vault, set)
	_, editedBody, _ := splitHeader(set[projectRel] + "a hand-added body line\n")
	forged := strings.Replace(set[projectRel]+"a hand-added body line\n",
		"regenerate: ./scripts/sync-vault.sh --write\n",
		"body_sha256: "+sha256Hex(editedBody)+"\nregenerate: ./scripts/sync-vault.sh --write\n", 1)
	writeFile(t, filepath.Join(vault, projectRel), forged)
	var sb strings.Builder
	if code := runWrite("testdata", vault, testNow, testCommit, &sb); code == 0 {
		t.Fatalf("write must refuse a file whose only valid stamp is in the comment, said: %q", sb.String())
	}
	after, err := os.ReadFile(filepath.Join(vault, projectRel))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != forged {
		t.Error("write must not touch a refused file")
	}
}

// TestEscapingInTableCells pins that a manifest value containing "|" is escaped
// to "\|" so it cannot split a markdown table cell. Without a fixture value
// that contains a "|", the escaping could be removed and every test still
// passed.
func TestEscapingInTableCells(t *testing.T) {
	set := mustExpectedSet(t, "testdata", testNow, testCommit)
	if !strings.Contains(set[projectRel], `a pipe \| in it`) {
		t.Errorf("a value containing '|' must be escaped to '\\|' in the table; got:\n%s", set[projectRel])
	}
}

// TestEmptySections pins the "- none" branches: a source with no ADRs renders
// the project file's ADR section as "- none", and a source with no lessons
// renders the lessons file's section as "- none".
func TestEmptySections(t *testing.T) {
	set := mustExpectedSet(t, "testdata/empty", testNow, testCommit)
	if !strings.Contains(set[projectRel], "## Architecture decisions\n\n- none\n") {
		t.Errorf("an empty ADR set must render '- none', got:\n%s", set[projectRel])
	}
	if !strings.Contains(set[lessonsRel], "## Lessons\n\n- none\n") {
		t.Errorf("an empty lessons set must render '- none', got:\n%s", set[lessonsRel])
	}
}

// TestLessonsMovedOutOfProjectFile pins item 1's content change: the project file
// no longer carries a lessons section, and the lessons content genuinely lives in
// the lessons file.
func TestLessonsMovedOutOfProjectFile(t *testing.T) {
	set := mustExpectedSet(t, "testdata", testNow, testCommit)
	if strings.Contains(set[projectRel], "## Lessons") {
		t.Error("the project file must NOT contain a lessons section")
	}
	if strings.Contains(set[projectRel], "Unknown is never zero.") {
		t.Error("the project file must not restate a lesson")
	}
	if !strings.Contains(set[lessonsRel], "## Lessons") {
		t.Error("the lessons file must contain the lessons section")
	}
	if !strings.Contains(set[lessonsRel], "Unknown is never zero.") {
		t.Error("the lessons file must carry the lessons content")
	}
}

// TestParseStatusRejects pins the closed vocabularies and the date check: a bad
// kind, a bad state and a bad date each fail with a named cause.
func TestParseStatusRejects(t *testing.T) {
	const base = "items:\n" +
		"  - id: X-1\n" +
		"    kind: slice\n" +
		"    state: merged\n" +
		"    date: 2026-01-02\n" +
		"    one_line: ok\n"
	for _, tc := range []struct {
		name string
		yaml string
		want string
	}{
		{"bad kind", strings.Replace(base, "kind: slice", "kind: sandwich", 1), "unknown kind"},
		{"bad state", strings.Replace(base, "state: merged", "state: done", 1), "unknown state"},
		{"bad date", strings.Replace(base, "date: 2026-01-02", "date: 02/01/2026", 1), "date must be YYYY-MM-DD"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseStatus([]byte(tc.yaml))
			if err == nil {
				t.Fatalf("%s must be rejected", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("%s must name the cause %q, said: %v", tc.name, tc.want, err)
			}
		})
	}
}

// TestNormalizeLeavesBodyAlone pins F5: normalization touches only the header,
// so a BODY line that happens to begin with a wall-clock key is not masked, and
// a real body difference still shows.
func TestNormalizeLeavesBodyAlone(t *testing.T) {
	a := "---\nsource_date: 2026-01-01\n---\n\ngenerated_at: not a header field\n"
	b := "---\nsource_date: 2026-02-02\n---\n\ngenerated_at: not a header field\n"
	if normalizeForCompare(a) != normalizeForCompare(b) {
		t.Error("a header wall-clock field must normalize to equal")
	}
	if normalizeForCompare(a) == a {
		t.Error("the header's source_date must have been blanked")
	}
	c := "---\nsource_date: 2026-01-01\n---\n\ngenerated_at: DIFFERENT\n"
	if normalizeForCompare(a) == normalizeForCompare(c) {
		t.Error("a body difference must NOT be masked")
	}
}

// TestCheckEnumeratesEachDifferingFileWithCause pins that --check enumerates
// EVERY differing file and names the CAUSE for each, in the exact vocabulary.
func TestCheckEnumeratesEachDifferingFileWithCause(t *testing.T) {
	vault := t.TempDir()
	set := mustExpectedSet(t, "testdata", testNow, testCommit)
	writeSet(t, vault, set)

	// (a) Every stamp names an older commit: all three files, each named STALE.
	var sb strings.Builder
	if code := runCheck("testdata", vault, testNow, "deadbee", &sb); code == 0 {
		t.Fatal("check must fail when every stamp names an older commit")
	}
	out := sb.String()
	for _, a := range generatedSet {
		if !strings.Contains(out, a.rel) {
			t.Errorf("check must enumerate %s, said:\n%s", a.rel, out)
		}
	}
	if n := strings.Count(out, "STALE"); n != len(generatedSet) {
		t.Errorf("every file must be named STALE when the commit moved, got %d STALE lines:\n%s", n, out)
	}

	// (b) A hand edit of ONE file names that file HAND-EDITED.
	writeFile(t, filepath.Join(vault, projectRel), set[projectRel]+"a hand-added line\n")
	sb.Reset()
	if code := runCheck("testdata", vault, testNow, testCommit, &sb); code == 0 {
		t.Fatal("check must fail on a hand edit")
	}
	if !strings.Contains(sb.String(), freshnessHandEdited+": "+projectRel) {
		t.Errorf("check must name %s HAND-EDITED, said:\n%s", projectRel, sb.String())
	}

	// (c) An unparseable stamp names that file UNREADABLE STAMP.
	corrupt := strings.Replace(set[projectRel], "body_sha256: ", "body_sha256: nothex", 1)
	if corrupt == set[projectRel] {
		t.Fatal("test setup: stamp not found")
	}
	writeFile(t, filepath.Join(vault, projectRel), corrupt)
	sb.Reset()
	if code := runCheck("testdata", vault, testNow, testCommit, &sb); code == 0 {
		t.Fatal("check must fail on an unreadable stamp")
	}
	if !strings.Contains(sb.String(), freshnessUnreadable+": "+projectRel) {
		t.Errorf("check must name %s UNREADABLE STAMP, said:\n%s", projectRel, sb.String())
	}
}

// TestIndexNamesNoNonexistentPathOrCommit pins the index's containment rule: it
// names exactly the generated paths, and every source_commit cell is the commit
// that file's OWN stamp names — never an invented one.
func TestIndexNamesNoNonexistentPathOrCommit(t *testing.T) {
	set := mustExpectedSet(t, "testdata", testNow, testCommit)

	rows := indexRows(t, set[indexRel])
	if len(rows) != len(generatedSet) {
		t.Fatalf("index has %d rows, want %d:\n%s", len(rows), len(generatedSet), set[indexRel])
	}
	commitCells := make(map[string]string, len(rows))
	for _, r := range rows {
		commitCells[strings.ReplaceAll(r[0], `\|`, "|")] = r[2]
	}
	for _, a := range generatedSet {
		got, ok := commitCells[a.rel]
		if !ok {
			t.Errorf("index must name %s", a.rel)
			continue
		}
		want := frontmatterField(set[a.rel], "source_commit")
		if got != want {
			t.Errorf("index names commit %q for %s, but its own stamp names %q", got, a.rel, want)
		}
	}
}

// TestIndexSelfConsistency pins that a freshly written set checks OK — the index
// never reports ITSELF as differing — that the index's own row reads OK, and
// that the index is deterministic apart from the wall-clock fields.
func TestIndexSelfConsistency(t *testing.T) {
	vault := t.TempDir()
	writeSet(t, vault, mustExpectedSet(t, "testdata", testNow, testCommit))

	var sb strings.Builder
	if code := runCheck("testdata", vault, testNow, testCommit, &sb); code != 0 {
		t.Fatalf("a freshly written set must check OK, including the index, got exit %d:\n%s", code, sb.String())
	}

	idx, err := os.ReadFile(filepath.Join(vault, indexRel))
	if err != nil {
		t.Fatal(err)
	}
	if got := rowFreshness(t, string(idx), indexRel); got != freshnessOK {
		t.Errorf("the index's own row must read OK, got %q", got)
	}

	a := mustExpectedSet(t, "testdata", testNow, testCommit)
	b := mustExpectedSet(t, "testdata", testNow.Add(3*time.Hour), testCommit)
	if normalizeForCompare(a[indexRel]) != normalizeForCompare(b[indexRel]) {
		t.Error("the index must be deterministic apart from the wall-clock fields")
	}
}

// TestIndexReportsEachFreshnessValue pins the freshness vocabulary: the index
// emits exactly OK / STALE / HAND-EDITED / UNREADABLE STAMP, one per scenario.
func TestIndexReportsEachFreshnessValue(t *testing.T) {
	set := mustExpectedSet(t, "testdata", testNow, testCommit)
	all := func(overrides map[string]string, commit string) []fileFact {
		contents := map[string]string{
			projectRel: set[projectRel],
			lessonsRel: set[lessonsRel],
			indexRel:   set[indexRel],
		}
		for rel, c := range overrides {
			if c == "" {
				delete(contents, rel)
				continue
			}
			contents[rel] = c
		}
		return diskFacts(contents, commit)
	}

	if got := rowFreshness(t, renderIndexBody(writtenFacts(testCommit)), projectRel); got != freshnessOK {
		t.Errorf("a fresh file must read %q, got %q", freshnessOK, got)
	}
	if got := rowFreshness(t, renderIndexBody(all(nil, "deadbee")), projectRel); got != freshnessStale {
		t.Errorf("an older-commit stamp must read %q, got %q", freshnessStale, got)
	}
	if got := rowFreshness(t, renderIndexBody(all(map[string]string{projectRel: set[projectRel] + "hand edit\n"}, testCommit)), projectRel); got != freshnessHandEdited {
		t.Errorf("a body_sha256 mismatch must read %q, got %q", freshnessHandEdited, got)
	}
	if got := rowFreshness(t, renderIndexBody(all(map[string]string{projectRel: "no stamp here\n"}, testCommit)), projectRel); got != freshnessUnreadable {
		t.Errorf("an unparseable stamp must read %q, got %q", freshnessUnreadable, got)
	}
	if got := rowFreshness(t, renderIndexBody(all(map[string]string{projectRel: ""}, testCommit)), projectRel); got != freshnessUnreadable {
		t.Errorf("a missing file must read %q, got %q", freshnessUnreadable, got)
	}
}

// --- small test helpers -----------------------------------------------------

// indexRows extracts the index body's data rows: four cells each, in order
// path / source / source_commit / freshness.
func indexRows(t *testing.T, idx string) [][4]string {
	t.Helper()
	var rows [][4]string
	for _, line := range strings.Split(idx, "\n") {
		if !strings.HasPrefix(line, "| ") {
			continue
		}
		cells := strings.Split(strings.Trim(strings.TrimSpace(line), "|"), "|")
		for i := range cells {
			cells[i] = strings.TrimSpace(cells[i])
		}
		if len(cells) != 4 || cells[0] == "path" || cells[0] == "---" {
			continue
		}
		rows = append(rows, [4]string{cells[0], cells[1], cells[2], cells[3]})
	}
	return rows
}

// rowFreshness returns the freshness cell for one path in an index.
func rowFreshness(t *testing.T, idx, rel string) string {
	t.Helper()
	for _, r := range indexRows(t, idx) {
		if strings.ReplaceAll(r[0], `\|`, "|") == rel {
			return r[3]
		}
	}
	t.Fatalf("no index row for %s in:\n%s", rel, idx)
	return ""
}

func writeSet(t *testing.T, vault string, set map[string]string) {
	t.Helper()
	for _, a := range generatedSet {
		writeFile(t, filepath.Join(vault, a.rel), set[a.rel])
	}
}

func readSet(t *testing.T, vault string) map[string]string {
	t.Helper()
	out := make(map[string]string, len(generatedSet))
	for _, a := range generatedSet {
		data, err := os.ReadFile(filepath.Join(vault, a.rel))
		if err != nil {
			t.Fatalf("read %s: %v", a.rel, err)
		}
		out[a.rel] = string(data)
	}
	return out
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
