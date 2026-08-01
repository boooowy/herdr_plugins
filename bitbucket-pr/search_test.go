package main

import (
	"testing"
	"time"
)

func TestMatchQuery(t *testing.T) {
	cases := []struct {
		name   string
		q      string
		fields []string
		want   bool
	}{
		{"empty query matches", "", []string{"anything"}, true},
		{"case insensitive", "CACHE", []string{"feat: route cache"}, true},
		{"substring", "ute ca", []string{"feat: route cache"}, true},
		{"terms are ANDed across fields", "cache tanaka",
			[]string{"feat: route cache", "tanaka"}, true},
		{"one missing term fails", "cache suzuki",
			[]string{"feat: route cache", "tanaka"}, false},
		{"no match", "redis", []string{"feat: route cache", "tanaka"}, false},
		{"japanese", "経路", []string{"経路検索のキャッシュ層"}, true},
		{"empty fields ignored", "x", []string{"", "xyz"}, true},
	}
	for _, c := range cases {
		if got := matchQuery(c.q, c.fields...); got != c.want {
			t.Errorf("%s: matchQuery(%q, %v) = %v", c.name, c.q, c.fields, got)
		}
	}
}

func TestSearchBoxHandle(t *testing.T) {
	var s searchBox
	s.open()
	if !s.active || s.query() != "" {
		t.Fatalf("after open: active=%v query=%q", s.active, s.query())
	}
	for _, r := range []rune{'a', 'b'} {
		if !s.handle(Key{Kind: KeyRune, R: r}) {
			t.Errorf("rune %q did not change the query", r)
		}
	}
	if s.query() != "ab" {
		t.Fatalf("query = %q, want %q", s.query(), "ab")
	}
	if !s.handle(Key{Kind: KeyBackspace}) || s.query() != "a" {
		t.Fatalf("after backspace: %q", s.query())
	}
	// Enter confirms: input mode ends, the filter survives.
	if s.handle(Key{Kind: KeyEnter}); s.active || s.query() != "a" {
		t.Fatalf("after enter: active=%v query=%q", s.active, s.query())
	}
	// Esc drops the filter entirely.
	s.open()
	if !s.handle(Key{Kind: KeyEsc}) || s.active || s.query() != "" {
		t.Fatalf("after esc: active=%v query=%q", s.active, s.query())
	}
}

// The key reader hands over one byte at a time, so multi-byte input has to be
// reassembled — and backspace must delete a whole rune, not a byte.
func TestSearchBoxMultibyte(t *testing.T) {
	var s searchBox
	s.open()
	for _, b := range []byte("経路") {
		s.handle(Key{Kind: KeyRune, R: rune(b)})
	}
	if s.query() != "経路" {
		t.Fatalf("query = %q, want 経路", s.query())
	}
	s.handle(Key{Kind: KeyBackspace})
	if s.query() != "経" {
		t.Fatalf("after backspace: %q, want 経", s.query())
	}
	// Backspace on an empty query leaves input mode instead of underflowing.
	s.handle(Key{Kind: KeyBackspace})
	s.handle(Key{Kind: KeyBackspace})
	if s.active || s.query() != "" {
		t.Fatalf("underflow: active=%v query=%q", s.active, s.query())
	}
}

func mkPR(id int, title, author, src string) PullRequest {
	pr := PullRequest{ID: id, Title: title}
	pr.Author.DisplayName = author
	pr.Source.Branch.Name = src
	pr.Destination.Branch.Name = "develop"
	return pr
}

// Filtered rows must keep pointing at their original a.prs index: Enter, o, y
// and b all resolve the PR through Row.Item.
func TestListFilterKeepsOriginalIndex(t *testing.T) {
	a := &app{w: 100, prs: []PullRequest{
		mkPR(482, "feat: 経路検索のキャッシュ層を追加", "kayashima", "feature/route-cache"),
		mkPR(479, "fix: null チェック漏れ", "tanaka", "fix/null-check"),
		mkPR(475, "chore: bump deps", "suzuki", "chore/deps"),
	}}
	v := &listView{}
	v.rebuild(a)
	if v.shown != 3 {
		t.Fatalf("unfiltered shown = %d", v.shown)
	}

	for _, c := range []struct {
		q    string
		want int // expected a.prs index of the single hit
	}{
		{"null", 1},
		{"479", 1},
		{"#479", 1},
		{"tanaka", 1},
		{"fix/null", 1},
		{"経路", 0},
		{"deps", 2},
	} {
		v.search.buf = []byte(c.q)
		v.rebuild(a)
		if v.shown != 1 {
			t.Errorf("q=%q: shown = %d, want 1", c.q, v.shown)
			continue
		}
		found := -1
		for _, r := range v.vp.Rows {
			if r.Kind == RowPR {
				found = r.Item.(int)
			}
		}
		if found != c.want {
			t.Errorf("q=%q: row item = %d, want %d", c.q, found, c.want)
		}
	}

	v.search.buf = []byte("nothing-matches")
	v.rebuild(a)
	if v.shown != 0 {
		t.Errorf("no-hit shown = %d", v.shown)
	}
	if len(v.vp.Rows) != 1 || v.vp.Rows[0].Kind != RowText || v.vp.Rows[0].Selectable {
		t.Errorf("no-hit rows = %+v, want one unselectable placeholder", v.vp.Rows)
	}
}

