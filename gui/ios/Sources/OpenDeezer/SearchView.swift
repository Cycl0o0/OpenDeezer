import SwiftUI

struct SearchView: View {
    @State private var query = ""
    @State private var results: SearchResponse?
    @State private var isLoading = false
    @State private var errorText: String?
    @State private var segment: Segment = .tracks

    private enum Segment: String, CaseIterable, Identifiable {
        case tracks = "Songs", artists = "Artists", albums = "Albums", playlists = "Playlists"
        var id: String { rawValue }
    }

    var body: some View {
        VStack(spacing: 0) {
            if results != nil {
                Picker("Filter", selection: $segment) {
                    ForEach(Segment.allCases) { Text($0.rawValue).tag($0) }
                }
                .pickerStyle(.segmented)
                .padding(.horizontal, 16)
                .padding(.vertical, 8)
            }
            content
        }
        .navigationTitle("Search")
        .searchable(text: $query, placement: .navigationBarDrawer(displayMode: .always), prompt: "Songs, artists, albums")
        .onChange(of: query) { _, newValue in
            Task { await search(newValue) }
        }
    }

    @ViewBuilder private var content: some View {
        if query.trimmingCharacters(in: .whitespaces).isEmpty {
            ContentUnavailableMessage(systemImage: "magnifyingglass", title: "Search Deezer", message: "Find songs, artists, albums and playlists.")
        } else if isLoading {
            ProgressView().frame(maxWidth: .infinity, maxHeight: .infinity)
        } else if let error = errorText {
            ContentUnavailableMessage(systemImage: "wifi.slash", title: "Search failed", message: error)
        } else if let results {
            List {
                switch segment {
                case .tracks:
                    ForEach(results.tracks) { track in
                        TrackRow(track: track, tracks: results.tracks)
                    }
                case .artists:
                    ForEach(results.artists ?? []) { artist in
                        NavigationLink { ArtistView(artistID: artist.id, artistName: artist.name) } label: {
                            HStack {
                                RemoteArtwork(url: artist.artworkUrl, cornerRadius: 22)
                                    .frame(width: 44, height: 44)
                                    .clipShape(Circle())
                                Text(artist.name)
                            }
                        }
                    }
                case .albums:
                    ForEach(results.albums) { album in
                        NavigationLink { AlbumDetailView(album: album) } label: {
                            HStack {
                                RemoteArtwork(url: album.artworkUrl, cornerRadius: 6)
                                    .frame(width: 44, height: 44)
                                VStack(alignment: .leading) {
                                    Text(album.name)
                                    Text(album.artistLine).font(.caption).foregroundStyle(.secondary)
                                }
                            }
                        }
                    }
                case .playlists:
                    ForEach(results.playlists) { playlist in
                        NavigationLink { PlaylistDetailView(playlist: playlist) } label: {
                            HStack {
                                RemoteArtwork(url: playlist.artworkUrl, cornerRadius: 6)
                                    .frame(width: 44, height: 44)
                                VStack(alignment: .leading) {
                                    Text(playlist.name)
                                    Text("\(playlist.trackCount) songs").font(.caption).foregroundStyle(.secondary)
                                }
                            }
                        }
                    }
                }
            }
            .listStyle(.plain)
        }
    }

    private func search(_ text: String) async {
        let trimmed = text.trimmingCharacters(in: .whitespaces)
        guard !trimmed.isEmpty else {
            results = nil
            return
        }
        try? await Task.sleep(nanoseconds: 250_000_000)
        guard trimmed == query.trimmingCharacters(in: .whitespaces) else { return }
        isLoading = true
        do {
            results = try await Engine.search(trimmed)
            errorText = nil
        } catch {
            errorText = error.localizedDescription
        }
        isLoading = false
    }
}
