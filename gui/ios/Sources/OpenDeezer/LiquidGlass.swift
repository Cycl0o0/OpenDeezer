import SwiftUI

// Liquid Glass (iOS 26 `SwiftUICore.View.glassEffect`) helpers, each with a
// clean `.ultraThinMaterial` fallback for iOS 17–25 so call sites don't need
// to branch themselves.

private struct GlassSurface<S: Shape>: ViewModifier {
    var shape: S
    var interactive: Bool = false

    func body(content: Content) -> some View {
        if #available(iOS 26.0, *) {
            content.glassEffect(interactive ? .regular.interactive() : .regular, in: shape)
        } else {
            content.background(.ultraThinMaterial, in: shape)
        }
    }
}

extension View {
    /// Rounded-rectangle glass surface — cards, sheets, settings rows.
    func glassCard(cornerRadius: CGFloat = 20, interactive: Bool = false) -> some View {
        modifier(GlassSurface(shape: RoundedRectangle(cornerRadius: cornerRadius, style: .continuous), interactive: interactive))
    }

    /// Capsule/pill glass surface — the mini player, transport bars, chips.
    func glassPill(interactive: Bool = false) -> some View {
        modifier(GlassSurface(shape: Capsule(style: .continuous), interactive: interactive))
    }

    /// Circular glass surface — round icon/transport buttons.
    func glassCircle(interactive: Bool = false) -> some View {
        modifier(GlassSurface(shape: Circle(), interactive: interactive))
    }

    /// `.glass` / `.glassProminent` button style on iOS 26+, a close bordered
    /// equivalent below.
    @ViewBuilder
    func glassButton(prominent: Bool = false) -> some View {
        if #available(iOS 26.0, *) {
            if prominent {
                self.buttonStyle(.glassProminent)
            } else {
                self.buttonStyle(.glass)
            }
        } else {
            if prominent {
                self.buttonStyle(.borderedProminent)
            } else {
                self.buttonStyle(.bordered)
            }
        }
    }
}
