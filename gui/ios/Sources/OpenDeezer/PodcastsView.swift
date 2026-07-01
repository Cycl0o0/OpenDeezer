import SwiftUI

struct PodcastsView: View {
    @State private var query = ""
    @State private var podcasts: [Podcast] = []
    @State private var isLoading = false
    @State private var errorText: String?
    @State private var hasSearched = false

    var body: some View {
        Group {
            if !hasSearched {
                ContentUnavailableMessage(
                    systemImage: "mic.fill", title: "Search Podcasts",
                    message: "Find shows by name to browse their episodes."
                )
            } else if isLoading {
                ProgressView()
            } else if let error = errorText {
                ContentUnavailableMessage(systemImage: "mic.slash", title: "Search failed", message: error)
            } else if podcasts.isEmpty {
                ContentUnavailableMessage(systemImage: "mic.slash", title: "No podcasts found", message: "Try a different search.")
            } else {
                List(podcasts) { podcast in
                    NavigationLink { PodcastDetailView(podcast: podcast) } label: {
                        HStack {
                            RemoteArtwork(url: podcast.artworkUrl, cornerRadius: 8)
                                .frame(width: 52, height: 52)
                            VStack(alignment: .leading, spacing: 2) {
                                Text(podcast.name).font(.body)
                                Text("\(podcast.episodeCount) episodes").font(.caption).foregroundStyle(.secondary)
                            }
                        }
                    }
                }
                .listStyle(.plain)
            }
        }
        .navigationTitle("Podcasts")
        .searchable(text: $query, prompt: "Search podcasts")
        .onSubmit(of: .search) { Task { await search() } }
    }

    private func search() async {
        guard !query.trimmingCharacters(in: .whitespaces).isEmpty else { return }
        isLoading = true
        hasSearched = true
        do {
            podcasts = try await Engine.searchPodcasts(query)
            errorText = nil
        } catch {
            errorText = error.localizedDescription
        }
        isLoading = false
    }
}

struct PodcastDetailView: View {
    let podcast: Podcast
    @EnvironmentObject private var player: PlayerController
    @State private var episodes: [Episode] = []
    @State private var isLoading = true
    @State private var errorText: String?

    var body: some View {
        List {
            Section {
                VStack(spacing: 10) {
                    RemoteArtwork(url: podcast.artworkUrl, cornerRadius: 12)
                        .frame(width: 160, height: 160)
                    Text(podcast.name).font(.title3.bold()).multilineTextAlignment(.center)
                    if !podcast.description.isEmpty {
                        Text(podcast.description)
                            .font(.footnote)
                            .foregroundStyle(.secondary)
                            .multilineTextAlignment(.center)
                            .lineLimit(4)
                    }
                }
                .frame(maxWidth: .infinity)
                .padding(.vertical, 12)
            }
            .listRowInsets(EdgeInsets())
            .listRowBackground(Color.clear)
            .listRowSeparator(.hidden)

            if isLoading {
                ProgressView().frame(maxWidth: .infinity)
            } else if let error = errorText {
                ContentUnavailableMessage(systemImage: "mic.slash", title: "Couldn't load episodes", message: error)
            } else {
                ForEach(episodes) { episode in
                    Button {
                        player.playEpisode(episode)
                    } label: {
                        VStack(alignment: .leading, spacing: 4) {
                            Text(episode.title).font(.body).foregroundStyle(.primary)
                            Text(episode.releaseDate).font(.caption2).foregroundStyle(.secondary)
                            if !episode.description.isEmpty {
                                Text(episode.description)
                                    .font(.caption)
                                    .foregroundStyle(.secondary)
                                    .lineLimit(2)
                            }
                            Text(episode.durationText).font(.caption2).foregroundStyle(.secondary)
                        }
                    }
                }
            }
        }
        .listStyle(.plain)
        .navigationBarTitleDisplayMode(.inline)
        .task { await load() }
    }

    private func load() async {
        isLoading = true
        do {
            episodes = try await Engine.podcastEpisodes(podcast.id)
            errorText = nil
        } catch {
            errorText = error.localizedDescription
        }
        isLoading = false
    }
}
