import SwiftUI

// AppSettings is the small, JSON-persisted preferences blob, stored alongside
// arl.txt in ~/.config/opendeezer/settings.json.
struct AppSettings: Codable {
    // Quality level: 0 = Normal (MP3 128), 1 = High (MP3 320), 2 = HiFi (FLAC).
    var quality: Int = 1
    // Keep playing in background: closing the window hides to the tray instead
    // of quitting.
    var closeToTray: Bool = true

    static var configDir: URL {
        FileManager.default.homeDirectoryForCurrentUser
            .appendingPathComponent(".config/opendeezer", isDirectory: true)
    }
    static var fileURL: URL { configDir.appendingPathComponent("settings.json") }

    static func load() -> AppSettings {
        guard let data = try? Data(contentsOf: fileURL),
              let s = try? JSONDecoder().decode(AppSettings.self, from: data) else {
            return AppSettings()
        }
        return s
    }

    func save() {
        try? FileManager.default.createDirectory(
            at: Self.configDir, withIntermediateDirectories: true)
        let enc = JSONEncoder()
        enc.outputFormatting = [.prettyPrinted, .sortedKeys]
        if let data = try? enc.encode(self) {
            try? data.write(to: Self.fileURL, options: .atomic)
        }
    }
}

// SettingsView — a compact Liquid Glass sheet for audio quality and background
// behaviour. Reachable from the sidebar account row.
struct SettingsView: View {
    @EnvironmentObject var app: AppState

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack(spacing: 10) {
                Image(systemName: "gearshape.fill")
                    .font(.system(size: 22)).foregroundStyle(DZ.accent)
                Text("Settings")
                    .font(.system(size: 22, weight: .bold)).foregroundStyle(DZ.textPri)
                Spacer()
            }
            .padding(.bottom, 18)

            // Audio quality
            settingsCard {
                VStack(alignment: .leading, spacing: 10) {
                    Label("Audio Quality", systemImage: "waveform")
                        .font(.system(size: 13, weight: .semibold)).foregroundStyle(DZ.textPri)
                    Picker("", selection: Binding(
                        get: { app.settings.quality },
                        set: { app.setQuality($0) })) {
                        Text("Normal · MP3 128").tag(0)
                        Text("High · MP3 320").tag(1)
                        Text("HiFi · FLAC").tag(2)
                    }
                    .pickerStyle(.segmented)
                    .labelsHidden()
                    Text("HiFi streams lossless FLAC when your account and the track support it, otherwise falls back to MP3. Applied immediately and on next launch.")
                        .font(.caption).foregroundStyle(DZ.textSec)
                    if let note = app.qualityEntitlementNote {
                        Label(note, systemImage: "exclamationmark.triangle.fill")
                            .font(.caption).foregroundStyle(DZ.accentMag)
                    }
                }
            }

            // Volume normalization (ReplayGain)
            settingsCard {
                Toggle(isOn: Binding(
                    get: { app.replayGain },
                    set: { app.setReplayGain($0) })) {
                    VStack(alignment: .leading, spacing: 2) {
                        Label("Volume normalization", systemImage: "speaker.wave.2.fill")
                            .font(.system(size: 13, weight: .semibold)).foregroundStyle(DZ.textPri)
                        Text("Evens out loudness differences between tracks (ReplayGain).")
                            .font(.caption).foregroundStyle(DZ.textSec)
                    }
                }
                .toggleStyle(.switch)
                .tint(DZ.accent)
            }

            // Background playback
            settingsCard {
                Toggle(isOn: Binding(
                    get: { app.settings.closeToTray },
                    set: { app.setCloseToTray($0) })) {
                    VStack(alignment: .leading, spacing: 2) {
                        Label("Keep playing in background", systemImage: "menubar.arrow.up.rectangle")
                            .font(.system(size: 13, weight: .semibold)).foregroundStyle(DZ.textPri)
                        Text("Closing the window hides it to the menu bar instead of quitting.")
                            .font(.caption).foregroundStyle(DZ.textSec)
                    }
                }
                .toggleStyle(.switch)
                .tint(DZ.accent)
            }

            HStack {
                Text("Stored in ~/.config/opendeezer/settings.json")
                    .font(.caption2).foregroundStyle(DZ.textSec)
                Spacer()
                Button("Done") { app.showSettings = false }
                    .buttonStyle(.glassProminent).tint(DZ.accent).controlSize(.large)
            }
            .padding(.top, 18)
        }
        .padding(24)
        .frame(width: 440)
        .background(DZ.windowBG)
    }

    @ViewBuilder
    private func settingsCard<C: View>(@ViewBuilder _ content: () -> C) -> some View {
        content()
            .frame(maxWidth: .infinity, alignment: .leading)
            .padding(16)
            .glassEffect(.regular, in: RoundedRectangle(cornerRadius: 14))
            .padding(.bottom, 14)
    }
}
