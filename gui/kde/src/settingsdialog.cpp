#include "settingsdialog.h"

#include <QCheckBox>
#include <QComboBox>
#include <QDesktopServices>
#include <QDialogButtonBox>
#include <QFormLayout>
#include <QGroupBox>
#include <QHBoxLayout>
#include <QJsonDocument>
#include <QJsonObject>
#include <QLabel>
#include <QLineEdit>
#include <QPushButton>
#include <QSettings>
#include <QUrl>
#include <QVBoxLayout>
#include <QtConcurrent>

// The Go engine's C API — only the remote-control calls are needed here
// (DZFree, to release DZControlConfigJSON's result, comes along with the
// header). Same include the other secondary KDE source files use.
extern "C" {
#include "libdeezercore.h"
}

// Remote-control settings (control API + phone remote). Redeclared here (like
// mainwindow.cpp does for its own additions) so the dialog still builds
// against an older generated header; identical redeclarations are harmless.
extern "C" char *DZControlConfigJSON(void); // {"enabled","addr","token","lan","running"}
extern "C" void  DZSetControlConfig(int enabled, char *addr, char *token);
extern "C" void  DZWebRemoteSetEnabled(int on);   // 1=enable, 0=disable
extern "C" char *DZWebRemoteInfoJSON(void);       // {"enabled":bool,...}

// v1.5.1 addition. Checks GitHub for a newer release; never downloads or
// installs anything. Result is a malloc'd JSON string — free with DZFree.
extern "C" char *DZCheckUpdateJSON(void); // {"current","latest","hasUpdate","url","notes"}

namespace {
const char *kKeyQuality    = "audio/qualityLevel"; // int: 0=128, 1=320, 2=FLAC
const char *kKeyReplayGain = "audio/replayGain";   // bool: loudness normalization
const char *kKeyTray       = "behavior/closeToTray";
const char *kKeyDevice     = "audio/outputDevice"; // string: device id ("" = default)
const char *kKeyGapless    = "audio/gapless";      // bool: gapless playback
const char *kKeyCrossfade  = "audio/crossfadeMs";  // int: 0/3000/6000/12000

QSettings openIni(const QString &path) { return QSettings(path, QSettings::IniFormat); }

// Take ownership of a malloc'd C string from a DZ*JSON call, copy it into a
// QByteArray and release the C buffer with DZFree (mirrors mainwindow.cpp).
QByteArray takeJson(char *s) {
    QByteArray b;
    if (s) {
        b = QByteArray(s);
        DZFree(s);
    }
    return b;
}
} // namespace

int SettingsDialog::loadQuality(const QString &iniPath) {
    QSettings s = openIni(iniPath);
    int v = s.value(kKeyQuality, 0).toInt(); // default: Normal (MP3_128)
    return v < 0 ? 0 : (v > 2 ? 2 : v);
}

bool SettingsDialog::loadReplayGain(const QString &iniPath) {
    QSettings s = openIni(iniPath);
    return s.value(kKeyReplayGain, false).toBool(); // default: off
}

bool SettingsDialog::loadCloseToTray(const QString &iniPath) {
    QSettings s = openIni(iniPath);
    return s.value(kKeyTray, true).toBool();      // default: keep playing in tray
}

QString SettingsDialog::loadOutputDevice(const QString &iniPath) {
    QSettings s = openIni(iniPath);
    return s.value(kKeyDevice, QString()).toString(); // default: system default
}

bool SettingsDialog::loadGapless(const QString &iniPath) {
    QSettings s = openIni(iniPath);
    return s.value(kKeyGapless, false).toBool();  // default: off
}

int SettingsDialog::loadCrossfadeMs(const QString &iniPath) {
    QSettings s = openIni(iniPath);
    int v = s.value(kKeyCrossfade, 0).toInt();    // default: off
    return v < 0 ? 0 : v;
}

