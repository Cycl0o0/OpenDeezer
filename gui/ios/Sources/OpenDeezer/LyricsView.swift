import SwiftUI

/// Synced (or plain) lyrics for the current track. Tapping a synced line
/// seeks playback there, mirroring Apple Music's lyrics view.
struct LyricsView: View {
    let track: Track
    @EnvironmentObject private var player: PlayerController
    @Environment(\.dismiss) private var dismiss

    @State private var lyrics: Lyrics?
    @State private var isLoading = true
    @State private var errorText: String?

    var body: some View {
        NavigationStack {
            Group {
                if isLoading {
                    ProgressView().frame(maxWidth: .infinity, maxHeight: .infinity)
                } else if let error = errorText {
                    ContentUnavailableMessage(
                        systemImage: "quote.bubble", title: "No lyrics", message: error
                    )
                } else if let lyrics, lyrics.isSynced {
                    syncedList(lyrics)
                } else if let lyrics, !lyrics.plain.isEmpty {
                    ScrollView {
                        Text(lyrics.plain)
                            .font(.title3.weight(.medium))
                            .multilineTextAlignment(.center)
                            .padding(24)
                    }
                } else {
                    ContentUnavailableMessage(
                        systemImage: "quote.bubble", title: "No lyrics",
                        message: "Lyrics aren't available for this track."
                    )
                }
            }
            .navigationTitle(track.name)
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .topBarTrailing) {
                    Button("Done") { dismiss() }
                }
            }
            .task { await load() }
        }
    }

    private func syncedList(_ lyrics: Lyrics) -> some View {
        ScrollViewReader { proxy in
            ScrollView {
                LazyVStack(alignment: .leading, spacing: 18) {
                    ForEach(Array(lyrics.synced.enumerated()), id: \.offset) { index, line in
                        Text(line.text.isEmpty ? " " : line.text)
                            .font(.title3.weight(isCurrent(index, lyrics.synced) ? .bold : .medium))
                            .foregroundStyle(isCurrent(index, lyrics.synced) ? Palette.accent : .secondary)
                            .id(index)
                            .onTapGesture { player.seek(to: line.timeMs) }
                    }
                }
                .padding(24)
            }
            .onChange(of: player.positionMs) { _, _ in
                if let idx = currentIndex(lyrics.synced) {
                    withAnimation(.easeOut(duration: 0.25)) { proxy.scrollTo(idx, anchor: .center) }
                }
            }
        }
    }

    private func currentIndex(_ lines: [LyricLine]) -> Int? {
        var result: Int?
        for (i, line) in lines.enumerated() where line.timeMs <= player.positionMs { result = i }
        return result
    }
    private func isCurrent(_ index: Int, _ lines: [LyricLine]) -> Bool {
        currentIndex(lines) == index
    }

    private func load() async {
        isLoading = true
        do {
            lyrics = try await Engine.lyrics(track.id)
        } catch {
            errorText = error.localizedDescription
        }
        isLoading = false
    }
}

/// Tiny stand-in for `ContentUnavailableView` that also works pre-iOS 17
/// call-sites elsewhere in the app (kept consistent everywhere).
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
