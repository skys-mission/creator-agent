package terminal

import (
	"bufio"
	"io"
	"os"
	"time"
	"unicode/utf8"
)

type Key int

const (
	KeyRune Key = iota
	KeyEnter
	KeyTab
	KeyBacktab // Shift+Tab (CSI Z)
	KeyBackspace
	KeyBackspace2
	KeyDelete
	KeyLeft
	KeyRight
	KeyUp
	KeyDown
	KeyHome
	KeyEnd
	KeyPgUp
	KeyPgDn
	KeyEsc
	KeyCtrlA
	KeyCtrlB
	KeyCtrlC
	KeyCtrlD
	KeyCtrlE
	KeyCtrlF
	KeyCtrlG
	KeyCtrlH
	KeyCtrlI
	KeyCtrlJ
	KeyCtrlK
	KeyCtrlL
	KeyCtrlM
	KeyCtrlN
	KeyCtrlO
	KeyCtrlP
	KeyCtrlQ
	KeyCtrlR
	KeyCtrlS
	KeyCtrlT
	KeyCtrlU
	KeyCtrlV
	KeyCtrlW
	KeyCtrlX
	KeyCtrlY
	KeyCtrlZ
)

type ModMask uint8

const (
	ModNone ModMask = 0
	ModAlt  ModMask = 1 << iota
	ModCtrl
)

// Mouse button codes reported by SGR mouse mode (\x1b[?1006h). Wheel events use buttons 64/65.
const (
	MouseLeft      = 0
	MouseMiddle    = 1
	MouseRight     = 2
	MouseWheelUp   = 64
	MouseWheelDown = 65
)

type Event interface{ event() }

func (*EventKey) event()    {}
func (*EventResize) event() {}
func (*EventMouse) event()  {}

type EventKey struct {
	key  Key
	rune rune
	mod  ModMask
}

func NewEventKey(k Key, r rune, m ModMask) *EventKey { return &EventKey{key: k, rune: r, mod: m} }

func (e *EventKey) Key() Key           { return e.key }
func (e *EventKey) Rune() rune         { return e.rune }
func (e *EventKey) Modifiers() ModMask { return e.mod }

type EventResize struct {
	W, H int
}

func (e *EventResize) Size() (int, int) { return e.W, e.H }

// EventMouse carries a decoded SGR mouse report. Button is a Mouse* constant; X/Y are 0-based
// screen coordinates; Press is true for button-press / wheel, false for button-release.
type EventMouse struct {
	button int
	x, y   int
	press  bool
}

func (e *EventMouse) Button() int          { return e.button }
func (e *EventMouse) Position() (int, int) { return e.x, e.y }
func (e *EventMouse) IsPress() bool        { return e.press }

// escTimeout is how long PumpInput waits, after reading a lone 0x1b, for a following byte before
// deciding the 0x1b is a standalone Esc rather than the start of an escape sequence. Terminals
// deliver multi-byte sequences (arrows, Alt combos, mouse reports) atomically within a few
// milliseconds, whereas a standalone Esc never has a follower — so without this timeout PumpInput
// blocks in the read syscall until the next unrelated keypress (the "Esc freezes the UI" bug).
// 30ms is the conventional value used by terminal libraries; raise it if reports come in from
// high-latency SSH links. Overridable in tests.
var escTimeout = 30 * time.Millisecond

// readResult is the outcome of an async single-byte read used by PumpInput's esc-timeout path.
type readResult struct {
	b   byte
	err error
}

// asyncReadByte reads one byte from br on a fresh goroutine and reports the result on a buffered
// channel. Used by PumpInput to probe for the byte following a 0x1b without blocking the decode
// loop. The buffered channel lets the goroutine always post its result and exit even if the caller
// has stopped selecting on it. CRITICAL: while this goroutine is in flight, no other goroutine may
// touch br (single-writer invariant); PumpInput guarantees this by parking in a channel-only select.
func asyncReadByte(br *bufio.Reader) chan readResult {
	c := make(chan readResult, 1)
	go func() {
		b, err := br.ReadByte()
		c <- readResult{b: b, err: err}
	}()
	return c
}

