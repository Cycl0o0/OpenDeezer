import SwiftUI

// RecentlyPlayedView — the "Recently played" screen reachable from the sidebar.
// It pairs the machine-local play history (DZHistoryRecentJSON) with a small
// 30-day listening-stats panel (DZHistoryStatsJSON: top tracks, top artists,
// total listening time). Recent rows and top-track rows play on tap by track id
// through the shared AppState playback path.
struct RecentlyPlayedView: View {
    @EnvironmentObject var app: AppState

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 0) {
                Text(L("Recently played"))
                    .font(.system(size: 34, weight: .bold)).foregroundStyle(DZ.textPri)
                    .padding(.horizontal, 24).padding(.top, 32).padding(.bottom, 18)

                if app.historyLoading {
                    HStack { Spacer()
                        ProgressView().controlSize(.large).tint(DZ.accent).padding(.top, 48)
                        Spacer() }
                } else if app.historyEntries.isEmpty
                            && (app.historyStats?.topTracks.isEmpty ?? true) {
                    emptyState
                } else {
                    if let stats = app.historyStats {
                        statsPanel(stats)
                    }
                    if !app.historyEntries.isEmpty {
                        sectionHeader(L("History"))
                        LazyVStack(spacing: 0) {
                            ForEach(Array(app.historyEntries.enumerated()), id: \.element.id) { idx, e in
                                HistoryRowView(index: idx, entry: e,
                                               isCurrent: app.current?.id == e.trackId) {
                                    app.playHistory(e)
                                }
                                Divider().overlay(DZ.hairline).padding(.leading, 24)
                            }
                        }
                    }
                }

                Spacer().frame(height: 96) // clear the floating player bar
            }
        }
        .scrollContentBackground(.hidden)
        .background(DZ.windowBG)
    }

    // MARK: stats panel (30 days)

    @ViewBuilder private func statsPanel(_ stats: HistoryStats) -> some View {
        VStack(alignment: .leading, spacing: 14) {
            HStack(alignment: .firstTextBaseline, spacing: 10) {
                Text(L("Last 30 days"))
                    .font(.system(size: 11, weight: .bold)).textCase(.uppercase)
                    .foregroundStyle(DZ.textSec)
                Spacer()
            }
            HStack(spacing: 10) {
                Image(systemName: "clock.fill").font(.system(size: 20)).foregroundStyle(DZ.accent)
                VStack(alignment: .leading, spacing: 1) {
                    Text(totalTimeText(stats.totalSeconds))
                        .font(.system(size: 26, weight: .bold)).foregroundStyle(DZ.textPri)
                    Text(L("Total listening time"))
                        .font(.caption).foregroundStyle(DZ.textSec)
                }
            }

            if !stats.topTracks.isEmpty || !stats.topArtists.isEmpty {
                HStack(alignment: .top, spacing: 24) {
                    if !stats.topTracks.isEmpty {
                        statColumn(L("Top Tracks")) {
                            ForEach(Array(stats.topTracks.prefix(5).enumerated()), id: \.element.id) { i, t in
                                StatRow(rank: i + 1, title: t.title, sub: t.artist,
                                        trailing: Lp("%d plays", t.plays),
                                        tappable: !t.trackId.isEmpty) {
                                    app.playTrackStat(t)
                                }
                            }
                        }
                    }
                    if !stats.topArtists.isEmpty {
                        statColumn(L("Top Artists")) {
                            ForEach(Array(stats.topArtists.prefix(5).enumerated()), id: \.element.id) { i, a in
                                StatRow(rank: i + 1, title: a.artist, sub: nil,
                                        trailing: Lp("%d plays", a.plays),
                                        tappable: false, onTap: {})
                            }
                        }
                    }
                }
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(18)
        .glassEffect(.regular, in: RoundedRectangle(cornerRadius: 16))
        .padding(.horizontal, 24).padding(.bottom, 8)
    }

    @ViewBuilder private func statColumn<C: View>(_ title: String,
                                                  @ViewBuilder _ content: () -> C) -> some View {
        VStack(alignment: .leading, spacing: 6) {
            Text(title)
                .font(.system(size: 13, weight: .semibold)).foregroundStyle(DZ.textPri)
            content()
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }

    private func sectionHeader(_ t: String) -> some View {
        Text(t).font(.system(size: 20, weight: .bold)).foregroundStyle(DZ.textPri)
            .frame(maxWidth: .infinity, alignment: .leading)
            .padding(.horizontal, 24).padding(.top, 20).padding(.bottom, 8)
    }

    private var emptyState: some View {
        VStack(spacing: 8) {
            Image(systemName: "clock.arrow.circlepath")
                .font(.system(size: 34)).foregroundStyle(DZ.textSec)
            Text(L("No listening history yet"))
                .font(.system(size: 14)).foregroundStyle(DZ.textSec)
            Text(L("Play something and it'll show up here."))
                .font(.caption).foregroundStyle(DZ.textSec)
        }
        .frame(maxWidth: .infinity).padding(.top, 60)
    }

    // Human total-listening time, e.g. "12h 34m". Locale-aware, no new strings.
    private func totalTimeText(_ seconds: Int64) -> String {
        if seconds < 60 { return "<1m" }
        let f = DateComponentsFormatter()
        f.allowedUnits = [.hour, .minute]
        f.unitsStyle = .abbreviated
        f.zeroFormattingBehavior = .dropAll
        return f.string(from: TimeInterval(seconds)) ?? "<1m"
    }
}

// StatRow — a compact top-track / top-artist row: rank, title (+ optional
// subtitle), and a trailing "N plays" count. Top-track rows are tappable to play.
private struct StatRow: View {
    let rank: Int
    let title: String
    let sub: String?
    let trailing: String
    let tappable: Bool
    let onTap: () -> Void
    @State private var hover = false

    var body: some View {
        HStack(spacing: 8) {
            Text("\(rank)").font(.system(size: 11)).monospacedDigit()
                .foregroundStyle(DZ.textSec).frame(width: 16, alignment: .trailing)
            VStack(alignment: .leading, spacing: 0) {
                Text(title).font(.system(size: 12, weight: .medium))
                    .foregroundStyle(DZ.textPri).lineLimit(1)
                if let sub, !sub.isEmpty {
                    Text(sub).font(.system(size: 10)).foregroundStyle(DZ.textSec).lineLimit(1)
                }
            }
            Spacer(minLength: 6)
            Text(trailing).font(.system(size: 10)).foregroundStyle(DZ.textSec)
                .lineLimit(1).fixedSize()
        }
        .padding(.vertical, 3).padding(.horizontal, 6)
        .background(hover && tappable ? Color.white.opacity(0.05) : .clear,
                    in: RoundedRectangle(cornerRadius: 6))
        .contentShape(Rectangle())
        .onTapGesture { if tappable { onTap() } }
        .onHover { h in if tappable { hover = h } }
    }
}

// HistoryRowView — one recently-played entry. Plays on tap; shows a relative
// timestamp ("2h ago") on the right and a waveform when it's the current track.
private struct HistoryRowView: View {
    let index: Int
    let entry: HistoryEntry
    let isCurrent: Bool
    let onPlay: () -> Void
    @State private var hover = false

    var body: some View {
        HStack(spacing: 12) {
            ZStack {
                if isCurrent {
                    Image(systemName: "waveform").foregroundStyle(DZ.accent)
                } else if hover {
                    Image(systemName: "play.fill").foregroundStyle(DZ.textPri)
                } else {
                    Image(systemName: "clock").foregroundStyle(DZ.textSec)
                }
            }
            .frame(width: 28)

            VStack(alignment: .leading, spacing: 1) {
                Text(entry.title).lineLimit(1)
                    .foregroundStyle(isCurrent ? DZ.accent : DZ.textPri)
                    .fontWeight(isCurrent ? .semibold : .regular)
                Text(entry.artist).font(.caption).foregroundStyle(DZ.textSec).lineLimit(1)
            }
            .frame(maxWidth: .infinity, alignment: .leading)

            Text(relativeTime(entry.startedAt))
                .font(.system(size: 11)).foregroundStyle(DZ.textSec)
                .lineLimit(1).fixedSize()
        }
        .font(.system(size: 13))
        .padding(.horizontal, 24).padding(.vertical, 8)
        .background(isCurrent ? DZ.nowTint : (hover ? Color.white.opacity(0.05) : .clear))
        .contentShape(Rectangle())
        .onTapGesture(perform: onPlay)
        .onHover { h in withAnimation(.easeOut(duration: 0.12)) { hover = h } }
    }

    // Locale-aware relative time ("2h ago"), no new strings.
    private func relativeTime(_ unix: Int64) -> String {
        let f = RelativeDateTimeFormatter()
        f.unitsStyle = .abbreviated
        return f.localizedString(for: Date(timeIntervalSince1970: TimeInterval(unix)),
                                 relativeTo: Date())
    }
}
