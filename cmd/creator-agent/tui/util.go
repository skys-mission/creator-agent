package tui

import (
	"context"
	"encoding/base64"
	"math/rand"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/skys-mission/creator-agent/cmd/creator-agent/tui/i18n"
	"github.com/skys-mission/creator-agent/config"
	"github.com/skys-mission/creator-agent/core"
	"github.com/skys-mission/creator-agent/paths"
)

func hostOf(baseURL string) string { return config.HostOf(baseURL) }

func coreUserHint(err error) string { return core.UserHint(err) }

func durationStr(d time.Duration) string {
	if d <= 0 {
		return ""
	}
	if d < time.Second {
		return i18n.T("time.ms", d.Milliseconds())
	}
	return i18n.T("time.s", d.Seconds())
}

func spaces(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.Repeat(" ", n)
}

// LocaleTitlecase returns s with its first letter uppercased (ASCII-only, avoids unicode dependency).
func LocaleTitlecase(s string) string {
	if s == "" {
		return s
	}
	rs := []rune(s)
	if rs[0] >= 'a' && rs[0] <= 'z' {
		rs[0] = rs[0] - 'a' + 'A'
	}
	return string(rs)
}

// agentColorFor returns a soft accent color for an agent name, used in the prompt context line
// and user-message left border (mirrors opencode's per-agent color).
func agentColorFor(name string) Style {
	switch name {
	case "build":
		return styleApprove()
	case "plan":
		return styleDiffHunk()
	default:
		h := 0
		for _, r := range name {
			h = h*31 + int(r)
		}
		colors := []Style{styleAssistantLabel(), styleDiffFile(), styleToolName(), stylePaletteAccent()}
		return colors[abs(h)%len(colors)]
	}
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// displaySessionTitle localizes the default placeholder title for the UI while keeping the
// timestamp. Non-default titles are returned unchanged.
func displaySessionTitle(title string) string {
	const corePrefix = "New session - "
	if !strings.HasPrefix(title, corePrefix) {
		return title
	}
	return i18n.T("session.default_title_prefix") + " - " + strings.TrimPrefix(title, corePrefix)
}

func relativeTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return i18n.T("time.just_now")
	case d < time.Hour:
		return i18n.T("time.m_ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return i18n.T("time.h_ago", int(d.Hours()))
	case d < 7*24*time.Hour:
		return i18n.T("time.d_ago", int(d.Hours()/24))
	default:
		return t.Format(i18n.T("time.date_format"))
	}
}

// modelPrice holds per-1K-token input/output USD prices (rough estimate).
type modelPrice struct {
	inPerK, outPerK float64
}

// modelPriceFor returns per-1K-token input/output USD prices for a model name.
// Most providers charge ~1/4~1/2 input vs output. Returns (0,0) for unknown models
// (status bar hides cost). Data from vendor pricing pages (rough estimate, display only).
func modelPriceFor(model string) modelPrice {
	name := strings.ToLower(model)
	switch {
	case strings.Contains(name, "deepseek-v4-flash"), strings.Contains(name, "deepseek-chat"):
		return modelPrice{inPerK: 0.00027, outPerK: 0.0011}
	case strings.Contains(name, "gpt-4o-mini"):
		return modelPrice{inPerK: 0.00015, outPerK: 0.0006}
	case strings.Contains(name, "gpt-4o"):
		return modelPrice{inPerK: 0.0025, outPerK: 0.01}
	case strings.Contains(name, "claude-3-5-sonnet"), strings.Contains(name, "claude-sonnet"):
		return modelPrice{inPerK: 0.003, outPerK: 0.015}
	case strings.Contains(name, "glm-4-flash"):
		return modelPrice{inPerK: 0.00001, outPerK: 0.0001}
	}
	return modelPrice{}
}

