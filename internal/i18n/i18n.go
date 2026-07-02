// Package i18n is a tiny gettext-style translation layer for the OpenDeezer TUI
// and the shared status strings served by the control API. Catalogs are flat
// JSON files (msgid -> translation) embedded at build time; the msgid is the
// exact English source string, so an untranslated key falls back to readable
// English automatically.
//
// The package is a leaf (no OpenDeezer imports) so any package can call T/Tf/Tn
// without an import cycle. The active catalog is swapped atomically, so SetLocale
// is safe to call from the UI thread while other goroutines read translations.
package i18n

import (
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
)

//go:embed locales/*.json
var localesFS embed.FS

// catalog is one locale's flat msgid -> msgstr map plus its language code.
type catalog struct {
	code    string
	strings map[string]string
}

// catalogs holds every embedded locale, keyed by language code. Populated once
// in init and never mutated afterwards, so concurrent reads need no lock.
var catalogs = map[string]*catalog{}

// current is the active catalog, swapped atomically by SetLocale.
var current atomic.Pointer[catalog]

// supported is the fixed set of shipped locales with native display names, in a
// stable order (source language first).
var supported = []LocaleInfo{
	{Code: "en", Name: "English"},
	{Code: "zh", Name: "中文"},
	{Code: "hi", Name: "हिन्दी"},
	{Code: "es", Name: "Español"},
	{Code: "fr", Name: "Français"},
	{Code: "ar", Name: "العربية"},
	{Code: "ru", Name: "Русский"},
}

// LocaleInfo is a supported language: its BCP-47-ish code and native display name.
type LocaleInfo struct {
	Code string
	Name string
}

func init() {
	entries, err := localesFS.ReadDir("locales")
	if err == nil {
		for _, e := range entries {
			name := e.Name()
			if !strings.HasSuffix(name, ".json") {
				continue
			}
			code := strings.TrimSuffix(name, ".json")
			b, rerr := localesFS.ReadFile("locales/" + name)
			if rerr != nil {
				continue
			}
			var m map[string]string
			if json.Unmarshal(b, &m) != nil {
				continue
			}
			catalogs[code] = &catalog{code: code, strings: m}
		}
	}
	if en := catalogs["en"]; en != nil {
		current.Store(en)
	} else {
		// Never happens in a correctly built binary (en.json is embedded), but
		// keep T working (returns the key) rather than nil-panicking.
		current.Store(&catalog{code: "en", strings: map[string]string{}})
	}
}

// T returns the translation for key in the active locale, or key itself (the
// English source) when there is no non-empty translation.
func T(key string) string {
	if cat := current.Load(); cat != nil {
		if s, ok := cat.strings[key]; ok && s != "" {
			return s
		}
	}
	return key
}

// Tf is T followed by fmt.Sprintf, for messages with format placeholders.
func Tf(key string, args ...any) string {
	return fmt.Sprintf(T(key), args...)
}

// Tn returns a plural-aware translation. singular is the msgid (the English
// singular source, e.g. "%d track"); plural is the English plural used as the
// fallback when the catalog has no entry. The count n is applied as the first
// format argument, so the placeholder for the number should come first in the
// template; any extra args follow.
//
// Per-locale variants are stored under "<singular>|<category>" keys (category is
// one of zero/one/two/few/many/other per the CLDR cardinal rules for the
// locale). Missing entries fall back to the English one/other rule.
func Tn(singular, plural string, n int, args ...any) string {
	cat := current.Load()
	code := "en"
	if cat != nil {
		code = cat.code
	}
	category := pluralCategory(code, n)
	template := ""
	if cat != nil {
		if s, ok := cat.strings[singular+"|"+category]; ok && s != "" {
			template = s
		}
	}
	if template == "" {
		// English source fallback (one/other).
		if n == 1 {
			template = singular
		} else {
			template = plural
		}
	}
	all := make([]any, 0, len(args)+1)
	all = append(all, n)
	all = append(all, args...)
	return fmt.Sprintf(template, all...)
}

// pluralCategory returns the CLDR cardinal plural category for n in the given
// locale, restricted to integer counts (v = 0). Unknown locales use the English
// rule.
func pluralCategory(code string, n int) string {
	if n < 0 {
		n = -n
	}
	switch code {
	case "zh":
		return "other"
	case "es", "en":
		if n == 1 {
			return "one"
		}
		return "other"
	case "hi", "fr":
		// one: i = 0 or n = 1  (fr: i = 0,1) — identical for non-negative ints.
		if n == 0 || n == 1 {
			return "one"
		}
		return "other"
	case "ru":
		mod10 := n % 10
		mod100 := n % 100
		switch {
		case mod10 == 1 && mod100 != 11:
			return "one"
		case mod10 >= 2 && mod10 <= 4 && (mod100 < 12 || mod100 > 14):
			return "few"
		default:
			return "many"
		}
	case "ar":
		mod100 := n % 100
		switch {
		case n == 0:
			return "zero"
		case n == 1:
			return "one"
		case n == 2:
			return "two"
		case mod100 >= 3 && mod100 <= 10:
			return "few"
		case mod100 >= 11 && mod100 <= 99:
			return "many"
		default:
			return "other"
		}
	default:
		if n == 1 {
			return "one"
		}
		return "other"
	}
}

// SetLocale switches the active catalog. code may be a bare language code ("fr")
// or a fuller locale ("fr_FR.UTF-8", "zh-Hans"); it is parsed down to a shipped
// language, falling back to English when unknown or empty.
func SetLocale(code string) {
	lc := parseLocale(code)
	if lc == "" {
		lc = "en"
	}
	if cat := catalogs[lc]; cat != nil {
		current.Store(cat)
		return
	}
	if en := catalogs["en"]; en != nil {
		current.Store(en)
	}
}

// Locale returns the active language code.
func Locale() string {
	if cat := current.Load(); cat != nil {
		return cat.code
	}
	return "en"
}

// Available returns the shipped locales (code + native display name) in a stable
// order, source language first.
func Available() []LocaleInfo {
	out := make([]LocaleInfo, len(supported))
	copy(out, supported)
	return out
}

// DetectLocale inspects the standard locale environment variables (in POSIX
// precedence order) and returns the first shipped language they name, or "en".
func DetectLocale() string {
	for _, env := range []string{"LANGUAGE", "LC_ALL", "LC_MESSAGES", "LANG"} {
		v := os.Getenv(env)
		if v == "" {
			continue
		}
		// LANGUAGE may hold a colon-separated priority list.
		for _, part := range strings.Split(v, ":") {
			if lc := parseLocale(part); lc != "" {
				return lc
			}
		}
	}
	return "en"
}

// parseLocale reduces a locale string ("fr_FR.UTF-8", "zh-Hans", "zh_CN", "es")
// to a shipped language code, or "" when it names no shipped language. The
// charset (.UTF-8) and modifier (@euro) suffixes are stripped; the primary
// language subtag decides the result (all Chinese variants map to zh — the one
// shipped Chinese catalog is Simplified).
func parseLocale(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if i := strings.IndexAny(s, ".@"); i >= 0 {
		s = s[:i]
	}
	s = strings.ReplaceAll(s, "-", "_")
	lang := strings.ToLower(s)
	if i := strings.IndexByte(lang, '_'); i >= 0 {
		lang = lang[:i]
	}
	switch lang {
	case "en", "zh", "hi", "es", "fr", "ar", "ru":
		return lang
	}
	return ""
}
