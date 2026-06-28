#include "logindialog.h"

#include <QCoreApplication>
#include <QDir>
#include <QFile>
#include <QFileInfo>
#include <QFont>
#include <QFrame>
#include <QHBoxLayout>
#include <QLabel>
#include <QLineEdit>
#include <QMetaObject>
#include <QProcess>
#include <QPushButton>
#include <QStandardPaths>
#include <QVBoxLayout>
#include <QtConcurrent>

#include <utility>

// The Go engine's C API — only DZInit is needed here.
extern "C" {
#include "libdeezercore.h"
}

namespace {

const char *kAccent = "#A238FF"; // Deezer "Electric Violet"

char *cstr(const QByteArray &b) { return const_cast<char *>(b.constData()); }

} // namespace

LoginDialog::LoginDialog(QString arlPath, QWidget *parent)
    : QDialog(parent), m_arlPath(std::move(arlPath)) {
    setWindowTitle(QStringLiteral("Log in — OpenDeezer"));
    setModal(true);
    resize(440, 360);

    auto *v = new QVBoxLayout(this);
    v->setContentsMargins(28, 28, 28, 28);
    v->setSpacing(14);

    auto *title = new QLabel(QStringLiteral("Welcome to OpenDeezer"));
    QFont tf = title->font();
    tf.setPointSize(tf.pointSize() + 6);
    tf.setBold(true);
    title->setFont(tf);
    v->addWidget(title);

    auto *blurb = new QLabel(QStringLiteral(
        "Sign in with your Deezer account to start listening. A browser window "
        "opens; we capture your session automatically — no copy/paste needed."));
    blurb->setWordWrap(true);
    v->addWidget(blurb);

    m_webBtn = new QPushButton(QStringLiteral("Log in with Deezer"));
    m_webBtn->setDefault(true);
    m_webBtn->setMinimumHeight(40);
    m_webBtn->setStyleSheet(
        QString("QPushButton{background:%1;color:white;border:none;border-radius:6px;"
                "font-weight:bold;padding:8px 16px;}"
                "QPushButton:hover{background:#B45CFF;}"
                "QPushButton:disabled{background:#5A4070;color:#CCC;}")
            .arg(kAccent));
    connect(m_webBtn, &QPushButton::clicked, this, &LoginDialog::runHelper);
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
    m_manualEdit->setEchoMode(QLineEdit::Password);
    connect(m_manualEdit, &QLineEdit::returnPressed, this, &LoginDialog::submitManual);
    m_manualBtn = new QPushButton(QStringLiteral("Use this ARL"));
    connect(m_manualBtn, &QPushButton::clicked, this, &LoginDialog::submitManual);
    row->addWidget(m_manualEdit, 1);
    row->addWidget(m_manualBtn);
    v->addLayout(row);

    m_status = new QLabel(this);
    m_status->setWordWrap(true);
    m_status->setVisible(false);
    v->addWidget(m_status);

    v->addStretch(1);
}

// Locate the opendeezer-login helper: next to this executable first (where both
// the standalone build and the unified launcher install it), then PATH.
QString LoginDialog::helperPath() const {
    const QString beside = QCoreApplication::applicationDirPath() + "/opendeezer-login";
    if (QFileInfo::exists(beside))
        return beside;
    const QString onPath = QStandardPaths::findExecutable(QStringLiteral("opendeezer-login"));
    if (!onPath.isEmpty())
        return onPath;
    return beside; // report the expected location in the error if missing
}

// Launch the standalone login helper and read the arl it prints to stdout. The
// web view runs entirely in that separate process, so it works in the dlopen'd
// unified launcher and a crash there cannot close this app.
void LoginDialog::runHelper() {
    if (m_verifying || (m_helper && m_helper->state() != QProcess::NotRunning))
        return;
    const QString helper = helperPath();
    if (!QFileInfo::exists(helper)) {
        showError(QStringLiteral("Login helper not found (%1). Paste your ARL below instead.")
                      .arg(helper));
        return;
    }
    setBusy(true);
    m_status->setStyleSheet(QString());
    m_status->setText(QStringLiteral("Opening Deezer login…"));
    m_status->setVisible(true);

    m_helper = new QProcess(this);
    m_helper->setProgram(helper);
    connect(m_helper, &QProcess::finished, this,
            [this](int code, QProcess::ExitStatus) {
                const QString out = QString::fromUtf8(m_helper->readAllStandardOutput()).trimmed();
                m_helper->deleteLater();
                m_helper = nullptr;
                if (code == 0 && !out.isEmpty()) {
                    tryArl(out);
                } else {
                    setBusy(false);
                    m_status->setVisible(false); // user closed it / no capture
                }
            });
    connect(m_helper, &QProcess::errorOccurred, this, [this](QProcess::ProcessError) {
        if (!m_helper)
            return;
        m_helper->deleteLater();
        m_helper = nullptr;
        setBusy(false);
        showError(QStringLiteral("Couldn't start the login helper. Paste your ARL below instead."));
    });
    m_helper->start();
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
            setBusy(false);
            showError(QStringLiteral(
                "Login failed — the session was invalid or expired. Please try again."));
        }, Qt::QueuedConnection);
    });
}

void LoginDialog::setBusy(bool busy) {
    if (m_webBtn)
        m_webBtn->setEnabled(!busy);
    if (m_manualBtn)
        m_manualBtn->setEnabled(!busy);
    if (m_manualEdit)
        m_manualEdit->setEnabled(!busy);
}

void LoginDialog::showError(const QString &msg) {
    m_status->setStyleSheet(QStringLiteral("color:#D32F2F;"));
    m_status->setText(msg);
    m_status->setVisible(true);
}
