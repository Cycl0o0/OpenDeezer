import SwiftUI

struct HomeView: View {
    @EnvironmentObject private var player: PlayerController
    @EnvironmentObject private var library: LibraryStore
    @EnvironmentObject private var session: SessionStore

    @State private var home: HomeResponse?
    @State private var isLoading = true
    @State private var errorText: String?

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 28) {
                Text(greeting)
                    .font(.largeTitle.bold())
                    .padding(.horizontal, 20)
                    .padding(.top, 4)

                quickPicks

                if isLoading {
                    ProgressView().frame(maxWidth: .infinity).padding(.top, 40)
                } else if let error = errorText {
                    ContentUnavailableMessage(systemImage: "wifi.slash", title: "Couldn't load Home", message: error)
                        .padding(.top, 24)
                } else {
                    if let tracks = home?.topTracks, !tracks.isEmpty {
                        SectionHeader(title: "Top Tracks")
                        TrackRail(tracks: tracks)
                    }
                    if let playlists = library.playlists.isEmpty ? home?.playlists : library.playlists, !playlists.isEmpty {
                        SectionHeader(title: "Your Playlists")
                        PlaylistRail(playlists: playlists)
                    }
                    if let albums = home?.topAlbums, !albums.isEmpty {
                        SectionHeader(title: "Top Albums")
                        AlbumRail(albums: albums)
                    }
                }
            }
            .padding(.bottom, 24)
        }
        .navigationTitle("")
        .navigationBarHidden(true)
        .task { await load() }
        .refreshable { await load() }
    }

    private var greeting: String {
        let hour = Calendar.current.component(.hour, from: Date())
        switch hour {
        case 5..<12: return "Good morning"
        case 12..<18: return "Good afternoon"
        default: return "Good evening"
        }
    }

    private var quickPicks: some View {
        ScrollView(.horizontal, showsIndicators: false) {
            HStack(spacing: 12) {
                NavigationLink { LikedSongsView() } label: {
                    QuickPickCard(title: "Liked Songs", systemImage: "heart.fill", tint: Palette.accent)
                }
                NavigationLink { FlowView() } label: {
                    QuickPickCard(title: "Flow", systemImage: "waveform", tint: .pink)
                }
                NavigationLink { ChartsView() } label: {
                    QuickPickCard(title: "Charts", systemImage: "chart.line.uptrend.xyaxis", tint: .orange)
                }
                NavigationLink { PodcastsView() } label: {
                    QuickPickCard(title: "Podcasts", systemImage: "mic.fill", tint: .teal)
                }
            }
            .padding(.horizontal, 20)
        }
        .buttonStyle(.plain)
    }

    private func load() async {
        isLoading = home == nil
        do {
            home = try await Engine.home()
            errorText = nil
        } catch {
            errorText = error.localizedDescription
        }
        isLoading = false
        await library.refreshPlaylists()
    }
}

// MARK: - Reusable pieces

struct SectionHeader: View {
    let title: String
    var body: some View {
        Text(title)
            .font(.title2.bold())
            .padding(.horizontal, 20)
    }
}

struct QuickPickCard: View {
    let title: String
    let systemImage: String
    let tint: Color

    var body: some View {
        HStack(spacing: 10) {
            ZStack {
                RoundedRectangle(cornerRadius: 8, style: .continuous)
                    .fill(tint.gradient)
                    .frame(width: 44, height: 44)
                Image(systemName: systemImage)
                    .foregroundStyle(.white)
                    .font(.system(size: 18, weight: .semibold))
            }
            Text(title)
                .font(.subheadline.weight(.semibold))
                .foregroundStyle(.primary)
                .lineLimit(1)
        }
        .padding(.trailing, 16)
        .frame(width: 200, height: 44)
        .glassCard(cornerRadius: 12)
    }
}

struct TrackRail: View {
    let tracks: [Track]
    var body: some View {
        ScrollView(.horizontal, showsIndicators: false) {
            HStack(alignment: .top, spacing: 16) {
                ForEach(tracks) { track in
                    TrackTile(track: track, tracks: tracks)
                }
            }
            .padding(.horizontal, 20)
        }
    }
}

struct TrackTile: View {
    let track: Track
    let tracks: [Track]
    @EnvironmentObject private var player: PlayerController

    var body: some View {
        Button {
            player.play(track, in: tracks)
        } label: {
            VStack(alignment: .leading, spacing: 6) {
                RemoteArtwork(url: track.artworkUrl, cornerRadius: 10)
                    .frame(width: 130, height: 130)
                Text(track.name)
                    .font(.subheadline.weight(.semibold))
                    .lineLimit(1)
                    .foregroundStyle(.primary)
                Text(track.artistLine)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }
            .frame(width: 130)
        }
        .buttonStyle(.plain)
    }
}

struct AlbumRail: View {
    let albums: [Album]
    var body: some View {
        ScrollView(.horizontal, showsIndicators: false) {
            HStack(alignment: .top, spacing: 16) {
                ForEach(albums) { album in
                    NavigationLink { AlbumDetailView(album: album) } label: {
                        VStack(alignment: .leading, spacing: 6) {
                            RemoteArtwork(url: album.artworkUrl, cornerRadius: 10)
                                .frame(width: 130, height: 130)
                            Text(album.name).font(.subheadline.weight(.semibold)).lineLimit(1).foregroundStyle(.primary)
                            Text(album.artistLine).font(.caption).foregroundStyle(.secondary).lineLimit(1)
                        }
                        .frame(width: 130)
                    }
                    .buttonStyle(.plain)
                }
            }
            .padding(.horizontal, 20)
        }
    }
}

struct PlaylistRail: View {
    let playlists: [Playlist]
    var body: some View {
        ScrollView(.horizontal, showsIndicators: false) {
            HStack(alignment: .top, spacing: 16) {
                ForEach(playlists) { playlist in
                    NavigationLink { PlaylistDetailView(playlist: playlist) } label: {
                        VStack(alignment: .leading, spacing: 6) {
                            RemoteArtwork(url: playlist.artworkUrl, cornerRadius: 10)
                                .frame(width: 130, height: 130)
                            Text(playlist.name).font(.subheadline.weight(.semibold)).lineLimit(1).foregroundStyle(.primary)
                            Text("\(playlist.trackCount) songs").font(.caption).foregroundStyle(.secondary)
                        }
                        .frame(width: 130)
                    }
                    .buttonStyle(.plain)
                }
            }
            .padding(.horizontal, 20)
        }
    }
}
