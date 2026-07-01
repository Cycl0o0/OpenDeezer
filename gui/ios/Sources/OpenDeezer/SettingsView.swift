import SwiftUI
import UIKit

struct SettingsView: View {
    @EnvironmentObject private var session: SessionStore
    @EnvironmentObject private var updates: UpdateStore
    @Environment(\.dismiss) private var dismiss

    @State private var quality = Engine.quality()
    @State private var gapless = Engine.gapless()
    @State private var replayGain = Engine.replayGain()
    @State private var crossfadeMs = Double(Engine.crossfadeMS())

    @State private var remoteEnabled = false
    @State private var remoteInfo: WebRemoteInfo?
    @State private var qrImage: UIImage?

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
                        .onChange(of: gapless) { _, value in Engine.setGapless(value) }
                    Toggle("ReplayGain", isOn: $replayGain)
                        .onChange(of: replayGain) { _, value in Engine.setReplayGain(value) }
                    VStack(alignment: .leading) {
                        HStack {
                            Text("Crossfade")
                            Spacer()
                            Text(crossfadeMs == 0 ? "Off" : "\(Int(crossfadeMs / 1000))s")
                                .foregroundStyle(.secondary)
                        }
                        Slider(value: $crossfadeMs, in: 0...12000, step: 1000)
                            .tint(Palette.accent)
                            .onChange(of: crossfadeMs) { _, value in Engine.setCrossfadeMS(Int(value)) }
                    }
                }

                Section {
                    Toggle("Phone Remote", isOn: $remoteEnabled)
                        .onChange(of: remoteEnabled) { _, value in
                            Engine.webRemoteSetEnabled(value)
                            Task { await refreshRemote() }
                        }
                    if remoteEnabled, let info = remoteInfo, info.enabled {
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
            .task { await refreshRemote() }
        }
    }

    private func refreshRemote() async {
        let info = await Engine.webRemoteInfo()
        remoteInfo = info
        remoteEnabled = info?.enabled ?? false
        if let data = await Engine.webRemoteQRPNG(), let image = UIImage(data: data) {
            qrImage = image
        } else {
            qrImage = nil
        }
    }
}
