// opendeezer-login — a tiny STANDALONE QtWebEngine login window.
//
// QtWebEngine cannot run reliably when the whole Qt app is dlopen'd by the
// unified Linux launcher (its Chromium sandbox/zygote can't fork from a dlopen'd
// library — clicking "Log in" crashed the app). The fix is to keep QtWebEngine
// out of the app entirely and run the login in this separate, normal executable.
//
// It opens the Deezer web login, captures the `arl` session cookie, prints it to
// stdout and exits 0. If the user closes the window without signing in, it exits
// non-zero. The parent app (any frontend) spawns it, reads the arl from stdout,
// and verifies/persists it — a crash here can never take down the main app.
#include <QApplication>
#include <QByteArray>
#include <QNetworkCookie>
#include <QUrl>
#include <QVBoxLayout>
#include <QWidget>

#include <QWebEngineCookieStore>
#include <QWebEngineProfile>
#include <QWebEngineView>

#include <cstdio>

int main(int argc, char **argv) {
    // The Chromium sandbox/zygote and GPU process are the usual crash sources on
    // Wayland/KDE; disable them so the web view starts reliably. It only hosts
    // the user's own Deezer login. Respect user overrides.
    if (qEnvironmentVariableIsEmpty("QTWEBENGINE_CHROMIUM_FLAGS"))
        qputenv("QTWEBENGINE_CHROMIUM_FLAGS", "--no-sandbox --disable-gpu --disable-gpu-compositing");
    if (qEnvironmentVariableIsEmpty("QTWEBENGINE_DISABLE_SANDBOX"))
        qputenv("QTWEBENGINE_DISABLE_SANDBOX", "1");

    QApplication::setAttribute(Qt::AA_ShareOpenGLContexts);
    QApplication app(argc, argv);
    app.setApplicationName("OpenDeezer Login");
    app.setApplicationDisplayName("Log in to Deezer");

    QWidget win;
    win.setWindowTitle(QStringLiteral("Log in to Deezer — OpenDeezer"));
    win.resize(520, 700);
    auto *v = new QVBoxLayout(&win);
    v->setContentsMargins(0, 0, 0, 0);

    auto *web = new QWebEngineView(&win);
    v->addWidget(web, 1);

    bool captured = false;
    QObject::connect(
        web->page()->profile()->cookieStore(), &QWebEngineCookieStore::cookieAdded,
        [&captured](const QNetworkCookie &c) {
            if (captured)
                return;
            if (c.name() != QByteArrayLiteral("arl"))
                return;
            if (!c.domain().contains(QStringLiteral("deezer.com")))
                return;
            const QString val = QString::fromUtf8(c.value()).trimmed();
            if (val.isEmpty())
                return;
            captured = true;
            // Hand the token back to the parent process and quit.
            std::fputs(val.toUtf8().constData(), stdout);
            std::fputc('\n', stdout);
            std::fflush(stdout);
            QApplication::exit(0);
        });
    web->page()->profile()->cookieStore()->loadAllCookies();
    web->load(QUrl(QStringLiteral("https://www.deezer.com/login")));

    win.show();
    app.exec();
    return captured ? 0 : 1;
}
