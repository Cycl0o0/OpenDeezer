import SwiftUI

/// Top-level state machine: launch spinner -> login -> app.
struct RootView: View {
    @StateObject private var session = SessionStore.shared
    @StateObject private var player = PlayerController.shared
    @StateObject private var library = LibraryStore.shared
    @StateObject private var updates = UpdateStore.shared
    @StateObject private var downloads = DownloadStore.shared

    var body: some View {
        Group {
            switch session.phase {
            case .launching:
                LaunchView()
                    .task { await session.bootstrap() }
            case .loggedOut:
                LoginView()
            case .noInternet:
                NoInternetView()
            case .ready:
                MainTabView()
            }
        }
        .environmentObject(session)
        .environmentObject(player)
        .environmentObject(library)
        .environmentObject(updates)
        .environmentObject(downloads)
        .tint(Palette.accent)
        .animation(.easeInOut, value: session.phase)
    }
}

private struct LaunchView: View {
    var body: some View {
        ZStack {
            Color.black.ignoresSafeArea()
            VStack(spacing: 16) {
                Image(systemName: "music.note")
                    .font(.system(size: 48, weight: .semibold))
                    .foregroundStyle(Palette.accent)
                ProgressView()
                    .tint(.white)
            }
        }
    }
}
