import SwiftUI

/// "Up Next" bottom sheet reached from Now Playing: the live play queue with the
/// current row highlighted, tap-to-jump, swipe-to-remove, drag-to-reorder (via
/// the toolbar Edit button) and Clear. Every edit routes through
/// `PlayerController`, which owns the queue and mirrors each change into the
/// engine (so web/Connect remotes stay in sync).
struct UpNextView: View {
    @EnvironmentObject private var player: PlayerController
    @EnvironmentObject private var downloads: DownloadStore
    @Environment(\.dismiss) private var dismiss

    @State private var showClearConfirm = false

    var body: some View {
        NavigationStack {
            Group {
                if player.queue.isEmpty {
                    ContentUnavailableMessage(
                        systemImage: "music.note.list", title: "Queue is empty",
                        message: String(localized: "Songs you play will line up here.")
                    )
                } else {
                    queueList
                }
            }
            .navigationTitle("Up Next")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .topBarLeading) {
                    Button { dismiss() } label: { Image(systemName: "chevron.down").font(.headline) }
                        .accessibilityLabel("Done")
                }
                ToolbarItem(placement: .topBarTrailing) {
                    if !player.queue.isEmpty { EditButton() }
                }
                ToolbarItem(placement: .bottomBar) {
                    if !player.queue.isEmpty {
                        Button(role: .destructive) { showClearConfirm = true } label: {
                            Label("Clear", systemImage: "trash")
                        }
                        .tint(.red)
                    }
                }
            }
            .confirmationDialog("Clear the queue?", isPresented: $showClearConfirm, titleVisibility: .visible) {
                Button("Clear Queue", role: .destructive) { player.clearQueue() }
                Button("Cancel", role: .cancel) {}
            } message: {
                Text("This keeps the current song playing and removes the rest.")
            }
        }
        .presentationDetents([.medium, .large])
        .presentationDragIndicator(.visible)
    }

    private var queueList: some View {
        List {
            ForEach(Array(player.queue.enumerated()), id: \.offset) { index, track in
                row(index: index, track: track)
                    .deleteDisabled(player.isCurrentQueueRow(index))
            }
            .onMove { source, destination in
                player.moveInQueue(fromOffsets: source, toOffset: destination)
            }
            .onDelete { offsets in
                player.removeFromQueue(atOffsets: offsets)
            }
        }
        .listStyle(.plain)
    }

    @ViewBuilder private func row(index: Int, track: Track) -> some View {
        let isCurrent = player.isCurrentQueueRow(index)
        Button {
            guard !isCurrent else { return }
            player.jumpToQueueIndex(index)
        } label: {
            HStack(spacing: 12) {
                RemoteArtwork(url: track.artworkUrl, cornerRadius: 6)
                    .frame(width: 46, height: 46)
                    .overlay {
                        if isCurrent {
                            ZStack {
                                Color.black.opacity(0.35)
                                Image(systemName: player.isPlaying ? "speaker.wave.2.fill" : "pause.fill")
                                    .font(.caption)
                                    .foregroundStyle(.white)
                            }
                            .clipShape(RoundedRectangle(cornerRadius: 6))
                        }
                    }
                VStack(alignment: .leading, spacing: 2) {
                    HStack(spacing: 6) {
                        Text(track.name)
                            .font(.body)
                            .lineLimit(1)
                            .foregroundStyle(isCurrent ? Palette.accent : .primary)
                        if track.explicit { ExplicitBadge() }
                    }
                    Text(track.artistLine)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                }
                Spacer()
                if downloads.isOffline(track.id) {
                    Image(systemName: "arrow.down.circle.fill")
                        .font(.caption)
                        .foregroundStyle(Palette.accent)
                        .accessibilityLabel("Downloaded")
                }
                Text(track.durationText)
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .accessibilityElement(children: .combine)
        .accessibilityLabel(isCurrent
            ? String(localized: "Now playing, \(track.name), \(track.artistLine)")
            : String(localized: "\(track.name), \(track.artistLine)"))
        .accessibilityHint(isCurrent ? "" : String(localized: "Double tap to play"))
    }
}
