import Foundation

// Wire models — match the JSON emitted by corelib (jTrack/jAlbum/jPlaylist).

struct Artist: Codable, Hashable {
    let id: String
    let name: String
}

struct Track: Codable, Hashable, Identifiable {
    let id: String
    let name: String
    let durationMs: Int64
    let artists: [Artist]
    let artistLine: String
    let albumName: String
    let artworkUrl: String

    var durationText: String { Self.timeText(durationMs) }

    static func timeText(_ ms: Int64) -> String {
        let s = max(0, ms) / 1000
        return String(format: "%d:%02d", s / 60, s % 60)
    }
}

struct Album: Codable, Hashable, Identifiable {
    let id: String
    let name: String
    let artists: [Artist]
    let artworkUrl: String
    var artistLine: String { artists.first?.name ?? "" }
}

struct Playlist: Codable, Hashable, Identifiable {
    let id: String
    let name: String
    let owner: String
    let trackCount: Int
    let artworkUrl: String
}

struct TracksResponse: Codable { let tracks: [Track] }
struct PlaylistsResponse: Codable { let playlists: [Playlist] }
struct SearchResponse: Codable {
    let tracks: [Track]
    let albums: [Album]
    let playlists: [Playlist]
}
struct ErrorResponse: Codable { let error: String }

// PlayerState mirrors audio.State in the Go core.
enum PlayerState: Int {
    case stopped = 0, loading, playing, paused, errored
}
