import SwiftUI

/// Apple-Music-style shell: Home / Search / Library tabs with a floating
/// Liquid-Glass mini player docked above the tab bar. On iOS 26 the system
/// tab bar itself already renders as Liquid Glass; on 17-25 it's the regular
/// translucent bar — either way the mini player sits in the same spot.
struct MainTabView: View {
    @EnvironmentObject private var player: PlayerController
    @EnvironmentObject private var updates: UpdateStore

    private enum Tab { case home, search, library }
    @State private var selectedTab: Tab = .home
    @State private var showNowPlaying = false

    var body: some View {
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
        .safeAreaInset(edge: .bottom, spacing: 0) {
            if player.hasNowPlaying {
                MiniPlayerView()
                    .padding(.horizontal, 8)
                    .padding(.bottom, 6)
                    .onTapGesture { showNowPlaying = true }
                    .transition(.move(edge: .bottom).combined(with: .opacity))
            }
        }
        .animation(.spring(response: 0.4, dampingFraction: 0.85), value: player.hasNowPlaying)
        .animation(.spring(response: 0.4, dampingFraction: 0.85), value: updates.hasUpdate)
        .sheet(isPresented: $showNowPlaying) {
            NowPlayingView()
        }
        .task { updates.checkOnce() }
    }
}
