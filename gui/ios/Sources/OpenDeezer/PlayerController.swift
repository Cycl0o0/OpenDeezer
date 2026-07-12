import AVFoundation
import Foundation
import MediaPlayer
import UIKit

/// Repeat modes mirrored from the engine's `SetRepeat` (0 off, 1 all, 2 one).
enum RepeatMode: Int, CaseIterable {
    case off = 0, all = 1, one = 2

    var systemImage: String {
        switch self {
        case .off: return "repeat"
        case .all: return "repeat"
        case .one: return "repeat.1"
        }
    }

    var accessibilityValue: String {
        switch self {
        case .off: return String(localized: "Off")
        case .all: return String(localized: "Repeat All")
        case .one: return String(localized: "Repeat One")
        }
    }
}

/// Owns the play queue and mirrors the Go engine's transport state for the UI.
/// Polls `OdmobileFinishedCount()` every 0.4s to notice track completion (the
/// engine has no completion callback across the gomobile boundary) and drives
/// auto-advance client-side, honoring shuffle/repeat — the same pattern the
/// desktop/Android GUIs use.
@MainActor
final class PlayerController: ObservableObject {
    static let shared = PlayerController()

    @Published private(set) var queue: [Track] = []
    @Published private(set) var currentIndex: Int?
    @Published private(set) var current: Track?
    @Published private(set) var state: PlayerState = .stopped
    @Published private(set) var positionMs: Int64 = 0
    @Published private(set) var durationMs: Int64 = 0
    @Published var isShuffle = false {
        didSet {
            let on = isShuffle
            Task { await Engine.setShuffle(on) }
            // Shuffle changes what "next" means: re-arm (or discard) the preload.
            armPreload()
        }
    }
    @Published private(set) var repeatMode: RepeatMode = .off
    @Published private(set) var formatLabel: String = ""
    /// True when the engine fell back to Deezer's 30-second preview for the
    /// current track (rare — Free normally streams the full 128 kbps track).
    @Published private(set) var isPreview = false
    @Published private(set) var artwork: UIImage?
    @Published private(set) var connectedDeviceAddr: String = ""
    @Published private(set) var volume: Double = Engine.volume()

    var isPlaying: Bool { state == .playing }
    var hasNowPlaying: Bool { current != nil }
    var canGoNext: Bool {
        guard let currentIndex, !queue.isEmpty else { return false }
        return (isShuffle && queue.count > 1) || repeatMode == .all || currentIndex < queue.count - 1
    }

    private var timer: Timer?
    private var lastFinished = 0
    // Gapless: id of the queue track armed engine-side via Engine.preload, or
    // nil when nothing is armed. When the armed track matches the deterministic
    // next entry at a track boundary, tick() advances the UI pointer without
    // re-issuing a play (the engine already swapped into the preloaded stream).
    private var preloadedId: String?
    private var artworkToken = 0
    private var playbackToken = 0
    private var playbackRequestPending = false
    private var seekToken = 0
    private var seeking = false
    private var volumeTask: Task<Void, Never>?
    private var wasPlayingBeforeInterruption = false
    private var outputSuspended = false
    private var idleSince: Date?

    /// Keep the OS audio output alive this long after pause/stop so brief
    /// track transitions don't tear it down, then suspend it (the oto queue
    /// otherwise renders silence forever, which keeps the app from ever being
    /// suspended in the background).
    private static let outputIdleGrace: TimeInterval = 5

    private init() {
        configureRemoteCommandCenter()
        observeAudioSessionNotifications()
    }

    /// Starts the 0.4s poll loop; call once the engine is logged in.
    func start() {
        guard timer == nil else { return }
        lastFinished = Engine.finishedCount()
        let t = Timer(timeInterval: 0.4, repeats: true) { [weak self] _ in
            Task { @MainActor in self?.tick() }
        }
        RunLoop.main.add(t, forMode: .common)
        timer = t
    }

