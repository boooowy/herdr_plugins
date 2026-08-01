package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
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
				!strings.Contains(f, "values.author.links.avatar.href") {
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
