package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

const (
	compactAvatarCols        = 2
	avatarCachePixels        = 64
	avatarMaxPixels          = 2048
	avatarCacheTTL           = 30 * 24 * time.Hour
	avatarRetryDelay         = 5 * time.Minute
	avatarDownloadConcurrent = 4
)

// appendAccountName reserves a square 2x1-cell slot immediately before an
// account name and returns the row-relative image placement for that slot.
func appendAccountName(spans []Span, account Account, name string, nameStyle, gapStyle StyleID, enabled bool) ([]Span, []RowAvatar) {
	var avatars []RowAvatar
	if enabled && account.AvatarURL() != "" {
		col := spansWidth(spans)
		spans = append(spans, Span{Text: strings.Repeat(" ", compactAvatarCols), Style: gapStyle})
		avatars = append(avatars, RowAvatar{
			URL: account.AvatarURL(), AccountID: account.AccountID,
			Col: col, Cols: compactAvatarCols, Rows: 1,
		})
	}
	spans = append(spans, Span{Text: name, Style: nameStyle})
	return spans, avatars
}

func spansWidth(spans []Span) int {
	w := 0
	for _, sp := range spans {
		w += displayWidth(sp.Text)
	}
	return w
}

type avatarEntry struct {
	image     image.Image
	loading   bool
	retryAt   time.Time
	refreshAt time.Time
	version   uint64
}

// avatarStore loads each unique local override or remote URL once, normalizes
// it to a small PNG, and keeps a disk cache so cached PR lists can paint
// immediately next time. All network and disk work stays off the UI goroutine.
type avatarStore struct {
	client    *bbClient
	jira      *jiraClient
	dir       string
	overrides map[string]string
	mu        sync.Mutex
	entries   map[string]*avatarEntry
	wanted    map[string]struct{}
	downloads chan struct{}
	jiraBulk  chan struct{}
	ready     chan struct{}
}

func newAvatarStore(client *bbClient, jira *jiraClient, overrides map[string]string) *avatarStore {
	return &avatarStore{
		client:    client,
		jira:      jira,
		dir:       filepath.Join(stateDir(), "cache", "avatars"),
		overrides: overrides,
		entries:   map[string]*avatarEntry{},
		wanted:    map[string]struct{}{},
		downloads: make(chan struct{}, avatarDownloadConcurrent),
		jiraBulk:  make(chan struct{}, 1),
		ready:     make(chan struct{}, 1),
	}
}

type avatarSource struct {
	key           string
	localPath     string
	fallbackURL   string
	jiraAccountID string
}

// source resolves an explicit local override before the Bitbucket image.
// The file identity is part of the key so replacing the file naturally
// invalidates both memory and disk caches without restarting the plugin.
func (s *avatarStore) source(accountID, fallbackURL string) avatarSource {
	jiraAccountID := ""
	if s.jira != nil && accountID != "" && isInitialsAvatarURL(fallbackURL) {
		jiraAccountID = accountID
	}
	jiraKey := ""
	if jiraAccountID != "" {
		jiraKey = ":jira:" + jiraAccountID
	}
	path := s.overrides[accountID]
	if path == "" {
		return avatarSource{
			key: "url:" + fallbackURL + jiraKey, fallbackURL: fallbackURL,
			jiraAccountID: jiraAccountID,
		}
	}
	identity := "missing"
	if st, err := os.Stat(path); err == nil && st.Mode().IsRegular() {
		identity = fmt.Sprintf("%d:%d", st.ModTime().UnixNano(), st.Size())
	}
	return avatarSource{
		key:       "file:" + path + ":" + identity + ":url:" + fallbackURL + jiraKey,
		localPath: path, fallbackURL: fallbackURL, jiraAccountID: jiraAccountID,
	}
}

func isInitialsAvatarURL(rawURL string) bool {
	return isInitialsAvatarURLDepth(rawURL, 0)
}