// contextWindowFor returns the context window size (token count) by model name.
// Returns 0 for unknown models (status bar hides ctx%).
func contextWindowFor(model string) int {
	name := strings.ToLower(model)
	switch {
	case strings.Contains(name, "gpt-4o"):
		return 128000
	case strings.Contains(name, "gpt-4.1"), strings.Contains(name, "gpt-5"):
		return 1000000
	case strings.Contains(name, "deepseek"):
		// deepseek-chat (V3) / v4-flash: 128K input context (64K is max output, not context window)
		return 128000
	case strings.Contains(name, "claude") && strings.Contains(name, "sonnet"):
		return 200000
	case strings.Contains(name, "glm-4"):
		return 128000
	case strings.Contains(name, "moonshot"), strings.Contains(name, "kimi"):
		return 128000
	case strings.Contains(name, "qwen"):
		return 128000
	}
	return 0
}

// ctxPercentStr estimates context usage percentage (most-recent-round input tokens / window).
//
// Uses the most recent round's input token (lastInput) rather than cumulative: input token
// contains full history, representing the true current context usage. After compact, history
// is compressed and lastInput drops, so ctx% also drops — matching user expectation that
// "compression freed space". Window size is estimated from model name (fallback when no metadata).
func ctxPercentStr(model string, lastInput int) string {
	window := contextWindowFor(model)
	if window <= 0 {
		return ""
	}
	pct := lastInput * 100 / window
	if pct > 999 {
		pct = 999
	}
	return i18n.T("status.ctx_percent", pct)
}

// costEstimateStr estimates session cost (input*inPrice + output*outPrice).
// Empty when model is unknown or cost is zero.
func costEstimateStr(model string, totalIn, totalOut int) string {
	p := modelPriceFor(model)
	if p.inPerK <= 0 && p.outPerK <= 0 {
		return ""
	}
	cost := float64(totalIn)/1000.0*p.inPerK + float64(totalOut)/1000.0*p.outPerK
	if cost <= 0 {
		return ""
	}
	return i18n.T("status.cost_estimate", cost)
}

var homeTipPick = func(n int) int {
	if n <= 0 {
		return 0
	}
	return rand.Intn(n)
}

// homeTipKeys is the ordered list of translation keys for the home-screen tip pool.
var homeTipKeys = []string{
	"home.tip.agents",
	"home.tip.palette",
	"home.tip.slash",
	"home.tip.mention",
	"home.tip.models",
	"home.tip.variants",
	"home.tip.keys",
	"home.tip.sessions",
	"home.tip.mcps",
	"home.tip.themes",
	"home.tip.digits",
	"home.tip.alt_enter",
	"home.tip.copy",
	"home.tip.cost",
	"home.tip.changes",
}

// homeTips returns the pool of tips shown on the home screen. Each tip references a real, available
// command or key so the hint is always actionable. Order is arbitrary (selection is random).
func homeTips() []string {
	tips := make([]string, len(homeTipKeys))
	for i, k := range homeTipKeys {
		tips[i] = i18n.T(k)
	}
	return tips
}

// pickHomeTip returns the tip to show on the home screen. If a.tip is already chosen (non-empty) it
// is reused; otherwise a fresh one is rolled and cached. Callers clear a.homeTip when leaving home.
func pickHomeTip(a *App) string {
	if a.homeTip != "" {
		return a.homeTip
	}
	pool := homeTips()
	a.homeTip = pool[homeTipPick(len(pool))]
	return a.homeTip
}

func clearHomeTip(a *App) {
	a.homeTip = ""
}

// TitleGenerator produces a concise session title from the first user message. Implemented by the
// main package (wrapping a one-shot LLM call); nil disables auto-titling (the TUI skips it).
type TitleGenerator interface {
	Generate(ctx context.Context, firstUserMsg string) (string, error)
}

// titleGeneratedMsg carries an LLM-generated title for a session. Produced by the background title
// goroutine; applied on the event loop.
type titleGeneratedMsg struct {
	sessionID string
	title     string
}

const titleMaxLen = 100

var thinkRe = regexp.MustCompile(`(?s)<think>.*?</think>`)

