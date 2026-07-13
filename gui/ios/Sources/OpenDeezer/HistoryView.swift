import SwiftUI

/// Recently played tracks + local listening stats (last 30 days). The history
/// log is machine-local (it never leaves the device); rows play by track id.
struct HistoryView: View {
    @EnvironmentObject private var player: PlayerController

    @State private var recent: [HistoryEntry] = []
    @State private var stats = HistoryStats(topTracks: [], topArtists: [], totalSeconds: 0)
    @State private var isLoading = true

    /// Number of days the stats summarize (matches the section title).
    private static let statsDays = 30

    private static let relative: RelativeDateTimeFormatter = {
        let f = RelativeDateTimeFormatter()
        f.unitsStyle = .abbreviated
        return f
    }()

    var body: some View {
        Group {
            if isLoading && recent.isEmpty && stats.topTracks.isEmpty {
                ProgressView()
            } else if recent.isEmpty && stats.topTracks.isEmpty && stats.topArtists.isEmpty {
                ContentUnavailableMessage(
                    systemImage: "clock.arrow.circlepath", title: "Nothing here yet",
                    message: String(localized: "Tracks you play will show up here.")
                )
            } else {
                List {
                    if stats.totalSeconds > 0 {
                        Section {
                            HStack {
                                Label("Last \(Self.statsDays) days", systemImage: "clock")
                                    .foregroundStyle(.secondary)
                                Spacer()
                                Text(Self.durationText(stats.totalSeconds))
                                    .font(.headline)
                                    .foregroundStyle(Palette.accent)
                            }
                        }
                    }

                    if !recent.isEmpty {
                        Section("Recently Played") {
                            ForEach(recent) { entry in
                                Button {
                                    replay(entry)
                                } label: {
                                    recentRow(entry)
                                }
                                .buttonStyle(.plain)
                            }
                        }
                    }

                    if !stats.topTracks.isEmpty {
                        Section("Top Tracks · \(Self.statsDays) days") {
                            ForEach(Array(stats.topTracks.prefix(20).enumerated()), id: \.element.id) { index, stat in
                                Button {
                                    player.play(stat.asTrack)
                                } label: {
                                    statTrackRow(stat, rank: index + 1)
                                }
                                .buttonStyle(.plain)
                            }
                        }
                    }

                    if !stats.topArtists.isEmpty {
                        Section("Top Artists · \(Self.statsDays) days") {
                            ForEach(Array(stats.topArtists.prefix(20).enumerated()), id: \.element.id) { index, stat in
                                statArtistRow(stat, rank: index + 1)
                            }
                        }
                    }
                }
                .listStyle(.plain)
            }
        }
        .navigationTitle("Recently Played")
        .navigationBarTitleDisplayMode(.inline)
        .task { await load() }
        .refreshable { await load() }
    }

    /// Track-only history rows — episodes replay through the podcast player, so
    /// they're excluded from the music queue we build for a tapped song.
    private var trackEntries: [HistoryEntry] { recent.filter { !$0.isEpisode } }

    /// Replay a history row (B14): an episode routes to the podcast player; a
    /// song starts a queue of the recent songs at the tapped one.
    private func replay(_ entry: HistoryEntry) {
        if entry.isEpisode {
            player.playEpisode(entry.asEpisode)
        } else {
            let entries = trackEntries
            let start = entries.firstIndex(of: entry) ?? 0
            player.playQueue(entries.map(\.asTrack), startAt: start)
        }
    }

    private func recentRow(_ entry: HistoryEntry) -> some View {
        HStack(spacing: 12) {
            VStack(alignment: .leading, spacing: 2) {
                Text(entry.title)
                    .font(.body)
                    .lineLimit(1)
                    .foregroundStyle(player.current?.id == entry.trackId ? Palette.accent : .primary)
                Text(entry.artist)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }
            Spacer()
            Text(Self.relative.localizedString(for: Date(timeIntervalSince1970: TimeInterval(entry.startedAt)), relativeTo: Date()))
                .font(.caption)
                .foregroundStyle(.secondary)
        }
        .contentShape(Rectangle())
    }

    private func statTrackRow(_ stat: HistoryTrackStat, rank: Int) -> some View {
        HStack(spacing: 12) {
            Text("\(rank)")
                .font(.subheadline)
                .foregroundStyle(.secondary)
                .frame(width: 20, alignment: .trailing)
            VStack(alignment: .leading, spacing: 2) {
                Text(stat.title).font(.body).lineLimit(1).foregroundStyle(.primary)
                Text(stat.artist).font(.caption).foregroundStyle(.secondary).lineLimit(1)
            }
            Spacer()
            Text("\(stat.plays)×")
                .font(.caption)
                .foregroundStyle(.secondary)
        }
        .contentShape(Rectangle())
    }

    private func statArtistRow(_ stat: HistoryArtistStat, rank: Int) -> some View {
        HStack(spacing: 12) {
            Text("\(rank)")
                .font(.subheadline)
                .foregroundStyle(.secondary)
                .frame(width: 20, alignment: .trailing)
            Text(stat.artist).font(.body).lineLimit(1)
            Spacer()
            Text("\(stat.plays)×")
                .font(.caption)
                .foregroundStyle(.secondary)
        }
    }

    private func load() async {
        isLoading = recent.isEmpty && stats.topTracks.isEmpty
        async let recentTask = Engine.historyRecent(50)
        async let statsTask = Engine.historyStats(sinceDays: Self.statsDays)
        recent = await recentTask
        stats = await statsTask
        isLoading = false
    }

    /// "2h 5m" / "45m" / "30s" from a whole-second total.
    private static func durationText(_ totalSec: Int64) -> String {
        let s = max(0, totalSec)
        let h = s / 3600, m = (s % 3600) / 60
        if h > 0 { return String(localized: "\(Int(h))h \(Int(m))m") }
        if m > 0 { return String(localized: "\(Int(m))m") }
        return String(localized: "\(Int(s))s")
    }
}
