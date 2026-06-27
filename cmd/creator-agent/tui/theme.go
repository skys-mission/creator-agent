package tui

import "sync/atomic"

type palette struct {
	name string
	mode string // "dark" | "light"

	primary   Color
	secondary Color
	accent    Color
	errCol    Color
	warning   Color
	success   Color
	info      Color
	text      Color
	textMuted Color

	background        Color
	backgroundPanel   Color
	backgroundElement Color
	border            Color
	borderActive      Color
	borderSubtle      Color

	// promptBorder is the Claude-layout input-box top-line color (and the ❯ prompt). Falls back to
	// borderActive when unset (legacy palettes built before this field was added).
	promptBorder Color

	diffAdded               Color
	diffRemoved             Color
	diffContext             Color
	diffHunkHeader          Color
	diffHighlightAdded      Color
	diffHighlightRemoved    Color
	diffAddedBg             Color
	diffRemovedBg           Color
	diffContextBg           Color
	diffLineNumber          Color
	diffAddedLineNumberBg   Color
	diffRemovedLineNumberBg Color

	markdownText            Color
	markdownHeading         Color
	markdownLink            Color
	markdownLinkText        Color
	markdownCode            Color
	markdownBlockQuote      Color
	markdownEmph            Color
	markdownStrong          Color
	markdownHorizontalRule  Color
	markdownListItem        Color
	markdownListEnumeration Color
	markdownImage           Color
	markdownImageText       Color
	markdownCodeBlock       Color

	syntaxComment     Color
	syntaxKeyword     Color
	syntaxFunction    Color
	syntaxVariable    Color
	syntaxString      Color
	syntaxNumber      Color
	syntaxType        Color
	syntaxOperator    Color
	syntaxPunctuation Color

	// legacy surface aliases used directly across render.go. mainBg == background, panelBg ==
	mainBg  Color
	panelBg Color
	frameBg Color
}

// palettePtr holds the active palette. Reads via Load() are race-free; applyTheme swaps in a new
// pointer. The palette carries its own name so callers read the theme name from the same atomic
// pointer instead of a separate unsynchronized variable.
var palettePtr atomic.Pointer[palette]

func init() {
	palettePtr.Store(opencodePalette("dark"))
}

func hexColor(s string) Color {
	if len(s) != 7 || s[0] != '#' {
		return ColorDefault
	}
	return NewRGBColor(hexVal(s[1:3]), hexVal(s[3:5]), hexVal(s[5:7]))
}

func hexVal(s string) int32 { return int32(hexDigit(s[0]))*16 + int32(hexDigit(s[1])) }

func hexDigit(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	}
	return 0
}

func opencodePalette(mode string) *palette {
	switch mode {
	case "light":
		return opencodeLightPalette()
	default:
		return opencodeDarkPalette()
	}
}

