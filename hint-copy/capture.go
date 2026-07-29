package main

import "sync"

// tabCapture is the raw ANSI text of every pane involved in the overlay.
// The socket round trips dominate startup, so they all run concurrently —
// and because the captured text is size-independent, the capture can overlap
// the wait for the overlay pty to settle to its final size.
type tabCapture struct {
	titles map[string]string
	ansi   map[string]string // pane id → raw ANSI capture
}

// captureConcurrency bounds the parallel pane.read calls: enough to overlap
// the round trips, low enough not to hammer herdr's accept loop.
const captureConcurrency = 6

// captureTab reads every pane of the snapshot layout (or just the target
// when no snapshot exists) concurrently, plus the pane titles for the
// composite borders. A failed read leaves "" — that pane paints blank,
// matching the previous serial behavior.
func captureTab(client *herdrClient, lay *paneLayoutResult, target string) *tabCapture {
	ids := []string{target}
	if lay != nil {
		ids = ids[:0]
		for _, p := range lay.Panes {
			ids = append(ids, p.PaneID)
		}
	}

	tc := &tabCapture{ansi: make(map[string]string, len(ids))}
	var wg sync.WaitGroup
	var mu sync.Mutex
	sem := make(chan struct{}, captureConcurrency)

	if lay != nil { // titles only decorate the composite borders
		wg.Add(1)
		go func() {
			defer wg.Done()
			titles := client.paneTitles()
			mu.Lock()
			tc.titles = titles
			mu.Unlock()
		}()
	}
	for _, id := range ids {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			text, _, err := client.paneReadFull(id, "visible", 0, true)
			if err != nil {
				text = ""
			}
			mu.Lock()
			tc.ansi[id] = text
			mu.Unlock()
		}(id)
	}
	wg.Wait()
	return tc
}

// tabParsed is the size-independent parse of a capture: per-pane styled
// cells and plain lines. parseANSI interns styles into the shared,
// unsynchronized styleTable, so parsing stays sequential by design — only
// the network reads above are parallel.
type tabParsed struct {
	titles map[string]string
	styled map[string][][]Cell
	plain  map[string][]string
}

func parseCapture(tc *tabCapture, tbl *styleTable) *tabParsed {
	p := &tabParsed{
		titles: tc.titles,
		styled: make(map[string][][]Cell, len(tc.ansi)),
		plain:  make(map[string][]string, len(tc.ansi)),
	}
	for id, text := range tc.ansi {
		cells, plain := parseANSI(text, tbl)
		p.styled[id] = cells
		p.plain[id] = plain
	}
	return p
}
