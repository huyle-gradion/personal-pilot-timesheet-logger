package run

import (
	"testing"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/evidence"
)

// S8L1-7: Collect merges evidence into the record (CollectedEvidence upsert +
// legacy references + collection summary), and a re-collection upserts.
func TestCollect_MergesEvidence(t *testing.T) {
	store := newStore(t)
	date := "2026-10-06"
	reg := evidence.NewRegistry()
	plug := &fakePlugin{kind: evidence.SourceLocalGit, ev: []evidence.Evidence{gitEvidence("e1", "acme/repo", "fix parser")}}
	reg.Register(plug)
	sources := []evidence.SourceConfig{{ID: "git", Type: evidence.SourceLocalGit, Enabled: true}}
	r := newRunner(t, Deps{Store: store, Registry: reg}, Config{Evidence: evidence.Config{Timezone: "UTC", Sources: sources}})

	res, err := r.Collect(ctx, date)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(res.Evidence) != 1 || res.MergedCount != 1 {
		t.Fatalf("unexpected result: %+v", res)
	}
	if plug.liveCalls != 1 {
		t.Fatalf("want one live collect, got %d", plug.liveCalls)
	}

	rec, err := store.GetDay(ctx, date)
	if err != nil {
		t.Fatalf("GetDay: %v", err)
	}
	if len(rec.CollectedEvidence) != 1 || rec.CollectedEvidence[0].ID != "e1" {
		t.Fatalf("want collected evidence, got %+v", rec.CollectedEvidence)
	}
	if rec.Collection == nil || !rec.Collection.Complete {
		t.Fatalf("want a complete collection summary, got %+v", rec.Collection)
	}
	if len(rec.Evidence) != 1 || rec.Evidence[0].ID != "e1" {
		t.Fatalf("want a legacy evidence reference, got %+v", rec.Evidence)
	}
	if rec.Evidence[0].Source != string(evidence.SourceLocalGit) {
		t.Errorf("want the source kind on the legacy ref, got %q", rec.Evidence[0].Source)
	}

	// A second collection upserts rather than duplicating.
	if _, err := r.Collect(ctx, date); err != nil {
		t.Fatalf("Collect 2: %v", err)
	}
	rec2, _ := store.GetDay(ctx, date)
	if len(rec2.CollectedEvidence) != 1 {
		t.Fatalf("a re-collection must upsert collected evidence, got %d", len(rec2.CollectedEvidence))
	}
	if len(rec2.Evidence) != 1 {
		t.Fatalf("legacy refs must upsert, got %d", len(rec2.Evidence))
	}
}
