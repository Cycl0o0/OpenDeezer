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
    @Published var isShuffle = false { didSet { Engine.setShuffle(isShuffle) } }
    @Published private(set) var repeatMode: RepeatMode = .off
    @Published private(set) var formatLabel: String = ""
    @Published private(set) var artwork: UIImage?
    @Published private(set) var connectedDeviceAddr: String = ""
    @Published private(set) var volume: Double = Engine.volume()

    var isPlaying: Bool { state == .playing }
    var hasNowPlaying: Bool { current != nil }

    private var timer: Timer?
    private var lastFinished = 0
    private var artworkToken = 0
    private var seeking = false

    private init() {
        configureRemoteCommandCenter()
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
        state = PlayerState(rawValue: Engine.state()) ?? .stopped
        if !seeking { positionMs = Engine.positionMS() }
        durationMs = Engine.durationMS()
        formatLabel = Engine.format()
        connectedDeviceAddr = Engine.connectedDevice()

        let finished = Engine.finishedCount()
        if finished != lastFinished {
            lastFinished = finished
            advance(auto: true)
        }
        updateNowPlayingInfo()
    }

    // MARK: - Queue control

    func play(_ track: Track, in tracks: [Track]? = nil) {
        let list = tracks ?? [track]
        let idx = list.firstIndex(of: track) ?? 0
        queue = list
        currentIndex = idx
        playCurrent()
    }

    func playQueue(_ tracks: [Track], startAt index: Int = 0) {
        guard !tracks.isEmpty else { return }
        queue = tracks
        currentIndex = min(max(index, 0), tracks.count - 1)
        playCurrent()
    }

    func playEpisode(_ episode: Episode) {
        queue = []
        currentIndex = nil
        current = Track(episode: episode)
        loadArtwork(url: episode.artworkUrl)
        Task { _ = await Engine.playEpisode(id: episode.id) }
    }

    private func playCurrent() {
        guard let idx = currentIndex, queue.indices.contains(idx) else { return }
        let track = queue[idx]
        current = track
        loadArtwork(url: track.artworkUrl)
        positionMs = 0
        Task {
            _ = await Engine.play(id: track.id, durationMs: track.durationMs)
        }
    }

    func togglePlayPause() {
        guard hasNowPlaying else { return }
        Task { await Engine.togglePause() }
    }
    func pause() { Task { await Engine.pause() } }
    func resume() { Task { await Engine.resume() } }

    func seek(to ms: Int64) {
        seeking = true
        positionMs = ms
        Task {
            await Engine.seek(ms: ms)
            seeking = false
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
    }

    private func advance(auto: Bool) {
        guard !queue.isEmpty, let idx = currentIndex else {
            if auto { stopPlayback() }
            return
        }
        if auto && repeatMode == .one {
            seek(to: 0)
            resume()
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
    }

    func stopPlayback() {
        Task { await Engine.stop() }
        current = nil
        currentIndex = nil
        queue = []
        state = .stopped
        MPNowPlayingInfoCenter.default().nowPlayingInfo = nil
    }

    func toggleShuffle() { isShuffle.toggle() }

    func cycleRepeat() {
        repeatMode = RepeatMode(rawValue: (repeatMode.rawValue + 1) % 3) ?? .off
        Engine.setRepeat(repeatMode.rawValue)
    }

    func setVolume(_ v: Double) {
        volume = v
        Engine.setVolume(v)
    }

    // MARK: - Connect

    func connect(to device: Device) async -> Bool {
        let ok = await Engine.connectDevice(device.addr)
        if ok { connectedDeviceAddr = device.addr }
        return ok
    }
    func disconnect() async {
        await Engine.disconnectDevice()
        connectedDeviceAddr = ""
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
        cc.playCommand.addTarget { [weak self] _ in self?.resume(); return .success }
        cc.pauseCommand.addTarget { [weak self] _ in self?.pause(); return .success }
        cc.togglePlayPauseCommand.addTarget { [weak self] _ in self?.togglePlayPause(); return .success }
        cc.nextTrackCommand.addTarget { [weak self] _ in self?.next(); return .success }
        cc.previousTrackCommand.addTarget { [weak self] _ in self?.previous(); return .success }
        cc.changePlaybackPositionCommand.addTarget { [weak self] event in
            guard let event = event as? MPChangePlaybackPositionCommandEvent else { return .commandFailed }
            self?.seek(to: Int64(event.positionTime * 1000))
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