func opencodeDarkPalette() *palette {
	p := &palette{name: "dark", mode: "dark"}
	p.primary = hexColor("#fab283")
	p.secondary = hexColor("#5c9cf5")
	p.accent = hexColor("#9d7cd8")
	p.errCol = hexColor("#e06c75")
	p.warning = hexColor("#f5a742")
	p.success = hexColor("#7fd88f")
	p.info = hexColor("#56b6c2")
	p.text = hexColor("#eeeeee")
	p.textMuted = hexColor("#808080")
	p.background = hexColor("#0a0a0a")
	p.backgroundPanel = hexColor("#141414")
	p.backgroundElement = hexColor("#1e1e1e")
	p.border = hexColor("#484848")
	p.borderActive = hexColor("#606060")
	p.borderSubtle = hexColor("#3c3c3c")
	p.promptBorder = p.primary
	p.diffAdded = hexColor("#4fd6be")
	p.diffRemoved = hexColor("#c53b53")
	p.diffContext = hexColor("#828bb8")
	p.diffHunkHeader = hexColor("#828bb8")
	p.diffHighlightAdded = hexColor("#b8db87")
	p.diffHighlightRemoved = hexColor("#e26a75")
	p.diffAddedBg = hexColor("#20303b")
	p.diffRemovedBg = hexColor("#37222c")
	p.diffContextBg = hexColor("#141414")
	p.diffLineNumber = hexColor("#8f8f8f")
	p.diffAddedLineNumberBg = hexColor("#1b2b34")
	p.diffRemovedLineNumberBg = hexColor("#2d1f26")
	p.markdownText = hexColor("#eeeeee")
	p.markdownHeading = p.accent
	p.markdownLink = p.primary
	p.markdownLinkText = p.info
	p.markdownCode = p.success
	p.markdownBlockQuote = hexColor("#e5c07b")
	p.markdownEmph = hexColor("#e5c07b")
	p.markdownStrong = p.warning
	p.markdownHorizontalRule = p.textMuted
	p.markdownListItem = p.primary
	p.markdownListEnumeration = p.info
	p.markdownImage = p.primary
	p.markdownImageText = p.info
	p.markdownCodeBlock = p.text
	p.syntaxComment = p.textMuted
	p.syntaxKeyword = p.accent
	p.syntaxFunction = p.primary
	p.syntaxVariable = p.errCol
	p.syntaxString = p.success
	p.syntaxNumber = p.warning
	p.syntaxType = hexColor("#e5c07b")
	p.syntaxOperator = p.info
	p.syntaxPunctuation = p.text
	// legacy surface aliases
	p.mainBg = p.background
	p.panelBg = p.backgroundPanel
	p.frameBg = p.background
	return p
}

func opencodeLightPalette() *palette {
	p := &palette{name: "light", mode: "light"}
	p.primary = hexColor("#3b7dd8")
	p.secondary = hexColor("#7b5bb6")
	p.accent = hexColor("#d68c27")
	p.errCol = hexColor("#d1383d")
	p.warning = hexColor("#d68c27")
	p.success = hexColor("#3d9a57")
	p.info = hexColor("#318795")
	p.text = hexColor("#1a1a1a")
	p.textMuted = hexColor("#8a8a8a")
	p.background = hexColor("#ffffff")
	p.backgroundPanel = hexColor("#fafafa")
	p.backgroundElement = hexColor("#f5f5f5")
	p.border = hexColor("#b8b8b8")
	p.borderActive = hexColor("#a0a0a0")
	p.borderSubtle = hexColor("#d4d4d4")
	p.promptBorder = p.primary
	p.diffAdded = hexColor("#1e725c")
	p.diffRemoved = hexColor("#c53b53")
	p.diffContext = hexColor("#7086b5")
	p.diffHunkHeader = hexColor("#7086b5")
	p.diffHighlightAdded = hexColor("#4db380")
	p.diffHighlightRemoved = hexColor("#f52a65")
	p.diffAddedBg = hexColor("#d5e5d5")
	p.diffRemovedBg = hexColor("#f7d8db")
	p.diffContextBg = hexColor("#fafafa")
	p.diffLineNumber = hexColor("#595959")
	p.diffAddedLineNumberBg = hexColor("#c5d5c5")
	p.diffRemovedLineNumberBg = hexColor("#e7c8cb")
	p.markdownText = p.text
	p.markdownHeading = p.accent
	p.markdownLink = p.primary
	p.markdownLinkText = p.info
	p.markdownCode = p.success
	p.markdownBlockQuote = hexColor("#b0851f")
	p.markdownEmph = hexColor("#b0851f")
	p.markdownStrong = p.warning
	p.markdownHorizontalRule = p.textMuted
	p.markdownListItem = p.primary
	p.markdownListEnumeration = p.info
	p.markdownImage = p.primary
	p.markdownImageText = p.info
	p.markdownCodeBlock = p.text
	p.syntaxComment = p.textMuted
	p.syntaxKeyword = p.accent
	p.syntaxFunction = p.primary
	p.syntaxVariable = p.errCol
	p.syntaxString = p.success
	p.syntaxNumber = p.warning
	p.syntaxType = hexColor("#b0851f")
	p.syntaxOperator = p.info
	p.syntaxPunctuation = p.text
	// legacy surface aliases
	p.mainBg = p.background
	p.panelBg = p.backgroundPanel
	p.frameBg = p.background
	return p
}

