import Foundation
import CDeezerCore

// Core wraps the C functions exported by the Go engine (libdeezercore).
// All list/search calls return a malloc'd JSON string that must be DZFree'd.
enum Core {
    // withC passes a Swift string as a mutable C string to a Go-exported call.
    private static func withC<T>(_ s: String, _ f: (UnsafeMutablePointer<CChar>) -> T) -> T {
        s.withCString { f(UnsafeMutablePointer(mutating: $0)) }
    }

    // withC2 passes two Swift strings as C strings (e.g. playlistID + trackID).
    private static func withC2<T>(_ a: String, _ b: String,
                                  _ f: (UnsafeMutablePointer<CChar>, UnsafeMutablePointer<CChar>) -> T) -> T {
        withC(a) { pa in withC(b) { pb in f(pa, pb) } }
    }

    // takeString copies + frees a malloc'd C string (for non-JSON returns).
    private static func takeString(_ ptr: UnsafeMutablePointer<CChar>?) -> String {
        guard let p = ptr else { return "" }
        defer { DZFree(p) }
        return String(cString: p)
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

    // MARK: now playing (engine truth)

    // DZNowPlayingJSON returns a jTrack-shaped object for the track the engine is
    // ACTUALLY playing. The remote (OpenDeezer Connect) variant carries no
    // per-artist ids (artists is null) but does supply artistId (the primary
    // artist's id) so the Artist button can navigate even when routed remotely.
    private struct NowPlayingTrack: Decodable {
        let id: String
        let name: String
        let durationMs: Int64
        let artists: [Artist]?
        let artistId: String?   // primary artist id; set by engine when routed via Connect
        let artistLine: String
        let albumName: String
        let artworkUrl: String
        let explicit: Bool
    }

    /// The track the engine is ACTUALLY playing — started on this device via the
    /// control API, or the REMOTE device's current track when routed through
    /// OpenDeezer Connect. Returns nil when the engine reports nothing (empty
    /// object / no id) so callers keep their last display.
    static func nowPlaying() -> Track? {
        guard let np = decode(NowPlayingTrack.self, takeJSON(DZNowPlayingJSON())),
              !np.id.isEmpty else { return nil }
        // When routed through OpenDeezer Connect the artists list is absent but
        // the engine now supplies artistId. Synthesise a single Artist entry so
        // the Artist button in PlayerBar stays enabled and openArtistForCurrent()
        // can navigate correctly.
        let artists: [Artist]
        if let a = np.artists, !a.isEmpty {
            artists = a
        } else if let aid = np.artistId, !aid.isEmpty {
            artists = [Artist(id: aid, name: np.artistLine)]
        } else {
            artists = []
        }
        return Track(id: np.id, name: np.name, durationMs: np.durationMs,
                     artists: artists, artistLine: np.artistLine,
                     albumName: np.albumName, artworkUrl: np.artworkUrl,
                     explicit: np.explicit)
    }

    // MARK: repeat / shuffle (engine forwarding for OpenDeezer Connect)

    /// Forwards the repeat-mode change to the connected remote device when routed.
    /// mode: 0 = off, 1 = all, 2 = one — matches RepeatMode.rawValue.
    static func setRepeat(_ mode: Int) { DZSetRepeat(Int32(mode)) }
    /// Forwards the shuffle change to the connected remote device when routed.
    static func setShuffle(_ on: Bool) { DZSetShuffle(on ? 1 : 0) }

    // MARK: audio quality

    /// Quality level: 0 = Normal (MP3 128), 1 = High (MP3 320), 2 = HiFi (FLAC).
    static func setQuality(_ level: Int) { DZSetQuality(Int32(level)) }
    static var quality: Int { Int(DZQuality()) }

    // MARK: replay gain

    /// Loudness normalization. The engine owns the value; init UI from `replayGain`.
    static func setReplayGain(_ on: Bool) { DZSetReplayGain(on ? 1 : 0) }
    static var replayGain: Bool { DZReplayGain() == 1 }

    // MARK: favorites / playlist mutations (v0.4)

    @discardableResult
    static func addFavorite(_ trackID: String) -> Bool {
        withC(trackID) { DZAddFavorite($0) } == 1
    }
    @discardableResult
    static func removeFavorite(_ trackID: String) -> Bool {
        withC(trackID) { DZRemoveFavorite($0) } == 1
    }
    @discardableResult
    static func addToPlaylist(_ playlistID: String, _ trackID: String) -> Bool {
        withC2(playlistID, trackID) { DZAddToPlaylist($0, $1) } == 1
    }
    @discardableResult
    static func removeFromPlaylist(_ playlistID: String, _ trackID: String) -> Bool {
        withC2(playlistID, trackID) { DZRemoveFromPlaylist($0, $1) } == 1
    }
    /// Creates an empty playlist; returns the new id (nil on failure).
    static func createPlaylist(_ title: String) -> String? {
        decode(CreatedPlaylist.self, takeJSON(withC(title) { DZCreatePlaylist($0) }))?.id
    }
    @discardableResult
    static func renamePlaylist(_ playlistID: String, _ title: String) -> Bool {
        withC2(playlistID, title) { DZRenamePlaylist($0, $1) } == 1
    }
    @discardableResult
    static func deletePlaylist(_ playlistID: String) -> Bool {
        withC(playlistID) { DZDeletePlaylist($0) } == 1
    }

    // MARK: Flow (v0.4)

    static func flow() -> [Track] {
        decode(TracksResponse.self, takeJSON(DZFlowJSON()))?.tracks ?? []
    }

    // MARK: podcasts (v0.4)

    static func searchPodcasts(_ q: String) -> [Podcast] {
        decode(PodcastsResponse.self, takeJSON(withC(q) { DZSearchPodcastsJSON($0) }))?.podcasts ?? []
    }
    static func podcastEpisodes(_ id: String) -> [Episode] {
        decode(EpisodesResponse.self, takeJSON(withC(id) { DZPodcastEpisodesJSON($0) }))?.episodes ?? []
    }
    /// Plays a podcast episode via the plain (unencrypted) stream path.
    @discardableResult
    static func playEpisode(_ id: String, durationMs: Int64) -> Bool {
        withC(id) { DZPlayEpisode($0, Int64(durationMs)) } == 1
    }

    // MARK: audio output device (v0.4)

    static func audioDevices() -> [AudioDevice] {
        decode(AudioDevicesResponse.self, takeJSON(DZAudioDevicesJSON()))?.devices ?? []
    }
    @discardableResult
    static func setAudioDevice(_ id: String) -> Bool {
        withC(id) { DZSetAudioDevice($0) } == 1
    }
    /// Selected output device id ("" = system default).
    static var currentAudioDevice: String { takeString(DZCurrentAudioDevice()) }

    // MARK: gapless / crossfade / preload (v0.4)

    static func setGapless(_ on: Bool) { DZSetGapless(on ? 1 : 0) }
    static var gapless: Bool { DZGapless() == 1 }
    static func setCrossfadeMS(_ ms: Int) { DZSetCrossfadeMS(Int32(ms)) }
    static var crossfadeMS: Int { Int(DZCrossfadeMS()) }
    /// Preloads the next track for a gapless/crossfaded transition.
    static func preload(_ trackID: String, durationMs: Int64) {
        withC(trackID) { DZPreload($0, Int64(durationMs)) }
    }

    // MARK: OpenDeezer Connect (device picker)

    /// Discovers OpenDeezer devices on the LAN (~700ms). Returns [] on none/error.
    /// The engine returns a bare JSON array; decode it directly.
    static func discoverDevices(timeoutMS: Int32 = 700) -> [Device] {
        decode([Device].self, takeJSON(DZDiscoverDevices(timeoutMS))) ?? []
    }
    /// Routes playback to the device at host:port; true on success. Once connected
    /// the existing transport calls transparently drive the chosen device.
    @discardableResult
    static func connectDevice(_ addr: String) -> Bool {
        withC(addr) { DZConnectDevice($0) } == 1
    }
    /// Returns playback to this computer.
    static func disconnectDevice() { DZDisconnectDevice() }
    /// Connected device's host:port ("" when playing on this computer).
    static var connectedDevice: String { takeString(DZConnectedDevice()) }

    // MARK: phone web remote

    struct WebRemoteInfo: Decodable {
        let enabled: Bool
        let code: String
        let url: String
        let port: Int
    }

    /// Enables (on=true) or disables the LAN web remote server.
    static func setWebRemoteEnabled(_ on: Bool) {
        DZWebRemoteSetEnabled(on ? 1 : 0)
    }

    /// Current web remote state: enabled flag, 6-digit pairing code, URL and port.
    static func webRemoteInfo() -> WebRemoteInfo? {
        decode(WebRemoteInfo.self, takeJSON(DZWebRemoteInfoJSON()))
    }

    /// PNG bytes of a QR code encoding the remote URL; nil when the remote is
    /// disabled or the engine returns nothing. Caller owns nothing — freed here.
    static func webRemoteQRPNG() -> Data? {
        var length: Int32 = 0
        guard let ptr = DZWebRemoteQRPNG(&length), length > 0 else { return nil }
        defer { DZFreeBytes(ptr) }
        return Data(bytes: ptr, count: Int(length))
    }
}
