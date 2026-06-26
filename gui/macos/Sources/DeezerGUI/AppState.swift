import Foundation
import SwiftUI

// Section is the sidebar selection.
enum Section: Hashable {
    case liked, playlists, search
}

enum RepeatMode: Int { case off, all, one }

@MainActor
final class AppState: ObservableObject {
    @Published var loggedIn = false
    @Published var loginError: String?
    @Published var busy = false
    @Published var userID = ""

    @Published var section: Section = .liked
    @Published var tracks: [Track] = []          // current track list / queue
    @Published var listTitle = "Liked Songs"
    @Published var listArtwork = ""              // hero artwork (empty => Liked gradient)
    @Published var listIsLiked = true            // hero style: gradient vs artwork
    @Published var listSubtitle = ""
    @Published var playlists: [Playlist] = []
    @Published var searchTracks: [Track] = []
    @Published var searchAlbums: [Album] = []
    @Published var searchPlaylists: [Playlist] = []
    @Published var query = ""

    // playback
    @Published var current: Track?
    @Published var state: PlayerState = .stopped
    @Published var positionMs: Int64 = 0
    @Published var durationMs: Int64 = 0
    @Published var volume: Double = 1
    @Published var shuffle = false
    @Published var repeatMode: RepeatMode = .off

    private var queueIndex = 0
    private var lastFinished = 0
    private var timer: Timer?

    // MARK: login

    func start() {
        guard let arl = Self.loadARL(), !arl.isEmpty else {
            loginError = "No ARL found. Set $DEEZER_ARL or ~/.config/deezertui/arl.txt"
            return
        }
        busy = true
        Task.detached {
            let ok = Core.initialize(arl: arl)
            await MainActor.run {
                self.busy = false
                self.loggedIn = ok
                if ok {
                    self.userID = Core.userID
                    self.volume = Core.volume
                    self.startTimer()
                    self.loadFavorites()
                } else {
                    self.loginError = "Login failed — invalid or expired ARL."
                }
            }
        }
    }

    static func loadARL() -> String? {
        if let v = ProcessInfo.processInfo.environment["DEEZER_ARL"], !v.isEmpty { return v }
        let home = FileManager.default.homeDirectoryForCurrentUser
        let path = home.appendingPathComponent(".config/deezertui/arl.txt")
        return (try? String(contentsOf: path, encoding: .utf8))?
            .trimmingCharacters(in: .whitespacesAndNewlines)
    }

    // MARK: browse

    func loadFavorites() {
        listTitle = "Liked Songs"
        listArtwork = ""
        listIsLiked = true
        runList { Core.favorites() }
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

    // MARK: playback

    func play(_ track: Track, in list: [Track]) {
        tracks = list
        queueIndex = list.firstIndex(of: track) ?? 0
        playCurrent()
    }

    private func playCurrent() {
        guard queueIndex >= 0, queueIndex < tracks.count else { return }
        let t = tracks[queueIndex]
        current = t
        durationMs = t.durationMs
        positionMs = 0
        Task.detached { Core.play(t.id, durationMs: t.durationMs) }
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
        let ms = Int64(max(0, min(1, f)) * Double(durationMs))
        positionMs = ms              // optimistic; the timer reconciles
        Core.seek(ms)
    }

    // MARK: polling

    private func startTimer() {
        lastFinished = Core.finishedCount
        timer = Timer.scheduledTimer(withTimeInterval: 0.4, repeats: true) { [weak self] _ in
            Task { @MainActor in self?.tick() }
        }
    }

    private func tick() {
        state = Core.state
        positionMs = Core.positionMs
        durationMs = Core.durationMs
        let f = Core.finishedCount
        if f != lastFinished {
            lastFinished = f
            if repeatMode == .one { playCurrent() } else { next() }
        }
    }
}
