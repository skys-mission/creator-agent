package tui

// keyparse_test.go verifies the input byte-stream decoder maps terminal escape sequences to the
// expected EventKey values, covering the keys the app actually binds.

import (
	"bufio"
	"strings"
	"testing"
)

// feed decodes a byte string and returns the produced events (reads until the reader is empty).
func feed(t *testing.T, in string) []Event {
	t.Helper()
	br := bufio.NewReader(strings.NewReader(in))
	var got []Event
	for {
		b, err := br.ReadByte()
		if err != nil {
			break
		}
		ev := Decode(br, b)
		if ev != nil {
			got = append(got, ev)
		}
	}
	return got
}

func TestDecodeASCIIRune(t *testing.T) {
	evs := feed(t, "h")
	if len(evs) != 1 {
		t.Fatalf("got %d events", len(evs))
	}
	ek := evs[0].(*EventKey)
	if ek.Key() != KeyRune || ek.Rune() != 'h' {
		t.Errorf("got key=%v rune=%q, want KeyRune/h", ek.Key(), ek.Rune())
	}
}

func TestDecodeMultiByteCJK(t *testing.T) {
	evs := feed(t, "你")
	ek := evs[0].(*EventKey)
	if ek.Key() != KeyRune || ek.Rune() != '你' {
		t.Errorf("got key=%v rune=%q, want KeyRune/你", ek.Key(), ek.Rune())
	}
}

func TestDecodeArrows(t *testing.T) {
	cases := map[string]Key{
		"\x1b[A": KeyUp, "\x1b[B": KeyDown, "\x1b[C": KeyRight, "\x1b[D": KeyLeft,
		"\x1bOA": KeyUp, "\x1bOB": KeyDown, "\x1bOC": KeyRight, "\x1bOD": KeyLeft,
	}
	for seq, want := range cases {
		evs := feed(t, seq)
		if len(evs) != 1 {
			t.Errorf("%q -> %d events", seq, len(evs))
			continue
		}
		ek := evs[0].(*EventKey)
		if ek.Key() != want {
			t.Errorf("%q -> key %v, want %v", seq, ek.Key(), want)
		}
	}
}

func TestDecodeHomeEndPgUpPgDnDelete(t *testing.T) {
	cases := map[string]Key{
		"\x1b[H": KeyHome, "\x1b[F": KeyEnd,
		"\x1bOH": KeyHome, "\x1bOF": KeyEnd,
		"\x1b[1~": KeyHome, "\x1b[4~": KeyEnd,
		"\x1b[5~": KeyPgUp, "\x1b[6~": KeyPgDn,
		"\x1b[3~": KeyDelete,
	}
	for seq, want := range cases {
		evs := feed(t, seq)
		if len(evs) != 1 {
			t.Errorf("%q -> %d events", seq, len(evs))
			continue
		}
		ek := evs[0].(*EventKey)
		if ek.Key() != want {
			t.Errorf("%q -> key %v, want %v", seq, ek.Key(), want)
		}
	}
}

func TestDecodeEnterTabBackspaceEsc(t *testing.T) {
	cases := map[string]Key{
		"\r": KeyEnter, "\n": KeyEnter,
		"\t":   KeyTab,
		"\x7f": KeyBackspace2, "\x08": KeyBackspace,
		"\x1b": KeyEsc,
	}
	for seq, want := range cases {
		evs := feed(t, seq)
		if len(evs) != 1 {
			t.Errorf("%q -> %d events", seq, len(evs))
			continue
		}
		ek := evs[0].(*EventKey)
		if ek.Key() != want {
			t.Errorf("%q -> key %v, want %v", seq, ek.Key(), want)
		}
	}
}

func TestDecodeCtrlCombos(t *testing.T) {
	cases := map[byte]Key{
		0x03: KeyCtrlC, // Ctrl+C
		0x04: KeyCtrlD, // Ctrl+D
		0x14: KeyCtrlT, // Ctrl+T
		0x19: KeyCtrlY, // Ctrl+Y
		0x15: KeyCtrlU, // Ctrl+U
		0x17: KeyCtrlW, // Ctrl+W
	}
	for b, want := range cases {
		evs := feed(t, string([]byte{b}))
		if len(evs) != 1 {
			t.Errorf("ctrl 0x%02x -> %d events", b, len(evs))
			continue
		}
		ek := evs[0].(*EventKey)
		if ek.Key() != want {
			t.Errorf("ctrl 0x%02x -> key %v, want %v", b, ek.Key(), want)
		}
	}
}

func TestDecodeAltEnter(t *testing.T) {
	evs := feed(t, "\x1b\r")
	if len(evs) != 1 {
		t.Fatalf("got %d events", len(evs))
	}
	ek := evs[0].(*EventKey)
	if ek.Key() != KeyEnter || ek.Modifiers()&ModAlt == 0 {
		t.Errorf("got key=%v mod=%v, want KeyEnter+ModAlt", ek.Key(), ek.Modifiers())
	}
}

