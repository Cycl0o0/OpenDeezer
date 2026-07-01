import SwiftUI

struct PlaylistDetailView: View {
    let playlist: Playlist
    @EnvironmentObject private var player: PlayerController
    @EnvironmentObject private var library: LibraryStore

    @State private var tracks: [Track] = []
    @State private var isLoading = true
    @State private var errorText: String?
    @State private var showRename = false
    @State private var renameText = ""
    @State private var showDeleteConfirm = false
    @Environment(\.dismiss) private var dismiss

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
                ContentUnavailableMessage(systemImage: "wifi.slash", title: "Couldn't load playlist", message: error)
            } else {
                ForEach(Array(tracks.enumerated()), id: \.element.id) { index, track in
                    TrackRow(track: track, tracks: tracks, showArtwork: false, indexLabel: index + 1)
                        .swipeActions(edge: .trailing) {
                            Button(role: .destructive) {
                                Task {
                                    _ = await Engine.removeFromPlaylist(playlist.id, trackID: track.id)
                                    tracks.removeAll { $0.id == track.id }
                                }
                            } label: {
                                Label("Remove", systemImage: "minus.circle")
                            }
                        }
                }
            }
        }
        .listStyle(.plain)
        .navigationBarTitleDisplayMode(.inline)
        .toolbar {
            ToolbarItem(placement: .topBarTrailing) {
                Menu {
                    Button { renameText = playlist.name; showRename = true } label: {
                        Label("Rename", systemImage: "pencil")
                    }
                    Button(role: .destructive) { showDeleteConfirm = true } label: {
                        Label("Delete Playlist", systemImage: "trash")
                    }
                } label: {
                    Image(systemName: "ellipsis.circle")
                }
            }
        }
        .alert("Rename Playlist", isPresented: $showRename) {
            TextField("Name", text: $renameText)
            Button("Cancel", role: .cancel) {}
            Button("Save") {
                Task { await library.renamePlaylist(playlist.id, title: renameText) }
            }
        }
        .confirmationDialog("Delete this playlist?", isPresented: $showDeleteConfirm, titleVisibility: .visible) {
            Button("Delete Playlist", role: .destructive) {
                Task {
                    await library.deletePlaylist(playlist.id)
                    dismiss()
                }
            }
        }
        .task { await load() }
        .refreshable { await load() }
    }

    private var header: some View {
        VStack(spacing: 10) {
            RemoteArtwork(url: playlist.artworkUrl, cornerRadius: 12)
                .frame(width: 180, height: 180)
                .shadow(radius: 10, y: 6)
            Text(playlist.name).font(.title2.bold()).multilineTextAlignment(.center)
            Text("\(playlist.owner) · \(playlist.trackCount) songs")
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
        }
        .frame(maxWidth: .infinity)
        .padding(.vertical, 20)
    }

    private func load() async {
        isLoading = tracks.isEmpty
        do {
            tracks = try await Engine.playlistTracks(playlist.id)
            errorText = nil
        } catch {
            errorText = error.localizedDescription
        }
        isLoading = false
    }
}
