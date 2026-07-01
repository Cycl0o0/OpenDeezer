import Foundation

// Wire models for the Odmobile (gomobile) engine.
//
// Most browse endpoints (Home/Search/Charts/Playlists/Favorites/Flow/ArtistTop)
// marshal purpose-built DTOs with explicit `json:"..."` tags — lowerCamelCase
// keys (id, name, durationMs, artistLine, artworkUrl, ...).
//
// A few endpoints (ArtistProfile, Lyrics, SearchPodcasts, PodcastEpisodes)
// marshal the engine's internal Go structs directly with NO json tags, so Go's
// default encoding emits the exported field names verbatim (ID, Name,
// DurationMS, ArtworkURL, ...). Track/Album/ArtistInfo below use a
// dual-casing decoder so the same model works for every endpoint regardless
// of which casing the engine used.

// MARK: - Dynamic key decoding helpers

struct AnyCodingKey: CodingKey {
    let stringValue: String
    var intValue: Int? { nil }
    init?(stringValue: String) { self.stringValue = stringValue }
    init?(intValue: Int) { nil }
}

extension KeyedDecodingContainer where Key == AnyCodingKey {
    private func k(_ s: String) -> AnyCodingKey { AnyCodingKey(stringValue: s)! }

    func first(_ keys: [String], default def: String = "") -> String {
        for key in keys { if let v = try? decode(String.self, forKey: k(key)) { return v } }
        return def
    }
    func firstOpt(_ keys: [String]) -> String? {
        for key in keys { if let v = try? decode(String.self, forKey: k(key)) { return v } }
        return nil
    }
    func first(_ keys: [String], default def: Int64 = 0) -> Int64 {
        for key in keys {
            if let v = try? decode(Int64.self, forKey: k(key)) { return v }
            if let v = try? decode(Double.self, forKey: k(key)) { return Int64(v) }
        }
        return def
    }
    func first(_ keys: [String], default def: Int = 0) -> Int {
        for key in keys { if let v = try? decode(Int.self, forKey: k(key)) { return v } }
        return def
    }
    func first(_ keys: [String], default def: Bool = false) -> Bool {
        for key in keys { if let v = try? decode(Bool.self, forKey: k(key)) { return v } }
        return def
    }
    func first<T: Decodable>(_ keys: [String], as type: [T].Type) -> [T] {
        for key in keys { if let v = try? decode([T].self, forKey: k(key)) { return v } }
        return []
    }
}

// MARK: - Core models

struct Artist: Decodable, Hashable {
    let id: String
    let name: String

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyCodingKey.self)
        id = c.first(["id", "ID"])
        name = c.first(["name", "Name"])
    }
}

struct Track: Decodable, Hashable, Identifiable {
    let id: String
    let name: String
    let durationMs: Int64
    let artists: [Artist]
    let artistLine: String
    let artistId: String?
    let albumName: String
    let artworkUrl: String
    let explicit: Bool

    /// Manual constructor (e.g. adapting a podcast `Episode` to a `Track` for
    /// the player queue) — the engine only ever adapts episodes client-side.
    init(id: String, name: String, durationMs: Int64, artists: [Artist] = [],
         artistLine: String? = nil, artistId: String? = nil, albumName: String = "",
         artworkUrl: String = "", explicit: Bool = false) {
        self.id = id
        self.name = name
        self.durationMs = durationMs
        self.artists = artists
        self.artistLine = artistLine ?? artists.map(\.name).joined(separator: ", ")
        self.artistId = artistId ?? artists.first?.id
        self.albumName = albumName
        self.artworkUrl = artworkUrl
        self.explicit = explicit
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyCodingKey.self)
        id = c.first(["id", "ID"])
        name = c.first(["name", "Name", "title", "Title"])
        durationMs = c.first(["durationMs", "DurationMS"])
        let artists: [Artist] = c.first(["artists", "Artists"], as: [Artist].self)
        self.artists = artists
        artistLine = c.firstOpt(["artistLine"]) ?? artists.map(\.name).joined(separator: ", ")
        artistId = c.firstOpt(["artistId"]) ?? artists.first?.id
        albumName = c.first(["albumName", "AlbumName"])
        artworkUrl = c.first(["artworkUrl", "ArtworkURL"])
        explicit = c.first(["explicit", "Explicit"])
    }

    var durationText: String { Self.timeText(durationMs) }

    static func timeText(_ ms: Int64) -> String {
        let s = max(0, ms) / 1000
        return String(format: "%d:%02d", s / 60, s % 60)
    }
}

struct Album: Decodable, Hashable, Identifiable {
    let id: String
    let name: String
    let artists: [Artist]
    let artworkUrl: String
    var artistLine: String { artists.map(\.name).joined(separator: ", ") }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyCodingKey.self)
        id = c.first(["id", "ID"])
        name = c.first(["name", "Name"])
        artists = c.first(["artists", "Artists"], as: [Artist].self)
        artworkUrl = c.first(["artworkUrl", "ArtworkURL"])
    }
}

