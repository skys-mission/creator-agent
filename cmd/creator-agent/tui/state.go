package tui

import (
	"context"
	"strings"
	"time"

	"github.com/clipperhouse/uax29/v2/graphemes"
	"github.com/skys-mission/creator-agent/cmd/creator-agent/tui/i18n"
	"github.com/skys-mission/creator-agent/config"
	"github.com/skys-mission/creator-agent/core"
	"github.com/skys-mission/creator-agent/core/middlewares"
)

type status int

const (
	statusIdle status = iota
	statusThinking
	statusRunningTool
	statusCompacting // a manual /compact is running (async history summarization)
	statusError
)

func (s status) String() string {
	switch s {
	case statusThinking:
		return i18n.T("status.thinking")
	case statusRunningTool:
		return i18n.T("status.running_tool")
	case statusError:
		return i18n.T("status.error")
	default:
		return i18n.T("status.idle")
	}
}

type msgKind int

const (
	kindUser msgKind = iota
	kindAssistant
	kindSystem // informational message (command output / hint), dim, not sent to the model
)

type completionsKind int

const (
	compNone  completionsKind = iota // menu closed (a.completions is empty)
	compSlash                        // a.completions holds slash command names
	compFile                         // a.completions holds relative file paths
)

type thinkingMode int

const (
	thinkingCompact  thinkingMode = iota // default: one-line summary
	thinkingExpanded                     // full reasoning
	thinkingHidden                       // not shown at all
)

// textBuf is a minimal, copy-safe text accumulator with a strings.Builder-compatible method set.
//
// It deliberately replaces strings.Builder inside msgBlock: strings.Builder carries a self-referential
// `addr *Builder` field (its copy detector), so copying a struct that embeds it (e.g. *a.current into
// a.messages, or a.messages... into a transient slice) leaves the copy's addr pointing at the original
// Builder. Once the original is freed (a.current = nil) or its backing array is reallocated, that addr
// becomes a dangling pointer living inside a live heap object; the GC follows it during marking and
// reports "marked free object" — a fatal heap corruption. textBuf holds only a []byte, so copying it
// is safe (it shares immutable, never-rewritten-in-place backing once finalized), and String() returns
// a fresh copy so no caller can alias the buffer.
type textBuf struct {
	b []byte
}

func (t *textBuf) WriteString(s string) (int, error) {
	t.b = append(t.b, s...)
	return len(s), nil
}

func (t *textBuf) WriteByte(c byte) error {
	t.b = append(t.b, c)
	return nil
}

func (t *textBuf) Reset() { t.b = nil }

// String returns the accumulated text. It copies the bytes so the result never aliases the internal
// buffer, keeping callers safe even if the buffer is later appended to.
func (t textBuf) String() string { return string(t.b) }

func (t textBuf) Len() int { return len(t.b) }

type msgBlock struct {
	kind     msgKind
	content  textBuf // assistant: plain text (rendered via markdown at finalize); user/system: raw text
	thinking textBuf // assistant reasoning (collapsible, dim)

	// streamEndMsg / interrupt finalization; nil means streaming (render layer falls back to plain).
	rendered []styledLine
	// renderedPlain is the plain-text fallback (for OSC 52 copy and non-markdown blocks).
	renderedPlain string

	thinkingMode thinkingMode

	toolCallName string
	toolCallArgs textBuf

	tools []toolCard
}

// finalize renders the current block's content into styled lines once. Called at streamEndMsg /
// interrupt finalization; after this the render layer reuses the cached styled lines.
func (b *msgBlock) finalize(md *markdownCache) {
	if b.kind == kindAssistant {
		body := b.content.String()
		if strings.TrimSpace(body) != "" {
			b.rendered = md.render(body)
			b.renderedPlain = body
		}
	}
}

type toolStatus int

const (
	toolRunning toolStatus = iota
	toolDone
	toolError
)

type toolCard struct {
	id       string
	name     string
	argsJSON string
	status   toolStatus
	result   string // brief result
	errMsg   string
	started  time.Time
	duration time.Duration

	parsedArgs toolCallInfo
	parsedJSON string

	expanded bool // interactive expansion (show full result/errMsg)
}

func (c *toolCard) parsed() toolCallInfo {
	if c.parsedJSON == c.argsJSON && c.argsJSON != "" {
		return c.parsedArgs
	}
	c.parsedArgs = parseToolInput(c.name, c.argsJSON)
	c.parsedJSON = c.argsJSON
	return c.parsedArgs
}

type changeRecord struct {
	path      string
	tool      string // write/edit
	argsJSON  string
	result    string
	timestamp time.Time
}

// todoItem is a TUI-side todo entry (parsed from todo_write tool argsJSON).
type todoItem struct {
	content string
	status  string // planned / pending / in_progress / completed
}

