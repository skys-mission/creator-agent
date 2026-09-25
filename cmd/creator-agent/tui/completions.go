package tui

// Inline command hints for the input box ("命令行提示"): typing "/" opens a suggestion menu of the
// registered slash commands (fuzzy-filtered as you type), ↑↓ previews, Tab accepts, Enter runs an
// exact command or accepts the highlighted item. This is the pre-cut TUI's completion machinery
// (docs/tui-cut.md), kept per product decision, restricted to slash commands — the @-mention file
// hints stay cut.

import (
	"fmt"
	"strings"

	"github.com/skys-mission/creator-agent/cmd/creator-agent/tui/i18n"
)

const compPageSize = 10

// getSlashCommands returns the registered command names in registry order.
func getSlashCommands() []string {
	out := make([]string, 0, len(commandRegistry))
	for _, e := range commandRegistry {
		out = append(out, e.name)
	}
	return out
}

// matchCommands returns the commands with the given name prefix (Tab-completion semantics).
func matchCommands(prefix string) []string {
	var out []string
	for _, c := range getSlashCommands() {
		if strings.HasPrefix(c, prefix) {
			out = append(out, c)
		}
	}
	return out
}

// commandDesc returns the localized description of a command (empty when unknown).
func commandDesc(c string) string {
	if findCommand(c) != nil {
		return i18n.T("cmd.desc." + c)
	}
	return ""
}

// matchCommandsFuzzy ranks commands by a fuzzy subsequence match against name + description.
// Empty query (bare "/") lists everything, for discoverability.
func matchCommandsFuzzy(query string) []string {
	q := normalizeLower(strings.TrimSpace(query))
	q = strings.TrimPrefix(q, "/")
	if q == "" {
		out := make([]string, len(getSlashCommands()))
		copy(out, getSlashCommands())
		return out
	}
	type scored struct {
		name  string
		score int
	}
	var hits []scored
	for _, c := range getSlashCommands() {
		s, ok := fuzzyScore(c, commandDesc(c), q)
		if !ok || s <= 0 {
			continue
		}
		hits = append(hits, scored{name: c, score: s})
	}
	// Stable descending sort by score (insertion sort; the list is tiny). Stability preserves the
	// registry order among equal scores.
	for i := 1; i < len(hits); i++ {
		for j := i; j > 0 && hits[j].score > hits[j-1].score; j-- {
			hits[j], hits[j-1] = hits[j-1], hits[j]
		}
	}
	out := make([]string, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.name)
	}
	return out
}

// fuzzyScore scores one candidate against the query: name matches weigh full, description matches
// half. ok=false when neither contains the query as a subsequence.
func fuzzyScore(name, desc, q string) (int, bool) {
	ns, nOk := subseqScore(name, q)
	ds, dOk := subseqScore(desc, q)
	if !nOk && !dOk {
		return 0, false
	}
	switch {
	case nOk && dOk:
		return ns + ds/2, true
	case nOk:
		return ns, true
	default:
		return ds, true
	}
}

// subseqScore scores how well target contains q as a subsequence: +50 for anchoring at the start,
// +15 per consecutive run, plus a shortness bonus. ok=false when q is not a subsequence.
func subseqScore(target, q string) (int, bool) {
	if q == "" {
		return 0, true
	}
	t := strings.ToLower(target)
	qi := 0
	score := 0
	prevMatchIdx := -1
	for i := 0; i < len(t) && qi < len(q); i++ {
		if t[i] == q[qi] {
			if qi == 0 && i == 0 {
				score += 50
			}
			if prevMatchIdx >= 0 && i == prevMatchIdx+1 {
				score += 15
			}
			prevMatchIdx = i
			qi++
		}
	}
	if qi != len(q) {
		return 0, false
	}
	score += maxi(0, 20-len(t))
	return score, true
}

// shouldShowSlashCompletions reports whether the hint menu should be open for input v: the input
// must start with "/" and contain no whitespace (once arguments begin the user has moved on). A
// lone "/" opens the menu with all commands for discoverability.
func shouldShowSlashCompletions(v string) bool {
	if !strings.HasPrefix(v, "/") {
		return false
	}
	return !strings.ContainsAny(v, " \t\n")
}

// closeCompletions dismisses the hint menu and resets its selection.
func closeCompletions(a *App) {
	a.completions = nil
	a.compIdx = 0
	a.compScroll = 0
}

