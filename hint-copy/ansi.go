package main

import (
	"strings"

	"github.com/mattn/go-runewidth"
)

// Dynamic styles: ANSI-captured pane content carries arbitrary SGR
// combinations (syntax highlighting etc.). They are interned into a
// styleTable; ids from dynStyleBase up index it, below are the built-ins.
const dynStyleBase StyleID = 16

type styleTable struct {
	ids  map[string]StyleID
	list []string // id - dynStyleBase → full SGR sequence
}

func newStyleTable() *styleTable {
	return &styleTable{ids: make(map[string]StyleID)}
}

// id interns an SGR parameter string ("1;38;5;208") and returns its StyleID.
func (t *styleTable) id(params string) StyleID {
	if params == "" {
		return styleNone
	}
	if id, ok := t.ids[params]; ok {
		return id
	}
	id := dynStyleBase + StyleID(len(t.list))
	t.list = append(t.list, "\x1b["+params+"m")
	t.ids[params] = id
	return id
}

func (t *styleTable) sequence(id StyleID) string {
	i := int(id - dynStyleBase)
	if i < 0 || i >= len(t.list) {
		return ""
	}
	return t.list[i]
}

// sgrState tracks the active SGR attributes while scanning captured ANSI.
// It handles reset and appends everything else, keeping 38/48 extended color
// specs intact — enough to reproduce terminal content faithfully.
type sgrState struct {
	params []string
}

func (st *sgrState) apply(seq string) {
	if seq == "" {
		st.params = nil
		return
	}
	parts := strings.Split(seq, ";")
	for i := 0; i < len(parts); i++ {
		switch parts[i] {
		case "", "0":
			st.params = nil
		case "38", "48":
			if i+1 < len(parts) && parts[i+1] == "5" && i+2 < len(parts) {
				st.params = append(st.params, strings.Join(parts[i:i+3], ";"))
				i += 2
			} else if i+1 < len(parts) && parts[i+1] == "2" && i+4 < len(parts) {
				st.params = append(st.params, strings.Join(parts[i:i+5], ";"))
				i += 4
			}
		default:
			st.params = append(st.params, parts[i])
		}
	}
}

func (st *sgrState) key() string { return strings.Join(st.params, ";") }

// parseANSI turns a raw ANSI capture into per-line cells (with interned
// styles) plus the plain text lines. Only SGR sequences are interpreted; all
// other CSI/OSC sequences are skipped. The plain lines are derived from the
// same scan, so extraction offsets always agree with the painted cells.
func parseANSI(text string, tbl *styleTable) (cells [][]Cell, plain []string) {
	rs := []rune(text)
	var line []Cell
	var sb strings.Builder
	st := sgrState{}
	flush := func() {
		cells = append(cells, line)
		plain = append(plain, sb.String())
		line = nil
		sb.Reset()
	}
	for i := 0; i < len(rs); {
		r := rs[i]
		switch {
		case r == '\x1b' && i+1 < len(rs) && rs[i+1] == '[':
			j := i + 2
			for j < len(rs) && !isCSIFinal(rs[j]) {
				j++
			}
			if j < len(rs) && rs[j] == 'm' {
				st.apply(string(rs[i+2 : j]))
			}
			i = j + 1
		case r == '\x1b' && i+1 < len(rs) && rs[i+1] == ']':
			// OSC: skip until BEL or ST (ESC \).
			j := i + 2
			for j < len(rs) && rs[j] != '\a' && !(rs[j] == '\x1b' && j+1 < len(rs) && rs[j+1] == '\\') {
				j++
			}
			if j < len(rs) && rs[j] == '\x1b' {
				j++
			}
			i = j + 1
		case r == '\x1b':
			i += 2 // ESC + one char (charset selection etc.)
		case r == '\n':
			flush()
			i++
		case r == '\r':
			i++
		default:
			style := tbl.id(st.key())
			line = append(line, Cell{R: r, Style: style})
			if runewidth.RuneWidth(r) == 2 {
				line = append(line, Cell{R: 0, Style: style})
			}
			sb.WriteRune(r)
			i++
		}
	}
	flush()
	return cells, plain
}

func isCSIFinal(r rune) bool { return r >= 0x40 && r <= 0x7e }
