package proposal

import "testing"

func TestResolveProject_RepoMapWins(t *testing.T) {
	r := ResolveProject(map[string]string{"acme/app": "Proj"}, "acme/app", []string{"Ignored"})
	if !r.Resolved || r.Project != "Proj" || r.Source != ProjectSourceRepoMap {
		t.Errorf("got %+v, want resolved Proj via repo_map", r)
	}
}

func TestResolveProject_SingleHint(t *testing.T) {
	r := ResolveProject(nil, "", []string{"Proj", "Proj"})
	if !r.Resolved || r.Project != "Proj" || r.Source != ProjectSourceHint {
		t.Errorf("got %+v, want resolved Proj via hint", r)
	}
}

func TestResolveProject_AmbiguousUnresolved(t *testing.T) {
	r := ResolveProject(nil, "", []string{"A", "B"})
	if r.Resolved {
		t.Errorf("ambiguous hints must be unresolved, got %+v", r)
	}
	if r.Source != ProjectSourceUnresolved {
		t.Errorf("source = %q, want unresolved", r.Source)
	}
}

func TestResolveProject_NoHintUnresolved(t *testing.T) {
	r := ResolveProject(nil, "", nil)
	if r.Resolved {
		t.Errorf("no hint must be unresolved, got %+v", r)
	}
}
