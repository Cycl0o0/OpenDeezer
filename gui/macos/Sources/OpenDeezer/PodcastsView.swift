import SwiftUI

// PodcastsView — search for podcast shows (DZSearchPodcastsJSON), open a show to
// list its episodes (DZPodcastEpisodesJSON), and play an episode via the plain
// stream path (DZPlayEpisode). When a show is open, the episode list replaces
// the search grid; a Back button returns to results.
struct PodcastsView: View {
    @EnvironmentObject var app: AppState

    var body: some View {
        Group {
            if let show = app.openedPodcast {
                PodcastEpisodesView(show: show)
            } else {
                browse
            }
        }
        .background(DZ.windowBG)
    }

    private var browse: some View {
        VStack(spacing: 0) {
            HStack {
                Image(systemName: "magnifyingglass").foregroundStyle(DZ.textSec)
                TextField(L("Search podcasts"), text: $app.podcastQuery)
                    .textFieldStyle(.plain).foregroundStyle(DZ.textPri)
                    .onSubmit { app.runPodcastSearch() }
            }
            .padding(12)
            .glassEffect(.regular, in: RoundedRectangle(cornerRadius: 12))
            .padding(.horizontal, 24).padding(.top, 18).padding(.bottom, 10)

            if app.podcastsLoading {
                Spacer()
                ProgressView().controlSize(.large).tint(DZ.accent)
                Spacer()
            } else if app.podcasts.isEmpty {
                Spacer()
                VStack(spacing: 8) {
                    Image(systemName: "mic").font(.system(size: 32)).foregroundStyle(DZ.textSec)
                    Text(L("Search for podcasts")).font(.system(size: 14)).foregroundStyle(DZ.textSec)
                }
                Spacer()
            } else {
                ScrollView {
                    LazyVGrid(columns: [GridItem(.adaptive(minimum: 170, maximum: 200), spacing: 20)],
                              spacing: 24) {
                        ForEach(app.podcasts) { p in
                            PodcastCard(podcast: p) { app.openPodcast(p) }
                        }
                    }
                    .padding(.horizontal, 24).padding(.top, 8).padding(.bottom, 96)
                }
                .scrollContentBackground(.hidden)
            }
        }
    }
}

private struct PodcastCard: View {
    let podcast: Podcast
    let onOpen: () -> Void
    @State private var hover = false

    var body: some View {
        Button(action: onOpen) {
            VStack(alignment: .leading, spacing: 8) {
                Artwork(url: podcast.artworkUrl, size: 170, radius: 8).shadow(radius: 6, y: 4)
                Text(podcast.name).font(.system(size: 13, weight: .medium))
                    .foregroundStyle(DZ.textPri).lineLimit(1)
                Text(podcast.episodeCount > 0 ? Lp("%d episodes", podcast.episodeCount) : L("Podcast"))
                    .font(.caption).foregroundStyle(DZ.textSec)
            }
        }
        .buttonStyle(.plain)
        .scaleEffect(hover ? 1.03 : 1)
        .onHover { h in withAnimation(.easeOut(duration: 0.15)) { hover = h } }
    }
}

// PodcastEpisodesView — the opened show's header + episode list.
private struct PodcastEpisodesView: View {
    @EnvironmentObject var app: AppState
    let show: Podcast

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 0) {
                header
                if app.podcastsLoading {
                    HStack { Spacer()
                        ProgressView().controlSize(.large).tint(DZ.accent).padding(40)
                        Spacer() }
                } else {
                    LazyVStack(spacing: 0) {
                        ForEach(app.podcastEpisodes) { e in
                            EpisodeRow(episode: e, isCurrent: app.current?.id == e.id) {
                                app.playEpisode(e)
                            }
                            Divider().overlay(DZ.hairline).padding(.leading, 24)
                        }
                    }
                    .padding(.bottom, 96)
                }
            }
        }
        .scrollContentBackground(.hidden)
        .background(DZ.windowBG)
    }

    private var header: some View {
        VStack(alignment: .leading, spacing: 14) {
            Button { app.closePodcast() } label: {
                // chevron.backward auto-mirrors under RTL (unlike chevron.left).
                Label(L("Podcasts"), systemImage: "chevron.backward")
            }
            .buttonStyle(.glass).tint(DZ.accent)

            HStack(alignment: .bottom, spacing: 20) {
                Artwork(url: show.artworkUrl, size: 140, radius: 10).shadow(radius: 14, y: 6)
                VStack(alignment: .leading, spacing: 8) {
                    Text(L("Podcast")).font(.system(size: 11, weight: .bold)).textCase(.uppercase)
                        .foregroundStyle(DZ.textSec)
                    Text(show.name).font(.system(size: 30, weight: .bold))
                        .foregroundStyle(DZ.textPri).lineLimit(2)
                    if !show.description.isEmpty {
                        Text(show.description)
                            .font(.system(size: 12)).foregroundStyle(DZ.textSec).lineLimit(3)
                    }
                }
                Spacer()
            }
        }
        .padding(24)
    }
}

private struct EpisodeRow: View {
    @EnvironmentObject var app: AppState
    let episode: Episode
    let isCurrent: Bool
    let onPlay: () -> Void
    @State private var hover = false

    private var meta: String {
        var parts: [String] = []
        if !episode.releaseDate.isEmpty { parts.append(episode.releaseDate) }
        if episode.durationMs > 0 { parts.append(episode.durationText) }
        return parts.joined(separator: " · ")
    }

    var body: some View {
        HStack(spacing: 12) {
            ZStack {
                if isCurrent {
                    Image(systemName: "waveform").foregroundStyle(DZ.accent)
                } else {
                    Image(systemName: hover ? "play.fill" : "play")
                        .foregroundStyle(hover ? DZ.textPri : DZ.textSec)
                }
            }
            .frame(width: 28)

            Artwork(url: episode.artworkUrl, size: 44, radius: 5)
            VStack(alignment: .leading, spacing: 2) {
                Text(episode.title).lineLimit(1)
                    .foregroundStyle(isCurrent ? DZ.accent : DZ.textPri)
                    .fontWeight(isCurrent ? .semibold : .regular)
                if !meta.isEmpty {
                    Text(meta).font(.caption).foregroundStyle(DZ.textSec).lineLimit(1)
                }
                if !episode.description.isEmpty {
                    Text(episode.description)
                        .font(.caption2).foregroundStyle(DZ.textSec).lineLimit(2)
                }
            }
            .frame(maxWidth: .infinity, alignment: .leading)
        }
        .font(.system(size: 13))
        .padding(.horizontal, 24).padding(.vertical, 8)
        .background(isCurrent ? DZ.nowTint : (hover ? Color.white.opacity(0.05) : .clear))
        .contentShape(Rectangle())
        .onTapGesture(perform: onPlay)
        .onHover { h in withAnimation(.easeOut(duration: 0.12)) { hover = h } }
    }
}
