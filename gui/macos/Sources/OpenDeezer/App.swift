import SwiftUI

@main
struct OpenDeezerApp: App {
    @StateObject private var app = AppState()

    var body: some Scene {
        WindowGroup("OpenDeezer") {
            RootView()
                .environmentObject(app)
                .frame(minWidth: 940, minHeight: 600)
                .tint(DZ.accent)
                .preferredColorScheme(.dark)
                .onAppear { app.start() }
        }
        .windowStyle(.titleBar)
        .windowToolbarStyle(.unified)
        .commands {
            // Replace the default "About OpenDeezer" panel with our credits.
            CommandGroup(replacing: .appInfo) {
                Button("About OpenDeezer") { app.showCredits = true }
            }
            // Standard ⌘, opens our Settings sheet.
            CommandGroup(replacing: .appSettings) {
                Button("Settings…") { app.showSettings = true }
                    .keyboardShortcut(",", modifiers: .command)
            }
        }
    }
}

struct RootView: View {
    @EnvironmentObject var app: AppState

    var body: some View {
        Group {
            if app.loggedIn {
                NavigationSplitView {
                    Sidebar()
                } detail: {
                    DetailView()
                }
            } else {
                LoginGate()
            }
        }
        .background(DZ.windowBG)
        .sheet(isPresented: $app.showLoginWeb) { DeezerLoginSheet() }
        .sheet(isPresented: $app.showCredits) { CreditsView() }
        .sheet(isPresented: $app.showSettings) { SettingsView() }
        .sheet(isPresented: $app.showLyrics) { LyricsView() }
        .sheet(isPresented: $app.showArtist) { ArtistView() }
        .sheet(isPresented: $app.showAddToPlaylist) { AddToPlaylistSheet() }
    }
}

struct LoginGate: View {
    @EnvironmentObject var app: AppState
    @State private var showManual = false

    var body: some View {
        VStack(spacing: 16) {
            Image(systemName: "heart.fill")
                .font(.system(size: 56)).foregroundStyle(DZ.accent)
            Text("OpenDeezer").font(.system(size: 34, weight: .bold)).foregroundStyle(DZ.textPri)

            if app.busy {
                ProgressView("Logging in…").tint(DZ.accent)
            } else {
                // Primary: open the embedded Deezer login webview. The arl cookie
                // is captured automatically once the user signs in.
                Button { app.beginWebLogin() } label: {
                    Label("Log in with Deezer", systemImage: "person.crop.circle")
                        .frame(maxWidth: 260)
                }
                .buttonStyle(.glassProminent).tint(DZ.accent).controlSize(.large)

                // Fallback: paste an ARL by hand.
                Button(showManual ? "Hide manual ARL" : "Enter ARL manually") {
                    withAnimation { showManual.toggle() }
                }
                .buttonStyle(.plain).font(.callout).foregroundStyle(DZ.textSec)

                if showManual {
                    VStack(spacing: 8) {
                        SecureField("Paste your ARL cookie", text: $app.manualARL)
                            .textFieldStyle(.roundedBorder).frame(maxWidth: 320)
                            .onSubmit { app.loginWithManualARL() }
                        Button("Sign in") { app.loginWithManualARL() }
                            .buttonStyle(.glass).tint(DZ.accent)
                            .disabled(app.manualARL.trimmingCharacters(in: .whitespaces).isEmpty)
                    }
                    .transition(.opacity)
                }

                if let e = app.loginError {
                    Text(e).foregroundStyle(.red)
                        .multilineTextAlignment(.center).frame(maxWidth: 400)
                }
            }
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .background(DZ.windowBG)
    }
}

// MARK: - Sidebar

struct Sidebar: View {
    @EnvironmentObject var app: AppState

