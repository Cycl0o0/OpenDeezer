#include "logindialog.h"

#include <QDir>
#include <QFile>
#include <QFileInfo>
#include <QFont>
#include <QFrame>
#include <QHBoxLayout>
#include <QLabel>
#include <QLineEdit>
#include <QMetaObject>
#include <QNetworkCookie>
#include <QPushButton>
#include <QStackedWidget>
#include <QUrl>
#include <QVBoxLayout>
#include <QtConcurrent>

#include <utility>

#include <QWebEngineCookieStore>
#include <QWebEngineProfile>
#include <QWebEngineView>

// The Go engine's C API — same archive the rest of the app links. We only need
// DZInit here (char* arl -> int 1=ok/0=fail), which is exactly how login works
// elsewhere in this frontend.
extern "C" {
#include "libdeezercore.h"
}

namespace {

const char *kAccent = "#A238FF"; // Deezer "Electric Violet"

// The Deezer web login. The bare site root also lands authenticated users on a
// page that sets the arl cookie, so either works; the login URL is friendlier.
const char *kLoginUrl = "https://www.deezer.com/login";

// Pass a QByteArray's bytes to a non-const char* C parameter. DZInit copies the
// string into Go memory during the call, so the QByteArray just has to outlive
// the call (it does — it is a named local in the caller).
char *cstr(const QByteArray &b) { return const_cast<char *>(b.constData()); }

} // namespace

LoginDialog::LoginDialog(QString arlPath, QWidget *parent)
    : QDialog(parent), m_arlPath(std::move(arlPath)) {
    setWindowTitle(QStringLiteral("Log in — OpenDeezer"));
    setModal(true);
    resize(520, 680);

    m_stack = new QStackedWidget(this);
    m_stack->addWidget(buildChooserPage()); // index 0
    m_stack->addWidget(buildWebPage());      // index 1

    m_status = new QLabel(this);
    m_status->setWordWrap(true);
    m_status->setVisible(false);

    auto *v = new QVBoxLayout(this);
    v->addWidget(m_stack, 1);
    v->addWidget(m_status);
}

// ---- chooser page (default) ----------------------------------------------

QWidget *LoginDialog::buildChooserPage() {
    auto *w = new QWidget;
    auto *v = new QVBoxLayout(w);
    v->setContentsMargins(28, 28, 28, 28);
    v->setSpacing(14);

    auto *title = new QLabel(QStringLiteral("Welcome to OpenDeezer"));
    QFont tf = title->font();
    tf.setPointSize(tf.pointSize() + 6);
    tf.setBold(true);
    title->setFont(tf);
    v->addWidget(title);

    auto *blurb = new QLabel(QStringLiteral(
        "Sign in with your Deezer account to start listening. We'll capture your "
        "session automatically — no need to copy an ARL by hand."));
    blurb->setWordWrap(true);
    v->addWidget(blurb);

    m_webBtn = new QPushButton(QStringLiteral("Log in with Deezer"));
    m_webBtn->setDefault(true);
    m_webBtn->setMinimumHeight(40);
    // Deezer-purple primary button, scoped so the rest stays native Breeze.
    m_webBtn->setStyleSheet(
        QString("QPushButton{background:%1;color:white;border:none;border-radius:6px;"
                "font-weight:bold;padding:8px 16px;}"
                "QPushButton:hover{background:#B45CFF;}"
                "QPushButton:disabled{background:#5A4070;color:#CCC;}")
            .arg(kAccent));
    connect(m_webBtn, &QPushButton::clicked, this, &LoginDialog::showWebLogin);
    v->addWidget(m_webBtn);

    v->addSpacing(8);
    auto *line = new QFrame;
    line->setFrameShape(QFrame::HLine);
    line->setFrameShadow(QFrame::Sunken);
    v->addWidget(line);

    auto *manualLbl = new QLabel(QStringLiteral("Already have an ARL? Paste it here:"));
    manualLbl->setWordWrap(true);
    v->addWidget(manualLbl);

    auto *row = new QHBoxLayout;
    m_manualEdit = new QLineEdit;
    m_manualEdit->setPlaceholderText(QStringLiteral("ARL token…"));
    m_manualEdit->setEchoMode(QLineEdit::Password); // it is a session secret
    connect(m_manualEdit, &QLineEdit::returnPressed, this, &LoginDialog::submitManual);
    m_manualBtn = new QPushButton(QStringLiteral("Use this ARL"));
    connect(m_manualBtn, &QPushButton::clicked, this, &LoginDialog::submitManual);
    row->addWidget(m_manualEdit, 1);
    row->addWidget(m_manualBtn);
    v->addLayout(row);

    v->addStretch(1);
    return w;
}

// ---- webview page ---------------------------------------------------------

