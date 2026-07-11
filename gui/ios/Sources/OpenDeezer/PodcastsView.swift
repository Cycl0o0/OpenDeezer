import SwiftUI

struct PodcastsView: View {
    @State private var query = ""
    @State private var podcasts: [Podcast] = []
    @State private var isLoading = false
    @State private var errorText: String?
    @State private var hasSearched = false
    @State private var searchTask: Task<Void, Never>?

    var body: some View {
        Group {
            if !hasSearched {
                ContentUnavailableMessage(
                    systemImage: "mic.fill", title: "Search Podcasts",
                    message: String(localized: "Find shows by name to browse their episodes.")
                )
            } else if isLoading {
                ProgressView()
            } else if let error = errorText {
                ContentUnavailableMessage(
                    systemImage: "mic.slash", title: "Search failed", message: error,
                    retry: startSearch
                )
            } else if podcasts.isEmpty {
                ContentUnavailableMessage(systemImage: "mic.slash", title: "No podcasts found", message: String(localized: "Try a different search."))
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
                .scrollDismissesKeyboard(.interactively)
            }
        }
        .navigationTitle("Podcasts")
        .searchable(text: $query, prompt: "Search podcasts")
        .onSubmit(of: .search) { startSearch() }
        .onChange(of: query) { _, _ in
            searchTask?.cancel()
            searchTask = nil
            podcasts = []
            errorText = nil
            hasSearched = false
            isLoading = false
        }
        .onDisappear {
            let wasSearching = searchTask != nil
            searchTask?.cancel()
            searchTask = nil
            isLoading = false
            if wasSearching { hasSearched = false }
        }
    }

    private func startSearch() {
        let term = query.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !term.isEmpty else { return }
        searchTask?.cancel()
        isLoading = true
        hasSearched = true
        errorText = nil
        searchTask = Task { await search(term) }
    }

    private func search(_ term: String) async {
        do {
            let response = try await Engine.searchPodcasts(term)
            guard !Task.isCancelled,
                  term == query.trimmingCharacters(in: .whitespacesAndNewlines) else { return }
            podcasts = response
            errorText = nil
        } catch {
            guard !Task.isCancelled,
                  term == query.trimmingCharacters(in: .whitespacesAndNewlines) else { return }
            podcasts = []
            errorText = error.localizedDescription
        }
        isLoading = false
        searchTask = nil
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
                ContentUnavailableMessage(
                    systemImage: "mic.slash", title: "Couldn't load episodes", message: error,
                    retry: { Task { await load() } }
                )
            } else if episodes.isEmpty {
                ContentUnavailableMessage(
                    systemImage: "mic.slash", title: "No episodes found",
                    message: String(localized: "Try again later.")
                )
            } else {
                ForEach(episodes) { episode in
                    Button {
                        player.playEpisode(episode)
                    } label: {
                        HStack(spacing: 12) {
                            VStack(alignment: .leading, spacing: 4) {
                                Text(episode.title).font(.body).foregroundStyle(.primary)
                                if !episode.releaseDate.isEmpty {
                                    Text(episode.releaseDate).font(.caption2).foregroundStyle(.secondary)
                                }
                                if !episode.description.isEmpty {
                                    Text(episode.description)
                                        .font(.caption)
                                        .foregroundStyle(.secondary)
                                        .lineLimit(2)
                                }
                                if episode.durationMs > 0 {
                                    Text(episode.durationText).font(.caption2).foregroundStyle(.secondary)
                                }
                            }
                            Spacer(minLength: 8)
                            Image(systemName: player.current?.id == episode.id && player.isPlaying
                                  ? "speaker.wave.2.fill" : "play.circle.fill")
                                .font(.title2)
                                .foregroundStyle(player.current?.id == episode.id ? Palette.accent : .secondary)
                                .accessibilityHidden(true)
                        }
                        .contentShape(Rectangle())
                    }
                    .buttonStyle(.plain)
                    .accessibilityHint("Play")
                }
            }
        }
        .listStyle(.plain)
        .navigationBarTitleDisplayMode(.inline)
        .task { await load() }
        .refreshable { await load() }
    }

    private func load() async {
        isLoading = episodes.isEmpty
        do {
            episodes = try await Engine.podcastEpisodes(podcast.id)
            errorText = nil
        } catch {
            if episodes.isEmpty { errorText = error.localizedDescription }
        }
        isLoading = false
    }
}