    var body: some View {
        VStack(spacing: 0) {
            List(selection: Binding(
                get: { app.section },
                set: { sel in
                    app.section = sel
                    switch sel {
                    case .liked: app.loadFavorites()
                    case .playlists: app.loadPlaylists()
                    case .charts: app.loadCharts()
                    case .flow: app.loadFlow()
                    case .search, .podcasts: break
                    }
                })) {
                SidebarLabel("Search", "magnifyingglass", .search)

                SwiftUI.Section {
                    SidebarLabel("Flow", "infinity", .flow)
                    SidebarLabel("Liked Songs", "heart.fill", .liked)
                    SidebarLabel("Playlists", "music.note.list", .playlists)
                    SidebarLabel("Charts", "chart.bar.fill", .charts)
                    SidebarLabel("Podcasts", "mic.fill", .podcasts)
                } header: {
                    Text("Library")
                        .font(.system(size: 11, weight: .bold)).textCase(.uppercase)
                        .foregroundStyle(DZ.textSec)
                }
            }
            .listStyle(.sidebar)
            .scrollContentBackground(.hidden)
            .background(DZ.sidebarBG)

            AccountRow(userID: app.userID)
        }
        .background(DZ.sidebarBG)
        .navigationSplitViewColumnWidth(min: 210, ideal: 234, max: 280)
    }
}

struct SidebarLabel: View {
    let title: String
    let symbol: String
    let tag: Section
    init(_ t: String, _ s: String, _ tag: Section) { title = t; symbol = s; self.tag = tag }

    var body: some View {
        Label {
            Text(title).font(.system(size: 13)).foregroundStyle(DZ.textPri)
        } icon: {
            Image(systemName: symbol).foregroundStyle(DZ.accent)
        }
        .tag(tag)
    }
}

struct AccountRow: View {
    @EnvironmentObject var app: AppState
    let userID: String

    private var accountName: String {
        if let a = app.account, !a.name.isEmpty { return a.name }
        return "OpenDeezer"
    }
    private var accountSubtitle: String {
        if let a = app.account, !a.offer.isEmpty { return a.offer }
        return userID.isEmpty ? "—" : "user \(userID)"
    }

    var body: some View {
        HStack(spacing: 10) {
            Image(systemName: "person.crop.circle.fill")
                .font(.system(size: 26)).foregroundStyle(DZ.accent)
            VStack(alignment: .leading, spacing: 1) {
                Text(accountName).font(.system(size: 13, weight: .medium))
                    .foregroundStyle(DZ.textPri).lineLimit(1)
                Text(accountSubtitle)
                    .font(.system(size: 11)).foregroundStyle(DZ.textSec).lineLimit(1)
            }
            Spacer()
            Button { app.showSettings = true } label: {
                Image(systemName: "gearshape").foregroundStyle(DZ.textSec)
            }
            .buttonStyle(.plain).help("Settings")
            Button { app.showCredits = true } label: {
                Image(systemName: "info.circle").foregroundStyle(DZ.textSec)
            }
            .buttonStyle(.plain).help("About OpenDeezer")
        }
        .padding(.horizontal, 14).padding(.vertical, 10)
        .overlay(Divider().overlay(DZ.hairline), alignment: .top)
    }
}

// CreditsView — the About sheet, crediting the author like the TUI.
struct CreditsView: View {
    @EnvironmentObject var app: AppState

    private var version: String {
        (Bundle.main.infoDictionary?["CFBundleShortVersionString"] as? String) ?? "0.4.1"
    }

