import Foundation
import SwiftUI
import AppKit

// Section is the sidebar selection.
enum Section: Hashable {
    case home, liked, playlists, search, charts, flow, podcasts
}

enum RepeatMode: Int { case off, all, one }

@MainActor
final class AppState: ObservableObject {
    @Published var loggedIn = false
    // Free-account gate: OpenDeezer needs on-demand streaming (Premium). When a
    // Deezer Free account logs in (account.premium == false) the whole app is
    // replaced by FreeAccountBlockedView; no browsing or playback is wired up.
    @Published var accountBlocked = false
    @Published var loginError: String?
    @Published var busy = false
    @Published var userID = ""
    @Published var account: Account?            // plan + entitlements (DZAccountJSON)
    @Published var replayGain = false           // loudness normalization (engine-owned)
    @Published var showCredits = false
    @Published var showSettings = false

    // Update check (GitHub releases) — silent + non-intrusive: checked once in
    // the background on launch (see start()); a small dismissible banner
    // appears only when a newer version exists. `updateCheckStatus` +
    // `checkingUpdate` back the manual "Check for Updates" button in About.
    @Published var updateInfo: UpdateInfo?
    @Published var showUpdateBanner = false
    @Published var updateCheckStatus: String?
    @Published var checkingUpdate = false

    // Embedded Deezer login webview + manual-ARL fallback (LoginGate).
    @Published var showLoginWeb = false
    @Published var manualARL = ""
    private var webLoginAttempted = false  // de-dupes cookie-observer firings

    // Persisted preferences (audio quality, close-to-tray).
    @Published var settings = AppSettings.load()
    private var started = false // guards start() against repeated onAppear

    // OS Now Playing surface + menu-bar tray / background-playback controller.
    let nowPlaying = NowPlayingController()
    let tray = TrayController()

    @Published var section: Section = .home
    @Published var homeData: HomeResponse?       // loaded by loadHome() / DZHomeJSON
    @Published var homeLoading = false
    @Published var tracks: [Track] = []          // current track list / queue
    @Published var listTitle = "Liked Songs"
    @Published var listArtwork = ""              // hero artwork (empty => Liked gradient)
    @Published var listIsLiked = true            // hero style: gradient vs artwork
    @Published var listHeroSymbol = "heart.fill" // glyph drawn on the gradient hero
    @Published var listSubtitle = ""
    @Published var playlists: [Playlist] = []
    @Published var searchTracks: [Track] = []
    @Published var searchAlbums: [Album] = []
    @Published var searchArtists: [ArtistInfo] = []
    @Published var searchPlaylists: [Playlist] = []
    @Published var query = ""

    // Liked-track ids — seeded from favorites, toggled locally for the heart UI.
    // (No is-liked query exists; this is a best-effort local mirror.)
    @Published var likedIDs: Set<String> = []

    // Charts (DZChartsJSON): tracks drive the shared hero/track-list, the rest
    // render as rails below.
    @Published var chartAlbums: [Album] = []
    @Published var chartArtists: [ArtistInfo] = []
    @Published var chartPlaylists: [Playlist] = []

    // Add-to-playlist picker (track action).
    @Published var showAddToPlaylist = false
    @Published var addTarget: Track?
    @Published var pickerPlaylists: [Playlist] = []
    @Published var pickerLoading = false

    // Playlist management (create / rename / delete on the Playlists view).
    @Published var showCreatePlaylist = false
    @Published var renameTarget: Playlist?
    @Published var deleteTarget: Playlist?

    // Podcasts browse.
    @Published var podcastQuery = ""
    @Published var podcasts: [Podcast] = []
    @Published var podcastEpisodes: [Episode] = []
    @Published var openedPodcast: Podcast?
    @Published var podcastsLoading = false

    // Audio output devices (Settings).
    @Published var audioDevices: [AudioDevice] = []
    @Published var currentAudioDeviceID = ""

    // OpenDeezer Connect (device picker): discovered devices + connected target.
    @Published var showDevicePicker = false
    @Published var devices: [Device] = []
    @Published var devicesLoading = false
    @Published var connectedDeviceAddr = ""   // "" => playing on this computer

    // Lyrics sheet (current track's lyrics; synced highlight driven by `tick`).
    @Published var showLyrics = false
    @Published var currentLyrics: Lyrics?     // lyrics for `lyricsTrackID`; nil => none/loading
    @Published var lyricsLoading = false
    private var lyricsCache: [String: Lyrics] = [:]   // per-track-id cache
    private var lyricsTrackID: String?                 // which track currentLyrics belongs to

