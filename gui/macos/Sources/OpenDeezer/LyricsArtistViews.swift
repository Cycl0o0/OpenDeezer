import SwiftUI

// MARK: - Lyrics

// LyricsView — a Liquid Glass sheet showing the now-playing track's lyrics.
// Synced lyrics highlight the active line and auto-scroll, driven by the same
// periodic UI tick that advances the progress bar (AppState.positionMs).
struct LyricsView: View {
    @EnvironmentObject var app: AppState

    private var synced: [LyricLine] { app.currentLyrics?.synced ?? [] }
    private var isSynced: Bool { (app.currentLyrics?.isSynced ?? false) && !synced.isEmpty }
    private var plain: String { app.currentLyrics?.plain ?? "" }

    // The active line is the last one whose timestamp has been reached.
    private var activeIndex: Int? {
        guard isSynced else { return nil }
        let pos = app.positionMs
        var idx: Int?
        for (i, line) in synced.enumerated() {
            if line.timeMs <= pos { idx = i } else { break }
        }
        return idx
    }

    var body: some View {
        VStack(spacing: 0) {
            header
            Divider().overlay(DZ.hairline)
            content
        }
        .frame(width: 460, height: 600)
        .background(DZ.windowBG)
        .onAppear { app.loadLyricsIfNeeded() }
        // Refetch when the track changes while the sheet is open.
        .onChange(of: app.current?.id) { _, _ in app.loadLyricsIfNeeded() }
    }

    private var header: some View {
        HStack(spacing: 12) {
            Artwork(url: app.current?.artworkUrl ?? "", size: 44, radius: 6)
            VStack(alignment: .leading, spacing: 2) {
                HStack(spacing: 5) {
                    if app.current?.explicit == true { ExplicitBadge() }
                    Text(app.current?.name ?? "Lyrics")
                        .font(.system(size: 16, weight: .bold)).foregroundStyle(DZ.textPri)
                        .lineLimit(1)
                }
                Text(app.current?.artistLine ?? "")
                    .font(.system(size: 12)).foregroundStyle(DZ.textSec).lineLimit(1)
            }
            Spacer()
            Button("Done") { app.showLyrics = false }
                .buttonStyle(.glass).tint(DZ.accent)
        }
        .padding(16)
    }

    @ViewBuilder private var content: some View {
        if app.lyricsLoading {
            centered { ProgressView().controlSize(.large).tint(DZ.accent) }
        } else if isSynced {
            syncedBody
        } else if !plain.isEmpty {
            ScrollView {
                Text(plain)
                    .font(.system(size: 15)).foregroundStyle(DZ.textPri)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .textSelection(.enabled)
                    .padding(24)
            }
            .scrollContentBackground(.hidden)
        } else {
            centered {
                VStack(spacing: 8) {
                    Image(systemName: "quote.bubble")
                        .font(.system(size: 32)).foregroundStyle(DZ.textSec)
                    Text("No lyrics available")
                        .font(.system(size: 14)).foregroundStyle(DZ.textSec)
                }
            }
        }
    }

    private var syncedBody: some View {
        ScrollViewReader { proxy in
            ScrollView {
                LazyVStack(alignment: .leading, spacing: 14) {
                    ForEach(Array(synced.enumerated()), id: \.offset) { i, line in
                        Text(line.text.isEmpty ? "♪" : line.text)
                            .font(.system(size: i == activeIndex ? 19 : 16,
                                          weight: i == activeIndex ? .bold : .regular))
                            .foregroundStyle(i == activeIndex ? DZ.textPri : DZ.textSec)
                            .frame(maxWidth: .infinity, alignment: .leading)
                            .contentShape(Rectangle())
                            .onTapGesture { app.seek(toMs: line.timeMs) } // click to seek
                            .id(i)
                    }
                }
                .padding(.horizontal, 24).padding(.vertical, 28)
            }
            .scrollContentBackground(.hidden)
            .animation(.easeOut(duration: 0.2), value: activeIndex)
            .onChange(of: activeIndex) { _, idx in
                guard let idx else { return }
                withAnimation(.easeOut(duration: 0.35)) {
                    proxy.scrollTo(idx, anchor: .center)
                }
            }
        }
    }

    @ViewBuilder private func centered<C: View>(@ViewBuilder _ c: () -> C) -> some View {
        VStack { Spacer(); c(); Spacer() }
            .frame(maxWidth: .infinity, maxHeight: .infinity)
    }
}

// MARK: - Artist

// ArtistView — a Liquid Glass sheet for an artist's profile (DZArtistProfileJSON):
// top tracks (playable), albums (open the album), related artists (open them).
struct ArtistView: View {
    @EnvironmentObject var app: AppState

