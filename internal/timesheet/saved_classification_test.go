package timesheet

import "testing"

func TestSavedClassificationDigestBindsSavedContent(t *testing.T) {
	base := Interval{ID: "iv-1", Date: "2026-10-26", Start: "09:00", End: "10:00", Project: "WORK", Description: "Build bridge"}
	digest := SavedClassificationDigest(base.Date, 3, []Interval{base})
	for _, tc := range []struct {
		name string
		edit func(*Interval)
	}{
		{"identity", func(iv *Interval) { iv.ID = "iv-2" }},
		{"date", func(iv *Interval) { iv.Date = "2026-10-25" }},
		{"start", func(iv *Interval) { iv.Start = "09:30" }},
		{"end", func(iv *Interval) { iv.End = "10:30" }},
		{"description", func(iv *Interval) { iv.Description = "Review bridge" }},
		{"project", func(iv *Interval) { iv.Project = "OTHER" }},
		{"catalog", func(iv *Interval) { iv.RemoteCatalogID = "cat-1" }},
		{"selection", func(iv *Interval) { iv.RemoteClassificationID = "cls-dev" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := base
			tc.edit(&changed)
			if got := SavedClassificationDigest(base.Date, 3, []Interval{changed}); got == digest {
				t.Fatal("changed saved content retained the old answer binding")
			}
		})
	}
	if SavedClassificationDigest(base.Date, 4, []Interval{base}) == digest {
		t.Fatal("changed revision retained the old answer binding")
	}
	if SavedClassificationDigest("2026-10-27", 3, []Interval{base}) == digest {
		t.Fatal("changed requested day retained the old answer binding")
	}
	if SavedClassificationDigest(base.Date, 3, nil) == digest {
		t.Fatal("removed interval retained the old answer binding")
	}
}

func TestSavedClassificationDigestIgnoresIntervalOrderWithoutMutation(t *testing.T) {
	first := Interval{ID: "iv-1", Description: "first"}
	second := Interval{ID: "iv-2", Description: "second"}
	input := []Interval{second, first}
	got := SavedClassificationDigest("2026-10-26", 1, input)
	if got != SavedClassificationDigest("2026-10-26", 1, []Interval{first, second}) {
		t.Fatal("interval ordering changed the saved answer binding")
	}
	if input[0].ID != second.ID {
		t.Fatal("digest reordered the caller's intervals")
	}
}