func isInitialsAvatarURLDepth(rawURL string, depth int) bool {
	if depth > 2 {
		return false
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	if isDirectInitialsAvatarURL(rawURL) {
		return true
	}
	// Gravatar URLs often hide Atlassian's initials image in their default
	// (`d`) query parameter. Treat that as Jira-eligible as well.
	for _, name := range []string{"d", "default"} {
		if nested := u.Query().Get(name); nested != "" && isInitialsAvatarURLDepth(nested, depth+1) {
			return true
		}
	}
	return false
}

func isDirectInitialsAvatarURL(rawURL string) bool {
	u, err := url.Parse(rawURL)
	return err == nil && strings.Contains(strings.ToLower(u.Path), "/initials/")
}

type avatarSnapshot struct {
	image   image.Image
	version uint64
}

// images returns the currently available images for one rendered frame and
// schedules only its missing visible sources. All disk/network work remains
// asynchronous; repeated users and in-flight requests are coalesced by key.
func (s *avatarStore) images(cells []AvatarCell) []avatarSnapshot {
	sources := make([]avatarSource, len(cells))
	snapshots := make([]avatarSnapshot, len(cells))
	pending := make([]avatarSource, 0, len(cells))
	now := time.Now()
	for i, cell := range cells {
		sources[i] = s.source(cell.AccountID, cell.URL)
	}

	s.mu.Lock()
	if s.entries == nil {
		s.entries = map[string]*avatarEntry{}
	}
	if s.downloads == nil {
		s.downloads = make(chan struct{}, avatarDownloadConcurrent)
	}
	if s.jiraBulk == nil {
		s.jiraBulk = make(chan struct{}, 1)
	}
	s.wanted = make(map[string]struct{}, len(cells))
	for i, source := range sources {
		if source.key == "url:" {
			continue
		}
		s.wanted[source.key] = struct{}{}
		e := s.entries[source.key]
		if e == nil {
			e = &avatarEntry{loading: true}
			s.entries[source.key] = e
			pending = append(pending, source)
		} else if !e.loading && !now.Before(e.retryAt) &&
			(e.image == nil || (!e.refreshAt.IsZero() && !now.Before(e.refreshAt))) {
			e.loading = true
			pending = append(pending, source)
		}
		snapshots[i] = avatarSnapshot{image: e.image, version: e.version}
	}
	s.mu.Unlock()

	if len(pending) > 0 {
		go s.loadMany(pending)
	}
	return snapshots
}

// image is kept as the single-account convenience used by focused views and
// tests. The graphics path calls images once with the full visible frame.
func (s *avatarStore) image(accountID, rawURL string) (image.Image, uint64) {
	got := s.images([]AvatarCell{{AccountID: accountID, URL: rawURL}})
	if len(got) == 0 {
		return nil, 0
	}
	return got[0].image, got[0].version
}

type avatarCacheMeta struct {
	ResolvedURL string    `json:"resolved_url"`
	CheckedAt   time.Time `json:"checked_at"`
}

type preparedAvatar struct {
	source  avatarSource
	cached  image.Image
	meta    avatarCacheMeta
	hasMeta bool
	modTime time.Time
}

type avatarDownload struct {
	prepared     preparedAvatar
	url          string
	cacheable    bool
	retryJira    bool
	resolvedJira string
	checkedAt    time.Time
}

func (s *avatarStore) loadMany(sources []avatarSource) {
	now := time.Now()
	jiraByAccount := make(map[string][]preparedAvatar)
	var jiraOrder []string
	var downloads []avatarDownload
	published := false

	for _, source := range sources {
		if !s.isWanted(source.key) {
			s.abort(source.key)
			continue
		}
		cached, modTime := s.loadDiskEntry(s.cachePath(source.key))
		meta, hasMeta := s.loadMeta(source.key)
		if source.jiraAccountID != "" && cached != nil && !hasMeta && !modTime.IsZero() {
			meta = avatarCacheMeta{CheckedAt: modTime}
			hasMeta = true
			s.saveMeta(source.key, meta)
		}
		prepared := preparedAvatar{source: source, cached: cached, meta: meta, hasMeta: hasMeta, modTime: modTime}
		if cached != nil {
			published = s.publish(source.key, cached, false) || published
		}

		// A valid local override is immutable for this key: its mtime and size
		// are already part of source.key.
		if source.localPath != "" {
			if cached != nil {
				s.finishState(source.key, time.Time{}, false)
				continue
			}
			data, err := readAvatarFile(source.localPath)
			if err == nil {
				var normalized image.Image
				normalized, err = normalizeAvatar(data)
				if err == nil {
					s.saveDisk(s.cachePath(source.key), normalized)
					published = s.publish(source.key, normalized, true) || published
					s.finishState(source.key, time.Time{}, false)
					continue
				}
			}
			debugf("avatar override: %s: %v; using API fallback", source.localPath, err)
		}

		if source.jiraAccountID == "" {
			if cached != nil {
				s.finishState(source.key, time.Time{}, false)
				continue
			}
			downloads = append(downloads, avatarDownload{prepared: prepared, url: source.fallbackURL, cacheable: true})
			continue
		}

		checkedAt := meta.CheckedAt
		if checkedAt.IsZero() {
			checkedAt = modTime // legacy PNGs become valid 30-day cache entries
		}
		if cached != nil && !checkedAt.IsZero() && now.Before(checkedAt.Add(avatarCacheTTL)) {
			s.finishState(source.key, checkedAt.Add(avatarCacheTTL), false)
			continue
		}
		if _, ok := jiraByAccount[source.jiraAccountID]; !ok {
			jiraOrder = append(jiraOrder, source.jiraAccountID)
		}
		jiraByAccount[source.jiraAccountID] = append(jiraByAccount[source.jiraAccountID], prepared)
	}
	if published {
		s.notify()
	}

	for start := 0; start < len(jiraOrder); start += jiraBulkMaxUsers {
		end := start + jiraBulkMaxUsers
		if end > len(jiraOrder) {
			end = len(jiraOrder)
		}
		ids := jiraOrder[start:end]
		active := make([]string, 0, len(ids))
		for _, id := range ids {
			for _, prepared := range jiraByAccount[id] {
				if s.isWanted(prepared.source.key) {
					active = append(active, id)
					break
				}
			}
		}
		if len(active) == 0 {
			for _, id := range ids {
				for _, prepared := range jiraByAccount[id] {
					s.abort(prepared.source.key)
				}
			}
			continue
		}

		s.jiraBulk <- struct{}{}
		stillActive := make([]string, 0, len(active))
		for _, id := range active {
			for _, prepared := range jiraByAccount[id] {
				if s.isWanted(prepared.source.key) {
					stillActive = append(stillActive, id)
					break
				}
			}
		}
		if len(stillActive) == 0 {
			<-s.jiraBulk
			for _, id := range ids {
				for _, prepared := range jiraByAccount[id] {
					s.abort(prepared.source.key)
				}
			}
			continue
		}
		resolved, err := s.jira.avatarURLs(stillActive)
		<-s.jiraBulk
		checkedAt := time.Now()
		for _, id := range ids {
			for _, prepared := range jiraByAccount[id] {
				if !s.isWanted(prepared.source.key) {
					s.abort(prepared.source.key)
					continue
				}
				rawURL := resolved[id]
				resolveErr := err
				if resolveErr == nil && rawURL == "" {
					resolveErr = fmt.Errorf("Jira bulk response omitted account %s", id)
				}
				if resolveErr == nil && isDirectInitialsAvatarURL(rawURL) {
					resolveErr = fmt.Errorf("Jira returned an initials avatar")
				}
				if resolveErr != nil {
					debugf("jira avatar: account %s: %v; using Bitbucket fallback", id, resolveErr)
					if prepared.cached != nil {
						s.finishState(prepared.source.key, time.Now(), true)
						continue
					}
					downloads = append(downloads, avatarDownload{
						prepared: prepared, url: prepared.source.fallbackURL, retryJira: true,
					})
					continue
				}
				if prepared.cached != nil && prepared.hasMeta && prepared.meta.ResolvedURL == rawURL {
					meta := avatarCacheMeta{ResolvedURL: rawURL, CheckedAt: checkedAt}
					s.saveMeta(prepared.source.key, meta)
					s.finishState(prepared.source.key, checkedAt.Add(avatarCacheTTL), false)
					continue
				}
				downloads = append(downloads, avatarDownload{
					prepared: prepared, url: rawURL, cacheable: true,
					resolvedJira: rawURL, checkedAt: checkedAt,
				})
			}
		}
	}

	s.runDownloads(downloads)
}

func (s *avatarStore) runDownloads(downloads []avatarDownload) {
	var wg sync.WaitGroup
	var published atomic.Bool
	for _, download := range downloads {
		download := download
		wg.Add(1)
		go func() {
			defer wg.Done()
			key := download.prepared.source.key
			if download.url == "" || !s.isWanted(key) {
				s.abort(key)
				return
			}
			s.downloads <- struct{}{}
			defer func() { <-s.downloads }()
			if !s.isWanted(key) {
				s.abort(key)
				return
			}
			data, err := s.client.avatar(download.url)
			if err == nil {
				var normalized image.Image
				normalized, err = normalizeAvatar(data)
				if err == nil {
					saved := true
					if download.cacheable {
						saved = s.saveDisk(s.cachePath(key), normalized)
					}
					refreshAt := time.Time{}
					if download.resolvedJira != "" && saved {
						meta := avatarCacheMeta{ResolvedURL: download.resolvedJira, CheckedAt: download.checkedAt}
						s.saveMeta(key, meta)
						refreshAt = download.checkedAt.Add(avatarCacheTTL)
					}
					if s.publish(key, normalized, true) {
						published.Store(true)
					}
					s.finishState(key, refreshAt, download.retryJira || (download.cacheable && !saved))
					return
				}
			}
			debugf("avatar: %s: %v", key, err)
			s.finishState(key, time.Now(), true)
		}()
	}
	wg.Wait()
	if published.Load() {
		s.notify()
	}
}

func (s *avatarStore) isWanted(key string) bool {
	s.mu.Lock()
	_, ok := s.wanted[key]
	s.mu.Unlock()
	return ok
}

func (s *avatarStore) abort(key string) {
	s.mu.Lock()
	if e := s.entries[key]; e != nil {
		e.loading = false
	}
	s.mu.Unlock()
}

func (s *avatarStore) loadSource(source avatarSource) ([]byte, bool, error) {
	if source.localPath != "" {
		data, err := readAvatarFile(source.localPath)
		if err == nil {
			if _, err = normalizeAvatar(data); err == nil {
				return data, true, nil
			}
		}
		debugf("avatar override: %s: %v; using API fallback", source.localPath, err)
	}
	if source.jiraAccountID != "" {
		jiraURL, err := s.jira.avatarURL(source.jiraAccountID)
		if err == nil && isDirectInitialsAvatarURL(jiraURL) {
			err = fmt.Errorf("Jira returned an initials avatar")
		}
		if err == nil {
			var data []byte
			data, err = s.client.avatar(jiraURL)
			if err == nil {
				return data, true, nil
			}
		}
		debugf("jira avatar: account %s: %v; using Bitbucket fallback", source.jiraAccountID, err)
		if source.fallbackURL == "" {
			return nil, false, err
		}
		data, fallbackErr := s.client.avatar(source.fallbackURL)
		return data, false, fallbackErr
	}
	if source.fallbackURL == "" {
		return nil, false, fmt.Errorf("no usable local override or Bitbucket fallback")
	}
	data, err := s.client.avatar(source.fallbackURL)
	return data, true, err
}

func readAvatarFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular file")
	}
	data, err := io.ReadAll(io.LimitReader(f, maxAvatarBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxAvatarBytes {
		return nil, fmt.Errorf("avatar exceeds %d bytes", maxAvatarBytes)
	}
	return data, nil
}

// loadDisk returns a usable cached image. PNGs are retained indefinitely;
// Jira URL freshness is tracked separately in the metadata sidecar.
func (s *avatarStore) loadDisk(path string) (image.Image, bool) {
	img, _ := s.loadDiskEntry(path)
	return img, img != nil
}

func (s *avatarStore) loadDiskEntry(path string) (image.Image, time.Time) {
	f, err := os.Open(path)
	if err != nil {
		return nil, time.Time{}
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, time.Time{}
	}
	img, err := png.Decode(f)
	if err != nil {
		return nil, time.Time{}
	}
	return img, st.ModTime()
}

func (s *avatarStore) saveDisk(path string, img image.Image) bool {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		debugf("avatar cache mkdir: %v", err)
		return false
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".avatar-*.png")
	if err != nil {
		debugf("avatar cache create: %v", err)
		return false
	}
	tmpPath := tmp.Name()
	ok := false
	defer func() {
		tmp.Close()
		if !ok {
			os.Remove(tmpPath)
		}
	}()
	if err := png.Encode(tmp, img); err != nil {
		debugf("avatar cache encode: %v", err)
		return false
	}
	if err := tmp.Chmod(0o600); err != nil {
		debugf("avatar cache chmod: %v", err)
		return false
	}
	if err := tmp.Close(); err != nil {
		debugf("avatar cache close: %v", err)
		return false
	}
	if err := os.Rename(tmpPath, path); err != nil {
		debugf("avatar cache rename: %v", err)
		return false
	}
	ok = true
	return true
}

