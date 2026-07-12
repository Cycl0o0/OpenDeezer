package ui

import (
	"github.com/Cycl0o0/OpenDeezer/v2/internal/config"
	"github.com/Cycl0o0/OpenDeezer/v2/internal/i18n"
)

// languageOrder is the cycle the Language menu row steps through. "" is Auto
// (detect from the environment); the rest are shipped locale codes in the same
// order as i18n.Available().
var languageOrder = []string{"", "en", "zh", "hi", "es", "fr", "ar", "ru"}

// applyLanguage activates a saved language code, falling back to environment
// detection when it is empty (Auto).
func applyLanguage(code string) {
	if code == "" {
		i18n.SetLocale(i18n.DetectLocale())
		return
	}
	i18n.SetLocale(code)
}

// languageName is the native display name for a locale code, or "Auto" when the
// code is empty.
func languageName(code string) string {
	if code == "" {
		return i18n.T("Auto")
	}
	for _, l := range i18n.Available() {
		if l.Code == code {
			return l.Name
		}
	}
	return code
}

// currentLanguageName is the native display name for the Language menu's own
// persisted selection (the file, not $OPENDEEZER_LANG), or "Auto" when unset.
func currentLanguageName() string {
	return languageName(config.LanguageSetting())
}

// cycleLanguage advances to the next language in languageOrder, persists it,
// applies it live, and returns its native display name. It anchors on the menu's
// persisted selection (config.LanguageSetting) rather than LoadLanguage so a set
// $OPENDEEZER_LANG cannot freeze the cycle, and it names `next` directly so the
// label always matches the locale applyLanguage just applied.
func (m *Model) cycleLanguage() string {
	cur := config.LanguageSetting()
	idx := 0
	for i, c := range languageOrder {
		if c == cur {
			idx = i
			break
		}
	}
	next := languageOrder[(idx+1)%len(languageOrder)]
	_ = config.SaveLanguage(next)
	applyLanguage(next)
	return languageName(next)
}
