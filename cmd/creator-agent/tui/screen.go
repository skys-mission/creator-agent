package tui

import (
	"fmt"
	"unicode/utf8"

	runewidth "github.com/mattn/go-runewidth"
)

// Cell is one screen position: a primary rune plus its style. Wide (CJK/emoji) runes occupy the
// primary cell; the following cell(s) are left as ' ' in the buffer (the renderer emits only the
// primary rune and then CUPs past the width, so the follow cell is never written as a glyph).
type Cell struct {
	main  rune
	style Style
}

// Screen is a double-buffered cell grid that writes to a terminal via absolute cursor positioning.
type Screen struct {
	out     termWriter // the terminal output (real TTY or a test buffer)
	w, h    int
	front   [][]Cell // last flushed frame
	back    [][]Cell // frame being built
	cur     Style    // last style emitted (for incremental sgr diffing)
	cursorX int      // requested cursor column (-1 = hidden)
	cursorY int      // requested cursor row (-1 = hidden)
}

// termWriter is the minimal output sink the Screen flushes to. The real Terminal implements it
// (buffered writes to /dev/tty or stdout); tests use a bytes.Buffer-backed impl.
type termWriter interface {
	Write(p []byte) (int, error)
}

// NewScreen creates a Screen of the given size backed by out. The grid is cleared to spaces with
// StyleDefault.
func NewScreen(out termWriter, w, h int) *Screen {
	s := &Screen{out: out, w: w, h: h, cursorX: -1, cursorY: -1}
	s.allocate()
	return s
}

// allocate (re)creates front/back grids and fills back with blank cells.
func (s *Screen) allocate() {
	mk := func() [][]Cell {
		g := make([][]Cell, s.h)
		for y := range g {
			g[y] = make([]Cell, s.w)
		}
		return g
	}
	s.back = mk()
	s.front = mk()
}

// Size returns the grid dimensions.
func (s *Screen) Size() (int, int) { return s.w, s.h }

// SetSize resizes the grid, discarding contents (full repaint follows).
func (s *Screen) SetSize(w, h int) {
	if w < 1 {
		w = 1
	}
	if h < 1 {
		h = 1
	}
	s.w, s.h = w, h
	s.allocate()
}

// SetContent sets the cell at (x,y) to mainc with style. Out-of-range coordinates are ignored.
// This matches tcell.Screen.SetContent's signature contract so callers migrate unchanged.
//
// For a display-width-2 rune, the follow cell (x+1) is automatically filled with a space of the
// same style. This is essential for incremental rendering: if the follow cell were left at its
// previous value, the front/back diff would not detect it as changed and a stale glyph would
// remain on screen — the cause of emoji/CJK rows appearing misaligned. Present() still skips
// emitting the follow cell (the terminal advances its cursor by 2 for a wide glyph); the fill
// only ensures the buffer/diff is correct.
func (s *Screen) SetContent(x, y int, mainc rune, _ []rune, style Style) {
	if x < 0 || y < 0 || x >= s.w || y >= s.h {
		return
	}
	s.back[y][x] = Cell{main: mainc, style: style}
	if runeW(mainc) == 2 && x+1 < s.w {
		s.back[y][x+1] = Cell{main: ' ', style: style}
	}
}

// GetContent returns the cell at (x,y) for inspection (used by tests).
func (s *Screen) GetContent(x, y int) (rune, Style) {
	if x < 0 || y < 0 || x >= s.w || y >= s.h {
		return ' ', StyleDefault
	}
	c := s.back[y][x]
	if c.main == 0 {
		return ' ', c.style
	}
	return c.main, c.style
}

// SimCell is one cell as exposed to tests (mirrors tcell.SimCell for migration parity).
type SimCell struct {
	Main  rune
	Style Style
}