func (s *avatarStore) cachePath(key string) string {
	sum := sha256.Sum256([]byte(key))
	return filepath.Join(s.dir, fmt.Sprintf("%x.png", sum))
}

func (s *avatarStore) metaPath(key string) string {
	sum := sha256.Sum256([]byte(key))
	return filepath.Join(s.dir, fmt.Sprintf("%x.json", sum))
}

func (s *avatarStore) loadMeta(key string) (avatarCacheMeta, bool) {
	data, err := os.ReadFile(s.metaPath(key))
	if err != nil {
		return avatarCacheMeta{}, false
	}
	var meta avatarCacheMeta
	if err := json.Unmarshal(data, &meta); err != nil || meta.CheckedAt.IsZero() {
		return avatarCacheMeta{}, false
	}
	return meta, true
}

func (s *avatarStore) saveMeta(key string, meta avatarCacheMeta) {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		debugf("avatar metadata mkdir: %v", err)
		return
	}
	data, err := json.Marshal(meta)
	if err != nil {
		debugf("avatar metadata encode: %v", err)
		return
	}
	tmp, err := os.CreateTemp(s.dir, ".avatar-meta-*.json")
	if err != nil {
		debugf("avatar metadata create: %v", err)
		return
	}
	tmpPath := tmp.Name()
	ok := false
	defer func() {
		tmp.Close()
		if !ok {
			os.Remove(tmpPath)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		debugf("avatar metadata write: %v", err)
		return
	}
	if err := tmp.Chmod(0o600); err != nil {
		debugf("avatar metadata chmod: %v", err)
		return
	}
	if err := tmp.Close(); err != nil {
		debugf("avatar metadata close: %v", err)
		return
	}
	if err := os.Rename(tmpPath, s.metaPath(key)); err != nil {
		debugf("avatar metadata rename: %v", err)
		return
	}
	ok = true
}

