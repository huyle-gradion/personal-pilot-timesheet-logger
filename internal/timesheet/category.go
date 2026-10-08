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

var (
	devRegex   = regexp.MustCompile(`(?i)\b(feat|feature|implement|coding|refactor|build|add|support|backend|frontend)\b`)
	maintRegex = regexp.MustCompile(`(?i)\b(fix|bugfix|bug|test|chore|deps|lint|style|hotfix|ci|revert|remediation)\b`)
	docsRegex  = regexp.MustCompile(`(?i)\b(docs|doc|design|adr|plan|investigate|sync|review|research|spike|spec)\b`)
)

func ClassifyEntry(entry SimpleEntry) SemanticCategory {
	if strings.EqualFold(entry.Source, "calendar") {
		return CategoryMeeting
	}
	combined := entry.Title + " " + entry.Description
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
