package timesheet

import (
	"regexp"
	"strings"
)

type SemanticCategory string

const (
	CategoryDevelopment  SemanticCategory = "development"
	CategoryMaintenance  SemanticCategory = "maintenance"
	CategoryResearchDocs SemanticCategory = "research_docs"
	CategoryMeeting      SemanticCategory = "meeting"
	CategoryOther        SemanticCategory = "other"
)

const (
	MaxTextMatchBytes = 4096
)

var (
	prefixDevRegex   = regexp.MustCompile(`(?i)^(feat|feature)(\(.*\))?:\s*`)
	prefixMaintRegex = regexp.MustCompile(`(?i)^(fix|bugfix|bug|test|chore|deps|lint|style|hotfix|ci|revert|remediation)(\(.*\))?:\s*`)
	prefixDocsRegex  = regexp.MustCompile(`(?i)^(docs|doc|design|adr|plan|investigate|sync|review|research|spike|spec)(\(.*\))?:\s*`)

	devRegex   = regexp.MustCompile(`(?i)\b(feat|feature|implement|coding|refactor|build|add|support|backend|frontend)\b`)
	maintRegex = regexp.MustCompile(`(?i)\b(fix|bugfix|bug|test|chore|deps|lint|style|hotfix|ci|revert|remediation)\b`)
	docsRegex  = regexp.MustCompile(`(?i)\b(docs|doc|design|adr|plan|investigate|sync|review|research|spike|spec)\b`)
)

// ClassifyEntry classifies an entry into a semantic category.
// It checks conventional commit prefixes before evaluating substring regexes on bounded body text.
func ClassifyEntry(entry SimpleEntry) SemanticCategory {
	if strings.EqualFold(entry.Source, "calendar") {
		return CategoryMeeting
	}

	title := strings.TrimSpace(entry.Title)
	if prefixDevRegex.MatchString(title) {
		return CategoryDevelopment
	}
	if prefixMaintRegex.MatchString(title) {
		return CategoryMaintenance
	}
	if prefixDocsRegex.MatchString(title) {
		return CategoryResearchDocs
	}

	combined := entry.Title + " " + entry.Description
	if len(combined) > MaxTextMatchBytes {
		combined = combined[:MaxTextMatchBytes]
	}

	if devRegex.MatchString(combined) {
		return CategoryDevelopment
	}
	if maintRegex.MatchString(combined) {
		return CategoryMaintenance
	}
	if docsRegex.MatchString(combined) {
		return CategoryResearchDocs
	}
	return CategoryOther
}

// AreCategoriesCompatible returns true if two categories can be clumped together into a single focus block.
// Per ADR-005, development and maintenance represent closely related engineering activities that merge.
func AreCategoriesCompatible(c1, c2 SemanticCategory) bool {
	if c1 == c2 {
		return true
	}
	if (c1 == CategoryDevelopment && c2 == CategoryMaintenance) ||
		(c1 == CategoryMaintenance && c2 == CategoryDevelopment) {
		return true
	}
	return false
}

// mergeCategories returns the primary category when combining two compatible categories.
func mergeCategories(c1, c2 SemanticCategory) SemanticCategory {
	if c1 == c2 {
		return c1
	}
	if (c1 == CategoryDevelopment && c2 == CategoryMaintenance) ||
		(c1 == CategoryMaintenance && c2 == CategoryDevelopment) {
		return CategoryDevelopment
	}
	return c1
}
