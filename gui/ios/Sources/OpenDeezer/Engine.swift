import Foundation
import Odmobile

/// Errors surfaced by the engine layer to the UI.
enum EngineError: LocalizedError {
    case server(String)
    case decode(String)

    var errorDescription: String? {
        switch self {
        case .server(let message): return message
        case .decode(let message): return String(localized: "Couldn't read the server's response (\(message)).")
        }
    }
}

/// Thin async/await façade over the `Odmobile*` C functions gomobile generates
/// from `mobile/odmobile.go`. Every call that can block (network I/O in the Go
/// runtime) is dispatched onto a background serial queue so SwiftUI's main
/// thread never stalls; state getters that are simple field reads are called
/// directly.
enum Engine {
    private static let ioQueue = DispatchQueue(label: "fr.cyclooo.OpenDeezer.engine.io", qos: .userInitiated)
    /// Bulk artwork downloads run here, concurrently, so a screenful of slow
    /// cover fetches (15s HTTP timeout each) can't head-of-line-block
    /// play/pause/seek on the serial transport queue.
    private static let fetchQueue = DispatchQueue(label: "fr.cyclooo.OpenDeezer.engine.fetch", qos: .utility, attributes: .concurrent)
    private static let decoder = JSONDecoder()

    private static func run<T: Sendable>(_ body: @escaping @Sendable () -> T) async -> T {
        await withCheckedContinuation { continuation in
            ioQueue.async { continuation.resume(returning: body()) }
        }
    }

    private static func runFetch<T: Sendable>(_ body: @escaping @Sendable () -> T) async -> T {
        await withCheckedContinuation { continuation in
            fetchQueue.async { continuation.resume(returning: body()) }
        }
    }

    private static func decode<T: Decodable>(_ json: String, as type: T.Type) throws -> T {
        let data = Data(json.utf8)
        if let err = try? decoder.decode(ErrorResponse.self, from: data), !err.error.isEmpty {
            throw EngineError.server(err.error)
        }
        do {
            return try decoder.decode(T.self, from: data)
        } catch {
            throw EngineError.decode(error.localizedDescription)
        }
    }

    // MARK: - Lifecycle / account

    static func initEngine(arl: String) async -> Bool { await run { OdmobileInit(arl) } }
    static func loggedIn() async -> Bool { await run { OdmobileLoggedIn() } }
    /// Why the most recent `initEngine` returned false: 0 ok, 1 ARL expired or
    /// invalid, 2 no internet, 3 other. A cheap atomic read engine-side, so
    /// (like `state`/`volume`) it's called directly; only meaningful right after
    /// `initEngine` returns false.
    static func loginErrorKind() -> Int { OdmobileLoginErrorKind() }
    static func setClientInfo(client: String, device: String) { OdmobileSetClientInfo(client, device) }

    static func account() async throws -> Account {
        try decode(await run { OdmobileAccount() }, as: Account.self)
    }

    /// Checks GitHub for a newer release. Network failures decode to
    /// `hasUpdate == false` engine-side, so this rarely throws in practice.
    static func checkUpdate() async -> UpdateInfo? {
        try? decode(await run { OdmobileCheckUpdate() }, as: UpdateInfo.self)
    }

    /// Tears the engine session down: stops playback, closes the control
    /// server (web-remote pairing + same-account auth) and the Connect-host
    /// advertiser, and forgets the Deezer client. A later `initEngine` starts
    /// services fresh for the new account.
    static func logout() async { await run { OdmobileLogout() } }

    // MARK: - Browse

