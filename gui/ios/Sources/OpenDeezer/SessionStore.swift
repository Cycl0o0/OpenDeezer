import Foundation

/// Drives the app's top-level navigation: bootstrapping the saved session,
/// logging in, the Free-tier premium gate, and logout.
@MainActor
final class SessionStore: ObservableObject {
    static let shared = SessionStore()

    enum Phase: Equatable {
        case launching
        case loggedOut
        case noInternet
        case ready
    }

    @Published private(set) var phase: Phase = .launching
    @Published private(set) var account: Account?
    @Published var lastError: String?

    private let arlKey = "arl"

    private init() {}

    /// Called once at app launch: try the saved ARL, otherwise show login.
    func bootstrap() async {
        Engine.setClientInfo(client: "ios", device: "OpenDeezer (iOS)")
        guard let arl = KeychainStore.load(key: arlKey), !arl.isEmpty else {
            phase = .loggedOut
            return
        }
        await login(arl: arl, persist: false)
    }

    @discardableResult
    func login(arl: String, persist: Bool = true) async -> Bool {
        let trimmed = arl.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else { return false }
        lastError = nil
        let ok = await Engine.initEngine(arl: trimmed)
        guard ok else {
            // Distinguish "no internet" (kind 2) from an expired/invalid ARL: on
            // a network failure keep the saved ARL and show the retry screen
            // instead of dropping the user back to login.
            if Engine.loginErrorKind() == 2 {
                phase = .noInternet
            } else {
                phase = .loggedOut
                lastError = String(localized: "Login failed. Check your ARL and try again.")
            }
            return false
        }
        if persist { KeychainStore.save(key: arlKey, value: trimmed) }
        AudioPrefs.applyOnLaunch() // re-apply saved quality/gapless/etc. (engine keeps them in memory only)

        // Free and paid accounts both reach the full UI: the engine streams
        // full-length tracks at standard quality (128 kbps) for Deezer Free,
        // gating only downloads/HiFi. Account parsing may fail while Init
        // succeeds — don't strand the user in that case either.
        account = try? await Engine.account()
        phase = .ready

        PlayerController.shared.start()
        RemoteHostStore.shared.applyOnLaunch()
        await LibraryStore.shared.refreshAll()
        return true
    }

    /// Retries the saved-ARL login after a network failure — driven by the
    /// No-Internet screen's Retry button. `login` re-derives the phase from the
    /// result: `.ready` once back online, `.noInternet` if still offline,
    /// `.loggedOut` on a genuine auth failure.
    func retrySavedLogin() async {
        guard let arl = KeychainStore.load(key: arlKey), !arl.isEmpty else {
            phase = .loggedOut
            return
        }
        await login(arl: arl, persist: false)
    }

    func logout() {
        KeychainStore.delete(key: arlKey)
        let wasCasting = !PlayerController.shared.connectedDeviceAddr.isEmpty
        PlayerController.shared.stopPlayback()
        // Stop advertising / serving the account over the network — otherwise
        // a paired web remote or Connect peer can keep driving the logged-out
        // account while the app shows the login screen.
        RemoteHostStore.shared.disableAll()
        // Disconnect the active Connect peer BEFORE wiping the session: halt the
        // remote device and clear app-side routing state (connectedDeviceAddr/
        // name) first, then tear down the engine session (control server,
        // Connect-host advertiser, Deezer client). Ordered in one Task so the
        // disconnect lands before logout; the next login re-inits services fresh.
        Task {
            if wasCasting { await PlayerController.shared.disconnect() }
            await Engine.logout()
        }
        account = nil
        phase = .loggedOut
    }
}