    // Artist detail sheet (DZArtistProfileJSON).
    @Published var showArtist = false
    @Published var artistProfile: ArtistProfile?
    @Published var artistLoading = false

    // playback
    @Published var current: Track?
    @Published var state: PlayerState = .stopped
    @Published var outputFormat = "" // human label of the current stream format
    @Published var positionMs: Int64 = 0
    @Published var durationMs: Int64 = 0
    @Published var volume: Double = 1
    @Published var shuffle = false
    @Published var repeatMode: RepeatMode = .off
    @Published var playingEpisode = false   // current item is a podcast episode (standalone)

    private var queueIndex = 0
    private var lastFinished = 0
    private var lastState: PlayerState = .stopped
    private var timer: Timer?

    // MARK: login

    // start() runs once on launch. If a saved ARL exists we auto-log-in;
    // otherwise we drop the user on LoginGate to log in with Deezer (or paste an
    // ARL manually). onAppear can fire more than once, hence the `started` guard.
    func start() {
        guard !started else { return }
        started = true
        checkForUpdates() // silent, backgrounded — never blocks startup
        guard let arl = Self.loadARL(), !arl.isEmpty else {
            return // no saved ARL — LoginGate offers the login options
        }
        attemptLogin(arl: arl, persist: false)
    }

    // User tapped "Log in with Deezer": open the embedded Deezer login webview.
    func beginWebLogin() {
        loginError = nil
        webLoginAttempted = false
        showLoginWeb = true
    }

    // Called by the embedded webview when a non-empty arl cookie is captured.
    // De-duped so the cookie observer can fire repeatedly without re-attempting.
    // On success the sheet dismisses into the app; on failure it stays open with
    // the error banner so the user can retry or cancel into manual entry.
    func webLoginCaptured(arl: String) {
        let v = arl.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !v.isEmpty, !webLoginAttempted, !busy else { return }
        webLoginAttempted = true
        attemptLogin(arl: v, persist: true) { ok in
            if ok { self.showLoginWeb = false }
            else { self.webLoginAttempted = false } // allow a fresh capture/retry
        }
    }

    // Manual ARL fallback (paste-the-cookie flow), kept alongside the webview.
    func loginWithManualARL() {
        let v = manualARL.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !v.isEmpty, !busy else { return }
        attemptLogin(arl: v, persist: true)
    }

    // Shared login path: DZInit off the main thread, then wire up the session on
    // success or surface an error. `persist` writes the ARL to the config file the
    // frontend reads at startup so the next launch auto-logs-in.
    private func attemptLogin(arl: String, persist: Bool,
                              completion: ((Bool) -> Void)? = nil) {
        busy = true
        loginError = nil
        Task.detached {
            let ok = Core.initialize(arl: arl)
            // Plan/entitlements are populated by login; fetch off the main thread.
            let acct = ok ? Core.account() : nil
            await MainActor.run {
                self.busy = false
                if ok {
                    if persist { Self.saveARL(arl) }
                    self.finishLogin(account: acct)
                } else {
                    self.loginError = "Login failed — invalid or expired ARL."
                }
                completion?(ok)
            }
        }
    }

    // Post-login wiring shared by auto / web / manual login. Runs on the main
    // actor after a successful DZInit.
    private func finishLogin(account acct: Account?) {
        userID = Core.userID
        account = acct
        // Free-account gate: a Deezer Free plan (premium == false) can't stream
        // on-demand, so block the app here — skip all session/playback wiring so
        // browsing and playback are never reachable. Reached by auto, web and
        // manual-ARL login alike (they all funnel through finishLogin).
        if let a = acct, !a.premium {
            accountBlocked = true
            loggedIn = true   // leave LoginGate; FreeAccountBlockedView takes over
            return
        }
        accountBlocked = false
        loggedIn = true
        volume = Core.volume
        replayGain = Core.replayGain
        // Apply persisted audio quality + gapless/crossfade, claim the OS Now
        // Playing command handlers, and wire up the tray.
        Core.setQuality(settings.quality)
        Core.setGapless(settings.gapless)
        Core.setCrossfadeMS(settings.crossfadeMS)
        currentAudioDeviceID = Core.currentAudioDevice
        connectedDeviceAddr = Core.connectedDevice
        nowPlaying.registerCommands(app: self)
        tray.closeToTray = settings.closeToTray
        tray.attach(app: self)
        startTimer()
        loadHome()
    }