struct ArtistInfo: Decodable, Hashable, Identifiable {
    let id: String
    let name: String
    let artworkUrl: String
    let nbFans: Int

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: AnyCodingKey.self)
        id = c.first(["id", "ID"])
        name = c.first(["name", "Name"])
        artworkUrl = c.first(["artworkUrl", "ArtworkURL"])
        nbFans = c.first(["nbFans", "NbFans"])
    }
}

struct Playlist: Decodable, Hashable, Identifiable {
    let id: String
    let name: String
    let owner: String
    let trackCount: Int
    let artworkUrl: String
}

// MARK: - Response wrappers

struct TracksResponse: Decodable { let tracks: [Track] }
struct PlaylistsResponse: Decodable { let playlists: [Playlist] }
struct ErrorResponse: Decodable { let error: String }
struct CreatedPlaylist: Decodable { let id: String }

struct SearchResponse: Decodable {
    let tracks: [Track]
    let albums: [Album]
    let playlists: [Playlist]
    let artists: [ArtistInfo]?
}

struct HomeResponse: Decodable {
    let topTracks: [Track]
    let topAlbums: [Album]
    let playlists: [Playlist]
}

struct ChartsResponse: Decodable {
    let tracks: [Track]
    let albums: [Album]
    let artists: [ArtistInfo]
    let playlists: [Playlist]
}

// ArtistProfile (raw ArtistPage — capitalized keys, no json tags in the engine).
struct ArtistProfilePage: Decodable {
    let artist: ArtistInfo
    let top: [Track]
    let albums: [Album]
    let related: [ArtistInfo]

    enum CodingKeys: String, CodingKey {
        case artist = "Artist", top = "Top", albums = "Albums", related = "Related"
    }
}

// Lyrics (raw Lyrics struct — capitalized keys).
struct LyricLine: Decodable, Hashable {
    let timeMs: Int64
    let text: String
    enum CodingKeys: String, CodingKey { case timeMs = "TimeMS", text = "Text" }
}

struct Lyrics: Decodable {
    let plain: String
    let synced: [LyricLine]
    var isSynced: Bool { !synced.isEmpty }
    enum CodingKeys: String, CodingKey { case plain = "Plain", synced = "Synced" }
}

// Podcasts (raw Podcast/Episode structs — capitalized keys).
struct Podcast: Decodable, Hashable, Identifiable {
    let id: String
    let name: String
    let description: String
    let artworkUrl: String
    let episodeCount: Int
    enum CodingKeys: String, CodingKey {
        case id = "ID", name = "Name", description = "Description"
        case artworkUrl = "ArtworkURL", episodeCount = "EpisodeCount"
    }
}
struct PodcastsResponse: Decodable { let podcasts: [Podcast] }

struct Episode: Decodable, Hashable, Identifiable {
    let id: String
    let title: String
    let description: String
    let artworkUrl: String
    let durationMs: Int64
    let releaseDate: String
    let podcastName: String
    enum CodingKeys: String, CodingKey {
        case id = "ID", title = "Title", description = "Description", artworkUrl = "ArtworkURL"
        case durationMs = "DurationMS", releaseDate = "ReleaseDate", podcastName = "PodcastName"
    }
    var durationText: String { Track.timeText(durationMs) }
}
struct EpisodesResponse: Decodable { let episodes: [Episode] }

// Account (DZAccountJSON — tagged lowerCamelCase).
struct Account: Decodable {
    let userId: String
    let name: String
    let offer: String
    let canHq: Bool
    let canHifi: Bool
    let premium: Bool
    let loggedIn: Bool
}

// Connect device (bare JSON array from OdmobileDiscoverDevices).
struct Device: Decodable, Hashable, Identifiable {
    let name: String
    let addr: String
    let client: String
    let version: String
    var id: String { addr }

    var typeLabel: String {
        switch client {
        case "tui": return "Terminal"
        case "darwin", "macos": return "Mac"
        case "windows": return "Windows"
        case "linux", "gnome", "kde": return "Linux"
        case "android": return "Android"
        case "ios": return "iPhone/iPad"
        default: return client.isEmpty ? "Device" : client.capitalized
        }
    }
    var symbol: String {
        switch client {
        case "tui": return "terminal"
        case "darwin", "macos": return "laptopcomputer"
        case "windows": return "pc"
        case "linux", "gnome", "kde": return "desktopcomputer"
        case "android": return "candybarphone"
        case "ios": return "iphone"
        default: return "hifispeaker.fill"
        }
    }
}

// Phone remote / web remote pairing info.
struct WebRemoteInfo: Decodable {
    let enabled: Bool
    let code: String
    let url: String
    let port: Int
}

// Update-check result (OdmobileCheckUpdate) — tagged lowerCamelCase.
// A network failure comes back with hasUpdate == false.
struct UpdateInfo: Decodable {
    let current: String
    let latest: String
    let hasUpdate: Bool
    let url: String
    let notes: String
}

// PlayerState mirrors audio.State in the Go core.
enum PlayerState: Int {
    case stopped = 0, loading, playing, paused, errored
}
