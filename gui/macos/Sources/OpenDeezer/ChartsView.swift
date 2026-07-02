import SwiftUI

// ChartsView — the global charts screen. The chart *tracks* drive the shared
// hero + track table (so Play/Shuffle work as elsewhere); chart albums, artists
// and playlists render as horizontal rails below. Album/artist/playlist tiles
// are all tappable into their existing destinations.
struct ChartsView: View {
    @EnvironmentObject var app: AppState

    var body: some View {
        ScrollView {
            VStack(spacing: 0) {
                HeroHeader()

                if !app.tracks.isEmpty {
                    TrackTable(tracks: app.tracks)
                }

                if !app.chartAlbums.isEmpty {
                    railHeader(L("Top Albums"))
                    rail {
                        ForEach(app.chartAlbums) { a in
                            TileCard(url: a.artworkUrl, title: a.name, sub: a.artistLine) {
                                app.openAlbumFromChart(a)
                            }
                        }
                    }
                }

                if !app.chartArtists.isEmpty {
                    railHeader(L("Top Artists"))
                    rail {
                        ForEach(app.chartArtists) { ar in
                            ArtistTileCard(artist: ar) { app.openArtist(ar.id) }
                        }
                    }
                }

                if !app.chartPlaylists.isEmpty {
                    railHeader(L("Top Playlists"))
                    rail {
                        ForEach(app.chartPlaylists) { p in
                            TileCard(url: p.artworkUrl, title: p.name,
                                     sub: Lp("%d tracks", p.trackCount)) {
                                app.openPlaylist(p)
                            }
                        }
                    }
                }

                Spacer().frame(height: 96) // clear the floating player bar
            }
        }
        .scrollContentBackground(.hidden)
        .background(DZ.windowBG)
    }

    private func railHeader(_ t: String) -> some View {
        Text(t).font(.system(size: 20, weight: .bold)).foregroundStyle(DZ.textPri)
            .frame(maxWidth: .infinity, alignment: .leading)
            .padding(.horizontal, 24).padding(.top, 22).padding(.bottom, 8)
    }

    @ViewBuilder private func rail<C: View>(@ViewBuilder _ content: () -> C) -> some View {
        ScrollView(.horizontal, showsIndicators: false) {
            HStack(alignment: .top, spacing: 18) { content() }
                .padding(.horizontal, 24)
        }
    }
}

// TileCard — a tappable artwork tile (albums / playlists rails).
struct TileCard: View {
    let url: String
    let title: String
    let sub: String
    let onTap: () -> Void
    @State private var hover = false

    var body: some View {
        Button(action: onTap) {
            VStack(alignment: .leading, spacing: 8) {
                ZStack(alignment: .bottomTrailing) {
                    Artwork(url: url, size: 150, radius: 8).shadow(radius: 6, y: 4)
                    if hover {
                        Circle().fill(DZ.accent).frame(width: 36, height: 36)
                            .overlay(Image(systemName: "play.fill").foregroundStyle(.white))
                            .shadow(radius: 4).padding(8)
                            .transition(.scale.combined(with: .opacity))
                    }
                }
                Text(title).font(.system(size: 13, weight: .medium))
                    .foregroundStyle(DZ.textPri).lineLimit(1)
                Text(sub).font(.caption).foregroundStyle(DZ.textSec).lineLimit(1)
            }
            .frame(width: 150, alignment: .leading)
        }
        .buttonStyle(.plain)
        .scaleEffect(hover ? 1.03 : 1)
        .onHover { h in withAnimation(.easeOut(duration: 0.15)) { hover = h } }
    }
}

// ArtistTileCard — a circular, tappable artist tile (charts / future search).
struct ArtistTileCard: View {
    let artist: ArtistInfo
    let onTap: () -> Void
    @State private var hover = false

    var body: some View {
        Button(action: onTap) {
            VStack(spacing: 8) {
                Artwork(url: artist.artworkUrl, size: 120, radius: 60).shadow(radius: 5, y: 3)
                Text(artist.name).font(.system(size: 12, weight: .medium))
                    .foregroundStyle(DZ.textPri).lineLimit(1).frame(width: 120)
            }
        }
        .buttonStyle(.plain)
        .scaleEffect(hover ? 1.04 : 1)
        .onHover { h in withAnimation(.easeOut(duration: 0.15)) { hover = h } }
    }
}