    static func loadARL() -> String? {
        if let v = ProcessInfo.processInfo.environment["DEEZER_ARL"], !v.isEmpty { return v }
        let home = FileManager.default.homeDirectoryForCurrentUser
        // Prefer the new config dir; fall back to the legacy one.
        for dir in ["opendeezer", "deezertui"] {
            let path = home.appendingPathComponent(".config/\(dir)/arl.txt")
            if let s = (try? String(contentsOf: path, encoding: .utf8))?
                .trimmingCharacters(in: .whitespacesAndNewlines), !s.isEmpty {
                return s
            }
        }
        return nil
    }

    // Persist the ARL to the SAME file loadARL() reads first (~/.config/opendeezer
    // /arl.txt) so the next launch auto-logs-in without the webview.
    static func saveARL(_ arl: String) {
        let dir = FileManager.default.homeDirectoryForCurrentUser
            .appendingPathComponent(".config/opendeezer", isDirectory: true)
        try? FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        let url = dir.appendingPathComponent("arl.txt")
        try? arl.write(to: url, atomically: true, encoding: .utf8)
    }

    // MARK: update check

    // Checks GitHub for a newer OpenDeezer release off the main thread. Called
    // once, silently, on launch (start()) — a dismissible banner then appears
    // only when a newer version exists. `manual` additionally drives the
    // About "Check for Updates" button, which reports "You're up to date."
    // when there's nothing new. Never downloads or installs anything itself.
    func checkForUpdates(manual: Bool = false) {
        if manual {
            checkingUpdate = true
            updateCheckStatus = nil
        }
        Task.detached {
            let info = Core.checkUpdate()
            await MainActor.run {
                self.checkingUpdate = false
                if let info, info.hasUpdate {
                    self.updateInfo = info
                    self.showUpdateBanner = true
                    if manual { self.updateCheckStatus = "OpenDeezer \(info.latest) is available." }
                } else if manual {
                    self.updateCheckStatus = "You're up to date."
                }
            }
        }
    }

    // Opens the release page in the user's default browser. Download/install
    // is intentionally out of scope — this just points the user at GitHub.
    func openUpdateURL() {
        guard let info = updateInfo, let url = URL(string: info.url) else { return }
        NSWorkspace.shared.open(url)
    }

    func dismissUpdateBanner() { showUpdateBanner = false }

    // Sidebar/about label for the signed-in account, e.g. "Jane · Premium".
    var accountLabel: String {
        if let a = account, !a.name.isEmpty {
            return a.offer.isEmpty ? a.name : "\(a.name) · \(a.offer)"
        }
        return userID.isEmpty ? "—" : "user \(userID)"
    }

    // Warns when the chosen quality exceeds the account's entitlement; nil otherwise.
    var qualityEntitlementNote: String? {
        guard let a = account else { return nil }
        let plan = a.offer.isEmpty ? "plan" : "\(a.offer) plan"
        if settings.quality >= 2 && !a.canHifi {
            return "Your \(plan) doesn't include HiFi (FLAC)."
        }
        if settings.quality >= 1 && !a.canHq {
            return "Your \(plan) doesn't include High (MP3 320)."
        }
        return nil
    }

    // MARK: browse

    // loadHome fetches the Home aggregator (DZHomeJSON) off the main thread.
    // Best-effort: empty sections simply show nothing on the home screen.
    func loadHome() {
        homeLoading = true
        Task.detached {
            let h = Core.home()
            await MainActor.run {
                self.homeData = h
                self.homeLoading = false
            }
        }
    }

