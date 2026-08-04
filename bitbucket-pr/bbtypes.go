package main

import (
	"strings"
	"time"
)

// Bitbucket Cloud API 2.0 payload slices — only the fields the viewer reads.

// Account is a Bitbucket user reference.
type Account struct {
	DisplayName string `json:"display_name"`
	Nickname    string `json:"nickname"`
	UUID        string `json:"uuid"`
	AccountID   string `json:"account_id"`
	Links       struct {
		Avatar struct {
			Href string `json:"href"`
		} `json:"avatar"`
	} `json:"links"`
}

// Name returns the best human label for the account.
func (a Account) Name() string {
	if a.DisplayName != "" {
		return a.DisplayName
	}
	if a.Nickname != "" {
		return a.Nickname
	}
	return "Unknown"
}

// AvatarURL returns the account's Bitbucket-provided thumbnail URL.
func (a Account) AvatarURL() string { return a.Links.Avatar.Href }

// PRRef is one endpoint of a pull request (source or destination).
// Repository is only requested by the cross-repo views (My PRs / Review),
// where the owning repo is not implied by the endpoint.
type PRRef struct {
	Branch struct {
		Name string `json:"name"`
	} `json:"branch"`
	Commit struct {
		Hash string `json:"hash"`
	} `json:"commit"`
	Repository struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
}

// Participant is a reviewer/participant entry on the PR detail payload.
type Participant struct {
	User     Account `json:"user"`
	Role     string  `json:"role"`  // PARTICIPANT | REVIEWER
	State    string  `json:"state"` // approved | changes_requested | ""
	Approved bool    `json:"approved"`
}

type reviewerStatus uint8

const (
	reviewerPending reviewerStatus = iota
	reviewerChangesRequested
	reviewerApproved
)

type reviewerInfo struct {
	User   Account
	Status reviewerStatus
}

// pullRequestReviewers normalizes reviewer state for every view. Bitbucket
// occasionally returns the same account more than once in participants; keep
// its first position while retaining the strongest state we have observed.
func pullRequestReviewers(pr *PullRequest) []reviewerInfo {
	seen := make(map[string]int)
	authorKey := accountKey(pr.Author)
	var reviewers []reviewerInfo
	for _, participant := range pr.Participants {
		role := strings.ToUpper(participant.Role)
		state := strings.ToLower(participant.State)
		if role != "REVIEWER" && !participant.Approved && state == "" {
			continue
		}
		key := accountKey(participant.User)
		if key == "" || key == authorKey {
			continue
		}
		status := reviewerPending
		switch {
		case participant.Approved || state == "approved":
			status = reviewerApproved
		case state == "changes_requested":
			status = reviewerChangesRequested
		case role != "REVIEWER":
			continue
		}
		if i, ok := seen[key]; ok {
			if status > reviewers[i].Status {
				reviewers[i].Status = status
			}
			continue
		}
		seen[key] = len(reviewers)
		reviewers = append(reviewers, reviewerInfo{User: participant.User, Status: status})
	}
	return reviewers
}

// PullRequest is a PR from the list or detail endpoint. The trimmed list
// payload includes compact reviewer participants; the detail fetch fills the
// description and the remaining fields.
type PullRequest struct {
	ID           int           `json:"id"`
	Title        string        `json:"title"`
	State        string        `json:"state"` // OPEN | MERGED | DECLINED | SUPERSEDED
	Author       Account       `json:"author"`
	Source       PRRef         `json:"source"`
	Destination  PRRef         `json:"destination"`
	CommentCount int           `json:"comment_count"`
	TaskCount    int           `json:"task_count"`
	CreatedOn    time.Time     `json:"created_on"`
	UpdatedOn    time.Time     `json:"updated_on"`
	Participants []Participant `json:"participants"`
	Summary      struct {
		Raw string `json:"raw"`
	} `json:"summary"`
	Rendered struct {
		Description struct {
			Raw string `json:"raw"`
		} `json:"description"`
	} `json:"rendered"`
	Links struct {
		HTML struct {
			Href string `json:"href"`
		} `json:"html"`
	} `json:"links"`
}

// Description returns the raw markdown description of the PR.
func (pr *PullRequest) Description() string {
	return pr.Summary.Raw
}

