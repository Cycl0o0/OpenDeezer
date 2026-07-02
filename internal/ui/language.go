package ui

import (
	"github.com/Cycl0o0/OpenDeezer/internal/config"
	"github.com/Cycl0o0/OpenDeezer/internal/i18n"
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

// currentLanguageName is the native display name for the persisted language
// setting, or "Auto" when unset.
func currentLanguageName() string {
	code := config.LoadLanguage()
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

// cycleLanguage advances to the next language in languageOrder, persists it,
// applies it live, and returns the new native display name.
func (m *Model) cycleLanguage() string {
	cur := config.LoadLanguage()
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
	return currentLanguageName()
}