    private func tick() {
        if !playbackRequestPending {
            state = PlayerState(rawValue: Engine.state()) ?? .stopped
            if !seeking { positionMs = Engine.positionMS() }
            // The engine reports 0 for podcast episodes; fall back to the
            // client-known duration so the scrubber / lock screen stay usable.
            let engineDuration = Engine.durationMS()
            durationMs = engineDuration > 0 ? engineDuration : (current?.durationMs ?? 0)
            formatLabel = Engine.format()
            isPreview = current != nil && Engine.isPreview()
        }
        connectedDeviceAddr = Engine.connectedDevice()

        let finished = Engine.finishedCount()
        if playbackRequestPending {
            lastFinished = finished
        } else if finished != lastFinished {
            lastFinished = finished
            // Gapless: if the deterministic next track was preloaded and the
            // engine kept playing across the boundary, it already swapped into
            // it — advance the UI pointer without re-issuing a play (which
            // would cut the audio for a full network re-resolve). Any other
            // outcome (preload failed/cleared, engine stopped, mode changed)
            // falls back to the plain advance.
            if let n = deterministicNextIndex(), let armed = preloadedId,
               queue.indices.contains(n), queue[n].id == armed,
               PlayerState(rawValue: Engine.state()) == .playing {
                seamlessAdvance(to: n)
            } else {
                advance(auto: true)
            }
        }
        updateOutputSuspension()
        updateNowPlayingInfo()
    }

    // MARK: - Queue control

    func play(_ track: Track, in tracks: [Track]? = nil) {
        let list = tracks ?? [track]
        let idx = list.firstIndex(of: track) ?? 0
        queue = list
        currentIndex = idx
        playCurrent()
        syncEngineQueue()
    }

    func playQueue(_ tracks: [Track], startAt index: Int = 0) {
        guard !tracks.isEmpty else { return }
        queue = tracks
        currentIndex = min(max(index, 0), tracks.count - 1)
        playCurrent()
        syncEngineQueue()
    }

    func playEpisode(_ episode: Episode) {
        queue = []
        currentIndex = nil
        // Episodes empty the queue, so any armed next-track preload is stale.
        preloadedId = nil
        Task { await Engine.clearPreload() }
        // Mirror the now-empty queue to the engine (podcasts aren't queue items).
        syncEngineQueue()
        current = Track(episode: episode)
        seekToken += 1
        seeking = false
        positionMs = 0
        durationMs = episode.durationMs
        state = .loading
        formatLabel = ""
        isPreview = false
        loadArtwork(url: episode.artworkUrl)
        // Re-baseline so a finish from the previous track landing in the same
        // poll window doesn't trigger a spurious advance.
        lastFinished = Engine.finishedCount()
        beginAudioPlayback()
        playbackToken += 1
        let token = playbackToken
        playbackRequestPending = true
        Task {
            let started = await Engine.playEpisode(id: episode.id, durationMs: episode.durationMs)
            guard token == playbackToken else { return }
            lastFinished = Engine.finishedCount()
            playbackRequestPending = false
            state = started ? (PlayerState(rawValue: Engine.state()) ?? .loading) : .errored
        }
    }

    private func playCurrent() {
        guard let idx = currentIndex, queue.indices.contains(idx) else { return }
        let track = queue[idx]
        current = track
        seekToken += 1
        seeking = false
        loadArtwork(url: track.artworkUrl)
        positionMs = 0
        durationMs = track.durationMs
        state = .loading
        formatLabel = ""
        isPreview = false
        // A full play discards any armed preload engine-side; forget ours too
        // so armPreload() re-arms (or clears) from scratch once the start lands.
        preloadedId = nil
        // Re-baseline so a finish from the previous track landing in the same
        // poll window doesn't trigger a spurious advance past this one.
        lastFinished = Engine.finishedCount()
        beginAudioPlayback()
        playbackToken += 1
        let token = playbackToken
        playbackRequestPending = true
        Task {
            let started = await Engine.play(id: track.id, durationMs: track.durationMs)
            guard token == playbackToken else { return }
            lastFinished = Engine.finishedCount()
            playbackRequestPending = false
            state = started ? (PlayerState(rawValue: Engine.state()) ?? .loading) : .errored
            // Gapless: arm the deterministic next track once this one is live.
            if started { armPreload() }
        }
    }

    // MARK: - Gapless preload

    /// True when the engine performs a seamless boundary swap (gapless or
    /// crossfade) — the only modes where preloading the next track pays off.
    private var seamless: Bool { Engine.gapless() || Engine.crossfadeMS() > 0 }

    /// The queue position tick() will advance to when the current track ends
    /// naturally, or nil when it isn't knowable up front (shuffle, repeat-one,
    /// end of queue without repeat-all).
    private func deterministicNextIndex() -> Int? {
        guard let idx = currentIndex, !queue.isEmpty, !isShuffle, repeatMode != .one else { return nil }
        if idx + 1 < queue.count { return idx + 1 }
        if repeatMode == .all { return 0 }
        return nil
    }