func (s *avatarStore) publish(rawURL string, img image.Image, force bool) bool {
	published := false
	s.mu.Lock()
	e := s.entries[rawURL]
	if e != nil && (force || e.image == nil) {
		e.image = img
		e.version++
		published = true
	}
	s.mu.Unlock()
	return published
}

func (s *avatarStore) finishState(rawURL string, refreshAt time.Time, retry bool) {
	s.mu.Lock()
	if e := s.entries[rawURL]; e != nil {
		e.loading = false
		if retry && refreshAt.IsZero() {
			refreshAt = time.Now()
		}
		e.refreshAt = refreshAt
		if retry {
			e.retryAt = time.Now().Add(avatarRetryDelay)
		} else {
			e.retryAt = time.Time{}
		}
	}
	s.mu.Unlock()
}

func (s *avatarStore) notify() {
	select {
	case s.ready <- struct{}{}:
	default:
	}
}

func normalizeAvatar(data []byte) (image.Image, error) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decode avatar config: %w", err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > avatarMaxPixels || cfg.Height > avatarMaxPixels {
		return nil, fmt.Errorf("avatar dimensions %dx%d are outside 1..%d", cfg.Width, cfg.Height, avatarMaxPixels)
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decode avatar: %w", err)
	}
	b := src.Bounds()
	side := b.Dx()
	if b.Dy() < side {
		side = b.Dy()
	}
	srcRect := image.Rect(
		b.Min.X+(b.Dx()-side)/2,
		b.Min.Y+(b.Dy()-side)/2,
		b.Min.X+(b.Dx()-side)/2+side,
		b.Min.Y+(b.Dy()-side)/2+side,
	)
	dst := image.NewNRGBA(image.Rect(0, 0, avatarCachePixels, avatarCachePixels))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), src, srcRect, xdraw.Src, nil)
	return dst, nil
}

