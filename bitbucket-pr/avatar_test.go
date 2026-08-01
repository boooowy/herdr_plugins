package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
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

func TestInitialsAvatarURLIncludesNestedGravatarDefault(t *testing.T) {
	rawURL := "https://secure.gravatar.com/avatar/hash?d=https%3A%2F%2Favatar-management.example%2Finitials%2FK-0.png"
	if !isInitialsAvatarURL(rawURL) {
		t.Fatal("nested Gravatar initials fallback must be eligible for Jira resolution")
	}
	if isDirectInitialsAvatarURL(rawURL) {
		t.Fatal("a Gravatar URL returned by Jira must remain a cacheable resolved image")
	}
	if isInitialsAvatarURL("https://cdn.example.test/custom/avatar.png") {
		t.Fatal("ordinary avatar URL must not be treated as initials")
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

func waitForAvatarSnapshots(t *testing.T, store *avatarStore, cells []AvatarCell) []avatarSnapshot {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		snapshots := store.images(cells)
		ready := len(snapshots) == len(cells)
		for _, snapshot := range snapshots {
			if snapshot.image == nil {
				ready = false
				break
			}
		}
		store.mu.Lock()
		for _, cell := range cells {
			if entry := store.entries[store.source(cell.AccountID, cell.URL).key]; entry == nil || entry.loading {
				ready = false
				break
			}
		}
		store.mu.Unlock()
		if ready {
			return snapshots
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for avatar images")
		}
		select {
		case <-store.ready:
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestAvatarStoreBulkResolvesAndRevalidatesPersistentCache(t *testing.T) {
	avatarData := encodeAvatar(t, color.NRGBA{G: 255, A: 255})
	var jiraCalls, imageCalls atomic.Int32
	var resolvedPrefix atomic.Value
	resolvedPrefix.Store("https://cdn.example.test/v1/")

	bb := newBBClient("bb@example.com", "bb-token", time.Second)
	bb.http.Transport = avatarRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		imageCalls.Add(1)
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(bytes.NewReader(avatarData)),
			Request:    req,
		}, nil
	})
	jira, err := newJiraClient("https://jira.example.test", "jira@example.com", "jira-token", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	jira.http.Transport = avatarRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		jiraCalls.Add(1)
		values := make([]map[string]any, 0)
		for _, accountID := range req.URL.Query()["accountId"] {
			values = append(values, map[string]any{
				"accountId":  accountID,
				"avatarUrls": map[string]string{"48x48": resolvedPrefix.Load().(string) + accountID + ".png"},
			})
		}
		body, marshalErr := json.Marshal(map[string]any{"values": values})
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(bytes.NewReader(body)),
			Request:    req,
		}, nil
	})

	dir := t.TempDir()
	var cells []AvatarCell
	for i := 0; i < jiraBulkMaxUsers+1; i++ {
		cells = append(cells, AvatarCell{
			AccountID: fmt.Sprintf("account-%d", i),
			URL:       fmt.Sprintf("https://bb.example.test/initials/A-%d.png", i),
		})
	}
	store := newAvatarStore(bb, jira, nil)
	store.dir = dir
	waitForAvatarSnapshots(t, store, cells)
	if jiraCalls.Load() != 2 || imageCalls.Load() != int32(len(cells)) {
		t.Fatalf("cold cache calls: Jira=%d images=%d", jiraCalls.Load(), imageCalls.Load())
	}

	// A new process uses the PNG and metadata without any network request.
	warm := newAvatarStore(bb, jira, nil)
	warm.dir = dir
	waitForAvatarSnapshots(t, warm, cells)
	if jiraCalls.Load() != 2 || imageCalls.Load() != int32(len(cells)) {
		t.Fatalf("warm cache calls: Jira=%d images=%d", jiraCalls.Load(), imageCalls.Load())
	}

	// After 30 days the account URLs are checked in one bulk request. When
	// unchanged, the indefinitely retained PNGs are not downloaded again.
	old := time.Now().Add(-avatarCacheTTL - time.Hour)
	for _, cell := range cells {
		source := warm.source(cell.AccountID, cell.URL)
		warm.saveMeta(source.key, avatarCacheMeta{
			ResolvedURL: resolvedPrefix.Load().(string) + cell.AccountID + ".png",
			CheckedAt:   old,
		})
	}
	revalidate := newAvatarStore(bb, jira, nil)
	revalidate.dir = dir
	waitForAvatarSnapshots(t, revalidate, cells)
	if jiraCalls.Load() != 4 || imageCalls.Load() != int32(len(cells)) {
		t.Fatalf("unchanged revalidation calls: Jira=%d images=%d", jiraCalls.Load(), imageCalls.Load())
	}

	// A changed Jira URL replaces only the affected cached image data.
	resolvedPrefix.Store("https://cdn.example.test/v2/")
	for _, cell := range cells {
		source := revalidate.source(cell.AccountID, cell.URL)
		revalidate.saveMeta(source.key, avatarCacheMeta{
			ResolvedURL: "https://cdn.example.test/v1/" + cell.AccountID + ".png",
			CheckedAt:   old,
		})
	}
	changed := newAvatarStore(bb, jira, nil)
	changed.dir = dir
	waitForAvatarSnapshots(t, changed, cells)
	if jiraCalls.Load() != 6 || imageCalls.Load() != int32(len(cells)*2) {
		t.Fatalf("changed revalidation calls: Jira=%d images=%d", jiraCalls.Load(), imageCalls.Load())
	}
}

