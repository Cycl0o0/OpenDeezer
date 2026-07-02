import SwiftUI

// AddToPlaylistSheet — picks a destination playlist for app.addTarget, populated
// from DZPlaylistsJSON. "New playlist…" reveals an inline field that creates a
// playlist (DZCreatePlaylist) and adds the track to it (DZAddToPlaylist).
struct AddToPlaylistSheet: View {
    @EnvironmentObject var app: AppState
    @State private var creating = false
    @State private var newName = ""

    var body: some View {
        VStack(spacing: 0) {
            header
            Divider().overlay(DZ.hairline)
            content
        }
        .frame(width: 420, height: 520)
        .background(DZ.windowBG)
    }

    private var header: some View {
        HStack(spacing: 12) {
            Artwork(url: app.addTarget?.artworkUrl ?? "", size: 40, radius: 5)
            VStack(alignment: .leading, spacing: 2) {
                Text(L("Add to Playlist"))
                    .font(.system(size: 16, weight: .bold)).foregroundStyle(DZ.textPri)
                Text(app.addTarget?.name ?? "")
                    .font(.system(size: 12)).foregroundStyle(DZ.textSec).lineLimit(1)
            }
            Spacer()
            Button(L("Cancel")) { app.showAddToPlaylist = false }
                .buttonStyle(.glass).tint(DZ.accent)
        }
        .padding(16)
    }

    @ViewBuilder private var content: some View {
        if app.pickerLoading {
            VStack { Spacer(); ProgressView().controlSize(.large).tint(DZ.accent); Spacer() }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
        } else {
            List {
                SwiftUI.Section {
                    Button { withAnimation { creating.toggle() } } label: {
                        Label(L("New playlist…"), systemImage: "plus.circle.fill")
                            .foregroundStyle(DZ.accent)
                    }
                    .buttonStyle(.plain)
                    .listRowBackground(Color.clear)

                    if creating {
                        HStack(spacing: 8) {
                            TextField(L("Playlist name"), text: $newName)
                                .textFieldStyle(.roundedBorder)
                                .onSubmit(commitCreate)
                            Button(L("Create"), action: commitCreate)
                                .buttonStyle(.glassProminent).tint(DZ.accent)
                                .disabled(newName.trimmingCharacters(in: .whitespaces).isEmpty)
                        }
                        .listRowBackground(Color.clear)
                    }
                }

                SwiftUI.Section {
                    ForEach(app.pickerPlaylists) { p in
                        Button { app.addTargetTrack(toPlaylist: p.id) } label: {
                            HStack(spacing: 10) {
                                Artwork(url: p.artworkUrl, size: 36, radius: 4)
                                VStack(alignment: .leading, spacing: 1) {
                                    Text(p.name).foregroundStyle(DZ.textPri).lineLimit(1)
                                    Text(Lp("%d tracks", p.trackCount))
                                        .font(.caption).foregroundStyle(DZ.textSec)
                                }
                                Spacer()
                            }
                            .contentShape(Rectangle())
                        }
                        .buttonStyle(.plain)
                        .listRowBackground(Color.clear)
                    }
                } header: {
                    Text(L("Your Playlists"))
                        .font(.system(size: 12, weight: .bold)).foregroundStyle(DZ.textSec)
                }
            }
            .listStyle(.inset)
            .scrollContentBackground(.hidden)
        }
    }

    private func commitCreate() {
        app.createPlaylistAndAddTarget(title: newName)
        newName = ""
        creating = false
    }
}
