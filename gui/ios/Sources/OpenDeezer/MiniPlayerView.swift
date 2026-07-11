import SwiftUI

/// Compact now-playing bar. In `accessory` mode it renders inside the iOS 26
/// `tabViewBottomAccessory` (the system supplies the Liquid-Glass container that
/// docks above the tab bar); otherwise it draws its own glass pill for the
/// pre-iOS-26 `safeAreaInset` fallback. Tapping it (handled by the parent) opens
/// the full Now Playing sheet.
struct MiniPlayerView: View {
    @EnvironmentObject private var player: PlayerController
    var accessory = false
    let onOpen: () -> Void

    var body: some View {
        if accessory {
            row
                .padding(.horizontal, 12)
                .frame(maxWidth: .infinity)
                .contentShape(Rectangle())
        } else {
            row
                .padding(.leading, 8)
                .padding(.trailing, 10)
                .padding(.vertical, 6)
                .frame(height: Palette.miniPlayerHeight)
                .glassPill()
                .overlay(alignment: .bottom) { progressBar }
        }
    }

    private var row: some View {
        HStack(spacing: 12) {
            Button(action: onOpen) {
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
                }
                .frame(maxWidth: .infinity, alignment: .leading)
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .accessibilityLabel("Now Playing")
            .accessibilityValue(nowPlayingAccessibilityValue)

            Button {
                player.togglePlayPause()
            } label: {
                Group {
                    if player.state == .loading {
                        ProgressView()
                    } else {
                        Image(systemName: player.isPlaying ? "pause.fill" : "play.fill")
                            .font(.system(size: 18))
                    }
                }
                .frame(width: 44, height: 44)
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .disabled(player.state == .loading)
            .accessibilityLabel(playPauseAccessibilityLabel)

            Button {
                player.next()
            } label: {
                Image(systemName: "forward.fill")
                    .font(.system(size: 16))
                    .frame(width: 44, height: 44)
                    .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .disabled(!player.canGoNext)
            .accessibilityLabel("Next")
        }
    }

    private var progressBar: some View {
        GeometryReader { geo in
            Capsule()
                .fill(Palette.accent)
                .frame(width: geo.size.width * progressFraction, height: 2)
        }
        .frame(height: 2)
        .padding(.horizontal, 14)
        .padding(.bottom, 3)
    }

    private var progressFraction: CGFloat {
        guard player.durationMs > 0 else { return 0 }
        return CGFloat(min(max(Double(player.positionMs) / Double(player.durationMs), 0), 1))
    }

    private var nowPlayingAccessibilityValue: String {
        [player.current?.name, player.current?.artistLine]
            .compactMap { $0 }
            .filter { !$0.isEmpty }
            .joined(separator: ", ")
    }

    private var playPauseAccessibilityLabel: String {
        if player.state == .loading { return String(localized: "Loading") }
        return player.isPlaying ? String(localized: "Pause") : String(localized: "Play")
    }
}
