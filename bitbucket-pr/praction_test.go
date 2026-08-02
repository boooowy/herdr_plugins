package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func prActionFixture(state string) (*app, *detailView) {
	a := &app{
		w:        120,
		h:        30,
		ctx:      uiContext{Workspace: "ws", Repo: "repo"},
		resultCh: make(chan func(*app), 4),
		detail:   map[int]*prDetail{},
		prsState: "OPEN",
	}
	pr := &PullRequest{ID: 37, Title: "PR action", State: state}
	a.detailFor(37).pr = pr
	a.prs = []PullRequest{*pr}
	v := &detailView{prID: 37, summary: pr}
	a.stack = []view{&listView{}, v}
	return a, v
}

func applyActionResult(t *testing.T, a *app) {
	t.Helper()
	select {
	case apply := <-a.resultCh:
		apply(a)
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for PR action")
	}
}

func TestPRActionConfirmationFlow(t *testing.T) {
	a, v := prActionFixture("OPEN")
	cases := []struct {
		key  rune
		kind detailActionKind
		text string
	}{
		{'a', detailActionApprove, "承認しますか"},
		{'A', detailActionUnapprove, "承認を取り消しますか"},
		{'X', detailActionDecline, "Declineしますか"},
	}
	for _, tc := range cases {
		v.handle(a, Key{Kind: KeyRune, R: tc.key})
		if v.pendingAction.kind != tc.kind || !strings.Contains(v.pendingAction.prompt, tc.text) {
			t.Fatalf("key %q: pending=%+v", tc.key, v.pendingAction)
		}
		if spec := v.modal(a); spec == nil || spec.Title != tc.kind.name() {
			t.Fatalf("key %q: modal=%+v", tc.key, spec)
		}
		v.handle(a, Key{Kind: KeyRune, R: 'n'})
		if v.pendingAction.kind != detailActionNone || !strings.Contains(a.status, "取り消し") {
			t.Fatalf("cancel %q: pending=%+v status=%q", tc.key, v.pendingAction, a.status)
		}
	}

	v.handle(a, Key{Kind: KeyRune, R: 'X'})
	if !v.interceptEsc(a) || v.pendingAction.kind != detailActionNone || !strings.Contains(a.status, "取り消し") {
		t.Errorf("Esc cancel: pending=%+v status=%q", v.pendingAction, a.status)
	}
}

func TestPRActionRequiresLoadedOpenPR(t *testing.T) {
	a, v := prActionFixture("DECLINED")
	v.handle(a, Key{Kind: KeyRune, R: 'a'})
	if v.pendingAction.kind != detailActionNone || !strings.Contains(a.status, "OPENのPRのみ") {
		t.Errorf("declined: pending=%+v status=%q", v.pendingAction, a.status)
	}

	a.detailFor(37).pr = nil
	v.handle(a, Key{Kind: KeyRune, R: 'a'})
	if v.pendingAction.kind != detailActionNone || !strings.Contains(a.status, "読み込み完了後") {
		t.Errorf("loading: pending=%+v status=%q", v.pendingAction, a.status)
	}

	a.detailFor(37).pr = &PullRequest{ID: 37, State: "OPEN"}
	v.prActionRunning = true
	v.handle(a, Key{Kind: KeyRune, R: 'a'})
	if v.pendingAction.kind != detailActionNone || !strings.Contains(a.status, "実行中") {
		t.Errorf("running: pending=%+v status=%q", v.pendingAction, a.status)
	}
}

func TestApproveUpdatesDetailListAndDiskCache(t *testing.T) {
	t.Setenv("HERDR_PLUGIN_STATE_DIR", t.TempDir())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/approve") {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		fmt.Fprint(w, `{"user":{"account_id":"me","display_name":"Me"},"role":"PARTICIPANT","approved":true,"state":"approved"}`)
	}))
	defer srv.Close()

	a, v := prActionFixture("OPEN")
	a.client = testClient(srv)
	savePRCache("ws", "repo", "OPEN", a.prs)
	v.handle(a, Key{Kind: KeyRune, R: 'a'})
	v.handle(a, Key{Kind: KeyRune, R: 'y'})
	if !v.prActionRunning {
		t.Fatal("approval should be in flight")
	}
	applyActionResult(t, a)

	if v.prActionRunning || !strings.Contains(a.status, "承認しました") {
		t.Fatalf("running=%v status=%q", v.prActionRunning, a.status)
	}
	if reviewers := pullRequestReviewers(a.detailFor(37).pr); len(reviewers) != 1 || reviewers[0].Status != reviewerApproved {
		t.Errorf("detail reviewers = %+v", reviewers)
	}
	if reviewers := pullRequestReviewers(&a.prs[0]); len(reviewers) != 1 || reviewers[0].Status != reviewerApproved {
		t.Errorf("list reviewers = %+v", reviewers)
	}
	cached := loadPRCache("ws", "repo", "OPEN")
	if len(cached) != 1 || len(pullRequestReviewers(&cached[0])) != 1 {
		t.Errorf("cached PRs = %+v", cached)
	}
}

