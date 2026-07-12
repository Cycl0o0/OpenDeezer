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
//
// The Equalizer group (v1.7) works the same way: DSP, persistence (the
// engine's eq.json) and preset tables all live in the core, so the dialog
// just mirrors DZEQJSON and pushes edits live through DZSetEQJSON.
#pragma once

#include <QDialog>
#include <QJsonObject>
#include <QSet>
#include <QString>
#include <QVector>

QT_BEGIN_NAMESPACE
class QComboBox;
class QCheckBox;
class QGroupBox;
class QLineEdit;
class QLabel;
class QPushButton;
class QSlider;
class QSpinBox;
class QTimer;
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
    // premium  = MainWindow's m_premium; the "Disable ads" row is shown only when
    //            false (Free accounts), since Premium has no ads to disable.
    SettingsDialog(const QString &iniPath,
                   const QVector<AudioDevice> &devices,
                   const QString &currentDeviceId,
                   bool premium,
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
    void applySleepTimer();    // pushes the chosen sleep-timer preset to the engine live
    void checkForUpdates();    // on-demand DZCheckUpdateJSON; shows the result inline
    void refreshEQ();          // (re)seeds every EQ control from DZEQJSON
    void flushEQ();            // pushes coalesced slider edits via DZSetEQJSON
    static void sendEQ(const QJsonObject &o); // one partial DZSetEQJSON update

    QString    m_iniPath;
    QString    m_initialDevice;            // to avoid re-applying an unchanged device
    QComboBox *m_quality     = nullptr;
    QCheckBox *m_replayGain  = nullptr;
    QCheckBox *m_tray        = nullptr;
    QComboBox *m_device      = nullptr;
    QCheckBox *m_gapless     = nullptr;
    QComboBox *m_crossfade   = nullptr;
    QComboBox *m_sleepTimer  = nullptr;    // Off / 15 / 30 / 45 / 60 min / End of track
    QSpinBox  *m_mediaCache  = nullptr;    // engine-owned raw-stream cache MB (DZMediaCacheMB); 0 = off, applies next launch
    QLineEdit *m_downloadDir = nullptr;    // engine-owned (DZDownloadDir/DZSetDownloadDir)
    QCheckBox *m_disableAds  = nullptr;    // Free-only; engine-owned (DZAdsDisabled/DZSetAdsDisabled)

    // ---- Equalizer (v1.7) ----
    // Engine-owned state (DZEQJSON / DZSetEQJSON), applied live like the
    // remote-control group — nothing here is persisted through m_iniPath.
    static constexpr int kEQBands = 10;
    QGroupBox *m_eqBox                = nullptr; // checkable: its checkbox is the on/off switch
    QComboBox *m_eqPreset             = nullptr; // core presets + a trailing "Custom" entry
    QSlider   *m_eqSliders[kEQBands]  = {};      // vertical band sliders, value = dB * 10
    QSlider   *m_eqPreamp             = nullptr; // horizontal output trim, value = dB * 10
    QCheckBox *m_eqMono               = nullptr; // mono downmix — in the Audio group (independent of EQ on/off)
    QTimer    *m_eqFlush              = nullptr; // 33 ms coalescing for per-pixel slider drags
    QSet<int>  m_eqDirtyBands;                   // bands edited since the last flush
    bool       m_eqPreampDirty        = false;
    bool       m_eqLoading            = false;   // guard: widgets are being seeded from the engine
    int        m_eqCustomIdx          = -1;      // combo index of the "Custom" entry

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
    // Last state pushed via DZSetControlConfig (seeded from the engine). save()
    // compares against these so an untouched remote group is never re-applied:
    // DZSetControlConfig restarts the control server, which would kill an
    // active Phone Remote session and persist the control API as enabled.
    bool    m_appliedCtrlEnable = false;
    bool    m_appliedCtrlLan    = false;
    QString m_appliedCtrlToken;
};
