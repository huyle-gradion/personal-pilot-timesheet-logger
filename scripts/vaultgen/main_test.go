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

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
