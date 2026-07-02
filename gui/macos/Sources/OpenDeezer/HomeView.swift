import SwiftUI

// HomeView — "Discovery Home" landing page shown after login.
// Displays a time-based greeting, four quick-pick shortcut cards, and
// horizontal rails of Top Tracks, Top Albums, and Your Playlists populated
// from DZHomeJSON. All navigation reuses the existing AppState actions.
struct HomeView: View {
    @EnvironmentObject var app: AppState

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 0) {

                // MARK: Greeting
                Text(greeting)
                    .font(.system(size: 34, weight: .bold))
                    .foregroundStyle(DZ.textPri)
                    .padding(.horizontal, 24)
                    .padding(.top, 32)
                    .padding(.bottom, 20)

                // MARK: Quick-pick row
                HStack(spacing: 14) {
                    QuickPickCard(title: L("Liked Songs"), symbol: "heart.fill") {
                        app.section = .liked
                        app.loadFavorites()
                    }
                    QuickPickCard(title: "Flow", symbol: "infinity") {
                        app.loadFlow()        // sets section = .flow internally
                    }
                    QuickPickCard(title: L("Charts"), symbol: "chart.bar.fill") {
                        app.loadCharts()      // sets section = .charts internally
                    }
                    QuickPickCard(title: L("Podcasts"), symbol: "mic.fill") {
                        app.section = .podcasts
                    }
                }
                .padding(.horizontal, 24)
                .padding(.bottom, 26)

                // MARK: Home content
                if app.homeLoading {
                    HStack {
                        Spacer()
                        ProgressView().controlSize(.large).tint(DZ.accent)
                            .padding(.top, 48)
                        Spacer()
                    }
                } else if let data = app.homeData {
                    if !data.topTracks.isEmpty {
                        railHeader(L("Top Tracks"))
                        trackRail(data.topTracks)
                    }
                    if !data.topAlbums.isEmpty {
                        railHeader(L("Top Albums"))
                        rail {
                            ForEach(data.topAlbums) { a in
                                TileCard(url: a.artworkUrl, title: a.name,
                                         sub: a.artistLine) {
                                    app.openAlbumFromChart(a)
                                }
                            }
                        }
                    }
                    if !data.playlists.isEmpty {
                        railHeader(L("Your Playlists"))
                        rail {
                            ForEach(data.playlists) { p in
                                TileCard(url: p.artworkUrl, title: p.name,
                                         sub: Lp("%d tracks", p.trackCount)) {
                                    app.openPlaylist(p)
                                }
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

    // MARK: - Private helpers

    private var greeting: String {
        let hour = Calendar.current.component(.hour, from: Date())
        switch hour {
        case 5..<12: return L("Good morning")
        case 12..<17: return L("Good afternoon")
        default: return L("Good evening")
        }
    }

    private func railHeader(_ title: String) -> some View {
        Text(title)
            .font(.system(size: 20, weight: .bold))
            .foregroundStyle(DZ.textPri)
            .frame(maxWidth: .infinity, alignment: .leading)
            .padding(.horizontal, 24).padding(.top, 22).padding(.bottom, 8)
    }

    // trackRail — horizontal scroll of HomeTrackCards.
    @ViewBuilder private func trackRail(_ tracks: [Track]) -> some View {
        ScrollView(.horizontal, showsIndicators: false) {
            HStack(alignment: .top, spacing: 16) {
                ForEach(tracks) { t in
                    HomeTrackCard(track: t, isCurrent: app.current?.id == t.id) {
                        app.play(t, in: tracks)
                    }
                }
            }
            .padding(.horizontal, 24)
        }
    }

    // rail — generic horizontal scroll for album / playlist TileCards.
    @ViewBuilder private func rail<C: View>(@ViewBuilder _ content: () -> C) -> some View {
        ScrollView(.horizontal, showsIndicators: false) {
            HStack(alignment: .top, spacing: 18) { content() }
                .padding(.horizontal, 24)
        }
    }
}

// MARK: - QuickPickCard

// QuickPickCard — a shortcut button for Liked Songs, Flow, Charts, Podcasts.
// Four of these fill the width equally below the greeting.
private struct QuickPickCard: View {
    let title: String
    let symbol: String
    let action: () -> Void
    @State private var hover = false

    var body: some View {
        Button(action: action) {
            HStack(spacing: 10) {
                Image(systemName: symbol)
                    .foregroundStyle(DZ.accent)
                    .font(.system(size: 14, weight: .semibold))
                Text(title)
                    .font(.system(size: 13, weight: .semibold))
                    .foregroundStyle(DZ.textPri)
                Spacer(minLength: 0)
            }
            .padding(.horizontal, 14).padding(.vertical, 11)
            .frame(maxWidth: .infinity)
            .background(DZ.panelBG, in: RoundedRectangle(cornerRadius: 8))
            .overlay(
                RoundedRectangle(cornerRadius: 8)
                    .strokeBorder(hover ? DZ.accent.opacity(0.45) : Color.clear,
                                  lineWidth: 1)
            )
        }
        .buttonStyle(.plain)
        .scaleEffect(hover ? 1.02 : 1)
        .onHover { h in withAnimation(.easeOut(duration: 0.12)) { hover = h } }
    }
}

// MARK: - HomeTrackCard

// HomeTrackCard — compact vertical tile (album art + title + artist) for the
// Top Tracks horizontal rail. Tapping plays via the same app.play(_:in:) path
// used by TrackRowView throughout the app. Context menu mirrors TrackRowView.
private struct HomeTrackCard: View {
    @EnvironmentObject var app: AppState
    let track: Track
    let isCurrent: Bool
    let onPlay: () -> Void
    @State private var hover = false

    var body: some View {
        Button(action: onPlay) {
            VStack(alignment: .leading, spacing: 8) {
                ZStack(alignment: .bottomTrailing) {
                    Artwork(url: track.artworkUrl, size: 130, radius: 8)
                        .shadow(radius: 6, y: 4)
                    if hover || isCurrent {
                        Circle().fill(DZ.accent).frame(width: 36, height: 36)
                            .overlay(
                                Image(systemName: isCurrent ? "waveform" : "play.fill")
                                    .foregroundStyle(.white)
                            )
                            .shadow(radius: 4).padding(8)
                            .transition(.scale.combined(with: .opacity))
                    }
                }
                VStack(alignment: .leading, spacing: 2) {
                    HStack(spacing: 4) {
                        if track.explicit { ExplicitBadge() }
                        Text(track.name)
                            .font(.system(size: 12, weight: .medium))
                            .foregroundStyle(isCurrent ? DZ.accent : DZ.textPri)
                            .lineLimit(1)
                    }
                    Text(track.artistLine)
                        .font(.caption)
                        .foregroundStyle(DZ.textSec)
                        .lineLimit(1)
                }
            }
            .frame(width: 130, alignment: .leading)
        }
        .buttonStyle(.plain)
        .scaleEffect(hover ? 1.03 : 1)
        .onHover { h in withAnimation(.easeOut(duration: 0.15)) { hover = h } }
        .contextMenu {
            Button { onPlay() } label: { Label(L("Play"), systemImage: "play.fill") }
            Button { app.toggleLike(track) } label: {
                Label(app.isLiked(track) ? L("Unlike") : L("Like"),
                      systemImage: app.isLiked(track) ? "heart.fill" : "heart")
            }
            Button { app.beginAddToPlaylist(track) } label: {
                Label(L("Add to Playlist…"), systemImage: "text.badge.plus")
            }
            if let aid = track.artists.first?.id {
                Button { app.openArtist(aid) } label: {
                    Label(L("Go to Artist"), systemImage: "music.mic")
                }
            }
        }
    }
}
