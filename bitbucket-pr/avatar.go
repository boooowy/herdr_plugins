package main

import (
	"bytes"
	"crypto/sha256"
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
	compactAvatarCols = 2
	avatarCachePixels = 64
	avatarMaxPixels   = 2048
	avatarCacheTTL    = 24 * time.Hour
	avatarRetryDelay  = 5 * time.Minute
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
	image   image.Image
	loading bool
	retryAt time.Time
	version uint64
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
	ready     chan struct{}
}

func newAvatarStore(client *bbClient, jira *jiraClient, overrides map[string]string) *avatarStore {
	return &avatarStore{
		client:    client,
		jira:      jira,
		dir:       filepath.Join(stateDir(), "cache", "avatars"),
		overrides: overrides,
		entries:   map[string]*avatarEntry{},
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
	u, err := url.Parse(rawURL)
	return err == nil && strings.Contains(strings.ToLower(u.Path), "/initials/")
}

// image returns a cached image and its generation. A miss starts one async
// load and returns nil; ready receives a coalesced repaint signal later.
func (s *avatarStore) image(accountID, rawURL string) (image.Image, uint64) {
	source := s.source(accountID, rawURL)
	if source.key == "url:" {
		return nil, 0
	}
	key := source.key
	now := time.Now()
	s.mu.Lock()
	e := s.entries[key]
	if e == nil {
		e = &avatarEntry{loading: true}
		s.entries[key] = e
		go s.load(source)
	} else if !e.loading && e.image == nil && !now.Before(e.retryAt) {
		e.loading = true
		go s.load(source)
	}
	img, version := e.image, e.version
	s.mu.Unlock()
	return img, version
}

func (s *avatarStore) load(source avatarSource) {
	path := s.cachePath(source.key)
	stale, fresh := s.loadDisk(path)
	if stale != nil {
		s.publish(source.key, stale, false)
	}
	if fresh {
		s.finish(source.key, stale != nil)
		return
	}

	data, cacheable, err := s.loadSource(source)
	if err == nil {
		var normalized image.Image
		normalized, err = normalizeAvatar(data)
		if err == nil {
			if !cacheable && stale != nil {
				s.finish(source.key, true)
				return
			}
			if cacheable {
				s.saveDisk(path, normalized)
			}
			s.publish(source.key, normalized, true)
			s.finish(source.key, true)
			return
		}
	}
	debugf("avatar: %s: %v", source.key, err)
	s.finish(source.key, stale != nil)
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
		if err == nil && isInitialsAvatarURL(jiraURL) {
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

// loadDisk returns a usable cached image and whether it is still fresh.
func (s *avatarStore) loadDisk(path string) (image.Image, bool) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, false
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		return nil, false
	}
	return img, time.Since(st.ModTime()) < avatarCacheTTL
}

func (s *avatarStore) saveDisk(path string, img image.Image) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		debugf("avatar cache mkdir: %v", err)
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".avatar-*.png")
	if err != nil {
		debugf("avatar cache create: %v", err)
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
	if err := png.Encode(tmp, img); err != nil {
		debugf("avatar cache encode: %v", err)
		return
	}
	if err := tmp.Chmod(0o600); err != nil {
		debugf("avatar cache chmod: %v", err)
		return
	}
	if err := tmp.Close(); err != nil {
		debugf("avatar cache close: %v", err)
		return
	}
	if err := os.Rename(tmpPath, path); err != nil {
		debugf("avatar cache rename: %v", err)
		return
	}
	ok = true
}

func (s *avatarStore) cachePath(key string) string {
	sum := sha256.Sum256([]byte(key))
	return filepath.Join(s.dir, fmt.Sprintf("%x.png", sum))
}

func (s *avatarStore) publish(rawURL string, img image.Image, force bool) {
	s.mu.Lock()
	e := s.entries[rawURL]
	if e != nil && (force || e.image == nil) {
		e.image = img
		e.version++
	}
	s.mu.Unlock()
	s.notify()
}

func (s *avatarStore) finish(rawURL string, usable bool) {
	s.mu.Lock()
	if e := s.entries[rawURL]; e != nil {
		e.loading = false
		if !usable {
			e.retryAt = time.Now().Add(avatarRetryDelay)
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
	for _, cell := range cells {
		img, version := g.store.image(cell.AccountID, cell.URL)
		if img == nil {
			continue
		}
		scene.avatars = append(scene.avatars, renderedAvatar{cell: cell, image: img})
		fmt.Fprintf(&key, "%s:%s@%d,%d,%d,%d:%d;", cell.AccountID, cell.URL, cell.X, cell.Y, cell.Cols, cell.Rows, version)
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
