#include "settingsdialog.h"

#include <QCheckBox>
#include <QComboBox>
#include <QDialogButtonBox>
#include <QFormLayout>
#include <QGroupBox>
#include <QLabel>
#include <QPushButton>
#include <QSettings>
#include <QVBoxLayout>

namespace {
const char *kKeyQuality    = "audio/qualityLevel"; // int: 0=128, 1=320, 2=FLAC
const char *kKeyReplayGain = "audio/replayGain";   // bool: loudness normalization
const char *kKeyTray       = "behavior/closeToTray";

QSettings openIni(const QString &path) { return QSettings(path, QSettings::IniFormat); }
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

SettingsDialog::SettingsDialog(const QString &iniPath, QWidget *parent)
    : QDialog(parent), m_iniPath(iniPath) {
    setWindowTitle(QStringLiteral("OpenDeezer Settings"));
    setModal(true);

    auto *root = new QVBoxLayout(this);

    // ---- Audio ----
    auto *audioBox  = new QGroupBox(QStringLiteral("Audio"));
    auto *audioForm = new QFormLayout(audioBox);
    m_quality = new QComboBox;
    m_quality->addItem(QStringLiteral("Normal — MP3 128 kbps"), 0);
    m_quality->addItem(QStringLiteral("High — MP3 320 kbps"), 1);
    m_quality->addItem(QStringLiteral("HiFi — FLAC lossless (falls back to MP3)"), 2);
    m_quality->setCurrentIndex(loadQuality(m_iniPath));
    audioForm->addRow(QStringLiteral("Streaming quality"), m_quality);
    m_replayGain = new QCheckBox(QStringLiteral("Normalize loudness (ReplayGain)"));
    m_replayGain->setChecked(loadReplayGain(m_iniPath));
    audioForm->addRow(QString(), m_replayGain);
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
    const int  level = m_quality->currentData().toInt();
    const bool rg    = m_replayGain->isChecked();
    const bool tray  = m_tray->isChecked();

    QSettings s = openIni(m_iniPath);
    s.setValue(kKeyQuality, level);
    s.setValue(kKeyReplayGain, rg);
    s.setValue(kKeyTray, tray);
    s.sync();

    emit qualityChanged(level);
    emit replayGainChanged(rg);
    emit closeToTrayChanged(tray);
}