// GetContents returns the whole back buffer plus its dimensions, for test inspection. This replaces
// tcell.SimulationScreen.GetContents.
func (s *Screen) GetContents() ([]SimCell, int, int) {
	out := make([]SimCell, s.w*s.h)
	for y := 0; y < s.h; y++ {
		for x := 0; x < s.w; x++ {
			c := s.back[y][x]
			mainc := c.main
			if mainc == 0 {
				mainc = ' '
			}
			out[y*s.w+x] = SimCell{Main: mainc, Style: c.style}
		}
	}
	return out, s.w, s.h
}

// GetCursor returns the requested cursor position and visibility (for test inspection). Replaces
// tcell.SimulationScreen.GetCursor.
func (s *Screen) GetCursor() (int, int, bool) {
	if s.cursorX < 0 || s.cursorY < 0 {
		return -1, -1, false
	}
	return s.cursorX, s.cursorY, true
}

// Clear fills the back buffer with spaces of the given style.
func (s *Screen) Clear(style Style) {
	for y := 0; y < s.h; y++ {
		for x := 0; x < s.w; x++ {
			s.back[y][x] = Cell{main: ' ', style: style}
		}
	}
}

// ShowCursor requests the cursor be shown at (x,y) after the next Present().
func (s *Screen) ShowCursor(x, y int) {
	if x < 0 || y < 0 || x >= s.w || y >= s.h {
		s.cursorX, s.cursorY = -1, -1
		return
	}
	s.cursorX, s.cursorY = x, y
}

// HideCursor requests the cursor be hidden after the next Present().
func (s *Screen) HideCursor() { s.cursorX, s.cursorY = -1, -1 }

// Sync flushes the full frame, forcing every cell to be re-emitted (no front/back diff). Used at
// startup and after a resize so a fresh frame is guaranteed. It is the Present variant the existing
// render() code calls once per frame.
func (s *Screen) Sync() error {
	return s.present(true)
}

// Show is an alias for Sync (incremental flush); present here for tcell API parity. The existing
// code uses Sync exclusively, so this only needs to exist for completeness.
func (s *Screen) Show() error { return s.present(false) }

// present emits the back buffer to the terminal using a run-based model: a CUP is emitted only at
// the start of each contiguous run of changed cells, and within a run bytes are written
// sequentially. After every wide (width-2) rune the run is broken and the next changed cell is
// re-positioned with an explicit CUP. This keeps output compact for ASCII while avoiding the
// per-emoji leftward drift that happens when a terminal's cursor advance disagrees with our
// runewidth model: relying on the terminal to advance by 2 after an emoji causes every following
// glyph in the run to land one cell too far left on terminals that advance by 1, whereas an
// absolute CUP snaps the next primary cell to the correct grid column regardless of how the
// terminal tracked the wide glyph.
func (s *Screen) present(force bool) error {
	var buf []byte
	if force {
		buf = append(buf, []byte(sgrReset)...)
		s.cur = StyleDefault
	}
	curStyle := s.cur
	// inRun tracks whether the previous emitted cell was part of the current sequential run (cursor
	// wide rune. Breaking after wide runes is the fix for emoji/CJK drift: it forces an absolute CUP
	// to the next primary cell instead of assuming the terminal advanced by exactly the same width
	for y := 0; y < s.h; y++ {
		inRun := false
		for x := 0; x < s.w; x++ {
			cell := s.back[y][x]
			frontCell := s.front[y][x]
			if !force && cell == frontCell {
				inRun = false
				continue
			}
			styleChanged := cell.style != curStyle
			if !inRun || styleChanged {
				buf = appendCUP(buf, x, y)
				inRun = true
			}
			if sg := sgr(curStyle, cell.style); sg != "" {
				buf = append(buf, []byte(sg)...)
				curStyle = cell.style
			}
			mainc := cell.main
			if mainc == 0 {
				mainc = ' '
			}
			var rb [utf8.UTFMax]byte
			n := utf8.EncodeRune(rb[:], mainc)
			buf = append(buf, rb[:n]...)
			// A width-2 rune consumes two grid cells; the follow cell (x+1) is skipped in the buffer
			// (SetContent auto-fills it with a space). Break the run after the wide rune so the next
			// primary cell is explicitly repositioned with a CUP, preventing drift when the terminal's
			// cursor advance for the wide glyph does not match our width model.
			if runeW(mainc) == 2 {
				x++ // skip the follow cell in the grid
				inRun = false
			}
		}
	}
	// Place the visible cursor where requested, or hide it.
	if s.cursorX >= 0 && s.cursorY >= 0 {
		buf = appendCUP(buf, s.cursorX, s.cursorY)
		buf = append(buf, []byte("\x1b[?25h")...)
	} else {
		buf = append(buf, []byte("\x1b[?25l")...)
	}
	if _, err := s.out.Write(buf); err != nil {
		return fmt.Errorf("screen write: %w", err)
	}
	for y := 0; y < s.h; y++ {
		copy(s.front[y], s.back[y])
	}
	s.cur = curStyle
	return nil
}

