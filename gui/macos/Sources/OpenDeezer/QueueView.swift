import SwiftUI

// UpNextView — the editable play-queue sheet ("Up Next"). Renders the app-owned
// play queue with the currently-playing row highlighted; tap a row to jump, drag
// to reorder, remove upcoming rows, "Download for offline" from any row, and
// Clear the up-next. Backed by AppState's queue (queueTracks / currentQueueIndex)
// and mutated only through its queue-edit methods, which re-mirror into the
// engine so playback stays correct.
struct UpNextView: View {
    @EnvironmentObject var app: AppState

    // Stable, unique per-row identity — a queue can legitimately repeat a track
    // id (the same song twice), so key ForEach by position, not by track id.
    private struct Row: Identifiable { let index: Int; let track: Track; var id: Int { index } }
    private var rows: [Row] {
        Array(app.queueTracks.enumerated()).map { Row(index: $0.offset, track: $0.element) }
    }

    var body: some View {
        VStack(spacing: 0) {
            header
            Divider().overlay(DZ.hairline)
            if rows.isEmpty {
                emptyState
            } else {
                List {
                    ForEach(rows) { row in
                        QueueRow(index: row.index, track: row.track,
                                 isCurrent: row.index == app.currentQueueIndex)
                            .listRowInsets(EdgeInsets(top: 2, leading: 10, bottom: 2, trailing: 10))
                            .listRowBackground(Color.clear)
                            .listRowSeparator(.hidden)
                    }
                    .onMove { app.moveQueue(from: $0, to: $1) }
                }
                .listStyle(.plain)
                .scrollContentBackground(.hidden)
            }
        }
        .frame(width: 460, height: 560)
        .background(DZ.windowBG)
    }

    private var header: some View {
        HStack(spacing: 10) {
            Image(systemName: "list.bullet").font(.system(size: 15)).foregroundStyle(DZ.accent)
            VStack(alignment: .leading, spacing: 1) {
                Text(L("Up Next")).font(.system(size: 16, weight: .bold)).foregroundStyle(DZ.textPri)
                Text(Lf("%d in queue", app.queueTracks.count))
                    .font(.system(size: 11)).foregroundStyle(DZ.textSec)
            }
            Spacer()
            // Clear the up-next (keeps the playing track). Only meaningful when
            // more than the current track is queued.
            Button(L("Clear")) { app.clearQueue() }
                .buttonStyle(.plain).font(.system(size: 12, weight: .medium))
                .foregroundStyle(app.queueTracks.count > 1 ? DZ.accent : DZ.textSec)
                .disabled(app.queueTracks.count <= 1)
            Button(L("Done")) { app.showQueue = false }
                .buttonStyle(.glass).tint(DZ.accent).controlSize(.small)
        }
        .padding(.horizontal, 16).padding(.vertical, 12)
    }

    private var emptyState: some View {
        VStack(spacing: 10) {
            Image(systemName: "music.note.list").font(.system(size: 40)).foregroundStyle(DZ.textSec)
            Text(L("Nothing in the queue"))
                .font(.system(size: 15, weight: .semibold)).foregroundStyle(DZ.textPri)
            Text(L("Play a track and it'll show up here."))
                .font(.system(size: 12)).foregroundStyle(DZ.textSec)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }
}

// QueueRow — one row of the Up-Next editor. The playing row is accented and
// can't be removed; upcoming rows show a remove control on hover and a context
// menu (play, remove, download for offline).
private struct QueueRow: View {
    @EnvironmentObject var app: AppState
    let index: Int
    let track: Track
    let isCurrent: Bool
    @State private var hover = false

    var body: some View {
        HStack(spacing: 10) {
            ZStack {
                if isCurrent {
                    Image(systemName: "waveform").foregroundStyle(DZ.accent)
                } else if hover {
                    Image(systemName: "play.fill").foregroundStyle(DZ.textPri).font(.system(size: 11))
                } else {
                    Text("\(index + 1)").foregroundStyle(DZ.textSec).monospacedDigit()
                        .font(.system(size: 12))
                }
            }
            .frame(width: 22)
            Artwork(url: track.artworkUrl, size: 34, radius: 4)
            VStack(alignment: .leading, spacing: 1) {
                HStack(spacing: 5) {
                    if track.explicit { ExplicitBadge() }
                    Text(track.name).lineLimit(1)
                        .foregroundStyle(isCurrent ? DZ.accent : DZ.textPri)
                        .fontWeight(isCurrent ? .semibold : .regular)
                    if app.isOffline(track) { OfflineBadge() }
                }
                Text(track.artistLine).font(.caption).foregroundStyle(DZ.textSec).lineLimit(1)
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            Text(track.durationText).font(.system(size: 11)).monospacedDigit()
                .foregroundStyle(DZ.textSec)
            // Remove upcoming rows (never the playing one).
            if !isCurrent {
                Button { app.removeFromQueue(at: index) } label: {
                    Image(systemName: "xmark.circle.fill").font(.system(size: 13))
                        .foregroundStyle(hover ? DZ.textPri : DZ.textSec)
                }
                .buttonStyle(.plain)
                .opacity(hover ? 1 : 0.35)
                .help(L("Remove from queue"))
                .accessibilityLabel(L("Remove from queue"))
            }
        }
        .font(.system(size: 13))
        .padding(.vertical, 5).padding(.horizontal, 8)
        .background(isCurrent ? DZ.nowTint : (hover ? Color.white.opacity(0.05) : .clear),
                    in: RoundedRectangle(cornerRadius: 8))
        .contentShape(Rectangle())
        .onTapGesture { app.jumpToQueueIndex(index) }
        .onHover { h in withAnimation(.easeOut(duration: 0.12)) { hover = h } }
        .contextMenu {
            Button { app.jumpToQueueIndex(index) } label: { Label(L("Play"), systemImage: "play.fill") }
            if !isCurrent {
                Button(role: .destructive) { app.removeFromQueue(at: index) } label: {
                    Label(L("Remove from queue"), systemImage: "minus.circle")
                }
            }
            Button { app.downloadForOffline(track) } label: {
                Label(L("Download for offline"), systemImage: "arrow.down.circle.dotted")
            }
            .disabled(!app.isPremium)
            .help(app.isPremium ? L("Download for offline") : L("Requires a paid Deezer plan"))
        }
    }
}

// OfflineBadge — a small glyph marking a track cached for zero-network playback
// (AppState.offlineIDs). Reused by the track list, queue editor and player bar.
struct OfflineBadge: View {
    var body: some View {
        Image(systemName: "arrow.down.circle.fill")
            .font(.system(size: 10)).foregroundStyle(DZ.accent)
            .help(L("Available offline"))
            .accessibilityLabel(L("Available offline"))
    }
}