// refineCompletions recomputes the hint menu from the current input (called after every edit).
func refineCompletions(a *App) {
	if !shouldShowSlashCompletions(a.input.Value()) {
		closeCompletions(a)
		return
	}
	a.completions = matchCommandsFuzzy(a.input.Value())
	a.compIdx = 0 // reset to top row on every refine (matches opencode)
	a.compScroll = 0
}

// handleCompletionKey routes one key while the hint menu is open. Selection moves mirror the
// highlighted item into the input (preview); Tab/Enter accept; Esc closes the menu but keeps the
// typed text; any editing key edits and re-refines. Quit keys are routed before this ever runs.
func handleCompletionKey(a *App, k Key, r rune) {
	n := len(a.completions)
	switch k {
	case KeyUp:
		if a.compIdx > 0 {
			a.compIdx--
		}
		ensureCompVisible(a)
		previewCompletion(a)
		return
	case KeyDown:
		if a.compIdx < n-1 {
			a.compIdx++
		}
		ensureCompVisible(a)
		previewCompletion(a)
		return
	case KeyPgUp:
		a.compIdx = maxi(0, a.compIdx-compPageSize)
		ensureCompVisible(a)
		previewCompletion(a)
		return
	case KeyPgDn:
		a.compIdx = mini(n-1, a.compIdx+compPageSize)
		ensureCompVisible(a)
		previewCompletion(a)
		return
	case KeyHome:
		a.compIdx = 0
		ensureCompVisible(a)
		previewCompletion(a)
		return
	case KeyEnd:
		a.compIdx = maxi(0, n-1)
		ensureCompVisible(a)
		previewCompletion(a)
		return
	case KeyTab:
		acceptCompletion(a)
		return
	case KeyEnter:
		// The typed text is already an exact command (e.g. a fully typed "/model-new", or a
		// previewed selection): Enter runs it now instead of eating a second Enter on accept.
		if findCommand(strings.TrimSpace(a.input.Value())) != nil {
			submitInput(a)
			return
		}
		acceptCompletion(a)
		return
	case KeyEsc:
		closeCompletions(a)
		return
	}
	// Editing keys: close the menu, apply the edit, re-refine (the edit may still be a query).
	closeCompletions(a)
	if k == KeyRune {
		if r != 0 {
			a.input.InsertRune(r)
		}
	} else if !editTextBuffer(k, r, &a.input) {
		return
	}
	refineCompletions(a)
}

// acceptCompletion writes the highlighted item into the input and closes the menu.
func acceptCompletion(a *App) {
	if a.compIdx >= 0 && a.compIdx < len(a.completions) {
		a.input.SetValue(a.completions[a.compIdx])
	}
	closeCompletions(a)
}

// ensureCompVisible scrolls the menu so the selection is within the visible page.
func ensureCompVisible(a *App) {
	if a.compIdx < a.compScroll {
		a.compScroll = a.compIdx
	}
	if a.compIdx >= a.compScroll+compPageSize {
		a.compScroll = a.compIdx - compPageSize + 1
	}
}

// previewCompletion mirrors the highlighted item into the input box without closing the menu, so
// the user sees what they are about to accept while navigating.
func previewCompletion(a *App) {
	if len(a.completions) == 0 || a.compIdx < 0 || a.compIdx >= len(a.completions) {
		return
	}
	a.input.SetValue(a.completions[a.compIdx])
}

// compRow is one rendered row of the hint menu.
type compRow struct {
	text     string
	selected bool
	muted    bool
}

// completionRows builds the visible hint-menu rows for the given width: the current page of
// candidates ("  name  description", selected row prefixed "> ") plus a "… N more" footer when
// the candidate list overflows the page.
func completionRows(a *App, width int) []compRow {
	cands := a.completions
	if len(cands) == 0 {
		return nil
	}
	end := mini(len(cands), a.compScroll+compPageSize)
	visible := cands[a.compScroll:end]
	rows := make([]compRow, 0, len(visible)+1)
	for i, c := range visible {
		absIdx := a.compScroll + i
		prefix := "  "
		selected := false
		if absIdx == a.compIdx {
			// '>' is width-unambiguous; the arrow glyph '▶' is ambiguous-width.
			prefix = "> "
			selected = true
		}
		text := fmt.Sprintf(prefix+"%-12s %s", c, commandDesc(c))
		rows = append(rows, compRow{text: truncatePlain(text, width), selected: selected})
	}
	if len(cands) > compPageSize {
		more := len(cands) - (a.compScroll + len(visible))
		if more < 0 {
			more = 0
		}
		rows = append(rows, compRow{text: fmt.Sprintf(i18n.T("completion.footer.more"), more), muted: true})
	}
	return rows
}
