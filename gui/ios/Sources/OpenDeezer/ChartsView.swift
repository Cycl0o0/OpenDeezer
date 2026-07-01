import SwiftUI

struct ChartsView: View {
    @State private var charts: ChartsResponse?
    @State private var isLoading = true
    @State private var errorText: String?

    var body: some View {
        ScrollView {
            if isLoading {
                ProgressView().padding(.top, 60)
            } else if let error = errorText {
                ContentUnavailableMessage(systemImage: "chart.line.uptrend.xyaxis", title: "Charts unavailable", message: error)
                    .padding(.top, 40)
            } else if let charts {
                VStack(alignment: .leading, spacing: 28) {
                    if !charts.tracks.isEmpty {
                        SectionHeader(title: "Top Tracks")
                        TrackRail(tracks: charts.tracks)
                    }
                    if !charts.albums.isEmpty {
                        SectionHeader(title: "Top Albums")
                        AlbumRail(albums: charts.albums)
                    }
                    if !charts.artists.isEmpty {
                        SectionHeader(title: "Top Artists")
                        ArtistRail(artists: charts.artists)
                    }
                    if !charts.playlists.isEmpty {
                        SectionHeader(title: "Top Playlists")
                        PlaylistRail(playlists: charts.playlists)
                    }
                }
                .padding(.vertical, 12)
            }
        }
        .navigationTitle("Charts")
        .task { await load() }
        .refreshable { await load() }
    }

    private func load() async {
        isLoading = charts == nil
        do {
            charts = try await Engine.charts()
            errorText = nil
        } catch {
            errorText = error.localizedDescription
        }
        isLoading = false
    }
}

struct ArtistRail: View {
    let artists: [ArtistInfo]
    var body: some View {
        ScrollView(.horizontal, showsIndicators: false) {
            HStack(alignment: .top, spacing: 16) {
                ForEach(artists) { artist in
                    NavigationLink { ArtistView(artistID: artist.id, artistName: artist.name) } label: {
                        VStack(spacing: 6) {
                            RemoteArtwork(url: artist.artworkUrl, cornerRadius: 65)
                                .frame(width: 110, height: 110)
                                .clipShape(Circle())
                            Text(artist.name)
                                .font(.caption.weight(.semibold))
                                .foregroundStyle(.primary)
                                .lineLimit(1)
                        }
                        .frame(width: 110)
                    }
                    .buttonStyle(.plain)
                }
            }
            .padding(.horizontal, 20)
        }
    }
}
