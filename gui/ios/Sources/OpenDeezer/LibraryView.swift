import SwiftUI

struct LibraryView: View {
    @EnvironmentObject private var library: LibraryStore
    @EnvironmentObject private var session: SessionStore
    @State private var showCreatePlaylist = false
    @State private var newPlaylistTitle = ""
    @State private var showSettings = false

    var body: some View {
        List {
            Section {
                NavigationLink { LikedSongsView() } label: {
                    Label {
                        Text("Liked Songs")
                    } icon: {
                        ZStack {
                            RoundedRectangle(cornerRadius: 6).fill(Palette.accent.gradient)
                                .frame(width: 32, height: 32)
                            Image(systemName: "heart.fill").foregroundStyle(.white).font(.system(size: 14))
                        }
                    }
                }
                NavigationLink { FlowView() } label: {
                    Label {
                        Text("Flow")
                    } icon: {
                        ZStack {
                            RoundedRectangle(cornerRadius: 6).fill(Color.pink.gradient)
                                .frame(width: 32, height: 32)
                            Image(systemName: "waveform").foregroundStyle(.white).font(.system(size: 14))
                        }
                    }
                }
                NavigationLink { ChartsView() } label: {
                    Label {
                        Text("Charts")
                    } icon: {
                        ZStack {
                            RoundedRectangle(cornerRadius: 6).fill(Color.orange.gradient)
                                .frame(width: 32, height: 32)
                            Image(systemName: "chart.line.uptrend.xyaxis").foregroundStyle(.white).font(.system(size: 14))
                        }
                    }
                }
                NavigationLink { PodcastsView() } label: {
                    Label {
                        Text("Podcasts")
                    } icon: {
                        ZStack {
                            RoundedRectangle(cornerRadius: 6).fill(Color.teal.gradient)
                                .frame(width: 32, height: 32)
                            Image(systemName: "mic.fill").foregroundStyle(.white).font(.system(size: 14))
                        }
                    }
                }
            }

            Section("Playlists") {
                ForEach(library.playlists) { playlist in
                    NavigationLink { PlaylistDetailView(playlist: playlist) } label: {
                        HStack {
                            RemoteArtwork(url: playlist.artworkUrl, cornerRadius: 6)
                                .frame(width: 40, height: 40)
                            VStack(alignment: .leading) {
                                Text(playlist.name)
                                Text("\(playlist.trackCount) songs").font(.caption).foregroundStyle(.secondary)
                            }
                        }
                    }
                }
                .onDelete { offsets in
                    for index in offsets {
                        let playlist = library.playlists[index]
                        Task { await library.deletePlaylist(playlist.id) }
                    }
                }

                Button {
                    showCreatePlaylist = true
                } label: {
                    Label("New Playlist", systemImage: "plus.circle.fill")
                }
            }
        }
        .navigationTitle("Library")
        .toolbar {
            ToolbarItem(placement: .topBarTrailing) {
                Button { showSettings = true } label: {
                    Image(systemName: "gearshape")
                }
            }
        }
        .sheet(isPresented: $showSettings) { SettingsView() }
        .alert("New Playlist", isPresented: $showCreatePlaylist) {
            TextField("Playlist name", text: $newPlaylistTitle)
            Button("Cancel", role: .cancel) { newPlaylistTitle = "" }
            Button("Create") {
                let title = newPlaylistTitle
                newPlaylistTitle = ""
                guard !title.isEmpty else { return }
                Task { await library.createPlaylist(title: title) }
            }
        }
        .task { await library.refreshAll() }
        .refreshable { await library.refreshAll() }
    }
}

struct LikedSongsView: View {
    @EnvironmentObject private var library: LibraryStore
    @EnvironmentObject private var player: PlayerController

    var body: some View {
        Group {
            if library.favorites.isEmpty {
                ContentUnavailableMessage(
                    systemImage: "heart", title: "No liked songs",
                    message: "Tracks you like will show up here."
                )
            } else {
                List {
                    ForEach(library.favorites) { track in
                        TrackRow(track: track, tracks: library.favorites)
                    }
                }
                .listStyle(.plain)
            }
        }
        .navigationTitle("Liked Songs")
        .task { await library.refreshFavorites() }
        .refreshable { await library.refreshFavorites() }
    }
}

/// Standard Apple-Music-style track row used across list-based screens.
struct TrackRow: View {
    let track: Track
    let tracks: [Track]
    var showArtwork: Bool = true
    var indexLabel: Int? = nil

    @EnvironmentObject private var player: PlayerController
    @EnvironmentObject private var library: LibraryStore
    @State private var showAddToPlaylist = false

    var body: some View {
        Button {
            player.play(track, in: tracks)
        } label: {
            HStack(spacing: 12) {
                if let indexLabel {
                    Text("\(indexLabel)")
                        .font(.subheadline)
                        .foregroundStyle(.secondary)
                        .frame(width: 20, alignment: .trailing)
                }
                if showArtwork {
                    RemoteArtwork(url: track.artworkUrl, cornerRadius: 6)
                        .frame(width: 46, height: 46)
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
                if isCurrent && player.isPlaying {
                    Image(systemName: "speaker.wave.2.fill")
                        .foregroundStyle(Palette.accent)
                        .font(.caption)
                } else {
                    Text(track.durationText)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
            }
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .swipeActions(edge: .trailing) {
            Button {
                library.toggleFavorite(track)
            } label: {
                Label("Like", systemImage: library.isFavorite(track.id) ? "heart.slash" : "heart")
            }
            .tint(Palette.accent)
        }
        .swipeActions(edge: .leading) {
            Button {
                showAddToPlaylist = true
            } label: {
                Label("Add", systemImage: "plus")
            }
            .tint(.blue)
        }
        .sheet(isPresented: $showAddToPlaylist) {
            AddToPlaylistSheet(track: track)
        }
        .contextMenu {
            Button {
                player.play(track, in: tracks)
            } label: {
                Label("Play", systemImage: "play.fill")
            }
            Button {
                library.toggleFavorite(track)
            } label: {
                Label(library.isFavorite(track.id) ? "Unlike" : "Like", systemImage: library.isFavorite(track.id) ? "heart.slash" : "heart")
            }
            Button {
                showAddToPlaylist = true
            } label: {
                Label("Add to Playlist", systemImage: "text.badge.plus")
            }
        }
    }

    private var isCurrent: Bool { player.current?.id == track.id }
}
