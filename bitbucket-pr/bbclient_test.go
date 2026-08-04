package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func testClient(srv *httptest.Server) *bbClient {
	c := newBBClient("u@example.com", "tok", 5*time.Second)
	c.base = srv.URL + "/2.0"
	return c
}

func TestPaginationFollowsNext(t *testing.T) {
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/2.0/repositories/ws/repo/pullrequests", func(w http.ResponseWriter, r *http.Request) {
		if u, p, ok := r.BasicAuth(); !ok || u != "u@example.com" || p != "tok" {
			w.WriteHeader(401)
			return
		}
		switch r.URL.Query().Get("page") {
		case "":
			// `next` is an absolute URL to be followed verbatim; no `size`
			// or `previous` fields at all.
			fmt.Fprintf(w, `{"values":[{"id":1},{"id":2}],"next":"%s/2.0/repositories/ws/repo/pullrequests?page=2"}`, srv.URL)
		case "2":
			fmt.Fprintf(w, `{"values":[{"id":3}],"next":"%s/2.0/repositories/ws/repo/pullrequests?page=3"}`, srv.URL)
		case "3":
			fmt.Fprint(w, `{"values":[{"id":4}]}`)
		}
	})
	srv = httptest.NewServer(mux)
	defer srv.Close()

	prs, err := testClient(srv).listPRs("ws", "repo", "OPEN")
	if err != nil {
		t.Fatal(err)
	}
	if len(prs) != 4 || prs[0].ID != 1 || prs[3].ID != 4 {
		t.Errorf("got %d PRs: %+v", len(prs), prs)
	}
}

func TestListPRsPageReturnsNext(t *testing.T) {
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/2.0/repositories/ws/repo/pullrequests", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("page") {
		case "":
			if f := r.URL.Query().Get("fields"); !strings.Contains(f, "values.title") ||
				!strings.Contains(f, "values.author.account_id") ||
				!strings.Contains(f, "values.participants.user.account_id") ||
				!strings.Contains(f, "values.participants.user.links.avatar.href") {
				t.Errorf("first URL should trim the payload via fields=, got %q", f)
			}
			fmt.Fprintf(w, `{"values":[{"id":1},{"id":2}],"next":"%s/2.0/repositories/ws/repo/pullrequests?page=2"}`, srv.URL)
		case "2":
			fmt.Fprint(w, `{"values":[{"id":3}]}`)
		}
	})
	srv = httptest.NewServer(mux)
	defer srv.Close()

	c := testClient(srv)
	prs, next, err := c.listPRsPage(c.listPRsFirstURL("ws", "repo", "OPEN"))
	if err != nil {
		t.Fatal(err)
	}
	if len(prs) != 2 || prs[0].ID != 1 || next == "" {
		t.Fatalf("page 1: %d PRs, next=%q", len(prs), next)
	}
	prs, next, err = c.listPRsPage(next)
	if err != nil {
		t.Fatal(err)
	}
	if len(prs) != 1 || prs[0].ID != 3 || next != "" {
		t.Errorf("page 2: %d PRs, next=%q", len(prs), next)
	}
}

func TestDiffFollowsRedirect(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/2.0/repositories/ws/repo/pullrequests/5/diff", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/internal/diff-blob", http.StatusFound)
	})
	mux.HandleFunc("/internal/diff-blob", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "diff --git a/x b/x\n")
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	text, err := testClient(srv).diff("ws", "repo", 5)
	if err != nil {
		t.Fatal(err)
	}
	if text != "diff --git a/x b/x\n" {
		t.Errorf("diff text = %q", text)
	}
}

func TestRetryOn429(t *testing.T) {
	var calls atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/2.0/repositories/ws/repo/pullrequests/9", func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		fmt.Fprint(w, `{"id":9,"title":"ok"}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	pr, err := testClient(srv).getPR("ws", "repo", 9)
	if err != nil {
		t.Fatal(err)
	}
	if pr.Title != "ok" || calls.Load() != 2 {
		t.Errorf("pr=%+v calls=%d", pr, calls.Load())
	}
}