// profile is the model info for the status bar (avoids importing config in render layer).
type profile struct {
	Name    string
	Model   string
	BaseURL string
}

// AgentSpec is one selectable agent surfaced by the /agents picker (a named persona: system prompt
// + tool whitelist). Defined in the tui package (separate from main.agentSpec) to avoid a tui->main
// import edge; main converts via toTUIAgents.
type AgentSpec struct {
	Name        string
	Description string
	ReadOnly    bool // read-only agents (e.g. plan) get a badge in the picker
}

// homeSessionEntry is one recent-session row on the home screen. Built from core.SessionInfo; the
// digit-key quick-switch handler reads the ID to switch sessions.
type homeSessionEntry struct {
	ID    string
	Title string
}

// Compactor manually compresses a session's stored history into a summary + recent tail (backing
// the /compact command). It returns the new message list (which the caller displays) and persists
// it. nil = manual compaction unavailable.
type Compactor func(ctx context.Context, sessionID string) ([]core.Message, error)

type diffOverlayState struct {
	open    bool
	title   string   // header line (file path or tool label)
	lines   []string // pre-rendered diff lines (plain text; colored at render by prefix)
	scrollY int      // top visible line index
}

type helpOverlayState struct {
	open   bool
	scroll int // top visible command index
}

type paletteItem struct {
	name   string       // canonical slash name, e.g. "/help"
	desc   string       // short description
	action func(a *App) // optional; if set, commit runs this instead of the slash command in name
}

type paletteState struct {
	picker[paletteItem]
}

// App is the central TUI state. Accessed only from the event-loop goroutine.
//
//   - rt: injected, stable across the session (agent handle, contexts, profile, approver,
//     rebuild factory, tool declarations, markdown cache).
//   - app: mutable conversation + accounting (messages, current streaming block, tool cards,
//     change records, todos, token/cost counters, status).
//   - ui: input + viewport + transient UI modes (terminal size, input buffer/cursor, history,
//     overlays, completion menu, approval selection).
type App struct {
	rt     runtimeState
	screen *Screen
	term   *Terminal // real terminal (nil in tests); its resize channel drives Screen.SetSize

	events chan any // internal message queue (see app.go event types)

	messages      []msgBlock
	current       *msgBlock
	changes       []changeRecord
	todoList      []todoItem
	usage         string
	totalIn       int
	totalOut      int
	lastInput     int
	status        status
	statusStarted time.Time

	width, height int
	atBottom      bool // message area scroll: true = follow to bottom

	input     inputBuffer
	history   []string // input history (old -> new), persisted to ~/.creator/history.json
	histIdx   int      // browse position: 0 = current draft, 1 = most recent, ...
	histDraft string   // saved draft when history browsing starts, restored on Down past newest

	helpOverlay helpOverlayState
	asking      *askMsg
	err         error
	quitting    bool

	approveIdx int // 0=allow once, 1=allow this session, 2=deny

	completions     []string
	compIdx         int
	compScroll      int             // top visible index in the inline completion menu
	completionsKind completionsKind // what a.completions holds: slash commands or file paths

	fileIndex      []string
	fileIndexDir   string
	fileIndexMtime time.Time

	diffOverlay diffOverlayState

	palette paletteState

	sessionPicker sessionPickerState

	modelPicker modelPickerState

	mcpPicker mcpPickerState

	agentsPicker agentsPickerState

	variantPicker variantPickerState

	modePicker modePickerState

	themePicker themePickerState

	whichKey whichKeyState

	// homeSessions caches the recent sessions rendered on the home screen, so the digit-key (1-9)
	// quick-switch handler can map a key to a session id without re-reading the store.
	homeSessions []homeSessionEntry
	homeTip      string

	// leaderActive is set after the user presses the leader prefix (Ctrl+X); the next key is then
	// interpreted as a leader sequence (opencode-style <ctrl+x> bindings). Cleared on the next key.
	leaderActive bool

	lastCtrlC time.Time

	msgScroll int

	spinnerFrame int

	// lastMouseRender throttles wheel-driven repaints: rapid wheel events are coalesced so a
	// large message viewport is not re-rendered on every single event.
	lastMouseRender time.Time

	// forceRender, when set, makes the next render() call flush a full Sync (every cell re-emitted)
	// instead of an incremental Show. Set by runLoop on startup/resize; cleared by render() after.
	forceRender bool

	// lastFrameOverlay records whether the previous render() painted a full-screen overlay
	// (diff/help/palette/picker/which-key). Those overlays clear and paint the whole screen,
	// including the left/right insets that a transparent main view never repaints; drawMain uses
	// this to clear the back buffer exactly once on overlay-exit, avoiding stale glyphs without
	// paying a full-screen clear on every steady-state frame.
	lastFrameOverlay bool

	// resumeOnStart opens the session picker on the first frame (TUI -r/--resume). Consumed once by
	// runLoop right before the initial render; openSessionPicker no-ops when the store is empty.
	resumeOnStart bool

	// quitCh is closed to signal the event loop to exit.
	quitCh chan struct{}

	// streamCancel cancels the in-flight turn's context (set by startStream, cleared on stream end /
	// interrupt). nil when no turn is running. Calling it stops the agent loop, running tools, and any
	// pending approval wait without tearing down the whole app, so Esc/Ctrl+C can interrupt one turn.
	streamCancel context.CancelFunc
	// streamGen tags each turn so events/end signals from a cancelled or superseded stream are dropped
	// (a stale stream goroutine must not mutate a newer turn's state). Bumped on each start and on
	// interrupt; the event loop ignores eventMsg/streamEndMsg whose gen != streamGen.
	streamGen uint64

	curBg Color
}

