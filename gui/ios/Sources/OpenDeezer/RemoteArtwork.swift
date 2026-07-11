import SwiftUI
import UIKit

/// Process-wide cache for cover art fetched through `Engine.fetch` (the Go
/// engine performs the HTTP request so the same networking stack + UA is used
/// everywhere the desktop/Android GUIs use it). NSCache-backed so a long
/// browsing session can't grow unbounded: capped entry count, auto-eviction
/// under memory pressure, full purge on an explicit memory warning.
actor ImageCache {
    static let shared = ImageCache()

    private let cache: NSCache<NSString, UIImage> = {
        let cache = NSCache<NSString, UIImage>()
        cache.countLimit = 200 // decoded 500x500 covers are ~1 MB each
        return cache
    }()

    init() {
        NotificationCenter.default.addObserver(
            forName: UIApplication.didReceiveMemoryWarningNotification,
            object: nil, queue: nil
        ) { _ in
            Task { await ImageCache.shared.removeAll() }
        }
    }

    func image(for url: String) -> UIImage? { cache.object(forKey: url as NSString) }
    func set(_ image: UIImage, for url: String) { cache.setObject(image, forKey: url as NSString) }
    private func removeAll() { cache.removeAllObjects() }
}

/// Square artwork view backed by `OdmobileFetch`, with a placeholder while
/// loading/missing. Drop-in replacement for `AsyncImage` since the engine
/// (not URLSession) must perform the request.
struct RemoteArtwork: View {
    let url: String
    var cornerRadius: CGFloat = 8

    @State private var image: UIImage?
    @State private var displayedURL: String?

    var body: some View {
        ZStack {
            if let image {
                Image(uiImage: image)
                    .resizable()
                    .aspectRatio(contentMode: .fill)
            } else {
                RoundedRectangle(cornerRadius: cornerRadius, style: .continuous)
                    .fill(LinearGradient(colors: [Color.gray.opacity(0.35), Color.gray.opacity(0.2)], startPoint: .topLeading, endPoint: .bottomTrailing))
                    .overlay {
                        Image(systemName: "music.note")
                            .foregroundStyle(.white.opacity(0.6))
                    }
            }
        }
        .clipShape(RoundedRectangle(cornerRadius: cornerRadius, style: .continuous))
        .accessibilityHidden(true)
        .task(id: url) { await load() }
    }

    private func load() async {
        // A RemoteArtwork often survives while its URL changes (the mini
        // player is the common case). Clear the previous cover immediately so
        // it is never displayed for the newly selected track.
        if displayedURL != url {
            displayedURL = url
            image = nil
        }
        guard !url.isEmpty else { return }
        if let cached = await ImageCache.shared.image(for: url) {
            guard !Task.isCancelled, displayedURL == url else { return }
            image = cached
            return
        }
        guard let data = await Engine.fetch(url), let img = UIImage(data: data) else { return }
        await ImageCache.shared.set(img, for: url)
        guard !Task.isCancelled, displayedURL == url else { return }
        image = img
    }
}