    func loadFavorites() {
        listTitle = "Liked Songs"
        listArtwork = ""
        listIsLiked = true
        listHeroSymbol = "heart.fill"
        busy = true
        Task.detached {
            let ts = Core.favorites()
            await MainActor.run {
                self.tracks = ts
                self.likedIDs = Set(ts.map { $0.id })   // seed the heart UI
                self.busy = false
            }
        }
    }
    func loadPlaylists() {
        tracks = []                  // show the grid, not a stale track list
        busy = true
        Task.detached {
            let ps = Core.playlists()
            await MainActor.run { self.playlists = ps; self.busy = false }
        }
    }
    func openPlaylist(_ p: Playlist) {
        section = .playlists
        listTitle = p.name
        listArtwork = p.artworkUrl
        listIsLiked = false
        listSubtitle = p.owner.isEmpty ? "Playlist" : "Playlist · \(p.owner)"
        runList { Core.playlistTracks(p.id) }
    }
    // Global charts: tracks drive the shared hero/track-list; albums, artists and
    // playlists render as rails below (see ChartsView).
    func loadCharts() {
        section = .charts
        listTitle = "Charts"
        listIsLiked = false
        listSubtitle = "Top worldwide"
        busy = true
        Task.detached {
            let c = Core.charts()
            await MainActor.run {
                self.tracks = c?.tracks ?? []
                self.chartAlbums = c?.albums ?? []
                self.chartArtists = c?.artists ?? []
                self.chartPlaylists = c?.playlists ?? []
                self.listArtwork = self.tracks.first?.artworkUrl ?? ""
                self.busy = false
            }
        }
    }

    // Flow: load the personalized stream and start playing immediately.
    func loadFlow() {
        section = .flow
        listTitle = "Flow"
        listArtwork = ""
        listIsLiked = true            // gradient hero
        listHeroSymbol = "infinity"
        listSubtitle = ""
        busy = true
        Task.detached {
            let ts = Core.flow()
            await MainActor.run {
                self.tracks = ts
                self.busy = false
                if let first = ts.first {
                    self.shuffle = false
                    self.play(first, in: ts)
                }
            }
        }
    }
    func openAlbum(_ a: Album) {
        listTitle = a.name
        listArtwork = a.artworkUrl
        listIsLiked = false
        listSubtitle = a.artistLine.isEmpty ? "Album" : "Album · \(a.artistLine)"
        runList { Core.albumTracks(a.id) }
    }

    // Play-all / shuffle-all from a hero header.
    func playAll() {
        guard let first = tracks.first else { return }
        shuffle = false
        play(first, in: tracks)
    }
    func shuffleAll() {
        guard !tracks.isEmpty else { return }
        shuffle = true
        play(tracks.randomElement()!, in: tracks)
    }
    func runSearch() {
        let q = query.trimmingCharacters(in: .whitespaces)
        guard !q.isEmpty else { return }
        busy = true
        Task.detached {
            let r = Core.search(q)
            await MainActor.run {
                self.searchTracks = r?.tracks ?? []
                self.searchAlbums = r?.albums ?? []
                self.searchArtists = r?.artists ?? []
                self.searchPlaylists = r?.playlists ?? []
                self.busy = false
            }
        }
    }

    private func runList(_ fetch: @escaping @Sendable () -> [Track]) {
        busy = true
        Task.detached {
            let ts = fetch()
            await MainActor.run { self.tracks = ts; self.busy = false }
        }
    }

    // MARK: lyrics

    // Fetch the current track's lyrics (cached per track id). Called from the
    // lyrics sheet on appear and whenever the playing track changes. A nil
    // result is cached as "no lyrics" so we don't refetch on every tick.
    func loadLyricsIfNeeded() {
        guard let id = current?.id, !id.isEmpty else {
            currentLyrics = nil; lyricsTrackID = nil; lyricsLoading = false
            return
        }
        if lyricsTrackID == id { return }   // already loaded / loading this track
        lyricsTrackID = id
        if let cached = lyricsCache[id] {
            currentLyrics = cached
            return
        }
        currentLyrics = nil
        lyricsLoading = true
        Task.detached {
            let ly = Core.lyrics(id)
            await MainActor.run {
                self.lyricsLoading = false
                // Ignore a stale fetch if the track changed meanwhile.
                guard self.lyricsTrackID == id else { return }
                if let ly { self.lyricsCache[id] = ly }
                self.currentLyrics = ly
            }
        }
    }

    // MARK: artist

    func openArtist(_ id: String) {
        guard !id.isEmpty else { return }
        showArtist = true
        artistProfile = nil
        artistLoading = true
        Task.detached {
            let p = Core.artistProfile(id)
            await MainActor.run {
                self.artistLoading = false
                self.artistProfile = p
            }
        }
    }

    // Open the artist of the now-playing track (first credited artist).
    func openArtistForCurrent() {
        guard let id = current?.artists.first?.id else { return }
        openArtist(id)
    }

    // Navigate from the artist sheet into an album, surfacing it in the main
    // detail column (reuses the existing album-tracks path).
    func openAlbumFromArtist(_ a: Album) {
        showArtist = false
        if section == .search { section = .playlists }  // ensure the track list shows
        openAlbum(a)
    }

