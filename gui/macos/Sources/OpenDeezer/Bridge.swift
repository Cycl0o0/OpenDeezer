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

    /// Human label for the current stream's actual format (e.g. "FLAC · lossless").
    static var format: String {
        guard let p = DZFormat() else { return "" }
        defer { DZFree(p) }
        return String(cString: p)
    }

    // MARK: account

    /// Logged-in plan + entitlements; nil until login completes.
    static func account() -> Account? {
        decode(Account.self, takeJSON(DZAccountJSON()))
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
    static func charts() -> ChartsResponse? {
        decode(ChartsResponse.self, takeJSON(DZChartsJSON()))
    }
    static func artistTop(_ id: String) -> [Track] {
        decode(TracksResponse.self, takeJSON(withC(id) { DZArtistTopJSON($0) }))?.tracks ?? []
    }
    static func artistProfile(_ id: String) -> ArtistProfile? {
        decode(ArtistProfile.self, takeJSON(withC(id) { DZArtistProfileJSON($0) }))
    }
    static func lyrics(_ id: String) -> Lyrics? {
        decode(Lyrics.self, takeJSON(withC(id) { DZLyricsJSON($0) }))
    }

    // MARK: playback

    @discardableResult
    static func play(_ id: String, durationMs: Int64) -> Bool {
        withC(id) { DZPlay($0, Int64(durationMs)) } == 1
    }
    static func togglePause() { DZTogglePause() }
    static func stop() { DZStop() }
    static func seek(_ ms: Int64) { DZSeek(ms) }
    static func setVolume(_ v: Double) { DZSetVolume(v) }
    static var volume: Double { DZVolume() }
    static var state: PlayerState { PlayerState(rawValue: Int(DZState())) ?? .stopped }
    static var positionMs: Int64 { DZPositionMS() }
    static var durationMs: Int64 { DZDurationMS() }
    static var finishedCount: Int { Int(DZFinishedCount()) }

    // MARK: audio quality

    /// Quality level: 0 = Normal (MP3 128), 1 = High (MP3 320), 2 = HiFi (FLAC).
    static func setQuality(_ level: Int) { DZSetQuality(Int32(level)) }
    static var quality: Int { Int(DZQuality()) }

    // MARK: replay gain

    /// Loudness normalization. The engine owns the value; init UI from `replayGain`.
    static func setReplayGain(_ on: Bool) { DZSetReplayGain(on ? 1 : 0) }
    static var replayGain: Bool { DZReplayGain() == 1 }
}