// appendCUP appends the CSI CUP (cursor position) sequence for 0-based (x,y) to buf and returns
// the extended slice. The on-wire form is `\x1b[y+1;x+1H` (1-based).
func appendCUP(buf []byte, x, y int) []byte {
	// Manual int formatting avoids fmt allocation in the hot path.
	buf = append(buf, '\x1b', '[')
	buf = append(buf, itoaBytes(y+1)...)
	buf = append(buf, ';')
	buf = append(buf, itoaBytes(x+1)...)
	buf = append(buf, 'H')
	return buf
}

// itoaBytes formats a small non-negative int into a freshly allocated byte slice. Used by
// appendCUP for row/column numbers (always small and positive).
func itoaBytes(n int) []byte {
	if n == 0 {
		return []byte{'0'}
	}
	var rev [12]byte
	i := 0
	for n > 0 {
		rev[i] = byte('0' + n%10)
		n /= 10
		i++
	}
	out := make([]byte, i)
	for j := 0; j < i; j++ {
		out[j] = rev[i-1-j]
	}
	return out
}

var widthCond = runewidth.DefaultCondition

// Rect is an absolute screen rectangle. X,Y is the top-left; W,H are the size (>= 0).
type Rect struct {
	X, Y, W, H int
}

// Surface is a clipped, colored view onto a *Screen. All drawing is expressed in the
// surface's LOCAL coordinates (col,row from 0,0 at the rect's top-left); the surface translates
// to absolute screen coordinates and drops anything outside its rect.
type Surface struct {
	screen *Screen
	rect   Rect
	bg     Color // region background, applied under styles that have no explicit bg
}

func newSurface(screen *Screen) *Surface {
	w, h := screen.Size()
	return &Surface{screen: screen, rect: Rect{0, 0, w, h}, bg: ColorDefault}
}

func newSurfaceSize(screen *Screen, w, h int) *Surface {
	return &Surface{screen: screen, rect: Rect{0, 0, w, h}, bg: ColorDefault}
}

// Sub returns a child surface for r (expressed in LOCAL coordinates of s), clamped to s's rect.
// The child inherits s.bg. A sub-rect with non-positive W or H yields an empty (no-op) surface.
func (s *Surface) Sub(r Rect) *Surface {
	absX := s.rect.X + r.X
	absY := s.rect.Y + r.Y
	x0 := maxi(s.rect.X, absX)
	y0 := maxi(s.rect.Y, absY)
	x1 := mini(s.rect.X+s.rect.W, absX+r.W)
	y1 := mini(s.rect.Y+s.rect.H, absY+r.H)
	w := x1 - x0
	h := y1 - y0
	if w < 0 {
		w = 0
	}
	if h < 0 {
		h = 0
	}
	return &Surface{screen: s.screen, rect: Rect{x0, y0, w, h}, bg: s.bg}
}

// WithBg returns a copy of s whose region background is bg. Used when entering a differently
// colored region (main area vs todo panel).
func (s *Surface) WithBg(bg Color) *Surface {
	cp := *s
	cp.bg = bg
	return &cp
}

// Rect returns the surface's absolute rectangle (size/position query).
func (s *Surface) Rect() Rect { return s.rect }

