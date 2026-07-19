package main

import (
	"os"

	"golang.org/x/sys/unix"
)

// KeyKind classifies decoded input events.
type KeyKind int

const (
	KeyRune KeyKind = iota
	KeyEsc
	KeyEnter
	KeyBackspace
	KeyCtrl // R holds the letter, e.g. {KeyCtrl, 'f'}
	KeyUp
	KeyDown
	KeyLeft
	KeyRight
)

// Key is one decoded keypress.
type Key struct {
	Kind KeyKind
	R    rune
}

// keyReader decodes raw-mode bytes into Keys. Its one non-trivial job is
// telling a bare Esc press apart from the ESC that starts an arrow-key
// sequence: after ESC it peeks (with a short poll timeout) for a following
// byte. peek is injectable for tests.
type keyReader struct {
	f       *os.File
	peek    func() bool // true when a byte is ready within the grace period
	pending []byte      // bytes consumed while disambiguating, replayed first
}

func newKeyReader(f *os.File) *keyReader {
	k := &keyReader{f: f}
	k.peek = func() bool {
		fds := []unix.PollFd{{Fd: int32(f.Fd()), Events: unix.POLLIN}}
		n, err := unix.Poll(fds, 20 /* ms */)
		return err == nil && n > 0
	}
	return k
}

func (k *keyReader) readByte() (byte, error) {
	if len(k.pending) > 0 {
		b := k.pending[0]
		k.pending = k.pending[1:]
		return b, nil
	}
	var buf [1]byte
	for {
		n, err := k.f.Read(buf[:])
		if err != nil {
			return 0, err
		}
		if n == 1 {
			return buf[0], nil
		}
	}
}

// Read blocks for the next keypress.
func (k *keyReader) Read() (Key, error) {
	b, err := k.readByte()
	if err != nil {
		return Key{}, err
	}
	switch {
	case b == 0x1b:
		if !k.peek() {
			return Key{Kind: KeyEsc}, nil
		}
		b2, err := k.readByte()
		if err != nil {
			return Key{Kind: KeyEsc}, nil
		}
		if b2 == 0x1b {
			// Two quick Esc presses: report one now, replay the other.
			k.pending = append(k.pending, b2)
			return Key{Kind: KeyEsc}, nil
		}
		if b2 == '[' || b2 == 'O' {
			b3, err := k.readByte()
			if err == nil {
				switch b3 {
				case 'A':
					return Key{Kind: KeyUp}, nil
				case 'B':
					return Key{Kind: KeyDown}, nil
				case 'C':
					return Key{Kind: KeyRight}, nil
				case 'D':
					return Key{Kind: KeyLeft}, nil
				}
			}
		}
		// Unknown sequence: swallow what we read, report Esc.
		return Key{Kind: KeyEsc}, nil
	case b == 0x0d || b == 0x0a:
		return Key{Kind: KeyEnter}, nil
	case b == 0x7f || b == 0x08:
		return Key{Kind: KeyBackspace}, nil
	case b < 0x20:
		return Key{Kind: KeyCtrl, R: rune('a' + b - 1)}, nil
	default:
		return Key{Kind: KeyRune, R: rune(b)}, nil
	}
}