    static func home() async throws -> HomeResponse {
        try decode(await run { OdmobileHome() }, as: HomeResponse.self)
    }
    static func favorites() async throws -> [Track] {
        try decode(await run { OdmobileFavorites() }, as: TracksResponse.self).tracks
    }
    static func playlists() async throws -> [Playlist] {
        try decode(await run { OdmobilePlaylists() }, as: PlaylistsResponse.self).playlists
    }
    static func playlistTracks(_ id: String) async throws -> [Track] {
        try decode(await run { OdmobilePlaylistTracks(id) }, as: TracksResponse.self).tracks
    }
    static func albumTracks(_ id: String) async throws -> [Track] {
        try decode(await run { OdmobileAlbumTracks(id) }, as: TracksResponse.self).tracks
    }
    static func flow() async throws -> [Track] {
        try decode(await run { OdmobileFlow() }, as: TracksResponse.self).tracks
    }
    static func charts() async throws -> ChartsResponse {
        try decode(await run { OdmobileCharts() }, as: ChartsResponse.self)
    }
    static func search(_ query: String) async throws -> SearchResponse {
        try decode(await run { OdmobileSearch(query) }, as: SearchResponse.self)
    }
    static func artistTop(_ id: String) async throws -> [Track] {
        try decode(await run { OdmobileArtistTop(id) }, as: TracksResponse.self).tracks
    }
    static func artistProfile(_ id: String) async throws -> ArtistProfilePage {
        try decode(await run { OdmobileArtistProfile(id) }, as: ArtistProfilePage.self)
    }
    static func lyrics(_ id: String) async throws -> Lyrics {
        try decode(await run { OdmobileLyrics(id) }, as: Lyrics.self)
    }
    static func searchPodcasts(_ query: String) async throws -> [Podcast] {
        try decode(await run { OdmobileSearchPodcasts(query) }, as: PodcastsResponse.self).podcasts
    }
    static func podcastEpisodes(_ id: String) async throws -> [Episode] {
        try decode(await run { OdmobilePodcastEpisodes(id) }, as: EpisodesResponse.self).episodes
    }

    // MARK: - Playback

    static func play(id: String, durationMs: Int64) async -> Bool {
        await run { OdmobilePlay(id, durationMs) }
    }
    static func playEpisode(id: String, durationMs: Int64) async -> Bool {
        await run { OdmobilePlayEpisodeMS(id, durationMs) }
    }
    /// Resolves and buffers the next queue track so the engine swaps into it
    /// seamlessly at the track boundary (mirrors the desktop GUIs' DZPreload).
    /// False on failure — the caller then falls back to a plain re-resolve at
    /// the boundary. `OdmobilePreload` is the gomobile binding of
    /// `Preload(id string) error` (BOOL + NSError out-param).
    static func preload(id: String) async -> Bool {
        await run {
            var err: NSError?
            return OdmobilePreload(id, &err) && err == nil
        }
    }
    /// Discards a previously armed preload. Call whenever the upcoming track
    /// stops being deterministic (shuffle/repeat toggles, queue edits, stop).
    static func clearPreload() async { await run { OdmobileClearPreload() } }
    static func pause() async { await run { OdmobilePause() } }
    static func resume() async { await run { OdmobileResume() } }
    static func togglePause() async { await run { OdmobileTogglePause() } }
    static func stop() async { await run { OdmobileStop() } }
    static func seek(ms: Int64) async { await run { OdmobileSeek(ms) } }
    /// Async: when routed to a Connect device this becomes a blocking HTTP
    /// POST (15s timeout) engine-side — it must never run on the main thread.
    static func setVolume(_ v: Double) async { await run { OdmobileSetVolume(v) } }
    /// Suspends/resumes the local OS audio device (never Connect-routed);
    /// cheap and local, safe to call synchronously.
    static func setOutputSuspended(_ on: Bool) { OdmobileSetOutputSuspended(on) }
    static func volume() -> Double { OdmobileVolume() }
    static func state() -> Int { OdmobileState() }
    static func positionMS() -> Int64 { OdmobilePositionMS() }
    static func durationMS() -> Int64 { OdmobileDurationMS() }
    static func finishedCount() -> Int { OdmobileFinishedCount() }
    static func format() -> String { OdmobileFormat() }

