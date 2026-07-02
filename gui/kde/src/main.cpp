// OpenDeezer (KDE) entry point. Qt6 Widgets follows the system Breeze QStyle on
// Plasma with zero effort, so the window looks native; only the accent widgets
// are restyled Deezer-purple (see MainWindow::buildTransport).
#include <QApplication>
#include <QIcon>
#include <QLibraryInfo>
#include <QLocale>
#include <QTranslator>

#include "mainwindow.h"

// Exported so the unified Linux launcher (gui/linux) can dlopen this backend as
// libopendeezer-qt.so and call opendeezer_run; the standalone opendeezer-kde
// executable wraps it with a trivial main (standalone.cpp).
extern "C" __attribute__((visibility("default")))
int opendeezer_run(int argc, char **argv) {
    // QtWebEngine (the embedded Deezer login web view) crashes on click in two
    // common situations: (1) the Chromium GPU process on Wayland/KDE, and (2) the
    // Chromium sandbox/zygote when the whole Qt app is dlopen'd by the unified
    // launcher — it can't fork the zygote from a dlopen'd library, so the window
    // closes. Disable the sandbox and force software GPU so the web view starts.
    // The sandbox only guards the login page (the user's own Deezer session);
    // manual-ARL login never touches QtWebEngine. Both respect a user override.
    if (qEnvironmentVariableIsEmpty("QTWEBENGINE_CHROMIUM_FLAGS"))
        qputenv("QTWEBENGINE_CHROMIUM_FLAGS", "--no-sandbox --disable-gpu --disable-gpu-compositing");
    if (qEnvironmentVariableIsEmpty("QTWEBENGINE_DISABLE_SANDBOX"))
        qputenv("QTWEBENGINE_DISABLE_SANDBOX", "1");

    // QtWebEngine (the embedded Deezer login webview, src/logindialog.cpp) shares
    // an OpenGL context with the GUI; this attribute must be set before the
    // QApplication is constructed. Harmless when the webview is never opened.
    QApplication::setAttribute(Qt::AA_ShareOpenGLContexts);

    QApplication app(argc, argv);
    QApplication::setApplicationName("OpenDeezer");
    QApplication::setApplicationDisplayName("OpenDeezer");
    QApplication::setOrganizationName("OpenDeezer");
    QApplication::setDesktopFileName("org.opendeezer.OpenDeezer");

    // App/window icon: the embedded resource works for the single binary; fall
    // back to the installed theme icon if present.
    QIcon icon(QStringLiteral(":/opendeezer.png"));
    if (icon.isNull())
        icon = QIcon::fromTheme("org.opendeezer.OpenDeezer");
    QApplication::setWindowIcon(icon);

    // Background playback: hiding the window to the tray must not quit the app.
    // Exit happens only on an explicit Quit (MainWindow::quitApp / closeEvent).
    QApplication::setQuitOnLastWindowClosed(false);

    // Localization: install the embedded catalog for the current system locale
    // (RESOURCE_PREFIX /i18n, filenames opendeezer_<lang>.qm). With no matching
    // .qm the English source strings are used unchanged. Qt Widgets mirror their
    // layouts for right-to-left locales once the layout direction is set.
    auto *translator = new QTranslator(&app);
    if (translator->load(QLocale(), QStringLiteral("opendeezer"),
                         QStringLiteral("_"), QStringLiteral(":/i18n")))
        app.installTranslator(translator);
    // Stock dialog buttons (OK/Cancel/…) come from Qt's own catalog; best-effort.
    auto *qtBase = new QTranslator(&app);
    if (qtBase->load(QLocale(), QStringLiteral("qtbase"), QStringLiteral("_"),
                     QLibraryInfo::path(QLibraryInfo::TranslationsPath)))
        app.installTranslator(qtBase);
    QApplication::setLayoutDirection(QLocale().textDirection());

    MainWindow w;
    w.resize(1100, 720);
    w.show();
    return app.exec();
}
