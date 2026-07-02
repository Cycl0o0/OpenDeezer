import SwiftUI
import UIKit

/// Sleep-timer choices offered in Settings. Raw value is the minute count;
/// `.off` cancels and `.endOfTrack` stops when the current track finishes.
private enum SleepOption: Int, CaseIterable, Identifiable {
    case off = 0
    case min15 = 15
    case min30 = 30
    case min45 = 45
    case min60 = 60
    case endOfTrack = -1

    var id: Int { rawValue }
    var label: String {
        switch self {
        case .off: return "Off"
        case .endOfTrack: return "End of Track"
        default: return "\(rawValue) min"
        }
    }
}

struct SettingsView: View {
    @EnvironmentObject private var session: SessionStore
    @EnvironmentObject private var updates: UpdateStore
    @StateObject private var hosts = RemoteHostStore.shared
    @Environment(\.dismiss) private var dismiss

    @State private var quality = AudioPrefs.quality
    @State private var gapless = AudioPrefs.gapless
    @State private var replayGain = AudioPrefs.replayGain
    @State private var crossfadeMs = Double(AudioPrefs.crossfadeMs)
    @State private var sleepOption: SleepOption = .off

    @State private var remoteInfo: WebRemoteInfo?
    @State private var qrImage: UIImage?
    @State private var connectInfo: ConnectHostInfo?

    private let qualities = [
        (0, "Normal", "MP3 · 128 kbps"),
        (1, "High", "MP3 · 320 kbps"),
        (2, "HiFi", "HiFi · FLAC"),
    ]

