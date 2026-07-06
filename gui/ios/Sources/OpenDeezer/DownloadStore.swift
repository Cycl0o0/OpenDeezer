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