// Width / Height return the surface's content size.
func (s *Surface) Width() int  { return s.rect.W }
func (s *Surface) Height() int { return s.rect.H }

// contains reports whether absolute column ax is within s's horizontal range with room for a rune
// of display width w (i.e. ax and ax+w-1 are both inside [rect.X, rect.X+rect.W)).
func (s *Surface) fits(ax, w int) bool {
	if w <= 0 {
		w = 1
	}
	return ax >= s.rect.X && ax+w <= s.rect.X+s.rect.W
}

func (s *Surface) inRow(ay int) bool {
	return ay >= s.rect.Y && ay < s.rect.Y+s.rect.H
}

// put writes a single rune (display width w) at local (col,row) with style, after compositing the
// region bg. Wide runes (w==2) are only written when both cells fit; otherwise nothing is written.
// Out-of-range coordinates are dropped silently.
func (s *Surface) put(col, row int, ch rune, w int, st Style) {
	ax := s.rect.X + col
	ay := s.rect.Y + row
	if !s.inRow(ay) {
		return
	}
	if !s.fits(ax, w) {
		return
	}
	s.screen.SetContent(ax, ay, ch, nil, compositeBg(s, st))
}

// SetCell writes a single rune at local (col,row), deriving its width from runewidth. Combining /
// zero-width runes are written onto the cell at (col-1) if present (i.e. attached to the previous
// glyph); here we simply place them at col without advancing, which callers handle by not
// advancing on zero width.
func (s *Surface) SetCell(col, row int, ch rune, st Style) {
	w := runeW(ch)
	if w < 0 {
		w = 0
	}
	s.put(col, row, ch, w, st)
}

// FillCell writes a rune of the given display width, padding it to fill its cells. Used for
// borders/repeat glyphs. If a wide rune would overflow the right edge it is replaced by spaces.
func (s *Surface) FillCell(col, row int, ch rune, st Style) {
	w := runeW(ch)
	if w <= 0 {
		w = 1
	}
	ax := s.rect.X + col
	ay := s.rect.Y + row
	if !s.inRow(ay) {
		return
	}
	if !s.fits(ax, w) {
		rem := s.rect.X + s.rect.W - ax
		for i := 0; i < rem; i++ {
			if ax+i >= s.rect.X+s.rect.W {
				break
			}
			s.screen.SetContent(ax+i, ay, ' ', nil, compositeBg(s, st))
		}
		return
	}
	s.screen.SetContent(ax, ay, ch, nil, compositeBg(s, st))
}

// --- line / rect helpers ---

// ClearRow fills a whole local row with spaces on the region background.
func (s *Surface) ClearRow(row int) {
	s.FillRectLocal(Rect{0, row, s.rect.W, 1}, ' ', StyleDefault)
}

// FillRectLocal fills a local rectangle (r in LOCAL coords) with ch on bg. Out-of-range parts are
// dropped. Uses compositeBg so explicit-bg styles keep their own bg.
func (s *Surface) FillRectLocal(r Rect, ch rune, st Style) {
	styled := compositeBg(s, st)
	for row := r.Y; row < r.Y+r.H; row++ {
		ay := s.rect.Y + row
		if !s.inRow(ay) {
			continue
		}
		for col := r.X; col < r.X+r.W; col++ {
			ax := s.rect.X + col
			if ax < s.rect.X || ax >= s.rect.X+s.rect.W {
				continue
			}
			s.screen.SetContent(ax, ay, ch, nil, styled)
		}
	}
}

// FillFrame fills the entire surface rectangle with ch on the given bg (ignores s.bg).
func (s *Surface) FillFrame(ch rune, bg Color) {
	st := StyleDefault.Background(bg)
	for row := 0; row < s.rect.H; row++ {
		ay := s.rect.Y + row
		for col := 0; col < s.rect.W; col++ {
			s.screen.SetContent(s.rect.X+col, ay, ch, nil, st)
		}
	}
}

