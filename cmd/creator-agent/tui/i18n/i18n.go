// Package i18n provides lightweight localization for the TUI.
//
// Only user-facing strings are translated; model-facing prompts and tool inputs stay in English.
// The current language is set once at TUI startup and read from the event-loop goroutine, so a
// simple package-level variable is sufficient (no concurrent toggling after startup).
package i18n

import (
	"fmt"
	"strings"
)

// Lang is a supported UI language.
type Lang string

const (
	EN Lang = "en"
	ZH Lang = "zh"
)

var (
	current = EN
	dicts   = make(map[Lang]map[string]string)
)

// SetLang switches the active UI language. Unknown values fall back to English.
func SetLang(l Lang) {
	switch l {
	case ZH:
		current = ZH
	default:
		current = EN
	}
}

// CurrentLang returns the active language.
func CurrentLang() Lang { return current }

// ParseLang converts a string to a supported Lang.
func ParseLang(s string) Lang {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "_", "-")
	switch s {
	case "zh", "zh-cn", "zh-tw", "zh-hk":
		return ZH
	default:
		return EN
	}
}

// T returns the localized template for key. If the template has placeholders, callers pass the
// values as args and the result is formatted with fmt.Sprintf. Indexed placeholders (%[1]s) are
// supported so translations can reorder arguments.
func T(key string, args ...any) string {
	tmpl := lookup(key)
	if tmpl == "" {
		// Missing translation: return the key itself so the bug is visible but the UI still works.
		if len(args) > 0 {
			return fmt.Sprintf("[%s]", key)
		}
		return key
	}
	if len(args) > 0 {
		return fmt.Sprintf(tmpl, args...)
	}
	return tmpl
}

func lookup(key string) string {
	dict, ok := dicts[current]
	if !ok {
		return ""
	}
	if v, ok := dict[key]; ok {
		return v
	}
	// Fall back to English.
	if v, ok := dicts[EN][key]; ok {
		return v
	}
	return ""
}

// register adds a dictionary for a language. Later registrations overwrite earlier ones.
func register(lang Lang, entries map[string]string) {
	if dicts[lang] == nil {
		dicts[lang] = make(map[string]string)
	}
	for k, v := range entries {
		dicts[lang][k] = v
	}
}
