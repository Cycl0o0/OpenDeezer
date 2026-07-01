import SwiftUI

/// Deezer Flow — an endless personalized mix.
struct FlowView: View {
    @EnvironmentObject private var player: PlayerController
    @State private var tracks: [Track] = []
    @State private var isLoading = true
    @State private var errorText: String?

    var body: some View {
        Group {
            if isLoading {
                ProgressView()
            } else if let error = errorText {
                ContentUnavailableMessage(systemImage: "waveform", title: "Flow unavailable", message: error)
            } else if tracks.isEmpty {
                ContentUnavailableMessage(systemImage: "waveform", title: "Flow is empty", message: "Try again later.")
            } else {
                List {
                    Section {
                        Button {
                            player.playQueue(tracks, startAt: 0)
                        } label: {
                            Label("Play Flow", systemImage: "play.fill").frame(maxWidth: .infinity)
                        }
                        .glassButton(prominent: true)
                        .tint(Palette.accent)
                        .listRowSeparator(.hidden)
                    }
                    ForEach(tracks) { track in
                        TrackRow(track: track, tracks: tracks)
                    }
                }
                .listStyle(.plain)
            }
        }
        .navigationTitle("Flow")
        .task { await load() }
        .refreshable { await load() }
    }

    private func load() async {
        isLoading = tracks.isEmpty
        do {
            tracks = try await Engine.flow()
            errorText = nil
        } catch {
            errorText = error.localizedDescription
        }
        isLoading = false
    }
}