SettingsDialog::SettingsDialog(const QString &iniPath,
                               const QVector<AudioDevice> &devices,
                               const QString &currentDeviceId, QWidget *parent)
    : QDialog(parent), m_iniPath(iniPath), m_initialDevice(currentDeviceId) {
    setWindowTitle(QStringLiteral("OpenDeezer Settings"));
    setModal(true);

    auto *root = new QVBoxLayout(this);

    // ---- Audio ----
    auto *audioBox  = new QGroupBox(QStringLiteral("Audio"));
    auto *audioForm = new QFormLayout(audioBox);
    m_quality = new QComboBox;
    m_quality->addItem(QStringLiteral("Normal — MP3 128 kbps"), 0);
    m_quality->addItem(QStringLiteral("High — MP3 320 kbps"), 1);
    m_quality->addItem(QStringLiteral("HiFi — FLAC lossless"), 2);
    m_quality->setCurrentIndex(loadQuality(m_iniPath));
    audioForm->addRow(QStringLiteral("Streaming quality"), m_quality);
    m_replayGain = new QCheckBox(QStringLiteral("Normalize loudness (ReplayGain)"));
    m_replayGain->setChecked(loadReplayGain(m_iniPath));
    audioForm->addRow(QString(), m_replayGain);

    // Output device — populated from the live device list passed in. The current
    // selection prefers the engine's active device, then the system default.
    m_device = new QComboBox;
    if (devices.isEmpty()) {
        m_device->addItem(QStringLiteral("System default"), QString());
    } else {
        for (const AudioDevice &d : devices) {
            QString label = d.name.isEmpty() ? QStringLiteral("System default") : d.name;
            if (d.isDefault)
                label += QStringLiteral("  (default)");
            m_device->addItem(label, d.id);
        }
        int sel = m_device->findData(currentDeviceId);
        if (sel < 0)
            for (int i = 0; i < devices.size(); ++i)
                if (devices[i].isDefault) { sel = i; break; }
        if (sel >= 0)
            m_device->setCurrentIndex(sel);
    }
    audioForm->addRow(QStringLiteral("Output device"), m_device);

    // Gapless + crossfade. Crossfade overlaps adjacent tracks; gapless butts
    // them with no silence. Both rely on the engine preloading the next track.
    m_gapless = new QCheckBox(QStringLiteral("Gapless playback"));
    m_gapless->setChecked(loadGapless(m_iniPath));
    audioForm->addRow(QString(), m_gapless);

    m_crossfade = new QComboBox;
    m_crossfade->addItem(QStringLiteral("Off"), 0);
    m_crossfade->addItem(QStringLiteral("3 seconds"), 3000);
    m_crossfade->addItem(QStringLiteral("6 seconds"), 6000);
    m_crossfade->addItem(QStringLiteral("12 seconds"), 12000);
    {
        const int xf = loadCrossfadeMs(m_iniPath);
        int sel = m_crossfade->findData(xf);
        m_crossfade->setCurrentIndex(sel < 0 ? 0 : sel);
    }
    audioForm->addRow(QStringLiteral("Crossfade"), m_crossfade);
    root->addWidget(audioBox);

    // ---- Behaviour ----
    auto *behBox  = new QGroupBox(QStringLiteral("Behaviour"));
    auto *behLay  = new QVBoxLayout(behBox);
    m_tray = new QCheckBox(QStringLiteral("Keep playing in the background "
                                          "(close to tray)"));
    m_tray->setChecked(loadCloseToTray(m_iniPath));
    auto *hint = new QLabel(QStringLiteral(
        "When enabled, closing the window hides it to the system tray and the "
        "music keeps playing. Use the tray icon to restore or quit."));
    hint->setWordWrap(true);
    QFont hf = hint->font();
    hf.setPointSize(qMax(1, hf.pointSize() - 1));
    hint->setFont(hf);
    behLay->addWidget(m_tray);
    behLay->addWidget(hint);
    root->addWidget(behBox);

    // ---- Remote control ----
    // Unlike the groups above, this talks to the engine directly and applies
    // on every change (it's toggling a live server, not a playback setting).
    auto *remoteBox  = new QGroupBox(QStringLiteral("Remote control"));
    auto *remoteForm = new QFormLayout(remoteBox);

    m_ctrlEnable = new QCheckBox(QStringLiteral("Enable remote control"));
    remoteForm->addRow(QString(), m_ctrlEnable);

    m_ctrlLan = new QCheckBox(QStringLiteral("Allow on local network (LAN)"));
    remoteForm->addRow(QString(), m_ctrlLan);

    m_ctrlToken = new QLineEdit;
    m_ctrlToken->setPlaceholderText(QStringLiteral("None"));
    remoteForm->addRow(QStringLiteral("Access token"), m_ctrlToken);

    m_phoneRemote = new QCheckBox(QStringLiteral("Enable Phone Remote"));
    remoteForm->addRow(QString(), m_phoneRemote);

    auto *remoteHint = new QLabel(QStringLiteral(
        "Lets another OpenDeezer app or your phone control playback over the "
        "network."));
    remoteHint->setWordWrap(true);
    QFont ref = remoteHint->font();
    ref.setPointSize(qMax(1, ref.pointSize() - 1));
    remoteHint->setFont(ref);
    remoteForm->addRow(remoteHint);
    root->addWidget(remoteBox);

    // Seed both controls from the engine's current state.
    {
        const QJsonObject cfg =
            QJsonDocument::fromJson(takeJson(DZControlConfigJSON())).object();
        m_ctrlEnable->setChecked(cfg.value("enabled").toBool());
        m_ctrlLan->setChecked(cfg.value("lan").toBool());
        m_ctrlToken->setText(cfg.value("token").toString());
    }
    m_ctrlLan->setEnabled(m_ctrlEnable->isChecked());
    m_ctrlToken->setEnabled(m_ctrlEnable->isChecked());
    {
        const QJsonObject info =
            QJsonDocument::fromJson(takeJson(DZWebRemoteInfoJSON())).object();
        m_phoneRemote->setChecked(info.value("enabled").toBool());
    }

    // Apply live on every change — no need to wait for OK.
    connect(m_ctrlEnable, &QCheckBox::toggled, this, [this](bool on) {
        m_ctrlLan->setEnabled(on);
        m_ctrlToken->setEnabled(on);
        applyControlConfig();
    });
    connect(m_ctrlLan, &QCheckBox::toggled, this, [this] { applyControlConfig(); });
    connect(m_ctrlToken, &QLineEdit::editingFinished, this, [this] { applyControlConfig(); });
    connect(m_phoneRemote, &QCheckBox::toggled, this,
            [](bool on) { DZWebRemoteSetEnabled(on ? 1 : 0); });

    // ---- About ----
    // On-demand release check (mirrors the background one MainWindow runs at
    // startup): never blocks, never downloads/installs anything — Download just
    // opens the GitHub release page in the browser.
    auto *aboutBox = new QGroupBox(QStringLiteral("About"));
    auto *aboutLay = new QVBoxLayout(aboutBox);
    auto *updRow   = new QHBoxLayout;
    m_checkUpdatesBtn = new QPushButton(QStringLiteral("Check for Updates"));
    updRow->addWidget(m_checkUpdatesBtn);
    m_updateResult = new QLabel;
    m_updateResult->setWordWrap(true);
    updRow->addWidget(m_updateResult, 1);
    m_updateDownloadBtn = new QPushButton(QStringLiteral("Download"));
    m_updateDownloadBtn->hide();
    updRow->addWidget(m_updateDownloadBtn);
    aboutLay->addLayout(updRow);
    root->addWidget(aboutBox);

    connect(m_checkUpdatesBtn, &QPushButton::clicked, this, &SettingsDialog::checkForUpdates);
    connect(m_updateDownloadBtn, &QPushButton::clicked, this, [this] {
        if (!m_updateUrl.isEmpty())
            QDesktopServices::openUrl(QUrl(m_updateUrl));
    });

    auto *buttons = new QDialogButtonBox(QDialogButtonBox::Ok | QDialogButtonBox::Cancel);
    // Deezer-purple accent on the default action.
    buttons->button(QDialogButtonBox::Ok)->setStyleSheet(
        QStringLiteral("QPushButton{background:#A238FF;color:white;"
                       "padding:5px 16px;border-radius:4px;}"));
    root->addWidget(buttons);

    connect(buttons, &QDialogButtonBox::accepted, this, [this] { save(); accept(); });
    connect(buttons, &QDialogButtonBox::rejected, this, &QDialog::reject);
}

