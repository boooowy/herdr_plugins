package main

import "time"

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
type PRRef struct {
	Branch struct {
		Name string `json:"name"`
	} `json:"branch"`
	Commit struct {
		Hash string `json:"hash"`
	} `json:"commit"`
}

// Participant is a reviewer/participant entry on the PR detail payload.
type Participant struct {
	User     Account `json:"user"`
	Role     string  `json:"role"`  // PARTICIPANT | REVIEWER
	State    string  `json:"state"` // approved | changes_requested | ""
	Approved bool    `json:"approved"`
}

// PullRequest is a PR from the list or detail endpoint. The list payload
// omits some fields (participants, reviewers); the detail fetch fills them.
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

// page is one page of a Bitbucket paginated collection. Follow Next verbatim
// (never construct page numbers); size/previous may be absent, so they are
// not modeled at all.
type page[T any] struct {
	Values []T    `json:"values"`
	Next   string `json:"next"`
}
