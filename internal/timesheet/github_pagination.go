package timesheet

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

type familyWalk struct {
	pages        int
	stop         string
	httpStatus   int
	httpBody     []byte
	err          error
	maxPages     int
	coveredFrom  time.Time
	coveredUntil time.Time
}

func (g *githubRun) familyPageLoop(query url.Values, path string, maxPages int, decode func(body []byte) (int, error)) familyWalk {
	return g.familyPageLoopWindow(query, path, maxPages, decode, nil)
}

func (g *githubRun) familyPageLoopWindow(query url.Values, path string, maxPages int, decode func(body []byte) (int, error), windowPassed func() bool) familyWalk {
	walk := familyWalk{maxPages: maxPages}
	for page := 1; page <= maxPages; page++ {
		q := cloneValues(query)
		q.Set("per_page", "100")
		q.Set("page", strconv.Itoa(page))
		response, err := g.getJSON(q, path)
		if err != nil {
			walk.stop, walk.err = "transport", err
			return walk
		}
		if response.status != http.StatusOK {
			walk.stop = "http"
			walk.httpStatus = response.status
			walk.httpBody = response.body
			walk.err = fmt.Errorf("provider responded with HTTP %d", response.status)
			return walk
		}
		n, err := decode(response.body)
		if err != nil {
			walk.stop = "decode"
			walk.err = fmt.Errorf("the provider response was not the expected document schema")
			return walk
		}
		switch response.pagination {
		case githubPaginationTerminal:
			walk.stop = "finished"
			return walk
		case githubPaginationUnknown:
			if n == 0 {
				walk.stop = "empty"
				return walk
			}
			if n < 100 {
				walk.stop = "finished"
				return walk
			}
		case githubPaginationNext:
		}
		if n == 100 && windowPassed != nil && windowPassed() {
			walk.stop = stopWindowPassed
			return walk
		}
		walk.pages++
	}
	walk.stop = "capped"
	return walk
}

func cloneValues(v url.Values) url.Values {
	out := url.Values{}
	for k, vs := range v {
		for _, x := range vs {
			out.Add(k, x)
		}
	}
	return out
}
