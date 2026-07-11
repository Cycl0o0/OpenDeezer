import SwiftUI

struct AlbumDetailView: View {
    let album: Album
    @EnvironmentObject private var player: PlayerController

    @State private var tracks: [Track] = []
    @State private var isLoading = true
    @State private var errorText: String?

    var body: some View {
        List {
            Section {
                header
            }
            .listRowInsets(EdgeInsets())
            .listRowBackground(Color.clear)
            .listRowSeparator(.hidden)

            if isLoading {
                ProgressView().frame(maxWidth: .infinity)
            } else if let error = errorText {
                ContentUnavailableMessage(
                    systemImage: "wifi.slash", title: "Couldn't load album", message: error,
                    retry: { Task { await load() } }
                )
            } else if tracks.isEmpty {
                ContentUnavailableMessage(
                    systemImage: "square.stack", title: "Albums",
                    message: String(localized: "This album is empty.")
                )
            } else {
                ForEach(Array(tracks.enumerated()), id: \.offset) { index, track in
                    TrackRow(track: track, tracks: tracks, showArtwork: false, indexLabel: index + 1)
                }
            }
        }
        .listStyle(.plain)
        .navigationBarTitleDisplayMode(.inline)
        .task { await load() }
        .refreshable { await load() }
    }

    private var header: some View {
        VStack(spacing: 10) {
            RemoteArtwork(url: album.artworkUrl, cornerRadius: 12)
                .frame(width: 180, height: 180)
                .shadow(radius: 10, y: 6)
            Text(album.name).font(.title2.bold()).multilineTextAlignment(.center)
            Text(album.artistLine).font(.footnote).foregroundStyle(.secondary)

            Button {
                guard !tracks.isEmpty else { return }
                player.playQueue(tracks, startAt: 0)
            } label: {
                Label("Play", systemImage: "play.fill").frame(maxWidth: .infinity)
            }
            .glassButton(prominent: true)
            .tint(Palette.accent)
            .padding(.horizontal, 40)
            .padding(.top, 4)
            .disabled(tracks.isEmpty)
        }
        .frame(maxWidth: .infinity)
        .padding(.vertical, 20)
    }

    private func load() async {
        isLoading = tracks.isEmpty
        do {
            tracks = try await Engine.albumTracks(album.id)
            errorText = nil
        } catch {
            if tracks.isEmpty { errorText = error.localizedDescription }
        }
        isLoading = false
    }
}
