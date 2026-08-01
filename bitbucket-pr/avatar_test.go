package main

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func setAvatarURL(a *Account, rawURL string) { a.Links.Avatar.Href = rawURL }

func TestAccountAvatarURLFromBitbucketJSON(t *testing.T) {
	var account Account
	if err := json.Unmarshal([]byte(`{"display_name":"Tanaka","account_id":"abc123","links":{"avatar":{"href":"https://example.test/a.png"}}}`), &account); err != nil {
		t.Fatal(err)
	}
	if account.Name() != "Tanaka" || account.AccountID != "abc123" || account.AvatarURL() != "https://example.test/a.png" {
		t.Errorf("account = %+v", account)
	}
}

func solidAvatar(c color.Color) image.Image {
	img := image.NewNRGBA(image.Rect(0, 0, avatarCachePixels, avatarCachePixels))
	for y := 0; y < avatarCachePixels; y++ {
		for x := 0; x < avatarCachePixels; x++ {
			img.Set(x, y, c)
		}
	}
	return img
}

func TestNormalizeAvatarCenterCropsAndResizes(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 6, 2))
	for y := 0; y < 2; y++ {
		for x := 0; x < 6; x++ {
			c := color.NRGBA{R: 255, A: 255}
			if x >= 2 && x < 4 {
				c = color.NRGBA{G: 255, A: 255}
			}
			src.SetNRGBA(x, y, c)
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, src); err != nil {
		t.Fatal(err)
	}
	got, err := normalizeAvatar(encoded.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if got.Bounds() != image.Rect(0, 0, avatarCachePixels, avatarCachePixels) {
		t.Fatalf("bounds = %v", got.Bounds())
	}
	r, g, _, _ := got.At(avatarCachePixels/2, avatarCachePixels/2).RGBA()
	if g <= r {
		t.Errorf("center crop did not retain the green middle: r=%d g=%d", r, g)
	}
}

func TestNormalizeAvatarRejectsOversizedDimensions(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, avatarMaxPixels+1, 1))
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, src); err != nil {
		t.Fatal(err)
	}
	if _, err := normalizeAvatar(encoded.Bytes()); err == nil {
		t.Error("oversized avatar must fail")
	}
}

func TestAvatarDiskCacheRoundTrip(t *testing.T) {
	dir := t.TempDir()
	store := &avatarStore{dir: dir}
	path := filepath.Join(dir, "avatar.png")
	store.saveDisk(path, solidAvatar(color.NRGBA{R: 10, G: 20, B: 30, A: 255}))
	img, fresh := store.loadDisk(path)
	if img == nil || !fresh || img.Bounds() != image.Rect(0, 0, avatarCachePixels, avatarCachePixels) {
		t.Errorf("img=%v fresh=%v", img, fresh)
	}
}

func encodeAvatar(t *testing.T, c color.Color) []byte {
	t.Helper()
	var out bytes.Buffer
	if err := png.Encode(&out, solidAvatar(c)); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

type avatarRoundTripFunc func(*http.Request) (*http.Response, error)

func (f avatarRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestAvatarStoreUsesLocalOverrideAndTracksFileChanges(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fukaya.png")
	if err := os.WriteFile(path, encodeAvatar(t, color.NRGBA{R: 255, A: 255}), 0o600); err != nil {
		t.Fatal(err)
	}
	store := &avatarStore{overrides: map[string]string{"account-1": path}}
	first := store.source("account-1", "https://example.test/initials.png")
	data, cacheable, err := store.loadSource(first)
	if err != nil {
		t.Fatal(err)
	}
	if !cacheable {
		t.Error("valid local override must be cacheable")
	}
	img, err := normalizeAvatar(data)
	if err != nil {
		t.Fatal(err)
	}
	r, _, _, _ := img.At(0, 0).RGBA()
	if r == 0 {
		t.Error("local override was not loaded")
	}

	changed := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, changed, changed); err != nil {
		t.Fatal(err)
	}
	second := store.source("account-1", "https://example.test/initials.png")
	if first.key == second.key {
		t.Error("file timestamp change must invalidate the avatar cache key")
	}
}

func TestAvatarStoreFallsBackWhenOverrideIsInvalid(t *testing.T) {
	fallback := encodeAvatar(t, color.NRGBA{B: 255, A: 255})
	path := filepath.Join(t.TempDir(), "broken.png")
	if err := os.WriteFile(path, []byte("not an image"), 0o600); err != nil {
		t.Fatal(err)
	}
	client := newBBClient("user@example.com", "token", time.Second)
	client.base = "https://api.example.test"
	client.http.Transport = avatarRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"image/png"}},
			Body:       io.NopCloser(bytes.NewReader(fallback)),
			Request:    req,
		}, nil
	})
	store := &avatarStore{client: client, overrides: map[string]string{"account-1": path}}
	data, cacheable, err := store.loadSource(store.source("account-1", client.base+"/avatar.png"))
	if err != nil {
		t.Fatal(err)
	}
	if !cacheable {
		t.Error("ordinary Bitbucket fallback must remain cacheable when Jira is disabled")
	}
	img, err := normalizeAvatar(data)
	if err != nil {
		t.Fatal(err)
	}
	_, _, b, _ := img.At(0, 0).RGBA()
	if b == 0 {
		t.Error("Bitbucket fallback image was not loaded")
	}
}

