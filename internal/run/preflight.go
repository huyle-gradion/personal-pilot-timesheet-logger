package run

import (
	"context"
	"errors"
	"sort"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/evidence"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/evidence/plugins"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/git"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/keychain"
)

// Check is one preflight check result (secret-free).
type Check struct {
	Name   string `json:"name"`
	Passed bool   `json:"passed"`
	Code   string `json:"code,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// PreflightOptions tunes Preflight.
type PreflightOptions struct {
	// Deep additionally runs the full VerifyContracts over ALL configured sources
	// (including disabled ones), not just the enabled-source preflight.
	Deep bool
}

// PreflightResult reports preflight readiness.
type PreflightResult struct {
	OK      bool                    `json:"ok"`
	Checks  []Check                 `json:"checks"`
	Sources []evidence.SourceResult `json:"sources,omitempty"`
}

// Preflight validates the timezone, resolves credential references, verifies
// source contracts, and (when a Remote is configured) initializes and verifies
// MCP capabilities. It REFUSES on incomplete contracts and never falls back to
// REST. A non-NotFound Keychain failure is terminal. No secret is ever placed in
// the result.
func (r *Runner) Preflight(ctx context.Context, opts PreflightOptions) (PreflightResult, error) {
	res := PreflightResult{}
	fail := func(name, code, detail string) {
		res.Checks = append(res.Checks, Check{Name: name, Passed: false, Code: code, Detail: detail})
	}
	pass := func(name string) {
		res.Checks = append(res.Checks, Check{Name: name, Passed: true})
	}

	// Timezone.
	if r.locErr != nil {
		fail("timezone", CodeTimezoneInvalid, "configured timezone is not a valid IANA name")
	} else {
		pass("timezone")
	}

	// Credentials: resolve every keychain: reference in the source configs. A
	// non-NotFound failure is terminal and stops the run before any remote call.
	terminal := false
	for _, s := range r.cfg.Evidence.Sources {
		for key, val := range s.Config {
			service, ok := keychainService(val)
			if !ok {
				continue
			}
			if _, err := r.keychainResolver().Lookup(ctx, service, ""); err != nil {
				if keychain.IsNotFound(err) {
					fail("credential_"+s.ID+"_"+key, CodeKeychainMissing, "referenced keychain item is absent")
					continue
				}
				fail("credential_"+s.ID+"_"+key, CodeKeychainTerminal, keychainCode(err))
				terminal = true
			}
		}
	}
	token, err := git.ResolveAuthToken(ctx, r.deps.GitToken)
	if err != nil {
		fail("git_credential", gitErrorCode(err), "git credential resolution failed")
		terminal = true
	} else if token == "" && r.gitRemoteSourceEnabled() {
		fail("git_credential", CodeGitCredentialMissing, "no GitHub credential resolved for an enabled remote git source")
		terminal = true
	}
	if terminal {
		res.OK = false
		return res, nil
	}
	pass("credentials")

	// Source contracts. Refuse enablement when any enabled source is not complete.
	results, err := r.deps.Registry.PreflightContracts(ctx, r.cfg.Evidence.Sources)
	if err != nil {
		return res, err
	}
	if opts.Deep {
		deep, derr := r.deps.Registry.VerifyContracts(ctx, r.cfg.Evidence.Sources)
		if derr != nil {
			return res, derr
		}
		results = mergeSourceResults(results, deep)
	}
	res.Sources = results
	if !evidence.ContractsComplete(results) {
		fail("contracts", CodeContractsIncomplete, "one or more enabled sources failed contract verification")
	} else {
		pass("contracts")
	}

	// Remote capabilities. There is NO REST fallback: a failure refuses the run.
	if r.deps.Remote != nil {
		if _, err := r.deps.Remote.Initialize(ctx); err != nil {
			fail("remote", CodeRemoteUnavailable, "MCP initialize failed")
		} else if err := r.deps.Remote.VerifyCapabilities(ctx); err != nil {
			fail("remote", CodeRemoteUnavailable, "a required MCP tool is unavailable")
		} else {
			pass("remote")
		}
	}

	res.OK = allPassed(res.Checks)
	return res, nil
}

func allPassed(checks []Check) bool {
	for _, c := range checks {
		if !c.Passed {
			return false
		}
	}
	return true
}

// GitSourceEnabled reports whether any enabled source needs a git credential. It
// covers the evidence git kinds AND the registered git plugin type
// (plugins.TypeGit), so a real config's git source is never missed. It is
// type-based; use GitRemoteSourceEnabled for the credential decision.
func GitSourceEnabled(sources []evidence.SourceConfig) bool {
	for _, s := range sources {
		if !s.Enabled {
			continue
		}
		switch s.Type {
		case evidence.SourceLocalGit, evidence.SourceRemotePR, plugins.TypeGit:
			return true
		}
	}
	return false
}

// GitRemoteSourceEnabled reports whether any enabled source needs a REMOTE
// (GitHub) credential. It is MODE-AWARE for the git plugin type: a git source
// whose mode is "local" needs no remote credential, while an absent mode
// (default "both"), "remote", or "both" does. The bare evidence git kinds keep
// their historical type-based behavior.
func GitRemoteSourceEnabled(sources []evidence.SourceConfig) bool {
	for _, s := range sources {
		if !s.Enabled {
			continue
		}
		switch s.Type {
		case evidence.SourceLocalGit, evidence.SourceRemotePR:
			return true
		case plugins.TypeGit:
			if plugins.GitModeRequiresRemote(s) {
				return true
			}
		}
	}
	return false
}

// gitRemoteSourceEnabled reports whether this runner's config needs a remote git
// credential.
func (r *Runner) gitRemoteSourceEnabled() bool {
	return GitRemoteSourceEnabled(r.cfg.Evidence.Sources)
}

// gitErrorCode maps a git credential resolution error to an accurate, secret-free
// code: a Keychain operating error keeps its own code; anything else is a plain
// git credential failure (never mislabeled as a Keychain terminal failure).
func gitErrorCode(err error) string {
	var opErr *keychain.OperatingError
	if errors.As(err, &opErr) && opErr.Code != "" {
		return opErr.Code
	}
	return CodeGitCredential
}

// mergeSourceResults unions preflight and deep results by SourceID (preflight
// wins), sorted by SourceID for determinism.
func mergeSourceResults(a, b []evidence.SourceResult) []evidence.SourceResult {
	byID := make(map[string]evidence.SourceResult, len(a)+len(b))
	for _, r := range a {
		byID[r.SourceID] = r
	}
	for _, r := range b {
		if _, ok := byID[r.SourceID]; !ok {
			byID[r.SourceID] = r
		}
	}
	out := make([]evidence.SourceResult, 0, len(byID))
	for _, r := range byID {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SourceID < out[j].SourceID })
	return out
}