func TestRetryOnTransportError(t *testing.T) {
	var calls atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/2.0/repositories/ws/repo/pullrequests/9", func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			// Kill the connection mid-flight: the client sees a transport
			// error (EOF), not an HTTP status.
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Fatal(err)
			}
			conn.Close()
			return
		}
		fmt.Fprint(w, `{"id":9,"title":"ok"}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	pr, err := testClient(srv).getPR("ws", "repo", 9)
	if err != nil {
		t.Fatal(err)
	}
	if pr.Title != "ok" || calls.Load() != 2 {
		t.Errorf("pr=%+v calls=%d", pr, calls.Load())
	}
}

func TestDetailEndpointsTrimPayloadFields(t *testing.T) {
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/2.0/repositories/ws/repo/pullrequests/5", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("fields"); got != detailPRFields {
			t.Errorf("detail fields = %q, want %q", got, detailPRFields)
		}
		fmt.Fprint(w, `{"id":5,"title":"trimmed"}`)
	})
	mux.HandleFunc("/2.0/repositories/ws/repo/pullrequests/5/diffstat", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("fields"); got != diffStatFields {
			t.Errorf("diffstat fields = %q, want %q", got, diffStatFields)
		}
		fmt.Fprint(w, `{"values":[{"status":"modified","new":{"path":"a.go"}}]}`)
	})
	mux.HandleFunc("/2.0/repositories/ws/repo/pullrequests/5/comments", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("fields"); got != commentFields {
			t.Errorf("comment fields = %q, want %q", got, commentFields)
		}
		if r.URL.Query().Get("page") == "2" {
			fmt.Fprint(w, `{"values":[{"id":2}]}`)
			return
		}
		fmt.Fprintf(w, `{"values":[{"id":1}],"next":"%s/2.0/repositories/ws/repo/pullrequests/5/comments?page=2&fields=%s"}`,
			srv.URL, url.QueryEscape(commentFields))
	})
	srv = httptest.NewServer(mux)
	defer srv.Close()

	c := testClient(srv)
	pr, err := c.getPR("ws", "repo", 5)
	if err != nil || pr.Title != "trimmed" {
		t.Fatalf("getPR: pr=%+v err=%v", pr, err)
	}
	stats, err := c.diffStat("ws", "repo", 5)
	if err != nil || len(stats) != 1 || stats[0].Path() != "a.go" {
		t.Fatalf("diffStat: stats=%+v err=%v", stats, err)
	}
	comments, err := c.comments("ws", "repo", 5)
	if err != nil || len(comments) != 2 || comments[1].ID != 2 {
		t.Fatalf("comments: comments=%+v err=%v", comments, err)
	}
}

func TestAvatarAuthIsStrippedOnCrossOriginRedirect(t *testing.T) {
	var cdnAuth string
	cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cdnAuth = r.Header.Get("Authorization")
		fmt.Fprint(w, "avatar")
	}))
	defer cdn.Close()

	var apiAuthOK bool
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		apiAuthOK = ok && u == "u@example.com" && p == "tok"
		http.Redirect(w, r, cdn.URL+"/image", http.StatusFound)
	}))
	defer api.Close()

	c := testClient(api)
	data, err := c.avatar(api.URL + "/avatar")
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "avatar" || !apiAuthOK {
		t.Errorf("data=%q apiAuthOK=%v", data, apiAuthOK)
	}
	if cdnAuth != "" {
		t.Errorf("authorization leaked to CDN: %q", cdnAuth)
	}
}

func TestAvatarRejectsOversizedBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(make([]byte, maxAvatarBytes+1))
	}))
	defer srv.Close()
	c := testClient(srv)
	if _, err := c.avatar(srv.URL + "/avatar"); err == nil {
		t.Error("oversized avatar body must fail")
	}
}

func TestPostComment(t *testing.T) {
	var got map[string]any
	mux := http.NewServeMux()
	mux.HandleFunc("/2.0/repositories/ws/repo/pullrequests/37/comments", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("content-type = %s", ct)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		fmt.Fprint(w, `{"id":999,"content":{"raw":"ok"}}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	to := 496
	c, err := testClient(srv).postComment("ws", "repo", 37, "テスト本文",
		&InlineAnchor{Path: "a.py", To: &to}, 123)
	if err != nil {
		t.Fatal(err)
	}
	if c.ID != 999 {
		t.Errorf("returned comment id = %d", c.ID)
	}
	if got["content"].(map[string]any)["raw"] != "テスト本文" {
		t.Errorf("content: %v", got["content"])
	}
	inline := got["inline"].(map[string]any)
	if inline["path"] != "a.py" || inline["to"] != float64(496) {
		t.Errorf("inline: %v", inline)
	}
	if got["parent"].(map[string]any)["id"] != float64(123) {
		t.Errorf("parent: %v", got["parent"])
	}
}

