import SwiftUI

struct AddToPlaylistSheet: View {
    let track: Track
    @EnvironmentObject private var library: LibraryStore
    @Environment(\.dismiss) private var dismiss

    @State private var showCreate = false
    @State private var newTitle = ""
    @State private var addedTo: Set<String> = []
    @State private var addingTo: Set<String> = []
    @State private var showAddError = false

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
                            guard !addingTo.contains(playlist.id) else { return }
                            addingTo.insert(playlist.id)
                            let ok = await library.addToPlaylist(playlist.id, track: track)
                            if ok {
                                addedTo.insert(playlist.id)
                            } else {
                                showAddError = true
                            }
                            addingTo.remove(playlist.id)
                        }
                    } label: {
                        HStack {
                            Text(playlist.name).foregroundStyle(.primary)
                            Spacer()
                            if addingTo.contains(playlist.id) {
                                ProgressView()
                            } else if addedTo.contains(playlist.id) {
                                Image(systemName: "checkmark.circle.fill").foregroundStyle(Palette.accent)
                            }
                        }
                    }
                    .disabled(addingTo.contains(playlist.id) || addedTo.contains(playlist.id))
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
                    let trimmed = title.trimmingCharacters(in: .whitespacesAndNewlines)
                    guard !trimmed.isEmpty else { return }
                    Task {
                        if let id = await library.createPlaylist(title: trimmed) {
                            if await library.addToPlaylist(id, track: track) {
                                addedTo.insert(id)
                            } else {
                                showAddError = true
                            }
                        } else {
                            showAddError = true
                        }
                    }
                }
                .disabled(newTitle.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
            }
        }
        .presentationDetents([.medium, .large])
        .alert("Add to Playlist", isPresented: $showAddError) {
            Button("Done", role: .cancel) {}
        } message: {
            Text("Try again later.")
        }
    }
}
