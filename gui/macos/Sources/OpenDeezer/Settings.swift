import SwiftUI
import AppKit

// AppSettings is the small, JSON-persisted preferences blob, stored alongside
// arl.txt in ~/.config/opendeezer/settings.json.
struct AppSettings: Codable {
    // Quality level: 0 = Normal (MP3 128), 1 = High (MP3 320), 2 = HiFi (FLAC).
    var quality: Int = 1
    // Keep playing in background: closing the window hides to the tray instead
    // of quitting.
    var closeToTray: Bool = true
    // Gapless playback (engine swaps preloaded tracks with no silence).
    var gapless: Bool = true
    // Crossfade duration in ms (0 = off). Applied to the engine on launch.
    var crossfadeMS: Int = 0

    enum CodingKeys: String, CodingKey { case quality, closeToTray, gapless, crossfadeMS }

    init() {}

    // Tolerant decode so older settings.json files (without the v0.4 keys) load.
    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        quality = try c.decodeIfPresent(Int.self, forKey: .quality) ?? 1
        closeToTray = try c.decodeIfPresent(Bool.self, forKey: .closeToTray) ?? true
        gapless = try c.decodeIfPresent(Bool.self, forKey: .gapless) ?? true
        crossfadeMS = try c.decodeIfPresent(Int.self, forKey: .crossfadeMS) ?? 0
    }

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

    // Phone Remote state (engine-owned, not persisted to settings.json).
    @State private var webRemoteEnabled = false
    @State private var webRemoteCode = ""
    @State private var webRemoteURL = ""
    @State private var webRemoteQRImage: NSImage? = nil

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

            ScrollView {
              VStack(alignment: .leading, spacing: 0) {
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

            // Output device
            settingsCard {
                VStack(alignment: .leading, spacing: 10) {
                    Label("Output Device", systemImage: "hifispeaker.fill")
                        .font(.system(size: 13, weight: .semibold)).foregroundStyle(DZ.textPri)
                    Picker("", selection: Binding(
                        get: { app.currentAudioDeviceID },
                        set: { app.setAudioDevice($0) })) {
                        // The engine reports "" as the system default device.
                        if !app.audioDevices.contains(where: { $0.id == app.currentAudioDeviceID }) {
                            Text("System Default").tag(app.currentAudioDeviceID)
                        }
                        ForEach(app.audioDevices) { d in
                            Text(d.isDefault ? "\(d.name) (System Default)" : d.name).tag(d.id)
                        }
                    }
                    .labelsHidden()
                    Text("Choose where audio plays. Switching takes effect on the next track or seek.")
                        .font(.caption).foregroundStyle(DZ.textSec)
                }
            }

            // Gapless playback
            settingsCard {
                Toggle(isOn: Binding(
                    get: { app.settings.gapless },
                    set: { app.setGapless($0) })) {
                    VStack(alignment: .leading, spacing: 2) {
                        Label("Gapless playback", systemImage: "forward.end.alt.fill")
                            .font(.system(size: 13, weight: .semibold)).foregroundStyle(DZ.textPri)
                        Text("Preloads the next track so albums play with no silence between songs.")
                            .font(.caption).foregroundStyle(DZ.textSec)
                    }
                }
                .toggleStyle(.switch)
                .tint(DZ.accent)
            }

            // Crossfade
            settingsCard {
                VStack(alignment: .leading, spacing: 10) {
                    Label("Crossfade", systemImage: "wave.3.forward")
                        .font(.system(size: 13, weight: .semibold)).foregroundStyle(DZ.textPri)
                    Picker("", selection: Binding(
                        get: { app.settings.crossfadeMS },
                        set: { app.setCrossfadeMS($0) })) {
                        Text("Off").tag(0)
                        Text("3s").tag(3000)
                        Text("6s").tag(6000)
                        Text("12s").tag(12000)
                    }
                    .pickerStyle(.segmented)
                    .labelsHidden()
                    Text("Fades the end of one track into the start of the next.")
                        .font(.caption).foregroundStyle(DZ.textSec)
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

            // Phone Remote
            settingsCard {
                VStack(alignment: .leading, spacing: 10) {
                    Toggle(isOn: Binding(
                        get: { webRemoteEnabled },
                        set: { on in
                            webRemoteEnabled = on
                            Core.setWebRemoteEnabled(on)
                            if on {
                                loadWebRemoteInfo()
                            } else {
                                webRemoteCode = ""
                                webRemoteURL = ""
                                webRemoteQRImage = nil
                            }
                        })) {
                        Label("Phone Remote", systemImage: "iphone.radiowaves.left.and.right")
                            .font(.system(size: 13, weight: .semibold)).foregroundStyle(DZ.textPri)
                    }
                    .toggleStyle(.switch)
                    .tint(DZ.accent)

                    Text("Scan with your phone (same Wi-Fi), then enter the code.")
                        .font(.caption).foregroundStyle(DZ.textSec)

                    if webRemoteEnabled, !webRemoteCode.isEmpty {
                        VStack(spacing: 8) {
                            if let img = webRemoteQRImage {
                                Image(nsImage: img)
                                    .resizable()
                                    .interpolation(.none)
                                    .frame(width: 160, height: 160)
                                    .clipShape(RoundedRectangle(cornerRadius: 10))
                            }
                            Text(webRemoteCode)
                                .font(.system(size: 32, weight: .bold, design: .monospaced))
                                .foregroundStyle(DZ.textPri)
                            Text(webRemoteURL)
                                .font(.caption).foregroundStyle(DZ.textSec)
                                .textSelection(.enabled)
                        }
                        .frame(maxWidth: .infinity, alignment: .center)
                        .padding(.top, 4)
                    }
                }
            }
              }
            }
            .scrollContentBackground(.hidden)

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
        .frame(width: 440, height: 620)
        .background(DZ.windowBG)
        .onAppear {
            app.loadAudioDevices()
            loadWebRemoteInfo()
        }
    }

    private func loadWebRemoteInfo() {
        Task.detached {
            let info = Core.webRemoteInfo()
            let qrData = (info?.enabled == true) ? Core.webRemoteQRPNG() : nil
            let img: NSImage? = qrData.flatMap { NSImage(data: $0) }
            await MainActor.run {
                webRemoteEnabled = info?.enabled ?? false
                webRemoteCode = info?.code ?? ""
                webRemoteURL = info?.url ?? ""
                webRemoteQRImage = img
            }
        }
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