void SettingsDialog::save() {
    const int     level = m_quality->currentData().toInt();
    const bool    rg    = m_replayGain->isChecked();
    const bool    tray  = m_tray->isChecked();
    const QString dev   = m_device->currentData().toString();
    const bool    gap   = m_gapless->isChecked();
    const int     xf    = m_crossfade->currentData().toInt();

    QSettings s = openIni(m_iniPath);
    s.setValue(kKeyQuality, level);
    s.setValue(kKeyReplayGain, rg);
    s.setValue(kKeyTray, tray);
    s.setValue(kKeyDevice, dev);
    s.setValue(kKeyGapless, gap);
    s.setValue(kKeyCrossfade, xf);
    s.sync();

    emit qualityChanged(level);
    emit replayGainChanged(rg);
    emit closeToTrayChanged(tray);
    // Re-applying the same output device restarts audio with an audible glitch,
    // so only emit when it actually changed.
    if (dev != m_initialDevice)
        emit outputDeviceChanged(dev);
    emit gaplessChanged(gap);
    emit crossfadeChanged(xf);

    // The remote-control group already applies itself live on every change;
    // this just catches a token edit still pending when OK is pressed.
    applyControlConfig();
}

void SettingsDialog::applyControlConfig() {
    const bool enabled = m_ctrlEnable->isChecked();
    const QByteArray addr  = m_ctrlLan->isChecked() ? QByteArray(":7654") : QByteArray();
    const QByteArray token = m_ctrlToken->text().toUtf8();
    DZSetControlConfig(enabled ? 1 : 0,
                        const_cast<char *>(addr.constData()),
                        const_cast<char *>(token.constData()));
}

