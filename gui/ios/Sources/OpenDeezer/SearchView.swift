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
        var titleKey: LocalizedStringKey {
            switch self {
            case .tracks: return "Songs"
            case .artists: return "Artists"
            case .albums: return "Albums"
            case .playlists: return "Playlists"
            }
        }
    }

    var body: some View {
        VStack(spacing: 0) {
            if results != nil && !query.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
                Picker("Filter", selection: $segment) {
                    ForEach(Segment.allCases) { Text($0.titleKey).tag($0) }
                }
                .pickerStyle(.segmented)
                .padding(.horizontal, 16)
                .padding(.vertical, 8)
            }
            content
        }
        .navigationTitle("Search")
        .searchable(text: $query, placement: .navigationBarDrawer(displayMode: .always), prompt: "Songs, artists, albums")
        .task(id: query) { await search(query) }
    }

    @ViewBuilder private var content: some View {
        if query.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
            ContentUnavailableMessage(systemImage: "magnifyingglass", title: "Search Deezer", message: String(localized: "Find songs, artists, albums and playlists."))
        } else if isLoading {
            ProgressView().frame(maxWidth: .infinity, maxHeight: .infinity)
        } else if let error = errorText {
            ContentUnavailableMessage(
                systemImage: "wifi.slash", title: "Search failed", message: error,
                retry: { Task { await search(query) } }
            )
        } else if let results {
            if resultCount(in: results) == 0 {
                ContentUnavailableMessage(
                    systemImage: "magnifyingglass", title: "Search Deezer",
                    message: String(localized: "Try a different search.")
                )
            } else {
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
                .scrollDismissesKeyboard(.interactively)
            }
        }
    }

    private func search(_ text: String) async {
        let trimmed = text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else {
            results = nil
            errorText = nil
            isLoading = false
            return
        }
        isLoading = true
        errorText = nil
        do {
            try await Task.sleep(nanoseconds: 250_000_000)
        } catch {
            return
        }
        guard !Task.isCancelled,
              trimmed == query.trimmingCharacters(in: .whitespacesAndNewlines) else { return }
        do {
            let response = try await Engine.search(trimmed)
            guard !Task.isCancelled,
                  trimmed == query.trimmingCharacters(in: .whitespacesAndNewlines) else { return }
            results = response
            errorText = nil
        } catch {
            guard !Task.isCancelled,
                  trimmed == query.trimmingCharacters(in: .whitespacesAndNewlines) else { return }
            results = nil
            errorText = error.localizedDescription
        }
        isLoading = false
    }

    private func resultCount(in results: SearchResponse) -> Int {
        switch segment {
        case .tracks: return results.tracks.count
        case .artists: return results.artists?.count ?? 0
        case .albums: return results.albums.count
        case .playlists: return results.playlists.count
        }
    }
}
