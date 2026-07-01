import SwiftUI

/// Apple-Music-style lyrics: large left-aligned lines over the blurred album
/// art, the active synced line highlighted and auto-scrolled to the upper
/// third, tap-to-seek. Failures/empties fall back to a clean "not available"
/// state — never the raw engine error (Deezer's gw returns a debug string for
/// tracks with no lyrics, which must not leak into the UI).
struct LyricsView: View {
    let track: Track
    @EnvironmentObject private var player: PlayerController
    @Environment(\.dismiss) private var dismiss

    @State private var lyrics: Lyrics?
    @State private var isLoading = true

    /// Synced lines with blank separator/noise entries removed.
    private var syncedLines: [LyricLine] {
        (lyrics?.synced ?? []).filter { !$0.text.trimmingCharacters(in: .whitespaces).isEmpty }
    }
    private var plainText: String {
        (lyrics?.plain ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
    }

    var body: some View {
        NavigationStack {
            // background as a .background (not a ZStack sibling) so the blurred,
            // scaledToFill artwork can't inflate the content's width past the
            // screen — that oversize was making the lyric GeometryReader wrap at
            // a too-wide size, clipping lines off the left in portrait.
            content
                .frame(maxWidth: .infinity, maxHeight: .infinity)
                .background { background }
                .navigationTitle(track.name)
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .principal) {
                    VStack(spacing: 1) {
                        Text(track.name).font(.subheadline.weight(.semibold)).lineLimit(1)
                        Text(track.artistLine).font(.caption2).foregroundStyle(.secondary).lineLimit(1)
                    }
                }
                ToolbarItem(placement: .topBarTrailing) {
                    Button { dismiss() } label: { Image(systemName: "chevron.down").font(.headline) }
                }
            }
            .task { await load() }
        }
    }

    @ViewBuilder private var content: some View {
        if isLoading {
            ProgressView().tint(.white)
        } else if !syncedLines.isEmpty {
            syncedView
        } else if !plainText.isEmpty {
            plainView
        } else {
            ContentUnavailableMessage(
                systemImage: "quote.bubble",
                title: "Lyrics not available",
                message: "This track doesn't have lyrics yet."
            )
        }
    }

    // MARK: - Synced (Apple Music style, tap-to-seek + auto-scroll)

    private var syncedView: some View {
        // Explicit line width from the actual viewport so long lines WRAP in
        // portrait (previously they rendered as single wide lines and got
        // clipped off the left; only fit unwrapped in landscape's width).
        GeometryReader { geo in
            ScrollViewReader { proxy in
                ScrollView(showsIndicators: false) {
                    LazyVStack(alignment: .leading, spacing: 22) {
                        Color.clear.frame(height: 8)
                        ForEach(Array(syncedLines.enumerated()), id: \.offset) { index, line in
                            Text(line.text)
                                .font(.title2.weight(.bold))
                                .multilineTextAlignment(.leading)
                                .foregroundStyle(color(for: index))
                                .opacity(opacity(for: index))
                                .frame(width: max(0, geo.size.width - 52), alignment: .leading)
                                .scaleEffect(index == activeIndex ? 1.0 : 0.96, anchor: .leading)
                                .animation(.easeOut(duration: 0.25), value: activeIndex)
                                .contentShape(Rectangle())
                                .id(index)
                                .onTapGesture { player.seek(to: line.timeMs) }
                        }
                        Color.clear.frame(height: 240)
                    }
                    .padding(.horizontal, 26)
                    .padding(.top, 12)
                }
                .onChange(of: activeIndex) { _, idx in
                    guard let idx else { return }
                    withAnimation(.easeInOut(duration: 0.35)) {
                        proxy.scrollTo(idx, anchor: UnitPoint(x: 0, y: 0.32))
                    }
                }
            }
        }
    }

    private var activeIndex: Int? {
        var result: Int?
        for (i, line) in syncedLines.enumerated() where line.timeMs <= player.positionMs { result = i }
        return result
    }
    private func color(for index: Int) -> Color {
        index == activeIndex ? .white : .white.opacity(0.55)
    }
    private func opacity(for index: Int) -> Double {
        guard let active = activeIndex else { return 0.55 }
        return index == active ? 1.0 : (index < active ? 0.4 : 0.55)
    }

    // MARK: - Plain fallback

    private var plainView: some View {
        ScrollView(showsIndicators: false) {
            Text(plainText)
                .font(.title3.weight(.semibold))
                .foregroundStyle(.white.opacity(0.85))
                .frame(maxWidth: .infinity, alignment: .leading)
                .padding(.horizontal, 26)
                .padding(.vertical, 24)
        }
    }

    // MARK: - Background (blurred artwork)

    @ViewBuilder private var background: some View {
        if let art = player.artwork {
            Image(uiImage: art)
                .resizable()
                .scaledToFill()
                .blur(radius: 70)
                .overlay(Color.black.opacity(0.55))
                .overlay(.ultraThinMaterial)
                .ignoresSafeArea()
        } else {
            LinearGradient(colors: [.black, Color(red: 0.1, green: 0.02, blue: 0.18)],
                           startPoint: .top, endPoint: .bottom)
                .ignoresSafeArea()
        }
    }

    private func load() async {
        isLoading = true
        // Deliberately swallow errors: a gw failure (no lyrics) must render the
        // clean empty state, not the raw engine/gw error string.
        lyrics = try? await Engine.lyrics(track.id)
        isLoading = false
    }
}

/// Tiny stand-in for `ContentUnavailableView`, styled for the dark lyrics/player
/// surfaces and reused across the app.
struct ContentUnavailableMessage: View {
    let systemImage: String
    let title: String
    let message: String

    var body: some View {
        VStack(spacing: 12) {
            Image(systemName: systemImage)
                .font(.system(size: 40))
                .foregroundStyle(.secondary)
            Text(title).font(.headline)
            Text(message)
                .font(.subheadline)
                .foregroundStyle(.secondary)
                .multilineTextAlignment(.center)
                .padding(.horizontal, 32)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }
}
