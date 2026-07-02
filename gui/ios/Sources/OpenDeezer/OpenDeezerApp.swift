import SwiftUI
import AVFoundation

@main
struct OpenDeezerApp: App {
    init() {
        configureAudioSession()
    }

    var body: some Scene {
        WindowGroup {
            RootView()
        }
    }

    /// `.playback` category so audio keeps going in the background / with the
    /// screen locked, and so the Control Center / lock screen transport (wired
    /// via MPRemoteCommandCenter in PlayerController) works. Paired with
    /// `UIBackgroundModes: [audio]` in Info.plist. The session is only
    /// *activated* when playback actually starts (PlayerController) — a
    /// non-mixable session activated here would pause other apps' audio the
    /// moment the app launches.
    private func configureAudioSession() {
        let session = AVAudioSession.sharedInstance()
        do {
            try session.setCategory(.playback, mode: .default, options: [])
        } catch {
            print("AVAudioSession setup failed: \(error)")
        }
    }
}