// PumpInput decodes the raw byte stream from r into Events and forwards them on ch until quit
// closes or r errors (e.g. EOF). It owns a bufio.Reader over r and is the sole reader.
//
// Esc handling: a lone 0x1b is indistinguishable from the start of a multi-byte escape sequence
// (arrows \x1b[A, Alt combos \x1b<key>, mouse reports) until the next byte arrives. Terminals send
// such sequences atomically, so if no byte follows within escTimeout the 0x1b is a standalone Esc.
// Without this timeout the read would block until the next unrelated keypress. The probe read runs
// on a helper goroutine (asyncReadByte); if it times out, its still-pending result is stashed in
// pending and reclaimed as the next input byte on the following iteration, so no byte is lost.
//
// Single-writer invariant on br: the main loop never touches br while pending != nil — it is parked
// in a channel-only select — so asyncReadByte's goroutine is the exclusive reader during that window.
func PumpInput(r io.Reader, ch chan<- Event, quit <-chan struct{}) {
	br := bufio.NewReader(r)
	var pending chan readResult // non-nil => an async read is in flight from a prior esc-timeout
	var first byte
	for {
		// Acquire the next first byte, reclaiming an in-flight async read if one exists.
		if pending != nil {
			select {
			case res := <-pending:
				pending = nil
				if res.err != nil {
					return
				}
				first = res.b
			case <-quit:
				return
			}
		} else {
			select {
			case <-quit:
				return
			default:
			}
			b, err := br.ReadByte()
			if err != nil {
				return
			}
			first = b
		}

		var ev Event
		if first == 0x1b {
			nextCh := asyncReadByte(br)
			select {
			case res := <-nextCh:
				if res.err != nil {
					ev = &EventKey{key: KeyEsc}
				} else {
					ev = decodeEscWithByte(br, res.b)
				}
			case <-time.After(escTimeout):
				ev = &EventKey{key: KeyEsc}
				pending = nextCh
			case <-quit:
				return
			}
		} else {
			ev = Decode(br, first)
		}
		if ev == nil {
			continue
		}
		select {
		case ch <- ev:
		case <-quit:
			return
		}
	}
}

func Decode(br *bufio.Reader, first byte) Event {
	switch {
	case first == 0x1b:
		return decodeEsc(br)
	case first == 0x7f:
		return &EventKey{key: KeyBackspace2}
	case first == 0x08:
		return &EventKey{key: KeyBackspace}
	case first == '\r' || first == '\n':
		return &EventKey{key: KeyEnter}
	case first == '\t':
		return &EventKey{key: KeyTab}
	case first < 0x20:
		return ctrlKey(first)
	case first < 0x80:
		return &EventKey{key: KeyRune, rune: rune(first)}
	default:
		return decodeUTF8(br, first)
	}
}

func ctrlKey(b byte) Event {
	keys := map[byte]Key{
		0x01: KeyCtrlA, 0x02: KeyCtrlB, 0x03: KeyCtrlC, 0x04: KeyCtrlD,
		0x05: KeyCtrlE, 0x06: KeyCtrlF, 0x07: KeyCtrlG, 0x08: KeyCtrlH,
		0x09: KeyCtrlI, 0x0a: KeyCtrlJ, 0x0b: KeyCtrlK, 0x0c: KeyCtrlL,
		0x0d: KeyCtrlM, 0x0e: KeyCtrlN, 0x0f: KeyCtrlO, 0x10: KeyCtrlP,
		0x11: KeyCtrlQ, 0x12: KeyCtrlR, 0x13: KeyCtrlS, 0x14: KeyCtrlT,
		0x15: KeyCtrlU, 0x16: KeyCtrlV, 0x17: KeyCtrlW, 0x18: KeyCtrlX,
		0x19: KeyCtrlY, 0x1a: KeyCtrlZ,
	}
	if k, ok := keys[b]; ok {
		return &EventKey{key: k}
	}
	return nil
}

// NOTE: decodeEsc / decodeCSI return the Event interface (not *EventKey) so that the "nothing
// decoded" case returns a true nil interface. Returning a typed (*EventKey)(nil) would make
// PumpInput's `ev == nil` check pass as non-nil (typed-nil pitfall), and a later e.Key() would
// dereference nil and crash the whole TUI on any unrecognized escape sequence.
func decodeEsc(br *bufio.Reader) Event {
	b, err := br.ReadByte()
	if err != nil {
		return &EventKey{key: KeyEsc}
	}
	return decodeEscWithByte(br, b)
}

// decodeEscWithByte continues escape-sequence decoding given the byte already read after the 0x1b.
// Split from decodeEsc so PumpInput's esc-timeout path can pass an already-read byte directly,
// avoiding a second blocking read. decodeEsc/Decode stay purely synchronous for their callers.
func decodeEscWithByte(br *bufio.Reader, b byte) Event {
	switch b {
	case '[':
		return decodeCSI(br)
	case 'O':
		nb, err := br.ReadByte()
		if err != nil {
			return &EventKey{key: KeyEsc}
		}
		switch nb {
		case 'A':
			return &EventKey{key: KeyUp}
		case 'B':
			return &EventKey{key: KeyDown}
		case 'C':
			return &EventKey{key: KeyRight}
		case 'D':
			return &EventKey{key: KeyLeft}
		case 'H':
			return &EventKey{key: KeyHome}
		case 'F':
			return &EventKey{key: KeyEnd}
		}
		return &EventKey{key: KeyEsc}
	case '\r', '\n':
		return &EventKey{key: KeyEnter, mod: ModAlt}
	case 0x7f:
		return &EventKey{key: KeyBackspace2, mod: ModAlt}
	default:
		if b >= 0x01 && b <= 0x1a {
			if ev := ctrlKey(b); ev != nil {
				ev.(*EventKey).mod = ModCtrl | ModAlt
				return ev
			}
		}
		if b >= 0x20 && b < 0x80 {
			return &EventKey{key: KeyRune, rune: rune(b), mod: ModAlt}
		}
		if b >= 0x80 {
			if ev := decodeUTF8(br, b); ev != nil {
				if ek, ok := ev.(*EventKey); ok {
					ek.mod = ModAlt
					return ek
				}
			}
		}
		return &EventKey{key: KeyEsc}
	}
}

