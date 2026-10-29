package timesheet

import (
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

type githubPagination uint8

const (
	githubPaginationUnknown githubPagination = iota
	githubPaginationNext
	githubPaginationTerminal
)

type githubResponse struct {
	status     int
	body       []byte
	pagination githubPagination
}

var githubRelationRE = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9.-]*$`)

func (g *githubRun) getJSON(q url.Values, path string) (githubResponse, error) {
	endpoint := *g.scope.apiBase
	endpoint.Path += strings.TrimPrefix(strings.TrimSuffix(path, "/"), "/")
	endpoint.RawQuery = q.Encode()
	req, err := http.NewRequest(http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return githubResponse{}, fmt.Errorf("request could not be constructed")
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("Authorization", "Bearer "+g.scope.token)
	resp, err := g.client.Do(req)
	if err != nil {
		return githubResponse{}, fmt.Errorf("the provider request could not be sent")
	}
	defer func() { _ = resp.Body.Close() }()
	result := githubResponse{status: resp.StatusCode, pagination: parseGithubPagination(resp.Header.Values("Link"))}
	limit := int64(maxGithubBodyBytes)
	if resp.StatusCode != http.StatusOK {
		limit = maxErrorBodyBytes
	}
	result.body, err = io.ReadAll(io.LimitReader(resp.Body, limit))
	if err != nil {
		result.body = nil
		if resp.StatusCode == http.StatusOK {
			return result, fmt.Errorf("the provider response could not be read")
		}
	}
	return result, nil
}

func parseGithubPagination(headers []string) githubPagination {
	state := githubPaginationUnknown
	for _, header := range headers {
		links, valid := splitGithubLinks(header)
		if !valid {
			return githubPaginationUnknown
		}
		for _, link := range links {
			if !strings.HasPrefix(link, "<") {
				return githubPaginationUnknown
			}
			target, params, found := strings.Cut(link[1:], ">")
			if !found || target == "" || strings.ContainsAny(target, "<> \t\r\n") {
				return githubPaginationUnknown
			}
			parsed, err := url.Parse(target)
			if err != nil || ((parsed.Scheme == "https" || parsed.Scheme == "http") && parsed.Host == "") {
				return githubPaginationUnknown
			}
			params = strings.TrimSpace(params)
			if !strings.HasPrefix(params, ";") {
				return githubPaginationUnknown
			}
			mediaType, attributes, err := mime.ParseMediaType("link" + params)
			_, anchored := attributes["anchor"]
			if err != nil || mediaType != "link" || anchored || len(strings.Fields(attributes["rel"])) == 0 {
				return githubPaginationUnknown
			}
			for _, rel := range strings.Fields(attributes["rel"]) {
				if !githubRelationRE.MatchString(rel) {
					extension, err := url.Parse(rel)
					if err != nil || !extension.IsAbs() {
						return githubPaginationUnknown
					}
				}
				switch strings.ToLower(rel) {
				case "next":
					state = githubPaginationNext
				case "prev", "first", "last":
					if state == githubPaginationUnknown {
						state = githubPaginationTerminal
					}
				}
			}
		}
	}
	return state
}

func splitGithubLinks(header string) ([]string, bool) {
	var links []string
	start := 0
	quoted, escaped, target := false, false, false
	for i, r := range header {
		switch {
		case escaped:
			escaped = false
		case quoted && r == '\\':
			escaped = true
		case !target && r == '"':
			quoted = !quoted
		case quoted:
		case r == '<':
			if target {
				return nil, false
			}
			target = true
		case r == '>':
			if !target {
				return nil, false
			}
			target = false
		case !target && r == ',':
			links = append(links, strings.TrimSpace(header[start:i]))
			start = i + 1
		}
	}
	links = append(links, strings.TrimSpace(header[start:]))
	return links, !quoted && !target && !escaped
}