    static func nowPlaying() async -> Track? {
        let json = await run { OdmobileNowPlaying() }
        guard let track = try? decode(json, as: Track.self), !track.id.isEmpty else { return nil }
        return track
    }

    // MARK: - Library writes

    static func addFavorite(_ id: String) async -> Bool { await run { OdmobileAddFavorite(id) } }
    static func removeFavorite(_ id: String) async -> Bool { await run { OdmobileRemoveFavorite(id) } }
    static func addToPlaylist(_ playlistID: String, trackID: String) async -> Bool {
        await run { OdmobileAddToPlaylist(playlistID, trackID) }
    }
    static func removeFromPlaylist(_ playlistID: String, trackID: String) async -> Bool {
        await run { OdmobileRemoveFromPlaylist(playlistID, trackID) }
    }
    static func createPlaylist(_ title: String) async throws -> String {
        try decode(await run { OdmobileCreatePlaylist(title) }, as: CreatedPlaylist.self).id
    }
    static func renamePlaylist(_ id: String, title: String) async -> Bool {
        await run { OdmobileRenamePlaylist(id, title) }
    }
    static func deletePlaylist(_ id: String) async -> Bool { await run { OdmobileDeletePlaylist(id) } }

    // MARK: - Settings

    static func setQuality(_ level: Int) { OdmobileSetQuality(level) }
    static func quality() -> Int { OdmobileQuality() }
    static func setReplayGain(_ on: Bool) { OdmobileSetReplayGain(on) }
    static func replayGain() -> Bool { OdmobileReplayGain() }
    static func setGapless(_ on: Bool) { OdmobileSetGapless(on) }
    static func gapless() -> Bool { OdmobileGapless() }
    static func setCrossfadeMS(_ ms: Int) { OdmobileSetCrossfadeMS(ms) }
    static func crossfadeMS() -> Int { OdmobileCrossfadeMS() }

    // MARK: - Equalizer

    /// Full EQ + mono-downmix state. The DSP, persistence and the
    /// preset→"custom" flip on manual band edits all live engine-side; the UI
    /// only renders this state and forwards changes.
    static func eqState() async -> EQState? {
        let json = await run { OdmobileEQJSON() }
        return try? decode(json, as: EQState.self)
    }

    /// EQ setters are cheap in-memory DSP-parameter writes (the engine
    /// debounces its own persistence), so they're safe to call synchronously.
    static func setEQEnabled(_ on: Bool) { applyEQ(["enabled": on]) }
    static func setEQMono(_ on: Bool) { applyEQ(["mono": on]) }
    static func setEQPreset(_ name: String) { applyEQ(["preset": name]) }
    static func setEQBand(index: Int, gainDb: Double) { applyEQ(["band": ["index": index, "gainDb": gainDb]]) }
    static func setEQPreamp(_ db: Double) { applyEQ(["preampDb": db]) }

    /// Serializes a partial EQ update for `OdmobileSetEQJSON` (every key optional).
    private static func applyEQ(_ fields: [String: Any]) {
        guard let data = try? JSONSerialization.data(withJSONObject: fields),
              let json = String(data: data, encoding: .utf8) else { return }
        _ = OdmobileSetEQJSON(json)
    }

    // MARK: - Sleep timer

    /// Pause after `minutes` (with fade-out), or when the current track ends if
    /// `endOfTrack` is true. Passing `minutes <= 0` with `endOfTrack == false`
    /// cancels any pending timer.
    static func setSleepTimer(minutes: Int, endOfTrack: Bool) {
        OdmobileSetSleepTimer(minutes, endOfTrack ? 1 : 0)
    }
    static func cancelSleepTimer() { OdmobileCancelSleepTimer() }
    static func sleepActive() -> Bool { OdmobileSleepActive() != 0 }
    static func sleepEndOfTrack() -> Bool { OdmobileSleepEndOfTrack() != 0 }
    static func sleepRemainingMS() -> Int64 { OdmobileSleepRemainingMS() }

