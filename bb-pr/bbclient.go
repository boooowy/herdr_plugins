package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const bbAPIBase = "https://api.bitbucket.org/2.0"

// bbClient is a minimal Bitbucket Cloud API 2.0 client: Basic auth with an
// Atlassian email + API token (App Passwords died 2026-06), `next`-link
// pagination, and redirect-following for the diff/diffstat endpoints.
type bbClient struct {
	email string
	token string
	base  string // API root; overridden in tests
	http  *http.Client
}

func newBBClient(email, token string, timeout time.Duration) *bbClient {
	c := &bbClient{email: email, token: token, base: bbAPIBase}
	c.http = &http.Client{
		Timeout: timeout,
		// The diff/diffstat endpoints answer with a 302 to a (possibly
		// signed) URL. Go re-sends Basic auth only to the same host, which
		// is the safe default; just cap the hops and log for debugging.
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return fmt.Errorf("stopped after %d redirects", len(via))
			}
			debugf("bb: redirect -> %s", req.URL)
			return nil
		},
	}
	return c
}

// httpError carries the status and a body snippet for API failures.
type httpError struct {
	Status int
	URL    string
	Body   string
}

func (e *httpError) Error() string {
	msg := e.Body
	if len(msg) > 200 {
		msg = msg[:200] + "…"
	}
	return fmt.Sprintf("bitbucket api: HTTP %d for %s: %s", e.Status, e.URL, msg)
}

// do performs one authenticated GET, retrying up to twice on transport
// errors and 429/5xx. Transport errors matter as much as status codes here:
// through a forward proxy (HTTPS_PROXY) a fresh CONNECT sporadically hangs
// or is refused, and a process's very first request is the most exposed.
func (c *bbClient) do(rawURL string) (*http.Response, error) {
	const maxAttempts = 3
	for attempt := 1; ; attempt++ {
		req, err := http.NewRequest(http.MethodGet, rawURL, nil)
		if err != nil {
			return nil, err
		}
		req.SetBasicAuth(c.email, c.token)
		resp, err := c.http.Do(req)
		if err != nil {
			if attempt < maxAttempts {
				debugf("bb: transport error for %s: %v — retrying", rawURL, err)
				time.Sleep(500 * time.Millisecond)
				continue
			}
			return nil, err
		}
		if (resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500) && attempt < maxAttempts {
			resp.Body.Close()
			debugf("bb: HTTP %d for %s, retrying", resp.StatusCode, rawURL)
			time.Sleep(700 * time.Millisecond)
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			resp.Body.Close()
			return nil, &httpError{Status: resp.StatusCode, URL: rawURL, Body: strings.TrimSpace(string(body))}
		}
		return resp, nil
	}
}

// getJSON decodes one GET response into out.
func (c *bbClient) getJSON(rawURL string, out any) error {
	resp, err := c.do(rawURL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode %s: %w", rawURL, err)
	}
	return nil
}

// getText fetches one GET response as plain text (the raw diff).
func (c *bbClient) getText(rawURL string) (string, error) {
	resp, err := c.do(rawURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", rawURL, err)
	}
	return string(data), nil
}

// getAll follows the collection's `next` links verbatim and returns every
// value. maxPages caps runaway collections (0 = default 50).
func getAll[T any](c *bbClient, firstURL string, maxPages int) ([]T, error) {
	if maxPages <= 0 {
		maxPages = 50
	}
	var all []T
	url := firstURL
	for i := 0; url != "" && i < maxPages; i++ {
		var pg page[T]
		if err := c.getJSON(url, &pg); err != nil {
			return nil, err
		}
		all = append(all, pg.Values...)
		url = pg.Next
	}
	return all, nil
}

func (c *bbClient) repoURL(ws, repo, suffix string) string {
	return fmt.Sprintf("%s/repositories/%s/%s%s", c.base, url.PathEscape(ws), url.PathEscape(repo), suffix)
}

// listPRFields trims the PR list payload to what the list view renders (the
// detail view refetches the full PR) — a big win on busy repos, where the
// untrimmed objects carry descriptions and full participant lists.
const listPRFields = "next," +
	"values.id,values.title,values.comment_count,values.updated_on," +
	"values.author,values.source.branch.name,values.destination.branch.name," +
	"values.links.html.href"

// listPRsFirstURL builds the first page of the repo's PR list filtered by
// state (OPEN | MERGED | DECLINED | SUPERSEDED). Follow-up pages come from
// the `next` link returned by listPRsPage.
func (c *bbClient) listPRsFirstURL(ws, repo, state string) string {
	return c.repoURL(ws, repo, "/pullrequests?pagelen=50&state="+url.QueryEscape(state)+
		"&fields="+url.QueryEscape(listPRFields))
}

// listPRsPage fetches one page of the PR list; next is "" on the last page.
func (c *bbClient) listPRsPage(pageURL string) (prs []PullRequest, next string, err error) {
	var pg page[PullRequest]
	if err := c.getJSON(pageURL, &pg); err != nil {
		return nil, "", err
	}
	return pg.Values, pg.Next, nil
}

// listPRs returns every PR for the state (used by `dump prs`; the UI pages
// on demand via listPRsPage instead).
func (c *bbClient) listPRs(ws, repo, state string) ([]PullRequest, error) {
	return getAll[PullRequest](c, c.listPRsFirstURL(ws, repo, state), 0)
}

// getPR returns the full PR detail (participants included).
func (c *bbClient) getPR(ws, repo string, id int) (*PullRequest, error) {
	var pr PullRequest
	if err := c.getJSON(c.repoURL(ws, repo, fmt.Sprintf("/pullrequests/%d", id)), &pr); err != nil {
		return nil, err
	}
	return &pr, nil
}

// diff returns the PR's raw unified diff text (the endpoint 302-redirects to
// the underlying repository diff).
func (c *bbClient) diff(ws, repo string, id int) (string, error) {
	return c.getText(c.repoURL(ws, repo, fmt.Sprintf("/pullrequests/%d/diff", id)))
}

// diffStat returns the per-file change summary (paginated, behind a 302).
func (c *bbClient) diffStat(ws, repo string, id int) ([]DiffStatEntry, error) {
	u := c.repoURL(ws, repo, fmt.Sprintf("/pullrequests/%d/diffstat?pagelen=100", id))
	return getAll[DiffStatEntry](c, u, 0)
}

// comments returns every comment on the PR (general + inline + replies).
func (c *bbClient) comments(ws, repo string, id int) ([]Comment, error) {
	u := c.repoURL(ws, repo, fmt.Sprintf("/pullrequests/%d/comments?pagelen=100", id))
	return getAll[Comment](c, u, 0)
}
