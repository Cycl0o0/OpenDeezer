import SwiftUI

// PlayerBar — the floating Apple-Music-style transport bar: controls left,
// now-playing centre with a thin scrubber, utilities right.
struct PlayerBar: View {
    @EnvironmentObject var app: AppState
    @State private var scrubbing = false
    @State private var scrub = 0.0

    private var isPlaying: Bool { app.state == .playing }
    private var progress: Double {
        app.durationMs > 0 ? min(1, Double(app.positionMs) / Double(app.durationMs)) : 0
    }

    var body: some View {
        // Liquid Glass (macOS 26): the bar is one glass surface; the play button
        // is its own tinted, interactive glass shape that morphs within the
        // container.
        GlassEffectContainer(spacing: 18) {
            HStack(spacing: 16) {
                transport
                castingChip
                Spacer(minLength: 12)
                nowPlaying
                Spacer(minLength: 12)
                utilities
            }
            .padding(.horizontal, 18)
            .frame(height: 66)
            .glassEffect(.regular, in: RoundedRectangle(cornerRadius: 22))
            // Swallow clicks so they don't fall through to the track list behind.
            .contentShape(RoundedRectangle(cornerRadius: 22))
        }
    }

    private var transport: some View {
        HStack(spacing: 18) {
            iconButton(app.shuffle ? "shuffle.circle.fill" : "shuffle",
                       tint: app.shuffle ? DZ.accent : DZ.textSec) { app.setShuffle(!app.shuffle) }
                .accessibilityLabel(L("Shuffle"))
                .accessibilityValue(app.shuffle ? L("On") : L("Off"))
            iconButton("backward.fill", size: 16, tint: DZ.textPri) { app.prev() }
                .accessibilityLabel(L("Previous"))
            Button { app.togglePause() } label: {
                Image(systemName: isPlaying ? "pause.fill" : "play.fill")
                    .font(.system(size: 18, weight: .bold))
                    .foregroundStyle(.white)
                    .frame(width: 40, height: 40)
                    .glassEffect(.regular.tint(DZ.accent).interactive(), in: Circle())
            }
            .buttonStyle(.plain)
            .accessibilityLabel(L("Play / Pause"))
            iconButton("forward.fill", size: 16, tint: DZ.textPri) { app.next() }
                .accessibilityLabel(L("Next"))
            iconButton(app.repeatMode == .one ? "repeat.1" : "repeat",
                       tint: app.repeatMode == .off ? DZ.textSec : DZ.accent) {
                app.cycleRepeat()
            }
            .accessibilityLabel(L("Repeat"))
            .accessibilityValue(repeatValueLabel)
        }
        .frame(width: 230, alignment: .leading)
    }

    // VoiceOver value for the repeat control's current mode.
    private var repeatValueLabel: String {
        switch app.repeatMode {
        case .off: return L("Off")
        case .all: return L("All")
        case .one: return L("One")
        }
    }

    // Casting chip: shown only while routed to a Connect device. Names the device
    // and offers a one-click "Play here" that returns playback to this computer
    // (the engine resumes locally). Kept truthful by the tick's connectedDevice
    // mirror, so it also appears/updates when another client changes the route.
    @ViewBuilder private var castingChip: some View {
        if app.isConnectedRemote {
            HStack(spacing: 8) {
                Image(systemName: "wave.3.right")
                    .font(.system(size: 11)).foregroundStyle(DZ.accent)
                Text(Lf("Playing on %@", app.connectedDeviceName))
                    .font(.system(size: 11, weight: .medium)).foregroundStyle(DZ.textPri)
                    .lineLimit(1)
                Button { app.disconnectDevice() } label: {
                    Text(L("Play here"))
                        .font(.system(size: 11, weight: .semibold)).foregroundStyle(DZ.accent)
                }
                .buttonStyle(.plain)
                .help(L("Play here"))
                .accessibilityLabel(L("Play here"))
            }
            .padding(.horizontal, 10).padding(.vertical, 6)
            .glassEffect(.regular, in: Capsule())
        }
    }