func TestAvatarStoreResolvesInitialsThroughJira(t *testing.T) {
	actual := encodeAvatar(t, color.NRGBA{G: 255, A: 255})
	requestedAvatar := ""
	bb := newBBClient("bb@example.com", "bb-token", time.Second)
	bb.http.Transport = avatarRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		requestedAvatar = req.URL.String()
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(bytes.NewReader(actual)),
			Request:    req,
		}, nil
	})
	jira, err := newJiraClient("https://jira.example.test", "jira@example.com", "jira-token", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	jira.http.Transport = avatarRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		body := `{"avatarUrls":{"48x48":"https://cdn.example.test/custom.png"}}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    req,
		}, nil
	})
	store := &avatarStore{client: bb, jira: jira}
	source := store.source("account-1", "https://cdn.example.test/initials/S-2.png")
	data, cacheable, err := store.loadSource(source)
	if err != nil {
		t.Fatal(err)
	}
	if !cacheable || requestedAvatar != "https://cdn.example.test/custom.png" {
		t.Errorf("cacheable=%v requested=%q", cacheable, requestedAvatar)
	}
	if _, err := normalizeAvatar(data); err != nil {
		t.Fatal(err)
	}
}

func TestAvatarStoreKeepsBitbucketInitialsWhenJiraFails(t *testing.T) {
	initials := encodeAvatar(t, color.NRGBA{R: 255, A: 255})
	requestedAvatar := ""
	bb := newBBClient("bb@example.com", "bb-token", time.Second)
	bb.http.Transport = avatarRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		requestedAvatar = req.URL.String()
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(bytes.NewReader(initials)),
			Request:    req,
		}, nil
	})
	jira, err := newJiraClient("https://jira.example.test", "jira@example.com", "bad-token", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	jira.http.Transport = avatarRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusUnauthorized,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"errorMessages":["unauthorized"]}`)),
			Request:    req,
		}, nil
	})
	store := &avatarStore{client: bb, jira: jira}
	fallbackURL := "https://cdn.example.test/initials/S-2.png"
	data, cacheable, err := store.loadSource(store.source("account-1", fallbackURL))
	if err != nil {
		t.Fatal(err)
	}
	if cacheable {
		t.Error("a Jira failure fallback must not be persisted under the Jira cache key")
	}
	if requestedAvatar != fallbackURL {
		t.Errorf("requested avatar = %q", requestedAvatar)
	}
	if _, err := normalizeAvatar(data); err != nil {
		t.Fatal(err)
	}
}