func catppuccinDarkPalette() *palette {
	p := &palette{name: "catppuccin", mode: "dark"}
	p.primary = hexColor("#89b4fa")
	p.secondary = hexColor("#cba6f7")
	p.accent = hexColor("#f5c2e7")
	p.errCol = hexColor("#f38ba8")
	p.warning = hexColor("#f9e2af")
	p.success = hexColor("#a6e3a1")
	p.info = hexColor("#94e2d5")
	p.text = hexColor("#cdd6f4")
	p.textMuted = hexColor("#9399b2")
	p.background = hexColor("#1e1e2e")
	p.backgroundPanel = hexColor("#181825")
	p.backgroundElement = hexColor("#11111b")
	p.border = hexColor("#313244")
	p.borderActive = hexColor("#45475a")
	p.borderSubtle = hexColor("#585b70")
	p.promptBorder = p.primary
	p.diffAdded = hexColor("#a6e3a1")
	p.diffRemoved = hexColor("#f38ba8")
	p.diffContext = hexColor("#7f849c")
	p.diffHunkHeader = hexColor("#7f849c")
	p.diffHighlightAdded = hexColor("#b8db87")
	p.diffHighlightRemoved = hexColor("#e26a75")
	p.diffAddedBg = hexColor("#24312b")
	p.diffRemovedBg = hexColor("#3c2a32")
	p.diffContextBg = p.backgroundPanel
	p.diffLineNumber = hexColor("#6c7086")
	p.diffAddedLineNumberBg = hexColor("#24312b")
	p.diffRemovedLineNumberBg = hexColor("#3c2a32")
	p.markdownText = p.text
	p.markdownHeading = p.secondary
	p.markdownLink = p.primary
	p.markdownLinkText = p.info
	p.markdownCode = p.success
	p.markdownBlockQuote = hexColor("#f9e2af")
	p.markdownEmph = hexColor("#f9e2af")
	p.markdownStrong = hexColor("#fab387")
	p.markdownHorizontalRule = p.textMuted
	p.markdownListItem = p.primary
	p.markdownListEnumeration = p.info
	p.markdownImage = p.primary
	p.markdownImageText = p.info
	p.markdownCodeBlock = p.text
	p.syntaxComment = p.textMuted
	p.syntaxKeyword = p.secondary
	p.syntaxFunction = p.primary
	p.syntaxVariable = p.errCol
	p.syntaxString = p.success
	p.syntaxNumber = hexColor("#fab387")
	p.syntaxType = hexColor("#f9e2af")
	p.syntaxOperator = p.info
	p.syntaxPunctuation = p.text
	p.mainBg = p.background
	p.panelBg = p.backgroundPanel
	p.frameBg = p.background
	return p
}