type renderedAvatar struct {
	cell  AvatarCell
	image image.Image
}

type avatarScene struct {
	seq     uint64
	avatars []renderedAvatar
}

// avatarGraphics owns Herdr's one image layer. Each submitted screen becomes
// one transparent composite, so many visible commenters can coexist even
// though pane.graphics.set accepts only one PNG.
type avatarGraphics struct {
	herdr   *herdrClient
	paneID  string
	metrics paneGraphicsMetrics
	store   *avatarStore

	work chan avatarScene
	stop chan struct{}
	done chan struct{}

	latest       atomic.Uint64
	lastSceneKey string // UI goroutine only
	active       bool   // graphics worker only
}

func newAvatarGraphics(herdr *herdrClient, client *bbClient, jira *jiraClient, enabled bool, overrides map[string]string) *avatarGraphics {
	if !enabled {
		return nil
	}
	paneID := os.Getenv("HERDR_PANE_ID")
	if paneID == "" {
		debugf("avatar: HERDR_PANE_ID is not set; graphics disabled")
		return nil
	}
	metrics, err := herdr.paneGraphicsInfo(paneID)
	if err != nil {
		debugf("avatar: pane graphics unavailable: %v", err)
		return nil
	}
	g := &avatarGraphics{
		herdr: herdr, paneID: paneID, metrics: metrics,
		store: newAvatarStore(client, jira, overrides),
		work:  make(chan avatarScene, 1), stop: make(chan struct{}), done: make(chan struct{}),
	}
	go g.run()
	return g
}

