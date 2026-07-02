import SwiftUI
import WebKit

// DeezerWebView embeds a WKWebView pointing at the Deezer web login and watches
// the shared cookie store. The moment a non-empty `arl` cookie appears (the
// user has finished logging in) it reports the value back, so nobody has to copy
// an ARL by hand. WebKit is a macOS system framework — no external dependency.
struct DeezerWebView: NSViewRepresentable {
    let url: URL
    let onARL: (String) -> Void

    func makeCoordinator() -> Coordinator { Coordinator(onARL: onARL) }

    func makeNSView(context: Context) -> WKWebView {
        let cfg = WKWebViewConfiguration()
        // Use the default (persistent) data store so the Deezer session — and the
        // arl cookie — behave like a normal browser login.
        cfg.websiteDataStore = .default()
        let web = WKWebView(frame: .zero, configuration: cfg)
        web.navigationDelegate = context.coordinator
        // Observe live cookie-store changes; the navigation delegate re-scans as a
        // fallback for flows that set arl without a change event we see in time.
        web.configuration.websiteDataStore.httpCookieStore.add(context.coordinator)
        web.load(URLRequest(url: url))
        return web
    }

    func updateNSView(_ nsView: WKWebView, context: Context) {}

    static func dismantleNSView(_ nsView: WKWebView, coordinator: Coordinator) {
        nsView.configuration.websiteDataStore.httpCookieStore.remove(coordinator)
    }

    final class Coordinator: NSObject, WKNavigationDelegate, WKHTTPCookieStoreObserver {
        let onARL: (String) -> Void
        init(onARL: @escaping (String) -> Void) { self.onARL = onARL }

        // Live cookie-store change — re-scan for a non-empty arl.
        func cookiesDidChange(in cookieStore: WKHTTPCookieStore) {
            scan(cookieStore)
        }

        // Fallback poll after each navigation (login redirects).
        func webView(_ webView: WKWebView, didFinish navigation: WKNavigation!) {
            scan(webView.configuration.websiteDataStore.httpCookieStore)
        }

        // Look for the `arl` cookie on a .deezer.com domain and report it.
        // No one-shot latch here: de-duping lives in AppState.webLoginCaptured
        // (webLoginAttempted/busy), which resets on a failed DZInit so the user
        // can retry inside the same sheet — a latch would swallow that retry.
        private func scan(_ store: WKHTTPCookieStore) {
            store.getAllCookies { [weak self] cookies in
                guard let self else { return }
                guard let arl = cookies.first(where: {
                    $0.name == "arl" && $0.domain.contains("deezer.com")
                }), !arl.value.isEmpty else { return }
                self.onARL(arl.value)
            }
        }
    }
}

// DeezerLoginSheet presents the embedded login webview. On capture it hands the
// arl to AppState (which runs DZInit + persists it); a spinner covers the webview
// while signing in, and a failure banner lets the user try again or fall back to
// manual ARL entry by cancelling.
struct DeezerLoginSheet: View {
    @EnvironmentObject var app: AppState
    // Either /login or / works; /login lands straight on the form.
    private let loginURL = URL(string: "https://www.deezer.com/login")!

    var body: some View {
        VStack(spacing: 0) {
            HStack(spacing: 8) {
                Image(systemName: "person.crop.circle").foregroundStyle(DZ.accent)
                Text("Log in with Deezer")
                    .font(.system(size: 15, weight: .semibold)).foregroundStyle(DZ.textPri)
                Spacer()
                Button("Cancel") { app.showLoginWeb = false }
                    .buttonStyle(.plain).foregroundStyle(DZ.textSec)
            }
            .padding(.horizontal, 16).padding(.vertical, 12)
            .overlay(Divider().overlay(DZ.hairline), alignment: .bottom)

            ZStack {
                DeezerWebView(url: loginURL) { arl in
                    // WebKit callbacks may arrive off the main actor; hop on.
                    Task { @MainActor in app.webLoginCaptured(arl: arl) }
                }
                if app.busy {
                    Color.black.opacity(0.45)
                    ProgressView("Signing in…").tint(DZ.accent)
                        .padding(20)
                        .background(.ultraThinMaterial, in: RoundedRectangle(cornerRadius: 12))
                }
            }

            if let e = app.loginError, !app.busy {
                HStack(spacing: 8) {
                    Image(systemName: "exclamationmark.triangle.fill").foregroundStyle(.red)
                    Text(e).foregroundStyle(DZ.textPri).font(.callout)
                    Spacer()
                }
                .padding(.horizontal, 16).padding(.vertical, 10)
                .background(DZ.panelBG)
            }
        }
        .frame(width: 520, height: 680)
        .background(DZ.windowBG)
    }
}