// WebURL returns the PR's browser URL.
func (pr *PullRequest) WebURL() string {
	return pr.Links.HTML.Href
}

// RepoSlug returns the owning repository's slug (cross-repo payloads only;
// "" when the repository field was not requested).
func (pr *PullRequest) RepoSlug() string {
	fn := pr.Destination.Repository.FullName
	if i := strings.IndexByte(fn, '/'); i >= 0 {
		return fn[i+1:]
	}
	return fn
}

// DiffStatEntry is one file's change summary from the diffstat endpoint.
type DiffStatEntry struct {
	Status       string `json:"status"` // added | removed | modified | renamed
	LinesAdded   int    `json:"lines_added"`
	LinesRemoved int    `json:"lines_removed"`
	Old          *struct {
		Path string `json:"path"`
	} `json:"old"`
	New *struct {
		Path string `json:"path"`
	} `json:"new"`
}

// Path returns the file's current path (new side, falling back to old for
// deleted files).
func (d DiffStatEntry) Path() string {
	if d.New != nil && d.New.Path != "" {
		return d.New.Path
	}
	if d.Old != nil {
		return d.Old.Path
	}
	return ""
}

// OldPath returns the pre-change path ("" when the file is new).
func (d DiffStatEntry) OldPath() string {
	if d.Old != nil {
		return d.Old.Path
	}
	return ""
}

// InlineAnchor positions an inline comment inside the PR diff. To is the
// line on the new side, From on the old side; a comment on a deleted line
// has only From. Multi-line selections carry the range start in
// StartTo/StartFrom (e.g. start_to:486 + to:496 = lines 486–496).
type InlineAnchor struct {
	Path      string `json:"path"`
	From      *int   `json:"from"`
	To        *int   `json:"to"`
	StartFrom *int   `json:"start_from"`
	StartTo   *int   `json:"start_to"`
	Outdated  bool   `json:"outdated"`
}

// Comment is one PR comment: general (Inline == nil) or inline.
type Comment struct {
	ID        int       `json:"id"`
	User      Account   `json:"user"`
	CreatedOn time.Time `json:"created_on"`
	UpdatedOn time.Time `json:"updated_on"`
	Deleted   bool      `json:"deleted"`
	Pending   bool      `json:"pending"`
	Content   struct {
		Raw string `json:"raw"`
	} `json:"content"`
	Parent *struct {
		ID int `json:"id"`
	} `json:"parent"`
	Inline *InlineAnchor `json:"inline"`
	// Resolution is present when the comment's thread is resolved
	// (thread roots only; absent or null otherwise).
	Resolution *CommentResolution `json:"resolution"`
}

// CommentResolution records who resolved a comment thread and when.
type CommentResolution struct {
	User      Account   `json:"user"`
	CreatedOn time.Time `json:"created_on"`
}

// Resolved reports whether the comment's thread is marked resolved.
func (c *Comment) Resolved() bool { return c.Resolution != nil }

// Repository is one entry from the workspace repository list, trimmed to
// what the repo picker renders plus the clone URLs.
type Repository struct {
	Slug        string    `json:"slug"`
	Name        string    `json:"name"`
	FullName    string    `json:"full_name"` // "workspace/slug"
	Description string    `json:"description"`
	UpdatedOn   time.Time `json:"updated_on"`
	Links       struct {
		Clone []struct {
			Name string `json:"name"` // "https" | "ssh"
			Href string `json:"href"`
		} `json:"clone"`
		HTML struct {
			Href string `json:"href"`
		} `json:"html"`
	} `json:"links"`
}

// CloneURL returns the clone URL for the given protocol ("ssh" | "https"),
// or "" when the API did not provide one. Always taken from the payload —
// never assembled locally, so credentials can never leak into it.
func (r *Repository) CloneURL(protocol string) string {
	for _, l := range r.Links.Clone {
		if l.Name == protocol {
			return l.Href
		}
	}
	return ""
}

// WebURL returns the repository's browser URL.
func (r *Repository) WebURL() string { return r.Links.HTML.Href }

// page is one page of a Bitbucket paginated collection. Follow Next verbatim
// (never construct page numbers); size/previous may be absent, so they are
// not modeled at all.
type page[T any] struct {
	Values []T    `json:"values"`
	Next   string `json:"next"`
}