func TestUnapproveRefreshesPRDetail(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		switch r.Method {
		case http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		case http.MethodGet:
			fmt.Fprint(w, `{"id":37,"title":"PR action","state":"OPEN","participants":[]}`)
		}
	}))
	defer srv.Close()

	a, v := prActionFixture("OPEN")
	approved := Participant{Role: "PARTICIPANT", Approved: true, State: "approved"}
	approved.User.AccountID = "me"
	a.detailFor(37).pr.Participants = []Participant{approved}
	a.prs[0].Participants = []Participant{approved}
	a.client = testClient(srv)
	v.handle(a, Key{Kind: KeyRune, R: 'A'})
	v.handle(a, Key{Kind: KeyRune, R: 'y'})
	applyActionResult(t, a)

	if calls != 2 || len(pullRequestReviewers(a.detailFor(37).pr)) != 0 {
		t.Errorf("calls=%d reviewers=%+v", calls, pullRequestReviewers(a.detailFor(37).pr))
	}
	if !strings.Contains(a.status, "取り消しました") {
		t.Errorf("status = %q", a.status)
	}
}

func TestDeclineUpdatesDetailAndRemovesOpenListEntry(t *testing.T) {
	t.Setenv("HERDR_PLUGIN_STATE_DIR", t.TempDir())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/decline") {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		fmt.Fprint(w, `{"id":37,"title":"PR action","state":"DECLINED"}`)
	}))
	defer srv.Close()

	a, v := prActionFixture("OPEN")
	a.client = testClient(srv)
	savePRCache("ws", "repo", "OPEN", a.prs)
	v.handle(a, Key{Kind: KeyRune, R: 'X'})
	v.handle(a, Key{Kind: KeyRune, R: 'y'})
	applyActionResult(t, a)

	if a.detailFor(37).pr.State != "DECLINED" || len(a.prs) != 0 {
		t.Errorf("detail=%+v prs=%+v", a.detailFor(37).pr, a.prs)
	}
	if cached := loadPRCache("ws", "repo", "OPEN"); len(cached) != 0 {
		t.Errorf("cached OPEN PRs = %+v", cached)
	}
	if strings.Contains(v.footer(a), "a:承認") || !strings.Contains(a.status, "Declineしました") {
		t.Errorf("footer=%q status=%q", v.footer(a), a.status)
	}
}

func TestUnapproveSuccessRefreshFailureIsReportedSeparately(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":{"message":"read denied"}}`)
	}))
	defer srv.Close()

	a, v := prActionFixture("OPEN")
	a.client = testClient(srv)
	v.handle(a, Key{Kind: KeyRune, R: 'A'})
	v.handle(a, Key{Kind: KeyRune, R: 'y'})
	applyActionResult(t, a)

	if v.prActionRunning || !strings.Contains(a.status, "承認取消しました") ||
		!strings.Contains(a.status, "表示更新に失敗") || !strings.Contains(a.status, "rで再読込") {
		t.Errorf("running=%v status=%q", v.prActionRunning, a.status)
	}
}

func TestPRActionFailureClearsRunningState(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"error":{"message":"write scope required"}}`)
	}))
	defer srv.Close()

	a, v := prActionFixture("OPEN")
	a.client = testClient(srv)
	v.handle(a, Key{Kind: KeyRune, R: 'a'})
	v.handle(a, Key{Kind: KeyRune, R: 'y'})
	applyActionResult(t, a)

	if v.prActionRunning || !strings.Contains(a.status, "承認に失敗") ||
		!strings.Contains(a.status, "write scope required") {
		t.Errorf("running=%v status=%q", v.prActionRunning, a.status)
	}
}
