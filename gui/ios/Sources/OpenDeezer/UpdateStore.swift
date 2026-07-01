import Foundation

/// Checks GitHub for a newer OpenDeezer release. Runs once automatically per
/// launch (non-blocking); Settings can also trigger it on demand.
@MainActor
final class UpdateStore: ObservableObject {
    static let shared = UpdateStore()

    @Published private(set) var info: UpdateInfo?
    @Published var bannerDismissed = false
    @Published private(set) var isChecking = false
    @Published private(set) var lastCheckWasManual = false

    private var checkedThisLaunch = false

    private init() {}

    var hasUpdate: Bool { info?.hasUpdate ?? false }

    /// Called once from the app root after login; no-ops on later calls.
    func checkOnce() {
        guard !checkedThisLaunch else { return }
        checkedThisLaunch = true
        Task { await check(manual: false) }
    }

    /// Settings "Check for Updates" row.
    func checkNow() async {
        await check(manual: true)
    }

    private func check(manual: Bool) async {
        isChecking = true
        lastCheckWasManual = manual
        info = await Engine.checkUpdate()
        if hasUpdate { bannerDismissed = false }
        isChecking = false
    }
}
