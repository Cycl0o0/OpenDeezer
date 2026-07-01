import SwiftUI

struct ArtistView: View {
    let artistID: String
    let artistName: String

    @State private var profile: ArtistProfilePage?
    @State private var isLoading = true
    @State private var errorText: String?

    var body: some View {
        ScrollView {
            if isLoading {
                ProgressView().padding(.top, 60)
            } else if let error = errorText {
                ContentUnavailableMessage(systemImage: "person.wave.2", title: "Couldn't load artist", message: error)
                    .padding(.top, 40)
            } else if let profile {
                VStack(alignment: .leading, spacing: 24) {
                    VStack(spacing: 8) {
                        RemoteArtwork(url: profile.artist.artworkUrl, cornerRadius: 90)
                            .frame(width: 160, height: 160)
                            .clipShape(Circle())
                        Text(profile.artist.name).font(.title2.bold())
                        if profile.artist.nbFans > 0 {
                            Text("\(profile.artist.nbFans.formatted()) fans")
                                .font(.footnote)
                                .foregroundStyle(.secondary)
                        }
                    }
                    .frame(maxWidth: .infinity)

                    if !profile.top.isEmpty {
                        SectionHeader(title: "Top Tracks")
                        VStack(spacing: 0) {
                            ForEach(Array(profile.top.prefix(10).enumerated()), id: \.element.id) { index, track in
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
    }

    private func load() async {
        isLoading = true
        do {
            profile = try await Engine.artistProfile(artistID)
            errorText = nil
        } catch {
            errorText = error.localizedDescription
        }
        isLoading = false
    }
}
