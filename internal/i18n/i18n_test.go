package i18n

import (
	"reflect"
	"regexp"
	"sort"
	"testing"
)

// resetLocale restores English after a test that switches locale.
func resetLocale(t *testing.T) {
	t.Cleanup(func() { SetLocale("en") })
}

func TestFallbackReturnsKey(t *testing.T) {
	resetLocale(t)
	SetLocale("en")
	if got := T("this key is not in any catalog"); got != "this key is not in any catalog" {
		t.Fatalf("missing key should fall back to itself, got %q", got)
	}
	// A known key resolves to a translation in a non-en locale.
	SetLocale("fr")
	if got := T("Loading…"); got != "Chargement…" {
		t.Fatalf("fr Loading… = %q, want Chargement…", got)
	}
	// Missing key still falls back to English (the key) even in fr.
	if got := T("nope nope nope"); got != "nope nope nope" {
		t.Fatalf("fr missing key = %q, want itself", got)
	}
}

func TestTfFormats(t *testing.T) {
	resetLocale(t)
	SetLocale("en")
	if got := Tf("Volume %d%%", 50); got != "Volume 50%" {
		t.Fatalf("Tf = %q, want Volume 50%%", got)
	}
	SetLocale("ru")
	if got := Tf("Error: %s", "boom"); got != "Ошибка: boom" {
		t.Fatalf("ru Tf = %q", got)
	}
}

