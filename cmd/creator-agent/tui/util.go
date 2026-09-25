package tui

import (
	"strings"

	"github.com/skys-mission/creator-agent/cmd/creator-agent/tui/i18n"
)

// Small pure helpers shared by the shell and the /model-new dialog.

func mini(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxi(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// orDash renders an empty value as the localized dash placeholder (confirm page).
func orDash(s string) string {
	if s == "" {
		return i18n.T("label.dash")
	}
	return s
}

// maskKey shows only the last 4 characters of an API key (or bullets when short), so the confirm
// screen reveals enough to recognize the key without printing the secret in full.
func maskKey(k string) string {
	r := []rune(k)
	if len(r) <= 4 {
		return strings.Repeat("•", len(r))
	}
	return strings.Repeat("•", len(r)-4) + string(r[len(r)-4:])
}

// truncatePlain clips s to a maximum display width, appending an ellipsis when it overflows.
func truncatePlain(s string, maxWidth int) string {
	if maxWidth <= 0 {
		return ""
	}
	if strW(s) <= maxWidth {
		return s
	}
	if maxWidth == 1 {
		return "…"
	}
	var b strings.Builder
	used := 0
	for _, ch := range s {
		w := runeW(ch)
		if w <= 0 {
			continue
		}
		if used+w > maxWidth-1 {
			break
		}
		b.WriteRune(ch)
		used += w
	}
	b.WriteRune('…')
	return b.String()
}
