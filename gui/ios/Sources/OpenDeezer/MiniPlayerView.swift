import SwiftUI

/// Floating Liquid-Glass mini player docked above the tab bar. Tapping it
/// (handled by the parent) opens the full Now Playing sheet.
struct MiniPlayerView: View {
    @EnvironmentObject private var player: PlayerController

    var body: some View {
        HStack(spacing: 12) {
            RemoteArtwork(url: player.current?.artworkUrl ?? "", cornerRadius: 8)
                .frame(width: 40, height: 40)

            VStack(alignment: .leading, spacing: 1) {
                Text(player.current?.name ?? "")
                    .font(.subheadline.weight(.semibold))
                    .lineLimit(1)
                Text(player.current?.artistLine ?? "")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }

            Spacer(minLength: 8)

            Button {
                player.togglePlayPause()
            } label: {
                Image(systemName: player.isPlaying ? "pause.fill" : "play.fill")
                    .font(.system(size: 18))
                    .frame(width: 32, height: 32)
                    .contentShape(Rectangle())
            }
            .buttonStyle(.plain)

            Button {
                player.next()
            } label: {
                Image(systemName: "forward.fill")
                    .font(.system(size: 16))
                    .frame(width: 32, height: 32)
                    .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
        }
        .padding(.leading, 8)
        .padding(.trailing, 10)
        .padding(.vertical, 6)
        .frame(height: Palette.miniPlayerHeight)
        .glassPill()
        .overlay(alignment: .bottom) {
            // Thin progress indicator along the bottom edge of the pill.
            GeometryReader { geo in
                Capsule()
                    .fill(Palette.accent)
                    .frame(width: geo.size.width * progressFraction, height: 2)
            }
            .frame(height: 2)
            .padding(.horizontal, 14)
            .padding(.bottom, 3)
        }
    }

    private var progressFraction: CGFloat {
        guard player.durationMs > 0 else { return 0 }
        return CGFloat(Double(player.positionMs) / Double(player.durationMs))
    }
}
