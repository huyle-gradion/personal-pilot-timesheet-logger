package evidence

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// idSchemeVersion is part of the stable-ID tuple. Bumping it changes every ID,
// which is intentional: it is a deliberate identity scheme migration. v2
// length-prefixes each tuple field so a NUL or any other byte inside a part
// cannot be confused with a field boundary.
const idSchemeVersion = "v2"

// ErrRepoKeyRequired is returned by repo-key-required identity helpers when the
// repository key is empty or whitespace. It prevents an adapter from emitting
// evidence whose identity has silently lost the repository dimension.
var ErrRepoKeyRequired = errors.New("evidence: repository key is required for identity")

// StableID returns the deterministic evidence identity for a kind and its tuple
// parts: sha256 over the length-prefixed tuple (idSchemeVersion, kind, parts...),
// rendered as "<kind>_<32 hex chars>". Each field is prefixed with its 8-byte
// big-endian byte length, so the tuple is unambiguous regardless of part content
// (a part containing NUL cannot alias a boundary). Identity is derived ONLY from
// stable, non-secret provenance; branch names never enter identity.
func StableID(kind SourceKind, parts ...string) string {
	h := sha256.New()
	writeField(h, idSchemeVersion)
	writeField(h, string(kind))
	for _, p := range parts {
		writeField(h, p)
	}
	sum := h.Sum(nil)
	return string(kind) + "_" + hex.EncodeToString(sum[:16])
}

// writeField writes an 8-byte big-endian length prefix followed by the raw bytes.
func writeField(h io.Writer, s string) {
	var prefix [8]byte
	binary.BigEndian.PutUint64(prefix[:], uint64(len(s)))
	_, _ = h.Write(prefix[:])
	_, _ = h.Write([]byte(s))
}

// CalendarID builds the calendar tuple: FeedID, UID, occurrenceStartUTC in
// RFC3339. The feed URL secret is never an identity input; only the owner's
// non-secret FeedID is.
func CalendarID(feedID, uid, occurrenceStartRFC3339 string) string {
	return StableID(SourceCalendar, feedID, uid, occurrenceStartRFC3339)
}

// LocalGitID builds the local-git tuple: CanonicalRepoKey, CommitSHA. It rejects
// an empty repository key with ErrRepoKeyRequired. The branch name is deliberately
// excluded so identical branch names in different repos never merge.
func LocalGitID(repoKey, commitSHA string) (string, error) {
	if strings.TrimSpace(repoKey) == "" {
		return "", ErrRepoKeyRequired
	}
	return StableID(SourceLocalGit, repoKey, commitSHA), nil
}

// RemotePRID builds the remote-PR tuple: CanonicalRepoKey, PRNumber. It rejects
// an empty repository key with ErrRepoKeyRequired.
func RemotePRID(repoKey string, prNumber int) (string, error) {
	if strings.TrimSpace(repoKey) == "" {
		return "", ErrRepoKeyRequired
	}
	return StableID(SourceRemotePR, repoKey, strconv.Itoa(prNumber)), nil
}

// ManualID builds the manual tuple: Date, UUID.
func ManualID(date, uuid string) string {
	return StableID(SourceManual, date, uuid)
}

// AgentSessionID builds the agent-session tuple: SessionID, EventType,
// atRFC3339Nano.
func AgentSessionID(sessionID, eventType, atRFC3339Nano string) string {
	return StableID(SourceAgentSession, sessionID, eventType, atRFC3339Nano)
}

var (
	schemePattern  = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*://`)
	scpLikePattern = regexp.MustCompile(`^[^/@\s]+@([^:/\s]+):(.+)$`)
)

// CanonicalRepoKey returns a stable, non-secret repository identity. When the
// remote origin carries a forge/host it is included (normalized, lowercased) so
// identically named repositories on different hosts never collide:
// "github.com/acme/app" differs from "gitlab.example.com/acme/app". When no
// origin is present the owner-configured non-secret id is used as the fallback.
// It returns "" when neither exists, and a filesystem path is NEVER accepted as
// identity.
//
// Do NOT reuse sanitizeSlugForID semantics: that collides "owner/repo" with
// "owner-repo". This function preserves the separator so distinct repositories
// never collide.
func CanonicalRepoKey(remoteOrigin, configuredID string) string {
	if key := canonicalFromRemoteOrigin(remoteOrigin); key != "" {
		return key
	}
	id := strings.TrimSpace(configuredID)
	if id == "" || looksLikeFilesystemPath(id) {
		return ""
	}
	return id
}

// canonicalFromRemoteOrigin parses a git remote origin into a lowercased
// "<host>/owner/name" (or "<owner>/<name>" when the origin carries no host). It
// returns "" for empty input, filesystem paths, and origins with no owner/name.
func canonicalFromRemoteOrigin(origin string) string {
	o := strings.TrimSpace(origin)
	if o == "" || looksLikeFilesystemPath(o) {
		return ""
	}

	var host, path string
	switch {
	case schemePattern.MatchString(o):
		u, err := url.Parse(o)
		if err != nil || u.Host == "" {
			return ""
		}
		host = u.Hostname()
		path = u.Path
	case scpLikePattern.MatchString(o):
		m := scpLikePattern.FindStringSubmatch(o)
		host = m[1]
		path = m[2]
	default:
		path = o
	}

	path = normalizeRepoPath(path)
	if path == "" {
		return ""
	}

	// A bare origin may carry a leading host-like segment ("github.com/o/n").
	if host == "" {
		segs := strings.Split(path, "/")
		if len(segs) > 2 && strings.Contains(segs[0], ".") {
			host = segs[0]
			path = strings.Join(segs[1:], "/")
		}
	}

	if host == "" {
		return strings.ToLower(path)
	}
	return strings.ToLower(host) + "/" + strings.ToLower(path)
}

// normalizeRepoPath trims a repository path to "owner/name"-or-deeper form,
// dropping leading/trailing slashes and a trailing ".git". It returns "" when the
// path has fewer than two segments.
func normalizeRepoPath(path string) string {
	p := strings.TrimSpace(path)
	p = strings.TrimPrefix(p, "/")
	p = strings.TrimSuffix(p, "/")
	p = strings.TrimSuffix(p, ".git")
	p = strings.TrimSuffix(p, "/")
	if p == "" {
		return ""
	}
	segs := strings.Split(p, "/")
	if len(segs) < 2 {
		return ""
	}
	for _, s := range segs {
		if s == "" {
			return ""
		}
	}
	return strings.Join(segs, "/")
}

// looksLikeFilesystemPath reports whether s is a filesystem path that must never
// be used as repository identity.
func looksLikeFilesystemPath(s string) bool {
	switch {
	case s == "":
		return false
	case strings.HasPrefix(s, "/"),
		strings.HasPrefix(s, "./"),
		strings.HasPrefix(s, "../"),
		strings.HasPrefix(s, "~"):
		return true
	case strings.Contains(s, `\`):
		return true
	case len(s) >= 2 && s[1] == ':' && isASCIIAlpha(s[0]):
		return true
	default:
		return false
	}
}

func isASCIIAlpha(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}
