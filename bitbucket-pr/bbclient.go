package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

const bbAPIBase = "https://api.bitbucket.org/2.0"
const maxAvatarBytes = 2 << 20

// bbClient is a minimal Bitbucket Cloud API 2.0 client: Basic auth with an
// Atlassian email + API token (App Passwords died 2026-06), `next`-link
// pagination, and redirect-following for the diff/diffstat endpoints.
type bbClient struct {
	email string
	token string
	base  string // API root; overridden in tests
	http  *http.Client

	// userUUID caches the authenticated account's UUID for the cross-repo
	// PR views (memory first, then the disk cache, then GET /user).
	uuidMu   sync.Mutex
	userUUID string
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

// postJSON performs one authenticated POST. Unlike do(), it never retries —
// a retried POST could double-post a comment.
func (c *bbClient) postJSON(rawURL string, payload, out any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, rawURL, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.SetBasicAuth(c.email, c.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return &httpError{Status: resp.StatusCode, URL: rawURL, Body: strings.TrimSpace(string(body))}
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

// send performs one authenticated body-less request (DELETE/POST actions).
// Like postJSON it never retries — these mutate server state.
func (c *bbClient) send(method, rawURL string) error {
	return c.sendResponse(method, rawURL, nil)
}

// sendResponse is send with optional JSON response decoding. PR approval
// returns a participant and Decline returns the updated PR, while DELETE
// actions commonly answer 204 with no body.
func (c *bbClient) sendResponse(method, rawURL string, out any) error {
	req, err := http.NewRequest(method, rawURL, nil)
	if err != nil {
		return err
	}
	req.SetBasicAuth(c.email, c.token)
	if out != nil {
		req.Header.Set("Accept", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return &httpError{Status: resp.StatusCode, URL: rawURL, Body: strings.TrimSpace(string(body))}
	}
	if out != nil && resp.StatusCode != http.StatusNoContent {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("decode %s: %w", rawURL, err)
		}
	}
	return nil
}

// approvePR records approval by the authenticated user.
func (c *bbClient) approvePR(ws, repo string, prID int) (*Participant, error) {
	var participant Participant
	u := c.repoURL(ws, repo, fmt.Sprintf("/pullrequests/%d/approve", prID))
	if err := c.sendResponse(http.MethodPost, u, &participant); err != nil {
		return nil, err
	}
	return &participant, nil
}

// unapprovePR withdraws approval by the authenticated user.
func (c *bbClient) unapprovePR(ws, repo string, prID int) error {
	return c.send(http.MethodDelete,
		c.repoURL(ws, repo, fmt.Sprintf("/pullrequests/%d/approve", prID)))
}

// declinePR closes an OPEN pull request. The partial response contains the
// same fields as getPR so it can replace the detail and list snapshots.
func (c *bbClient) declinePR(ws, repo string, prID int) (*PullRequest, error) {
	var pr PullRequest
	u := c.repoURL(ws, repo, fmt.Sprintf("/pullrequests/%d/decline?fields=%s",
		prID, url.QueryEscape(detailPRFields)))
	if err := c.sendResponse(http.MethodPost, u, &pr); err != nil {
		return nil, err
	}
	return &pr, nil
}

// deleteComment removes a PR comment (204). A comment with visible replies
// is soft-deleted server-side: it stays in the collection as deleted:true.
func (c *bbClient) deleteComment(ws, repo string, prID, commentID int) error {
	return c.send(http.MethodDelete,
		c.repoURL(ws, repo, fmt.Sprintf("/pullrequests/%d/comments/%d", prID, commentID)))
}

// resolveComment marks an inline comment thread resolved (roots only; the
// API rejects replies and non-diff comments with 403, re-resolving with 409).
func (c *bbClient) resolveComment(ws, repo string, prID, commentID int) error {
	return c.send(http.MethodPost,
		c.repoURL(ws, repo, fmt.Sprintf("/pullrequests/%d/comments/%d/resolve", prID, commentID)))
}

// unresolveComment reopens a resolved thread (204).
func (c *bbClient) unresolveComment(ws, repo string, prID, commentID int) error {
	return c.send(http.MethodDelete,
		c.repoURL(ws, repo, fmt.Sprintf("/pullrequests/%d/comments/%d/resolve", prID, commentID)))
}

// postComment creates a PR comment: general (inline == nil), inline
// (path + to/from), or a reply (parentID > 0 — the server copies the
// parent's anchor, so replies pass inline == nil).
func (c *bbClient) postComment(ws, repo string, id int, body string, inline *InlineAnchor, parentID int) (*Comment, error) {
	payload := map[string]any{"content": map[string]any{"raw": body}}
	if inline != nil {
		// Multi-line ranges: start_to/start_from is the range START, to/from
		// its END (official API contract since 2025-09).
		in := map[string]any{"path": inline.Path}
		if inline.To != nil {
			in["to"] = *inline.To
		}
		if inline.From != nil {
			in["from"] = *inline.From
		}
		if inline.StartTo != nil {
			in["start_to"] = *inline.StartTo
		}
		if inline.StartFrom != nil {
			in["start_from"] = *inline.StartFrom
		}
		payload["inline"] = in
	}
	if parentID > 0 {
		payload["parent"] = map[string]any{"id": parentID}
	}
	var out Comment
	u := c.repoURL(ws, repo, fmt.Sprintf("/pullrequests/%d/comments", id))
	if err := c.postJSON(u, payload, &out); err != nil {
		return nil, err
	}
	return &out, nil
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

// avatar fetches a Bitbucket-provided account image. Credentials are added
// only when the image URL has the same origin as the configured API base;
// avatar links often redirect to a public Atlassian CDN and must never carry
// the Bitbucket API token there.
func (c *bbClient) avatar(rawURL string) ([]byte, error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("invalid avatar URL %q", rawURL)
	}
	base, err := url.Parse(c.base)
	if err != nil {
		return nil, fmt.Errorf("invalid API base: %w", err)
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && sameOrigin(u, base)) {
		return nil, fmt.Errorf("refusing non-HTTPS avatar URL %q", rawURL)
	}

	req, err := http.NewRequest(http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	if sameOrigin(u, base) {
		req.SetBasicAuth(c.email, c.token)
	}
	req.Header.Set("Accept", "image/png,image/jpeg,image/gif,image/webp")

	// Clone the client so redirect handling can explicitly strip credentials
	// across origins without changing the behavior of ordinary API requests.
	hc := *c.http
	previousCheck := hc.CheckRedirect
	hc.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		if next.URL.Scheme != "https" && base.Scheme != "http" {
			return fmt.Errorf("refusing avatar redirect to non-HTTPS URL %q", next.URL)
		}
		if len(via) > 0 && !sameOrigin(next.URL, via[0].URL) {
			next.Header.Del("Authorization")
		}
		if previousCheck != nil {
			return previousCheck(next, via)
		}
		return nil
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, &httpError{Status: resp.StatusCode, URL: rawURL, Body: strings.TrimSpace(string(body))}
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxAvatarBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxAvatarBytes {
		return nil, fmt.Errorf("avatar exceeds %d bytes", maxAvatarBytes)
	}
	return data, nil
}

func sameOrigin(a, b *url.URL) bool {
	return strings.EqualFold(a.Scheme, b.Scheme) && strings.EqualFold(a.Host, b.Host)
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
	"values.author.display_name,values.author.nickname,values.author.uuid,values.author.account_id,values.author.links.avatar.href," +
	"values.participants.role,values.participants.state,values.participants.approved," +
	"values.participants.user.display_name,values.participants.user.nickname,values.participants.user.uuid," +
	"values.participants.user.account_id,values.participants.user.links.avatar.href," +
	"values.source.branch.name,values.destination.branch.name," +
	"values.links.html.href"

// detailPRFields keeps the PR-detail request focused on fields used by the
// Overview header, reviewer list, actions, and Outline analysis. Bitbucket's
// default object also carries rendered HTML and other metadata that the TUI
// never reads.
const detailPRFields = "id,title,state,comment_count,updated_on,summary.raw," +
	"author.display_name,author.nickname,author.uuid,author.account_id,author.links.avatar.href," +
	"participants.role,participants.state,participants.approved," +
	"participants.user.display_name,participants.user.nickname,participants.user.uuid," +
	"participants.user.account_id,participants.user.links.avatar.href," +
	"source.branch.name,source.commit.hash,destination.branch.name,destination.commit.hash," +
	"links.html.href"

// diffStatFields and commentFields retain `next` because both endpoints are
// paginated and getAll follows the server-provided URL verbatim.
const diffStatFields = "next,values.status,values.lines_added,values.lines_removed," +
	"values.old.path,values.new.path"

const commentFields = "next,values.id,values.created_on,values.updated_on,values.deleted,values.pending," +
	"values.content.raw,values.parent.id," +
	"values.inline.path,values.inline.from,values.inline.to,values.inline.start_from," +
	"values.inline.start_to,values.inline.outdated," +
	"values.user.display_name,values.user.nickname,values.user.uuid," +
	"values.user.account_id,values.user.links.avatar.href," +
	"values.resolution.created_on,values.resolution.user.display_name," +
	"values.resolution.user.nickname,values.resolution.user.uuid,values.resolution.user.account_id"

// repoListFields trims the workspace repository list to what the picker
// renders plus the clone/browser URLs.
const repoListFields = "next," +
	"values.slug,values.name,values.full_name,values.description,values.updated_on," +
	"values.links.clone.name,values.links.clone.href,values.links.html.href"

// listReposFirstURL builds the first page of the workspace's repository
// list, most recently updated first. A non-empty query narrows it
// server-side by name substring; follow-up pages come from the `next` link
// returned by listReposPage.
func (c *bbClient) listReposFirstURL(ws, query string) string {
	u := fmt.Sprintf("%s/repositories/%s?pagelen=50&sort=-updated_on&fields=%s",
		c.base, url.PathEscape(ws), url.QueryEscape(repoListFields))
	if query != "" {
		// The q operand is a quoted string inside the query expression.
		quoted := `"` + strings.ReplaceAll(query, `"`, `\"`) + `"`
		u += "&q=" + url.QueryEscape(`name~`+quoted)
	}
	return u
}

// listReposPage fetches one page of the repository list; next is "" on the
// last page.
func (c *bbClient) listReposPage(pageURL string) (repos []Repository, next string, err error) {
	var pg page[Repository]
	if err := c.getJSON(pageURL, &pg); err != nil {
		return nil, "", err
	}
	return pg.Values, pg.Next, nil
}

// crossPRFields extends the PR-list payload with the owning repository —
// the cross-repo views (My PRs / Review) render and navigate by it.
const crossPRFields = listPRFields + ",values.destination.repository.full_name"

// crossPRMaxPages caps the cross-repo lists: they are dashboards, not
// exhaustive archives.
const crossPRMaxPages = 4

// currentUserUUID resolves the authenticated account's UUID, cached in
// memory and on disk (keyed by email, so switching accounts refetches).
func (c *bbClient) currentUserUUID() (string, error) {
	c.uuidMu.Lock()
	defer c.uuidMu.Unlock()
	if c.userUUID != "" {
		return c.userUUID, nil
	}
	if uuid := loadUserUUIDCache(c.email); uuid != "" {
		c.userUUID = uuid
		return uuid, nil
	}
	var out struct {
		UUID string `json:"uuid"`
	}
	if err := c.getJSON(c.base+"/user?fields=uuid", &out); err != nil {
		return "", err
	}
	if out.UUID == "" {
		return "", fmt.Errorf("bitbucket api: /user returned no uuid")
	}
	saveUserUUIDCache(c.email, out.UUID)
	c.userUUID = out.UUID
	return out.UUID, nil
}

// listMyPRs returns the user's open PRs across the workspace, newest first
// (the "Your pull requests" dashboard). One endpoint covers all repos.
func (c *bbClient) listMyPRs(ws, uuid string) ([]PullRequest, error) {
	u := fmt.Sprintf("%s/workspaces/%s/pullrequests/%s?state=OPEN&pagelen=50&fields=%s",
		c.base, url.PathEscape(ws), url.PathEscape(uuid), url.QueryEscape(crossPRFields))
	prs, err := getAll[PullRequest](c, u, crossPRMaxPages)
	if err != nil {
		return nil, err
	}
	sortPRsByUpdated(prs)
	return prs, nil
}

// listReviewingPRs returns open PRs that list uuid as a reviewer, merged
// newest-first ("Pull requests to review"). Bitbucket has no cross-repo
// endpoint for the reviewer role, so each repository is queried separately
// with a small worker pool; failed repos are reported but do not sink the
// rest of the result.
func (c *bbClient) listReviewingPRs(ws, uuid string, slugs []string) (prs []PullRequest, failed []string, err error) {
	query := fmt.Sprintf(`state="OPEN" AND reviewers.uuid="%s"`, uuid)
	var (
		mu  sync.Mutex
		wg  sync.WaitGroup
		sem = make(chan struct{}, 6)
	)
	for _, slug := range slugs {
		wg.Add(1)
		go func(slug string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			u := c.repoURL(ws, slug, "/pullrequests?pagelen=50&q="+url.QueryEscape(query)+
				"&fields="+url.QueryEscape(crossPRFields))
			got, _, pageErr := c.listPRsPage(u)
			mu.Lock()
			defer mu.Unlock()
			if pageErr != nil {
				debugf("review prs %s: %v", slug, pageErr)
				failed = append(failed, slug)
				return
			}
			prs = append(prs, got...)
		}(slug)
	}
	wg.Wait()
	if len(prs) == 0 && len(failed) > 0 && len(failed) == len(slugs) {
		return nil, failed, fmt.Errorf("全%d件のリポジトリで取得に失敗しました", len(slugs))
	}
	sortPRsByUpdated(prs)
	sort.Strings(failed)
	return prs, failed, nil
}

func sortPRsByUpdated(prs []PullRequest) {
	sort.SliceStable(prs, func(i, j int) bool { return prs[i].UpdatedOn.After(prs[j].UpdatedOn) })
}

// repoDetailFields is repoListFields without the values. prefix, for the
// single-repository endpoint (the Outline tab's clone shortcut needs the
// clone URLs when the viewer was not launched through the picker).
const repoDetailFields = "slug,name,full_name,description,updated_on," +
	"links.clone.name,links.clone.href,links.html.href"

// getRepo returns one repository (clone/browser links included).
func (c *bbClient) getRepo(ws, repo string) (*Repository, error) {
	var r Repository
	u := c.repoURL(ws, repo, "?fields="+url.QueryEscape(repoDetailFields))
	if err := c.getJSON(u, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

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
	u := c.repoURL(ws, repo, fmt.Sprintf("/pullrequests/%d?fields=%s", id, url.QueryEscape(detailPRFields)))
	if err := c.getJSON(u, &pr); err != nil {
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
	u := c.repoURL(ws, repo, fmt.Sprintf("/pullrequests/%d/diffstat?pagelen=100&fields=%s",
		id, url.QueryEscape(diffStatFields)))
	return getAll[DiffStatEntry](c, u, 0)
}

// comments returns every comment on the PR (general + inline + replies).
func (c *bbClient) comments(ws, repo string, id int) ([]Comment, error) {
	u := c.repoURL(ws, repo, fmt.Sprintf("/pullrequests/%d/comments?pagelen=100&fields=%s",
		id, url.QueryEscape(commentFields)))
	return getAll[Comment](c, u, 0)
}
