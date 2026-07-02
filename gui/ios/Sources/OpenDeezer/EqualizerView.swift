import SwiftUI

/// 10-band graphic equalizer + mono downmix, pushed from Settings. All DSP
/// state lives in the engine — which persists it itself and flips the preset
/// to "custom" on any manual band edit — so this view only renders controls,
/// forwards changes, and re-reads state when it opens (another client may
/// have changed it).
struct EqualizerView: View {
    @State private var enabled = false
    @State private var mono = false
    @State private var preset = "flat"
    @State private var presets: [String] = []
    @State private var gains: [Double] = Array(repeating: 0, count: 10)
    @State private var bands: [Double] = []
    @State private var preamp: Double = 0

    /// True while engine state is being applied to the controls, so their
    /// onChange handlers don't echo the values straight back. Best-effort —
    /// a stray echo is harmless because every EQ set is idempotent.
    @State private var syncing = false
    /// Last time a live drag value was forwarded, keyed by band index
    /// (`preampIndex` for the preamp). Sliders fire continuously during a
    /// drag, so live updates are coalesced to ~30/s; the final value is
    /// always sent when the drag ends.
    @State private var lastSend: [Int: Date] = [:]

    private let preampIndex = -1
    private let minSendInterval: TimeInterval = 1.0 / 30

    var body: some View {
        List {
            Section {
                Toggle("Equalizer", isOn: $enabled)
                    .onChange(of: enabled) { _, value in
                        guard !syncing else { return }
                        Engine.setEQEnabled(value)
                    }
                Picker("Preset", selection: $preset) {
                    ForEach(pickerPresets, id: \.self) { name in
                        Text(presetLabel(name)).tag(name)
                    }
                }
                .pickerStyle(.menu)
                .onChange(of: preset) { _, value in
                    // "custom" is a state the engine enters on band edits,
                    // not a preset you can apply — skip it (and echoes).
                    guard !syncing, value != "custom" else { return }
                    Engine.setEQPreset(value)
                    Task { await refresh() } // pull the preset's band gains
                }
            } footer: {
                Text("Adjusting a band switches the preset to Custom.")
            }

            Section("Bands") {
                HStack(alignment: .bottom, spacing: 0) {
                    ForEach(gains.indices, id: \.self) { index in
                        bandColumn(index)
                    }
                }
                .padding(.vertical, 4)
            }

            Section {
                VStack(alignment: .leading) {
                    HStack {
                        Text("Preamp")
                        Spacer()
                        Text("\(gainText(preamp)) dB")
                            .foregroundStyle(.secondary)
                    }
                    Slider(value: preampBinding, in: -12...12, step: 0.5) { editing in
                        if !editing { Engine.setEQPreamp(preamp) }
                    }
                    .tint(Palette.accent)
                }
            } footer: {
                Text("Lower the preamp if boosted bands make loud tracks clip.")
            }

            Section {
                Toggle("Mono Audio", isOn: $mono)
                    .onChange(of: mono) { _, value in
                        guard !syncing else { return }
                        Engine.setEQMono(value)
                    }
            } header: {
                Text("Accessibility")
            } footer: {
                Text("Play the same mix in both channels. Works independently of the equalizer.")
            }
        }
        .navigationTitle("Equalizer")
        .navigationBarTitleDisplayMode(.inline)
        .task { await refresh() }
    }

    // MARK: - Band column

    /// One vertical slider: gain readout on top, frequency label underneath.
    /// SwiftUI has no vertical `Slider`, so a horizontal one is laid out at
    /// the target height, rotated -90°, then clamped back into a narrow column.
    private func bandColumn(_ index: Int) -> some View {
        VStack(spacing: 8) {
            Text(gainText(gains[index]))
                .font(.system(size: 9).monospacedDigit())
                .foregroundStyle(.secondary)
            Slider(value: bandBinding(index), in: -12...12, step: 0.5) { editing in
                if !editing { Engine.setEQBand(index: index, gainDb: gains[index]) }
            }
            .tint(Palette.accent)
            .frame(width: 150)
            .rotationEffect(.degrees(-90))
            .frame(width: 24, height: 150)
            Text(bandLabel(index))
                .font(.system(size: 9))
                .foregroundStyle(.secondary)
        }
        .frame(maxWidth: .infinity)
    }

    // MARK: - Live-drag bindings (throttled)

    private func bandBinding(_ index: Int) -> Binding<Double> {
        Binding {
            gains[index]
        } set: { value in
            gains[index] = value
            preset = "custom" // the engine flips too; mirror it immediately
            if throttleAllows(index) { Engine.setEQBand(index: index, gainDb: value) }
        }
    }

    private var preampBinding: Binding<Double> {
        Binding {
            preamp
        } set: { value in
            preamp = value
            if throttleAllows(preampIndex) { Engine.setEQPreamp(value) }
        }
    }

    /// Rate-limits live drag updates for one slider to ~30/s.
    private func throttleAllows(_ key: Int) -> Bool {
        let now = Date()
        guard now.timeIntervalSince(lastSend[key] ?? .distantPast) >= minSendInterval else { return false }
        lastSend[key] = now
        return true
    }

    // MARK: - State / formatting

    private func refresh() async {
        guard let state = await Engine.eqState() else { return }
        syncing = true
        enabled = state.enabled
        mono = state.mono
        preset = state.preset
        presets = state.presets
        gains = state.gainsDb
        bands = state.bands
        preamp = state.preampDb
        // Clear after the pending view update so the onChange handlers fired
        // by the assignments above still see syncing == true.
        Task { @MainActor in syncing = false }
    }

    /// Engine presets plus the engine-managed "custom" state so the picker
    /// always has a row to select when the user has edited bands.
    private var pickerPresets: [String] {
        presets.isEmpty ? [preset] : presets + ["custom"]
    }

    /// "bass-boost" → "Bass Boost".
    private func presetLabel(_ name: String) -> String {
        let titled = name.split(separator: "-")
            .map { $0.prefix(1).uppercased() + $0.dropFirst() }
            .joined(separator: " ")
        return String(localized: String.LocalizationValue(titled))
    }

    /// Band center in compact form: 31, 63, … 1K, 2K, … 16K.
    private func bandLabel(_ index: Int) -> String {
        guard index < bands.count else { return "" }
        let hz = bands[index]
        return hz >= 1000 ? "\(Int(hz / 1000))K" : "\(Int(hz))"
    }

    /// Signed compact dB: "+4.5", "-6", "0".
    private func gainText(_ db: Double) -> String {
        db == 0 ? "0" : String(format: "%+g", db)
    }
}
