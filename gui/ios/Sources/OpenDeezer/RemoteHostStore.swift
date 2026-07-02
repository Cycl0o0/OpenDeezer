import Foundation

/// Persists and applies the two "make this device reachable" toggles that
/// normally require editing a config file on desktop:
///   • OpenDeezer Connect host — advertise this device so other same-account
///     OpenDeezer apps can discover and control it.
///   • Phone remote — the browser-based remote (QR + pairing code).
/// Both are re-applied after login so they survive app relaunches.
@MainActor
final class RemoteHostStore: ObservableObject {
    static let shared = RemoteHostStore()

    private let defaults = UserDefaults.standard
    private let connectKey = "connectHostEnabled"
    private let remoteKey = "phoneRemoteEnabled"

    @Published var connectHostEnabled: Bool {
        didSet {
            defaults.set(connectHostEnabled, forKey: connectKey)
            Engine.connectHostSetEnabled(connectHostEnabled)
        }
    }
    @Published var phoneRemoteEnabled: Bool {
        didSet {
            defaults.set(phoneRemoteEnabled, forKey: remoteKey)
            Engine.webRemoteSetEnabled(phoneRemoteEnabled)
        }
    }

    private init() {
        connectHostEnabled = defaults.bool(forKey: connectKey)
        phoneRemoteEnabled = defaults.bool(forKey: remoteKey)
    }

    /// Re-enable whichever hosts were on last run. Called once the engine is
    /// logged in (the control server needs the account for same-account auth).
    func applyOnLaunch() {
        if connectHostEnabled { Engine.connectHostSetEnabled(true) }
        if phoneRemoteEnabled { Engine.webRemoteSetEnabled(true) }
    }

    /// Tear down both hosts on logout so the logged-out account can't keep
    /// being controlled/streamed over the network. didSet fires on every
    /// assignment (even false -> false), so this always stops the engine-side
    /// servers.
    func disableAll() {
        connectHostEnabled = false
        phoneRemoteEnabled = false
    }
}