    private var nowPlaying: some View {
        HStack(spacing: 10) {
            Artwork(url: app.current?.artworkUrl ?? "", size: 40, radius: 5)
            VStack(spacing: 3) {
                HStack(spacing: 5) {
                    if app.current?.explicit == true { ExplicitBadge() }
                    Text(app.current?.name ?? L("Nothing playing"))
                        .font(.system(size: 12, weight: .semibold)).foregroundStyle(DZ.textPri)
                        .lineLimit(1)
                }
                HStack(spacing: 6) {
                    Text(subtitleText)
                        .font(.system(size: 11)).foregroundStyle(DZ.textSec).lineLimit(1)
                    if app.current != nil && !app.outputFormat.isEmpty {
                        Text(app.outputFormat)
                            .font(.system(size: 9, weight: .semibold))
                            .foregroundStyle(DZ.accent)
                            .padding(.horizontal, 5).padding(.vertical, 1)
                            .background(DZ.accent.opacity(0.15), in: Capsule())
                    }
                    // 30-second preview fallback (see Core.isPreview / AppState).
                    if app.current != nil && app.isPreview {
                        Text(L("Preview"))
                            .font(.system(size: 9, weight: .semibold))
                            .foregroundStyle(DZ.accentMag)
                            .padding(.horizontal, 5).padding(.vertical, 1)
                            .background(DZ.accentMag.opacity(0.15), in: Capsule())
                    }
                }
                scrubber
            }
        }
        .frame(maxWidth: 420)
    }

    private var subtitleText: String {
        guard let c = app.current else { return "" }
        return c.albumName.isEmpty ? c.artistLine : "\(c.artistLine) — \(c.albumName)"
    }

    private var scrubber: some View {
        HStack(spacing: 6) {
            Text(Track.timeText(scrubbing ? Int64(scrub * Double(app.durationMs)) : app.positionMs))
                .font(.system(size: 9)).monospacedDigit().foregroundStyle(DZ.textSec)
            Slider(value: Binding(
                get: { scrubbing ? scrub : progress },
                set: { scrub = $0 }),
                in: 0...1,
                onEditingChanged: { editing in
                    scrubbing = editing
                    if !editing { app.seek(toFraction: scrub) }
                })
            .controlSize(.mini)
            .tint(DZ.accent)
            .disabled(app.current == nil)
            Text(Track.timeText(app.durationMs))
                .font(.system(size: 9)).monospacedDigit().foregroundStyle(DZ.textSec)
        }
    }

    private var utilities: some View {
        HStack(spacing: 14) {
            // Like / unlike the now-playing track (one-shot; local toggle state).
            iconButton(app.isCurrentLiked ? "heart.fill" : "heart",
                       tint: app.isCurrentLiked ? DZ.accent : DZ.textSec) {
                app.toggleLikeCurrent()
            }
            .disabled(app.current == nil || app.playingEpisode)
            .help(app.isCurrentLiked ? L("Unlike") : L("Like"))
            .accessibilityLabel(L("Like"))
            .accessibilityValue(app.isCurrentLiked ? L("On") : L("Off"))
            // Lyrics for the now-playing track.
            iconButton("quote.bubble", tint: app.showLyrics ? DZ.accent : DZ.textSec) {
                app.showLyrics = true
            }
            .disabled(app.current == nil)
            .help(L("Lyrics"))
            // Jump to the now-playing track's artist.
            iconButton("music.mic", tint: DZ.textSec) {
                app.openArtistForCurrent()
            }
            .disabled(app.current?.artists.first == nil)
            .help(L("Go to Artist"))
            // OpenDeezer Connect: pick a device to play on (Spotify-Connect style).
            iconButton("rectangle.connected.to.line.below",
                       tint: app.isConnectedRemote ? DZ.accent : DZ.textSec) {
                app.showDevicePicker = true
                app.discoverDevices()
            }
            .help(app.isConnectedRemote ? L("Connected — choose a device") : L("Connect to a device"))
            HStack(spacing: 6) {
                Image(systemName: "speaker.fill").font(.system(size: 11)).foregroundStyle(DZ.textSec)
                Slider(value: Binding(get: { app.volume }, set: { app.setVolume($0) }), in: 0...1)
                    .frame(width: 84).tint(DZ.accent)
            }
        }
        .frame(width: 282, alignment: .trailing)
    }

    private func iconButton(_ symbol: String, size: CGFloat = 15,
                            tint: Color, _ action: @escaping () -> Void) -> some View {
        Button(action: action) {
            Image(systemName: symbol).font(.system(size: size)).foregroundStyle(tint)
        }
        .buttonStyle(.plain)
    }
}
