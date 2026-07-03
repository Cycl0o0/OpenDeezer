import SwiftUI

/// Shown when the saved-ARL login fails because the device is offline (engine
/// login-error kind 2) rather than because the ARL expired. Keeps the session
/// (the ARL stays saved) and offers a Retry that re-runs the login.
struct NoInternetView: View {
    @EnvironmentObject private var session: SessionStore
    @State private var isRetrying = false

    var body: some View {
        ZStack {
            Color(.systemBackground).ignoresSafeArea()
            VStack(spacing: 20) {
                Spacer()
                ZStack {
                    Circle().fill(Palette.accent.opacity(0.15)).frame(width: 96, height: 96)
                    Image(systemName: "wifi.slash")
                        .font(.system(size: 36, weight: .semibold))
                        .foregroundStyle(Palette.accent)
                }
                Text("No Internet Connection")
                    .font(.title2.bold())
                Text("Check your connection and try again.")
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
                    .multilineTextAlignment(.center)
                    .padding(.horizontal, 36)
                Spacer()
                Button {
                    guard !isRetrying else { return }
                    Task {
                        isRetrying = true
                        await session.retrySavedLogin()
                        isRetrying = false
                    }
                } label: {
                    HStack(spacing: 8) {
                        if isRetrying { ProgressView().tint(.white) }
                        Text("Retry")
                    }
                    .frame(maxWidth: .infinity)
                    .padding(.vertical, 4)
                }
                .glassButton(prominent: true)
                .tint(Palette.accent)
                .disabled(isRetrying)
                .padding(.horizontal, 48)
                .padding(.bottom, 32)
            }
        }
    }
}