// runtimeState holds injected dependencies and stable handles for the session lifetime.
type runtimeState struct {
	ag        core.Agent
	ctx       context.Context
	cancel    context.CancelFunc
	prof      profile
	approver  *asyncApprover
	rebuild   func(profileName string) (config.Profile, error)
	toolInfos []core.ToolInfo
	md        *markdownCache

	// nil-safe: callers guard on nil before Load/List/Save.
	store        core.SessionStore
	sessionID    string
	profiles     map[string]config.Profile
	mcpManager   MCPManager
	rebuildTools func() error

	agents       []AgentSpec
	currentAgent string
	// unavailable. The factory enqueues a switchAgentMsg to apply the swap on the event loop.
	agentSwitch func(name string) error

	variants       map[string]config.Variant
	currentVariant string
	variantSwitch  func(name string) error

	// modeCtl is the shared permission-mode controller (default/trust/auto/readonly); nil = legacy path.
	modeCtl     *middlewares.ModeController
	currentMode string // display cache of modeCtl's current mode (event-loop only, lock-free read)

	titleGen TitleGenerator

	compactor Compactor

	sender func(msg any)
}

func (a *App) addSystem(s string) {
	mb := msgBlock{kind: kindSystem}
	mb.content.WriteString(s)
	a.messages = append(a.messages, mb)
	a.atBottom = true
}

func orDash(s string) string {
	if s == "" {
		return i18n.T("label.dash")
	}
	return s
}

// truncateBytesMax returns s truncated to at most max bytes on a rune boundary, without appending
// any ellipsis (the caller adds its own). Use this for byte-budgeted previews of UTF-8 text where a
// naive s[:max] slice would split a multi-byte rune and emit invalid UTF-8.
func truncateBytesMax(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if len(s) <= max {
		return s
	}
	end := max
	if end > len(s) {
		end = len(s)
	}
	for end > 0 && !utf8RuneStart(s[end]) {
		end--
	}
	return s[:end]
}

// truncateStr truncates a string for display (byte-safe truncation with ellipsis).
// Delegates to truncateBytesMax for the rune-boundary cut, then appends the ellipsis.
func truncateStr(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if len(s) <= n {
		return s
	}
	return truncateBytesMax(s, n) + "..."
}

// truncateStrW truncates s to fit within maxW display columns, appending an ellipsis ("…") when
// truncation occurs. Display-width aware (strW), iterating grapheme clusters so ZWJ emoji sequences
// are measured as a single unit with width 2 — unlike truncateStr, which measures bytes and can
// overrun on wide content.
func truncateStrW(s string, maxW int) string {
	if maxW <= 0 {
		return ""
	}
	if strW(s) <= maxW {
		return s
	}
	const ell = "…"
	if maxW <= strW(ell) {
		return ell
	}
	limit := maxW - strW(ell)
	var out strings.Builder
	w := 0
	iter := graphemes.FromString(s)
	for iter.Next() {
		cluster := iter.Value()
		cw := strW(cluster)
		if w+cw > limit {
			break
		}
		out.WriteString(cluster)
		w += cw
	}
	return out.String() + ell
}

func utf8RuneStart(b byte) bool {
	return b&0xC0 != 0x80
}

func toolShortDesc(desc string) string {
	desc = strings.TrimSpace(desc)
	if desc == "" {
		return ""
	}
	if i := strings.IndexByte(desc, '\n'); i >= 0 {
		desc = desc[:i]
	}
	desc = strings.TrimSpace(desc)
	return truncateStr(desc, 70)
}

func nonEmpty(parts ...string) []string {
	var out []string
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// sendReply sends an approval reply non-blocking. approve may have already returned due to ctx
// cancellation (no one reading replyCh); a bare send would block the event loop and deadlock the TUI.
func sendReply(ch chan bool, v bool) {
	select {
	case ch <- v:
	default:
	}
}