    /// The track worth preloading, or nil. Never preload while routed to a
    /// Connect remote: the remote streams its own audio, so a local preload
    /// would download a duplicate stream for nothing.
    private func nextTrackForPreload() -> Track? {
        guard seamless, connectedDeviceAddr.isEmpty, let n = deterministicNextIndex() else { return nil }
        return queue[n]
    }

    /// Arms the deterministic next track engine-side (or discards a stale
    /// preload when the upcoming track is no longer knowable). A failed
    /// preload just leaves `preloadedId` nil, so tick() falls back to the
    /// plain full-resolve advance at the boundary.
    private func armPreload() {
        let next = nextTrackForPreload()
        if let next {
            guard next.id != preloadedId else { return }
            preloadedId = next.id
            Task {
                let ok = await Engine.preload(id: next.id)
                if !ok, preloadedId == next.id { preloadedId = nil }
            }
        } else if preloadedId != nil {
            preloadedId = nil
            Task { await Engine.clearPreload() }
        }
    }

    /// Advances the UI to `queue[n]` after the engine performed a seamless
    /// swap — the preloaded track is already audible, so no `Engine.play`
    /// re-resolve, no `.loading` flash, then the new next is armed.
    private func seamlessAdvance(to n: Int) {
        currentIndex = n
        let track = queue[n]
        current = track
        seekToken += 1
        seeking = false
        positionMs = 0
        let engineDuration = Engine.durationMS()
        durationMs = engineDuration > 0 ? engineDuration : track.durationMs
        state = .playing
        formatLabel = Engine.format()
        isPreview = Engine.isPreview()
        loadArtwork(url: track.artworkUrl)
        preloadedId = nil
        armPreload()
        // Keep the engine cursor aligned after a gapless promote.
        syncEngineQueueIndex()
        updateNowPlayingInfo()
    }

    func togglePlayPause() {
        guard hasNowPlaying else { return }
        if !isPlaying { beginAudioPlayback() }
        Task { await Engine.togglePause() }
    }
    func pause() { Task { await Engine.pause() } }
    func resume() {
        beginAudioPlayback()
        Task { await Engine.resume() }
    }

    func seek(to ms: Int64) {
        seekToken += 1
        let token = seekToken
        seeking = true
        positionMs = ms
        Task {
            await Engine.seek(ms: ms)
            if token == seekToken { seeking = false }
        }
    }

    func next() { advance(auto: false) }

    func previous() {
        if positionMs > 3000 || currentIndex == nil {
            seek(to: 0)
            return
        }
        guard let idx = currentIndex, !queue.isEmpty else { return }
        var newIndex = idx - 1
        if newIndex < 0 {
            newIndex = repeatMode == .all ? queue.count - 1 : 0
        }
        currentIndex = newIndex
        playCurrent()
        syncEngineQueueIndex()
    }

    private func advance(auto: Bool) {
        guard !queue.isEmpty, let idx = currentIndex else {
            if auto { stopPlayback() }
            return
        }
        if auto && repeatMode == .one {
            // The engine is Stopped once a track finishes (its decode loop has
            // exited), so seek+resume are no-ops — restart the track instead.
            playCurrent()
            return
        }
        var newIndex: Int
        if isShuffle && queue.count > 1 {
            repeat { newIndex = Int.random(in: 0..<queue.count) } while newIndex == idx
        } else {
            newIndex = idx + 1
        }
        if newIndex >= queue.count {
            guard repeatMode == .all else {
                if auto { stopPlayback() }
                return
            }
            newIndex = 0
        }
        currentIndex = newIndex
        playCurrent()
        syncEngineQueueIndex()
    }

    func stopPlayback() {
        playbackToken += 1
        playbackRequestPending = false
        seekToken += 1
        seeking = false
        preloadedId = nil
        Task {
            await Engine.stop()
            await Engine.clearPreload()
        }
        current = nil
        currentIndex = nil
        queue = []
        state = .stopped
        isPreview = false
        // Clear the engine-mirrored queue too so remotes don't show a stale one.
        syncEngineQueue()
        MPNowPlayingInfoCenter.default().nowPlayingInfo = nil
    }

    // MARK: - Engine queue sync (app queue -> engine)

    /// Push the whole app queue + current index into the engine so web/Connect
    /// remotes see the real queue and `/next`+`/prev` walk it. Called whenever the
    /// queue CONTENT changes; index-only moves use `syncEngineQueueIndex()`.
    /// Best-effort — the JSON is built on the main actor, the sync runs off it.
    private func syncEngineQueue() {
        let json = Engine.queueJSON(queue)
        let idx = currentIndex ?? -1
        Task {
            await Engine.setQueueJSON(json)
            await Engine.setQueueIndex(idx)
        }
    }