    var body: some View {
        VStack(spacing: 14) {
            Image(systemName: "heart.fill")
                .font(.system(size: 52)).foregroundStyle(DZ.accent)
            Text("OpenDeezer").font(.system(size: 26, weight: .bold)).foregroundStyle(DZ.textPri)
            Text("v\(version) · An open source reimplementation of Deezer")
                .font(.callout).foregroundStyle(DZ.textSec)
            Text("by Cycl0o0").font(.body).foregroundStyle(DZ.accent)

            if app.loggedIn {
                Text("Signed in as \(app.accountLabel)")
                    .font(.caption).foregroundStyle(DZ.textSec)
            }

            Divider().frame(width: 240)

            VStack(spacing: 4) {
                Text("Built with SwiftUI + a Go engine").foregroundStyle(DZ.textPri)
                Text("go-mp3 + oto · x/crypto/blowfish").foregroundStyle(DZ.textSec)
                Text("Audio decrypted + decoded locally — your ARL never leaves your machine.")
                    .foregroundStyle(DZ.textSec).multilineTextAlignment(.center)
                Text("AGPL-3.0 · Not affiliated with Deezer.").foregroundStyle(DZ.textSec)
            }
            .font(.caption).frame(maxWidth: 320)

            Button("Done") { app.showCredits = false }
                .buttonStyle(.glassProminent).tint(DZ.accent).controlSize(.large)
                .padding(.top, 4)
        }
        .padding(28)
        .frame(width: 420)
        .background(DZ.windowBG)
    }
}

// MARK: - Detail routing

struct DetailView: View {
    @EnvironmentObject var app: AppState
    var body: some View {
        Group {
            switch app.section {
            case .liked, .flow:
                TrackListScreen()
            case .charts:
                ChartsView()
            case .playlists:
                if app.tracks.isEmpty {
                    PlaylistGrid(playlists: app.playlists) { app.openPlaylist($0) }
                } else {
                    TrackListScreen()
                }
            case .search:
                SearchView()
            case .podcasts:
                PodcastsView()
            }
        }
        .background(DZ.windowBG)
        .overlay { if app.busy { ProgressView().controlSize(.large).tint(DZ.accent) } }
        // Floating player bar, confined to the detail column (not the sidebar).
        .overlay(alignment: .bottom) {
            PlayerBar().padding(.horizontal, 12).padding(.bottom, 12)
        }
    }
}

// MARK: - Hero + track list

struct TrackListScreen: View {
    @EnvironmentObject var app: AppState

    var body: some View {
        ScrollView {
            VStack(spacing: 0) {
                HeroHeader()
                TrackTable(tracks: app.tracks)
                    .padding(.bottom, 96) // clear the floating player bar
            }
        }
        .scrollContentBackground(.hidden)
        .background(DZ.windowBG)
    }
}

struct HeroHeader: View {
    @EnvironmentObject var app: AppState

    private var subtitle: String {
        app.listIsLiked ? "\(app.tracks.count) songs" : app.listSubtitle
    }

    var body: some View {
        ZStack(alignment: .bottomLeading) {
            // Ambient backdrop: blurred artwork (playlist/album) or brand gradient (liked).
            Group {
                if app.listIsLiked || app.listArtwork.isEmpty {
                    LinearGradient(colors: [DZ.accentMag, DZ.accent, DZ.windowBG],
                                   startPoint: .topLeading, endPoint: .bottomTrailing)
                } else {
                    AsyncImage(url: URL(string: app.listArtwork)) { img in
                        img.resizable().scaledToFill()
                    } placeholder: { DZ.accent }
                    .blur(radius: 60)
                    .overlay(LinearGradient(
                        colors: [DZ.accentMag.opacity(0.30), .clear, DZ.windowBG],
                        startPoint: .top, endPoint: .bottom))
                }
            }
            .frame(height: 280).clipped()
            // Let the ambient artwork bleed up under the Liquid Glass toolbar.
            .backgroundExtensionEffect()

            HStack(alignment: .bottom, spacing: 22) {
                heroArt
                VStack(alignment: .leading, spacing: 8) {
                    Text(app.listTitle)
                        .font(.system(size: 34, weight: .bold)).foregroundStyle(DZ.textPri)
                        .lineLimit(2)
                    Text(subtitle)
                        .font(.title3).foregroundStyle(DZ.textPri.opacity(0.9))
                    HStack(spacing: 12) {
                        Button { app.playAll() } label: {
                            Label("Play", systemImage: "play.fill")
                        }
                        .buttonStyle(.glassProminent).tint(DZ.accent)
                        Button { app.shuffleAll() } label: {
                            Label("Shuffle", systemImage: "shuffle")
                        }
                        .buttonStyle(.glass)
                    }
                    .controlSize(.large)
                    .padding(.top, 4)
                }
                Spacer()
            }
            .padding(24)
        }
    }

