package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	maxJiraUserBytes = 64 << 10
	jiraBulkMaxUsers = 10
)

// jiraClient resolves organization-visible Atlassian avatars through the
// authenticated Jira user API. It is optional: Bitbucket remains fully
// usable when the Jira environment is not configured.
type jiraClient struct {
	username string
	token    string
	base     *url.URL
	http     *http.Client
}

func newJiraClientFromEnv(timeout time.Duration) *jiraClient {
	base := strings.TrimSpace(os.Getenv("JIRA_URL"))
	username := strings.TrimSpace(os.Getenv("JIRA_USERNAME"))
	token := strings.TrimSpace(os.Getenv("JIRA_API_TOKEN"))
	if base == "" || username == "" || token == "" {
		return nil
	}
	c, err := newJiraClient(base, username, token, timeout)
	if err != nil {
		debugf("jira avatar: disabled: %v", err)
		return nil
	}
	return c
}

func newJiraClient(rawBase, username, token string, timeout time.Duration) (*jiraClient, error) {
	base, err := url.Parse(strings.TrimRight(rawBase, "/"))
	if err != nil || base.Scheme != "https" || base.Host == "" {
		return nil, fmt.Errorf("JIRA_URL must be an HTTPS site URL")
	}
	return &jiraClient{
		username: username,
		token:    token,
		base:     base,
		http: &http.Client{
			Timeout: timeout,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}, nil
}

func (c *jiraClient) avatarURL(accountID string) (string, error) {
	if c == nil || accountID == "" {
		return "", fmt.Errorf("Jira avatar lookup is not configured")
	}
	u := *c.base
	u.Path = strings.TrimRight(u.Path, "/") + "/rest/api/3/user"
	q := u.Query()
	q.Set("accountId", accountID)
	u.RawQuery = q.Encode()

	req, err := http.NewRequest(http.MethodGet, u.String(), nil)
	if err != nil {
		return "", err
	}
	req.SetBasicAuth(c.username, c.token)
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", &httpError{Status: resp.StatusCode, URL: u.String(), Body: strings.TrimSpace(string(body))}
	}

	var user struct {
		AvatarURLs map[string]string `json:"avatarUrls"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxJiraUserBytes)).Decode(&user); err != nil {
		return "", fmt.Errorf("decode Jira user %s: %w", accountID, err)
	}
	for _, size := range []string{"48x48", "32x32", "24x24", "16x16"} {
		if rawURL := user.AvatarURLs[size]; rawURL != "" {
			return rawURL, nil
		}
	}
	return "", fmt.Errorf("Jira user %s has no avatar URL", accountID)
}

// avatarURLs resolves up to jiraBulkMaxUsers accounts in one Jira request.
// Callers chunk larger visible sets so list rendering never becomes one
// request per author/reviewer.
func (c *jiraClient) avatarURLs(accountIDs []string) (map[string]string, error) {
	if c == nil || len(accountIDs) == 0 {
		return nil, fmt.Errorf("Jira avatar lookup is not configured")
	}
	if len(accountIDs) > jiraBulkMaxUsers {
		return nil, fmt.Errorf("Jira bulk avatar lookup accepts at most %d users", jiraBulkMaxUsers)
	}

	u := *c.base
	u.Path = strings.TrimRight(u.Path, "/") + "/rest/api/3/user/bulk"
	q := u.Query()
	seen := make(map[string]struct{}, len(accountIDs))
	for _, accountID := range accountIDs {
		if accountID == "" {
			continue
		}
		if _, ok := seen[accountID]; ok {
			continue
		}
		seen[accountID] = struct{}{}
		q.Add("accountId", accountID)
	}
	if len(seen) == 0 {
		return nil, fmt.Errorf("Jira bulk avatar lookup has no account IDs")
	}
	q.Set("maxResults", fmt.Sprintf("%d", len(seen)))
	u.RawQuery = q.Encode()

	req, err := http.NewRequest(http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(c.username, c.token)
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, &httpError{Status: resp.StatusCode, URL: u.String(), Body: strings.TrimSpace(string(body))}
	}

	var page struct {
		Values []struct {
			AccountID  string            `json:"accountId"`
			AvatarURLs map[string]string `json:"avatarUrls"`
		} `json:"values"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxJiraUserBytes*jiraBulkMaxUsers)).Decode(&page); err != nil {
		return nil, fmt.Errorf("decode Jira bulk users: %w", err)
	}
	result := make(map[string]string, len(page.Values))
	for _, user := range page.Values {
		if rawURL := preferredAvatarURL(user.AvatarURLs); rawURL != "" {
			result[user.AccountID] = rawURL
		}
	}
	return result, nil
}

func preferredAvatarURL(urls map[string]string) string {
	for _, size := range []string{"48x48", "32x32", "24x24", "16x16"} {
		if rawURL := urls[size]; rawURL != "" {
			return rawURL
		}
	}
	return ""
}
