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
        case .off: return String(localized: "Off")
        case .endOfTrack: return String(localized: "End of Track")
        default: return String(localized: "\(rawValue) min")
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

    // Engine-owned, loaded on appear: the shared download folder and the
    // Free-tier ads / play-reporting opt-out (shown only for a Free account).
    @State private var downloadFolder = ""
    @State private var adsDisabled = false

    private let qualities: [(Int, LocalizedStringKey, String)] = [
        (0, "Normal", "MP3 · 128 kbps"),
        (1, "High", "MP3 · 320 kbps"),
        (2, "HiFi", "HiFi · FLAC"),
    ]

    /// A paid plan (Premium / Family / HiFi). Downloads and the HiFi/HQ tiers
    /// need one; Free streams full-length at standard quality (128 kbps) and is
    /// the only tier that sees the ads opt-out.
    private var isPremium: Bool { session.account?.premium ?? false }

    var body: some View {
        NavigationStack {
            List {
                if let account = session.account {
                    Section {
                        HStack {
                            VStack(alignment: .leading, spacing: 2) {
                                Text(account.name).font(.headline)
                                Text(account.offer).font(.caption).foregroundStyle(.secondary)
                                // Free accounts stream full-length at standard
                                // quality (128 kbps); HiFi/HQ need a paid plan.
                                if !isPremium {
                                    Text("Free account · standard quality (128 kbps)")
                                        .font(.caption2)
                                        .foregroundStyle(Palette.accent)
                                }
                            }
                            Spacer()
                        }
                    }
                }

                Section("Audio Quality") {
                    ForEach(qualities, id: \.0) { level, name, detail in
                        Button {
                            guard canUseQuality(level) else { return }
                            quality = level
                            AudioPrefs.quality = level
                            Engine.setQuality(level)
                        } label: {
                            HStack {
                                VStack(alignment: .leading) {
                                    Text(name).foregroundStyle(.primary)
                                    Text(detail).font(.caption).foregroundStyle(.secondary)
                                    if !canUseQuality(level) {
                                        Text(isPremium
                                             ? String(localized: "Not available with your plan")
                                             : String(localized: "Requires a paid Deezer plan"))
                                            .font(.caption2)
                                            .foregroundStyle(.secondary)
                                    }
                                }
                                Spacer()
                                if !canUseQuality(level) {
                                    Image(systemName: "lock.fill").foregroundStyle(.secondary)
                                } else if quality == level {
                                    Image(systemName: "checkmark").foregroundStyle(Palette.accent)
                                }
                            }
                        }
                        .disabled(!canUseQuality(level))
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
                            Text(crossfadeMs == 0 ? String(localized: "Off") : String(localized: "\(Int(crossfadeMs / 1000))s"))
                                .foregroundStyle(.secondary)
                        }
                        Slider(value: $crossfadeMs, in: 0...12000, step: 1000)
                            .tint(Palette.accent)
                            .accessibilityLabel("Crossfade")
                            .accessibilityValue(crossfadeMs == 0
                                                ? String(localized: "Off")
                                                : String(localized: "\(Int(crossfadeMs / 1000))s"))
                            .onChange(of: crossfadeMs) { _, value in AudioPrefs.crossfadeMs = Int(value); Engine.setCrossfadeMS(Int(value)) }
                    }
                    NavigationLink("Equalizer") { EqualizerView() }
                }

                // Downloads — premium-only, so shown only for a paid plan. The
                // folder is engine-owned; iOS sandboxing keeps it inside the app,
                // so it's read-only here (no picker) with the caveat spelled out.
                if isPremium {
                    Section {
                        Text(downloadFolder.isEmpty ? String(localized: "Default folder") : downloadFolder)
                            .font(.footnote.monospaced())
                            .foregroundStyle(.secondary)
                            .lineLimit(2)
                            .truncationMode(.middle)
                            .textSelection(.enabled)
                    } header: {
                        Text("Downloads")
                    } footer: {
                        Text("On iOS, tracks are saved inside OpenDeezer and available in the Files app. Choosing another folder isn't supported.")
                    }
                }

                // Disable ads — Deezer Free only (paid plans carry no ads). The
                // opt-out is engine-owned; loaded on appear, applied immediately.
                if !isPremium {
                    Section {
                        Toggle(isOn: $adsDisabled) {
                            Label("Disable ads", systemImage: "megaphone.slash")
                        }
                        .onChange(of: adsDisabled) { _, value in
                            Task { await Engine.setAdsDisabled(value) }
                        }
                    } footer: {
                        Text("Deezer Free is ad-supported. Reporting your plays — like the official app — credits artists and drives the ads. Disabling this removes ads but stops reporting your plays, which denies artists their play count and breaks Deezer's terms of use. Use at your own risk.")
                    }
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
                                    .accessibilityHidden(true)
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
                                Text(info.hasUpdate ? String(localized: "\(info.latest) available") : String(localized: "Up to date"))
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
                if isPremium {
                    downloadFolder = await Engine.downloadDir()
                } else {
                    adsDisabled = await Engine.adsDisabled()
                }
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

    private func canUseQuality(_ level: Int) -> Bool {
        guard let account = session.account else { return true }
        switch level {
        case 0: return true
        case 1: return account.canHq
        case 2: return account.canHifi
        default: return false
        }
    }
}
