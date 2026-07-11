import SwiftUI

/// Apple-Music-style shell: Home / Search / Library tabs with the now-playing
/// mini player docked above the tab bar. On iOS 26 it's the system
/// `tabViewBottomAccessory` (native Liquid-Glass dock that sits *above* the tab
/// bar and morphs with it); on 17-25 it's a floating glass pill via
/// `safeAreaInset`. Either way, tapping it opens the full Now Playing sheet.
struct MainTabView: View {
    @EnvironmentObject private var player: PlayerController
    @EnvironmentObject private var updates: UpdateStore
    @EnvironmentObject private var downloads: DownloadStore

    private enum Tab { case home, search, library }
    @State private var selectedTab: Tab = .home
    @State private var showNowPlaying = false

    var body: some View {
        content
            .animation(.spring(response: 0.4, dampingFraction: 0.85), value: player.hasNowPlaying)
            .animation(.spring(response: 0.4, dampingFraction: 0.85), value: updates.hasUpdate)
            .overlay(alignment: .bottom) { downloadToast }
            .animation(.spring(response: 0.4, dampingFraction: 0.85), value: downloads.status)
            .sheet(isPresented: $showNowPlaying) {
                NowPlayingView()
                    .presentationDragIndicator(.visible)
            }
            .task { updates.checkOnce() }
    }

    /// Transient download-status note (saved path / error / in-flight), docked
    /// low so it clears the tab bar and mini player. Tap to dismiss.
    @ViewBuilder private var downloadToast: some View {
        if let status = downloads.status {
            Text(status)
                .font(.footnote)
                .lineLimit(2)
                .multilineTextAlignment(.center)
                .padding(.horizontal, 16)
                .padding(.vertical, 10)
                .frame(maxWidth: 360)
                .glassPill()
                .padding(.horizontal, 24)
                .padding(.bottom, player.hasNowPlaying ? Palette.miniPlayerHeight + 90 : 90)
                .onTapGesture { downloads.dismiss() }
                .transition(.move(edge: .bottom).combined(with: .opacity))
        }
    }

    @ViewBuilder private var content: some View {
        if #available(iOS 26.0, *) {
            tabs
                .tabViewBottomAccessory {
                    if player.hasNowPlaying {
                        MiniPlayerView(accessory: true) { showNowPlaying = true }
                    }
                }
        } else {
            tabs
                .safeAreaInset(edge: .bottom, spacing: 0) {
                    if player.hasNowPlaying {
                        MiniPlayerView { showNowPlaying = true }
                            .padding(.horizontal, 8)
                            .padding(.bottom, 6)
                            .transition(.move(edge: .bottom).combined(with: .opacity))
                    }
                }
        }
    }

    private var tabs: some View {
        TabView(selection: $selectedTab) {
            NavigationStack { HomeView() }
                .tabItem { Label("Home", systemImage: "house.fill") }
                .tag(Tab.home)

            NavigationStack { SearchView() }
                .tabItem { Label("Search", systemImage: "magnifyingglass") }
                .tag(Tab.search)

            NavigationStack { LibraryView() }
                .tabItem { Label("Library", systemImage: "music.note.list") }
                .tag(Tab.library)
        }
        .safeAreaInset(edge: .top, spacing: 0) {
            UpdateBanner()
        }
    }
}
