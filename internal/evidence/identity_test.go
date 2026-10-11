package evidence

import (
	"errors"
	"strings"
	"testing"
)

func TestStableID_Deterministic(t *testing.T) {
	a := StableID(SourceManual, "2026-10-08", "uuid-1")
	b := StableID(SourceManual, "2026-10-08", "uuid-1")
	if a != b {
		t.Fatalf("StableID not deterministic: %q != %q", a, b)
	}
	if !strings.HasPrefix(a, string(SourceManual)+"_") {
		t.Errorf("expected kind prefix, got %q", a)
	}
	hexPart := strings.TrimPrefix(a, string(SourceManual)+"_")
	if len(hexPart) != 32 {
		t.Errorf("expected 32 hex chars, got %d (%q)", len(hexPart), hexPart)
	}
}

func TestStableID_KindAndTupleSeparation(t *testing.T) {
	// Same parts, different kind must not collide.
	if StableID(SourceLocalGit, "repo", "sha") == StableID(SourceRemotePR, "repo", "sha") {
		t.Error("different kinds with same parts collided")
	}

	// Tuple boundaries must be unambiguous: ["ab","c"] must differ from ["a","bc"].
	if StableID(SourceManual, "ab", "c") == StableID(SourceManual, "a", "bc") {
		t.Error("tuple boundary ambiguity: parts joined without a separator would collide")
	}

	// Per-kind tuple helpers separate correctly.
	if CalendarID("feed", "uid", "2026-10-08T09:00:00Z") == CalendarID("feed", "uid", "2026-10-08T10:00:00Z") {
		t.Error("calendar identity ignored occurrence start")
	}
	gitID, err := LocalGitID("repo", "sha")
	if err != nil {
		t.Fatalf("LocalGitID: %v", err)
	}
	prID, err := RemotePRID("repo", 1)
	if err != nil {
		t.Fatalf("RemotePRID: %v", err)
	}
	if gitID == prID {
		t.Error("local git and remote PR identities collided")
	}
	if ManualID("2026-10-08", "u") == AgentSessionID("u", "", "") {
		t.Error("manual and agent-session identities collided")
	}
}

// TestStableID_NULInPartsIsUnambiguous proves length-prefixed fields do not alias
// when a part itself contains NUL.
func TestStableID_NULInPartsIsUnambiguous(t *testing.T) {
	a := StableID(SourceManual, "a\x00b", "c")
	b := StableID(SourceManual, "a", "b\x00c")
	if a == b {
		t.Errorf("NUL inside a part aliased a field boundary: %q == %q", a, b)
	}
}

// TestStableID_BranchNeverEntersIdentity proves identical branch names across
// different repositories never merge: the branch is simply not part of the
// identity tuple.
func TestStableID_BranchNeverEntersIdentity(t *testing.T) {
	repoA := CanonicalRepoKey("git@github.com:org/repo-a.git", "")
	repoB := CanonicalRepoKey("git@github.com:org/repo-b.git", "")
	if repoA == repoB || repoA == "" || repoB == "" {
		t.Fatalf("expected distinct repo keys, got %q and %q", repoA, repoB)
	}
	const sha = "0123456789abcdef"
	const branch = "feature/shared-name"
	_ = branch

	idA, err := LocalGitID(repoA, sha)
	if err != nil {
		t.Fatalf("LocalGitID A: %v", err)
	}
	idB, err := LocalGitID(repoB, sha)
	if err != nil {
		t.Fatalf("LocalGitID B: %v", err)
	}
	if idA == idB {
		t.Errorf("same commit SHA in different repos produced the same ID %q; branch must not merge topics", idA)
	}
}

// TestLocalGitID_RejectsEmptyRepoKey and TestRemotePRID_RejectsEmptyRepoKey prove
// repo-key-required helpers refuse an empty/whitespace key, so an adapter cannot
// emit evidence whose identity silently lost the repository dimension.
func TestLocalGitID_RejectsEmptyRepoKey(t *testing.T) {
	for _, key := range []string{"", "   ", "\t"} {
		id, err := LocalGitID(key, "sha")
		if !errors.Is(err, ErrRepoKeyRequired) {
			t.Errorf("LocalGitID(%q): expected ErrRepoKeyRequired, got %v", key, err)
		}
		if id != "" {
			t.Errorf("LocalGitID(%q): expected empty ID on refusal, got %q", key, id)
		}
	}
}

func TestRemotePRID_RejectsEmptyRepoKey(t *testing.T) {
	for _, key := range []string{"", "   "} {
		id, err := RemotePRID(key, 42)
		if !errors.Is(err, ErrRepoKeyRequired) {
			t.Errorf("RemotePRID(%q): expected ErrRepoKeyRequired, got %v", key, err)
		}
		if id != "" {
			t.Errorf("RemotePRID(%q): expected empty ID on refusal, got %q", key, id)
		}
	}
}

