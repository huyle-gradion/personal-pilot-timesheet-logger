package timesheet

import "testing"

func TestWorkflowPlanCurrentTracksExactContributionRevisions(t *testing.T) {
	const prior = "2026-10-26"
	for _, change := range []string{"unchanged", "origin revised", "origin removed", "origin added", "unrelated revised"} {
		t.Run(change, func(t *testing.T) {
			store, path := snapshotStore(t)
			state := store.State()
			origin := snapshotDay(prior)
			origin.Confirmed[0].Start, origin.Confirmed[0].End = "23:00", "01:00"
			state.Days = map[string]*Day{snapshotDate: snapshotDay(snapshotDate), prior: origin}
			state.Plans = map[string]*PlanRecord{
				"ready": {PlanID: "ready", Date: snapshotDate, State: PlanReady, CreatedAt: "2026-10-27T10:00:00Z",
					SourceRevisions: map[string]int{snapshotDate: 2, prior: 2}},
			}
			want := false
			switch change {
			case "unchanged":
				want = true
			case "origin revised":
				origin.Revision++
			case "origin removed":
				delete(state.Days, prior)
			case "origin added":
				delete(state.Plans["ready"].SourceRevisions, prior)
			case "unrelated revised":
				state.Days["2026-10-20"] = snapshotDay("2026-10-20")
				state.Days["2026-10-20"].Revision = 99
				want = true
			}
			if err := store.CommitState(state); err != nil {
				t.Fatal(err)
			}
			got, err := ReadWorkflowSnapshot(snapshotConfig(path), store, snapshotDate)
			if err != nil {
				t.Fatal(err)
			}
			if got.PlanCurrent != want {
				t.Fatalf("PlanCurrent = %v, want %v for %s", got.PlanCurrent, want, change)
			}
		})
	}
}
