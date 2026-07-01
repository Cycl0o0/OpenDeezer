import SwiftUI

/// Full-screen Now Playing sheet: big artwork, Liquid-Glass transport,
/// scrubber, shuffle/repeat, like, lyrics and the Connect device picker.
struct NowPlayingView: View {
    @EnvironmentObject private var player: PlayerController
    @EnvironmentObject private var library: LibraryStore
    @Environment(\.dismiss) private var dismiss

    @State private var showLyrics = false
    @State private var showDevices = false
    @State private var isScrubbing = false
    @State private var scrubValue: Double = 0

    var body: some View {
        NavigationStack {
            GeometryReader { geo in
                VStack(spacing: 0) {
                    artwork(size: min(geo.size.width - 64, 360))
                        .padding(.top, 12)

                    titleRow
                        .padding(.top, 28)
                        .padding(.horizontal, 28)

                    scrubber
                        .padding(.top, 18)
                        .padding(.horizontal, 28)

                    transport
                        .padding(.top, 22)
                        .padding(.horizontal, 12)

                    volumeRow
                        .padding(.top, 26)
                        .padding(.horizontal, 28)

                    Spacer(minLength: 12)

                    bottomBar
                        .padding(.horizontal, 28)
                        .padding(.bottom, 8)
                }
            }
            .background(nowPlayingBackground)
            .toolbar {
                ToolbarItem(placement: .topBarLeading) {
                    Button {
                        dismiss()
                    } label: {
                        Image(systemName: "chevron.down")
                            .font(.headline)
                    }
                }
                if !player.connectedDeviceAddr.isEmpty {
                    ToolbarItem(placement: .principal) {
                        Label("Connect", systemImage: "hifispeaker.fill")
                            .font(.caption.weight(.semibold))
                            .foregroundStyle(Palette.accent)
                    }
                }
            }
        }
        .sheet(isPresented: $showLyrics) {
            if let track = player.current {
                LyricsView(track: track)
            }
        }
        .sheet(isPresented: $showDevices) {
            DevicePickerView()
        }
    }

    // MARK: - Sections

    @ViewBuilder private func artwork(size: CGFloat) -> some View {
        RemoteArtwork(url: player.current?.artworkUrl ?? "", cornerRadius: 16)
            .frame(width: size, height: size)
            .shadow(color: .black.opacity(0.35), radius: 24, y: 12)
    }

    private var titleRow: some View {
        HStack(alignment: .center) {
            VStack(alignment: .leading, spacing: 4) {
                HStack(spacing: 6) {
                    Text(player.current?.name ?? "")
                        .font(.title3.weight(.bold))
                        .lineLimit(1)
                    if player.current?.explicit == true {
                        ExplicitBadge()
                    }
                }
                Text(player.current?.artistLine ?? "")
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }
            Spacer()
            Button {
                if let track = player.current { library.toggleFavorite(track) }
            } label: {
                Image(systemName: (player.current.map { library.isFavorite($0.id) } ?? false) ? "heart.fill" : "heart")
                    .font(.title3)
                    .foregroundStyle(Palette.accent)
            }
        }
    }

    private var scrubber: some View {
        VStack(spacing: 4) {
            Slider(
                value: Binding(
                    get: { isScrubbing ? scrubValue : Double(player.positionMs) },
                    set: { scrubValue = $0 }
                ),
                in: 0...max(Double(player.durationMs), 1),
                onEditingChanged: { editing in
                    if editing {
                        isScrubbing = true
                        scrubValue = Double(player.positionMs)
                    } else {
                        player.seek(to: Int64(scrubValue))
                        isScrubbing = false
                    }
                }
            )
            .tint(Palette.accent)

            HStack {
                Text(Track.timeText(isScrubbing ? Int64(scrubValue) : player.positionMs))
                Spacer()
                if !player.formatLabel.isEmpty {
                    Text(player.formatLabel)
                }
                Spacer()
                Text(Track.timeText(player.durationMs))
            }
            .font(.caption2.monospacedDigit())
            .foregroundStyle(.secondary)
        }
    }

    private var transport: some View {
        HStack(spacing: 0) {
            Button { player.toggleShuffle() } label: {
                Image(systemName: "shuffle")
                    .font(.system(size: 18))
                    .foregroundStyle(player.isShuffle ? Palette.accent : .secondary)
                    .frame(maxWidth: .infinity)
            }
            Button { player.previous() } label: {
                Image(systemName: "backward.fill")
                    .font(.system(size: 26))
                    .frame(maxWidth: .infinity)
            }
            Button { player.togglePlayPause() } label: {
                Image(systemName: player.isPlaying ? "pause.fill" : "play.fill")
                    .font(.system(size: 34))
                    .frame(width: 72, height: 72)
            }
            .glassCircle(interactive: true)
            Button { player.next() } label: {
                Image(systemName: "forward.fill")
                    .font(.system(size: 26))
                    .frame(maxWidth: .infinity)
            }
            Button { player.cycleRepeat() } label: {
                Image(systemName: player.repeatMode.systemImage)
                    .font(.system(size: 18))
                    .foregroundStyle(player.repeatMode == .off ? .secondary : Palette.accent)
                    .frame(maxWidth: .infinity)
            }
        }
        .buttonStyle(.plain)
        .foregroundStyle(.primary)
    }

    private var volumeRow: some View {
        HStack(spacing: 10) {
            Image(systemName: "speaker.fill").font(.caption).foregroundStyle(.secondary)
            Slider(value: Binding(get: { player.volume }, set: { player.setVolume($0) }), in: 0...1)
                .tint(.secondary)
            Image(systemName: "speaker.wave.3.fill").font(.caption).foregroundStyle(.secondary)
        }
    }

    private var bottomBar: some View {
        HStack {
            Button { showLyrics = true } label: {
                Image(systemName: "quote.bubble")
                    .font(.system(size: 18))
                    .frame(width: 44, height: 44)
            }
            Spacer()
            Button { showDevices = true } label: {
                Image(systemName: player.connectedDeviceAddr.isEmpty ? "hifispeaker" : "hifispeaker.fill")
                    .font(.system(size: 18))
                    .foregroundStyle(player.connectedDeviceAddr.isEmpty ? .primary : Palette.accent)
                    .frame(width: 44, height: 44)
            }
        }
        .glassCard(cornerRadius: 22)
        .padding(.horizontal, 4)
    }

    @ViewBuilder private var nowPlayingBackground: some View {
        if let artwork = player.artwork {
            Image(uiImage: artwork)
                .resizable()
                .scaledToFill()
                .blur(radius: 60)
                .overlay(.ultraThinMaterial)
                .ignoresSafeArea()
        } else {
            Color(.systemBackground).ignoresSafeArea()
        }
    }
}

/// Small red "E" badge for explicit tracks, reused across list rows.
struct ExplicitBadge: View {
    var body: some View {
        Text("E")
            .font(.caption2.bold())
            .foregroundStyle(.secondary)
            .padding(.horizontal, 4)
            .padding(.vertical, 1)
            .background(RoundedRectangle(cornerRadius: 3).stroke(Color.secondary, lineWidth: 1))
    }
}