    // Open an album from the Charts rails. Route to the shared track-list screen
    // (the charts screen itself only renders chart data).
    func openAlbumFromChart(_ a: Album) {
        section = .playlists
        openAlbum(a)
    }

    // MARK: likes

    func isLiked(_ track: Track) -> Bool { likedIDs.contains(track.id) }
    var isCurrentLiked: Bool {
        guard let c = current, !playingEpisode else { return false }
        return likedIDs.contains(c.id)
    }

    // One-shot like/unlike with optimistic local state (no is-liked query exists).
    func toggleLike(_ track: Track) {
        let id = track.id
        if likedIDs.contains(id) {
            likedIDs.remove(id)
            Task.detached { Core.removeFavorite(id) }
        } else {
            likedIDs.insert(id)
            Task.detached { Core.addFavorite(id) }
        }
    }
    func toggleLikeCurrent() {
        guard let c = current, !playingEpisode else { return }
        toggleLike(c)
    }

    // MARK: add-to-playlist

    func beginAddToPlaylist(_ track: Track) {
        addTarget = track
        pickerPlaylists = []
        pickerLoading = true
        showAddToPlaylist = true
        Task.detached {
            let ps = Core.playlists()
            await MainActor.run {
                self.pickerPlaylists = ps
                self.pickerLoading = false
            }
        }
    }
    func addTargetTrack(toPlaylist playlistID: String) {
        guard let t = addTarget else { return }
        let tid = t.id
        showAddToPlaylist = false
        addTarget = nil
        Task.detached { Core.addToPlaylist(playlistID, tid) }
    }
    func createPlaylistAndAddTarget(title: String) {
        let name = title.trimmingCharacters(in: .whitespaces)
        guard !name.isEmpty, let t = addTarget else { return }
        let tid = t.id
        showAddToPlaylist = false
        addTarget = nil
        Task.detached {
            if let pid = Core.createPlaylist(name) { Core.addToPlaylist(pid, tid) }
        }
    }

    // MARK: playlist management

    func beginRename(_ p: Playlist) { renameTarget = p }

    func createPlaylist(title: String) {
        let name = title.trimmingCharacters(in: .whitespaces)
        guard !name.isEmpty else { return }
        busy = true
        Task.detached {
            _ = Core.createPlaylist(name)
            let ps = Core.playlists()
            await MainActor.run { self.playlists = ps; self.busy = false }
        }
    }
    func renamePlaylist(_ p: Playlist, to title: String) {
        let name = title.trimmingCharacters(in: .whitespaces)
        guard !name.isEmpty else { return }
        let id = p.id
        Task.detached {
            Core.renamePlaylist(id, name)
            let ps = Core.playlists()
            await MainActor.run { self.playlists = ps }
        }
    }
    func deletePlaylist(_ p: Playlist) {
        let id = p.id
        Task.detached {
            Core.deletePlaylist(id)
            let ps = Core.playlists()
            await MainActor.run { self.playlists = ps }
        }
    }

    // MARK: podcasts

    func runPodcastSearch() {
        let q = podcastQuery.trimmingCharacters(in: .whitespaces)
        guard !q.isEmpty else { return }
        openedPodcast = nil
        podcastEpisodes = []
        podcastsLoading = true
        Task.detached {
            let ps = Core.searchPodcasts(q)
            await MainActor.run {
                self.podcasts = ps
                self.podcastsLoading = false
            }
        }
    }
    func openPodcast(_ p: Podcast) {
        openedPodcast = p
        podcastEpisodes = []
        podcastsLoading = true
        Task.detached {
            let eps = Core.podcastEpisodes(p.id)
            await MainActor.run {
                self.podcastEpisodes = eps
                self.podcastsLoading = false
            }
        }
    }
    func closePodcast() { openedPodcast = nil; podcastEpisodes = [] }

    // Play a podcast episode via the plain-stream path. Episodes are standalone
    // (not part of the music queue), so the finished-count handler stops rather
    // than advancing into `tracks`.
    func playEpisode(_ e: Episode) {
        playingEpisode = true
        let t = Track(id: e.id, name: e.title, durationMs: e.durationMs,
                      artists: [], artistLine: openedPodcast?.name ?? "Podcast",
                      albumName: openedPodcast?.name ?? "", artworkUrl: e.artworkUrl,
                      explicit: false)
        current = t
        durationMs = e.durationMs
        positionMs = 0
        lastState = .loading
        nowPlaying.update(track: t, state: .loading, positionMs: 0, durationMs: e.durationMs)
        let id = e.id, dur = e.durationMs
        Task.detached { Core.playEpisode(id, durationMs: dur) }
    }

