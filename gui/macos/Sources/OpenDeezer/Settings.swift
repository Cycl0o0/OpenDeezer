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

    // Remote control (control API) state — read from DZControlConfigJSON on
    // appear, applied via DZSetControlConfig only on explicit user changes
    // (applying restarts the server, so it must never fire programmatically).
    // controlAddr preserves a custom address configured elsewhere (TUI/config);
    // it is only recomputed when the user actually flips the LAN toggle.
    @State private var controlEnabled = false
    @State private var controlLAN = false
    @State private var controlAddr = ""
    @State private var controlToken = ""

    // Download folder — engine-owned (single source of truth in config, not in
    // settings.json). Loaded from Core.downloadDir() on appear; changed via the
    // Choose… panel below. Empty means the engine's default folder.
    @State private var downloadFolder = ""

    // Free-tier ads / play-reporting opt-out — engine-owned, shown only for a
    // Deezer Free account (premium plans have no ads). Loaded on appear.
    @State private var adsDisabled = false

    // On-disk raw-stream cache budget in MB (engine-owned, media.json; 0 = off).
    // Loaded on appear; a change applies at the next launch (the cache is
    // attached to the player once at startup).
    @State private var mediaCacheMB = 0
    private let mediaCachePresets = [128, 256, 512, 1024, 2048]

    // Sleep timer remaining, refreshed once a second while the sheet is open.
    // The armed mode itself lives in AppState (app.sleepMode); the engine owns
    // the countdown, so we only mirror the "12:34" display here.
    @State private var sleepRemaining = ""
    private let sleepTick = Timer.publish(every: 1, on: .main, in: .common).autoconnect()

    // Equalizer state (engine-owned, persisted in ~/.config/opendeezer/eq.json).
    // Re-read on appear: another client (TUI, phone remote) may have changed it.
    // Band frequencies and preset names come from the engine so they're never
    // hardcoded twice; the defaults below only render until the first load.
    @State private var eqEnabled = false
    @State private var eqMono = false
    @State private var eqPreset = "flat"
    @State private var eqGains = [Double](repeating: 0, count: 10)
    @State private var eqPreamp = 0.0
    @State private var eqBands: [Double] = [31.5, 63, 125, 250, 500,
                                            1000, 2000, 4000, 8000, 16000]
    @State private var eqPresets = ["flat"]
    // Coalesces continuous Slider drags into at most one in-flight engine call
    // (same shape as AppState.flushVolume). No client-side saving: the engine
    // debounces disk persistence itself.
    @State private var pendingEQ: (@Sendable () -> Void)? = nil
    @State private var eqSendInFlight = false

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack(spacing: 10) {
                Image(systemName: "gearshape.fill")
                    .font(.system(size: 22)).foregroundStyle(DZ.accent)
                Text(L("Settings"))
                    .font(.system(size: 22, weight: .bold)).foregroundStyle(DZ.textPri)
                Spacer()
            }
            .padding(.bottom, 18)

            ScrollView {
              VStack(alignment: .leading, spacing: 0) {
            // Audio quality
            settingsCard {
                VStack(alignment: .leading, spacing: 10) {
                    Label(L("Audio Quality"), systemImage: "waveform")
                        .font(.system(size: 13, weight: .semibold)).foregroundStyle(DZ.textPri)
                    Picker("", selection: Binding(
                        get: { app.settings.quality },
                        set: { app.setQuality($0) })) {
                        Text(L("Normal · MP3 128")).tag(0)
                        Text(L("High · MP3 320")).tag(1)
                        Text(L("HiFi · FLAC lossless")).tag(2)
                    }
                    .pickerStyle(.segmented)
                    .labelsHidden()
                    Text(L("Applied immediately and on next launch."))
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
                    Label(L("Output Device"), systemImage: "hifispeaker.fill")
                        .font(.system(size: 13, weight: .semibold)).foregroundStyle(DZ.textPri)
                    Picker("", selection: Binding(
                        get: { app.currentAudioDeviceID },
                        set: { app.setAudioDevice($0) })) {
                        // The engine reports "" as the system default device.
                        if !app.audioDevices.contains(where: { $0.id == app.currentAudioDeviceID }) {
                            Text(L("System Default")).tag(app.currentAudioDeviceID)
                        }
                        ForEach(app.audioDevices) { d in
                            Text(d.isDefault ? Lf("%@ (System Default)", d.name) : d.name).tag(d.id)
                        }
                    }
                    .labelsHidden()
                    Text(L("Choose where audio plays. Switching takes effect on the next track or seek."))
                        .font(.caption).foregroundStyle(DZ.textSec)
                }
            }

            // Gapless playback
            settingsCard {
                Toggle(isOn: Binding(
                    get: { app.settings.gapless },
                    set: { app.setGapless($0) })) {
                    VStack(alignment: .leading, spacing: 2) {
                        Label(L("Gapless playback"), systemImage: "forward.end.alt.fill")
                            .font(.system(size: 13, weight: .semibold)).foregroundStyle(DZ.textPri)
                        Text(L("Preloads the next track so albums play with no silence between songs."))
                            .font(.caption).foregroundStyle(DZ.textSec)
                    }
                }
                .toggleStyle(.switch)
                .tint(DZ.accent)
            }

            // Crossfade
            settingsCard {
                VStack(alignment: .leading, spacing: 10) {
                    Label(L("Crossfade"), systemImage: "wave.3.forward")
                        .font(.system(size: 13, weight: .semibold)).foregroundStyle(DZ.textPri)
                    Picker("", selection: Binding(
                        get: { app.settings.crossfadeMS },
                        set: { app.setCrossfadeMS($0) })) {
                        Text(L("Off")).tag(0)
                        Text("3s").tag(3000)
                        Text("6s").tag(6000)
                        Text("12s").tag(12000)
                    }
                    .pickerStyle(.segmented)
                    .labelsHidden()
                    Text(L("Fades the end of one track into the start of the next."))
                        .font(.caption).foregroundStyle(DZ.textSec)
                }
            }

            // Sleep timer
            settingsCard {
                VStack(alignment: .leading, spacing: 10) {
                    Label(L("Sleep timer"), systemImage: "moon.zzz.fill")
                        .font(.system(size: 13, weight: .semibold)).foregroundStyle(DZ.textPri)
                    Picker("", selection: Binding(
                        get: { app.sleepMode },
                        set: { app.setSleepMode($0); updateSleepRemaining() })) {
                        Text(L("Off")).tag(0)
                        Text(Lf("%d min", 15)).tag(15)
                        Text(Lf("%d min", 30)).tag(30)
                        Text(Lf("%d min", 45)).tag(45)
                        Text(Lf("%d min", 60)).tag(60)
                        Text(L("End of track")).tag(-1)
                    }
                    .pickerStyle(.menu)
                    .labelsHidden()
                    Text(sleepTimerNote)
                        .font(.caption).foregroundStyle(DZ.textSec)
                }
            }

            // Volume normalization (ReplayGain)
            settingsCard {
                Toggle(isOn: Binding(
                    get: { app.replayGain },
                    set: { app.setReplayGain($0) })) {
                    VStack(alignment: .leading, spacing: 2) {
                        Label(L("Volume normalization"), systemImage: "speaker.wave.2.fill")
                            .font(.system(size: 13, weight: .semibold)).foregroundStyle(DZ.textPri)
                        Text(L("Evens out loudness differences between tracks (ReplayGain)."))
                            .font(.caption).foregroundStyle(DZ.textSec)
                    }
                }
                .toggleStyle(.switch)
                .tint(DZ.accent)
            }

            // Equalizer — 10-band graphic EQ applied by the shared engine.
            // The engine owns the state; slider drags call set-band live and
            // any manual band edit flips the preset to Custom (engine-side).
            settingsCard {
                VStack(alignment: .leading, spacing: 10) {
                    Toggle(isOn: Binding(
                        get: { eqEnabled },
                        set: { on in
                            eqEnabled = on
                            Task.detached { Core.setEQEnabled(on) }
                        })) {
                        Label(L("Equalizer"), systemImage: "slider.vertical.3")
                            .font(.system(size: 13, weight: .semibold)).foregroundStyle(DZ.textPri)
                    }
                    .toggleStyle(.switch)
                    .tint(DZ.accent)

                    Picker("", selection: Binding(
                        get: { eqPreset },
                        set: { selectEQPreset($0) })) {
                        ForEach(eqPresets, id: \.self) { p in
                            Text(eqPresetLabel(p)).tag(p)
                        }
                        // Display-only entry the selection lands on after a
                        // manual band edit; selecting it applies nothing.
                        Text(L("Custom")).tag("custom")
                    }
                    .pickerStyle(.menu)
                    .labelsHidden()
                    .disabled(!eqEnabled)

                    HStack(alignment: .bottom, spacing: 4) {
                        ForEach(0..<10, id: \.self) { i in
                            eqBandColumn(i)
                        }
                    }
                    .frame(maxWidth: .infinity)
                    .disabled(!eqEnabled)
                    .opacity(eqEnabled ? 1 : 0.45)

                    HStack(spacing: 8) {
                        Text(L("Preamp")).font(.system(size: 13)).foregroundStyle(DZ.textPri)
                        Slider(value: Binding(
                            get: { eqPreamp },
                            set: { v in
                                eqPreamp = v
                                sendEQ { Core.setEQPreamp(v) }
                            }), in: -12...12)
                            .tint(DZ.accent)
                        Text(dbLabel(eqPreamp) + " dB")
                            .font(.system(size: 11, design: .monospaced))
                            .foregroundStyle(DZ.textSec)
                            .frame(width: 56, alignment: .trailing)
                    }
                    .disabled(!eqEnabled)
                    .opacity(eqEnabled ? 1 : 0.45)
                }
            }

            // Mono audio — engine-side downmix, independent of the EQ switch.
            settingsCard {
                Toggle(isOn: Binding(
                    get: { eqMono },
                    set: { on in
                        eqMono = on
                        Task.detached { Core.setEQMono(on) }
                    })) {
                    VStack(alignment: .leading, spacing: 2) {
                        Label(L("Mono audio"), systemImage: "speaker.wave.1.fill")
                            .font(.system(size: 13, weight: .semibold)).foregroundStyle(DZ.textPri)
                        Text(L("Plays both channels as one — helpful with a single speaker or hearing in one ear."))
                            .font(.caption).foregroundStyle(DZ.textSec)
                    }
                }
                .toggleStyle(.switch)
                .tint(DZ.accent)
            }

            // Downloads — the folder is owned by the engine (single source of
            // truth in ~/.config/opendeezer), so we read/write it via Core and
            // never persist a copy in settings.json. Downloads are premium-only.
            settingsCard {
                VStack(alignment: .leading, spacing: 10) {
                    Label(L("Downloads"), systemImage: "arrow.down.circle")
                        .font(.system(size: 13, weight: .semibold)).foregroundStyle(DZ.textPri)
                    Text(L("Where downloaded tracks are saved."))
                        .font(.caption).foregroundStyle(DZ.textSec)
                    HStack(spacing: 8) {
                        Text(downloadFolder.isEmpty ? L("Default folder") : downloadFolder)
                            .font(.system(size: 12, design: .monospaced))
                            .foregroundStyle(DZ.textSec)
                            .lineLimit(1).truncationMode(.middle)
                            .textSelection(.enabled)
                            .frame(maxWidth: .infinity, alignment: .leading)
                        Button(L("Choose…")) { chooseDownloadFolder() }
                            .buttonStyle(.glass).tint(DZ.accent)
                    }
                    if !app.isPremium {
                        Label(L("Requires a paid Deezer plan"),
                              systemImage: "exclamationmark.triangle.fill")
                            .font(.caption).foregroundStyle(DZ.accentMag)
                    }
                }
            }

            // Stream cache — engine-owned on-disk cache for streamed audio
            // (media.json; 0 = off). Changing it takes effect at the next launch
            // (the cache is attached to the player once, before playback).
            settingsCard {
                VStack(alignment: .leading, spacing: 10) {
                    Label(L("Stream cache"), systemImage: "internaldrive")
                        .font(.system(size: 13, weight: .semibold)).foregroundStyle(DZ.textPri)
                    Picker("", selection: Binding(
                        get: { mediaCacheMB },
                        set: { v in
                            mediaCacheMB = v
                            Task.detached { _ = Core.setMediaCacheMB(v) }
                        })) {
                        Text(L("Off")).tag(0)
                        // Surface a custom value loaded from the engine (set via
                        // TUI/config) that isn't one of the presets.
                        if mediaCacheMB != 0 && !mediaCachePresets.contains(mediaCacheMB) {
                            Text(Lf("%d MB", mediaCacheMB)).tag(mediaCacheMB)
                        }
                        ForEach(mediaCachePresets, id: \.self) { mb in
                            Text(Lf("%d MB", mb)).tag(mb)
                        }
                    }
                    .pickerStyle(.menu)
                    .labelsHidden()
                    Text(L("On-disk cache for streamed audio (0 = off). Applies at next launch."))
                        .font(.caption).foregroundStyle(DZ.textSec)
                }
            }

            // Disable ads — Deezer Free only (premium plans carry no ads). The
            // opt-out is engine-owned; loading on appear and applied immediately.
            if !app.isPremium {
                settingsCard {
                    VStack(alignment: .leading, spacing: 10) {
                        Toggle(isOn: Binding(
                            get: { adsDisabled },
                            set: { on in
                                adsDisabled = on
                                Task.detached { Core.setAdsDisabled(on) }
                            })) {
                            Label(L("Disable ads"), systemImage: "megaphone.slash")
                                .font(.system(size: 13, weight: .semibold)).foregroundStyle(DZ.textPri)
                        }
                        .toggleStyle(.switch)
                        .tint(DZ.accent)
                        Text(L("Deezer Free is ad-supported. Reporting your plays — like the official app — credits artists and drives the ads. Disabling this removes ads but stops reporting your plays, which denies artists their play count and breaks Deezer's terms of use. Use at your own risk."))
                            .font(.caption).foregroundStyle(DZ.textSec)
                    }
                }
            }

            // Background playback
            settingsCard {
                Toggle(isOn: Binding(
                    get: { app.settings.closeToTray },
                    set: { app.setCloseToTray($0) })) {
                    VStack(alignment: .leading, spacing: 2) {
                        Label(L("Keep playing in background"), systemImage: "menubar.arrow.up.rectangle")
                            .font(.system(size: 13, weight: .semibold)).foregroundStyle(DZ.textPri)
                        Text(L("Closing the window hides it to the menu bar instead of quitting."))
                            .font(.caption).foregroundStyle(DZ.textSec)
                    }
                }
                .toggleStyle(.switch)
                .tint(DZ.accent)
            }

            // Remote control — the control API (used by the phone/web remote and
            // by other OpenDeezer clients on the network) plus the Phone Remote
            // pairing flow, which runs on the same server.
            settingsCard {
                VStack(alignment: .leading, spacing: 10) {
                    Label(L("Remote control"), systemImage: "antenna.radiowaves.left.and.right")
                        .font(.system(size: 13, weight: .semibold)).foregroundStyle(DZ.textPri)
                    Text(L("Lets other devices control playback over the network."))
                        .font(.caption).foregroundStyle(DZ.textSec)

                    Toggle(isOn: Binding(
                        get: { controlEnabled },
                        set: { on in
                            controlEnabled = on
                            applyControlConfig()
                        })) {
                        Text(L("Enable")).font(.system(size: 13)).foregroundStyle(DZ.textPri)
                    }
                    .toggleStyle(.switch)
                    .tint(DZ.accent)

                    Toggle(isOn: Binding(
                        get: { controlLAN },
                        set: { on in
                            controlLAN = on
                            controlAddr = on ? ":7654" : ""
                            applyControlConfig()
                        })) {
                        Text(L("Allow on local network (LAN)"))
                            .font(.system(size: 13)).foregroundStyle(DZ.textPri)
                    }
                    .toggleStyle(.switch)
                    .tint(DZ.accent)
                    .disabled(!controlEnabled)

                    HStack(spacing: 8) {
                        Text(L("Access token")).font(.system(size: 13)).foregroundStyle(DZ.textPri)
                        TextField(L("None"), text: Binding(
                            get: { controlToken },
                            set: { controlToken = $0 }))
                            .textFieldStyle(.roundedBorder)
                            .onSubmit { applyControlConfig() }
                    }
                    .disabled(!controlEnabled)
                    // No .onChange here: it would fire when loadControlConfig()
                    // populates the field (silently restarting the server and
                    // clobbering the config) and on every keystroke. The token
                    // applies on submit (Enter) instead.

                    Divider().overlay(DZ.hairline).padding(.vertical, 2)

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
                        Label(L("Phone Remote"), systemImage: "iphone.radiowaves.left.and.right")
                            .font(.system(size: 13, weight: .semibold)).foregroundStyle(DZ.textPri)
                    }
                    .toggleStyle(.switch)
                    .tint(DZ.accent)

                    Text(L("Scan with your phone (same Wi-Fi), then enter the code."))
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
                Text(Lf("Stored in %@", "~/.config/opendeezer/settings.json"))
                    .font(.caption2).foregroundStyle(DZ.textSec)
                Spacer()
                Button(L("Done")) { app.showSettings = false }
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
            loadControlConfig()
            loadEQState()
            loadDownloadFolder()
            loadAdsDisabled()
            loadMediaCache()
            updateSleepRemaining()
        }
        .onReceive(sleepTick) { _ in updateSleepRemaining() }
    }

    // MARK: equalizer

    // One EQ band: dB readout on top, a rotated vertical slider, Hz label
    // below. The inner frame's width becomes the vertical run; the outer frame
    // reserves the rotated footprint so the HStack lays the columns out evenly.
    @ViewBuilder
    private func eqBandColumn(_ i: Int) -> some View {
        VStack(spacing: 4) {
            Text(dbLabel(eqGains[i]))
                .font(.system(size: 9, design: .monospaced)).foregroundStyle(DZ.textSec)
            Slider(value: eqGainBinding(i), in: -12...12)
                .tint(DZ.accent)
                .frame(width: 96)
                .rotationEffect(.degrees(-90))
                .frame(width: 24, height: 96)
            Text(hzLabel(eqBands[i]))
                .font(.system(size: 9)).foregroundStyle(DZ.textSec)
        }
        .frame(maxWidth: .infinity)
    }

    // Gain binding for band i with a soft 0 dB detent (values inside ±0.5 dB
    // snap to flat, like a hardware EQ's center notch). A manual edit shows the
    // preset as Custom immediately; the engine flips its own state to match.
    private func eqGainBinding(_ i: Int) -> Binding<Double> {
        Binding(
            get: { eqGains[i] },
            set: { v in
                let db = abs(v) < 0.5 ? 0 : v
                guard eqGains[i] != db else { return }
                eqGains[i] = db
                eqPreset = "custom"
                sendEQ { Core.setEQBand(i, gainDb: db) }
            })
    }

    // Applies a preset, then re-reads the engine state so all sliders jump to
    // the preset's curve. "Custom" is the display-only entry — nothing to apply.
    private func selectEQPreset(_ name: String) {
        eqPreset = name
        guard name != "custom" else { return }
        Task.detached {
            Core.setEQPreset(name)
            let st = Core.eqState()
            await MainActor.run {
                guard let st else { return }
                eqGains = st.gainsDb
                eqPreset = st.preset
            }
        }
    }

    // Sends at most one EQ engine call at a time, always ending on the latest
    // value — Slider fires continuously during a drag (same shape as
    // AppState.flushVolume). Persistence is debounced engine-side.
    private func sendEQ(_ call: @escaping @Sendable () -> Void) {
        pendingEQ = call
        flushEQ()
    }

    private func flushEQ() {
        guard !eqSendInFlight, let call = pendingEQ else { return }
        pendingEQ = nil
        eqSendInFlight = true
        Task.detached {
            call()
            await MainActor.run {
                eqSendInFlight = false
                flushEQ()
            }
        }
    }

    // Populate the Equalizer card from the engine's current state (another
    // client may have changed it since the sheet was last open).
    private func loadEQState() {
        Task.detached {
            let st = Core.eqState()
            await MainActor.run {
                guard let st else { return }
                eqEnabled = st.enabled
                eqMono = st.mono
                eqPreamp = st.preampDb
                eqPreset = st.preset
                if st.gainsDb.count == eqGains.count { eqGains = st.gainsDb }
                if st.bands.count == eqBands.count { eqBands = st.bands }
                if !st.presets.isEmpty { eqPresets = st.presets }
            }
        }
    }

    // "bass-boost" -> "Bass Boost" for the preset menu.
    private func eqPresetLabel(_ name: String) -> String {
        name.split(separator: "-")
            .map { $0.prefix(1).uppercased() + $0.dropFirst() }
            .joined(separator: " ")
    }

    // 31.5 -> "31", 250 -> "250", 16000 -> "16k".
    private func hzLabel(_ hz: Double) -> String {
        hz >= 1000 ? "\(Int(hz / 1000))k" : "\(Int(hz))"
    }

    // Signed one-decimal gain, with flat shown as a plain "0".
    private func dbLabel(_ db: Double) -> String {
        db == 0 ? "0" : String(format: "%+.1f", db)
    }

    // Caption under the sleep-timer picker: shows the live countdown while a
    // minutes-based timer runs, otherwise a short description of the choice.
    private var sleepTimerNote: String {
        if app.sleepMode == -1 {
            return L("Playback pauses when the current track ends.")
        } else if app.sleepMode > 0 {
            return sleepRemaining.isEmpty
                ? Lp("Fades out and pauses after %d minutes.", app.sleepMode)
                : Lf("Fades out and pauses in %@.", sleepRemaining)
        }
        return L("Automatically pause playback after a set time.")
    }

    // Refresh the "12:34" remaining string from the engine (minutes mode only).
    private func updateSleepRemaining() {
        guard app.sleepMode > 0, Core.sleepActive() else { sleepRemaining = ""; return }
        let ms = Core.sleepRemainingMS()
        guard ms > 0 else { sleepRemaining = ""; return }
        let secs = Int(ms / 1000)
        sleepRemaining = String(format: "%d:%02d", secs / 60, secs % 60)
    }

    // MARK: downloads

    // Read the engine's current download folder off the main thread.
    private func loadDownloadFolder() {
        Task.detached {
            let dir = Core.downloadDir()
            await MainActor.run { downloadFolder = dir }
        }
    }

    // Read the engine's on-disk stream-cache budget (media.json) off the main
    // thread so the picker shows the persisted value.
    private func loadMediaCache() {
        Task.detached {
            let mb = Core.mediaCacheMB()
            await MainActor.run { mediaCacheMB = mb }
        }
    }

    // Read the engine's free-tier ads opt-out (Deezer Free only) off the main
    // thread. Skipped for premium accounts, which never show the toggle.
    private func loadAdsDisabled() {
        guard !app.isPremium else { return }
        Task.detached {
            let on = Core.adsDisabled()
            await MainActor.run { adsDisabled = on }
        }
    }

    // Pick a new download folder; persist it in the engine and refresh the row.
    private func chooseDownloadFolder() {
        let panel = NSOpenPanel()
        panel.canChooseDirectories = true
        panel.canChooseFiles = false
        panel.allowsMultipleSelection = false
        panel.prompt = L("Choose")
        if !downloadFolder.isEmpty {
            panel.directoryURL = URL(fileURLWithPath: downloadFolder)
        }
        guard panel.runModal() == .OK, let url = panel.url else { return }
        if Core.setDownloadDir(url.path) {
            downloadFolder = Core.downloadDir()
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

    // Populate the Remote control toggles from the engine's current config.
    // Only sets @State directly — must never call applyControlConfig(), which
    // would restart the running server (and drop web-remote pairing) just for
    // opening the sheet.
    private func loadControlConfig() {
        Task.detached {
            let cfg = Core.controlConfig()
            await MainActor.run {
                controlEnabled = cfg?.enabled ?? false
                controlLAN = cfg?.lan ?? false
                controlAddr = cfg?.addr ?? ""
                controlToken = cfg?.token ?? ""
            }
        }
    }

    // Persists + applies the Remote control settings on a user change. The addr
    // is the loaded one unless the LAN toggle was flipped (":7654" = all
    // interfaces, "" = localhost only), so a custom address survives edits.
    private func applyControlConfig() {
        Core.setControlConfig(enabled: controlEnabled,
                              addr: controlAddr,
                              token: controlToken)
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
