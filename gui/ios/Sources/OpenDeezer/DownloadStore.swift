import Foundation

/// Drives the premium-only "Download track" action and its transient status
/// toast (rendered by `MainTabView`). Downloads save the full track to the
/// engine's shared folder off the main thread; the result — the saved path or
/// an error — is surfaced as a self-dismissing note.
@MainActor
final class DownloadStore: ObservableObject {
    static let shared = DownloadStore()

    /// Transient status: the in-flight "Downloading…" note, the saved path on
    /// success, or an error on failure. `nil` hides the toast.
    @Published var status: String?

    private var token = 0

    private init() {}

    /// Download `track` into the shared default folder. Premium-only: Free
    /// accounts stream (standard 128 kbps) but can't download, so we guard here
    /// as well as hiding the menu item.
    func download(_ track: Track, isPremium: Bool) {
        guard isPremium else {
            setStatus(String(localized: "Requires a paid Deezer plan"))
            return
        }
        let name = track.name
        setStatus(String(localized: "Downloading “\(name)”…"), autoDismiss: false)
        Task {
            let result = await Engine.download(id: track.id)
            if let path = result.path, !path.isEmpty {
                setStatus(String(localized: "Saved to \(path)"))
            } else if let error = result.error, !error.isEmpty {
                setStatus(error)
            } else {
                setStatus(String(localized: "Download failed."))
            }
        }
    }

    /// Download every track of `album` into the shared folder. Premium-only, the
    /// same gate as single-track downloads; the batch summary replaces the
    /// in-flight note when it finishes.
    func downloadAlbum(id: String, name: String, isPremium: Bool) {
        downloadBatch(name: name, isPremium: isPremium) { await Engine.downloadAlbum(id: id) }
    }

    /// Download every track of `playlist` into the shared folder (premium-only).
    func downloadPlaylist(id: String, name: String, isPremium: Bool) {
        downloadBatch(name: name, isPremium: isPremium) { await Engine.downloadPlaylist(id: id) }
    }

    private func downloadBatch(name: String, isPremium: Bool,
                               _ run: @escaping () async -> Engine.BatchDownloadResult) {
        guard isPremium else {
            setStatus(String(localized: "Requires a paid Deezer plan"))
            return
        }
        setStatus(String(localized: "Downloading “\(name)”…"), autoDismiss: false)
        Task {
            let result = await run()
            setStatus(Self.batchSummary(result))
        }
    }

    /// Human summary of a batch download: the engine error when nothing saved
    /// (e.g. the premium gate on a Free account), else a saved/failed tally.
    private static func batchSummary(_ r: Engine.BatchDownloadResult) -> String {
        if r.saved == 0 && r.failed == 0 && !r.error.isEmpty { return r.error }
        if r.failed > 0 {
            return String(localized: "Saved \(r.saved), \(r.failed) failed")
        }
        return String(localized: "Saved \(r.saved) tracks")
    }

    /// Show a status note. `autoDismiss` (default) clears it after a few seconds;
    /// the in-flight "Downloading…" note passes `false` so it stays until the
    /// result replaces it. A token guards against a stale timer clearing a newer
    /// message.
    func setStatus(_ message: String?, autoDismiss: Bool = true) {
        status = message
        token += 1
        guard autoDismiss, message != nil else { return }
        let current = token
        Task {
            try? await Task.sleep(nanoseconds: 4_000_000_000)
            if token == current { status = nil }
        }
    }

    func dismiss() { setStatus(nil) }
}