    // MARK: audio output devices

    func loadAudioDevices() {
        Task.detached {
            let devs = Core.audioDevices()
            let cur = Core.currentAudioDevice
            await MainActor.run {
                self.audioDevices = devs
                self.currentAudioDeviceID = cur
            }
        }
    }
    func setAudioDevice(_ id: String) {
        currentAudioDeviceID = id
        Task.detached { Core.setAudioDevice(id) }
    }

    // MARK: OpenDeezer Connect (device picker)

    var isConnectedRemote: Bool { !connectedDeviceAddr.isEmpty }
    var connectedDeviceName: String {
        devices.first(where: { $0.addr == connectedDeviceAddr })?.name ?? connectedDeviceAddr
    }

    // Run a LAN discovery probe and refresh the connected-device state.
    func discoverDevices() {
        devices = []
        devicesLoading = true
        Task.detached {
            let ds = Core.discoverDevices()
            let cur = Core.connectedDevice
            await MainActor.run {
                self.devices = ds
                self.connectedDeviceAddr = cur
                self.devicesLoading = false
            }
        }
    }
    // Route playback to a device; the existing transport then drives it remotely.
    func connectDevice(_ d: Device) {
        let addr = d.addr
        Task.detached {
            let ok = Core.connectDevice(addr)
            let cur = Core.connectedDevice
            await MainActor.run {
                if ok { self.connectedDeviceAddr = cur }
                self.showDevicePicker = false
            }
        }
    }
    // Return playback to this computer.
    func disconnectDevice() {
        connectedDeviceAddr = ""
        showDevicePicker = false
        Task.detached { Core.disconnectDevice() }
    }

    // MARK: gapless / crossfade (persisted + applied to the engine)

    func setGapless(_ on: Bool) {
        settings.gapless = on
        Core.setGapless(on)
        settings.save()
    }
    func setCrossfadeMS(_ ms: Int) {
        settings.crossfadeMS = ms
        Core.setCrossfadeMS(ms)
        settings.save()
    }

    // True when the engine performs a seamless swap (gapless or crossfade);
    // gates next-track preloading and the no-replay UI advance.
    private var seamless: Bool { settings.gapless || settings.crossfadeMS > 0 }

    // MARK: playback

    func play(_ track: Track, in list: [Track]) {
        tracks = list
        queueIndex = list.firstIndex(of: track) ?? 0
        playCurrent()
    }

    private func playCurrent() {
        guard queueIndex >= 0, queueIndex < tracks.count else { return }
        playingEpisode = false
        let t = tracks[queueIndex]
        current = t
        durationMs = t.durationMs
        positionMs = 0
        lastState = .loading
        // Push the new track to the OS Now Playing surface immediately.
        nowPlaying.update(track: t, state: .loading, positionMs: 0, durationMs: t.durationMs)
        // Preload the deterministic next track so the engine can swap seamlessly.
        let next = nextTrackForPreload()
        Task.detached {
            Core.play(t.id, durationMs: t.durationMs)
            if let n = next { Core.preload(n.id, durationMs: n.durationMs) }
        }
    }

    // The next index the queue will advance to deterministically (nil if none, or
    // when shuffle / repeat-one make it non-deterministic).
    private func deterministicNextIndex() -> Int? {
        guard !tracks.isEmpty, !shuffle, repeatMode != .one else { return nil }
        if queueIndex + 1 < tracks.count { return queueIndex + 1 }
        if repeatMode == .all { return 0 }
        return nil
    }

    // The track to preload for a seamless transition, or nil when not applicable.
    private func nextTrackForPreload() -> Track? {
        guard seamless, !playingEpisode, let n = deterministicNextIndex() else { return nil }
        return tracks[n]
    }

    func togglePause() { Core.togglePause() }

    func next() {
        guard !tracks.isEmpty else { return }
        if shuffle && tracks.count > 1 {
            var n = queueIndex
            while n == queueIndex { n = Int.random(in: 0..<tracks.count) }
            queueIndex = n
        } else if queueIndex + 1 < tracks.count {
            queueIndex += 1
        } else if repeatMode == .all {
            queueIndex = 0
        } else { return }
        playCurrent()
    }