func (g *avatarGraphics) ready() <-chan struct{} { return g.store.ready }

// submit resolves every visible URL from cache, starts missing downloads,
// and coalesces rapid cursor/scroll changes down to the newest scene.
func (g *avatarGraphics) submit(cells []AvatarCell) {
	if g == nil {
		return
	}
	var scene avatarScene
	var key strings.Builder
	snapshots := g.store.images(cells)
	for i, cell := range cells {
		snapshot := snapshots[i]
		if snapshot.image == nil {
			continue
		}
		scene.avatars = append(scene.avatars, renderedAvatar{cell: cell, image: snapshot.image})
		fmt.Fprintf(&key, "%s:%s@%d,%d,%d,%d:%d;", cell.AccountID, cell.URL, cell.X, cell.Y, cell.Cols, cell.Rows, snapshot.version)
	}
	if key.String() == g.lastSceneKey {
		return
	}
	g.lastSceneKey = key.String()
	scene.seq = g.latest.Add(1)
	select {
	case g.work <- scene:
	default:
		select {
		case <-g.work:
		default:
		}
		g.work <- scene
	}
}

func (g *avatarGraphics) run() {
	defer close(g.done)
	for {
		select {
		case scene := <-g.work:
			g.apply(scene)
		case <-g.stop:
			if g.active {
				if err := g.herdr.paneGraphicsClear(g.paneID); err != nil {
					debugf("avatar: final graphics clear: %v", err)
				}
			}
			return
		}
	}
}