QWidget *LoginDialog::buildWebPage() {
    auto *w = new QWidget;
    auto *v = new QVBoxLayout(w);
    v->setContentsMargins(0, 0, 0, 0);
    v->setSpacing(0);

    auto *bar = new QHBoxLayout;
    bar->setContentsMargins(8, 6, 8, 6);
    auto *back = new QPushButton(QStringLiteral("‹ Back"));
    back->setFlat(true);
    connect(back, &QPushButton::clicked, this, [this] {
        if (!m_verifying)
            m_stack->setCurrentIndex(0);
    });
    bar->addWidget(back);
    auto *hint = new QLabel(QStringLiteral("Sign in to Deezer below…"));
    bar->addWidget(hint, 1);
    v->addLayout(bar);

    // The view is created lazily in showWebLogin() so the WebEngine process only
    // spins up if the user actually chooses the web login.
    return w;
}

// ---- flow -----------------------------------------------------------------

void LoginDialog::showWebLogin() {
    if (m_web == nullptr) {
        m_web = new QWebEngineView(this);
        // The page's profile owns the cookie store; watch it for the arl cookie.
        // loadAllCookies() replays any cookies already present so a user who is
        // still signed in from a previous run is picked up immediately.
        QWebEngineProfile *profile = m_web->page()->profile();
        connect(profile->cookieStore(), &QWebEngineCookieStore::cookieAdded,
                this, &LoginDialog::onCookieAdded);
        profile->cookieStore()->loadAllCookies();

        // Insert the view into the web page's layout (below the back bar). Give
        // it stretch + a minimum size — without stretch the QWebEngineView gets
        // its (tiny) size hint and collapses to ~0px, so "nothing shows up".
        auto *page = m_stack->widget(1);
        if (auto *box = qobject_cast<QVBoxLayout *>(page->layout()))
            box->addWidget(m_web, 1);
        else
            page->layout()->addWidget(m_web);
        m_web->setMinimumSize(480, 560);
        m_web->show();
        m_web->load(QUrl(QString::fromUtf8(kLoginUrl)));
    }
    m_status->setVisible(false);
    m_stack->setCurrentIndex(1);
}

// Fires for every cookie the Deezer pages set. We want the session token: a
// non-empty cookie named "arl" on the deezer.com domain.
void LoginDialog::onCookieAdded(const QNetworkCookie &cookie) {
    if (m_captured || m_verifying)
        return;
    if (cookie.name() != QByteArrayLiteral("arl"))
        return;
    if (!cookie.domain().contains(QStringLiteral("deezer.com")))
        return;
    const QString value = QString::fromUtf8(cookie.value()).trimmed();
    if (value.isEmpty())
        return;
    m_captured = true;
    tryArl(value);
}

void LoginDialog::submitManual() {
    const QString arl = m_manualEdit->text().trimmed();
    if (arl.isEmpty()) {
        showError(QStringLiteral("Enter an ARL, or use \"Log in with Deezer\"."));
        return;
    }
    tryArl(arl);
}

// Verify the ARL with DZInit (blocking network) on a worker thread; on success
// persist it and accept(), otherwise surface an error and let the user retry.
void LoginDialog::tryArl(const QString &arl) {
    if (m_verifying)
        return;
    m_verifying = true;
    setBusy(true);
    m_status->setStyleSheet(QString());
    m_status->setText(QStringLiteral("Verifying your account…"));
    m_status->setVisible(true);

    const QByteArray ab = arl.toUtf8();
    QtConcurrent::run([this, ab, arl] {
        const int ok = DZInit(cstr(ab));
        QMetaObject::invokeMethod(this, [this, ok, arl] {
            m_verifying = false;
            if (ok) {
                m_arl = arl;
                // Persist to the same file loadARL() reads so next launch
                // auto-logs-in. Best effort: a failed write still logs in now.
                QDir().mkpath(QFileInfo(m_arlPath).absolutePath());
                QFile f(m_arlPath);
                if (f.open(QIODevice::WriteOnly | QIODevice::Truncate)) {
                    f.write(arl.toUtf8());
                    f.write("\n");
                    f.close();
                }
                accept();
                return;
            }
            // Failed: allow another attempt (web or manual).
            m_captured = false;
            setBusy(false);
            m_stack->setCurrentIndex(0);
            showError(QStringLiteral(
                "Login failed — the session was invalid or expired. "
                "Please try again."));
        }, Qt::QueuedConnection);
    });
}

void LoginDialog::setBusy(bool busy) {
    if (m_webBtn)     m_webBtn->setEnabled(!busy);
    if (m_manualBtn)  m_manualBtn->setEnabled(!busy);
    if (m_manualEdit) m_manualEdit->setEnabled(!busy);
}

void LoginDialog::showError(const QString &msg) {
    m_status->setStyleSheet(QStringLiteral("color:#D32F2F;"));
    m_status->setText(msg);
    m_status->setVisible(true);
}