    /// Align the engine queue's cursor to the current index after an index-only
    /// change (advance / previous / seamless gapless promote).
    private func syncEngineQueueIndex() {
        let idx = currentIndex ?? -1
        Task { await Engine.setQueueIndex(idx) }
    }

    // MARK: - Radio (song / artist mixes)

    /// Start a "song radio" seeded from `track`. Mirrors the Flow load+play path;
    /// falls back to just the seed track if the mix can't be fetched.
    func startRadio(seededBy track: Track) {
        Task {
            let mix = (try? await Engine.trackMix(track.id)) ?? []
            if mix.isEmpty { play(track) } else { playQueue(mix, startAt: 0) }
        }
    }

    /// Start an "artist radio" seeded from an artist id. No-op if the mix is empty.
    func startArtistRadio(artistID: String) {
        Task {
            let mix = (try? await Engine.artistMix(artistID)) ?? []
            if !mix.isEmpty { playQueue(mix, startAt: 0) }
        }
    }

    func toggleShuffle() { isShuffle.toggle() }

    func cycleRepeat() {
        repeatMode = RepeatMode(rawValue: (repeatMode.rawValue + 1) % 3) ?? .off
        let mode = repeatMode.rawValue
        Task { await Engine.setRepeat(mode) }
        // Repeat changes what "next" means: re-arm (or discard) the preload.
        armPreload()
    }

    func setVolume(_ v: Double) {
        volume = v
        // Coalesce slider updates before they reach the serial engine queue;
        // a Connect-routed volume request can otherwise block for 15 seconds.
        volumeTask?.cancel()
        let delay: UInt64 = connectedDeviceAddr.isEmpty ? 30_000_000 : 200_000_000
        volumeTask = Task {
            do {
                try await Task.sleep(nanoseconds: delay)
            } catch {
                return
            }
            guard !Task.isCancelled else { return }
            await Engine.setVolume(v)
            if !Task.isCancelled { volumeTask = nil }
        }
    }

    // MARK: - Audio session / output lifecycle

    /// Activates the session and wakes the OS output right before local
    /// playback (re)starts. No-op while routed to a Connect device — remote
    /// playback must not interrupt other apps' audio on this phone.
    private func beginAudioPlayback() {
        guard connectedDeviceAddr.isEmpty else { return }
        idleSince = nil
        try? AVAudioSession.sharedInstance().setActive(true, options: [])
        if outputSuspended {
            outputSuspended = false
            Engine.setOutputSuspended(false)
        }
    }

    /// Suspends the OS output + deactivates the session once playback has
    /// been idle (paused/stopped/routed remotely) past the grace period, so
    /// iOS can suspend the app instead of receiving silence forever.
    private func updateOutputSuspension() {
        let locallyPlaying = state == .playing && connectedDeviceAddr.isEmpty
        if locallyPlaying {
            idleSince = nil
            // Self-heal: the grace timer (or an interruption) may have
            // suspended the output while a play request was still resolving
            // its stream URL; without this the AudioQueue stays paused and
            // the app shows "playing" in silence.
            if outputSuspended { beginAudioPlayback() }
            return
        }
        if idleSince == nil { idleSince = Date() }
        if !outputSuspended, let since = idleSince,
           Date().timeIntervalSince(since) >= Self.outputIdleGrace {
            suspendOutput()
        }
    }

    /// Order matters: stop the queue first, then deactivate (deactivating a
    /// session with a running AudioQueue fails).
    private func suspendOutput() {
        outputSuspended = true
        Engine.setOutputSuspended(true)
        try? AVAudioSession.sharedInstance().setActive(false, options: .notifyOthersOnDeactivation)
    }

    private func observeAudioSessionNotifications() {
        let center = NotificationCenter.default
        let session = AVAudioSession.sharedInstance()
        center.addObserver(forName: AVAudioSession.interruptionNotification, object: session, queue: nil) { [weak self] note in
            let userInfo = note.userInfo
            Task { @MainActor in self?.handleInterruption(userInfo) }
        }
        center.addObserver(forName: AVAudioSession.routeChangeNotification, object: session, queue: nil) { [weak self] note in
            let userInfo = note.userInfo
            Task { @MainActor in self?.handleRouteChange(userInfo) }
        }
    }

