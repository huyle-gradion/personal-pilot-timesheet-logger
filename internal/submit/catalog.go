package submit

import (
	"strings"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/gradion"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/workflow"
)

// BlockUnverifiedClassification is a distinct pre-execution block reason: a
// planned operation's classification could not be VERIFIED against the catalog.
// It is used when the catalog cannot prove absence — it is filtered (Query != "")
// or partial (!Complete) — or when the confirmed interval's activity type
// disagrees with the catalog's derived type. The block produces ZERO writes.
const BlockUnverifiedClassification = "unverified_classification"

// BlockUnknownClassification is the stronger block reason for an authoritative
// FULL catalog (Query == "" && Complete) in which the planned classification id
// is genuinely ABSENT or empty: absence from a full catalog PROVES the choice is
// not a valid classification, rather than merely failing to retrieve it. The
// block produces ZERO writes.
const BlockUnknownClassification = "unknown_classification"

// ClassificationIssue is one validation finding for a planned operation.
type ClassificationIssue struct {
	// LocalID is the plan operation's local interval id.
	LocalID string
	// ClassificationID is the operation's chosen classification id (may be empty).
	ClassificationID string
	// Absent is true when the id is empty or not present in the catalog. Such a
	// finding may be RESOLVED by an authoritative targeted fetch when the catalog
	// is not full (see run.Runner.Submit). It is false for an activity-type
	// disagreement, which is a proven conflict that a targeted fetch cannot clear.
	Absent bool
	// Reason is the block reason for the finding.
	Reason string
}

// ClassifyOperations inspects every planned operation's classification against a
// fetched or cached catalog and returns one issue per operation that must block.
// An empty or absent id is "absent": it is BlockUnknownClassification under a
// FULL catalog (proven absent) and BlockUnverifiedClassification otherwise
// (absence cannot be proven). An activity-type disagreement is always
// BlockUnverifiedClassification. The payload is never extended: only the
// classification id is sent to log_time/edit_time.
//
// NOTE: this is a lower-level, catalog-only helper. The SUBMISSION BOUNDARY
// (run.Runner.Submit) uses ProveClassification + InconsistentIDMetadata instead,
// because a submission must be proven against a FRESH LIVE result (a cache never
// proves identity) and by the recorded code/name rather than the uuid alone.
func ClassifyOperations(cat gradion.Catalog, ops []workflow.PlanOperation, confirmed []workflow.ConfirmedInterval) []ClassificationIssue {
	byID := make(map[string]gradion.ClassificationOption, len(cat.Options))
	for _, o := range cat.Options {
		if id := strings.TrimSpace(o.ID); id != "" {
			byID[id] = o
		}
	}
	activityByLocal := make(map[string]string, len(confirmed))
	for _, ci := range confirmed {
		activityByLocal[ci.ID] = strings.TrimSpace(ci.ActivityType)
	}

	full := cat.IsFullCatalog()
	var issues []ClassificationIssue
	for _, op := range ops {
		id := strings.TrimSpace(op.ClassificationID)
		opt, ok := byID[id]
		if id == "" || !ok {
			reason := BlockUnverifiedClassification
			if full {
				reason = BlockUnknownClassification
			}
			issues = append(issues, ClassificationIssue{
				LocalID: op.LocalID, ClassificationID: id, Absent: true, Reason: reason,
			})
			continue
		}
		declared := activityByLocal[op.LocalID]
		if declared != "" && opt.ActivityType != "" && !strings.EqualFold(declared, opt.ActivityType) {
			issues = append(issues, ClassificationIssue{
				LocalID: op.LocalID, ClassificationID: id, Absent: false, Reason: BlockUnverifiedClassification,
			})
		}
	}
	return issues
}

// IssueReasons renders issues as sorted, deduped "<reason>:<localID>" strings.
func IssueReasons(issues []ClassificationIssue) []string {
	reasons := make([]string, 0, len(issues))
	for _, is := range issues {
		reasons = append(reasons, is.Reason+":"+is.LocalID)
	}
	return dedupSorted(reasons)
}

// ValidateCatalog checks every planned operation's classification against a
// fetched or cached catalog and returns sorted, deduped block reasons. It blocks
// when the operation's classification id is empty or absent (BlockUnknownClassification
// under a full catalog, BlockUnverifiedClassification otherwise) or when the
// confirmed interval carries an activity type that disagrees with the catalog's
// derived activity type. An empty catalog is treated as unverifiable, never as
// "no constraint". The payload is never extended: only the classification id is
// sent to log_time/edit_time.
func ValidateCatalog(cat gradion.Catalog, ops []workflow.PlanOperation, confirmed []workflow.ConfirmedInterval) []string {
	return IssueReasons(ClassifyOperations(cat, ops, confirmed))
}

// BlockReselectClassification is a distinct pre-execution block reason: a planned
// selection carries NO retrieval metadata (classification code/name), so it
// cannot be re-proven from a fresh LIVE targeted read. The run fails closed and
// asks for an explicit reselect by code; existing bytes/history are preserved and
// the run produces ZERO writes.
const BlockReselectClassification = "reselect_classification"

// ClassificationLookup is the retrieval metadata for ONE selected classification
// in a planned submission: the selected stable id, the RECORDED code/name
// (retrieval hints, possibly empty for a legacy record), and the locally recorded
// derived activity type.
type ClassificationLookup struct {
	LocalID      string
	ID           string
	Code         string
	Name         string
	ActivityType string
}