    var body: some View {
        NavigationStack {
            List {
                if let account = session.account {
                    Section {
                        HStack {
                            VStack(alignment: .leading, spacing: 2) {
                                Text(account.name).font(.headline)
                                Text(account.offer).font(.caption).foregroundStyle(.secondary)
                            }
                            Spacer()
                        }
                    }
                }

                Section("Audio Quality") {
                    ForEach(qualities, id: \.0) { level, name, detail in
                        Button {
                            quality = level
                            AudioPrefs.quality = level
                            Engine.setQuality(level)
                        } label: {
                            HStack {
                                VStack(alignment: .leading) {
                                    Text(name).foregroundStyle(.primary)
                                    Text(detail).font(.caption).foregroundStyle(.secondary)
                                }
                                Spacer()
                                if quality == level {
                                    Image(systemName: "checkmark").foregroundStyle(Palette.accent)
                                }
                            }
                        }
                    }
                }

                Section("Playback") {
                    Toggle("Gapless Playback", isOn: $gapless)
                        .onChange(of: gapless) { _, value in AudioPrefs.gapless = value; Engine.setGapless(value) }
                    Toggle("ReplayGain", isOn: $replayGain)
                        .onChange(of: replayGain) { _, value in AudioPrefs.replayGain = value; Engine.setReplayGain(value) }
                    VStack(alignment: .leading) {
                        HStack {
                            Text("Crossfade")
                            Spacer()
                            Text(crossfadeMs == 0 ? "Off" : "\(Int(crossfadeMs / 1000))s")
                                .foregroundStyle(.secondary)
                        }
                        Slider(value: $crossfadeMs, in: 0...12000, step: 1000)
                            .tint(Palette.accent)
                            .onChange(of: crossfadeMs) { _, value in AudioPrefs.crossfadeMs = Int(value); Engine.setCrossfadeMS(Int(value)) }
                    }
                    NavigationLink("Equalizer") { EqualizerView() }
                }

                Section {
                    Picker("Sleep Timer", selection: $sleepOption) {
                        ForEach(SleepOption.allCases) { option in
                            Text(option.label).tag(option)
                        }
                    }
                    .onChange(of: sleepOption) { _, value in applySleep(value) }
                } header: {
                    Text("Sleep Timer")
                } footer: {
                    Text("Pause playback after a set time (with a gentle fade-out) or when the current track ends.")
                }

                Section {
                    Toggle("OpenDeezer Connect", isOn: $hosts.connectHostEnabled)
                        .onChange(of: hosts.connectHostEnabled) { _, _ in
                            Task { await refreshConnect() }
                        }
                    if hosts.connectHostEnabled, let info = connectInfo, info.enabled {
                        HStack {
                            Label("Discoverable as", systemImage: "dot.radiowaves.left.and.right")
                                .foregroundStyle(.secondary)
                            Spacer()
                            Text(info.name.isEmpty ? info.addr : info.name)
                                .font(.footnote)
                                .foregroundStyle(.secondary)
                                .textSelection(.enabled)
                        }
                        Text(info.addr)
                            .font(.caption.monospaced())
                            .foregroundStyle(.secondary)
                            .textSelection(.enabled)
                    }
                } header: {
                    Text("OpenDeezer Connect")
                } footer: {
                    Text("Let your other OpenDeezer devices (same Deezer account) find this iPhone and control its playback over the local network.")
                }

                Section {
                    Toggle("Phone Remote", isOn: $hosts.phoneRemoteEnabled)
                        .onChange(of: hosts.phoneRemoteEnabled) { _, _ in
                            Task { await refreshRemote() }
                        }
                    if hosts.phoneRemoteEnabled, let info = remoteInfo, info.enabled {
                        VStack(spacing: 10) {
                            if let qrImage {
                                Image(uiImage: qrImage)
                                    .interpolation(.none)
                                    .resizable()
                                    .frame(width: 160, height: 160)
                            }
                            Text(info.code)
                                .font(.title3.monospaced().weight(.bold))
                            Text(info.url)
                                .font(.caption)
                                .foregroundStyle(.secondary)
                                .textSelection(.enabled)
                        }
                        .frame(maxWidth: .infinity)
                        .padding(.vertical, 8)
                    }
                } header: {
                    Text("Phone Remote")
                } footer: {
                    Text("Control playback from a browser on the same network — scan the QR or open the URL.")
                }

                Section {
                    Button {
                        Task { await updates.checkNow() }
                    } label: {
                        HStack {
                            Text("Check for Updates")
                            Spacer()
                            if updates.isChecking {
                                ProgressView()
                            } else if let info = updates.info {
                                Text(info.hasUpdate ? "\(info.latest) available" : "Up to date")
                                    .foregroundStyle(.secondary)
                            }
                        }
                    }
                    if let info = updates.info, info.hasUpdate {
                        Button("Download \(info.latest)") {
                            if let url = URL(string: info.url) { UIApplication.shared.open(url) }
                        }
                    }
                }

                Section {
                    Button("Log Out", role: .destructive) {
                        session.logout()
                        dismiss()
                    }
                }
            }
            .navigationTitle("Settings")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .topBarTrailing) {
                    Button("Done") { dismiss() }
                }
            }
            .task {
                syncSleep()
                await refreshRemote()
                await refreshConnect()
            }
        }
    }

    /// Arm or cancel the engine's sleep timer for the chosen preset.
    private func applySleep(_ option: SleepOption) {
        switch option {
        case .off: Engine.cancelSleepTimer()
        case .endOfTrack: Engine.setSleepTimer(minutes: 0, endOfTrack: true)
        default: Engine.setSleepTimer(minutes: option.rawValue, endOfTrack: false)
        }
    }

    /// Reflect the engine's current sleep state in the picker when Settings
    /// opens. An active minutes timer can't be mapped back to an exact preset,
    /// so the current selection is left untouched in that case.
    private func syncSleep() {
        if Engine.sleepEndOfTrack() {
            sleepOption = .endOfTrack
        } else if !Engine.sleepActive() {
            sleepOption = .off
        }
    }

    private func refreshRemote() async {
        let info = await Engine.webRemoteInfo()
        remoteInfo = info
        if let data = await Engine.webRemoteQRPNG(), let image = UIImage(data: data) {
            qrImage = image
        } else {
            qrImage = nil
        }
    }

    private func refreshConnect() async {
        connectInfo = await Engine.connectHostInfo()
    }
}
