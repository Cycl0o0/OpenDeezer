import Foundation

// Localization helpers.
//
// This app is a SwiftPM module, so SwiftUI's implicit `LocalizedStringKey`
// lookup (which uses `Bundle.main`) does NOT see the package's localized
// resources — those live in the generated `OpenDeezer_OpenDeezer.bundle`
// reachable only through `Bundle.module`. Every user-facing literal is therefore
// wrapped in `L()` / `Lf()` / `Lp()` below so the lookup targets `Bundle.module`.
//
// Keys are the exact English source strings; `en.lproj/Localizable.strings`
// mirrors each key to itself.

/// Plain lookup. Returns the localized string for `key` from `Bundle.module`.
@inline(__always)
func L(_ key: String) -> String {
    NSLocalizedString(key, bundle: .module, comment: "")
}

/// Format lookup — for keys containing `%@` / `%d` placeholders. Positional
/// (`%1$@`, `%2$@`) reordering in a translation is resolved by `String(format:)`.
@inline(__always)
func Lf(_ key: String, _ args: CVarArg...) -> String {
    String(format: NSLocalizedString(key, bundle: .module, comment: ""),
           locale: .current, arguments: args)
}

/// Plural-aware lookup (backed by `Localizable.stringsdict`). Resolves the
/// `%#@…@` variable token to the correct CLDR plural category for the current
/// language (e.g. Russian one/few/many, Arabic's six categories).
@inline(__always)
func Lp(_ key: String, _ count: Int) -> String {
    String.localizedStringWithFormat(
        NSLocalizedString(key, bundle: .module, comment: ""), count)
}
