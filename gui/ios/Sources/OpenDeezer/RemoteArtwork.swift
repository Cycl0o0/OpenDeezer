import SwiftUI
import UIKit

/// Process-wide cache for cover art fetched through `Engine.fetch` (the Go
/// engine performs the HTTP request so the same networking stack + UA is used
/// everywhere the desktop/Android GUIs use it).
actor ImageCache {
    static let shared = ImageCache()
    private var cache: [String: UIImage] = [:]

    func image(for url: String) -> UIImage? { cache[url] }
    func set(_ image: UIImage, for url: String) { cache[url] = image }
}

/// Square artwork view backed by `OdmobileFetch`, with a placeholder while
/// loading/missing. Drop-in replacement for `AsyncImage` since the engine
/// (not URLSession) must perform the request.
struct RemoteArtwork: View {
    let url: String
    var cornerRadius: CGFloat = 8

    @State private var image: UIImage?

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
        .task(id: url) { await load() }
    }

    private func load() async {
        guard !url.isEmpty else { image = nil; return }
        if let cached = await ImageCache.shared.image(for: url) {
            image = cached
            return
        }
        guard let data = await Engine.fetch(url), let img = UIImage(data: data) else { return }
        await ImageCache.shared.set(img, for: url)
        if Task.isCancelled { return }
        image = img
    }
}