func TestDecodeAltChar(t *testing.T) {
	evs := feed(t, "\x1bb")
	ek := evs[0].(*EventKey)
	if ek.Key() != KeyRune || ek.Rune() != 'b' || ek.Modifiers()&ModAlt == 0 {
		t.Errorf("got key=%v rune=%q mod=%v, want Rune/b+Alt", ek.Key(), ek.Rune(), ek.Modifiers())
	}
}

func TestPumpInputStopsOnQuit(t *testing.T) {
	ch := make(chan Event, 4)
	quit := make(chan struct{})
	go PumpInput(strings.NewReader(""), ch, quit)
	close(quit)
	// Should not block / should return. We just assert no deadlock (test passes if it returns).
}

// TestDecodeCtrlAltK verifies ESC followed by a control byte (0x0b = Ctrl-K) decodes to
// KeyCtrlK with both ModCtrl and ModAlt — the sequence terminals send for Ctrl+Alt+K, which is
// the which-key panel toggle (mirrors opencode's binding).
func TestDecodeCtrlAltK(t *testing.T) {
	evs := feed(t, "\x1b\x0b")
	ek := evs[0].(*EventKey)
	if ek.Key() != KeyCtrlK {
		t.Fatalf("key = %v, want KeyCtrlK", ek.Key())
	}
	if ek.Modifiers()&ModCtrl == 0 {
		t.Errorf("missing ModCtrl; mod=%v", ek.Modifiers())
	}
	if ek.Modifiers()&ModAlt == 0 {
		t.Errorf("missing ModAlt; mod=%v", ek.Modifiers())
	}
}

// TestDecodeShiftTab verifies Shift+Tab (CSI Z, "\x1b[Z") decodes to KeyBacktab. This is the
// regression for a nil-pointer crash: previously decodeCSI had no 'Z' case and fell through to
// `return nil`, but as a typed (*EventKey)(nil) boxed into the Event interface it was non-nil, so
// PumpInput forwarded it and handleKey's e.Key() dereferenced nil.
func TestDecodeShiftTab(t *testing.T) {
	evs := feed(t, "\x1b[Z")
	if len(evs) != 1 {
		t.Fatalf("got %d events, want 1 (KeyBacktab)", len(evs))
	}
	ek, ok := evs[0].(*EventKey)
	if !ok {
		t.Fatalf("event is %T, want *EventKey", evs[0])
	}
	if ek.Key() != KeyBacktab {
		t.Errorf("key = %v, want KeyBacktab", ek.Key())
	}
}

// TestDecodeUnknownCSIReturnsTrueNil guards the broader typed-nil pitfall: any unrecognized CSI
// final byte (here 'z' is a valid final byte in 0x40..0x7e but unhandled) must yield a true nil
// interface so PumpInput drops it, not a typed (*EventKey)(nil) that would later crash a handler.
func TestDecodeUnknownCSIReturnsTrueNil(t *testing.T) {
	br := bufio.NewReader(strings.NewReader("\x1b[0z"))
	first, err := br.ReadByte()
	if err != nil {
		t.Fatalf("read first byte: %v", err)
	}
	ev := Decode(br, first)
	if ev != nil {
		t.Errorf("Decode(unrecognized CSI) = %T(%[1]v), want nil interface (not typed-nil)", ev)
	}
}

// TestDecodeCtrlAltLetter covers a couple more Ctrl+Alt+letter combos to confirm the decode path
// generalizes beyond K (e.g. Ctrl+Alt+C = ESC 0x03, Ctrl+Alt+P = ESC 0x10).
func TestDecodeCtrlAltLetter(t *testing.T) {
	cases := []struct {
		seq  string
		want Key
		name string
	}{
		{"\x1b\x03", KeyCtrlC, "Ctrl+Alt+C"},
		{"\x1b\x10", KeyCtrlP, "Ctrl+Alt+P"},
		{"\x1b\x02", KeyCtrlB, "Ctrl+Alt+B"},
	}
	for _, tc := range cases {
		evs := feed(t, tc.seq)
		ek := evs[0].(*EventKey)
		if ek.Key() != tc.want {
			t.Errorf("%s: key = %v, want %v", tc.name, ek.Key(), tc.want)
		}
		if ek.Modifiers()&(ModCtrl|ModAlt) != ModCtrl|ModAlt {
			t.Errorf("%s: mod = %v, want ModCtrl|ModAlt", tc.name, ek.Modifiers())
		}
	}
}

