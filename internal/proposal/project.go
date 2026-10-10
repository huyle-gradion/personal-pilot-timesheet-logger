package proposal

import (
	"sort"
	"strings"
)

// ResolveProject suggests a project for a block. A configured repository-to-
// project mapping wins; otherwise a single distinct hint is used. Multiple
// distinct hints are ambiguous, and no hint is unresolved. Titles never choose a
// project.
func ResolveProject(projectMap map[string]string, repository string, hints []string) ProjectResolution {
	repo := strings.TrimSpace(repository)
	if repo != "" {
		if p, ok := projectMap[repo]; ok && strings.TrimSpace(p) != "" {
			return ProjectResolution{Project: strings.TrimSpace(p), Source: ProjectSourceRepoMap, Resolved: true}
		}
	}

	distinct := distinctNonEmpty(hints)
	switch len(distinct) {
	case 1:
		return ProjectResolution{Project: distinct[0], Source: ProjectSourceHint, Resolved: true}
	default:
		return ProjectResolution{Source: ProjectSourceUnresolved, Resolved: false}
	}
}

// distinctNonEmpty returns the sorted distinct non-empty trimmed values.
func distinctNonEmpty(values []string) []string {
	seen := make(map[string]bool, len(values))
	var out []string
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}