    private func handleInterruption(_ userInfo: [AnyHashable: Any]?) {
        guard let raw = userInfo?[AVAudioSessionInterruptionTypeKey] as? UInt,
              let type = AVAudioSession.InterruptionType(rawValue: raw) else { return }
        switch type {
        case .began:
            // Suspend immediately (no grace): the system paused the queue
            // anyway, and only an explicit resume restarts it afterwards.
            wasPlayingBeforeInterruption = isPlaying
            if isPlaying { pause() }
            suspendOutput()
        case .ended:
            let optRaw = (userInfo?[AVAudioSessionInterruptionOptionKey] as? UInt) ?? 0
            let options = AVAudioSession.InterruptionOptions(rawValue: optRaw)
            if wasPlayingBeforeInterruption && options.contains(.shouldResume) {
                resume()
            }
            wasPlayingBeforeInterruption = false
        @unknown default:
            break
        }
    }

    private func handleRouteChange(_ userInfo: [AnyHashable: Any]?) {
        guard let raw = userInfo?[AVAudioSessionRouteChangeReasonKey] as? UInt,
              let reason = AVAudioSession.RouteChangeReason(rawValue: raw) else { return }
        // Headphones unplugged / Bluetooth device gone: don't blast the speaker.
        if reason == .oldDeviceUnavailable && isPlaying { pause() }
    }

    // MARK: - Connect

    func connect(to device: Device) async -> Bool {
        let ok = await Engine.connectDevice(device.addr)
        if ok {
            connectedDeviceAddr = device.addr
            // Remote playback streams on the remote: discard the local preload.
            armPreload()
        }
        return ok
    }
    func disconnect() async {
        await Engine.disconnectDevice()
        connectedDeviceAddr = ""
        // Back to local playback: re-arm the next queue entry if one is knowable.
        armPreload()
    }

    // MARK: - Artwork

    private func loadArtwork(url: String) {
        artworkToken += 1
        let token = artworkToken
        artwork = nil
        guard !url.isEmpty else { return }
        Task {
            if let cached = await ImageCache.shared.image(for: url) {
                if token == artworkToken { artwork = cached }
                return
            }
            guard let data = await Engine.fetch(url), let img = UIImage(data: data) else { return }
            await ImageCache.shared.set(img, for: url)
            if token == artworkToken { artwork = img }
        }
    }

    // MARK: - Now Playing Info Center + Remote Command Center

    private func updateNowPlayingInfo() {
        guard let track = current else { return }
        var info: [String: Any] = [
            MPMediaItemPropertyTitle: track.name,
            MPMediaItemPropertyArtist: track.artistLine,
            MPMediaItemPropertyAlbumTitle: track.albumName,
            MPMediaItemPropertyPlaybackDuration: Double(durationMs) / 1000,
            MPNowPlayingInfoPropertyElapsedPlaybackTime: Double(positionMs) / 1000,
            MPNowPlayingInfoPropertyPlaybackRate: isPlaying ? 1.0 : 0.0,
        ]
        if let artwork {
            info[MPMediaItemPropertyArtwork] = MPMediaItemArtwork(boundsSize: artwork.size) { _ in artwork }
        }
        MPNowPlayingInfoCenter.default().nowPlayingInfo = info
    }

    private func configureRemoteCommandCenter() {
        let cc = MPRemoteCommandCenter.shared()
        cc.playCommand.addTarget { [weak self] _ in
            Task { @MainActor in self?.resume() }
            return .success
        }
        cc.pauseCommand.addTarget { [weak self] _ in
            Task { @MainActor in self?.pause() }
            return .success
        }
        cc.togglePlayPauseCommand.addTarget { [weak self] _ in
            Task { @MainActor in self?.togglePlayPause() }
            return .success
        }
        cc.nextTrackCommand.addTarget { [weak self] _ in
            Task { @MainActor in self?.next() }
            return .success
        }
        cc.previousTrackCommand.addTarget { [weak self] _ in
            Task { @MainActor in self?.previous() }
            return .success
        }
        cc.changePlaybackPositionCommand.addTarget { [weak self] event in
            guard let event = event as? MPChangePlaybackPositionCommandEvent else { return .commandFailed }
            let position = Int64(event.positionTime * 1000)
            Task { @MainActor in self?.seek(to: position) }
            return .success
        }
    }
}

private extension Track {
    /// Adapts a podcast episode to a Track so the Now Playing UI can render it
    /// like any other queue item (mirrors the engine's `Episode.AsTrack`).
    init(episode: Episode) {
        self.init(
            id: episode.id, name: episode.title, durationMs: episode.durationMs,
            artistLine: episode.podcastName, albumName: episode.podcastName,
            artworkUrl: episode.artworkUrl
        )
    }
}