func TestParseLocale(t *testing.T) {
	cases := map[string]string{
		"fr_FR.UTF-8":          "fr",
		"zh-Hans":              "zh",
		"zh_CN":                "zh",
		"zh_TW":                "zh",
		"es":                   "es",
		"en_US.UTF-8":          "en",
		"ru_RU":                "ru",
		"ar_EG.UTF-8@modifier": "ar",
		"hi_IN":                "hi",
		"C":                    "",
		"POSIX":                "",
		"de_DE":                "",
		"":                     "",
	}
	for in, want := range cases {
		if got := parseLocale(in); got != want {
			t.Errorf("parseLocale(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDetectLocale(t *testing.T) {
	// LANG only.
	t.Setenv("LANGUAGE", "")
	t.Setenv("LC_ALL", "")
	t.Setenv("LC_MESSAGES", "")
	t.Setenv("LANG", "fr_FR.UTF-8")
	if got := DetectLocale(); got != "fr" {
		t.Fatalf("DetectLocale LANG=fr_FR.UTF-8 = %q, want fr", got)
	}
	// LANGUAGE precedence + colon list, first unsupported skipped.
	t.Setenv("LANGUAGE", "de:zh_CN")
	t.Setenv("LANG", "fr_FR.UTF-8")
	if got := DetectLocale(); got != "zh" {
		t.Fatalf("DetectLocale LANGUAGE=de:zh_CN = %q, want zh", got)
	}
	// LC_ALL wins over LANG.
	t.Setenv("LANGUAGE", "")
	t.Setenv("LC_ALL", "ru_RU.UTF-8")
	t.Setenv("LANG", "fr_FR.UTF-8")
	if got := DetectLocale(); got != "ru" {
		t.Fatalf("DetectLocale LC_ALL=ru = %q, want ru", got)
	}
	// Nothing set -> en.
	t.Setenv("LC_ALL", "")
	t.Setenv("LANG", "")
	if got := DetectLocale(); got != "en" {
		t.Fatalf("DetectLocale empty = %q, want en", got)
	}
}

func TestSetLocaleUnknownFallsBackToEnglish(t *testing.T) {
	resetLocale(t)
	SetLocale("de_DE")
	if Locale() != "en" {
		t.Fatalf("unknown locale should fall back to en, got %q", Locale())
	}
	SetLocale("zh-Hans")
	if Locale() != "zh" {
		t.Fatalf("zh-Hans should set zh, got %q", Locale())
	}
}

func TestPluralCategoryRussian(t *testing.T) {
	cases := map[int]string{
		1: "one", 21: "one", 101: "one",
		2: "few", 3: "few", 4: "few", 22: "few", 24: "few",
		5: "many", 6: "many", 11: "many", 12: "many", 14: "many", 25: "many", 100: "many", 0: "many",
	}
	for n, want := range cases {
		if got := pluralCategory("ru", n); got != want {
			t.Errorf("pluralCategory(ru, %d) = %q, want %q", n, got, want)
		}
	}
}

func TestPluralCategoryArabic(t *testing.T) {
	cases := map[int]string{
		0: "zero", 1: "one", 2: "two",
		3: "few", 4: "few", 10: "few", 103: "few", 204: "few",
		11: "many", 26: "many", 99: "many", 111: "many",
		100: "other", 101: "other", 102: "other", 200: "other",
	}
	for n, want := range cases {
		if got := pluralCategory("ar", n); got != want {
			t.Errorf("pluralCategory(ar, %d) = %q, want %q", n, got, want)
		}
	}
}

func TestPluralCategoryEnglishAndChinese(t *testing.T) {
	if pluralCategory("en", 1) != "one" || pluralCategory("en", 0) != "other" || pluralCategory("en", 2) != "other" {
		t.Fatal("english plural rule wrong")
	}
	for _, n := range []int{0, 1, 2, 5, 100} {
		if got := pluralCategory("zh", n); got != "other" {
			t.Fatalf("pluralCategory(zh, %d) = %q, want other", n, got)
		}
	}
}

func TestTnPicksCatalogPluralForm(t *testing.T) {
	resetLocale(t)
	// Russian one/few/many selection off the embedded catalog.
	SetLocale("ru")
	if got := Tn("Queue (%d track)", "Queue (%d tracks)", 1); got != "Очередь (1 трек)" {
		t.Errorf("ru n=1 = %q", got)
	}
	if got := Tn("Queue (%d track)", "Queue (%d tracks)", 3); got != "Очередь (3 трека)" {
		t.Errorf("ru n=3 = %q", got)
	}
	if got := Tn("Queue (%d track)", "Queue (%d tracks)", 5); got != "Очередь (5 треков)" {
		t.Errorf("ru n=5 = %q", got)
	}
	// Arabic few form.
	SetLocale("ar")
	if got := Tn("Queue (%d track)", "Queue (%d tracks)", 3); got != "قائمة الانتظار (3 أغانٍ)" {
		t.Errorf("ar n=3 = %q", got)
	}
	if got := Tn("Queue (%d track)", "Queue (%d tracks)", 1); got != "قائمة الانتظار (1 أغنية)" {
		t.Errorf("ar n=1 = %q", got)
	}
}

func TestTnFallbackWhenNoCatalogEntry(t *testing.T) {
	resetLocale(t)
	SetLocale("en")
	if got := Tn("apple (%d)", "apples (%d)", 1); got != "apple (1)" {
		t.Errorf("en n=1 fallback = %q", got)
	}
	if got := Tn("apple (%d)", "apples (%d)", 2); got != "apples (2)" {
		t.Errorf("en n=2 fallback = %q", got)
	}
	// An unknown msgid always falls back to the English one/other source strings,
	// regardless of the active locale (n==1 -> singular, else plural).
	SetLocale("zh")
	if got := Tn("apple (%d)", "apples (%d)", 1); got != "apple (1)" {
		t.Errorf("zh n=1 fallback = %q (want English singular)", got)
	}
	if got := Tn("apple (%d)", "apples (%d)", 9); got != "apples (9)" {
		t.Errorf("zh n=9 fallback = %q (want English plural)", got)
	}
}

func TestEveryCatalogNonEmptyAndComplete(t *testing.T) {
	en := catalogs["en"]
	if en == nil || len(en.strings) == 0 {
		t.Fatal("en catalog missing or empty")
	}
	for _, l := range supported {
		cat := catalogs[l.Code]
		if cat == nil {
			t.Fatalf("catalog for %q not embedded", l.Code)
		}
		if len(cat.strings) == 0 {
			t.Fatalf("catalog %q is empty", l.Code)
		}
		for k, v := range cat.strings {
			if v == "" {
				t.Errorf("catalog %q has empty value for key %q", l.Code, k)
			}
		}
		// Completeness: every en key must exist in this catalog.
		for k := range en.strings {
			if _, ok := cat.strings[k]; !ok {
				t.Errorf("catalog %q is missing key %q", l.Code, k)
			}
		}
		// No stray keys the source doesn't have.
		for k := range cat.strings {
			if _, ok := en.strings[k]; !ok {
				t.Errorf("catalog %q has extra key %q not in en", l.Code, k)
			}
		}
	}
}

var verbRe = regexp.MustCompile(`%%|%[-+ #0-9.*\[\]$]*[a-zA-Z]`)

// formatVerbs returns the sorted multiset of format-verb letters in s, ignoring
// the literal "%%" escape. %[1]s and %s both normalize to "s", so a translation
// may reorder positional args without failing parity.
func formatVerbs(s string) []string {
	var out []string
	for _, m := range verbRe.FindAllString(s, -1) {
		if m == "%%" {
			continue
		}
		out = append(out, m[len(m)-1:])
	}
	sort.Strings(out)
	return out
}

func TestPlaceholderParity(t *testing.T) {
	en := catalogs["en"]
	for _, l := range supported {
		if l.Code == "en" {
			continue
		}
		cat := catalogs[l.Code]
		for k, ev := range en.strings {
			tv, ok := cat.strings[k]
			if !ok {
				continue // completeness covered by the other test
			}
			want := formatVerbs(ev)
			got := formatVerbs(tv)
			if !reflect.DeepEqual(want, got) {
				t.Errorf("placeholder mismatch in %q for key %q: en=%v (%q) %s=%v (%q)",
					l.Code, k, want, ev, l.Code, got, tv)
			}
		}
	}
}

func TestAvailable(t *testing.T) {
	got := Available()
	if len(got) != 7 {
		t.Fatalf("Available len = %d, want 7", len(got))
	}
	if got[0].Code != "en" || got[0].Name != "English" {
		t.Fatalf("Available[0] = %+v", got[0])
	}
	// Mutating the returned slice must not affect the package state.
	got[1].Name = "MUTATED"
	if Available()[1].Name == "MUTATED" {
		t.Fatal("Available returned a shared backing array")
	}
}