func catppuccinLightPalette() *palette {
	p := &palette{name: "catppuccin", mode: "light"}
	p.primary = hexColor("#1e66f5")
	p.secondary = hexColor("#8839ef")
	p.accent = hexColor("#ea76cb")
	p.errCol = hexColor("#d20f39")
	p.warning = hexColor("#df8e1d")
	p.success = hexColor("#40a02b")
	p.info = hexColor("#179299")
	p.text = hexColor("#4c4f69")
	p.textMuted = hexColor("#7c7f93")
	p.background = hexColor("#eff1f5")
	p.backgroundPanel = hexColor("#e6e9ef")
	p.backgroundElement = hexColor("#dce0e8")
	p.border = hexColor("#bcc0cc")
	p.borderActive = hexColor("#acb0be")
	p.borderSubtle = hexColor("#ccd0da")
	p.promptBorder = p.primary
	p.diffAdded = hexColor("#40a02b")
	p.diffRemoved = hexColor("#d20f39")
	p.diffContext = hexColor("#6c6f85")
	p.diffHunkHeader = hexColor("#6c6f85")
	p.diffHighlightAdded = hexColor("#4db380")
	p.diffHighlightRemoved = hexColor("#f52a65")
	p.diffAddedBg = hexColor("#d6f0d9")
	p.diffRemovedBg = hexColor("#f6dfe2")
	p.diffContextBg = p.backgroundPanel
	p.diffLineNumber = hexColor("#8c8fa1")
	p.diffAddedLineNumberBg = hexColor("#d6f0d9")
	p.diffRemovedLineNumberBg = hexColor("#f6dfe2")
	p.markdownText = p.text
	p.markdownHeading = p.secondary
	p.markdownLink = p.primary
	p.markdownLinkText = p.info
	p.markdownCode = p.success
	p.markdownBlockQuote = p.warning
	p.markdownEmph = p.warning
	p.markdownStrong = p.warning
	p.markdownHorizontalRule = p.textMuted
	p.markdownListItem = p.primary
	p.markdownListEnumeration = p.info
	p.markdownImage = p.primary
	p.markdownImageText = p.info
	p.markdownCodeBlock = p.text
	p.syntaxComment = p.textMuted
	p.syntaxKeyword = p.secondary
	p.syntaxFunction = p.primary
	p.syntaxVariable = p.errCol
	p.syntaxString = p.success
	p.syntaxNumber = p.warning
	p.syntaxType = p.warning
	p.syntaxOperator = p.info
	p.syntaxPunctuation = p.text
	p.mainBg = p.background
	p.panelBg = p.backgroundPanel
	p.frameBg = p.background
	return p
}

func catppuccinPalette(mode string) *palette {
	if mode == "light" {
		return catppuccinLightPalette()
	}
	return catppuccinDarkPalette()
}

// darkPalette / lightPalette are opencode dark/light aliases for back-compat with the legacy
// "dark"/"light" theme names used across config and tests.
func darkPalette() *palette  { return opencodeDarkPalette() }
func lightPalette() *palette { return opencodeLightPalette() }

func paletteForName(name string) *palette {
	switch normalizeThemeName(name) {
	case "light":
		return opencodeLightPalette()
	case "catppuccin":
		return catppuccinDarkPalette()
	case "catppuccin-light":
		return catppuccinLightPalette()
	default:
		return opencodeDarkPalette()
	}
}

func applyTheme(name string) string {
	normalized := normalizeThemeName(name)
	palettePtr.Store(paletteForName(normalized))
	return normalized
}

// currentTheme returns the normalized name of the currently applied theme, read from the atomic
// palette pointer (race-free).
func currentTheme() string { return palettePtr.Load().name }

func normalizeThemeName(name string) string {
	switch name {
	case "dark", "light", "catppuccin":
		return name
	}
	switch normalizeLower(name) {
	case "light":
		return "light"
	case "catppuccin":
		return "catppuccin"
	case "catppuccin-light", "catppuccin_latte", "latte":
		return "catppuccin-light"
	default:
		return "dark"
	}
}

// isSupportedTheme reports whether name names a real built-in theme (case/whitespace-insensitive).
// It must NOT route through normalizeThemeName, because that function maps unknown input to "dark"
// (its fallback), which would make every string look supported.
func isSupportedTheme(name string) bool {
	switch normalizeLower(name) {
	case "dark", "light", "catppuccin", "catppuccin-light":
		return true
	}
	return false
}

// normalizeLower trims surrounding whitespace and lowercases the string (ASCII). Avoids pulling
// strings.ToLower into the hot path for the very common "dark"/"light" exact match above.
func normalizeLower(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == ' ' || c == '\t' {
			continue
		}
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		out = append(out, c)
	}
	return string(out)
}

func cycleTheme() string {
	switch currentTheme() {
	case "dark":
		return applyTheme("light")
	case "light":
		return applyTheme("catppuccin")
	case "catppuccin":
		return applyTheme("dark")
	}
	return applyTheme("dark")
}

func pal() *palette { return palettePtr.Load() }

