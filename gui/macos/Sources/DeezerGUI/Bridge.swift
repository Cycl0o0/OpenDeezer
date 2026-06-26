import Foundation
import CDeezerCore

// Core wraps the C functions exported by the Go engine (libdeezercore).
// All list/search calls return a malloc'd JSON string that must be DZFree'd.
enum Core {
    // withC passes a Swift string as a mutable C string to a Go-exported call.
    private static func withC<T>(_ s: String, _ f: (UnsafeMutablePointer<CChar>) -> T) -> T {
        s.withCString { f(UnsafeMutablePointer(mutating: $0)) }
    }

    private static func takeJSON(_ ptr: UnsafeMutablePointer<CChar>?) -> Data? {
        guard let p = ptr else { return nil }
        defer { DZFree(p) }
        return String(cString: p).data(using: .utf8)
    }

    private static func decode<T: Decodable>(_ type: T.Type, _ data: Data?) -> T? {
        guard let data else { return nil }
        return try? JSONDecoder().decode(T.self, from: data)
    }

    // MARK: session

    static func initialize(arl: String) -> Bool {
        withC(arl) { DZInit($0) } == 1
    }

    static var userID: String {
        guard let p = DZUserID() else { return "" }
        defer { DZFree(p) }
        return String(cString: p)
    }

    // MARK: browse

    static func favorites() -> [Track] {
        decode(TracksResponse.self, takeJSON(DZFavoritesJSON()))?.tracks ?? []
    }
    static func playlists() -> [Playlist] {
        decode(PlaylistsResponse.self, takeJSON(DZPlaylistsJSON()))?.playlists ?? []
    }
    static func playlistTracks(_ id: String) -> [Track] {
        decode(TracksResponse.self, takeJSON(withC(id) { DZPlaylistTracksJSON($0) }))?.tracks ?? []
    }
    static func albumTracks(_ id: String) -> [Track] {
        decode(TracksResponse.self, takeJSON(withC(id) { DZAlbumTracksJSON($0) }))?.tracks ?? []
    }
    static func search(_ q: String) -> SearchResponse? {
        decode(SearchResponse.self, takeJSON(withC(q) { DZSearchJSON($0) }))
    }

    // MARK: playback

    @discardableResult
    static func play(_ id: String, durationMs: Int64) -> Bool {
        withC(id) { DZPlay($0, Int64(durationMs)) } == 1
    }
    static func togglePause() { DZTogglePause() }
    static func stop() { DZStop() }
    static func setVolume(_ v: Double) { DZSetVolume(v) }
    static var volume: Double { DZVolume() }
    static var state: PlayerState { PlayerState(rawValue: Int(DZState())) ?? .stopped }
    static var positionMs: Int64 { DZPositionMS() }
    static var durationMs: Int64 { DZDurationMS() }
    static var finishedCount: Int { Int(DZFinishedCount()) }
}
