import SwiftUI

struct AddToPlaylistSheet: View {
    let track: Track
    @EnvironmentObject private var library: LibraryStore
    @Environment(\.dismiss) private var dismiss

    @State private var showCreate = false
    @State private var newTitle = ""
    @State private var addedTo: Set<String> = []

    var body: some View {
        NavigationStack {
            List {
                Button {
                    showCreate = true
                } label: {
                    Label("New Playlist", systemImage: "plus.circle.fill")
                }
                ForEach(library.playlists) { playlist in
                    Button {
                        Task {
                            let ok = await library.addToPlaylist(playlist.id, track: track)
                            if ok { addedTo.insert(playlist.id) }
                        }
                    } label: {
                        HStack {
                            Text(playlist.name).foregroundStyle(.primary)
                            Spacer()
                            if addedTo.contains(playlist.id) {
                                Image(systemName: "checkmark.circle.fill").foregroundStyle(Palette.accent)
                            }
                        }
                    }
                }
            }
            .navigationTitle("Add to Playlist")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .topBarTrailing) {
                    Button("Done") { dismiss() }
                }
            }
            .alert("New Playlist", isPresented: $showCreate) {
                TextField("Playlist name", text: $newTitle)
                Button("Cancel", role: .cancel) { newTitle = "" }
                Button("Create") {
                    let title = newTitle
                    newTitle = ""
                    guard !title.isEmpty else { return }
                    Task {
                        if let id = await library.createPlaylist(title: title) {
                            _ = await library.addToPlaylist(id, track: track)
                            addedTo.insert(id)
                        }
                    }
                }
            }
        }
    }
}
