package metrics

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestStore_DuplicateImportIsNoOp asserts a reimport of an unchanged transcript
// does not rewrite the run file and that re-aggregation is byte-identical.
func TestStore_DuplicateImportIsNoOp(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	ctx := context.Background()

	rec, err := Import(ctx, "testdata/pi_partial_offline.jsonl", ImportOptions{Date: "2026-10-11"})
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	wrote, err := store.WriteRun(rec)
	if err != nil || !wrote {
		t.Fatalf("first write: wrote=%v err=%v", wrote, err)
	}
	runPath := filepath.Join(dir, "runs", sanitizeMust(t, rec.RunID)+".json")
	firstRun, err := os.ReadFile(runPath)
	if err != nil {
		t.Fatalf("read run: %v", err)
	}

	wrote, err = store.WriteRun(rec)
	if err != nil || wrote {
		t.Fatalf("second write: wrote=%v err=%v (want no-op)", wrote, err)
	}
	secondRun, _ := os.ReadFile(runPath)
	if !bytes.Equal(firstRun, secondRun) {
		t.Fatal("reimport rewrote the run file")
	}

	// Aggregate twice: the file must be byte-identical.
	agg1, err := store.Aggregate(ctx, "2026-10-11", nil)
	if err != nil {
		t.Fatalf("aggregate 1: %v", err)
	}
	aggPath := filepath.Join(dir, "2026-10-11.json")
	firstAgg, _ := os.ReadFile(aggPath)
	agg2, err := store.Aggregate(ctx, "2026-10-11", nil)
	if err != nil {
		t.Fatalf("aggregate 2: %v", err)
	}
	secondAgg, _ := os.ReadFile(aggPath)
	if !bytes.Equal(firstAgg, secondAgg) {
		t.Fatal("re-aggregation is not byte-identical")
	}
	b1, _ := json.Marshal(agg1)
	b2, _ := json.Marshal(agg2)
	if !bytes.Equal(b1, b2) {
		t.Fatal("aggregate values differ across runs")
	}
}

// TestStore_ChangedContentRewrites asserts a changed content hash rewrites the
// run file.
func TestStore_ChangedContentRewrites(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewStore(dir)
	rec := RunRecord{RunID: "run-x", ContentHash: "hash-a", Date: "2026-10-11", Provenance: ProvenanceObserved}
	if wrote, err := store.WriteRun(rec); err != nil || !wrote {
		t.Fatalf("write a: %v %v", wrote, err)
	}
	rec.ContentHash = "hash-b"
	if wrote, err := store.WriteRun(rec); err != nil || !wrote {
		t.Fatalf("write b: %v %v", wrote, err)
	}
	got, err := store.ReadRun("run-x")
	if err != nil || got.ContentHash != "hash-b" {
		t.Fatalf("read: %+v %v", got, err)
	}
}

// TestImport_NewSessionIsNewRun asserts a new session produces a distinct
// RunRecord (same date, distinct RunID).
func TestImport_NewSessionIsNewRun(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.jsonl")
	b := filepath.Join(dir, "b.jsonl")
	writeSession(t, a, "session-a")
	writeSession(t, b, "session-b")

	ra, err := Import(context.Background(), a, ImportOptions{Date: "2026-10-11"})
	if err != nil {
		t.Fatalf("import a: %v", err)
	}
	rb, err := Import(context.Background(), b, ImportOptions{Date: "2026-10-11"})
	if err != nil {
		t.Fatalf("import b: %v", err)
	}
	if ra.RunID == rb.RunID {
		t.Fatalf("distinct sessions share run id %q", ra.RunID)
	}
	if ra.Date != rb.Date {
		t.Fatalf("dates differ: %q %q", ra.Date, rb.Date)
	}
}

func writeSession(t *testing.T, path, sessionID string) {
	t.Helper()
	content := `{"type":"session","id":"` + sessionID + `","cwd":"/repo","timestamp":"2026-10-11T05:15:03.251Z","version":3}` + "\n" +
		`{"type":"message","message":{"role":"assistant","timestamp":1791695713126,"content":[{"type":"toolCall","name":"bash","arguments":{"command":"timesheet review"}}],"usage":{"input":10,"output":1,"cacheRead":0,"cacheWrite":0,"reasoning":0,"totalTokens":11}}}` + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write session: %v", err)
	}
}

func sanitizeMust(t *testing.T, id string) string {
	t.Helper()
	s, err := sanitizeRunID(id)
	if err != nil {
		t.Fatalf("sanitize: %v", err)
	}
	return s
}

// TestSanitizeRunID_NoTruncationCollision (F8b) asserts an over-long id is
// replaced by a fixed-length sha256, never lossily truncated.
func TestSanitizeRunID_NoTruncationCollision(t *testing.T) {
	long1 := strings.Repeat("a", 200)
	long2 := strings.Repeat("a", 128) + strings.Repeat("b", 50) // shares the first 128 chars
	s1, err := sanitizeRunID(long1)
	if err != nil {
		t.Fatalf("sanitize long1: %v", err)
	}
	s2, err := sanitizeRunID(long2)
	if err != nil {
		t.Fatalf("sanitize long2: %v", err)
	}
	if s1 == s2 {
		t.Fatalf("over-long ids collided after truncation: %q", s1)
	}
	if !strings.HasPrefix(s1, "sha256-") {
		t.Fatalf("over-long id not hashed: %q", s1)
	}
	if len(s1) > MaxRunIDLen {
		t.Fatalf("hashed id length %d exceeds cap %d", len(s1), MaxRunIDLen)
	}
	// A short id is sanitized as-is.
	short, err := sanitizeRunID("session-abc")
	if err != nil || short != "session-abc" {
		t.Fatalf("short id = %q err=%v", short, err)
	}
}
