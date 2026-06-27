// OpenDeezer (KDE) entry point. Qt6 Widgets follows the system Breeze QStyle on
// Plasma with zero effort, so the window looks native; only the accent widgets
// are restyled Deezer-purple (see MainWindow::buildTransport).
#include <QApplication>
#include <QIcon>

#include "mainwindow.h"

// Exported so the unified Linux launcher (gui/linux) can dlopen this backend as
// libopendeezer-qt.so and call opendeezer_run; the standalone opendeezer-kde
// executable wraps it with a trivial main (standalone.cpp).
extern "C" __attribute__((visibility("default")))
int opendeezer_run(int argc, char **argv) {
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

    MainWindow w;
    w.resize(1100, 720);
    w.show();
    return app.exec();
}
