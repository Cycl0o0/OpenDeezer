import SwiftUI
import WebKit

struct LoginView: View {
    @EnvironmentObject private var session: SessionStore
    @State private var showWebLogin = false
    @State private var showManualEntry = false
    @State private var manualARL = ""
    @State private var isLoggingIn = false

    var body: some View {
        ZStack {
            LinearGradient(
                colors: [Color.black, Color(red: 0.12, green: 0.02, blue: 0.2), Color.black],
                startPoint: .top, endPoint: .bottom
            )
            .ignoresSafeArea()

            VStack(spacing: 28) {
                Spacer()

                VStack(spacing: 12) {
                    ZStack {
                        Circle()
                            .fill(Palette.accent.opacity(0.25))
                            .frame(width: 96, height: 96)
                        Image(systemName: "music.note")
                            .font(.system(size: 40, weight: .bold))
                            .foregroundStyle(Palette.accent)
                    }
                    Text("OpenDeezer")
                        .font(.largeTitle.bold())
                        .foregroundStyle(.white)
                    Text("Sign in with your Deezer account to stream your library, playlists and Flow.")
                        .font(.subheadline)
                        .foregroundStyle(.white.opacity(0.65))
                        .multilineTextAlignment(.center)
                        .padding(.horizontal, 40)
                }

                Spacer()

                VStack(spacing: 14) {
                    if isLoggingIn {
                        ProgressView().tint(.white)
                    }
                    if let error = session.lastError {
                        Text(error)
                            .font(.footnote)
                            .foregroundStyle(.red)
                            .multilineTextAlignment(.center)
                            .padding(.horizontal, 32)
                    }

                    Button {
                        showWebLogin = true
                    } label: {
                        Text("Log in with Deezer")
                            .font(.headline)
                            .frame(maxWidth: .infinity)
                            .padding(.vertical, 14)
                    }
                    .glassButton(prominent: true)
                    .tint(Palette.accent)
                    .padding(.horizontal, 32)

                    Button("Paste ARL cookie manually") {
                        showManualEntry = true
                    }
                    .font(.subheadline)
                    .foregroundStyle(.white.opacity(0.75))
                }
                .padding(.bottom, 48)
            }
        }
        .sheet(isPresented: $showWebLogin) {
            DeezerWebLoginView { arl in
                showWebLogin = false
                Task { await attemptLogin(arl) }
            }
        }
        .alert("Paste ARL cookie", isPresented: $showManualEntry) {
            TextField("arl", text: $manualARL)
                .autocorrectionDisabled()
                .textInputAutocapitalization(.never)
            Button("Cancel", role: .cancel) { manualARL = "" }
            Button("Log in") {
                let value = manualARL
                manualARL = ""
                Task { await attemptLogin(value) }
            }
        } message: {
            Text("On deezer.com, open developer tools > Application > Cookies and copy the value of \"arl\".")
        }
    }

    private func attemptLogin(_ arl: String) async {
        isLoggingIn = true
        _ = await session.login(arl: arl)
        isLoggingIn = false
    }
}

/// WKWebView-hosted Deezer login page; polls the cookie jar for `arl` once
/// the user finishes signing in, then hands it back to `LoginView`.
struct DeezerWebLoginView: UIViewControllerRepresentable {
    var onARL: (String) -> Void

    func makeUIViewController(context: Context) -> WebLoginViewController {
        WebLoginViewController(onARL: onARL)
    }
    func updateUIViewController(_ uiViewController: WebLoginViewController, context: Context) {}
}

final class WebLoginViewController: UIViewController {
    private let onARL: (String) -> Void
    private var webView: WKWebView!
    private var pollTimer: Timer?
    private var resolved = false

    init(onARL: @escaping (String) -> Void) {
        self.onARL = onARL
        super.init(nibName: nil, bundle: nil)
    }
    @available(*, unavailable)
    required init?(coder: NSCoder) { fatalError("unavailable") }

    override func viewDidLoad() {
        super.viewDidLoad()
        view.backgroundColor = .systemBackground

        let bar = UIView()
        bar.translatesAutoresizingMaskIntoConstraints = false
        view.addSubview(bar)

        let closeButton = UIButton(type: .system)
        closeButton.setImage(UIImage(systemName: "xmark.circle.fill"), for: .normal)
        closeButton.tintColor = .secondaryLabel
        closeButton.addTarget(self, action: #selector(closeTapped), for: .touchUpInside)
        closeButton.translatesAutoresizingMaskIntoConstraints = false
        bar.addSubview(closeButton)

        webView = WKWebView(frame: .zero, configuration: WKWebViewConfiguration())
        webView.translatesAutoresizingMaskIntoConstraints = false
        view.addSubview(webView)

        NSLayoutConstraint.activate([
            bar.topAnchor.constraint(equalTo: view.safeAreaLayoutGuide.topAnchor),
            bar.leadingAnchor.constraint(equalTo: view.leadingAnchor),
            bar.trailingAnchor.constraint(equalTo: view.trailingAnchor),
            bar.heightAnchor.constraint(equalToConstant: 44),

            closeButton.trailingAnchor.constraint(equalTo: bar.trailingAnchor, constant: -12),
            closeButton.centerYAnchor.constraint(equalTo: bar.centerYAnchor),
            closeButton.widthAnchor.constraint(equalToConstant: 30),
            closeButton.heightAnchor.constraint(equalToConstant: 30),

            webView.topAnchor.constraint(equalTo: bar.bottomAnchor),
            webView.leadingAnchor.constraint(equalTo: view.leadingAnchor),
            webView.trailingAnchor.constraint(equalTo: view.trailingAnchor),
            webView.bottomAnchor.constraint(equalTo: view.bottomAnchor),
        ])

        webView.load(URLRequest(url: URL(string: "https://www.deezer.com/login")!))
        pollTimer = Timer.scheduledTimer(withTimeInterval: 1.0, repeats: true) { [weak self] _ in
            self?.checkCookie()
        }
    }

    @objc private func closeTapped() { dismiss(animated: true) }

    private func checkCookie() {
        guard !resolved else { return }
        webView.configuration.websiteDataStore.httpCookieStore.getAllCookies { [weak self] cookies in
            guard let self, !self.resolved else { return }
            guard let arl = cookies.first(where: { $0.name == "arl" && $0.domain.contains("deezer.com") }) else { return }
            self.resolved = true
            self.pollTimer?.invalidate()
            DispatchQueue.main.async { self.onARL(arl.value) }
        }
    }

    deinit { pollTimer?.invalidate() }
}
