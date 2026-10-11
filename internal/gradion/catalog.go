package gradion

import (
	"bytes"
	"context"
	"regexp"
	"strings"
	"time"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/mcp"
)

// ClassificationOption is one selectable classification. ID and Name come from
// the tool's structured reference envelope; Code and ActivityType are DERIVED by
// the server and appear ONLY in the tool's plain-text rendering. ActivityType is
// display + validation only: it is NEVER sent to log_time/edit_time.
type ClassificationOption struct {
	ID           string `json:"id"`
	Name         string `json:"name,omitempty"`
	Code         string `json:"code,omitempty"`
	ActivityType string `json:"activity_type,omitempty"`
}

// Catalog is a fetched (or cached) classification catalog with provenance.
// Complete is false when the tool's text header reports truncation or when the
// result reaches the tool's 20-row cap. Source is "live" or "cache".
type Catalog struct {
	Options    []ClassificationOption `json:"options"`
	Complete   bool                   `json:"complete"`
	Query      string                 `json:"query,omitempty"`
	ObservedAt time.Time              `json:"observed_at"`
	Source     string                 `json:"source"`
}

// CatalogMaxAge bounds how old a cached catalog may be before it is STALE. It is
// the single shared policy: the cache display (internal/selection) and the
// submission gate (internal/run) both reference it, so they can never drift.
const CatalogMaxAge = 24 * time.Hour

// IsFullCatalog reports whether the catalog is an UNFILTERED, COMPLETE fetch:
// Query is empty AND Complete is true. Only a full catalog can PROVE a
// classification ABSENT. A filtered (Query != "") or partial (!Complete) catalog
// can only show that a choice was not retrieved for this query, which never
// proves the choice invalid.
func (c Catalog) IsFullCatalog() bool {
	return strings.TrimSpace(c.Query) == "" && c.Complete
}

// CatalogFresh reports whether a catalog is usable at now: a live catalog is
// always fresh; a cache is fresh only when observed within CatalogMaxAge. A zero
// ObservedAt is fresh only for a live catalog (an undated cache is unusable).
func CatalogFresh(cat Catalog, now time.Time) bool {
	if strings.TrimSpace(cat.Source) == "live" {
		return true
	}
	if cat.ObservedAt.IsZero() {
		return false
	}
	age := now.Sub(cat.ObservedAt)
	if age < 0 {
		age = 0
	}
	return age <= CatalogMaxAge
}

// classificationLine is one parsed text line:
//
//   - <Name> (<Code>) [<Activity Type>] · <uuid>
type classificationLine struct {
	Name         string
	Code         string
	ActivityType string
	ID           string
}

// classificationEntryRe matches one catalog text line. Name is non-greedy so the
// trailing "(Code) [Activity Type] · uuid" suffix anchors the split. Code and
// Activity Type may be empty ("()"/"[]"); the uuid is the trailing token.
var classificationEntryRe = regexp.MustCompile(`^-\s+(.+?)\s+\(([^)]*)\)\s+\[([^\]]*)\]\s*·\s*(\S+)\s*$`)

// classificationTruncationRe matches the truncation header, e.g.
// "20+ classifications found (showing 20 — refine search to narrow)".
var classificationTruncationRe = regexp.MustCompile(`(?i)^\s*\d+\+.*showing\s+\d+`)

// classificationHeaderRe matches a non-entry informational header, e.g.
// "0 classifications found".
var classificationHeaderRe = regexp.MustCompile(`(?i)^\s*\d+\s+classifications?\s+found`)

// ClassificationCatalog fetches the caller's classifications and returns a
// Catalog. The structured reference envelope supplies ID + Name; the plain-text
// rendering supplies Code + Activity Type (the server DERIVES activity type from
// the classification, so there is no standalone activity-type tool). A text block
// that is present but wholly unparseable fails closed with CodeCatalogUnparsed.
func (c *Client) ClassificationCatalog(ctx context.Context, query string) (Catalog, error) {
	if err := c.guardVerified(); err != nil {
		return Catalog{}, err
	}
	q := strings.TrimSpace(query)
	args := map[string]any{}
	if q != "" {
		args["search"] = q
	}
	res, err := c.callRead(ctx, ToolGetClassifications, args)
	if err != nil {
		return Catalog{}, err
	}
	if res.IsError {
		return Catalog{}, &Error{Code: CodeReadToolError, Message: ToolGetClassifications + " returned a tool-level error"}
	}
	return parseClassificationCatalog(res, q, time.Now().UTC())
}