    // MARK: - Connect

    static func discoverDevices(timeoutMs: Int) async -> [Device] {
        let json = await run { OdmobileDiscoverDevices(timeoutMs) }
        return (try? decode(json, as: [Device].self)) ?? []
    }
    static func connectDevice(_ addr: String) async -> Bool { await run { OdmobileConnectDevice(addr) } }
    static func disconnectDevice() async { await run { OdmobileDisconnectDevice() } }
    static func connectedDevice() -> String { OdmobileConnectedDevice() }
    /// Async like setVolume: Connect-routed, so a blocking HTTP POST engine-side.
    static func setRepeat(_ mode: Int) async { await run { OdmobileSetRepeat(mode) } }
    static func setShuffle(_ on: Bool) async { await run { OdmobileSetShuffle(on ? 1 : 0) } }

    // MARK: - Web remote

    static func webRemoteSetEnabled(_ on: Bool) { OdmobileWebRemoteSetEnabled(on ? 1 : 0) }
    static func webRemoteInfo() async -> WebRemoteInfo? {
        let json = await run { OdmobileWebRemoteInfo() }
        return try? decode(json, as: WebRemoteInfo.self)
    }
    static func webRemoteQRPNG() async -> Data? { await run { OdmobileWebRemoteQRPNG() } }

    // MARK: - OpenDeezer Connect host (make this device controllable)

    static func connectHostSetEnabled(_ on: Bool) { OdmobileConnectHostSetEnabled(on ? 1 : 0) }
    static func connectHostInfo() async -> ConnectHostInfo? {
        let json = await run { OdmobileConnectHostInfo() }
        return try? decode(json, as: ConnectHostInfo.self)
    }

    // MARK: - Downloads (premium-only)

    /// `OdmobileDownloadTrack` result: `{"path":"…"}` on success, `{"error":"…"}`
    /// on failure. Both optional so an empty/malformed reply still decodes.
    struct DownloadResult: Decodable {
        let path: String?
        let error: String?
    }

    /// Downloads `id` into `destDir` ("" -> the engine's shared default folder).
    /// Blocking engine-side (network fetch + Blowfish decrypt), so it runs on the
    /// IO queue. Downloads are premium-only; a Free account comes back with an
    /// error in the result.
    static func download(id: String, destDir: String = "") async -> DownloadResult {
        let json = await run { OdmobileDownloadTrack(id, destDir) }
        return (try? decoder.decode(DownloadResult.self, from: Data(json.utf8)))
            ?? DownloadResult(path: nil, error: nil)
    }
    /// The current download folder (env / config / default).
    static func downloadDir() async -> String { await run { OdmobileDownloadDir() } }
    /// Persists the download folder ("" resets to the default); true on success.
    @discardableResult
    static func setDownloadDir(_ path: String) async -> Bool { await run { OdmobileSetDownloadDir(path) } }
    /// True when the current track is Deezer's 30-second preview fallback — rare;
    /// a Free account normally streams the full 128 kbps track. Cheap engine read,
    /// so (like `state`/`volume`) it's called directly.
    static func isPreview() -> Bool { OdmobileIsPreview() }

    // MARK: - Free-tier ads / play reporting (Deezer Free only)

    /// Whether the Free-tier ads / play-reporting opt-out is on. Only meaningful
    /// for a Deezer Free account — paid plans carry no ads.
    static func adsDisabled() async -> Bool { await run { OdmobileAdsDisabled() } }
    @discardableResult
    static func setAdsDisabled(_ disabled: Bool) async -> Bool { await run { OdmobileSetAdsDisabled(disabled) } }

    // MARK: - Misc

    static func fetch(_ url: String) async -> Data? { await runFetch { OdmobileFetch(url) } }
}