// On-demand release check: runs DZCheckUpdateJSON off the GUI thread (it hits
// the network) and shows the result inline. Never downloads or installs
// anything — Download just opens the release page in the browser.
void SettingsDialog::checkForUpdates() {
    m_checkUpdatesBtn->setEnabled(false);
    m_updateDownloadBtn->hide();
    m_updateResult->setText(QStringLiteral("Checking…"));
    m_updateResult->setToolTip(QString());

    QtConcurrent::run([this] {
        const QByteArray j = takeJson(DZCheckUpdateJSON());
        QMetaObject::invokeMethod(this, [this, j] {
            m_checkUpdatesBtn->setEnabled(true);
            const QJsonObject o = QJsonDocument::fromJson(j).object();
            const QString latest = o.value("latest").toString();
            if (o.value("hasUpdate").toBool()) {
                m_updateUrl = o.value("url").toString();
                m_updateResult->setText(
                    QStringLiteral("OpenDeezer %1 is available.").arg(latest));
                m_updateResult->setToolTip(o.value("notes").toString());
                m_updateDownloadBtn->show();
            } else if (!latest.isEmpty()) {
                m_updateResult->setText(QStringLiteral("You're up to date (%1).").arg(latest));
            } else {
                m_updateResult->setText(
                    QStringLiteral("Couldn't check for updates — try again later."));
            }
        });
    });
}