// parseClassificationCatalog joins the structured reference envelope (authoritative
// for ID + Name) with the text rendering (authoritative for Code + Activity Type)
// by uuid. Complete is false on a truncation header or at the tool's result cap.
func parseClassificationCatalog(res mcp.CallResult, query string, observed time.Time) (Catalog, error) {
	cat := Catalog{Query: query, ObservedAt: observed, Source: "live", Complete: true}

	nameByID := map[string]string{}
	var structuredIDs []string
	if len(bytes.TrimSpace(res.Structured)) > 0 {
		items, err := parseReferenceEnvelope(res)
		if err != nil {
			return Catalog{}, err
		}
		for _, it := range items {
			id := strings.TrimSpace(it.ID)
			if id == "" {
				continue
			}
			if _, seen := nameByID[id]; !seen {
				structuredIDs = append(structuredIDs, id)
			}
			nameByID[id] = strings.TrimSpace(it.Label)
		}
	}

	textNameByID := map[string]string{}
	codeByID := map[string]classificationLine{}
	var textIDs []string
	if text := strings.TrimSpace(res.Content); text != "" {
		lines, truncated, unparseable := parseClassificationText(text)
		if truncated {
			cat.Complete = false
		}
		if len(lines) == 0 && unparseable {
			return Catalog{}, &Error{Code: CodeCatalogUnparsed, Message: "classification text block had no parseable entries"}
		}
		for _, ln := range lines {
			if _, seen := codeByID[ln.ID]; !seen {
				textIDs = append(textIDs, ln.ID)
			}
			codeByID[ln.ID] = ln
			if n := strings.TrimSpace(ln.Name); n != "" {
				textNameByID[ln.ID] = n
			}
		}
	} else if len(structuredIDs) > 0 {
		// The text block is the ONLY source of Code + Activity Type. A structured
		// envelope with NO text block is unverifiable: report it PARTIAL rather than
		// pretending the catalog is complete with empty Code/ActivityType.
		cat.Complete = false
	}

	order := append([]string(nil), textIDs...)
	inText := make(map[string]bool, len(textIDs))
	for _, id := range textIDs {
		inText[id] = true
	}
	for _, id := range structuredIDs {
		if !inText[id] {
			order = append(order, id)
		}
	}

	for _, id := range order {
		name := nameByID[id]
		if name == "" {
			name = textNameByID[id]
		}
		ln := codeByID[id]
		cat.Options = append(cat.Options, ClassificationOption{
			ID: id, Name: name, Code: ln.Code, ActivityType: ln.ActivityType,
		})
	}

	// The tool caps results at MaxClassificationResults; reaching the cap means the
	// catalog may be truncated even without a header.
	if len(cat.Options) >= MaxClassificationResults {
		cat.Complete = false
	}
	return cat, nil
}

// parseClassificationText parses the text rendering. It returns the parsed entry
// lines, whether a truncation header was seen, and whether any non-empty,
// non-header line failed to match the entry pattern (so an all-garbage text block
// fails closed while an empty catalog does not).
func parseClassificationText(text string) (lines []classificationLine, truncated, unparseable bool) {
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimRight(raw, "\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if m := classificationEntryRe.FindStringSubmatch(trimmed); m != nil {
			lines = append(lines, classificationLine{
				Name: strings.TrimSpace(m[1]), Code: strings.TrimSpace(m[2]),
				ActivityType: strings.TrimSpace(m[3]), ID: strings.TrimSpace(m[4]),
			})
			continue
		}
		if classificationTruncationRe.MatchString(trimmed) {
			truncated = true
			continue
		}
		if classificationHeaderRe.MatchString(trimmed) {
			continue
		}
		unparseable = true
	}
	return lines, truncated, unparseable
}
