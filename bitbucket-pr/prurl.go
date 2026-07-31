package main

import (
	"fmt"
	"regexp"
	"strconv"
)

// prURLRe matches Bitbucket Cloud pull-request web URLs, e.g.
// https://bitbucket.org/ws/repo/pull-requests/123/some-title/diff
var prURLRe = regexp.MustCompile(
	`^https://bitbucket\.org/([\w.-]+)/([\w.-]+)/pull-requests/(\d+)(?:[/?#]|$)`)

// parsePRURL extracts (workspace, repo_slug, pr id) from a PR web URL.
func parsePRURL(url string) (workspace, repo string, id int, err error) {
	m := prURLRe.FindStringSubmatch(url)
	if m == nil {
		return "", "", 0, fmt.Errorf("not a bitbucket.org pull-request URL: %q", url)
	}
	id, err = strconv.Atoi(m[3])
	if err != nil {
		return "", "", 0, err
	}
	return m[1], m[2], id, nil
}
