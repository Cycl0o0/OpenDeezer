import SwiftUI

struct ArtistView: View {
    let artistID: String
    let artistName: String

    @EnvironmentObject private var player: PlayerController
    @State private var profile: ArtistProfilePage?
    @State private var isLoading = true
    @State private var errorText: String?

    var body: some View {
        ScrollView {
            if isLoading {
                ProgressView().padding(.top, 60)
            } else if let error = errorText {
                ContentUnavailableMessage(
                    systemImage: "person.wave.2", title: "Couldn't load artist", message: error,
                    retry: { Task { await load() } }
                )
                    .padding(.top, 40)
            } else if let profile {
                VStack(alignment: .leading, spacing: 24) {
                    VStack(spacing: 8) {
                        RemoteArtwork(url: profile.artist.artworkUrl, cornerRadius: 90)
                            .frame(width: 160, height: 160)
                            .clipShape(Circle())
                        Text(profile.artist.name).font(.title2.bold())
                        if profile.artist.nbFans > 0 {
                            Text("\(profile.artist.nbFans) fans")
                                .font(.footnote)
                                .foregroundStyle(.secondary)
                        }
                        Button {
                            player.startArtistRadio(artistID: artistID)
                        } label: {
                            Label("Start Radio", systemImage: "dot.radiowaves.left.and.right")
                                .frame(maxWidth: .infinity)
                        }
                        .glassButton(prominent: true)
                        .tint(Palette.accent)
                        .padding(.horizontal, 40)
                        .padding(.top, 4)
                    }
                    .frame(maxWidth: .infinity)

                    if !profile.top.isEmpty {
                        SectionHeader(title: "Top Tracks")
                        VStack(spacing: 0) {
                            ForEach(Array(profile.top.prefix(10).enumerated()), id: \.offset) { index, track in
                                TrackRow(track: track, tracks: profile.top, showArtwork: true, indexLabel: index + 1)
                                    .padding(.horizontal, 20)
                                Divider().padding(.leading, 78)
                            }
                        }
                    }
                    if !profile.albums.isEmpty {
                        SectionHeader(title: "Albums")
                        AlbumRail(albums: profile.albums)
                    }
                    if !profile.related.isEmpty {
                        SectionHeader(title: "Fans Also Like")
                        ArtistRail(artists: profile.related)
                    }
                }
                .padding(.vertical, 16)
            }
        }
        .navigationTitle(artistName)
        .navigationBarTitleDisplayMode(.inline)
        .task { await load() }
        .refreshable { await load() }
    }

    private func load() async {
        isLoading = profile == nil
        do {
            profile = try await Engine.artistProfile(artistID)
            errorText = nil
        } catch {
            if profile == nil { errorText = error.localizedDescription }
        }
        isLoading = false
    }
}
