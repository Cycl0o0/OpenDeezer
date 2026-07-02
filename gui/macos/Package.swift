// swift-tools-version: 5.9
import PackageDescription

// OpenDeezer — native SwiftUI front-end. It links the Go engine compiled to a C
// static archive (Clib/libdeezercore.a); run `make corelib` in gui/macos to
// (re)build that archive before `swift build`.
let package = Package(
    name: "OpenDeezer",
    // Base development language; the localized .lproj bundles live under
    // Sources/OpenDeezer/Resources and are picked up by the .process rule below.
    defaultLocalization: "en",
    // Liquid Glass (WWDC25) ships in macOS 26 (Tahoe).
    platforms: [.macOS("26.0")],
    targets: [
        .systemLibrary(name: "CDeezerCore", path: "Clib"),
        .executableTarget(
            name: "OpenDeezer",
            dependencies: ["CDeezerCore"],
            // Localized strings/plurals (en, zh-Hans, hi, es, fr, ar, ru). Emitted
            // into .build/apple/Products/<config>/OpenDeezer_OpenDeezer.bundle and
            // reached at runtime via Bundle.module (see Sources/OpenDeezer/L.swift).
            resources: [
                .process("Resources"),
            ],
            linkerSettings: [
                .unsafeFlags([
                    "-L", "Clib", "-ldeezercore",
                    "-framework", "CoreFoundation",
                    "-framework", "Security",
                    "-framework", "CoreAudio",
                    "-framework", "AudioToolbox",
                    "-framework", "AudioUnit",
                    "-framework", "Foundation",
                    // OS Now Playing / media keys (MPNowPlayingInfoCenter,
                    // MPRemoteCommandCenter) + AppKit tray (NSStatusItem).
                    "-framework", "MediaPlayer",
                    "-framework", "AppKit",
                    // Embedded Deezer login webview + arl-cookie capture
                    // (WKWebView / WKHTTPCookieStore). System framework, no
                    // external dependency — ships with the macOS SDK.
                    "-framework", "WebKit",
                ])
            ]
        ),
    ]
)
