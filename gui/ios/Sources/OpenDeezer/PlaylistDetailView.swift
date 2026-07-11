import SwiftUI

struct PlaylistDetailView: View {
    let playlist: Playlist
    @EnvironmentObject private var player: PlayerController
    @EnvironmentObject private var library: LibraryStore

    @State private var trackItems: [PlaylistTrackItem] = []
    @State private var isLoading = true
    @State private var errorText: String?
    @State private var showRename = false
    @State private var renameText = ""
    @State private var displayName: String
    @State private var showRenameError = false
    @State private var showDeleteConfirm = false
    @State private var showDeleteError = false
    @State private var showRemoveError = false
    @Environment(\.dismiss) private var dismiss

    init(playlist: Playlist) {
        self.playlist = playlist
        _displayName = State(initialValue: playlist.name)
    }

    var body: some View {
        List {
            Section {
                header
            }
            .listRowInsets(EdgeInsets())
            .listRowBackground(Color.clear)
            .listRowSeparator(.hidden)

            if isLoading {
                ProgressView().frame(maxWidth: .infinity)
            } else if let error = errorText {
                ContentUnavailableMessage(
                    systemImage: "wifi.slash", title: "Couldn't load playlist", message: error,
                    retry: { Task { await load() } }
                )
            } else if trackItems.isEmpty {
                ContentUnavailableMessage(
                    systemImage: "music.note.list", title: "Playlists",
                    message: String(localized: "This playlist is empty.")
                )
            } else {
                ForEach(Array(trackItems.enumerated()), id: \.element.id) { index, item in
                    playlistTrackRow(item, at: index)
                }
            }
        }
        .listStyle(.plain)
        .navigationBarTitleDisplayMode(.inline)
        .toolbar {
            if canEdit {
                ToolbarItem(placement: .topBarTrailing) {
                    Menu {
                        Button { renameText = displayName; showRename = true } label: {
                            Label("Rename", systemImage: "pencil")
                        }
                        Button(role: .destructive) { showDeleteConfirm = true } label: {
                            Label("Delete Playlist", systemImage: "trash")
                        }
                    } label: {
                        Image(systemName: "ellipsis.circle")
                    }
                    .accessibilityLabel("More")
                }
            }
        }
        .alert("Rename Playlist", isPresented: $showRename) {
            TextField("Name", text: $renameText)
            Button("Cancel", role: .cancel) {}
            Button("Save") {
                let title = renameText.trimmingCharacters(in: .whitespacesAndNewlines)
                guard !title.isEmpty else { return }
                Task {
                    let renamed = await library.renamePlaylist(playlist.id, title: title)
                    if renamed {
                        displayName = title
                    } else {
                        showRenameError = true
                    }
                }
            }
            .disabled(renameText.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
        }
        .alert("Rename Playlist", isPresented: $showRenameError) {
            Button("Done", role: .cancel) {}
        } message: {
            Text("Try again later.")
        }
        .alert("Remove", isPresented: $showRemoveError) {
            Button("Done", role: .cancel) {}
        } message: {
            Text("Try again later.")
        }
        .confirmationDialog("Delete this playlist?", isPresented: $showDeleteConfirm, titleVisibility: .visible) {
            Button("Delete Playlist", role: .destructive) {
                Task {
                    if await library.deletePlaylist(playlist.id) {
                        dismiss()
                    } else {
                        showDeleteError = true
                    }
                }
            }
        }
        .alert("Delete Playlist", isPresented: $showDeleteError) {
            Button("Done", role: .cancel) {}
        } message: {
            Text("Try again later.")
        }
        .task { await load() }
        .refreshable { await load() }
    }

    private var header: some View {
        VStack(spacing: 10) {
            RemoteArtwork(url: playlist.artworkUrl, cornerRadius: 12)
                .frame(width: 180, height: 180)
                .shadow(radius: 10, y: 6)
            Text(displayName).font(.title2.bold()).multilineTextAlignment(.center)
            (Text(playlist.owner) + Text(verbatim: " · ") + Text("\(displayTrackCount) songs"))
                .font(.footnote)
                .foregroundStyle(.secondary)

            Button {
                guard !tracks.isEmpty else { return }
                player.playQueue(tracks, startAt: 0)
            } label: {
                Label("Play", systemImage: "play.fill")
                    .frame(maxWidth: .infinity)
            }
            .glassButton(prominent: true)
            .tint(Palette.accent)
            .padding(.horizontal, 40)
            .padding(.top, 4)
            .disabled(tracks.isEmpty)
        }
        .frame(maxWidth: .infinity)
        .padding(.vertical, 20)
    }

    private func load() async {
        isLoading = trackItems.isEmpty
        do {
            let response = try await Engine.playlistTracks(playlist.id)
            trackItems = response.map { PlaylistTrackItem(track: $0) }
            errorText = nil
        } catch {
            if trackItems.isEmpty { errorText = error.localizedDescription }
        }
        isLoading = false
    }

    private var canEdit: Bool {
        library.playlists.contains { $0.id == playlist.id }
    }

    private var displayTrackCount: Int {
        isLoading || errorText != nil ? playlist.trackCount : trackItems.count
    }

    private var tracks: [Track] { trackItems.map(\.track) }

    @ViewBuilder
    private func playlistTrackRow(_ item: PlaylistTrackItem, at index: Int) -> some View {
        let row = TrackRow(track: item.track, tracks: tracks, showArtwork: false, indexLabel: index + 1)
        if canEdit {
            row.swipeActions(edge: .trailing, allowsFullSwipe: false) {
                Button(role: .destructive) {
                    Task {
                        let removed = await Engine.removeFromPlaylist(playlist.id, trackID: item.track.id)
                        guard removed else {
                            showRemoveError = true
                            return
                        }
                        if let currentIndex = trackItems.firstIndex(where: { $0.id == item.id }) {
                            trackItems.remove(at: currentIndex)
                        }
                    }
                } label: {
                    Label("Remove", systemImage: "minus.circle")
                }
            }
        } else {
            row
        }
    }
}

private struct PlaylistTrackItem: Identifiable {
    let id = UUID()
    let track: Track
}