// ApplyTheme is the exported entry point for setting the active theme by name. Intended for startup
// wiring (main.go reads config.Appearance). Returns the normalized name applied.
func ApplyTheme(name string) string { return applyTheme(name) }

// CurrentTheme is the exported entry point for reading the active theme name.
func CurrentTheme() string { return currentTheme() }

// CycleTheme is the exported entry point that rotates dark/light/catppuccin and returns the new name.
func CycleTheme() string { return cycleTheme() }

// SupportedThemes returns the names of the built-in themes, in display order.
func SupportedThemes() []string { return []string{"dark", "light", "catppuccin"} }

// ---------------------------------------------------------------------------
// Style constructors. Each reads the active palette via pal() so a theme switch is reflected live.
// Two families:
//   - opencode-semantic constructors (stylePrimary, styleBackground, styleBorder, ...) back the
//     opencode-aligned layout. These are the preferred API for new render code.
//   - legacy constructors (styleHeader, styleUser, styleAssistant, ...) are kept for call sites
//     not yet migrated; they map onto the new semantic colors.
// ---------------------------------------------------------------------------

func stylePrimary() Style   { return StyleDefault.Foreground(pal().primary) }
func styleSecondary() Style { return StyleDefault.Foreground(pal().secondary) }
func styleAccent() Style    { return StyleDefault.Foreground(pal().accent).Bold(true) }
func styleError() Style     { return StyleDefault.Foreground(pal().errCol).Bold(true) }
func styleWarning() Style   { return StyleDefault.Foreground(pal().warning) }
func styleSuccess() Style   { return StyleDefault.Foreground(pal().success) }
func styleInfo() Style      { return StyleDefault.Foreground(pal().info) }
func styleText() Style      { return StyleDefault.Foreground(pal().text) }
func styleTextMuted() Style { return StyleDefault.Foreground(pal().textMuted) }

func styleBackground() Style        { return StyleDefault.Background(pal().background) }
func styleBackgroundPanel() Style   { return StyleDefault.Background(pal().backgroundPanel) }
func styleBackgroundElement() Style { return StyleDefault.Background(pal().backgroundElement) }
func styleBorder() Style            { return StyleDefault.Foreground(pal().border) }
func styleBorderActive() Style      { return StyleDefault.Foreground(pal().borderActive) }
func styleBorderSubtle() Style      { return StyleDefault.Foreground(pal().borderSubtle) }

func styleDiffAdded() Style   { return StyleDefault.Foreground(pal().diffAdded) }
func styleDiffRemoved() Style { return StyleDefault.Foreground(pal().diffRemoved) }
func styleDiffContext() Style { return StyleDefault.Foreground(pal().diffContext) }
func styleDiffHunk() Style    { return StyleDefault.Foreground(pal().diffHunkHeader) }
func styleDiffFile() Style    { return StyleDefault.Foreground(pal().primary).Bold(true) }

func styleMarkdownText() Style       { return StyleDefault.Foreground(pal().markdownText) }
func styleMarkdownHeading() Style    { return StyleDefault.Foreground(pal().markdownHeading).Bold(true) }
func styleMarkdownLink() Style       { return StyleDefault.Foreground(pal().markdownLink) }
func styleMarkdownLinkText() Style   { return StyleDefault.Foreground(pal().markdownLinkText) }
func styleMarkdownCode() Style       { return StyleDefault.Foreground(pal().markdownCode) }
func styleMarkdownBlockQuote() Style { return StyleDefault.Foreground(pal().markdownBlockQuote) }
func styleMarkdownEmph() Style       { return StyleDefault.Foreground(pal().markdownEmph) }
func styleMarkdownStrong() Style     { return StyleDefault.Foreground(pal().markdownStrong).Bold(true) }
func styleMarkdownListItem() Style   { return StyleDefault.Foreground(pal().markdownListItem) }
func styleMarkdownListEnum() Style   { return StyleDefault.Foreground(pal().markdownListEnumeration) }
func styleMarkdownCodeBlock() Style  { return StyleDefault.Foreground(pal().markdownCodeBlock) }

