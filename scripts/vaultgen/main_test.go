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

const (
	testCommit    = "abc1234"
	goldenRelPath = "testdata/want/assignment-1 (generated).md"
)

// TestGenerateGolden pins the WHOLE generated file — header, frontmatter and
// body — for the committed testdata inputs. A wrong generator fails here rather
// than silently writing a plausible file. Regenerate the golden with:
//
//	UPDATE_GOLDEN=1 go test ./scripts/vaultgen/
func TestGenerateGolden(t *testing.T) {
	got, err := generate("testdata", testNow, testCommit)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll(filepath.Dir(goldenRelPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(goldenRelPath, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("golden updated: %s", goldenRelPath)
		return
	}

	want, err := os.ReadFile(goldenRelPath)
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	if got != string(want) {
		t.Errorf("golden mismatch for %s\n--- got ---\n%s\n--- want ---\n%s", goldenRelPath, got, want)
	}
}

// TestDeterminismApartFromGeneratedAt pins the "only generated_at varies" rule:
// two runs on the same day differ in generated_at and in nothing else.
func TestDeterminismApartFromGeneratedAt(t *testing.T) {
	a, err := generate("testdata", testNow, testCommit)
	if err != nil {
		t.Fatalf("generate a: %v", err)
	}
	b, err := generate("testdata", testNow.Add(7*time.Hour+13*time.Minute), testCommit)
	if err != nil {
		t.Fatalf("generate b: %v", err)
	}
	if a == b {
		t.Fatal("expected the two runs to differ in generated_at")
	}
	if normalizeForCompare(a) != normalizeForCompare(b) {
		t.Error("two runs on the same day must be byte-identical apart from generated_at (and source_date)")
	}
}

// TestHandEditRefusal pins the hash check itself: a freshly generated file
// verifies, and one with any appended byte does not.
func TestHandEditRefusal(t *testing.T) {
	content, err := generate("testdata", testNow, testCommit)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
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

// TestCheckPassesThenFails pins --check: it passes on a freshly generated file
// even when only generated_at differs, then fails as a HAND EDIT after a
// tamper, then fails as STALE when the source commit moves.
func TestCheckPassesThenFails(t *testing.T) {
	vault := t.TempDir()
	target := filepath.Join(vault, generatedRel)

	content, err := generate("testdata", testNow, testCommit)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	writeFile(t, target, content)

	// A later generated_at (same day) must NOT be reported as a difference.
	if code := runCheck("testdata", vault, testNow.Add(time.Hour), testCommit, io.Discard); code != 0 {
		t.Errorf("check must pass when only generated_at differs, got exit %d", code)
	}

	// A hand edit must fail, and the reason must be a hand edit.
	writeFile(t, target, content+"a hand-added line\n")
	var sb strings.Builder
	if code := runCheck("testdata", vault, testNow, testCommit, &sb); code == 0 {
		t.Fatal("check must fail on a hand edit")
	}
	if !strings.Contains(sb.String(), "HAND EDIT") {
		t.Errorf("check must name the hand edit as the cause, said: %q", sb.String())
	}

	// A moved source commit is staleness, not a hand edit.
	writeFile(t, target, content)
	sb.Reset()
	if code := runCheck("testdata", vault, testNow, "deadbee", &sb); code == 0 {
		t.Fatal("check must fail when the source moved")
	}
	if !strings.Contains(sb.String(), "STALE") {
		t.Errorf("check must name staleness as the cause, said: %q", sb.String())
	}
}

// TestWriteRefusesHandEditedFile pins the loud, non-destructive refusal: a
// hand-edited target is not overwritten and --write exits non-zero.
func TestWriteRefusesHandEditedFile(t *testing.T) {
	vault := t.TempDir()
	target := filepath.Join(vault, generatedRel)

	content, err := generate("testdata", testNow, testCommit)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	edited := content + "a hand-added line\n"
	writeFile(t, target, edited)

	var sb strings.Builder
	if code := runWrite("testdata", vault, testNow, testCommit, &sb); code == 0 {
		t.Fatal("write must refuse a hand-edited file")
	}
	if !strings.Contains(sb.String(), "REFUSING") {
		t.Errorf("refusal must say so, said: %q", sb.String())
	}
	after, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != edited {
		t.Fatal("write must not have overwritten the hand-edited file")
	}
}

// TestWriteCreatesWhenAbsent pins the normal path: absent target is written and
// verifies intact.
func TestWriteCreatesWhenAbsent(t *testing.T) {
	vault := t.TempDir()
	target := filepath.Join(vault, generatedRel)

	if code := runWrite("testdata", vault, testNow, testCommit, io.Discard); code != 0 {
		t.Fatalf("write must succeed on an absent target, got exit %d", code)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyIntact(string(data)); err != nil {
		t.Fatalf("written file must verify intact: %v", err)
	}
}

// TestCheckPassesAcrossCalendarDay pins the cross-day rule: a file generated on
// one day must still --check OK when the wall-clock fields are advanced by a
// FULL DAY, because every wall-clock field is excluded from the comparison —
// the frontmatter's generated_at and source_date AND the do-not-edit comment's
// date. (The older determinism test only moves time within the same day, so it
// never exercised the comment's date.)
func TestCheckPassesAcrossCalendarDay(t *testing.T) {
	vault := t.TempDir()
	target := filepath.Join(vault, generatedRel)

	content, err := generate("testdata", testNow, testCommit)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	writeFile(t, target, content)

	var sb strings.Builder
	if code := runCheck("testdata", vault, testNow.Add(24*time.Hour), testCommit, &sb); code != 0 {
		t.Errorf("check must pass across a calendar day (only wall-clock fields differ), got exit %d: %s", code, sb.String())
	}
}

// TestVaultRootRejected pins that an empty or relative --vault is refused: it
// must exit non-zero, name the vault usage error, and create NO file. An empty
// root would otherwise resolve the target against the working directory —
// inside the repository — breaking the "never writes a repository file"
// invariant.
func TestVaultRootRejected(t *testing.T) {
	for _, tc := range []struct{ name, vault string }{
		{"empty", ""},
		{"relative", "relative-vault"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			joined := filepath.Join(tc.vault, generatedRel)
			// If a buggy build writes the file, clean it out of the checkout.
			defer os.RemoveAll(filepath.Dir(joined))

			var sb strings.Builder
			if code := runWrite("testdata", tc.vault, testNow, testCommit, &sb); code == 0 {
				t.Fatalf("--write with a %s --vault must be refused, said: %q", tc.name, sb.String())
			}
			if !strings.Contains(strings.ToLower(sb.String()), "vault") {
				t.Errorf("refusal must name the vault usage error, said: %q", sb.String())
			}
			if _, err := os.Stat(joined); !os.IsNotExist(err) {
				t.Errorf("a refused --write must create no file, but %s exists", joined)
			}

			var sb2 strings.Builder
			if code := runCheck("testdata", tc.vault, testNow, testCommit, &sb2); code == 0 {
				t.Fatalf("--check with a %s --vault must be refused, said: %q", tc.name, sb2.String())
			}
		})
	}
}

// TestStampInCommentIsIgnored pins that only the frontmatter BETWEEN the two
// "---" delimiters supplies body_sha256. A hand-edited file that hides a
// MATCHING stamp in the do-not-edit comment must be refused, byte-untouched,
// because the comment is not part of the frontmatter. A malformed or absent
// stamp must be treated as unreadable and refused too.
func TestStampInCommentIsIgnored(t *testing.T) {
	content, err := generate("testdata", testNow, testCommit)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	// A hand edit: append a body line, then drop a stamp that MATCHES the edited
	// body into the comment. The real frontmatter stamp is now stale, but the
	// comment's copy matches — which is exactly what used to be trusted.
	edited := content + "a hand-added body line\n"
	_, editedBody, ok := splitHeader(edited)
	if !ok {
		t.Fatal("test setup: no header block")
	}
	forged := strings.Replace(edited,
		"regenerate: ./scripts/sync-vault.sh --write\n",
		"body_sha256: "+sha256Hex(editedBody)+"\nregenerate: ./scripts/sync-vault.sh --write\n", 1)
	if forged == edited {
		t.Fatal("test setup: comment marker not found")
	}
	if err := verifyIntact(forged); err == nil {
		t.Error("a body_sha256 in the comment must not be trusted")
	}

	vault := t.TempDir()
	target := filepath.Join(vault, generatedRel)
	writeFile(t, target, forged)
	var sb strings.Builder
	if code := runWrite("testdata", vault, testNow, testCommit, &sb); code == 0 {
		t.Fatalf("write must refuse a file whose only valid stamp is in the comment, said: %q", sb.String())
	}
	after, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != forged {
		t.Error("write must not touch a refused file")
	}

	// A malformed stamp (short, uppercase, non-hex, too long) is unreadable.
	_, body, _ := splitHeader(content)
	real := sha256Hex(body)
	for _, bad := range []string{"deadbeef", strings.ToUpper(real), real[:63], real + "0"} {
		tampered := strings.Replace(content, "body_sha256: "+real, "body_sha256: "+bad, 1)
		if tampered == content {
			t.Fatalf("test setup: frontmatter stamp not found for %q", bad)
		}
		if err := verifyIntact(tampered); err == nil {
			t.Errorf("malformed stamp %q must be refused", bad)
		}
	}
}

// TestEscapingInTableCells pins that a manifest value containing "|" is escaped
// to "\|" so it cannot split a markdown table cell. Without a fixture value
// that contains a "|", the escaping could be removed and every test still
// passed.
func TestEscapingInTableCells(t *testing.T) {
	got, err := generate("testdata", testNow, testCommit)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if !strings.Contains(got, `a pipe \| in it`) {
		t.Errorf("a value containing '|' must be escaped to '\\|' in the table; got:\n%s", got)
	}
}

// TestEmptySections pins the "- none" branches: a source with no lessons and no
// ADRs must render both sections as "- none" rather than an empty or missing
// list.
func TestEmptySections(t *testing.T) {
	got, err := generate("testdata/empty", testNow, testCommit)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if !strings.Contains(got, "## Architecture decisions\n\n- none\n") {
		t.Errorf("an empty ADR set must render '- none', got:\n%s", got)
	}
	if !strings.Contains(got, "## Lessons\n\n- none\n") {
		t.Errorf("an empty lessons set must render '- none', got:\n%s", got)
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

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
