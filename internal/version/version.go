// Package version is the single source of truth for the OpenDeezer release
// number. Every Go surface (TUI, corelib c-archive, gomobile binding, MCP
// server) reads it from here, so a release bump can never miss one of them
// again (1.6.0 shipped with the engine still reporting 1.5.2, which made every
// desktop GUI show a permanent false "update available" banner).
//
// Bump it with scripts/bump-version.sh, which also updates the per-client
// build files (Info.plist, Gradle, meson, pbxproj, manifests, …).
package version

// Number is the current release, without a leading "v".
const Number = "1.7.0"
