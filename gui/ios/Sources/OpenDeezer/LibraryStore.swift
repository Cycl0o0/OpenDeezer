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

    /// Refresh only the liked-track id set via the lightweight `favoriteIDs`
    /// fetch (no full track objects), so the heart is truthful for any track
    /// without the heavier favorites refresh. Skips overwriting on an empty
    /// result — the engine returns "[]" on a transient fetch failure too, and we
    /// don't want a blip to wipe every heart. `toggleFavorite` still handles the
    /// unlike-your-last-favorite case optimistically.
    func refreshFavoriteIDs() async {
        let ids = await Engine.favoriteIDs()
        guard !ids.isEmpty else { return }
        favoriteIDs = Set(ids)
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
            Task {
                if !(await Engine.removeFavorite(id)) { await refreshFavorites() }
            }
        } else {
            favoriteIDs.insert(id)
            favorites.insert(track, at: 0)
            Task {
                if !(await Engine.addFavorite(id)) { await refreshFavorites() }
            }
        }
    }

    @discardableResult
    func createPlaylist(title: String) async -> String? {
        let id = try? await Engine.createPlaylist(title)
        await refreshPlaylists()
        return id
    }

    @discardableResult
    func renamePlaylist(_ id: String, title: String) async -> Bool {
        let renamed = await Engine.renamePlaylist(id, title: title)
        if renamed { await refreshPlaylists() }
        return renamed
    }

    @discardableResult
    func deletePlaylist(_ id: String) async -> Bool {
        let deleted = await Engine.deletePlaylist(id)
        if deleted { await refreshPlaylists() }
        return deleted
    }

    func addToPlaylist(_ playlistID: String, track: Track) async -> Bool {
        await Engine.addToPlaylist(playlistID, trackID: track.id)
    }
}