func styleSyntaxComment() Style     { return StyleDefault.Foreground(pal().syntaxComment) }
func styleSyntaxKeyword() Style     { return StyleDefault.Foreground(pal().syntaxKeyword) }
func styleSyntaxFunction() Style    { return StyleDefault.Foreground(pal().syntaxFunction) }
func styleSyntaxVariable() Style    { return StyleDefault.Foreground(pal().syntaxVariable) }
func styleSyntaxString() Style      { return StyleDefault.Foreground(pal().syntaxString) }
func styleSyntaxNumber() Style      { return StyleDefault.Foreground(pal().syntaxNumber) }
func styleSyntaxType() Style        { return StyleDefault.Foreground(pal().syntaxType) }
func styleSyntaxOperator() Style    { return StyleDefault.Foreground(pal().syntaxOperator) }
func styleSyntaxPunctuation() Style { return StyleDefault.Foreground(pal().syntaxPunctuation) }

// styleSyntaxCodeBlock is the default text color for code-block content (the "plain" code text
// that is not a recognized token). Mirrors opencode's markdownCodeBlock key.
func styleSyntaxCodeBlock() Style { return StyleDefault.Foreground(pal().markdownCodeBlock) }

// --- legacy constructors (map onto the new semantic colors) ---

func styleHeader() Style         { return StyleDefault.Foreground(pal().text).Bold(true) }
func styleHeaderDim() Style      { return StyleDefault.Foreground(pal().textMuted) }
func styleUser() Style           { return StyleDefault.Foreground(pal().text) }
func styleAssistant() Style      { return StyleDefault.Foreground(pal().text) }
func styleAssistantLabel() Style { return StyleDefault.Foreground(pal().accent).Bold(true) }
func styleSystem() Style         { return StyleDefault.Foreground(pal().textMuted) }
func styleThinking() Style       { return StyleDefault.Foreground(pal().textMuted) }
func styleToolName() Style       { return StyleDefault.Foreground(pal().primary).Bold(true) }
func styleToolDim() Style        { return StyleDefault.Foreground(pal().textMuted) }
func styleToolOK() Style         { return StyleDefault.Foreground(pal().success) }
func styleToolErr() Style        { return StyleDefault.Foreground(pal().errCol) }
func styleApprove() Style        { return StyleDefault.Foreground(pal().success).Bold(true) }
func styleReject() Style         { return StyleDefault.Foreground(pal().errCol).Bold(true) }
func styleDiffAdd() Style        { return StyleDefault.Foreground(pal().diffAdded) }
func styleDiffDel() Style        { return StyleDefault.Foreground(pal().diffRemoved) }
func styleErrorBar() Style {
	return StyleDefault.Foreground(ColorWhite).Background(pal().errCol)
}
func styleStatusBar() Style { return StyleDefault.Foreground(pal().textMuted) }
func styleCompSelected() Style {
	return StyleDefault.Foreground(pal().text).Background(pal().backgroundElement).Bold(true)
}
func styleCompItem() Style { return StyleDefault.Foreground(pal().textMuted) }

func stylePaletteSelected() Style {
	return StyleDefault.Foreground(pal().text).Background(pal().backgroundElement).Bold(true)
}
func stylePaletteSelectedDesc() Style {
	return StyleDefault.Foreground(pal().textMuted).Background(pal().backgroundElement)
}
func stylePaletteItem() Style     { return StyleDefault.Foreground(pal().text) }
func stylePaletteItemDesc() Style { return StyleDefault.Foreground(pal().textMuted) }
func stylePaletteAccent() Style   { return StyleDefault.Foreground(pal().primary).Bold(true) }

func styleBold() Style   { return StyleDefault.Foreground(pal().markdownStrong).Bold(true) }
func styleItalic() Style { return StyleDefault.Foreground(pal().markdownEmph).Italic(true) }
func styleCode() Style {
	return StyleDefault.Foreground(pal().markdownCode).Background(pal().backgroundElement)
}
