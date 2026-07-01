// OpenDeezer — Settings dialog.
//
// A small modal QDialog persisting to the app config dir alongside arl.txt
// (~/.config/opendeezer/settings.ini) via QSettings(IniFormat). Two settings:
//   * audio quality — Normal (MP3_128) / High (MP3_320) -> DZSetQuality
//   * close-to-tray  — keep the engine playing in the background on window close
// The dialog only edits + persists values; it emits the two signals below on
// accept so MainWindow can apply them (DZSetQuality / tray behaviour).
//
// The Remote control group is different: it talks to the engine directly
// (DZControlConfigJSON / DZSetControlConfig / DZWebRemote*) and applies on
// every change rather than waiting for OK, since it's toggling a live server.
#pragma once

#include <QDialog>
#include <QString>
#include <QVector>

QT_BEGIN_NAMESPACE
class QComboBox;
class QCheckBox;
class QLineEdit;
class QLabel;
class QPushButton;
QT_END_NAMESPACE

// One output device row, parsed from DZAudioDevicesJSON by MainWindow and passed
// into the dialog. id "" means the system default device.
struct AudioDevice {
    QString id;
    QString name;
    bool    isDefault = false;
};

class SettingsDialog : public QDialog {
    Q_OBJECT
public:
    // iniPath  = absolute path to ~/.config/opendeezer/settings.ini.
    // devices  = current output devices (from DZAudioDevicesJSON).
    // currentDeviceId = the engine's selected device (DZCurrentAudioDevice).
    SettingsDialog(const QString &iniPath,
                   const QVector<AudioDevice> &devices,
                   const QString &currentDeviceId,
                   QWidget *parent = nullptr);

    // Read persisted values (used by MainWindow at startup, without a dialog).
    static int     loadQuality(const QString &iniPath); // 0=Normal,1=High,2=HiFi
    static bool    loadReplayGain(const QString &iniPath);
    static bool    loadCloseToTray(const QString &iniPath);
    static QString loadOutputDevice(const QString &iniPath); // "" = default
    static bool    loadGapless(const QString &iniPath);
    static int     loadCrossfadeMs(const QString &iniPath);  // 0/3000/6000/12000

signals:
    void qualityChanged(int level);       // 0=MP3_128, 1=MP3_320, 2=FLAC
    void replayGainChanged(bool on);      // loudness normalization toggle
    void closeToTrayChanged(bool on);
    void outputDeviceChanged(const QString &deviceId); // "" = default
    void gaplessChanged(bool on);
    void crossfadeChanged(int ms);

private:
    void save();
    void applyControlConfig(); // pushes enable/LAN/token to the engine live
    void checkForUpdates();    // on-demand DZCheckUpdateJSON; shows the result inline

    QString    m_iniPath;
    QString    m_initialDevice;            // to avoid re-applying an unchanged device
    QComboBox *m_quality     = nullptr;
    QCheckBox *m_replayGain  = nullptr;
    QCheckBox *m_tray        = nullptr;
    QComboBox *m_device      = nullptr;
    QCheckBox *m_gapless     = nullptr;
    QComboBox *m_crossfade   = nullptr;

    // ---- About / manual update check (v1.5.1) ----
    QPushButton *m_checkUpdatesBtn   = nullptr;
    QLabel      *m_updateResult      = nullptr;
    QPushButton *m_updateDownloadBtn = nullptr;
    QString      m_updateUrl;                 // release page from the last successful check

    // Remote control (control API + phone remote) — read from / applied to the
    // engine directly, not persisted through m_iniPath.
    QCheckBox *m_ctrlEnable   = nullptr;
    QCheckBox *m_ctrlLan      = nullptr;
    QLineEdit *m_ctrlToken    = nullptr;
    QCheckBox *m_phoneRemote  = nullptr;
};