func TestPostCommentMultiLineRange(t *testing.T) {
	var got map[string]any
	mux := http.NewServeMux()
	mux.HandleFunc("/2.0/repositories/ws/repo/pullrequests/37/comments", func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&got)
		fmt.Fprint(w, `{"id":1}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	start, end := 13, 15
	_, err := testClient(srv).postComment("ws", "repo", 37, "range",
		&InlineAnchor{Path: "a.py", StartTo: &start, To: &end}, 0)
	if err != nil {
		t.Fatal(err)
	}
	in := got["inline"].(map[string]any)
	if in["start_to"] != float64(13) || in["to"] != float64(15) {
		t.Errorf("inline = %v", in)
	}
	if _, ok := in["start_from"]; ok {
		t.Error("start_from must be omitted for new-side ranges")
	}
}

func TestPostCommentGeneralOmitsAnchor(t *testing.T) {
	var got map[string]any
	mux := http.NewServeMux()
	mux.HandleFunc("/2.0/repositories/ws/repo/pullrequests/37/comments", func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&got)
		fmt.Fprint(w, `{"id":1}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	if _, err := testClient(srv).postComment("ws", "repo", 37, "hi", nil, 0); err != nil {
		t.Fatal(err)
	}
	if _, ok := got["inline"]; ok {
		t.Error("general comment must not send inline")
	}
	if _, ok := got["parent"]; ok {
		t.Error("root comment must not send parent")
	}
}

func TestCommentActions(t *testing.T) {
	var gotMethod string
	mux := http.NewServeMux()
	mux.HandleFunc("/2.0/repositories/ws/repo/pullrequests/37/comments/5", func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/2.0/repositories/ws/repo/pullrequests/37/comments/5/resolve", func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		if r.Method == http.MethodPost {
			fmt.Fprint(w, `{"type":"comment_resolution"}`) // 200
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := testClient(srv)

	if err := c.deleteComment("ws", "repo", 37, 5); err != nil || gotMethod != http.MethodDelete {
		t.Errorf("delete: err=%v method=%s", err, gotMethod)
	}
	if err := c.resolveComment("ws", "repo", 37, 5); err != nil || gotMethod != http.MethodPost {
		t.Errorf("resolve: err=%v method=%s", err, gotMethod)
	}
	if err := c.unresolveComment("ws", "repo", 37, 5); err != nil || gotMethod != http.MethodDelete {
		t.Errorf("unresolve: err=%v method=%s", err, gotMethod)
	}
}

func TestPullRequestActions(t *testing.T) {
	var approveMethod, unapproveMethod, declineMethod string
	mux := http.NewServeMux()
	mux.HandleFunc("/2.0/repositories/ws/repo/pullrequests/37/approve", func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if !ok || u != "u@example.com" || p != "tok" {
			t.Errorf("missing basic auth")
		}
		switch r.Method {
		case http.MethodPost:
			approveMethod = r.Method
			fmt.Fprint(w, `{"user":{"account_id":"me","display_name":"Me"},"role":"PARTICIPANT","approved":true,"state":"approved"}`)
		case http.MethodDelete:
			unapproveMethod = r.Method
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/2.0/repositories/ws/repo/pullrequests/37/decline", func(w http.ResponseWriter, r *http.Request) {
		declineMethod = r.Method
		if got := r.URL.Query().Get("fields"); got != detailPRFields {
			t.Errorf("decline fields = %q, want %q", got, detailPRFields)
		}
		fmt.Fprint(w, `{"id":37,"title":"closed","state":"DECLINED"}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := testClient(srv)

	participant, err := c.approvePR("ws", "repo", 37)
	if err != nil || participant.User.AccountID != "me" || !participant.Approved {
		t.Fatalf("approve: participant=%+v err=%v", participant, err)
	}
	if err := c.unapprovePR("ws", "repo", 37); err != nil {
		t.Fatalf("unapprove: %v", err)
	}
	pr, err := c.declinePR("ws", "repo", 37)
	if err != nil || pr.State != "DECLINED" {
		t.Fatalf("decline: pr=%+v err=%v", pr, err)
	}
	if approveMethod != http.MethodPost || unapproveMethod != http.MethodDelete || declineMethod != http.MethodPost {
		t.Errorf("methods: approve=%s unapprove=%s decline=%s", approveMethod, unapproveMethod, declineMethod)
	}
}

func TestPullRequestActionDoesNotRetry(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"error":{"message":"temporary"}}`)
	}))
	defer srv.Close()

	_, err := testClient(srv).approvePR("ws", "repo", 37)
	if err == nil {
		t.Fatal("approve should fail")
	}
	if calls.Load() != 1 {
		t.Errorf("mutation calls = %d, want 1", calls.Load())
	}
}

func TestCommentActionErrorSurfaces(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict) // already resolved
		fmt.Fprint(w, `{"error":{"message":"already resolved"}}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	err := testClient(srv).resolveComment("ws", "repo", 37, 5)
	he, ok := err.(*httpError)
	if !ok || he.Status != http.StatusConflict {
		t.Errorf("err = %v (%T)", err, err)
	}
}

func TestHTTPErrorSurfacesStatus(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		fmt.Fprint(w, `{"error":{"message":"Token is invalid"}}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	_, err := testClient(srv).getPR("ws", "repo", 1)
	he, ok := err.(*httpError)
	if !ok {
		t.Fatalf("err = %v (%T), want *httpError", err, err)
	}
	if he.Status != 401 {
		t.Errorf("status = %d", he.Status)
	}
}

func TestListReposFirstURLBuildsQuery(t *testing.T) {
	c := newBBClient("u@example.com", "tok", 5*time.Second)

	u := c.listReposFirstURL("my-ws", "")
	if !strings.HasPrefix(u, "https://api.bitbucket.org/2.0/repositories/my-ws?") {
		t.Errorf("base URL = %q", u)
	}
	parsed, err := url.Parse(u)
	if err != nil {
		t.Fatal(err)
	}
	q := parsed.Query()
	if q.Get("sort") != "-updated_on" || q.Get("pagelen") != "50" {
		t.Errorf("sort/pagelen: %v", q)
	}
	if f := q.Get("fields"); !strings.Contains(f, "values.slug") || !strings.Contains(f, "values.links.clone.href") {
		t.Errorf("fields must trim the payload, got %q", f)
	}
	if q.Get("q") != "" {
		t.Errorf("no q param expected without a query, got %q", q.Get("q"))
	}

	parsed, err = url.Parse(c.listReposFirstURL("my-ws", `ga"te`))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := parsed.Query().Get("q"), `name~"ga\"te"`; got != want {
		t.Errorf("q = %q, want %q (quoted and escaped)", got, want)
	}
}

func TestListReposPageAndGetRepo(t *testing.T) {
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/2.0/repositories/ws", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"values":[{"slug":"a","full_name":"ws/a","links":{"clone":[{"name":"ssh","href":"git@bitbucket.org:ws/a.git"}]}}],"next":"%s/2.0/repositories/ws?page=2"}`, srv.URL)
	})
	mux.HandleFunc("/2.0/repositories/ws/a", func(w http.ResponseWriter, r *http.Request) {
		if f := r.URL.Query().Get("fields"); !strings.Contains(f, "links.clone.href") {
			t.Errorf("getRepo should trim via fields=, got %q", f)
		}
		fmt.Fprint(w, `{"slug":"a","full_name":"ws/a","links":{"clone":[{"name":"https","href":"https://bitbucket.org/ws/a.git"}]}}`)
	})
	srv = httptest.NewServer(mux)
	defer srv.Close()

	c := testClient(srv)
	repos, next, err := c.listReposPage(c.listReposFirstURL("ws", ""))
	if err != nil {
		t.Fatal(err)
	}
	if len(repos) != 1 || repos[0].Slug != "a" || next == "" {
		t.Errorf("repos=%+v next=%q", repos, next)
	}
	if got := repos[0].CloneURL("ssh"); got != "git@bitbucket.org:ws/a.git" {
		t.Errorf("CloneURL(ssh) = %q", got)
	}
	if got := repos[0].CloneURL("https"); got != "" {
		t.Errorf("CloneURL(https) = %q, want empty when absent", got)
	}

	repo, err := c.getRepo("ws", "a")
	if err != nil {
		t.Fatal(err)
	}
	if repo.CloneURL("https") != "https://bitbucket.org/ws/a.git" {
		t.Errorf("getRepo clone url = %q", repo.CloneURL("https"))
	}
}