func (g *avatarGraphics) apply(scene avatarScene) {
	if scene.seq != g.latest.Load() {
		return
	}
	if len(scene.avatars) == 0 {
		if g.active {
			if err := g.herdr.paneGraphicsClear(g.paneID); err != nil {
				debugf("avatar: graphics clear: %v", err)
				return
			}
			g.active = false
		}
		return
	}
	pngData, imageW, imageH, placement, err := composeAvatarScene(scene.avatars, g.metrics)
	if err != nil {
		debugf("avatar: compose: %v", err)
		return
	}
	if scene.seq != g.latest.Load() {
		return
	}
	if err := g.herdr.paneGraphicsSet(g.paneID, pngData, imageW, imageH, placement); err != nil {
		debugf("avatar: graphics set: %v", err)
		return
	}
	g.active = true
}

func (g *avatarGraphics) close() {
	if g == nil {
		return
	}
	close(g.stop)
	<-g.done
}

func composeAvatarScene(avatars []renderedAvatar, metrics paneGraphicsMetrics) ([]byte, int, int, AvatarCell, error) {
	if len(avatars) == 0 || metrics.CellWidthPX <= 0 || metrics.CellHeightPX <= 0 {
		return nil, 0, 0, AvatarCell{}, fmt.Errorf("empty avatar scene or invalid cell metrics")
	}
	minX, minY := avatars[0].cell.X, avatars[0].cell.Y
	maxX := avatars[0].cell.X + avatars[0].cell.Cols
	maxY := avatars[0].cell.Y + avatars[0].cell.Rows
	for _, a := range avatars[1:] {
		if a.cell.X < minX {
			minX = a.cell.X
		}
		if a.cell.Y < minY {
			minY = a.cell.Y
		}
		if x := a.cell.X + a.cell.Cols; x > maxX {
			maxX = x
		}
		if y := a.cell.Y + a.cell.Rows; y > maxY {
			maxY = y
		}
	}
	cols, rows := maxX-minX, maxY-minY
	imageW, imageH := cols*metrics.CellWidthPX, rows*metrics.CellHeightPX
	canvas := image.NewNRGBA(image.Rect(0, 0, imageW, imageH))
	for _, a := range avatars {
		x := (a.cell.X - minX) * metrics.CellWidthPX
		y := (a.cell.Y - minY) * metrics.CellHeightPX
		dst := image.Rect(x, y, x+a.cell.Cols*metrics.CellWidthPX, y+a.cell.Rows*metrics.CellHeightPX)
		xdraw.CatmullRom.Scale(canvas, dst, a.image, a.image.Bounds(), xdraw.Over, nil)
	}
	var out bytes.Buffer
	if err := png.Encode(&out, canvas); err != nil {
		return nil, 0, 0, AvatarCell{}, err
	}
	return out.Bytes(), imageW, imageH, AvatarCell{X: minX, Y: minY, Cols: cols, Rows: rows}, nil
}
