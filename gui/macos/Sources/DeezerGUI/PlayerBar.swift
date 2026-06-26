import SwiftUI

// PlayerBar — the floating Apple-Music-style transport bar: controls left,
// now-playing centre with a thin scrubber, utilities right.
struct PlayerBar: View {
    @EnvironmentObject var app: AppState

    private var isPlaying: Bool { app.state == .playing }
    private var progress: Double {
        app.durationMs > 0 ? min(1, Double(app.positionMs) / Double(app.durationMs)) : 0
    }

    var body: some View {
        HStack(spacing: 16) {
            transport
            Spacer(minLength: 12)
            nowPlaying
            Spacer(minLength: 12)
            utilities
        }
        .padding(.horizontal, 16)
        .frame(height: 64)
        .background(
            RoundedRectangle(cornerRadius: 14)
                .fill(DZ.panelBG)
                .overlay(RoundedRectangle(cornerRadius: 14).strokeBorder(DZ.hairline))
                .shadow(color: .black.opacity(0.45), radius: 16, y: 6)
        )
    }

    private var transport: some View {
        HStack(spacing: 18) {
            iconButton(app.shuffle ? "shuffle.circle.fill" : "shuffle",
                       tint: app.shuffle ? DZ.accent : DZ.textSec) { app.shuffle.toggle() }
            iconButton("backward.fill", size: 16, tint: DZ.textPri) { app.prev() }
            Button { app.togglePause() } label: {
                Image(systemName: isPlaying ? "pause.fill" : "play.fill")
                    .font(.system(size: 18, weight: .bold))
                    .foregroundStyle(DZ.textPri)
                    .frame(width: 34, height: 34)
                    .background(Circle().fill(DZ.accent))
            }
            .buttonStyle(.plain)
            iconButton("forward.fill", size: 16, tint: DZ.textPri) { app.next() }
            iconButton(app.repeatMode == .one ? "repeat.1" : "repeat",
                       tint: app.repeatMode == .off ? DZ.textSec : DZ.accent) {
                app.repeatMode = RepeatMode(rawValue: (app.repeatMode.rawValue + 1) % 3) ?? .off
            }
        }
        .frame(width: 230, alignment: .leading)
    }

    private var nowPlaying: some View {
        HStack(spacing: 10) {
            Artwork(url: app.current?.artworkUrl ?? "", size: 40, radius: 5)
            VStack(spacing: 3) {
                Text(app.current?.name ?? "Nothing playing")
                    .font(.system(size: 12, weight: .semibold)).foregroundStyle(DZ.textPri)
                    .lineLimit(1)
                Text(subtitleText)
                    .font(.system(size: 11)).foregroundStyle(DZ.textSec).lineLimit(1)
                scrubber
            }
        }
        .frame(maxWidth: 420)
    }

    private var subtitleText: String {
        guard let c = app.current else { return "" }
        return c.albumName.isEmpty ? c.artistLine : "\(c.artistLine) — \(c.albumName)"
    }

    private var scrubber: some View {
        HStack(spacing: 6) {
            Text(Track.timeText(app.positionMs))
                .font(.system(size: 9)).monospacedDigit().foregroundStyle(DZ.textSec)
            GeometryReader { g in
                ZStack(alignment: .leading) {
                    Capsule().fill(DZ.hairline).frame(height: 3)
                    Capsule().fill(DZ.accent).frame(width: progress * g.size.width, height: 3)
                }
                .frame(maxHeight: .infinity, alignment: .center)
            }
            .frame(height: 3)
            Text(Track.timeText(app.durationMs))
                .font(.system(size: 9)).monospacedDigit().foregroundStyle(DZ.textSec)
        }
    }

    private var utilities: some View {
        HStack(spacing: 14) {
            Image(systemName: "list.bullet").foregroundStyle(DZ.textSec)
            HStack(spacing: 6) {
                Image(systemName: "speaker.fill").font(.system(size: 11)).foregroundStyle(DZ.textSec)
                Slider(value: Binding(get: { app.volume }, set: { app.setVolume($0) }), in: 0...1)
                    .frame(width: 84).tint(DZ.accent)
            }
        }
        .frame(width: 150, alignment: .trailing)
    }

    private func iconButton(_ symbol: String, size: CGFloat = 15,
                            tint: Color, _ action: @escaping () -> Void) -> some View {
        Button(action: action) {
            Image(systemName: symbol).font(.system(size: size)).foregroundStyle(tint)
        }
        .buttonStyle(.plain)
    }
}