// TestDecodeSGRMouseWheel verifies SGR mouse mode wheel events decode to EventMouse with the
// correct button and 0-based coordinates. Format: CSI < button ; col ; row M (press/wheel).
func TestDecodeSGRMouseWheel(t *testing.T) {
	cases := []struct {
		seq        string
		wantButton int
		wantX      int
		wantY      int
		wantPress  bool
		name       string
	}{
		{"\x1b[<64;1;1M", MouseWheelUp, 0, 0, true, "wheel up at origin"},
		{"\x1b[<65;1;1M", MouseWheelDown, 0, 0, true, "wheel down at origin"},
		{"\x1b[<64;10;5M", MouseWheelUp, 9, 4, true, "wheel up at 9,4"},
		{"\x1b[<65;80;24M", MouseWheelDown, 79, 23, true, "wheel down at 79,23"},
	}
	for _, tc := range cases {
		evs := feed(t, tc.seq)
		if len(evs) != 1 {
			t.Errorf("%s: got %d events, want 1", tc.name, len(evs))
			continue
		}
		em, ok := evs[0].(*EventMouse)
		if !ok {
			t.Errorf("%s: event is %T, want *EventMouse", tc.name, evs[0])
			continue
		}
		if em.Button() != tc.wantButton {
			t.Errorf("%s: button = %d, want %d", tc.name, em.Button(), tc.wantButton)
		}
		x, y := em.Position()
		if x != tc.wantX || y != tc.wantY {
			t.Errorf("%s: pos = (%d,%d), want (%d,%d)", tc.name, x, y, tc.wantX, tc.wantY)
		}
		if em.IsPress() != tc.wantPress {
			t.Errorf("%s: press = %v, want %v", tc.name, em.IsPress(), tc.wantPress)
		}
	}
}

// TestDecodeSGRMousePressRelease verifies button press/release distinction (M vs m final byte).
func TestDecodeSGRMousePressRelease(t *testing.T) {
	evs := feed(t, "\x1b[<0;5;3M")
	if len(evs) != 1 {
		t.Fatalf("press: got %d events", len(evs))
	}
	em := evs[0].(*EventMouse)
	if em.Button() != MouseLeft || !em.IsPress() {
		t.Errorf("press: button=%d press=%v, want 0/true", em.Button(), em.IsPress())
	}

	evs = feed(t, "\x1b[<0;5;3m")
	if len(evs) != 1 {
		t.Fatalf("release: got %d events", len(evs))
	}
	em = evs[0].(*EventMouse)
	if em.Button() != MouseLeft || em.IsPress() {
		t.Errorf("release: button=%d press=%v, want 0/false", em.Button(), em.IsPress())
	}
}

// TestDecodeSGRMouseMalformed returns true nil on bad payloads so PumpInput drops them.
func TestDecodeSGRMouseMalformed(t *testing.T) {
	cases := []string{
		"\x1b[<M",       // no params
		"\x1b[<64;5M",   // only 2 fields
		"\x1b[<64;x;5M", // non-numeric
	}
	for _, seq := range cases {
		br := bufio.NewReader(strings.NewReader(seq))
		first, err := br.ReadByte()
		if err != nil {
			t.Fatalf("%q: read: %v", seq, err)
		}
		ev := Decode(br, first)
		if ev != nil {
			t.Errorf("%q: got %T(%[1]v), want nil", seq, ev)
		}
	}
}

// TestDecodeLegacyMouseWheel verifies X10 legacy mouse mode decoding for terminals that ignore
// SGR encoding (e.g. macOS Terminal.app). The format is CSI M <button+32> <col+32> <row+32>.
func TestDecodeLegacyMouseWheel(t *testing.T) {
	cases := []struct {
		seq        string
		wantButton int
		wantX      int
		wantY      int
		name       string
	}{
		// wheel up at (1,1): 64+32=96, 1+32=33, 1+32=33
		{"\x1b[M\x60\x21\x21", MouseWheelUp, 0, 0, "wheel up at origin"},
		// wheel down at (1,1): 65+32=97, 1+32=33, 1+32=33
		{"\x1b[M\x61\x21\x21", MouseWheelDown, 0, 0, "wheel down at origin"},
		// left click at (10,5): 0+32=32, 10+32=42, 5+32=37
		{"\x1b[M\x20\x2a\x25", MouseLeft, 9, 4, "left click at 9,4"},
	}
	for _, tc := range cases {
		evs := feed(t, tc.seq)
		if len(evs) != 1 {
			t.Errorf("%s: got %d events, want 1", tc.name, len(evs))
			continue
		}
		em, ok := evs[0].(*EventMouse)
		if !ok {
			t.Errorf("%s: event is %T, want *EventMouse", tc.name, evs[0])
			continue
		}
		if em.Button() != tc.wantButton {
			t.Errorf("%s: button = %d, want %d", tc.name, em.Button(), tc.wantButton)
		}
		x, y := em.Position()
		if x != tc.wantX || y != tc.wantY {
			t.Errorf("%s: pos = (%d,%d), want (%d,%d)", tc.name, x, y, tc.wantX, tc.wantY)
		}
		if !em.IsPress() {
			t.Errorf("%s: press = false, want true", tc.name)
		}
	}
}

// TestDecodeLegacyMouseMalformed verifies malformed legacy mouse reports return true nil.
func TestDecodeLegacyMouseMalformed(t *testing.T) {
	br := bufio.NewReader(strings.NewReader("\x1b[M\x60\x21")) // only 2 bytes after M
	first, err := br.ReadByte()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	ev := Decode(br, first)
	if ev != nil {
		t.Errorf("got %T(%[1]v), want nil", ev)
	}
}
