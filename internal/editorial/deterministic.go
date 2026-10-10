package editorial

import (
	"fmt"
	"strings"
)

// DeterministicText composes plain text from a target's non-time identity and its
// facts. It invents no impact and never states a duration, so it is always safe to
// surface for review.
func DeterministicText(target Target) Text {
	return Text{
		Title:       deterministicTitle(target),
		Description: deterministicDescription(target),
	}
}

func deterministicTitle(target Target) string {
	if repo := strings.TrimSpace(target.Repository); repo != "" {
		if target.PRNumber > 0 {
			return fmt.Sprintf("%s#%d", repo, target.PRNumber)
		}
		return repo
	}
	if b := strings.TrimSpace(target.Branch); b != "" && !isDefaultBranch(b) {
		return b
	}
	if p := strings.TrimSpace(target.Project); p != "" {
		return p
	}
	if k := strings.TrimSpace(target.Kind); k != "" {
		return k
	}
	return "untitled"
}

func deterministicDescription(target Target) string {
	var parts []string
	seen := make(map[string]bool)
	for _, f := range target.Facts {
		t := strings.TrimSpace(f.Text)
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		parts = append(parts, t)
	}
	desc := strings.Join(parts, "; ")
	if desc == "" {
		desc = strings.TrimSpace(target.Project)
	}
	if desc == "" {
		desc = strings.TrimSpace(target.Kind)
	}
	if desc == "" {
		desc = "no description"
	}
	if len(desc) > MaxDescriptionBytes {
		desc = truncateUTF8(desc, MaxDescriptionBytes)
	}
	return desc
}

func isDefaultBranch(b string) bool {
	switch strings.ToLower(b) {
	case "main", "master", "trunk", "head", "develop", "development":
		return true
	}
	return false
}