// Same contract on the Files tab: Enter reads d.diffstat[Row.Item].
func TestFileFilterKeepsOriginalIndex(t *testing.T) {
	a := &app{w: 100, detail: map[int]*prDetail{}}
	d := &prDetail{diffstat: []DiffStatEntry{
		{Status: "modified", New: &struct {
			Path string `json:"path"`
		}{Path: "src/cache/route_cache.go"}},
		{Status: "added", New: &struct {
			Path string `json:"path"`
		}{Path: "src/api/handler.go"}},
		{Status: "removed", Old: &struct {
			Path string `json:"path"`
		}{Path: "legacy/old_cache.go"}},
	}}
	v := &detailView{tab: tabFiles}

	v.search[tabFiles].buf = []byte("handler")
	rows := v.fileRows(a, d)
	if v.shownFiles != 1 {
		t.Fatalf("shown = %d, want 1", v.shownFiles)
	}
	if rows[0].Kind != RowFile || rows[0].Item.(int) != 1 {
		t.Errorf("row = %+v, want item 1", rows[0])
	}

	// A removed file matches on its old path.
	v.search[tabFiles].buf = []byte("legacy")
	rows = v.fileRows(a, d)
	if v.shownFiles != 1 || rows[0].Item.(int) != 2 {
		t.Errorf("old-path match: shown=%d rows=%+v", v.shownFiles, rows)
	}

	v.search[tabFiles].buf = []byte("cache")
	v.fileRows(a, d)
	if v.shownFiles != 2 {
		t.Errorf("two hits: shown = %d", v.shownFiles)
	}
}

func TestFilterComments(t *testing.T) {
	base := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	// Thread A: inline on route_cache.go, root by tanaka + one reply.
	rootA := mkComment(1, 0, "src/cache/route_cache.go", nil, iptr(15), false, base)
	rootA.Content.Raw = "ttl は config から取れるようにしませんか?"
	rootA.User.DisplayName = "tanaka"
	replyA := mkComment(2, 1, "", nil, nil, false, base.Add(time.Hour))
	replyA.Content.Raw = "次の PR でやります"
	replyA.User.DisplayName = "kayashima"
	// Thread B: general comment, unrelated.
	rootB := mkComment(3, 0, "", nil, nil, false, base.Add(2*time.Hour))
	rootB.Content.Raw = "LGTM"
	rootB.User.DisplayName = "suzuki"

	all := []Comment{rootA, replyA, rootB}

	ids := func(cs []Comment) []int {
		out := make([]int, len(cs))
		for i, c := range cs {
			out[i] = c.ID
		}
		return out
	}
	eq := func(got []int, want ...int) bool {
		if len(got) != len(want) {
			return false
		}
		for i := range got {
			if got[i] != want[i] {
				return false
			}
		}
		return true
	}

	if got := ids(filterComments(all, "")); !eq(got, 1, 2, 3) {
		t.Errorf("empty query = %v, want all", got)
	}
	// A hit on the root keeps its reply, so the answer never loses context.
	if got := ids(filterComments(all, "ttl")); !eq(got, 1, 2) {
		t.Errorf("root hit = %v, want [1 2]", got)
	}
	// A hit on the reply keeps the root it answers.
	if got := ids(filterComments(all, "やります")); !eq(got, 1, 2) {
		t.Errorf("reply hit = %v, want [1 2]", got)
	}
	// Inline threads match on their file path...
	if got := ids(filterComments(all, "route_cache")); !eq(got, 1, 2) {
		t.Errorf("path hit = %v, want [1 2]", got)
	}
	// ...and on their line label.
	if got := ids(filterComments(all, "L15")); !eq(got, 1, 2) {
		t.Errorf("line-label hit = %v, want [1 2]", got)
	}
	// Author names match on the root or on a reply.
	if got := ids(filterComments(all, "suzuki")); !eq(got, 3) {
		t.Errorf("author hit = %v, want [3]", got)
	}
	if got := ids(filterComments(all, "kayashima")); !eq(got, 1, 2) {
		t.Errorf("reply-author hit = %v, want [1 2]", got)
	}
	if got := ids(filterComments(all, "redis")); len(got) != 0 {
		t.Errorf("no hit = %v, want none", got)
	}
}
