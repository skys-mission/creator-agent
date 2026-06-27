package tui

import (
	"fmt"
	"strings"
)

// Color is an RGB color or the sentinel ColorDefault (transparent / use terminal default).
type Color uint32

const (
	colorRGBBit = 0x80000000 // top bit marks a non-default Color carrying RGB in the low 24 bits

	// ColorDefault is the "not set" sentinel: foreground/background marked default lets the
	// terminal use its own color. compositeBg/withRegionBg rely on bg == ColorDefault to decide
	// whether to apply the region background.
	ColorDefault Color = 0
	// ColorWhite / ColorBlack are the only named colors used in production code.
	// They carry colorRGBBit so they are distinguishable from ColorDefault (0).
	ColorWhite Color = colorRGBBit | 0xffffff
	ColorBlack Color = colorRGBBit | 0x000000
)

// NewRGBColor packs an (r,g,b) triple into a Color. The top bit is set so the value is
// distinguishable from ColorDefault (0) and from any raw RGB that happens to be 0 (pure black,
// which is instead represented as ColorBlack with the top bit set).
func NewRGBColor(r, g, b int32) Color {
	return Color(colorRGBBit) | Color(uint32(r&0xff)<<16|uint32(g&0xff)<<8|uint32(b&0xff))
}

// IsDefault reports whether c is the unset sentinel.
func (c Color) IsDefault() bool { return c == ColorDefault }

// RGB unpacks c into (r,g,b) and reports whether it carries an explicit RGB value.
func (c Color) RGB() (r, g, b int, ok bool) {
	if c&colorRGBBit == 0 {
		return 0, 0, 0, false
	}
	v := uint32(c) & 0xffffff
	return int((v >> 16) & 0xff), int((v >> 8) & 0xff), int(v & 0xff), true
}

const (
	attrBold   = 1 << iota // 1
	attrItalic             // 2
)

// Style is a foreground color, background color, and a small set of attributes. The zero value is
// StyleDefault (everything unset) — the "no style" sentinel that compositeBg/withRegionBg key on.
type Style struct {
	fg, bg Color
	bold   bool
	italic bool
}

// StyleDefault is the zero Style (default fg/bg, no attributes). It is the sentinel for "no
// explicit style" throughout the package.
var StyleDefault = Style{}

// Foreground returns a copy of st with the foreground set to c.
func (st Style) Foreground(c Color) Style { st.fg = c; return st }

// Background returns a copy of st with the background set to c.
func (st Style) Background(c Color) Style { st.bg = c; return st }

// Bold returns a copy of st with the bold attribute set to on.
func (st Style) Bold(on bool) Style { st.bold = on; return st }

// Italic returns a copy of st with the italic attribute set to on.
func (st Style) Italic(on bool) Style { st.italic = on; return st }

// Decompose returns the foreground, background, and attribute mask of st. The signature mirrors
// tcell.Style.Decompose so callers (compositeBg, withRegionBg, tests) work unchanged: the third
// value encodes bold/italic but is currently ignored by all readers.
func (st Style) Decompose() (fg, bg Color, attr int) {
	attr = 0
	if st.bold {
		attr |= attrBold
	}
	if st.italic {
		attr |= attrItalic
	}
	return st.fg, st.bg, attr
}

// sgr builds the SGR (Select Graphic Rendition) escape sequence that switches the terminal from
// prev to st. Returns "" if no attribute/color change is needed. Colors use 24-bit direct
// (SGR 38;2;r;g;b / 48;2;r;g;b); default fg/bg are reset with SGR 39 / 49.
//
// Only the differences between prev and st are emitted, to minimize bytes per cell. The terminal
// maintains its own current-attribute state across cells (we never emit a full reset+rebuild each
// cell because that bloats output and can flicker).
func sgr(prev, st Style) string {
	changed := false
	var parts []string

	if prev.bold != st.bold {
		if st.bold {
			parts = append(parts, "1")
		} else {
			parts = append(parts, "22")
		}
		changed = true
	}
	if prev.italic != st.italic {
		if st.italic {
			parts = append(parts, "3")
		} else {
			parts = append(parts, "23")
		}
		changed = true
	}
	if prev.fg != st.fg {
		parts = append(parts, colorSGR(st.fg, true))
		changed = true
	}
	if prev.bg != st.bg {
		parts = append(parts, colorSGR(st.bg, false))
		changed = true
	}
	if !changed {
		return ""
	}
	return "\x1b[" + strings.Join(parts, ";") + "m"
}

func colorSGR(c Color, fg bool) string {
	if c.IsDefault() {
		if fg {
			return "39"
		}
		return "49"
	}
	r, g, b, _ := c.RGB()
	prefix := "38"
	if !fg {
		prefix = "48"
	}
	return fmt.Sprintf("%s;2;%d;%d;%d", prefix, r, g, b)
}

const sgrReset = "\x1b[0m"
