// OpenDeezer — Deezer login dialog (webview + automatic ARL capture).
//
// A modal QDialog offering two ways to authenticate:
//   1. "Log in with Deezer" — embeds a native QWebEngineView pointing at the
//      Deezer web login. After the user signs in, the engine's `arl` session
//      cookie is captured automatically from the profile cookie store (no
//      copy/paste). This is the default, friendly path.
//   2. Manual ARL entry — the existing fallback for users who already have an
//      ARL string.
//
// In both cases the captured/entered ARL is verified with DZInit (the same call
// the app uses to log in) on a worker thread. On success the ARL is persisted to
// the SAME file the frontend reads at startup (~/.config/opendeezer/arl.txt) so
// the next launch auto-logs-in, and the dialog accept()s with the validated ARL
// available via arl(). On failure an error is shown and the user may retry or
// switch methods.
#pragma once

#include <QDialog>
#include <QString>

QT_BEGIN_NAMESPACE
class QStackedWidget;
class QLineEdit;
class QLabel;
class QPushButton;
class QNetworkCookie;
QT_END_NAMESPACE

class QWebEngineView;

class LoginDialog : public QDialog {
    Q_OBJECT
public:
    // arlPath = absolute path to the arl.txt the frontend reads at startup
    // (~/.config/opendeezer/arl.txt). The validated ARL is written there.
    explicit LoginDialog(QString arlPath, QWidget *parent = nullptr);

    // The validated ARL — only meaningful after exec() returned Accepted.
    QString arl() const { return m_arl; }

private:
    // ---- UI construction ----
    QWidget *buildChooserPage();
    QWidget *buildWebPage();

    // ---- flow ----
    void showWebLogin();                              // open the embedded webview
    void onCookieAdded(const QNetworkCookie &cookie); // watch for the arl cookie
    void submitManual();                              // "Use this ARL" button
    void tryArl(const QString &arl);                  // DZInit on a worker thread
    void setBusy(bool busy);                          // disable inputs while verifying
    void showError(const QString &msg);

    QString m_arlPath;            // where to persist a validated ARL
    QString m_arl;                // the validated ARL (set on success)
    bool    m_captured  = false;  // guards duplicate cookie captures
    bool    m_verifying = false;  // a DZInit verification is in flight

    QStackedWidget *m_stack       = nullptr; // 0 = chooser, 1 = webview
    QPushButton    *m_webBtn      = nullptr;
    QLineEdit      *m_manualEdit  = nullptr;
    QPushButton    *m_manualBtn   = nullptr;
    QLabel         *m_status      = nullptr;  // shared status / error line
    QWebEngineView *m_web         = nullptr;
};
