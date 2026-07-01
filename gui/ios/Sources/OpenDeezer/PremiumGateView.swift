import SwiftUI

/// Blocks Deezer Free accounts — the engine can't stream on-demand tracks
/// without a paid entitlement (see `Account.premium`).
struct PremiumGateView: View {
    @EnvironmentObject private var session: SessionStore

    var body: some View {
        ZStack {
            Color(.systemBackground).ignoresSafeArea()
            VStack(spacing: 20) {
                Spacer()
                ZStack {
                    Circle().fill(Palette.accent.opacity(0.15)).frame(width: 96, height: 96)
                    Image(systemName: "lock.fill")
                        .font(.system(size: 36, weight: .semibold))
                        .foregroundStyle(Palette.accent)
                }
                Text("Deezer Premium required")
                    .font(.title2.bold())
                if let name = session.account?.name, !name.isEmpty {
                    Text("Hi \(name) — your account is on Deezer Free.")
                        .font(.subheadline)
                        .foregroundStyle(.secondary)
                }
                Text("OpenDeezer streams full tracks with your Deezer account, which requires a paid plan (Premium, Family or HiFi).")
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
                    .multilineTextAlignment(.center)
                    .padding(.horizontal, 36)
                Spacer()
                Button("Log out") { session.logout() }
                    .glassButton()
                    .padding(.horizontal, 48)
                    .padding(.bottom, 32)
            }
        }
    }
}
