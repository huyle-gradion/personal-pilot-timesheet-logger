package plugins

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/evidence"
)

const fixtureDoc = `{
  "evidence": [
    {
      "kind": "calendar",
      "title": "Standup",
      "start": "2026-10-08T09:00:00Z",
      "end": "2026-10-08T09:15:00Z",
      "status": "cancelled",
      "invitation": "declined",
      "identity_parts": ["primary", "uid-1", "2026-10-08T09:00:00Z"]
    },
    {
      "kind": "local_git",
      "title": "commit abc123",
      "identity_parts": ["owner/repo", "abc123"],
      "repository": "owner/repo",
      "branch": "feature/x"
    },
    {
      "kind": "agent_session",
      "title": "agent finish",
      "invitation": "unknown",
      "identity_parts": ["session-1", "finish", "2026-10-08T17:00:00Z"]
    }
  ]
}`

func writeFixture(t *testing.T, doc string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fixture.json")
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

func fixtureSource(path string) evidence.SourceConfig {
	return evidence.SourceConfig{
		ID:      "fix",
		Type:    TypeFixture,
		Enabled: true,
		Config:  map[string]string{ConfigKeyPath: path},
	}
}

// TestFixture_EndToEnd proves a registered fixture plugin emits contract evidence
// and that a cancelled event and a declined invitation are RETAINED with status
// (never dropped at the contract layer).
func TestFixture_EndToEnd(t *testing.T) {
	reg := evidence.NewRegistry()
	Register(reg)

	ev, summary, err := reg.CollectDay(context.Background(),
		[]evidence.SourceConfig{fixtureSource(writeFixture(t, fixtureDoc))},
		evidence.CollectRequest{Date: "2026-10-08"})
	if err != nil {
		t.Fatalf("CollectDay returned error: %v", err)
	}
	if !summary.Complete {
		t.Fatalf("expected complete collection, got %+v", summary)
	}
	if len(ev) != 3 {
		t.Fatalf("expected 3 evidence items, got %d", len(ev))
	}

	var cancelled, declined, unknownRetained, allDayDurationUnknown bool
	for _, e := range ev {
		if e.Kind == evidence.SourceCalendar && e.Status == evidence.EventStatusCancelled {
			cancelled = true
		}
		if e.Invitation == evidence.InvitationDeclined {
			declined = true
		}
		if e.Invitation == evidence.InvitationUnknown {
			unknownRetained = true
		}
		if e.Kind == evidence.SourceLocalGit && e.Duration.IsUnknown() {
			allDayDurationUnknown = true
		}
	}
	if !cancelled {
		t.Error("cancelled event was not retained with its status")
	}
	if !declined {
		t.Error("declined invitation was not retained")
	}
	if !unknownRetained {
		t.Error("unknown invitation was not retained (must not default to accepted)")
	}
	if !allDayDurationUnknown {
		t.Error("commit evidence should carry an unknown duration, not zero known minutes")
	}
}

// TestFixture_IdenticalRunStableIDs proves two runs over the same fixture yield
// the same ID set, so duplicate recollection is stable.
func TestFixture_IdenticalRunStableIDs(t *testing.T) {
	reg := evidence.NewRegistry()
	Register(reg)
	src := fixtureSource(writeFixture(t, fixtureDoc))

	first, _, err := reg.CollectDay(context.Background(), []evidence.SourceConfig{src}, evidence.CollectRequest{Date: "2026-10-08"})
	if err != nil {
		t.Fatalf("first CollectDay failed: %v", err)
	}
	second, _, err := reg.CollectDay(context.Background(), []evidence.SourceConfig{src}, evidence.CollectRequest{Date: "2026-10-08"})
	if err != nil {
		t.Fatalf("second CollectDay failed: %v", err)
	}

	if len(first) != len(second) {
		t.Fatalf("evidence count changed between runs: %d vs %d", len(first), len(second))
	}
	ids := map[string]int{}
	for _, e := range first {
		ids[e.ID]++
	}
	for _, e := range second {
		ids[e.ID]--
	}
	for id, delta := range ids {
		if delta != 0 {
			t.Errorf("evidence ID %q not stable across runs", id)
		}
	}
}

// TestFixture_MissingPathFailsCheck proves a missing fixture path fails the
// contract check and yields an incomplete source.
func TestFixture_MissingPathFailsCheck(t *testing.T) {
	reg := evidence.NewRegistry()
	Register(reg)

	_, summary, err := reg.CollectDay(context.Background(),
		[]evidence.SourceConfig{fixtureSource(filepath.Join(t.TempDir(), "nope.json"))},
		evidence.CollectRequest{Date: "2026-10-08"})
	if err != nil {
		t.Fatalf("CollectDay returned error: %v", err)
	}
	if summary.Sources[0].Status != evidence.StatusIncomplete {
		t.Errorf("expected incomplete for a missing fixture path, got %+v", summary.Sources[0])
	}
}

// TestFixture_VerifyContractsDeterministic proves the fixture passes the
// contract path (fixed-input determinism) as a complete source.
func TestFixture_VerifyContractsDeterministic(t *testing.T) {
	reg := evidence.NewRegistry()
	Register(reg)

	results, err := reg.VerifyContracts(context.Background(),
		[]evidence.SourceConfig{fixtureSource(writeFixture(t, fixtureDoc))})
	if err != nil {
		t.Fatalf("VerifyContracts returned error: %v", err)
	}
	if len(results) != 1 || results[0].Status != evidence.StatusComplete {
		t.Fatalf("expected one complete contract result, got %+v", results)
	}
}
