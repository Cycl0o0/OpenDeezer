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

    /// `.playback` + active session so audio keeps going in the background /
    /// with the screen locked, and so the Control Center / lock screen
    /// transport (wired via MPRemoteCommandCenter in PlayerController) works.
    /// Paired with `UIBackgroundModes: [audio]` in Info.plist.
    private func configureAudioSession() {
        let session = AVAudioSession.sharedInstance()
        do {
            try session.setCategory(.playback, mode: .default, options: [])
            try session.setActive(true, options: [])
        } catch {
            print("AVAudioSession setup failed: \(error)")
        }
    }
}
