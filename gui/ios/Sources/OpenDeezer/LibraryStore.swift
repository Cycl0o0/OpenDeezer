import Foundation

/// Caches the user's favorites + playlists so the "liked" heart and library
/// lists stay in sync across every screen without refetching constantly.
@MainActor
final class LibraryStore: ObservableObject {
    static let shared = LibraryStore()

    @Published private(set) var favorites: [Track] = []
    @Published private(set) var favoriteIDs: Set<String> = []
    @Published private(set) var playlists: [Playlist] = []
    @Published private(set) var isLoading = false

    private init() {}

    func refreshAll() async {
        isLoading = true
        async let f: Void = refreshFavorites()
        async let p: Void = refreshPlaylists()
        _ = await (f, p)
        isLoading = false
    }

    func refreshFavorites() async {
        if let tracks = try? await Engine.favorites() {
            favorites = tracks
            favoriteIDs = Set(tracks.map(\.id))
        }
    }

    func refreshPlaylists() async {
        if let lists = try? await Engine.playlists() {
            playlists = lists
        }
    }

    func isFavorite(_ id: String) -> Bool { favoriteIDs.contains(id) }

    func toggleFavorite(_ track: Track) {
        let id = track.id
        if favoriteIDs.contains(id) {
            favoriteIDs.remove(id)
            favorites.removeAll { $0.id == id }
            Task { _ = await Engine.removeFavorite(id) }
        } else {
            favoriteIDs.insert(id)
            favorites.insert(track, at: 0)
            Task { _ = await Engine.addFavorite(id) }
        }
    }

    @discardableResult
    func createPlaylist(title: String) async -> String? {
        let id = try? await Engine.createPlaylist(title)
        await refreshPlaylists()
        return id
    }

    func renamePlaylist(_ id: String, title: String) async {
        _ = await Engine.renamePlaylist(id, title: title)
        await refreshPlaylists()
    }

    func deletePlaylist(_ id: String) async {
        _ = await Engine.deletePlaylist(id)
        await refreshPlaylists()
    }

    func addToPlaylist(_ playlistID: String, track: Track) async -> Bool {
        await Engine.addToPlaylist(playlistID, trackID: track.id)
    }
}