func TestAvatarStoreMigratesLegacyJiraPNGWithoutNetwork(t *testing.T) {
	var jiraCalls atomic.Int32
	bb := newBBClient("bb@example.com", "bb-token", time.Second)
	jira, err := newJiraClient("https://jira.example.test", "jira@example.com", "jira-token", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	jira.http.Transport = avatarRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		jiraCalls.Add(1)
		return nil, fmt.Errorf("legacy cache should suppress Jira lookup")
	})

	dir := t.TempDir()
	cell := AvatarCell{AccountID: "legacy", URL: "https://bb.example.test/initials/L-1.png"}
	writer := newAvatarStore(bb, jira, nil)
	writer.dir = dir
	source := writer.source(cell.AccountID, cell.URL)
	if !writer.saveDisk(writer.cachePath(source.key), solidAvatar(color.NRGBA{R: 255, A: 255})) {
		t.Fatal("failed to create legacy cache PNG")
	}

	store := newAvatarStore(bb, jira, nil)
	store.dir = dir
	waitForAvatarSnapshots(t, store, []AvatarCell{cell})
	if jiraCalls.Load() != 0 {
		t.Fatalf("Jira calls = %d", jiraCalls.Load())
	}
	meta, ok := store.loadMeta(source.key)
	if !ok || meta.CheckedAt.IsZero() || meta.ResolvedURL != "" {
		t.Fatalf("legacy metadata = %+v, ok=%v", meta, ok)
	}
}