func TestComposeAvatarSceneUsesCellMetricsAndTransparency(t *testing.T) {
	avatars := []renderedAvatar{
		{cell: AvatarCell{X: 2, Y: 1, Cols: 2, Rows: 1}, image: solidAvatar(color.NRGBA{R: 255, A: 255})},
		{cell: AvatarCell{X: 6, Y: 3, Cols: 2, Rows: 1}, image: solidAvatar(color.NRGBA{B: 255, A: 255})},
	}
	data, w, h, placement, err := composeAvatarScene(avatars, paneGraphicsMetrics{CellWidthPX: 9, CellHeightPX: 18})
	if err != nil {
		t.Fatal(err)
	}
	if placement.X != 2 || placement.Y != 1 || placement.Cols != 6 || placement.Rows != 3 {
		t.Errorf("placement = %+v", placement)
	}
	if w != 54 || h != 54 {
		t.Errorf("image = %dx%d, want 54x54", w, h)
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, alpha := img.At(27, 18).RGBA() // transparent gap between avatars
	if alpha != 0 {
		t.Errorf("gap alpha = %d, want 0", alpha)
	}
}

func TestAvatarMetadataInThreadRows(t *testing.T) {
	base := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	thread := CommentThread{Root: Comment{ID: 1, CreatedOn: base}, Replies: []Reply{{
		Comment: Comment{ID: 2, CreatedOn: base}, Depth: 1,
	}}}
	thread.Root.User.DisplayName = "root"
	thread.Root.User.AccountID = "root-account"
	thread.Replies[0].User.DisplayName = "reply"
	setAvatarURL(&thread.Root.User, "https://example.test/root.png")
	setAvatarURL(&thread.Replies[0].User, "https://example.test/reply.png")

	rows := threadRows(thread, 60, base, "", true)
	if len(rows[0].Avatars) != 1 || rows[0].Avatars[0].Cols != 2 {
		t.Fatalf("root avatars = %+v", rows[0].Avatars)
	}
	if rows[0].Avatars[0].AccountID != "root-account" {
		t.Errorf("root account ID = %q", rows[0].Avatars[0].AccountID)
	}
	foundReply := false
	for _, r := range rows {
		if len(r.Avatars) > 0 && r.Avatars[0].URL == "https://example.test/reply.png" {
			foundReply = true
		}
	}
	if !foundReply {
		t.Error("reply avatar metadata not found")
	}
}

func TestViewportSelectedOnlyAvatar(t *testing.T) {
	rows := []Row{
		{Selectable: true, Avatars: []RowAvatar{{URL: "a", Col: 1, Cols: 2, Rows: 1, SelectedOnly: true}}},
		{Selectable: true, Avatars: []RowAvatar{{URL: "b", Col: 1, Cols: 2, Rows: 1, SelectedOnly: true}}},
	}
	v := Viewport{Rows: rows, Cursor: 1}
	s := NewScreen(20, 4)
	v.Paint(s, Rect{X: 0, Y: 1, W: 20, H: 2})
	if len(s.Avatars()) != 1 || s.Avatars()[0].URL != "b" || s.Avatars()[0].Y != 2 {
		t.Errorf("avatars = %+v", s.Avatars())
	}
}

func TestListShowsAvatarOnlyForSelectedPR(t *testing.T) {
	a := &app{w: 100, h: 20, avatars: &avatarGraphics{}}
	a.prs = []PullRequest{{ID: 1, Title: "one"}, {ID: 2, Title: "two"}}
	for i := range a.prs {
		a.prs[i].Author.DisplayName = "author"
		setAvatarURL(&a.prs[i].Author, "https://example.test/avatar.png")
	}
	v := &listView{}
	v.rebuild(a)
	if len(v.vp.Rows[0].Avatars) != 1 || !v.vp.Rows[0].Avatars[0].SelectedOnly {
		t.Fatalf("row avatar = %+v", v.vp.Rows[0].Avatars)
	}
	s := NewScreen(a.w, a.h)
	v.vp.Paint(s, Rect{X: 0, Y: 2, W: a.w, H: a.h - 3})
	if len(s.Avatars()) != 1 {
		t.Errorf("visible avatars = %+v", s.Avatars())
	}
}

func TestMasterCommentRowsPlaceRootAndReplyAvatars(t *testing.T) {
	base := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	root := Comment{ID: 1, CreatedOn: base}
	root.User.DisplayName = "root"
	setAvatarURL(&root.User, "https://example.test/root.png")
	reply := Reply{Comment: Comment{ID: 2, CreatedOn: base}, Depth: 1}
	reply.User.DisplayName = "reply"
	setAvatarURL(&reply.User, "https://example.test/reply.png")
	thread := CommentThread{Root: root, Replies: []Reply{reply}}

	rootRow := masterThreadRow(thread, 50, true)
	replyRow := masterReplyRow(reply, 50, true)
	if len(rootRow.Avatars) != 1 || len(replyRow.Avatars) != 1 {
		t.Errorf("root=%+v reply=%+v", rootRow.Avatars, replyRow.Avatars)
	}
}

func TestDetailHeaderAvatarPlacement(t *testing.T) {
	a := &app{w: 100, h: 30, avatars: &avatarGraphics{}, detail: map[int]*prDetail{}}
	pr := &PullRequest{ID: 7, Title: "avatar test", State: "OPEN"}
	pr.Author.DisplayName = "author"
	setAvatarURL(&pr.Author, "https://example.test/author.png")
	a.detail[7] = &prDetail{pr: pr}
	v := &detailView{prID: 7}
	s := NewScreen(a.w, a.h)
	v.render(a, s)
	if len(s.Avatars()) != 1 {
		t.Fatalf("avatars = %+v", s.Avatars())
	}
	got := s.Avatars()[0]
	if got.X != 1 || got.Y != 0 || got.Cols != 4 || got.Rows != 2 {
		t.Errorf("header avatar = %+v", got)
	}
}