func decodeCSI(br *bufio.Reader) Event {
	var params []byte
	const maxCSIParams = 32
	for {
		b, err := br.ReadByte()
		if err != nil {
			return &EventKey{key: KeyEsc}
		}
		if b >= 0x40 && b <= 0x7e {
			// SGR mouse mode: CSI < button ; col ; row M/m
			if len(params) > 0 && params[0] == '<' {
				return decodeSGRMouse(params[1:], b)
			}
			// Legacy X10 mouse mode: CSI M <button+32> <col+32> <row+32>.
			// Terminals that do not support SGR encoding (\x1b[?1006h), e.g. macOS Terminal.app,
			// fall back to this format. Without decoding it, the 3 trailing bytes leak into the
			// key stream and corrupt the TUI.
			if b == 'M' && len(params) == 0 {
				return decodeLegacyMouse(br)
			}
			switch b {
			case 'A':
				return &EventKey{key: KeyUp}
			case 'B':
				return &EventKey{key: KeyDown}
			case 'C':
				return &EventKey{key: KeyRight}
			case 'D':
				return &EventKey{key: KeyLeft}
			case 'H':
				return &EventKey{key: KeyHome}
			case 'F':
				return &EventKey{key: KeyEnd}
			case 'Z': // Shift+Tab (CSI Z) — reverse tab.
				return &EventKey{key: KeyBacktab}
			case '~':
				switch string(params) {
				case "1", "7":
					return &EventKey{key: KeyHome}
				case "4", "8":
					return &EventKey{key: KeyEnd}
				case "3":
					return &EventKey{key: KeyDelete}
				case "5":
					return &EventKey{key: KeyPgUp}
				case "6":
					return &EventKey{key: KeyPgDn}
				}
				return nil
			}
			return nil
		}
		if len(params) < maxCSIParams {
			params = append(params, b)
		}
	}
}

// decodeLegacyMouse parses the X10 legacy mouse report: CSI M followed by three bytes whose
// values are (button+32), (col+32), (row+32). col/row are 1-based; converted to 0-based. Returns a
// true nil interface if the stream is too short or values are out of range, so PumpInput drops it.
//
// Limitation: coordinates > 95 produce byte values >= 128 and may collide with UTF-8 in some
// terminal implementations. This is a terminal-side limitation of the X10 protocol; wheel events
// still work because the button code (64/65) is what we care about.
func decodeLegacyMouse(br *bufio.Reader) Event {
	var cb [3]byte
	for i := range cb {
		b, err := br.ReadByte()
		if err != nil {
			return nil // truncated mouse report: drop it rather than emit Esc
		}
		cb[i] = b
	}
	button := int(cb[0]) - 32
	col := int(cb[1]) - 32
	row := int(cb[2]) - 32
	if button < 0 || col <= 0 || row <= 0 {
		return nil
	}
	return &EventMouse{button: button, x: col - 1, y: row - 1, press: true}
}

// decodeSGRMouse parses the SGR mouse payload "button;col;row" (the bytes after '<') with final
// byte 'M' (press/wheel) or 'm' (release). col/row are 1-based from the terminal; converted to
// 0-based. Returns a true nil interface on parse error so PumpInput drops the event.
func decodeSGRMouse(params []byte, final byte) Event {
	var button, col, row int
	fields := [3]*int{&button, &col, &row}
	fi, n := 0, 0
	for _, b := range params {
		if b == ';' {
			fi++
			if fi >= 3 {
				return nil
			}
			n = 0
			continue
		}
		if b < '0' || b > '9' {
			return nil
		}
		n = n*10 + int(b-'0')
		*fields[fi] = n
	}
	if fi != 2 {
		return nil
	}
	return &EventMouse{button: button, x: col - 1, y: row - 1, press: final == 'M'}
}

func decodeUTF8(br *bufio.Reader, first byte) Event {
	var size int
	switch {
	case first&0xe0 == 0xc0:
		size = 2
	case first&0xf0 == 0xe0:
		size = 3
	case first&0xf8 == 0xf0:
		size = 4
	default:
		return nil
	}
	buf := make([]byte, 0, size)
	buf = append(buf, first)
	for len(buf) < size {
		b, err := br.ReadByte()
		if err != nil {
			return nil
		}
		buf = append(buf, b)
	}
	r, _ := utf8.DecodeRune(buf)
	if r == utf8.RuneError {
		return nil
	}
	return &EventKey{key: KeyRune, rune: r}
}

func StdinReader() io.Reader { return os.Stdin }
