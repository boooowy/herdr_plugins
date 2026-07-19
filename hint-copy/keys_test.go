package main

import (
	"os"
	"testing"
)

// pipeReader builds a keyReader over an os.Pipe with an injected peek that
// reports whether more bytes were written (pipes are pollable but tests
// control the grace-period outcome explicitly).
func pipeReader(t *testing.T, data []byte, peekResult bool) *keyReader {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	if _, err := w.Write(data); err != nil {
		t.Fatal(err)
	}
	w.Close()
	k := newKeyReader(r)
	k.peek = func() bool { return peekResult }
	return k
}

func TestKeyReaderPlainAndCtrl(t *testing.T) {
	k := pipeReader(t, []byte{'a', 0x06, 0x0d, 0x7f}, false)
	want := []Key{
		{Kind: KeyRune, R: 'a'},
		{Kind: KeyCtrl, R: 'f'},
		{Kind: KeyEnter},
		{Kind: KeyBackspace},
	}
	for i, w := range want {
		got, err := k.Read()
		if err != nil || got != w {
			t.Errorf("key %d = %+v (err %v), want %+v", i, got, err, w)
		}
	}
}

func TestKeyReaderBareEsc(t *testing.T) {
	k := pipeReader(t, []byte{0x1b}, false) // nothing follows in the grace period
	got, err := k.Read()
	if err != nil || got.Kind != KeyEsc {
		t.Errorf("got %+v err %v", got, err)
	}
}

func TestKeyReaderArrows(t *testing.T) {
	k := pipeReader(t, []byte("\x1b[A\x1b[B\x1b[C\x1b[D"), true)
	want := []KeyKind{KeyUp, KeyDown, KeyRight, KeyLeft}
	for i, kind := range want {
		got, err := k.Read()
		if err != nil || got.Kind != kind {
			t.Errorf("arrow %d = %+v err %v", i, got, err)
		}
	}
}

func TestKeyReaderDoubleEsc(t *testing.T) {
	k := pipeReader(t, []byte{0x1b, 0x1b}, true) // two quick presses stay two
	for i := 0; i < 2; i++ {
		got, err := k.Read()
		if err != nil || got.Kind != KeyEsc {
			t.Errorf("esc %d = %+v err %v", i, got, err)
		}
	}
}

func TestKeyReaderUnknownSequenceIsEsc(t *testing.T) {
	k := pipeReader(t, []byte("\x1b[Z"), true) // shift-tab: swallowed, reported as Esc
	got, _ := k.Read()
	if got.Kind != KeyEsc {
		t.Errorf("got %+v", got)
	}
}
