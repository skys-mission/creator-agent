package tui

import (
	"strings"

	"github.com/skys-mission/creator-agent/cmd/creator-agent/tui/i18n"
)

// slashCommands is lazily populated on first access (to avoid init-order dependency on
// commandRegistry, which is defined in commands.go). Once populated it is immutable.
var slashCommands []string

func getSlashCommands() []string {
	if slashCommands == nil {
		out := make([]string, 0, len(commandRegistry))
		for _, e := range commandRegistry {
			out = append(out, e.name)
		}
		slashCommands = out
	}
	return slashCommands
}

func matchCommands(prefix string) []string {
	var out []string
	for _, c := range getSlashCommands() {
		if strings.HasPrefix(c, prefix) {
			out = append(out, c)
		}
	}
	return out
}

func commandDesc(c string) string {
	if e := findCommand(c); e != nil {
		return i18n.T("cmd.desc." + c)
	}
	return ""
}

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

func refineSlashCompletions(a *App) {
	v := a.input.Value()
	if !shouldShowSlashCompletions(v) {
		a.completions = nil
		a.completionsKind = compNone
		a.compIdx = 0
		a.compScroll = 0
		return
	}
	a.completions = matchCommandsFuzzy(v)
	a.completionsKind = compSlash
	a.compIdx = 0 // reset to top row on every refine (matches opencode)
	a.compScroll = 0
}

// shouldShowSlashCompletions reports whether the inline slash popup should be open for input v:
// the input must start with "/" and contain no whitespace (so "/help foo" hides the menu — the
// user has moved on to arguments). A lone "/" opens the menu with all commands (empty query) for
// discoverability. Mirrors opencode's "/"-trigger condition.
func shouldShowSlashCompletions(v string) bool {
	if !strings.HasPrefix(v, "/") {
		return false
	}
	return !strings.ContainsAny(v, " \t\n")
}

// refineFileCompletions recomputes the @-mention file candidate list from the current input.
// When the input no longer has a triggering `@` token, the menu is closed. The file index is
// pulled from the working-directory cache (fileindex.go).
func refineFileCompletions(a *App) {
	v := a.input.Value()
	if !shouldShowFileCompletions(v) {
		a.completions = nil
		a.completionsKind = compNone
		a.compIdx = 0
		a.compScroll = 0
		return
	}
	q := normalizeLower(stripMentionRange(extractMentionQuery(v)))
	idx := getFileIndex(a, ".")
	a.completions = matchFilesFuzzy(idx, q)
	a.completionsKind = compFile
	a.compIdx = 0
	a.compScroll = 0
}

// shouldShowFileCompletions reports whether the @-mention popup should be open for input v: there
// must be a triggering `@` (per mentionTriggerIndex rules) with the user still typing the path
// token (no whitespace after it).
func shouldShowFileCompletions(v string) bool {
	return mentionTriggerIndex(v) >= 0
}

func matchFilesFuzzy(files []string, query string) []string {
	const cap = 10
	if query == "" {
		out := make([]string, 0, cap)
		for _, f := range files {
			out = append(out, f)
			if len(out) >= cap {
				break
			}
		}
		return out
	}
	type scored struct {
		path  string
		score int
	}
	var hits []scored
	for _, f := range files {
		s, ok := fuzzyScore(f, "", query)
		if !ok || s <= 0 {
			continue
		}
		s += maxi(0, 40-len(f))
		hits = append(hits, scored{path: f, score: s})
	}
	for i := 1; i < len(hits); i++ {
		for j := i; j > 0 && hits[j].score > hits[j-1].score; j-- {
			hits[j], hits[j-1] = hits[j-1], hits[j]
		}
	}
	out := make([]string, 0, cap)
	for _, h := range hits {
		out = append(out, h.path)
		if len(out) >= cap {
			break
		}
	}
	return out
}

// refineCompletions is the single dispatcher called after every input mutation: it decides
// whether the slash popup, the file-mention popup, or neither should be open, and updates
// a.completions / a.completionsKind accordingly. Slash takes precedence over file (a leading "/"
// never also triggers "@").
func refineCompletions(a *App) {
	v := a.input.Value()
	switch {
	case shouldShowSlashCompletions(v):
		refineSlashCompletions(a)
	case shouldShowFileCompletions(v):
		refineFileCompletions(a)
	default:
		a.completions = nil
		a.completionsKind = compNone
		a.compIdx = 0
		a.compScroll = 0
	}
}
