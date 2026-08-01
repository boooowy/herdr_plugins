package main

import (
	"encoding/base64"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestNewJiraClientFromEnvRequiresAllVariables(t *testing.T) {
	for _, missing := range []string{"JIRA_URL", "JIRA_USERNAME", "JIRA_API_TOKEN"} {
		t.Run(missing, func(t *testing.T) {
			t.Setenv("JIRA_URL", "https://jira.example.test")
			t.Setenv("JIRA_USERNAME", "user@example.com")
			t.Setenv("JIRA_API_TOKEN", "jira-token")
			t.Setenv(missing, "")
			if got := newJiraClientFromEnv(time.Second); got != nil {
				t.Fatalf("missing %s must disable Jira avatar lookup", missing)
			}
		})
	}

	t.Setenv("JIRA_URL", "https://jira.example.test")
	t.Setenv("JIRA_USERNAME", "user@example.com")
	t.Setenv("JIRA_API_TOKEN", "jira-token")
	if got := newJiraClientFromEnv(time.Second); got == nil {
		t.Fatal("complete Jira environment must enable avatar lookup")
	}
}

func TestJiraAvatarURLUsesAccountIDAndBasicAuth(t *testing.T) {
	client, err := newJiraClient("https://jira.example.test", "user@example.com", "jira-token", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	client.http.Transport = avatarRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/rest/api/3/user" || req.URL.Query().Get("accountId") != "account-1" {
			t.Errorf("request URL = %s", req.URL)
		}
		wantAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte("user@example.com:jira-token"))
		if got := req.Header.Get("Authorization"); got != wantAuth {
			t.Errorf("Authorization = %q", got)
		}
		body := `{"avatarUrls":{"16x16":"https://cdn.example.test/16","48x48":"https://cdn.example.test/48"}}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    req,
		}, nil
	})

	got, err := client.avatarURL("account-1")
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://cdn.example.test/48" {
		t.Errorf("avatar URL = %q", got)
	}
}
