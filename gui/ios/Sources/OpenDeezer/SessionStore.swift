import Foundation

/// Drives the app's top-level navigation: bootstrapping the saved session,
/// logging in, the Free-tier premium gate, and logout.
@MainActor
final class SessionStore: ObservableObject {
    static let shared = SessionStore()

    enum Phase: Equatable {
        case launching
        case loggedOut
        case gated
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
            phase = .loggedOut
            lastError = "Login failed. Check your ARL and try again."
            return false
        }
        if persist { KeychainStore.save(key: arlKey, value: trimmed) }

        if let acct = try? await Engine.account() {
            account = acct
            phase = acct.premium ? .ready : .gated
        } else {
            // Account parsing failed but Init succeeded — don't strand the user.
            phase = .ready
        }

        if phase == .ready {
            PlayerController.shared.start()
            await LibraryStore.shared.refreshAll()
        }
        return true
    }

    func logout() {
        KeychainStore.delete(key: arlKey)
        PlayerController.shared.stopPlayback()
        account = nil
        phase = .loggedOut
    }
}
