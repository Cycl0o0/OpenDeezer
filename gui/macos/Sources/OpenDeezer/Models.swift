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
    let explicit: Bool   // explicit lyrics/content — shows an "E" badge

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
    // Artist entities (jArtistInfo). Optional/tolerant: the current engine's
    // DZSearchJSON omits them, so the UI hides the Artists section when empty.
    let artists: [ArtistInfo]?
}
struct ErrorResponse: Codable { let error: String }

// Result of DZCreatePlaylist ({"id":"..."}).
struct CreatedPlaylist: Codable { let id: String }

// Podcast show (DZSearchPodcastsJSON).
struct Podcast: Codable, Hashable, Identifiable {
    let id: String
    let name: String
    let description: String
    let artworkUrl: String
    let episodeCount: Int
}
struct PodcastsResponse: Codable { let podcasts: [Podcast] }

// Podcast episode (DZPodcastEpisodesJSON). Played via the plain-stream path.
struct Episode: Codable, Hashable, Identifiable {
    let id: String
    let title: String
    let description: String
    let artworkUrl: String
    let durationMs: Int64
    let releaseDate: String

    var durationText: String { Track.timeText(durationMs) }
}
struct EpisodesResponse: Codable { let episodes: [Episode] }

// Audio output device (DZAudioDevicesJSON). id "" == system default.
struct AudioDevice: Codable, Hashable, Identifiable {
    let id: String
    let name: String
    let isDefault: Bool
}
struct AudioDevicesResponse: Codable { let devices: [AudioDevice] }

// Account tier + entitlements (DZAccountJSON).
struct Account: Codable {
    let userId: String
    let name: String
    let offer: String
    let canHq: Bool
    let canHifi: Bool
    let premium: Bool   // a paid plan that can stream on-demand; false = Deezer Free
    let loggedIn: Bool
}

// Artist summary (jArtistInfo) used by charts / artist profile.
struct ArtistInfo: Codable, Hashable, Identifiable {
    let id: String
    let name: String
    let artworkUrl: String
    let nbFans: Int
}

// Global charts (DZChartsJSON).
struct ChartsResponse: Codable {
    let tracks: [Track]
    let albums: [Album]
    let artists: [ArtistInfo]
    let playlists: [Playlist]
}

// Artist profile page (DZArtistProfileJSON).
struct ArtistProfile: Codable {
    let artist: ArtistInfo
    let top: [Track]
    let albums: [Album]
    let related: [ArtistInfo]
}

// One timed lyric line (DZLyricsJSON.synced[]).
struct LyricLine: Codable, Hashable {
    let timeMs: Int64
    let text: String
}

// Track lyrics (DZLyricsJSON).
struct Lyrics: Codable {
    let plain: String
    let synced: [LyricLine]
    let isSynced: Bool
}

// PlayerState mirrors audio.State in the Go core.
enum PlayerState: Int {
    case stopped = 0, loading, playing, paused, errored
}