// DrawText writes styled runs onto a single local row, starting at colStart. Wide runes that do not
// fit before the right edge stop the write (the remaining cells keep whatever was there; callers
// typically ClearRow first). Zero-width runes (combining marks) are skipped: they should be rendered as combining marks on
// the combining slice of a primary rune, but our content stream rarely contains them, and writing
// a zero-width rune as a cell's primary would corrupt that cell. Returns columns consumed.
func (s *Surface) DrawText(colStart, row int, runs []styledRun) int {
	cx := colStart
	for _, run := range runs {
		st := run.style
		for _, ch := range run.text {
			w := runeW(ch)
			if w <= 0 {
				continue // skip combining/zero-width runes
			}
			if !s.fits(s.rect.X+cx, w) {
				return cx - colStart
			}
			ax := s.rect.X + cx
			ay := s.rect.Y + row
			if s.inRow(ay) {
				s.screen.SetContent(ax, ay, ch, nil, compositeBg(s, st))
			}
			cx += w
		}
	}
	return cx - colStart
}

// DrawPlain writes a plain string on a single local row starting at colStart with one style.
// Wide-rune-safe, same clipping as DrawText. Zero-width runes are skipped (see DrawText). Returns
// columns consumed.
func (s *Surface) DrawPlain(colStart, row int, text string, st Style) int {
	cx := colStart
	for _, ch := range text {
		w := runeW(ch)
		if w <= 0 {
			continue
		}
		if !s.fits(s.rect.X+cx, w) {
			return cx - colStart
		}
		ax := s.rect.X + cx
		ay := s.rect.Y + row
		if s.inRow(ay) {
			s.screen.SetContent(ax, ay, ch, nil, compositeBg(s, st))
		}
		cx += w
	}
	return cx - colStart
}

// RepeatPlain writes the rune ch repeated n times on a single local row starting at colStart.
// Wide runes that would overflow are replaced by spaces for the remainder. Used for borders (─).
func (s *Surface) RepeatPlain(colStart, row int, ch rune, n int, st Style) {
	w := runeW(ch)
	if w <= 0 {
		w = 1
	}
	cx := colStart
	for i := 0; i < n; i++ {
		if s.fits(s.rect.X+cx, w) {
			ax := s.rect.X + cx
			ay := s.rect.Y + row
			if s.inRow(ay) {
				s.screen.SetContent(ax, ay, ch, nil, compositeBg(s, st))
			}
			cx += w
		} else {
			// Overflow: pad the rest of the row with spaces.
			ax := s.rect.X + cx
			ay := s.rect.Y + row
			if s.inRow(ay) {
				for ax < s.rect.X+s.rect.W {
					s.screen.SetContent(ax, ay, ' ', nil, compositeBg(s, st))
					ax++
				}
			}
			return
		}
	}
}

// ShowCursor places the terminal cursor at local (col,row) if inside the surface; else hides it.
func (s *Surface) ShowCursor(col, row int) {
	ax := s.rect.X + col
	ay := s.rect.Y + row
	if ax >= s.rect.X && ax < s.rect.X+s.rect.W && ay >= s.rect.Y && ay < s.rect.Y+s.rect.H {
		s.screen.ShowCursor(ax, ay)
	} else {
		s.screen.HideCursor()
	}
}

// HideCursor hides the terminal cursor.
func (s *Surface) HideCursor() { s.screen.HideCursor() }

func compositeBg(s *Surface, st Style) Style {
	_, bg, _ := st.Decompose()
	if bg != ColorDefault {
		return st
	}
	return st.Background(s.bg)
}

func styleBg(st Style) Color {
	_, bg, _ := st.Decompose()
	return bg
}

// --- runewidth wrappers (single source of width truth) ---

// runeW returns the display width of a rune (0 for combining/zero-width, 1 for narrow, 2 for
// CJK/fullwidth/emoji). Uses the package-level widthCond so rendering and cursor math share
// one algorithm and one EastAsianWidth setting.
func runeW(r rune) int {
	return widthCond.RuneWidth(r)
}

// strW returns the display width of a string (sum of runeW).
func strW(s string) int {
	return widthCond.StringWidth(s)
}
