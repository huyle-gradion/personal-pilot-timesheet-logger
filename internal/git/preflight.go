package git

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strings"
	"time"
)

// PreflightStatus conveys whether prerequisites are met to collect Git activity.
type PreflightStatus struct {
	Ready       bool     `json:"ready"`
	MissingKeys []string `json:"missing_keys"`
	Guide       string   `json:"guide"`
	Token       string   `json:"token,omitempty"`
}

// probeGHAuthToken queries `gh auth token` with bounded timeout and safe subprocess execution.
func probeGHAuthToken(ctx context.Context) (string, error) {
	probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	cmd := exec.CommandContext(probeCtx, "gh", "auth", "token")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = nil // Suppress stderr output from gh

	if err := cmd.Run(); err != nil {
		return "", err
	}

	tok := strings.TrimSpace(stdout.String())
	return tok, nil
}

// ResolveAuthToken resolves a GitHub authentication token from arguments, env vars, or GitHub CLI.
func ResolveAuthToken(ctx context.Context, explicitToken string) string {
	if tok := strings.TrimSpace(explicitToken); tok != "" {
		return tok
	}
	if tok := strings.TrimSpace(os.Getenv("GITHUB_TOKEN")); tok != "" {
		return tok
	}
	if tok := strings.TrimSpace(os.Getenv("GH_TOKEN")); tok != "" {
		return tok
	}
	if tok, err := probeGHAuthToken(ctx); err == nil && tok != "" {
		return tok
	}
	return ""
}

// CheckPreflight validates prerequisites for Git collection (authentication and target repositories).
func CheckPreflight(ctx context.Context, token string, repos []string) PreflightStatus {
	var missing []string
	var guideParts []string

	resolvedToken := ResolveAuthToken(ctx, token)
	if resolvedToken == "" {
		missing = append(missing, "GITHUB_TOKEN")
		guideParts = append(guideParts,
			"GitHub authentication token is missing or not configured.\n"+
				"To resolve this, please do one of the following:\n"+
				"  1. Log in with GitHub CLI (recommended):\n"+
				"     gh auth login\n\n"+
				"  2. Or generate a Personal Access Token (classic) with 'repo' and 'read:user' scopes:\n"+
				"     Visit: https://github.com/settings/tokens\n"+
				"     Then export the token:\n"+
				"     export GITHUB_TOKEN=\"ghp_your_personal_access_token_here\"",
		)
	}

	// Filter and validate repos
	var validRepos []string
	for _, r := range repos {
		r = strings.TrimSpace(r)
		if r != "" {
			validRepos = append(validRepos, r)
		}
	}

	if len(validRepos) == 0 {
		missing = append(missing, "TRACKED_REPOS")
		guideParts = append(guideParts,
			"No tracked repositories specified.\n"+
				"To resolve this, please specify target repositories:\n"+
				"  1. Via CLI flag:\n"+
				"     -repos=owner/repo1,owner/repo2 or -repos=.\n\n"+
				"  2. Or export the TRACKED_REPOS environment variable:\n"+
				"     export TRACKED_REPOS=\"owner/repo1,owner/repo2\"\n\n"+
				"  3. Or run inside a local git repository with default:\n"+
				"     -repos=.",
		)
	}

	ready := len(missing) == 0
	guide := strings.Join(guideParts, "\n\n")

	return PreflightStatus{
		Ready:       ready,
		MissingKeys: missing,
		Guide:       guide,
		Token:       resolvedToken,
	}
}
