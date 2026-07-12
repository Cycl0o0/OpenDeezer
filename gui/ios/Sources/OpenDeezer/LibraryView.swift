import SwiftUI

struct LibraryView: View {
    @EnvironmentObject private var library: LibraryStore
    @EnvironmentObject private var session: SessionStore
    @State private var showCreatePlaylist = false
    @State private var newPlaylistTitle = ""
    @State private var showCreateError = false
    @State private var showSettings = false
    @State private var playlistPendingDeletion: Playlist?
    @State private var showDeleteConfirm = false
    @State private var showDeleteError = false

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
                NavigationLink { HistoryView() } label: {
                    Label {
                        Text("Recently Played")
                    } icon: {
                        ZStack {
                            RoundedRectangle(cornerRadius: 6).fill(Color.indigo.gradient)
                                .frame(width: 32, height: 32)
                            Image(systemName: "clock.arrow.circlepath").foregroundStyle(.white).font(.system(size: 14))
                        }
                    }
                }
            }

            Section("Playlists") {
                if library.isLoading && library.playlists.isEmpty {
                    ProgressView()
                        .frame(maxWidth: .infinity)
                        .listRowSeparator(.hidden)
                }
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
                    .swipeActions {
                        Button(role: .destructive) {
                            playlistPendingDeletion = playlist
                            showDeleteConfirm = true
                        } label: {
                            Label("Delete Playlist", systemImage: "trash")
                        }
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
                .accessibilityLabel("Settings")
            }
        }
        .sheet(isPresented: $showSettings) { SettingsView() }
        .alert("New Playlist", isPresented: $showCreatePlaylist) {
            TextField("Playlist name", text: $newPlaylistTitle)
            Button("Cancel", role: .cancel) { newPlaylistTitle = "" }
            Button("Create") {
                let title = newPlaylistTitle
                newPlaylistTitle = ""
                let trimmed = title.trimmingCharacters(in: .whitespacesAndNewlines)
                guard !trimmed.isEmpty else { return }
                Task {
                    if await library.createPlaylist(title: trimmed) == nil { showCreateError = true }
                }
            }
            .disabled(newPlaylistTitle.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
        }
        .alert("New Playlist", isPresented: $showCreateError) {
            Button("Done", role: .cancel) {}
        } message: {
            Text("Try again later.")
        }
        .confirmationDialog("Delete this playlist?", isPresented: $showDeleteConfirm, titleVisibility: .visible) {
            Button("Delete Playlist", role: .destructive) {
                guard let playlist = playlistPendingDeletion else { return }
                playlistPendingDeletion = nil
                Task {
                    if !(await library.deletePlaylist(playlist.id)) { showDeleteError = true }
                }
            }
            Button("Cancel", role: .cancel) { playlistPendingDeletion = nil }
        }
        .alert("Delete Playlist", isPresented: $showDeleteError) {
            Button("Done", role: .cancel) {}
        } message: {
            Text("Try again later.")
        }
        .task { await library.refreshAll() }
        .refreshable { await library.refreshAll() }
    }
}

struct LikedSongsView: View {
    @EnvironmentObject private var library: LibraryStore
    @EnvironmentObject private var player: PlayerController
    @State private var isLoading = true

    var body: some View {
        Group {
            if isLoading && library.favorites.isEmpty {
                ProgressView()
            } else if library.favorites.isEmpty {
                ContentUnavailableMessage(
                    systemImage: "heart", title: "No liked songs",
                    message: String(localized: "Tracks you like will show up here.")
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
        .task {
            await library.refreshFavorites()
            isLoading = false
        }
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
    @EnvironmentObject private var session: SessionStore
    @EnvironmentObject private var downloads: DownloadStore
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
                        .accessibilityLabel("Playing")
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
                Label(
                    library.isFavorite(track.id) ? "Unlike" : "Like",
                    systemImage: library.isFavorite(track.id) ? "heart.slash" : "heart"
                )
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
                player.startRadio(seededBy: track)
            } label: {
                Label("Start Radio", systemImage: "dot.radiowaves.left.and.right")
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
            // Downloads save the full track to disk — premium-only, so it's
            // shown only for paid accounts (Free streams but can't download).
            if session.account?.premium == true {
                Button {
                    downloads.download(track, isPremium: true)
                } label: {
                    Label("Download", systemImage: "arrow.down.circle")
                }
            }
        }
    }

    private var isCurrent: Bool { player.current?.id == track.id }
}