    @ViewBuilder private var heroArt: some View {
        if app.listIsLiked || app.listArtwork.isEmpty {
            RoundedRectangle(cornerRadius: 10)
                .fill(LinearGradient(colors: [DZ.accent, DZ.accentMag],
                                     startPoint: .top, endPoint: .bottom))
                .frame(width: 168, height: 168)
                .overlay(Image(systemName: app.listHeroSymbol).font(.system(size: 56)).foregroundStyle(.white))
                .shadow(radius: 18, y: 8)
        } else {
            Artwork(url: app.listArtwork, size: 168, radius: 10)
                .shadow(radius: 18, y: 8)
        }
    }
}

struct TrackTable: View {
    @EnvironmentObject var app: AppState
    let tracks: [Track]

    var body: some View {
        LazyVStack(spacing: 0) {
            // column header
            HStack(spacing: 12) {
                Text("#").frame(width: 28, alignment: .center)
                Text("Title").frame(maxWidth: .infinity, alignment: .leading)
                Text("Album").frame(maxWidth: .infinity, alignment: .leading)
                Text("Time").frame(width: 56, alignment: .trailing)
            }
            .font(.system(size: 11, weight: .semibold)).textCase(.uppercase)
            .foregroundStyle(DZ.textSec)
            .padding(.horizontal, 24).padding(.vertical, 6)
            Divider().overlay(DZ.hairline)

            ForEach(Array(tracks.enumerated()), id: \.element.id) { idx, t in
                TrackRowView(index: idx, track: t,
                             isCurrent: app.current?.id == t.id) {
                    app.play(t, in: tracks)
                }
                Divider().overlay(DZ.hairline).padding(.leading, 24)
            }
        }
    }
}

struct TrackRowView: View {
    @EnvironmentObject var app: AppState
    let index: Int
    let track: Track
    let isCurrent: Bool
    let onPlay: () -> Void
    @State private var hover = false

    var body: some View {
        HStack(spacing: 12) {
            ZStack {
                if isCurrent {
                    Image(systemName: "waveform").foregroundStyle(DZ.accent)
                } else if hover {
                    Image(systemName: "play.fill").foregroundStyle(DZ.textPri)
                } else {
                    Text("\(index + 1)").foregroundStyle(DZ.textSec).monospacedDigit()
                }
            }
            .frame(width: 28)

            Artwork(url: track.artworkUrl, size: 36, radius: 4)
            VStack(alignment: .leading, spacing: 1) {
                Text(track.name).lineLimit(1)
                    .foregroundStyle(isCurrent ? DZ.accent : DZ.textPri)
                    .fontWeight(isCurrent ? .semibold : .regular)
                Text(track.artistLine).font(.caption).foregroundStyle(DZ.textSec).lineLimit(1)
            }
            .frame(maxWidth: .infinity, alignment: .leading)

            Text(track.albumName).foregroundStyle(DZ.textSec).lineLimit(1)
                .frame(maxWidth: .infinity, alignment: .leading)
            Text(track.durationText).foregroundStyle(DZ.textSec).monospacedDigit()
                .frame(width: 56, alignment: .trailing)
        }
        .font(.system(size: 13))
        .padding(.horizontal, 24).padding(.vertical, 7)
        .background(isCurrent ? DZ.nowTint : (hover ? Color.white.opacity(0.05) : .clear))
        .contentShape(Rectangle())
        .onTapGesture(perform: onPlay)
        .onHover { h in withAnimation(.easeOut(duration: 0.12)) { hover = h } }
        .contextMenu {
            Button { onPlay() } label: { Label("Play", systemImage: "play.fill") }
            Button { app.toggleLike(track) } label: {
                Label(app.isLiked(track) ? "Unlike" : "Like",
                      systemImage: app.isLiked(track) ? "heart.fill" : "heart")
            }
            Button { app.beginAddToPlaylist(track) } label: {
                Label("Add to Playlist…", systemImage: "text.badge.plus")
            }
            if let aid = track.artists.first?.id {
                Button { app.openArtist(aid) } label: {
                    Label("Go to Artist", systemImage: "music.mic")
                }
            }
        }
    }
}

// MARK: - Library grid

struct PlaylistGrid: View {
    @EnvironmentObject var app: AppState
    let playlists: [Playlist]
    let onOpen: (Playlist) -> Void
    private let cols = [GridItem(.adaptive(minimum: 170, maximum: 200), spacing: 20)]

