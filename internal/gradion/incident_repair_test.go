package gradion

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/workflow"
)

// TestRemoteState_ContentObservedOmitempty proves the ADDITIVE ContentObserved
// field is omitempty: a false/legacy value marshals byte-identically (no
// content_observed key), so workflow.PlanHash is unchanged for a legacy plan that
// carries ExpectedRemote. A true value is serialized.
func TestRemoteState_ContentObservedOmitempty(t *testing.T) {
	legacy := workflow.RemoteState{
		RemoteID: "r1", Date: "2026-10-06", StartTime: "09:00", EndTime: "10:00",
		ClassificationID: "c1", Description: "work",
	}
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatalf("marshal legacy state: %v", err)
	}
	if strings.Contains(string(data), "content_observed") {
		t.Fatalf("a false/legacy ContentObserved must be omitted, got %s", data)
	}

	observed := legacy
	observed.ContentObserved = true
	data, err = json.Marshal(observed)
	if err != nil {
		t.Fatalf("marshal observed state: %v", err)
	}
	if !strings.Contains(string(data), `"content_observed":true`) {
		t.Fatalf("a true ContentObserved must be serialized, got %s", data)
	}
}

// TestGradion_NoProductionDebugDumps is a TARGETED regression guard for the
// incident's specific failure mode: an env-gated debug dump of the raw write
// payload to stderr. It is deliberately NARROW — it scans only the non-test
// .go files in THIS (top-level internal/gradion) package directory for a fixed
// set of forbidden substrings. LIMITS: it is a lexical scan, not a data-flow
// analysis; it does not follow calls into other packages, does not detect an
// indirect/dynamic write, and does not cover subpackages (there are none here).
// It is a regression tripwire, not a general guarantee. Test files are exempt
// (they may print via testing).
func TestGradion_NoProductionDebugDumps(t *testing.T) {
	forbidden := []string{
		"os.Getenv",
		"os.Stderr",
		"os.Stdout",
		"fmt.Fprint",
		"fmt.Fprintf",
		"fmt.Fprintln",
		"log.Print",
		"log.Printf",
		"log.Println",
	}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	checked := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		checked++
		src := string(data)
		for _, f := range forbidden {
			if strings.Contains(src, f) {
				t.Errorf("%s: production code must not contain %q (env-gated debug dumps of the payload are forbidden)", name, f)
			}
		}
	}
	if checked == 0 {
		t.Fatal("guard checked no production files")
	}
}

// TestContentObserved_DoesNotFeedFingerprintOrPlanHash proves the ADDITIVE field
// does not change LocalFingerprint, and that a legacy plan (ExpectedRemote
// ContentObserved false) hashes identically while an observed expectation is a
// distinct plan (as intended).
func TestContentObserved_DoesNotFeedFingerprintOrPlanHash(t *testing.T) {
	rec := workflow.Record{
		SchemaVersion: workflow.SchemaVersion,
		Date:          "2026-10-06",
		Confirmed: []workflow.ConfirmedInterval{{
			ID: "c1", Date: "2026-10-06", StartTime: "09:00", EndTime: "10:00", ClassificationID: "cls-1",
		}},
		RemoteMappings: []workflow.RemoteMapping{{
			LocalID: "c1", RemoteID: "r1", Date: "2026-10-06", StartTime: "09:00", EndTime: "10:00",
			ClassificationID: "cls-1",
			LastObserved:     workflow.RemoteState{RemoteID: "r1"},
		}},
	}
	f1 := workflow.LocalFingerprint(rec)
	rec.RemoteMappings[0].LastObserved.ContentObserved = true
	if workflow.LocalFingerprint(rec) != f1 {
		t.Error("ContentObserved must not feed LocalFingerprint")
	}

	legacy := &workflow.RemoteState{RemoteID: "r1", ContentObserved: false}
	plan := workflow.SubmissionPlan{
		Date: "2026-10-06", LocalFingerprint: "fp", RemoteComplete: true,
		Operations: []workflow.PlanOperation{{
			LocalID: "c1", Kind: workflow.PlanOpEdit, RemoteID: "r1", ExpectedRemote: legacy,
		}},
	}
	h1 := workflow.PlanHash(plan)

	observed := &workflow.RemoteState{RemoteID: "r1", ContentObserved: true}
	observedPlan := plan
	observedPlan.Operations = []workflow.PlanOperation{{
		LocalID: "c1", Kind: workflow.PlanOpEdit, RemoteID: "r1", ExpectedRemote: observed,
	}}
	if workflow.PlanHash(observedPlan) == h1 {
		t.Error("an observed expectation must be a distinct plan (the field is serialized when true)")
	}
}