// maybeTriggerTitleGeneration kicks off an async title generation for the current session when it is
// still using the default (timestamped) title and has exactly one user message. Called after a turn
// ends (handleStreamEnd), when the agent has already persisted history. Best-effort: any error is
// swallowed (the placeholder title remains). Runs the generation on a fresh goroutine so the UI is
// never blocked.
func maybeTriggerTitleGeneration(a *App) {
	if a.rt.titleGen == nil || a.rt.store == nil || a.rt.ctx == nil {
		return
	}
	sessionID := a.rt.sessionID
	if sessionID == "" {
		return
	}
	// is set (manually or by a prior generation), never overwrite it.
	title, _, _, err := loadMetaBestEffort(a.rt.store, sessionID)
	if err != nil {
		return
	}
	if !core.IsDefaultSessionTitle(title) && title != "" {
		return
	}
	msgs, err := a.rt.store.Load(sessionID)
	if err != nil {
		return
	}
	// opencode's gate: exactly one real user message. After the first turn this holds; the guard
	// also prevents re-titling a multi-message session that somehow still has a default title.
	var firstUser string
	userCount := 0
	for _, m := range msgs {
		if m.Role != core.RoleUser {
			continue
		}
		if strings.TrimSpace(m.Content) == "" {
			continue
		}
		userCount++
		if firstUser == "" {
			firstUser = m.Content
		}
	}
	if userCount != 1 || firstUser == "" {
		return
	}
	gen := a.rt.titleGen
	ctx := a.rt.ctx
	go func() {
		raw, gerr := gen.Generate(ctx, firstUser)
		if gerr != nil {
			return // best-effort: keep the placeholder.
		}
		cleaned := cleanTitle(raw)
		if cleaned == "" {
			return
		}
		// Non-blocking enqueue: if the event queue is full, drop rather than block the title goroutine.
		if a.rt.sender != nil {
			a.rt.sender(titleGeneratedMsg{sessionID: sessionID, title: cleaned})
		}
	}()
}

// applyTitle persists a generated title for its session (if still the active session) and forces a
// repaint so the sidebar / home / picker reflect it. Runs on the event-loop goroutine.
func applyTitle(a *App, msg titleGeneratedMsg) {
	if a.rt.store == nil || msg.title == "" {
		return
	}
	// it in the meantime; rename is a future line, but guard for safety).
	curTitle, _, _, err := loadMetaBestEffort(a.rt.store, msg.sessionID)
	if err != nil {
		return
	}
	if curTitle != "" && !core.IsDefaultSessionTitle(curTitle) {
		return
	}
	msgs, err := a.rt.store.Load(msg.sessionID)
	if err != nil {
		return
	}
	if err := a.rt.store.SaveWithMeta(msg.sessionID, msg.title, msgs); err != nil {
		return
	}
	a.forceRender = true
}

func cleanTitle(raw string) string {
	s := thinkRe.ReplaceAllString(raw, "")
	var picked string
	for _, line := range strings.Split(s, "\n") {
		t := strings.TrimSpace(line)
		if t != "" {
			picked = t
			break
		}
	}
	if picked == "" {
		return ""
	}
	if len([]rune(picked)) > titleMaxLen {
		r := []rune(picked)
		picked = string(r[:titleMaxLen-3]) + "..."
	}
	return picked
}

// osc52Copy writes the text to the system clipboard via OSC 52, drawn on the screen as a
// zero-cell escape. The screen must be a real terminal (not a simulation screen) for this to work.
func osc52Copy(screen *Screen, text string) {
	trimmed := strings.TrimRight(text, "\n")
	encoded := base64.StdEncoding.EncodeToString([]byte(trimmed))
	WriteOSC52(encoded)
}

func lastAssistantText(a *App) string {
	if a.current != nil && a.current.content.Len() > 0 {
		return a.current.content.String()
	}
	for i := len(a.messages) - 1; i >= 0; i-- {
		mb := &a.messages[i]
		if mb.kind == kindAssistant {
			if mb.renderedPlain != "" {
				return mb.renderedPlain
			}
			return mb.content.String()
		}
	}
	return ""
}

// saveLastReply writes the last assistant reply to ~/.creator/last-reply.md and returns the
// path. This is the /copy fallback for terminals where mouse selection is impractical.
func saveLastReply(text string) (string, error) {
	path, err := paths.LastReplyFile()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		return "", err
	}
	return path, nil
}
