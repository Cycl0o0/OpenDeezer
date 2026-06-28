// OpenDeezer — Deezer login dialog.
//
// Two ways to authenticate:
//   1. "Log in with Deezer" — launches the standalone `opendeezer-login` helper
//      (a separate QtWebEngine process; see src/loginhelper.cpp). It opens the
//      Deezer web login and prints the captured `arl` cookie to stdout. Running
//      it out-of-process is what makes login work in the dlopen'd unified
//      launcher (QtWebEngine can't run inside a dlopen'd library) and means a
//      web-view crash can never take down the app.
//   2. Manual ARL entry — the fallback for users who already have an ARL.
//
// Either way the ARL is verified with DZInit on a worker thread, persisted to
// the file the frontend reads at startup (~/.config/opendeezer/arl.txt), and the
// dialog accept()s with arl() set.
#pragma once

#include <QDialog>
#include <QString>

QT_BEGIN_NAMESPACE
class QLineEdit;
class QLabel;
class QPushButton;
class QProcess;
QT_END_NAMESPACE

class LoginDialog : public QDialog {
    Q_OBJECT
public:
    explicit LoginDialog(QString arlPath, QWidget *parent = nullptr);

    QString arl() const { return m_arl; }

private:
    void runHelper();                // launch opendeezer-login, read the arl
    void submitManual();             // "Use this ARL" button
    void tryArl(const QString &arl); // DZInit on a worker thread
    void setBusy(bool busy);
    void showError(const QString &msg);
    QString helperPath() const; // locate the opendeezer-login executable

    QString m_arlPath;
    QString m_arl;
    bool    m_verifying = false;

    QPushButton *m_webBtn     = nullptr;
    QLineEdit   *m_manualEdit = nullptr;
    QPushButton *m_manualBtn  = nullptr;
    QLabel      *m_status     = nullptr;
    QProcess    *m_helper     = nullptr;
};
