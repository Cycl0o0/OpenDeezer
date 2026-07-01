import SwiftUI
import UIKit

/// Small dismissible banner shown once per launch when a newer OpenDeezer
/// release is available on GitHub.
struct UpdateBanner: View {
    @EnvironmentObject private var updates: UpdateStore

    var body: some View {
        if let info = updates.info, info.hasUpdate, !updates.bannerDismissed {
            HStack(spacing: 10) {
                Image(systemName: "arrow.down.circle.fill")
                    .foregroundStyle(Palette.accent)
                Text("OpenDeezer \(info.latest) available")
                    .font(.subheadline.weight(.medium))
                    .lineLimit(1)
                Spacer(minLength: 8)
                Button("Download") {
                    if let url = URL(string: info.url) {
                        UIApplication.shared.open(url)
                    }
                }
                .font(.subheadline.weight(.semibold))
                .foregroundStyle(Palette.accent)
                Button {
                    updates.bannerDismissed = true
                } label: {
                    Image(systemName: "xmark")
                        .font(.caption.weight(.semibold))
                        .foregroundStyle(.secondary)
                }
            }
            .padding(.horizontal, 14)
            .padding(.vertical, 10)
            .glassPill()
            .padding(.horizontal, 12)
            .padding(.top, 6)
        }
    }
}
