// swift-tools-version: 5.9
import PackageDescription

// DeezerGUI — native SwiftUI front-end. It links the Go engine compiled to a C
// static archive (Clib/libdeezercore.a); run `make corelib` in gui/macos to
// (re)build that archive before `swift build`.
let package = Package(
    name: "DeezerGUI",
    // Liquid Glass (WWDC25) ships in macOS 26 (Tahoe).
    platforms: [.macOS("26.0")],
    targets: [
        .systemLibrary(name: "CDeezerCore", path: "Clib"),
        .executableTarget(
            name: "DeezerGUI",
            dependencies: ["CDeezerCore"],
            linkerSettings: [
                .unsafeFlags([
                    "-L", "Clib", "-ldeezercore",
                    "-framework", "CoreFoundation",
                    "-framework", "Security",
                    "-framework", "CoreAudio",
                    "-framework", "AudioToolbox",
                    "-framework", "AudioUnit",
                    "-framework", "Foundation",
                ])
            ]
        ),
    ]
)
