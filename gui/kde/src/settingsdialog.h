// OpenDeezer — Settings dialog.
//
// A small modal QDialog persisting to the app config dir alongside arl.txt
// (~/.config/opendeezer/settings.ini) via QSettings(IniFormat). Two settings:
//   * audio quality — Normal (MP3_128) / High (MP3_320) -> DZSetQuality
//   * close-to-tray  — keep the engine playing in the background on window close
// The dialog only edits + persists values; it emits the two signals below on
// accept so MainWindow can apply them (DZSetQuality / tray behaviour).
#pragma once

#include <QDialog>

QT_BEGIN_NAMESPACE
class QComboBox;
class QCheckBox;
QT_END_NAMESPACE

class SettingsDialog : public QDialog {
    Q_OBJECT
public:
    // iniPath = absolute path to ~/.config/opendeezer/settings.ini.
    SettingsDialog(const QString &iniPath, QWidget *parent = nullptr);

    // Read persisted values (used by MainWindow at startup, without a dialog).
    static int  loadQuality(const QString &iniPath); // 0=Normal,1=High,2=HiFi
    static bool loadReplayGain(const QString &iniPath);
    static bool loadCloseToTray(const QString &iniPath);

signals:
    void qualityChanged(int level);       // 0=MP3_128, 1=MP3_320, 2=FLAC
    void replayGainChanged(bool on);      // loudness normalization toggle
    void closeToTrayChanged(bool on);

private:
    void save();

    QString    m_iniPath;
    QComboBox *m_quality    = nullptr;
    QCheckBox *m_replayGain = nullptr;
    QCheckBox *m_tray       = nullptr;
};