func TestCurrentUserUUIDCachesOnDisk(t *testing.T) {
	t.Setenv("HERDR_PLUGIN_STATE_DIR", t.TempDir())
	calls := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/2.0/user", func(w http.ResponseWriter, r *http.Request) {
		calls++
		fmt.Fprint(w, `{"uuid":"{abc-123}"}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := testClient(srv)
	for i := 0; i < 2; i++ {
		uuid, err := c.currentUserUUID()
		if err != nil {
			t.Fatal(err)
		}
		if uuid != "{abc-123}" {
			t.Errorf("uuid = %q", uuid)
		}
	}
	if calls != 1 {
		t.Errorf("GET /user calls = %d, want 1 (memory cache)", calls)
	}

	// A fresh client (same email) must hit the disk cache, not the API.
	c2 := testClient(srv)
	if _, err := c2.currentUserUUID(); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Errorf("GET /user calls = %d, want 1 (disk cache)", calls)
	}

	// A different account must not reuse the cached identity.
	c3 := testClient(srv)
	c3.email = "other@example.com"
	if _, err := c3.currentUserUUID(); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Errorf("GET /user calls = %d, want 2 (different email refetches)", calls)
	}
}

func TestListMyPRsSortsNewestFirst(t *testing.T) {
	t.Setenv("HERDR_PLUGIN_STATE_DIR", t.TempDir())
	mux := http.NewServeMux()
	mux.HandleFunc("/2.0/workspaces/ws/pullrequests/{abc}", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("state") != "OPEN" {
			t.Errorf("state = %q, want OPEN", r.URL.Query().Get("state"))
		}
		if f := r.URL.Query().Get("fields"); !strings.Contains(f, "values.destination.repository.full_name") {
			t.Errorf("fields must include the owning repository, got %q", f)
		}
		fmt.Fprint(w, `{"values":[
			{"id":1,"updated_on":"2026-08-01T00:00:00Z","destination":{"repository":{"full_name":"ws/repo-a"}}},
			{"id":2,"updated_on":"2026-08-03T00:00:00Z","destination":{"repository":{"full_name":"ws/repo-b"}}}]}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	prs, err := testClient(srv).listMyPRs("ws", "{abc}")
	if err != nil {
		t.Fatal(err)
	}
	if len(prs) != 2 || prs[0].ID != 2 || prs[1].ID != 1 {
		t.Fatalf("prs = %+v, want newest first", prs)
	}
	if got := prs[0].RepoSlug(); got != "repo-b" {
		t.Errorf("RepoSlug = %q", got)
	}
}

func TestListReviewingPRsMergesAndReportsFailures(t *testing.T) {
	mux := http.NewServeMux()
	pr := func(id int, updated, repo string) string {
		return fmt.Sprintf(`{"id":%d,"updated_on":"%s","destination":{"repository":{"full_name":"ws/%s"}}}`, id, updated, repo)
	}
	mux.HandleFunc("/2.0/repositories/ws/repo-a/pullrequests", func(w http.ResponseWriter, r *http.Request) {
		if q := r.URL.Query().Get("q"); !strings.Contains(q, `reviewers.uuid="{abc}"`) || !strings.Contains(q, `state="OPEN"`) {
			t.Errorf("q = %q", q)
		}
		fmt.Fprintf(w, `{"values":[%s]}`, pr(1, "2026-08-01T00:00:00Z", "repo-a"))
	})
	mux.HandleFunc("/2.0/repositories/ws/repo-b/pullrequests", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"values":[%s]}`, pr(2, "2026-08-03T00:00:00Z", "repo-b"))
	})
	mux.HandleFunc("/2.0/repositories/ws/repo-c/pullrequests", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	prs, failed, err := testClient(srv).listReviewingPRs("ws", "{abc}", []string{"repo-a", "repo-b", "repo-c"})
	if err != nil {
		t.Fatal(err)
	}
	if len(prs) != 2 || prs[0].ID != 2 || prs[1].ID != 1 {
		t.Fatalf("prs = %+v, want merged newest first", prs)
	}
	if len(failed) != 1 || failed[0] != "repo-c" {
		t.Errorf("failed = %v", failed)
	}
}