    var body: some View {
        VStack(spacing: 0) {
            toolbar
            Divider().overlay(DZ.hairline)
            if app.artistLoading && app.artistProfile == nil {
                VStack { Spacer()
                    ProgressView().controlSize(.large).tint(DZ.accent)
                    Spacer() }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else if let p = app.artistProfile {
                profileBody(p)
            } else {
                VStack { Spacer()
                    Text("Couldn't load this artist.")
                        .font(.system(size: 14)).foregroundStyle(DZ.textSec)
                    Spacer() }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
            }
        }
        .frame(width: 720, height: 640)
        .background(DZ.windowBG)
    }

    private var toolbar: some View {
        HStack {
            Spacer()
            Button("Done") { app.showArtist = false }
                .buttonStyle(.glass).tint(DZ.accent)
        }
        .padding(.horizontal, 16).padding(.vertical, 12)
    }

    private func profileBody(_ p: ArtistProfile) -> some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 26) {
                artistHeader(p.artist)

                if !p.top.isEmpty {
                    sectionHeader("Top Tracks")
                    LazyVStack(spacing: 0) {
                        ForEach(Array(p.top.enumerated()), id: \.element.id) { i, t in
                            TrackRowView(index: i, track: t,
                                         isCurrent: app.current?.id == t.id) {
                                app.play(t, in: p.top)
                            }
                            Divider().overlay(DZ.hairline).padding(.leading, 24)
                        }
                    }
                }

                if !p.albums.isEmpty {
                    sectionHeader("Albums")
                    ScrollView(.horizontal, showsIndicators: false) {
                        HStack(alignment: .top, spacing: 18) {
                            ForEach(p.albums) { a in
                                AlbumCard(album: a) { app.openAlbumFromArtist(a) }
                            }
                        }
                        .padding(.horizontal, 24)
                    }
                }

                if !p.related.isEmpty {
                    sectionHeader("Related Artists")
                    ScrollView(.horizontal, showsIndicators: false) {
                        HStack(alignment: .top, spacing: 18) {
                            ForEach(p.related) { ar in
                                ArtistAvatar(artist: ar) { app.openArtist(ar.id) }
                            }
                        }
                        .padding(.horizontal, 24)
                    }
                }
            }
            .padding(.bottom, 28)
        }
        .scrollContentBackground(.hidden)
    }

    private func artistHeader(_ a: ArtistInfo) -> some View {
        HStack(alignment: .center, spacing: 20) {
            Artwork(url: a.artworkUrl, size: 132, radius: 66)   // circular
                .shadow(radius: 14, y: 6)
            VStack(alignment: .leading, spacing: 8) {
                Text("Artist").font(.system(size: 11, weight: .bold)).textCase(.uppercase)
                    .foregroundStyle(DZ.textSec)
                Text(a.name).font(.system(size: 34, weight: .bold))
                    .foregroundStyle(DZ.textPri).lineLimit(2)
                if a.nbFans > 0 {
                    Text("\(a.nbFans.formatted()) fans")
                        .font(.title3).foregroundStyle(DZ.textPri.opacity(0.9))
                }
            }
            Spacer()
        }
        .padding(.horizontal, 24).padding(.top, 8)
    }

    private func sectionHeader(_ t: String) -> some View {
        Text(t).font(.system(size: 20, weight: .bold)).foregroundStyle(DZ.textPri)
            .frame(maxWidth: .infinity, alignment: .leading)
            .padding(.horizontal, 24)
    }
}

// AlbumCard — a tappable artwork tile used in the artist sheet's Albums rail.
private struct AlbumCard: View {
    let album: Album
    let onOpen: () -> Void
    @State private var hover = false

    var body: some View {
        Button(action: onOpen) {
            VStack(alignment: .leading, spacing: 8) {
                ZStack(alignment: .bottomTrailing) {
                    Artwork(url: album.artworkUrl, size: 150, radius: 8)
                        .shadow(radius: 6, y: 4)
                    if hover {
                        Circle().fill(DZ.accent).frame(width: 36, height: 36)
                            .overlay(Image(systemName: "play.fill").foregroundStyle(.white))
                            .shadow(radius: 4).padding(8)
                            .transition(.scale.combined(with: .opacity))
                    }
                }
                Text(album.name).font(.system(size: 13, weight: .medium))
                    .foregroundStyle(DZ.textPri).lineLimit(1)
                Text(album.artistLine).font(.caption).foregroundStyle(DZ.textSec).lineLimit(1)
            }
            .frame(width: 150, alignment: .leading)
        }
        .buttonStyle(.plain)
        .scaleEffect(hover ? 1.03 : 1)
        .onHover { h in withAnimation(.easeOut(duration: 0.15)) { hover = h } }
    }
}

// ArtistAvatar — a circular, tappable related-artist tile.
private struct ArtistAvatar: View {
    let artist: ArtistInfo
    let onOpen: () -> Void
    @State private var hover = false

    var body: some View {
        Button(action: onOpen) {
            VStack(spacing: 8) {
                Artwork(url: artist.artworkUrl, size: 110, radius: 55)
                    .shadow(radius: 5, y: 3)
                Text(artist.name).font(.system(size: 12, weight: .medium))
                    .foregroundStyle(DZ.textPri).lineLimit(1)
                    .frame(width: 110)
            }
        }
        .buttonStyle(.plain)
        .scaleEffect(hover ? 1.04 : 1)
        .onHover { h in withAnimation(.easeOut(duration: 0.15)) { hover = h } }
    }
}