// Query returns the bounded targeted-read query for a lookup: the recorded code
// (preferred), else the recorded name, trimmed. An empty result means the lookup
// carries NO retrieval metadata, so it can only be proven against a fresh LIVE
// FULL COMPLETE catalog (else it must be reselected).
func (l ClassificationLookup) Query() string {
	if c := strings.TrimSpace(l.Code); c != "" {
		return c
	}
	return strings.TrimSpace(l.Name)
}

// metadataKey is the exact set of retrieval metadata that every operation sharing
// one classification id must agree on. It is compared case-insensitively for the
// derived activity type and with whitespace-normalized names.
type metadataKey struct {
	code  string
	name  string
	atype string
}

func lookupKey(l ClassificationLookup) metadataKey {
	return metadataKey{
		code:  strings.TrimSpace(l.Code),
		name:  normalizeName(l.Name),
		atype: strings.ToLower(strings.TrimSpace(l.ActivityType)),
	}
}

// InconsistentIDMetadata returns one BlockUnverifiedClassification reason per
// lookup whose classification id is shared by another lookup carrying DIFFERENT
// retrieval metadata or a different derived activity type. Such a group must
// BLOCK — it is never resolved "one-wins" — because the recorded identity is
// ambiguous. Empty for a consistent set.
func InconsistentIDMetadata(lookups []ClassificationLookup) []string {
	groups := map[string]map[metadataKey]bool{}
	for _, l := range lookups {
		id := strings.TrimSpace(l.ID)
		if id == "" {
			continue
		}
		if groups[id] == nil {
			groups[id] = map[metadataKey]bool{}
		}
		groups[id][lookupKey(l)] = true
	}
	var reasons []string
	for _, l := range lookups {
		id := strings.TrimSpace(l.ID)
		if id == "" {
			continue
		}
		if len(groups[id]) > 1 {
			reasons = append(reasons, BlockUnverifiedClassification+":"+l.LocalID)
		}
	}
	return reasons
}

// ProveClassification validates ONE lookup against a catalog and returns "" when
// the choice is PROVEN, else a stable block reason. full marks the catalog as the
// authoritative UNFILTERED+COMPLETE initial fetch: only then is ABSENCE proven
// (BlockUnknownClassification); a targeted result can never prove absence, so the
// failure is BlockUnverifiedClassification.
//
// Every proof requires a LIVE result (cat.Source == "live"); a cache never
// validates identity. A targeted result must additionally be COMPLETE (an
// incomplete/capped page fails closed even when the desired row is present). The
// row must be the EXACT selected stable id, unique (no duplicate id, and no
// duplicate exact code/name match), carry a nonempty name, code, and derived
// activity type, and the recorded code (exact) and name (normalized, when
// present) must match. The local derived activity type must be nonempty and match.
func ProveClassification(cat gradion.Catalog, lk ClassificationLookup, full bool) string {
	if !strings.EqualFold(strings.TrimSpace(cat.Source), "live") {
		return BlockUnverifiedClassification
	}
	id := strings.TrimSpace(lk.ID)
	if full {
		if id == "" || !hasExactID(cat, id) {
			return BlockUnknownClassification
		}
	} else {
		if !cat.Complete {
			// A partial/filtered targeted page proves nothing, even when it contains
			// the desired row.
			return BlockUnverifiedClassification
		}
		// A targeted read is an EXACT lookup: a page carrying anything other than
		// the single desired row is an unrelated/ambiguous result and fails closed,
		// even when the desired row is present.
		if len(cat.Options) != 1 {
			return BlockUnverifiedClassification
		}
	}
	return proveRow(cat, lk)
}

// proveRow applies the row-level safety checks shared by the full and targeted
// paths.
func proveRow(cat gradion.Catalog, lk ClassificationLookup) string {
	id := strings.TrimSpace(lk.ID)
	code := strings.TrimSpace(lk.Code)
	name := strings.TrimSpace(lk.Name)
	atype := strings.TrimSpace(lk.ActivityType)
	if id == "" || atype == "" {
		return BlockUnverifiedClassification
	}

	var idRows []gradion.ClassificationOption
	codeMatches, nameMatches := 0, 0
	for _, o := range cat.Options {
		if strings.TrimSpace(o.ID) == id {
			idRows = append(idRows, o)
		}
		if code != "" && strings.TrimSpace(o.Code) == code {
			codeMatches++
		}
		if name != "" && normalizeName(o.Name) == normalizeName(name) {
			nameMatches++
		}
	}
	// A missing, duplicate, or ambiguous exact match never resolves "one-wins".
	if len(idRows) != 1 {
		return BlockUnverifiedClassification
	}
	if code != "" && codeMatches != 1 {
		return BlockUnverifiedClassification
	}
	if name != "" && nameMatches != 1 {
		return BlockUnverifiedClassification
	}

	row := idRows[0]
	if strings.TrimSpace(row.Code) == "" || strings.TrimSpace(row.Name) == "" || strings.TrimSpace(row.ActivityType) == "" {
		return BlockUnverifiedClassification
	}
	if code != "" && strings.TrimSpace(row.Code) != code {
		return BlockUnverifiedClassification
	}
	if name != "" && normalizeName(row.Name) != normalizeName(name) {
		return BlockUnverifiedClassification
	}
	if !strings.EqualFold(atype, strings.TrimSpace(row.ActivityType)) {
		return BlockUnverifiedClassification
	}
	return ""
}

func hasExactID(cat gradion.Catalog, id string) bool {
	for _, o := range cat.Options {
		if strings.TrimSpace(o.ID) == id {
			return true
		}
	}
	return false
}

// normalizeName lowercases and whitespace-collapses a classification name for a
// tolerant but non-fabricating comparison. It never invents or fuzzy-matches.
func normalizeName(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}
