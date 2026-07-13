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

    /// Why the most recent `initialize` failed — only meaningful when it returned
    /// false: 0 = ok, 1 = ARL expired/invalid (re-auth needed), 2 = no internet
    /// (offer retry), 3 = other. The DZLoginErrorKind symbol lands in
    /// Clib/libdeezercore.h when `make corelib` regenerates it; the engine export
    /// already exists.
    static func loginErrorKind() -> Int { Int(DZLoginErrorKind()) }

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
    /// The account's liked-track ids (DZFavoriteIDsJSON) as a bare JSON string
    /// array (e.g. ["123","456"]). Seeds the truthful heart state so the like
    /// button is accurate on every track — not just tracks in the loaded list.
    static func favoriteIDs() -> [String] {
        decode([String].self, takeJSON(DZFavoriteIDsJSON())) ?? []
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
    static func home() -> HomeResponse? {
        decode(HomeResponse.self, takeJSON(DZHomeJSON()))
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

    // MARK: downloads

    // Downloads (premium-only) reuse the same C-string idioms as playback. The
    // DZDownload*/DZIsPreview symbols land in Clib/libdeezercore.h when
    // `make corelib` regenerates it; the engine exports already exist.

    /// Downloads `id` into `dir` ("" -> the shared default folder). Returns the
    /// engine's JSON: {"path":"..."} on success or {"error":"..."} on failure.
    static func download(_ id: String, to dir: String) -> String {
        takeString(withC2(id, dir) { DZDownloadTrack($0, $1) })
    }
    /// Current download folder (env / config / default).
    static func downloadDir() -> String { takeString(DZDownloadDir()) }
    /// Persists the download folder ("" resets to the default); true on success.
    static func setDownloadDir(_ p: String) -> Bool { withC(p) { DZSetDownloadDir($0) } == 1 }
    /// True when the current track is Deezer's 30-second preview (fallback).
    static func isPreview() -> Bool { DZIsPreview() == 1 }

    // MARK: free-tier ads (Deezer Free)

    // The DZAdsDisabled / DZSetAdsDisabled symbols land in Clib/libdeezercore.h
    // when `make corelib` regenerates it; the engine exports already exist.
    // Only meaningful for a Deezer Free account — premium plans have no ads.

    /// True when the free-tier ads / play-reporting opt-out is on.
    static func adsDisabled() -> Bool { DZAdsDisabled() == 1 }
    /// Turns the free-tier ads / play-reporting opt-out on (off == true) or off.
    static func setAdsDisabled(_ off: Bool) { _ = DZSetAdsDisabled(off ? 1 : 0) }

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

    // DZGetRepeat / DZGetShuffle report the mode the engine is ACTUALLY in — the
    // engine queue's when playing here (so a change made by any local client is
    // seen), or the routed device's snapshot when casting. The poll reads these
    // so the displayed repeat/shuffle always reflect the truth.

    /// Engine repeat mode: "off" | "all" | "one".
    static func getRepeat() -> String { takeString(DZGetRepeat()) }
    /// True when the engine reports shuffle on.
    static func getShuffle() -> Bool { DZGetShuffle() == 1 }

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

    // MARK: radio / mixes (v2.2)

    // DZTrackMixJSON / DZArtistMixJSON return {tracks:[...]} in the exact same
    // wire shape as DZFlowJSON, so they decode through TracksResponse and feed
    // the identical load+play code path Flow uses.

    /// "Song radio" seeded from a track (the seed track is kept first).
    static func trackMix(_ id: String) -> [Track] {
        decode(TracksResponse.self, takeJSON(withC(id) { DZTrackMixJSON($0) }))?.tracks ?? []
    }
    /// "Artist radio" seeded from an artist.
    static func artistMix(_ id: String) -> [Track] {
        decode(TracksResponse.self, takeJSON(withC(id) { DZArtistMixJSON($0) }))?.tracks ?? []
    }

    // MARK: listening history + stats (v2.2)

    /// Newest `n` entries of the machine-local listening history (newest first);
    /// n <= 0 returns all. Empty/unavailable history yields [].
    static func historyRecent(_ n: Int) -> [HistoryEntry] {
        decode([HistoryEntry].self, takeJSON(DZHistoryRecentJSON(Int32(n)))) ?? []
    }
    /// Local listening stats over the last `sinceDays` (<= 0 = all history).
    static func historyStats(sinceDays: Int) -> HistoryStats? {
        decode(HistoryStats.self, takeJSON(DZHistoryStatsJSON(Int32(sinceDays))))
    }

    // MARK: batch downloads (v2.2)

    // Album / playlist downloads reuse the single-track idioms: blocking + JSON
    // out ({"saved":N,"failed":N,"dir":"...","error":""}), premium-gated the same
    // way, so callers must invoke them off the main thread like download(_:to:).

    /// Downloads every track of `albumID` to the shared download folder; returns
    /// the engine's batch-summary JSON.
    static func downloadAlbum(_ albumID: String) -> String {
        takeString(withC(albumID) { DZDownloadAlbum($0) })
    }
    /// Downloads every track of `playlistID` to the shared download folder.
    static func downloadPlaylist(_ playlistID: String) -> String {
        takeString(withC(playlistID) { DZDownloadPlaylist($0) })
    }

    // MARK: media cache (v2.2)

    /// On-disk raw-stream cache budget in MB (0 = off, the default).
    static func mediaCacheMB() -> Int { Int(DZMediaCacheMB()) }
    /// Persists the raw-stream cache budget (0/negative disables it). The cache
    /// is attached to the player once at startup, so a change takes effect at the
    /// next launch. True on success.
    @discardableResult
    static func setMediaCacheMB(_ mb: Int) -> Bool { DZSetMediaCacheMB(Int32(mb)) == 1 }

    // MARK: queue sync (v2.2)

    // Mirror the GUI's play queue into the engine so remote /status and the
    // engine's gapless-promote path see — and can drive — the real queue. The
    // JSON is an array of the same track objects the DZ*JSON endpoints return
    // ({id,name,durationMs,artistLine,artists,albumName,artworkUrl,explicit});
    // encoding [Track] produces exactly that (artistId is optional server-side).

    /// Replaces the engine-side queue with `tracks`. Pass [] to clear. True on ok.
    @discardableResult
    static func queueSet(_ tracks: [Track]) -> Bool {
        guard let data = try? JSONEncoder().encode(tracks),
              let js = String(data: data, encoding: .utf8) else { return false }
        return withC(js) { DZQueueSet($0) } == 1
    }
    /// Moves the engine queue's cursor to `i` (clamped engine-side).
    static func queueSetIndex(_ i: Int) { DZQueueSetIndex(Int32(i)) }

    // MARK: queue editing — Up-Next editor (3.0)

    // The engine now exposes surgical queue edits (DZQueueRemove/DZQueueMove/
    // DZQueueInsertNext) plus a full snapshot (DZQueueJSON). AppState keeps the
    // GUI's own play queue authoritative and re-mirrors it with DZQueueSet after
    // each edit (one source of truth), so these thin wrappers exist for parity /
    // callers that want to drive the engine queue directly.

    /// Full engine-queue snapshot: content-version, cursor and tracks — lets a
    /// GUI adopt queue edits made by a remote controller.
    struct QueueSnapshot: Decodable {
        let version: Int64
        let index: Int
        let tracks: [Track]
    }
    static func queueJSON() -> QueueSnapshot? {
        decode(QueueSnapshot.self, takeJSON(DZQueueJSON()))
    }
    /// Content-mutation counter (bumped by set/add/remove/move, not cursor moves).
    static var queueVersion: Int64 { Int64(DZQueueVersion()) }
    /// Engine queue cursor (-1 when empty / unsynced).
    static var queueIndex: Int { Int(DZQueueIndex()) }

    /// Removes the engine queue's track at `i` (the playing row is guarded engine-side).
    static func queueRemove(_ i: Int) { DZQueueRemove(Int32(i)) }
    /// Reorders the engine queue, moving `from` to `to` (cursor follows the moved track).
    static func queueMove(from: Int, to: Int) { DZQueueMove(Int32(from), Int32(to)) }
    /// Splices `track` into the engine queue right after the playing row ("play next").
    static func queueInsertNext(_ track: Track) {
        guard let data = try? JSONEncoder().encode(track),
              let js = String(data: data, encoding: .utf8) else { return }
        withC(js) { DZQueueInsertNext($0) }
    }

    // MARK: offline download (cache a track's ciphertext for zero-network play)

    // DZDownloadForOffline pre-fetches the encrypted ciphertext into the on-disk
    // media cache and persists the stream meta, so a later DZPlay serves it with
    // zero network. Blocking + premium-only like DZDownloadTrack — call it off the
    // main thread. Returns {"status":"downloaded"|"cached","key":"..."} on success
    // or {"error":"..."}.
    static func downloadForOffline(_ id: String) -> String {
        takeString(withC(id) { DZDownloadForOffline($0) })
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
    /// Discards a preloaded next track. Call when the upcoming track is no
    /// longer deterministic (shuffle/repeat toggled after a preload was armed)
    /// so a stale preload can't be gaplessly swapped in.
    static func clearPreload() { DZClearPreload() }

    // MARK: sleep timer (v1.6)

    /// Arms the sleep timer. `minutes` > 0 pauses playback (with an automatic
    /// fade-out) after that many minutes; `endOfTrack` pauses when the current
    /// track ends (minutes ignored). minutes <= 0 with endOfTrack == false cancels.
    static func setSleepTimer(minutes: Int, endOfTrack: Bool) {
        DZSetSleepTimer(Int32(minutes), endOfTrack ? 1 : 0)
    }
    /// Cancels any armed sleep timer.
    static func cancelSleepTimer() { DZCancelSleepTimer() }
    /// True while a sleep timer (minutes or end-of-track) is armed.
    static func sleepActive() -> Bool { DZSleepTimerActive() == 1 }
    /// True when the armed timer fires at the end of the current track.
    static func sleepEndOfTrack() -> Bool { DZSleepTimerEndOfTrack() == 1 }
    /// Milliseconds until a minutes-based timer fires (0 for end-of-track / off).
    static func sleepRemainingMS() -> Int64 { DZSleepTimerRemainingMS() }

    // MARK: equalizer / mono downmix (v1.7)

    // The DZEQJSON / DZSetEQJSON symbols land in Clib/libdeezercore.h when
    // `make corelib` regenerates it; the engine exports already exist.

    /// Full EQ snapshot: current settings plus the core-owned band frequencies
    /// and preset names, so the UI never hardcodes them.
    struct EQState: Decodable {
        let enabled: Bool
        let mono: Bool
        let preampDb: Double
        let gainsDb: [Double]
        let preset: String
        let bands: [Double]
        let presets: [String]
    }

    /// Current EQ state; nil until the engine is up. The engine owns the values
    /// (persisted in ~/.config/opendeezer/eq.json) — re-read on panel open.
    static func eqState() -> EQState? {
        decode(EQState.self, takeJSON(DZEQJSON()))
    }

    // setEQ sends a partial-update JSON object to DZSetEQJSON. Every key is
    // optional; the engine flips preset to "custom" on manual band edits and
    // debounces persistence itself.
    @discardableResult
    private static func setEQ(_ obj: [String: Any]) -> Bool {
        guard let data = try? JSONSerialization.data(withJSONObject: obj),
              let js = String(data: data, encoding: .utf8) else { return false }
        return withC(js) { DZSetEQJSON($0) } == 1
    }

    static func setEQEnabled(_ on: Bool) { setEQ(["enabled": on]) }
    /// Mono downmix — independent of the EQ enable flag.
    static func setEQMono(_ on: Bool) { setEQ(["mono": on]) }
    /// Applies a named preset (one of EQState.presets); the engine sets all bands.
    @discardableResult
    static func setEQPreset(_ name: String) -> Bool { setEQ(["preset": name]) }
    /// Sets one band's gain (dB, -12..+12); the engine flips preset to "custom".
    static func setEQBand(_ index: Int, gainDb: Double) {
        setEQ(["band": ["index": index, "gainDb": gainDb]])
    }
    /// Output preamp in dB (-12..+12).
    static func setEQPreamp(_ db: Double) { setEQ(["preampDb": db]) }

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

    // MARK: remote control (control API)

    struct ControlConfig: Decodable {
        let enabled: Bool
        let addr: String
        let token: String
        let lan: Bool
        let running: Bool
    }

    /// Current remote-control (control API) settings, for populating Settings.
    static func controlConfig() -> ControlConfig? {
        decode(ControlConfig.self, takeJSON(DZControlConfigJSON()))
    }

    /// Persists and applies the remote-control settings. addr: "" = localhost
    /// only, ":7654" = LAN (all interfaces), or a full host:port. token: "" = no
    /// token required.
    static func setControlConfig(enabled: Bool, addr: String, token: String) {
        withC2(addr, token) { a, t in DZSetControlConfig(enabled ? 1 : 0, a, t) }
    }

    // MARK: update check

    /// Checks GitHub for a newer OpenDeezer release. Never downloads or installs
    /// anything — just reports whether one exists so the caller can point the
    /// user at the release page. Network failure -> hasUpdate == false.
    static func checkUpdate() -> UpdateInfo? {
        decode(UpdateInfo.self, takeJSON(DZCheckUpdateJSON()))
    }
}