    func prev() {
        guard !tracks.isEmpty else { return }
        if queueIndex > 0 { queueIndex -= 1 }
        playCurrent()
    }

    func setVolume(_ v: Double) {
        volume = v
        Core.setVolume(v)
    }

    func seek(toFraction f: Double) {
        seek(toMs: Int64(max(0, min(1, f)) * Double(durationMs)))
    }

    // seek(toMs:) is the absolute-position seek used by the scrubber and by the
    // OS "change playback position" remote command.
    func seek(toMs ms: Int64) {
        let clamped = max(0, min(ms, durationMs))
        positionMs = clamped          // optimistic; the timer reconciles
        Core.seek(clamped)
        nowPlaying.updatePlayback(state: state, positionMs: clamped, durationMs: durationMs)
    }

    // MARK: settings

    func setQuality(_ level: Int) {
        settings.quality = level
        Core.setQuality(level)
        settings.save()
    }

    func setCloseToTray(_ on: Bool) {
        settings.closeToTray = on
        tray.closeToTray = on
        settings.save()
    }

    // ReplayGain is owned by the engine (no persisted setting); mirror its state.
    func setReplayGain(_ on: Bool) {
        replayGain = on
        Core.setReplayGain(on)
    }

    // Toggle shuffle and forward the change to the connected remote (if any).
    func setShuffle(_ on: Bool) {
        shuffle = on
        Core.setShuffle(on)
    }

    // Advance the repeat cycle (off → all → one → off) and forward to remote.
    func cycleRepeat() {
        repeatMode = RepeatMode(rawValue: (repeatMode.rawValue + 1) % 3) ?? .off
        Core.setRepeat(repeatMode.rawValue)
    }

    // MARK: polling

    private func startTimer() {
        lastFinished = Core.finishedCount
        timer = Timer.scheduledTimer(withTimeInterval: 0.4, repeats: true) { [weak self] _ in
            Task { @MainActor in self?.tick() }
        }
    }

    private func tick() {
        let s = Core.state
        positionMs = Core.positionMs
        durationMs = Core.durationMs
        // Only re-publish to the OS when the playback state actually changes —
        // the system extrapolates elapsed time from the rate between pushes.
        if s != lastState {
            lastState = s
            nowPlaying.updatePlayback(state: s, positionMs: positionMs, durationMs: durationMs)
        }
        state = s
        outputFormat = Core.format
        // Engine-truth now-playing sync. DZNowPlayingJSON reports the track the
        // engine is ACTUALLY playing — started here via the control API, or, when
        // routed through OpenDeezer Connect, the REMOTE device's current track.
        // Adopt it only when it names a different track than what's shown; keep the
        // last display when it reports nothing (nil). Gating on the id means the
        // artwork only reloads on a real track change, so there's no flicker /
        // redundant art fetch. Runs before the finished-count advance so a local
        // queue advance (which sets `current` itself) wins this tick and the engine
        // truth reconciles on the next one.
        if let np = Core.nowPlaying(), np.id != current?.id {
            current = np
            durationMs = np.durationMs
            lastState = s
            nowPlaying.update(track: np, state: s, positionMs: positionMs, durationMs: np.durationMs)
        }
        let f = Core.finishedCount
        if f != lastFinished {
            lastFinished = f
            handleAdvance()
        }
    }

    // Decide what to do when the engine reports a track finished.
    private func handleAdvance() {
        // Episodes are standalone: stop at the end rather than entering the queue.
        if playingEpisode { return }
        if repeatMode == .one { playCurrent(); return }
        // If a seamless transition was armed and the engine is STILL playing, it
        // already swapped to the preloaded next track. Advance the UI's queue
        // pointer WITHOUT re-issuing DZPlay, refresh now-playing, and preload the
        // new next. Otherwise fall back to an explicit play of the next track.
        if seamless, let n = deterministicNextIndex(), Core.state == .playing {
            queueIndex = n
            let t = tracks[queueIndex]
            current = t
            durationMs = t.durationMs
            positionMs = 0
            lastState = .playing
            nowPlaying.update(track: t, state: .playing, positionMs: 0, durationMs: t.durationMs)
            if let next = nextTrackForPreload() {
                Task.detached { Core.preload(next.id, durationMs: next.durationMs) }
            }
        } else {
            next()
        }
    }
}