    // Text buffers for the create / rename prompts.
    @State private var createText = ""
    @State private var renameText = ""

    var body: some View {
        ScrollView {
            HStack {
                Text("Playlists").font(.system(size: 26, weight: .bold))
                    .foregroundStyle(DZ.textPri)
                Spacer()
                Button { createText = ""; app.showCreatePlaylist = true } label: {
                    Label("New Playlist", systemImage: "plus")
                }
                .buttonStyle(.glass).tint(DZ.accent)
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            .padding(.horizontal, 24).padding(.top, 20)

            LazyVGrid(columns: cols, spacing: 24) {
                ForEach(playlists) { p in
                    PlaylistCard(playlist: p) { onOpen(p) }
                }
            }
            .padding(.horizontal, 24).padding(.top, 8).padding(.bottom, 96)
        }
        .scrollContentBackground(.hidden)
        .background(DZ.windowBG)
        // Create
        .alert("New Playlist", isPresented: $app.showCreatePlaylist) {
            TextField("Playlist name", text: $createText)
            Button("Create") { app.createPlaylist(title: createText) }
                .disabled(createText.trimmingCharacters(in: .whitespaces).isEmpty)
            Button("Cancel", role: .cancel) {}
        } message: { Text("Name your new playlist.") }
        // Rename
        .alert("Rename Playlist",
               isPresented: Binding(get: { app.renameTarget != nil },
                                    set: { if !$0 { app.renameTarget = nil } })) {
            TextField("Playlist name", text: $renameText)
            Button("Save") {
                if let p = app.renameTarget { app.renamePlaylist(p, to: renameText) }
                app.renameTarget = nil
            }
            .disabled(renameText.trimmingCharacters(in: .whitespaces).isEmpty)
            Button("Cancel", role: .cancel) { app.renameTarget = nil }
        } message: { Text("Enter a new name.") }
        .onChange(of: app.renameTarget) { _, p in renameText = p?.name ?? "" }
        // Delete (confirm)
        .confirmationDialog(
            "Delete \u{201C}\(app.deleteTarget?.name ?? "")\u{201D}?",
            isPresented: Binding(get: { app.deleteTarget != nil },
                                 set: { if !$0 { app.deleteTarget = nil } }),
            titleVisibility: .visible) {
            Button("Delete", role: .destructive) {
                if let p = app.deleteTarget { app.deletePlaylist(p) }
                app.deleteTarget = nil
            }
            Button("Cancel", role: .cancel) { app.deleteTarget = nil }
        } message: { Text("This can't be undone.") }
    }
}

struct PlaylistCard: View {
    @EnvironmentObject var app: AppState
    let playlist: Playlist
    let onOpen: () -> Void
    @State private var hover = false