func TestAvatarStoreLimitsConcurrentDownloads(t *testing.T) {
	avatarData := encodeAvatar(t, color.NRGBA{B: 255, A: 255})
	var active, maximum atomic.Int32
	bb := newBBClient("bb@example.com", "bb-token", time.Second)
	bb.http.Transport = avatarRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		current := active.Add(1)
		for {
			previous := maximum.Load()
			if current <= previous || maximum.CompareAndSwap(previous, current) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		active.Add(-1)
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(bytes.NewReader(avatarData)),
			Request:    req,
		}, nil
	})
	store := newAvatarStore(bb, nil, nil)
	store.dir = t.TempDir()
	var cells []AvatarCell
	for i := 0; i < avatarDownloadConcurrent*2; i++ {
		cells = append(cells, AvatarCell{URL: fmt.Sprintf("https://cdn.example.test/%d.png", i)})
	}
	waitForAvatarSnapshots(t, store, cells)
	if got := maximum.Load(); got < 2 || got > avatarDownloadConcurrent {
		t.Fatalf("maximum concurrent downloads = %d", got)
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

func TestComposeAvatarSceneDrawsApprovedBadgeOnlyWhenRequested(t *testing.T) {
	metrics := paneGraphicsMetrics{CellWidthPX: 9, CellHeightPX: 18}
	compose := func(badge AvatarBadge) image.Image {
		t.Helper()
		data, _, _, _, err := composeAvatarScene([]renderedAvatar{{
			cell:  AvatarCell{Cols: 2, Rows: 1, Badge: badge},
			image: solidAvatar(color.NRGBA{R: 180, G: 20, B: 20, A: 255}),
		}}, metrics)
		if err != nil {
			t.Fatal(err)
		}
		img, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		return img
	}
	hasBadgePixels := func(img image.Image) (green, white bool) {
		for y := img.Bounds().Dy() / 2; y < img.Bounds().Dy(); y++ {
			for x := img.Bounds().Dx() / 2; x < img.Bounds().Dx(); x++ {
				r, g, b, _ := img.At(x, y).RGBA()
				green = green || (g > 40000 && r < 20000 && b < 30000)
				white = white || (r > 55000 && g > 55000 && b > 55000)
			}
		}
		return green, white
	}
	green, white := hasBadgePixels(compose(AvatarBadgeApproved))
	if !green || !white {
		t.Fatalf("approved badge pixels: green=%v white=%v", green, white)
	}
	green, white = hasBadgePixels(compose(AvatarBadgeNone))
	if green || white {
		t.Fatalf("plain avatar unexpectedly has badge pixels: green=%v white=%v", green, white)
	}
}

func TestAvatarSceneKeyIncludesBadge(t *testing.T) {
	cell := AvatarCell{URL: "avatar", AccountID: "account", X: 1, Y: 2, Cols: 2, Rows: 1}
	plain := avatarSceneItemKey(cell, 7)
	cell.Badge = AvatarBadgeApproved
	approved := avatarSceneItemKey(cell, 7)
	if plain == approved {
		t.Fatalf("scene keys are equal: %q", plain)
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

func TestViewportOnlyEmitsAvatarsForVisibleRows(t *testing.T) {
	rows := []Row{
		{Avatars: []RowAvatar{{URL: "visible", Cols: 2, Rows: 1}}},
		{Avatars: []RowAvatar{{URL: "offscreen", Cols: 2, Rows: 1}}},
	}
	v := Viewport{Rows: rows, Cursor: -1}
	s := NewScreen(20, 2)
	v.Paint(s, Rect{X: 0, Y: 0, W: 20, H: 1})
	if len(s.Avatars()) != 1 || s.Avatars()[0].URL != "visible" {
		t.Fatalf("visible avatars = %+v", s.Avatars())
	}
}

func TestListShowsAuthorAndReviewerAvatarsForAllVisiblePRs(t *testing.T) {
	a := &app{w: 100, h: 20, avatars: &avatarGraphics{}}
	a.prs = []PullRequest{{ID: 1, Title: "one"}, {ID: 2, Title: "two"}}
	for i := range a.prs {
		a.prs[i].Author.DisplayName = "author"
		a.prs[i].Author.AccountID = fmt.Sprintf("author-%d", i)
		setAvatarURL(&a.prs[i].Author, fmt.Sprintf("https://example.test/author-%d.png", i))
		reviewer := Participant{Role: "REVIEWER"}
		reviewer.User.DisplayName = "reviewer"
		reviewer.User.AccountID = fmt.Sprintf("reviewer-%d", i)
		setAvatarURL(&reviewer.User, fmt.Sprintf("https://example.test/reviewer-%d.png", i))
		a.prs[i].Participants = []Participant{reviewer}
	}
	v := &listView{}
	v.rebuild(a)
	if len(v.vp.Rows[0].Avatars) != 1 || v.vp.Rows[0].Avatars[0].SelectedOnly {
		t.Fatalf("author avatar = %+v", v.vp.Rows[0].Avatars)
	}
	if len(v.vp.Rows[1].Avatars) != 1 || v.vp.Rows[1].Avatars[0].AccountID != "reviewer-0" {
		t.Fatalf("reviewer avatar = %+v", v.vp.Rows[1].Avatars)
	}
	s := NewScreen(a.w, a.h)
	v.vp.Paint(s, Rect{X: 0, Y: 2, W: a.w, H: a.h - 3})
	if len(s.Avatars()) != 4 {
		t.Errorf("visible avatars = %+v", s.Avatars())
	}
}

func TestListAccountColumnsStayAlignedAcrossContentLengths(t *testing.T) {
	a := &app{w: 140, h: 20, avatars: &avatarGraphics{}}
	a.prs = []PullRequest{
		{ID: 1, Title: "short", CommentCount: 0},
		{ID: 2, Title: strings.Repeat("long title ", 12), CommentCount: 5},
		{ID: 3, Title: "日本語タイトル", CommentCount: 1234},
	}
	for i := range a.prs {
		pr := &a.prs[i]
		pr.Author.DisplayName = strings.Repeat("author", i+1)
		pr.Author.AccountID = fmt.Sprintf("author-%d", i)
		setAvatarURL(&pr.Author, fmt.Sprintf("https://example.test/author-%d.png", i))
		pr.Source.Branch.Name = strings.Repeat("feature/", i+1)
		pr.Destination.Branch.Name = "main"
		reviewer := Participant{Role: "REVIEWER"}
		reviewer.User.DisplayName = "reviewer"
		reviewer.User.AccountID = fmt.Sprintf("reviewer-%d", i)
		setAvatarURL(&reviewer.User, fmt.Sprintf("https://example.test/reviewer-%d.png", i))
		pr.Participants = []Participant{reviewer}
	}

	v := &listView{}
	v.rebuild(a)
	wantCol := listIDWidth + listTitleMaxWidth + listCommentWidth + listRoleWidth
	for i := range a.prs {
		author := v.vp.Rows[i*2]
		reviewers := v.vp.Rows[i*2+1]
		if len(author.Avatars) != 1 || author.Avatars[0].Col != wantCol {
			t.Errorf("PR %d author avatars = %+v, want col %d", i, author.Avatars, wantCol)
		}
		if len(reviewers.Avatars) != 1 || reviewers.Avatars[0].Col != wantCol {
			t.Errorf("PR %d reviewer avatars = %+v, want col %d", i, reviewers.Avatars, wantCol)
		}
	}
}

func TestListNarrowLayoutShrinksReviewerStripAfterTitle(t *testing.T) {
	a := &app{w: 24, h: 10, avatars: &avatarGraphics{}}
	pr := PullRequest{ID: 1, Title: strings.Repeat("title", 20)}
	pr.Author.DisplayName = "author"
	setAvatarURL(&pr.Author, "https://example.test/author.png")
	for i := 0; i < 6; i++ {
		participant := Participant{Role: "REVIEWER"}
		participant.User.DisplayName = fmt.Sprintf("reviewer-%d", i)
		participant.User.AccountID = fmt.Sprintf("reviewer-%d", i)
		setAvatarURL(&participant.User, fmt.Sprintf("https://example.test/reviewer-%d.png", i))
		pr.Participants = append(pr.Participants, participant)
	}
	a.prs = []PullRequest{pr}
	v := &listView{}
	v.rebuild(a)
	if got := listTitleWidth(a); got != 1 {
		t.Fatalf("title width = %d, want 1", got)
	}
	meta := v.vp.Rows[1]
	if len(meta.Avatars) != 1 {
		t.Fatalf("narrow reviewer avatars = %+v", meta.Avatars)
	}
	if text := spansText(meta); !strings.Contains(text, "+5") {
		t.Fatalf("narrow reviewer overflow = %q", text)
	}
}

func TestListReviewerAvatarsAreDeduplicatedAndLimited(t *testing.T) {
	a := &app{w: 100, h: 20, avatars: &avatarGraphics{}}
	pr := PullRequest{ID: 1, Title: "reviewers"}
	pr.Author.DisplayName = "author"
	pr.Author.AccountID = "author"
	setAvatarURL(&pr.Author, "https://example.test/author.png")
	for i := 0; i < 6; i++ {
		participant := Participant{Role: "REVIEWER", Approved: i == 0, State: ""}
		if i == 1 {
			participant.State = "changes_requested"
		}
		participant.User.DisplayName = fmt.Sprintf("reviewer-%d", i)
		participant.User.AccountID = fmt.Sprintf("reviewer-%d", i)
		setAvatarURL(&participant.User, fmt.Sprintf("https://example.test/reviewer-%d.png", i))
		pr.Participants = append(pr.Participants, participant)
	}
	pr.Participants = append(pr.Participants, pr.Participants[0], Participant{Role: "PARTICIPANT", User: pr.Participants[1].User})
	a.prs = []PullRequest{pr}

	v := &listView{}
	v.rebuild(a)
	meta := v.vp.Rows[1]
	if len(meta.Avatars) != listReviewerLimit {
		t.Fatalf("reviewer avatars = %+v", meta.Avatars)
	}
	if meta.Avatars[0].Badge != AvatarBadgeApproved {
		t.Errorf("approved reviewer badge = %v", meta.Avatars[0].Badge)
	}
	for i, avatar := range meta.Avatars[1:] {
		if avatar.Badge != AvatarBadgeNone {
			t.Errorf("reviewer %d badge = %v, want none", i+1, avatar.Badge)
		}
	}
	var text strings.Builder
	for _, span := range meta.Spans {
		text.WriteString(span.Text)
	}
	if !strings.Contains(text.String(), "+2") {
		t.Errorf("reviewer overflow missing: %q", text.String())
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