func TestCanonicalRepoKey_RemoteOriginForms(t *testing.T) {
	cases := map[string]string{
		"git@github.com:Owner/Repo.git":       "github.com/owner/repo",
		"https://github.com/Owner/Repo.git":   "github.com/owner/repo",
		"https://github.com/Owner/Repo":       "github.com/owner/repo",
		"ssh://git@github.com/Owner/Repo.git": "github.com/owner/repo",
		"git://github.com/Owner/Repo.git":     "github.com/owner/repo",
		"github.com/Owner/Repo":               "github.com/owner/repo",
		// A bare origin with no host keeps just owner/name.
		"Owner/Repo": "owner/repo",
	}
	for origin, want := range cases {
		if got := CanonicalRepoKey(origin, ""); got != want {
			t.Errorf("CanonicalRepoKey(%q) = %q, want %q", origin, got, want)
		}
	}
}

// TestCanonicalRepoKey_HostsDoNotCollide proves identically named repos on
// different hosts do not merge.
func TestCanonicalRepoKey_HostsDoNotCollide(t *testing.T) {
	gh := CanonicalRepoKey("git@github.com:acme/app.git", "")
	gl := CanonicalRepoKey("git@gitlab.example.com:acme/app.git", "")
	if gh == gl {
		t.Fatalf("different hosts collided: %q == %q", gh, gl)
	}
	if gh != "github.com/acme/app" {
		t.Errorf("expected github.com/acme/app, got %q", gh)
	}
	if gl != "gitlab.example.com/acme/app" {
		t.Errorf("expected gitlab.example.com/acme/app, got %q", gl)
	}
}

// TestCanonicalRepoKey_DistinctSeparators proves "owner/repo" and "owner-repo"
// do not collide (unlike sanitizeSlugForID, which collapses the separator).
func TestCanonicalRepoKey_DistinctSeparators(t *testing.T) {
	slash := CanonicalRepoKey("owner/repo", "")
	dash := CanonicalRepoKey("", "owner-repo")
	if slash != "owner/repo" {
		t.Fatalf("expected owner/repo, got %q", slash)
	}
	if dash != "owner-repo" {
		t.Fatalf("expected owner-repo, got %q", dash)
	}
	if slash == dash {
		t.Error("owner/repo and owner-repo collided; separator semantics were lost")
	}
}

// TestCanonicalRepoKey_RefusesFilesystemPath proves a filesystem path is never an
// identity: with no origin and no configured id the key is empty.
func TestCanonicalRepoKey_RefusesFilesystemPath(t *testing.T) {
	refused := []struct{ origin, configured string }{
		{"/Users/someone/projects/repo", ""},
		{"./relative/repo", ""},
		{"../up/repo", ""},
		{"~/repos/mine", ""},
		{"", ""},
		{"", "/Users/someone/projects/repo"},
		{`C:\Users\someone\repo`, ""},
	}
	for _, c := range refused {
		if got := CanonicalRepoKey(c.origin, c.configured); got != "" {
			t.Errorf("CanonicalRepoKey(%q, %q) = %q, want refusal (empty)", c.origin, c.configured, got)
		}
	}
}

// TestCanonicalRepoKey_ConfiguredFallback proves a remote-less repo uses the
// owner-configured non-secret id when no origin is present.
func TestCanonicalRepoKey_ConfiguredFallback(t *testing.T) {
	if got := CanonicalRepoKey("", "personal-pilot"); got != "personal-pilot" {
		t.Errorf("expected configured fallback id, got %q", got)
	}
}

// TestJournalID_ExplicitVsImplicit proves the journal identity helper distinguishes
// an explicit id (sourceID/date/X, section- and content-independent) from an
// implicit one (sourceID/date/section/content-hash).
func TestJournalID_ExplicitVsImplicit(t *testing.T) {
	a := JournalID("src", "2026-10-08", "", "work journal", "h1")
	if a != JournalID("src", "2026-10-08", "", "work journal", "h1") {
		t.Error("implicit journal identity must be deterministic")
	}
	if a == JournalID("src", "2026-10-08", "", "daily impact", "h1") {
		t.Error("identical content in different sections must not collide")
	}
	if a == JournalID("src", "2026-10-08", "", "work journal", "h2") {
		t.Error("a content change must change implicit identity")
	}

	e1 := JournalID("src", "2026-10-08", "abc", "work journal", "h1")
	e2 := JournalID("src", "2026-10-08", "abc", "daily impact", "h2")
	if e1 != e2 {
		t.Error("an explicit id must be independent of section and content")
	}
	if e1 == a {
		t.Error("explicit and implicit identities must differ")
	}
	// An invalid explicit id falls back to the implicit identity.
	if JournalID("src", "d", "has space", "s", "h") != JournalID("src", "d", "", "s", "h") {
		t.Error("an invalid explicit id must fall back to implicit identity")
	}
}

func TestValidJournalExplicitID(t *testing.T) {
	for _, ok := range []string{"a", "abc.def:1-2_3", "x"} {
		if !ValidJournalExplicitID(ok) {
			t.Errorf("expected %q to be valid", ok)
		}
	}
	for _, bad := range []string{"", "has space", "with/slash", "emoji😀", strings.Repeat("a", 65)} {
		if ValidJournalExplicitID(bad) {
			t.Errorf("expected %q to be invalid", bad)
		}
	}
}