    var body: some View {
        Button(action: onOpen) {
            VStack(alignment: .leading, spacing: 8) {
                ZStack(alignment: .bottomTrailing) {
                    Artwork(url: playlist.artworkUrl, size: 170, radius: 8)
                        .shadow(radius: 6, y: 4)
                    if hover {
                        Circle().fill(DZ.accent).frame(width: 40, height: 40)
                            .overlay(Image(systemName: "play.fill").foregroundStyle(.white))
                            .shadow(radius: 4).padding(10)
                            .transition(.scale.combined(with: .opacity))
                    }
                }
                Text(playlist.name).font(.system(size: 13, weight: .medium))
                    .foregroundStyle(DZ.textPri).lineLimit(1)
                Text("\(playlist.trackCount) tracks").font(.caption).foregroundStyle(DZ.textSec)
            }
        }
        .buttonStyle(.plain)
        .scaleEffect(hover ? 1.03 : 1)
        .onHover { h in withAnimation(.easeOut(duration: 0.15)) { hover = h } }
        .contextMenu {
            Button { onOpen() } label: { Label("Open", systemImage: "play.fill") }
            Button { app.beginRename(playlist) } label: { Label("Rename…", systemImage: "pencil") }
            Button(role: .destructive) { app.deleteTarget = playlist } label: {
                Label("Delete…", systemImage: "trash")
            }
        }
    }
}

// MARK: - Search

struct SearchView: View {
    @EnvironmentObject var app: AppState
    var body: some View {
        VStack(spacing: 0) {
            HStack {
                Image(systemName: "magnifyingglass").foregroundStyle(DZ.textSec)
                TextField("Search tracks, albums, playlists", text: $app.query)
                    .textFieldStyle(.plain).foregroundStyle(DZ.textPri)
                    .onSubmit { app.runSearch() }
            }
            .padding(12)
            .glassEffect(.regular, in: RoundedRectangle(cornerRadius: 12))
            .padding(.horizontal, 24).padding(.top, 18).padding(.bottom, 10)

            List {
                if !app.searchTracks.isEmpty {
                    searchSection("Tracks") {
                        ForEach(Array(app.searchTracks.enumerated()), id: \.element.id) { i, t in
                            TrackRowView(index: i, track: t,
                                         isCurrent: app.current?.id == t.id) {
                                app.play(t, in: app.searchTracks)
                            }
                            .listRowInsets(EdgeInsets())
                            .listRowBackground(Color.clear)
                        }
                    }
                }
                if !app.searchArtists.isEmpty {
                    searchSection("Artists") {
                        ForEach(app.searchArtists) { ar in
                            CompactRow(url: ar.artworkUrl, title: ar.name,
                                       sub: ar.nbFans > 0 ? "\(ar.nbFans.formatted()) fans" : "Artist") {
                                app.openArtist(ar.id)
                            }
                        }
                    }
                }
                if !app.searchAlbums.isEmpty {
                    searchSection("Albums") {
                        ForEach(app.searchAlbums) { a in
                            CompactRow(url: a.artworkUrl, title: a.name, sub: a.artistLine) {
                                app.openAlbum(a)
                            }
                        }
                    }
                }
                if !app.searchPlaylists.isEmpty {
                    searchSection("Playlists") {
                        ForEach(app.searchPlaylists) { p in
                            CompactRow(url: p.artworkUrl, title: p.name, sub: "\(p.trackCount) tracks") {
                                app.openPlaylist(p)
                            }
                        }
                    }
                }
            }
            .listStyle(.inset)
            .scrollContentBackground(.hidden)
        }
        .background(DZ.windowBG)
    }

    private func searchSection<C: View>(_ title: String, @ViewBuilder _ content: () -> C) -> some View {
        SwiftUI.Section {
            content()
        } header: {
            Text(title).font(.system(size: 12, weight: .bold)).foregroundStyle(DZ.textSec)
        }
    }
}

struct CompactRow: View {
    let url: String
    let title: String
    let sub: String
    let onTap: () -> Void
    var body: some View {
        Button(action: onTap) {
            HStack(spacing: 10) {
                Artwork(url: url, size: 36, radius: 4)
                VStack(alignment: .leading, spacing: 1) {
                    Text(title).foregroundStyle(DZ.textPri).lineLimit(1)
                    Text(sub).font(.caption).foregroundStyle(DZ.textSec).lineLimit(1)
                }
                Spacer()
            }
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .listRowBackground(Color.clear)
    }
}

// MARK: - Artwork

struct Artwork: View {
    let url: String
    let size: CGFloat
    var radius: CGFloat = 4
    var body: some View {
        AsyncImage(url: URL(string: url)) { phase in
            switch phase {
            case .success(let img): img.resizable().scaledToFill()
            default:
                Rectangle().fill(DZ.panelBG)
                    .overlay(Image(systemName: "music.note").foregroundStyle(DZ.textSec))
            }
        }
        .frame(width: size, height: size)
        .clipShape(RoundedRectangle(cornerRadius: radius))
    }
}
